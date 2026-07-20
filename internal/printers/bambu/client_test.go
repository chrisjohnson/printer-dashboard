package bambu

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
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

func (t *mockMQTTToken) Done() <-chan struct{} { return t.doneCh }
func (t *mockMQTTToken) Error() error          { return t.err }
func (t *mockMQTTToken) Wait() bool            { <-t.doneCh; return true }
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

func (m *mockMQTTMessage) Duplicate() bool   { return m.duplicate }
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
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1 (BirdsEye only — H2S has a single physical camera)", len(streams))
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
	if len(streams) != 1 {
		t.Fatalf("CameraStreams() returned %d streams; want 1 (Camera only — H2S has a single physical camera)", len(streams))
	}

	// The stream is the MQTT URL directly
	if streams[0].URL != "rtsps://bblp:d5a78d50@10.0.0.50:322/streaming/live/1" {
		t.Errorf("streams[0].URL = %q; want MQTT URL", streams[0].URL)
	}
	if streams[0].Label != "Camera" {
		t.Errorf("streams[0].Label = %q; want %q", streams[0].Label, "Camera")
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
	// Verify all H2-series models get the RTSPS camera stream (a single
	// physical camera — see CameraStreams for why no second stream is
	// derived).
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
		if len(streams) != 1 {
			t.Errorf("Model %q: CameraStreams() returned %d streams; want 1 (RTSPS)", model, len(streams))
			continue
		}
		if !strings.Contains(streams[0].URL, "live/1") {
			t.Errorf("Model %q: streams[0].URL = %q; want live/1", model, streams[0].URL)
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
	if s.RemainingTime != 108000 {
		t.Errorf("RemainingTime = %d; want 108000 (1800 minutes * 60)", s.RemainingTime)
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

	// Report without chamber_temper but with info.temp (H2S-style packed integer).
	// 3932188 = (60 << 16) | 28 → current = 28°C, target = 60°C.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "test.gcode",
			"mc_percent": 25,
			"mc_remaining_time": 7200,
			"nozzle_temper": 200.0,
			"home_flag": 0,
			"info": {
				"temp": 3932188
			}
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp == nil || *s.ChamberTemp != 28.0 {
		t.Errorf("ChamberTemp = %v; want 28.0 (from info.temp packed-integer decode)", s.ChamberTemp)
	}
	if s.ChamberTargetTemp == nil || *s.ChamberTargetTemp != 60.0 {
		t.Errorf("ChamberTargetTemp = %v; want 60.0 (from info.temp packed-integer decode)", s.ChamberTargetTemp)
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
	if s2.RemainingTime != 108000 {
		t.Errorf("After second report: RemainingTime = %d; want 108000 (1800 minutes * 60)", s2.RemainingTime)
	}
}

func TestHandleReport_StateDefaultIdle(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Empty gcode_state should NOT overwrite state — it preserves whatever
	// was set before. On a fresh client the initial state is "".
	payload := []byte(`{
		"print": {
			"gcode_state": "",
			"gcode_file": "",
			"mc_percent": 0
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "" {
		t.Errorf("State = %q; want %q (empty gcode_state should not change state)", s.State, "")
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

// ---------------------------------------------------------------------------
// HMS handling tests
// ---------------------------------------------------------------------------

func TestHandleReport_HMS_WarningOnly_StateStaysHealthy(t *testing.T) {
	c := newTestPrinterClient(nil)

	// severity "common" (code>>16 == 3) — module byte 0x0C (xcam), matches
	// the pybambu oracle sample used in parser_test.go.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "print.gcode",
			"print_error": 0,
			"hms": [{"attr": 201327360, "code": 196615}]
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "printing" {
		t.Errorf("State = %q; want %q (HMS warning-only must not trip error)", s.State, "printing")
	}
	if len(s.HMSErrors) != 0 {
		t.Errorf("HMSErrors = %+v; want empty", s.HMSErrors)
	}
	if len(s.HMSWarnings) != 1 {
		t.Fatalf("HMSWarnings len = %d; want 1", len(s.HMSWarnings))
	}
	if s.HMSWarnings[0].Severity != "common" {
		t.Errorf("HMSWarnings[0].Severity = %q; want %q", s.HMSWarnings[0].Severity, "common")
	}
	if s.HMSWarnings[0].Module != "xcam" {
		t.Errorf("HMSWarnings[0].Module = %q; want %q", s.HMSWarnings[0].Module, "xcam")
	}
	if s.ErrorMsg != "" {
		t.Errorf("ErrorMsg = %q; want empty", s.ErrorMsg)
	}
}

func TestHandleReport_HMS_FatalTripsError_PrintErrorZero(t *testing.T) {
	c := newTestPrinterClient(nil)

	// severity "fatal" (code>>16 == 1), module byte 0x05 (mainboard).
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "print.gcode",
			"print_error": 0,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q", s.State, "error")
	}
	if len(s.HMSErrors) != 1 {
		t.Fatalf("HMSErrors len = %d; want 1", len(s.HMSErrors))
	}
	if s.HMSErrors[0].Severity != "fatal" {
		t.Errorf("HMSErrors[0].Severity = %q; want %q", s.HMSErrors[0].Severity, "fatal")
	}
	if s.ErrorMsg != s.HMSErrors[0].Code {
		t.Errorf("ErrorMsg = %q; want HMS code %q (fallback since print_error is 0)", s.ErrorMsg, s.HMSErrors[0].Code)
	}
}

func TestHandleReport_HMS_SeriousTripsError(t *testing.T) {
	c := newTestPrinterClient(nil)

	// severity "serious" (code>>16 == 2), module byte 0x08 (toolhead).
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"hms": [{"attr": 134217728, "code": 131072}]
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q", s.State, "error")
	}
	if len(s.HMSErrors) != 1 || s.HMSErrors[0].Severity != "serious" {
		t.Fatalf("HMSErrors = %+v; want one serious entry", s.HMSErrors)
	}
}

// TestHandleReport_HMS_CoverOffScenario reproduces the actual failure mode
// this card is about: print_error stays 0, gcode_state stays RUNNING
// throughout, but an HMS fatal/serious entry shows up mid-stream (e.g. a
// force/vibration-disturbance code on a P1S with no door sensor). The
// dashboard must flip to State="error" from the HMS channel alone.
func TestHandleReport_HMS_CoverOffScenario(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Healthy reports first, print_error=0 and gcode_state=RUNNING throughout.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "benchy.gcode",
			"print_error": 0,
			"mc_percent": 40
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.State != "printing" {
		t.Fatalf("Before HMS: State = %q; want %q", s1.State, "printing")
	}

	// Mid-stream: HMS fatal entry appears, print_error and gcode_state
	// unchanged (both still "healthy").
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "benchy.gcode",
			"print_error": 0,
			"mc_percent": 41,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "error" {
		t.Errorf("After HMS fatal: State = %q; want %q (print_error=0, gcode_state=RUNNING should not matter)", s2.State, "error")
	}
	if len(s2.HMSErrors) != 1 {
		t.Fatalf("After HMS fatal: HMSErrors len = %d; want 1", len(s2.HMSErrors))
	}
	if s2.ErrorMsg == "" {
		t.Error("After HMS fatal: ErrorMsg is empty; want HMS-derived summary")
	}
}

func TestHandleReport_HMS_PrintErrorPrecedenceOverHMS(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Both print_error (nonzero) and an HMS fatal entry present simultaneously
	// — print_error's message must win (backward compat), HMS summary is
	// only the fallback when print_error itself produced no message.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 503,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.State != "error" {
		t.Errorf("State = %q; want %q", s.State, "error")
	}
	if s.ErrorMsg != "print_error=503" {
		t.Errorf("ErrorMsg = %q; want %q (print_error takes precedence over HMS summary)", s.ErrorMsg, "print_error=503")
	}
	if len(s.HMSErrors) != 1 {
		t.Errorf("HMSErrors len = %d; want 1 (still populated even though ErrorMsg came from print_error)", len(s.HMSErrors))
	}
}

func TestHandleReport_HMS_AbsentFieldDoesNotWipeExisting(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: HMS fatal entry present, trips error.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if len(s1.HMSErrors) != 1 {
		t.Fatalf("After first report: HMSErrors len = %d; want 1", len(s1.HMSErrors))
	}

	// Second report: a heartbeat-style report with the "hms" key entirely
	// absent from the JSON (not an empty array) — must NOT wipe the existing
	// HMSErrors/HMSWarnings.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"mc_percent": 42
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if len(s2.HMSErrors) != 1 {
		t.Errorf("After absent hms field: HMSErrors len = %d; want 1 (preserved, not wiped)", len(s2.HMSErrors))
	}
	if s2.State != "error" {
		t.Errorf("After absent hms field: State = %q; want %q (preserved)", s2.State, "error")
	}
}

func TestHandleReport_HMS_ClearsOnEmptyPresentArray(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: HMS fatal entry present, trips error.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.State != "error" || len(s1.HMSErrors) != 1 {
		t.Fatalf("After first report: State=%q HMSErrors=%+v; want error/1 entry", s1.State, s1.HMSErrors)
	}

	// Second report: hms explicitly present but empty ([]), plus healthy
	// print_error/gcode_state — this is the recovery signal and must clear
	// both HMSErrors and HMSWarnings.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"mc_percent": 50,
			"hms": []
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if len(s2.HMSErrors) != 0 {
		t.Errorf("After empty-present hms: HMSErrors = %+v; want empty (cleared)", s2.HMSErrors)
	}
	if len(s2.HMSWarnings) != 0 {
		t.Errorf("After empty-present hms: HMSWarnings = %+v; want empty (cleared)", s2.HMSWarnings)
	}
	if s2.State != "printing" {
		t.Errorf("After empty-present hms: State = %q; want %q (recovered)", s2.State, "printing")
	}
	if s2.ErrorMsg != "" {
		t.Errorf("After empty-present hms: ErrorMsg = %q; want empty", s2.ErrorMsg)
	}
}

// TestHandleReport_HMS_ResolvedInFirmwareEventuallyUnlatches covers the
// primary K-072 step 6b regression: firmware simply stops sending the "hms"
// key at all once a condition resolves (no explicit "hms: []" ever arrives),
// while gcode_state keeps reporting a healthy value. A single such report
// must NOT clear state (see TestHandleReport_HMS_AbsentFieldDoesNotWipeExisting),
// but enough consecutive ones must eventually decay the stale HMSErrors and
// let State recover — otherwise the dashboard is stuck on "error" forever.
func TestHandleReport_HMS_ResolvedInFirmwareEventuallyUnlatches(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: HMS fatal entry present, trips error.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.State != "error" || len(s1.HMSErrors) != 1 {
		t.Fatalf("After first report: State=%q HMSErrors=%+v; want error/1 entry", s1.State, s1.HMSErrors)
	}

	// Firmware never sends "hms" again — condition resolved, but only via
	// omission, not an explicit empty array. gcode_state stays healthy.
	healthyNoHMS := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"mc_percent": 55
		}
	}`)

	// One such report is not enough on its own (matches the existing
	// single-heartbeat protection) — HMSErrors/State must still be latched.
	c.handleReport(nil, newMockMessage(healthyNoHMS))
	sAfterOne := c.Status()
	if len(sAfterOne.HMSErrors) != 1 {
		t.Fatalf("After 1 healthy/no-hms report: HMSErrors len = %d; want 1 (not yet decayed)", len(sAfterOne.HMSErrors))
	}
	if sAfterOne.State != "error" {
		t.Fatalf("After 1 healthy/no-hms report: State = %q; want %q (not yet decayed)", sAfterOne.State, "error")
	}

	// A second consecutive healthy/no-hms report crosses the threshold and
	// must decay the stale HMSErrors, un-latching State.
	c.handleReport(nil, newMockMessage(healthyNoHMS))
	sAfterTwo := c.Status()
	if len(sAfterTwo.HMSErrors) != 0 {
		t.Errorf("After 2 healthy/no-hms reports: HMSErrors = %+v; want empty (decayed)", sAfterTwo.HMSErrors)
	}
	if sAfterTwo.State != "printing" {
		t.Errorf("After 2 healthy/no-hms reports: State = %q; want %q (recovered)", sAfterTwo.State, "printing")
	}
	if sAfterTwo.ErrorMsg != "" {
		t.Errorf("After 2 healthy/no-hms reports: ErrorMsg = %q; want empty", sAfterTwo.ErrorMsg)
	}
}

// TestHandleReport_HMS_UnhealthyGcodeStateDoesNotCountTowardDecay ensures the
// decay streak only advances on reports where gcode_state is BOTH present and
// healthy — a report with gcode_state absent, or present but itself
// unhealthy (e.g. FAILED), must not count toward the threshold, and must
// reset any streak already in progress.
func TestHandleReport_HMS_UnhealthyGcodeStateDoesNotCountTowardDecay(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))

	healthyNoHMS := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0
		}
	}`)
	// First healthy/no-hms report builds streak = 1.
	c.handleReport(nil, newMockMessage(healthyNoHMS))

	// A report with gcode_state entirely absent resets the streak instead of
	// advancing it.
	heartbeatNoGcodeState := []byte(`{
		"print": {
			"mc_percent": 60
		}
	}`)
	c.handleReport(nil, newMockMessage(heartbeatNoGcodeState))

	// One more healthy/no-hms report only brings the streak back to 1, not
	// past the threshold — HMSErrors must still be latched.
	c.handleReport(nil, newMockMessage(healthyNoHMS))
	s := c.Status()
	if len(s.HMSErrors) != 1 {
		t.Errorf("HMSErrors len = %d; want 1 (streak was reset by the absent-gcode_state report, not yet decayed)", len(s.HMSErrors))
	}
	if s.State != "error" {
		t.Errorf("State = %q; want %q (not yet decayed)", s.State, "error")
	}
}

// TestHandleReport_HMS_ExplicitClearWithAbsentGcodeStateUnlatchesState covers
// the narrower secondary case flagged in K-072 step 6b: firmware DOES send an
// explicit "hms: []" to clear the condition, but that same report is a
// heartbeat that also omits gcode_state entirely. Without special handling,
// the normal "if p.GcodeState != {}" reassignment never runs and State would
// stay stuck on "error" even though HMSErrors itself is correctly cleared.
func TestHandleReport_HMS_ExplicitClearWithAbsentGcodeStateUnlatchesState(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"print_error": 0,
			"hms": [{"attr": 83886080, "code": 65536}]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.State != "error" || len(s1.HMSErrors) != 1 {
		t.Fatalf("After first report: State=%q HMSErrors=%+v; want error/1 entry", s1.State, s1.HMSErrors)
	}

	// hms explicitly cleared, but gcode_state is absent this cycle (a
	// heartbeat-style report).
	payload2 := []byte(`{
		"print": {
			"mc_percent": 61,
			"hms": []
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if len(s2.HMSErrors) != 0 {
		t.Errorf("After explicit hms clear: HMSErrors = %+v; want empty", s2.HMSErrors)
	}
	if s2.State == "error" {
		t.Errorf("After explicit hms clear (gcode_state absent this cycle): State = %q; want NOT %q (HMS was the sole cause and is now cleared)", s2.State, "error")
	}
	if s2.ErrorMsg != "" {
		t.Errorf("After explicit hms clear: ErrorMsg = %q; want empty", s2.ErrorMsg)
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
	// Step 1: first heartbeat, no meaningful gcode_state.
	// State should remain empty (not "idle") since gcode_state is empty.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "",
			"gcode_file": "",
			"mc_percent": 0
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()

	if s1.State != "" {
		t.Errorf("After empty gcode_state: State = %q; want %q", s1.State, "")
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

	// Step 3: device finishes booting, reports IDLE. Because SUCCESS just
	// latched State to "complete", the complete->idle latch requires
	// completeIdleStreakThreshold consecutive IDLE reports before State
	// settles — a single IDLE report here must not flip it yet (see
	// TestHandleReport_SuccessThenSingleIdle_DoesNotDropState).
	payload3 := []byte(`{
		"print": {
			"gcode_state": "IDLE",
			"gcode_file": "",
			"mc_percent": 0
		}
	}`)
	c.handleReport(nil, newMockMessage(payload3))
	s3 := c.Status()

	if s3.State != "complete" {
		t.Errorf("After first IDLE post-SUCCESS: State = %q; want %q (latched)", s3.State, "complete")
	}
	if s3.ErrorMsg != "" {
		t.Errorf("After first IDLE post-SUCCESS: ErrorMsg = %q; want empty", s3.ErrorMsg)
	}

	// Step 4: a second consecutive IDLE report meets the latch threshold and
	// State settles to "idle".
	payload4 := []byte(`{
		"print": {
			"gcode_state": "IDLE",
			"gcode_file": "",
			"mc_percent": 0
		}
	}`)
	c.handleReport(nil, newMockMessage(payload4))
	s4 := c.Status()

	if s4.State != "idle" {
		t.Errorf("After second IDLE post-SUCCESS: State = %q; want %q", s4.State, "idle")
	}
	if s4.ErrorMsg != "" {
		t.Errorf("After second IDLE post-SUCCESS: ErrorMsg = %q; want empty", s4.ErrorMsg)
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
	if s2.RemainingTime != 216000 {
		t.Errorf("Step 2: RemainingTime = %d; want 216000 (3600 minutes * 60)", s2.RemainingTime)
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
	if s4.RemainingTime != 84000 {
		t.Errorf("Step 4: RemainingTime = %d; want 84000 (1400 minutes * 60)", s4.RemainingTime)
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

	// Step 6: IDLE (first report back to idle after completion). The
	// complete->idle latch requires completeIdleStreakThreshold consecutive
	// IDLE reports before State actually settles, so State is still
	// "complete" here (see TestHandleReport_SuccessThenSingleIdle_DoesNotDropState).
	payload6 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload6))
	s6 := c.Status()

	if s6.State != "complete" {
		t.Errorf("Step 6: State = %q; want %q (latched)", s6.State, "complete")
	}
	if s6.ErrorMsg != "" {
		t.Errorf("Step 6: ErrorMsg = %q; want empty", s6.ErrorMsg)
	}

	// Step 7: a second consecutive IDLE report meets the latch threshold and
	// State settles to "idle".
	payload7 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload7))
	s7 := c.Status()

	if s7.State != "idle" {
		t.Errorf("Step 7: State = %q; want %q", s7.State, "idle")
	}
	if s7.ErrorMsg != "" {
		t.Errorf("Step 7: ErrorMsg = %q; want empty", s7.ErrorMsg)
	}
}

// ---------------------------------------------------------------------------
// K-059: Heartbeat-style reports with empty gcode_state should not clobber state
// ---------------------------------------------------------------------------

func TestHandleReport_HeartbeatPreservesPrintingState(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: printer is actively printing.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode",
			"mc_percent": 50,
			"mc_remaining_time": 3600
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.State != "printing" {
		t.Fatalf("After RUNNING: State = %q; want %q", s1.State, "printing")
	}

	// Second report: heartbeat with empty gcode_state but temperature data
	// (typical H2S heartbeat during active print).
	payload2 := []byte(`{
		"print": {
			"gcode_state": "",
			"bed_temper": 55.0,
			"nozzle_temper": 210.0,
			"mc_percent": 55
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "printing" {
		t.Errorf("After heartbeat (empty gcode_state): State = %q; want %q (should preserve previous)",
			s2.State, "printing")
	}
	// Other fields should still update
	if s2.Progress != 0.55 {
		t.Errorf("After heartbeat: Progress = %f; want 0.55", s2.Progress)
	}
	if s2.BedTemp == nil || *s2.BedTemp != 55.0 {
		t.Errorf("After heartbeat: BedTemp = %v; want 55.0", s2.BedTemp)
	}
}

func TestHandleReport_HeartbeatPreservesPausedState(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: paused.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "PAUSE",
			"mc_percent": 50
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))

	// Second report: heartbeat with empty gcode_state.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "",
			"mc_percent": 50
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s := c.Status()

	if s.State != "paused" {
		t.Errorf("After heartbeat (empty gcode_state): State = %q; want %q (should preserve paused)",
			s.State, "paused")
	}
}

func TestHandleReport_ExplicitIdleStillWorks(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Set state to printing.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	if c.Status().State != "printing" {
		t.Fatalf("setup: State = %q; want %q", c.Status().State, "printing")
	}

	// Explicit IDLE should transition back to idle.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s := c.Status()

	if s.State != "idle" {
		t.Errorf("After explicit IDLE: State = %q; want %q", s.State, "idle")
	}
}

// ---------------------------------------------------------------------------
// K-059: H2S info.temp packed-integer decode — current in low 16 bits,
// target in high 16 bits.
// ---------------------------------------------------------------------------

func TestHandleReport_InfoTempPackedInteger(t *testing.T) {
	c := newTestPrinterClient(nil)

	// 3932220: low 16 bits = 60 (current), high 16 bits = 60 (target).
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "test.gcode",
			"nozzle_temper": 200.0,
			"info": {
				"temp": 3932220
			}
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp == nil {
		t.Fatal("ChamberTemp = nil; want 60.0")
	}
	if *s.ChamberTemp != 60.0 {
		t.Errorf("ChamberTemp = %f; want 60.0 (3932220 & 0xFFFF = 60)", *s.ChamberTemp)
	}
	if s.ChamberTargetTemp == nil {
		t.Fatal("ChamberTargetTemp = nil; want 60.0")
	}
	if *s.ChamberTargetTemp != 60.0 {
		t.Errorf("ChamberTargetTemp = %f; want 60.0 (3932220 >> 16 = 60)", *s.ChamberTargetTemp)
	}
}

func TestHandleReport_InfoTempPackedIntegerAsymmetric(t *testing.T) {
	c := newTestPrinterClient(nil)

	// (60 << 16) | 45 = 3932205: current = 45, target = 60.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"info": {
				"temp": 3932205
			}
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp == nil {
		t.Fatal("ChamberTemp = nil; want 45.0")
	}
	if *s.ChamberTemp != 45.0 {
		t.Errorf("ChamberTemp = %f; want 45.0", *s.ChamberTemp)
	}
	if s.ChamberTargetTemp == nil {
		t.Fatal("ChamberTargetTemp = nil; want 60.0")
	}
	if *s.ChamberTargetTemp != 60.0 {
		t.Errorf("ChamberTargetTemp = %f; want 60.0", *s.ChamberTargetTemp)
	}
}

func TestHandleReport_InfoTempOutOfRangeIgnored(t *testing.T) {
	c := newTestPrinterClient(nil)

	// 50000000: low 16 bits = 61568 (out of range), high 16 bits = 762 (out of range).
	// Both current and target should be ignored.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"info": {
				"temp": 50000000
			}
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp != nil {
		t.Errorf("ChamberTemp = %v; want nil (current out of range)", s.ChamberTemp)
	}
	if s.ChamberTargetTemp != nil {
		t.Errorf("ChamberTargetTemp = %v; want nil (target out of range)", s.ChamberTargetTemp)
	}
}

func TestHandleReport_InfoTempPreservesPreviousWhenOutOfRange(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Set initial chamber and target temps.
	c.mu.Lock()
	c.status.ChamberTemp = float64Ptr(25.0)
	c.status.ChamberTargetTemp = float64Ptr(40.0)
	c.mu.Unlock()

	// Report with out-of-range info.temp — should not clobber existing values.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"info": {
				"temp": 50000000
			}
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.ChamberTemp == nil || *s.ChamberTemp != 25.0 {
		t.Errorf("ChamberTemp = %v; want 25.0 (should preserve previous when new value is out of range)", s.ChamberTemp)
	}
	if s.ChamberTargetTemp == nil || *s.ChamberTargetTemp != 40.0 {
		t.Errorf("ChamberTargetTemp = %v; want 40.0 (should preserve previous when new value is out of range)", s.ChamberTargetTemp)
	}
}

// ---------------------------------------------------------------------------
// K-002: P1S subtask_name fallback for CurrentFile
// ---------------------------------------------------------------------------

// TestHandleReport_SubtaskNameFallback_WhenGcodeFileAbsent verifies that when
// gcode_file is absent (nil or empty), subtask_name is used as the fallback
// for CurrentFile. This is the P1S behavior: the printer sends subtask_name
// during printing instead of gcode_file.
func TestHandleReport_SubtaskNameFallback_WhenGcodeFileAbsent(t *testing.T) {
	c := newTestPrinterClient(nil)

	// P1S-style report: gcode_file absent, subtask_name present.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"subtask_name": "benchy_v2.gcode",
			"mc_percent": 42,
			"mc_remaining_time": 1200
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.CurrentFile != "benchy_v2.gcode" {
		t.Errorf("CurrentFile = %q; want %q (should fall back to subtask_name)", s.CurrentFile, "benchy_v2.gcode")
	}
	if s.State != "printing" {
		t.Errorf("State = %q; want %q", s.State, "printing")
	}
}

// TestHandleReport_SubtaskNameFallback_WhenGcodeFileEmpty verifies that an
// explicit empty string for gcode_file still triggers the subtask_name fallback.
func TestHandleReport_SubtaskNameFallback_WhenGcodeFileEmpty(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "",
			"subtask_name": "phone_case.gcode",
			"mc_percent": 10
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.CurrentFile != "phone_case.gcode" {
		t.Errorf("CurrentFile = %q; want %q (empty gcode_file should fall back to subtask_name)", s.CurrentFile, "phone_case.gcode")
	}
}

// TestHandleReport_GcodeFilePreferredOverSubtaskName verifies that gcode_file
// takes precedence when both gcode_file and subtask_name are present. Some
// printer models may send both fields; gcode_file is the canonical source.
func TestHandleReport_GcodeFilePreferredOverSubtaskName(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "primary_file.gcode",
			"subtask_name": "subtask_file.gcode",
			"mc_percent": 50
		}
	}`)

	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.CurrentFile != "primary_file.gcode" {
		t.Errorf("CurrentFile = %q; want %q (gcode_file should take precedence over subtask_name)", s.CurrentFile, "primary_file.gcode")
	}
}

// TestHandleReport_SubtaskNameFallback_PreservedAcrossHeartbeat verifies that
// a file name set via subtask_name fallback is preserved across subsequent
// heartbeat reports that omit both gcode_file and subtask_name.
func TestHandleReport_SubtaskNameFallback_PreservedAcrossHeartbeat(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: P1S-style with subtask_name.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"subtask_name": "dragon.gcode",
			"mc_percent": 30,
			"mc_remaining_time": 2400
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()

	if s1.CurrentFile != "dragon.gcode" {
		t.Fatalf("After first report: CurrentFile = %q; want %q", s1.CurrentFile, "dragon.gcode")
	}

	// Second report: heartbeat that omits both gcode_file and subtask_name.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "",
			"mc_percent": 35
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.CurrentFile != "dragon.gcode" {
		t.Errorf("After heartbeat: CurrentFile = %q; want %q (should be preserved from subtask_name)", s2.CurrentFile, "dragon.gcode")
	}
}

// TestHandleReport_SubtaskNameFallback_NeitherPresent verifies that when
// neither gcode_file nor subtask_name is present, CurrentFile is not touched.
func TestHandleReport_SubtaskNameFallback_NeitherPresent(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Set an initial CurrentFile.
	c.setStatus(printers.PrinterStatus{
		ID:          "test-id",
		Name:        "Test Printer",
		Type:        "bambu",
		CurrentFile: "previous_file.gcode",
	})

	// Report with neither field.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"mc_percent": 50
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.CurrentFile != "previous_file.gcode" {
		t.Errorf("CurrentFile = %q; want %q (should be preserved when neither field present)", s.CurrentFile, "previous_file.gcode")
	}
}

// ---------------------------------------------------------------------------
// K-003: CurrentFile cleared when printer goes idle
// ---------------------------------------------------------------------------

// TestHandleReport_IdleClearsCurrentFile verifies that when the printer
// transitions to an idle state, CurrentFile is cleared.
func TestHandleReport_IdleClearsCurrentFile(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: printer is printing with a file.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "benchy.gcode",
			"mc_percent": 50
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.CurrentFile != "benchy.gcode" {
		t.Fatalf("After RUNNING: CurrentFile = %q; want %q", s1.CurrentFile, "benchy.gcode")
	}

	// Second report: printer goes idle (print completed).
	payload2 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "idle" {
		t.Fatalf("After IDLE: State = %q; want %q", s2.State, "idle")
	}
	if s2.CurrentFile != "" {
		t.Errorf("After IDLE: CurrentFile = %q; want empty (print finished)", s2.CurrentFile)
	}
}

// TestHandleReport_IdleClearsCurrentFile_P1S_SubtaskName covers the P1S
// path where the filename came from subtask_name (not gcode_file). The
// same idle-clearing behavior must apply.
func TestHandleReport_IdleClearsCurrentFile_P1S_SubtaskName(t *testing.T) {
	c := newTestPrinterClient(nil)

	// P1S-style report with subtask_name.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"subtask_name": "dragon.gcode",
			"mc_percent": 90
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	if c.Status().CurrentFile != "dragon.gcode" {
		t.Fatalf("After RUNNING: CurrentFile = %q; want %q", c.Status().CurrentFile, "dragon.gcode")
	}

	// STANDBY also maps to idle — verify it clears too.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "STANDBY"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "idle" {
		t.Fatalf("After STANDBY: State = %q; want %q", s2.State, "idle")
	}
	if s2.CurrentFile != "" {
		t.Errorf("After STANDBY: CurrentFile = %q; want empty", s2.CurrentFile)
	}
}

// TestHandleReport_HeartbeatEmptyGcodeFilePreservesCurrentFile verifies that
// a heartbeat report with empty gcode_file while still printing does NOT
// clear CurrentFile.
func TestHandleReport_HeartbeatEmptyGcodeFilePreservesCurrentFile(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: printer is printing.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "model.gcode",
			"mc_percent": 30
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))

	// Second report: heartbeat with empty gcode_file, but state still RUNNING.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "",
			"mc_percent": 35
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.CurrentFile != "model.gcode" {
		t.Errorf("After heartbeat with empty gcode_file: CurrentFile = %q; want %q (preserved)",
			s2.CurrentFile, "model.gcode")
	}
}

