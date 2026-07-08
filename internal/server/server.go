package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	cfg    *config.Config
	http   *http.Server
	mux    *http.ServeMux
}

// New creates a new Server from the provided config.
func New(cfg *config.Config) (*Server, error) {
	mux := http.NewServeMux()

	s := &Server{
		cfg: cfg,
		mux: mux,
		http: &http.Server{
			Addr:    cfg.Listen,
			Handler: mux,
		},
	}

	s.registerRoutes()
	return s, nil
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/printers", s.handleListPrinters)
	s.mux.HandleFunc("GET /api/printers/{id}", s.handleGetPrinter)
	s.mux.HandleFunc("POST /api/printers/{id}/pause", s.handlePause)
	s.mux.HandleFunc("POST /api/printers/{id}/resume", s.handleResume)
	s.mux.HandleFunc("POST /api/printers/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/printers/{id}/skip", s.handleSkipObject)
}

// Start begins the HTTP server and blocks until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		log.Printf("HTTP server listening on %s", s.cfg.Listen)
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Println("Shutting down HTTP server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// --- Handlers (stubs) ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleListPrinters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"printers":[]}`))
}

func (s *Server) handleGetPrinter(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(fmt.Sprintf(`{"id":%q,"status":"unknown"}`, id)))
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":"not implemented"}`))
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":"not implemented"}`))
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":"not implemented"}`))
}

func (s *Server) handleSkipObject(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":"not implemented"}`))
}
