package bambu

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// hmsHealthyStreakThreshold is the number of consecutive reports with a
// present, healthy gcode_state and an absent "hms" key required before
// handleReport decays stale HMSErrors/HMSWarnings. See handleReport's HMS
// block for the full policy and rationale.
const hmsHealthyStreakThreshold = 2

// completeIdleStreakThreshold is the number of consecutive "idle" reports
// required before handleReport allows an "idle" gcode_state to overwrite a
// latched State="complete". Bambu firmware briefly reports SUCCESS right
// after a print finishes, then settles to IDLE on the very next MQTT push —
// without this latch, State flickers complete->idle within 1-2 reports and a
// connected dashboard client sees COMPLETE flash and vanish. See
// handleReport's State block for the full policy. Any non-idle, non-complete
// state (e.g. a new print starting) still overrides "complete" immediately;
// only the complete->idle edge is latched.
const completeIdleStreakThreshold = 2

// Client implements the printers.Printer interface for Bambu Lab printers
// via Bambu's cloud MQTT infrastructure. No LAN mode or developer mode required.
type Client struct {
	cfg         config.PrinterDef
	cloud       *BambuCloudClient
	mu          sync.RWMutex
	status      printers.PrinterStatus
	mqttClient  mqtt.Client
	camIPCamURL string
	model       string // printer model (e.g., "H2S", "P1S", "X1C") from config or cloud API

	// StatusCh is an optional channel that receives the full printer status
	// after each report parse. If nil, no status updates are emitted.
	// The channel should be buffered to avoid blocking MQTT processing.
	StatusCh chan printers.PrinterStatus

	// hmsHealthyStreak counts consecutive reports where gcode_state was
	// present and mapped to a healthy (non-error, non-FAILED) state while
	// the "hms" key itself was absent (not refreshed). Used by handleReport
	// to decay/clear stale HMSErrors/HMSWarnings if firmware simply stops
	// sending "hms" once a condition resolves, instead of sending an
	// explicit "hms: []". See handleReport's HMS block for the full policy.
	hmsHealthyStreak int

	// completeIdleStreak counts consecutive "idle"-mapped reports seen while
	// State is latched to "complete". Used by handleReport to require
	// completeIdleStreakThreshold consecutive idle reports before letting
	// "idle" overwrite a "complete" state, guarding against the brief
	// SUCCESS->IDLE flicker Bambu firmware exhibits right after a print
	// finishes. See handleReport's State block for the full policy.
	completeIdleStreak int
}

// New creates a new Bambu printer client for cloud MQTT connectivity.
//
// The cloud client must already be authenticated (Login or LoginWithToken called).
func New(cfg config.PrinterDef, cloud *BambuCloudClient) *Client {
	status := printers.PrinterStatus{
		ID:         cfg.ID,
		Name:       cfg.Name,
		Type:       "bambu",
		HasChamber: IsH2S(cfg.Model),
	}

	return &Client{
		cfg:    cfg,
		cloud:  cloud,
		status: status,
		model:  cfg.Model, // pre-populate from config if available
	}
}

// SetModel sets the printer model name (e.g., "H2S", "P1S", "X1C").
// This is used for camera URL format detection when ipcam_url is not available.
// It also re-derives the HasChamber capability flag, since SetModel runs
// after New() in server.go and may change the effective model (e.g. when the
// config omits Model and it's only learned later via the cloud API).
func (c *Client) SetModel(model string) {
	c.mu.Lock()
	c.model = model
	c.mu.Unlock()

	s := c.Status()
	s.HasChamber = IsH2S(model)
	c.setStatus(s)
}

// ID returns the printer's unique identifier.
func (c *Client) ID() string { return c.cfg.ID }

// Name returns the printer's human-readable name.
func (c *Client) Name() string { return c.cfg.Name }

// Status returns the current cached status. Safe for concurrent use.
func (c *Client) Status() printers.PrinterStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// setStatus updates the cached status under the write lock and sends the
// updated status on StatusCh if configured. The send is non-blocking to avoid
// slowing down MQTT processing.
func (c *Client) setStatus(s printers.PrinterStatus) {
	c.mu.Lock()
	c.status = s
	c.mu.Unlock()

	if c.StatusCh != nil {
		select {
		case c.StatusCh <- s:
		default:
			// Channel full, drop update (reader is slow)
		}
	}
}

