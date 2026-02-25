# Babble Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go server that tails Claude Code session logs and streams classified events to a browser UI that plays sounds from curated sound packs.

**Architecture:** Go backend watches `~/.claude/projects/` JSONL files, parses events, classifies them into 9 categories, and broadcasts via WebSocket. Browser frontend receives events, displays a live event stream with session sidebar, and plays audio from the active sound pack using Web Audio API.

**Tech Stack:** Go 1.25, gorilla/websocket, fsnotify, vanilla JS + Web Audio API, HTML/CSS

---

### Task 1: Go Project Scaffold

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `cmd/serve.go`

**Step 1: Initialize Go module**

Run: `go mod init github.com/dacort/babble`

**Step 2: Create main.go entry point**

```go
// main.go
package main

import (
	"fmt"
	"os"

	"github.com/dacort/babble/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

**Step 3: Create cmd/serve.go with basic CLI**

```go
// cmd/serve.go
package cmd

import (
	"flag"
	"fmt"
)

func Execute() error {
	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	port := serveCmd.Int("p", 3333, "port to listen on")

	if len(os.Args) < 2 {
		fmt.Println("Usage: babble <command>")
		fmt.Println("  serve    Start the Babble server")
		return nil
	}

	switch os.Args[1] {
	case "serve":
		serveCmd.Parse(os.Args[2:])
		return runServe(*port)
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func runServe(port int) error {
	fmt.Printf("Babble listening on http://localhost:%d\n", port)
	return nil
}
```

**Step 4: Build and verify**

Run: `go build -o babble . && ./babble serve`
Expected: `Babble listening on http://localhost:3333`

**Step 5: Commit**

```bash
git add go.mod main.go cmd/
git commit -m "feat: scaffold Go project with serve command"
```

---

### Task 2: JSONL Event Types

**Files:**
- Create: `internal/events/event.go`
- Create: `internal/events/event_test.go`

**Step 1: Write the test for event parsing**

```go
// internal/events/event_test.go
package events

import "testing"

func TestParseAssistantToolUse(t *testing.T) {
	line := `{"type":"assistant","sessionId":"abc-123","cwd":"/Users/test/src/myproject","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"main.go"}}]},"timestamp":"2026-02-25T19:42:01Z"}`
	ev, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Category != CategoryWrite {
		t.Errorf("expected category %q, got %q", CategoryWrite, ev.Category)
	}
	if ev.Event != "Edit" {
		t.Errorf("expected event Edit, got %s", ev.Event)
	}
	if ev.Detail != "main.go" {
		t.Errorf("expected detail main.go, got %s", ev.Detail)
	}
	if ev.Session != "myproject" {
		t.Errorf("expected session myproject, got %s", ev.Session)
	}
}

func TestParseAssistantThinking(t *testing.T) {
	line := `{"type":"assistant","sessionId":"abc-123","cwd":"/Users/test/src/proj","message":{"content":[{"type":"thinking"}]},"timestamp":"2026-02-25T19:42:01Z"}`
	ev, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Category != CategoryAmbient {
		t.Errorf("expected category %q, got %q", CategoryAmbient, ev.Category)
	}
}

func TestParseToolResultError(t *testing.T) {
	line := `{"type":"user","sessionId":"abc-123","cwd":"/Users/test/src/proj","message":{"content":[{"type":"tool_result","content":"<tool_use_error>File does not exist.</tool_use_error>","is_error":true}]},"timestamp":"2026-02-25T19:42:01Z"}`
	ev, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Category != CategoryError {
		t.Errorf("expected category %q, got %q", CategoryError, ev.Category)
	}
}

func TestParseUserInput(t *testing.T) {
	line := `{"type":"user","sessionId":"abc-123","cwd":"/Users/test/src/proj","message":{"role":"user","content":"hello"},"timestamp":"2026-02-25T19:42:01Z"}`
	ev, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Category != CategoryWarn {
		t.Errorf("expected category %q, got %q", CategoryWarn, ev.Category)
	}
	if ev.Event != "UserInput" {
		t.Errorf("expected event UserInput, got %s", ev.Event)
	}
}

func TestParseProgress(t *testing.T) {
	line := `{"type":"progress","sessionId":"abc-123","cwd":"/Users/test/src/proj","data":{"type":"hook_progress"},"timestamp":"2026-02-25T19:42:01Z"}`
	ev, err := ParseLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Category != CategoryMeta {
		t.Errorf("expected category %q, got %q", CategoryMeta, ev.Category)
	}
}

func TestParseSkipsFileHistorySnapshot(t *testing.T) {
	line := `{"type":"file-history-snapshot","messageId":"abc"}`
	_, err := ParseLine([]byte(line))
	if err != ErrSkipEvent {
		t.Errorf("expected ErrSkipEvent, got %v", err)
	}
}

func TestSessionNameFromCwd(t *testing.T) {
	tests := []struct {
		cwd  string
		want string
	}{
		{"/Users/dacort/src/babble", "babble"},
		{"/Users/dacort/src/my-project", "my-project"},
		{"/home/user/code/app", "app"},
	}
	for _, tt := range tests {
		got := sessionNameFromCwd(tt.cwd)
		if got != tt.want {
			t.Errorf("sessionNameFromCwd(%q) = %q, want %q", tt.cwd, got, tt.want)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/events/ -v`
Expected: compilation errors (types not defined yet)

**Step 3: Write the event types and parser**

```go
// internal/events/event.go
package events

import (
	"encoding/json"
	"errors"
	"path/filepath"
)

type Category string

const (
	CategoryAmbient Category = "ambient"
	CategoryAction  Category = "action"
	CategoryRead    Category = "read"
	CategoryWrite   Category = "write"
	CategoryNetwork Category = "network"
	CategorySuccess Category = "success"
	CategoryWarn    Category = "warn"
	CategoryError   Category = "error"
	CategoryMeta    Category = "meta"
)

var ErrSkipEvent = errors.New("skip event")

type BabbleEvent struct {
	Session   string   `json:"session"`
	SessionID string   `json:"sessionId"`
	Category  Category `json:"category"`
	Event     string   `json:"event"`
	Detail    string   `json:"detail"`
	Timestamp string   `json:"timestamp"`
}

// Raw JSONL structures — only decode what we need.

type rawLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Cwd       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Data      json.RawMessage `json:"data"`
}

type rawMessage struct {
	Role    string           `json:"role"`
	Content json.RawMessage  `json:"content"`
}

type contentBlock struct {
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	IsError bool            `json:"is_error"`
	Content interface{}     `json:"content"`
}

type rawData struct {
	Type string `json:"type"`
}

// Tool name → category mapping
var toolCategory = map[string]Category{
	"Read":            CategoryRead,
	"Grep":            CategoryRead,
	"Glob":            CategoryRead,
	"Edit":            CategoryWrite,
	"Write":           CategoryWrite,
	"NotebookEdit":    CategoryWrite,
	"Bash":            CategoryAction,
	"WebFetch":        CategoryNetwork,
	"WebSearch":       CategoryNetwork,
	"Task":            CategoryMeta,
	"EnterPlanMode":   CategoryMeta,
	"ExitPlanMode":    CategoryMeta,
	"AskUserQuestion": CategoryWarn,
	"Skill":           CategoryMeta,
	"TodoWrite":       CategoryMeta,
	"TaskCreate":      CategoryMeta,
	"TaskUpdate":      CategoryMeta,
}

// Tool name → detail extraction key
var toolDetailKey = map[string]string{
	"Read":      "file_path",
	"Edit":      "file_path",
	"Write":     "file_path",
	"Grep":      "pattern",
	"Glob":      "pattern",
	"Bash":      "command",
	"WebFetch":  "url",
	"WebSearch": "query",
	"Task":      "description",
}

func ParseLine(data []byte) (*BabbleEvent, error) {
	var raw rawLine
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	session := sessionNameFromCwd(raw.Cwd)

	switch raw.Type {
	case "assistant":
		return parseAssistant(raw, session)
	case "user":
		return parseUser(raw, session)
	case "progress":
		return parseProgress(raw, session)
	case "system":
		return &BabbleEvent{
			Session:   session,
			SessionID: raw.SessionID,
			Category:  CategoryMeta,
			Event:     "System",
			Timestamp: raw.Timestamp,
		}, nil
	default:
		return nil, ErrSkipEvent
	}
}

func parseAssistant(raw rawLine, session string) (*BabbleEvent, error) {
	var msg rawMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return nil, ErrSkipEvent
	}

	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil, ErrSkipEvent
	}

	if len(blocks) == 0 {
		return nil, ErrSkipEvent
	}

	// Use first block to determine event type.
	block := blocks[0]

	switch block.Type {
	case "thinking":
		return &BabbleEvent{
			Session:   session,
			SessionID: raw.SessionID,
			Category:  CategoryAmbient,
			Event:     "Think",
			Timestamp: raw.Timestamp,
		}, nil

	case "text":
		return &BabbleEvent{
			Session:   session,
			SessionID: raw.SessionID,
			Category:  CategoryAmbient,
			Event:     "Text",
			Timestamp: raw.Timestamp,
		}, nil

	case "tool_use":
		cat, ok := toolCategory[block.Name]
		if !ok {
			cat = CategoryAction
		}
		detail := extractDetail(block.Name, block.Input)
		return &BabbleEvent{
			Session:   session,
			SessionID: raw.SessionID,
			Category:  cat,
			Event:     block.Name,
			Detail:    detail,
			Timestamp: raw.Timestamp,
		}, nil

	default:
		return nil, ErrSkipEvent
	}
}

func parseUser(raw rawLine, session string) (*BabbleEvent, error) {
	var msg rawMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return nil, ErrSkipEvent
	}

	// Check for tool_result with error
	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "tool_result" && b.IsError {
				return &BabbleEvent{
					Session:   session,
					SessionID: raw.SessionID,
					Category:  CategoryError,
					Event:     "ToolError",
					Timestamp: raw.Timestamp,
				}, nil
			}
		}
	}

	// Regular user input
	if msg.Role == "user" {
		return &BabbleEvent{
			Session:   session,
			SessionID: raw.SessionID,
			Category:  CategoryWarn,
			Event:     "UserInput",
			Timestamp: raw.Timestamp,
		}, nil
	}

	return nil, ErrSkipEvent
}

