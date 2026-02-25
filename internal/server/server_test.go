package server_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dacort/babble/internal/events"
	"github.com/dacort/babble/internal/server"
	"github.com/dacort/babble/internal/sessions"
)

// wsURL converts a host:port address to a WebSocket URL for the given path.
func wsURL(addr, path string) string {
	return fmt.Sprintf("ws://%s%s", addr, path)
}

// httpURL converts a host:port address to an HTTP URL for the given path.
func httpURL(addr, path string) string {
	return fmt.Sprintf("http://%s%s", addr, path)
}

// dialWS connects a WebSocket client to the given URL, retrying briefly to
// allow the server goroutine to start. The connection is closed automatically
// when the test ends.
func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	var (
		conn *websocket.Conn
		err  error
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, _, err = websocket.DefaultDialer.Dial(url, nil)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial websocket %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// TestEndToEnd exercises the complete pipeline:
//
//	JSONL write → session manager tail → event parse → hub broadcast → WebSocket
//
// It also verifies the three main HTTP endpoints.
func TestEndToEnd(t *testing.T) {
	// --- 1. Temp directories -------------------------------------------------
	watchPath := t.TempDir()
	packsDir := t.TempDir()

	// --- 2. Project subdirectory mimicking ~/.claude/projects/... -----------
	// The session manager expects: watchPath/<project-dir>/<session>.jsonl
	// The project-dir name is arbitrary; the session label comes from cwd.
	projectDir := filepath.Join(watchPath, "-Users-test-src-myapp")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	// --- 3. Minimal default pack in packsDir --------------------------------
	defaultPackDir := filepath.Join(packsDir, "default")
	if err := os.MkdirAll(defaultPackDir, 0o755); err != nil {
		t.Fatalf("mkdir default pack: %v", err)
	}
	packJSON := `{"name":"default","description":"Default pack","author":"test","version":"1.0.0","categories":{}}`
	if err := os.WriteFile(filepath.Join(defaultPackDir, "pack.json"), []byte(packJSON), 0o644); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}

	// --- 4. Config path in a temp location (no file = defaults used) --------
	configPath := filepath.Join(t.TempDir(), "config.json")

	// --- 5. Create server with a test staticFS ------------------------------
	staticFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>babble</body></html>")},
	}
	srv := server.New(0, staticFS, packsDir, configPath)

	// --- 6 & 7. Start session manager and server ----------------------------
	// Bind the listener first so we know the actual port before starting.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	addr := ln.Addr().String()

	// Start the server. http.Serve blocks until the listener is closed, which
	// happens via t.Cleanup, so the goroutine will exit cleanly.
	go func() { _ = srv.StartWithListener(ln) }()

	// Start the session manager, wired to the same event channel as the server.
	mgr := sessions.NewManager(watchPath, srv.EventCh())
	go func() { _ = mgr.Start() }()
	t.Cleanup(mgr.Stop)

	// --- 8. Connect a WebSocket client --------------------------------------
	conn := dialWS(t, wsURL(addr, "/ws"))

	// Give the hub a moment to register the new client before writing an event.
	time.Sleep(50 * time.Millisecond)

	// --- 9. Write a JSONL event line to the session file --------------------
	sessionFile := filepath.Join(projectDir, "test-id.jsonl")
	line := `{"type":"assistant","sessionId":"test-id","cwd":"/Users/test/src/myapp",` +
		`"message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"main.go"}}]},` +
		`"timestamp":"2026-02-25T19:42:01Z"}` + "\n"

	if err := os.WriteFile(sessionFile, []byte(line), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	// --- 10. Read from WebSocket with a 5 s timeout -------------------------
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	_, msg, readErr := conn.ReadMessage()
	if readErr != nil {
		t.Fatalf("read WebSocket message: %v", readErr)
	}

	// --- 11. Verify the received event --------------------------------------
	var got events.BabbleEvent
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("unmarshal event: %v (raw: %s)", err, msg)
	}

	if got.Category != events.CategoryWrite {
		t.Errorf("category = %q, want %q", got.Category, events.CategoryWrite)
	}
	if got.Event != "Edit" {
		t.Errorf("event = %q, want %q", got.Event, "Edit")
	}
	if got.Session != "myapp" {
		t.Errorf("session = %q, want %q", got.Session, "myapp")
	}
	if got.Detail != "main.go" {
		t.Errorf("detail = %q, want %q", got.Detail, "main.go")
	}

	// --- 12. HTTP endpoint checks -------------------------------------------
	t.Run("GET / returns 200", func(t *testing.T) {
		resp, err := http.Get(httpURL(addr, "/"))
		if err != nil {
			t.Fatalf("GET /: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("GET /api/packs returns JSON array", func(t *testing.T) {
		resp, err := http.Get(httpURL(addr, "/api/packs"))
		if err != nil {
			t.Fatalf("GET /api/packs: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json prefix", ct)
		}
		var packs []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&packs); err != nil {
			t.Fatalf("decode packs: %v", err)
		}
		if len(packs) != 1 {
			t.Errorf("len(packs) = %d, want 1", len(packs))
		}
		if name, _ := packs[0]["name"].(string); name != "default" {
			t.Errorf("packs[0].name = %q, want %q", name, "default")
		}
	})

	t.Run("GET /api/config returns JSON object", func(t *testing.T) {
		resp, err := http.Get(httpURL(addr, "/api/config"))
		if err != nil {
			t.Fatalf("GET /api/config: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json prefix", ct)
		}
		var cfg map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
			t.Fatalf("decode config: %v", err)
		}
		if _, ok := cfg["port"]; !ok {
			t.Error("config missing 'port' field")
		}
		if _, ok := cfg["activePack"]; !ok {
			t.Error("config missing 'activePack' field")
		}
	})
}
