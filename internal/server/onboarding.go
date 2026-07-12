package server

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers/bambu"
)

// ---------------------------------------------------------------------------
// Handlers — Bambu Lab Cloud (email/password + 2FA)
// ---------------------------------------------------------------------------

// handleOnboardingStart shows the printer type selection page.
func (s *Server) handleOnboardingStart(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, onboardingStartTemplate, nil)
}

// handleOnboardingBambuLoginPage shows the email/password login form.
func (s *Server) handleOnboardingBambuLoginPage(w http.ResponseWriter, r *http.Request) {
	// Clear any previous onboarding state
	s.onboardingMu.Lock()
	s.onboardingEmail = ""
	s.onboardingToken = ""
	s.onboardingUserID = ""
	s.onboardingDevices = nil
	s.onboardingCloud = nil
	s.onboardingMu.Unlock()

	renderTemplate(w, bambuLoginTemplate, map[string]any{
		"LoginURL": "/onboarding/bambu/login",
	})
}

// handleOnboardingBambuLogin processes the email/password login form.
// If 2FA is required, sends the verification code and redirects to the code page.
// If no 2FA, completes login and redirects to device selection.
func (s *Server) handleOnboardingBambuLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")
	if email == "" || password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Email and password are required"})
		return
	}

	// Create a fresh cloud client for this login attempt
	region := "global"
	if s.cfg.BambuAccount != nil && s.cfg.BambuAccount.Region != "" {
		region = s.cfg.BambuAccount.Region
	}
	cloud := bambu.NewBambuCloudClient(region)

	// Try step 1: initial login
	lr, err := cloud.LoginStep1(email, password)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": fmt.Sprintf("Login failed: %v", err)})
		return
	}

	if lr.LoginType == "verifyCode" {
		// 2FA required — send code and show code entry page
		if err := cloud.SendVerificationCode(email); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": fmt.Sprintf("Failed to send verification code: %v", err)})
			return
		}

		s.onboardingMu.Lock()
		s.onboardingEmail = email
		s.onboardingCloud = cloud
		s.onboardingMu.Unlock()

		writeJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"needCode": true,
			"redirect": "/onboarding/bambu/code",
		})
		return
	}

	// No 2FA needed — login is complete
	s.onboardingMu.Lock()
	s.onboardingToken = cloud.Token()
	s.onboardingUserID = cloud.UserID()
	s.onboardingDevices, _ = cloud.GetDevices()
	s.onboardingCloud = nil
	s.onboardingEmail = ""
	s.onboardingMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"redirect": "/onboarding/bambu/select",
	})
}

// handleOnboardingBambuCodePage shows the 2FA verification code form.
func (s *Server) handleOnboardingBambuCodePage(w http.ResponseWriter, r *http.Request) {
	s.onboardingMu.Lock()
	email := s.onboardingEmail
	s.onboardingMu.Unlock()

	if email == "" {
		http.Redirect(w, r, "/onboarding/bambu", http.StatusFound)
		return
	}

	renderTemplate(w, bambuCodeTemplate, map[string]any{
		"Email":    email,
		"CodeURL":  "/onboarding/bambu/code",
		"LoginURL": "/onboarding/bambu",
	})
}

// handleOnboardingBambuCodeSubmit processes the 2FA verification code.
func (s *Server) handleOnboardingBambuCodeSubmit(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Verification code is required"})
		return
	}

	s.onboardingMu.Lock()
	email := s.onboardingEmail
	cloud := s.onboardingCloud
	s.onboardingMu.Unlock()

	if email == "" || cloud == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "No login in progress — start over"})
		return
	}

	// Complete login with verification code
	if err := cloud.LoginStep2(email, code); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": fmt.Sprintf("Verification failed: %v", err)})
		return
	}

	// Fetch devices
	devices, err := cloud.GetDevices()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": fmt.Sprintf("Failed to fetch devices: %v", err)})
		return
	}

	s.onboardingMu.Lock()
	s.onboardingToken = cloud.Token()
	s.onboardingUserID = cloud.UserID()
	s.onboardingDevices = devices
	s.onboardingCloud = nil // done with the partial client
	s.onboardingMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"redirect": "/onboarding/bambu/select",
	})
}

// handleOnboardingBambuSelect shows the device selection page.
func (s *Server) handleOnboardingBambuSelect(w http.ResponseWriter, r *http.Request) {
	s.onboardingMu.Lock()
	token := s.onboardingToken
	devices := s.onboardingDevices
	userID := s.onboardingUserID
	s.onboardingMu.Unlock()

	if token == "" || devices == nil {
		http.Redirect(w, r, "/onboarding/bambu", http.StatusFound)
		return
	}

	renderTemplate(w, onboardingSelectTemplate, map[string]any{
		"UserID":     userID,
		"Devices":    devices,
		"HasDevices": len(devices) > 0,
	})
}

// handleOnboardingBambuSave saves the config and reloads printers.
func (s *Server) handleOnboardingBambuSave(w http.ResponseWriter, r *http.Request) {
	s.onboardingMu.Lock()
	token := s.onboardingToken
	userID := s.onboardingUserID
	devices := s.onboardingDevices
	s.onboardingMu.Unlock()

	if token == "" || userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "No token — restart onboarding"})
		return
	}

	// Parse which devices the user selected
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	selectedIDs := r.Form["device_ids"]
	if len(selectedIDs) == 0 {
		// If none explicitly selected, add all
		for _, d := range devices {
			selectedIDs = append(selectedIDs, d.DevID)
		}
	}

	// Map selected device IDs to DeviceInfo
	selectedMap := make(map[string]bambu.DeviceInfo)
	for _, d := range devices {
		selectedMap[d.DevID] = d
	}

	// Build printer entries (append to existing, don't replace)
	existingPrinters := s.cfg.Printers
	if existingPrinters == nil {
		existingPrinters = make([]config.PrinterDef, 0)
	}
	newPrinters := make([]config.PrinterDef, 0)
	for _, id := range selectedIDs {
		dev, ok := selectedMap[id]
		if !ok {
			continue
		}
		// Check if this printer is already configured
		alreadyExists := false
		for _, p := range existingPrinters {
			if p.Serial == dev.DevID {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			continue
		}

		// Use the full serial as the printer ID to guarantee uniqueness.
		// Serial numbers are unique identifiers for Bambu devices.
		id := strings.ToLower(dev.DevID)

		newPrinters = append(newPrinters, config.PrinterDef{
			ID:     id,
			Name:   dev.Name,
			Type:   "bambu",
			Serial: dev.DevID,
			Model:  dev.DevModelName, // persist model (e.g. "H2S", "P1S") for camera routing
			// Host and AccessCode are optional — user can add later for camera
		})
	}

	// Merge new printers with existing
	allPrinters := append(existingPrinters, newPrinters...)

	// Update config
	region := "global"
	if s.cfg.BambuAccount != nil && s.cfg.BambuAccount.Region != "" {
		region = s.cfg.BambuAccount.Region
	}
	s.cfg.BambuAccount = &config.BambuAccount{
		Token:  token,
		UserID: userID,
		Region: region,
	}
	s.cfg.Printers = allPrinters

	// Save token to disk for server restarts
	cloud := bambu.NewBambuCloudClient(region)
	cloud.SetTokenFromLogin(token, userID, region)
	tokenPath := bambu.DefaultTokenPath("") // generic path for token-based accounts
	if s.cfg.BambuAccount != nil && s.cfg.BambuAccount.Email != "" {
		tokenPath = bambu.DefaultTokenPath(s.cfg.BambuAccount.Email)
	}
	cloud.SetTokenFile(tokenPath)
	if err := cloud.SaveToken(); err != nil {
		log.Printf("WARNING: failed to save token to disk: %v", err)
	}

	// Save to config file
	if err := s.cfg.Save(); err != nil {
		log.Printf("ERROR saving config: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to save config: %v", err),
		})
		return
	}

	log.Printf("Config saved with %d new printer(s).", len(newPrinters))

	// Clear onboarding state
	s.onboardingMu.Lock()
	s.onboardingEmail = ""
	s.onboardingToken = ""
	s.onboardingUserID = ""
	s.onboardingDevices = nil
	s.onboardingCloud = nil
	s.onboardingMu.Unlock()

	// Reload config and reconnect printers
	if err := s.reloadConfig(); err != nil {
		log.Printf("ERROR reloading config: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"success":        true,
			"warning":        fmt.Sprintf("Config saved but reload failed: %v. Please restart the server.", err),
			"redirect":       "/",
			"printers_added": len(newPrinters),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"redirect":       "/",
		"printers_added": len(newPrinters),
	})
}

// ---------------------------------------------------------------------------
// Handlers — Snapmaker (local Paxx firmware)
// ---------------------------------------------------------------------------

// handleOnboardingSnapmakerPage shows the manual entry form for Snapmaker U1.
func (s *Server) handleOnboardingSnapmakerPage(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, snapmakerFormTemplate, nil)
}