func parseProgress(raw rawLine, session string) (*BabbleEvent, error) {
	var d rawData
	if raw.Data != nil {
		json.Unmarshal(raw.Data, &d)
	}
	return &BabbleEvent{
		Session:   session,
		SessionID: raw.SessionID,
		Category:  CategoryMeta,
		Event:     "Progress",
		Detail:    d.Type,
		Timestamp: raw.Timestamp,
	}, nil
}

func extractDetail(toolName string, input json.RawMessage) string {
	key, ok := toolDetailKey[toolName]
	if !ok || input == nil {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	if v, ok := m[key]; ok {
		s, _ := v.(string)
		// Truncate long details (e.g. bash commands)
		if len(s) > 80 {
			s = s[:77] + "..."
		}
		return s
	}
	return ""
}

func sessionNameFromCwd(cwd string) string {
	if cwd == "" {
		return "unknown"
	}
	return filepath.Base(cwd)
}
```

**Step 4: Run tests**

Run: `go test ./internal/events/ -v`
Expected: all tests pass

**Step 5: Commit**

```bash
git add internal/events/
git commit -m "feat: add JSONL event parser and classifier"
```

---

### Task 3: Session Manager (Log Tailing)

**Files:**
- Create: `internal/sessions/manager.go`
- Create: `internal/sessions/manager_test.go`

**Step 1: Write test for session discovery and tailing**

```go
// internal/sessions/manager_test.go
package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dacort/babble/internal/events"
)

