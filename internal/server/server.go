package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
	"github.com/chrisjohnson/printer-dashboard/internal/printers/bambu"
)

// Server holds the HTTP server, printer registry, and dependencies.
type Server struct {
	cfg      *config.Config
	http     *http.Server
	mux      *http.ServeMux
	printers map[string]printers.Printer
	mu       sync.RWMutex
}

// New creates a new Server from the provided config.
func New(cfg *config.Config) (*Server, error) {
	mux := http.NewServeMux()

	s := &Server{
		cfg:      cfg,
		mux:      mux,
		printers: make(map[string]printers.Printer),
		http: &http.Server{
			Addr:    cfg.Listen,
			Handler: mux,
		},
	}

	// Authenticate with Bambu Cloud if we have Bambu printers
	var bambuCloud *bambu.BambuCloudClient
	if cfg.BambuAccount != nil {
		bambuCloud = bambu.NewBambuCloudClient(cfg.BambuAccount.Region)
		if cfg.BambuAccount.Token != "" {
			if err := bambuCloud.LoginWithToken(cfg.BambuAccount.Token); err != nil {
				log.Printf("WARNING: Bambu cloud token login failed: %v", err)
			}
		} else if cfg.BambuAccount.Email != "" && cfg.BambuAccount.Password != "" {
			// Attempt login — if 2FA is required, we'll log a warning and the user
			// can provide a pre-obtained token instead.
			if err := bambuCloud.Login(cfg.BambuAccount.Email, cfg.BambuAccount.Password, nil); err != nil {
				log.Printf("WARNING: Bambu cloud login failed (2FA may be required): %v", err)
				log.Printf("  To fix: obtain a token manually and set it in config as 'bambu_account.token'")
				log.Printf("  See PLAN.md or run external tools to get a token.")
			}
		}
	}

	// Initialize printer clients from config
	for _, pdef := range cfg.Printers {
		var p printers.Printer
		switch pdef.Type {
		case "bambu":
			if bambuCloud == nil || bambuCloud.Token() == "" {
				log.Printf("WARNING: printer %q requires Bambu cloud auth, skipping", pdef.Name)
				continue
			}
			p = bambu.New(pdef, bambuCloud)
		// case "snapmaker":
		// 	p = snapmaker.New(pdef)
		default:
			log.Printf("WARNING: printer %q has unsupported type %q — skipping (add support in internal/printers/)", pdef.Name, pdef.Type)
			continue
		}
		s.printers[pdef.ID] = p
		log.Printf("Registered printer: %s (%s)", pdef.Name, pdef.Type)
	}

	s.registerRoutes()
	return s, nil
}

