package camera

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ---------------------------------------------------------------------------
// Bambu Lab P1S Camera Protocol
//
// The P1S camera on port 6000 uses a binary TLS protocol (NOT HTTP):
//   1. TCP connect to printer_ip:6000
//   2. TLS handshake (self-signed cert, InsecureSkipVerify required)
//   3. Send 80-byte binary auth packet
//   4. Receive continuous JPEG frames: 16-byte header + JPEG data
//
// Auth packet (80 bytes total):
//   Bytes  0-3:  uint32 LE 0x40 (64)
//   Bytes  4-7:  uint32 LE 0x3000 (12288)
//   Bytes  8-11: uint32 LE 0
//   Bytes 12-15: uint32 LE 0
//   Bytes 16-47: username "bblp" padded with nulls to 32 chars
//   Bytes 48-79: access code padded with nulls to 32 chars
//
// Frame format:
//   16-byte header:
//     Bytes  0-3: uint32 LE payload size (length of JPEG data)
//     Bytes  4-7: 0x00 (fixed)
//     Bytes  8-11: 0x01 (fixed)
//     Bytes 12-15: 0x00 (fixed)
//   Followed by payload_size bytes of JPEG data (starts with FF D8 FF)
//
// The stream is continuous at approximately 1 FPS.
// ---------------------------------------------------------------------------

const (
	// bambuAuthPacketSize is the fixed size of the binary auth packet.
	bambuAuthPacketSize = 80

	// bambuUsername is the fixed username for the auth packet.
	bambuUsername = "bblp"

	// bambuFrameHeaderSize is the size of the frame header preceding each JPEG.
	bambuFrameHeaderSize = 16

	// bambuReadTimeout is the timeout for reading the initial frame header
	// after connecting (to verify the connection is alive).
	bambuReadTimeout = 3 * time.Second
)

// BuildAuthPacket constructs the 80-byte binary authentication packet
// required by the Bambu Lab P1S camera protocol.
//
// The packet contains:
//   - Fixed header fields (little-endian uint32)
//   - Username "bblp" padded to 32 bytes with nulls
//   - Access code padded to 32 bytes with nulls
func BuildAuthPacket(accessCode string) [bambuAuthPacketSize]byte {
	var pkt [bambuAuthPacketSize]byte

	// Bytes 0-3: uint32 LE 0x40 (64)
	binary.LittleEndian.PutUint32(pkt[0:4], 0x40)

	// Bytes 4-7: uint32 LE 0x3000 (12288)
	binary.LittleEndian.PutUint32(pkt[4:8], 0x3000)

	// Bytes 8-11: uint32 LE 0
	binary.LittleEndian.PutUint32(pkt[8:12], 0)

	// Bytes 12-15: uint32 LE 0
	binary.LittleEndian.PutUint32(pkt[12:16], 0)

	// Bytes 16-47: username "bblp" padded with nulls to 32 chars
	copy(pkt[16:48], bambuUsername)

	// Bytes 48-79: access code padded with nulls to 32 chars
	copy(pkt[48:80], accessCode)

	return pkt
}

// BambuStreamReader manages a TLS connection to a Bambu Lab P1S camera
// and reads JPEG frames from the binary stream.
type BambuStreamReader struct {
	conn   net.Conn
	cancel context.CancelFunc

	// nextFrame holds a pre-read frame (used for the first frame which is
	// read during NewBambuStreamReader to verify the connection).
	nextFrame []byte
}

