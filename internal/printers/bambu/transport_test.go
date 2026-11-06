package bambu

import (
	"errors"
	"strings"
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

func transportTestClient(t *testing.T, def config.PrinterDef, cloud *BambuCloudClient) *Client {
	t.Helper()
	if def.ID == "" {
		def = testPrinterClientConfig
	}
	c := New(def, cloud)
	c.status = printers.PrinterStatus{ID: def.ID, Name: def.Name, Type: "bambu", Online: true, State: "printing"}
	markReported(c)
	return c
}

func cloudWithCreds() *BambuCloudClient {
	cl := &BambuCloudClient{token: "tok", userID: "999", region: "global"}
	return cl
}

// TestTransportCandidates_RanksLANFirst pins the default ordering.
//
// LAN is preferred not because it is always available but because when it IS
// available it fails better: lower latency, survives an internet outage, and
// does not depend on a credential that expires silently. Cloud is the fallback
// and the remote-access path, not the primary.
func TestTransportCandidates_RanksLANFirst(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())

	got := c.transportCandidates()
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2 (lan then cloud): %+v", len(got), got)
	}
	if got[0].kind != transportLAN {
		t.Errorf("first candidate = %q, want lan", got[0].kind)
	}
	if got[1].kind != transportCloud {
		t.Errorf("second candidate = %q, want cloud", got[1].kind)
	}
	if got[0].username != "bblp" {
		t.Errorf("lan username = %q, want bblp", got[0].username)
	}
	if got[0].password != testPrinterClientConfig.AccessCode {
		t.Error("lan password must be the device access code")
	}
	if !strings.Contains(got[0].broker, testPrinterClientConfig.Host) {
		t.Errorf("lan broker = %q, want it to name the printer's own address", got[0].broker)
	}
}

// TestTransportCandidates_DegradesToWhatIsConfigured covers a mixed fleet: a
// printer with no local address is cloud-only, and one with no account is
// LAN-only. Neither should offer a candidate it cannot authenticate against.
func TestTransportCandidates_DegradesToWhatIsConfigured(t *testing.T) {
	t.Run("no host means cloud only", func(t *testing.T) {
		def := testPrinterClientConfig
		def.Host = ""
		c := transportTestClient(t, def, cloudWithCreds())

		got := c.transportCandidates()
		if len(got) != 1 || got[0].kind != transportCloud {
			t.Errorf("candidates = %+v, want cloud only", got)
		}
	})

	t.Run("no cloud means lan only", func(t *testing.T) {
		c := transportTestClient(t, testPrinterClientConfig, nil)

		got := c.transportCandidates()
		if len(got) != 1 || got[0].kind != transportLAN {
			t.Errorf("candidates = %+v, want lan only", got)
		}
	})

	t.Run("neither means no candidates", func(t *testing.T) {
		def := testPrinterClientConfig
		def.Host = ""
		def.AccessCode = ""
		c := transportTestClient(t, def, nil)

		if got := c.transportCandidates(); len(got) != 0 {
			t.Errorf("candidates = %+v, want none", got)
		}
	})
}

// TestTransportCandidates_PinHonouredAndExplained checks that an explicit
// mqtt_transport pin is respected, and that an unsatisfiable pin says so rather
// than quietly falling back to the transport the operator ruled out.
func TestTransportCandidates_PinHonouredAndExplained(t *testing.T) {
	t.Run("pin lan", func(t *testing.T) {
		def := testPrinterClientConfig
		def.MQTTTransport = "lan"
		c := transportTestClient(t, def, cloudWithCreds())

		got := c.transportCandidates()
		if len(got) != 1 || got[0].kind != transportLAN {
			t.Errorf("candidates = %+v, want lan only", got)
		}
	})

	t.Run("pin cloud", func(t *testing.T) {
		def := testPrinterClientConfig
		def.MQTTTransport = "cloud"
		c := transportTestClient(t, def, cloudWithCreds())

		got := c.transportCandidates()
		if len(got) != 1 || got[0].kind != transportCloud {
			t.Errorf("candidates = %+v, want cloud only", got)
		}
	})

	t.Run("pin lan with no local config yields nothing, not a silent cloud fallback", func(t *testing.T) {
		def := testPrinterClientConfig
		def.MQTTTransport = "lan"
		def.Host = ""
		c := transportTestClient(t, def, cloudWithCreds())

		if got := c.transportCandidates(); len(got) != 0 {
			t.Errorf("candidates = %+v, want none — falling back to cloud would contradict the pin", got)
		}
	})
}

