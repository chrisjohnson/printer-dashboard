package snapmaker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// ---------------------------------------------------------------------------
// Constructor and initial status tests
// ---------------------------------------------------------------------------

func TestNewPrinter(t *testing.T) {
	cfg := config.PrinterDef{
		ID:   "workshop-u1",
		Name: "Workshop U1",
		Type: "snapmaker",
		Host: "192.168.1.100",
		Port: 8080,
	}

	p := New(cfg)

	if p.ID() != "workshop-u1" {
		t.Errorf("ID() = %q; want %q", p.ID(), "workshop-u1")
	}
	if p.Name() != "Workshop U1" {
		t.Errorf("Name() = %q; want %q", p.Name(), "Workshop U1")
	}

	s := p.Status()
	if s.ID != "workshop-u1" {
		t.Errorf("Status().ID = %q; want %q", s.ID, "workshop-u1")
	}
	if s.Name != "Workshop U1" {
		t.Errorf("Status().Name = %q; want %q", s.Name, "Workshop U1")
	}
	if s.Type != "snapmaker" {
		t.Errorf("Status().Type = %q; want %q", s.Type, "snapmaker")
	}
	if s.Online {
		t.Error("Status().Online = true; want false (initial state)")
	}
	if s.State != "" {
		t.Errorf("Status().State = %q; want %q (initial state)", s.State, "")
	}
}

// ---------------------------------------------------------------------------
// Camera URL tests
// ---------------------------------------------------------------------------

