package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/camera"
	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
	"github.com/chrisjohnson/printer-dashboard/internal/printers/bambu"
	"github.com/chrisjohnson/printer-dashboard/internal/printers/snapmaker"
	"github.com/chrisjohnson/printer-dashboard/internal/ws"
	"github.com/gorilla/websocket"
)

// Server holds the HTTP server, printer registry, and dependencies.
type Server struct {
	cfg        *config.Config
	http       *http.Server
	mux        *http.ServeMux
	printers   map[string]printers.Printer
	mu         sync.RWMutex
	bambuCloud *bambu.BambuCloudClient // cached for onboarding/reload
	configPath string                   // path to config.yaml for saving
	printersCtx context.CancelFunc      // cancel for printer connection goroutines
	cameraMgr  *camera.CameraManager    // persistent Bambu camera connections
	rtspMgr    *camera.Go2RTCManager    // go2rtc subprocess manager for RTSPS cameras

	// Onboarding provisional state (single-user, set during wizard)
	onboardingMu        sync.Mutex
	onboardingEmail     string // email for 2FA flow
	onboardingToken     string
	onboardingUserID    string
	onboardingDevices   []bambu.DeviceInfo
	onboardingCloud     *bambu.BambuCloudClient // partially-authenticated client

	// wsHub manages WebSocket connections for real-time status updates.
	wsHub *ws.Hub
}

// New creates a new Server from the provided config.
func New(cfg *config.Config, configPath string) (*Server, error) {
	mux := http.NewServeMux()

	s := &Server{
		cfg:        cfg,
		mux:        mux,
		printers:   make(map[string]printers.Printer),
		configPath: configPath,
		http: &http.Server{
			Addr:    cfg.Listen,
			Handler: mux,
		},
	}

	// Initialize persistent camera connection manager.
	s.cameraMgr = camera.NewCameraManager(context.Background())

	// Initialize go2rtc manager for RTSPS camera streams (H2S, etc.)
	s.rtspMgr = camera.NewGo2RTCManager("", 0)

	// Initialize WebSocket hub for real-time status updates
	s.wsHub = ws.NewHub()
	go s.wsHub.Run()

	s.initBambuCloud()
	s.initPrinters()
	s.migratePrinterModels() // backfill model for printers added before model field existed

	// Pre-connect camera streams eagerly on startup so frames are already
	// buffered before the first browser page load. This eliminates the
	// broken-image flash when the dashboard initially renders.
	for _, pdef := range cfg.Printers {
		if pdef.Type != "bambu" || pdef.Host == "" || pdef.AccessCode == "" {
			continue
		}
		if bambu.IsH2S(pdef.Model) {
			// H2-series: try RTSPS streams (port 322, requires LAN mode).
			// Only pre-connects the BirdsEye (/live/1) feed to eliminate the
			// broken-image flash on first load; the Toolhead (/live/2) feed
			// is started lazily on first request via camera.RTSPStreamKey.
			rtspsURL := fmt.Sprintf("rtsps://bblp:%s@%s:322/streaming/live/1", pdef.AccessCode, pdef.Host)
			parsedURL, err := url.Parse(rtspsURL)
			if err != nil {
				log.Printf("camera: failed to parse H2S camera URL for %s: %v", pdef.Name, err)
				continue
			}
			streamKey := camera.RTSPStreamKey(parsedURL)
			if _, err := s.rtspMgr.Start(context.Background(), streamKey, rtspsURL); err != nil {
				log.Printf("camera: failed to pre-connect H2S camera %s via RTSPS: %v", pdef.Name, err)
			}
		} else {
			// P1S/A1/X1 series: use bambus:// binary protocol on port 6000
			s.cameraMgr.GetBuffer(pdef.Host, 6000, pdef.AccessCode)
		}
	}

	s.registerRoutes()
	return s, nil
}

