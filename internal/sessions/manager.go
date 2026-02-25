// Package sessions watches a directory tree of Claude Code session logs
// (JSONL files) and emits parsed BabbleEvents as new lines are appended.
package sessions

import (
	"bufio"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/dacort/babble/internal/events"
)

// Manager watches a directory tree for JSONL session files and tails them,
// forwarding parsed BabbleEvents to the provided channel.
//
// Layout expected under watchPath:
//
//	watchPath/
//	  <project-name>/
//	    <session-id>.jsonl
//	    <session-id>/
//	      subagents/
//	        agent-<id>.jsonl
//	    ...
//
// Manager is safe to use from multiple goroutines — Stop may be called
// concurrently with Start.
type Manager struct {
	watchPath string
	eventCh   chan<- *events.BabbleEvent

	done chan struct{} // closed by Stop to signal all goroutines to exit

	mu      sync.Mutex
	tailing map[string]chan struct{} // path → per-file write-notify channel
}

// NewManager creates a Manager that watches watchPath and sends parsed events
// to eventCh.
func NewManager(watchPath string, eventCh chan<- *events.BabbleEvent) *Manager {
	return &Manager{
		watchPath: watchPath,
		eventCh:   eventCh,
		done:      make(chan struct{}),
		tailing:   make(map[string]chan struct{}),
	}
}

// Start begins watching for new and modified JSONL files. It blocks until Stop
// is called, then returns nil. Any watcher initialisation error is returned
// immediately.
func (m *Manager) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Watch the base directory so we notice new project subdirectories.
	if err := watcher.Add(m.watchPath); err != nil {
		return err
	}

	// Discover and watch existing project subdirectories and their JSONL files.
	if err := m.discoverExisting(watcher); err != nil {
		return err
	}

	// Event loop: handle fsnotify events until done is closed.
	for {
		select {
		case <-m.done:
			return nil

		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			m.handleFSEvent(watcher, ev)

		case fsErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("sessions: watcher error: %v", fsErr)
		}
	}
}

// Stop signals the manager to shut down. It is safe to call from any goroutine
// and may be called multiple times.
func (m *Manager) Stop() {
	select {
	case <-m.done:
		// Already stopped.
	default:
		close(m.done)
	}
}

// discoverExisting globs for existing project subdirectories and JSONL files.
func (m *Manager) discoverExisting(watcher *fsnotify.Watcher) error {
	entries, err := os.ReadDir(m.watchPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectDir := filepath.Join(m.watchPath, entry.Name())
		m.watchProjectDir(watcher, projectDir, true /* seekEnd */)
	}
	return nil
}

// watchProjectDir adds a project directory to the watcher and tails any
// existing JSONL files within it, including subagent files nested under
// {sessionId}/subagents/.
//
// seekEnd controls whether existing files are tailed from their current end
// (true = startup discovery; false = newly created directory).
func (m *Manager) watchProjectDir(watcher *fsnotify.Watcher, dir string, seekEnd bool) {
	if err := watcher.Add(dir); err != nil {
		log.Printf("sessions: watch %s: %v", dir, err)
		return
	}

	// Tail top-level JSONL files (main session logs).
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		log.Printf("sessions: glob %s: %v", dir, err)
		return
	}
	for _, path := range matches {
		m.startTailing(path, seekEnd)
	}

	// Also discover and watch subagent directories:
	// {projectDir}/{sessionId}/subagents/*.jsonl
	m.discoverSubagents(watcher, dir, seekEnd)
}

// discoverSubagents finds existing {sessionId}/subagents/ directories within
// a project dir and watches them for JSONL files.
func (m *Manager) discoverSubagents(watcher *fsnotify.Watcher, projectDir string, seekEnd bool) {
	// Look for {sessionId}/ directories (named like UUIDs).
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionDir := filepath.Join(projectDir, entry.Name())
		subagentDir := filepath.Join(sessionDir, "subagents")

		// Watch the session dir so we notice when subagents/ is created.
		watcher.Add(sessionDir)

		// If subagents/ already exists, watch it and tail its files.
		if isDir(subagentDir) {
			m.watchSubagentDir(watcher, subagentDir, seekEnd)
		}
	}
}

// watchSubagentDir watches a subagents/ directory and tails any JSONL files in it.
func (m *Manager) watchSubagentDir(watcher *fsnotify.Watcher, dir string, seekEnd bool) {
	if err := watcher.Add(dir); err != nil {
		log.Printf("sessions: watch subagents %s: %v", dir, err)
		return
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return
	}
	for _, path := range matches {
		m.startTailing(path, seekEnd)
	}
}

