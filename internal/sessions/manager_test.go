package sessions_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dacort/babble/internal/events"
	"github.com/dacort/babble/internal/sessions"
)

// bashLine returns a JSONL line that represents a Bash tool_use event in the
// given working directory. This mirrors real Claude Code session log format.
func bashLine(cwd string) string {
	return fmt.Sprintf(
		`{"type":"assistant","sessionId":"sess01","timestamp":"2024-01-01T00:00:00Z","cwd":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}`,
		cwd,
	) + "\n"
}

// systemLine returns a JSONL line that represents a system event.
func systemLine(cwd string) string {
	return fmt.Sprintf(
		`{"type":"system","sessionId":"sess01","timestamp":"2024-01-01T00:00:00Z","cwd":%q}`,
		cwd,
	) + "\n"
}

// skipLine returns a JSONL line that should be discarded (ErrSkipEvent).
func skipLine() string {
	return `{"type":"file-history-snapshot","sessionId":"skip01","timestamp":"2024-01-01T00:00:00Z","cwd":"/tmp/proj"}` + "\n"
}

// receiveWithin drains eventCh until it receives an event satisfying pred or
// the deadline elapses. It returns the matching event or nil on timeout.
func receiveWithin(t *testing.T, ch <-chan *events.BabbleEvent, pred func(*events.BabbleEvent) bool, d time.Duration) *events.BabbleEvent {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-ch:
			if pred(ev) {
				return ev
			}
		case <-deadline:
			return nil
		}
	}
}

// TestManagerDiscoversAndTailsSessions is the primary acceptance test.
// It creates a temp directory tree mimicking ~/.claude/projects/, starts the
// manager, writes a JSONL line, and verifies the parsed event arrives.
func TestManagerDiscoversAndTailsSessions(t *testing.T) {
	root := t.TempDir()

	// Create a project subdirectory mirroring the real layout.
	projectDir := filepath.Join(root, "myapp")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	// Create a session file.
	sessionFile := filepath.Join(projectDir, "session.jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatalf("create session file: %v", err)
	}
	f.Close()

	eventCh := make(chan *events.BabbleEvent, 32)
	m := sessions.NewManager(root, eventCh)

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.Start()
	}()

	// Give the manager a moment to discover the existing file and set up
	// inotify watches before we write the event line.
	time.Sleep(200 * time.Millisecond)

	// Append a Bash event line to the session file.
	f, err = os.OpenFile(sessionFile, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open session file for append: %v", err)
	}
	if _, err := f.WriteString(bashLine("/home/user/myapp")); err != nil {
		t.Fatalf("write to session file: %v", err)
	}
	f.Close()

	// The event should arrive within 2 seconds.
	ev := receiveWithin(t, eventCh, func(ev *events.BabbleEvent) bool {
		return ev.Event == "Bash"
	}, 2*time.Second)

	if ev == nil {
		t.Fatal("timed out waiting for Bash event")
	}
	if ev.Session != "myapp" {
		t.Errorf("session = %q, want %q", ev.Session, "myapp")
	}

	m.Stop()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start() returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Start() did not return after Stop()")
	}
}

