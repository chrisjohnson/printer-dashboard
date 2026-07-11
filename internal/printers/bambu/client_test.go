package bambu

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// ---------------------------------------------------------------------------
// MQTT mock types
// ---------------------------------------------------------------------------

// mockMQTTToken implements mqtt.Token for testing.
type mockMQTTToken struct {
	mqtt.Token
	doneCh chan struct{}
	err    error
}

func (t *mockMQTTToken) Done() <-chan struct{}                { return t.doneCh }
func (t *mockMQTTToken) Error() error                         { return t.err }
func (t *mockMQTTToken) Wait() bool                           { <-t.doneCh; return true }
func (t *mockMQTTToken) WaitTimeout(d time.Duration) bool {
	timer := time.NewTimer(d)
	select {
	case <-t.doneCh:
		if !timer.Stop() {
			<-timer.C
		}
		return true
	case <-timer.C:
		return false
	}
}

// mockMQTTClient implements the subset of mqtt.Client used by our code.
type mockMQTTClient struct {
	mqtt.Client
	isConnected  bool
	subscribeFn  func(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token
	publishFn    func(topic string, qos byte, retained bool, payload interface{}) mqtt.Token
	disconnectFn func(quiesce uint)
}

func (c *mockMQTTClient) IsConnected() bool { return c.isConnected }

func (c *mockMQTTClient) Subscribe(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token {
	if c.subscribeFn != nil {
		return c.subscribeFn(topic, qos, callback)
	}
	return &mockMQTTToken{doneCh: closedCh()}
}

func (c *mockMQTTClient) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token {
	if c.publishFn != nil {
		return c.publishFn(topic, qos, retained, payload)
	}
	return &mockMQTTToken{doneCh: closedCh()}
}

func (c *mockMQTTClient) Disconnect(quiesce uint) {
	if c.disconnectFn != nil {
		c.disconnectFn(quiesce)
	}
}

// mockMQTTMessage implements mqtt.Message for testing.
type mockMQTTMessage struct {
	mqtt.Message
	payload   []byte
	topic     string
	duplicate bool
	qos       byte
	retained  bool
}

func (m *mockMQTTMessage) Duplicate() bool  { return m.duplicate }
func (m *mockMQTTMessage) Qos() byte         { return m.qos }
func (m *mockMQTTMessage) Retained() bool    { return m.retained }
func (m *mockMQTTMessage) Topic() string     { return m.topic }
func (m *mockMQTTMessage) MessageID() uint16 { return 0 }
func (m *mockMQTTMessage) Payload() []byte   { return m.payload }
func (m *mockMQTTMessage) Ack()              {}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// closedCh returns an already-closed channel, useful for mocking success tokens.
func closedCh() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// testPrinterClientConfig is the default config used in most tests.
var testPrinterClientConfig = config.PrinterDef{
	ID:         "test-id",
	Name:       "Test Printer",
	Type:       "bambu",
	Serial:     "SERIAL001",
	Host:       "10.0.0.1",
	AccessCode: "1234",
}

// newTestPrinterClient creates a Client with a known config and optional MQTT mock.
// Pass nil for mc when the mock is not needed.
func newTestPrinterClient(mc mqtt.Client) *Client {
	c := New(testPrinterClientConfig, nil)
	c.mqttClient = mc
	return c
}

// newMockMessage creates a mock MQTT message with the given payload.
func newMockMessage(payload []byte) *mockMQTTMessage {
	return &mockMQTTMessage{payload: payload}
}

// ---------------------------------------------------------------------------
// Initial status tests
// ---------------------------------------------------------------------------

func TestClient_InitialStatus(t *testing.T) {
	c := newTestPrinterClient(nil)
	s := c.Status()

	if s.ID != "test-id" {
		t.Errorf("Status().ID = %q; want %q", s.ID, "test-id")
	}
	if s.Name != "Test Printer" {
		t.Errorf("Status().Name = %q; want %q", s.Name, "Test Printer")
	}
	if s.Type != "bambu" {
		t.Errorf("Status().Type = %q; want %q", s.Type, "bambu")
	}
	if s.Online {
		t.Error("Status().Online = true; want false for initial status")
	}
	if s.State != "" {
		t.Errorf("Status().State = %q; want %q (initial state is zero value)", s.State, "")
	}
	if s.Progress != 0 {
		t.Errorf("Status().Progress = %f; want 0", s.Progress)
	}
	if s.ErrorMsg != "" {
		t.Errorf("Status().ErrorMsg = %q; want empty", s.ErrorMsg)
	}
}

// ---------------------------------------------------------------------------
// Camera URL tests
// ---------------------------------------------------------------------------

func TestClient_CameraStreams(t *testing.T) {
	c := newTestPrinterClient(nil)

	streams := c.CameraStreams()
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1", len(streams))
	}
	expected := "bambus://10.0.0.1:6000?token=1234"
	if streams[0].URL != expected {
		t.Errorf("CameraStreams()[0].URL = %q; want %q", streams[0].URL, expected)
	}
	if streams[0].Type != "internal" {
		t.Errorf("CameraStreams()[0].Type = %q; want %q", streams[0].Type, "internal")
	}
	if streams[0].Label != "Camera" {
		t.Errorf("CameraStreams()[0].Label = %q; want %q", streams[0].Label, "Camera")
	}
}

func TestClient_CameraStreams_NoHost(t *testing.T) {
	cfg := config.PrinterDef{
		ID:         "test-id-no-cam",
		Name:       "No Camera",
		Type:       "bambu",
		Serial:     "SERIAL002",
		AccessCode: "1234",
		// Host is empty
	}
	c := New(cfg, nil)

	streams := c.CameraStreams()
	if len(streams) != 0 {
		t.Errorf("CameraStreams() returned %d streams; want 0", len(streams))
	}
}

func TestClient_CameraStreams_NoAccessCode(t *testing.T) {
	cfg := config.PrinterDef{
		ID:     "test-id-no-ac",
		Name:   "No Access Code",
		Type:   "bambu",
		Serial: "SERIAL003",
		Host:   "10.0.0.2",
		// AccessCode is empty
	}
	c := New(cfg, nil)

	streams := c.CameraStreams()
	if len(streams) != 0 {
		t.Errorf("CameraStreams() returned %d streams; want 0", len(streams))
	}
}

// ---------------------------------------------------------------------------
// CameraStreams — H2S multi-camera tests
// ---------------------------------------------------------------------------

func TestClient_CameraStreams_H2S_ConfigFallback(t *testing.T) {
	cfg := config.PrinterDef{
		ID:         "h2s-config",
		Name:       "H2S Config",
		Type:       "bambu",
		Serial:     "SERIAL-H2S-1",
		Host:       "10.0.0.50",
		AccessCode: "d5a78d50",
		Model:      "H2S",
	}
	c := New(cfg, nil)

	streams := c.CameraStreams()
	if len(streams) != 2 {
		t.Fatalf("CameraStreams() returned %d streams; want 2 (RTSPS x2)", len(streams))
	}

	// BirdsEye camera (live/1)
	if streams[0].URL != "rtsps://bblp:d5a78d50@10.0.0.50:322/streaming/live/1" {
		t.Errorf("streams[0].URL = %q; want rtsps URL for live/1", streams[0].URL)
	}
	if streams[0].Label != "BirdsEye Camera" {
		t.Errorf("streams[0].Label = %q; want %q", streams[0].Label, "BirdsEye Camera")
	}
	if streams[0].Type != "internal" {
		t.Errorf("streams[0].Type = %q; want %q", streams[0].Type, "internal")
	}

	// Toolhead camera (live/2)
	if streams[1].URL != "rtsps://bblp:d5a78d50@10.0.0.50:322/streaming/live/2" {
		t.Errorf("streams[1].URL = %q; want rtsps URL for live/2", streams[1].URL)
	}
	if streams[1].Label != "Toolhead Camera" {
		t.Errorf("streams[1].Label = %q; want %q", streams[1].Label, "Toolhead Camera")
	}
}

func TestClient_CameraStreams_H2S_MQTTCamURL(t *testing.T) {
	cfg := config.PrinterDef{
		ID:         "h2s-mqtt",
		Name:       "H2S MQTT",
		Type:       "bambu",
		Serial:     "SERIAL-H2S-2",
		Host:       "10.0.0.50",
		AccessCode: "d5a78d50",
		Model:      "H2S",
	}
	c := New(cfg, nil)

	// Simulate MQTT reporting ipcam_url ending with /streaming/live/1
	c.mu.Lock()
	c.camIPCamURL = "rtsps://bblp:d5a78d50@10.0.0.50:322/streaming/live/1"
	c.mu.Unlock()

	streams := c.CameraStreams()
	if len(streams) != 2 {
		t.Fatalf("CameraStreams() returned %d streams; want 2 (Camera + derived Toolhead)", len(streams))
	}

	// First stream is the MQTT URL directly
	if streams[0].URL != "rtsps://bblp:d5a78d50@10.0.0.50:322/streaming/live/1" {
		t.Errorf("streams[0].URL = %q; want MQTT URL", streams[0].URL)
	}
	if streams[0].Label != "Camera" {
		t.Errorf("streams[0].Label = %q; want %q", streams[0].Label, "Camera")
	}

	// Second stream is derived from the MQTT URL (live/1 → live/2)
	expectedToolhead := "rtsps://bblp:d5a78d50@10.0.0.50:322/streaming/live/2"
	if streams[1].URL != expectedToolhead {
		t.Errorf("streams[1].URL = %q; want %q", streams[1].URL, expectedToolhead)
	}
	if streams[1].Label != "Toolhead Camera" {
		t.Errorf("streams[1].Label = %q; want %q", streams[1].Label, "Toolhead Camera")
	}
}

func TestClient_CameraStreams_H2S_MQTTCamURL_NoLive1(t *testing.T) {
	// If the MQTT URL doesn't contain /streaming/live/1, we should NOT
	// derive a second stream even for H2S.
	cfg := config.PrinterDef{
		ID:         "h2s-nolive1",
		Name:       "H2S NoLive1",
		Type:       "bambu",
		Serial:     "SERIAL-H2S-3",
		Model:      "H2S",
	}
	c := New(cfg, nil)

	c.mu.Lock()
	c.camIPCamURL = "rtsps://bblp:x@10.0.0.50:322/something/else"
	c.mu.Unlock()

	streams := c.CameraStreams()
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1 (no derivation from non-live/1 URL)", len(streams))
	}
	if streams[0].URL != "rtsps://bblp:x@10.0.0.50:322/something/else" {
		t.Errorf("streams[0].URL = %q; want MQTT URL", streams[0].URL)
	}
}

