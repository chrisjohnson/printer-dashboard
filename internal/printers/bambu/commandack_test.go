package bambu

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Tests for firmware-ack correlation on the Bambu command path.
//
// Background: publishing to MQTT at QoS 0 only proves the broker accepted the
// packet. Real hardware was observed replying on the report topic with
// {"print":{"command":"gcode_line","result":"failed","reason":"mqtt message
// verify failed",...}} while the HTTP endpoint still reported success, so the
// UI showed nothing at all happening. These tests lock in that the reply is now
// surfaced instead of discarded.

// ackDeliveringClient returns a client whose mock MQTT publish synchronously
// feeds the given report payload back through handleReport, simulating a
// printer that answers immediately.
func ackDeliveringClient(t *testing.T, ackPayload string) *Client {
	t.Helper()
	var c *Client
	c = newTestPrinterClient(&mockMQTTClient{
		isConnected: true,
		publishFn: func(_ string, _ byte, _ bool, _ interface{}) mqtt.Token {
			c.handleReport(nil, newMockMessage([]byte(ackPayload)))
			return &mockMQTTToken{doneCh: closedCh()}
		},
	})
	// Long enough that the ack (delivered synchronously above) always wins the
	// race against the timeout, but short enough to keep the suite quick.
	c.ackTimeout = 2 * time.Second
	return c
}

func TestPublishCommand_FirmwareRejectionBecomesError(t *testing.T) {
	// Verbatim shape observed from an H2S rejecting a jog/home gcode_line.
	c := ackDeliveringClient(t, `{"print":{"command":"gcode_line","err_code":84033543,`+
		`"is_from_mqtt":true,"param":"G28\n","reason":"mqtt message verify failed","result":"failed"}}`)

	err := c.publishCommand(context.Background(), "home_all", homeAllCommand())
	if err == nil {
		t.Fatal("publishCommand() = nil; want error when firmware replies result:failed")
	}
	for _, want := range []string{"bambu test-id:", "home_all", "mqtt message verify failed", "84033543"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q; want it to contain %q", err.Error(), want)
		}
	}
}

func TestPublishCommand_SystemNamespaceRejectionBecomesError(t *testing.T) {
	// system-namespace replies carry no print section at all, so they used to
	// be dropped before the print handler ever ran.
	c := ackDeliveringClient(t, `{"system":{"command":"ledctrl","reason":"not authorized","result":"failed","sequence_id":"0"}}`)

	err := c.publishCommand(context.Background(), "light", setLightCommand(true))
	if err == nil {
		t.Fatal("publishCommand() = nil; want error for a failed system-namespace ack")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("error %q; want it to contain the reason %q", err.Error(), "not authorized")
	}
}

func TestPublishCommand_SuccessAckReturnsNil(t *testing.T) {
	c := ackDeliveringClient(t, `{"print":{"sequence_id":"0","command":"gcode_line",`+
		`"param":"G90\n","reason":"success","result":"success"}}`)

	if err := c.publishCommand(context.Background(), "jog", jogCommand(1, 0, 0, 1500)); err != nil {
		t.Errorf("publishCommand() = %v; want nil for a successful ack", err)
	}
}

func TestPublishCommand_NoAckTimesOutAsSuccess(t *testing.T) {
	// Not every firmware/command combination replies. Timing out must stay a
	// non-error — claiming failure here would be a false alarm.
	c := newTestPrinterClient(&mockMQTTClient{
		isConnected: true,
		publishFn:   func(_ string, _ byte, _ bool, _ interface{}) mqtt.Token { return &mockMQTTToken{doneCh: closedCh()} },
	})
	c.ackTimeout = 15 * time.Millisecond

	if err := c.publishCommand(context.Background(), "pause", pauseCommand()); err != nil {
		t.Errorf("publishCommand() = %v; want nil when no ack arrives", err)
	}
}

func TestPublishCommand_LeavesNoWaiterBehind(t *testing.T) {
	c := newTestPrinterClient(&mockMQTTClient{
		isConnected: true,
		publishFn:   func(_ string, _ byte, _ bool, _ interface{}) mqtt.Token { return &mockMQTTToken{doneCh: closedCh()} },
	})
	c.ackTimeout = 10 * time.Millisecond

	_ = c.publishCommand(context.Background(), "pause", pauseCommand())

	c.ackMu.Lock()
	n := len(c.ackWaiters["print:pause"])
	c.ackMu.Unlock()
	if n != 0 {
		t.Errorf("%d ack waiter(s) left registered after publishCommand; want 0 (leak)", n)
	}
}

