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

func TestCameraStreams(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.PrinterDef
		want []printers.CameraStream
	}{
		{
			name: "with host and port",
			cfg: config.PrinterDef{
				Host: "192.168.1.100",
				Port: 8080,
			},
			want: []printers.CameraStream{
				{URL: "http://192.168.1.100:8080/webcam/stream.mjpg", Type: "internal", Label: "Camera"},
				{URL: "http://192.168.1.100:8080/screen/snapshot", Type: "touchscreen", Label: "Touchscreen"},
			},
		},
		{
			name: "with host only (default port 80)",
			cfg: config.PrinterDef{
				Host: "10.0.0.50",
				Port: 0,
			},
			want: []printers.CameraStream{
				{URL: "http://10.0.0.50:80/webcam/stream.mjpg", Type: "internal", Label: "Camera"},
				{URL: "http://10.0.0.50:80/screen/snapshot", Type: "touchscreen", Label: "Touchscreen"},
			},
		},
		{
			name: "with access code",
			cfg: config.PrinterDef{
				Host:       "192.168.1.100",
				Port:       8080,
				AccessCode: "my-access-code",
			},
			want: []printers.CameraStream{
				{URL: "http://192.168.1.100:8080/webcam/stream.mjpg?access_code=my-access-code", Type: "internal", Label: "Camera"},
				{URL: "http://192.168.1.100:8080/screen/snapshot?access_code=my-access-code", Type: "touchscreen", Label: "Touchscreen"},
			},
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
			got := p.CameraStreams()

			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("CameraStreams = %+v; want %+v", got, tt.want)
			}
			for i := range got {
				if got[i].URL != tt.want[i].URL {
					t.Errorf("CameraStreams[%d].URL = %q; want %q", i, got[i].URL, tt.want[i].URL)
				}
				if got[i].Type != tt.want[i].Type {
					t.Errorf("CameraStreams[%d].Type = %q; want %q", i, got[i].Type, tt.want[i].Type)
				}
				if got[i].Label != tt.want[i].Label {
					t.Errorf("CameraStreams[%d].Label = %q; want %q", i, got[i].Label, tt.want[i].Label)
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

	report := &apiPrinterResponse{
		State: &stateReport{
			Text: "Printing",
			Flags: &stateFlags{
				Printing: true,
			},
		},
		Temperature: makeTempReport(
			tempPair{"bed", &temperatureEntry{Actual: 55.0, Target: 60.0}},
			tempPair{"tool0", &temperatureEntry{Actual: 210.0, Target: 220.0}},
			tempPair{"tool1", &temperatureEntry{Actual: 30.0, Target: 0.0}},
			tempPair{"tool2", &temperatureEntry{Actual: 30.0, Target: 0.0}},
			tempPair{"tool3", &temperatureEntry{Actual: 30.0, Target: 0.0}},
		),
	}

	p.handleStatusReport(report)
	s := p.Status()

	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
	if s.BedTemp == nil || *s.BedTemp != 55.0 {
		t.Errorf("BedTemp = %v; want 55.0", s.BedTemp)
	}
	if s.NozzleTemp == nil || *s.NozzleTemp != 210.0 {
		t.Errorf("NozzleTemp = %v; want 210.0", s.NozzleTemp)
	}
	if s.BedTargetTemp == nil || *s.BedTargetTemp != 60.0 {
		t.Errorf("BedTargetTemp = %v; want 60.0", s.BedTargetTemp)
	}
	if s.NozzleTargetTemp == nil || *s.NozzleTargetTemp != 220.0 {
		t.Errorf("NozzleTargetTemp = %v; want 220.0", s.NozzleTargetTemp)
	}
	if !s.Online {
		t.Error("Online = false; want true")
	}
	// Tool0 → NozzleTemp, all tools → NozzleTemps
	if len(s.NozzleTemps) != 4 {
		t.Errorf("len(NozzleTemps) = %d; want 4", len(s.NozzleTemps))
	} else {
		if s.NozzleTemps[0].Index != 0 || s.NozzleTemps[0].Actual == nil || *s.NozzleTemps[0].Actual != 210.0 {
			t.Errorf("NozzleTemps[0] = %+v; want {Index:0 Actual:210 Target:220}", s.NozzleTemps[0])
		}
		if s.NozzleTemps[1].Index != 1 || s.NozzleTemps[1].Actual == nil || *s.NozzleTemps[1].Actual != 30.0 {
			t.Errorf("NozzleTemps[1] = %+v; want {Index:1 Actual:30 Target:0}", s.NozzleTemps[1])
		}
	}
}

func TestHandleStatusReport_PartialUpdate(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	// First: full update with state + temps.
	full := &apiPrinterResponse{
		State: &stateReport{
			Text: "Printing",
			Flags: &stateFlags{
				Printing: true,
			},
		},
		Temperature: makeTempReport(
			tempPair{"bed", &temperatureEntry{Actual: 55.0, Target: 60.0}},
			tempPair{"tool0", &temperatureEntry{Actual: 210.0, Target: 220.0}},
		),
	}
	p.handleStatusReport(full)

	// Second: partial update — only state changes (no temperature section).
	partial := &apiPrinterResponse{
		State: &stateReport{
			Text: "Printing",
			Flags: &stateFlags{
				Printing: true,
			},
		},
	}
	p.handleStatusReport(partial)
	s := p.Status()

	// Fields from the partial update.
	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}

	// Temperature fields from the first update should be preserved
	// (the second report has no Temperature section).
	if s.BedTemp == nil || *s.BedTemp != 55.0 {
		t.Errorf("BedTemp = %v; want 55.0 (preserved)", s.BedTemp)
	}
	if s.NozzleTemp == nil || *s.NozzleTemp != 210.0 {
		t.Errorf("NozzleTemp = %v; want 210.0 (preserved)", s.NozzleTemp)
	}
	if s.NozzleTargetTemp == nil || *s.NozzleTargetTemp != 220.0 {
		t.Errorf("NozzleTargetTemp = %v; want 220.0 (preserved)", s.NozzleTargetTemp)
	}
}

func TestHandleStatusReport_ErrorState(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	// Test 1: error via flags
	report := &apiPrinterResponse{
		State: &stateReport{
			Text: "Operational",
			Flags: &stateFlags{
				Error: true,
			},
		},
	}
	p.handleStatusReport(report)
	s := p.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q (error via flags)", s.State, "error")
	}

	// Test 2: error via text
	p2 := New(config.PrinterDef{ID: "test2", Name: "Test2"})
	report2 := &apiPrinterResponse{
		State: &stateReport{
			Text: "Error",
		},
	}
	p2.handleStatusReport(report2)
	s2 := p2.Status()

	if s2.State != "error" {
		t.Errorf("State = %q; want %q (error via text)", s2.State, "error")
	}
}

