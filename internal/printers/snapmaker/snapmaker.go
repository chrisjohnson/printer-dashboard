// Package snapmaker provides a printer client for Snapmaker U1 printers
// running Paxx firmware. Communication uses REST API for commands and
// WebSocket (or REST polling) for real-time status updates.
package snapmaker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// Default intervals for the Connect lifecycle.
const (
	pollInterval    = 3 * time.Second  // REST status polling interval
	wsReconnectWait = 15 * time.Second // time between WebSocket reconnection attempts
	wsPingPeriod    = 30 * time.Second // WebSocket ping interval
	wsWriteWait     = 10 * time.Second // WebSocket write deadline
	wsReadWait      = 10 * time.Second // WebSocket read deadline
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

	// wsConn is the current WebSocket connection, or nil if not connected.
	wsConn *websocket.Conn
	wsMu   sync.Mutex
}

// wsMsg carries a parsed status report or an error from the WebSocket
// goroutine to the main Connect() loop.
type wsMsg struct {
	status *paxxStatus
	err    error
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

// Connect establishes a connection to the printer and begins monitoring
// its status. It blocks until the context is cancelled.
//
// Lifecycle:
//  1. Fetch initial status from GET /api/printer
//  2. Dial WebSocket ws://host:port/ws for real-time updates
//  3. On WS failure, fall back to REST polling at pollInterval
//  4. Periodically attempt to re-establish the WebSocket
func (p *Printer) Connect(ctx context.Context) error {
	// 1. Initial status snapshot.
	if status, err := p.fetchStatus(ctx); err != nil {
		p.setStatus(printers.PrinterStatus{
			ID:   p.cfg.ID,
			Name: p.cfg.Name,
			Type: "snapmaker",
			State: "error",
			ErrorMsg: fmt.Sprintf("initial status: %v", err),
		})
	} else {
		p.handleStatusReport(status)
	}

	// Channels for coordinating WS and polling.
	wsCh := make(chan wsMsg, 4)

	// Start the WebSocket connection in the background.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wsWG sync.WaitGroup
	wsWG.Add(1)
	go func() {
		defer wsWG.Done()
		p.wsConnect(ctx, wsCh)
	}()

	// REST polling timer — only active when WS is down.
	pollTicker := time.NewTicker(pollInterval)
	pollTicker.Stop()
	defer pollTicker.Stop()

	// WS reconnection timer — fires periodically to retry WebSocket.
	wsRetryTicker := time.NewTicker(wsReconnectWait)
	wsRetryTicker.Stop()
	defer wsRetryTicker.Stop()

	// Main event loop.
	for {
		select {
		case <-ctx.Done():
			// Wait for the WS goroutine to finish, then return.
			wsWG.Wait()
			return ctx.Err()

		case msg := <-wsCh:
			if msg.err != nil {
				// WS connection failed or disconnected.
				// Start REST polling + WS retry timers.
				pollTicker.Reset(pollInterval)
				wsRetryTicker.Reset(wsReconnectWait)
				continue
			}
			if msg.status != nil {
				// Successful WS status update.
				p.handleStatusReport(msg.status)
			}

		case <-pollTicker.C:
			// REST polling tick — fetch status via HTTP.
			status, err := p.fetchStatus(ctx)
			if err != nil {
				// Printer may be unreachable — mark offline.
				p.setStatus(printers.PrinterStatus{
					ID:       p.cfg.ID,
					Name:     p.cfg.Name,
					Type:     "snapmaker",
					Online:   false,
					State:    "error",
					ErrorMsg: fmt.Sprintf("status poll failed: %v", err),
				})
				continue
			}
			p.handleStatusReport(status)

		case <-wsRetryTicker.C:
			// Attempt to re-establish the WebSocket.
			wsWG.Add(1)
			go func() {
				defer wsWG.Done()
				p.wsConnect(ctx, wsCh)
			}()
		}
	}
}

// wsConnect dials the printer's WebSocket and reads status messages until
// the connection fails or the context is cancelled. Each received status
// report is sent to wsCh. On exit, wsCh receives an error message to
// trigger the fallback mechanism.
func (p *Printer) wsConnect(ctx context.Context, wsCh chan<- wsMsg) {
	wsURL := p.wsURL()
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		// Signal failure so the main loop starts fallback.
		select {
		case wsCh <- wsMsg{err: fmt.Errorf("ws dial: %w", err)}:
		case <-ctx.Done():
		}
		return
	}

	// Store the connection so CameraURLs and other methods can check it.
	p.wsMu.Lock()
	p.wsConn = conn
	p.wsMu.Unlock()

	defer func() {
		p.wsMu.Lock()
		p.wsConn = nil
		p.wsMu.Unlock()
		conn.Close()
	}()

	// Set read deadline for detecting stale connections.
	if err := conn.SetReadDeadline(time.Now().Add(wsReadWait)); err != nil {
		select {
		case wsCh <- wsMsg{err: fmt.Errorf("ws set read deadline: %w", err)}:
		case <-ctx.Done():
		}
		return
	}

	// Configure pong handler to extend read deadline.
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadWait))
	})

	// Start a goroutine to send pings periodically.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(wsPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Signal that the WS connection was established successfully.
	select {
	case wsCh <- wsMsg{}:
	case <-ctx.Done():
		return
	}

	// Read loop.
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			// Connection lost — signal fallback.
			select {
			case wsCh <- wsMsg{err: fmt.Errorf("ws read: %w", err)}:
			case <-ctx.Done():
			}
			return
		}

		status, err := parseReport(message)
		if err != nil {
			// Malformed message — log and skip (keep connection alive).
			continue
		}

		// Reset read deadline after a successful read.
		if err := conn.SetReadDeadline(time.Now().Add(wsReadWait)); err != nil {
			select {
			case wsCh <- wsMsg{err: fmt.Errorf("ws read deadline: %w", err)}:
			case <-ctx.Done():
			}
			return
		}

		select {
		case wsCh <- wsMsg{status: status}:
		case <-ctx.Done():
			return
		}
	}
}

