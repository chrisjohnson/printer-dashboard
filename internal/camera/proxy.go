package camera

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConnectTimeout is how long we wait for the upstream camera to accept the connection.
const ConnectTimeout = 5 * time.Second

// Handler returns an http.HandlerFunc that proxies MJPEG/snapshot camera streams.
// Expected query parameter: url (the target camera stream URL, must be absolute).
//
// If mgr is non-nil, Bambu cameras use the shared CameraManager for persistent
// connections and frame buffering. If nil, Bambu cameras fall back to on-demand
// connections (original behavior).
//
// A custom transport with a short dial timeout is used so that unreachable cameras
// fail fast without affecting the long-lived streaming connection.
func Handler(mgr *CameraManager, rtspMgr *Go2RTCManager) http.HandlerFunc {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: ConnectTimeout,
		}).DialContext,
	}
	client := &http.Client{Transport: transport}

	return func(w http.ResponseWriter, r *http.Request) {
		rawURL := r.URL.Query().Get("url")
		if rawURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid url parameter"})
			return
		}

		parsedURL, err := url.Parse(rawURL)
		if err != nil || !parsedURL.IsAbs() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid url parameter"})
			return
		}

		// Dispatch by URL scheme: bambus:// uses the Bambu Lab binary TLS protocol.
		if parsedURL.Scheme == "bambus" {
			BambuCameraHandler(mgr).ServeHTTP(w, r)
			return
		}

		// rtsps:// uses go2rtc to convert to HTTP MJPEG, then proxy.
		if parsedURL.Scheme == "rtsps" {
			if rtspMgr == nil {
				writeJSON(w, http.StatusNotImplemented, map[string]string{
					"error": "RTSPS camera support requires go2rtc (install with: brew install go2rtc or download from github.com/AlexxIT/go2rtc)",
				})
				return
			}
			// Start go2rtc instance for this RTSPS URL.
			// Use host:port as the stream key for deduplication.
			// Use Hostname()+Port() to ensure credentials are never included
			// (matching the format used by server.go for pre-connect).
			streamKey := parsedURL.Hostname() + ":" + parsedURL.Port() // e.g., "192.168.1.100:322"
			mjpegURL, err := rtspMgr.Start(r.Context(), streamKey, rawURL)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{
					"error": fmt.Sprintf("RTSPS camera unavailable: %v", err),
				})
				return
			}
			// Re-issue the request as an HTTP request to go2rtc's MJPEG endpoint.
			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mjpegURL, nil)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}

			transport := &http.Transport{
				DialContext: (&net.Dialer{
					Timeout: ConnectTimeout,
				}).DialContext,
			}
			client := &http.Client{Transport: transport}

			resp, err := client.Do(req)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "RTSPS stream unreachable"})
				return
			}
			defer resp.Body.Close()

			for key, vals := range resp.Header {
				for _, v := range vals {
					w.Header().Add(key, v)
				}
			}
			w.WriteHeader(resp.StatusCode)

			if _, err := io.Copy(w, resp.Body); err != nil {
				log.Printf("camera proxy: RTSPS streaming error for %s: %v", rawURL, err)
			}
			return
		}

		// Unknown scheme check: reject anything that isn't http or https.
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			writeJSON(w, http.StatusNotImplemented, map[string]string{
				"error": fmt.Sprintf("unsupported camera URL scheme: %s (supported: http, https, bambus, rtsps)", parsedURL.Scheme),
			})
			return
		}

		// Use the request context so client disconnect cancels the upstream request
		// and cleans up goroutines. No additional timeout — once connected, the stream
		// may run indefinitely (e.g. MJPEG).
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rawURL, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		// rawURL already contains any query parameters the camera URL had,
		// so they are forwarded to the upstream as-is.

		resp, err := client.Do(req)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "camera stream unreachable"})
			return
		}
		defer resp.Body.Close()

		// Copy upstream status code and headers (especially Content-Type for MJPEG)
		for key, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Stream the response body. The request context is bound to the client's
		// context, so when the client disconnects the upstream request is cancelled
		// and io.Copy will return. The deferred resp.Body.Close() ensures cleanup.
		// We do NOT use a separate goroutine — writing to http.ResponseWriter after
		// the handler returns is undefined behaviour (nil pointer crash).
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("camera proxy: streaming error for %s: %v", rawURL, err)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// placeholderJPEG is a 1x1 middle-gray JPEG served as a placeholder when
// no camera frame is available yet. Generated with Go's image/jpeg at Q50.
var placeholderJPEG = []byte{
	0xff, 0xd8, 0xff, 0xdb, 0x00, 0x84, 0x00, 0x10, 0x0b, 0x0c, 0x0e, 0x0c, 0x0a, 0x10, 0x0e, 0x0d,
	0x0e, 0x12, 0x11, 0x10, 0x13, 0x18, 0x28, 0x1a, 0x18, 0x16, 0x16, 0x18, 0x31, 0x23, 0x25, 0x1d,
	0x28, 0x3a, 0x33, 0x3d, 0x3c, 0x39, 0x33, 0x38, 0x37, 0x40, 0x48, 0x5c, 0x4e, 0x40, 0x44, 0x57,
	0x45, 0x37, 0x38, 0x50, 0x6d, 0x51, 0x57, 0x5f, 0x62, 0x67, 0x68, 0x67, 0x3e, 0x4d, 0x71, 0x79,
	0x70, 0x64, 0x78, 0x5c, 0x65, 0x67, 0x63, 0x01, 0x11, 0x12, 0x12, 0x18, 0x15, 0x18, 0x2f, 0x1a,
	0x1a, 0x2f, 0x63, 0x42, 0x38, 0x42, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63,
	0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63,
	0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63,
	0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0x63, 0xff, 0xc0, 0x00, 0x0b, 0x08, 0x00, 0x01, 0x00,
	0x01, 0x01, 0x01, 0x11, 0x00, 0xff, 0xc4, 0x00, 0xd2, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05,
	0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x10, 0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04, 0x03, 0x05,
	0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7d, 0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21,
	0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08, 0x23,
	0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16, 0x17,
	0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a,
	0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a,
	0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a,
	0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99,
	0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7,
	0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5,
	0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf1,
	0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xff, 0xda, 0x00, 0x08, 0x01, 0x01, 0x00,
	0x00, 0x3f, 0x00, 0x2b, 0xff, 0xd9,
}

