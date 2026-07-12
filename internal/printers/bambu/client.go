package bambu

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

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
}

// New creates a new Bambu printer client for cloud MQTT connectivity.
//
// The cloud client must already be authenticated (Login or LoginWithToken called).
func New(cfg config.PrinterDef, cloud *BambuCloudClient) *Client {
	status := printers.PrinterStatus{
		ID:   cfg.ID,
		Name: cfg.Name,
		Type: "bambu",
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
func (c *Client) SetModel(model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.model = model
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
		c.setStatus(printers.PrinterStatus{
			ID:     c.cfg.ID,
			Name:   c.cfg.Name,
			Type:   "bambu",
			Online: false,
			State:  "error",
			ErrorMsg: fmt.Sprintf("MQTT connect failed: %v", err),
		})
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

	if r.Print == nil {
		return // not a print status report
	}

	p := r.Print
	s := c.Status()
	s.Online = true

	// Map states. Only update when gcode_state is explicitly provided;
	// heartbeat-style reports may omit it, and we must not clobber the
	// last-known state (e.g. "printing") with "idle" in that case.
	if p.GcodeState != "" {
		s.State = mapState(p.GcodeState)
	}
	if p.GcodeFile != nil && *p.GcodeFile != "" {
		s.CurrentFile = *p.GcodeFile
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
		// H2S (O1S) may report chamber temp via info.temp. Some firmware
		// versions send it as a scaled integer (real_temp × 100000) rather
		// than degrees Celsius. Detect and convert.
		temp := *p.Info.Temp
		if temp > 500 {
			// Likely a raw sensor value — convert to °C.
			temp /= 100000
		}
		// Sanity check: chamber temperature must be in a plausible range.
		if temp >= -50 && temp <= 100 {
			s.ChamberTemp = &temp
		} else {
			log.Printf("bambu %s: info.temp out of range (raw=%.0f, scaled=%.1f), ignoring",
				c.cfg.ID, *p.Info.Temp, temp)
		}
	}
	if p.ChamberTargetTemper != nil {
		s.ChamberTargetTemp = p.ChamberTargetTemper
	}

	if p.McPercent != nil {
		s.Progress = float64(*p.McPercent) / 100.0
	}
	if p.McRemainingTime != nil {
		s.RemainingTime = *p.McRemainingTime
	}
	if p.LayerNum != nil {
		s.CurrentLayer = *p.LayerNum
	}
	if p.TotalLayerNum != nil {
		s.TotalLayers = *p.TotalLayerNum
	}

	// Check for error state
	if p.GcodeState == "FAILED" || p.PrintError != nil && *p.PrintError != 0 {
		s.State = "error"
		if p.PrintError != nil {
			s.ErrorMsg = fmt.Sprintf("print_error=%d", *p.PrintError)
		}
	} else if s.State != "error" {
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
