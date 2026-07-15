package camera

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
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
		handler := Handler(nil, nil)
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
		handler := Handler(nil, nil)
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

		handler := Handler(nil, nil)
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

		handler := Handler(nil, nil)
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

func TestHandler_RTSPS_NoGo2RTC(t *testing.T) {
	handler := Handler(nil, nil) // rtspMgr is nil
	req := httptest.NewRequest(http.MethodGet, "/api/camera/proxy?url="+url.QueryEscape("rtsps://192.168.1.100:322/live"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d; want 501", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding JSON body: %v", err)
	}
	if !strings.Contains(body["error"], "go2rtc") {
		t.Errorf(`body["error"] = %q; want message mentioning go2rtc`, body["error"])
	}
}

func TestHandler_UnknownScheme(t *testing.T) {
	handler := Handler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/camera/proxy?url="+url.QueryEscape("unknown://some.host/stream"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d; want 501", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding JSON body: %v", err)
	}
	if !strings.Contains(body["error"], "unknown") {
		t.Errorf(`body["error"] = %q; want message mentioning unknown scheme`, body["error"])
	}
}

func TestHandler_RTSPS_WithGo2RTC(t *testing.T) {
	// Start a mock HTTP server that mimics go2rtc's API.
	wantBody := "mock-mjpeg-data"
	mockGo2RTC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// go2rtc's /api/streams endpoint is used for health polling.
		if strings.Contains(r.URL.Path, "/api/streams") {
			w.WriteHeader(http.StatusOK)
			return
		}
		// go2rtc's /api/stream.mjpeg endpoint serves the MJPEG stream.
		if strings.Contains(r.URL.Path, "/api/stream.mjpeg") {
			w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, wantBody)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockGo2RTC.Close()

	mockURL, _ := url.Parse(mockGo2RTC.URL)
	mockPort, _ := strconv.Atoi(mockURL.Port())

	// Create a Go2RTCManager and inject a pre-registered instance pointing to
	// the mock server so that Start() returns early without exec'ing a binary.
	rtspMgr := NewGo2RTCManager("", 0)
	streamKey := "192.168.1.100:322_live"
	rtspMgr.mu.Lock()
	rtspMgr.instances[streamKey] = &go2rtcInstance{
		streamKey: streamKey,
		rtspsURL:  "rtsps://192.168.1.100:322/live",
		localPort: mockPort,
		startedAt: time.Now(),
	}
	rtspMgr.mu.Unlock()

	handler := Handler(nil, rtspMgr)
	req := httptest.NewRequest(http.MethodGet, "/api/camera/proxy?url="+url.QueryEscape("rtsps://192.168.1.100:322/live"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200", resp.StatusCode)
	}

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if string(gotBody) != wantBody {
		t.Errorf("body = %q; want %q", string(gotBody), wantBody)
	}
}

func TestFrameHandler_RTSPS_NoGo2RTC(t *testing.T) {
	handler := FrameHandler(nil, nil) // rtspMgr is nil
	req := httptest.NewRequest(http.MethodGet, "/api/camera/frame?url="+url.QueryEscape("rtsps://192.168.1.100:322/live"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Without a Go2RTCManager, FrameHandler should serve the placeholder JPEG.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200 (placeholder)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q; want image/jpeg", ct)
	}

	// Should be the placeholder JPEG.
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if len(gotBody) != len(placeholderJPEG) {
		t.Errorf("body length = %d; want %d (placeholder JPEG)", len(gotBody), len(placeholderJPEG))
	}
}

func TestFrameHandler_UnknownScheme(t *testing.T) {
	handler := FrameHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/camera/frame?url="+url.QueryEscape("unknown://some.host/stream"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Unknown schemes should also serve a placeholder.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200 (placeholder)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q; want image/jpeg", ct)
	}

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if len(gotBody) != len(placeholderJPEG) {
		t.Errorf("body length = %d; want %d (placeholder JPEG)", len(gotBody), len(placeholderJPEG))
	}
}

func TestFrameHandler_RTSPS_WithGo2RTC(t *testing.T) {
	// Start a mock HTTP server that mimics go2rtc's frame API.
	mockGo2RTC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/streams") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "/api/frame.jpeg") {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			// Return a minimal valid JPEG (SOI + EOI markers) that is longer
			// than the placeholder so we can distinguish them.
			jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xff, 0xd9}
			w.Write(jpeg)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockGo2RTC.Close()

	mockURL, _ := url.Parse(mockGo2RTC.URL)
	mockPort, _ := strconv.Atoi(mockURL.Port())

	rtspMgr := NewGo2RTCManager("", 0)
	streamKey := "192.168.1.100:322_live"
	rtspMgr.mu.Lock()
	rtspMgr.instances[streamKey] = &go2rtcInstance{
		streamKey: streamKey,
		rtspsURL:  "rtsps://192.168.1.100:322/live",
		localPort: mockPort,
		startedAt: time.Now(),
	}
	rtspMgr.mu.Unlock()

	handler := FrameHandler(nil, rtspMgr)
	req := httptest.NewRequest(http.MethodGet, "/api/camera/frame?url="+url.QueryEscape("rtsps://192.168.1.100:322/live"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q; want image/jpeg", ct)
	}

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	// Verify we got the frame from the mock, not the placeholder.
	if string(gotBody) == string(placeholderJPEG) {
		t.Errorf("body is placeholder JPEG; expected frame from mock go2rtc")
	}
	// Verify it's a JPEG (starts with FF D8).
	if len(gotBody) < 2 || gotBody[0] != 0xff || gotBody[1] != 0xd8 {
		t.Errorf("body does not start with JPEG SOI marker; got %x", gotBody[:2])
	}

	// Clean up: stop the go2rtc instance (it doesn't have a real cmd, so
	// just delete it from the map).
	rtspMgr.mu.Lock()
	delete(rtspMgr.instances, streamKey)
	rtspMgr.mu.Unlock()
}

func TestFrameHandler_RTSPS_LastGoodFrame(t *testing.T) {
	// Start a mock HTTP server that mimics go2rtc's frame API.
	frameJPEG := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xff, 0xd9}
	mockGo2RTC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/streams") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "/api/frame.jpeg") {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.Write(frameJPEG)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockGo2RTC.Close()

	mockURL, _ := url.Parse(mockGo2RTC.URL)
	mockPort, _ := strconv.Atoi(mockURL.Port())

	rtspMgr := NewGo2RTCManager("", 0)
	streamKey := "192.168.1.100:322_live"
	rtspMgr.mu.Lock()
	rtspMgr.instances[streamKey] = &go2rtcInstance{
		streamKey: streamKey,
		rtspsURL:  "rtsps://192.168.1.100:322/live",
		localPort: mockPort,
		startedAt: time.Now(),
	}
	rtspMgr.mu.Unlock()

	handler := FrameHandler(nil, rtspMgr)
	req := httptest.NewRequest(http.MethodGet, "/api/camera/frame?url="+url.QueryEscape("rtsps://192.168.1.100:322/live"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	firstBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading first response body: %v", err)
	}
	if string(firstBody) != string(frameJPEG) {
		t.Fatalf("first response body = %x; want %x", firstBody, frameJPEG)
	}

	// Remove the go2rtc instance so FrameURL returns !ok on the next call,
	// simulating a transient failure after a good frame was already cached.
	rtspMgr.mu.Lock()
	delete(rtspMgr.instances, streamKey)
	rtspMgr.mu.Unlock()

	req2 := httptest.NewRequest(http.MethodGet, "/api/camera/frame?url="+url.QueryEscape("rtsps://192.168.1.100:322/live"), nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	resp2 := w2.Result()
	defer resp2.Body.Close()
	secondBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("reading second response body: %v", err)
	}
	if string(secondBody) != string(firstBody) {
		t.Errorf("second response body = %x; want last-good frame %x", secondBody, firstBody)
	}
	if string(secondBody) == string(placeholderJPEG) {
		t.Errorf("second response body is the placeholder; want the cached last-good frame")
	}
}

func TestFrameHandler_HTTP_LastGoodFrame(t *testing.T) {
	frameJPEG := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xff, 0xd9}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.Write(frameJPEG)
	}))

	handler := FrameHandler(nil, nil)
	proxyURL := "/api/camera/frame?url=" + url.QueryEscape(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	firstBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading first response body: %v", err)
	}
	if string(firstBody) != string(frameJPEG) {
		t.Fatalf("first response body = %x; want %x", firstBody, frameJPEG)
	}

	// Close the upstream so the second request fails and must fall back to
	// the cached last-good frame instead of the placeholder.
	upstream.Close()

	req2 := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	resp2 := w2.Result()
	defer resp2.Body.Close()
	secondBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("reading second response body: %v", err)
	}
	if string(secondBody) != string(firstBody) {
		t.Errorf("second response body = %x; want last-good frame %x", secondBody, firstBody)
	}
	if string(secondBody) == string(placeholderJPEG) {
		t.Errorf("second response body is the placeholder; want the cached last-good frame")
	}
}