// TestTransportCandidates_StickyAfterSuccess is what stops a fleet of Path A/B
// printers (local port open, third-party clients refused) from paying a failed
// LAN handshake before every cloud reconnect.
func TestTransportCandidates_StickyAfterSuccess(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())

	c.setTransportLive(transportCloud)

	got := c.transportCandidates()
	if len(got) != 2 || got[0].kind != transportCloud || got[1].kind != transportLAN {
		t.Errorf("candidates after a proven cloud session = %+v, want cloud first", got)
	}
}

// TestMarkCloudAuthRejected_LiveLANIsDegradedNotBlind is the semantic heart of
// the transport work, and the correction to PR #40's assumption that cloud is
// the only path.
//
// A printer streaming every reading over its own broker is NOT a printer the
// dashboard cannot see. Flagging it stale would have the dashboard shouting
// "I cannot see this printer" about a printer it is looking at directly — and
// would disable every control on a card that is working perfectly.
func TestMarkCloudAuthRejected_LiveLANIsDegradedNotBlind(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
	c.setTransportLive(transportLAN)

	c.markCloudAuthRejected()

	s := c.Status()
	if s.AuthInvalid {
		t.Error("AuthInvalid = true; a LAN-live printer is not blind, only degraded")
	}
	if !s.CloudDegraded {
		t.Error("CloudDegraded = false; the expired sign-in must still be reported")
	}
	if s.Stale {
		t.Error("Stale = true; readings are arriving live over LAN")
	}
	if !s.Online {
		t.Error("Online = false; the printer is reachable")
	}
	if s.Transport != "lan" {
		t.Errorf("Transport = %q, want lan", s.Transport)
	}
}

// TestMarkCloudAuthRejected_CloudLiveIsBlind is the PR #40 behaviour, preserved
// for the case it was written for: cloud is the only path, so a rejected token
// really does mean the dashboard has gone blind.
func TestMarkCloudAuthRejected_CloudLiveIsBlind(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
	c.setTransportLive(transportCloud)

	c.markCloudAuthRejected()

	s := c.Status()
	if !s.AuthInvalid {
		t.Error("AuthInvalid = false; on a cloud-only printer a dead token IS blindness")
	}
	if s.CloudDegraded {
		t.Error("CloudDegraded = true; must not soften a real outage")
	}
	if !s.Stale {
		t.Error("Stale = false; the card is showing a frozen picture")
	}
	if !strings.Contains(s.StaleReason, "sign-in") {
		t.Errorf("StaleReason = %q; want the actionable sign-in message", s.StaleReason)
	}
}

// TestNoteTransportError_IgnoresFailedCandidate stops a dead fallback path from
// blanking a live card: cloud refusing us says nothing about a session that is
// running fine over LAN.
func TestNoteTransportError_IgnoresFailedCandidate(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
	c.setTransportLive(transportLAN)

	c.noteTransportError(transportCloud, "the print server refused the connection")

	s := c.Status()
	if s.Stale || !s.Online {
		t.Errorf("Stale/Online = %v/%v; a failed non-active candidate must not change a live status", s.Stale, s.Online)
	}
}

func TestNoteTransportError_OnActiveTransportMarksStale(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
	c.setTransportLive(transportLAN)

	c.noteTransportError(transportLAN, "the printer refused the connection")

	if s := c.Status(); !s.Stale {
		t.Error("Stale = false; losing the transport actually in use must be reported")
	}
}

