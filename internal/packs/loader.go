// Package packs provides types and functions for loading sound pack manifests
// from disk.
package packs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CategorySound describes the sound configuration for a single event category.
// A pack is either file-based (Files populated) or synthesized (Synth populated).
type CategorySound struct {
	Files    []string `json:"files,omitempty"`
	Loop     bool     `json:"loop"`
	Volume   float64  `json:"volume"`
	Synth    string   `json:"synth,omitempty"`
	Freq     float64  `json:"freq,omitempty"`
	Duration float64  `json:"duration,omitempty"`
}

// Pack represents a sound pack manifest loaded from a pack.json file.
// Dir is the absolute path to the directory containing the pack; it is not
// serialized to JSON.
type Pack struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Author      string                   `json:"author"`
	Version     string                   `json:"version"`
	IsSynth     bool                     `json:"synth,omitempty"`
	Categories  map[string]CategorySound `json:"categories"`
	Dir         string                   `json:"-"`
}

// LoadPack reads and parses the pack.json file inside dir. It returns a
// pointer to the parsed Pack with its Dir field set to the absolute path of
// dir. An error is returned if the file cannot be read or if the JSON is
// malformed.
func LoadPack(dir string) (*Pack, error) {
	manifestPath := filepath.Join(dir, "pack.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("packs: read %s: %w", manifestPath, err)
	}

	var p Pack
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("packs: parse %s: %w", manifestPath, err)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("packs: abs path for %s: %w", dir, err)
	}
	p.Dir = abs

	return &p, nil
}

// ListPacks reads all subdirectories of baseDir and attempts to load each as
// a Pack. Subdirectories that do not contain a valid pack.json are silently
// skipped. An error is returned only if baseDir itself cannot be read.
func ListPacks(baseDir string) ([]*Pack, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("packs: read dir %s: %w", baseDir, err)
	}

	var result []*Pack
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		p, err := LoadPack(filepath.Join(baseDir, entry.Name()))
		if err != nil {
			// Skip packs that cannot be loaded without surfacing the error
			// to the caller — a missing or malformed pack.json is not fatal.
			continue
		}
		result = append(result, p)
	}

	return result, nil
}
