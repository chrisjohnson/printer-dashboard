package bambu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Bambu Cloud API base URLs.
const (
	apiBaseGlobal = "https://api.bambulab.com"
	apiBaseChina  = "https://api.bambulab.cn"
)

// --- API request / response types ---

type loginRequest struct {
	Account  string `json:"account"`
	Password string `json:"password"`
	APIError string `json:"apiError"`
}

type loginResponse struct {
	Success    bool   `json:"success"`
	AccessToken string `json:"accessToken"`
	LoginType  string `json:"loginType,omitempty"`
	TFAKey     string `json:"tfaKey,omitempty"`
}

type verifyCodeRequest struct {
	Email string `json:"email"`
	Type  string `json:"type"`
}

type deviceBindResponse struct {
	Message string `json:"message"`
	Devices []struct {
		DevID         string `json:"dev_id"`
		Name          string `json:"name"`
		Online        bool   `json:"online"`
		PrintStatus   string `json:"print_status"`
		DevModelName  string `json:"dev_model_name"`
		DevProductName string `json:"dev_product_name"`
		DevAccessCode string `json:"dev_access_code"`
	} `json:"devices"`
}

type preferenceResponse struct {
	UserID string `json:"userId"`
}

// BambuCloudClient handles HTTP communication with the Bambu Lab Cloud API.
type BambuCloudClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
	userID     string
	region     string
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

// LoginWithToken sets a pre-obtained token and fetches the user ID.
func (c *BambuCloudClient) LoginWithToken(token string) error {
	c.token = token
	uid, err := c.getUserID()
	if err != nil {
		return fmt.Errorf("getting user ID from token: %w", err)
	}
	c.userID = uid
	log.Printf("bambu cloud: authenticated with token, user_id=%s", uid)
	return nil
}

// Login performs email/password login and handles 2FA if needed.
// If a codeCallback is provided, it will be called to get the email verification code.
// If codeCallback is nil, login will attempt without 2FA (may fail if 2FA is required).
func (c *BambuCloudClient) Login(email, password string, codeCallback func() (string, error)) error {
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
		if codeCallback == nil {
			return fmt.Errorf("2FA required but no code callback provided")
		}

		// Send verification code to email
		vcr := verifyCodeRequest{Email: email, Type: "codeLogin"}
		if _, err := c.doJSON("POST", "/v1/user-service/user/sendemail/code", vcr); err != nil {
			return fmt.Errorf("sending verification code: %w", err)
		}

		log.Println("2FA verification code sent to your email.")
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
			return fmt.Errorf("2FA login request failed: %w", err)
		}
		if err := json.Unmarshal(data, &lr); err != nil {
			return fmt.Errorf("parsing 2FA login response: %w", err)
		}
	}

	if !lr.Success {
		return fmt.Errorf("login failed: success=false, loginType=%s", lr.LoginType)
	}

	c.token = lr.AccessToken

	// Step 3: Get user ID
	uid, err := c.getUserID()
	if err != nil {
		return fmt.Errorf("getting user ID after login: %w", err)
	}
	c.userID = uid

	log.Printf("bambu cloud: logged in as user_id=%s", uid)
	return nil
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
			DevID:         d.DevID,
			Name:          d.Name,
			Online:        d.Online,
			PrintStatus:   d.PrintStatus,
			DevModelName:  d.DevModelName,
			DevProductName: d.DevProductName,
			DevAccessCode: d.DevAccessCode,
		})
	}
	return devices, nil
}

// DeviceInfo holds information about a Bambu printer from the cloud API.
type DeviceInfo struct {
	DevID         string `json:"dev_id"`
	Name          string `json:"name"`
	Online        bool   `json:"online"`
	PrintStatus   string `json:"print_status"`
	DevModelName  string `json:"dev_model_name"`
	DevProductName string `json:"dev_product_name"`
	DevAccessCode string `json:"dev_access_code"`
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
	if pref.UserID == "" {
		return "", fmt.Errorf("user_id not found in preference response")
	}
	return pref.UserID, nil
}

// doJSON performs an HTTP request with JSON body and returns the response body.
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
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// doAuthJSON performs an authenticated HTTP request with Bearer token.
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
