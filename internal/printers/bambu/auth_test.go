package bambu

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helper: JWT token builder
// ---------------------------------------------------------------------------

// makeTestJWT creates a JWT-like token for testing. The payload is base64-encoded
// using the provided encoding (defaults to base64.RawURLEncoding).
// The header and signature are dummy values — parseJWT only reads the payload.
func makeTestJWT(t *testing.T, payload map[string]any, enc *base64.Encoding) string {
	t.Helper()
	if enc == nil {
		enc = base64.RawURLEncoding
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal JWT payload: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadB64 := enc.EncodeToString(payloadBytes)
	return header + "." + payloadB64 + ".fakesignature"
}

// makeJWTInfo creates the same payload map used by makeTestJWT.
func makeJWTInfo(exp, iat int64, email, userID string) map[string]any {
	return map[string]any{
		"exp":     exp,
		"iat":     iat,
		"email":   email,
		"user_id": userID,
	}
}

// ---------------------------------------------------------------------------
// Helper: test client factory
// ---------------------------------------------------------------------------

// newTestClient creates a BambuCloudClient pointed at the given test server URL.
func newTestClient(serverURL string) *BambuCloudClient {
	return &BambuCloudClient{
		httpClient: &http.Client{},
		baseURL:    serverURL,
	}
}

// ---------------------------------------------------------------------------
// Helper: verify a saved token file
// ---------------------------------------------------------------------------

func assertTokenFile(t *testing.T, path, wantToken, wantUserID, wantRegion string, wantExpiryNonZero bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read token file: %v", err)
	}
	var td tokenData
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("failed to parse token file: %v\ncontent: %s", err, string(data))
	}
	if td.Token != wantToken {
		t.Errorf("token file token = %q; want %q", td.Token, wantToken)
	}
	if td.UserID != wantUserID {
		t.Errorf("token file user_id = %q; want %q", td.UserID, wantUserID)
	}
	if td.Region != wantRegion {
		t.Errorf("token file region = %q; want %q", td.Region, wantRegion)
	}
	if wantExpiryNonZero && td.ExpiresAt == 0 {
		t.Error("token file expires_at = 0; expected non-zero")
	}
}

// ---------------------------------------------------------------------------
// Test: ParseJWT / parseJWT
// ---------------------------------------------------------------------------

