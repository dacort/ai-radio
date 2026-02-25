// Package events provides types and parsing logic for Claude Code session logs
// stored in JSONL format.
package events

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
)

// Category classifies a BabbleEvent into a display bucket.
type Category string

const (
	// CategoryAmbient covers thinking and plain text blocks.
	CategoryAmbient Category = "ambient"
	// CategoryAction covers Bash tool use.
	CategoryAction Category = "action"
	// CategoryRead covers Read, Grep, and Glob tool use.
	CategoryRead Category = "read"
	// CategoryWrite covers Edit, Write, and NotebookEdit tool use.
	CategoryWrite Category = "write"
	// CategoryNetwork covers WebFetch and WebSearch tool use.
	CategoryNetwork Category = "network"
	// CategorySuccess covers tool_result blocks that succeeded.
	CategorySuccess Category = "success"
	// CategoryWarn covers AskUserQuestion and human user input turns.
	CategoryWarn Category = "warn"
	// CategoryError covers tool_result blocks with is_error=true.
	CategoryError Category = "error"
	// CategoryMeta covers Task, session lifecycle, and progress events.
	CategoryMeta Category = "meta"
)

// ErrSkipEvent is returned by ParseLine for events that carry no useful
// information for the UI (e.g. file-history-snapshot).
var ErrSkipEvent = errors.New("skip event")

// BabbleEvent is the normalised representation of a single log line.
type BabbleEvent struct {
	Session    string   `json:"session"`
	SessionID  string   `json:"sessionId"`
	Category   Category `json:"category"`
	Event      string   `json:"event"`
	Detail     string   `json:"detail"`
	Timestamp  string   `json:"timestamp"`
	IsSubagent bool     `json:"isSubagent,omitempty"`
}

// -----------------------------------------------------------------------------
// Raw JSONL shapes — used only during parsing.
// -----------------------------------------------------------------------------

// rawLine is the top-level envelope of every JSONL record.
type rawLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Message   *rawMessage     `json:"message"`
	Data      *rawProgressData `json:"data"`
}

// rawMessage represents the message field present on assistant and user events.
type rawMessage struct {
	Role    string       `json:"role"`
	Content []rawContent `json:"content"`
}

// rawContent represents a single element in the content array.
type rawContent struct {
	Type      string          `json:"type"`
	// tool_use fields
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result fields
	IsError bool `json:"is_error"`
}

// rawProgressData is the data object inside progress events.
type rawProgressData struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"` // non-nil when relaying subagent activity
}

// -----------------------------------------------------------------------------
// Category and detail lookup tables.
// -----------------------------------------------------------------------------

// toolCategory maps tool names to their display category.
var toolCategory = map[string]Category{
	"Read":          CategoryRead,
	"Grep":          CategoryRead,
	"Glob":          CategoryRead,
	"Edit":          CategoryWrite,
	"Write":         CategoryWrite,
	"NotebookEdit":  CategoryWrite,
	"Bash":          CategoryAction,
	"WebFetch":      CategoryNetwork,
	"WebSearch":     CategoryNetwork,
	"Task":          CategoryMeta,
	"EnterPlanMode": CategoryMeta,
	"ExitPlanMode":  CategoryMeta,
	"Skill":         CategoryMeta,
	"TodoWrite":     CategoryMeta,
	"TaskCreate":    CategoryMeta,
	"TaskUpdate":    CategoryMeta,
	"AskUserQuestion": CategoryWarn,
}

// toolDetailKey maps tool names to the JSON key inside the input object that
// contains the most useful human-readable detail.
var toolDetailKey = map[string]string{
	"Read":        "file_path",
	"Edit":        "file_path",
	"Write":       "file_path",
	"NotebookEdit": "notebook_path",
	"Grep":        "pattern",
	"Glob":        "pattern",
	"Bash":        "command",
	"WebFetch":    "url",
	"WebSearch":   "query",
	"Task":        "description",
}

// skippedTypes is the set of top-level event types that carry no UI value.
var skippedTypes = map[string]bool{
	"file-history-snapshot": true,
}

// -----------------------------------------------------------------------------
// Public API.
// -----------------------------------------------------------------------------

