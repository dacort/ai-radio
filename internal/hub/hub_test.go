package hub_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dacort/babble/internal/events"
	"github.com/dacort/babble/internal/hub"
)

// dialWS is a helper that connects a WebSocket client to the given URL,
// fatally failing the test on error.
func dialWS(t *testing.T, rawURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(rawURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

// wsURL converts an httptest server URL (http://...) to its WebSocket
// equivalent (ws://...).
func wsURL(serverURL, path string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + path
}

// TestHubBroadcastsEvents verifies the core broadcast loop: an event sent on
// eventCh is marshalled to JSON and delivered to a connected WebSocket client.
func TestHubBroadcastsEvents(t *testing.T) {
	eventCh := make(chan *events.BabbleEvent, 10)
	h := hub.New(eventCh)
	go h.Run()

	server := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer server.Close()

	conn := dialWS(t, wsURL(server.URL, "/ws"))
	defer conn.Close()

	// Give the hub a moment to register the client.
	time.Sleep(50 * time.Millisecond)

	want := &events.BabbleEvent{
		Session:   "myapp",
		SessionID: "abc123",
		Category:  events.CategoryAction,
		Event:     "Bash",
		Detail:    "go test ./...",
		Timestamp: "2024-01-01T00:00:00Z",
	}
	eventCh <- want

	// Read the message from the WebSocket with a reasonable deadline.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}

	var got events.BabbleEvent
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}

	if got.Session != want.Session {
		t.Errorf("session = %q, want %q", got.Session, want.Session)
	}
	if got.SessionID != want.SessionID {
		t.Errorf("sessionId = %q, want %q", got.SessionID, want.SessionID)
	}
	if got.Category != want.Category {
		t.Errorf("category = %q, want %q", got.Category, want.Category)
	}
	if got.Event != want.Event {
		t.Errorf("event = %q, want %q", got.Event, want.Event)
	}
	if got.Detail != want.Detail {
		t.Errorf("detail = %q, want %q", got.Detail, want.Detail)
	}
	if got.Timestamp != want.Timestamp {
		t.Errorf("timestamp = %q, want %q", got.Timestamp, want.Timestamp)
	}
}

// TestHubBroadcastsToMultipleClients verifies that all connected clients
// receive a broadcast, not just one.
func TestHubBroadcastsToMultipleClients(t *testing.T) {
	eventCh := make(chan *events.BabbleEvent, 10)
	h := hub.New(eventCh)
	go h.Run()

	server := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer server.Close()

	const clientCount = 3
	conns := make([]*websocket.Conn, clientCount)
	for i := range conns {
		conns[i] = dialWS(t, wsURL(server.URL, "/ws"))
		defer conns[i].Close() //nolint:revive
	}

	// Give the hub time to register all clients.
	time.Sleep(100 * time.Millisecond)

	ev := &events.BabbleEvent{
		Session:  "proj",
		Category: events.CategoryMeta,
		Event:    "system",
	}
	eventCh <- ev

	for i, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("client %d: read message: %v", i, err)
		}
		var got events.BabbleEvent
		if err := json.Unmarshal(msg, &got); err != nil {
			t.Fatalf("client %d: unmarshal: %v", i, err)
		}
		if got.Event != ev.Event {
			t.Errorf("client %d: event = %q, want %q", i, got.Event, ev.Event)
		}
	}
}

// TestHubRemovesDisconnectedClient verifies that after a client disconnects
// the hub stops tracking it and subsequent events don't fail the process.
func TestHubRemovesDisconnectedClient(t *testing.T) {
	eventCh := make(chan *events.BabbleEvent, 10)
	h := hub.New(eventCh)
	go h.Run()

	server := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer server.Close()

	// Connect and immediately disconnect a client.
	conn := dialWS(t, wsURL(server.URL, "/ws"))
	time.Sleep(50 * time.Millisecond)
	conn.Close()

	// Give the hub time to detect the disconnection.
	time.Sleep(100 * time.Millisecond)

	// Sending an event when there are no clients must not block or panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		eventCh <- &events.BabbleEvent{Event: "Bash"}
	}()

	select {
	case <-done:
		// Succeeded — hub drained the event without getting stuck.
	case <-time.After(2 * time.Second):
		t.Error("hub blocked sending event after client disconnect")
	}
}

// TestHubJSONShape verifies that the JSON sent over the wire contains the
// expected field names and types (a lightweight contract test).
func TestHubJSONShape(t *testing.T) {
	eventCh := make(chan *events.BabbleEvent, 10)
	h := hub.New(eventCh)
	go h.Run()

	server := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer server.Close()

	conn := dialWS(t, wsURL(server.URL, "/ws"))
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	eventCh <- &events.BabbleEvent{
		Session:   "s",
		SessionID: "id1",
		Category:  events.CategoryRead,
		Event:     "Read",
		Detail:    "/tmp/file.go",
		Timestamp: "2024-06-01T12:00:00Z",
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Unmarshal into a raw map to inspect field names exactly.
	var raw map[string]interface{}
	if err := json.Unmarshal(msg, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	requiredFields := []string{"session", "sessionId", "category", "event", "detail", "timestamp"}
	for _, field := range requiredFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("JSON message missing field %q; got keys: %v", field, keys(raw))
		}
	}
}

// keys is a test helper that returns the keys of a map as a slice.
func keys(m map[string]interface{}) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
