package camera

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// FrameBuffer stores the latest JPEG frame from a Bambu camera and
// notifies waiting consumers using a channel-close-and-replace pattern.
// Each Update closes the old notify channel and creates a new one,
// waking all goroutines blocked in WaitForFrame.
type FrameBuffer struct {
	mu     sync.RWMutex
	frame  []byte
	seq    uint64
	notify chan struct{} // closed + replaced on each Update to wake waiters
	closed bool         // set by Close(), prevents re-notification loop
}

// errBufferClosed is returned by WaitForFrame when the buffer is closed.
var errBufferClosed = fmt.Errorf("frame buffer closed")

// NewFrameBuffer creates a new FrameBuffer.
func NewFrameBuffer() *FrameBuffer {
	return &FrameBuffer{
		notify: make(chan struct{}),
	}
}

// Latest returns a copy of the most recent frame, or nil if no frame
// has been received yet.
func (fb *FrameBuffer) Latest() []byte {
	fb.mu.RLock()
	defer fb.mu.RUnlock()
	if fb.frame == nil {
		return nil
	}
	f := make([]byte, len(fb.frame))
	copy(f, fb.frame)
	return f
}

// Update stores a new frame and broadcasts to all waiters by closing
// the current notify channel and creating a new one.
func (fb *FrameBuffer) Update(frame []byte) {
	fb.mu.Lock()
	fb.frame = make([]byte, len(frame))
	copy(fb.frame, frame)
	fb.seq++
	close(fb.notify)
	fb.notify = make(chan struct{})
	fb.mu.Unlock()
}

// WaitForFrame blocks until a frame newer than lastSeq is available,
// or until ctx is cancelled. It returns the new frame and its sequence
// number so the caller can track which frames it has already seen.
// Returns errBufferClosed if the buffer has been closed.
func (fb *FrameBuffer) WaitForFrame(ctx context.Context, lastSeq uint64) ([]byte, uint64, error) {
	fb.mu.RLock()
	for fb.seq <= lastSeq {
		if fb.closed {
			fb.mu.RUnlock()
			return nil, 0, errBufferClosed
		}
		notifyCh := fb.notify
		fb.mu.RUnlock()

		select {
		case <-notifyCh:
			// Channel closed — a new frame arrived. Loop to check.
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}

		fb.mu.RLock()
	}

	// Got a new frame.
	seq := fb.seq
	frame := make([]byte, len(fb.frame))
	copy(frame, fb.frame)
	fb.mu.RUnlock()
	return frame, seq, nil
}

// Close releases any blocked waiters and prevents future WaitForFrame calls
// from blocking. After Close, WaitForFrame returns errBufferClosed.
func (fb *FrameBuffer) Close() {
	fb.mu.Lock()
	fb.closed = true
	close(fb.notify)
	fb.mu.Unlock()
}

// CameraManager maintains one persistent TLS connection per camera
// and buffers the latest frame for instant delivery to browser clients.
type CameraManager struct {
	mu      sync.Mutex
	cameras map[string]*cameraEntry
	ctx     context.Context
	cancel  context.CancelFunc
}

type cameraEntry struct {
	buffer *FrameBuffer
	cancel context.CancelFunc
}

// NewCameraManager creates a new CameraManager. The provided context
// controls the lifetime of all background connections — when it is
// cancelled, all connection loops exit.
func NewCameraManager(ctx context.Context) *CameraManager {
	ctx, cancel := context.WithCancel(ctx)
	return &CameraManager{
		cameras: make(map[string]*cameraEntry),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// GetBuffer returns a FrameBuffer for the given camera, starting a
// background connection if one isn't already running. The key is
// "host:port" which uniquely identifies the camera endpoint.
func (m *CameraManager) GetBuffer(host string, port int, accessCode string) *FrameBuffer {
	key := net.JoinHostPort(host, strconv.Itoa(port))

	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.cameras[key]; ok {
		return entry.buffer
	}

	buffer := NewFrameBuffer()
	ctx, cancel := context.WithCancel(m.ctx)
	entry := &cameraEntry{
		buffer: buffer,
		cancel: cancel,
	}
	m.cameras[key] = entry

	// Start background connection loop.
	go m.connectionLoop(ctx, host, port, accessCode, buffer)

	return buffer
}

// connectionLoop maintains a persistent connection to the camera.
// It reconnects with exponential backoff on failure.
func (m *CameraManager) connectionLoop(ctx context.Context, host string, port int, accessCode string, buffer *FrameBuffer) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("bambu camera: connecting to %s:%d", host, port)
		reader, err := NewBambuStreamReader(ctx, host, port, accessCode)
		if err != nil {
			log.Printf("bambu camera: connection failed for %s:%d: %v (retry in %v)", host, port, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		log.Printf("bambu camera: connected to %s:%d", host, port)
		backoff = 1 * time.Second // reset on successful connection

		// Read frames continuously.
		for {
			frame, err := reader.ReadFrame()
			if err != nil {
				if ctx.Err() != nil {
					reader.Close()
					return
				}
				log.Printf("bambu camera: read error for %s:%d: %v", host, port, err)
				reader.Close()
				break // exit inner loop to reconnect
			}

			buffer.Update(frame)
		}
	}
}

// Stop terminates all background connections.
func (m *CameraManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.cameras {
		entry.cancel()
	}
	m.cancel()
}
