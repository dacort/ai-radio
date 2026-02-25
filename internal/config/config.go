// Package config handles loading and saving babble's JSON configuration file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds all user-configurable settings for babble. Field names are
// kept in camelCase JSON to match the browser UI conventions.
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

// Default returns a *Config populated with the documented sentinel values.
// All map and slice fields are initialised to empty (non-nil) collections so
// that callers can safely range/index them without a nil check.
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

// DefaultPath returns the canonical location for the config file:
// ~/.config/babble/config.json. The leading ~ is not expanded; callers that
// need the real path should use os.UserHomeDir themselves.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to the tilde form; the caller can expand it later.
		return "~/.config/babble/config.json"
	}
	return filepath.Join(home, ".config", "babble", "config.json")
}

// Load reads the JSON file at path and unmarshals it over a set of defaults,
// so any field absent from the file retains its default value. If path does
// not exist, Load returns the defaults with a nil error — a missing config
// file is not an error condition.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	// Ensure collections are never nil after unmarshalling a JSON file that
	// omits them (e.g. `"categoryVolumes": null`).
	if cfg.CategoryVolumes == nil {
		cfg.CategoryVolumes = map[string]float64{}
	}
	if cfg.MutedSessions == nil {
		cfg.MutedSessions = []string{}
	}
	if cfg.EventOverrides == nil {
		cfg.EventOverrides = map[string]string{}
	}

	return cfg, nil
}

// Save serialises cfg as indented JSON and writes it to path, creating any
// missing parent directories with mode 0755. The file is written atomically
// from the Go perspective (os.WriteFile truncates then writes).
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", filepath.Dir(path), err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}

	return nil
}
