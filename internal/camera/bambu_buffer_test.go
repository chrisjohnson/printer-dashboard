package camera

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestFrameBuffer_LatestNilBeforeAnyFrame(t *testing.T) {
	fb := NewFrameBuffer()
	if got := fb.Latest(); got != nil {
		t.Errorf("Latest() = %v; want nil", got)
	}
}

func TestFrameBuffer_UpdateAndLatest(t *testing.T) {
	fb := NewFrameBuffer()

	frame := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	fb.Update(frame)

	got := fb.Latest()
	if got == nil {
		t.Fatal("Latest() returned nil after Update")
	}
	if len(got) != len(frame) {
		t.Errorf("Latest() len = %d; want %d", len(got), len(frame))
	}
	for i := range frame {
		if got[i] != frame[i] {
			t.Errorf("Latest()[%d] = 0x%x; want 0x%x", i, got[i], frame[i])
		}
	}
}

func TestFrameBuffer_LatestReturnsCopy(t *testing.T) {
	fb := NewFrameBuffer()
	frame := []byte{0x01, 0x02, 0x03}
	fb.Update(frame)

	got1 := fb.Latest()
	got2 := fb.Latest()

	// Modify got1 and verify got2 is unaffected.
	got1[0] = 0xFF
	if got2[0] == 0xFF {
		t.Error("Latest() returned the same backing array — must return a copy")
	}
}

func TestFrameBuffer_UpdateReturnsCopy(t *testing.T) {
	fb := NewFrameBuffer()
	frame := []byte{0x01, 0x02, 0x03}
	fb.Update(frame)

	// Mutate the original slice — the buffer should be unaffected.
	frame[0] = 0xFF
	got := fb.Latest()
	if got[0] == 0xFF {
		t.Error("Update() stored a reference instead of a copy")
	}
}

func TestFrameBuffer_UpdateOverwrites(t *testing.T) {
	fb := NewFrameBuffer()
	fb.Update([]byte{0x01})
	fb.Update([]byte{0x02, 0x03})

	got := fb.Latest()
	if len(got) != 2 || got[0] != 0x02 || got[1] != 0x03 {
		t.Errorf("Latest() = %v; want [0x02 0x03]", got)
	}
}

func TestFrameBuffer_WaitForFrameBlocksUntilUpdate(t *testing.T) {
	fb := NewFrameBuffer()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got []byte
	var seq uint64
	done := make(chan struct{})

	go func() {
		defer close(done)
		var err error
		got, seq, err = fb.WaitForFrame(ctx, 0)
		if err != nil {
			t.Errorf("WaitForFrame() error = %v", err)
		}
	}()

	// Give the goroutine time to start waiting.
	time.Sleep(50 * time.Millisecond)

	fb.Update([]byte{0xAA, 0xBB})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitForFrame did not return after Update")
	}

	if got == nil || len(got) != 2 || got[0] != 0xAA || got[1] != 0xBB {
		t.Errorf("WaitForFrame() = %v; want [0xAA 0xBB]", got)
	}
	if seq != 1 {
		t.Errorf("seq = %d; want 1", seq)
	}
}

func TestFrameBuffer_WaitForFrameContextCancellation(t *testing.T) {
	fb := NewFrameBuffer()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := fb.WaitForFrame(ctx, 0)
	if err == nil {
		t.Fatal("WaitForFrame() should return error on context cancellation")
	}
	if ctx.Err() == nil {
		t.Fatal("context.Err() should not be nil")
	}
}

func TestFrameBuffer_WaitForFrameSkipsOldFrames(t *testing.T) {
	fb := NewFrameBuffer()
	fb.Update([]byte{0x01}) // seq=1
	fb.Update([]byte{0x02}) // seq=2
	fb.Update([]byte{0x03}) // seq=3

	ctx := context.Background()

	// Ask for frames after seq=2 — should get seq=3 immediately.
	got, seq, err := fb.WaitForFrame(ctx, 2)
	if err != nil {
		t.Fatalf("WaitForFrame() error = %v", err)
	}
	if seq != 3 {
		t.Errorf("seq = %d; want 3", seq)
	}
	if len(got) != 1 || got[0] != 0x03 {
		t.Errorf("frame = %v; want [0x03]", got)
	}
}

func TestFrameBuffer_MultipleWaiters(t *testing.T) {
	fb := NewFrameBuffer()
	ctx := context.Background()

	const n = 5
	var wg sync.WaitGroup
	results := make([]uint64, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, seq, err := fb.WaitForFrame(ctx, 0)
			if err != nil {
				t.Errorf("goroutine %d: WaitForFrame() error = %v", idx, err)
			}
			results[idx] = seq
		}(i)
	}

	// Give goroutines time to start waiting.
	time.Sleep(50 * time.Millisecond)

	// Update so all goroutines wake up.
	fb.Update([]byte{0xFF})

	wg.Wait()

	for i, s := range results {
		if s != 1 {
			t.Errorf("goroutine %d: seq = %d; want 1", i, s)
		}
	}
}

func TestFrameBuffer_ConcurrentUpdates(t *testing.T) {
	fb := NewFrameBuffer()
	ctx := context.Background()

	// Writer goroutine: push 100 frames.
	const count = 100
	done := make(chan struct{})
	go func() {
		for i := 0; i < count; i++ {
			fb.Update([]byte{byte(i)})
		}
		close(done)
	}()

	// Reader goroutine: wait for the last frame.
	var gotSeq uint64
	readerDone := make(chan struct{})
	go func() {
		_, seq, err := fb.WaitForFrame(ctx, 0)
		if err != nil {
			t.Errorf("reader: WaitForFrame() error = %v", err)
		}
		gotSeq = seq
		close(readerDone)
	}()

	<-done
	<-readerDone

	// The reader should eventually see seq == count (the latest frame).
	// Due to the broadcast pattern, it may see any seq >= 1.
	if gotSeq < 1 || gotSeq > uint64(count) {
		t.Errorf("gotSeq = %d; want 1..%d", gotSeq, count)
	}
}

func TestFrameBuffer_Close(t *testing.T) {
	fb := NewFrameBuffer()
	ctx := context.Background()

	done := make(chan struct{})
	var waitErr error
	go func() {
		defer close(done)
		_, _, waitErr = fb.WaitForFrame(ctx, 0)
	}()

	time.Sleep(50 * time.Millisecond)
	fb.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close() did not unstick the waiter")
	}

	if waitErr != errBufferClosed {
		t.Errorf("WaitForFrame error after Close = %v; want %v", waitErr, errBufferClosed)
	}
}

func TestCameraManager_GetBufferIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewCameraManager(ctx)

	// GetBuffer should return the same FrameBuffer for the same key.
	buf1 := mgr.GetBuffer("192.168.1.100", 6000, "secret")
	buf2 := mgr.GetBuffer("192.168.1.100", 6000, "secret")

	if buf1 != buf2 {
		t.Error("GetBuffer returned different FrameBuffer for same key")
	}

	// Different port should be different buffer.
	buf3 := mgr.GetBuffer("192.168.1.100", 6001, "secret")
	if buf1 == buf3 {
		t.Error("GetBuffer returned same FrameBuffer for different port")
	}
}

func TestCameraManager_Stop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := NewCameraManager(ctx)

	// GetBuffer starts a connection loop that will fail (no server),
	// but it should not leak goroutines after Stop.
	mgr.GetBuffer("127.0.0.1", 1, "secret") // port 1 — won't connect
	time.Sleep(50 * time.Millisecond)

	mgr.Stop()
	cancel()
	// If goroutines leak, the test process won't exit cleanly.
}