// CameraStreams returns the available camera/display streams for this printer.
func (c *Client) CameraStreams() []printers.CameraStream {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var streams []printers.CameraStream

	if c.camIPCamURL != "" {
		// Use the URL from MQTT report — it already has the right format.
		// H2S-series printers only expose a single chamber camera; a
		// second stream was previously guessed at /streaming/live/2, but
		// that path 404s on real hardware (confirmed against a live H2S),
		// so it's not offered here.
		streams = append(streams, printers.CameraStream{
			URL:   c.camIPCamURL,
			Type:  "internal",
			Label: "Camera",
		})
		return streams
	}

	// Fallback: construct URL from config (host + access code)
	if c.cfg.Host != "" && c.cfg.AccessCode != "" {
		if IsH2S(c.model) {
			// RTSPS stream on port 322 (requires LAN mode enabled on printer).
			streams = append(streams, printers.CameraStream{
				URL:   fmt.Sprintf("rtsps://bblp:%s@%s:322/streaming/live/1", c.cfg.AccessCode, c.cfg.Host),
				Type:  "internal",
				Label: "BirdsEye Camera",
			})
		} else {
			// P1S, A1, X1 series use bambus:// binary TLS protocol on port 6000.
			streams = append(streams, printers.CameraStream{
				URL:   fmt.Sprintf("bambus://%s:6000?token=%s", c.cfg.Host, c.cfg.AccessCode),
				Type:  "internal",
				Label: "Camera",
			})
		}
		return streams
	}

	return nil
}

// Connect establishes the cloud MQTT connection and begins listening for reports.
// Blocks until the context is cancelled (caller should run in a goroutine).
func (c *Client) Connect(ctx context.Context) error {
	broker := "ssl://" + MQTTBroker(c.cloud.region)

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("printer-dashboard-%s-%d", c.cfg.ID, time.Now().UnixNano())).
		SetUsername(c.cloud.MQTTUsername()).
		SetPassword(c.cloud.MQTTPassword()).
		SetTLSConfig(&tls.Config{
			InsecureSkipVerify: true, // Bambu's cloud cert may not be in system store
		}).
		SetOnConnectHandler(c.onConnect).
		SetConnectionLostHandler(c.onConnectionLost).
		SetReconnectingHandler(c.onReconnecting).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(30 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second).
		SetCleanSession(true)

	c.mqttClient = mqtt.NewClient(opts)

	// Connect with timeout
	token := c.mqttClient.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return fmt.Errorf("bambu %s: cloud MQTT connection timeout to %s", c.cfg.ID, broker)
	}
	if err := token.Error(); err != nil {
		s := c.Status()
		s.Online = false
		s.State = "error"
		s.ErrorMsg = fmt.Sprintf("MQTT connect failed: %v", err)
		c.setStatus(s)
		return fmt.Errorf("bambu %s: cloud MQTT connect: %w", c.cfg.ID, err)
	}

	log.Printf("bambu %s: connected to cloud MQTT at %s (user=%s)", c.cfg.ID, broker, c.cloud.MQTTUsername())

	// Block until context is cancelled (keep goroutine alive)
	<-ctx.Done()

	// Disconnect
	if c.mqttClient != nil && c.mqttClient.IsConnected() {
		c.mqttClient.Disconnect(1000)
		log.Printf("bambu %s: disconnected from cloud MQTT", c.cfg.ID)
	}
	return nil
}

// onConnect is called when the MQTT client connects (or reconnects).
func (c *Client) onConnect(client mqtt.Client) {
	log.Printf("bambu %s: cloud MQTT connected (or reconnected)", c.cfg.ID)

	// Subscribe to the printer's report topic
	topic := fmt.Sprintf("device/%s/report", c.cfg.Serial)
	token := client.Subscribe(topic, 0, c.handleReport)
	if token.WaitTimeout(10 * time.Second) {
		if err := token.Error(); err != nil {
			log.Printf("bambu %s: subscribe error: %v", c.cfg.ID, err)
			return
		}
	}
	log.Printf("bambu %s: subscribed to %s", c.cfg.ID, topic)

	// Request a full status push to get current state
	c.requestPushAll(client)
}

