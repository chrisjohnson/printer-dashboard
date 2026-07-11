package tutk

/*
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

// BambuTunnel types (from Bambu Studio's BambuTunnel.h)
typedef void* Bambu_Tunnel;

typedef enum {
    VIDE,
    AUDI
} Bambu_StreamType;

typedef enum {
    AVC1,
    MJPG,
} Bambu_VideoSubType;

typedef enum {
    video_avc_packet,
    video_avc_byte_stream,
    video_jpeg,
    audio_raw,
    audio_adts
} Bambu_FormatType;

typedef struct {
    int type;
    int sub_type;
    union {
        struct { int width; int height; int frame_rate; } video;
        struct { int sample_rate; int channel_count; int sample_size; } audio;
    } format;
    int format_type;
    int format_size;
    int max_frame_size;
    unsigned char const* format_buffer;
} Bambu_StreamInfo;

typedef struct {
    int itrack;
    int size;
    int flags;
    unsigned char const* buffer;
    unsigned long long decode_time;
} Bambu_Sample;

typedef enum {
    Bambu_success = 0,
    Bambu_stream_end = -1,
    Bambu_would_block = -2,
    Bambu_buffer_limit = -3
} Bambu_Error;

// Function pointer types for dynamically loaded library
typedef int (*bambu_init_fn)(void);
typedef void (*bambu_deinit_fn)(void);
typedef int (*bambu_create_fn)(Bambu_Tunnel*, char const*);
typedef int (*bambu_open_fn)(Bambu_Tunnel);
typedef void (*bambu_close_fn)(Bambu_Tunnel);
typedef void (*bambu_destroy_fn)(Bambu_Tunnel);
typedef int (*bambu_start_stream_fn)(Bambu_Tunnel, int);
typedef int (*bambu_get_stream_count_fn)(Bambu_Tunnel);
typedef int (*bambu_get_stream_info_fn)(Bambu_Tunnel, int, Bambu_StreamInfo*);
typedef int (*bambu_read_sample_fn)(Bambu_Tunnel, Bambu_Sample*);
typedef char const* (*bambu_get_last_error_fn)(void);
typedef void (*bambu_free_log_msg_fn)(char const*);

// Global function pointers populated by loadLibrary
static bambu_init_fn             pBambu_Init = NULL;
static bambu_deinit_fn           pBambu_Deinit = NULL;
static bambu_create_fn           pBambu_Create = NULL;
static bambu_open_fn             pBambu_Open = NULL;
static bambu_close_fn            pBambu_Close = NULL;
static bambu_destroy_fn          pBambu_Destroy = NULL;
static bambu_start_stream_fn     pBambu_StartStream = NULL;
static bambu_get_stream_count_fn pBambu_GetStreamCount = NULL;
static bambu_get_stream_info_fn  pBambu_GetStreamInfo = NULL;
static bambu_read_sample_fn      pBambu_ReadSample = NULL;
static bambu_get_last_error_fn   pBambu_GetLastErrorMsg = NULL;
static bambu_free_log_msg_fn     pBambu_FreeLogMsg = NULL;

// loadBambuLibrary opens libBambuSource.so via dlopen and resolves all symbols.
// Returns 0 on success, -1 with seterr on failure.
static int loadBambuLibrary(const char* libPath) {
    void* handle = dlopen(libPath, RTLD_LAZY | RTLD_LOCAL);
    if (!handle) {
        return -1;
    }

    pBambu_Init = (bambu_init_fn)dlsym(handle, "Bambu_Init");
    pBambu_Deinit = (bambu_deinit_fn)dlsym(handle, "Bambu_Deinit");
    pBambu_Create = (bambu_create_fn)dlsym(handle, "Bambu_Create");
    pBambu_Open = (bambu_open_fn)dlsym(handle, "Bambu_Open");
    pBambu_Close = (bambu_close_fn)dlsym(handle, "Bambu_Close");
    pBambu_Destroy = (bambu_destroy_fn)dlsym(handle, "Bambu_Destroy");
    pBambu_StartStream = (bambu_start_stream_fn)dlsym(handle, "Bambu_StartStream");
    pBambu_GetStreamCount = (bambu_get_stream_count_fn)dlsym(handle, "Bambu_GetStreamCount");
    pBambu_GetStreamInfo = (bambu_get_stream_info_fn)dlsym(handle, "Bambu_GetStreamInfo");
    pBambu_ReadSample = (bambu_read_sample_fn)dlsym(handle, "Bambu_ReadSample");
    pBambu_GetLastErrorMsg = (bambu_get_last_error_fn)dlsym(handle, "Bambu_GetLastErrorMsg");
    pBambu_FreeLogMsg = (bambu_free_log_msg_fn)dlsym(handle, "Bambu_FreeLogMsg");

    if (!pBambu_Init || !pBambu_Deinit || !pBambu_Create || !pBambu_Open ||
        !pBambu_Close || !pBambu_Destroy || !pBambu_StartStream ||
        !pBambu_GetStreamCount || !pBambu_GetStreamInfo || !pBambu_ReadSample) {
        dlclose(handle);
        return -1;
    }

    return 0;
}

// Wrapper functions that call through function pointers
static int Bambu_Init_wrap(void) { return pBambu_Init(); }
static void Bambu_Deinit_wrap(void) { pBambu_Deinit(); }
static int Bambu_Create_wrap(Bambu_Tunnel* t, char const* p) { return pBambu_Create(t, p); }
static int Bambu_Open_wrap(Bambu_Tunnel t) { return pBambu_Open(t); }
static void Bambu_Close_wrap(Bambu_Tunnel t) { pBambu_Close(t); }
static void Bambu_Destroy_wrap(Bambu_Tunnel t) { pBambu_Destroy(t); }
static int Bambu_StartStream_wrap(Bambu_Tunnel t, int v) { return pBambu_StartStream(t, v); }
static int Bambu_GetStreamCount_wrap(Bambu_Tunnel t) { return pBambu_GetStreamCount(t); }
static int Bambu_GetStreamInfo_wrap(Bambu_Tunnel t, int i, Bambu_StreamInfo* info) { return pBambu_GetStreamInfo(t, i, info); }
static int Bambu_ReadSample_wrap(Bambu_Tunnel t, Bambu_Sample* s) { return pBambu_ReadSample(t, s); }
static char const* Bambu_GetLastErrorMsg_wrap(void) { return pBambu_GetLastErrorMsg(); }
static void Bambu_FreeLogMsg_wrap(char const* m) { pBambu_FreeLogMsg(m); }
*/
import "C"
import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
	"unsafe"
)