func TestCameraURLs(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.PrinterDef
		want []string
	}{
		{
			name: "with host and port",
			cfg: config.PrinterDef{
				Host: "192.168.1.100",
				Port: 8080,
			},
			want: []string{"http://192.168.1.100:8080/camera"},
		},
		{
			name: "with host only (default port 80)",
			cfg: config.PrinterDef{
				Host: "10.0.0.50",
				Port: 0,
			},
			want: []string{"http://10.0.0.50:80/camera"},
		},
		{
			name: "no host",
			cfg: config.PrinterDef{
				Host: "",
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.cfg)
			got := p.CameraURLs()

			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("CameraURLs = %v; want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("CameraURLs[%d] = %q; want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// setStatus / StatusCh tests
// ---------------------------------------------------------------------------

func TestSetStatus_UpdatesStatus(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	p.setStatus(printers.PrinterStatus{
		ID:     "test",
		Name:   "Test",
		Online: true,
		State:  "printing",
	})

	s := p.Status()
	if !s.Online {
		t.Error("Online = false; want true after setStatus")
	}
	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
}

func TestSetStatus_StatusChNonBlocking(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	// Unbuffered channel — send is non-blocking, so no reader needed.
	ch := make(chan printers.PrinterStatus)
	p.StatusCh = ch

	// These must not block — the default case drops the update.
	p.setStatus(printers.PrinterStatus{ID: "test", Name: "Test", Online: true})
	p.setStatus(printers.PrinterStatus{ID: "test", Name: "Test", Online: false})

	// Status should still reflect the last call to setStatus.
	s := p.Status()
	if s.Online {
		t.Error("Online = true; want false (last setStatus set it false)")
	}
}

func TestSetStatus_StatusChDelivers(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	ch := make(chan printers.PrinterStatus, 1)
	p.StatusCh = ch

	want := printers.PrinterStatus{ID: "test", Name: "Test", State: "idle"}
	p.setStatus(want)

	got := <-ch
	if got.State != want.State {
		t.Errorf("StatusCh received State = %q; want %q", got.State, want.State)
	}
}

func TestSetStatus_StatusChNil(t *testing.T) {
	// When StatusCh is nil, setStatus must not panic.
	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.StatusCh = nil

	p.setStatus(printers.PrinterStatus{ID: "test", Name: "Test", Online: true})
	// If we get here without a panic, the test passes.
}

// ---------------------------------------------------------------------------
// REST command tests (with mock HTTP server)
// ---------------------------------------------------------------------------

// mockPaxxServer creates an httptest.Server that simulates a Snapmaker U1
// running Paxx firmware. It returns the server and a capture function that
// returns the last received HTTP request (safe to call after the server has
// handled a request).
func mockPaxxServer(t *testing.T, expectedPath string, statusCode int, responseBody string) (*httptest.Server, func() *http.Request) {
	t.Helper()

	var mu sync.Mutex
	var captured *http.Request

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r
		mu.Unlock()

		if r.URL.Path != expectedPath {
			t.Errorf("request path = %q; want %q", r.URL.Path, expectedPath)
		}
		if r.Method != http.MethodPost {
			t.Errorf("request method = %q; want %q", r.Method, http.MethodPost)
		}
		w.WriteHeader(statusCode)
		if responseBody != "" {
			fmt.Fprint(w, responseBody)
		}
	}))

	captureFn := func() *http.Request {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
	return ts, captureFn
}

func TestPause_SendsCorrectRequest(t *testing.T) {
	ts, _ := mockPaxxServer(t, "/api/print/pause", 200, "")
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test-u1", Name: "Test U1"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	err := p.Pause(context.Background())
	if err != nil {
		t.Errorf("Pause() returned error: %v", err)
	}
}

func TestResume_SendsCorrectRequest(t *testing.T) {
	ts, _ := mockPaxxServer(t, "/api/print/resume", 200, "")
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test-u1", Name: "Test U1"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	err := p.Resume(context.Background())
	if err != nil {
		t.Errorf("Resume() returned error: %v", err)
	}
}

func TestCancel_SendsCorrectRequest(t *testing.T) {
	ts, _ := mockPaxxServer(t, "/api/print/cancel", 200, "")
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test-u1", Name: "Test U1"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	err := p.Cancel(context.Background())
	if err != nil {
		t.Errorf("Cancel() returned error: %v", err)
	}
}

func TestSkipObject_SendsCorrectRequest(t *testing.T) {
	ts, _ := mockPaxxServer(t, "/api/printer/command", 200, "")
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test-u1", Name: "Test U1"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	err := p.SkipObject(context.Background())
	if err != nil {
		t.Errorf("SkipObject() returned error: %v", err)
	}
}

func TestCommand_SendsAccessCode(t *testing.T) {
	ts, captureReq := mockPaxxServer(t, "/api/print/pause", 200, "")
	defer ts.Close()

	p := New(config.PrinterDef{
		ID:         "test-u1",
		Name:       "Test U1",
		AccessCode: "my-secret-code",
	})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	err := p.Pause(context.Background())
	if err != nil {
		t.Errorf("Pause() returned error: %v", err)
	}

	req := captureReq()
	if req == nil {
		t.Fatal("no request was captured")
	}

	// Check that the access code was sent as a header.
	if req.Header.Get("X-Access-Code") != "my-secret-code" {
		t.Errorf("X-Access-Code header = %q; want %q",
			req.Header.Get("X-Access-Code"), "my-secret-code")
	}

	// Check that the access code was sent as a query parameter.
	if got := req.URL.Query().Get("access_code"); got != "my-secret-code" {
		t.Errorf("access_code query param = %q; want %q", got, "my-secret-code")
	}
}

func TestCommand_HTTPError(t *testing.T) {
	ts, _ := mockPaxxServer(t, "/api/print/pause", 500, "Internal Server Error")
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test-u1", Name: "Test U1"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	err := p.Pause(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if err.Error() != "snapmaker test-u1: pause returned HTTP 500: Internal Server Error" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCommand_Unauthorized(t *testing.T) {
	ts, _ := mockPaxxServer(t, "/api/print/pause", 401, "Unauthorized")
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test-u1", Name: "Test U1"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	err := p.Pause(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 401, got nil")
	}
	if err.Error() != "snapmaker test-u1: pause returned HTTP 401: Unauthorized" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCommand_Unreachable(t *testing.T) {
	p := New(config.PrinterDef{
		ID:   "test-u1",
		Name: "Test U1",
		Host: "192.0.2.1", // TEST-NET, guaranteed unreachable
		Port: 1,
	})
	// Use the default HTTP client with a short timeout.
	p.httpClient = &http.Client{}

	// Shorten context timeout so the test doesn't hang.
	ctx, cancel := context.WithTimeout(context.Background(), 10)
	defer cancel()

	err := p.Pause(ctx)
	if err == nil {
		t.Fatal("expected error for unreachable host, got nil")
	}
}

// ---------------------------------------------------------------------------
// Connect stub tests
// ---------------------------------------------------------------------------

func TestConnect_BlocksUntilCancelled(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Connect(ctx)
	}()

	// Cancel the context and wait for Connect to return.
	cancel()
	err := <-errCh

	if err != context.Canceled {
		t.Errorf("Connect() returned %v; want %v", err, context.Canceled)
	}
}

