// Package snapmaker provides a printer client for Snapmaker U1 printers
// running Paxx firmware. Communication uses REST API for commands and
// WebSocket (or REST polling) for real-time status updates.
//
// Architecture:
//   - Control: HTTP POST to the printer's REST API (pause, resume, cancel)
//   - Status: WebSocket push with REST polling fallback
//   - Commands: Maps to POST /api/print/{action} (pause/resume/cancel)
//     with X-Access-Code header for authentication.
//
// This is Session B of the implementation — the Connect() method is a stub
// that blocks until the context is cancelled. The full WebSocket + REST
// polling lifecycle will be added in Sessions C-D.
package snapmaker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// Printer implements the printers.Printer interface for Snapmaker U1 printers
// running Paxx firmware.
type Printer struct {
	cfg config.PrinterDef
	mu  sync.RWMutex

	status printers.PrinterStatus

	// httpClient is used for all REST API calls to the printer.
	httpClient *http.Client

	// testBaseURL overrides baseURL() when set (for unit tests only).
	testBaseURL string

	// StatusCh sends the full printer status after each status update.
	// Configured by the server in initPrinters (same pattern as Bambu).
	// The send is non-blocking — slow consumers drop updates.
	StatusCh chan printers.PrinterStatus
}

// New creates a new Snapmaker printer client from the given config.
func New(cfg config.PrinterDef) *Printer {
	return &Printer{
		cfg: cfg,
		status: printers.PrinterStatus{
			ID:   cfg.ID,
			Name: cfg.Name,
			Type: "snapmaker",
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// buildCameraURLs derives the camera stream URL from the printer config.
// Snapmaker U1 with Paxx typically streams at http://{host}:{port}/camera.
func buildCameraURLs(cfg config.PrinterDef) []string {
	if cfg.Host == "" {
		return nil
	}
	port := cfg.Port
	if port == 0 {
		port = 8080
	}
	return []string{fmt.Sprintf("http://%s:%d/camera", cfg.Host, port)}
}

// ID returns the printer's unique identifier.
func (p *Printer) ID() string { return p.cfg.ID }

// Name returns the printer's human-readable name.
func (p *Printer) Name() string { return p.cfg.Name }

// Connect blocks until the context is cancelled.
//
// TODO (Session C-D): Replace with full lifecycle:
//  1. GET /api/printer → initial status snapshot
//  2. Dial WebSocket ws://host:port/ws → real-time push
//  3. On WS error → REST polling fallback at 3s intervals
//  4. Every 15s → attempt WS re-dial
func (p *Printer) Connect(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Status returns the current cached printer status under a read lock.
func (p *Printer) Status() printers.PrinterStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// setStatus updates the cached status under a write lock and sends the
// updated status on StatusCh if configured. The send is non-blocking to
// avoid slowing down status processing.
func (p *Printer) setStatus(s printers.PrinterStatus) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()

	if p.StatusCh != nil {
		select {
		case p.StatusCh <- s:
		default:
			// Channel full, drop update (reader is slow)
		}
	}
}

// baseURL returns the HTTP base URL for the printer's REST API.
func (p *Printer) baseURL() string {
	if p.testBaseURL != "" {
		return p.testBaseURL
	}
	port := p.cfg.Port
	if port == 0 {
		port = 8080
	}
	return fmt.Sprintf("http://%s:%d", p.cfg.Host, port)
}

// buildRequest creates an HTTP request to the printer's REST API, injecting
// the access code as both a query parameter and a header.
func (p *Printer) buildRequest(method, path string, body io.Reader) (*http.Request, error) {
	url := p.baseURL() + path
	if p.cfg.AccessCode != "" {
		url += "?access_code=" + p.cfg.AccessCode
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if p.cfg.AccessCode != "" {
		req.Header.Set("X-Access-Code", p.cfg.AccessCode)
	}
	return req, nil
}

// doCommand sends a REST command to the printer and returns an error if the
// response status is not 2xx.
func (p *Printer) doCommand(ctx context.Context, action string) error {
	req, err := p.buildRequest(http.MethodPost, "/api/print/"+action, nil)
	if err != nil {
		return fmt.Errorf("snapmaker %s: building %s request: %w", p.cfg.ID, action, err)
	}
	req = req.WithContext(ctx)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("snapmaker %s: %s request failed: %w", p.cfg.ID, action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("snapmaker %s: %s returned HTTP %d: %s", p.cfg.ID, action, resp.StatusCode, string(body))
	}
	return nil
}

// Pause sends a pause command to the printer.
func (p *Printer) Pause(ctx context.Context) error {
	return p.doCommand(ctx, "pause")
}

// Resume sends a resume command to the printer.
func (p *Printer) Resume(ctx context.Context) error {
	return p.doCommand(ctx, "resume")
}

// Cancel sends a cancel command to the printer.
func (p *Printer) Cancel(ctx context.Context) error {
	return p.doCommand(ctx, "cancel")
}

// SkipObject sends a G-code command to skip the current object.
// Uses POST /api/printer/command with the skip G-code.
func (p *Printer) SkipObject(ctx context.Context) error {
	req, err := p.buildRequest(http.MethodPost, "/api/printer/command", nil)
	if err != nil {
		return fmt.Errorf("snapmaker %s: building skip request: %w", p.cfg.ID, err)
	}
	req = req.WithContext(ctx)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("snapmaker %s: skip request failed: %w", p.cfg.ID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("snapmaker %s: skip returned HTTP %d: %s", p.cfg.ID, resp.StatusCode, string(body))
	}
	return nil
}

// CameraURLs returns the camera stream URL for this printer.
func (p *Printer) CameraURLs() []string {
	return buildCameraURLs(p.cfg)
}
