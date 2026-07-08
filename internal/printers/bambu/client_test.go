package bambu

import (
	"context"
	"errors"
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

func TestClient_CameraURLs(t *testing.T) {
	c := newTestPrinterClient(nil)

	urls := c.CameraURLs()
	if len(urls) != 1 {
		t.Fatalf("CameraURLs() returned %d URLs; want 1", len(urls))
	}
	expected := "http://10.0.0.1:6000/?token=1234"
	if urls[0] != expected {
		t.Errorf("CameraURLs()[0] = %q; want %q", urls[0], expected)
	}
}

func TestClient_CameraURLs_NoHost(t *testing.T) {
	cfg := config.PrinterDef{
		ID:         "test-id-no-cam",
		Name:       "No Camera",
		Type:       "bambu",
		Serial:     "SERIAL002",
		AccessCode: "1234",
		// Host is empty
	}
	c := New(cfg, nil)

	urls := c.CameraURLs()
	if len(urls) != 0 {
		t.Errorf("CameraURLs() returned %d URLs; want 0", len(urls))
	}
}

func TestClient_CameraURLs_NoAccessCode(t *testing.T) {
	cfg := config.PrinterDef{
		ID:     "test-id-no-ac",
		Name:   "No Access Code",
		Type:   "bambu",
		Serial: "SERIAL003",
		Host:   "10.0.0.2",
		// AccessCode is empty
	}
	c := New(cfg, nil)

	urls := c.CameraURLs()
	if len(urls) != 0 {
		t.Errorf("CameraURLs() returned %d URLs; want 0", len(urls))
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
	if s.BedTemp != 55.5 {
		t.Errorf("BedTemp = %f; want 55.5", s.BedTemp)
	}
	if s.BedTargetTemp != 60.0 {
		t.Errorf("BedTargetTemp = %f; want 60.0", s.BedTargetTemp)
	}
	if s.NozzleTemp != 210.0 {
		t.Errorf("NozzleTemp = %f; want 210.0", s.NozzleTemp)
	}
	if s.NozzleTargetTemp != 220.0 {
		t.Errorf("NozzleTargetTemp = %f; want 220.0", s.NozzleTargetTemp)
	}
	if s.ChamberTemp != 30.0 {
		t.Errorf("ChamberTemp = %f; want 30.0", s.ChamberTemp)
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
	c.status.BedTemp = 55.0
	c.status.NozzleTemp = 200.0
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
	if s.BedTemp != 55.0 {
		t.Errorf("BedTemp = %f; want 55.0 (should preserve previous value)", s.BedTemp)
	}
	if s.NozzleTemp != 200.0 {
		t.Errorf("NozzleTemp = %f; want 200.0 (should preserve previous value)", s.NozzleTemp)
	}
	// Other fields should still update.
	if s.BedTargetTemp != 60.0 {
		t.Errorf("BedTargetTemp = %f; want 60.0", s.BedTargetTemp)
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

	if s.ChamberTemp != 28.5 {
		t.Errorf("ChamberTemp = %f; want 28.5 (from info.temp fallback)", s.ChamberTemp)
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

	if s.ChamberTemp != 30.0 {
		t.Errorf("ChamberTemp = %f; want 30.0 (chamber_temper should take priority)", s.ChamberTemp)
	}
}

func TestHandleReport_ChamberTempPreservedWhenMissing(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Set initial chamber temp.
	c.mu.Lock()
	c.status.ChamberTemp = 25.0
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

	if s.ChamberTemp != 25.0 {
		t.Errorf("ChamberTemp = %f; want 25.0 (should preserve previous value)", s.ChamberTemp)
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
