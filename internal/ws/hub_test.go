package ws

import (
	"testing"
	"time"
)

// --------------------------------------------------------------------------
// Assertion helpers
// --------------------------------------------------------------------------

// assertSendReceives checks that the client's send channel contains exactly
// the expected message within the timeout.
func assertSendReceives(t *testing.T, c *Client, expected []byte) {
	t.Helper()
	select {
	case msg := <-c.send:
		if string(msg) != string(expected) {
			t.Errorf("client: expected message %q, got %q", string(expected), string(msg))
		}
	case <-time.After(time.Second):
		t.Errorf("client: timed out waiting for message %q", string(expected))
	}
}

// assertSendEmpty checks that the client's send channel has no messages
// within a short timeout. If the channel is closed (client disconnected),
// that is treated as empty.
func assertSendEmpty(t *testing.T, c *Client) {
	t.Helper()
	select {
	case msg, ok := <-c.send:
		if !ok {
			// Channel is closed — no more messages possible, treat as empty.
			return
		}
		t.Errorf("client: expected no message, got %q", string(msg))
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

// waitForHubLen polls up to 2s for Len() to reach the expected value.
// Helps with timing in slow-client tests where the Run loop needs to
// process a broadcast + disconnect before we check.
func waitForHubLen(t *testing.T, h *Hub, expected int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if h.Len() == expected {
			return
		}
		select {
		case <-deadline:
			t.Errorf("Hub.Len() never reached %d (stuck at %d)", expected, h.Len())
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

func TestHub_Register(t *testing.T) {
	h := NewHub()
	go h.Run()

	c := &Client{
		send: make(chan []byte, 1),
	}
	h.Register(c)

	waitForHubLen(t, h, 1)

	// Clean up
	h.Stop()
}

func TestHub_Unregister(t *testing.T) {
	h := NewHub()
	go h.Run()

	c := &Client{
		send: make(chan []byte, 1),
	}
	h.Register(c)
	waitForHubLen(t, h, 1)

	h.Unregister(c)
	waitForHubLen(t, h, 0)

	// The send channel should have been closed by the hub
	_, ok := <-c.send
	if ok {
		t.Error("expected client.send to be closed after unregister")
	}

	h.Stop()
}

func TestHub_Broadcast(t *testing.T) {
	h := NewHub()
	go h.Run()

	c1 := &Client{send: make(chan []byte, 1)}
	c2 := &Client{send: make(chan []byte, 1)}
	h.Register(c1)
	h.Register(c2)
	waitForHubLen(t, h, 2)

	msg := []byte("hello from hub")
	h.Broadcast(msg)

	assertSendReceives(t, c1, msg)
	assertSendReceives(t, c2, msg)

	h.Stop()
}

func TestHub_Broadcast_NotSentToUnregistered(t *testing.T) {
	h := NewHub()
	go h.Run()

	c1 := &Client{send: make(chan []byte, 1)}
	c2 := &Client{send: make(chan []byte, 1)}
	h.Register(c1)
	h.Register(c2)
	waitForHubLen(t, h, 2)

	// Unregister c2
	h.Unregister(c2)
	waitForHubLen(t, h, 1)

	msg := []byte("only for c1")
	h.Broadcast(msg)

	assertSendReceives(t, c1, msg)
	assertSendEmpty(t, c2)

	h.Stop()
}

func TestHub_Broadcast_SlowClient_ShouldNotBlock(t *testing.T) {
	h := NewHub()
	go h.Run()

	// Both clients have bufSize=1.
	// cFast will be responsive; cSlow will have its buffer full.
	cFast := &Client{send: make(chan []byte, 1)}
	cSlow := &Client{send: make(chan []byte, 1)}

	h.Register(cFast)
	h.Register(cSlow)
	waitForHubLen(t, h, 2)

	// Fill cSlow's buffer so the next broadcast will hit the default case
	// (non-blocking send) and cause the hub to disconnect cSlow.
	cSlow.send <- []byte("fill")

	// Broadcast a message. This should NOT block — the hub will disconnect cSlow.
	h.Broadcast([]byte("important"))

	// cFast should receive the message
	assertSendReceives(t, cFast, []byte("important"))

	// cSlow should still have its fill message (the broadcast was dropped)
	assertSendReceives(t, cSlow, []byte("fill"))
	// and cSlow's send should now be closed (disconnected by hub)
	assertSendEmpty(t, cSlow)

	// The slow client should have been removed from the hub
	waitForHubLen(t, h, 1)

	h.Stop()
}