// servePlaceholder writes the placeholder JPEG to the response.
func servePlaceholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(placeholderJPEG)))
	w.Write(placeholderJPEG)
}

// readFirstJPEGFromMJPEG reads the first JPEG frame from a multipart/x-mixed-replace
// MJPEG stream. It returns the raw JPEG bytes (without the multipart headers).
func readFirstJPEGFromMJPEG(r io.Reader) []byte {
	br := bufio.NewReader(r)
	// Read until we find a JPEG start marker (FF D8).
	for {
		b, err := br.ReadByte()
		if err != nil {
			return placeholderJPEG
		}
		if b != 0xFF {
			continue
		}
		next, err := br.ReadByte()
		if err != nil {
			return placeholderJPEG
		}
		if next != 0xD8 {
			continue
		}
		// Found JPEG SOI marker. Read until FF D9 (EOI).
		// We already have FF D8 — start collecting from there.
		jpeg := []byte{0xFF, 0xD8}
		for {
			b, err := br.ReadByte()
			if err != nil {
				return placeholderJPEG
			}
			jpeg = append(jpeg, b)
			if b == 0xFF {
				next, err := br.ReadByte()
				if err != nil {
					return placeholderJPEG
				}
				jpeg = append(jpeg, next)
				if next == 0xD9 {
					// Found JPEG EOI marker — done.
					return jpeg
				}
			}
		}
	}
}

// FrameHandler returns an http.HandlerFunc that serves a single JPEG frame
// from the camera. For Bambu cameras it returns the latest buffered frame;
// for HTTP cameras it makes a single proxied request. If no frame is available
// it serves a placeholder JPEG to avoid browser flicker.
//
// Expected query parameter: url (the target camera URL, must be absolute).
func FrameHandler(mgr *CameraManager, rtspMgr *Go2RTCManager) http.HandlerFunc {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: ConnectTimeout,
		}).DialContext,
	}
	client := &http.Client{Transport: transport}

	return func(w http.ResponseWriter, r *http.Request) {
		rawURL := r.URL.Query().Get("url")
		if rawURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid url parameter"})
			return
		}

		parsedURL, err := url.Parse(rawURL)
		if err != nil || !parsedURL.IsAbs() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid url parameter"})
			return
		}

		if parsedURL.Scheme == "bambus" {
			host := parsedURL.Hostname()
			portStr := parsedURL.Port()
			if host == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing host in bambus url"})
				return
			}
			if portStr == "" {
				portStr = "6000"
			}
			port, err := strconv.Atoi(portStr)
			if err != nil || port < 1 || port > 65535 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid port in bambus url"})
				return
			}
			accessCode := parsedURL.Query().Get("token")
			if accessCode == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing token in bambus url"})
				return
			}

			var frame []byte
			if mgr != nil {
				buffer := mgr.GetBuffer(host, port, accessCode)
				frame = buffer.Latest()
			}
			if frame == nil {
				frame = placeholderJPEG
			}

			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Length", strconv.Itoa(len(frame)))
			w.Write(frame)
			return
		}

		if parsedURL.Scheme == "rtsps" {
			if rtspMgr == nil {
				servePlaceholder(w)
				return
			}
			streamKey := parsedURL.Hostname() + ":" + parsedURL.Port()
			if _, err := rtspMgr.Start(r.Context(), streamKey, rawURL); err != nil {
				servePlaceholder(w)
				return
			}
			frameURL, ok := rtspMgr.FrameURL(streamKey)
			if !ok {
				servePlaceholder(w)
				return
			}

			transport := &http.Transport{
				DialContext: (&net.Dialer{
					Timeout: ConnectTimeout,
				}).DialContext,
			}
			client := &http.Client{Transport: transport}

			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, frameURL, nil)
			if err != nil {
				servePlaceholder(w)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				servePlaceholder(w)
				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
			if err != nil {
				servePlaceholder(w)
				return
			}

			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Write(body)
			return
		}

		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			servePlaceholder(w)
			return
		}

		// HTTP camera: make a single GET request and serve the response body.
		// For MJPEG streams (multipart/x-mixed-replace), read only the first
		// JPEG frame instead of consuming the entire never-ending stream.
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rawURL, nil)
		if err != nil {
			servePlaceholder(w)
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			servePlaceholder(w)
			return
		}
		defer resp.Body.Close()

		var body []byte
		contentType := resp.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "multipart/x-mixed-replace") {
			// Read just the first JPEG frame from the MJPEG stream.
			// Each part is: --boundary\r\nContent-Type: image/jpeg\r\n\r\n<JPEG bytes>\r\n
			body = readFirstJPEGFromMJPEG(resp.Body)
			contentType = "image/jpeg"
		} else {
			// Single-image response — read the body with a 10MB limit.
			body, err = io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
			if err != nil {
				servePlaceholder(w)
				return
			}
		}
		if contentType == "" {
			contentType = "image/jpeg"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}
}
