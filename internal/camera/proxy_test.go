package camera

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantBody   map[string]string // non-nil for error JSON responses
		wantCT     string            // expected Content-Type for success
		wantResp   string            // expected response body for success
	}{
		{
			name:       "missing url parameter",
			query:      "",
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]string{"error": "missing or invalid url parameter"},
		},
		{
			name:       "invalid url parameter",
			query:      "url=not-a-url",
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]string{"error": "missing or invalid url parameter"},
		},
		{
			name:       "relative url rejected",
			query:      "url=/relative/path",
			wantStatus: http.StatusBadRequest,
			wantBody:   map[string]string{"error": "missing or invalid url parameter"},
		},
	}

	t.Run("errors", func(t *testing.T) {
		handler := Handler(nil)
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/api/camera/proxy?"+tt.query, nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)

				resp := w.Result()
				defer resp.Body.Close()

				if resp.StatusCode != tt.wantStatus {
					t.Errorf("status = %d; want %d", resp.StatusCode, tt.wantStatus)
				}
				if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type = %q; want application/json", ct)
				}

				var body map[string]string
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatalf("decoding JSON body: %v", err)
				}
				for k, v := range tt.wantBody {
					if body[k] != v {
						t.Errorf("body[%q] = %q; want %q", k, body[k], v)
					}
				}
			})
		}
	})

	t.Run("unreachable upstream", func(t *testing.T) {
		// Port 1 is almost certainly not listening, so the dial will fail.
		handler := Handler(nil)
		req := httptest.NewRequest(http.MethodGet, "/api/camera/proxy?url=http://127.0.0.1:1/nonexistent", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("status = %d; want 502", resp.StatusCode)
		}

		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decoding JSON body: %v", err)
		}
		if body["error"] != "camera stream unreachable" {
			t.Errorf(`body["error"] = %q; want "camera stream unreachable"`, body["error"])
		}
	})

	t.Run("successful proxy", func(t *testing.T) {
		// Create an upstream test server that returns an MJPEG-like response.
		wantContentType := "multipart/x-mixed-replace; boundary=--boundary"
		wantBody := "--boundary\r\nContent-Type: image/jpeg\r\n\r\nfake-jpeg-data\r\n--boundary--"

		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", wantContentType)
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, wantBody)
		}))
		defer upstream.Close()

		handler := Handler(nil)
		proxyURL := "/api/camera/proxy?url=" + url.QueryEscape(upstream.URL)
		req := httptest.NewRequest(http.MethodGet, proxyURL, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d; want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != wantContentType {
			t.Errorf("Content-Type = %q; want %q", ct, wantContentType)
		}

		gotBody, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading response body: %v", err)
		}
		if string(gotBody) != wantBody {
			t.Errorf("body = %q; want %q", string(gotBody), wantBody)
		}
	})

	t.Run("forwards upstream status code (non-200)", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "not found")
		}))
		defer upstream.Close()

		handler := Handler(nil)
		proxyURL := "/api/camera/proxy?url=" + url.QueryEscape(upstream.URL)
		req := httptest.NewRequest(http.MethodGet, proxyURL, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d; want 404", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("Content-Type = %q; want image/jpeg", ct)
		}

		gotBody, _ := io.ReadAll(resp.Body)
		if strings.TrimSpace(string(gotBody)) != "not found" {
			t.Errorf("body = %q; want %q", strings.TrimSpace(string(gotBody)), "not found")
		}
	})
}
