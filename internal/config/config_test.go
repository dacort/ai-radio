package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dacort/babble/internal/config"
)

// TestLoadDefaultConfig verifies that Default() returns the documented
// sentinel values and that all map/slice fields are initialised (not nil).
func TestLoadDefaultConfig(t *testing.T) {
	cfg := config.Default()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, 3333},
		{"AutoOpen", cfg.AutoOpen, true},
		{"ActivePack", cfg.ActivePack, "default"},
		{"WatchPath", cfg.WatchPath, "~/.claude/projects"},
		{"IdleTimeout", cfg.IdleTimeout, "5m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}

	t.Run("CategoryVolumes not nil", func(t *testing.T) {
		if cfg.CategoryVolumes == nil {
			t.Error("CategoryVolumes should be an empty map, got nil")
		}
	})

	t.Run("MutedSessions not nil", func(t *testing.T) {
		if cfg.MutedSessions == nil {
			t.Error("MutedSessions should be an empty slice, got nil")
		}
	})

	t.Run("EventOverrides not nil", func(t *testing.T) {
		if cfg.EventOverrides == nil {
			t.Error("EventOverrides should be an empty map, got nil")
		}
	})
}

// TestSaveAndLoad round-trips a Config through Save then Load and verifies
// that all fields survive the JSON serialisation cycle.
func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.json")

	original := &config.Config{
		Port:        4444,
		AutoOpen:    false,
		ActivePack:  "retro",
		WatchPath:   "/tmp/projects",
		IdleTimeout: "10m",
		CategoryVolumes: map[string]float64{
			"tool_use": 0.8,
			"thinking": 0.5,
		},
		MutedSessions: []string{"session-abc", "session-xyz"},
		EventOverrides: map[string]string{
			"tool_use": "ping.mp3",
		},
	}

	if err := config.Save(original, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	t.Run("Port", func(t *testing.T) {
		if loaded.Port != original.Port {
			t.Errorf("got %d, want %d", loaded.Port, original.Port)
		}
	})
	t.Run("AutoOpen", func(t *testing.T) {
		if loaded.AutoOpen != original.AutoOpen {
			t.Errorf("got %v, want %v", loaded.AutoOpen, original.AutoOpen)
		}
	})
	t.Run("ActivePack", func(t *testing.T) {
		if loaded.ActivePack != original.ActivePack {
			t.Errorf("got %q, want %q", loaded.ActivePack, original.ActivePack)
		}
	})
	t.Run("WatchPath", func(t *testing.T) {
		if loaded.WatchPath != original.WatchPath {
			t.Errorf("got %q, want %q", loaded.WatchPath, original.WatchPath)
		}
	})
	t.Run("IdleTimeout", func(t *testing.T) {
		if loaded.IdleTimeout != original.IdleTimeout {
			t.Errorf("got %q, want %q", loaded.IdleTimeout, original.IdleTimeout)
		}
	})
	t.Run("CategoryVolumes", func(t *testing.T) {
		for k, want := range original.CategoryVolumes {
			if got := loaded.CategoryVolumes[k]; got != want {
				t.Errorf("CategoryVolumes[%q] = %v, want %v", k, got, want)
			}
		}
	})
	t.Run("MutedSessions", func(t *testing.T) {
		if len(loaded.MutedSessions) != len(original.MutedSessions) {
			t.Fatalf("len(MutedSessions) = %d, want %d", len(loaded.MutedSessions), len(original.MutedSessions))
		}
		for i, s := range original.MutedSessions {
			if loaded.MutedSessions[i] != s {
				t.Errorf("MutedSessions[%d] = %q, want %q", i, loaded.MutedSessions[i], s)
			}
		}
	})
	t.Run("EventOverrides", func(t *testing.T) {
		for k, want := range original.EventOverrides {
			if got := loaded.EventOverrides[k]; got != want {
				t.Errorf("EventOverrides[%q] = %q, want %q", k, got, want)
			}
		}
	})
}

// TestSaveCreatesParentDirs verifies that Save creates missing intermediate
// directories rather than returning an error.
func TestSaveCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	// Three levels deep; none exist yet.
	path := filepath.Join(dir, "a", "b", "c", "config.json")

	if err := config.Save(config.Default(), path); err != nil {
		t.Fatalf("Save with missing parent dirs: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("config file not created: %v", err)
	}
}

// TestLoadMissing verifies that Load returns defaults (not an error) when the
// target file does not exist.
func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}

	def := config.Default()
	if cfg.Port != def.Port {
		t.Errorf("Port: got %d, want %d", cfg.Port, def.Port)
	}
	if cfg.ActivePack != def.ActivePack {
		t.Errorf("ActivePack: got %q, want %q", cfg.ActivePack, def.ActivePack)
	}
}

// TestDefaultPath verifies that DefaultPath returns a non-empty string ending
// in config.json.
func TestDefaultPath(t *testing.T) {
	p := config.DefaultPath()
	if p == "" {
		t.Fatal("DefaultPath returned empty string")
	}
	base := filepath.Base(p)
	if base != "config.json" {
		t.Errorf("DefaultPath base = %q, want \"config.json\"", base)
	}
}