func TestClient_CameraStreams_P1S_SingleCamera(t *testing.T) {
	cfg := config.PrinterDef{
		ID:         "p1s-config",
		Name:       "P1S",
		Type:       "bambu",
		Serial:     "SERIAL-P1S-1",
		Host:       "10.0.0.10",
		AccessCode: "abcdef",
		Model:      "P1S",
	}
	c := New(cfg, nil)

	streams := c.CameraStreams()
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1", len(streams))
	}
	if streams[0].URL != "bambus://10.0.0.10:6000?token=abcdef" {
		t.Errorf("streams[0].URL = %q; want bambus:// URL", streams[0].URL)
	}
	if streams[0].Label != "Camera" {
		t.Errorf("streams[0].Label = %q; want %q", streams[0].Label, "Camera")
	}
}

func TestClient_CameraStreams_UnknownModel_DefaultsToP1S(t *testing.T) {
	cfg := config.PrinterDef{
		ID:         "unknown-model",
		Name:       "Unknown Model",
		Type:       "bambu",
		Serial:     "SERIAL-UNK-1",
		Host:       "10.0.0.20",
		AccessCode: "111111",
		Model:      "SomeUnknownPrinter",
	}
	c := New(cfg, nil)

	streams := c.CameraStreams()
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1 (default to P1S behavior)", len(streams))
	}
	if streams[0].URL != "bambus://10.0.0.20:6000?token=111111" {
		t.Errorf("streams[0].URL = %q; want bambus:// URL (P1S default)", streams[0].URL)
	}
}