// NewBambuStreamReader establishes a TLS connection to the Bambu camera,
// sends the authentication packet, and verifies the connection by reading
// the first frame header.
//
// The caller must call Close() when done to release the connection.
func NewBambuStreamReader(ctx context.Context, host string, port int, accessCode string) (*BambuStreamReader, error) {
	ctx, cancel := context.WithCancel(ctx)

	addr := net.JoinHostPort(host, strconv.Itoa(port))

	// TCP dial with timeout
	dialer := &net.Dialer{Timeout: ConnectTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bambu camera: tcp dial %s: %w", addr, err)
	}

	// TLS handshake with self-signed cert (same as MQTT client).
	// Note: H2S/O1S printers only support TLS_RSA_WITH_AES_256_GCM_SHA384 (a
	// static-RSA cipher excluded from Go's defaults), so we must list it explicitly.
	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
		CipherSuites: []uint16{
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384, // required by H2S/O1S on port 6000
			// Go's default TLS 1.2 cipher suites (replicated explicitly so we can
			// include the RSA cipher above without losing forward-secrecy ciphers):
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		cancel()
		return nil, fmt.Errorf("bambu camera: tls handshake: %w", err)
	}

	r := &BambuStreamReader{
		conn:   tlsConn,
		cancel: cancel,
	}

	// Send auth packet
	pkt := BuildAuthPacket(accessCode)
	if _, err := r.conn.Write(pkt[:]); err != nil {
		r.Close()
		return nil, fmt.Errorf("bambu camera: send auth: %w", err)
	}

	// Read the first frame header to verify the connection is alive.
	// This also consumes the header so the first ReadFrame call gets the JPEG data.
	if err := r.conn.SetReadDeadline(time.Now().Add(bambuReadTimeout)); err != nil {
		r.Close()
		return nil, fmt.Errorf("bambu camera: set read deadline: %w", err)
	}
	var header [bambuFrameHeaderSize]byte
	if _, err := io.ReadFull(r.conn, header[:]); err != nil {
		r.Close()
		return nil, fmt.Errorf("bambu camera: read initial header: %w", err)
	}
	// Clear the deadline for subsequent reads (the stream is continuous).
	if err := r.conn.SetReadDeadline(time.Time{}); err != nil {
		r.Close()
		return nil, fmt.Errorf("bambu camera: clear read deadline: %w", err)
	}

	// Parse the payload size from the header so we can return the first frame.
	payloadSize := binary.LittleEndian.Uint32(header[0:4])
	if payloadSize == 0 {
		r.Close()
		return nil, fmt.Errorf("bambu camera: initial frame has zero payload size")
	}

	// Read the JPEG data for the first frame.
	jpegData := make([]byte, payloadSize)
	if _, err := io.ReadFull(r.conn, jpegData); err != nil {
		r.Close()
		return nil, fmt.Errorf("bambu camera: read initial frame: %w", err)
	}

	// Store the first frame so ReadFrame can return it.
	r.nextFrame = jpegData

	return r, nil
}

// ReadFrame reads the next JPEG frame from the camera stream.
// It returns the raw JPEG data (without the 16-byte header).
//
// The returned slice is only valid until the next call to ReadFrame.
// Callers should copy the data if they need to keep it.
func (r *BambuStreamReader) ReadFrame() ([]byte, error) {
	// If we have a pre-read first frame, return it.
	if r.nextFrame != nil {
		frame := r.nextFrame
		r.nextFrame = nil
		return frame, nil
	}

	return r.readNextFrame()
}

// readNextFrame reads a frame from the connection (no pre-read cache).
func (r *BambuStreamReader) readNextFrame() ([]byte, error) {
	var header [bambuFrameHeaderSize]byte
	if _, err := io.ReadFull(r.conn, header[:]); err != nil {
		return nil, fmt.Errorf("bambu camera: read frame header: %w", err)
	}

	payloadSize := binary.LittleEndian.Uint32(header[0:4])
	if payloadSize == 0 {
		return nil, fmt.Errorf("bambu camera: frame has zero payload size")
	}

	jpegData := make([]byte, payloadSize)
	if _, err := io.ReadFull(r.conn, jpegData); err != nil {
		return nil, fmt.Errorf("bambu camera: read frame data: %w", err)
	}

	return jpegData, nil
}

// Close tears down the TLS connection and cancels the context.
func (r *BambuStreamReader) Close() error {
	r.cancel()
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// BambuCameraHandler returns an http.HandlerFunc that serves the Bambu Lab
// P1S camera stream as MJPEG over HTTP.
//
// If mgr is non-nil, it uses the CameraManager's persistent connection and
// frame buffer for instant frame delivery. If nil, it falls back to the
// on-demand connection approach (original behavior).
//
// Expected query parameter: url (bambus://host:port?token=xxx)
//
// The handler:
//   - Parses the bambus:// URL to extract host, port, and access code
//   - Establishes a TLS connection to the camera (on-demand) or reads
//     from a persistent background connection (via CameraManager)
//   - Streams JPEG frames as multipart/x-mixed-replace MJPEG
//   - Handles client disconnect gracefully
//   - Returns 502 on connection/auth failure (on-demand mode only)
func BambuCameraHandler(mgr *CameraManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawURL := r.URL.Query().Get("url")
		if rawURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing url parameter"})
			return
		}

		parsedURL, err := url.Parse(rawURL)
		if err != nil || parsedURL.Scheme != "bambus" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bambus url"})
			return
		}

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

		if mgr != nil {
			buffer := mgr.GetBuffer(host, port, accessCode)
			serveFromBuffer(w, r, buffer)
		} else {
			serveOnDemand(w, r, rawURL, host, port, accessCode)
		}
	}
}