func TestParseJWT(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Unix()
	past := time.Now().Add(-24 * time.Hour).Unix()

	t.Run("valid JWT", func(t *testing.T) {
		token := makeTestJWT(t, makeJWTInfo(future, past, "test@example.com", "12345"), nil)
		jp, err := parseJWT(token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if jp.UserID != "12345" {
			t.Errorf("UserID = %q; want %q", jp.UserID, "12345")
		}
		if jp.Email != "test@example.com" {
			t.Errorf("Email = %q; want %q", jp.Email, "test@example.com")
		}
		if jp.Exp != future {
			t.Errorf("Exp = %d; want %d", jp.Exp, future)
		}
		if jp.Iat != past {
			t.Errorf("Iat = %d; want %d", jp.Iat, past)
		}
	})

	t.Run("ParseJWT exported wrapper", func(t *testing.T) {
		token := makeTestJWT(t, makeJWTInfo(future, past, "alias@example.com", "67890"), nil)
		info, err := ParseJWT(token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UserID != "67890" {
			t.Errorf("UserID = %q; want %q", info.UserID, "67890")
		}
		if info.Email != "alias@example.com" {
			t.Errorf("Email = %q; want %q", info.Email, "alias@example.com")
		}
		if info.Exp != future {
			t.Errorf("Exp = %d; want %d", info.Exp, future)
		}
		if info.Iat != past {
			t.Errorf("Iat = %d; want %d", info.Iat, past)
		}
	})

	t.Run("JWT with padding in payload", func(t *testing.T) {
		// StdEncoding adds '=' padding and may use '+' and '/' characters.
		payload := map[string]any{"exp": future, "iat": past, "email": "pad@test.com", "user_id": "pad1"}
		token := makeTestJWT(t, payload, base64.StdEncoding)
		jp, err := parseJWT(token)
		if err != nil {
			t.Fatalf("unexpected error with padded payload: %v", err)
		}
		if jp.UserID != "pad1" {
			t.Errorf("UserID = %q; want %q", jp.UserID, "pad1")
		}
	})

	t.Run("JWT with RawURLEncoding (no padding)", func(t *testing.T) {
		payload := map[string]any{"exp": future, "iat": past, "email": "rawurl@test.com", "user_id": "rurl1"}
		token := makeTestJWT(t, payload, base64.RawURLEncoding)
		jp, err := parseJWT(token)
		if err != nil {
			t.Fatalf("unexpected error with RawURLEncoding: %v", err)
		}
		if jp.UserID != "rurl1" {
			t.Errorf("UserID = %q; want %q", jp.UserID, "rurl1")
		}
	})

	t.Run("fewer than 3 parts", func(t *testing.T) {
		token := "header.payload"
		_, err := parseJWT(token)
		if err == nil {
			t.Fatal("expected error for 2-part token")
		}
		if !strings.Contains(err.Error(), "expected 3 parts") {
			t.Errorf("error = %q; want 'expected 3 parts'", err.Error())
		}
	})

	t.Run("invalid base64 payload", func(t *testing.T) {
		token := "header.!!!invalid-base64!!.sig"
		_, err := parseJWT(token)
		if err == nil {
			t.Fatal("expected error for invalid base64")
		}
		if !strings.Contains(err.Error(), "decode JWT payload") {
			t.Errorf("error = %q; want 'decode JWT payload'", err.Error())
		}
	})

	t.Run("non-JSON payload", func(t *testing.T) {
		// Valid base64 that decodes to non-JSON text
		payloadB64 := base64.RawURLEncoding.EncodeToString([]byte("this is not json"))
		token := "header." + payloadB64 + ".sig"
		_, err := parseJWT(token)
		if err == nil {
			t.Fatal("expected error for non-JSON payload")
		}
		if !strings.Contains(err.Error(), "parse JWT payload") {
			t.Errorf("error = %q; want 'parse JWT payload'", err.Error())
		}
	})

	t.Run("missing exp field", func(t *testing.T) {
		payload := map[string]any{"email": "noexp@test.com", "user_id": "noexp1"}
		token := makeTestJWT(t, payload, nil)
		jp, err := parseJWT(token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if jp.Exp != 0 {
			t.Errorf("Exp = %d; want 0", jp.Exp)
		}
		if jp.UserID != "noexp1" {
			t.Errorf("UserID = %q; want %q", jp.UserID, "noexp1")
		}
	})
}

// ---------------------------------------------------------------------------
// Test: TokenValid
// ---------------------------------------------------------------------------

func TestTokenValid(t *testing.T) {
	t.Run("empty token", func(t *testing.T) {
		c := &BambuCloudClient{}
		if c.TokenValid() {
			t.Error("TokenValid() = true; want false for empty token")
		}
	})

	t.Run("future expiry", func(t *testing.T) {
		c := &BambuCloudClient{
			token:       "some-token",
			tokenExpiry: time.Now().Add(2 * time.Hour), // >1h from now
		}
		if !c.TokenValid() {
			t.Error("TokenValid() = false; want true for future expiry")
		}
	})

	t.Run("past expiry", func(t *testing.T) {
		c := &BambuCloudClient{
			token:       "some-token",
			tokenExpiry: time.Now().Add(-1 * time.Hour), // already expired
		}
		if c.TokenValid() {
			t.Error("TokenValid() = true; want false for past expiry")
		}
	})

	t.Run("within 1 hour of expiry", func(t *testing.T) {
		c := &BambuCloudClient{
			token:       "some-token",
			tokenExpiry: time.Now().Add(30 * time.Minute), // <1h left
		}
		if c.TokenValid() {
			t.Error("TokenValid() = true; want false when <1h until expiry")
		}
	})

	t.Run("zero expiry (unknown)", func(t *testing.T) {
		c := &BambuCloudClient{
			token:       "non-empty-token-without-expiry",
			tokenExpiry: time.Time{},
		}
		if !c.TokenValid() {
			t.Error("TokenValid() = false; want true for unknown expiry (defensive)")
		}
	})
}

// ---------------------------------------------------------------------------
// Test: TokenExpiry
// ---------------------------------------------------------------------------

func TestTokenExpiry(t *testing.T) {
	t.Run("returns saved value when set", func(t *testing.T) {
		expected := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		c := &BambuCloudClient{
			token:       "some-token",
			tokenExpiry: expected,
		}
		got := c.TokenExpiry()
		if !got.Equal(expected) {
			t.Errorf("TokenExpiry() = %v; want %v", got, expected)
		}
	})

	t.Run("parses from JWT when saved is zero", func(t *testing.T) {
		future := time.Now().Add(48 * time.Hour).Unix()
		payload := makeJWTInfo(future, time.Now().Unix(), "jwt@test.com", "jwt1")
		token := makeTestJWT(t, payload, nil)
		c := &BambuCloudClient{
			token:       token,
			tokenExpiry: time.Time{}, // zero
		}
		got := c.TokenExpiry()
		expected := time.Unix(future, 0)
		if !got.Equal(expected) {
			t.Errorf("TokenExpiry() = %v; want %v", got, expected)
		}
	})

	t.Run("returns zero for unparseable token", func(t *testing.T) {
		c := &BambuCloudClient{
			token:       "not-a-jwt",
			tokenExpiry: time.Time{},
		}
		got := c.TokenExpiry()
		if !got.IsZero() {
			t.Errorf("TokenExpiry() = %v; want zero time", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Test: TokenLifetimeLeft
// ---------------------------------------------------------------------------

func TestTokenLifetimeLeft(t *testing.T) {
	t.Run("returns duration for future expiry", func(t *testing.T) {
		exp := time.Now().Add(2 * time.Hour)
		c := &BambuCloudClient{tokenExpiry: exp}
		left := c.TokenLifetimeLeft()
		if left <= 0 || left > 3*time.Hour {
			t.Errorf("TokenLifetimeLeft() = %v; want ~2h", left)
		}
	})

	t.Run("returns zero for unknown expiry", func(t *testing.T) {
		c := &BambuCloudClient{tokenExpiry: time.Time{}}
		if got := c.TokenLifetimeLeft(); got != 0 {
			t.Errorf("TokenLifetimeLeft() = %v; want 0", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Test: Token persistence (SaveToken, LoadToken, DeleteToken)
// ---------------------------------------------------------------------------

func TestTokenPersistence(t *testing.T) {
	t.Run("SaveToken with nil tokenFile", func(t *testing.T) {
		c := &BambuCloudClient{token: "t", userID: "1"}
		err := c.SaveToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("SaveToken creates directory and file", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := filepath.Join(dir, "subdir", "token.json")
		c := &BambuCloudClient{
			tokenFile: tokenFile,
			token:     "test-create-token",
			userID:    "42",
			region:    "global",
		}
		err := c.SaveToken()
		if err != nil {
			t.Fatalf("SaveToken failed: %v", err)
		}
		if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
			t.Fatal("token file was not created")
		}
		assertTokenFile(t, tokenFile, "test-create-token", "42", "global", false)
	})

	t.Run("LoadToken with non-existent file", func(t *testing.T) {
		c := &BambuCloudClient{
			tokenFile: filepath.Join(t.TempDir(), "nonexistent.json"),
		}
		loaded, err := c.LoadToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded {
			t.Error("LoadToken() = true; want false for non-existent file")
		}
	})

	t.Run("LoadToken with valid file", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := filepath.Join(dir, "token.json")
		expiry := time.Now().Add(24 * time.Hour)
		td := tokenData{
			Token:     "test-load-token",
			UserID:    "99",
			Region:    "china",
			ExpiresAt: expiry.Unix(),
			Email:     "load@test.com",
		}
		data, _ := json.Marshal(td)
		if err := os.WriteFile(tokenFile, data, 0600); err != nil {
			t.Fatalf("failed to write test token file: %v", err)
		}
		c := &BambuCloudClient{tokenFile: tokenFile}
		loaded, err := c.LoadToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !loaded {
			t.Fatal("LoadToken() = false; want true")
		}
		if c.token != "test-load-token" {
			t.Errorf("c.token = %q; want %q", c.token, "test-load-token")
		}
		if c.userID != "99" {
			t.Errorf("c.userID = %q; want %q", c.userID, "99")
		}
		if c.region != "china" {
			t.Errorf("c.region = %q; want %q", c.region, "china")
		}
		if c.tokenExpiry.Unix() != expiry.Unix() {
			t.Errorf("c.tokenExpiry = %v; want %v", c.tokenExpiry, expiry)
		}
	})

	t.Run("LoadToken with corrupted JSON", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := filepath.Join(dir, "corrupt.json")
		if err := os.WriteFile(tokenFile, []byte("{bad json"), 0600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
		c := &BambuCloudClient{tokenFile: tokenFile}
		_, err := c.LoadToken()
		if err == nil {
			t.Fatal("expected error for corrupted JSON")
		}
		if !strings.Contains(err.Error(), "parse token file") {
			t.Errorf("error = %q; want 'parse token file'", err.Error())
		}
	})

	t.Run("LoadToken with empty fields", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := filepath.Join(dir, "empty.json")
		td := tokenData{Token: "", UserID: ""}
		data, _ := json.Marshal(td)
		if err := os.WriteFile(tokenFile, data, 0600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
		c := &BambuCloudClient{tokenFile: tokenFile}
		_, err := c.LoadToken()
		if err == nil {
			t.Fatal("expected error for empty fields")
		}
		if !strings.Contains(err.Error(), "empty fields") {
			t.Errorf("error = %q; want 'empty fields'", err.Error())
		}
	})

	t.Run("DeleteToken removes file", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := filepath.Join(dir, "delete_me.json")
		c := &BambuCloudClient{
			tokenFile: tokenFile,
			token:     "del-token",
			userID:    "del1",
		}
		if err := c.SaveToken(); err != nil {
			t.Fatalf("SaveToken failed: %v", err)
		}
		if err := c.DeleteToken(); err != nil {
			t.Fatalf("DeleteToken failed: %v", err)
		}
		if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
			t.Error("token file still exists after DeleteToken")
		}
	})

	t.Run("DeleteToken with non-existent file", func(t *testing.T) {
		c := &BambuCloudClient{
			tokenFile: filepath.Join(t.TempDir(), "never_existed.json"),
		}
		if err := c.DeleteToken(); err != nil {
			t.Fatalf("DeleteToken returned error for non-existent file: %v", err)
		}
	})

	t.Run("DeleteToken with nil tokenFile", func(t *testing.T) {
		c := &BambuCloudClient{}
		if err := c.DeleteToken(); err != nil {
			t.Fatalf("DeleteToken returned error with nil tokenFile: %v", err)
		}
	})

	t.Run("LoadToken with nil tokenFile", func(t *testing.T) {
		c := &BambuCloudClient{}
		loaded, err := c.LoadToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded {
			t.Error("LoadToken() = true; want false with nil tokenFile")
		}
	})
}

// ---------------------------------------------------------------------------
// Test: LoginStep1
// ---------------------------------------------------------------------------

func TestLoginStep1(t *testing.T) {
	t.Run("direct success (no 2FA)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/v1/user-service/user/login" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(loginResponse{
				Success:     true,
				AccessToken: "direct-token-abc",
				ExpiresIn:   86400,
			})
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		lr, err := c.LoginStep1("user@test.com", "password123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !lr.Success {
			t.Error("LoginStep1.Success = false; want true")
		}
		if lr.AccessToken != "direct-token-abc" {
			t.Errorf("AccessToken = %q; want %q", lr.AccessToken, "direct-token-abc")
		}
		if lr.ExpiresIn != 86400 {
			t.Errorf("ExpiresIn = %d; want %d", lr.ExpiresIn, 86400)
		}
	})

	t.Run("requires 2FA", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(loginResponse{
				Success:   false,
				LoginType: "verifyCode",
				TFAKey:    "tfa-key-xyz",
			})
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		lr, err := c.LoginStep1("user@test.com", "password123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lr.LoginType != "verifyCode" {
			t.Errorf("LoginType = %q; want %q", lr.LoginType, "verifyCode")
		}
		if lr.TFAKey != "tfa-key-xyz" {
			t.Errorf("TFAKey = %q; want %q", lr.TFAKey, "tfa-key-xyz")
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid credentials"}`))
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		_, err := c.LoginStep1("user@test.com", "wrong-pass")
		if err == nil {
			t.Fatal("expected error for 400 response")
		}
		if !strings.Contains(err.Error(), "API error 400") {
			t.Errorf("error = %q; want 'API error 400'", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Test: SendVerificationCode
// ---------------------------------------------------------------------------

func TestSendVerificationCode(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/v1/user-service/user/sendemail/code" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			// Verify the request body
			var body verifyCodeRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode request: %v", err)
			}
			if body.Email != "2fa@test.com" {
				t.Errorf("email = %q; want %q", body.Email, "2fa@test.com")
			}
			if body.Type != "codeLogin" {
				t.Errorf("type = %q; want %q", body.Type, "codeLogin")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":true}`))
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		err := c.SendVerificationCode("2fa@test.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"server error"}`))
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		err := c.SendVerificationCode("fail@test.com")
		if err == nil {
			t.Fatal("expected error for 500 response")
		}
		if !strings.Contains(err.Error(), "sending verification code") {
			t.Errorf("error = %q; want 'sending verification code'", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Test: LoginStep2
// ---------------------------------------------------------------------------

func TestLoginStep2(t *testing.T) {
	t.Run("valid code", func(t *testing.T) {
		var requestCount int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount++
			switch r.URL.Path {
			case "/v1/user-service/user/login":
				// Verify request body
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("failed to decode login request: %v", err)
				}
				if body["account"] != "step2@test.com" {
					t.Errorf("account = %q; want %q", body["account"], "step2@test.com")
				}
				if body["code"] != "123456" {
					t.Errorf("code = %q; want %q", body["code"], "123456")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(loginResponse{
					Success:     true,
					AccessToken: "step2-token-valid",
					ExpiresIn:   43200,
				})

			case "/v1/design-user-service/my/preference":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(preferenceResponse{
					UserID: json.Number("777"),
				})

			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.tokenFile = filepath.Join(t.TempDir(), "step2_token.json")

		err := c.LoginStep2("step2@test.com", "123456")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.token != "step2-token-valid" {
			t.Errorf("c.token = %q; want %q", c.token, "step2-token-valid")
		}
		if c.userID != "777" {
			t.Errorf("c.userID = %q; want %q", c.userID, "777")
		}
		if c.tokenExpiry.IsZero() {
			t.Error("c.tokenExpiry is zero; expected non-zero")
		}
		// Verify token was persisted
		assertTokenFile(t, c.tokenFile, "step2-token-valid", "777", "", true)
		if requestCount != 2 {
			t.Errorf("expected 2 requests (login + preference), got %d", requestCount)
		}
	})

	t.Run("invalid code", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(loginResponse{
				Success:     false,
				AccessToken: "",
			})
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		err := c.LoginStep2("step2@test.com", "wrong-code")
		if err == nil {
			t.Fatal("expected error for invalid code")
		}
		if !strings.Contains(err.Error(), "code rejected") {
			t.Errorf("error = %q; want 'code rejected'", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Test: LoginWithToken
// ---------------------------------------------------------------------------

func TestLoginWithToken(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/design-user-service/my/preference" {
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			auth := r.Header.Get("Authorization")
			if auth != "Bearer token-via-loginwithtoken" {
				t.Errorf("Authorization = %q; want 'Bearer token-via-loginwithtoken'", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(preferenceResponse{
				UserID: json.Number("555"),
			})
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.tokenFile = filepath.Join(t.TempDir(), "lwt_token.json")

		err := c.LoginWithToken("token-via-loginwithtoken")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.token != "token-via-loginwithtoken" {
			t.Errorf("c.token = %q; want %q", c.token, "token-via-loginwithtoken")
		}
		if c.userID != "555" {
			t.Errorf("c.userID = %q; want %q", c.userID, "555")
		}
		// Token should be persisted
		assertTokenFile(t, c.tokenFile, "token-via-loginwithtoken", "555", "", false)
	})

	t.Run("token rejected by server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		err := c.LoginWithToken("bad-token")
		if err == nil {
			t.Fatal("expected error for rejected token")
		}
		if !strings.Contains(err.Error(), "token rejected by server") {
			t.Errorf("error = %q; want 'token rejected by server'", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Test: GetDevices
// ---------------------------------------------------------------------------

func TestGetDevices(t *testing.T) {
	t.Run("returns list of devices", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/iot-service/api/user/bind" {
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(deviceBindResponse{
				Message: "success",
				Devices: []struct {
					DevID          string `json:"dev_id"`
					Name           string `json:"name"`
					Online         bool   `json:"online"`
					PrintStatus    string `json:"print_status"`
					DevModelName   string `json:"dev_model_name"`
					DevProductName string `json:"dev_product_name"`
					DevAccessCode  string `json:"dev_access_code"`
				}{
					{DevID: "P1P001", Name: "Office P1P", Online: true, PrintStatus: "RUNNING", DevModelName: "P1P", DevProductName: "Bambu Lab P1P", DevAccessCode: "1234"},
					{DevID: "X1C002", Name: "Lab X1C", Online: false, PrintStatus: "IDLE", DevModelName: "X1C", DevProductName: "Bambu Lab X1 Carbon", DevAccessCode: "5678"},
				},
			})
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.token = "device-test-token"

		devices, err := c.GetDevices()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(devices) != 2 {
			t.Fatalf("len(devices) = %d; want 2", len(devices))
		}
		if devices[0].DevID != "P1P001" {
			t.Errorf("devices[0].DevID = %q; want %q", devices[0].DevID, "P1P001")
		}
		if devices[0].Name != "Office P1P" {
			t.Errorf("devices[0].Name = %q; want %q", devices[0].Name, "Office P1P")
		}
		if !devices[0].Online {
			t.Error("devices[0].Online = false; want true")
		}
		if devices[0].PrintStatus != "RUNNING" {
			t.Errorf("devices[0].PrintStatus = %q; want %q", devices[0].PrintStatus, "RUNNING")
		}
		if devices[0].DevModelName != "P1P" {
			t.Errorf("devices[0].DevModelName = %q; want %q", devices[0].DevModelName, "P1P")
		}
		if devices[0].DevProductName != "Bambu Lab P1P" {
			t.Errorf("devices[0].DevProductName = %q; want %q", devices[0].DevProductName, "Bambu Lab P1P")
		}
		if devices[0].DevAccessCode != "1234" {
			t.Errorf("devices[0].DevAccessCode = %q; want %q", devices[0].DevAccessCode, "1234")
		}
		if devices[1].DevID != "X1C002" {
			t.Errorf("devices[1].DevID = %q; want %q", devices[1].DevID, "X1C002")
		}
	})

	t.Run("empty device list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(deviceBindResponse{
				Message: "success",
				Devices: []struct {
					DevID          string `json:"dev_id"`
					Name           string `json:"name"`
					Online         bool   `json:"online"`
					PrintStatus    string `json:"print_status"`
					DevModelName   string `json:"dev_model_name"`
					DevProductName string `json:"dev_product_name"`
					DevAccessCode  string `json:"dev_access_code"`
				}{},
			})
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.token = "device-empty-token"

		devices, err := c.GetDevices()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(devices) != 0 {
			t.Errorf("len(devices) = %d; want 0", len(devices))
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal error"}`))
		}))
		defer srv.Close()

		c := newTestClient(srv.URL)
		c.token = "device-error-token"

		_, err := c.GetDevices()
		if err == nil {
			t.Fatal("expected error for 500 response")
		}
		if !strings.Contains(err.Error(), "get devices") {
			t.Errorf("error = %q; want 'get devices'", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Test: NewBambuCloudClient
// ---------------------------------------------------------------------------

func TestNewBambuCloudClient(t *testing.T) {
	t.Run("global region", func(t *testing.T) {
		c := NewBambuCloudClient("global")
		if c == nil {
			t.Fatal("NewBambuCloudClient returned nil")
		}
		if c.baseURL != apiBaseGlobal {
			t.Errorf("baseURL = %q; want %q", c.baseURL, apiBaseGlobal)
		}
		if c.region != "global" {
			t.Errorf("region = %q; want %q", c.region, "global")
		}
	})

	t.Run("china region", func(t *testing.T) {
		c := NewBambuCloudClient("china")
		if c == nil {
			t.Fatal("NewBambuCloudClient returned nil")
		}
		if c.baseURL != apiBaseChina {
			t.Errorf("baseURL = %q; want %q", c.baseURL, apiBaseChina)
		}
		if c.region != "china" {
			t.Errorf("region = %q; want %q", c.region, "china")
		}
	})

	t.Run("unknown region defaults to global", func(t *testing.T) {
		c := NewBambuCloudClient("europe")
		if c.baseURL != apiBaseGlobal {
			t.Errorf("baseURL = %q; want %q", c.baseURL, apiBaseGlobal)
		}
	})
}

// ---------------------------------------------------------------------------
// Test: DefaultTokenPath
// ---------------------------------------------------------------------------

func TestDefaultTokenPath(t *testing.T) {
	email := "user@example.com"
	path := DefaultTokenPath(email)
	expected := filepath.Join(DefaultTokenDir, "bambu_token_user_at_example_dot_com.json")
	if path != expected {
		t.Errorf("DefaultTokenPath(%q) = %q; want %q", email, path, expected)
	}
}

// ---------------------------------------------------------------------------
// Test: MQTTBroker and MQTT credentials
// ---------------------------------------------------------------------------

func TestMQTTBroker(t *testing.T) {
	t.Run("global region", func(t *testing.T) {
		broker := MQTTBroker("global")
		if broker != "us.mqtt.bambulab.com:8883" {
			t.Errorf("MQTTBroker(global) = %q; want %q", broker, "us.mqtt.bambulab.com:8883")
		}
	})

	t.Run("china region", func(t *testing.T) {
		broker := MQTTBroker("china")
		if broker != "cn.mqtt.bambulab.com:8883" {
			t.Errorf("MQTTBroker(china) = %q; want %q", broker, "cn.mqtt.bambulab.com:8883")
		}
	})

	t.Run("default region (non-china)", func(t *testing.T) {
		broker := MQTTBroker("europe")
		if broker != "us.mqtt.bambulab.com:8883" {
			t.Errorf("MQTTBroker(europe) = %q; want %q", broker, "us.mqtt.bambulab.com:8883")
		}
	})
}

func TestMQTTCredentials(t *testing.T) {
	c := &BambuCloudClient{
		token:  "jwt-token-abc",
		userID: "12345",
	}
	if username := c.MQTTUsername(); username != "u_12345" {
		t.Errorf("MQTTUsername() = %q; want %q", username, "u_12345")
	}
	if password := c.MQTTPassword(); password != "jwt-token-abc" {
		t.Errorf("MQTTPassword() = %q; want %q", password, "jwt-token-abc")
	}
}

// ---------------------------------------------------------------------------
// Test: FormatDuration
// ---------------------------------------------------------------------------

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{
			name:     "negative (expired)",
			duration: -1 * time.Hour,
			want:     "expired",
		},
		{
			name:     "days and hours",
			duration: 25 * time.Hour,
			want:     "1d 1h 0m",
		},
		{
			name:     "multiple days",
			duration: 50 * time.Hour,
			want:     "2d 2h 0m",
		},
		{
			name:     "hours and minutes",
			duration: 2*time.Hour + 30*time.Minute,
			want:     "2h 30m",
		},
		{
			name:     "only minutes",
			duration: 45 * time.Minute,
			want:     "45m",
		},
		{
			name:     "zero duration",
			duration: 0,
			want:     "0m",
		},
		{
			name:     "exactly one day",
			duration: 24 * time.Hour,
			want:     "1d 0h 0m",
		},
		{
			name:     "single hour",
			duration: 1 * time.Hour,
			want:     "1h 0m",
		},
		{
			name:     "single minute",
			duration: 1 * time.Minute,
			want:     "1m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("FormatDuration(%v) = %q; want %q", tt.duration, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test: SetTokenFromLogin
// ---------------------------------------------------------------------------

func TestSetTokenFromLogin(t *testing.T) {
	c := &BambuCloudClient{}
	c.SetTokenFromLogin("token-from-login", "user-42", "china")
	if c.token != "token-from-login" {
		t.Errorf("token = %q; want %q", c.token, "token-from-login")
	}
	if c.userID != "user-42" {
		t.Errorf("userID = %q; want %q", c.userID, "user-42")
	}
	if c.region != "china" {
		t.Errorf("region = %q; want %q", c.region, "china")
	}
}

// ---------------------------------------------------------------------------
// Test: SetTokenFile
// ---------------------------------------------------------------------------

func TestSetTokenFile(t *testing.T) {
	c := &BambuCloudClient{}
	path := "/some/path/token.json"
	c.SetTokenFile(path)
	if c.tokenFile != path {
		t.Errorf("tokenFile = %q; want %q", c.tokenFile, path)
	}
}

// ---------------------------------------------------------------------------
// Test: Token and UserID accessors
// ---------------------------------------------------------------------------

func TestAccessors(t *testing.T) {
	c := &BambuCloudClient{
		token:  "access-token-value",
		userID: "access-user-id",
	}
	if got := c.Token(); got != "access-token-value" {
		t.Errorf("Token() = %q; want %q", got, "access-token-value")
	}
	if got := c.UserID(); got != "access-user-id" {
		t.Errorf("UserID() = %q; want %q", got, "access-user-id")
	}
}
