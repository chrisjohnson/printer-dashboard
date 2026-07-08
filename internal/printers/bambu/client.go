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

// Client implements the printers.Printer interface for Bambu Lab printers.
type Client struct {
	cfg        config.PrinterDef
	mu         sync.RWMutex
	status     printers.PrinterStatus
	mqttClient mqtt.Client
	camURLs    []string
}

// New creates a new Bambu printer client from the given configuration.
func New(cfg config.PrinterDef) *Client {
	status := printers.PrinterStatus{
		ID:   cfg.ID,
		Name: cfg.Name,
		Type: "bambu",
	}

	// Build camera URLs (printer provides MJPEG on port 6000)
	camURLs := []string{
		fmt.Sprintf("http://%s:6000/?token=%s", cfg.Host, cfg.AccessCode),
	}

	return &Client{
		cfg:     cfg,
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
	return c.camURLs
}

// Status returns the current cached status. Safe for concurrent use.
func (c *Client) Status() printers.PrinterStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// setStatus updates the cached status under the write lock.
func (c *Client) setStatus(s printers.PrinterStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = s
}

// Connect establishes the MQTT connection and begins listening for reports.
// Blocks until the context is cancelled (caller should run in a goroutine).
func (c *Client) Connect(ctx context.Context) error {
	broker := fmt.Sprintf("ssl://%s:%d", c.cfg.Host, c.cfg.Port)
	if c.cfg.Port == 0 {
		broker = fmt.Sprintf("ssl://%s:8883", c.cfg.Host)
	}

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("printer-dashboard-%s", c.cfg.ID)).
		SetUsername("bblp").
		SetPassword(c.cfg.AccessCode).
		SetTLSConfig(&tls.Config{
			InsecureSkipVerify: true, // Bambu uses self-signed certs
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

	// Connect (with timeout)
	token := c.mqttClient.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return fmt.Errorf("bambu %s: MQTT connection timeout", c.cfg.ID)
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
		return fmt.Errorf("bambu %s: MQTT connect: %w", c.cfg.ID, err)
	}

	log.Printf("bambu %s: connected to MQTT broker at %s", c.cfg.ID, broker)

	// Block until context is cancelled (keep the goroutine alive)
	<-ctx.Done()

	// Disconnect
	if c.mqttClient != nil && c.mqttClient.IsConnected() {
		c.mqttClient.Disconnect(1000)
		log.Printf("bambu %s: disconnected", c.cfg.ID)
	}
	return nil
}

// onConnect is called when the MQTT client connects (or reconnects).
func (c *Client) onConnect(client mqtt.Client) {
	log.Printf("bambu %s: MQTT connected (or reconnected)", c.cfg.ID)

	// Mark online
	c.setStatus(printers.PrinterStatus{
		ID:   c.cfg.ID,
		Name: c.cfg.Name,
		Type: "bambu",
		Online: true,
	})

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
}

// onConnectionLost is called when the MQTT connection is lost.
func (c *Client) onConnectionLost(client mqtt.Client, err error) {
	log.Printf("bambu %s: MQTT connection lost: %v", c.cfg.ID, err)
	s := c.Status()
	s.Online = false
	s.State = "error"
	s.ErrorMsg = fmt.Sprintf("MQTT disconnected: %v", err)
	c.setStatus(s)
}

// onReconnecting is called when the client begins reconnecting.
func (c *Client) onReconnecting(client mqtt.Client, opts *mqtt.ClientOptions) {
	log.Printf("bambu %s: MQTT reconnecting...", c.cfg.ID)
}

// handleReport processes incoming MQTT messages on the report topic.
func (c *Client) handleReport(_ mqtt.Client, msg mqtt.Message) {
	r, err := parseReport(msg.Payload())
	if err != nil {
		log.Printf("bambu %s: failed to parse report: %v\n  payload: %s", c.cfg.ID, err, string(msg.Payload()))
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
	s.CurrentFile = p.GcodeFile
	s.BedTemp = p.BedTemper
	s.BedTargetTemp = p.BedTarget
	s.NozzleTemp = p.NozzleTemper
	s.NozzleTargetTemp = p.NozzleTarget

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

	// Clear error message when not in error state
	if s.State != "error" {
		s.ErrorMsg = ""
	}

	c.setStatus(s)
}

// --- Commands ---

// publishCommand publishes a command JSON payload to the printer's request topic.
func (c *Client) publishCommand(ctx context.Context, payload []byte) error {
	if c.mqttClient == nil || !c.mqttClient.IsConnected() {
		return fmt.Errorf("bambu %s: not connected", c.cfg.ID)
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
func (c *Client) SkipObject(ctx context.Context) error {
	return c.publishCommand(ctx, skipObjectCommand())
}

// Ensure Client satisfies the Printer interface.
var _ printers.Printer = (*Client)(nil)
