package camera

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Fake go2rtc binary (compiled once and reused across tests)
// ---------------------------------------------------------------------------

// fakeGo2RTCBin stores the path to the compiled fake go2rtc binary.
var fakeGo2RTCBin string

// TestMain compiles the fake go2rtc binary once before running tests and
// cleans it up afterwards.
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "go2rtc-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "go2rtc test: create temp dir: %v\n", err)
		os.Exit(1)
	}

	fakeGo2RTCBin = filepath.Join(tmpDir, "fakego2rtc")
	if err := compileFakeGo2RTC(fakeGo2RTCBin); err != nil {
		fmt.Fprintf(os.Stderr, "go2rtc test: compile fake binary: %v\n", err)
		os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(tmpDir)
	os.Exit(code)
}

const fakeGo2RTCSrc = `package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// findListenAddr extracts the listen address from a simple YAML config
// produced by the Go2RTCManager (line-based, no YAML dependency needed).
func findListenAddr(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "listen:") {
			val := strings.TrimSpace(line[7:])
			val = strings.Trim(val, "\"")
			return val
		}
	}
	return ""
}

func main() {
	configPath := flag.String("c", "", "config path")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "fakego2rtc: missing -c flag")
		os.Exit(1)
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakego2rtc: read config: %v\n", err)
		os.Exit(1)
	}

	addr := findListenAddr(data)
	if addr == "" {
		fmt.Fprintln(os.Stderr, "fakego2rtc: could not find listen address in config")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/frames", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"count": 1})
	})
	mux.HandleFunc("/api/stream.mjpeg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Write([]byte("--frame\r\nContent-Type: image/jpeg\r\n\r\nfake-jpeg\r\n--frame--"))
	})
	mux.HandleFunc("/api/frame.jpeg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte{0xFF, 0xD8, 0xFF})
	})

	server := &http.Server{Addr: addr, Handler: mux}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		server.Close()
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "fakego2rtc: server error: %v\n", err)
		os.Exit(1)
	}
}
`

// compileFakeGo2RTC compiles the fake go2rtc binary to the given path.
// It finds the module root so that 'go build' can resolve any imports
// (currently none beyond stdlib, but this keeps the approach extensible).
func compileFakeGo2RTC(binPath string) error {
	modRoot, err := findModuleRoot()
	if err != nil {
		return fmt.Errorf("find module root: %w", err)
	}

	srcPath := binPath + ".go"
	if err := os.WriteFile(srcPath, []byte(fakeGo2RTCSrc), 0644); err != nil {
		return fmt.Errorf("write source: %w", err)
	}

	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	cmd.Dir = modRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, out)
	}

	return nil
}

// findModuleRoot walks up from the current working directory to find go.mod.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestGo2RTCNew_Defaults(t *testing.T) {
	m := NewGo2RTCManager("", 0)
	if m.binaryPath != "go2rtc" {
		t.Errorf("binaryPath = %q; want %q", m.binaryPath, "go2rtc")
	}
	if m.basePort != 1984 {
		t.Errorf("basePort = %d; want %d", m.basePort, 1984)
	}
	if m.instances == nil {
		t.Error("instances map should be initialized")
	}
}

func TestGo2RTCNew_Custom(t *testing.T) {
	m := NewGo2RTCManager("/usr/local/bin/go2rtc", 3000)
	if m.binaryPath != "/usr/local/bin/go2rtc" {
		t.Errorf("binaryPath = %q; want %q", m.binaryPath, "/usr/local/bin/go2rtc")
	}
	if m.basePort != 3000 {
		t.Errorf("basePort = %d; want %d", m.basePort, 3000)
	}
}

func TestGo2RTCStart_BinaryNotFound(t *testing.T) {
	m := NewGo2RTCManager("/nonexistent/go2rtc", 9000)
	ctx := context.Background()

	_, err := m.Start(ctx, "test", "rtsps://example.com/stream")
	if err == nil {
		t.Fatal("expected error when binary does not exist")
	}
}

func TestGo2RTCStart_FakeBinary(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9100)
	defer m.StopAll()

	ctx := context.Background()
	url, err := m.Start(ctx, "test", "rtsps://example.com/stream")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	wantPrefix := "http://127.0.0.1:9100/api/stream.mjpeg?src=test"
	if url != wantPrefix {
		t.Errorf("URL = %q; want %q", url, wantPrefix)
	}

	if !m.IsRunning("test") {
		t.Error("IsRunning should be true after Start")
	}

	if !m.Healthy("test") {
		t.Error("Healthy should be true after Start")
	}

	frameURL, ok := m.FrameURL("test")
	if !ok {
		t.Fatal("FrameURL returned false for running stream")
	}
	wantFrameURL := "http://127.0.0.1:9100/api/frame.jpeg?src=test"
	if frameURL != wantFrameURL {
		t.Errorf("FrameURL = %q; want %q", frameURL, wantFrameURL)
	}
}

