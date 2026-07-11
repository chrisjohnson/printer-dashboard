package camera

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Go2RTCManager manages go2rtc subprocess instances for proxying RTSPS camera streams.
// Each RTSPS stream gets its own go2rtc instance that exposes the stream as HTTP MJPEG.
type Go2RTCManager struct {
	binaryPath string
	basePort   int
	mu         sync.Mutex
	instances  map[string]*go2rtcInstance
	nextOffset int // monotonic port-allocation counter, incremented under mu

	// rootCtx is the parent context for every spawned subprocess. It must
	// NOT be a caller's per-HTTP-request context: Start is called from
	// request handlers (via r.Context()), and a stream is meant to keep
	// running (shared, cached by streamKey) after that one request ends.
	// Only rootCtx's cancellation (StopAll/app shutdown) or an explicit
	// Stop should kill an instance.
	rootCtx    context.Context
	rootCancel context.CancelFunc
}

// go2rtcInstance represents a single running go2rtc subprocess.
type go2rtcInstance struct {
	streamKey  string
	rtspsURL   string
	localPort  int
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	startedAt  time.Time
	configPath string
}

// go2rtcConfig is the YAML configuration written for each go2rtc instance.
type go2rtcConfig struct {
	Streams map[string]string `yaml:"streams"`
	API     go2rtcAPIConfig   `yaml:"api"`
	RTSP    go2rtcRTSPConfig  `yaml:"rtsp"`
}

type go2rtcAPIConfig struct {
	Listen string `yaml:"listen"`
}

// go2rtcRTSPConfig sets a per-instance RTSP listen port. Each go2rtc
// subprocess must get its own port: go2rtc's "ffmpeg:" pseudo-source (used
// to transcode H264 to MJPEG) pulls the source stream back from go2rtc's own
// RTSP listener, so if two instances both default to :8554 the second one's
// bind fails and its ffmpeg transcode is left with no loopback URL to read.
type go2rtcRTSPConfig struct {
	Listen string `yaml:"listen"`
}

// NewGo2RTCManager creates a new Go2RTCManager.
// binaryPath defaults to "go2rtc" if empty.
// basePort defaults to 1984 if <= 0.
func NewGo2RTCManager(binaryPath string, basePort int) *Go2RTCManager {
	if binaryPath == "" {
		binaryPath = "go2rtc"
	}
	if basePort <= 0 {
		basePort = 1984
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &Go2RTCManager{
		binaryPath: binaryPath,
		basePort:   basePort,
		instances:  make(map[string]*go2rtcInstance),
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
	}
}

// Start launches a go2rtc subprocess that proxies the given RTSPS URL to a
// local HTTP MJPEG endpoint.
//
// streamKey is a unique identifier for the stream (used for stop/lookup).
// If the streamKey is already running, Start returns the existing URL (idempotent).
//
// Returns the local HTTP MJPEG URL (e.g.
// "http://127.0.0.1:1985/api/stream.mjpeg?src=streamKey") and any error encountered.
func (m *Go2RTCManager) Start(ctx context.Context, streamKey, rtspsURL string) (string, error) {
	m.mu.Lock()

	// Idempotent: if already running, return existing URL.
	if inst, ok := m.instances[streamKey]; ok {
		m.mu.Unlock()
		return inst.mjpegURL(), nil
	}

	// Allocate the next available ports from a monotonic counter (not
	// len(m.instances)): two concurrent Start calls for different new
	// stream keys could otherwise both read the same len() before either
	// is registered and collide on the same ports. rtspPort uses its own
	// range, well clear of go2rtc's other default ports (8554 RTSP, 8555
	// WebRTC), so each instance gets its own RTSP listener too.
	offset := m.nextOffset
	m.nextOffset++
	port := m.basePort + offset
	rtspPort := 18554 + offset

	m.mu.Unlock()

	// go2rtc's MJPEG endpoint requires a JPEG/RAW source; the camera's raw
	// stream is H264, so declare a second virtual stream that transcodes it
	// via ffmpeg (go2rtc's built-in "ffmpeg:" pseudo-source, which pulls the
	// named stream back from go2rtc's own RTSP listener).
	mjpegKey := streamKey + "_mjpeg"

	// Build the YAML config.
	cfg := go2rtcConfig{
		Streams: map[string]string{
			streamKey: rtspsURL,
			mjpegKey:  fmt.Sprintf("ffmpeg:%s#video=mjpeg", streamKey),
		},
		API: go2rtcAPIConfig{
			Listen: fmt.Sprintf("127.0.0.1:%d", port),
		},
		RTSP: go2rtcRTSPConfig{
			Listen: fmt.Sprintf("127.0.0.1:%d", rtspPort),
		},
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return "", fmt.Errorf("go2rtc: marshal config: %w", err)
	}

	// Write config to a temp file.
	tmpFile, err := os.CreateTemp("", "go2rtc-*.yaml")
	if err != nil {
		return "", fmt.Errorf("go2rtc: create temp file: %w", err)
	}
	configPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(configPath)
		return "", fmt.Errorf("go2rtc: write config: %w", err)
	}
	tmpFile.Close()

	// Derive a cancellable context for this instance from the manager's
	// long-lived rootCtx (NOT the caller's ctx, which may be a per-HTTP-
	// request context that gets cancelled as soon as that one request
	// ends). Cancelling it sends SIGKILL via exec.CommandContext.
	instanceCtx, cancel := context.WithCancel(m.rootCtx)

	cmd := exec.CommandContext(instanceCtx, m.binaryPath, "-c", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		os.Remove(configPath)
		return "", fmt.Errorf("go2rtc: start process for stream %s: %w", streamKey, err)
	}

	inst := &go2rtcInstance{
		streamKey:  streamKey,
		rtspsURL:   rtspsURL,
		localPort:  port,
		cmd:        cmd,
		cancel:     cancel,
		startedAt:  time.Now(),
		configPath: configPath,
	}

	// Wait up to 10 seconds for go2rtc's HTTP API to become responsive.
	ready, err := m.waitForReady(ctx, inst, 10*time.Second)
	if err != nil {
		cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		cmd.Process.Kill()
		cancel()
		os.Remove(configPath)
		return "", fmt.Errorf("go2rtc: wait for stream %s: %w", streamKey, err)
	}
	if !ready {
		cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		cmd.Process.Kill()
		cancel()
		os.Remove(configPath)
		return "", fmt.Errorf("go2rtc: stream %s did not become ready within 10s", streamKey)
	}

	// Store the instance. Double-check in case another goroutine started the
	// same key (or a concurrent Start for a different key added an instance
	// and shifted ports) while we were working.
	m.mu.Lock()
	if existing, ok := m.instances[streamKey]; ok {
		m.mu.Unlock()
		// Another instance was already added — shut this one down.
		cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		cmd.Process.Kill()
		cancel()
		os.Remove(configPath)
		return existing.mjpegURL(), nil
	}
	m.instances[streamKey] = inst
	m.mu.Unlock()

	return inst.mjpegURL(), nil
}

