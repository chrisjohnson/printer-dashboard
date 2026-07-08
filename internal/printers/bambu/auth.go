package bambu

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Bambu Cloud API base URLs.
const (
	apiBaseGlobal = "https://api.bambulab.com"
	apiBaseChina  = "https://api.bambulab.cn"
)

// DefaultTokenDir is the directory where tokens are stored (~/.printer-dashboard).
var DefaultTokenDir = filepath.Join(os.Getenv("HOME"), ".printer-dashboard")

// --- API request / response types ---

type loginRequest struct {
	Account  string `json:"account"`
	Password string `json:"password"`
	APIError string `json:"apiError"`
}

type loginResponse struct {
	Success     bool   `json:"success"`
	AccessToken string `json:"accessToken"`
	LoginType   string `json:"loginType,omitempty"`
	TFAKey      string `json:"tfaKey,omitempty"`
	ExpiresIn   int64  `json:"expiresIn,omitempty"`
	HasToken    bool   `json:"hasToken,omitempty"`
}

type verifyCodeRequest struct {
	Email string `json:"email"`
	Type  string `json:"type"`
}

type deviceBindResponse struct {
	Message string `json:"message"`
	Devices []struct {
		DevID          string `json:"dev_id"`
		Name           string `json:"name"`
		Online         bool   `json:"online"`
		PrintStatus    string `json:"print_status"`
		DevModelName   string `json:"dev_model_name"`
		DevProductName string `json:"dev_product_name"`
		DevAccessCode  string `json:"dev_access_code"`
	} `json:"devices"`
}

type preferenceResponse struct {
	UserID json.Number `json:"uid"`
}

// --- JWT token payload (for reading expiry) ---

type jwtPayload struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Exp    int64  `json:"exp"`
	Iat    int64  `json:"iat"`
}

// JWTInfo holds decoded (unsigned) info from a Bambu JWT token.
type JWTInfo struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Exp    int64  `json:"exp"`
	Iat    int64  `json:"iat"`
}

// ParseJWT decodes a JWT payload without cryptographic verification.
// Useful for reading expiry and user info from a token before using it.
func ParseJWT(token string) (*JWTInfo, error) {
	jp, err := parseJWT(token)
	if err != nil {
		return nil, err
	}
	return &JWTInfo{
		UserID: jp.UserID,
		Email:  jp.Email,
		Exp:    jp.Exp,
		Iat:    jp.Iat,
	}, nil
}

// BambuCloudClient handles HTTP communication with the Bambu Lab Cloud API.
type BambuCloudClient struct {
	httpClient  *http.Client
	baseURL     string
	token       string
	userID      string
	region      string
	tokenFile   string    // path to persisted token, empty = don't persist
	tokenExpiry time.Time // when the token expires (zero = unknown)
}

// NewBambuCloudClient creates a new client for the Bambu Cloud API.
func NewBambuCloudClient(region string) *BambuCloudClient {
	baseURL := apiBaseGlobal
	if region == "china" {
		baseURL = apiBaseChina
	}
	return &BambuCloudClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		region:     region,
	}
}

// SetTokenFile sets the path for persisting the token to disk.
// If set, the token will be saved after successful login and loaded on startup.
func (c *BambuCloudClient) SetTokenFile(path string) {
	c.tokenFile = path
}

// DefaultTokenPath returns the default token file path for the given account email.
func DefaultTokenPath(email string) string {
	// Use a hash of the email to create a unique filename
	safeName := strings.ReplaceAll(email, "@", "_at_")
	safeName = strings.ReplaceAll(safeName, ".", "_dot_")
	return filepath.Join(DefaultTokenDir, fmt.Sprintf("bambu_token_%s.json", safeName))
}

// --- Token persistence ---

// tokenData is the format saved to disk.
type tokenData struct {
	Token     string `json:"token"`
	UserID    string `json:"user_id"`
	Region    string `json:"region"`
	Email     string `json:"email,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"` // Unix timestamp
}

