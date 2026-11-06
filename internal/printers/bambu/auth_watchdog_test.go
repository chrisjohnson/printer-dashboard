package bambu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// ---------------------------------------------------------------------------
// Token expiry persistence
//
// The bug these pin: SaveToken wrote time.Time{}.Unix() for an unknown expiry.
// That is not 0, it is -62135596800 (Unix time of year 1), and LoadToken only
// accepted values > 0 — so "unknown" round-tripped as "unknown" and TokenValid
// fell into its assume-valid branch forever. Bambu issues opaque tokens with no
// expiry at all, so on real hardware this meant the dashboard believed a dead
// credential was healthy indefinitely and never looked again.
// ---------------------------------------------------------------------------

func TestTokenExpiryUnix(t *testing.T) {
	if got := tokenExpiryUnix(time.Time{}); got != 0 {
		t.Errorf("tokenExpiryUnix(zero) = %d, want 0 (Go's zero time is %d, which is a lie on disk)",
			got, time.Time{}.Unix())
	}
	exp := time.Now().Add(time.Hour)
	if got := tokenExpiryUnix(exp); got != exp.Unix() {
		t.Errorf("tokenExpiryUnix(%v) = %d, want %d", exp, got, exp.Unix())
	}
}

func TestSaveToken_UnknownExpiryWritesZeroNotYearOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")

	c := &BambuCloudClient{tokenFile: path, token: "opaque-token", userID: "7", region: "global"}
	// tokenExpiry deliberately left zero: this is the opaque-token case.
	if err := c.SaveToken(); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "-62135596800") {
		t.Errorf("token file contains the Go zero-time sentinel; it must record unknown expiry as absent/0:\n%s", raw)
	}

	var td tokenData
	if err := json.Unmarshal(raw, &td); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if td.ExpiresAt != 0 {
		t.Errorf("ExpiresAt = %d, want 0", td.ExpiresAt)
	}
}