// TestManagerTailsNewFile verifies that the manager picks up a JSONL file
// created after Start() is called (new session in an existing project dir).
func TestManagerTailsNewFile(t *testing.T) {
	root := t.TempDir()

	projectDir := filepath.Join(root, "newproject")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	eventCh := make(chan *events.BabbleEvent, 32)
	m := sessions.NewManager(root, eventCh)

	go m.Start() //nolint:errcheck
	defer m.Stop()

	time.Sleep(200 * time.Millisecond)

	// Create the JSONL file only after the manager is already watching.
	sessionFile := filepath.Join(projectDir, "new_session.jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Write a line immediately on create.
	if _, err := f.WriteString(bashLine("/home/user/newproject")); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	ev := receiveWithin(t, eventCh, func(ev *events.BabbleEvent) bool {
		return ev.Event == "Bash"
	}, 2*time.Second)

	if ev == nil {
		t.Fatal("timed out waiting for Bash event from new file")
	}
}

// TestManagerPicksUpNewProjectDir verifies that the manager watches for new
// project subdirectories added after Start(), and then tails their JSONL files.
func TestManagerPicksUpNewProjectDir(t *testing.T) {
	root := t.TempDir()

	eventCh := make(chan *events.BabbleEvent, 32)
	m := sessions.NewManager(root, eventCh)

	go m.Start() //nolint:errcheck
	defer m.Stop()

	time.Sleep(200 * time.Millisecond)

	// Create a brand-new project directory while the manager is running.
	projectDir := filepath.Join(root, "latecomer")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Now create a JSONL file in the new directory.
	sessionFile := filepath.Join(projectDir, "sess.jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.WriteString(bashLine("/home/user/latecomer")); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	ev := receiveWithin(t, eventCh, func(ev *events.BabbleEvent) bool {
		return ev.Event == "Bash"
	}, 2*time.Second)

	if ev == nil {
		t.Fatal("timed out waiting for Bash event from late project dir")
	}
}

// TestManagerSkipsSkipEvents verifies that ErrSkipEvent lines are silently
// discarded and do not appear on the event channel.
func TestManagerSkipsSkipEvents(t *testing.T) {
	root := t.TempDir()

	projectDir := filepath.Join(root, "skiptest")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionFile := filepath.Join(projectDir, "sess.jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.Close()

	eventCh := make(chan *events.BabbleEvent, 32)
	m := sessions.NewManager(root, eventCh)
	go m.Start() //nolint:errcheck
	defer m.Stop()

	time.Sleep(200 * time.Millisecond)

	f, err = os.OpenFile(sessionFile, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Write a skip line followed by a real line. We should only see the real one.
	f.WriteString(skipLine())       //nolint:errcheck
	f.WriteString(systemLine("/tmp/skiptest")) //nolint:errcheck
	f.Close()

	ev := receiveWithin(t, eventCh, func(ev *events.BabbleEvent) bool {
		// We should receive the system event, not a skip event.
		return ev.Event == "system"
	}, 2*time.Second)

	if ev == nil {
		t.Fatal("timed out waiting for system event after skip line")
	}

	// Ensure no unexpected extra events piled up from the skip line.
	// The channel should be empty (or contain only the system event we already consumed).
	select {
	case extra := <-eventCh:
		// Any extra event is fine as long as it is not from the skip line;
		// file-history-snapshot should never surface.
		if extra.Event == "file-history-snapshot" {
			t.Errorf("unexpected file-history-snapshot event on channel: %+v", extra)
		}
	default:
		// Channel empty — ideal.
	}
}

// TestManagerDoesNotTailSameFileTwice ensures that if a file is discovered
// both via glob and via fsnotify, it is only tailed once.
func TestManagerDoesNotTailSameFileTwice(t *testing.T) {
	root := t.TempDir()

	projectDir := filepath.Join(root, "dedup")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Pre-create the file with content so it's discovered on startup.
	sessionFile := filepath.Join(projectDir, "sess.jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.Close()

	eventCh := make(chan *events.BabbleEvent, 32)
	m := sessions.NewManager(root, eventCh)
	go m.Start() //nolint:errcheck
	defer m.Stop()

	time.Sleep(200 * time.Millisecond)

	// Write a single event.
	f, err = os.OpenFile(sessionFile, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.WriteString(bashLine("/home/user/dedup")) //nolint:errcheck
	f.Close()

	// Collect all events received within 500ms.
	var received []*events.BabbleEvent
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case ev := <-eventCh:
			received = append(received, ev)
		case <-timeout:
			goto done
		}
	}
done:
	// Should have received exactly one Bash event, not two.
	bashCount := 0
	for _, ev := range received {
		if ev.Event == "Bash" {
			bashCount++
		}
	}
	if bashCount != 1 {
		t.Errorf("received %d Bash events, want exactly 1 (dedup check)", bashCount)
	}
}