func TestManagerDiscoversAndTailsSessions(t *testing.T) {
	// Create a temp dir mimicking ~/.claude/projects/
	tmpDir := t.TempDir()
	projDir := filepath.Join(tmpDir, "-Users-test-src-myapp")
	os.MkdirAll(projDir, 0755)

	// Create a session JSONL file
	sessionFile := filepath.Join(projDir, "test-session-id.jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatal(err)
	}

	eventCh := make(chan *events.BabbleEvent, 10)
	mgr := NewManager(tmpDir, eventCh)
	go mgr.Start()
	defer mgr.Stop()

	// Give manager time to discover the file
	time.Sleep(200 * time.Millisecond)

	// Append a line to the session file
	line := `{"type":"assistant","sessionId":"test-session-id","cwd":"/Users/test/src/myapp","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]},"timestamp":"2026-02-25T19:42:01Z"}` + "\n"
	f.WriteString(line)
	f.Sync()

	// Should receive the event
	select {
	case ev := <-eventCh:
		if ev.Event != "Bash" {
			t.Errorf("expected event Bash, got %s", ev.Event)
		}
		if ev.Session != "myapp" {
			t.Errorf("expected session myapp, got %s", ev.Session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/sessions/ -v`
Expected: compilation errors

**Step 3: Implement the session manager**

```go
// internal/sessions/manager.go
package sessions

import (
	"bufio"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/dacort/babble/internal/events"
)

type Manager struct {
	watchPath string
	eventCh   chan<- *events.BabbleEvent
	watcher   *fsnotify.Watcher
	tailing   map[string]struct{} // files being tailed
	mu        sync.Mutex
	done      chan struct{}
}

func NewManager(watchPath string, eventCh chan<- *events.BabbleEvent) *Manager {
	return &Manager{
		watchPath: watchPath,
		eventCh:   eventCh,
		tailing:   make(map[string]struct{}),
		done:      make(chan struct{}),
	}
}

func (m *Manager) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	m.watcher = watcher

	// Discover existing JSONL files and start tailing them.
	m.discoverExisting()

	// Watch the base directory for new project subdirectories.
	watcher.Add(m.watchPath)

	// Also watch existing subdirectories.
	entries, _ := os.ReadDir(m.watchPath)
	for _, e := range entries {
		if e.IsDir() {
			watcher.Add(filepath.Join(m.watchPath, e.Name()))
		}
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					watcher.Add(event.Name)
				}
			}
			if strings.HasSuffix(event.Name, ".jsonl") {
				if event.Has(fsnotify.Create) {
					m.startTailing(event.Name)
				}
				if event.Has(fsnotify.Write) {
					m.startTailing(event.Name)
				}
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		case <-m.done:
			return nil
		}
	}
}

func (m *Manager) Stop() {
	close(m.done)
	if m.watcher != nil {
		m.watcher.Close()
	}
}

func (m *Manager) discoverExisting() {
	matches, _ := filepath.Glob(filepath.Join(m.watchPath, "*", "*.jsonl"))
	for _, path := range matches {
		m.startTailing(path)
	}
}

func (m *Manager) startTailing(path string) {
	m.mu.Lock()
	if _, ok := m.tailing[path]; ok {
		m.mu.Unlock()
		return
	}
	m.tailing[path] = struct{}{}
	m.mu.Unlock()

	go m.tailFile(path)
}

func (m *Manager) tailFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("failed to open %s: %v", path, err)
		return
	}
	defer f.Close()

	// Seek to end — only process new events.
	f.Seek(0, io.SeekEnd)

	reader := bufio.NewReader(f)
	for {
		select {
		case <-m.done:
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			// No more data — wait for fsnotify to wake us.
			// Use a simple poll as fallback since fsnotify handles the wakeup.
			select {
			case <-m.done:
				return
			case <-m.waitForWrite(path):
				continue
			}
		}

		ev, err := events.ParseLine(line)
		if err != nil {
			continue // skip unparseable or ErrSkipEvent
		}

		select {
		case m.eventCh <- ev:
		case <-m.done:
			return
		}
	}
}