// handleFSEvent processes a single fsnotify event.
func (m *Manager) handleFSEvent(watcher *fsnotify.Watcher, ev fsnotify.Event) {
	path := ev.Name

	switch {
	// A new directory was created.
	case ev.Op.Has(fsnotify.Create) && isDir(path):
		base := filepath.Base(path)
		if base == "subagents" {
			// A subagents/ directory appeared inside a session dir.
			m.watchSubagentDir(watcher, path, false)
		} else if filepath.Base(filepath.Dir(path)) == m.watchDirBase() {
			// A new project directory appeared directly under watchPath.
			m.watchProjectDir(watcher, path, false)
		} else {
			// Could be a {sessionId}/ directory inside a project dir.
			// Watch it so we notice when subagents/ is created inside.
			watcher.Add(path)
			// Check if subagents/ already exists (MkdirAll can create both at once).
			subagentsDir := filepath.Join(path, "subagents")
			if isDir(subagentsDir) {
				m.watchSubagentDir(watcher, subagentsDir, false)
			}
		}

	// A new JSONL file appeared.
	case ev.Op.Has(fsnotify.Create) && isJSONL(path):
		// Read from beginning since content may have been written before we
		// process this event.
		m.startTailing(path, false)

	// An existing JSONL file was written to.
	case ev.Op.Has(fsnotify.Write) && isJSONL(path):
		// Ensure the file is being tailed. startTailing is a no-op if the
		// file is already tracked.
		m.startTailing(path, false)
		m.notifyWrite(path)
	}
}

// watchDirBase returns the base name of the watchPath for directory matching.
func (m *Manager) watchDirBase() string {
	return filepath.Base(m.watchPath)
}

// startTailing begins tailing path in a new goroutine. If path is already
// being tailed it returns immediately (deduplication). seekEnd controls
// whether the file is read from its current end (true) or from the beginning
// (false).
func (m *Manager) startTailing(path string, seekEnd bool) {
	m.mu.Lock()
	if _, ok := m.tailing[path]; ok {
		m.mu.Unlock()
		return
	}
	// Each tailer gets its own buffered write-notify channel.
	notifyCh := make(chan struct{}, 1)
	m.tailing[path] = notifyCh
	m.mu.Unlock()

	isSubagent := strings.Contains(path, "/subagents/")
	go m.tail(path, seekEnd, notifyCh, isSubagent)
}

// notifyWrite wakes up the tailer goroutine for path, if one exists.
func (m *Manager) notifyWrite(path string) {
	m.mu.Lock()
	notifyCh, ok := m.tailing[path]
	m.mu.Unlock()
	if !ok {
		return
	}
	select {
	case notifyCh <- struct{}{}:
	default:
		// Channel already has a pending notification; the tailer will wake up.
	}
}

// tail opens path and reads new lines as they are appended. When seekEnd is
// true the cursor is positioned at the current EOF before reading begins;
// when false the file is read from the start.
func (m *Manager) tail(path string, seekEnd bool, notifyCh <-chan struct{}, isSubagent bool) {
	defer func() {
		m.mu.Lock()
		delete(m.tailing, path)
		m.mu.Unlock()
	}()

	f, err := os.Open(path)
	if err != nil {
		log.Printf("sessions: open %s: %v", path, err)
		return
	}
	defer f.Close()

	if seekEnd {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			log.Printf("sessions: seek %s: %v", path, err)
			return
		}
	}

	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("sessions: read %s: %v", path, err)
				return
			}
			// Preserve any partial line accumulated before EOF.
			if len(line) > 0 {
				reader.Reset(io.MultiReader(strings.NewReader(string(line)), f))
			}
			// Block until the file is written to or shutdown is requested.
			select {
			case <-m.done:
				return
			case <-notifyCh:
				// New data may be available; retry the read.
			}
			continue
		}

		trimmed := strings.TrimRight(string(line), "\r\n")
		if trimmed == "" {
			continue
		}

		ev, parseErr := events.ParseLine([]byte(trimmed))
		if parseErr != nil {
			if errors.Is(parseErr, events.ErrSkipEvent) {
				continue
			}
			log.Printf("sessions: parse %s: %v", path, parseErr)
			continue
		}

		ev.IsSubagent = isSubagent

		select {
		case m.eventCh <- ev:
		case <-m.done:
			return
		}
	}
}

// isDir reports whether path currently exists as a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// isJSONL reports whether path has the .jsonl extension.
func isJSONL(path string) bool {
	return strings.HasSuffix(path, ".jsonl")
}