// initBambuCloud sets up the Bambu cloud client and attempts authentication.
func (s *Server) initBambuCloud() {
	cfg := s.cfg
	if cfg.BambuAccount == nil {
		return
	}

	s.bambuCloud = bambu.NewBambuCloudClient(cfg.BambuAccount.Region)

	// Token persistence: ~/.printer-dashboard/bambu_token_{email}.json
	tokenPath := bambu.DefaultTokenPath(cfg.BambuAccount.Email)
	if cfg.BambuAccount.Token != "" {
		tokenPath = filepath.Join(bambu.DefaultTokenDir, "bambu_token.json")
	}
	s.bambuCloud.SetTokenFile(tokenPath)

	// Authentication strategy (tried in order):
	// 1. Token from config
	// 2. Saved token from disk
	// 3. Email/password login (may require 2FA)

	authenticated := false

	// Strategy 1: Token from config
	if cfg.BambuAccount.Token != "" {
		log.Printf("Trying Bambu cloud auth with config token...")
		if err := s.bambuCloud.LoginWithToken(cfg.BambuAccount.Token); err != nil {
			log.Printf("WARNING: Bambu cloud token from config rejected: %v", err)
		} else {
			authenticated = true
		}
	}

	// Strategy 2: Saved token from disk
	if !authenticated {
		if loaded, err := s.bambuCloud.LoadToken(); err != nil {
			log.Printf("WARNING: Could not load saved Bambu token: %v", err)
		} else if loaded {
			log.Printf("Loaded saved Bambu token from %s", tokenPath)
			if s.bambuCloud.TokenValid() {
				if _, err := s.bambuCloud.GetDevices(); err != nil {
					log.Printf("WARNING: Saved Bambu token expired or invalid: %v", err)
					s.bambuCloud.DeleteToken()
				} else {
					authenticated = true
					log.Printf("Bambu cloud: saved token is valid, expires in %s",
						bambu.FormatDuration(s.bambuCloud.TokenLifetimeLeft()))
				}
			} else {
				log.Printf("Saved Bambu token expired (was valid for %s, delete and re-auth)",
					bambu.FormatDuration(s.bambuCloud.TokenLifetimeLeft()))
				s.bambuCloud.DeleteToken()
			}
		}
	}

	// Strategy 3: Email/password (best-effort)
	if !authenticated && cfg.BambuAccount.Email != "" && cfg.BambuAccount.Password != "" {
		log.Printf("Attempting Bambu cloud login with email/password...")
		if err := s.bambuCloud.Login(cfg.BambuAccount.Email, cfg.BambuAccount.Password, nil); err != nil {
			log.Printf("WARNING: Bambu cloud login failed (2FA may be required): %v", err)
		} else {
			authenticated = true
		}
	}

	if !authenticated {
		log.Printf("WARNING: No valid Bambu cloud authentication available.")
		log.Printf("  Bambu printers will be skipped. Use the onboarding wizard to add printers.")
	}
}

// initPrinters creates printer clients from config. Safe to call after config change.
func (s *Server) initPrinters() {
	cfg := s.cfg
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear existing printers
	s.printers = make(map[string]printers.Printer)

	for _, pdef := range cfg.Printers {
		var p printers.Printer
		switch pdef.Type {
		case "bambu":
			if s.bambuCloud == nil || s.bambuCloud.Token() == "" {
				log.Printf("WARNING: printer %q requires Bambu cloud auth, skipping", pdef.Name)
				continue
			}
			bp := bambu.New(pdef, s.bambuCloud)
			if pdef.Model != "" {
				bp.SetModel(pdef.Model)
			}
			if s.wsHub != nil {
				// Set up a buffered channel for real-time status updates.
				// The broadcast goroutine is started in connectAllPrinters
				// so it can be cancelled with the printer context on reload.
				bp.StatusCh = make(chan printers.PrinterStatus, 16)
			}
			p = bp
		case "snapmaker":
			sp := snapmaker.New(pdef)
			if s.wsHub != nil {
				// Set up a buffered channel for real-time status updates.
				// The broadcast goroutine is started in connectAllPrinters
				// so it can be cancelled with the printer context on reload.
				sp.StatusCh = make(chan printers.PrinterStatus, 16)
			}
			p = sp
		default:
			log.Printf("WARNING: printer %q has unsupported type %q — skipping", pdef.Name, pdef.Type)
			continue
		}
		s.printers[pdef.ID] = p
		log.Printf("Registered printer: %s (%s)", pdef.Name, pdef.Type)
	}
}

