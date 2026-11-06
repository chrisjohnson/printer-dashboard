package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

func cameraMock() *mockPrinterWithCameras {
	return &mockPrinterWithCameras{
		MockPrinter: MockPrinter{id: "p1s", name: "P1S"},
		cameras: []printers.CameraStream{
			{URL: "bambus://192.168.1.15:6000?token=64894711", Type: "internal", Label: "Camera"},
		},
	}
}

// TestEnrichWithStreams_DisabledInstanceAdvertisesNothing is the core guarantee:
// with DISABLE_CAMERAS set, no camera stream reaches the API no matter what the
// driver or config would have offered — so nothing downstream can try to dial a
// single-session camera port this instance has no business holding.
func TestEnrichWithStreams_DisabledInstanceAdvertisesNothing(t *testing.T) {
	driverStreams := []printers.CameraStream{
		{URL: "bambus://192.168.1.15:6000?token=64894711", Type: "internal", Label: "Camera"},
	}
	status := printers.PrinterStatus{ID: "p1s", Name: "P1S", Type: "bambu"}

	t.Run("disabled", func(t *testing.T) {
		s := newTestServer(nil)
		s.setCamerasOn(false)

		got := s.enrichWithStreams(driverStreams, "p1s", status)
		if len(got.CameraStreams) != 0 {
			t.Errorf("CameraStreams = %+v; want none on a disabled instance", got.CameraStreams)
		}
	})

	t.Run("enabled control", func(t *testing.T) {
		// Without this the test above could pass for the wrong reason.
		s := newTestServer(nil)
		got := s.enrichWithStreams(driverStreams, "p1s", status)
		if len(got.CameraStreams) == 0 {
			t.Fatal("control failed: streams dropped even with cameras enabled")
		}
	})
}

// TestCameraRoutesAreNotRegisteredWhenDisabled closes the hole the status gate
// alone left: camera.Handler and FrameHandlerWithCache call GetBuffer directly,
// so a stale browser tab, a bookmark, or any scripted request for a camera URL
// would still dial the printer and steal its single-session camera slot even
// from an instance configured never to touch cameras.
func TestCameraRoutesAreNotRegisteredWhenDisabled(t *testing.T) {
	s := newTestServer(map[string]printers.Printer{"p1s": cameraMock()})
	s.setCamerasOn(false)

	for _, path := range []string{"/api/camera/proxy", "/api/camera/frame"} {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?url=bambus%3A%2F%2F10.0.0.1%3A6000", nil))

		if rec.Code == http.StatusOK {
			t.Errorf("%s returned 200 on a disabled instance; the route is still live", path)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: bad JSON %q", path, rec.Body.String())
			continue
		}
		if body["disabled"] != true {
			t.Errorf("%s: body = %v; want an explicit disabled verdict", path, body)
		}
	}
}