// ---------------------------------------------------------------------------
// handleStatusReport tests
// ---------------------------------------------------------------------------

func TestHandleStatusReport_FullUpdate(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	report := &paxxStatus{
		Status:        "running",
		Progress:      float64Ptr(0.5),
		File:          stringPtr("model.gcode"),
		BedTemp:       float64Ptr(55.0),
		BedTarget:     float64Ptr(60.0),
		NozzleTemp:    float64Ptr(210.0),
		NozzleTarget:  float64Ptr(220.0),
		ChamberTemp:   float64Ptr(30.0),
		RemainingTime: intPtr(1800),
		CurrentLayer:  intPtr(5),
		TotalLayers:   intPtr(100),
	}

	p.handleStatusReport(report)
	s := p.Status()

	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
	if s.Progress != 0.5 {
		t.Errorf("Progress = %f; want 0.5", s.Progress)
	}
	if s.CurrentFile != "model.gcode" {
		t.Errorf("CurrentFile = %q; want %q", s.CurrentFile, "model.gcode")
	}
	if s.BedTemp != 55.0 {
		t.Errorf("BedTemp = %f; want 55.0", s.BedTemp)
	}
	if s.NozzleTemp != 210.0 {
		t.Errorf("NozzleTemp = %f; want 210.0", s.NozzleTemp)
	}
	if s.BedTargetTemp != 60.0 {
		t.Errorf("BedTargetTemp = %f; want 60.0", s.BedTargetTemp)
	}
	if s.NozzleTargetTemp != 220.0 {
		t.Errorf("NozzleTargetTemp = %f; want 220.0", s.NozzleTargetTemp)
	}
	if s.ChamberTemp != 30.0 {
		t.Errorf("ChamberTemp = %f; want 30.0", s.ChamberTemp)
	}
	if s.RemainingTime != 1800 {
		t.Errorf("RemainingTime = %d; want 1800", s.RemainingTime)
	}
	if s.CurrentLayer != 5 {
		t.Errorf("CurrentLayer = %d; want 5", s.CurrentLayer)
	}
	if s.TotalLayers != 100 {
		t.Errorf("TotalLayers = %d; want 100", s.TotalLayers)
	}
	if !s.Online {
		t.Error("Online = false; want true")
	}
}

func TestHandleStatusReport_PartialUpdate(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	// First: full update.
	full := &paxxStatus{
		Status:       "running",
		Progress:     float64Ptr(0.5),
		File:         stringPtr("model.gcode"),
		BedTemp:      float64Ptr(55.0),
		NozzleTemp:   float64Ptr(210.0),
		CurrentLayer: intPtr(5),
		TotalLayers:  intPtr(100),
	}
	p.handleStatusReport(full)

	// Second: partial update — only state and progress change.
	partial := &paxxStatus{
		Status:   "running",
		Progress: float64Ptr(0.75),
	}
	p.handleStatusReport(partial)
	s := p.Status()

	// Fields from the partial update.
	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
	if s.Progress != 0.75 {
		t.Errorf("Progress = %f; want 0.75", s.Progress)
	}

	// Fields from the first update should be preserved.
	if s.CurrentFile != "model.gcode" {
		t.Errorf("CurrentFile = %q; want %q (preserved)", s.CurrentFile, "model.gcode")
	}
	if s.BedTemp != 55.0 {
		t.Errorf("BedTemp = %f; want 55.0 (preserved)", s.BedTemp)
	}
	if s.NozzleTemp != 210.0 {
		t.Errorf("NozzleTemp = %f; want 210.0 (preserved)", s.NozzleTemp)
	}
	if s.CurrentLayer != 5 {
		t.Errorf("CurrentLayer = %d; want 5 (preserved)", s.CurrentLayer)
	}
	if s.TotalLayers != 100 {
		t.Errorf("TotalLayers = %d; want 100 (preserved)", s.TotalLayers)
	}
}

func TestHandleStatusReport_ErrorState(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	report := &paxxStatus{
		Status: "error",
		Error:  stringPtr("Heater timeout"),
	}
	p.handleStatusReport(report)
	s := p.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q", s.State, "error")
	}
	if s.ErrorMsg != "Heater timeout" {
		t.Errorf("ErrorMsg = %q; want %q", s.ErrorMsg, "Heater timeout")
	}
}

// ---------------------------------------------------------------------------
// fetchStatus tests
// ---------------------------------------------------------------------------