// migratePrinterModels backfills the Model field for printers that were added
// to config.yaml before the model field existed (e.g., via older onboarding code).
// It queries the Bambu cloud API and persists any discovered models.
func (s *Server) migratePrinterModels() {
	if s.bambuCloud == nil {
		return
	}
	devices, err := s.bambuCloud.GetDevices()
	if err != nil {
		log.Printf("migrate models: could not fetch device list: %v", err)
		return
	}
	modelBySerial := make(map[string]string, len(devices))
	for _, d := range devices {
		modelBySerial[d.DevID] = d.DevModelName
	}

	changed := false
	for i, p := range s.cfg.Printers {
		if p.Type != "bambu" || p.Model != "" {
			continue
		}
		if model, ok := modelBySerial[p.Serial]; ok && model != "" {
			s.cfg.Printers[i].Model = model
			changed = true
			log.Printf("migrate models: set model %q for printer %q (%s)", model, p.Name, p.Serial)
		}
	}
	if changed {
		if err := s.cfg.Save(); err != nil {
			log.Printf("migrate models: failed to save config: %v", err)
		} else {
			log.Printf("migrate models: config saved with backfilled model fields")
		}
	}
}

// Start begins the HTTP server and printer connections, blocks until ctx cancels.
func (s *Server) Start(ctx context.Context) error {
	// Start printer connection loops in background
	printerCtx, cancelPrinters := context.WithCancel(context.Background())
	s.printersCtx = cancelPrinters
	defer cancelPrinters()

	s.connectAllPrinters(printerCtx)

	// Start HTTP server
	errCh := make(chan error, 1)
	go func() {
		log.Printf("HTTP server listening on %s", s.cfg.Listen)
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Println("Shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := s.http.Shutdown(shutdownCtx)
		if s.cameraMgr != nil {
			s.cameraMgr.Stop()
		}
		if s.rtspMgr != nil {
			s.rtspMgr.StopAll()
			log.Println("Stopped all go2rtc instances")
		}
		return err
	case err := <-errCh:
		return err
	}
}

// connectAllPrinters launches all registered printer connection goroutines
// and, for printers with a StatusCh, forwarding goroutines that broadcast
// status updates to all connected WebSocket clients.
func (s *Server) connectAllPrinters(ctx context.Context) {
	for id, p := range s.printers {
		// Start status forwarding for printers with a StatusCh.
		if s.wsHub != nil {
			if bp, ok := p.(*bambu.Client); ok && bp.StatusCh != nil {
				s.startStatusForwarder(ctx, id, bp.StatusCh)
			}
			if sp, ok := p.(*snapmaker.Printer); ok && sp.StatusCh != nil {
				s.startStatusForwarder(ctx, id, sp.StatusCh)
			}
		}

		// Start the printer connection goroutine (blocks until ctx cancelled).
		go func(printer printers.Printer) {
			log.Printf("Connecting to printer: %s", printer.Name())
			if err := printer.Connect(ctx); err != nil {
				log.Printf("Printer %s disconnected with error: %v", printer.Name(), err)
			}
		}(p)
	}
}

// startStatusForwarder reads from statusCh and broadcasts every received
// status to all WebSocket clients. It exits when ctx is cancelled or the
// channel is closed.
func (s *Server) startStatusForwarder(ctx context.Context, printerID string, statusCh chan printers.PrinterStatus) {
	go func(pid string, ch chan printers.PrinterStatus) {
		log.Printf("[ws] starting status forwarder for printer %s", pid)
		defer log.Printf("[ws] stopped status forwarder for printer %s", pid)
		for {
			select {
			case status, ok := <-ch:
				if !ok {
					return
				}
				enriched := s.enrichStatusWithCameras(pid, status)
				msg := map[string]any{
					"type":    "printer_update",
					"printer": enriched,
				}
				data, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				s.wsHub.Broadcast(data)
			case <-ctx.Done():
				return
			}
		}
	}(printerID, statusCh)
}

// reloadConfig re-reads config.yaml, re-initializes Bambu cloud + printers.
// Used after onboarding writes a new config.
func (s *Server) reloadConfig() error {
	// Cancel existing printer connections
	if s.printersCtx != nil {
		s.printersCtx()
	}

	// Re-read config
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return fmt.Errorf("reloading config: %w", err)
	}
	s.cfg = cfg

	// Re-init Bambu cloud
	s.bambuCloud = nil
	s.initBambuCloud()

	// Re-init printers
	s.initPrinters()

	// Restart connections
	printerCtx, cancel := context.WithCancel(context.Background())
	s.printersCtx = cancel
	s.connectAllPrinters(printerCtx)

	log.Printf("Configuration reloaded: %d printer(s) registered", len(s.printers))
	return nil
}

