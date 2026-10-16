package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Client represents a single connected browser tab.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// Hub manages all active WebSocket connections and broadcasts messages to them.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
}

// NewHub creates an initialized Hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]bool)}
}

// ServeWS upgrades an HTTP connection to WebSocket and registers the client.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}

	client := &Client{hub: h, conn: conn, send: make(chan []byte, 32)}

	h.mu.Lock()
	h.clients[client] = true
	h.mu.Unlock()

	log.Printf("[WS] Client connected. Total: %d", h.clientCount())

	// Write pump: sends queued messages to the browser
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.clients, client)
			h.mu.Unlock()
			conn.Close()
			log.Printf("[WS] Client disconnected. Total: %d", h.clientCount())
		}()
		for msg := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// Read pump: keeps the connection alive (discards incoming frames)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	close(client.send)
}

// Broadcast sends a named event + data payload to all connected clients.
// This is called by the file watcher goroutine when new log data arrives.
func (h *Hub) Broadcast(event string, data any) {
	payload := map[string]any{"event": event, "data": data}
	msg, _ := json.Marshal(payload)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		select {
		case client.send <- msg:
		default:
			// Client send buffer is full — skip it
		}
	}
}

func (h *Hub) clientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