func TestFrameHandler_HTTP_MJPEG_ReadError_ServesLastGood(t *testing.T) {
	frameJPEG := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xff, 0xd9}
	var serveMJPEG bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !serveMJPEG {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.Write(frameJPEG)
			return
		}
		// Simulate a connection that drops mid-frame: announce MJPEG but
		// never write a complete JPEG (no EOI marker), then close the
		// connection by hijacking it.
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "--frame\r\nContent-Type: image/jpeg\r\n\r\n\xff\xd8\xff\xe0incomplete")
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijacking connection: %v", err)
		}
		conn.Close()
	}))
	defer upstream.Close()

	handler := FrameHandler(nil, nil)
	proxyURL := "/api/camera/frame?url=" + url.QueryEscape(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	firstBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading first response body: %v", err)
	}
	if string(firstBody) != string(frameJPEG) {
		t.Fatalf("first response body = %x; want %x", firstBody, frameJPEG)
	}

	serveMJPEG = true

	req2 := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	resp2 := w2.Result()
	defer resp2.Body.Close()
	secondBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("reading second response body: %v", err)
	}
	if string(secondBody) != string(firstBody) {
		t.Errorf("second response body = %x; want last-good frame %x", secondBody, firstBody)
	}
	if string(secondBody) == string(placeholderJPEG) {
		t.Errorf("second response body is the placeholder; want the cached last-good frame")
	}
}

func TestFrameHandler_HTTP_NoCache_ServesPlaceholder(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableURL := upstream.URL
	upstream.Close() // close before any request is ever made against it

	handler := FrameHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/camera/frame?url="+url.QueryEscape(unreachableURL), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200 (placeholder)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q; want image/jpeg", ct)
	}

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if string(gotBody) != string(placeholderJPEG) {
		t.Errorf("body is not the placeholder JPEG on first-ever unreachable request")
	}
}

func TestFrameHandler_Bambus_WithMgr(t *testing.T) {
	// Basic test to verify FrameHandler still works with bambus scheme.
	handler := FrameHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/camera/frame?url="+url.QueryEscape("bambus://192.168.1.100:6000/?token=test123"), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Without a CameraManager, bambus should return the placeholder.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200 (placeholder)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q; want image/jpeg", ct)
	}
}