// TestHandleReport_NewPrintPopulatesCurrentFile verifies that after a print
// completes and CurrentFile is cleared, a new print populates it again.
func TestHandleReport_NewPrintPopulatesCurrentFile(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First print: running.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "first_print.gcode",
			"mc_percent": 80
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	if c.Status().CurrentFile != "first_print.gcode" {
		t.Fatalf("After first print: CurrentFile = %q; want %q", c.Status().CurrentFile, "first_print.gcode")
	}

	// Print completes → idle → CurrentFile cleared.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	if c.Status().CurrentFile != "" {
		t.Fatalf("After IDLE: CurrentFile = %q; want empty", c.Status().CurrentFile)
	}

	// Second print starts.
	payload3 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "second_print.gcode",
			"mc_percent": 5
		}
	}`)
	c.handleReport(nil, newMockMessage(payload3))
	s3 := c.Status()

	if s3.CurrentFile != "second_print.gcode" {
		t.Errorf("After second print starts: CurrentFile = %q; want %q",
			s3.CurrentFile, "second_print.gcode")
	}
}

// TestHandleReport_SuccessDoesNotClearCurrentFile verifies that SUCCESS
// (complete) state does NOT clear CurrentFile — only idle does.  The user
// may still want to see the completed file's name on the dashboard.
func TestHandleReport_SuccessDoesNotClearCurrentFile(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Printing.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "done.gcode",
			"mc_percent": 99
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))

	// SUCCESS — state is "complete", not "idle", so CurrentFile must survive.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "SUCCESS",
			"mc_percent": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()

	if s2.State != "complete" {
		t.Fatalf("After SUCCESS: State = %q; want %q", s2.State, "complete")
	}
	if s2.CurrentFile != "done.gcode" {
		t.Errorf("After SUCCESS: CurrentFile = %q; want %q (complete is not idle, should preserve)",
			s2.CurrentFile, "done.gcode")
	}
}

// TestHandleReport_SuccessIdleIdleSequence_CurrentFileClearsBeforeState
// locks in the exact real-world SUCCESS -> IDLE -> IDLE firmware sequence
// and the intended relative timing of CurrentFile vs. State:
//
//   - CurrentFile clears off the *raw* per-report mapped state, so it clears
//     at the FIRST IDLE report — one report before State unlatches.
//   - State stays latched at "complete" through that first IDLE (K-004's
//     flicker fix) and only drops to "idle" on the SECOND consecutive IDLE
//     report (completeIdleStreakThreshold).
//
// This is a deliberate decoupling (K-007): CurrentFile's clear condition no
// longer rides on the complete->idle latch, so future tuning of the latch
// threshold can't silently shift CurrentFile timing too. If this test ever
// needs to change, it means the intended relationship between the two has
// changed and should be a conscious decision, not a side effect.
func TestHandleReport_SuccessIdleIdleSequence_CurrentFileClearsBeforeState(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Printing.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "done.gcode",
			"mc_percent": 99
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	if s1 := c.Status(); s1.CurrentFile != "done.gcode" {
		t.Fatalf("After RUNNING: CurrentFile = %q; want %q", s1.CurrentFile, "done.gcode")
	}

	// Report 1: SUCCESS — State becomes "complete", CurrentFile preserved.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "SUCCESS",
			"mc_percent": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()
	if s2.State != "complete" {
		t.Fatalf("After SUCCESS: State = %q; want %q", s2.State, "complete")
	}
	if s2.CurrentFile != "done.gcode" {
		t.Fatalf("After SUCCESS: CurrentFile = %q; want %q (preserved)", s2.CurrentFile, "done.gcode")
	}

	idlePayload := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)

	// Report 2: first IDLE. State stays latched at "complete" (K-004), but
	// CurrentFile clears immediately since the raw mapped state IS "idle".
	c.handleReport(nil, newMockMessage(idlePayload))
	s3 := c.Status()
	if s3.State != "complete" {
		t.Fatalf("After SUCCESS then 1st IDLE: State = %q; want %q (still latched)", s3.State, "complete")
	}
	if s3.CurrentFile != "" {
		t.Fatalf("After SUCCESS then 1st IDLE: CurrentFile = %q; want empty (clears on raw idle)", s3.CurrentFile)
	}

	// Report 3: second consecutive IDLE. Threshold met, State finally
	// settles to "idle" too. CurrentFile remains empty.
	c.handleReport(nil, newMockMessage(idlePayload))
	s4 := c.Status()
	if s4.State != "idle" {
		t.Fatalf("After SUCCESS then 2nd IDLE: State = %q; want %q (threshold met)", s4.State, "idle")
	}
	if s4.CurrentFile != "" {
		t.Fatalf("After SUCCESS then 2nd IDLE: CurrentFile = %q; want empty", s4.CurrentFile)
	}
}

// ---------------------------------------------------------------------------
// complete->idle latch tests (State flicker fix)
// ---------------------------------------------------------------------------

// TestHandleReport_SuccessThenSingleIdle_DoesNotDropState verifies that a
// single IDLE report immediately following SUCCESS does NOT drop State from
// "complete" back to "idle". Bambu firmware reports SUCCESS briefly right
// after a print finishes, then settles to IDLE on the very next MQTT push —
// without a latch this flickers "complete" -> "idle" within one report,
// which a connected dashboard client would see as COMPLETE flashing and
// vanishing.
func TestHandleReport_SuccessThenSingleIdle_DoesNotDropState(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Printing.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "done.gcode",
			"mc_percent": 99
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))

	// SUCCESS — state becomes "complete".
	payload2 := []byte(`{
		"print": {
			"gcode_state": "SUCCESS",
			"mc_percent": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	if s2 := c.Status(); s2.State != "complete" {
		t.Fatalf("After SUCCESS: State = %q; want %q", s2.State, "complete")
	}

	// A single subsequent IDLE report must NOT drop State from "complete".
	payload3 := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)
	c.handleReport(nil, newMockMessage(payload3))
	s3 := c.Status()
	if s3.State != "complete" {
		t.Fatalf("After SUCCESS then single IDLE: State = %q; want %q (latched)", s3.State, "complete")
	}
}