// handleOnboardingSnapmakerSave saves a Snapmaker printer config.
func (s *Server) handleOnboardingSnapmakerSave(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	host := strings.TrimSpace(r.FormValue("host"))
	portStr := strings.TrimSpace(r.FormValue("port"))
	accessCode := strings.TrimSpace(r.FormValue("access_code"))

	if name == "" || host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Name and host are required"})
		return
	}

	port := 80 // default when not specified
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   fmt.Sprintf("Invalid port %q: must be a number between 1 and 65535", portStr),
			})
			return
		}
		port = p
	}

	// Generate a short ID from the name
	shortID := strings.ToLower(name)
	shortID = strings.ReplaceAll(shortID, " ", "-")
	if len(shortID) > 16 {
		shortID = shortID[:16]
	}

	existingPrinters := s.cfg.Printers
	if existingPrinters == nil {
		existingPrinters = make([]config.PrinterDef, 0)
	}

	newPrinter := config.PrinterDef{
		ID:         shortID,
		Name:       name,
		Type:       "snapmaker",
		Host:       host,
		Port:       port,
		AccessCode: accessCode,
	}

	s.cfg.Printers = append(existingPrinters, newPrinter)

	if err := s.cfg.Save(); err != nil {
		log.Printf("ERROR saving config: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to save config: %v", err),
		})
		return
	}

	// Reload config and reconnect printers
	if err := s.reloadConfig(); err != nil {
		log.Printf("ERROR reloading config: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"warning":  fmt.Sprintf("Config saved but reload failed: %v. Please restart the server.", err),
			"redirect": "/",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"redirect": "/",
	})
}

// ---------------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------------

// indexOnboardingTemplate is shown when no printers are configured.
const indexOnboardingTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Printer Dashboard</title>
<style>
  :root {
    --bg-page: #f5f5f7;
    --bg-card: #ffffff;
    --text: #1d1d1f;
    --text-muted: #6e6e73;
    --text-subtle: #8a8a8e;
    --accent: #3b82f6;
    --accent-hover: #2f6fd6;
    --border-subtle: #e5e5ea;
    --shadow-card: 0 1px 3px rgba(0,0,0,.06), 0 1px 2px rgba(0,0,0,.04);
    --radius-control: 8px;
    --radius-card: 12px;
    --radius-pill: 999px;
    --tag-success-bg: #dcfce7;
    --tag-success-text: #15803d;
    --tag-warning-bg: #fef3c7;
    --tag-warning-text: #92400e;
    --tag-error-bg: #fee2e2;
    --tag-error-text: #b91c1c;
    --tag-info-bg: #dbeafe;
    --tag-info-text: #1e40af;
    --tag-neutral-bg: #f1f3f5;
    --tag-neutral-text: #4b5563;
    --tag-neutral-text-secondary: #6b7280;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg-page); color: var(--text); padding: 24px;
    display: flex; justify-content: center; align-items: center; min-height: 100vh;
  }
  .onboarding {
    text-align: center; max-width: 520px;
  }
  h1 { font-size: 1.5rem; font-weight: 700; margin-bottom: 8px; color: var(--text); display: inline-flex; align-items: center; gap: 10px; }
  h1 svg { width: 28px; height: 28px; flex-shrink: 0; color: var(--accent); }
  p { color: var(--text-muted); margin-bottom: 24px; font-size: 1.125rem; }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: var(--radius-control);
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none;
    background: var(--accent); color: #fff;
  }
  .btn:hover { background: var(--accent-hover); }
  .step-list { text-align: left; margin: 24px 0; color: var(--text-muted); font-size: 0.875rem; }
  .step-list li { margin: 8px 0; }
</style>
</head>
<body>
<div class="onboarding">
  <h1><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9V2h12v7"/><path d="M6 18H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-2"/><rect x="6" y="14" width="12" height="8" rx="1"/></svg>Printer Dashboard</h1>
  <p>No printers configured yet. Let's set one up.</p>
  <a href="/onboarding" class="btn">+ Add Your First Printer</a>