func TestAckKey(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"print gcode_line", `{"print":{"command":"gcode_line","param":"G28\n"}}`, "print:gcode_line"},
		{"print pause", `{"print":{"command":"pause"}}`, "print:pause"},
		{"system ledctrl", `{"system":{"sequence_id":"0","command":"ledctrl"}}`, "system:ledctrl"},
		{"pushing pushall", `{"pushing":{"command":"pushall"}}`, "pushing:pushall"},
		{"no command field", `{"print":{"param":"x"}}`, ""},
		{"unknown namespace", `{"weird":{"command":"x"}}`, ""},
		{"not json", `nope`, ""},
		{"empty object", `{}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ackKey([]byte(tc.payload)); got != tc.want {
				t.Errorf("ackKey(%q) = %q; want %q", tc.payload, got, tc.want)
			}
		})
	}
}

// A command reply echoes the command and carries no status fields. Parsing it as
// status would read the absent home_flag as 0 and falsely mark a homed printer
// as unhomed on every gcode_line reply.
func TestHandleReport_CommandAckDoesNotClobberStatus(t *testing.T) {
	c := newTestPrinterClient(nil)

	// Baseline: a normal status report that reports the printer homed.
	c.handleReport(nil, newMockMessage([]byte(`{"print":{"command":"push_status","gcode_state":"IDLE",`+
		`"home_flag":1,"nozzle_temper":25.0}}`)))
	if h := c.Status().Homed; h == nil || !*h {
		t.Fatalf("Homed after status report = %v; want true", h)
	}

	// Now a bare gcode_line reply, which has no home_flag at all.
	c.handleReport(nil, newMockMessage([]byte(`{"print":{"sequence_id":"0","command":"gcode_line",`+
		`"param":"G90\n","reason":"success","result":"success"}}`)))

	s := c.Status()
	if h := s.Homed; h == nil || !*h {
		t.Errorf("Homed after command ack = %v; want it to stay true (ack has no home_flag)", h)
	}
	if s.State != "idle" {
		t.Errorf("State after command ack = %q; want it to stay %q", s.State, "idle")
	}
}

func TestHandleReport_FailedAckDoesNotChangeStatus(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.handleReport(nil, newMockMessage([]byte(`{"print":{"command":"push_status","gcode_state":"IDLE","home_flag":1}}`)))

	c.handleReport(nil, newMockMessage([]byte(`{"print":{"command":"gcode_line","result":"failed",`+
		`"reason":"mqtt message verify failed","err_code":84033543}}`)))

	if s := c.Status(); s.State != "idle" {
		t.Errorf("State = %q; want %q — a rejected command must not alter reported state", s.State, "idle")
	}
}

// ---------------------------------------------------------------------------
// Staleness refresh (keepStatusFresh)
// ---------------------------------------------------------------------------

// pushCounter returns a client whose mock records how many times a request was
// published, i.e. how many pushall refreshes it asked for.
func pushCounter() (*Client, func() int) {
	var mu sync.Mutex
	var n int
	c := newTestPrinterClient(&mockMQTTClient{
		isConnected: true,
		publishFn: func(topic string, _ byte, _ bool, _ interface{}) mqtt.Token {
			if strings.Contains(topic, "/request") {
				mu.Lock()
				n++
				mu.Unlock()
			}
			return &mockMQTTToken{doneCh: closedCh()}
		},
	})
	c.statusStaleTimeout = 40 * time.Millisecond
	c.freshCheckEvery = 5 * time.Millisecond
	return c, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// startFresh runs keepStatusFresh in the background the way onConnect does, and
// returns a stop func (simulate a reconnect, which supersedes it) plus a channel
// closed when the goroutine actually exits.
func startFresh(c *Client) (stop func(), exited <-chan struct{}) {
	c.mu.Lock()
	c.freshGen++
	gen := c.freshGen
	c.mu.Unlock()

	exitedCh := make(chan struct{})
	go func() { c.keepStatusFresh(gen); close(exitedCh) }()
	return func() {
		c.mu.Lock()
		c.freshGen++
		c.mu.Unlock()
	}, exitedCh
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

func TestKeepStatusFresh_RequestsPushWhenQuiet(t *testing.T) {
	c, pushes := pushCounter()
	c.mu.Lock()
	c.lastStatusReport = time.Now()
	c.mu.Unlock()
	stop, _ := startFresh(c)
	defer stop()

	if !waitFor(func() bool { return pushes() >= 1 }, 2*time.Second) {
		t.Fatalf("keepStatusFresh did not request a full push while the status stream was quiet (pushes=%d)", pushes())
	}
}

func TestKeepStatusFresh_LeavesHealthyPrinterAlone(t *testing.T) {
	// A printer that keeps pushing status must not be poked: OpenBambuAPI warns
	// pushall can stall P1P-class hardware, so this only fires on real silence.
	c, pushes := pushCounter()
	stop, _ := startFresh(c)
	defer stop()

	for i := 0; i < 20; i++ {
		c.handleReport(nil, newMockMessage([]byte(`{"print":{"command":"push_status","gcode_state":"IDLE"}}`)))
		time.Sleep(5 * time.Millisecond)
	}
	if n := pushes(); n != 0 {
		t.Errorf("requested %d full push(es) while status was arriving; want 0", n)
	}
}

func TestKeepStatusFresh_ExitsWhenSuperseded(t *testing.T) {
	c, _ := pushCounter()
	c.statusStaleTimeout = time.Hour // never stale: only the supersede check can end it
	stop, exited := startFresh(c)

	stop() // simulate a reconnect starting a newer goroutine

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("keepStatusFresh kept running after being superseded by a newer connection")
	}
}