// TestHandleReport_SuccessThenTwoIdles_DropsStateToIdle verifies that once
// completeIdleStreakThreshold consecutive IDLE reports have been seen after
// SUCCESS, State does eventually settle to "idle".
func TestHandleReport_SuccessThenTwoIdles_DropsStateToIdle(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "done.gcode",
			"mc_percent": 99
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))

	payload2 := []byte(`{
		"print": {
			"gcode_state": "SUCCESS",
			"mc_percent": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	if s2 := c.Status(); s2.State != "complete" {
		t.Fatalf("After SUCCESS: State = %q; want %q", s2.State, "complete")
	}

	idlePayload := []byte(`{
		"print": {
			"gcode_state": "IDLE"
		}
	}`)

	// First IDLE report: still latched.
	c.handleReport(nil, newMockMessage(idlePayload))
	if s := c.Status(); s.State != "complete" {
		t.Fatalf("After SUCCESS then 1 IDLE: State = %q; want %q (still latched)", s.State, "complete")
	}

	// Second consecutive IDLE report: threshold met, State settles to "idle".
	c.handleReport(nil, newMockMessage(idlePayload))
	if s := c.Status(); s.State != "idle" {
		t.Fatalf("After SUCCESS then 2 IDLEs: State = %q; want %q (threshold met)", s.State, "idle")
	}
}

// TestHandleReport_SuccessThenRunning_OverridesImmediately verifies that a
// new print starting (RUNNING) immediately overrides a latched "complete"
// state with no delay — only the complete->idle edge is latched.
func TestHandleReport_SuccessThenRunning_OverridesImmediately(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "first.gcode",
			"mc_percent": 99
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))

	payload2 := []byte(`{
		"print": {
			"gcode_state": "SUCCESS",
			"mc_percent": 100
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	if s2 := c.Status(); s2.State != "complete" {
		t.Fatalf("After SUCCESS: State = %q; want %q", s2.State, "complete")
	}

	// New print starts immediately (no intervening IDLE reports).
	payload3 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"gcode_file": "second.gcode",
			"mc_percent": 1
		}
	}`)
	c.handleReport(nil, newMockMessage(payload3))
	s3 := c.Status()
	if s3.State != "printing" {
		t.Fatalf("After SUCCESS then RUNNING: State = %q; want %q (immediate override, no latch)", s3.State, "printing")
	}
}

// ---------------------------------------------------------------------------
// Light state tests (lights_report parsing in handleReport)
// ---------------------------------------------------------------------------

func TestHandleReport_LightState_FromLightsReport_On(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"lights_report": [
				{"node": "chamber_light", "mode": "on"}
			]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.LightOn == nil {
		t.Fatal("LightOn = nil; want true")
	}
	if !*s.LightOn {
		t.Error("LightOn = false; want true")
	}
}

func TestHandleReport_LightState_FromLightsReport_Off(t *testing.T) {
	c := newTestPrinterClient(nil)

	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"lights_report": [
				{"node": "chamber_light", "mode": "off"}
			]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.LightOn == nil {
		t.Fatal("LightOn = nil; want false")
	}
	if *s.LightOn {
		t.Error("LightOn = true; want false")
	}
}

func TestHandleReport_LightState_LightsReportWithMultipleEntries(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Multiple entries — chamber_light is the one we care about.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"lights_report": [
				{"node": "status_light", "mode": "on"},
				{"node": "chamber_light", "mode": "on"}
			]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.LightOn == nil {
		t.Fatal("LightOn = nil; want true")
	}
	if !*s.LightOn {
		t.Error("LightOn = false; want true (chamber_light is on)")
	}
}

func TestHandleReport_LightState_LightsReportNoChamberLight(t *testing.T) {
	c := newTestPrinterClient(nil)

	// lights_report present but no chamber_light entry — LightOn unchanged.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"lights_report": [
				{"node": "status_light", "mode": "on"}
			]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.LightOn != nil {
		t.Errorf("LightOn = %v; want nil (no chamber_light entry)", *s.LightOn)
	}
}

func TestHandleReport_LightState_LightsReportEmpty(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Empty lights_report array — LightOn unchanged.
	payload := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"lights_report": []
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.LightOn != nil {
		t.Errorf("LightOn = %v; want nil (empty lights_report)", *s.LightOn)
	}
}

func TestHandleReport_LightState_SystemLedCtrlNoLongerSetsLight(t *testing.T) {
	c := newTestPrinterClient(nil)

	// The old system.ledctrl path should no longer set LightOn.
	payload := []byte(`{
		"system": {
			"ledctrl": {
				"node": "chamber_light",
				"mode": "on"
			}
		}
	}`)
	c.handleReport(nil, newMockMessage(payload))
	s := c.Status()

	if s.LightOn != nil {
		t.Errorf("LightOn = %v; want nil (system.ledctrl no longer drives light state)", *s.LightOn)
	}
}

func TestHandleReport_LightState_UpdatedAcrossReports(t *testing.T) {
	c := newTestPrinterClient(nil)

	// First report: light on.
	payload1 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"lights_report": [
				{"node": "chamber_light", "mode": "on"}
			]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload1))
	s1 := c.Status()
	if s1.LightOn == nil || !*s1.LightOn {
		t.Fatalf("After first report: LightOn = %v; want true", s1.LightOn)
	}

	// Second report: light off.
	payload2 := []byte(`{
		"print": {
			"gcode_state": "RUNNING",
			"lights_report": [
				{"node": "chamber_light", "mode": "off"}
			]
		}
	}`)
	c.handleReport(nil, newMockMessage(payload2))
	s2 := c.Status()
	if s2.LightOn == nil {
		t.Fatal("After second report: LightOn = nil; want false")
	}
	if *s2.LightOn {
		t.Error("After second report: LightOn = true; want false")
	}
}

// ---------------------------------------------------------------------------
// publishCommand tests
// ---------------------------------------------------------------------------

func TestPublishCommand_NotConnected_NilClient(t *testing.T) {
	c := newTestPrinterClient(nil)
	// Ensure mqttClient is nil.
	c.mqttClient = nil

	err := c.publishCommand(context.Background(), "test-cmd", []byte("test"))
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

	err := c.publishCommand(context.Background(), "test-cmd", []byte("test"))
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

	err := c.publishCommand(context.Background(), "test-cmd", []byte("test"))
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

	err := c.publishCommand(context.Background(), "test-cmd", []byte("test"))
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

	err := c.publishCommand(context.Background(), "test-cmd", []byte("test"))
	if err == nil {
		t.Fatal("expected error from token")
	}
	if err.Error() != "broker unavailable" {
		t.Errorf("error = %q; want %q", err.Error(), "broker unavailable")
	}
}

func TestPublishCommand_LogsAuditLine(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.mqttClient = &mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, _ interface{}) mqtt.Token {
			return &mockMQTTToken{doneCh: closedCh()}
		},
	}

	var buf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	err := c.publishCommand(context.Background(), "pause", pauseCommand())
	if err != nil {
		t.Fatalf("publishCommand() returned error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "bambu test-id: sending command pause") {
		t.Errorf("log output = %q; want it to contain %q", got, "bambu test-id: sending command pause")
	}
	// Privacy: the payload/secrets must never be logged, only the command name.
	if strings.Contains(got, string(pauseCommand())) {
		t.Errorf("log output = %q; must not contain the raw command payload", got)
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
	if len(streams) != 1 {
		t.Fatalf("After SetModel(H2S): CameraStreams() returned %d streams; want 1 (RTSPS)", len(streams))
	}
	if !strings.Contains(streams[0].URL, "live/1") {
		t.Errorf("streams[0].URL = %q; want live/1", streams[0].URL)
	}
}

// ---------------------------------------------------------------------------
// HasChamber tests
// ---------------------------------------------------------------------------

func TestNew_HasChamber(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"H2S", true},
		{"H2D", true},
		{"O1S", true},
		{"P1S", false},
		{"A1", false},
		{"X1C", false},
		{"", false},
	}

	for _, tt := range tests {
		cfg := config.PrinterDef{
			ID:     "haschamber-new",
			Name:   "HasChamber New",
			Type:   "bambu",
			Serial: "SERIAL-HC-1",
			Model:  tt.model,
		}
		c := New(cfg, nil)
		got := c.Status().HasChamber
		if got != tt.want {
			t.Errorf("New() with model %q: Status().HasChamber = %v; want %v", tt.model, got, tt.want)
		}
	}
}

func TestSetModel_HasChamber(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"H2S", true},
		{"H2D", true},
		{"O1S", true},
		{"P1S", false},
		{"A1", false},
		{"X1C", false},
	}

	for _, tt := range tests {
		c := newTestPrinterClient(nil)
		c.SetModel(tt.model)
		got := c.Status().HasChamber
		if got != tt.want {
			t.Errorf("SetModel(%q): Status().HasChamber = %v; want %v", tt.model, got, tt.want)
		}
	}
}

func TestSetModel_HasChamber_RecomputesAfterNew(t *testing.T) {
	// New() populates HasChamber from cfg.Model; SetModel() runs later
	// (server.go) and must recompute it, since the effective model can
	// change (e.g. config omits Model, learned later via cloud API).
	cfg := config.PrinterDef{
		ID:     "haschamber-recompute",
		Name:   "HasChamber Recompute",
		Type:   "bambu",
		Serial: "SERIAL-HC-2",
		// No Model set — New() should produce HasChamber=false.
	}
	c := New(cfg, nil)
	if got := c.Status().HasChamber; got != false {
		t.Fatalf("New() with no model: Status().HasChamber = %v; want false", got)
	}

	c.SetModel("H2S")
	if got := c.Status().HasChamber; got != true {
		t.Errorf("after SetModel(H2S): Status().HasChamber = %v; want true", got)
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
		{"O1S", true}, // Bambu Cloud API internal code for H2S
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

// ---------------------------------------------------------------------------
// K-013: Connect — initial-connect retry with backoff
// ---------------------------------------------------------------------------
//
// These tests exercise Connect's real retry loop end-to-end against a local
// TCP listener rather than the mockMQTTClient seam used elsewhere in this
// file — mockMQTTClient only fakes the post-construction mqtt.Client, but
// Connect constructs its own real *paho* client internally (via
// mqtt.NewClient), so the only way to exercise the retry loop faithfully is
// a real (if minimal) TCP/MQTT endpoint. brokerOverride and
// connectBackoffBase/connectBackoffMax are test-only seams on Client for
// exactly this purpose (see their doc comments in client.go).

// writeRawConnack writes a minimal, hand-rolled MQTT 3.1.1 CONNACK packet
// (session-present=0, return-code=0/Accepted) directly to conn, bypassing
// any real MQTT packet decode of what the client sent. This is sufficient
// for Paho's client-side handshake (connectMQTT) to treat the connection as
// successfully established.
func writeRawConnack(conn net.Conn) error {
	_, err := conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
	return err
}

// newTestConnectClient builds a Client wired for Connect(), pointed at
// brokerAddr (host:port, no scheme) via brokerOverride, with a fast test
// backoff schedule.
func newTestConnectClient(id, brokerAddr string, backoffBase, backoffMax time.Duration) *Client {
	cfg := config.PrinterDef{
		ID:     id,
		Name:   "Connect Test " + id,
		Type:   "bambu",
		Serial: "SERIAL-CONNECT-" + id,
	}
	cloud := NewBambuCloudClient("us")
	c := New(cfg, cloud)
	c.brokerOverride = "tcp://" + brokerAddr
	c.connectBackoffBase = backoffBase
	c.connectBackoffMax = backoffMax
	return c
}

// TestConnect_RetriesInitialConnectFailure verifies that a failing initial
// connect is retried rather than giving up after one attempt: the listener
// refuses (closes without responding) the first few connections, then
// starts accepting and completing the MQTT handshake. Connect must not
// return until it eventually succeeds, at which point it blocks on
// ctx.Done() as usual (the existing successful-connect path, unchanged).
func TestConnect_RetriesInitialConnectFailure(t *testing.T) {
	const failuresBeforeSuccess = 3

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()

	var acceptCount int64
	handshakeSucceeded := make(chan struct{})
	var handshakeSucceededOnce sync.Once

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			n := atomic.AddInt64(&acceptCount, 1)
			if n <= failuresBeforeSuccess {
				// Simulate a failing connect: close immediately without any
				// MQTT handshake response.
				conn.Close()
				continue
			}
			// From here on, complete the handshake successfully and hold
			// the connection open (mimicking a real broker) until the test
			// tears it down.
			go func(c net.Conn) {
				defer c.Close()
				if err := writeRawConnack(c); err != nil {
					return
				}
				handshakeSucceededOnce.Do(func() { close(handshakeSucceeded) })
				// Keep reading (and discarding) to drain PINGREQs etc. and
				// avoid the client seeing a premature EOF/reset that would
				// look like a second failure.
				buf := make([]byte, 256)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	c := newTestConnectClient("retry-success", ln.Addr().String(), 5*time.Millisecond, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connectDone := make(chan error, 1)
	go func() {
		connectDone <- c.Connect(ctx)
	}()

	// Wait for the fake broker to complete a successful handshake (each
	// retry sleeps at most ~20ms, so this should resolve well within a
	// couple hundred ms — generous headroom without waiting through a real
	// 1s/2s/4s/... schedule).
	select {
	case <-handshakeSucceeded:
	case err := <-connectDone: // Connect only returns after ctx.Done(); a return here is unexpected this early.
		t.Fatalf("Connect returned before context was cancelled (err=%v)", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the retry loop to reach a successful handshake")
	}

	if got := atomic.LoadInt64(&acceptCount); got <= failuresBeforeSuccess {
		t.Fatalf("acceptCount = %d; want > %d (expected the retry loop to keep attempting past the simulated failures)", got, failuresBeforeSuccess)
	}

	// Verify the success path behaves exactly as before: Connect blocks
	// until ctx is cancelled, then returns cleanly.
	cancel()
	select {
	case err := <-connectDone:
		if err != nil {
			t.Errorf("Connect() error = %v; want nil after clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Connect did not return after context cancellation")
	}
}

// TestConnect_RetryLoopRespectsContextCancellation verifies that a pending
// retry (backoff sleep) is interrupted promptly when ctx is cancelled,
// rather than sleeping through shutdown — Connect must return quickly, not
// after a queued backoff interval elapses.
func TestConnect_RetryLoopRespectsContextCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Always fail — this test only cares about cancellation
			// interrupting the backoff sleep, so the connect must never
			// succeed.
			conn.Close()
		}
	}()

	// A deliberately long backoff — if cancellation were not respected
	// promptly, the test would have to wait out this whole interval.
	const longBackoff = 10 * time.Second
	c := newTestConnectClient("retry-cancel", ln.Addr().String(), longBackoff, longBackoff)

	ctx, cancel := context.WithCancel(context.Background())

	connectDone := make(chan error, 1)
	go func() {
		connectDone <- c.Connect(ctx)
	}()

	// Let the first attempt fail and the retry loop enter its backoff sleep.
	time.Sleep(200 * time.Millisecond)

	cancel()

	select {
	case err := <-connectDone:
		if err != nil {
			t.Errorf("Connect() error = %v; want nil (cancellation during retry is a clean shutdown, not a connect error)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Connect did not return promptly after context cancellation during backoff sleep (retry loop is not respecting ctx.Done())")
	}
}