// waitForWrite returns a channel that closes when the file is written to.
// This is a simple mechanism — the main loop's fsnotify also drives re-reads.
func (m *Manager) waitForWrite(path string) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		// Simple: use fsnotify on the file itself.
		w, err := fsnotify.NewWatcher()
		if err != nil {
			close(ch)
			return
		}
		defer w.Close()
		w.Add(path)
		select {
		case <-w.Events:
			close(ch)
		case <-m.done:
			close(ch)
		}
	}()
	return ch
}
```

**Step 4: Add fsnotify dependency and run tests**

Run: `go get github.com/fsnotify/fsnotify && go test ./internal/sessions/ -v -timeout 10s`
Expected: test passes

**Step 5: Commit**

```bash
git add internal/sessions/ go.mod go.sum
git commit -m "feat: add session manager with JSONL file tailing"
```

---

### Task 4: WebSocket Hub

**Files:**
- Create: `internal/hub/hub.go`
- Create: `internal/hub/hub_test.go`

**Step 1: Write the test**

```go
// internal/hub/hub_test.go
package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dacort/babble/internal/events"
	"github.com/gorilla/websocket"
)

func TestHubBroadcastsEvents(t *testing.T) {
	eventCh := make(chan *events.BabbleEvent, 10)
	h := New(eventCh)
	go h.Run()

	server := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send an event
	eventCh <- &events.BabbleEvent{
		Session:   "testproj",
		SessionID: "abc-123",
		Category:  events.CategoryWrite,
		Event:     "Edit",
		Detail:    "main.go",
		Timestamp: "2026-02-25T19:42:01Z",
	}

	// Read from WebSocket
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var ev events.BabbleEvent
	if err := json.Unmarshal(msg, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Event != "Edit" {
		t.Errorf("expected event Edit, got %s", ev.Event)
	}
	if ev.Session != "testproj" {
		t.Errorf("expected session testproj, got %s", ev.Session)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go get github.com/gorilla/websocket && go test ./internal/hub/ -v`
Expected: compilation errors

**Step 3: Implement the WebSocket hub**

```go
// internal/hub/hub.go
package hub

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/dacort/babble/internal/events"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Hub struct {
	eventCh <-chan *events.BabbleEvent
	clients map[*websocket.Conn]struct{}
	mu      sync.Mutex
}

func New(eventCh <-chan *events.BabbleEvent) *Hub {
	return &Hub{
		eventCh: eventCh,
		clients: make(map[*websocket.Conn]struct{}),
	}
}

func (h *Hub) Run() {
	for ev := range h.eventCh {
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		h.broadcast(data)
	}
}

func (h *Hub) broadcast(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			conn.Close()
			delete(h.clients, conn)
		}
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}
	h.mu.Lock()
	h.clients[conn] = struct{}{}
	h.mu.Unlock()

	// Keep connection alive — read and discard client messages.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			h.mu.Lock()
			delete(h.clients, conn)
			h.mu.Unlock()
			conn.Close()
			return
		}
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/hub/ -v -timeout 10s`
Expected: test passes

**Step 5: Commit**

```bash
git add internal/hub/ go.mod go.sum
git commit -m "feat: add WebSocket hub for broadcasting events to browser"
```

---

### Task 5: HTTP Server (Tie It Together)

**Files:**
- Create: `internal/server/server.go`
- Modify: `cmd/serve.go`

**Step 1: Create the HTTP server that serves static files + WebSocket**

```go
// internal/server/server.go
package server

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/dacort/babble/internal/events"
	"github.com/dacort/babble/internal/hub"
)

type Server struct {
	port    int
	hub     *hub.Hub
	eventCh chan *events.BabbleEvent
	staticFS fs.FS
}

func New(port int, staticFS fs.FS) *Server {
	eventCh := make(chan *events.BabbleEvent, 100)
	h := hub.New(eventCh)
	return &Server{
		port:     port,
		hub:      h,
		eventCh:  eventCh,
		staticFS: staticFS,
	}
}

func (s *Server) EventCh() chan<- *events.BabbleEvent {
	return s.eventCh
}

func (s *Server) Start() error {
	go s.hub.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.hub.HandleWS)
	mux.Handle("/", http.FileServer(http.FS(s.staticFS)))

	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("Babble listening on http://localhost:%d\n", s.port)
	return http.ListenAndServe(addr, mux)
}
```

**Step 2: Wire everything together in cmd/serve.go**

Update `cmd/serve.go` to import session manager, create server, and start both:

```go
// cmd/serve.go
package cmd

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/dacort/babble/internal/server"
	"github.com/dacort/babble/internal/sessions"
)

//go:embed all:web
var webFS embed.FS