// waitForReady polls the go2rtc /api/streams endpoint until it responds with
// 200 OK, the context is cancelled, or the timeout expires.
func (m *Go2RTCManager) waitForReady(ctx context.Context, inst *go2rtcInstance, timeout time.Duration) (bool, error) {
	pollCtx, pollCancel := context.WithTimeout(ctx, timeout)
	defer pollCancel()

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/api/streams", inst.localPort)

	for {
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil // timed out
		default:
		}

		req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, url, nil)
		if err != nil {
			return false, err
		}

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true, nil
			}
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// Stop terminates a running go2rtc instance by streamKey.
// It sends SIGTERM and waits up to 5 seconds for graceful shutdown,
// then force-kills if necessary.  Returns an error if streamKey is not found.
func (m *Go2RTCManager) Stop(streamKey string) error {
	m.mu.Lock()
	inst, ok := m.instances[streamKey]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("go2rtc: stream %s not found", streamKey)
	}
	delete(m.instances, streamKey)
	m.mu.Unlock()

	// Send SIGTERM for graceful shutdown.
	if err := inst.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("go2rtc: signal SIGTERM to stream %s: %v", streamKey, err)
	}

	// Wait up to 5 seconds.
	done := make(chan struct{})
	go func() {
		inst.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Graceful shutdown completed.
	case <-time.After(5 * time.Second):
		log.Printf("go2rtc: stream %s did not stop gracefully, sending SIGKILL", streamKey)
		inst.cancel() // triggers exec.CommandContext's SIGKILL
		<-done
	}

	// Ensure context is cancelled (no-op if already cancelled).
	inst.cancel()

	// Clean up the temp config file.
	if err := os.Remove(inst.configPath); err != nil && !os.IsNotExist(err) {
		log.Printf("go2rtc: remove config for stream %s: %v", streamKey, err)
	}

	return nil
}

// StopAll terminates all running go2rtc instances.
func (m *Go2RTCManager) StopAll() {
	m.mu.Lock()
	keys := make([]string, 0, len(m.instances))
	for k := range m.instances {
		keys = append(keys, k)
	}
	m.mu.Unlock()

	for _, key := range keys {
		if err := m.Stop(key); err != nil {
			log.Printf("go2rtc: stop stream %s: %v", key, err)
		}
	}
}

// IsRunning returns true if a go2rtc instance is registered for the given streamKey.
func (m *Go2RTCManager) IsRunning(streamKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.instances[streamKey]
	return ok
}

// Healthy returns true if the go2rtc instance for streamKey responds to a
// health-check request on its /api/streams endpoint.
func (m *Go2RTCManager) Healthy(streamKey string) bool {
	m.mu.Lock()
	inst, ok := m.instances[streamKey]
	m.mu.Unlock()
	if !ok {
		return false
	}

	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/streams", inst.localPort)

	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// FrameURL returns the single-frame snapshot URL for a running stream.
// The bool is false if the stream is not running.
func (m *Go2RTCManager) FrameURL(streamKey string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[streamKey]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("http://127.0.0.1:%d/api/frame.jpeg?src=%s", inst.localPort, streamKey+"_mjpeg"), true
}

// mjpegURL returns the MJPEG stream URL for this go2rtc instance.
//
// go2rtc's MJPEG/snapshot endpoints require a JPEG/RAW source, but camera
// streams (e.g. H2S) are H264, so this targets the "<streamKey>_mjpeg"
// virtual stream that Start configures to transcode via ffmpeg.
func (inst *go2rtcInstance) mjpegURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/api/stream.mjpeg?src=%s", inst.localPort, inst.streamKey+"_mjpeg")
}