func TestLoadToken_LegacyZeroTimeSentinelIsUnknownNotExpired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")
	// Exactly what older builds left on disk.
	data := `{"token":"t","user_id":"7","region":"global","expires_at":-62135596800}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := &BambuCloudClient{tokenFile: path}
	if _, err := c.LoadToken(); err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if !c.tokenExpiry.IsZero() {
		t.Errorf("tokenExpiry = %v, want zero (unknown) — a year-1 sentinel must not be read as a real date", c.tokenExpiry)
	}
	// And unknown must not be reported as expired, which is what would make the
	// dashboard nag for a re-login on every opaque token.
	if !c.TokenValid() {
		t.Error("TokenValid() = false for an unknown expiry; want true (cannot prove otherwise without a round trip)")
	}
}

// ---------------------------------------------------------------------------
// VerifyToken — the authoritative "does this credential still work" check
// ---------------------------------------------------------------------------

func TestVerifyToken(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(preferenceResponse{UserID: json.Number("123")})
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.token = "good"
		if err := c.VerifyToken(); err != nil {
			t.Errorf("VerifyToken() = %v, want nil", err)
		}
	})

	t.Run("rejected is classified as auth", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"code":4,"error":"Please login.","message":""}`))
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.token = "dead"
		err := c.VerifyToken()
		if err == nil {
			t.Fatal("VerifyToken() = nil, want an error for a 401")
		}
		if !errors.Is(err, ErrAuthRejected) {
			t.Errorf("VerifyToken() error = %v; want it to wrap ErrAuthRejected", err)
		}
	})

	t.Run("server trouble is NOT auth", func(t *testing.T) {
		// The distinction matters: if a 500 or a timeout were classed as a
		// rejected credential, a hiccup at Bambu would tell the user to go and
		// re-enter a password they just got right.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"boom"}`))
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.token = "fine"
		err := c.VerifyToken()
		if err == nil {
			t.Fatal("expected an error for a 500")
		}
		if errors.Is(err, ErrAuthRejected) {
			t.Errorf("VerifyToken() = %v; a 500 must not be reported as a credential problem", err)
		}
	})

	t.Run("empty token is auth", func(t *testing.T) {
		c := newTestClient("http://127.0.0.1:1")
		if err := c.VerifyToken(); !errors.Is(err, ErrAuthRejected) {
			t.Errorf("VerifyToken() with no token = %v; want ErrAuthRejected", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Error classification and humanisation
// ---------------------------------------------------------------------------

func TestIsAuthRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"wrapped sentinel", errors.New("x: " + ErrAuthRejected.Error()), false}, // text-only wrap is not enough
		{"errors.Is sentinel", fmtWrap(ErrAuthRejected), true},
		{"paho not authorized", errors.New("not Authorized"), true},
		{"paho british spelling", errors.New("Connection Refused: Not Authorised"), true},
		{"bad username or password", errors.New("Connection Refused: Username or Password in unknown format"), true},
		{"keepalive", errors.New("pingresp not received, disconnecting"), false},
		{"reset", errors.New("read tcp 10.0.0.1:8883: connection reset by peer"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthRejection(tc.err); got != tc.want {
				t.Errorf("isAuthRejection(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// fmtWrap wraps with %w so errors.Is can see the sentinel, the way the real
// call sites do.
func fmtWrap(inner error) error { return errWrapped{inner} }

type errWrapped struct{ inner error }

func (e errWrapped) Error() string { return "cloud API said: " + e.inner.Error() }
func (e errWrapped) Unwrap() error { return e.inner }

func TestDescribeTransport(t *testing.T) {
	t.Run("keepalive jargon is replaced", func(t *testing.T) {
		got := describeTransport(errors.New("pingresp not received, disconnecting"))
		if strings.Contains(got, "pingresp") {
			t.Errorf("describeTransport leaked raw paho text: %q", got)
		}
		if got == "" {
			t.Error("describeTransport returned empty")
		}
	})

	t.Run("auth rejection becomes the actionable sentence", func(t *testing.T) {
		got := describeTransport(errors.New("not Authorized"))
		if got != authRejectedReason {
			t.Errorf("describeTransport = %q, want %q", got, authRejectedReason)
		}
	})

	t.Run("unknown errors pass through", func(t *testing.T) {
		// A weird error is still more useful than a vague one; swallowing the
		// text is how you end up with "something went wrong".
		got := describeTransport(errors.New("weird brand new failure"))
		if got != "weird brand new failure" {
			t.Errorf("describeTransport = %q, want the original text preserved", got)
		}
	})
}

// ---------------------------------------------------------------------------
// The client-side verdict
// ---------------------------------------------------------------------------

func newAuthTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	cloud := newTestClient(srv.URL)
	// A client with no token at all is its own case (see VerifyToken); these
	// tests are about a token the cloud does or does not accept.
	cloud.token = "test-token"
	c := New(testPrinterClientConfig, cloud)
	c.status = printers.PrinterStatus{ID: "test-id", Name: "Test Printer", Type: "bambu", Online: true, State: "printing"}
	markReported(c)
	return c, srv
}

func TestCheckCloudAuth_RejectedMarksActionableStale(t *testing.T) {
	c, srv := newAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":4,"error":"Please login."}`))
	})
	defer srv.Close()

	c.checkCloudAuth()

	s := c.Status()
	if !s.AuthInvalid {
		t.Error("AuthInvalid = false; want true once the cloud refuses the credential")
	}
	if !s.Stale {
		t.Error("Stale = false; want true — the card is showing a frozen picture")
	}
	if s.Online {
		t.Error("Online = true; want false")
	}
	// The whole point: the reason names the cause and the fix, not the socket.
	if !strings.Contains(s.StaleReason, "sign-in") {
		t.Errorf("StaleReason = %q; want it to name the expired sign-in", s.StaleReason)
	}
	if strings.Contains(s.StaleReason, "pingresp") || strings.Contains(s.StaleReason, "MQTT") {
		t.Errorf("StaleReason = %q; must not leak transport jargon for an auth failure", s.StaleReason)
	}
	// Last-known telemetry survives: the printer is fine, we are just locked out.
	if s.State != "printing" {
		t.Errorf("State = %q, want %q preserved", s.State, "printing")
	}
}

func TestCheckCloudAuth_InconclusiveLeavesVerdictAlone(t *testing.T) {
	// A 500 says nothing about the credential, so it must neither raise nor
	// clear the alarm. Otherwise a Bambu outage would silently mark a real
	// sign-in problem as fixed.
	c, srv := newAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	c.markAuthRejected()
	c.checkCloudAuth()

	s := c.Status()
	if !s.AuthInvalid {
		t.Error("AuthInvalid = false; an inconclusive check must not clear a standing rejection")
	}
}

func TestCheckCloudAuth_AcceptedClearsRejection(t *testing.T) {
	c, srv := newAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(preferenceResponse{UserID: json.Number("123")})
	})
	defer srv.Close()

	c.markAuthRejected()
	if !c.Status().AuthInvalid {
		t.Fatal("setup: expected the rejection to be recorded first")
	}

	c.checkCloudAuth()

	if s := c.Status(); s.AuthInvalid {
		t.Error("AuthInvalid = true; want the banner withdrawn once the credential works again")
	}
}

func TestMarkAuthRejected_IsIdempotent(t *testing.T) {
	// The watchdog ticks for the life of the process; a standing rejection must
	// not re-broadcast a status update every few minutes.
	c, srv := newAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer srv.Close()

	updates := make(chan printers.PrinterStatus, 8)
	c.StatusCh = updates

	c.markAuthRejected()
	c.markAuthRejected()

	select {
	case <-updates:
	default:
		t.Fatal("expected one status update from the first rejection")
	}
	select {
	case <-updates:
		t.Error("second markAuthRejected re-broadcast a status; a standing condition should be recorded once")
	default:
	}
}

func TestOnConnectionLost_AuthRefusalMarksAuthInvalid(t *testing.T) {
	// Bambu's broker can also express a dead credential by dropping the session
	// instead of refusing the CONNECT, so the connection-lost path has to
	// classify too — otherwise the auth cause is overwritten by keep-alive text.
	c := newTestPrinterClient(nil)
	c.setStatus(printers.PrinterStatus{ID: "test-id", Name: "Test Printer", Type: "bambu", Online: true, State: "printing"})
	markReported(c)

	c.onConnectionLost(nil, errors.New("not Authorized"))

	s := c.Status()
	if !s.AuthInvalid {
		t.Errorf("AuthInvalid = false; StaleReason = %q", s.StaleReason)
	}
	if strings.Contains(s.StaleReason, "MQTT disconnected") {
		t.Errorf("StaleReason = %q; an auth refusal must not be dressed up as a dropped link", s.StaleReason)
	}
}

func TestHandleReport_ClearsAuthInvalid(t *testing.T) {
	// If reports are arriving, the credential is by definition working, whatever
	// the last probe said.
	c := newTestPrinterClient(nil)
	c.authInvalid = true
	c.setStatus(printers.PrinterStatus{
		ID: "test-id", Name: "Test Printer", Type: "bambu",
		Online: false, State: "printing", Stale: true, AuthInvalid: true,
		StaleReason: authRejectedReason,
	})

	c.handleReport(nil, newMockMessage([]byte(`{"print": {"gcode_state": "RUNNING"}}`)))

	s := c.Status()
	if s.AuthInvalid {
		t.Error("AuthInvalid = true; live reports should clear it")
	}
	if s.Stale {
		t.Error("Stale = true; live reports should clear it")
	}
}

func TestMarkAuthUnavailable_NoAccountConfigured(t *testing.T) {
	// The no-credentials-at-all case: the printer must still appear, with a
	// reason, rather than vanishing from the dashboard.
	t.Run("never reported - reason lands in ErrorMsg", func(t *testing.T) {
		c := New(testPrinterClientConfig, nil)

		c.MarkAuthUnavailable(noAccountReason)

		s := c.Status()
		if !s.AuthInvalid {
			t.Error("AuthInvalid = false; want true")
		}
		// Nothing was ever received, so there is no last-known-good picture to
		// freeze — this is a plain error, and the reason rides along with it.
		if s.State != "error" {
			t.Errorf("State = %q, want %q", s.State, "error")
		}
		if !strings.Contains(s.ErrorMsg, "account") {
			t.Errorf("ErrorMsg = %q; want it to name the missing account", s.ErrorMsg)
		}
	})

	t.Run("previously reported - reason lands in StaleReason", func(t *testing.T) {
		c := New(testPrinterClientConfig, nil)
		c.setStatus(printers.PrinterStatus{
			ID: "test-id", Name: "Test Printer", Type: "bambu",
			Online: true, State: "printing", Progress: 0.4,
		})
		markReported(c)

		c.MarkAuthUnavailable(noAccountReason)

		s := c.Status()
		if !s.AuthInvalid {
			t.Error("AuthInvalid = false; want true")
		}
		if !strings.Contains(s.StaleReason, "account") {
			t.Errorf("StaleReason = %q; want it to name the missing account", s.StaleReason)
		}
		if s.State != "printing" || s.Progress != 0.4 {
			t.Errorf("State/Progress = %q/%v; want the last known readings preserved", s.State, s.Progress)
		}
	})
}

func TestConnect_NilCloudDoesNotPanic(t *testing.T) {
	// Guards the reason initPrinters can now register an unauthenticated
	// printer: Connect used to dereference c.cloud unconditionally.
	c := New(testPrinterClientConfig, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Connect(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Connect() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Connect() did not return for a client with no cloud credentials")
	}

	if s := c.Status(); !s.AuthInvalid {
		t.Error("AuthInvalid = false; want the no-account state recorded")
	}
}

func TestWatchAuth_ExitsOnContextCancel(t *testing.T) {
	c, srv := newAuthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(preferenceResponse{UserID: json.Number("123")})
	})
	defer srv.Close()
	c.authCheckEvery = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() { c.watchAuth(ctx); close(returned) }()

	time.Sleep(20 * time.Millisecond) // let it tick at least once
	cancel()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("watchAuth did not stop when its context was cancelled")
	}
}
