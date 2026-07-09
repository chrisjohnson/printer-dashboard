package snapmaker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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
			name: "with host only (default port 8080)",
			cfg: config.PrinterDef{
				Host: "10.0.0.50",
				Port: 0,
			},
			want: []string{"http://10.0.0.50:8080/camera"},
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