// wsUpgrader upgrades HTTP connections to WebSocket connections.
// It allows all origins since the dashboard is a single-user application.
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) registerRoutes() {
	// Web UI
	s.mux.HandleFunc("GET /", s.handleIndex)

	// Onboarding wizard
	s.mux.HandleFunc("GET /onboarding", s.handleOnboardingStart)
	s.mux.HandleFunc("GET /onboarding/bambu", s.handleOnboardingBambuLoginPage)
	s.mux.HandleFunc("POST /onboarding/bambu/login", s.handleOnboardingBambuLogin)
	s.mux.HandleFunc("GET /onboarding/bambu/code", s.handleOnboardingBambuCodePage)
	s.mux.HandleFunc("POST /onboarding/bambu/code", s.handleOnboardingBambuCodeSubmit)
	s.mux.HandleFunc("GET /onboarding/bambu/select", s.handleOnboardingBambuSelect)
	s.mux.HandleFunc("POST /onboarding/bambu/save", s.handleOnboardingBambuSave)
	s.mux.HandleFunc("GET /onboarding/snapmaker", s.handleOnboardingSnapmakerPage)
	s.mux.HandleFunc("POST /onboarding/snapmaker/save", s.handleOnboardingSnapmakerSave)

	// WebSocket
	s.mux.HandleFunc("GET /ws", s.handleWebSocket)

	// API
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/printers", s.handleListPrinters)
	s.mux.HandleFunc("GET /api/printers/{id}", s.handleGetPrinter)
	s.mux.HandleFunc("POST /api/printers/{id}/pause", s.handlePause)
	s.mux.HandleFunc("POST /api/printers/{id}/resume", s.handleResume)
	s.mux.HandleFunc("POST /api/printers/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/printers/{id}/skip", s.handleSkipObject)

	// Camera stream proxy (same-origin proxy to avoid mixed-content issues)
	s.mux.HandleFunc("GET /api/camera/proxy", camera.Handler(s.cameraMgr, s.rtspMgr))
	s.mux.HandleFunc("GET /api/camera/frame", camera.FrameHandler(s.cameraMgr, s.rtspMgr))
}

// renderTemplate is a helper that executes a Go template and writes to the response.
func renderTemplate(w http.ResponseWriter, tmpl string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, err := template.New("page").Parse(tmpl)
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := t.Execute(w, data); err != nil {
		log.Printf("Template execute error: %v", err)
	}
}

// getPrinter safely retrieves a printer by ID from the registry.
func (s *Server) getPrinter(id string) (printers.Printer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.printers[id]
	return p, ok
}

// writeJSON is a helper to marshal and write a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Error writing JSON response: %v", err)
	}
}

// enrichStatusWithCameras merges the printer driver's camera streams with
// externally-configured cameras from CameraDef configs.
// Ordering: driver internal → config external → driver external/touchscreen.
// The caller must NOT hold s.mu (this method acquires it as needed).
func (s *Server) enrichStatusWithCameras(printerID string, status printers.PrinterStatus) printers.PrinterStatus {
	s.mu.RLock()
	p, ok := s.printers[printerID]
	s.mu.RUnlock()
	if !ok {
		return status
	}

	return s.enrichWithStreams(p.CameraStreams(), printerID, status)
}