</div>
</body>
</html>`

// indexDashboardTemplate is the main dashboard shown when printers are configured.
// Mobile-first responsive layout: single column on small screens, grid on desktop.
// Desktop cards show full detail (layer count, file name, all buttons).
// Mobile cards collapse to essentials (state, progress, temps, pause/cancel).
const indexDashboardTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Printer Dashboard</title>
  <style>
    :root {
      --bg-page: #f5f5f7;
      --bg-card: #ffffff;
      --text: #1d1d1f;
      --text-muted: #6e6e73;
      --text-subtle: #8a8a8e;
      --accent: #3b82f6;
      --accent-hover: #2f6fd6;
      --border-subtle: #e5e5ea;
      --shadow-card: 0 1px 3px rgba(0,0,0,.06), 0 1px 2px rgba(0,0,0,.04);
      --radius-control: 8px;
      --radius-card: 12px;
      --radius-pill: 999px;
      --danger: #dc2626;
      --danger-hover: #b91c1c;
      /* Heat-source icon tints (by type) — warm bed, sky nozzle, cool chamber */
      --temp-bed: #f97316;
      --temp-nozzle: #3b82f6;
      --temp-chamber: #14b8a6;
      --tag-success-bg: #dcfce7;
      --tag-success-text: #15803d;
      --tag-warning-bg: #fef3c7;
      --tag-warning-text: #92400e;
      --tag-error-bg: #fee2e2;
      --tag-error-text: #b91c1c;
      --tag-info-bg: #dbeafe;
      --tag-info-text: #1e40af;
      --tag-neutral-bg: #f1f3f5;
      --tag-neutral-text: #4b5563;
      --tag-neutral-text-secondary: #6b7280;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: var(--bg-page); color: var(--text); padding: 16px;
    }
    h1 { font-size: 1.375rem; font-weight: 700; margin-bottom: 16px; color: var(--text); display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
    h1 .count { color: var(--text-subtle); font-size: 0.8125rem; font-weight: 500; }
    .header-actions { margin-left: auto; display: flex; gap: 8px; }

    /* Printer grid — mobile first: single column */
    .printers { display: grid; grid-template-columns: 1fr; gap: 12px; }

    /* Card — compact on mobile, expands on desktop */
    .card {
      background: var(--bg-card); background-clip: padding-box;
      border-radius: var(--radius-card); padding: 12px;
      box-shadow: 0 0 0 1px rgba(0,0,0,.06), var(--shadow-card);
      display: flex; flex-direction: column; gap: 8px;
    }
    .card-header {
      display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
    }
    .card-header h2 { font-size: 1rem; font-weight: 700; letter-spacing: -0.01em; }
    .tag {
      display: inline-block; padding: 2px 8px; border-radius: 6px;
      font-size: 0.6875rem; font-weight: 700; text-transform: uppercase;
      letter-spacing: 0.04em;
    }
    .tag.printing { background: var(--tag-success-bg); color: var(--tag-success-text); }
    .tag.paused { background: var(--tag-warning-bg); color: var(--tag-warning-text); }
    .tag.idle { background: var(--tag-neutral-bg); color: var(--tag-neutral-text); }
    .tag.error { background: var(--tag-error-bg); color: var(--tag-error-text); }
    .tag.complete { background: var(--tag-success-bg); color: var(--tag-success-text); }
    .tag.unknown { background: var(--tag-neutral-bg); color: var(--tag-neutral-text); }
    .tag.offline { background: var(--tag-neutral-bg); color: var(--tag-neutral-text-secondary); }

    .card-online { font-size: 0.6875rem; color: var(--text-subtle); margin-left: auto; display: inline-flex; align-items: center; gap: 4px; }
    .card-online.yes { color: var(--tag-success-text); }
    .card-online svg { width: 13px; height: 13px; flex-shrink: 0; }

    .error-banner {
      background: var(--tag-error-bg); color: var(--tag-error-text);
      padding: 8px 12px; border-radius: var(--radius-control);
      font-size: 0.8125rem; line-height: 1.4;
      word-break: break-word;
    }

    /* Progress bar — always visible */
    .progress-section { margin: 4px 0; }
    .progress-bar { background: var(--border-subtle); height: 6px; border-radius: var(--radius-pill); overflow: hidden; }
    .progress-bar .fill { background: var(--accent); height: 100%; border-radius: var(--radius-pill); }
    .progress-text { font-size: 0.8125rem; color: var(--text-muted); display: flex; justify-content: space-between; margin-top: 4px; }

    /* Temperature row — compact on mobile, expanded on desktop */
    .temps {
      display: flex; flex-direction: column; gap: 4px;
      font-size: 0.75rem; color: var(--text-muted);
      padding: 4px 0;
    }
    .temps .label { color: var(--text-subtle); font-weight: 500; display: flex; align-items: center; gap: 4px; }
    .temps .val { color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; }
    .temps .target { color: var(--text-muted); }
    /* Editable target-temp input — soft pill, faint border, accent focus ring. */
    input.target {
      width: 5.5em; padding: 3px 8px;
      font-size: inherit; font-family: inherit; font-weight: 600;
      color: var(--accent); text-align: center;
      background: #f6f8fc; border: 1px solid var(--border-subtle);
      border-radius: var(--radius-pill); outline: none;
      font-variant-numeric: tabular-nums; -moz-appearance: textfield;
    }
    input.target:hover { border-color: #c7d2e8; }
    input.target:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(59,130,246,.18); background: #fff; }
    input.target::-webkit-outer-spin-button, input.target::-webkit-inner-spin-button { -webkit-appearance: none; margin: 0; }
    .temp-row { display: flex; justify-content: space-between; align-items: center; width: 100%; gap: 8px; padding: 2px 0; }
    .temps .label { gap: 7px; }
    .temp-icon { width: 24px; height: 24px; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; line-height: 1; color: var(--text-muted); }
    .temp-icon svg { width: 24px; height: 24px; flex-shrink: 0; display: block; }
    .temp-icon.bed { color: var(--temp-bed); }
    .temp-icon.nozzle { color: var(--temp-nozzle); }
    .temp-icon.chamber { color: var(--temp-chamber); }
    .temp-values { display: flex; gap: 8px; align-items: center; }

    /* File name — hidden on mobile, shown on desktop (see media query below).
       Always rendered in the markup (with a "—" placeholder when no file is
       printing) so its row height is reserved from first paint; a later WS
       update swapping in a real filename never changes card height. */
    .filename { display: none; font-size: 0.75rem; color: var(--text-subtle); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

    /* Controls — always visible but less buttons on mobile */
    .controls { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 4px; }
    .controls button {
      flex: 1; min-width: 0;
      background: var(--accent); color: #fff; border: 1px solid var(--accent);
      padding: 9px 14px; border-radius: var(--radius-control); cursor: pointer;
      font-size: 0.8125rem; font-weight: 700;
      display: inline-flex; align-items: center; justify-content: center; gap: 6px;
      line-height: 1;
    }
    .controls button svg { width: 18px; height: 18px; flex-shrink: 0; }
    .controls button:hover:not(:disabled) { background: var(--accent-hover); border-color: var(--accent-hover); }
    .controls button:disabled { opacity: 0.4; cursor: not-allowed; }
    .controls button.danger { background: var(--danger); border-color: var(--danger); color: #fff; }
    .controls button.danger:hover:not(:disabled) { background: var(--danger-hover); border-color: var(--danger-hover); }
    /* Hide skip + resume on mobile */
    .btn-skip, .btn-resume { display: none; }

    /* Layer info — desktop only (see media query below). Always rendered
       (with a "—" placeholder when no layer data yet) for the same
       reserved-height reason as .filename above. */
    .layer-info { display: none; font-size: 0.75rem; color: var(--text-subtle); }

    .add-printer {
      display: inline-block; margin-top: 12px; padding: 8px 16px;
      background: var(--accent); color: #fff; border-radius: var(--radius-control);
      text-decoration: none; font-size: 0.8125rem; font-weight: 600;
    }
    .add-printer:hover { background: var(--accent-hover); }

    /* ─── Desktop (>=768px) ─── */
    @media (min-width: 768px) {
      body { padding: 24px; }
      h1 { font-size: 1.5rem; }
      .printers { grid-template-columns: repeat(auto-fill, minmax(500px, 1fr)); gap: 16px; }
      .card { padding: 16px; gap: 8px; }
      .card-header h2 { font-size: 1.125rem; }
      .temps { font-size: 0.8125rem; gap: 8px 20px; }
      .filename { display: block; }
      .layer-info { display: block; }
      .btn-skip, .btn-resume { display: inline-block; }
      .progress-bar { height: 8px; }
    }

    /* Camera section */
    .camera-section {
      display: flex; gap: 8px; margin: 8px 0;
      overflow: hidden; align-items: stretch;
    }
    .camera-slot {
      flex: 1; position: relative; min-width: 0; min-height: 300px;
      background: #0a0a0a; border-radius: var(--radius-card); overflow: hidden;
      border: 1px solid var(--border-subtle);
      display: flex; flex-direction: column;
      visibility: hidden;
    }
    .camera-slot img {
      width: 100%; aspect-ratio: 3/2; object-fit: contain;
      display: block; background: #000; flex-shrink: 0;
    }
    .camera-slot img.touchscreen-img {
      width: 100%; object-fit: contain;
      display: block; background: #000;
    }
    .camera-nav {
      display: flex; align-items: center; justify-content: space-between;
      padding: 4px 8px; background: #f1f3f5; flex-shrink: 0;
    }
    .camera-nav button {
      background: var(--bg-card); border: 1px solid var(--border-subtle); color: var(--text-muted);
      border-radius: var(--radius-control); cursor: pointer; padding: 1px 10px;
      font-size: 1rem; line-height: 1.4;
      display: inline-flex; align-items: center; justify-content: center;
    }
    .camera-nav button svg { width: 20px; height: 20px; flex-shrink: 0; display: block; }
    .camera-nav button:hover { background: #e9ebee; border-color: #d0d0d6; }
    .camera-nav .cam-label { font-size: 0.6875rem; color: var(--text-subtle); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    .camera-placeholder {
      display: flex; align-items: center; justify-content: center;
      width: 100%; min-height: 80px;
      background: #f1f3f5; border-radius: var(--radius-card);
      border: 1px solid var(--border-subtle);
      color: var(--text-muted); font-size: 0.75rem; font-style: italic;
      padding: 16px;
    }
    .cam-error {
      display: none; align-items: center; justify-content: center;
      width: 100%; aspect-ratio: 3/2;
      background: #f1f3f5; border-radius: var(--radius-card);
      color: var(--tag-error-text); font-size: 0.8125rem;
    }
    /* ─── Wide desktop (>=1200px) ─── */
    @media (min-width: 1200px) {
      .printers { grid-template-columns: repeat(auto-fill, minmax(600px, 1fr)); }
      .card { padding: 20px; }
      .temps { font-size: 0.875rem; gap: 8px 28px; }
    }
    .empty-message { color: var(--text-muted); padding: 20px; }
    .empty-message a { color: var(--accent); }
    .error-message { color: var(--tag-error-text); padding: 20px; }
  </style>
</head>
<body>
  <h1>
    Printer Dashboard
    <span class="count" id="printer-count"></span>
    <span class="header-actions">
      <a href="/onboarding" class="add-printer">+ Add Printer</a>
    </span>
  </h1>
  <div class="printers" id="printer-list">
    {{range .SkeletonCards}}
    <div class="card">
      <div class="card-header">
        <h2>&nbsp;</h2>
        <span class="tag unknown">&nbsp;</span>
        <span class="card-online"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="6"/></svg>Offline</span>
      </div>
      <div class="progress-section">
        <div class="progress-bar"><div class="fill" style="width:0%"></div></div>
        <div class="progress-text"><span>&nbsp;</span><span>&nbsp;</span></div>
      </div>
      <div class="camera-section">
        <div class="camera-slot">
          <div class="cam-error" style="display:none;"><span>Stream unavailable</span></div>
          <div class="camera-nav">
            <button class="cam-prev" disabled><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6"/></svg></button>
            <span class="cam-label">&nbsp;</span>
            <button class="cam-next" disabled><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg></button>
          </div>
        </div>
      </div>
      <div class="temps">
        <span class="temp-row">
          <span class="label"><span class="temp-icon bed"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="4" y1="9" x2="20" y2="9"/><path d="M8 13c-1 1-1 2 0 3s1 2 0 3"/><path d="M12 13c-1 1-1 2 0 3s1 2 0 3"/><path d="M16 13c-1 1-1 2 0 3s1 2 0 3"/></svg></span>Bed:</span>
          <span class="temp-values"><span class="val">--°C</span><input class="target" type="text" inputmode="decimal" value="--" disabled></span>
        </span>
        <span class="temp-row">
          <span class="label"><span class="temp-icon nozzle"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 3h10l-1.5 9H8.5z"/><path d="M10.5 12l1.5 6 1.5-6"/><circle cx="18.5" cy="5.5" r="4.5" fill="var(--bg-card)"/><text x="18.5" y="8" text-anchor="middle" font-size="7" font-weight="700" stroke="none" fill="currentColor" font-family="-apple-system,sans-serif">1</text></svg></span>Nozzle 1:</span>
          <span class="temp-values"><span class="val">--°C</span><input class="target" type="text" inputmode="decimal" value="--" disabled></span>
        </span>
        <span class="temp-row">
          <span class="label"><span class="temp-icon chamber"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="6" width="16" height="14" rx="1"/><path d="M4 10h16"/></svg></span>Chamber:</span>
          <span class="temp-values"><span class="val">--°C</span></span>
        </span>
      </div>
      <div class="filename">&nbsp;</div>
      <div class="layer-info">&nbsp;</div>
      <div class="error-banner" style="display:none;"></div>
      <div class="controls">
        <button disabled><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="9" y1="5" x2="9" y2="19"/><line x1="15" y1="5" x2="15" y2="19"/></svg>Pause</button>
        <button class="btn-resume" disabled><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 5l11 7-11 7z"/></svg>Resume</button>
        <button class="danger" disabled><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="6" y="6" width="12" height="12" rx="1"/></svg>Cancel</button>
        <button class="btn-skip" disabled><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 5l9 7-9 7z"/><line x1="18" y1="5" x2="18" y2="19"/></svg>Skip Object</button>
      </div>
    </div>
    {{end}}
  </div>
  <script>
    window._printerCache = {};
    window._wsReconnectDelay = 1000;

    function mergeWithCache(p) {
      const cached = window._printerCache[p.id] || {};
      const merged = {};
      // Copy all cached values first
      Object.keys(cached).forEach(k => merged[k] = cached[k]);
      // Override with new values (only if they're not null/undefined)
      Object.keys(p).forEach(k => {
        if (p[k] !== null && p[k] !== undefined) {
          merged[k] = p[k];
        }
      });
      window._printerCache[p.id] = merged;
      return merged;
    }

    function updateCard(p, rebuildCameras) {
      const card = document.getElementById('printer-' + p.id);
      if (!card) { loadPrinters(); return; }

      // Update printer count in header
      const list = Object.keys(window._printerCache);
      document.getElementById('printer-count').textContent =
        list.length + ' printer' + (list.length !== 1 ? 's' : '');

      if (rebuildCameras) {
        // Full rebuild — replace entire card, camera section included
        card.outerHTML = renderCard(p);
        const newCard = document.getElementById('printer-' + p.id);
        if (newCard) {
          // Camera slot is rendered inside the card — no further setup needed
        }
        return;
      }

      // ── Normal update: only update non-camera parts of the card ──
      const st = p.state || 'unknown';
      const stCls = p.online ? st : 'offline';
      const progress = (p.progress * 100).toFixed(1);
      const timeStr = p.remaining_time > 0 ? formatTime(p.remaining_time) : '';

      // 1. State tag
      const tag = card.querySelector('.tag');
      if (tag) { tag.className = 'tag ' + stCls; tag.textContent = st; }

      // 2. Online indicator
      const onlineEl = card.querySelector('.card-online');
      if (onlineEl) {
        if (p.online) { onlineEl.className = 'card-online yes'; onlineEl.innerHTML = svgStatusDot(true); }
        else { onlineEl.className = 'card-online'; onlineEl.innerHTML = svgStatusDot(false) + 'Offline'; }
      }

      // 3. Progress bar fill
      const fill = card.querySelector('.progress-bar .fill');
      if (fill) fill.style.width = progress + '%';

      // 4. Progress text (percent + time)
      const pt = card.querySelector('.progress-text');
      if (pt) pt.innerHTML = '<span>' + progress + '%</span><span>' + timeStr + '</span>';

      // 5. Temperatures — update each temp-row in place
      const temps = card.querySelector('.temps');
      if (temps) {
        const bed = p.bed_temp !== null ? p.bed_temp.toFixed(1) : '?';
        const bedT = p.bed_target_temp !== null ? p.bed_target_temp.toFixed(1) : '?';
        const nozzle = p.nozzle_temp !== null ? p.nozzle_temp.toFixed(1) : '?';
        const nozzleT = p.nozzle_target_temp !== null ? p.nozzle_target_temp.toFixed(1) : '?';
        const chamberVal = p.chamber_temp !== null ? p.chamber_temp.toFixed(1) : '?';

        // Set an input.target's value only when the user isn't editing it, so
        // a live WS update never clobbers what they're typing.
        function setTargetInput(row, targetVal) {
          const inp = row.querySelector('.target');
          if (inp && document.activeElement !== inp) inp.value = targetVal;
        }

        const rows = temps.querySelectorAll('.temp-row');
        // Row 0: bed
        if (rows[0]) {
          const val = rows[0].querySelector('.val');
          if (val) val.textContent = bed + '\u00b0C';
          setTargetInput(rows[0], bedT);
        }
        // Row 1: nozzle 1
        if (rows[1]) {
          const val = rows[1].querySelector('.val');
          if (val) val.textContent = nozzle + '\u00b0C';
          setTargetInput(rows[1], nozzleT);
        }
        // Rows 2+: extra nozzles (skip index 0)
        let extraIdx = 2;
        (p.nozzle_temps || []).forEach(function(nt) {
          if (nt.index === 0) return;
          if (rows[extraIdx]) {
            const actualStr = nt.actual !== null ? nt.actual.toFixed(1) : '?';
            const targetStr = nt.target !== null ? nt.target.toFixed(1) : '?';
            const val = rows[extraIdx].querySelector('.val');
            if (val) val.textContent = actualStr + '\u00b0C';
            setTargetInput(rows[extraIdx], targetStr);
          }
          extraIdx++;
        });
        // Last row: chamber
        const lastRow = rows[rows.length - 1];
        if (lastRow) {
          const val = lastRow.querySelector('.val');
          if (val) val.textContent = chamberVal + '\u00b0C';
        }
      }

      // 6. File name — row is always present (see renderCard); only swap the
      // text so a job starting/finishing never changes the card's height.
      const fileEl = card.querySelector('.filename');
      if (fileEl) fileEl.textContent = p.current_file ? escapeHtml(p.current_file) : '—';

      // 7. Layer info — same always-present pattern as file name.
      const layerEl = card.querySelector('.layer-info');
      if (layerEl) layerEl.textContent = (p.total_layers > 0) ? ('Layer ' + p.current_layer + ' / ' + p.total_layers) : '—';

      // 8. Error banner — always present in the DOM (see renderCard); entering
      // or leaving error state is real new information, so unlike filename/
      // layer-info it's allowed to change card height here.
      const errorEl = card.querySelector('.error-banner');
      if (errorEl && st === 'error' && p.error_msg) {
        errorEl.textContent = escapeHtml(p.error_msg);
        errorEl.style.display = '';
      } else if (errorEl) {
        errorEl.style.display = 'none';
      }

      // 9. Control buttons
      const pauseBtn = card.querySelector('button[onclick*="pause"]');
      const resumeBtn = card.querySelector('button[onclick*="resume"]');
      const cancelBtn = card.querySelector('button[onclick*="cancel"]');
      const skipBtn = card.querySelector('button[onclick*="skip"]');
      if (pauseBtn) pauseBtn.disabled = st !== 'printing';
      if (resumeBtn) resumeBtn.disabled = st !== 'paused';
      if (cancelBtn) cancelBtn.disabled = st !== 'printing' && st !== 'paused';
      if (skipBtn) skipBtn.disabled = st !== 'printing';
    }

    function loadPrinters() {
      fetch('/api/printers')
        .then(r => {
          if (!r.ok) throw new Error('Failed to load printers');
          return r.json();
        })
        .then(data => {
          const container = document.getElementById('printer-list');
          const count = document.getElementById('printer-count');
          const list = data.printers || [];
          count.textContent = list.length + ' printer' + (list.length !== 1 ? 's' : '');
          if (list.length === 0) {
            container.innerHTML = '<p class="empty-message">No printers configured. <a href="/onboarding">Add one</a>.</p>';
            return;
          }
          // Populate cache with full response
          list.forEach(function(p) {
            window._printerCache[p.id] = p;
          });
          container.innerHTML = list.map(renderCard).join('');
          // Start periodic refresh for camera frames. The browser keeps the
          // old frame visible while the new one loads, so no flicker.
          // Skip errored images (display:none) to avoid aborting pending
          // requests that may still succeed on timeout/retry.
          if (!window._camInterval) {
            window._camInterval = setInterval(function() {
              document.querySelectorAll('.camera-slot img[data-frame-url]').forEach(function(img) {
                if (img.style.display !== 'none') {
                  img.src = img.getAttribute('data-frame-url') + '&_t=' + Date.now();
                }
              });
            }, 2000);
          }
        })
        .catch(() => {
          document.getElementById('printer-list').innerHTML = '<p class="error-message">Error loading printers.</p>';
        });
    }

    // ── Inline SVG icon set (Lucide/Heroicons stroke style) ──
    // Each returns a fixed viewBox="0 0 24 24" <svg> string that inherits its
    // color via currentColor and is sized by CSS (.temp-icon svg / button svg).
    // The skeleton markup (server-rendered first paint) inlines the SAME strings
    // literally so it matches renderCard byte-for-byte — keep them in sync.
    const _svgOpen = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">';
    // Heated bed: a flat platform bar with heat waves rising beneath it.
    function svgBed() {
      return _svgOpen +
        '<line x1="4" y1="9" x2="20" y2="9"/>' +
        '<path d="M8 13c-1 1-1 2 0 3s1 2 0 3"/>' +
        '<path d="M12 13c-1 1-1 2 0 3s1 2 0 3"/>' +
        '<path d="M16 13c-1 1-1 2 0 3s1 2 0 3"/>' +
        '</svg>';
    }
    // Nozzle: a downward-narrowing extruder tip with a numbered badge so
    // nozzle 1/2/3 are unambiguous even at 14px.
    function svgNozzle(idx) {
      return _svgOpen +
        '<path d="M7 3h10l-1.5 9H8.5z"/>' +
        '<path d="M10.5 12l1.5 6 1.5-6"/>' +
        '<circle cx="18.5" cy="5.5" r="4.5" fill="var(--bg-card)"/>' +
        '<text x="18.5" y="8" text-anchor="middle" font-size="7" font-weight="700" stroke="none" fill="currentColor" font-family="-apple-system,sans-serif">' + idx + '</text>' +
        '</svg>';
    }
    // Chamber: an enclosure/box outline (the print enclosure). Not a heat source.
    function svgChamber() {
      return _svgOpen +
        '<rect x="4" y="6" width="16" height="14" rx="1"/>' +
        '<path d="M4 10h16"/>' +
        '</svg>';
    }
    function svgPause() {
      return _svgOpen + '<line x1="9" y1="5" x2="9" y2="19"/><line x1="15" y1="5" x2="15" y2="19"/></svg>';
    }
    function svgResume() {
      return _svgOpen + '<path d="M7 5l11 7-11 7z"/></svg>';
    }
    function svgCancel() {
      return _svgOpen + '<rect x="6" y="6" width="12" height="12" rx="1"/></svg>';
    }
    function svgSkip() {
      return _svgOpen + '<path d="M5 5l9 7-9 7z"/><line x1="18" y1="5" x2="18" y2="19"/></svg>';
    }
    function svgChevron(dir) {
      return dir === 'left'
        ? _svgOpen + '<path d="M15 6l-6 6 6 6"/></svg>'
        : _svgOpen + '<path d="M9 6l6 6-6 6"/></svg>';
    }
    // Online status dot: filled circle when online, hollow ring when offline.
    function svgStatusDot(online) {
      return online
        ? _svgOpen.replace('fill="none"', 'fill="currentColor"') + '<circle cx="12" cy="12" r="6"/></svg>'
        : _svgOpen + '<circle cx="12" cy="12" r="6"/></svg>';
    }

    // Editable target-temp input. Keeps the .target class so updateCard's
    // selector still finds it. STUB: onchange calls setTargetTemp (no-op for now).
    function targetInput(printerId, sensor, value) {
      return '<input class="target" type="text" inputmode="decimal"' +
        ' value="' + escapeHtml(String(value)) + '"' +
        ' onchange="setTargetTemp(\'' + printerId + '\',\'' + sensor + '\',this.value)">';
    }

    // STUB: set a new target temperature. Does not POST/apply yet — placeholder
    // so the wiring exists for a later real implementation.
    function setTargetTemp(printerId, sensor, value) {
      console.log('setTargetTemp (stub):', printerId, sensor, value);
    }

    function renderCard(p) {
      const st = p.state || 'unknown';
      const stCls = p.online ? st : 'offline';
      const progress = (p.progress * 100).toFixed(1);
      const timeStr = p.remaining_time > 0 ? formatTime(p.remaining_time) : '';

      // Temperatures — null-safe with '---' fallback
      const bed = p.bed_temp !== null ? p.bed_temp.toFixed(1) : '?';
      const bedT = p.bed_target_temp !== null ? p.bed_target_temp.toFixed(1) : '?';
      const nozzle = p.nozzle_temp !== null ? p.nozzle_temp.toFixed(1) : '?';
      const nozzleT = p.nozzle_target_temp !== null ? p.nozzle_target_temp.toFixed(1) : '?';
      const chamberVal = p.chamber_temp !== null ? p.chamber_temp.toFixed(1) : '?';

      // Online indicator
      const onlineDot = p.online
        ? '<span class="card-online yes">' + svgStatusDot(true) + '</span>'
        : '<span class="card-online">' + svgStatusDot(false) + 'Offline</span>';

      // File name (desktop only). Always rendered — with a "—" placeholder
      // when no file is printing — so the row's height is reserved from
      // first paint and a later WS update can't shift the card's height.
      const fileHtml = '<div class="filename">' + (p.current_file ? escapeHtml(p.current_file) : '—') + '</div>';

      // Layer info (desktop only). Same always-rendered/placeholder pattern.
      const layerHtml = '<div class="layer-info">' + (p.total_layers > 0 ? ('Layer ' + p.current_layer + ' / ' + p.total_layers) : '—') + '</div>';

      // Error banner — shown when state is "error" and error_msg is non-empty.
      // Unlike .filename/.layer-info, this reflects genuinely new information
      // (an actual error), so it's fine for it to change card height when it
      // appears/disappears — that's a real state transition, not a loading
      // artifact. The element is still always present in the DOM (hidden via
      // display:none rather than omitted) so renderCard() and updateCard()
      // agree on shape, and a later WS error update can find and show it
      // without a full card rebuild.
      const errorHtml = '<div class="error-banner"' + ((st === 'error' && p.error_msg) ? '' : ' style="display:none;"') + '>' + escapeHtml(p.error_msg || '') + '</div>';

      return '<div class="card" id="printer-' + p.id + '">' +
        '<div class="card-header">' +
          '<h2>' + escapeHtml(p.name) + '</h2>' +
          '<span class="tag ' + stCls + '">' + st + '</span>' +
          onlineDot +
        '</div>' +
        '<div class="progress-section">' +
          '<div class="progress-bar"><div class="fill" style="width:' + progress + '%"></div></div>' +
          '<div class="progress-text"><span>' + progress + '%</span><span>' + timeStr + '</span></div>' +
        '</div>' +
        // Camera section — above temps, with placeholder when no streams
        (function() {
          const streams = p.camera_streams || [];
          if (streams.length === 0) {
            // Placeholder with reason based on printer type
            let reason = 'No camera feeds available.';
            if (p.type === 'bambu') {
              reason = 'Camera: add LAN IP and access code in printer settings.';
            } else if (p.type === 'snapmaker') {
              reason = 'Camera: ensure printer is reachable and has a webcam configured.';
            }
            return '<div class="camera-section" id="cam-section-' + p.id + '"><div class="camera-placeholder">' + reason + '</div></div>';
          }
          if (!window._cameraSlots) window._cameraSlots = {};
          if (window._cameraSlots[p.id] === undefined) window._cameraSlots[p.id] = 0;
          const idx = window._cameraSlots[p.id] % streams.length;
          const stream = streams[idx];
          const label = escapeHtml(stream.label);
          const rawUrl = stream.url;
          const interactiveUrl = escapeHtml(rawUrl.replace('/screen/snapshot', '/screen/'));
          const snapshotUrl = escapeHtml(rawUrl);
          const isTouchscreen = stream.type === 'touchscreen';
          let html = '<div class="camera-slot" id="cam-' + p.id + '-0" data-type="' + escapeHtml(stream.type) + '">';
          if (isTouchscreen) {
            // Touchscreen: use frame endpoint (buffered) to avoid progressive
            // rendering. Link opens the raw interactive screen in a new tab.
            var rawCameraUrl = rawUrl;
            var m = rawUrl.match(/[?&]url=([^&]+)/);
            if (m) rawCameraUrl = decodeURIComponent(m[1]);
            var frameUrl = '/api/camera/frame?url=' + encodeURIComponent(rawCameraUrl);
            html += '<a href="' + interactiveUrl + '" target="_blank" rel="noopener" title="Open touchscreen in new tab">';
            html += '<img src="' + frameUrl + '&_t=' + Date.now() + '" class="touchscreen-img" alt="' + label + '" loading="lazy" onload="this.closest(\'.camera-slot\').style.visibility=\'visible\'" onerror="this.closest(\'.camera-slot\').style.visibility=\'visible\';this.style.display=\'none\';">';
            html += '</a>';
          } else {
            // Single frame with periodic refresh: no MJPEG streaming.
            // /api/camera/frame returns a single complete JPEG (or placeholder)
            // which the browser fully decodes before firing onload, so there
            // is never a blank/flash rendering frame. The setInterval refreshes
            // the src every 2s with a cache-busting param; the browser keeps
            // the old frame visible until the new one is decoded.
            // rawUrl is the proxied URL; extract the raw camera URL from it.
            var rawCameraUrl = rawUrl;
            var m = rawUrl.match(/[?&]url=([^&]+)/);
            if (m) rawCameraUrl = decodeURIComponent(m[1]);
            var frameUrl = '/api/camera/frame?url=' + encodeURIComponent(rawCameraUrl);
            html += '<img id="cam-' + p.id + '" src="' + frameUrl + '&_t=' + Date.now() + '" alt="' + label + '" style="display:block;width:100%;object-fit:contain;background:#000;" onload="this.closest(\'.camera-slot\').style.visibility=\'visible\'" onerror="this.closest(\'.camera-slot\').style.visibility=\'visible\';this.style.display=\'none\';this.nextElementSibling.style.display=\'flex\';" data-frame-url="' + escapeHtml(frameUrl) + '">';
          }
          html += '<div class="cam-error" style="display:none;"><span>Stream unavailable</span></div>';
          html += '<div class="camera-nav">';
          html += '<button class="cam-prev" onclick="cameraFlip(\'' + p.id + '\',-1)">' + svgChevron('left') + '</button>';
          html += '<span class="cam-label">' + label + '</span>';
          html += '<button class="cam-next" onclick="cameraFlip(\'' + p.id + '\',1)">' + svgChevron('right') + '</button>';
          html += '</div></div>';
          return '<div class="camera-section" id="cam-section-' + p.id + '">' + html + '</div>';
        })() +
        '<div class="temps">' +
        // Bed row
          '<span class="temp-row">' +
            '<span class="label"><span class="temp-icon bed">' + svgBed() + '</span>Bed:</span>' +
            '<span class="temp-values"><span class="val">' + bed + '°C</span>' + targetInput(p.id, 'bed', bedT) + '</span>' +
          '</span>' +
        // Primary nozzle (tool0)
          '<span class="temp-row">' +
            '<span class="label"><span class="temp-icon nozzle">' + svgNozzle(1) + '</span>Nozzle 1:</span>' +
            '<span class="temp-values"><span class="val">' + nozzle + '°C</span>' + targetInput(p.id, 'nozzle', nozzleT) + '</span>' +
          '</span>' +
        // Extra nozzles (tool1+)
          (p.nozzle_temps || []).filter(function(nt) { return nt.index > 0; }).map(function(nt) {
            const actualStr = nt.actual !== null ? nt.actual.toFixed(1) : '?';
            const targetStr = nt.target !== null ? nt.target.toFixed(1) : '?';
            return '<span class="temp-row">' +
              '<span class="label"><span class="temp-icon nozzle">' + svgNozzle(nt.index + 1) + '</span>Nozzle ' + (nt.index + 1) + ':</span>' +
              '<span class="temp-values"><span class="val">' + actualStr + '°C</span>' + targetInput(p.id, 'nozzle' + nt.index, targetStr) + '</span>' +
            '</span>';
          }).join('') +
          // Chamber
          '<span class="temp-row">' +
            '<span class="label"><span class="temp-icon chamber">' + svgChamber() + '</span>Chamber:</span>' +
            '<span class="temp-values"><span class="val">' + chamberVal + '°C</span></span>' +
          '</span>' +
        '</div>' +
        fileHtml +
        layerHtml +
        errorHtml +
        '<div class="controls">' +
          '<button onclick="cmd(\'' + p.id + '\',\'pause\')" ' + (st !== 'printing' ? 'disabled' : '') + '>' + svgPause() + 'Pause</button>' +
          '<button onclick="cmd(\'' + p.id + '\',\'resume\')" class="btn-resume" ' + (st !== 'paused' ? 'disabled' : '') + '>' + svgResume() + 'Resume</button>' +
          '<button onclick="cmd(\'' + p.id + '\',\'cancel\')" class="danger" ' + (st !== 'printing' && st !== 'paused' ? 'disabled' : '') + '>' + svgCancel() + 'Cancel</button>' +
          '<button onclick="cmd(\'' + p.id + '\',\'skip\')" class="btn-skip" ' + (st !== 'printing' ? 'disabled' : '') + '>' + svgSkip() + 'Skip Object</button>' +
        '</div>' +
      '</div>';
    }

    function formatTime(sec) {
      const h = Math.floor(sec / 3600);
      const m = Math.floor((sec % 3600) / 60);
      return h + 'h ' + m + 'm';
    }

    function escapeHtml(s) {
      if (!s) return '';
      return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    }

    function cmd(id, action) {
      fetch('/api/printers/' + id + '/' + action, { method: 'POST' })
        .then(r => r.json())
        .then(d => { if (d.status !== 'ok') alert(d.error || 'Command failed'); })
        .catch(() => alert('Network error'));
    }

    window._cameraSlots = window._cameraSlots || {};

    function cameraFlip(printerId, dir) {
      const p = window._printerCache[printerId];
      if (!p) return;
      const streams = p.camera_streams || [];
      if (streams.length === 0) return;
      if (window._cameraSlots[printerId] === undefined) window._cameraSlots[printerId] = 0;
      window._cameraSlots[printerId] = (window._cameraSlots[printerId] + dir + streams.length) % streams.length;
      updateCard(p, true);
    }

    function connectWebSocket() {
      const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsUrl = protocol + '//' + location.host + '/ws';
      const ws = new WebSocket(wsUrl);

      ws.onmessage = function(event) {
        const msg = JSON.parse(event.data);
        if (msg.type === 'printer_update') {
          const merged = mergeWithCache(msg.printer);
          updateCard(merged);
        }
      };

      ws.onclose = function() {
        setTimeout(function() {
          // On reconnect, re-fetch full state to make sure we're in sync
          loadPrinters();
          connectWebSocket();
        }, window._wsReconnectDelay);
        window._wsReconnectDelay = Math.min(window._wsReconnectDelay * 2, 30000);
      };

      ws.onopen = function() {
        window._wsReconnectDelay = 1000; // Reset on successful connection
      };
    }

    loadPrinters();
    connectWebSocket();

    // Touchscreen image refresh — poll every 3 seconds for live feel
    setInterval(function() {
        document.querySelectorAll('.camera-slot img.touchscreen-img').forEach(function(img) {
            var src = img.src;
            src = src.replace(/([?&])_t=\d+/, '$1_t=' + Date.now());
            if (src.indexOf('_t=') === -1) {
                src += (src.indexOf('?') === -1 ? '?' : '&') + '_t=' + Date.now();
            }
            img.src = src;
        });
    }, 3000);
  </script>
</body>
</html>`