// Start begins the HTTP server and printer connections, blocks until ctx cancels.
func (s *Server) Start(ctx context.Context) error {
	// Start printer connection loops in background
	printerCtx, cancelPrinters := context.WithCancel(context.Background())
	defer cancelPrinters()

	for _, p := range s.printers {
		go func(printer printers.Printer) {
			log.Printf("Connecting to printer: %s", printer.Name())
			if err := printer.Connect(printerCtx); err != nil {
				log.Printf("Printer %s disconnected with error: %v", printer.Name(), err)
			}
		}(p)
	}

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

func (s *Server) registerRoutes() {
	// Web UI
	s.mux.HandleFunc("GET /", s.handleIndex)

	// API
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/printers", s.handleListPrinters)
	s.mux.HandleFunc("GET /api/printers/{id}", s.handleGetPrinter)
	s.mux.HandleFunc("POST /api/printers/{id}/pause", s.handlePause)
	s.mux.HandleFunc("POST /api/printers/{id}/resume", s.handleResume)
	s.mux.HandleFunc("POST /api/printers/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/printers/{id}/skip", s.handleSkipObject)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Printer Dashboard</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #111; color: #eee; padding: 20px; }
    h1 { font-size: 1.5rem; margin-bottom: 1rem; color: #fff; }
    .printers { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 16px; }
    .card { background: #1e1e1e; border: 1px solid #333; border-radius: 12px; padding: 16px; }
    .card h2 { font-size: 1.1rem; margin-bottom: 8px; }
    .status { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 0.8rem; }
    .status.printing { background: #2d7d46; }
    .status.paused { background: #b8860b; }
    .status.idle { background: #555; }
    .status.error { background: #c0392b; }
    .status.unknown { background: #666; }
    .progress-bar { background: #333; height: 8px; border-radius: 4px; margin: 8px 0; overflow: hidden; }
    .progress-bar .fill { background: #2d7d46; height: 100%; transition: width 0.5s; }
    .temps { font-size: 0.85rem; color: #aaa; margin-top: 4px; }
    .controls { margin-top: 12px; display: flex; gap: 8px; flex-wrap: wrap; }
    .controls button { background: #333; color: #fff; border: 1px solid #555; padding: 6px 12px; border-radius: 6px; cursor: pointer; font-size: 0.8rem; }
    .controls button:hover { background: #444; }
    .controls button:disabled { opacity: 0.4; cursor: not-allowed; }
    #printer-list { margin-top: 16px; }
  </style>
</head>
<body>
  <h1>🖨 Printer Dashboard</h1>
  <div class="printers" id="printer-list">
    <p>Loading printers...</p>
  </div>
  <script>
    async function loadPrinters() {
      try {
        const res = await fetch('/api/printers');
        const data = await res.json();
        const container = document.getElementById('printer-list');
        if (!data.printers || data.printers.length === 0) {
          container.innerHTML = '<p>No printers configured.</p>';
          return;
        }
        container.innerHTML = data.printers.map(p => renderCard(p)).join('');
      } catch (e) {
        document.getElementById('printer-list').innerHTML = '<p>Error loading printers.</p>';
      }
    }

    function renderCard(p) {
      const stateClass = p.state || 'unknown';
      const progress = (p.progress * 100).toFixed(1);
      const timeStr = p.remaining_time > 0 ? formatTime(p.remaining_time) : '';
      return '<div class="card" id="printer-' + p.id + '">' +
        '<h2>' + escapeHtml(p.name) + ' <span class="status ' + stateClass + '">' + stateClass + '</span></h2>' +
        '<div class="progress-bar"><div class="fill" style="width:' + progress + '%"></div></div>' +
        '<div><strong>' + progress + '%</strong> ' + timeStr + '</div>' +
        (p.current_file ? '<div style="font-size:0.8rem;color:#888;margin-top:4px">' + escapeHtml(p.current_file) + '</div>' : '') +
        '<div class="temps">🔥 Bed: ' + p.bed_temp.toFixed(1) + '°C / ' + p.bed_target_temp.toFixed(1) + '°C &nbsp;|&nbsp; 🌡 Nozzle: ' + p.nozzle_temp.toFixed(1) + '°C / ' + p.nozzle_target_temp.toFixed(1) + '°C</div>' +
        '<div class="controls">' +
        '<button onclick="sendCmd(\'' + p.id + '\',\'pause\')" ' + (p.state !== 'printing' ? 'disabled' : '') + '>⏸ Pause</button>' +
        '<button onclick="sendCmd(\'' + p.id + '\',\'resume\')" ' + (p.state !== 'paused' ? 'disabled' : '') + '>▶ Resume</button>' +
        '<button onclick="sendCmd(\'' + p.id + '\',\'cancel\')" ' + (p.state !== 'printing' && p.state !== 'paused' ? 'disabled' : '') + '>⏹ Cancel</button>' +
        '<button onclick="sendCmd(\'' + p.id + '\',\'skip\')" ' + (p.state !== 'printing' ? 'disabled' : '') + '>⏭ Skip</button>' +
        '</div>' +
        '</div>';
    }

    function formatTime(sec) {
      const h = Math.floor(sec / 3600);
      const m = Math.floor((sec % 3600) / 60);
      return '⏱ ' + h + 'h ' + m + 'm left';
    }

    function escapeHtml(s) {
      if (!s) return '';
      return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    }

    async function sendCmd(id, cmd) {
      try {
        const res = await fetch('/api/printers/' + id + '/' + cmd, { method: 'POST' });
        const data = await res.json();
        if (!res.ok) alert('Error: ' + (data.error || res.statusText));
      } catch (e) {
        alert('Network error: ' + e.message);
      }
    }

    loadPrinters();
    setInterval(loadPrinters, 5000); // Poll every 5 seconds for now
  </script>
</body>
</html>`)
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