// serveFromBuffer streams MJPEG frames from a FrameBuffer to the client.
// It blocks until the first frame arrives before writing any HTTP response
// headers, so the browser never receives an empty response that would cause
// a broken-image flash.
func serveFromBuffer(w http.ResponseWriter, r *http.Request, buffer *FrameBuffer) {
	// Wait for first frame before sending any HTTP response.
	// This blocks until either a frame arrives (from the CameraManager's
	// background connection) or the client disconnects. The browser sees
	// a pending HTTP request — never a zero-byte multipart response.
	firstFrame, firstSeq, err := buffer.WaitForFrame(r.Context(), 0)
	if err != nil {
		return // client disconnected or context cancelled before first frame
	}

	boundary := "frame"
	w.Header().Set("Content-Type", fmt.Sprintf("multipart/x-mixed-replace; boundary=%s", boundary))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("bambu camera: response writer does not support flushing")
		return
	}

	// Write the first frame immediately (headers + data atomically).
	fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(firstFrame))
	w.Write(firstFrame)
	io.WriteString(w, "\r\n")
	flusher.Flush()

	lastSeq := firstSeq
	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		frame, seq, err := buffer.WaitForFrame(r.Context(), lastSeq)
		if err != nil {
			return // client disconnected
		}
		lastSeq = seq

		// Write MJPEG boundary and headers.
		_, writeErr := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(frame))
		if writeErr != nil {
			return
		}

		if _, writeErr = w.Write(frame); writeErr != nil {
			return
		}

		if _, writeErr = io.WriteString(w, "\r\n"); writeErr != nil {
			return
		}

		flusher.Flush()
	}
}

// serveOnDemand is the original behavior: connect to the camera on each
// request and stream frames directly.
func serveOnDemand(w http.ResponseWriter, r *http.Request, rawURL string, host string, port int, accessCode string) {
	reader, err := NewBambuStreamReader(r.Context(), host, port, accessCode)
	if err != nil {
		log.Printf("bambu camera: connection failed for %s: %v", rawURL, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "camera stream unreachable"})
		return
	}
	defer reader.Close()

	// Set MJPEG response headers.
	boundary := "frame"
	w.Header().Set("Content-Type", fmt.Sprintf("multipart/x-mixed-replace; boundary=%s", boundary))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Stream frames until client disconnects or an error occurs.
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("bambu camera: response writer does not support flushing")
		return
	}

	for {
		select {
		case <-r.Context().Done():
			// Client disconnected.
			return
		default:
		}

		frame, err := reader.ReadFrame()
		if err != nil {
			if r.Context().Err() != nil {
				// Client disconnected, not a real error.
				return
			}
			log.Printf("bambu camera: read frame error for %s: %v", rawURL, err)
			return
		}

		// Write MJPEG boundary and headers.
		// Format: --boundary\r\nContent-Type: image/jpeg\r\nContent-Length: N\r\n\r\n
		_, writeErr := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(frame))
		if writeErr != nil {
			if r.Context().Err() != nil {
				return
			}
			log.Printf("bambu camera: write header error for %s: %v", rawURL, writeErr)
			return
		}

		// Write JPEG data.
		if _, writeErr = w.Write(frame); writeErr != nil {
			if r.Context().Err() != nil {
				return
			}
			log.Printf("bambu camera: write frame error for %s: %v", rawURL, writeErr)
			return
		}

		// Write trailing CRLF.
		if _, writeErr = io.WriteString(w, "\r\n"); writeErr != nil {
			if r.Context().Err() != nil {
				return
			}
			log.Printf("bambu camera: write trailer error for %s: %v", rawURL, writeErr)
			return
		}

		flusher.Flush()
	}
}