func TestHandleQueryReport_FullUpdate(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	report := &moonrakerQueryResponse{}
	report.Result.Status = &queryStatus{
		PrintStats: &printStatsReport{
			Filename:      "model.gcode",
			PrintDuration: 100.0,
			State:         "printing",
			Info: &printStatsInfo{
				CurrentLayer: 42,
				TotalLayer:   65,
			},
		},
		VirtualSDCard: &virtualSDCardReport{
			Progress: 0.5,
		},
	}

	p.handleQueryReport(report)
	s := p.Status()

	if s.CurrentFile != "model.gcode" {
		t.Errorf("CurrentFile = %q; want %q", s.CurrentFile, "model.gcode")
	}
	if s.CurrentLayer != 42 {
		t.Errorf("CurrentLayer = %d; want 42", s.CurrentLayer)
	}
	if s.TotalLayers != 65 {
		t.Errorf("TotalLayers = %d; want 65", s.TotalLayers)
	}
	if s.Progress != 0.5 {
		t.Errorf("Progress = %f; want 0.5", s.Progress)
	}
}

func TestHandleQueryReport_PreservesExistingState(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})

	// Set some initial status state.
	p.setStatus(printers.PrinterStatus{
		ID:     "test",
		Name:   "Test",
		Type:   "snapmaker",
		Online: true,
		State:  "printing",
	})

	report := &moonrakerQueryResponse{}
	report.Result.Status = &queryStatus{
		PrintStats: &printStatsReport{
			Filename: "newfile.gcode",
		},
		VirtualSDCard: &virtualSDCardReport{
			Progress: 0.75,
		},
	}

	p.handleQueryReport(report)
	s := p.Status()

	// Query data should be set.
	if s.CurrentFile != "newfile.gcode" {
		t.Errorf("CurrentFile = %q; want %q", s.CurrentFile, "newfile.gcode")
	}
	if s.Progress != 0.75 {
		t.Errorf("Progress = %f; want 0.75", s.Progress)
	}
	// State should be preserved from initial setStatus.
	if s.State != "printing" {
		t.Errorf("State = %q; want %q (preserved)", s.State, "printing")
	}
}

