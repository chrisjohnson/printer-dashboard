package bambu

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"strconv"
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

// initialConnectBackoffBase is the starting backoff duration used by
// Connect's initial-connect retry loop (1s, 2s, 4s, 8s, 16s, capped at
// initialConnectBackoffMax). Retries are indefinite — see Connect's doc
// comment for rationale.
const initialConnectBackoffBase = 1 * time.Second

// initialConnectBackoffMax caps the doubling backoff in Connect's retry
// loop, matching the ceiling already used for AutoReconnect via
// SetMaxReconnectInterval below, for consistency between the two retry
// paths.
const initialConnectBackoffMax = 30 * time.Second

// reportSilenceTimeout is the duration to wait after subscribing and sending
// pushall before warning that the printer may be in LAN Mode (which prevents
// cloud MQTT reports from arriving). A printer in LAN Mode stops publishing
// to Bambu's cloud MQTT broker by design, so the dashboard shows it as
// permanently offline with no indication of why. This timeout surfaces that
// condition as a actionable warning rather than silent failure.
const reportSilenceTimeout = 60 * time.Second

// validateTemp checks that a temperature value is within [min, max] and
// returns a pointer to it if valid, or nil (with a log warning) if out
// of range.  The current codebase uses two temperature ranges for chamber
// values: -50..100 for ChamberTemp (ambient temperatures can dip below
// zero) and 0..100 for ChamberTargetTemp (the chamber heater can only
// raise temperature from ambient, never below it).
func validateTemp(id string, value float64, min, max float64) *float64 {
	if value < min || value > max {
		log.Printf("bambu %s: temperature %.1f out of range [%.0f, %.0f], ignoring", id, value, min, max)
		return nil
	}
	return &value
}