func TestClient_CameraStreams_EmptyModel_DefaultsToP1S(t *testing.T) {
	// No model set — should default to P1S single-camera behavior
	cfg := config.PrinterDef{
		ID:         "no-model",
		Name:       "No Model",
		Type:       "bambu",
		Serial:     "SERIAL-NM-1",
		Host:       "10.0.0.30",
		AccessCode: "222222",
	}
	c := New(cfg, nil)

	streams := c.CameraStreams()
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1", len(streams))
	}
	if streams[0].URL != "bambus://10.0.0.30:6000?token=222222" {
		t.Errorf("streams[0].URL = %q; want bambus:// URL", streams[0].URL)
	}
}

func TestClient_CameraStreams_H2DSeries(t *testing.T) {
	// Verify all H2-series models get multi-camera behavior
	for _, model := range []string{"H2S", "H2D", "H2C", "H2D PRO", "P2S", "X2D"} {
		cfg := config.PrinterDef{
			ID:         "h2-series",
			Name:       model + " Test",
			Type:       "bambu",
			Serial:     "SERIAL-H2-1",
			Host:       "10.0.0.99",
			AccessCode: "testac",
			Model:      model,
		}
		c := New(cfg, nil)

		streams := c.CameraStreams()
		if len(streams) != 2 {
			t.Errorf("Model %q: CameraStreams() returned %d streams; want 2 (RTSPS x2)", model, len(streams))
			continue
		}
		if !strings.Contains(streams[0].URL, "live/1") {
			t.Errorf("Model %q: streams[0].URL = %q; want live/1", model, streams[0].URL)
		}
		if !strings.Contains(streams[1].URL, "live/2") {
			t.Errorf("Model %q: streams[1].URL = %q; want live/2", model, streams[1].URL)
		}
	}
}

func TestClient_CameraStreams_IPCamURLTakesPriority_OverConfig(t *testing.T) {
	// Even with config fallback available, MQTT ipcam_url should win.
	cfg := config.PrinterDef{
		ID:         "pref-test",
		Name:       "Pref Test",
		Type:       "bambu",
		Serial:     "SERIAL-PREF-1",
		Host:       "10.0.0.1",
		AccessCode: "1234",
		Model:      "P1S",
	}
	c := New(cfg, nil)

	c.mu.Lock()
	c.camIPCamURL = "rtsp://mqtt-priority/stream"
	c.mu.Unlock()

	streams := c.CameraStreams()
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1", len(streams))
	}
	if streams[0].URL != "rtsp://mqtt-priority/stream" {
		t.Errorf("streams[0].URL = %q; want MQTT ipcam_url", streams[0].URL)
	}
}

