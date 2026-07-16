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
	"github.com/chrisjohnson/printer-dashboard/internal/printers/snapmaker"
	"github.com/chrisjohnson/printer-dashboard/internal/ws"
)

// ---------------------------------------------------------------------------
// MockPrinter — implements printers.Printer for testing
// ---------------------------------------------------------------------------

// MockPrinter is a test double that implements the printers.Printer interface.
type MockPrinter struct {
	printers.Printer // embed to satisfy interface at compile time

	id   string
	name string
	stat printers.PrinterStatus
	mu   sync.Mutex

	pauseErr   error
	resumeErr  error
	cancelErr  error
	skipErr    error
	connectErr error

	setBedTempErr     error
	setNozzleTempErr  error
	setChamberTempErr error
	setLightErr       error

	PauseCalled          bool
	ResumeCalled         bool
	CancelCalled         bool
	SkipCalled           bool
	SetBedTempCalled     bool
	SetNozzleTempCalled  bool
	SetChamberTempCalled bool
	SetLightCalled       bool
	LastLightOn          *bool
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

func (m *MockPrinter) CameraStreams() []printers.CameraStream { return nil }

func (m *MockPrinter) SetBedTemp(_ context.Context, _ int) error {
	m.SetBedTempCalled = true
	return m.setBedTempErr
}

func (m *MockPrinter) SetNozzleTemp(_ context.Context, _ int) error {
	m.SetNozzleTempCalled = true
	return m.setNozzleTempErr
}

func (m *MockPrinter) SetChamberTemp(_ context.Context, _ int) error {
	m.SetChamberTempCalled = true
	return m.setChamberTempErr
}

func (m *MockPrinter) SetLight(_ context.Context, on bool) error {
	m.SetLightCalled = true
	m.LastLightOn = &on
	return m.setLightErr
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestServer creates a minimal Server with the given printer map.
func newTestServer(printersMap map[string]printers.Printer) *Server {
	wsHub := ws.NewHub()
	go wsHub.Run()

	s := &Server{
		cfg:               &config.Config{Listen: ":0"},
		mux:               http.NewServeMux(),
		printers:          printersMap,
		wsHub:             wsHub,
		skipTracker:       NewSkipTracker(),
		hmsDismissTracker: NewHMSDismissTracker(),
		lastPrintState:    make(map[string]string),
	}
	s.registerRoutes()
	return s
}

// float64Ptr returns a pointer to the given float64 value.
func float64Ptr(v float64) *float64 { return &v }

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

	t.Run("has_chamber round-trips in JSON", func(t *testing.T) {
		pChamber := &MockPrinter{
			id:   "with-chamber",
			name: "With Chamber",
			stat: printers.PrinterStatus{ID: "with-chamber", Name: "With Chamber", HasChamber: true},
		}
		pNoChamber := &MockPrinter{
			id:   "no-chamber",
			name: "No Chamber",
			stat: printers.PrinterStatus{ID: "no-chamber", Name: "No Chamber", HasChamber: false},
		}
		s := newTestServer(map[string]printers.Printer{
			"with-chamber": pChamber,
			"no-chamber":   pNoChamber,
		})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers")
		defer resp.Body.Close()

		var body map[string]any
		decodeBody(t, resp, &body)

		list := body["printers"].([]any)
		if len(list) != 2 {
			t.Fatalf("expected 2 printers, got %d", len(list))
		}

		byID := make(map[string]map[string]any)
		for _, p := range list {
			m := p.(map[string]any)
			byID[m["id"].(string)] = m
		}

		if got := byID["with-chamber"]["has_chamber"]; got != true {
			t.Errorf("with-chamber: has_chamber = %v; want true", got)
		}
		if got := byID["no-chamber"]["has_chamber"]; got != false {
			t.Errorf("no-chamber: has_chamber = %v; want false", got)
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

	t.Run("active printers before inactive", func(t *testing.T) {
		pIdle := &MockPrinter{
			id:   "idle1",
			name: "alpha",
			stat: printers.PrinterStatus{ID: "idle1", Name: "alpha", State: "idle"},
		}
		pPrinting := &MockPrinter{
			id:   "print1",
			name: "delta",
			stat: printers.PrinterStatus{ID: "print1", Name: "delta", State: "printing"},
		}
		pPaused := &MockPrinter{
			id:   "pause1",
			name: "bravo",
			stat: printers.PrinterStatus{ID: "pause1", Name: "bravo", State: "paused"},
		}
		s := newTestServer(map[string]printers.Printer{
			"idle1":  pIdle,
			"print1": pPrinting,
			"pause1": pPaused,
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

		// Active printers (printing, paused) come first sorted A-Z,
		// then inactive printers sorted A-Z.
		expected := []string{"bravo", "delta", "alpha"}
		for i, want := range expected {
			got := list[i].(map[string]any)["name"].(string)
			if got != want {
				t.Errorf("position %d: expected %q, got %q", i, want, got)
			}
		}
	})

	t.Run("all inactive same as alphabetical", func(t *testing.T) {
		pC := &MockPrinter{
			id:   "c",
			name: "charlie",
			stat: printers.PrinterStatus{ID: "c", Name: "charlie", State: "idle"},
		}
		pA := &MockPrinter{
			id:   "a",
			name: "alpha",
			stat: printers.PrinterStatus{ID: "a", Name: "alpha", State: "idle"},
		}
		pB := &MockPrinter{
			id:   "b",
			name: "bravo",
			stat: printers.PrinterStatus{ID: "b", Name: "bravo", State: "complete"},
		}
		s := newTestServer(map[string]printers.Printer{
			"c": pC,
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

		// "complete" is tier 1 (active), idle printers are tier 2.
		// Active first (A-Z), then inactive (A-Z).
		expected := []string{"bravo", "alpha", "charlie"}
		for i, want := range expected {
			got := list[i].(map[string]any)["name"].(string)
			if got != want {
				t.Errorf("position %d: expected %q, got %q", i, want, got)
			}
		}
	})

	t.Run("active sorted alphabetically within group", func(t *testing.T) {
		pA := &MockPrinter{
			id:   "a",
			name: "zebra",
			stat: printers.PrinterStatus{ID: "a", Name: "zebra", State: "printing"},
		}
		pB := &MockPrinter{
			id:   "b",
			name: "alpha",
			stat: printers.PrinterStatus{ID: "b", Name: "alpha", State: "paused"},
		}
		s := newTestServer(map[string]printers.Printer{
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
		if len(list) != 2 {
			t.Fatalf("expected 2 printers, got %d", len(list))
		}

		// Both active — sorted A-Z within the active group.
		expected := []string{"alpha", "zebra"}
		for i, want := range expected {
			got := list[i].(map[string]any)["name"].(string)
			if got != want {
				t.Errorf("position %d: expected %q, got %q", i, want, got)
			}
		}
	})

	t.Run("three-tier sort error active idle", func(t *testing.T) {
		pIdle1 := &MockPrinter{
			id:   "idle1",
			name: "echo",
			stat: printers.PrinterStatus{ID: "idle1", Name: "echo", State: "idle"},
		}
		pError1 := &MockPrinter{
			id:   "err1",
			name: "foxtrot",
			stat: printers.PrinterStatus{ID: "err1", Name: "foxtrot", State: "error"},
		}
		pActive1 := &MockPrinter{
			id:   "act1",
			name: "delta",
			stat: printers.PrinterStatus{ID: "act1", Name: "delta", State: "printing"},
		}
		pError2 := &MockPrinter{
			id:   "err2",
			name: "alpha",
			stat: printers.PrinterStatus{ID: "err2", Name: "alpha", State: "error"},
		}
		pActive2 := &MockPrinter{
			id:   "act2",
			name: "bravo",
			stat: printers.PrinterStatus{ID: "act2", Name: "bravo", State: "paused"},
		}
		pIdle2 := &MockPrinter{
			id:   "idle2",
			name: "charlie",
			stat: printers.PrinterStatus{ID: "idle2", Name: "charlie", State: "idle"},
		}
		s := newTestServer(map[string]printers.Printer{
			"err1":  pError1,
			"err2":  pError2,
			"act1":  pActive1,
			"act2":  pActive2,
			"idle1": pIdle1,
			"idle2": pIdle2,
		})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers")
		defer resp.Body.Close()

		var body map[string]any
		decodeBody(t, resp, &body)

		list := body["printers"].([]any)
		if len(list) != 6 {
			t.Fatalf("expected 6 printers, got %d", len(list))
		}

		// Error first (A-Z), then active (A-Z), then idle (A-Z).
		expected := []string{"alpha", "foxtrot", "bravo", "delta", "charlie", "echo"}
		for i, want := range expected {
			got := list[i].(map[string]any)["name"].(string)
			if got != want {
				t.Errorf("position %d: expected %q, got %q", i, want, got)
			}
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
			BedTemp:          float64Ptr(60.5),
			BedTargetTemp:    float64Ptr(65.0),
			NozzleTemp:       float64Ptr(220.0),
			NozzleTargetTemp: float64Ptr(220.0),
			ChamberTemp:      float64Ptr(35.0),
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
			"nozzle_temp", "nozzle_target_temp", "chamber_temp", "chamber_target_temp",
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
			ID:       "err-1",
			Name:     "Error Printer",
			Type:     "bambu",
			State:    "error",
			ErrorMsg: "Heater anomaly detected",
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
// enrichStatusWithCameras tests
// ---------------------------------------------------------------------------

type mockPrinterWithCameras struct {
	MockPrinter
	cameras []printers.CameraStream
}

func (m *mockPrinterWithCameras) CameraStreams() []printers.CameraStream {
	return m.cameras
}

func TestEnrichStatusWithCameras_DriverOnly(t *testing.T) {
	cfg := &config.Config{
		Listen: ":0",
	}
	s := &Server{
		cfg:      cfg,
		printers: make(map[string]printers.Printer),
		mux:      http.NewServeMux(),
	}

	p := &mockPrinterWithCameras{
		MockPrinter: MockPrinter{id: "p1", name: "Test"},
		cameras: []printers.CameraStream{
			{URL: "http://camera.local/stream", Type: "internal", Label: "Camera"},
		},
	}
	s.printers["p1"] = p

	status := printers.PrinterStatus{ID: "p1", Name: "Test"}
	enriched := s.enrichStatusWithCameras("p1", status)

	if len(enriched.CameraStreams) != 1 {
		t.Fatalf("expected 1 camera stream, got %d", len(enriched.CameraStreams))
	}
	if enriched.CameraStreams[0].URL != "/api/camera/proxy?url=http%3A%2F%2Fcamera.local%2Fstream" {
		t.Errorf("URL = %q; want %q", enriched.CameraStreams[0].URL, "/api/camera/proxy?url=http%3A%2F%2Fcamera.local%2Fstream")
	}
	if enriched.CameraStreams[0].Type != "internal" {
		t.Errorf("Type = %q; want %q", enriched.CameraStreams[0].Type, "internal")
	}
}

func TestEnrichStatusWithCameras_ConfigOnly(t *testing.T) {
	cfg := &config.Config{
		Listen: ":0",
		Cameras: []config.CameraDef{
			{ID: "cam1", Name: "Workshop", URL: "http://cam.local/feed", PrinterID: "p1"},
		},
	}
	s := &Server{
		cfg:      cfg,
		printers: make(map[string]printers.Printer),
		mux:      http.NewServeMux(),
	}

	p := &MockPrinter{id: "p1", name: "Test"}
	s.printers["p1"] = p

	status := printers.PrinterStatus{ID: "p1", Name: "Test"}
	enriched := s.enrichStatusWithCameras("p1", status)

	if len(enriched.CameraStreams) != 1 {
		t.Fatalf("expected 1 camera stream, got %d", len(enriched.CameraStreams))
	}
	if enriched.CameraStreams[0].URL != "/api/camera/proxy?url=http%3A%2F%2Fcam.local%2Ffeed" {
		t.Errorf("URL = %q; want %q", enriched.CameraStreams[0].URL, "/api/camera/proxy?url=http%3A%2F%2Fcam.local%2Ffeed")
	}
	if enriched.CameraStreams[0].Type != "external" {
		t.Errorf("Type = %q; want %q", enriched.CameraStreams[0].Type, "external")
	}
	if enriched.CameraStreams[0].Label != "Workshop" {
		t.Errorf("Label = %q; want %q", enriched.CameraStreams[0].Label, "Workshop")
	}
}

func TestEnrichStatusWithCameras_Merged(t *testing.T) {
	cfg := &config.Config{
		Listen: ":0",
		Cameras: []config.CameraDef{
			{ID: "cam1", Name: "Front Door", URL: "http://front/feed", PrinterID: "p1"},
		},
	}
	s := &Server{
		cfg:      cfg,
		printers: make(map[string]printers.Printer),
		mux:      http.NewServeMux(),
	}

	p := &mockPrinterWithCameras{
		MockPrinter: MockPrinter{id: "p1", name: "Test"},
		cameras: []printers.CameraStream{
			{URL: "http://internal/stream", Type: "internal", Label: "Camera"},
			{URL: "http://touch/display", Type: "touchscreen", Label: "Touchscreen"},
		},
	}
	s.printers["p1"] = p

	status := printers.PrinterStatus{ID: "p1", Name: "Test"}
	enriched := s.enrichStatusWithCameras("p1", status)

	if len(enriched.CameraStreams) != 3 {
		t.Fatalf("expected 3 camera streams, got %d", len(enriched.CameraStreams))
	}

	// Order: internal → config external → remaining (touchscreen)
	if enriched.CameraStreams[0].Type != "internal" {
		t.Errorf("stream[0].Type = %q; want %q", enriched.CameraStreams[0].Type, "internal")
	}
	if enriched.CameraStreams[1].Type != "external" || enriched.CameraStreams[1].Label != "Front Door" {
		t.Errorf("stream[1] = %+v; want Type=external Label=Front Door", enriched.CameraStreams[1])
	}
	if enriched.CameraStreams[2].Type != "touchscreen" {
		t.Errorf("stream[2].Type = %q; want %q", enriched.CameraStreams[2].Type, "touchscreen")
	}
}

func TestEnrichStatusWithCameras_PrinterNotFound(t *testing.T) {
	cfg := &config.Config{Listen: ":0"}
	s := &Server{
		cfg:      cfg,
		printers: make(map[string]printers.Printer),
		mux:      http.NewServeMux(),
	}

	status := printers.PrinterStatus{ID: "nonexistent", Name: "Ghost"}
	enriched := s.enrichStatusWithCameras("nonexistent", status)

	// Should return status unchanged
	if len(enriched.CameraStreams) != 0 {
		t.Errorf("expected 0 camera streams for unknown printer, got %d", len(enriched.CameraStreams))
	}
	if enriched.ID != "nonexistent" {
		t.Errorf("ID = %q; want %q", enriched.ID, "nonexistent")
	}
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

		var body map[string]any
		decodeBody(t, resp, &body)
		if body["status"] != "ok" {
			t.Errorf(`expected "status":"ok", got %v`, body["status"])
		}
		if count, _ := body["skipped_count"].(float64); count != 1 {
			t.Errorf(`expected "skipped_count":1, got %v`, body["skipped_count"])
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
// GET /api/printers/{id}/skipped
// ---------------------------------------------------------------------------

func TestHandleGetSkipped(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers/nonexistent/skipped")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("empty before any skip", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers/printer-1/skipped")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body struct {
			Objects []SkippedObject `json:"objects"`
			Count   int             `json:"count"`
		}
		decodeBody(t, resp, &body)
		if body.Count != 0 {
			t.Errorf("expected count 0, got %d", body.Count)
		}
		if body.Objects == nil {
			t.Error("expected objects to be an empty array, not null")
		}
	})

	t.Run("reflects skips recorded via POST /skip", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		mustPost(t, ts.URL, "/api/printers/printer-1/skip").Body.Close()
		mustPost(t, ts.URL, "/api/printers/printer-1/skip").Body.Close()

		resp := mustGet(t, ts.URL, "/api/printers/printer-1/skipped")
		defer resp.Body.Close()

		var body struct {
			Objects []SkippedObject `json:"objects"`
			Count   int             `json:"count"`
		}
		decodeBody(t, resp, &body)
		if body.Count != 2 {
			t.Errorf("expected count 2, got %d", body.Count)
		}
		if len(body.Objects) != 2 {
			t.Errorf("expected 2 objects, got %d", len(body.Objects))
		}
	})
}

// ---------------------------------------------------------------------------
// clearSkippedOnPrintEnd — pause must not clear, real session end must
// ---------------------------------------------------------------------------

func TestClearSkippedOnPrintEnd(t *testing.T) {
	newServer := func() *Server {
		return &Server{skipTracker: NewSkipTracker(), lastPrintState: make(map[string]string)}
	}

	t.Run("pause does not clear", func(t *testing.T) {
		s := newServer()
		s.skipTracker.RecordSkip("p1", "A")

		s.clearSkippedOnPrintEnd("p1", "printing")
		s.clearSkippedOnPrintEnd("p1", "paused")

		if got := s.skipTracker.GetSkipped("p1"); len(got) != 1 {
			t.Errorf("expected skip history preserved across pause, got %d entries", len(got))
		}
	})

	t.Run("resume from pause does not clear", func(t *testing.T) {
		s := newServer()
		s.skipTracker.RecordSkip("p1", "A")

		s.clearSkippedOnPrintEnd("p1", "paused")
		s.clearSkippedOnPrintEnd("p1", "printing")

		if got := s.skipTracker.GetSkipped("p1"); len(got) != 1 {
			t.Errorf("expected skip history preserved across resume, got %d entries", len(got))
		}
	})

	t.Run("completion clears", func(t *testing.T) {
		s := newServer()
		s.skipTracker.RecordSkip("p1", "A")

		s.clearSkippedOnPrintEnd("p1", "printing")
		s.clearSkippedOnPrintEnd("p1", "complete")

		if got := s.skipTracker.GetSkipped("p1"); len(got) != 0 {
			t.Errorf("expected skip history cleared on completion, got %d entries", len(got))
		}
	})

	t.Run("cancel from paused clears", func(t *testing.T) {
		s := newServer()
		s.skipTracker.RecordSkip("p1", "A")

		s.clearSkippedOnPrintEnd("p1", "paused")
		s.clearSkippedOnPrintEnd("p1", "idle")

		if got := s.skipTracker.GetSkipped("p1"); len(got) != 0 {
			t.Errorf("expected skip history cleared on cancel, got %d entries", len(got))
		}
	})

	t.Run("first-ever status does not clear", func(t *testing.T) {
		s := newServer()
		s.skipTracker.RecordSkip("p1", "A")

		// No prior clearSkippedOnPrintEnd call for p1 — lastPrintState is
		// empty, so this must not be treated as a "was active" transition.
		s.clearSkippedOnPrintEnd("p1", "idle")

		if got := s.skipTracker.GetSkipped("p1"); len(got) != 1 {
			t.Errorf("expected skip history preserved on first-ever status, got %d entries", len(got))
		}
	})

	t.Run("printers are isolated", func(t *testing.T) {
		s := newServer()
		s.skipTracker.RecordSkip("p1", "A")
		s.skipTracker.RecordSkip("p2", "B")

		s.clearSkippedOnPrintEnd("p1", "printing")
		s.clearSkippedOnPrintEnd("p1", "complete")

		if got := s.skipTracker.GetSkipped("p1"); len(got) != 0 {
			t.Errorf("p1: expected cleared, got %d entries", len(got))
		}
		if got := s.skipTracker.GetSkipped("p2"); len(got) != 1 {
			t.Errorf("p2: expected untouched, got %d entries", len(got))
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

// ---------------------------------------------------------------------------
// Snapmaker WebSocket broadcast integration
// ---------------------------------------------------------------------------

func TestSnapmakerStatusForwarding(t *testing.T) {
	s := newTestServer(nil)
	t.Cleanup(func() { s.wsHub.Stop() })

	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	// Connect a WebSocket client
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()
	waitForHubLen(t, s.wsHub, 1)

	// Create a real snapmaker printer with StatusCh wired up
	p := snapmaker.New(config.PrinterDef{ID: "sm-1", Name: "Snap U1"})
	p.StatusCh = make(chan printers.PrinterStatus, 4)

	// Start the status forwarder directly (same helper used by connectAllPrinters)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startStatusForwarder(ctx, "sm-1", p.StatusCh)

	// Give the forwarder goroutine a moment to start
	time.Sleep(10 * time.Millisecond)

	// Send a status update via StatusCh (exercises the forwarding path)
	p.StatusCh <- printers.PrinterStatus{
		ID:     "sm-1",
		Name:   "Snap U1",
		Type:   "snapmaker",
		Online: true,
		State:  "printing",
	}

	// Read the broadcast message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws message: %v", err)
	}

	var msg map[string]any
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg["type"] != "printer_update" {
		t.Errorf("type = %v; want printer_update", msg["type"])
	}

	printer, ok := msg["printer"].(map[string]any)
	if !ok {
		t.Fatal("printer field not a map")
	}
	if printer["id"] != "sm-1" {
		t.Errorf("printer.id = %v; want sm-1", printer["id"])
	}
	if printer["name"] != "Snap U1" {
		t.Errorf("printer.name = %v; want Snap U1", printer["name"])
	}
	if printer["state"] != "printing" {
		t.Errorf("printer.state = %v; want printing", printer["state"])
	}
	if printer["online"] != true {
		t.Errorf("printer.online = %v; want true", printer["online"])
	}
}

func TestSnapmakerStatusForwarding_ErrorMsg(t *testing.T) {
	s := newTestServer(nil)
	t.Cleanup(func() { s.wsHub.Stop() })

	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	// Connect a WebSocket client
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()
	waitForHubLen(t, s.wsHub, 1)

	p := snapmaker.New(config.PrinterDef{ID: "sm-err", Name: "Error U1"})
	p.StatusCh = make(chan printers.PrinterStatus, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startStatusForwarder(ctx, "sm-err", p.StatusCh)

	time.Sleep(10 * time.Millisecond)

	// Send an error status via StatusCh
	p.StatusCh <- printers.PrinterStatus{
		ID:       "sm-err",
		Name:     "Error U1",
		Type:     "snapmaker",
		Online:   false,
		State:    "error",
		ErrorMsg: "dial tcp 192.168.1.100:8080: connect: connection refused",
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws message: %v", err)
	}

	var msg map[string]any
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	printer, ok := msg["printer"].(map[string]any)
	if !ok {
		t.Fatal("printer field not a map")
	}
	if printer["error_msg"] != "dial tcp 192.168.1.100:8080: connect: connection refused" {
		t.Errorf("error_msg = %v; want dial error message", printer["error_msg"])
	}
}

// ---------------------------------------------------------------------------
// Dashboard template — error banner rendering
// ---------------------------------------------------------------------------

func TestDashboardTemplate_ErrorBanner(t *testing.T) {
	// Verify the template constant contains error_msg rendering logic
	if !strings.Contains(indexDashboardTemplate, "error_msg") {
		t.Error("indexDashboardTemplate should reference error_msg in renderCard")
	}
	if !strings.Contains(indexDashboardTemplate, "error-banner") {
		t.Error("indexDashboardTemplate should define .error-banner CSS class")
	}
	if !strings.Contains(indexDashboardTemplate, "errorHtml") {
		t.Error("indexDashboardTemplate should define errorHtml in renderCard")
	}
}

// ---------------------------------------------------------------------------
// Cache-Control headers (K-051)
// ---------------------------------------------------------------------------

func TestNoCacheHeaders(t *testing.T) {
	t.Run("API health endpoint", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(noCacheMiddleware(s.mux))
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/health")
		defer resp.Body.Close()

		if got := resp.Header.Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
			t.Errorf("Cache-Control = %q; want %q", got, "no-cache, no-store, must-revalidate")
		}
		if got := resp.Header.Get("Pragma"); got != "no-cache" {
			t.Errorf("Pragma = %q; want %q", got, "no-cache")
		}
		if got := resp.Header.Get("Expires"); got != "0" {
			t.Errorf("Expires = %q; want %q", got, "0")
		}
	})

	t.Run("dashboard index", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(noCacheMiddleware(s.mux))
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/")
		defer resp.Body.Close()

		if got := resp.Header.Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
			t.Errorf("Cache-Control = %q; want %q", got, "no-cache, no-store, must-revalidate")
		}
		if got := resp.Header.Get("Pragma"); got != "no-cache" {
			t.Errorf("Pragma = %q; want %q", got, "no-cache")
		}
		if got := resp.Header.Get("Expires"); got != "0" {
			t.Errorf("Expires = %q; want %q", got, "0")
		}
	})

	t.Run("onboarding page", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(noCacheMiddleware(s.mux))
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/onboarding")
		defer resp.Body.Close()

		if got := resp.Header.Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
			t.Errorf("Cache-Control = %q; want %q", got, "no-cache, no-store, must-revalidate")
		}
	})

	t.Run("printers API", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(noCacheMiddleware(s.mux))
		t.Cleanup(ts.Close)

		resp := mustGet(t, ts.URL, "/api/printers")
		defer resp.Body.Close()

		if got := resp.Header.Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
			t.Errorf("Cache-Control = %q; want %q", got, "no-cache, no-store, must-revalidate")
		}
	})
}

// ---------------------------------------------------------------------------
// Test helpers — JSON POST
// ---------------------------------------------------------------------------

// mustPostJSON is a helper that POSTs a JSON body to a URL and returns the response.
func mustPostJSON(t *testing.T, baseURL, path string, body any) *http.Response {
	t.Helper()
	var buf strings.Builder
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encoding JSON body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("creating POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("executing POST %s: %v", path, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/bed-temp
// ---------------------------------------------------------------------------

func TestHandleSetBedTemp(t *testing.T) {
	t.Run("found and sets", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/bed-temp", map[string]int{"temp": 60})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		var body map[string]string
		decodeBody(t, resp, &body)
		if body["status"] != "ok" {
			t.Errorf(`expected "status":"ok", got %q`, body["status"])
		}
		if !p.SetBedTempCalled {
			t.Error("expected SetBedTempCalled to be true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/nonexistent/bed-temp", map[string]int{"temp": 60})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("bad request body", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/bed-temp")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/nozzle-temp
// ---------------------------------------------------------------------------

func TestHandleSetNozzleTemp(t *testing.T) {
	t.Run("found and sets", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/nozzle-temp", map[string]int{"temp": 210})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if !p.SetNozzleTempCalled {
			t.Error("expected SetNozzleTempCalled to be true")
		}
	})

	t.Run("error from printer", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer", setNozzleTempErr: errors.New("not connected")}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/nozzle-temp", map[string]int{"temp": 210})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", resp.StatusCode)
		}
		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != "not connected" {
			t.Errorf(`expected error "not connected", got %q`, body["error"])
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/chamber-temp
// ---------------------------------------------------------------------------

func TestHandleSetChamberTemp(t *testing.T) {
	t.Run("found and sets", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/chamber-temp", map[string]int{"temp": 35})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if !p.SetChamberTempCalled {
			t.Error("expected SetChamberTempCalled to be true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/nonexistent/chamber-temp", map[string]int{"temp": 35})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/light
// ---------------------------------------------------------------------------

func TestHandleSetLight(t *testing.T) {
	t.Run("found and turns on", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/light", map[string]bool{"on": true})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if !p.SetLightCalled {
			t.Error("expected SetLightCalled to be true")
		}
		if p.LastLightOn == nil || !*p.LastLightOn {
			t.Error("expected LastLightOn to be true")
		}
	})

	t.Run("found and turns off", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/light", map[string]bool{"on": false})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if p.LastLightOn == nil || *p.LastLightOn {
			t.Error("expected LastLightOn to be false")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/nonexistent/light", map[string]bool{"on": true})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("error from printer", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer", setLightErr: errors.New("MQTT disconnected")}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/light", map[string]bool{"on": true})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", resp.StatusCode)
		}
		var body map[string]string
		decodeBody(t, resp, &body)
		if body["error"] != "MQTT disconnected" {
			t.Errorf(`expected error "MQTT disconnected", got %q`, body["error"])
		}
	})
}

// ---------------------------------------------------------------------------
// Light on/off round-trips in JSON status
// ---------------------------------------------------------------------------

func TestHandleListPrinters_LightOnRoundTrip(t *testing.T) {
	on := true
	p := &MockPrinter{
		id:   "p1",
		name: "Light Printer",
		stat: printers.PrinterStatus{ID: "p1", Name: "Light Printer", LightOn: &on},
	}
	s := newTestServer(map[string]printers.Printer{"p1": p})
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL, "/api/printers/p1")
	defer resp.Body.Close()

	var raw map[string]any
	decodeBody(t, resp, &raw)

	if raw["light_on"] != true {
		t.Errorf("light_on: expected true, got %v", raw["light_on"])
	}
}

// ---------------------------------------------------------------------------
// POST /api/printers/{id}/hms/dismiss
// ---------------------------------------------------------------------------

func TestHandleDismissHMS(t *testing.T) {
	t.Run("found and dismisses", func(t *testing.T) {
		p := &MockPrinter{
			id:   "printer-1",
			name: "My Printer",
			stat: printers.PrinterStatus{
				ID:   "printer-1",
				Name: "My Printer",
				HMSErrors: []printers.HMSEntry{
					{Code: "0300-2000-0003-0001", Module: "AMS", Severity: "fatal"},
				},
			},
		}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/hms/dismiss", map[string]string{"code": "0300-2000-0003-0001"})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var body map[string]string
		decodeBody(t, resp, &body)
		if body["status"] != "ok" {
			t.Errorf(`expected "status":"ok", got %q`, body["status"])
		}

		if !s.hmsDismissTracker.IsDismissed("printer-1", "0300-2000-0003-0001") {
			t.Error("expected code to be marked dismissed")
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestServer(nil)
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/nonexistent/hms/dismiss", map[string]string{"code": "0300-2000-0003-0001"})
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

	t.Run("bad request body", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPost(t, ts.URL, "/api/printers/printer-1/hms/dismiss")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("missing code", func(t *testing.T) {
		p := &MockPrinter{id: "printer-1", name: "My Printer"}
		s := newTestServer(map[string]printers.Printer{"printer-1": p})
		ts := httptest.NewServer(s.mux)
		t.Cleanup(ts.Close)

		resp := mustPostJSON(t, ts.URL, "/api/printers/printer-1/hms/dismiss", map[string]string{})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// Dismissed HMS codes are filtered from outbound status
// ---------------------------------------------------------------------------

func TestHandleListPrinters_FiltersDismissedHMS(t *testing.T) {
	p := &MockPrinter{
		id:   "p1",
		name: "Test Printer",
		stat: printers.PrinterStatus{
			ID:   "p1",
			Name: "Test Printer",
			HMSErrors: []printers.HMSEntry{
				{Code: "err-dismissed", Module: "AMS", Severity: "fatal"},
				{Code: "err-active", Module: "AMS", Severity: "fatal"},
			},
			HMSWarnings: []printers.HMSEntry{
				{Code: "warn-dismissed", Module: "AMS", Severity: "common"},
			},
		},
	}
	s := newTestServer(map[string]printers.Printer{"p1": p})
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	// Dismiss before the first read so the fixture reflects post-dismiss state.
	s.hmsDismissTracker.Dismiss("p1", "err-dismissed")
	s.hmsDismissTracker.Dismiss("p1", "warn-dismissed")

	resp := mustGet(t, ts.URL, "/api/printers")
	defer resp.Body.Close()

	var body map[string]any
	decodeBody(t, resp, &body)

	list, ok := body["printers"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("expected 1 printer, got %v", body["printers"])
	}
	prt := list[0].(map[string]any)

	hmsErrors, _ := prt["hms_errors"].([]any)
	if len(hmsErrors) != 1 {
		t.Fatalf("expected 1 remaining hms_errors entry, got %d: %v", len(hmsErrors), hmsErrors)
	}
	if code := hmsErrors[0].(map[string]any)["code"]; code != "err-active" {
		t.Errorf("expected remaining error code 'err-active', got %v", code)
	}

	if hmsWarnings, ok := prt["hms_warnings"]; ok {
		t.Errorf("expected hms_warnings to be filtered out entirely (omitempty), got %v", hmsWarnings)
	}
}

func TestHandleGetPrinter_FiltersDismissedHMS(t *testing.T) {
	p := &MockPrinter{
		id:   "p1",
		name: "Test Printer",
		stat: printers.PrinterStatus{
			ID:   "p1",
			Name: "Test Printer",
			HMSErrors: []printers.HMSEntry{
				{Code: "err-dismissed", Module: "AMS", Severity: "fatal"},
			},
		},
	}
	s := newTestServer(map[string]printers.Printer{"p1": p})
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)

	s.hmsDismissTracker.Dismiss("p1", "err-dismissed")

	resp := mustGet(t, ts.URL, "/api/printers/p1")
	defer resp.Body.Close()

	var body map[string]any
	decodeBody(t, resp, &body)

	if hmsErrors, ok := body["hms_errors"]; ok {
		t.Errorf("expected hms_errors to be filtered out entirely (omitempty), got %v", hmsErrors)
	}
}
