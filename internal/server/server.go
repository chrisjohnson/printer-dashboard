package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
	"github.com/chrisjohnson/printer-dashboard/internal/printers/bambu"
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

	// Onboarding provisional state (single-user, set during wizard)
	onboardingMu        sync.Mutex
	onboardingEmail     string // email for 2FA flow
	onboardingToken     string
	onboardingUserID    string
	onboardingDevices   []bambu.DeviceInfo
	onboardingCloud     *bambu.BambuCloudClient // partially-authenticated client
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

	s.initBambuCloud()
	s.initPrinters()
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
			p = bambu.New(pdef, s.bambuCloud)
		// case "snapmaker":
		// 	p = snapmaker.New(pdef)
		default:
			log.Printf("WARNING: printer %q has unsupported type %q — skipping", pdef.Name, pdef.Type)
			continue
		}
		s.printers[pdef.ID] = p
		log.Printf("Registered printer: %s (%s)", pdef.Name, pdef.Type)
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
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// connectAllPrinters launches all registered printer connection goroutines.
func (s *Server) connectAllPrinters(ctx context.Context) {
	for _, p := range s.printers {
		go func(printer printers.Printer) {
			log.Printf("Connecting to printer: %s", printer.Name())
			if err := printer.Connect(ctx); err != nil {
				log.Printf("Printer %s disconnected with error: %v", printer.Name(), err)
			}
		}(p)
	}
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

	// API
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/printers", s.handleListPrinters)
	s.mux.HandleFunc("GET /api/printers/{id}", s.handleGetPrinter)
	s.mux.HandleFunc("POST /api/printers/{id}/pause", s.handlePause)
	s.mux.HandleFunc("POST /api/printers/{id}/resume", s.handleResume)
	s.mux.HandleFunc("POST /api/printers/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/printers/{id}/skip", s.handleSkipObject)
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

// writeError is a helper to write a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// --- Web UI ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	printerCount := len(s.printers)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if printerCount == 0 {
		// Show onboarding welcome page
		renderTemplate(w, indexOnboardingTemplate, nil)
		return
	}

	// Show the dashboard
	renderTemplate(w, indexDashboardTemplate, map[string]any{
		"HasPrinters": true,
	})
}

// --- API Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleListPrinters(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	printerList := make([]printers.PrinterStatus, 0, len(s.printers))
	for _, p := range s.printers {
		printerList = append(printerList, p.Status())
	}
	s.mu.RUnlock()

	// Sort by name (case-insensitive) for consistent ordering
	sort.Slice(printerList, func(i, j int) bool {
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
	writeJSON(w, 200, p.Status())
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
