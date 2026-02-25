package packs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dacort/babble/internal/packs"
)

// writePack writes a pack.json to dir with the given pack data.
func writePack(t *testing.T, dir string, p packs.Pack) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), data, 0o644); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}
}

func TestLoadPack(t *testing.T) {
	t.Run("valid synth pack", func(t *testing.T) {
		dir := t.TempDir()
		want := packs.Pack{
			Name:        "TestPack",
			Description: "A test pack",
			Author:      "tester",
			Version:     "1.0.0",
			IsSynth:     true,
			Categories: map[string]packs.CategorySound{
				"ambient": {
					Synth:    "sine",
					Freq:     220,
					Duration: 2.0,
					Loop:     true,
					Volume:   0.15,
				},
				"action": {
					Synth:    "click",
					Freq:     800,
					Duration: 0.05,
					Loop:     false,
					Volume:   0.4,
				},
			},
		}
		writePack(t, dir, want)

		got, err := packs.LoadPack(dir)
		if err != nil {
			t.Fatalf("LoadPack: %v", err)
		}

		if got.Name != want.Name {
			t.Errorf("Name: got %q, want %q", got.Name, want.Name)
		}
		if got.Description != want.Description {
			t.Errorf("Description: got %q, want %q", got.Description, want.Description)
		}
		if got.Author != want.Author {
			t.Errorf("Author: got %q, want %q", got.Author, want.Author)
		}
		if got.Version != want.Version {
			t.Errorf("Version: got %q, want %q", got.Version, want.Version)
		}
		if got.IsSynth != want.IsSynth {
			t.Errorf("IsSynth: got %v, want %v", got.IsSynth, want.IsSynth)
		}
		if got.Dir != dir {
			t.Errorf("Dir: got %q, want %q", got.Dir, dir)
		}
		if len(got.Categories) != len(want.Categories) {
			t.Errorf("Categories len: got %d, want %d", len(got.Categories), len(want.Categories))
		}
		ambient := got.Categories["ambient"]
		if ambient.Synth != "sine" {
			t.Errorf("ambient.Synth: got %q, want %q", ambient.Synth, "sine")
		}
		if ambient.Freq != 220 {
			t.Errorf("ambient.Freq: got %v, want %v", ambient.Freq, 220)
		}
		if !ambient.Loop {
			t.Errorf("ambient.Loop: got false, want true")
		}
	})

	t.Run("valid file-based pack", func(t *testing.T) {
		dir := t.TempDir()

		// Create fake audio files.
		audioFile := filepath.Join(dir, "ambient.ogg")
		if err := os.WriteFile(audioFile, []byte("fake audio"), 0o644); err != nil {
			t.Fatalf("write audio file: %v", err)
		}

		want := packs.Pack{
			Name:    "FilePack",
			Version: "2.0.0",
			Categories: map[string]packs.CategorySound{
				"ambient": {
					Files:  []string{"ambient.ogg"},
					Loop:   true,
					Volume: 0.5,
				},
			},
		}
		writePack(t, dir, want)

		got, err := packs.LoadPack(dir)
		if err != nil {
			t.Fatalf("LoadPack: %v", err)
		}
		if got.Dir != dir {
			t.Errorf("Dir: got %q, want %q", got.Dir, dir)
		}
		files := got.Categories["ambient"].Files
		if len(files) != 1 || files[0] != "ambient.ogg" {
			t.Errorf("Files: got %v, want [ambient.ogg]", files)
		}
	})

	t.Run("missing pack.json", func(t *testing.T) {
		dir := t.TempDir()
		_, err := packs.LoadPack(dir)
		if err == nil {
			t.Fatal("expected error for missing pack.json, got nil")
		}
	})

	t.Run("malformed pack.json", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte("{not valid json"), 0o644); err != nil {
			t.Fatalf("write bad pack.json: %v", err)
		}
		_, err := packs.LoadPack(dir)
		if err == nil {
			t.Fatal("expected error for malformed pack.json, got nil")
		}
	})

	t.Run("dir does not exist", func(t *testing.T) {
		_, err := packs.LoadPack("/nonexistent/path/that/does/not/exist")
		if err == nil {
			t.Fatal("expected error for nonexistent dir, got nil")
		}
	})
}

func TestListPacks(t *testing.T) {
	t.Run("two valid packs", func(t *testing.T) {
		baseDir := t.TempDir()

		for _, name := range []string{"pack-a", "pack-b"} {
			dir := filepath.Join(baseDir, name)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
			writePack(t, dir, packs.Pack{
				Name:    name,
				Version: "1.0.0",
				Categories: map[string]packs.CategorySound{
					"ambient": {Synth: "sine", Freq: 440, Volume: 0.5},
				},
			})
		}

		got, err := packs.ListPacks(baseDir)
		if err != nil {
			t.Fatalf("ListPacks: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("len: got %d, want 2", len(got))
		}

		names := make(map[string]bool)
		for _, p := range got {
			names[p.Name] = true
		}
		if !names["pack-a"] {
			t.Error("missing pack-a")
		}
		if !names["pack-b"] {
			t.Error("missing pack-b")
		}
	})

	t.Run("invalid subdirs are skipped", func(t *testing.T) {
		baseDir := t.TempDir()

		// Valid pack.
		validDir := filepath.Join(baseDir, "good")
		if err := os.Mkdir(validDir, 0o755); err != nil {
			t.Fatalf("mkdir good: %v", err)
		}
		writePack(t, validDir, packs.Pack{
			Name:    "good",
			Version: "1.0.0",
			Categories: map[string]packs.CategorySound{
				"ambient": {Synth: "sine", Freq: 440, Volume: 0.5},
			},
		})

		// Invalid pack (no pack.json).
		badDir := filepath.Join(baseDir, "bad")
		if err := os.Mkdir(badDir, 0o755); err != nil {
			t.Fatalf("mkdir bad: %v", err)
		}

		got, err := packs.ListPacks(baseDir)
		if err != nil {
			t.Fatalf("ListPacks: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("len: got %d, want 1", len(got))
		}
		if got[0].Name != "good" {
			t.Errorf("Name: got %q, want %q", got[0].Name, "good")
		}
	})

	t.Run("empty base dir", func(t *testing.T) {
		baseDir := t.TempDir()
		got, err := packs.ListPacks(baseDir)
		if err != nil {
			t.Fatalf("ListPacks: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len: got %d, want 0", len(got))
		}
	})

	t.Run("base dir does not exist", func(t *testing.T) {
		_, err := packs.ListPacks("/nonexistent/base/dir")
		if err == nil {
			t.Fatal("expected error for nonexistent base dir, got nil")
		}
	})
}