func TestHandleReport_CapturesIPCamURL(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Report with camera data (no print section).
	payload := []byte(`{
		"camera": {
			"ipcam_url": "rtsp://camera.example.com/stream"
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))

	streams := c.CameraStreams()
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1", len(streams))
	}
	if streams[0].URL != "rtsp://camera.example.com/stream" {
		t.Errorf("CameraStreams()[0].URL = %q; want %q", streams[0].URL, "rtsp://camera.example.com/stream")
	}
	if streams[0].Type != "internal" {
		t.Errorf("CameraStreams()[0].Type = %q; want %q", streams[0].Type, "internal")
	}
	if streams[0].Label != "Camera" {
		t.Errorf("CameraStreams()[0].Label = %q; want %q", streams[0].Label, "Camera")
	}

	// IPCamURL should take precedence over host/access code based URL.
	// Set up a client with host+access code, send camera report, verify IPCamURL wins.
	c2 := New(config.PrinterDef{
		ID:         "test-id-pref",
		Name:       "Test Pref",
		Type:       "bambu",
		Serial:     "SERIAL004",
		Host:       "10.0.0.1",
		AccessCode: "1234",
	}, nil)

	payload2 := []byte(`{
		"camera": {
			"ipcam_url": "rtsp://preferred-stream/feed"
		}
	}`)
	c2.handleReport(nil, newMockMessage(payload2))

	streams2 := c2.CameraStreams()
	if len(streams2) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1", len(streams2))
	}
	if streams2[0].URL != "rtsp://preferred-stream/feed" {
		t.Errorf("CameraStreams()[0].URL = %q; want %q (IPCamURL should take precedence)", streams2[0].URL, "rtsp://preferred-stream/feed")
	}
}

// ---------------------------------------------------------------------------
// handleReport tests
// ---------------------------------------------------------------------------

func TestHandleReport_FullStatusUpdate(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode",
			"mc_percent": 75,
			"mc_remaining_time": 1800,
			"bed_temper": 55.5,
			"bed_target_temper": 60.0,
			"nozzle_temper": 210.0,
			"nozzle_target_temper": 220.0,
			"chamber_temper": 30.0,
			"layer_num": 15,
			"total_layer_num": 100,
			"print_error": 0
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
	if s.CurrentFile != "model.gcode" {
		t.Errorf("CurrentFile = %q; want %q", s.CurrentFile, "model.gcode")
	}
	if s.Progress != 0.75 {
		t.Errorf("Progress = %f; want 0.75", s.Progress)
	}
	if s.RemainingTime != 1800 {
		t.Errorf("RemainingTime = %d; want 1800", s.RemainingTime)
	}
	if s.BedTemp == nil || *s.BedTemp != 55.5 {
		t.Errorf("BedTemp = %v; want 55.5", s.BedTemp)
	}
	if s.BedTargetTemp == nil || *s.BedTargetTemp != 60.0 {
		t.Errorf("BedTargetTemp = %v; want 60.0", s.BedTargetTemp)
	}
	if s.NozzleTemp == nil || *s.NozzleTemp != 210.0 {
		t.Errorf("NozzleTemp = %v; want 210.0", s.NozzleTemp)
	}
	if s.NozzleTargetTemp == nil || *s.NozzleTargetTemp != 220.0 {
		t.Errorf("NozzleTargetTemp = %v; want 220.0", s.NozzleTargetTemp)
	}
	if s.ChamberTemp == nil || *s.ChamberTemp != 30.0 {
		t.Errorf("ChamberTemp = %v; want 30.0", s.ChamberTemp)
	}
	if s.CurrentLayer != 15 {
		t.Errorf("CurrentLayer = %d; want 15", s.CurrentLayer)
	}
	if s.TotalLayers != 100 {
		t.Errorf("TotalLayers = %d; want 100", s.TotalLayers)
	}
	if !s.Online {
		t.Error("Online = false; want true after report")
	}
	if s.ErrorMsg != "" {
		t.Errorf("ErrorMsg = %q; want empty for successful print", s.ErrorMsg)
	}
}

func TestHandleReport_NilBedTemper(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Set initial temperature values.
	c.mu.Lock()
	c.status.BedTemp = float64Ptr(55.0)
	c.status.NozzleTemp = float64Ptr(200.0)
	c.mu.Unlock()

	// Send a report that omits bed_temper and nozzle_temper
	// (intentionally absent keys — Go's JSON decoder will leave *float64 fields nil).
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode",
			"mc_percent": 50,
			"mc_remaining_time": 3600,
			"bed_target_temper": 60.0,
			"nozzle_target_temper": 220.0,
			"chamber_temper": 28.0,
			"layer_num": 5,
			"total_layer_num": 50,
			"print_error": 0
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	// Previous values should be preserved.
	if s.BedTemp == nil || *s.BedTemp != 55.0 {
		t.Errorf("BedTemp = %v; want 55.0 (should preserve previous value)", s.BedTemp)
	}
	if s.NozzleTemp == nil || *s.NozzleTemp != 200.0 {
		t.Errorf("NozzleTemp = %v; want 200.0 (should preserve previous value)", s.NozzleTemp)
	}
	// Other fields should still update.
	if s.BedTargetTemp == nil || *s.BedTargetTemp != 60.0 {
		t.Errorf("BedTargetTemp = %v; want 60.0", s.BedTargetTemp)
	}
	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
}

func TestHandleReport_ChamberTempFallback(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Report without chamber_temper but with info.temp (H2S-style).
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "test.gcode",
			"mc_percent": 25,
			"mc_remaining_time": 7200,
			"nozzle_temper": 200.0,
			"home_flag": 0,
			"info": {
				"temp": 28.5
			}
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp == nil || *s.ChamberTemp != 28.5 {
		t.Errorf("ChamberTemp = %v; want 28.5 (from info.temp fallback)", s.ChamberTemp)
	}
}

func TestHandleReport_ChamberTempDirectPreferred(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Report with both chamber_temper and info.temp — chamber_temper should win.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "test.gcode",
			"mc_percent": 25,
			"mc_remaining_time": 7200,
			"nozzle_temper": 200.0,
			"chamber_temper": 30.0,
			"home_flag": 0,
			"info": {
				"temp": 28.5
			}
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp == nil || *s.ChamberTemp != 30.0 {
		t.Errorf("ChamberTemp = %v; want 30.0 (chamber_temper should take priority)", s.ChamberTemp)
	}
}

func TestHandleReport_ChamberTempPreservedWhenMissing(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Set initial chamber temp.
	c.mu.Lock()
	c.status.ChamberTemp = float64Ptr(25.0)
	c.mu.Unlock()

	// Report with neither chamber_temper nor info.temp.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "test.gcode",
			"mc_percent": 10,
			"mc_remaining_time": 1000,
			"nozzle_temper": 200.0
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp == nil || *s.ChamberTemp != 25.0 {
		t.Errorf("ChamberTemp = %v; want 25.0 (should preserve previous value)", s.ChamberTemp)
	}
}

func TestHandleReport_FailedState(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload := []byte(`{
		"print": {
			"gcode_state": "FAILED",
			"gcode_file": "failed.gcode",
			"print_error": 503
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q", s.State, "error")
	}
	if s.ErrorMsg != "print_error=503" {
		t.Errorf("ErrorMsg = %q; want %q", s.ErrorMsg, "print_error=503")
	}
}

func TestHandleReport_FailedStateWithoutPrintError(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload := []byte(`{
		"print": {
			"gcode_state": "FAILED",
			"gcode_file": "failed.gcode"
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q", s.State, "error")
	}
	// When FAILED without print_error, ErrorMsg should not be set.
	if s.ErrorMsg != "" {
		t.Errorf("ErrorMsg = %q; want empty when FAILED without print_error", s.ErrorMsg)
	}
}

func TestHandleReport_PrintErrorOnly(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Non-zero print_error with RUNNING state should also trigger error.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "print.gcode",
			"print_error": 123,
			"mc_percent": 50,
			"mc_remaining_time": 1800
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q", s.State, "error")
	}
	if s.ErrorMsg != "print_error=123" {
		t.Errorf("ErrorMsg = %q; want %q", s.ErrorMsg, "print_error=123")
	}
}

func TestHandleReport_PrintErrorZero(t *testing.T) {
	c := newTestPrinterClient(nil)

	// print_error=0 should NOT trigger error state.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "print.gcode",
			"print_error": 0,
			"mc_percent": 50,
			"mc_remaining_time": 1800
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
	if s.ErrorMsg != "" {
		t.Errorf("ErrorMsg = %q; want empty when print_error=0", s.ErrorMsg)
	}
}

func TestHandleReport_NoPrintSection(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Set initial state to something recognisable.
	c.setStatus(printers.PrinterStatus{
		ID:     "test-id",
		Name:   "Test Printer",
		Type:   "bambu",
		State:  "printing",
		Online: true,
	})

	// Send report without a print section (e.g. only camera info).
	payload := []byte(`{
		"camera": {
			"ipcam_url": "rtsp://camera"
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	// Status should be unchanged.
	if s.State != "printing" {
		t.Errorf("State = %q; want %q (unchanged)", s.State, "printing")
	}
	if !s.Online {
		t.Error("Online = false; want true (unchanged)")
	}
}

func TestHandleReport_MultipleUpdates(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: 50% progress.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode",
			"mc_percent": 50,
			"mc_remaining_time": 3600,
			"layer_num": 10,
			"total_layer_num": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()

	if s1.Progress != 0.50 {
		t.Errorf("After first report: Progress = %f; want 0.50", s1.Progress)
	}
	if s1.CurrentLayer != 10 {
		t.Errorf("After first report: CurrentLayer = %d; want 10", s1.CurrentLayer)
	}

	// Second report: 75% progress, layer 30.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode",
			"mc_percent": 75,
			"mc_remaining_time": 1800,
			"layer_num": 30,
			"total_layer_num": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.Progress != 0.75 {
		t.Errorf("After second report: Progress = %f; want 0.75", s2.Progress)
	}
	if s2.CurrentLayer != 30 {
		t.Errorf("After second report: CurrentLayer = %d; want 30", s2.CurrentLayer)
	}
	if s2.RemainingTime != 1800 {
		t.Errorf("After second report: RemainingTime = %d; want 1800", s2.RemainingTime)
	}
}

func TestHandleReport_StateDefaultIdle(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Empty gcode_state should map to "idle".
	payload := []byte(`{
		"print": {
			"gcode_state": "",
			"gcode_file": "",
			"mc_percent": 0
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "idle" {
		t.Errorf("State = %q; want %q (empty gcode_state maps to idle)", s.State, "idle")
	}
}

func TestHandleReport_ErrorClearedOnNormalState(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: error.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "FAILED",
			"print_error": 503
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.State != "error" {
		t.Fatalf("After FAILED: State = %q; want %q", s1.State, "error")
	}

	// Second report: normal running (no error).
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode",
			"mc_percent": 10,
			"mc_remaining_time": 3600,
			"print_error": 0
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "printing" {
		t.Errorf("After RUNNING: State = %q; want %q", s2.State, "printing")
	}
	if s2.ErrorMsg != "" {
		t.Errorf("After RUNNING: ErrorMsg = %q; want empty", s2.ErrorMsg)
	}
}

func TestHandleReport_ErrorPreservedOnSubsequentError(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: error with specific message.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "FAILED",
			"print_error": 503
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.ErrorMsg != "print_error=503" {
		t.Fatalf("After first error: ErrorMsg = %q; want %q", s1.ErrorMsg, "print_error=503")
	}

	// Second report: different error.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 999
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "error" {
		t.Errorf("After second error: State = %q; want %q", s2.State, "error")
	}
	if s2.ErrorMsg != "print_error=999" {
		t.Errorf("After second error: ErrorMsg = %q; want %q", s2.ErrorMsg, "print_error=999")
	}
}

func TestHandleReport_ProgressRounding(t *testing.T) {
	c := newTestPrinterClient(nil)

	// mc_percent=33 should give progress=0.33 (integer division handled by conversion to float64 first).
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"mc_percent": 33,
			"mc_remaining_time": 100
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.Progress != 0.33 {
		t.Errorf("Progress = %f; want 0.33", s.Progress)
	}
}

func TestHandleReport_ParseError(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Malformed JSON — handleReport should log and return without changing status.
	c.setStatus(printers.PrinterStatus{
		ID:     "test-id",
		Name:   "Test Printer",
		Type:   "bambu",
		State:  "printing",
		Online: true,
	})

	payload := []byte(`{bad json`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "printing" {
		t.Errorf("State = %q; want %q (unchanged after parse error)", s.State, "printing")
	}
}

func TestHandleReport_BootSequenceFlicker(t *testing.T) {
	c := newTestPrinterClient(nil)

	// P1S boot sequence: empty → SUCCESS → IDLE
	// Step 1: first heartbeat, no meaningful gcode_state
	payload1 := []byte(`{
		"print": {
			"gcode_state": "",
			"gcode_file": "",
			"mc_percent": 0
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()

	if s1.State != "idle" {
		t.Errorf("After empty gcode_state: State = %q; want %q", s1.State, "idle")
	}
	if !s1.Online {
		t.Error("After empty gcode_state: Online = false; want true")
	}

	// Step 2: previous print's SUCCESS stored in NVRAM
	payload2 := []byte(`{
		"print": {
			"gcode_state": "SUCCESS",
			"gcode_file": "",
			"mc_percent": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "complete" {
		t.Errorf("After SUCCESS: State = %q; want %q", s2.State, "complete")
	}
	if s2.ErrorMsg != "" {
		t.Errorf("After SUCCESS: ErrorMsg = %q; want empty", s2.ErrorMsg)
	}

	// Step 3: device finishes booting, reports IDLE
	payload3 := []byte(`{
		"print": {
			"gcode_state": "IDLE",
			"gcode_file": "",
			"mc_percent": 0
		}
	}`)
	c.handleReport(nil, newMockMessage(payload3))
	s3 := c.Status()

	if s3.State != "idle" {
		t.Errorf("After IDLE: State = %q; want %q", s3.State, "idle")
	}
	if s3.ErrorMsg != "" {
		t.Errorf("After IDLE: ErrorMsg = %q; want empty", s3.ErrorMsg)
	}
}

func TestHandleReport_FullPrintLifecycle(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Step 1: IDLE
	payload1 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()

	if s1.State != "idle" {
		t.Errorf("Step 1: State = %q; want %q", s1.State, "idle")
	}

	// Step 2: RUNNING with full details
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "benchy.gcode",
			"mc_percent": 10,
			"mc_remaining_time": 3600,
			"layer_num": 1,
			"total_layer_num": 100,
			"bed_temper": 55,
			"nozzle_temper": 210,
			"bed_target_temper": 60,
			"nozzle_target_temper": 220
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "printing" {
		t.Errorf("Step 2: State = %q; want %q", s2.State, "printing")
	}
	if s2.Progress != 0.10 {
		t.Errorf("Step 2: Progress = %f; want 0.10", s2.Progress)
	}
	if s2.RemainingTime != 3600 {
		t.Errorf("Step 2: RemainingTime = %d; want 3600", s2.RemainingTime)
	}
	if s2.CurrentFile != "benchy.gcode" {
		t.Errorf("Step 2: CurrentFile = %q; want %q", s2.CurrentFile, "benchy.gcode")
	}
	if s2.CurrentLayer != 1 {
		t.Errorf("Step 2: CurrentLayer = %d; want 1", s2.CurrentLayer)
	}
	if s2.TotalLayers != 100 {
		t.Errorf("Step 2: TotalLayers = %d; want 100", s2.TotalLayers)
	}
	if s2.BedTemp == nil || *s2.BedTemp != 55.0 {
		t.Errorf("Step 2: BedTemp = %v; want 55.0", s2.BedTemp)
	}
	if s2.NozzleTemp == nil || *s2.NozzleTemp != 210.0 {
		t.Errorf("Step 2: NozzleTemp = %v; want 210.0", s2.NozzleTemp)
	}
	if s2.BedTargetTemp == nil || *s2.BedTargetTemp != 60.0 {
		t.Errorf("Step 2: BedTargetTemp = %v; want 60.0", s2.BedTargetTemp)
	}
	if s2.NozzleTargetTemp == nil || *s2.NozzleTargetTemp != 220.0 {
		t.Errorf("Step 2: NozzleTargetTemp = %v; want 220.0", s2.NozzleTargetTemp)
	}

	// Step 3: PAUSE (minimal report — no progress/temp updates)
	payload3 := []byte(`{
		"print": {
			"gcode_state": "PAUSE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload3))
	s3 := c.Status()

	if s3.State != "paused" {
		t.Errorf("Step 3: State = %q; want %q", s3.State, "paused")
	}
	// Progress should be preserved from step 2
	if s3.Progress != 0.10 {
		t.Errorf("Step 3: Progress = %f; want 0.10 (preserved from step 2)", s3.Progress)
	}
	// CurrentFile should be preserved
	if s3.CurrentFile != "benchy.gcode" {
		t.Errorf("Step 3: CurrentFile = %q; want %q (preserved from step 2)", s3.CurrentFile, "benchy.gcode")
	}

	// Step 4: RUNNING again (progress updated)
	payload4 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"mc_percent": 60,
			"mc_remaining_time": 1400,
			"layer_num": 42
		}
	}`)
	c.handleReport(nil, newMockMessage(payload4))
	s4 := c.Status()

	if s4.State != "printing" {
		t.Errorf("Step 4: State = %q; want %q", s4.State, "printing")
	}
	if s4.Progress != 0.60 {
		t.Errorf("Step 4: Progress = %f; want 0.60", s4.Progress)
	}
	if s4.RemainingTime != 1400 {
		t.Errorf("Step 4: RemainingTime = %d; want 1400", s4.RemainingTime)
	}
	if s4.CurrentLayer != 42 {
		t.Errorf("Step 4: CurrentLayer = %d; want 42", s4.CurrentLayer)
	}
	// Fields not sent in this report should be preserved
	if s4.CurrentFile != "benchy.gcode" {
		t.Errorf("Step 4: CurrentFile = %q; want %q (preserved)", s4.CurrentFile, "benchy.gcode")
	}
	if s4.TotalLayers != 100 {
		t.Errorf("Step 4: TotalLayers = %d; want 100 (preserved)", s4.TotalLayers)
	}
	if s4.BedTemp == nil || *s4.BedTemp != 55.0 {
		t.Errorf("Step 4: BedTemp = %v; want 55.0 (preserved)", s4.BedTemp)
	}

	// Step 5: SUCCESS (job complete)
	payload5 := []byte(`{
		"print": {
			"gcode_state": "SUCCESS",
			"mc_percent": 100,
			"mc_remaining_time": 0
		}
	}`)
	c.handleReport(nil, newMockMessage(payload5))
	s5 := c.Status()

	if s5.State != "complete" {
		t.Errorf("Step 5: State = %q; want %q", s5.State, "complete")
	}
	if s5.Progress != 1.0 {
		t.Errorf("Step 5: Progress = %f; want 1.0", s5.Progress)
	}
	if s5.RemainingTime != 0 {
		t.Errorf("Step 5: RemainingTime = %d; want 0", s5.RemainingTime)
	}

	// Step 6: IDLE (back to idle after completion)
	payload6 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload6))
	s6 := c.Status()

	if s6.State != "idle" {
		t.Errorf("Step 6: State = %q; want %q", s6.State, "idle")
	}
	if s6.ErrorMsg != "" {
		t.Errorf("Step 6: ErrorMsg = %q; want empty", s6.ErrorMsg)
	}
}

// ---------------------------------------------------------------------------
// publishCommand tests
// ---------------------------------------------------------------------------

func TestPublishCommand_NotConnected_NilClient(t *testing.T) {
	c := newTestPrinterClient(nil)
	// Ensure mqttClient is nil.
	c.mqttClient = nil

	err := c.publishCommand(context.Background(), []byte("test"))
	if err == nil {
		t.Fatal("expected error for nil MQTT client")
	}
	if err.Error() != "bambu test-id: not connected to cloud MQTT" {
		t.Errorf("error = %q; want %q", err.Error(), "bambu test-id: not connected to cloud MQTT")
	}
}

func TestPublishCommand_NotConnected_Disconnected(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{isConnected: false}

	err := c.publishCommand(context.Background(), []byte("test"))
	if err == nil {
		t.Fatal("expected error for disconnected client")
	}
	if err.Error() != "bambu test-id: not connected to cloud MQTT" {
		t.Errorf("error = %q; want %q", err.Error(), "bambu test-id: not connected to cloud MQTT")
	}
}

func TestPublishCommand_ConnectedSuccess(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, _ interface{}) mqtt.Token {
			return &mockMQTTToken{doneCh: closedCh()}
		},
	}

	err := c.publishCommand(context.Background(), []byte("test"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPublishCommand_Timeout(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, _ interface{}) mqtt.Token {
			// A channel that never closes causes WaitTimeout to return false.
			return &mockMQTTToken{doneCh: make(chan struct{})}
		},
	}

	err := c.publishCommand(context.Background(), []byte("test"))
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err.Error() != "bambu test-id: command publish timeout" {
		t.Errorf("error = %q; want %q", err.Error(), "bambu test-id: command publish timeout")
	}
}

func TestPublishCommand_Error(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, _ interface{}) mqtt.Token {
			return &mockMQTTToken{
				doneCh: closedCh(),
				err:    errors.New("broker unavailable"),
			}
		},
	}

	err := c.publishCommand(context.Background(), []byte("test"))
	if err == nil {
		t.Fatal("expected error from token")
	}
	if err.Error() != "broker unavailable" {
		t.Errorf("error = %q; want %q", err.Error(), "broker unavailable")
	}
}

// ---------------------------------------------------------------------------
// Command delegation tests
// ---------------------------------------------------------------------------

func TestCommandDelegation_Pause(t *testing.T) {
	var capturedPayload []byte
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, payload interface{}) mqtt.Token {
			capturedPayload = payload.([]byte)
			return &mockMQTTToken{doneCh: closedCh()}
		},
	}

	err := c.Pause(context.Background())
	if err != nil {
		t.Fatalf("Pause() returned error: %v", err)
	}

	expected := pauseCommand()
	if string(capturedPayload) != string(expected) {
		t.Errorf("Pause() payload = %q; want %q", string(capturedPayload), string(expected))
	}
}

func TestCommandDelegation_Resume(t *testing.T) {
	var capturedPayload []byte
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, payload interface{}) mqtt.Token {
			capturedPayload = payload.([]byte)
			return &mockMQTTToken{doneCh: closedCh()}
		},
	}

	err := c.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume() returned error: %v", err)
	}

	expected := resumeCommand()
	if string(capturedPayload) != string(expected) {
		t.Errorf("Resume() payload = %q; want %q", string(capturedPayload), string(expected))
	}
}

func TestCommandDelegation_Cancel(t *testing.T) {
	var capturedPayload []byte
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, payload interface{}) mqtt.Token {
			capturedPayload = payload.([]byte)
			return &mockMQTTToken{doneCh: closedCh()}
		},
	}

	err := c.Cancel(context.Background())
	if err != nil {
		t.Fatalf("Cancel() returned error: %v", err)
	}

	expected := stopCommand()
	if string(capturedPayload) != string(expected) {
		t.Errorf("Cancel() payload = %q; want %q", string(capturedPayload), string(expected))
	}
}

func TestCommandDelegation_SkipObject(t *testing.T) {
	var capturedPayload []byte
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, payload interface{}) mqtt.Token {
			capturedPayload = payload.([]byte)
			return &mockMQTTToken{doneCh: closedCh()}
		},
	}

	err := c.SkipObject(context.Background())
	if err != nil {
		t.Fatalf("SkipObject() returned error: %v", err)
	}

	expected := skipObjectCommand()
	if string(capturedPayload) != string(expected) {
		t.Errorf("SkipObject() payload = %q; want %q", string(capturedPayload), string(expected))
	}
}

// ---------------------------------------------------------------------------
// ID / Name accessor tests
// ---------------------------------------------------------------------------

func TestClient_ID(t *testing.T) {
	c := newTestPrinterClient(nil)
	if id := c.ID(); id != "test-id" {
		t.Errorf("ID() = %q; want %q", id, "test-id")
	}
}

func TestClient_Name(t *testing.T) {
	c := newTestPrinterClient(nil)
	if name := c.Name(); name != "Test Printer" {
		t.Errorf("Name() = %q; want %q", name, "Test Printer")
	}
}

// ---------------------------------------------------------------------------
// SetModel tests
// ---------------------------------------------------------------------------

func TestClient_SetModel(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Initially model is empty (testPrinterClientConfig has no Model)
	c.mu.RLock()
	if c.model != "" {
		t.Errorf("initial model = %q; want empty", c.model)
	}
	c.mu.RUnlock()

	c.SetModel("H2S")

	c.mu.RLock()
	if c.model != "H2S" {
		t.Errorf("after SetModel: model = %q; want %q", c.model, "H2S")
	}
	c.mu.RUnlock()

	// Verify the model affects CameraStreams behavior
	cfg := config.PrinterDef{
		ID:         "setmodel-cam",
		Name:       "SetModel Cam",
		Type:       "bambu",
		Serial:     "SERIAL-SM-1",
		Host:       "10.0.0.5",
		AccessCode: "pass123",
	}
	c2 := New(cfg, nil)
	c2.SetModel("H2S")

	streams := c2.CameraStreams()
	if len(streams) != 2 {
		t.Fatalf("After SetModel(H2S): CameraStreams() returned %d streams; want 2 (RTSPS x2)", len(streams))
	}
	if !strings.Contains(streams[0].URL, "live/1") {
		t.Errorf("streams[0].URL = %q; want live/1", streams[0].URL)
	}
	if !strings.Contains(streams[1].URL, "live/2") {
		t.Errorf("streams[1].URL = %q; want live/2", streams[1].URL)
	}
}

// ---------------------------------------------------------------------------
// isH2S tests
// ---------------------------------------------------------------------------

func TestIsH2S(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"H2S", true},
		{"h2s", true},
		{"H2D", true},
		{"H2C", true},
		{"H2D PRO", true},
		{"P2S", true},
		{"X2D", true},
		{"O1S", true},   // Bambu Cloud API internal code for H2S
		{"P1S", false},
		{"A1", false},
		{"X1C", false},
		{"X1E", false},
		{"", false},
		{"SomeUnknown", false},
		{"h2d pro", true}, // case-insensitive
	}

	for _, tt := range tests {
		got := IsH2S(tt.model)
		if got != tt.want {
			t.Errorf("IsH2S(%q) = %v; want %v", tt.model, got, tt.want)
		}
	}
}

func TestNew_PrepopulatesModelFromConfig(t *testing.T) {
	cfg := config.PrinterDef{
		ID:     "model-pop",
		Name:   "Model Pop",
		Type:   "bambu",
		Serial: "SERIAL-MP-1",
		Model:  "H2S",
	}
	c := New(cfg, nil)

	c.mu.RLock()
	got := c.model
	c.mu.RUnlock()

	if got != "H2S" {
		t.Errorf("New() model = %q; want %q (should pre-populate from config)", got, "H2S")
	}
}