// Client implements the printers.Printer interface for Bambu Lab printers.
//
// Default mode: cloud MQTT via Bambu's infrastructure (no LAN mode required).
// LAN Mode support: surface a warning if no reports arrive within
// reportSilenceTimeout (see K-106). Full local broker support is tracked
// separately and not implemented here.
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

	// brokerOverride, when non-empty, replaces the derived cloud MQTT broker
	// address in Connect. Test-only seam so unit tests can point the client
	// at a local TCP listener instead of Bambu's real cloud broker.
	brokerOverride string

	// connectBackoffBase overrides initialConnectBackoffBase when non-zero.
	// Test-only seam to keep the initial-connect retry loop's backoff
	// schedule fast in unit tests.
	connectBackoffBase time.Duration

	// connectBackoffMax overrides initialConnectBackoffMax when non-zero.
	// Test-only seam, paired with connectBackoffBase.
	connectBackoffMax time.Duration

	// firstReportCh is closed when the first report arrives after a
	// (re)connection. Used by the silence-warning goroutine started in
	// onConnect to stop waiting once a report has been received.
	firstReportCh chan struct{}

	// reportSilenceWarned guards against logging the LAN Mode silence
	// warning more than once per connection lifecycle.
	reportSilenceWarned sync.Once
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
		if UsesRTSPS(c.model) {
			// RTSPS stream on port 322 (requires LAN mode enabled on printer).
			streams = append(streams, printers.CameraStream{
				URL:   fmt.Sprintf("rtsps://bblp:%s@%s:322/streaming/live/1", c.cfg.AccessCode, c.cfg.Host),
				Type:  "internal",
				Label: "BirdsEye Camera",
			})
		} else {
			// P1S, A1 series use bambus:// binary TLS protocol on port 6000.
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
//
// The initial connect attempt is retried indefinitely with doubling backoff
// (1s, 2s, 4s, ... capped at initialConnectBackoffMax) if it fails or times
// out. There is no attempt ceiling: once connected, Paho's own AutoReconnect
// (configured below) takes over for connection-lost-after-success, and that
// path also retries forever — this keeps the two retry paths consistent, and
// the current status model has no "permanently failed" state to fall back to
// anyway. Retries stop promptly if ctx is cancelled.
func (c *Client) Connect(ctx context.Context) error {
	broker := "ssl://" + MQTTBroker(c.cloud.region)
	if c.brokerOverride != "" {
		broker = c.brokerOverride
	}

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

	if err := c.connectWithRetry(ctx, broker); err != nil {
		// Only returns non-nil when ctx was cancelled before a successful
		// connect — report that as a clean (nil) shutdown-triggered return,
		// matching the pre-retry-loop contract that Connect only returns a
		// non-nil error for a genuine connect failure. Since retries are
		// indefinite, the only way out without a successful connect is
		// cancellation.
		return nil
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

// connectWithRetry attempts the initial MQTT connect, retrying indefinitely
// with doubling backoff on failure/timeout until it succeeds or ctx is
// cancelled. Returns nil on success, or ctx.Err() if ctx was cancelled
// before a successful connect.
func (c *Client) connectWithRetry(ctx context.Context, broker string) error {
	backoffBase := initialConnectBackoffBase
	if c.connectBackoffBase > 0 {
		backoffBase = c.connectBackoffBase
	}
	backoffMax := initialConnectBackoffMax
	if c.connectBackoffMax > 0 {
		backoffMax = c.connectBackoffMax
	}

	backoff := backoffBase
	attempt := 0
	for {
		attempt++

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		token := c.mqttClient.Connect()
		var connectErr error
		if !token.WaitTimeout(15 * time.Second) {
			connectErr = fmt.Errorf("bambu %s: cloud MQTT connection timeout to %s", c.cfg.ID, broker)
		} else if err := token.Error(); err != nil {
			connectErr = fmt.Errorf("bambu %s: cloud MQTT connect: %w", c.cfg.ID, err)
		}

		if connectErr == nil {
			return nil
		}

		s := c.Status()
		s.Online = false
		s.State = "error"
		s.ErrorMsg = fmt.Sprintf("MQTT connect failed: %v", connectErr)
		c.setStatus(s)

		log.Printf("bambu %s: cloud MQTT initial connect attempt %d failed: %v (retry in %v)",
			c.cfg.ID, attempt, connectErr, backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// onConnect is called when the MQTT client connects (or reconnects).
func (c *Client) onConnect(client mqtt.Client) {
	log.Printf("bambu %s: cloud MQTT connected (or reconnected)", c.cfg.ID)

	// Reset the first-report channel so the silence warning timer can
	// monitor the next connection's report stream.
	c.mu.Lock()
	c.firstReportCh = make(chan struct{})
	c.mu.Unlock()

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

	// Start a goroutine that warns if no reports arrive within the
	// silence timeout. This catches the common case of a printer in
	// LAN Mode (which stops publishing to cloud MQTT by design) —
	// without this warning, the dashboard shows the printer as
	// permanently offline with no indication of why.
	go c.silenceWarning(client)
}

// silenceWarning waits for the first report after connection. If none
// arrives within reportSilenceTimeout, it logs a warning that the
// printer may be in LAN Mode.
func (c *Client) silenceWarning(_ mqtt.Client) {
	select {
	case <-c.firstReportCh:
		return // first report arrived — nothing to warn about
	case <-time.After(reportSilenceTimeout):
		c.mu.RLock()
		serial := c.cfg.Serial
		c.mu.RUnlock()
		c.reportSilenceWarned.Do(func() {
			log.Printf("bambu %s: no MQTT reports received in %.0f seconds after subscribing — printer may be in LAN Mode (which stops publishing to cloud MQTT by design). See GitHub issue #22.", serial, reportSilenceTimeout.Seconds())
		})
	}
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
	// Signal the silence-warning goroutine that a report arrived, so it
	// stops waiting and doesn't emit a false LAN Mode warning.
	c.mu.RLock()
	firstCh := c.firstReportCh
	c.mu.RUnlock()
	if firstCh != nil {
		select {
		case <-firstCh:
			// Already closed (first report already arrived).
		default:
			close(firstCh)
		}
	}

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
	var newState string
	if p.GcodeState != "" {
		newState = mapState(p.GcodeState)
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
	//
	// Deliberately keyed off the raw per-report `newState` (computed above),
	// not the latched `s.State`: CurrentFile clearing should reflect what the
	// firmware just reported, not the delayed/derived UI-display value from
	// the complete->idle latch. This means CurrentFile clears one report
	// earlier than the COMPLETE badge does (at the first IDLE after SUCCESS,
	// while State is still latched at "complete" for one more report) — the
	// filename disappears promptly while the COMPLETE badge lingers briefly
	// by design. This is intentional: it decouples CurrentFile's semantics
	// from the latch threshold so future latch tuning can't silently shift
	// CurrentFile timing too. See TestHandleReport_IdleClearsCurrentFile and
	// TestHandleReport_SuccessIdleIdleSequence for the exact locked-in timing.
	if p.GcodeState != "" && newState == "idle" {
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
		// Direct-wire chamber_temper — validate against the same bounds as
		// info.temp fallback (-50..100): ambient temperatures can dip
		// below zero, unlike the heater target which is always non-negative.
		// Only update status on valid values; out-of-range values are
		// ignored to avoid clobbering a previously-good reading.
		if v := validateTemp(c.cfg.ID, *p.ChamberTemper, -50, 100); v != nil {
			s.ChamberTemp = v
		}
	} else if p.Info != nil && p.Info.Temp != nil {
		// H2S sends info.temp as a packed 32-bit integer:
		//   Low 16 bits  (val & 0xFFFF)      = current temperature in °C
		//   High 16 bits ((val >> 16) & 0xFFFF) = target temperature in °C
		//
		// If the wire sends a non-integer (e.g. firmware changes the
		// encoding in the future), int64() truncates silently — warn so
		// that drift surfaces during testing rather than silently
		// discarding fractional parts in production.
		if *p.Info.Temp != float64(int64(*p.Info.Temp)) {
			log.Printf("bambu %s: info.temp has fractional part %.4f, truncating to %d",
				c.cfg.ID, *p.Info.Temp, int64(*p.Info.Temp))
		}
		raw := int64(*p.Info.Temp)
		current := float64(raw & 0xFFFF)
		target := float64((raw >> 16) & 0xFFFF)
		if v := validateTemp(c.cfg.ID, current, -50, 100); v != nil {
			s.ChamberTemp = v
		}
		if v := validateTemp(c.cfg.ID, target, 0, 100); v != nil {
			s.ChamberTargetTemp = v
		}
	}
	// Don't overwrite ChamberTargetTemp if it was already decoded from info.temp.
	if p.ChamberTargetTemper != nil && s.ChamberTargetTemp == nil {
		// Direct-wire chamber_target_temper — heater target is always
		// non-negative (0..100) since the chamber can only heat above
		// ambient, never cool below it.
		if v := validateTemp(c.cfg.ID, *p.ChamberTargetTemper, 0, 100); v != nil {
			s.ChamberTargetTemp = v
		}
	}

	// AMS (Automatic Material System) data — parse when present in the report.
	// P1S AMS 1 does not report humidity/temp fields; H2S AMS 2 Pro and X1 series do.
	// P1S sends delta updates (only changed fields); H2S sends full updates.
	if p.Ams != nil {
		s.AMSUnits = parseAMSData(p.Ams)
		// Parse active tray ID: (ams_id * 4) + tray_id, 254=external, 255=none
		if p.Ams.TrayNow != "" {
			if trayNow, err := strconv.Atoi(p.Ams.TrayNow); err == nil {
				s.ActiveTrayID = trayNow
			} else {
				s.ActiveTrayID = -1
			}
		} else {
			s.ActiveTrayID = -1
		}
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

	// HomeFlag: Bambu reports this as a bitmask, per the OpenBambuAPI/pybambu
	// convention — bit 0 (value 1, "AXIS_HOMED") indicates the X/Y/Z axes are
	// homed; other bits encode unrelated state (e.g. auto-leveling, filament
	// presence) that we don't need here. Treat bit 0 as the sole "homed"
	// signal: set when it's on, clear when it's off. home_flag is always
	// present (not a pointer) on Bambu reports carrying a "print" section, so
	// this updates on every report rather than being gated on presence like
	// the pointer fields above.
	homed := p.HomeFlag&0x1 != 0
	s.Homed = &homed

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

// publishCommand publishes a command JSON payload to the printer's request
// topic. cmdName is a short, human-readable identifier for the command being
// sent (e.g. "pause", "set_bed_temp") used only for audit logging — it must
// never contain the payload or any secrets, just a name.
func (c *Client) publishCommand(ctx context.Context, cmdName string, payload []byte) error {
	if c.mqttClient == nil || !c.mqttClient.IsConnected() {
		return fmt.Errorf("bambu %s: not connected to cloud MQTT", c.cfg.ID)
	}

	log.Printf("bambu %s: sending command %s", c.cfg.ID, cmdName)

	topic := fmt.Sprintf("device/%s/request", c.cfg.Serial)
	token := c.mqttClient.Publish(topic, 0, false, payload)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("bambu %s: command publish timeout", c.cfg.ID)
	}
	return token.Error()
}

// Pause pauses the current print job.
func (c *Client) Pause(ctx context.Context) error {
	return c.publishCommand(ctx, "pause", pauseCommand())
}

// Resume resumes a paused print job.
func (c *Client) Resume(ctx context.Context) error {
	return c.publishCommand(ctx, "resume", resumeCommand())
}

// Cancel stops and cancels the current print job.
func (c *Client) Cancel(ctx context.Context) error {
	return c.publishCommand(ctx, "stop", stopCommand())
}

// SkipObject attempts to skip the current object.
// Note: For Bambu, this uses the project_file command with skip_object param.
// The skip_objects command with obj_list may also work on newer firmware.
func (c *Client) SkipObject(ctx context.Context) error {
	return c.publishCommand(ctx, "skip_object", skipObjectCommand())
}

// SetBedTemp sets the bed heater target temperature via G-code M140.
func (c *Client) SetBedTemp(ctx context.Context, temp int) error {
	return c.publishCommand(ctx, "set_bed_temp", setBedTempCommand(temp))
}

// SetNozzleTemp sets the primary nozzle target temperature via G-code M104.
func (c *Client) SetNozzleTemp(ctx context.Context, temp int) error {
	return c.publishCommand(ctx, "set_nozzle_temp", setNozzleTempCommand(temp))
}

// SetChamberTemp sets the chamber heater target temperature via set_ctt.
func (c *Client) SetChamberTemp(ctx context.Context, temp int) error {
	return c.publishCommand(ctx, "set_ctt", setCTTCommand(temp))
}

// SetLight turns the chamber light on or off.
func (c *Client) SetLight(ctx context.Context, on bool) error {
	return c.publishCommand(ctx, "ledctrl", setLightCommand(on))
}

// HomeAll homes all axes via G-code G28.
func (c *Client) HomeAll(ctx context.Context) error {
	return c.publishCommand(ctx, "home_all", homeAllCommand())
}

// Jog moves the toolhead by the given relative deltas (mm) at the given
// feedrate (mm/min) via G-code.
func (c *Client) Jog(ctx context.Context, x, y, z float64, speedMMPerMin int) error {
	return c.publishCommand(ctx, "jog", jogCommand(x, y, z, speedMMPerMin))
}

// SetHomed is a no-op for Bambu printers — they expose homed_axes in the
// device report so we don't need to track homing state client-side.
func (c *Client) SetHomed(homed *bool) {
	// No-op: Bambu devices report homed_axes natively.
}

// parseAMSData converts the wire-format amsData struct into the public
// printers.AMSUnit format. Handles P1S delta updates (missing fields) and
// H2S full updates uniformly.
func parseAMSData(ams *amsData) []printers.AMSUnit {
	if ams == nil || len(ams.AMS) == 0 {
		return nil
	}

	units := make([]printers.AMSUnit, 0, len(ams.AMS))
	for _, au := range ams.AMS {
		// Parse AMS unit ID
		unitID, err := strconv.Atoi(au.ID)
		if err != nil {
			unitID = 0 // fallback
		}

		// Parse trays
		trays := make([]printers.FilamentSlot, 0, len(au.Tray))
		for _, t := range au.Tray {
			// Parse tray index
			trayIdx, err := strconv.Atoi(t.ID)
			if err != nil {
				trayIdx = 0
			}

			// Parse temperature bounds (wire sends as string)
			nozzleMin, _ := strconv.Atoi(t.NozzleTempMin)
			nozzleMax, _ := strconv.Atoi(t.NozzleTempMax)

			// Determine if loaded: state 2=loaded, 3=ready, 10=reading, 11=loaded+data
			loaded := t.State == 2 || t.State == 3 || t.State == 10 || t.State == 11

			trays = append(trays, printers.FilamentSlot{
				Index:        trayIdx,
				Type:         t.TrayType,
				Color:        t.TrayColor,
				InfoIdx:      t.TrayInfoIdx,
				NozzleTempMin: nozzleMin,
				NozzleTempMax: nozzleMax,
				RemainingMM:  t.Remain,
				Weight:       t.TrayWeight,
				TagUID:       t.TagUID,
				Loaded:       loaded,
			})
		}

		units = append(units, printers.AMSUnit{
			ID:          unitID,
			Humidity:    au.Humidity,    // empty on P1S AMS 1
			HumidityRaw: au.HumidityRaw, // empty on P1S AMS 1
			Temp:        au.Temp,        // empty on P1S AMS 1
			Trays:       trays,
		})
	}
	return units
}

// Ensure Client satisfies the Printer interface.
var _ printers.Printer = (*Client)(nil)

// UsesRTSPS returns true if the model's camera uses the RTSPS protocol
// on port 322 (requires LAN mode enabled on printer). This covers both
// the H2 series (which also has H2-specific semantics via IsH2S) and
// the X1 series, which serves RTSPS but does NOT share H2 semantics
// like multi-camera or HasChamber.
func UsesRTSPS(model string) bool {
	return IsH2S(model) || IsX1Series(model)
}

// IsX1Series returns true if the model name indicates an X1-series printer
// (X1, X1 Carbon, X1E) whose camera serves RTSPS on port 322 rather than
// the bambus:// binary protocol on port 6000 used by P1/A1 series.
func IsX1Series(model string) bool {
	switch strings.ToUpper(model) {
	case "BL-P001": // X1 Carbon
		return true
	}
	return false
}

// IsH2S returns true if the model name indicates an H2-series (or similar)
// printer with H2-specific semantics (HasChamber, multi-camera handling).
// It matches both marketing names (e.g. "H2S") and Bambu Cloud API internal
// model codes (e.g. "O1S").
func IsH2S(model string) bool {
	switch strings.ToUpper(model) {
	case "H2S", "H2D", "H2C", "H2D PRO", "P2S", "X2D", "O1S":
		return true
	}
	return false
}