// ---------------------------------------------------------------------------
// Templates — Onboarding Start
// ---------------------------------------------------------------------------

// onboardingStartTemplate is the printer type selection page.
const onboardingStartTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Add Printer</title>
<style>
  :root {
    --bg-page: #f5f5f7;
    --bg-card: #ffffff;
    --text: #1d1d1f;
    --text-muted: #6e6e73;
    --text-subtle: #8a8a8e;
    --accent: #3b82f6;
    --accent-hover: #2f6fd6;
    --border-subtle: #e5e5ea;
    --shadow-card: 0 1px 3px rgba(0,0,0,.06), 0 1px 2px rgba(0,0,0,.04);
    --radius-control: 8px;
    --radius-card: 12px;
    --radius-pill: 999px;
    --tag-success-bg: #dcfce7;
    --tag-success-text: #15803d;
    --tag-warning-bg: #fef3c7;
    --tag-warning-text: #92400e;
    --tag-error-bg: #fee2e2;
    --tag-error-text: #b91c1c;
    --tag-info-bg: #dbeafe;
    --tag-info-text: #1e40af;
    --tag-neutral-bg: #f1f3f5;
    --tag-neutral-text: #4b5563;
    --tag-neutral-text-secondary: #6b7280;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg-page); color: var(--text); padding: 40px 24px;
  }
  .container { max-width: 600px; margin: 0 auto; }
  h1 { font-size: 1.5rem; font-weight: 700; margin-bottom: 4px; color: var(--text); }
  .subtitle { color: var(--text-muted); margin-bottom: 24px; }
  .option {
    background: var(--bg-card); border: 1px solid var(--border-subtle); border-radius: var(--radius-card);
    padding: 16px; margin-bottom: 12px; cursor: pointer;
    display: block; text-decoration: none; color: inherit;
    box-shadow: var(--shadow-card);
  }
  .option:hover { border-color: var(--accent); }
  .option h3 { font-size: 1.125rem; font-weight: 700; margin-bottom: 4px; }
  .option p { color: var(--text-muted); font-size: 0.875rem; }
  .option .tag {
    display: inline-block; background: var(--tag-success-bg); color: var(--tag-success-text);
    padding: 2px 8px; border-radius: 6px; font-size: 0.75rem; font-weight: 700;
    margin-left: 8px; vertical-align: middle;
  }
  .option .tag-coming {
    display: inline-block; background: var(--tag-warning-bg); color: var(--tag-warning-text);
    padding: 2px 8px; border-radius: 6px; font-size: 0.75rem; font-weight: 700;
    margin-left: 8px; vertical-align: middle;
  }
  .back { display: inline-block; margin-top: 16px; color: var(--accent); text-decoration: none; }
  .back:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="container">
  <h1>+ Add Printer</h1>
  <p class="subtitle">Select your printer type to get started.</p>

  <a href="/onboarding/bambu" class="option">
    <div>
      <h3>Bambu Lab (Cloud) <span class="tag">Recommended</span></h3>
      <p>Connect via Bambu Cloud. Works with P1S, A1, X1, H2S and all Bambu printers.
      No LAN mode or developer mode required. Uses your Bambu Lab account email and password.</p>
    </div>
  </a>

  <a href="/onboarding/snapmaker" class="option">
    <div>
      <h3>Snapmaker U1 (Local) <span class="tag-coming">Beta</span></h3>
      <p>Connect to a Snapmaker U1 running Paxx firmware on your local network.
      Enter the IP address, port, and access code manually.</p>
    </div>
  </a>

  <a href="/" class="back">← Back to dashboard</a>