// SaveToken persists the current token to disk.
func (c *BambuCloudClient) SaveToken() error {
	if c.tokenFile == "" {
		return nil // persistence not configured
	}
	dir := filepath.Dir(c.tokenFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	data := tokenData{
		Token:     c.token,
		UserID:    c.userID,
		Region:    c.region,
		ExpiresAt: c.tokenExpiry.Unix(),
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	if err := os.WriteFile(c.tokenFile, encoded, 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	log.Printf("bambu cloud: token saved to %s", c.tokenFile)
	return nil
}

// LoadToken loads a previously saved token from disk.
// Returns true if a token was loaded, false if no file exists.
func (c *BambuCloudClient) LoadToken() (bool, error) {
	if c.tokenFile == "" {
		return false, nil
	}
	data, err := os.ReadFile(c.tokenFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read token file: %w", err)
	}
	var td tokenData
	if err := json.Unmarshal(data, &td); err != nil {
		return false, fmt.Errorf("parse token file: %w", err)
	}
	if td.Token == "" || td.UserID == "" {
		return false, fmt.Errorf("token file has empty fields")
	}
	c.token = td.Token
	c.userID = td.UserID
	c.region = td.Region
	if td.ExpiresAt > 0 {
		c.tokenExpiry = time.Unix(td.ExpiresAt, 0)
	}
	return true, nil
}

// DeleteToken removes the persisted token file.
func (c *BambuCloudClient) DeleteToken() error {
	if c.tokenFile == "" {
		return nil
	}
	if err := os.Remove(c.tokenFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete token file: %w", err)
	}
	return nil
}

// --- Token inspection ---

// parseJWT decodes a JWT payload without verification (for reading expiry etc.).
func parseJWT(token string) (*jwtPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}
	// Decode base64 payload (part 2)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try with padding
		payload, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			// Try standard base64
			payload, err = base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				return nil, fmt.Errorf("decode JWT payload: %w", err)
			}
		}
	}
	var jp jwtPayload
	if err := json.Unmarshal(payload, &jp); err != nil {
		return nil, fmt.Errorf("parse JWT payload: %w", err)
	}
	return &jp, nil
}

// TokenExpiry returns the token's expiry time.
func (c *BambuCloudClient) TokenExpiry() time.Time {
	if !c.tokenExpiry.IsZero() {
		return c.tokenExpiry
	}
	// Fallback for JWT tokens (old format or if expiry wasn't saved)
	jp, err := parseJWT(c.token)
	if err != nil || jp == nil {
		return time.Time{}
	}
	return time.Unix(jp.Exp, 0)
}

// TokenValid checks if the token is still valid (not expired and has a reasonable lifetime).
func (c *BambuCloudClient) TokenValid() bool {
	if c.token == "" {
		return false
	}
	exp := c.TokenExpiry()
	if exp.IsZero() {
		// Unknown expiry — assume valid (e.g., opaque token loaded from disk
		// without saved expiry, or old JWT that failed to parse).
		return true
	}
	// Consider valid if more than 1 hour until expiry
	return time.Now().Add(1 * time.Hour).Before(exp)
}

// TokenLifetimeLeft returns the duration until the token expires.
// Returns 0 if expiry is unknown.
func (c *BambuCloudClient) TokenLifetimeLeft() time.Duration {
	exp := c.TokenExpiry()
	if exp.IsZero() {
		return 0
	}
	return time.Until(exp)
}

// --- Authentication ---

// LoginWithToken sets a pre-obtained token and fetches the user ID.
// If token persistence is configured, it saves the token after successful verification.
func (c *BambuCloudClient) LoginWithToken(token string) error {
	c.token = token
	uid, err := c.getUserID()
	if err != nil {
		return fmt.Errorf("token rejected by server: %w", err)
	}
	c.userID = uid

	// Show token lifetime
	if exp := c.TokenExpiry(); !exp.IsZero() {
		left := time.Until(exp)
		log.Printf("bambu cloud: authenticated with token, user_id=%s, expires in %s (at %s)",
			uid, FormatDuration(left), exp.Format("2006-01-02 15:04:05"))
	} else {
		log.Printf("bambu cloud: authenticated with token, user_id=%s", uid)
	}

	// Persist token
	if err := c.SaveToken(); err != nil {
		log.Printf("bambu cloud: warning: failed to save token: %v", err)
	}
	return nil
}