// Package-level errors for C API failures.
var (
	ErrWouldBlock       = fmt.Errorf("tutk: read would block after retries")
	ErrSessionClosed    = fmt.Errorf("tutk: session closed")
	ErrOpenTimeout      = fmt.Errorf("tutk: open timed out")
	ErrNoVideoStream    = fmt.Errorf("tutk: no video stream found")
	ErrLibraryNotLoaded = fmt.Errorf("tutk: libBambuSource.so not loaded — call LoadLibrary first")
)

// Retry constants.
const (
	maxReadSampleRetries  = 50
	readSampleRetrySleep  = 10 * time.Millisecond
	startStreamRetries    = 50
	startStreamRetrySleep = 100 * time.Millisecond
	openTimeout           = 30 * time.Second // max time for P2P handshake
)

// Default library search paths (tried in order).
var defaultLibPaths = []string{
	"libBambuSource.so",                             // CWD / LD_LIBRARY_PATH
	"/usr/local/lib/libBambuSource.so",              // Docker bundle path
	"/app/libBambuSource.so",                        // App directory
}

var (
	libraryLoaded bool
	libraryLoadMu sync.Mutex
	libraryLoadErr error
)

// LoadLibrary loads libBambuSource.so from the given path and resolves all
// function pointers. If path is empty, searches default locations.
// Safe to call multiple times — subsequent calls are no-ops on success.
func LoadLibrary(path string) error {
	libraryLoadMu.Lock()
	defer libraryLoadMu.Unlock()

	if libraryLoaded {
		return nil
	}

	paths := []string{path}
	if path == "" {
		paths = defaultLibPaths
	}

	for _, p := range paths {
		cPath := C.CString(p)
		ret := C.loadBambuLibrary(cPath)
		C.free(unsafe.Pointer(cPath))
		if ret == 0 {
			libraryLoaded = true
			log.Printf("[tutk] loaded libBambuSource.so from %s", p)
			return nil
		}
	}

	libraryLoadErr = fmt.Errorf("tutk: could not load libBambuSource.so from any location (tried: %v)", paths)
	return libraryLoadErr
}

// Package-level init state for Bambu_Init.
var (
	bambuInitOnce sync.Once
	bambuInitErr  error
)

// bambuInit calls Bambu_Init exactly once. Safe for concurrent use.
func bambuInit() error {
	if !libraryLoaded {
		return ErrLibraryNotLoaded
	}
	bambuInitOnce.Do(func() {
		ret := C.Bambu_Init_wrap()
		if ret != 0 {
			bambuInitErr = fmt.Errorf("Bambu_Init failed: %d", int(ret))
		}
	})
	return bambuInitErr
}

// Session manages the lifecycle of a single TUTK P2P camera connection.
//
// ReadSample and Open are NOT safe for concurrent use — call from a single
// goroutine. Close() IS safe for concurrent use (guarded by mutex).
type Session struct {
	creds  Credentials
	tunnel C.Bambu_Tunnel
	mu     sync.Mutex
	closed bool
}