func Execute() error {
	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	port := serveCmd.Int("p", 3333, "port to listen on")
	noOpen := serveCmd.Bool("no-open", false, "don't auto-open browser")

	if len(os.Args) < 2 {
		fmt.Println("Usage: babble <command>")
		fmt.Println("  serve    Start the Babble server")
		return nil
	}

	switch os.Args[1] {
	case "serve":
		serveCmd.Parse(os.Args[2:])
		return runServe(*port, *noOpen)
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func runServe(port int, noOpen bool) error {
	// Resolve watch path
	home, _ := os.UserHomeDir()
	watchPath := filepath.Join(home, ".claude", "projects")

	// Create static FS from embedded files
	staticFS, _ := fs.Sub(webFS, "web")

	// Create server
	srv := server.New(port, staticFS)

	// Create and start session manager
	mgr := sessions.NewManager(watchPath, srv.EventCh())
	go mgr.Start()

	// Auto-open browser
	if !noOpen {
		url := fmt.Sprintf("http://localhost:%d", port)
		openBrowser(url)
	}

	return srv.Start()
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	}
}
```

**Step 3: Create a minimal placeholder web page**

Create `cmd/web/index.html` with a simple "Babble is running" message (the real UI comes in Task 7).

```html
<!DOCTYPE html>
<html>
<head><title>Babble</title></head>
<body>
  <h1>Babble</h1>
  <p>Connecting to event stream...</p>
  <pre id="log"></pre>
  <script>
    const ws = new WebSocket(`ws://${location.host}/ws`);
    const log = document.getElementById('log');
    ws.onmessage = (e) => {
      const ev = JSON.parse(e.data);
      const line = `${ev.timestamp} [${ev.session}] ${ev.category} ${ev.event} ${ev.detail}\n`;
      log.textContent = line + log.textContent;
    };
  </script>
</body>
</html>
```

**Step 4: Build and manually test**

Run: `go build -o babble . && ./babble serve --no-open`
Then open `http://localhost:3333` in a browser. Start a Claude Code session and verify events appear.

**Step 5: Commit**

```bash
git add internal/server/ cmd/ go.mod go.sum
git commit -m "feat: wire HTTP server, session manager, and WebSocket together"
```

---

### Task 6: Sound Pack Loader

**Files:**
- Create: `internal/packs/loader.go`
- Create: `internal/packs/loader_test.go`

**Step 1: Write the test**

```go
// internal/packs/loader_test.go
package packs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPack(t *testing.T) {
	dir := t.TempDir()
	packDir := filepath.Join(dir, "testpack")
	os.MkdirAll(packDir, 0755)

	manifest := `{
		"name": "Test Pack",
		"description": "A test",
		"author": "test",
		"version": "1.0.0",
		"categories": {
			"action": {"files": ["action.mp3"], "loop": false, "volume": 0.6},
			"ambient": {"files": ["ambient.mp3"], "loop": true, "volume": 0.3}
		}
	}`
	os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0644)
	os.WriteFile(filepath.Join(packDir, "action.mp3"), []byte("fake"), 0644)
	os.WriteFile(filepath.Join(packDir, "ambient.mp3"), []byte("fake"), 0644)

	pack, err := LoadPack(packDir)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Name != "Test Pack" {
		t.Errorf("expected name 'Test Pack', got %q", pack.Name)
	}
	if len(pack.Categories) != 2 {
		t.Errorf("expected 2 categories, got %d", len(pack.Categories))
	}
	if !pack.Categories["ambient"].Loop {
		t.Error("expected ambient to loop")
	}
}

func TestListPacks(t *testing.T) {
	dir := t.TempDir()

	// Create two packs
	for _, name := range []string{"ocean", "space"} {
		packDir := filepath.Join(dir, name)
		os.MkdirAll(packDir, 0755)
		manifest := `{"name":"` + name + `","description":"test","author":"test","version":"1.0.0","categories":{}}`
		os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0644)
	}

	packs, err := ListPacks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 {
		t.Errorf("expected 2 packs, got %d", len(packs))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/packs/ -v`
Expected: compilation errors

**Step 3: Implement the pack loader**

```go
// internal/packs/loader.go
package packs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type CategorySound struct {
	Files  []string `json:"files"`
	Loop   bool     `json:"loop"`
	Volume float64  `json:"volume"`
}

type Pack struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Author      string                   `json:"author"`
	Version     string                   `json:"version"`
	Categories  map[string]CategorySound `json:"categories"`
	Dir         string                   `json:"-"` // filesystem path
}

func LoadPack(dir string) (*Pack, error) {
	manifestPath := filepath.Join(dir, "pack.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading pack.json: %w", err)
	}
	var pack Pack
	if err := json.Unmarshal(data, &pack); err != nil {
		return nil, fmt.Errorf("parsing pack.json: %w", err)
	}
	pack.Dir = dir
	return &pack, nil
}

func ListPacks(baseDir string) ([]*Pack, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}
	var packs []*Pack
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pack, err := LoadPack(filepath.Join(baseDir, e.Name()))
		if err != nil {
			continue // skip invalid packs
		}
		packs = append(packs, pack)
	}
	return packs, nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/packs/ -v`
Expected: all tests pass

**Step 5: Commit**

```bash
git add internal/packs/
git commit -m "feat: add sound pack loader"
```

---

### Task 7: Serve Sound Packs via HTTP

**Files:**
- Modify: `internal/server/server.go`
- Create: `internal/server/packs_handler.go`

**Step 1: Add API endpoint for listing packs and serving audio files**

```go
// internal/server/packs_handler.go
package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/dacort/babble/internal/packs"
)

type PacksHandler struct {
	packsDir string
}

func NewPacksHandler(packsDir string) *PacksHandler {
	return &PacksHandler{packsDir: packsDir}
}

// GET /api/packs — list all available packs
func (h *PacksHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	packList, err := packs.ListPacks(h.packsDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(packList)
}

// GET /api/packs/{name}/manifest — get pack manifest
func (h *PacksHandler) HandleManifest(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pack, err := packs.LoadPack(filepath.Join(h.packsDir, name))
	if err != nil {
		http.Error(w, "pack not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pack)
}

// /sounds/{name}/ — serve audio files from pack directory
func (h *PacksHandler) SoundsFS() http.Handler {
	return http.StripPrefix("/sounds/", http.FileServer(http.Dir(h.packsDir)))
}
```

**Step 2: Register routes in server.go**

Add to the `Start()` method's mux setup:

```go
packsHandler := NewPacksHandler(s.packsDir)
mux.HandleFunc("GET /api/packs", packsHandler.HandleList)
mux.HandleFunc("GET /api/packs/{name}/manifest", packsHandler.HandleManifest)
mux.Handle("/sounds/", packsHandler.SoundsFS())
```

Update `Server` struct to accept `packsDir` and pass it through from `cmd/serve.go`.

**Step 3: Build and verify**

Run: `go build -o babble . && ./babble serve --no-open`
Then: `curl http://localhost:3333/api/packs`
Expected: JSON array of installed packs (empty if none installed yet)

**Step 4: Commit**

```bash
git add internal/server/
git commit -m "feat: add HTTP API for listing packs and serving audio files"
```

---

### Task 8: Default Sound Pack

**Files:**
- Create: `soundpacks/default/pack.json`
- Create: sound files (generated via Web Audio API offline rendering or sourced)

Since we can't easily include audio files in the repo, we'll create a "synth" default pack that uses Web Audio API synthesis defined in the manifest. This pack ships with the binary and produces sounds programmatically — no audio files needed.

**Step 1: Create the default pack manifest**

```json
// soundpacks/default/pack.json
{
  "name": "Default",
  "description": "Built-in synthesized sounds",
  "author": "babble",
  "version": "1.0.0",
  "synth": true,
  "categories": {
    "ambient":  { "synth": "sine",  "freq": 220, "duration": 2.0, "loop": true,  "volume": 0.15 },
    "action":   { "synth": "click", "freq": 800, "duration": 0.05, "loop": false, "volume": 0.4 },
    "read":     { "synth": "sine",  "freq": 440, "duration": 0.1,  "loop": false, "volume": 0.3 },
    "write":    { "synth": "sine",  "freq": 523, "duration": 0.15, "loop": false, "volume": 0.4 },
    "network":  { "synth": "sine",  "freq": 660, "duration": 0.2,  "loop": false, "volume": 0.3 },
    "success":  { "synth": "chord", "freq": 523, "duration": 0.4,  "loop": false, "volume": 0.5 },
    "warn":     { "synth": "saw",   "freq": 330, "duration": 0.3,  "loop": false, "volume": 0.6 },
    "error":    { "synth": "noise", "freq": 200, "duration": 0.3,  "loop": false, "volume": 0.7 },
    "meta":     { "synth": "sine",  "freq": 880, "duration": 0.1,  "loop": false, "volume": 0.2 }
  }
}
```

**Step 2: Embed the default pack in the Go binary**

Add to `cmd/serve.go` or a new file:

```go
//go:embed all:soundpacks
var defaultPacksFS embed.FS
```

On startup, copy the embedded default pack to `~/.config/babble/soundpacks/default/` if it doesn't already exist.

**Step 3: Update the audio engine (Task 9) to handle synth packs**

The browser audio engine will check for `"synth": true` in the pack manifest and generate sounds via Web Audio API oscillators instead of loading audio files.

**Step 4: Commit**

```bash
git add soundpacks/ cmd/
git commit -m "feat: add default synthesized sound pack"
```

---

### Task 9: Browser Audio Engine

**Files:**
- Create: `cmd/web/js/audio.js`

**Step 1: Implement the Web Audio API engine**

```javascript
// cmd/web/js/audio.js

class BabbleAudio {
  constructor() {
    this.ctx = null; // Created on first user interaction (autoplay policy)
    this.pack = null;
    this.categoryVolumes = {};
    this.masterVolume = 0.8;
    this.mutedSessions = new Set();
    this.activeLoops = {}; // category → oscillator node
  }

  async init() {
    this.ctx = new AudioContext();
    this.masterGain = this.ctx.createGain();
    this.masterGain.gain.value = this.masterVolume;
    this.masterGain.connect(this.ctx.destination);
  }

  async loadPack(packName) {
    const resp = await fetch(`/api/packs/${packName}/manifest`);
    this.pack = await resp.json();
    this.stopAllLoops();
  }

  play(event) {
    if (!this.ctx || !this.pack) return;
    if (this.mutedSessions.has(event.session)) return;

    const category = event.category;
    const catDef = this.pack.categories[category];
    if (!catDef) return;

    if (this.pack.synth) {
      this.playSynth(category, catDef);
    } else {
      this.playSample(category, catDef);
    }
  }

  playSynth(category, catDef) {
    const vol = this.getCategoryVolume(category, catDef.volume || 0.5);

    if (catDef.loop) {
      this.startLoop(category, catDef, vol);
      return;
    }

    const gain = this.ctx.createGain();
    gain.gain.value = vol;
    gain.connect(this.masterGain);

    const now = this.ctx.currentTime;
    const dur = catDef.duration || 0.1;

    switch (catDef.synth) {
      case 'sine':
      case 'saw': {
        const osc = this.ctx.createOscillator();
        osc.type = catDef.synth === 'saw' ? 'sawtooth' : 'sine';
        osc.frequency.value = catDef.freq || 440;
        osc.connect(gain);
        gain.gain.setValueAtTime(vol, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + dur);
        osc.start(now);
        osc.stop(now + dur);
        break;
      }
      case 'click': {
        const osc = this.ctx.createOscillator();
        osc.type = 'square';
        osc.frequency.value = catDef.freq || 800;
        osc.connect(gain);
        gain.gain.setValueAtTime(vol, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.03);
        osc.start(now);
        osc.stop(now + 0.05);
        break;
      }
      case 'chord': {
        const freqs = [catDef.freq, catDef.freq * 1.25, catDef.freq * 1.5];
        freqs.forEach(f => {
          const osc = this.ctx.createOscillator();
          osc.type = 'sine';
          osc.frequency.value = f;
          const g = this.ctx.createGain();
          g.gain.value = vol / freqs.length;
          g.connect(this.masterGain);
          g.gain.setValueAtTime(vol / freqs.length, now);
          g.gain.exponentialRampToValueAtTime(0.001, now + dur);
          osc.connect(g);
          osc.start(now);
          osc.stop(now + dur);
        });
        break;
      }
      case 'noise': {
        const bufferSize = this.ctx.sampleRate * dur;
        const buffer = this.ctx.createBuffer(1, bufferSize, this.ctx.sampleRate);
        const data = buffer.getChannelData(0);
        for (let i = 0; i < bufferSize; i++) {
          data[i] = Math.random() * 2 - 1;
        }
        const source = this.ctx.createBufferSource();
        source.buffer = buffer;
        source.connect(gain);
        gain.gain.setValueAtTime(vol, now);
        gain.gain.exponentialRampToValueAtTime(0.001, now + dur);
        source.start(now);
        break;
      }
    }
  }

  async playSample(category, catDef) {
    const vol = this.getCategoryVolume(category, catDef.volume || 0.5);
    const file = catDef.files[Math.floor(Math.random() * catDef.files.length)];
    const packName = this.pack.name.toLowerCase();
    const url = `/sounds/${packName}/${file}`;

    const resp = await fetch(url);
    const arrayBuf = await resp.arrayBuffer();
    const audioBuf = await this.ctx.decodeAudioData(arrayBuf);

    const source = this.ctx.createBufferSource();
    source.buffer = audioBuf;
    source.loop = catDef.loop || false;

    const gain = this.ctx.createGain();
    gain.gain.value = vol;
    source.connect(gain);
    gain.connect(this.masterGain);
    source.start();

    if (catDef.loop) {
      this.activeLoops[category] = { source, gain };
    }
  }

  startLoop(category, catDef, vol) {
    if (this.activeLoops[category]) return; // already looping

    const osc = this.ctx.createOscillator();
    osc.type = 'sine';
    osc.frequency.value = catDef.freq || 220;
    const gain = this.ctx.createGain();
    gain.gain.value = vol;
    osc.connect(gain);
    gain.connect(this.masterGain);
    osc.start();
    this.activeLoops[category] = { source: osc, gain };
  }

  stopAllLoops() {
    for (const [cat, loop] of Object.entries(this.activeLoops)) {
      loop.source.stop();
      delete this.activeLoops[cat];
    }
  }

  getCategoryVolume(category, packDefault) {
    return this.categoryVolumes[category] ?? packDefault;
  }

  setCategoryVolume(category, vol) {
    this.categoryVolumes[category] = vol;
    if (this.activeLoops[category]) {
      this.activeLoops[category].gain.gain.value = vol;
    }
  }

  setMasterVolume(vol) {
    this.masterVolume = vol;
    if (this.masterGain) {
      this.masterGain.gain.value = vol;
    }
  }

  toggleSessionMute(session) {
    if (this.mutedSessions.has(session)) {
      this.mutedSessions.delete(session);
    } else {
      this.mutedSessions.add(session);
    }
  }
}

export default BabbleAudio;
```

**Step 2: Commit**

```bash
git add cmd/web/js/
git commit -m "feat: add Web Audio API engine with synth and sample playback"
```

---

### Task 10: Browser UI

**Files:**
- Modify: `cmd/web/index.html`
- Create: `cmd/web/css/style.css`
- Create: `cmd/web/js/app.js`

**Step 1: Create the main UI**

Build the full browser interface with:
- Session sidebar (list of sessions with activity indicators, mute toggles, event rates)
- Event stream (scrolling log of events, color-coded by session)
- Bottom bar with category volume sliders
- Top bar with pack selector and settings
- WebSocket connection with auto-reconnect

The HTML should be a single `index.html` that imports `app.js` (which imports `audio.js`).

Key behaviors:
- On first click/interaction, initialize AudioContext (browser autoplay policy)
- Fetch pack list from `/api/packs` on load, let user select
- Load selected pack manifest from `/api/packs/{name}/manifest`
- Connect to WebSocket at `/ws`, parse incoming events
- For each event: add to event stream UI, play sound via audio engine
- Track sessions: add new sessions to sidebar, update event rates every second
- Session filtering: click a session to filter the event stream
- Category volumes: sliders in bottom bar, update audio engine in real-time

**Step 2: Create the CSS**

Minimal, dark-themed CSS optimized for a background ambient tool. Small viewport footprint. Monospace font for the event stream.

**Step 3: Build and test manually**

Run: `go build -o babble . && ./babble serve`
Verify: browser opens, sessions appear, events scroll, sounds play.

**Step 4: Commit**

```bash
git add cmd/web/
git commit -m "feat: add browser UI with session sidebar, event stream, and audio controls"
```

---

### Task 11: Configuration Persistence

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Write the test**

```go
// internal/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.Port != 3333 {
		t.Errorf("expected port 3333, got %d", cfg.Port)
	}
	if cfg.ActivePack != "default" {
		t.Errorf("expected pack 'default', got %q", cfg.ActivePack)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := Default()
	cfg.Port = 9999
	cfg.ActivePack = "ocean"

	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Port != 9999 {
		t.Errorf("expected port 9999, got %d", loaded.Port)
	}
	if loaded.ActivePack != "ocean" {
		t.Errorf("expected pack 'ocean', got %q", loaded.ActivePack)
	}
}

func TestLoadMissing(t *testing.T) {
	cfg, err := Load("/nonexistent/config.json")
	if err != nil {
		t.Fatal(err)
	}
	// Should return defaults
	if cfg.Port != 3333 {
		t.Errorf("expected default port 3333, got %d", cfg.Port)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`

**Step 3: Implement config**

```go
// internal/config/config.go
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	Port            int                `json:"port"`
	AutoOpen        bool               `json:"autoOpen"`
	ActivePack      string             `json:"activePack"`
	WatchPath       string             `json:"watchPath"`
	IdleTimeout     string             `json:"idleTimeout"`
	CategoryVolumes map[string]float64 `json:"categoryVolumes"`
	MutedSessions   []string           `json:"mutedSessions"`
	EventOverrides  map[string]string  `json:"eventOverrides"`
}

func Default() *Config {
	return &Config{
		Port:            3333,
		AutoOpen:        true,
		ActivePack:      "default",
		WatchPath:       "~/.claude/projects",
		IdleTimeout:     "5m",
		CategoryVolumes: map[string]float64{},
		MutedSessions:   []string{},
		EventOverrides:  map[string]string{},
	}
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "babble", "config.json")
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
```

**Step 4: Run tests**

Run: `go test ./internal/config/ -v`
Expected: all tests pass

**Step 5: Wire config into cmd/serve.go**

Load config on startup, use it for port/watchPath/activePack defaults, allow CLI flags to override.

**Step 6: Commit**

```bash
git add internal/config/ cmd/
git commit -m "feat: add configuration persistence to ~/.config/babble/config.json"
```

---

### Task 12: Config API Endpoints

**Files:**
- Create: `internal/server/config_handler.go`
- Modify: `internal/server/server.go`

**Step 1: Add GET/PUT /api/config endpoint**

The browser UI needs to read and update config (active pack, volumes, muted sessions). Add:
- `GET /api/config` — return current config as JSON
- `PUT /api/config` — update config, save to disk, return updated config

**Step 2: Register routes in server.go**

```go
mux.HandleFunc("GET /api/config", configHandler.HandleGet)
mux.HandleFunc("PUT /api/config", configHandler.HandleUpdate)
```

**Step 3: Build and verify**

Run: `curl http://localhost:3333/api/config`
Expected: JSON config object

**Step 4: Commit**

```bash
git add internal/server/
git commit -m "feat: add config API endpoints for browser UI"
```

---

### Task 13: End-to-End Integration Test

**Files:**
- Create: `integration_test.go`

**Step 1: Write an integration test**

```go
// integration_test.go
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dacort/babble/internal/events"
	"github.com/gorilla/websocket"
)

func TestEndToEnd(t *testing.T) {
	// Set up temp dirs
	tmpDir := t.TempDir()
	watchDir := filepath.Join(tmpDir, "projects", "-Users-test-src-myapp")
	os.MkdirAll(watchDir, 0755)
	packsDir := filepath.Join(tmpDir, "packs")
	os.MkdirAll(packsDir, 0755)

	// Start server on random port
	// ... (start server in goroutine, connect WebSocket, write JSONL line,
	//      verify event received over WebSocket)

	// Write a JSONL line
	sessionFile := filepath.Join(watchDir, "session.jsonl")
	f, _ := os.Create(sessionFile)
	time.Sleep(500 * time.Millisecond)

	line := `{"type":"assistant","sessionId":"test","cwd":"/Users/test/src/myapp","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"main.go"}}]},"timestamp":"2026-02-25T19:42:01Z"}` + "\n"
	f.WriteString(line)
	f.Sync()

	// Verify event arrives over WebSocket
	// ... (read message, assert category=write, event=Edit)
}
```

**Step 2: Run the integration test**

Run: `go test -v -run TestEndToEnd -timeout 30s`

**Step 3: Commit**

```bash
git add integration_test.go
git commit -m "test: add end-to-end integration test"
```

---

### Task 14: Polish and README

**Files:**
- Create: `README.md`
- Modify: `main.go` (add version flag)

**Step 1: Write README with quickstart**

Cover: what Babble is, how to install (`go install`), how to run (`babble serve`), how to create custom sound packs, screenshot of UI.

**Step 2: Add version/help flags**

**Step 3: Final build and test**

Run: `go test ./... -v && go build -o babble .`

**Step 4: Commit**

```bash
git add README.md main.go
git commit -m "docs: add README with quickstart and sound pack guide"
```
