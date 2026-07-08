package server

import (
	"fmt"
	"log"
	"net/http"
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

	port := 8080 // default
	if portStr != "" {
		if p, err := fmt.Sscanf(portStr, "%d", &port); err != nil || p != 1 {
			port = 8080
		}
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
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #111; color: #eee; padding: 20px;
    display: flex; justify-content: center; align-items: center; min-height: 100vh;
  }
  .onboarding {
    text-align: center; max-width: 520px;
  }
  h1 { font-size: 2rem; margin-bottom: 8px; }
  p { color: #999; margin-bottom: 32px; font-size: 1.05rem; }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: 10px;
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none; transition: background 0.2s;
    background: #0071e3; color: #fff;
  }
  .btn:hover { background: #0064cc; }
  .step-list { text-align: left; margin: 24px 0; color: #bbb; font-size: 0.9rem; }
  .step-list li { margin: 8px 0; }
</style>
</head>
<body>
<div class="onboarding">
  <h1>🖨 Printer Dashboard</h1>
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
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: #111; color: #eee; padding: 12px;
    }
    h1 { font-size: 1.3rem; margin-bottom: 12px; color: #fff; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
    h1 .count { color: #666; font-size: 0.85rem; font-weight: 400; }
    .header-actions { margin-left: auto; display: flex; gap: 8px; }

    /* Printer grid — mobile first: single column */
    .printers { display: grid; grid-template-columns: 1fr; gap: 10px; }

    /* Card — compact on mobile, expands on desktop */
    .card {
      background: #1e1e1e; border: 1px solid #333; border-radius: 10px; padding: 12px;
      display: flex; flex-direction: column; gap: 6px;
    }
    .card-header {
      display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
    }
    .card-header h2 { font-size: 1rem; font-weight: 600; }
    .tag {
      display: inline-block; padding: 2px 8px; border-radius: 4px;
      font-size: 0.7rem; font-weight: 600; text-transform: uppercase;
      letter-spacing: 0.03em;
    }
    .tag.printing { background: #2d7d46; color: #fff; }
    .tag.paused { background: #b8860b; color: #fff; }
    .tag.idle { background: #555; color: #ddd; }
    .tag.error { background: #c0392b; color: #fff; }
    .tag.complete { background: #1a5a3a; color: #90ee90; }
    .tag.unknown { background: #444; color: #aaa; }
    .tag.offline { background: #333; color: #666; }

    .card-online { font-size: 0.7rem; color: #444; margin-left: auto; }
    .card-online.yes { color: #2d7d46; }

    /* Progress bar — always visible */
    .progress-section { margin: 2px 0; }
    .progress-bar { background: #2a2a2a; height: 6px; border-radius: 3px; overflow: hidden; }
    .progress-bar .fill { background: #2d7d46; height: 100%; transition: width 0.5s; }
    .progress-text { font-size: 0.8rem; color: #aaa; display: flex; justify-content: space-between; }

    /* Temperature row — compact on mobile, expanded on desktop */
    .temps {
      display: flex; flex-wrap: wrap; gap: 4px 14px;
      font-size: 0.78rem; color: #aaa;
      padding: 4px 0;
    }
    .temps .label { color: #666; }
    .temps .val { color: #ddd; font-variant-numeric: tabular-nums; }
    .temps .target { color: #888; }
    .temp-row { display: flex; gap: 4px; align-items: center; }

    /* File name — only on desktop */
    .filename { display: none; font-size: 0.75rem; color: #666; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

    /* Controls — always visible but less buttons on mobile */
    .controls { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 4px; }
    .controls button {
      background: #333; color: #fff; border: 1px solid #555;
      padding: 4px 10px; border-radius: 5px; cursor: pointer;
      font-size: 0.75rem; transition: background 0.15s;
    }
    .controls button:hover { background: #444; }
    .controls button:disabled { opacity: 0.3; cursor: not-allowed; }
    .controls button.danger { border-color: #8b3a3a; color: #f88; }
    .controls button.danger:hover { background: #5a2222; }
    /* Hide skip + resume on mobile */
    .btn-skip, .btn-resume { display: none; }

    /* Layer info — desktop only */
    .layer-info { display: none; font-size: 0.75rem; color: #666; }

    .add-printer {
      display: inline-block; margin-top: 10px; padding: 8px 16px;
      background: #0071e3; color: #fff; border-radius: 8px;
      text-decoration: none; font-size: 0.85rem; font-weight: 500;
    }
    .add-printer:hover { background: #0064cc; }

    /* ─── Desktop (>=768px) ─── */
    @media (min-width: 768px) {
      body { padding: 24px; }
      h1 { font-size: 1.5rem; }
      .printers { grid-template-columns: repeat(auto-fill, minmax(400px, 1fr)); gap: 16px; }
      .card { padding: 16px; gap: 8px; }
      .card-header h2 { font-size: 1.1rem; }
      .temps { font-size: 0.85rem; gap: 6px 20px; }
      .filename { display: block; }
      .layer-info { display: block; }
      .btn-skip, .btn-resume { display: inline-block; }
      .progress-bar { height: 8px; }
    }

    /* ─── Wide desktop (>=1200px) ─── */
    @media (min-width: 1200px) {
      .printers { grid-template-columns: repeat(auto-fill, minmax(480px, 1fr)); }
      .card { padding: 20px; }
      .temps { font-size: 0.9rem; gap: 8px 28px; }
    }
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
    <p style="color:#666;padding:20px;">Loading printers...</p>
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

    function updateCard(p) {
      const container = document.getElementById('printer-list');
      const card = document.getElementById('printer-' + p.id);
      if (!card) {
        // New printer appeared — rebuild entire list
        loadPrinters();
        return;
      }
      // Update printer count
      const count = document.getElementById('printer-count');
      const list = Object.keys(window._printerCache);
      count.textContent = list.length + ' printer' + (list.length !== 1 ? 's' : '');
      // Replace the card's outerHTML with a freshly rendered one
      card.outerHTML = renderCard(p);
    }

    function loadPrinters() {
      fetch('/api/printers')
        .then(r => r.json())
        .then(data => {
          const container = document.getElementById('printer-list');
          const count = document.getElementById('printer-count');
          const list = data.printers || [];
          count.textContent = list.length + ' printer' + (list.length !== 1 ? 's' : '');
          if (list.length === 0) {
            container.innerHTML = '<p style="color:#666;padding:20px;">No printers configured. <a href="/onboarding" style="color:#0071e3;">Add one</a>.</p>';
            return;
          }
          // Populate cache with full response
          list.forEach(function(p) {
            window._printerCache[p.id] = p;
          });
          container.innerHTML = list.map(renderCard).join('');
        })
        .catch(() => {
          document.getElementById('printer-list').innerHTML = '<p style="color:#c0392b;padding:20px;">Error loading printers.</p>';
        });
    }

    function renderCard(p) {
      const st = p.state || 'unknown';
      const stCls = p.online ? st : 'offline';
      const progress = (p.progress * 100).toFixed(1);
      const timeStr = p.remaining_time > 0 ? formatTime(p.remaining_time) : '';

      // Temperatures
      const bed = p.bed_temp.toFixed(1);
      const bedT = p.bed_target_temp.toFixed(1);
      const nozzle = p.nozzle_temp.toFixed(1);
      const nozzleT = p.nozzle_target_temp.toFixed(1);
      const chamber = p.chamber_temp > 0 ? '<span class="temp-row"><span class="label">CH:</span><span class="val">' + p.chamber_temp.toFixed(1) + '°C</span></span>' : '';

      // Online indicator
      const onlineDot = p.online ? '<span class="card-online yes">●</span>' : '<span class="card-online">○ Offline</span>';

      // File name (desktop only)
      const fileHtml = p.current_file ? '<div class="filename">' + escapeHtml(p.current_file) + '</div>' : '';

      // Layer info (desktop only)
      const layerHtml = (p.total_layers > 0) ? '<div class="layer-info">Layer ' + p.current_layer + ' / ' + p.total_layers + '</div>' : '';

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
        '<div class="temps">' +
          '<span class="temp-row"><span class="label">BED:</span><span class="val">' + bed + '°C</span> <span class="target">→' + bedT + '°C</span></span>' +
          '<span class="temp-row"><span class="label">NOZ:</span><span class="val">' + nozzle + '°C</span> <span class="target">→' + nozzleT + '°C</span></span>' +
          chamber +
        '</div>' +
        fileHtml +
        layerHtml +
        '<div class="controls">' +
          '<button onclick="cmd(\'' + p.id + '\',\'pause\')" ' + (st !== 'printing' ? 'disabled' : '') + '>⏸</button>' +
          '<button onclick="cmd(\'' + p.id + '\',\'resume\')" class="btn-resume" ' + (st !== 'paused' ? 'disabled' : '') + '>▶ Resume</button>' +
          '<button onclick="cmd(\'' + p.id + '\',\'cancel\')" class="danger" ' + (st !== 'printing' && st !== 'paused' ? 'disabled' : '') + '>⏹ Cancel</button>' +
          '<button onclick="cmd(\'' + p.id + '\',\'skip\')" class="btn-skip" ' + (st !== 'printing' ? 'disabled' : '') + '>⏭ Skip</button>' +
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
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #111; color: #eee; padding: 40px 20px;
  }
  .container { max-width: 600px; margin: 0 auto; }
  h1 { font-size: 1.5rem; margin-bottom: 4px; }
  .subtitle { color: #999; margin-bottom: 24px; }
  .option {
    background: #1e1e1e; border: 1px solid #333; border-radius: 12px;
    padding: 20px; margin-bottom: 12px; cursor: pointer;
    transition: border-color 0.2s;
    display: block; text-decoration: none; color: inherit;
  }
  .option:hover { border-color: #0071e3; }
  .option h3 { font-size: 1.1rem; margin-bottom: 4px; }
  .option p { color: #999; font-size: 0.9rem; }
  .option .tag {
    display: inline-block; background: #2d7d46; color: #fff;
    padding: 2px 8px; border-radius: 4px; font-size: 0.75rem;
    margin-left: 8px; vertical-align: middle;
  }
  .option .tag-coming {
    display: inline-block; background: #b8860b; color: #fff;
    padding: 2px 8px; border-radius: 4px; font-size: 0.75rem;
    margin-left: 8px; vertical-align: middle;
  }
  .back { display: inline-block; margin-top: 16px; color: #0071e3; text-decoration: none; }
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
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #111; color: #eee; padding: 40px 20px;
    display: flex; justify-content: center;
  }
  .container { max-width: 460px; width: 100%; }
  h1 { font-size: 1.5rem; margin-bottom: 4px; }
  .subtitle { color: #999; margin-bottom: 24px; }
  .card {
    background: #1e1e1e; border: 1px solid #333; border-radius: 12px;
    padding: 24px; margin-bottom: 16px;
  }
  .card label { display: block; margin-bottom: 6px; color: #ccc; font-size: 0.9rem; font-weight: 500; }
  .card input[type="email"],
  .card input[type="password"] {
    width: 100%; padding: 12px; background: #000; color: #eee;
    border: 1px solid #333; border-radius: 8px;
    font-size: 1rem; margin-bottom: 16px;
  }
  .card input:focus { outline: none; border-color: #0071e3; }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: 10px;
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none; transition: background 0.2s;
    width: 100%;
  }
  .btn-primary { background: #0071e3; color: #fff; }
  .btn-primary:hover { background: #0064cc; }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .btn-secondary { background: #333; color: #eee; border: 1px solid #555; }
  .btn-secondary:hover { background: #444; }
  .status {
    display: none; padding: 16px; border-radius: 8px; margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.error { display: block; background: #7f1d1d; color: #fecaca; }
  .status.info { display: block; background: #1e3a5f; color: #bfdbfe; }
  .back { display: inline-block; margin-top: 16px; color: #0071e3; text-decoration: none; }
  .back:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="container">
  <h1>🔑 Sign in to Bambu Lab</h1>
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
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #111; color: #eee; padding: 40px 20px;
    display: flex; justify-content: center;
  }
  .container { max-width: 460px; width: 100%; }
  h1 { font-size: 1.5rem; margin-bottom: 4px; }
  .subtitle { color: #999; margin-bottom: 24px; }
  .card {
    background: #1e1e1e; border: 1px solid #333; border-radius: 12px;
    padding: 24px; margin-bottom: 16px;
  }
  .card label { display: block; margin-bottom: 6px; color: #ccc; font-size: 0.9rem; font-weight: 500; }
  .card input[type="text"] {
    width: 100%; padding: 12px; background: #000; color: #eee;
    border: 1px solid #333; border-radius: 8px;
    font-size: 1.5rem; text-align: center; letter-spacing: 8px;
    margin-bottom: 16px; font-family: monospace;
  }
  .card input:focus { outline: none; border-color: #0071e3; }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: 10px;
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none; transition: background 0.2s;
    width: 100%;
  }
  .btn-primary { background: #0071e3; color: #fff; }
  .btn-primary:hover { background: #0064cc; }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .status {
    display: none; padding: 16px; border-radius: 8px; margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.error { display: block; background: #7f1d1d; color: #fecaca; }
  .status.info { display: block; background: #1e3a5f; color: #bfdbfe; }
  .back { display: inline-block; margin-top: 16px; color: #0071e3; text-decoration: none; }
  .back:hover { text-decoration: underline; }
  .email-info { color: #6ee7b7; font-size: 0.9rem; margin-bottom: 16px; text-align: center; }
</style>
</head>
<body>
<div class="container">
  <h1>📧 Verification Code Sent</h1>
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
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #111; color: #eee; padding: 40px 20px;
    display: flex; justify-content: center;
  }
  .container { max-width: 600px; width: 100%; }
  h1 { font-size: 1.5rem; margin-bottom: 4px; }
  .subtitle { color: #999; margin-bottom: 24px; }
  .printer-item {
    background: #1e1e1e; border: 1px solid #333; border-radius: 12px;
    padding: 16px; margin-bottom: 12px;
    display: flex; align-items: center; gap: 12px;
    transition: border-color 0.2s;
  }
  .printer-item:hover { border-color: #555; }
  .printer-item input[type="checkbox"] {
    width: 20px; height: 20px; accent-color: #0071e3; flex-shrink: 0;
  }
  .printer-info { flex: 1; }
  .printer-info .name { font-weight: 600; font-size: 1rem; }
  .printer-info .detail { color: #999; font-size: 0.85rem; margin-top: 2px; }
  .printer-info .online { display: inline-block; padding: 1px 6px; border-radius: 3px; font-size: 0.75rem; }
  .printer-info .online.yes { background: #2d7d46; color: #fff; }
  .printer-info .online.no { background: #555; color: #ddd; }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: 10px;
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none; transition: background 0.2s;
  }
  .btn-primary { background: #0071e3; color: #fff; width: 100%; }
  .btn-primary:hover { background: #0064cc; }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .empty { color: #999; text-align: center; padding: 40px; }
  .back { display: inline-block; margin-top: 16px; color: #0071e3; text-decoration: none; }
  .back:hover { text-decoration: underline; }
  .status {
    display: none; padding: 16px; border-radius: 8px; margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.saving { display: block; background: #1e3a5f; color: #bfdbfe; }
  .status.done { display: block; background: #065f46; color: #d1fae5; }
  .status.error { display: block; background: #7f1d1d; color: #fecaca; }
  .user-badge { color: #6ee7b7; font-size: 0.85rem; margin-bottom: 16px; }
</style>
</head>
<body>
<div class="container">
  <h1>✅ Signed In</h1>
  <p class="subtitle">
    Select the printers to add to your dashboard.
    {{if .HasDevices}}
      <span style="color:#6ee7b7;">{{len .Devices}} printer(s) found on your account.</span>
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

      <div style="margin-top: 8px; color: #666; font-size: 0.85rem;">
        You can add LAN IP and access code later for camera access.
      </div>

      <button type="submit" class="btn btn-primary" style="margin-top: 20px;">
        + Add Selected Printers
      </button>
    {{else}}
      <div class="empty">
        <p>No printers are bound to this Bambu account.</p>
        <p style="margin-top: 8px; font-size: 0.9rem;">
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
      status.textContent = '✅ ' + d.printers_added + ' printer(s) added! Redirecting...';
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
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #111; color: #eee; padding: 40px 20px;
    display: flex; justify-content: center;
  }
  .container { max-width: 460px; width: 100%; }
  h1 { font-size: 1.5rem; margin-bottom: 4px; }
  .subtitle { color: #999; margin-bottom: 24px; }
  .card {
    background: #1e1e1e; border: 1px solid #333; border-radius: 12px;
    padding: 24px; margin-bottom: 16px;
  }
  .card label { display: block; margin-bottom: 6px; color: #ccc; font-size: 0.9rem; font-weight: 500; }
  .card input[type="text"],
  .card input[type="number"] {
    width: 100%; padding: 12px; background: #000; color: #eee;
    border: 1px solid #333; border-radius: 8px;
    font-size: 1rem; margin-bottom: 16px;
  }
  .card input:focus { outline: none; border-color: #0071e3; }
  .card .hint { color: #666; font-size: 0.8rem; margin-top: -12px; margin-bottom: 16px; }
  .btn {
    display: inline-block; padding: 14px 32px; border-radius: 10px;
    font-size: 1rem; font-weight: 600; cursor: pointer;
    text-decoration: none; border: none; transition: background 0.2s;
    width: 100%;
  }
  .btn-primary { background: #0071e3; color: #fff; }
  .btn-primary:hover { background: #0064cc; }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
  .status {
    display: none; padding: 16px; border-radius: 8px; margin-top: 16px;
    font-weight: 500; text-align: center;
  }
  .status.error { display: block; background: #7f1d1d; color: #fecaca; }
  .status.info { display: block; background: #1e3a5f; color: #bfdbfe; }
  .back { display: inline-block; margin-top: 16px; color: #0071e3; text-decoration: none; }
  .back:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="container">
  <h1>🔧 Add Snapmaker U1</h1>
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
      <input type="number" id="port" name="port" placeholder="e.g. 8080" value="8080">
      <div class="hint">The port the Paxx API listens on (default: 8080).</div>

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
      status.textContent = '✅ Printer added! Redirecting...';
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
