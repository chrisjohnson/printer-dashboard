package ws

import (
	"sync/atomic"
)

// Hub manages WebSocket client registration and message broadcasting.
// It follows the single-goroutine pattern: all access to the clients map
// happens in the Run() goroutine, so no mutex is needed.
type Hub struct {
	// clients is the set of registered clients.
	clients map[*Client]bool

	// broadcast is a channel for incoming messages to broadcast.
	broadcast chan []byte

	// register receives new client registrations.
	register chan *Client

	// unregister receives client unregistration requests.
	unregister chan *Client

	// stop signals the Run loop to exit.
	stop chan struct{}

	// clientCount is atomically updated for safe concurrent access via Len().
	clientCount atomic.Int32
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		stop:       make(chan struct{}),
	}
}

// Run starts the hub's main event loop. Must be called as a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			h.clientCount.Add(1)

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.clientCount.Add(-1)
			}

		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Client's send buffer is full; disconnect it.
					close(client.send)
					delete(h.clients, client)
					h.clientCount.Add(-1)
				}
			}

		case <-h.stop:
			return
		}
	}
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client from the hub and closes its send channel.
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// Broadcast sends a message to all connected clients.
// Slow clients (those with a full send buffer) are disconnected.
func (h *Hub) Broadcast(data []byte) {
	h.broadcast <- data
}

// Stop signals the Run loop to exit and closes all client send channels.
func (h *Hub) Stop() {
	close(h.stop)
}

// Len returns the number of connected clients.
// This is safe for concurrent use.
func (h *Hub) Len() int {
	return int(h.clientCount.Load())
}
