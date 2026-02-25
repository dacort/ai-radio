// Package hub provides a WebSocket hub that broadcasts BabbleEvents to all
// connected browser clients.
package hub

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/dacort/babble/internal/events"
)

// upgrader accepts WebSocket connections from any origin. Origin checking is
// intentionally permissive because Babble is a local-only tool.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Hub receives BabbleEvents on an input channel and fans them out as JSON
// text messages to every connected WebSocket client.
//
// Typical usage:
//
//	h := hub.New(eventCh)
//	go h.Run()
//	http.HandleFunc("/ws", h.HandleWS)
type Hub struct {
	eventCh <-chan *events.BabbleEvent

	mu      sync.Mutex
	clients map[*websocket.Conn]struct{}
}

// New creates a Hub that reads from eventCh.
func New(eventCh <-chan *events.BabbleEvent) *Hub {
	return &Hub{
		eventCh: eventCh,
		clients: make(map[*websocket.Conn]struct{}),
	}
}

// Run reads BabbleEvents from the event channel and broadcasts each one as a
// JSON text message to all connected clients. It blocks until eventCh is
// closed.
func (h *Hub) Run() {
	for ev := range h.eventCh {
		payload, err := json.Marshal(ev)
		if err != nil {
			log.Printf("hub: marshal event: %v", err)
			continue
		}
		h.broadcast(payload)
	}
}

// broadcast sends payload to every registered client. Clients that cannot be
// written to are closed and removed from the set.
func (h *Hub) broadcast(payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			log.Printf("hub: write to client: %v — removing", err)
			conn.Close()
			delete(h.clients, conn)
		}
	}
}

// HandleWS upgrades an HTTP request to a WebSocket connection, registers the
// client, and then reads (and discards) incoming messages until the connection
// closes. Discarding messages is required so that the gorilla/websocket library
// can process control frames (ping/pong/close) and detect disconnection.
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an HTTP error response; just log and return.
		log.Printf("hub: upgrade: %v", err)
		return
	}

	h.addClient(conn)

	// Read loop: discard all client-originated messages but keep the connection
	// alive and detect when the client closes it.
	go func() {
		defer h.removeClient(conn)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				// Any error here (including normal close) means the connection
				// is gone.
				return
			}
		}
	}()
}

// addClient registers conn in the client set.
func (h *Hub) addClient(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = struct{}{}
}

// removeClient closes conn and removes it from the client set.
func (h *Hub) removeClient(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[conn]; ok {
		conn.Close()
		delete(h.clients, conn)
	}
}
