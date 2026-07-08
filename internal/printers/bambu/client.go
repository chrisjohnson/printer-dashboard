package bambu

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// Client implements the printers.Printer interface for Bambu Lab printers
// via Bambu's cloud MQTT infrastructure. No LAN mode or developer mode required.
type Client struct {
	cfg        config.PrinterDef
	cloud      *BambuCloudClient
	mu         sync.RWMutex
	status     printers.PrinterStatus
	mqttClient mqtt.Client
	camURLs    []string

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

	// Build camera URLs if we have the local IP and access code.
	// The local camera stream on port 6000 works even without LAN mode enabled.
	camURLs := []string{}
	if cfg.Host != "" && cfg.AccessCode != "" {
		camURLs = append(camURLs,
			fmt.Sprintf("http://%s:6000/?token=%s", cfg.Host, cfg.AccessCode),
		)
	}

	return &Client{
		cfg:     cfg,
		cloud:   cloud,
		status:  status,
		camURLs: camURLs,
	}
}

// ID returns the printer's unique identifier.
func (c *Client) ID() string { return c.cfg.ID }

// Name returns the printer's human-readable name.
func (c *Client) Name() string { return c.cfg.Name }

// CameraURLs returns the printer's camera stream URLs.
func (c *Client) CameraURLs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.camURLs
}

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

	if r.Print == nil {
		return // not a print status report
	}

	p := r.Print
	s := c.Status()
	s.Online = true

	// Map states
	s.State = mapState(p.GcodeState)
	if p.GcodeFile != nil && *p.GcodeFile != "" {
		s.CurrentFile = *p.GcodeFile
	}

	// Temperatures — only update when the field is present in the report.
	// Many status reports omit temperature fields, and Go defaults *float64 to nil.
	if p.BedTemper != nil {
		s.BedTemp = *p.BedTemper
	}
	if p.NozzleTemper != nil {
		s.NozzleTemp = *p.NozzleTemper
	}
	if p.BedTarget != nil {
		s.BedTargetTemp = *p.BedTarget
	}
	if p.NozzleTarget != nil {
		s.NozzleTargetTemp = *p.NozzleTarget
	}
	if p.ChamberTemper != nil {
		s.ChamberTemp = *p.ChamberTemper
	} else if p.Info != nil && p.Info.Temp != nil {
		// H2S (O1S) reports chamber temp via info.temp instead of chamber_temper
		s.ChamberTemp = *p.Info.Temp
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