// Login performs email/password login and handles 2FA if needed.
// If a codeCallback is provided, it will be called to get the email verification code.
// If codeCallback is nil, login will attempt without 2FA (will fail if 2FA is required).
// Automatically persists the token if token file is configured.
func (c *BambuCloudClient) Login(email, password string, codeCallback func() (string, error)) error {
	log.Printf("bambu cloud: logging in as %s...", email)

	// Step 1: Initial login
	loginBody := loginRequest{
		Account:  email,
		Password: password,
		APIError: "",
	}

	data, err := c.doJSON("POST", "/v1/user-service/user/login", loginBody)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}

	var lr loginResponse
	if err := json.Unmarshal(data, &lr); err != nil {
		return fmt.Errorf("parsing login response: %w", err)
	}

	// Step 2: Handle 2FA if needed
	if !lr.Success && lr.LoginType == "verifyCode" {
		log.Printf("bambu cloud: initial login response (raw): %s", string(data))
		if codeCallback == nil {
			return fmt.Errorf("2FA required (check your email for verification code). Either provide a code callback, or use a pre-obtained token instead")
		}

		// Send verification code to email
		vcr := verifyCodeRequest{Email: email, Type: "codeLogin"}
		if _, err := c.doJSON("POST", "/v1/user-service/user/sendemail/code", vcr); err != nil {
			return fmt.Errorf("sending verification code: %w", err)
		}

		log.Println("bambu cloud: 2FA verification code sent to your email.")
		code, err := codeCallback()
		if err != nil {
			return fmt.Errorf("getting verification code: %w", err)
		}

		// Login with code
		loginWithCode := map[string]string{
			"account": email,
			"code":    code,
		}
		data, err = c.doJSON("POST", "/v1/user-service/user/login", loginWithCode)
		if err != nil {
			return fmt.Errorf("2FA login request failed: %w (body: %s)", err, string(data))
		}
		if err := json.Unmarshal(data, &lr); err != nil {
			return fmt.Errorf("parsing 2FA login response: %w", err)
		}
		log.Printf("bambu cloud: 2FA response: success=%v loginType=%s hasToken=%v",
			lr.Success, lr.LoginType, lr.AccessToken != "")
	}

	// After 2FA, the API may return an accessToken but omit the success field.
	// Treat any response with a non-empty accessToken as successful.
	if !lr.Success && lr.AccessToken == "" {
		return fmt.Errorf("login failed: success=false, loginType=%s, raw=%s", lr.LoginType, string(data))
	}

	c.token = lr.AccessToken
	if lr.ExpiresIn > 0 {
		c.tokenExpiry = time.Now().Add(time.Duration(lr.ExpiresIn) * time.Second)
	}

	// Step 3: Get user ID
	uid, err := c.getUserID()
	if err != nil {
		log.Printf("bambu cloud: getUserID failed, trying to parse from token...")
		// Try to extract user_id from the token itself if it's a JWT
		if jp, parseErr := parseJWT(c.token); parseErr == nil && jp.UserID != "" {
			uid = jp.UserID
			log.Printf("bambu cloud: extracted user_id=%s from JWT token", uid)
			err = nil
		}
	}
	if err != nil {
		return fmt.Errorf("getting user ID after login: %w", err)
	}
	c.userID = uid

	// Show token lifetime
	if exp := c.TokenExpiry(); !exp.IsZero() {
		left := time.Until(exp)
		log.Printf("bambu cloud: logged in as user_id=%s, token expires in %s (at %s)",
			uid, FormatDuration(left), exp.Format("2006-01-02 15:04:05"))
	} else {
		log.Printf("bambu cloud: logged in as user_id=%s (expiry unknown)", uid)
	}

	// Persist token
	if err := c.SaveToken(); err != nil {
		log.Printf("bambu cloud: warning: failed to save token: %v", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Split login flow (for web UI — separate steps instead of a single callback)
// ---------------------------------------------------------------------------

// LoginStep1 sends the email/password and returns the login response.
//
// If the response indicates 2FA is needed (LoginType == "verifyCode"),
// call SendVerificationCode then LoginStep2.
//
// If the response includes an access token and no 2FA is required, the client
// is fully authenticated after this call: token and userID are set, and the
// token is persisted (if a tokenFile is configured).
func (c *BambuCloudClient) LoginStep1(email, password string) (*loginResponse, error) {
	loginBody := loginRequest{
		Account:  email,
		Password: password,
		APIError: "",
	}
	data, err := c.doJSON("POST", "/v1/user-service/user/login", loginBody)
	if err != nil {
		return nil, fmt.Errorf("login request failed: %w", err)
	}
	var lr loginResponse
	if err := json.Unmarshal(data, &lr); err != nil {
		return nil, fmt.Errorf("parsing login response: %w", err)
	}

	// If the response includes a token and no 2FA is needed, the login is
	// complete — set the token, fetch the user ID, and persist.
	if lr.AccessToken != "" && lr.LoginType != "verifyCode" {
		c.token = lr.AccessToken
		if lr.ExpiresIn > 0 {
			c.tokenExpiry = time.Now().Add(time.Duration(lr.ExpiresIn) * time.Second)
		}

		uid, err := c.getUserID()
		if err != nil {
			// Fallback: try to extract user_id from JWT without an API call
			if jp, parseErr := parseJWT(c.token); parseErr == nil && jp.UserID != "" {
				uid = jp.UserID
				err = nil
			}
		}
		if err != nil {
			return nil, fmt.Errorf("getting user ID after login: %w", err)
		}
		c.userID = uid

		if err := c.SaveToken(); err != nil {
			log.Printf("bambu cloud: warning: failed to save token: %v", err)
		}
	}

	return &lr, nil
}

// SendVerificationCode requests a 2FA verification code be sent to the user's email.
func (c *BambuCloudClient) SendVerificationCode(email string) error {
	vcr := verifyCodeRequest{Email: email, Type: "codeLogin"}
	if _, err := c.doJSON("POST", "/v1/user-service/user/sendemail/code", vcr); err != nil {
		return fmt.Errorf("sending verification code: %w", err)
	}
	log.Println("bambu cloud: 2FA verification code sent to email.")
	return nil
}

// LoginStep2 completes the 2FA login with the verification code.
// Sets the token and fetches the user ID on success.
func (c *BambuCloudClient) LoginStep2(email, code string) error {
	loginWithCode := map[string]string{
		"account": email,
		"code":    code,
	}
	data, err := c.doJSON("POST", "/v1/user-service/user/login", loginWithCode)
	if err != nil {
		return fmt.Errorf("2FA login request failed: %w (body: %s)", err, string(data))
	}
	var lr loginResponse
	if err := json.Unmarshal(data, &lr); err != nil {
		return fmt.Errorf("parsing 2FA login response: %w", err)
	}

	if !lr.Success && lr.AccessToken == "" {
		return fmt.Errorf("login failed (code rejected)")
	}

	c.token = lr.AccessToken
	if lr.ExpiresIn > 0 {
		c.tokenExpiry = time.Now().Add(time.Duration(lr.ExpiresIn) * time.Second)
	}

	// Fetch user ID
	uid, err := c.getUserID()
	if err != nil {
		return fmt.Errorf("getting user ID after login: %w", err)
	}
	c.userID = uid

	// Persist token
	if err := c.SaveToken(); err != nil {
		log.Printf("bambu cloud: warning: failed to save token: %v", err)
	}

	return nil
}

// SetTokenFromLogin sets the token, user ID, and region on an existing client
// (used by the web onboarding flow to save state from the server-side client).
func (c *BambuCloudClient) SetTokenFromLogin(token, userID, region string) {
	c.token = token
	c.userID = userID
	c.region = region
}

// EnsureAuthenticated checks the current token and attempts to re-auth if needed.
// If email/password are provided, it will try to login again.
// Returns true if a valid token is available after the attempt.
func (c *BambuCloudClient) EnsureAuthenticated(email, password string, codeCallback func() (string, error)) bool {
	if c.TokenValid() {
		return true
	}

	if c.token != "" {
		log.Printf("bambu cloud: token expired or expiring soon (lifetime left: %s), re-authenticating...",
			FormatDuration(c.TokenLifetimeLeft()))
	}

	// Try to re-auth with email/password
	if email != "" && password != "" {
		if err := c.Login(email, password, codeCallback); err != nil {
			log.Printf("bambu cloud: re-auth failed: %v", err)
			return false
		}
		return true
	}

	// If we have a token but it's expired and no credentials to refresh
	if c.token != "" {
		log.Printf("bambu cloud: token expired, cannot refresh (no credentials configured)")
		return false
	}

	return false
}

// Token returns the current JWT access token.
func (c *BambuCloudClient) Token() string { return c.token }

// UserID returns the numeric user ID.
func (c *BambuCloudClient) UserID() string { return c.userID }

// GetDevices fetches the list of devices bound to the user's account.
func (c *BambuCloudClient) GetDevices() ([]DeviceInfo, error) {
	data, err := c.doAuthJSON("GET", "/v1/iot-service/api/user/bind", nil)
	if err != nil {
		return nil, fmt.Errorf("get devices: %w", err)
	}

	var resp deviceBindResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing devices response: %w", err)
	}

	devices := make([]DeviceInfo, 0, len(resp.Devices))
	for _, d := range resp.Devices {
		devices = append(devices, DeviceInfo{
			DevID:          d.DevID,
			Name:           d.Name,
			Online:         d.Online,
			PrintStatus:    d.PrintStatus,
			DevModelName:   d.DevModelName,
			DevProductName: d.DevProductName,
			DevAccessCode:  d.DevAccessCode,
		})
	}
	return devices, nil
}

// DeviceInfo holds information about a Bambu printer from the cloud API.
type DeviceInfo struct {
	DevID          string `json:"dev_id"`
	Name           string `json:"name"`
	Online         bool   `json:"online"`
	PrintStatus    string `json:"print_status"`
	DevModelName   string `json:"dev_model_name"`
	DevProductName string `json:"dev_product_name"`
	DevAccessCode  string `json:"dev_access_code"`
}

// getUserID fetches the user's numeric ID from the preference endpoint.
func (c *BambuCloudClient) getUserID() (string, error) {
	data, err := c.doAuthJSON("GET", "/v1/design-user-service/my/preference", nil)
	if err != nil {
		return "", err
	}
	var pref preferenceResponse
	if err := json.Unmarshal(data, &pref); err != nil {
		return "", fmt.Errorf("parsing preference: %w", err)
	}
	if pref.UserID.String() == "" {
		return "", fmt.Errorf("user_id not found in preference response")
	}
	return pref.UserID.String(), nil
}

// --- HTTP helpers ---

func (c *BambuCloudClient) doJSON(method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

func (c *BambuCloudClient) doAuthJSON(method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// MQTTUsername returns the MQTT username for cloud connections.
// Format: u_{user_id}
func (c *BambuCloudClient) MQTTUsername() string {
	return "u_" + c.userID
}

// MQTTPassword returns the MQTT password (the JWT access token).
func (c *BambuCloudClient) MQTTPassword() string {
	return c.token
}

// MQTTBroker returns the cloud MQTT broker address.
func MQTTBroker(region string) string {
	if region == "china" {
		return "cn.mqtt.bambulab.com:8883"
	}
	return "us.mqtt.bambulab.com:8883"
}

// --- helpers ---

func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