func TestHandleQueryReport_NilReport(t *testing.T) {
	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.setStatus(printers.PrinterStatus{ID: "test", Name: "Test", Online: true, State: "idle"})

	// Must not panic.
	p.handleQueryReport(nil)
	s := p.Status()
	if s.State != "idle" {
		t.Errorf("State = %q; want %q", s.State, "idle")
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
		fmt.Fprint(w, `{"state":{"text":"Operational","flags":{"operational":true,"ready":true}}}`)
	}))
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	status, err := p.fetchStatus(context.Background())
	if err != nil {
		t.Fatalf("fetchStatus() returned error: %v", err)
	}
	if status.State == nil {
		t.Fatal("State is nil")
	}
	if status.State.Text != "Operational" {
		t.Errorf("State.Text = %q; want %q", status.State.Text, "Operational")
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

func TestFetchQueryStatus_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printer/objects/query" {
			t.Errorf("path = %q; want %q", r.URL.Path, "/printer/objects/query")
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"result":{"status":{"print_stats":{"filename":"model.gcode","print_duration":100.0,"state":"printing","info":{"current_layer":5,"total_layer":100}},"virtual_sdcard":{"progress":0.5}}}}`)
	}))
	defer ts.Close()

	p := New(config.PrinterDef{ID: "test", Name: "Test"})
	p.testBaseURL = ts.URL
	p.httpClient = ts.Client()

	report, err := p.fetchQueryStatus(context.Background())
	if err != nil {
		t.Fatalf("fetchQueryStatus() returned error: %v", err)
	}
	if report.Result.Status == nil {
		t.Fatal("Result.Status is nil")
	}
	if report.Result.Status.PrintStats == nil {
		t.Fatal("PrintStats is nil")
	}
	if report.Result.Status.PrintStats.Filename != "model.gcode" {
		t.Errorf("Filename = %q; want %q", report.Result.Status.PrintStats.Filename, "model.gcode")
	}
	if report.Result.Status.VirtualSDCard == nil {
		t.Fatal("VirtualSDCard is nil")
	}
	if report.Result.Status.VirtualSDCard.Progress != 0.5 {
		t.Errorf("Progress = %f; want 0.5", report.Result.Status.VirtualSDCard.Progress)
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
// U1 with Moonraker-style API. It serves:
//   - GET /api/printer → returns Moonraker-style status JSON
//   - GET /printer/objects/query → returns Moonraker query JSON
//   - WebSocket /websocket → pushes status JSON messages
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
			"state": map[string]any{
				"text": "Operational",
				"flags": map[string]any{
					"operational": true, "paused": false, "printing": false,
					"cancelling": false, "pausing": false, "error": false,
					"ready": true, "closedOrError": false,
				},
			},
		})
	})

	mux.HandleFunc("/printer/objects/query", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"print_stats": map[string]any{
						"filename":       "",
						"print_duration": 0,
						"state":          "",
						"message":        "",
					},
					"virtual_sdcard": map[string]any{
						"progress": 0,
					},
				},
			},
		})
	})

	mux.HandleFunc("/websocket", func(w http.ResponseWriter, r *http.Request) {
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

	// Send a status update via WebSocket (apiPrinterResponse format).
	mock.sendWS(t, map[string]any{
		"temperature": map[string]any{
			"bed":   map[string]any{"actual": 55.0, "offset": 0, "target": 60.0},
			"tool0": map[string]any{"actual": 210.0, "offset": 0, "target": 220.0},
		},
		"state": map[string]any{
			"text": "Printing",
			"flags": map[string]any{
				"printing": true,
			},
		},
	})

	// Give Connect time to process the message.
	time.Sleep(50 * time.Millisecond)

	s := p.Status()
	if s.State != "printing" {
		t.Errorf("after WS: State = %q; want %q", s.State, "printing")
	}
	if s.BedTemp == nil || *s.BedTemp != 55.0 {
		t.Errorf("after WS: BedTemp = %v; want 55.0", s.BedTemp)
	}
	if s.NozzleTemp == nil || *s.NozzleTemp != 210.0 {
		t.Errorf("after WS: NozzleTemp = %v; want 210.0", s.NozzleTemp)
	}
	if s.BedTargetTemp == nil || *s.BedTargetTemp != 60.0 {
		t.Errorf("after WS: BedTargetTemp = %v; want 60.0", s.BedTargetTemp)
	}
	if s.NozzleTargetTemp == nil || *s.NozzleTargetTemp != 220.0 {
		t.Errorf("after WS: NozzleTargetTemp = %v; want 220.0", s.NozzleTargetTemp)
	}
	if !s.Online {
		t.Error("after WS: Online = false; want true")
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
		"temperature": map[string]any{
			"bed":   map[string]any{"actual": 55.0, "offset": 0, "target": 60.0},
			"tool0": map[string]any{"actual": 210.0, "offset": 0, "target": 220.0},
		},
		"state": map[string]any{
			"text": "Printing",
			"flags": map[string]any{
				"printing": true,
			},
		},
	})

	time.Sleep(50 * time.Millisecond)

	// Second message: partial update (state only).
	mock.sendWS(t, map[string]any{
		"state": map[string]any{
			"text": "Printing",
			"flags": map[string]any{
				"printing": true,
			},
		},
	})

	time.Sleep(50 * time.Millisecond)

	s := p.Status()

	// State updated.
	if s.State != "printing" {
		t.Errorf("after partial: State = %q; want %q", s.State, "printing")
	}

	// Preserved fields from first update (no Temperature in second message).
	if s.BedTemp == nil || *s.BedTemp != 55.0 {
		t.Errorf("after partial: BedTemp = %v; want 55.0 (preserved)", s.BedTemp)
	}
	if s.NozzleTemp == nil || *s.NozzleTemp != 210.0 {
		t.Errorf("after partial: NozzleTemp = %v; want 210.0 (preserved)", s.NozzleTemp)
	}
	if s.NozzleTargetTemp == nil || *s.NozzleTargetTemp != 220.0 {
		t.Errorf("after partial: NozzleTargetTemp = %v; want 220.0 (preserved)", s.NozzleTargetTemp)
	}

	if err := stopConnect(mock, cancel, errCh); err != context.Canceled {
		t.Errorf("Connect() returned %v; want %v", err, context.Canceled)
	}
}