func TestGo2RTCStart_Idempotent(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9200)
	defer m.StopAll()

	ctx := context.Background()

	url1, err := m.Start(ctx, "test", "rtsps://example.com/stream")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	url2, err := m.Start(ctx, "test", "rtsps://example.com/stream")
	if err != nil {
		t.Fatalf("second Start (idempotent): %v", err)
	}

	if url1 != url2 {
		t.Errorf("URLs differ between starts: %q vs %q", url1, url2)
	}

	// Should still have only one instance.
	if m.IsRunning("test") {
		// Verify FrameURL works.
		if _, ok := m.FrameURL("test"); !ok {
			t.Error("FrameURL should work after idempotent Start")
		}
	} else {
		t.Error("IsRunning should be true after idempotent Start")
	}
}

func TestGo2RTCStop(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9300)
	defer m.StopAll()

	ctx := context.Background()
	_, err := m.Start(ctx, "test", "rtsps://example.com/stream")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !m.IsRunning("test") {
		t.Error("IsRunning should be true before Stop")
	}

	if err := m.Stop("test"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if m.IsRunning("test") {
		t.Error("IsRunning should be false after Stop")
	}

	if m.Healthy("test") {
		t.Error("Healthy should be false after Stop")
	}

	if _, ok := m.FrameURL("test"); ok {
		t.Error("FrameURL should return false after Stop")
	}
}

func TestGo2RTCStop_NotFound(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9400)
	err := m.Stop("nonexistent")
	if err == nil {
		t.Fatal("expected error when stopping nonexistent stream")
	}
}

func TestGo2RTCStopAll(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9500)

	ctx := context.Background()
	_, err := m.Start(ctx, "stream1", "rtsps://example.com/1")
	if err != nil {
		t.Fatalf("Start stream1: %v", err)
	}
	_, err = m.Start(ctx, "stream2", "rtsps://example.com/2")
	if err != nil {
		t.Fatalf("Start stream2: %v", err)
	}

	m.StopAll()

	if m.IsRunning("stream1") {
		t.Error("stream1 should be stopped after StopAll")
	}
	if m.IsRunning("stream2") {
		t.Error("stream2 should be stopped after StopAll")
	}
}

func TestGo2RTCIsRunning(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9600)

	if m.IsRunning("nonexistent") {
		t.Error("IsRunning should be false for nonexistent stream")
	}

	// Direct map insertion to test IsRunning without a subprocess.
	m.mu.Lock()
	m.instances["test"] = &go2rtcInstance{
		streamKey: "test",
		localPort: 9600,
	}
	m.mu.Unlock()

	if !m.IsRunning("test") {
		t.Error("IsRunning should be true after adding instance")
	}
}

func TestGo2RTCFrameURL(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9700)

	// Nonexistent stream.
	if _, ok := m.FrameURL("nonexistent"); ok {
		t.Error("FrameURL should return false for nonexistent stream")
	}

	// Insert a test instance directly.
	m.mu.Lock()
	m.instances["test"] = &go2rtcInstance{
		streamKey: "test",
		localPort: 9700,
	}
	m.mu.Unlock()

	url, ok := m.FrameURL("test")
	if !ok {
		t.Fatal("FrameURL should return true for existing stream")
	}
	want := "http://127.0.0.1:9700/api/frame.jpeg?src=test"
	if url != want {
		t.Errorf("FrameURL = %q; want %q", url, want)
	}
}

func TestGo2RTCHealthy_NotRunning(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9800)

	if m.Healthy("nonexistent") {
		t.Error("Healthy should return false for nonexistent stream")
	}
}

func TestGo2RTCConcurrency(t *testing.T) {
	m := NewGo2RTCManager(fakeGo2RTCBin, 9900)
	defer m.StopAll()

	ctx := context.Background()

	// Start a stream.
	url1, err := m.Start(ctx, "concurrent", "rtsps://example.com/stream")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Concurrent idempotent calls should all succeed and return the same URL.
	done := make(chan string, 5)
	for i := 0; i < 5; i++ {
		go func() {
			u, err := m.Start(ctx, "concurrent", "rtsps://example.com/stream")
			if err != nil {
				done <- fmt.Sprintf("error: %v", err)
				return
			}
			done <- u
		}()
	}

	for i := 0; i < 5; i++ {
		select {
		case result := <-done:
			if result != url1 && !stringsHasPrefix(result, "error:") {
				t.Errorf("concurrent Start returned different URL: %q", result)
			}
			if stringsHasPrefix(result, "error:") {
				t.Errorf("concurrent Start failed: %s", result)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for concurrent Starts")
		}
	}
}

// stringsHasPrefix is a small helper to avoid importing strings just for one call.
func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