// TestHandleAuthRejection_DistinguishesCredentialPerTransport: a refusal on
// cloud is an expired account token (sign in again); a refusal on LAN is a
// wrong device access code (edit config.yaml). Same CONNACK code, unrelated
// fixes — and sending someone to the sign-in screen to fix an access code is a
// dead end.
func TestHandleAuthRejection_DistinguishesCredentialPerTransport(t *testing.T) {
	t.Run("lan refusal is an access code problem", func(t *testing.T) {
		c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
		c.setTransportLive(transportLAN)

		c.handleAuthRejection(transportLAN)

		s := c.Status()
		if s.AuthInvalid || s.CloudDegraded {
			t.Errorf("AuthInvalid/CloudDegraded = %v/%v; a bad access code is not a cloud credential problem",
				s.AuthInvalid, s.CloudDegraded)
		}
		if !strings.Contains(s.StaleReason, "access code") {
			t.Errorf("StaleReason = %q; want it to name the access code", s.StaleReason)
		}
	})

	t.Run("cloud refusal is a token problem", func(t *testing.T) {
		c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
		c.setTransportLive(transportCloud)

		c.handleAuthRejection(transportCloud)

		if s := c.Status(); !s.AuthInvalid {
			t.Error("AuthInvalid = false; a cloud rejection must register as a credential problem")
		}
	})
}

// TestHandleReport_LiveLANReportsDoNotClearCloudDegraded guards the bug found
// while verifying this live: a LAN report proves the transport works, not that
// the account token is valid, so it must not withdraw the expired-sign-in
// notice on every push.
func TestHandleReport_LiveLANReportsDoNotClearCloudDegraded(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
	c.setTransportLive(transportLAN)
	c.markCloudAuthRejected()

	if s := c.Status(); !s.CloudDegraded {
		t.Fatal("setup: expected the degraded notice first")
	}

	c.handleReport(nil, newMockMessage([]byte(`{"print": {"gcode_state": "RUNNING"}}`)))

	if s := c.Status(); !s.CloudDegraded {
		t.Error("CloudDegraded cleared by a LAN report; only the watchdog, which asks Bambu, may clear it")
	}
}

func TestClearCloudAuthProblemWithdrawsBothVerdicts(t *testing.T) {
	c := transportTestClient(t, testPrinterClientConfig, cloudWithCreds())
	c.setTransportLive(transportLAN)
	c.markCloudAuthRejected()

	c.clearCloudAuthProblem()

	s := c.Status()
	if s.CloudDegraded || s.AuthInvalid {
		t.Errorf("CloudDegraded/AuthInvalid = %v/%v; want both withdrawn once the credential works",
			s.CloudDegraded, s.AuthInvalid)
	}
}

// TestTransportSpecString_NoCredentialLeak: these strings go to the log, and
// the password is a live device access code / account token.
func TestTransportSpecString_NoCredentialLeak(t *testing.T) {
	spec := transportSpec{kind: transportLAN, broker: "ssl://10.0.0.1:8883", username: "bblp", password: "supersecret"}
	s := spec.String()
	if strings.Contains(s, "supersecret") {
		t.Errorf("String() = %q; must not contain the password", s)
	}
	if !strings.Contains(s, "lan") {
		t.Errorf("String() = %q; want it to name the transport", s)
	}
}

// TestDescribeTransport_KeepAliveStaysNonAuth is a regression guard for the
// original misdiagnosis: keep-alive jargon must never be read as a credential
// problem, or a wifi blip starts telling people to re-authenticate.
func TestDescribeTransport_KeepAliveStaysNonAuth(t *testing.T) {
	if isAuthRejection(errors.New("pingresp not received, disconnecting")) {
		t.Error("a keep-alive timeout was classified as an auth rejection")
	}
}