// enrichWithStreams is a helper that merges camera streams into a status.
// It does not acquire any locks — the caller is responsible for ensuring
// the config data is safe to read.
func (s *Server) enrichWithStreams(driverStreams []printers.CameraStream, printerID string, status printers.PrinterStatus) printers.PrinterStatus {
	// External cameras from config
	var configStreams []printers.CameraStream
	for _, cam := range s.cfg.Cameras {
		if cam.PrinterID == printerID {
			configStreams = append(configStreams, printers.CameraStream{
				URL:   cam.URL,
				Type:  "external",
				Label: cam.Name,
			})
		}
	}

	if len(driverStreams) == 0 && len(configStreams) == 0 {
		return status
	}

	// Merge: internal drivers → config externals → remaining drivers
	var merged []printers.CameraStream
	for _, s := range driverStreams {
		if s.Type == "internal" {
			merged = append(merged, s)
		}
	}
	merged = append(merged, configStreams...)
	for _, s := range driverStreams {
		if s.Type != "internal" {
			merged = append(merged, s)
		}
	}

	// Rewrite camera URLs to go through our same-origin proxy.
	for i := range merged {
		if merged[i].Type != "touchscreen" {
			merged[i].URL = proxyCameraURL(merged[i].URL)
		}
	}

	status.CameraStreams = merged
	return status
}

// proxyCameraURL rewrites a camera stream URL to go through the server-side
// proxy endpoint, keeping requests same-origin to avoid mixed-content issues.
func proxyCameraURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	return "/api/camera/proxy?url=" + url.QueryEscape(rawURL)
}

// writeError is a helper to write a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// --- WebSocket ---

// handleWebSocket upgrades an HTTP connection to a WebSocket connection and
// registers the client with the hub. Clients receive real-time printer status
// updates as JSON messages.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	client := ws.NewClient(s.wsHub, conn)
	s.wsHub.Register(client)

	go client.WritePump()
	go client.ReadPump()
}

// --- Web UI ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	printerCount := len(s.printers)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if printerCount == 0 {
		// Show onboarding welcome page
		renderTemplate(w, indexOnboardingTemplate, nil)
		return
	}

	// Show the dashboard. SkeletonCards lets the template render N
	// placeholder cards server-side (matching the real printer count) so the
	// client-side innerHTML swap in loadPrinters() doesn't change page
	// height/layout once real data arrives.
	renderTemplate(w, indexDashboardTemplate, map[string]any{
		"HasPrinters":   true,
		"SkeletonCards": make([]struct{}, printerCount),
	})
}

// --- API Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// printerSortPriority returns a sort tier for the given printer state:
//
//	0 = error/needs attention (sorted first)
//	1 = active (printing, paused, or any other non-idle, non-error state)
//	2 = idle/inactive (sorted last)
func printerSortPriority(state string) int {
	switch state {
	case "error":
		return 0
	case "idle":
		return 2
	default:
		return 1
	}
}

func (s *Server) handleListPrinters(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	printerList := make([]printers.PrinterStatus, 0, len(s.printers))
	for id, p := range s.printers {
		printerList = append(printerList, s.enrichWithStreams(p.CameraStreams(), id, p.Status()))
	}
	s.mu.RUnlock()

	// Three-tier sort: error (needs attention) → active (printing/paused/etc.) → idle.
	// Within each tier, sort A-Z by name (case-insensitive).
	sort.Slice(printerList, func(i, j int) bool {
		pi := printerSortPriority(printerList[i].State)
		pj := printerSortPriority(printerList[j].State)
		if pi != pj {
			return pi < pj
		}
		return strings.ToLower(printerList[i].Name) < strings.ToLower(printerList[j].Name)
	})

	writeJSON(w, 200, map[string]any{"printers": printerList})
}

func (s *Server) handleGetPrinter(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.getPrinter(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("printer %q not found", id))
		return
	}
	writeJSON(w, 200, s.enrichStatusWithCameras(id, p.Status()))
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.getPrinter(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("printer %q not found", id))
		return
	}
	if err := p.Pause(r.Context()); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.getPrinter(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("printer %q not found", id))
		return
	}
	if err := p.Resume(r.Context()); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.getPrinter(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("printer %q not found", id))
		return
	}
	if err := p.Cancel(r.Context()); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleSkipObject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.getPrinter(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("printer %q not found", id))
		return
	}
	if err := p.SkipObject(r.Context()); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