</div>
</body>
</html>`

// ---------------------------------------------------------------------------
// Templates — Bambu Login (email/password)
// ---------------------------------------------------------------------------

// bambuLoginTemplate is the email/password login form for Bambu Cloud.
const bambuLoginTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Bambu Lab — Sign In</title>
<style>
  :root {
    --bg-page: #f5f5f7;
    --bg-card: #ffffff;
    --text: #1d1d1f;
    --text-muted: #6e6e73;
    --text-subtle: #8a8a8e;
    --accent: #3b82f6;
    --accent-hover: #2f6fd6;
    --border-subtle: #e5e5ea;
    --shadow-card: 0 1px 3px rgba(0,0,0,.06), 0 1px 2px rgba(0,0,0,.04);
    --radius-control: 8px;
    --radius-card: 12px;
    --radius-pill: 999px;
    --tag-success-bg: #dcfce7;
    --tag-success-text: #15803d;
    --tag-warning-bg: #fef3c7;
    --tag-warning-text: #92400e;
    --tag-error-bg: #fee2e2;
    --tag-error-text: #b91c1c;
    --tag-info-bg: #dbeafe;
    --tag-info-text: #1e40af;
    --tag-neutral-bg: #f1f3f5;
    --tag-neutral-text: #4b5563;
    --tag-neutral-text-secondary: #6b7280;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg-page); color: var(--text); padding: 40px 24px;
    display: flex; justify-content: center;
  }
  .container { max-width: 460px; width: 100%; }
  h1 { font-size: 1.5rem; font-weight: 700; margin-bottom: 4px; color: var(--text); display: inline-flex; align-items: center; gap: 10px; }
  h1 svg { width: 26px; height: 26px; flex-shrink: 0; color: var(--accent); }
  .subtitle { color: var(--text-muted); margin-bottom: 24px; }
  .card {
    background: var(--bg-card); border: 1px solid var(--border-subtle); border-radius: var(--radius-card);
    padding: 24px; margin-bottom: 16px;
    box-shadow: var(--shadow-card);
  }
  .card label { display: block; margin-bottom: 6px; color: var(--text); font-size: 0.875rem; font-weight: 500; }
  .card input[type="email"],
  .card input[type="password"] {
    width: 100%; padding: 12px; background: var(--bg-card); color: var(--text);
    border: 1px solid var(--border-subtle); border-radius: var(--radius-control);
    font-size: 1rem; margin-bottom: 16px;
  }
  .card input:focus { outline: none; border-color: var(--accent); }
  .card input[type="email"]:hover,
  .card input[type="password"]:hover {
    border-color: var(--accent-hover);
  }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: var(--radius-control);
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none;
    width: 100%;
  }
  .btn-primary { background: var(--accent); color: #fff; }
  .btn-primary:hover { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-secondary { background: var(--bg-card); color: var(--text); border: 1px solid var(--border-subtle); }
  .btn-secondary:hover { background: #f1f3f5; }
  .status {
    display: none; padding: 16px; border-radius: var(--radius-control); margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.error { display: block; background: var(--tag-error-bg); color: var(--tag-error-text); }
  .status.info { display: block; background: var(--tag-info-bg); color: var(--tag-info-text); }
  .back { display: inline-block; margin-top: 16px; color: var(--accent); text-decoration: none; }
  .back:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="container">
  <h1><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="7.5" cy="15.5" r="4.5"/><path d="M10.7 12.3 21 2"/><path d="m17 5 3 3"/><path d="m14 8 3 3"/></svg>Sign in to Bambu Lab</h1>
  <p class="subtitle">Enter your Bambu Lab account credentials. If 2FA is enabled, we'll ask for a verification code next.</p>

  <div class="card">
    <form id="loginForm">
      <label for="email">Email</label>
      <input type="email" id="email" name="email" placeholder="you@example.com" required autocomplete="email">

      <label for="password">Password</label>
      <input type="password" id="password" name="password" placeholder="Your Bambu Lab password" required autocomplete="current-password">

      <button type="submit" class="btn btn-primary" id="submitBtn">Sign In</button>
    </form>
    <div id="status" class="status"></div>
  </div>

  <a href="/onboarding" class="back">← Back to printer selection</a>
</div>

<script>
document.getElementById('loginForm').addEventListener('submit', async function(e) {
  e.preventDefault();
  const btn = document.getElementById('submitBtn');
  const status = document.getElementById('status');
  btn.disabled = true;
  btn.textContent = 'Signing in...';
  status.className = 'status info';
  status.textContent = 'Contacting Bambu Cloud...';

  const form = new FormData(this);
  try {
    const res = await fetch('{{.LoginURL}}', { method: 'POST', body: form });
    const d = await res.json();
    if (d.success && d.needCode) {
      window.location.href = d.redirect;
    } else if (d.success) {
      window.location.href = d.redirect;
    } else {
      status.className = 'status error';
      status.textContent = d.error || 'Login failed. Check your credentials.';
      btn.disabled = false;
      btn.textContent = 'Sign In';
    }
  } catch (err) {
    status.className = 'status error';
    status.textContent = 'Network error: ' + err.message;
    btn.disabled = false;
    btn.textContent = 'Sign In';
  }
});
</script>
</body>
</html>`