// wsURL returns the WebSocket URL for this printer.
func (p *Printer) wsURL() string {
	base := p.baseURL()
	// Convert http:// → ws://
	if len(base) > 4 && base[:4] == "http" {
		return "ws" + base[4:] + "/ws"
	}
	return base + "/ws"
}

// fetchStatus performs a GET /api/printer to retrieve the current printer
// status and returns the parsed report.
func (p *Printer) fetchStatus(ctx context.Context) (*paxxStatus, error) {
	req, err := p.buildRequest(http.MethodGet, "/api/printer", nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	status, err := parseReport(body)
	if err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return status, nil
}

// handleStatusReport merges a paxxStatus report into the cached
// PrinterStatus, preserving previous values for fields that are absent
// from the report (nil pointer fields).
func (p *Printer) handleStatusReport(s *paxxStatus) {
	current := p.Status()

	current.Online = true
	current.State = mapState(s.Status)

	// Pointer fields — only update when present in the report.
	if s.Progress != nil {
		current.Progress = *s.Progress
	}
	if s.File != nil {
		current.CurrentFile = *s.File
	}

	// Error state: only update ErrorMsg when the report includes an error.
	if s.Status == "error" || s.Status == "failed" {
		current.State = "error"
		if s.Error != nil {
			current.ErrorMsg = *s.Error
		}
	}

	if s.BedTemp != nil {
		current.BedTemp = *s.BedTemp
	}
	if s.BedTarget != nil {
		current.BedTargetTemp = *s.BedTarget
	}
	if s.NozzleTemp != nil {
		current.NozzleTemp = *s.NozzleTemp
	}
	if s.NozzleTarget != nil {
		current.NozzleTargetTemp = *s.NozzleTarget
	}
	if s.ChamberTemp != nil {
		current.ChamberTemp = *s.ChamberTemp
	}
	if s.PrintDuration != nil {
		// Not directly mapped to PrinterStatus — could use for eta calculation later.
	}
	if s.RemainingTime != nil {
		current.RemainingTime = *s.RemainingTime
	}
	if s.CurrentLayer != nil {
		current.CurrentLayer = *s.CurrentLayer
	}
	if s.TotalLayers != nil {
		current.TotalLayers = *s.TotalLayers
	}

	p.setStatus(current)
}

// marshalStatus converts the current PrinterStatus to JSON bytes for
// WebSocket broadcast. Used by the server in connectAllPrinters.
func (p *Printer) marshalStatus() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return json.Marshal(p.status)
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
