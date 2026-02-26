package events_test

import (
	"errors"
	"testing"

	"github.com/dacort/babble/internal/events"
)

// TestParseAssistantToolUse verifies that an assistant message containing a
// tool_use block for the Edit tool is classified as category write and that
// the detail is extracted from the file_path input field.
func TestParseAssistantToolUse(t *testing.T) {
	line := []byte(`{
		"type": "assistant",
		"sessionId": "abc123",
		"timestamp": "2024-01-01T00:00:00Z",
		"cwd": "/home/user/myproject",
		"message": {
			"role": "assistant",
			"content": [
				{
					"type": "tool_use",
					"name": "Edit",
					"input": {
						"file_path": "/home/user/myproject/main.go",
						"old_string": "foo",
						"new_string": "bar"
					}
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryWrite {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryWrite)
	}
	if ev.Detail != "/home/user/myproject/main.go" {
		t.Errorf("detail = %q, want %q", ev.Detail, "/home/user/myproject/main.go")
	}
	if ev.Event != "Edit" {
		t.Errorf("event = %q, want %q", ev.Event, "Edit")
	}
	if ev.Session != "myproject" {
		t.Errorf("session = %q, want %q", ev.Session, "myproject")
	}
	if ev.SessionID != "abc123" {
		t.Errorf("sessionId = %q, want %q", ev.SessionID, "abc123")
	}
}

// TestParseAssistantThinking verifies that a thinking block in an assistant
// message is classified as category ambient.
func TestParseAssistantThinking(t *testing.T) {
	line := []byte(`{
		"type": "assistant",
		"sessionId": "def456",
		"timestamp": "2024-01-01T00:01:00Z",
		"cwd": "/home/user/project",
		"message": {
			"role": "assistant",
			"content": [
				{
					"type": "thinking",
					"thinking": "Let me think about this problem..."
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryAmbient {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryAmbient)
	}
	if ev.Event != "thinking" {
		t.Errorf("event = %q, want %q", ev.Event, "thinking")
	}
}

// TestParseToolResultError verifies that a user message containing a
// tool_result block with is_error=true is classified as category error.
func TestParseToolResultError(t *testing.T) {
	line := []byte(`{
		"type": "user",
		"sessionId": "ghi789",
		"timestamp": "2024-01-01T00:02:00Z",
		"cwd": "/home/user/project",
		"message": {
			"role": "user",
			"content": [
				{
					"type": "tool_result",
					"tool_use_id": "toolu_01",
					"is_error": true,
					"content": [{"type": "text", "text": "command not found: foo"}]
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryError {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryError)
	}
	if ev.Event != "tool_result" {
		t.Errorf("event = %q, want %q", ev.Event, "tool_result")
	}
}

// TestParseUserInput verifies that a user message (human turn) is classified
// as category warn (waiting for/receiving input).
func TestParseUserInput(t *testing.T) {
	line := []byte(`{
		"type": "user",
		"sessionId": "jkl012",
		"timestamp": "2024-01-01T00:03:00Z",
		"cwd": "/home/user/project",
		"message": {
			"role": "user",
			"content": [
				{
					"type": "text",
					"text": "Please fix the bug"
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryWarn {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryWarn)
	}
	if ev.Event != "user_input" {
		t.Errorf("event = %q, want %q", ev.Event, "user_input")
	}
}

// TestParseProgress verifies that a progress event is classified as category meta.
func TestParseProgress(t *testing.T) {
	line := []byte(`{
		"type": "progress",
		"sessionId": "mno345",
		"timestamp": "2024-01-01T00:04:00Z",
		"cwd": "/home/user/project",
		"data": {
			"type": "hook_progress",
			"detail": "running pre-commit hook"
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryInit {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryInit)
	}
	if ev.Event != "session_start" {
		t.Errorf("event = %q, want %q", ev.Event, "session_start")
	}
}

// TestParseSkipsFileHistorySnapshot verifies that file-history-snapshot events
// return ErrSkipEvent so callers know to discard them.
func TestParseSkipsFileHistorySnapshot(t *testing.T) {
	line := []byte(`{
		"type": "file-history-snapshot",
		"sessionId": "pqr678",
		"timestamp": "2024-01-01T00:05:00Z",
		"cwd": "/home/user/project"
	}`)

	ev, err := events.ParseLine(line)
	if !errors.Is(err, events.ErrSkipEvent) {
		t.Errorf("expected ErrSkipEvent, got err=%v ev=%v", err, ev)
	}
	if ev != nil {
		t.Errorf("expected nil event for skipped line, got %+v", ev)
	}
}

// TestSessionNameFromCwd verifies that the session name is derived from the
// last component of the cwd path.
func TestSessionNameFromCwd(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{"unix absolute path", "/home/user/myproject", "myproject"},
		{"nested path", "/Users/alice/code/babble", "babble"},
		{"single component", "/rootdir", "rootdir"},
		{"trailing slash", "/home/user/myproject/", "myproject"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := events.SessionNameFromCwd(tc.cwd)
			if got != tc.want {
				t.Errorf("SessionNameFromCwd(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

// TestDetailTruncation verifies that details longer than 80 characters are
// truncated to exactly 80 characters.
func TestDetailTruncation(t *testing.T) {
	longPath := "/home/user/" + string(make([]byte, 100))
	for i := range longPath[11:] {
		_ = i // fill with zeros which are valid bytes
	}
	// Build a path that is definitely > 80 chars
	longPath = "/home/user/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/file.go"

	line := []byte(`{
		"type": "assistant",
		"sessionId": "trunc01",
		"timestamp": "2024-01-01T00:06:00Z",
		"cwd": "/home/user/project",
		"message": {
			"role": "assistant",
			"content": [
				{
					"type": "tool_use",
					"name": "Read",
					"input": {
						"file_path": "` + longPath + `"
					}
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ev.Detail) > 80 {
		t.Errorf("detail length = %d, want <= 80; detail = %q", len(ev.Detail), ev.Detail)
	}
}

// TestParseBashToolUse verifies that a Bash tool_use block is classified as
// category action and the detail is the command field.
func TestParseBashToolUse(t *testing.T) {
	line := []byte(`{
		"type": "assistant",
		"sessionId": "bash01",
		"timestamp": "2024-01-01T00:07:00Z",
		"cwd": "/home/user/project",
		"message": {
			"role": "assistant",
			"content": [
				{
					"type": "tool_use",
					"name": "Bash",
					"input": {
						"command": "go test ./..."
					}
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryAction {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryAction)
	}
	if ev.Detail != "go test ./..." {
		t.Errorf("detail = %q, want %q", ev.Detail, "go test ./...")
	}
}

// TestParseToolResultSuccess verifies that a successful tool_result is
// classified as category success.
func TestParseToolResultSuccess(t *testing.T) {
	line := []byte(`{
		"type": "user",
		"sessionId": "suc01",
		"timestamp": "2024-01-01T00:08:00Z",
		"cwd": "/home/user/project",
		"message": {
			"role": "user",
			"content": [
				{
					"type": "tool_result",
					"tool_use_id": "toolu_02",
					"is_error": false,
					"content": [{"type": "text", "text": "ok"}]
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategorySuccess {
		t.Errorf("category = %q, want %q", ev.Category, events.CategorySuccess)
	}
}

// TestParseWebFetch verifies that a WebFetch tool_use is classified as network
// with the url as detail.
func TestParseWebFetch(t *testing.T) {
	line := []byte(`{
		"type": "assistant",
		"sessionId": "net01",
		"timestamp": "2024-01-01T00:09:00Z",
		"cwd": "/home/user/project",
		"message": {
			"role": "assistant",
			"content": [
				{
					"type": "tool_use",
					"name": "WebFetch",
					"input": {
						"url": "https://pkg.go.dev/net/http",
						"prompt": "what is the Handler interface"
					}
				}
			]
		}
	}`)

	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryNetwork {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryNetwork)
	}
	if ev.Detail != "https://pkg.go.dev/net/http" {
		t.Errorf("detail = %q, want %q", ev.Detail, "https://pkg.go.dev/net/http")
	}
}

// TestParseProgressWithSubagentMessageSkipped verifies that progress events
// relaying subagent activity (data.message present) are skipped to avoid
// double-counting with direct subagent file tailing.
func TestParseProgressWithSubagentMessageSkipped(t *testing.T) {
	line := []byte(`{"type":"progress","sessionId":"abc","cwd":"/tmp","data":{"message":{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}}}`)
	_, err := events.ParseLine(line)
	if !errors.Is(err, events.ErrSkipEvent) {
		t.Errorf("expected ErrSkipEvent for progress with subagent message, got %v", err)
	}
}

// TestParseProgressWithoutMessageNotSkipped verifies that regular progress
// events (like hook_progress) are NOT skipped.
func TestParseProgressWithoutMessageNotSkipped(t *testing.T) {
	line := []byte(`{"type":"progress","sessionId":"abc","cwd":"/tmp","data":{"type":"hook_progress"}}`)
	ev, err := events.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Category != events.CategoryInit {
		t.Errorf("category = %q, want %q", ev.Category, events.CategoryInit)
	}
}