// ParseLine parses a single JSONL line from a Claude Code session log and
// returns a BabbleEvent. It returns (nil, ErrSkipEvent) for events that should
// be discarded by the caller, and a non-nil error for malformed input.
func ParseLine(line []byte) (*BabbleEvent, error) {
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	// Discard known-uninteresting event types.
	if skippedTypes[raw.Type] {
		return nil, ErrSkipEvent
	}

	ev := &BabbleEvent{
		Session:   SessionNameFromCwd(raw.Cwd),
		SessionID: raw.SessionID,
		Timestamp: raw.Timestamp,
	}

	switch raw.Type {
	case "assistant":
		return parseAssistant(ev, raw.Message)

	case "user":
		return parseUser(ev, raw.Message)

	case "progress":
		// Progress events with data.message are the main session relaying
		// subagent activity. We skip these because we tail subagent JSONL
		// files directly, which avoids double-counting.
		if raw.Data != nil && raw.Data.Message != nil {
			return nil, ErrSkipEvent
		}
		ev.Category = CategoryMeta
		ev.Event = "progress"
		if raw.Data != nil && raw.Data.Type != "" {
			ev.Detail = truncate(raw.Data.Type, 80)
		}
		return ev, nil

	case "system":
		ev.Category = CategoryMeta
		ev.Event = "system"
		return ev, nil

	default:
		// Unknown top-level type — treat as meta so it surfaces rather than
		// silently disappearing.
		ev.Category = CategoryMeta
		ev.Event = raw.Type
		return ev, nil
	}
}

// SessionNameFromCwd returns the last non-empty path component of cwd, which
// is used as the human-readable session name shown in the UI.
func SessionNameFromCwd(cwd string) string {
	// path.Base handles trailing slashes and returns the last component.
	// We use path (not filepath) because the cwd from Claude Code logs is
	// always in Unix format regardless of the host OS.
	cleaned := strings.TrimRight(cwd, "/")
	if cleaned == "" {
		return cwd
	}
	return path.Base(cleaned)
}

// -----------------------------------------------------------------------------
// Internal helpers.
// -----------------------------------------------------------------------------

// parseAssistant handles type=assistant lines.
func parseAssistant(ev *BabbleEvent, msg *rawMessage) (*BabbleEvent, error) {
	if msg == nil || len(msg.Content) == 0 {
		ev.Category = CategoryAmbient
		ev.Event = "assistant"
		return ev, nil
	}

	// Use the first interesting content block to classify the event.
	// If there are multiple blocks we prefer tool_use over thinking/text.
	for _, block := range msg.Content {
		switch block.Type {
		case "tool_use":
			return classifyToolUse(ev, block)
		}
	}

	// No tool_use — fall back to the first block type.
	first := msg.Content[0]
	switch first.Type {
	case "thinking":
		ev.Category = CategoryAmbient
		ev.Event = "thinking"
	case "text":
		ev.Category = CategoryAmbient
		ev.Event = "text"
	default:
		ev.Category = CategoryAmbient
		ev.Event = first.Type
	}
	return ev, nil
}

// classifyToolUse maps a tool_use content block to category + detail.
func classifyToolUse(ev *BabbleEvent, block rawContent) (*BabbleEvent, error) {
	ev.Event = block.Name

	if cat, ok := toolCategory[block.Name]; ok {
		ev.Category = cat
	} else {
		ev.Category = CategoryMeta
	}

	// Extract the detail value from the input JSON object.
	if key, ok := toolDetailKey[block.Name]; ok {
		var input map[string]json.RawMessage
		if err := json.Unmarshal(block.Input, &input); err == nil {
			if raw, exists := input[key]; exists {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					ev.Detail = truncate(s, 80)
				}
			}
		}
	}

	return ev, nil
}

// parseUser handles type=user lines.
func parseUser(ev *BabbleEvent, msg *rawMessage) (*BabbleEvent, error) {
	if msg == nil || len(msg.Content) == 0 {
		ev.Category = CategoryWarn
		ev.Event = "user_input"
		return ev, nil
	}

	// Scan for tool_result blocks first — they take precedence.
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			ev.Event = "tool_result"
			if block.IsError {
				ev.Category = CategoryError
			} else {
				ev.Category = CategorySuccess
			}
			return ev, nil
		}
	}

	// Plain user turn (human input).
	ev.Category = CategoryWarn
	ev.Event = "user_input"
	return ev, nil
}

// truncate returns s truncated to at most maxLen runes.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