// requestPushAll sends a pushall command to get the full printer state.
func (c *Client) requestPushAll(client mqtt.Client) {
	topic := fmt.Sprintf("device/%s/request", c.cfg.Serial)
	payload := `{"pushing":{"command":"pushall","version":1,"push_target":1}}`
	token := client.Publish(topic, 0, false, []byte(payload))
	if token.WaitTimeout(5 * time.Second) {
		if err := token.Error(); err != nil {
			log.Printf("bambu %s: pushall error: %v", c.cfg.ID, err)
		}
	}
}

// onConnectionLost is called when the MQTT connection is lost.
func (c *Client) onConnectionLost(client mqtt.Client, err error) {
	log.Printf("bambu %s: cloud MQTT connection lost: %v", c.cfg.ID, err)
	s := c.Status()
	s.Online = false
	s.State = "error"
	s.ErrorMsg = fmt.Sprintf("MQTT disconnected: %v", err)
	c.setStatus(s)
}

// onReconnecting is called when the client begins reconnecting.
func (c *Client) onReconnecting(client mqtt.Client, opts *mqtt.ClientOptions) {
	log.Printf("bambu %s: cloud MQTT reconnecting...", c.cfg.ID)
}

// handleReport processes incoming MQTT messages on the report topic.
func (c *Client) handleReport(_ mqtt.Client, msg mqtt.Message) {
	r, err := parseReport(msg.Payload())
	if err != nil {
		log.Printf("bambu %s: failed to parse report: %v", c.cfg.ID, err)
		return
	}

	// Capture camera URL from camera reports (even without print data)
	if r.Camera != nil {
		c.mu.Lock()
		if r.Camera.IPCamURL != "" {
			c.camIPCamURL = r.Camera.IPCamURL
			log.Printf("bambu %s: MQTT camera report: ipcam_url=%s", c.cfg.ID, r.Camera.IPCamURL)
		}
		if r.Camera.TimelapseURL != "" {
			log.Printf("bambu %s: MQTT camera report: timelapse_url=%s", c.cfg.ID, r.Camera.TimelapseURL)
		}
		c.mu.Unlock()
	}

	// System reports can carry other fields (e.g. command ACKs) but light
	// state is reported via print.lights_report, handled below.

	if r.Print == nil {
		return // not a print status report
	}

	p := r.Print
	s := c.Status()
	s.Online = true
	hadHMSErrors := len(s.HMSErrors) > 0

	// Light state — parse from print.lights_report (the actual wire format
	// for Bambu light state reports). The old system.ledctrl path only
	// carried command ACKs, not the live state.
	for _, lr := range p.LightsReport {
		if lr.Node == "chamber_light" {
			on := lr.Mode == "on"
			s.LightOn = &on
			break
		}
	}

	// Map states. Only update when gcode_state is explicitly provided;
	// heartbeat-style reports may omit it, and we must not clobber the
	// last-known state (e.g. "printing") with "idle" in that case.
	//
	// complete->idle latch: Bambu firmware reports SUCCESS (-> "complete")
	// briefly right after a print finishes, then settles to IDLE (-> "idle")
	// on the very next MQTT push. Applying that "idle" immediately would
	// clobber "complete" within 1-2 reports, causing a connected dashboard
	// client to see COMPLETE flash and vanish. Require
	// completeIdleStreakThreshold consecutive idle reports while latched to
	// "complete" before allowing the overwrite. Any other newly-reported
	// state (e.g. "printing" from a new print starting) still overrides
	// "complete" immediately — only the complete->idle edge is latched.
	if p.GcodeState != "" {
		newState := mapState(p.GcodeState)
		if s.State == "complete" && newState == "idle" {
			c.completeIdleStreak++
			if c.completeIdleStreak >= completeIdleStreakThreshold {
				s.State = newState
				c.completeIdleStreak = 0
			}
		} else {
			s.State = newState
			c.completeIdleStreak = 0
		}
	}

	// CurrentFile: set from gcode_file (preferred) or subtask_name (P1S
	// fallback).  Clear when the printer is explicitly idle — the print has
	// finished.  Only when gcode_state is explicitly provided to avoid
	// clobbering on heartbeat-style reports that omit gcode_state.
	if p.GcodeState != "" && s.State == "idle" {
		s.CurrentFile = ""
	} else if p.GcodeFile != nil && *p.GcodeFile != "" {
		s.CurrentFile = *p.GcodeFile
	} else if p.SubtaskName != nil && *p.SubtaskName != "" {
		// P1S uses subtask_name for the current print filename instead of
		// gcode_file during printing. Fall back to it when gcode_file is
		// absent or empty.
		s.CurrentFile = *p.SubtaskName
	}

	// Temperatures — only update when the field is present in the report.
	// Many status reports omit temperature fields, and Go defaults *float64 to nil.
	if p.BedTemper != nil {
		s.BedTemp = p.BedTemper
	}
	if p.NozzleTemper != nil {
		s.NozzleTemp = p.NozzleTemper
	}
	if p.BedTarget != nil {
		s.BedTargetTemp = p.BedTarget
	}
	if p.NozzleTarget != nil {
		s.NozzleTargetTemp = p.NozzleTarget
	}
	if p.ChamberTemper != nil {
		s.ChamberTemp = p.ChamberTemper
	} else if p.Info != nil && p.Info.Temp != nil {
		// H2S sends info.temp as a packed 32-bit integer:
		//   Low 16 bits  (val & 0xFFFF)      = current temperature in °C
		//   High 16 bits ((val >> 16) & 0xFFFF) = target temperature in °C
		raw := int64(*p.Info.Temp)
		current := float64(raw & 0xFFFF)
		target := float64((raw >> 16) & 0xFFFF)
		if current >= -50 && current <= 100 {
			s.ChamberTemp = &current
		} else {
			log.Printf("bambu %s: info.temp current out of range (raw=%d, current=%.0f), ignoring",
				c.cfg.ID, raw, current)
		}
		if target >= 0 && target <= 100 {
			s.ChamberTargetTemp = &target
		}
	}
	// Don't overwrite ChamberTargetTemp if it was already decoded from info.temp.
	if p.ChamberTargetTemper != nil && s.ChamberTargetTemp == nil {
		s.ChamberTargetTemp = p.ChamberTargetTemper
	}

	// HMS (Health Management System) codes — only update when the "hms" key
	// is present in this report (p.HMS != nil covers both a populated array
	// and an explicit empty array []); a heartbeat-style report that omits
	// "hms" entirely must not wipe previously-reported HMS state on its own.
	// An empty [] DOES count as present, so it clears both slices — that's
	// the explicit recovery signal.
	if p.HMS != nil {
		s.HMSErrors, s.HMSWarnings = splitHMS(p.HMS, c.model)
		c.hmsHealthyStreak = 0
	} else if p.GcodeState != "" && isHealthyGcodeState(p.GcodeState) {
		// Staleness decay: some firmware simply stops sending "hms" once a
		// condition resolves, instead of sending an explicit "hms: []". If
		// we only ever cleared on an explicit empty array, a printer that
		// never sends that empty array again would stay latched in
		// State="error" forever regardless of how healthy gcode_state looks.
		//
		// Require hmsHealthyStreakThreshold CONSECUTIVE reports with a
		// present, healthy gcode_state and no "hms" key before decaying —
		// not just one. A single such report is deliberately NOT enough
		// (see TestHandleReport_HMS_AbsentFieldDoesNotWipeExisting): one
		// heartbeat missing "hms" is common and unremarkable, so treating it
		// as an instant clear would be too eager and could paper over a
		// real, still-active fault the printer just didn't re-report yet.
		// Multiple consecutive ones alongside a healthy state machine is a
		// much stronger signal the condition actually resolved.
		c.hmsHealthyStreak++
		if c.hmsHealthyStreak >= hmsHealthyStreakThreshold {
			s.HMSErrors = nil
			s.HMSWarnings = nil
		}
	} else {
		// gcode_state absent, or present but not healthy (e.g. FAILED) —
		// doesn't count toward the decay streak either way.
		c.hmsHealthyStreak = 0
	}

	if p.McPercent != nil {
		s.Progress = float64(*p.McPercent) / 100.0
	}
	if p.McRemainingTime != nil {
		s.RemainingTime = *p.McRemainingTime * 60
	}
	if p.LayerNum != nil {
		s.CurrentLayer = *p.LayerNum
	}
	if p.TotalLayerNum != nil {
		s.TotalLayers = *p.TotalLayerNum
	}

	// Check for error state. HMS errors (severity fatal/serious) trip this
	// independently of print_error/gcode_state — this is the channel a
	// cover-off event on a P1S (no door sensor) actually surfaces through,
	// since print_error can stay 0 the whole time.
	if p.GcodeState == "FAILED" || (p.PrintError != nil && *p.PrintError != 0) || len(s.HMSErrors) > 0 {
		s.State = "error"
		if p.PrintError != nil && *p.PrintError != 0 {
			// print_error message takes precedence (backward compat).
			s.ErrorMsg = fmt.Sprintf("print_error=%d", *p.PrintError)
		} else if len(s.HMSErrors) > 0 {
			// Fallback: only HMS tripped it — summarize the HMS entries,
			// preferring each entry's human-readable message (falling back to
			// the raw code when no message was found in the vendored table).
			summaries := make([]string, len(s.HMSErrors))
			for i, e := range s.HMSErrors {
				summaries[i] = hmsEntrySummary(e)
			}
			s.ErrorMsg = strings.Join(summaries, "; ")
		}
	} else if s.State != "error" {
		s.ErrorMsg = ""
	} else if hadHMSErrors && p.GcodeState == "" {
		// Secondary un-latch case: HMS errors existed before this report and
		// are gone now (explicit "hms: []" above, or decayed via the
		// staleness streak), print_error/gcode_state=FAILED aren't tripping
		// it either — but this same report also omitted gcode_state, so the
		// normal "if p.GcodeState != {}" reassignment a few lines up never
		// ran and s.State is still latched to the stale "error" value. HMS
		// was the only thing keeping it there, and HMS no longer agrees, so
		// fall back to "idle" (mapState's own convention for an absent
		// gcode_state) rather than leaving it stuck on "error" indefinitely.
		s.State = "idle"
		s.ErrorMsg = ""
	}

	c.setStatus(s)
}