// ---------------------------------------------------------------------------
// Templates — Bambu 2FA Code
// ---------------------------------------------------------------------------

// bambuCodeTemplate is the 2FA verification code entry page.
const bambuCodeTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Bambu Lab — Verification Code</title>
<style>
  :root {
    --bg-page: #f5f5f7;
    --bg-card: #ffffff;
    --text: #1d1d1f;
    --text-muted: #6e6e73;
    --text-subtle: #8a8a8e;
    --accent: #3b82f6;
    --accent-hover: #2f6fd6;
    --border-subtle: #e5e5ea;
    --shadow-card: 0 1px 3px rgba(0,0,0,.06), 0 1px 2px rgba(0,0,0,.04);
    --radius-control: 8px;
    --radius-card: 12px;
    --radius-pill: 999px;
    --tag-success-bg: #dcfce7;
    --tag-success-text: #15803d;
    --tag-warning-bg: #fef3c7;
    --tag-warning-text: #92400e;
    --tag-error-bg: #fee2e2;
    --tag-error-text: #b91c1c;
    --tag-info-bg: #dbeafe;
    --tag-info-text: #1e40af;
    --tag-neutral-bg: #f1f3f5;
    --tag-neutral-text: #4b5563;
    --tag-neutral-text-secondary: #6b7280;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg-page); color: var(--text); padding: 40px 24px;
    display: flex; justify-content: center;
  }
  .container { max-width: 460px; width: 100%; }
  h1 { font-size: 1.5rem; font-weight: 700; margin-bottom: 4px; color: var(--text); display: inline-flex; align-items: center; gap: 10px; }
  h1 svg { width: 26px; height: 26px; flex-shrink: 0; color: var(--accent); }
  .subtitle { color: var(--text-muted); margin-bottom: 24px; }
  .card {
    background: var(--bg-card); border: 1px solid var(--border-subtle); border-radius: var(--radius-card);
    padding: 24px; margin-bottom: 16px;
    box-shadow: var(--shadow-card);
  }
  .card label { display: block; margin-bottom: 6px; color: var(--text); font-size: 0.875rem; font-weight: 500; }
  .card input[type="text"] {
    width: 100%; padding: 12px; background: var(--bg-card); color: var(--text);
    border: 1px solid var(--border-subtle); border-radius: var(--radius-control);
    font-size: 1.5rem; text-align: center; letter-spacing: 8px;
    margin-bottom: 16px; font-family: monospace;
  }
  .card input:focus { outline: none; border-color: var(--accent); }
  .card input[type="text"]:hover {
    border-color: var(--accent-hover);
  }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: var(--radius-control);
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none;
    width: 100%;
  }
  .btn-primary { background: var(--accent); color: #fff; }
  .btn-primary:hover { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .status {
    display: none; padding: 16px; border-radius: var(--radius-control); margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.error { display: block; background: var(--tag-error-bg); color: var(--tag-error-text); }
  .status.info { display: block; background: var(--tag-info-bg); color: var(--tag-info-text); }
  .back { display: inline-block; margin-top: 16px; color: var(--accent); text-decoration: none; }
  .back:hover { text-decoration: underline; }
  .email-info { color: var(--tag-success-text); font-size: 0.875rem; font-weight: 600; margin-bottom: 16px; text-align: center; }
</style>
</head>
<body>
<div class="container">
  <h1><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="16" rx="2"/><path d="m2 7 10 6 10-6"/></svg>Verification Code Sent</h1>
  <p class="subtitle">Check your inbox (and spam) for the 6-digit code.</p>
  <div class="email-info">Sent to: <strong>{{.Email}}</strong></div>

  <div class="card">
    <form id="codeForm">
      <label for="code">Verification Code</label>
      <input type="text" id="code" name="code" placeholder="000000" maxlength="6" inputmode="numeric" pattern="[0-9]*" autocomplete="one-time-code">

      <button type="submit" class="btn btn-primary" id="submitBtn">Verify Code</button>
    </form>
    <div id="status" class="status"></div>
  </div>

  <a href="{{.LoginURL}}" class="back">← Start over (different account)</a>
</div>

<script>
document.getElementById('codeForm').addEventListener('submit', async function(e) {
  e.preventDefault();
  const btn = document.getElementById('submitBtn');
  const status = document.getElementById('status');
  const code = document.getElementById('code').value.trim();
  if (!code || code.length < 4) {
    status.className = 'status error';
    status.textContent = 'Please enter the full verification code.';
    return;
  }
  btn.disabled = true;
  btn.textContent = 'Verifying...';
  status.className = 'status info';
  status.textContent = 'Completing login...';

  try {
    const res = await fetch('{{.CodeURL}}', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'code=' + encodeURIComponent(code)
    });
    const d = await res.json();
    if (d.success) {
      window.location.href = d.redirect;
    } else {
      status.className = 'status error';
      status.textContent = d.error || 'Verification failed.';
      btn.disabled = false;
      btn.textContent = 'Verify Code';
    }
  } catch (err) {
    status.className = 'status error';
    status.textContent = 'Network error: ' + err.message;
    btn.disabled = false;
    btn.textContent = 'Verify Code';
  }
});
</script>
</body>
</html>`

// ---------------------------------------------------------------------------
// Templates — Device Selection
// ---------------------------------------------------------------------------

// onboardingSelectTemplate shows bound printers and lets user pick which to add.
const onboardingSelectTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Connect Bambu Lab — Select Printers</title>
<style>
  :root {
    --bg-page: #f5f5f7;
    --bg-card: #ffffff;
    --text: #1d1d1f;
    --text-muted: #6e6e73;
    --text-subtle: #8a8a8e;
    --accent: #3b82f6;
    --accent-hover: #2f6fd6;
    --border-subtle: #e5e5ea;
    --shadow-card: 0 1px 3px rgba(0,0,0,.06), 0 1px 2px rgba(0,0,0,.04);
    --radius-control: 8px;
    --radius-card: 12px;
    --radius-pill: 999px;
    --tag-success-bg: #dcfce7;
    --tag-success-text: #15803d;
    --tag-warning-bg: #fef3c7;
    --tag-warning-text: #92400e;
    --tag-error-bg: #fee2e2;
    --tag-error-text: #b91c1c;
    --tag-info-bg: #dbeafe;
    --tag-info-text: #1e40af;
    --tag-neutral-bg: #f1f3f5;
    --tag-neutral-text: #4b5563;
    --tag-neutral-text-secondary: #6b7280;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg-page); color: var(--text); padding: 40px 24px;
    display: flex; justify-content: center;
  }
  .container { max-width: 600px; width: 100%; }
  h1 { font-size: 1.5rem; font-weight: 700; margin-bottom: 4px; color: var(--text); display: inline-flex; align-items: center; gap: 10px; }
  h1 svg { width: 26px; height: 26px; flex-shrink: 0; color: var(--accent); }
  .subtitle { color: var(--text-muted); margin-bottom: 24px; }
  .printer-item {
    background: var(--bg-card); border: 1px solid var(--border-subtle); border-radius: var(--radius-card);
    padding: 16px; margin-bottom: 12px;
    display: flex; align-items: center; gap: 12px;
    box-shadow: var(--shadow-card);
  }
  .printer-item:hover { border-color: #d0d0d6; }
  .printer-item input[type="checkbox"] {
    width: 20px; height: 20px; accent-color: var(--accent); flex-shrink: 0;
  }
  .printer-info { flex: 1; }
  .printer-info .name { font-weight: 600; font-size: 1rem; }
  .printer-info .detail { color: var(--text-muted); font-size: 0.8125rem; margin-top: 2px; }
  .printer-info .online { display: inline-block; padding: 1px 6px; border-radius: 6px; font-size: 0.75rem; font-weight: 700; }
  .printer-info .online.yes { background: var(--tag-success-bg); color: var(--tag-success-text); }
  .printer-info .online.no { background: var(--tag-neutral-bg); color: var(--tag-neutral-text-secondary); }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: var(--radius-control);
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none;
  }
  .btn-primary { background: var(--accent); color: #fff; width: 100%; }
  .btn-primary:hover { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .empty { color: var(--text-muted); text-align: center; padding: 40px; }
  .back { display: inline-block; margin-top: 16px; color: var(--accent); text-decoration: none; }
  .back:hover { text-decoration: underline; }
  .status {
    display: none; padding: 16px; border-radius: var(--radius-control); margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.saving { display: block; background: var(--tag-info-bg); color: var(--tag-info-text); }
  .status.done { display: block; background: var(--tag-success-bg); color: var(--tag-success-text); }
  .status.error { display: block; background: var(--tag-error-bg); color: var(--tag-error-text); }
  .user-badge { color: #15803d; font-size: 0.8125rem; font-weight: 600; margin-bottom: 16px; }
</style>
</head>
<body>
<div class="container">
  <h1><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><path d="m9 11 3 3L22 4"/></svg>Signed In</h1>
  <p class="subtitle">
    Select the printers to add to your dashboard.
    {{if .HasDevices}}
      <span style="color:#15803d;font-weight:600;">{{len .Devices}} printer(s) found on your account.</span>
    {{end}}
  </p>
  <div class="user-badge">User ID: {{.UserID}}</div>

  <form id="selectForm" action="/onboarding/bambu/save" method="POST">
    {{if .HasDevices}}
      {{range .Devices}}
      <div class="printer-item">
        <input type="checkbox" name="device_ids" value="{{.DevID}}" id="dev-{{.DevID}}" checked>
        <div class="printer-info">
          <div class="name">
            <label for="dev-{{.DevID}}">{{.Name}}</label>
            <span class="online {{if .Online}}yes{{else}}no{{end}}">
              {{if .Online}}Online{{else}}Offline{{end}}
            </span>
          </div>
          <div class="detail">
            Serial: {{.DevID}} &nbsp;|&nbsp; Model: {{.DevProductName}}
          </div>
        </div>
      </div>
      {{end}}

      <div style="margin-top: 8px; color: var(--text-muted); font-size: 0.8125rem;">
        You can add LAN IP and access code later for camera access.
      </div>

      <button type="submit" class="btn btn-primary" style="margin-top: 24px;">
        + Add Selected Printers
      </button>
    {{else}}
      <div class="empty">
        <p>No printers are bound to this Bambu account.</p>
        <p style="margin-top: 8px; font-size: 0.875rem;">
          Make sure you've added printers to your account in Bambu Handy or Bambu Studio.
        </p>
        <p style="margin-top: 16px;">
          The token is still saved — you can add printers later by editing config.yaml.
        </p>
      </div>
    {{end}}
  </form>

  <div id="status" class="status"></div>

  <a href="/onboarding/bambu" class="back">← Back to sign in</a>
</div>

<script>
document.getElementById('selectForm')?.addEventListener('submit', function(e) {
  e.preventDefault();
  const status = document.getElementById('status');
  status.className = 'status saving';
  status.textContent = 'Saving configuration and connecting printers...';

  const form = e.target;
  const formData = new FormData(form);

  fetch(form.action, {
    method: 'POST',
    body: formData
  }).then(r => r.json()).then(d => {
    if (d.success) {
      status.className = 'status done';
      status.textContent = d.printers_added + ' printer(s) added! Redirecting...';
      setTimeout(() => { window.location.href = d.redirect; }, 1500);
    } else {
      status.className = 'status error';
      status.textContent = 'Error: ' + d.error;
    }
  }).catch(e => {
    status.className = 'status error';
    status.textContent = 'Network error: ' + e.message;
  });
});
</script>
</body>
</html>`

// ---------------------------------------------------------------------------
// Templates — Snapmaker Form
// ---------------------------------------------------------------------------

// snapmakerFormTemplate is the manual entry form for Snapmaker U1.
const snapmakerFormTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Add Snapmaker U1</title>
<style>
  :root {
    --bg-page: #f5f5f7;
    --bg-card: #ffffff;
    --text: #1d1d1f;
    --text-muted: #6e6e73;
    --text-subtle: #8a8a8e;
    --accent: #3b82f6;
    --accent-hover: #2f6fd6;
    --border-subtle: #e5e5ea;
    --shadow-card: 0 1px 3px rgba(0,0,0,.06), 0 1px 2px rgba(0,0,0,.04);
    --radius-control: 8px;
    --radius-card: 12px;
    --radius-pill: 999px;
    --tag-success-bg: #dcfce7;
    --tag-success-text: #15803d;
    --tag-warning-bg: #fef3c7;
    --tag-warning-text: #92400e;
    --tag-error-bg: #fee2e2;
    --tag-error-text: #b91c1c;
    --tag-info-bg: #dbeafe;
    --tag-info-text: #1e40af;
    --tag-neutral-bg: #f1f3f5;
    --tag-neutral-text: #4b5563;
    --tag-neutral-text-secondary: #6b7280;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg-page); color: var(--text); padding: 40px 24px;
    display: flex; justify-content: center;
  }
  .container { max-width: 460px; width: 100%; }
  h1 { font-size: 1.5rem; font-weight: 700; margin-bottom: 4px; color: var(--text); display: inline-flex; align-items: center; gap: 10px; }
  h1 svg { width: 26px; height: 26px; flex-shrink: 0; color: var(--accent); }
  .subtitle { color: var(--text-muted); margin-bottom: 24px; }
  .card {
    background: var(--bg-card); border: 1px solid var(--border-subtle); border-radius: var(--radius-card);
    padding: 24px; margin-bottom: 16px;
    box-shadow: var(--shadow-card);
  }
  .card label { display: block; margin-bottom: 6px; color: var(--text); font-size: 0.875rem; font-weight: 500; }
  .card input[type="text"],
  .card input[type="number"] {
    width: 100%; padding: 12px; background: var(--bg-card); color: var(--text);
    border: 1px solid var(--border-subtle); border-radius: var(--radius-control);
    font-size: 1rem; margin-bottom: 16px;
  }
  .card input:focus { outline: none; border-color: var(--accent); }
  .card input[type="text"]:hover,
  .card input[type="number"]:hover {
    border-color: var(--accent-hover);
  }
  .card .hint { color: var(--text-muted); font-size: 0.75rem; margin-top: -12px; margin-bottom: 16px; }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: var(--radius-control);
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none;
    width: 100%;
  }
  .btn-primary { background: var(--accent); color: #fff; }
  .btn-primary:hover { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .status {
    display: none; padding: 16px; border-radius: var(--radius-control); margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.error { display: block; background: var(--tag-error-bg); color: var(--tag-error-text); }
  .status.info { display: block; background: var(--tag-info-bg); color: var(--tag-info-text); }
  .back { display: inline-block; margin-top: 16px; color: var(--accent); text-decoration: none; }
  .back:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="container">
  <h1><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>Add Snapmaker U1</h1>
  <p class="subtitle">Enter the network details for your Snapmaker U1 running Paxx firmware.</p>

  <div class="card">
    <form id="snapmakerForm">
      <label for="name">Printer Name</label>
      <input type="text" id="name" name="name" placeholder="e.g. Workshop U1" required>
      <div class="hint">A friendly name to identify this printer.</div>

      <label for="host">Host / IP Address</label>
      <input type="text" id="host" name="host" placeholder="e.g. 192.168.1.102" required>
      <div class="hint">The local network address of your Snapmaker U1.</div>

      <label for="port">Port</label>
      <input type="number" id="port" name="port" placeholder="e.g. 80" value="80">
      <div class="hint">The port the printer API listens on (default: 80).</div>

      <label for="access_code">Access Code (optional)</label>
      <input type="text" id="access_code" name="access_code" placeholder="API key if required">
      <div class="hint">If your Paxx firmware requires an API key or access code.</div>

      <button type="submit" class="btn btn-primary" id="submitBtn">+ Add Printer</button>
    </form>
    <div id="status" class="status"></div>
  </div>

  <a href="/onboarding" class="back">← Back to printer selection</a>
</div>

<script>
document.getElementById('snapmakerForm').addEventListener('submit', async function(e) {
  e.preventDefault();
  const btn = document.getElementById('submitBtn');
  const status = document.getElementById('status');
  btn.disabled = true;
  btn.textContent = 'Saving...';
  status.className = 'status info';
  status.textContent = 'Saving configuration...';

  const form = new FormData(this);
  try {
    const res = await fetch('/onboarding/snapmaker/save', { method: 'POST', body: form });
    const d = await res.json();
    if (d.success) {
      status.className = 'status info';
      status.textContent = 'Printer added! Redirecting...';
      setTimeout(() => { window.location.href = '/'; }, 1500);
    } else {
      status.className = 'status error';
      status.textContent = d.error || 'Failed to save.';
      btn.disabled = false;
      btn.textContent = '+ Add Printer';
    }
  } catch (err) {
    status.className = 'status error';
    status.textContent = 'Network error: ' + err.message;
    btn.disabled = false;
    btn.textContent = '+ Add Printer';
  }
});
</script>
</body>
</html>`