func TestFetchStatus_Success(t *testing.T) {
	// Mock REST server that returns a printer status.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/printer" {
			t.Errorf("path = %q; want %q", r.URL.Path, "/api/printer")
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"idle","progress":0}`)
	}))
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	status, err := p.fetchStatus(context.Background())
	if err != nil {
		t.Fatalf("fetchStatus() returned error: %v", err)
	}
	if status.Status != "idle" {
		t.Errorf("Status = %q; want %q", status.Status, "idle")
	}
}

func TestFetchStatus_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "printer error")
	}))
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	_, err := p.fetchStatus(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// ---------------------------------------------------------------------------
// Connect lifecycle tests (with mock REST + WebSocket server)
// ---------------------------------------------------------------------------

// mockPaxx represents a mock Snapmaker U1 that serves REST and WebSocket.
type mockPaxx struct {
	Server *httptest.Server

	connMu   sync.Mutex
	wsConn   *websocket.Conn  // client-side WS conn (snapmaker → mock)
	srvConn  *websocket.Conn  // server-side WS conn (mock handler side)
	ready    chan struct{}    // closed once wsConn is set
}

// mockSnapmakerServer creates an httptest.Server that acts as a Snapmaker
// U1 with Paxx firmware. It serves:
//   - GET /api/printer → returns the current status JSON
//   - WebSocket /ws → pushes status JSON messages
func mockSnapmakerServer(t *testing.T) *mockPaxx {
	t.Helper()

	m := &mockPaxx{
		ready: make(chan struct{}),
	}

	var upgrader = websocket.Upgrader{}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/printer", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"status":   "idle",
			"progress": 0,
		})
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("ws upgrade: %v", err)
			return
		}
		m.connMu.Lock()
		m.srvConn = conn
		m.connMu.Unlock()
		close(m.ready)

		// Block until the connection drops.
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	})

	m.Server = httptest.NewServer(mux)
	return m
}

// waitConn blocks until the mock server has accepted a WebSocket connection
// or the context is cancelled.
func (m *mockPaxx) waitConn(ctx context.Context) error {
	select {
	case <-m.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sendWS sends a JSON status report over the established WebSocket.
// It must be called after waitConn has succeeded.
func (m *mockPaxx) sendWS(t *testing.T, status map[string]any) {
	t.Helper()

	m.connMu.Lock()
	conn := m.srvConn
	m.connMu.Unlock()

	if conn == nil {
		t.Fatal("sendWS: WebSocket not connected; call waitConn first")
	}

	if err := conn.WriteJSON(status); err != nil {
		t.Fatalf("writing WS message: %v", err)
	}
}

// Close shuts down the mock server. It closes the server-side WebSocket
// connection first to unblock the WS handler's read loop, then closes the
// HTTP server.
func (m *mockPaxx) Close() {
	m.connMu.Lock()
	if m.srvConn != nil {
		m.srvConn.Close()
	}
	m.connMu.Unlock()
	m.Server.Close()
}

// runConnect starts p.Connect(ctx) in a goroutine and returns the error
// channel. The caller must call stopConnect to shut it down.
func runConnect(p *Printer) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Connect(ctx)
	}()
	return cancel, errCh
}

// stopConnect cancels the context, closes the mock server so the WS read
// loop exits promptly, and waits for Connect to return.
//
// Close the mock server FIRST to drop the WS connection before cancelling.
// This lets wsConnect's ReadMessage error out immediately instead of
// waiting for the read deadline (wsReadWait = 10s).
func stopConnect(mock *mockPaxx, cancel context.CancelFunc, errCh <-chan error) error {
	mock.Close()
	cancel()
	return <-errCh
}

func TestConnect_FetchInitialStatus(t *testing.T) {
	mock := mockSnapmakerServer(t)
	defer mock.Close()

	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.testBaseURL = mock.Server.URL
	p.httpClient = mock.Server.Client()

	cancel, errCh := runConnect(p)

	// Wait for WebSocket connection (means initial fetch + WS dial succeeded).
	wsCtx, wsCancel := context.WithTimeout(context.Background(), time.Second)
	defer wsCancel()
	if err := mock.waitConn(wsCtx); err != nil {
		t.Fatalf("timed out waiting for WebSocket connection: %v", err)
	}

	s := p.Status()
	if s.State != "idle" {
		t.Errorf("initial State = %q; want %q", s.State, "idle")
	}
	if !s.Online {
		t.Error("Online = false after initial status fetch; want true")
	}

	// Stop Connect by cancelling and closing the mock server.
	if err := stopConnect(mock, cancel, errCh); err != context.Canceled {
		t.Errorf("Connect() returned %v; want %v", err, context.Canceled)
	}
}

func TestConnect_WebSocketMessage(t *testing.T) {
	mock := mockSnapmakerServer(t)
	defer mock.Close()

	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.testBaseURL = mock.Server.URL
	p.httpClient = mock.Server.Client()

	cancel, errCh := runConnect(p)

	wsCtx, wsCancel := context.WithTimeout(context.Background(), time.Second)
	defer wsCancel()
	if err := mock.waitConn(wsCtx); err != nil {
		t.Fatalf("timed out waiting for WebSocket connection: %v", err)
	}

	// Send a status update via WebSocket.
	mock.sendWS(t, map[string]any{
		"status":        "running",
		"progress":      0.5,
		"file":          "model.gcode",
		"bed_temp":      55.0,
		"nozzle_temp":   210.0,
		"current_layer": 5,
		"total_layers":  100,
	})

	// Give Connect time to process the message.
	time.Sleep(50 * time.Millisecond)

	s := p.Status()
	if s.State != "printing" {
		t.Errorf("after WS: State = %q; want %q", s.State, "printing")
	}
	if s.Progress != 0.5 {
		t.Errorf("after WS: Progress = %f; want 0.5", s.Progress)
	}
	if s.CurrentFile != "model.gcode" {
		t.Errorf("after WS: CurrentFile = %q; want %q", s.CurrentFile, "model.gcode")
	}
	if s.BedTemp != 55.0 {
		t.Errorf("after WS: BedTemp = %f; want 55.0", s.BedTemp)
	}
	if s.CurrentLayer != 5 {
		t.Errorf("after WS: CurrentLayer = %d; want 5", s.CurrentLayer)
	}
	if s.TotalLayers != 100 {
		t.Errorf("after WS: TotalLayers = %d; want 100", s.TotalLayers)
	}

	if err := stopConnect(mock, cancel, errCh); err != context.Canceled {
		t.Errorf("Connect() returned %v; want %v", err, context.Canceled)
	}
}

func TestConnect_WebSocketPartialUpdate(t *testing.T) {
	mock := mockSnapmakerServer(t)
	defer mock.Close()

	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.testBaseURL = mock.Server.URL
	p.httpClient = mock.Server.Client()

	cancel, errCh := runConnect(p)

	wsCtx, wsCancel := context.WithTimeout(context.Background(), time.Second)
	defer wsCancel()
	if err := mock.waitConn(wsCtx); err != nil {
		t.Fatalf("timed out waiting for WebSocket connection: %v", err)
	}

	// First message: full update.
	mock.sendWS(t, map[string]any{
		"status":        "running",
		"progress":      0.5,
		"file":          "model.gcode",
		"bed_temp":      55.0,
		"nozzle_temp":   210.0,
		"current_layer": 5,
		"total_layers":  100,
	})

	time.Sleep(50 * time.Millisecond)

	// Second message: partial update (only progress and layer change).
	mock.sendWS(t, map[string]any{
		"status":        "running",
		"progress":      0.75,
		"current_layer": 42,
	})

	time.Sleep(50 * time.Millisecond)

	s := p.Status()

	// Updated fields.
	if s.Progress != 0.75 {
		t.Errorf("after partial: Progress = %f; want 0.75", s.Progress)
	}
	if s.CurrentLayer != 42 {
		t.Errorf("after partial: CurrentLayer = %d; want 42", s.CurrentLayer)
	}

	// Preserved fields.
	if s.CurrentFile != "model.gcode" {
		t.Errorf("after partial: CurrentFile = %q; want %q (preserved)", s.CurrentFile, "model.gcode")
	}
	if s.BedTemp != 55.0 {
		t.Errorf("after partial: BedTemp = %f; want 55.0 (preserved)", s.BedTemp)
	}
	if s.TotalLayers != 100 {
		t.Errorf("after partial: TotalLayers = %d; want 100 (preserved)", s.TotalLayers)
	}

	if err := stopConnect(mock, cancel, errCh); err != context.Canceled {
		t.Errorf("Connect() returned %v; want %v", err, context.Canceled)
	}
}
