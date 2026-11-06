package bambu

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// transportKind identifies how the dashboard reaches a printer's MQTT broker.
//
// The distinction matters because the two paths fail in opposite ways. Cloud
// depends on a credential that expires and on the wider internet; LAN depends on
// the printer being on the same network and on a toggle whose name changes with
// the firmware generation. A fleet has printers in every combination, and the
// only honest way to know which one works for a given unit is to try it.
type transportKind string

const (
	// transportLAN is the printer's own broker: ssl://<host>:8883, username
	// "bblp", password the device access code. Available without any toggle on
	// pre-Bambu-Connect firmware (the P1S in this fleet), and with Developer
	// Mode / LAN mode on later firmware.
	transportLAN transportKind = "lan"

	// transportCloud is Bambu's hosted broker: ssl://<region>.mqtt.bambulab.com
	// :8883, username "u_<user_id>", password the account access token.
	transportCloud transportKind = "cloud"
)

// Config values for PrinterDef.MQTTTransport.
const (
	transportAuto  = "auto"
	transportLANPin = "lan"
	transportCloudPin = "cloud"
)

// transportSpec is one concrete way to reach one printer's MQTT broker, fully
// resolved and ready to hand to Paho.
type transportSpec struct {
	kind     transportKind
	broker   string
	username string
	password string
}

// String is for logs: identifies the path without leaking the credential.
func (t transportSpec) String() string {
	return fmt.Sprintf("%s(%s user=%s)", t.kind, t.broker, t.username)
}

// transportCandidates returns the ways to reach this printer, best first.
//
// Ranking is deliberately *not* a firmware or model lookup. The protocol
// reference is explicit that local-mode toggles are named differently across
// firmware generations and that an open local port is not evidence that the
// broker will accept us — "authentication behavior, not port reachability, is
// the signal". So we order by what is configured and what has worked before,
// then let the MQTT handshake decide. This mirrors how command signing is
// already handled: send, watch for the rejection, latch the lesson.
//
// LAN is preferred when available: it is lower latency, it keeps working when
// the WAN or Bambu's cloud is down, and it does not depend on a credential that
// silently expires — which is exactly the failure that blinded two reachable
// printers for eight days.
func (c *Client) transportCandidates() []transportSpec {
	if c.brokerOverride != "" {
		// Test seam: a single explicit endpoint, no ranking. Connect's tests
		// point this at a local listener.
		return []transportSpec{{
			kind:     transportCloud,
			broker:   c.brokerOverride,
			username: "test",
			password: "test",
		}}
	}

	lan, lanOK := c.lanTransport()
	cloud, cloudOK := c.cloudTransport()

	switch strings.ToLower(strings.TrimSpace(c.cfg.MQTTTransport)) {
	case transportLANPin:
		if lanOK {
			return []transportSpec{lan}
		}
		c.noteTransportSkipped(transportLAN, "pinned to lan but no host/access code is configured")
		return nil
	case transportCloudPin:
		if cloudOK {
			return []transportSpec{cloud}
		}
		c.noteTransportSkipped(transportCloud, "pinned to cloud but no cloud credentials are available")
		return nil
	}

	var out []transportSpec
	// Sticky: keep whatever this printer last proved it could use, so a unit
	// whose LAN broker refuses third-party clients does not pay a failed
	// handshake before every cloud connect.
	switch c.preferredTransport() {
	case transportCloud:
		if cloudOK {
			out = append(out, cloud)
		}
		if lanOK {
			out = append(out, lan)
		}
	default:
		if lanOK {
			out = append(out, lan)
		}
		if cloudOK {
			out = append(out, cloud)
		}
	}
	return out
}

// lanTransport builds the local broker spec. Both a host and an access code are
// required: the access code is the device credential MQTT enforces, and without
// it the connect is refused the same way a wrong password is.
func (c *Client) lanTransport() (transportSpec, bool) {
	if c.cfg.Host == "" || c.cfg.AccessCode == "" || c.cfg.Serial == "" {
		return transportSpec{}, false
	}
	return transportSpec{
		kind:     transportLAN,
		broker:   fmt.Sprintf("ssl://%s:8883", c.cfg.Host),
		username: "bblp",
		password: c.cfg.AccessCode,
	}, true
}

// cloudTransport builds the hosted broker spec. Returns false when there is no
// account to authenticate as — a present-but-dead token still counts as
// available, because the refusal is what the auth watchdog reports on.
func (c *Client) cloudTransport() (transportSpec, bool) {
	if c.cloud == nil || c.cloud.Token() == "" || c.cloud.UserID() == "" {
		return transportSpec{}, false
	}
	return transportSpec{
		kind:     transportCloud,
		broker:   "ssl://" + MQTTBroker(c.cloud.region),
		username: c.cloud.MQTTUsername(),
		password: c.cloud.MQTTPassword(),
	}, true
}

// preferredTransport returns the last transport this printer proved it could
// use, defaulting to LAN.
func (c *Client) preferredTransport() transportKind {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.activeTransport != "" {
		return c.activeTransport
	}
	return transportLAN
}

// setTransportLive records the transport backing the current session. It is the
// single source of truth for "how are we reaching this printer right now", and
// it is what makes a mixed fleet legible from the dashboard instead of
// something you have to infer from which cards happen to be updating.
func (c *Client) setTransportLive(kind transportKind) {
	c.mu.Lock()
	changed := c.activeTransport != kind
	c.activeTransport = kind
	c.mu.Unlock()

	if changed {
		logTransportChange(c.cfg.ID, kind)
	}

	s := c.Status()
	s.Transport = string(kind)
	c.setStatus(s)
}

// noteTransportSkipped logs a pin that cannot be honoured, rather than silently
// falling back to something the operator explicitly ruled out.
func (c *Client) noteTransportSkipped(kind transportKind, why string) {
	log.Printf("bambu %s: mqtt_transport=%s ignored: %s", c.cfg.ID, kind, why)
}

// logTransportChange announces which path a printer ended up on. Worth a log
// line of its own: on a mixed fleet this is the fact you reach for first when a
// card behaves unexpectedly, and it is the only place a wrong probe verdict
// becomes visible.
func logTransportChange(id string, kind transportKind) {
	switch kind {
	case transportLAN:
		log.Printf("bambu %s: live over LOCAL MQTT (printer's own broker)", id)
	default:
		log.Printf("bambu %s: live over CLOUD MQTT", id)
	}
}

// transportLostDelay is how long to wait before walking the candidate list
// again after a session drops. Short enough that a failover is barely
// noticeable, long enough that a broker refusing us does not spin.
const transportLostDelay = 3 * time.Second

// noTransportRetryEvery bounds how long the dashboard waits before re-checking
// for a transport when none is currently configured. ReloadConfig builds fresh
// clients, so this mainly covers credentials appearing without a restart.
const noTransportRetryEvery = 2 * time.Minute
