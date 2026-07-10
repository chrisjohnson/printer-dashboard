package camera

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ConnectTimeout is how long we wait for the upstream camera to accept the connection.
const ConnectTimeout = 5 * time.Second

// Handler returns an http.HandlerFunc that proxies MJPEG/snapshot camera streams.
// Expected query parameter: url (the target camera stream URL, must be absolute).
//
// A custom transport with a short dial timeout is used so that unreachable cameras
// fail fast without affecting the long-lived streaming connection.
func Handler() http.HandlerFunc {
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