// --- Commands ---

// publishCommand publishes a command JSON payload to the printer's request topic.
func (c *Client) publishCommand(ctx context.Context, payload []byte) error {
	if c.mqttClient == nil || !c.mqttClient.IsConnected() {
		return fmt.Errorf("bambu %s: not connected to cloud MQTT", c.cfg.ID)
	}

	topic := fmt.Sprintf("device/%s/request", c.cfg.Serial)
	token := c.mqttClient.Publish(topic, 0, false, payload)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("bambu %s: command publish timeout", c.cfg.ID)
	}
	return token.Error()
}

// Pause pauses the current print job.
func (c *Client) Pause(ctx context.Context) error {
	return c.publishCommand(ctx, pauseCommand())
}

// Resume resumes a paused print job.
func (c *Client) Resume(ctx context.Context) error {
	return c.publishCommand(ctx, resumeCommand())
}

// Cancel stops and cancels the current print job.
func (c *Client) Cancel(ctx context.Context) error {
	return c.publishCommand(ctx, stopCommand())
}

// SkipObject attempts to skip the current object.
// Note: For Bambu, this uses the project_file command with skip_object param.
// The skip_objects command with obj_list may also work on newer firmware.
func (c *Client) SkipObject(ctx context.Context) error {
	return c.publishCommand(ctx, skipObjectCommand())
}