// NewSession creates a new TUTK session from credentials.
// Requires LoadLibrary to have been called first.
func NewSession(creds Credentials) (*Session, error) {
	if err := bambuInit(); err != nil {
		return nil, err
	}
	return &Session{creds: creds}, nil
}

// Open connects to the camera via TUTK P2P. Blocks until connected or context cancelled.
func (s *Session) Open(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	s.mu.Unlock()

	url := BuildURL(s.creds)
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))

	var tunnel C.Bambu_Tunnel
	ret := C.Bambu_Create_wrap(&tunnel, cURL)
	if ret != 0 {
		return lastError("Bambu_Create", int(ret))
	}

	// Open with context-aware timeout via goroutine.
	errCh := make(chan error, 1)
	go func() {
		ret := C.Bambu_Open_wrap(tunnel)
		if ret != 0 {
			errCh <- lastError("Bambu_Open", int(ret))
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			C.Bambu_Destroy_wrap(tunnel)
			return err
		}
	case <-ctx.Done():
		// Context cancelled — close the tunnel to unblock Bambu_Open.
		go C.Bambu_Close_wrap(tunnel)
		<-errCh
		C.Bambu_Destroy_wrap(tunnel)
		return ctx.Err()
	}

	s.tunnel = tunnel

	// Start stream with retry loop for would_block.
	if err := s.startStream(tunnel); err != nil {
		C.Bambu_Close_wrap(tunnel)
		C.Bambu_Destroy_wrap(tunnel)
		s.tunnel = nil
		return err
	}

	// Verify we have at least one video stream.
	streamCount := int(C.Bambu_GetStreamCount_wrap(tunnel))
	if streamCount < 1 {
		C.Bambu_Close_wrap(tunnel)
		C.Bambu_Destroy_wrap(tunnel)
		s.tunnel = nil
		return ErrNoVideoStream
	}

	// Log stream info for diagnostics.
	var info C.Bambu_StreamInfo
	if ret := C.Bambu_GetStreamInfo_wrap(tunnel, 1, &info); ret == 0 {
		log.Printf("[tutk] stream: type=%d sub_type=%d width=%d height=%d fps=%d",
			int(info.type), int(info.sub_type),
			int(info.format.video.width), int(info.format.video.height),
			int(info.format.video.frame_rate))
	}

	return nil
}

// startStream calls Bambu_StartStream with retry logic for would_block.
func (s *Session) startStream(tunnel C.Bambu_Tunnel) error {
	for retries := 0; retries < startStreamRetries; retries++ {
		ret := C.Bambu_StartStream_wrap(tunnel, 1)
		if ret == 0 {
			return nil
		}
		if int(ret) == int(C.Bambu_would_block) {
			C.usleep(C.useconds_t(startStreamRetrySleep / time.Microsecond))
			continue
		}
		return lastError("Bambu_StartStream", int(ret))
	}
	return fmt.Errorf("Bambu_StartStream: would_block after %d retries", startStreamRetries)
}

// ReadSample reads the next JPEG frame. Blocks until a frame arrives or error.
func (s *Session) ReadSample() ([]byte, error) {
	s.mu.Lock()
	if s.closed || s.tunnel == nil {
		s.mu.Unlock()
		return nil, ErrSessionClosed
	}
	tunnel := s.tunnel
	s.mu.Unlock()

	var sample C.Bambu_Sample
	for retries := 0; retries < maxReadSampleRetries; retries++ {
		ret := C.Bambu_ReadSample_wrap(tunnel, &sample)
		if ret == 0 {
			frame := C.GoBytes(unsafe.Pointer(sample.buffer), C.int(sample.size))
			return frame, nil
		}
		if int(ret) == int(C.Bambu_would_block) {
			C.usleep(C.useconds_t(readSampleRetrySleep / time.Microsecond))
			continue
		}
		if int(ret) == int(C.Bambu_stream_end) {
			return nil, fmt.Errorf("tutk: stream ended")
		}
		return nil, lastError("Bambu_ReadSample", int(ret))
	}
	return nil, ErrWouldBlock
}

// Close tears down the TUTK connection. Idempotent and thread-safe.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.tunnel != nil {
		C.Bambu_Close_wrap(s.tunnel)
		C.Bambu_Destroy_wrap(s.tunnel)
		s.tunnel = nil
	}
	return nil
}

// StreamInfo contains basic info about the connected stream (for logging).
type StreamInfo struct {
	Width     int
	Height    int
	FrameRate int
	Codec     string // "MJPG" or "AVC1"
}

// lastError returns a formatted error that includes the C library's error message.
func lastError(op string, code int) error {
	msg := C.Bambu_GetLastErrorMsg_wrap()
	if msg != nil {
		extra := C.GoString(msg)
		C.Bambu_FreeLogMsg_wrap(msg)
		return fmt.Errorf("%s failed (code=%d): %s", op, code, extra)
	}
	return fmt.Errorf("%s failed (code=%d)", op, code)
}
