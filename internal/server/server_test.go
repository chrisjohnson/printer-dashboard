package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
	"github.com/chrisjohnson/printer-dashboard/internal/ws"
)

// ---------------------------------------------------------------------------
// MockPrinter — implements printers.Printer for testing
// ---------------------------------------------------------------------------

// MockPrinter is a test double that implements the printers.Printer interface.
type MockPrinter struct {
	printers.Printer // embed to satisfy interface at compile time

	id         string
	name       string
	stat       printers.PrinterStatus
	mu         sync.Mutex

	pauseErr   error
	resumeErr  error
	cancelErr  error
	skipErr    error
	connectErr error

	PauseCalled  bool
	ResumeCalled bool
	CancelCalled bool
	SkipCalled   bool
}

func (m *MockPrinter) ID() string { return m.id }

func (m *MockPrinter) Name() string { return m.name }

func (m *MockPrinter) Connect(_ context.Context) error { return m.connectErr }

func (m *MockPrinter) Status() printers.PrinterStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stat
}

func (m *MockPrinter) Pause(_ context.Context) error {
	m.PauseCalled = true
	return m.pauseErr
}

func (m *MockPrinter) Resume(_ context.Context) error {
	m.ResumeCalled = true
	return m.resumeErr
}

func (m *MockPrinter) Cancel(_ context.Context) error {
	m.CancelCalled = true
	return m.cancelErr
}

func (m *MockPrinter) SkipObject(_ context.Context) error {
	m.SkipCalled = true
	return m.skipErr
}

func (m *MockPrinter) CameraURLs() []string { return nil }

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestServer creates a minimal Server with the given printer map.
func newTestServer(printersMap map[string]printers.Printer) *Server {
	wsHub := ws.NewHub()
	go wsHub.Run()

	s := &Server{
		cfg:      &config.Config{Listen: ":0"},
		mux:      http.NewServeMux(),
		printers: printersMap,
		wsHub:    wsHub,
	}
	s.registerRoutes()
	return s
}

// mustGet is a helper that GETs a URL and returns the response.
func mustGet(t *testing.T, baseURL, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatalf("creating GET request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("executing GET %s: %v", path, err)
	}
	return resp
}

// mustPost is a helper that POSTs to a URL and returns the response.
func mustPost(t *testing.T, baseURL, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+path, nil)
	if err != nil {
		t.Fatalf("creating POST request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("executing POST %s: %v", path, err)
	}
	return resp
}

// decodeBody decodes the JSON response body into the given destination.
func decodeBody(t *testing.T, resp *http.Response, dest any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GET /api/health
// ---------------------------------------------------------------------------

func TestHandleHealth(t *testing.T) {
	s := newTestServer(nil)
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL, "/api/health")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var body map[string]string
	decodeBody(t, resp, &body)

	if body["status"] != "ok" {
		t.Errorf(`expected "status":"ok", got %q`, body["status"])
	}
}

// ---------------------------------------------------------------------------
// GET /api/printers
// ---------------------------------------------------------------------------