// SetBedTemp sets the bed heater target temperature via G-code M140.
func (c *Client) SetBedTemp(ctx context.Context, temp int) error {
	return c.publishCommand(ctx, setBedTempCommand(temp))
}

// SetNozzleTemp sets the primary nozzle target temperature via G-code M104.
func (c *Client) SetNozzleTemp(ctx context.Context, temp int) error {
	return c.publishCommand(ctx, setNozzleTempCommand(temp))
}

// SetChamberTemp sets the chamber heater target temperature via set_ctt.
func (c *Client) SetChamberTemp(ctx context.Context, temp int) error {
	return c.publishCommand(ctx, setCTTCommand(temp))
}

// SetLight turns the chamber light on or off.
func (c *Client) SetLight(ctx context.Context, on bool) error {
	return c.publishCommand(ctx, setLightCommand(on))
}

// Ensure Client satisfies the Printer interface.
var _ printers.Printer = (*Client)(nil)

// IsH2S returns true if the model name indicates an H2-series (or similar)
// printer with multiple cameras and RTSPS protocol.
// It matches both marketing names (e.g. "H2S") and Bambu Cloud API internal
// model codes (e.g. "O1S").
func IsH2S(model string) bool {
	switch strings.ToUpper(model) {
	case "H2S", "H2D", "H2C", "H2D PRO", "P2S", "X2D", "O1S":
		return true
	}
	return false
}
