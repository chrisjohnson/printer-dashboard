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
}

type go2rtcAPIConfig struct {
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
	return &Go2RTCManager{
		binaryPath: binaryPath,
		basePort:   basePort,
		instances:  make(map[string]*go2rtcInstance),
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

	// Allocate the next available port.
	port := m.basePort + len(m.instances)

	m.mu.Unlock()

	// Build the YAML config.
	cfg := go2rtcConfig{
		Streams: map[string]string{
			streamKey: rtspsURL,
		},
		API: go2rtcAPIConfig{
			Listen: fmt.Sprintf("127.0.0.1:%d", port),
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

	// Derive a cancellable context for this instance. Cancelling it sends
	// SIGKILL via exec.CommandContext and releases resources.
	instanceCtx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(instanceCtx, m.binaryPath, "-c", configPath)
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

	// Wait up to 5 seconds for go2rtc's HTTP API to become responsive.
	ready, err := m.waitForReady(ctx, inst, 5*time.Second)
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
		return "", fmt.Errorf("go2rtc: stream %s did not become ready within 5s", streamKey)
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

// waitForReady polls the go2rtc /api/frames endpoint until it responds with
// 200 OK, the context is cancelled, or the timeout expires.
func (m *Go2RTCManager) waitForReady(ctx context.Context, inst *go2rtcInstance, timeout time.Duration) (bool, error) {
	pollCtx, pollCancel := context.WithTimeout(ctx, timeout)
	defer pollCancel()

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/api/frames", inst.localPort)

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
// health-check request on its /api/frames endpoint.
func (m *Go2RTCManager) Healthy(streamKey string) bool {
	m.mu.Lock()
	inst, ok := m.instances[streamKey]
	m.mu.Unlock()
	if !ok {
		return false
	}

	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/frames", inst.localPort)

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
	return fmt.Sprintf("http://127.0.0.1:%d/api/frame.jpeg?src=%s", inst.localPort, streamKey), true
}

// mjpegURL returns the MJPEG stream URL for this go2rtc instance.
func (inst *go2rtcInstance) mjpegURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/api/stream.mjpeg?src=%s", inst.localPort, inst.streamKey)
}