func TestHandleListPrinters(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body map[string]any
		decodeBody(t, resp, &body)

		printers, ok := body["printers"].([]any)
		if !ok {
			t.Fatalf("expected 'printers' key to be an array, got %T", body["printers"])
		}
		if len(printers) != 0 {
			t.Errorf("expected empty printers array, got %d elements", len(printers))
		}
	})

	t.Run("one printer", func(t *testing.T) {
		p := &MockPrinter{
			id:   "p1",
			name: "Test Printer",
			stat: printers.PrinterStatus{
				ID:   "p1",
				Name: "Test Printer",
				Type: "test",
			},
		}
		s := newTestServer(map[string]printers.Printer{"p1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body map[string]any
		decodeBody(t, resp, &body)

		list, ok := body["printers"].([]any)
		if !ok {
			t.Fatalf("'printers' is not an array: %T", body["printers"])
		}
		if len(list) != 1 {
			t.Fatalf("expected 1 printer, got %d", len(list))
		}

		prt := list[0].(map[string]any)
		if prt["id"] != "p1" {
			t.Errorf("expected id 'p1', got %v", prt["id"])
		}
		if prt["name"] != "Test Printer" {
			t.Errorf("expected name 'Test Printer', got %v", prt["name"])
		}
	})

	t.Run("sorted case-insensitive", func(t *testing.T) {
		pA := &MockPrinter{
			id:   "a",
			name: "A-printer",
			stat: printers.PrinterStatus{ID: "a", Name: "A-printer"},
		}
		pB := &MockPrinter{
			id:   "b",
			name: "b-printer",
			stat: printers.PrinterStatus{ID: "b", Name: "b-printer"},
		}
		pZ := &MockPrinter{
			id:   "z",
			name: "z-printer",
			stat: printers.PrinterStatus{ID: "z", Name: "z-printer"},
		}
		s := newTestServer(map[string]printers.Printer{
			"z": pZ,
			"a": pA,
			"b": pB,
		})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers")
		defer resp.Body.Close()

		var body map[string]any
		decodeBody(t, resp, &body)

		list := body["printers"].([]any)
		if len(list) != 3 {
			t.Fatalf("expected 3 printers, got %d", len(list))
		}

		// Verify case-insensitive sort: A-printer (A) < b-printer (b) < z-printer (z)
		names := make([]string, 3)
		for i, p := range list {
			names[i] = p.(map[string]any)["name"].(string)
		}

		expected := []string{"A-printer", "b-printer", "z-printer"}
		for i := range expected {
			if names[i] != expected[i] {
				t.Errorf("position %d: expected %q, got %q", i, expected[i], names[i])
			}
		}

		// Double-check the sort ourselves
		if !sort.SliceIsSorted(names, func(i, j int) bool {
			return strings.ToLower(names[i]) < strings.ToLower(names[j])
		}) {
			t.Errorf("printers are not sorted case-insensitively: %v", names)
		}
	})

	t.Run("status json format", func(t *testing.T) {
		stat := printers.PrinterStatus{
			ID:               "fmt-1",
			Name:             "Format Check",
			Type:             "bambu",
			Online:           true,
			State:            "printing",
			Progress:         0.45,
			RemainingTime:    3600,
			CurrentFile:      "benchy.gcode",
			BedTemp:          60.5,
			BedTargetTemp:    65.0,
			NozzleTemp:       220.0,
			NozzleTargetTemp: 220.0,
			ChamberTemp:      35.0,
			CurrentLayer:     5,
			TotalLayers:      100,
			ErrorMsg:         "",
		}
		p := &MockPrinter{
			id:   "fmt-1",
			name: "Format Check",
			stat: stat,
		}
		s := newTestServer(map[string]printers.Printer{"fmt-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers/fmt-1")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}

		var raw map[string]any
		decodeBody(t, resp, &raw)

		// Check required keys exist
		requiredKeys := []string{
			"id", "name", "type", "online", "state", "progress",
			"remaining_time", "current_file", "bed_temp", "bed_target_temp",
			"nozzle_temp", "nozzle_target_temp", "chamber_temp",
			"current_layer", "total_layers",
		}
		for _, key := range requiredKeys {
			if _, ok := raw[key]; !ok {
				t.Errorf("required JSON key %q is missing", key)
			}
		}

		// error_msg is omitempty and empty — should not be present
		if _, ok := raw["error_msg"]; ok {
			t.Errorf(`"error_msg" should be omitted when empty (omitempty tag)`)
		}

		// Verify specific values
		if raw["id"] != "fmt-1" {
			t.Errorf("id: expected fmt-1, got %v", raw["id"])
		}
		if raw["name"] != "Format Check" {
			t.Errorf("name: expected 'Format Check', got %v", raw["name"])
		}
		if raw["type"] != "bambu" {
			t.Errorf("type: expected bambu, got %v", raw["type"])
		}
		if raw["online"] != true {
			t.Errorf("online: expected true, got %v", raw["online"])
		}
		if raw["state"] != "printing" {
			t.Errorf("state: expected printing, got %v", raw["state"])
		}
		if raw["progress"].(float64) != 0.45 {
			t.Errorf("progress: expected 0.45, got %v", raw["progress"])
		}
		if raw["remaining_time"].(float64) != 3600 {
			t.Errorf("remaining_time: expected 3600, got %v", raw["remaining_time"])
		}
		if raw["current_file"] != "benchy.gcode" {
			t.Errorf("current_file: expected benchy.gcode, got %v", raw["current_file"])
		}
		if raw["current_layer"].(float64) != 5 {
			t.Errorf("current_layer: expected 5, got %v", raw["current_layer"])
		}
		if raw["total_layers"].(float64) != 100 {
			t.Errorf("total_layers: expected 100, got %v", raw["total_layers"])
		}
	})

	t.Run("error_msg present when non-empty", func(t *testing.T) {
		stat := printers.PrinterStatus{
			ID:        "err-1",
			Name:      "Error Printer",
			Type:      "bambu",
			State:     "error",
			ErrorMsg:  "Heater anomaly detected",
		}
		p := &MockPrinter{
			id:   "err-1",
			name: "Error Printer",
			stat: stat,
		}
		s := newTestServer(map[string]printers.Printer{"err-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers/err-1")
		defer resp.Body.Close()

		var raw map[string]any
		decodeBody(t, resp, &raw)

		if msg, ok := raw["error_msg"]; !ok {
			t.Errorf(`"error_msg" should be present when non-empty`)
		} else if msg != "Heater anomaly detected" {
			t.Errorf(`error_msg: expected "Heater anomaly detected", got %v`, msg)
		}
	})
}

// ---------------------------------------------------------------------------
// GET /ws — WebSocket
// ---------------------------------------------------------------------------

// waitForHubLen polls up to 2s for hub.Len() to reach the expected value.
func waitForHubLen(t *testing.T, h *ws.Hub, expected int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if h.Len() == expected {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("Hub.Len() never reached %d (stuck at %d)", expected, h.Len())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestHandleWebSocket(t *testing.T) {
	s := newTestServer(nil)
	t.Cleanup(func() { s.wsHub.Stop() })

	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	// Convert http URL to ws URL
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	// Connect via WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer conn.Close()

	// Wait for the hub to register our client
	waitForHubLen(t, s.wsHub, 1)

	// Broadcast a message through the hub to verify end-to-end delivery
	msg := []byte(`{"type":"test","data":"hello"}`)
	s.wsHub.Broadcast(msg)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read broadcast message: %v", err)
	}
	if string(got) != string(msg) {
		t.Errorf("expected message %q, got %q", string(msg), string(got))
	}
}

// ---------------------------------------------------------------------------
// GET /api/printers/{id}
// ---------------------------------------------------------------------------

func TestHandleGetPrinter(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		p := &MockPrinter{
			id:   "printer-1",
			name: "My Printer",
			stat: printers.PrinterStatus{
				ID:     "printer-1",
				Name:   "My Printer",
				Type:   "bambu",
				State:  "idle",
				Online: true,
			},
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers/printer-1")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var status printers.PrinterStatus
		decodeBody(t, resp, &status)

		if status.ID != "printer-1" {
			t.Errorf("expected id 'printer-1', got %q", status.ID)
		}
		if status.Name != "My Printer" {
			t.Errorf("expected name 'My Printer', got %q", status.Name)
		}
		if status.State != "idle" {
			t.Errorf("expected state 'idle', got %q", status.State)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers/nonexistent")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)

		if body["error"] != `printer "nonexistent" not found` {
			t.Errorf("unexpected error message: %q", body["error"])
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/pause
// ---------------------------------------------------------------------------

func TestHandlePause(t *testing.T) {
	t.Run("found and pauses", func(t *testing.T) {
		p := &MockPrinter{
			id:   "printer-1",
			name: "My Printer",
			stat: printers.PrinterStatus{ID: "printer-1", Name: "My Printer"},
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/pause")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["status"] != "ok" {
			t.Errorf(`expected "status":"ok", got %q`, body["status"])
		}

		if !p.PauseCalled {
			t.Error("expected PauseCalled to be true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/nonexistent/pause")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != `printer "nonexistent" not found` {
			t.Errorf("unexpected error message: %q", body["error"])
		}
	})

	t.Run("pause error", func(t *testing.T) {
		p := &MockPrinter{
			id:       "printer-1",
			name:     "My Printer",
			pauseErr: errors.New("busy"),
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/pause")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != "busy" {
			t.Errorf(`expected error "busy", got %q`, body["error"])
		}

		if !p.PauseCalled {
			t.Error("expected PauseCalled to be true even on error")
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/resume
// ---------------------------------------------------------------------------

func TestHandleResume(t *testing.T) {
	t.Run("found and resumes", func(t *testing.T) {
		p := &MockPrinter{
			id:   "printer-1",
			name: "My Printer",
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/resume")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["status"] != "ok" {
			t.Errorf(`expected "status":"ok", got %q`, body["status"])
		}

		if !p.ResumeCalled {
			t.Error("expected ResumeCalled to be true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/nonexistent/resume")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != `printer "nonexistent" not found` {
			t.Errorf("unexpected error message: %q", body["error"])
		}
	})

	t.Run("resume error", func(t *testing.T) {
		p := &MockPrinter{
			id:        "printer-1",
			name:      "My Printer",
			resumeErr: errors.New("not paused"),
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/resume")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != "not paused" {
			t.Errorf(`expected error "not paused", got %q`, body["error"])
		}

		if !p.ResumeCalled {
			t.Error("expected ResumeCalled to be true even on error")
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/cancel
// ---------------------------------------------------------------------------

func TestHandleCancel(t *testing.T) {
	t.Run("found and cancels", func(t *testing.T) {
		p := &MockPrinter{
			id:   "printer-1",
			name: "My Printer",
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/cancel")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["status"] != "ok" {
			t.Errorf(`expected "status":"ok", got %q`, body["status"])
		}

		if !p.CancelCalled {
			t.Error("expected CancelCalled to be true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/nonexistent/cancel")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != `printer "nonexistent" not found` {
			t.Errorf("unexpected error message: %q", body["error"])
		}
	})

	t.Run("cancel error", func(t *testing.T) {
		p := &MockPrinter{
			id:        "printer-1",
			name:      "My Printer",
			cancelErr: errors.New("cannot cancel"),
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/cancel")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != "cannot cancel" {
			t.Errorf(`expected error "cannot cancel", got %q`, body["error"])
		}

		if !p.CancelCalled {
			t.Error("expected CancelCalled to be true even on error")
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/skip
// ---------------------------------------------------------------------------

func TestHandleSkip(t *testing.T) {
	t.Run("found and skips", func(t *testing.T) {
		p := &MockPrinter{
			id:   "printer-1",
			name: "My Printer",
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/skip")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["status"] != "ok" {
			t.Errorf(`expected "status":"ok", got %q`, body["status"])
		}

		if !p.SkipCalled {
			t.Error("expected SkipCalled to be true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/nonexistent/skip")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != `printer "nonexistent" not found` {
			t.Errorf("unexpected error message: %q", body["error"])
		}
	})

	t.Run("skip error", func(t *testing.T) {
		p := &MockPrinter{
			id:      "printer-1",
			name:    "My Printer",
			skipErr: errors.New("cannot skip"),
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/skip")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != "cannot skip" {
			t.Errorf(`expected error "cannot skip", got %q`, body["error"])
		}

		if !p.SkipCalled {
			t.Error("expected SkipCalled to be true even on error")
		}
	})
}

// ---------------------------------------------------------------------------
// Dashboard and onboarding template tests
// ---------------------------------------------------------------------------

func TestHandleIndex_ZeroPrinters(t *testing.T) {
	s := newTestServer(nil)
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL, "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body := mustReadBody(t, resp)
	if !strings.Contains(body, "+ Add Your First Printer") {
		t.Error("expected zero-state template to contain '+ Add Your First Printer' button")
	}
	if !strings.Contains(body, `href="/onboarding"`) {
		t.Error("expected zero-state template to have link to /onboarding")
	}
	if strings.Contains(body, "+ Add Printer") {
		t.Error("zero-state template should NOT contain dashboard '+ Add Printer' button")
	}
}

func TestHandleIndex_WithPrinter(t *testing.T) {
	s := newTestServer(map[string]printers.Printer{
		"p1": &MockPrinter{
			id:   "p1",
			name: "Test Printer",
			stat: printers.PrinterStatus{
				ID:   "p1",
				Name: "Test Printer",
			},
		},
	})
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL, "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body := mustReadBody(t, resp)
	if !strings.Contains(body, "+ Add Printer") {
		t.Error("expected dashboard template to contain '+ Add Printer' button")
	}
	if !strings.Contains(body, `href="/onboarding"`) {
		t.Error("expected dashboard template to have link to /onboarding")
	}
	if strings.Contains(body, "+ Add Your First Printer") {
		t.Error("dashboard template should NOT contain zero-state '+ Add Your First Printer' button")
	}
}

func TestHandleOnboardingStart_AddPrinterButton(t *testing.T) {
	s := newTestServer(nil)
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL, "/onboarding")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body := mustReadBody(t, resp)
	if !strings.Contains(body, "+ Add Printer") {
		t.Error("expected onboarding start page to contain '+ Add Printer' heading")
	}
	if !strings.Contains(body, `href="/onboarding/bambu"`) {
		t.Error("expected onboarding start page to have link to Bambu cloud option")
	}
}