// TestHandleCameraStatus_DisabledIsNotReportedAsFailure: "disabled here" and
// "the printer's camera is broken" are different facts. Conflating them is what
// made the stale-container incident read as a printer fault.
func TestHandleCameraStatus_DisabledIsNotReportedAsFailure(t *testing.T) {
	s := newTestServer(map[string]printers.Printer{"p1s": cameraMock()})
	s.setCamerasOn(false)

	req := httptest.NewRequest(http.MethodGet, "/api/printers/p1s/camera-status", nil)
	req.SetPathValue("id", "p1s")
	rec := httptest.NewRecorder()
	s.handleCameraStatus(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v (%s)", err, rec.Body.String())
	}
	if body["disabled"] != true {
		t.Errorf("body = %v; want disabled:true so the client can stay quiet", body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "disabled") {
		t.Errorf("error = %q; want it to name the toggle, not the printer", msg)
	}
}

// TestHandleIndex_SetsCamerasDisabledFlag checks the flag reaches the page. The
// frontend is an embedded string with no build step, so a template typo would
// silently leave the camera block rendering.
func TestHandleIndex_SetsCamerasDisabledFlag(t *testing.T) {
	build := func(disabled bool) string {
		s := newTestServer(map[string]printers.Printer{"p1s": cameraMock()})
		s.setCamerasOn(!disabled)
		rec := httptest.NewRecorder()
		s.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}

	if got := build(true); !strings.Contains(got, "window._camerasEnabled = false;") {
		t.Error("index with cameras disabled did not set the JS flag")
	}
	if got := build(false); !strings.Contains(got, "window._camerasEnabled = true;") {
		t.Error("index with cameras enabled did not set the JS flag to false")
	}
}

// TestHandleListPrinters_LeaksWithCameraStreams guards the whole API surface at
// once: if streams ever leak through, every browser tab starts dialing a port
// that silently drops them and shows a bogus printer error.
func TestHandleListPrinters_NoStreamLeakWhenDisabled(t *testing.T) {
	s := newTestServer(map[string]printers.Printer{"p1s": cameraMock()})
	s.setCamerasOn(false)

	rec := httptest.NewRecorder()
	s.handleListPrinters(rec, httptest.NewRequest(http.MethodGet, "/api/printers", nil))

	var payload struct {
		Printers []printers.PrinterStatus `json:"printers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Printers) != 1 {
		t.Fatalf("printers = %d, want 1", len(payload.Printers))
	}
	if len(payload.Printers[0].CameraStreams) != 0 {
		t.Errorf("API leaked camera streams on a disabled instance: %+v", payload.Printers[0].CameraStreams)
	}
}

// ── runtime toggle ───────────────────────────────────────────────────────────

// TestSetCamerasOnFlipsStateWithoutRestart is the point of the toggle: the env
// var only picks the starting value, and the operator can look at the video on a
// dev instance without editing anything or bouncing the container.
func TestSetCamerasOnFlipsStateWithoutRestart(t *testing.T) {
	s := newTestServer(map[string]printers.Printer{"p1s": cameraMock()})

	if !s.camerasOn() {
		t.Fatal("a test server should start with cameras on")
	}
	s.setCamerasOn(false)
	if s.camerasOn() {
		t.Error("cameras still on after setCamerasOn(false)")
	}
	if len(s.enrichWithStreams(cameraMock().CameraStreams(), "p1s", printers.PrinterStatus{}).CameraStreams) != 0 {
		t.Error("streams still advertised after disabling at runtime")
	}

	s.setCamerasOn(true)
	if !s.camerasOn() {
		t.Error("cameras did not come back on")
	}
	if len(s.enrichWithStreams(cameraMock().CameraStreams(), "p1s", printers.PrinterStatus{}).CameraStreams) == 0 {
		t.Error("streams not advertised again after re-enabling")
	}
}

// TestSetCamerasOnIsIdempotent: the poll re-applies the server value on every
// refresh, so re-setting the current state must not tear down and rebuild live
// camera connections each time.
func TestSetCamerasOnIsIdempotent(t *testing.T) {
	s := newTestServer(nil)
	s.setCamerasOn(true)
	s.setCamerasOn(true)
	s.setCamerasOn(false)
	s.setCamerasOn(false)
	if s.camerasOn() {
		t.Error("final state should be off")
	}
}

func TestHandleSetCamerasAPI(t *testing.T) {
	s := newTestServer(map[string]printers.Printer{"p1s": cameraMock()})

	post := func(body string) map[string]any {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/cameras", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		s.handleSetCameras(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	if got := post(`{"enabled":false}`); got["enabled"] != false {
		t.Errorf("disable response = %v", got)
	}
	if got := post(`{"enabled":true}`); got["enabled"] != true {
		t.Errorf("enable response = %v", got)
	}
	if rec := httptest.NewRecorder(); func() bool {
		s.handleSetCameras(rec, httptest.NewRequest(http.MethodPost, "/api/cameras", strings.NewReader(`{}`)))
		return rec.Code == http.StatusBadRequest
	}() != true {
		t.Error("missing \"enabled\" should be a 400, not silently accepted")
	}
}

// TestHandleListPrintersReportsCameraState keeps the poll in sync with reality so
// a second tab reflects a flip made in the first, rather than each browser
// holding a private opinion about a shared printer resource.
func TestHandleListPrintersReportsCameraState(t *testing.T) {
	s := newTestServer(map[string]printers.Printer{"p1s": cameraMock()})

	read := func() (any, any) {
		rec := httptest.NewRecorder()
		s.handleListPrinters(rec, httptest.NewRequest(http.MethodGet, "/api/printers", nil))
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return payload["cameras_enabled"], payload["cameras_sessions"]
	}

	if on, _ := read(); on != true {
		t.Errorf("cameras_enabled = %v, want true", on)
	}
	s.setCamerasOn(false)
	if on, _ := read(); on != false {
		t.Errorf("cameras_enabled = %v after disabling, want false", on)
	}
}
