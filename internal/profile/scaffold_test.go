package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderManifest_RoundTrip(t *testing.T) {
	in := Manifest{
		Name:        "chatea-pro",
		Version:     "0.1.0",
		Description: "Metodología de Chatea Pro",
		Compat:      ManifestCompat{Mneme: ">=1.28.0"},
	}

	data, err := RenderManifest(in)
	if err != nil {
		t.Fatalf("RenderManifest: unexpected error: %v", err)
	}

	out, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest(RenderManifest(in)): unexpected error: %v", err)
	}

	if out.Name != in.Name || out.Version != in.Version || out.Description != in.Description || out.Compat.Mneme != in.Compat.Mneme {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestRenderManifest_InvalidName(t *testing.T) {
	_, err := RenderManifest(Manifest{Name: "../evil", Version: "0.1.0"})
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("RenderManifest: expected ErrInvalidManifest, got %v", err)
	}
}

func TestRenderManifest_MissingFields(t *testing.T) {
	if _, err := RenderManifest(Manifest{}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("RenderManifest: expected ErrInvalidManifest for empty manifest, got %v", err)
	}
}

func TestScaffold_CreatesExpectedTree(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "chatea-pro")

	if err := Scaffold(dest, ScaffoldInput{Name: "chatea-pro"}); err != nil {
		t.Fatalf("Scaffold: unexpected error: %v", err)
	}

	// Directories + their .gitkeep.
	for _, dir := range []string{
		agentsSubdir,
		skillsSubdir,
		blocksSubdir,
		templatesSubdir,
		filepath.Join("scaffolds", "_blueprints"),
	} {
		full := filepath.Join(dest, dir)
		info, err := os.Stat(full)
		if err != nil || !info.IsDir() {
			t.Fatalf("Scaffold: expected directory %s to exist: %v", full, err)
		}
		if _, err := os.Stat(filepath.Join(full, ".gitkeep")); err != nil {
			t.Errorf("Scaffold: expected .gitkeep in %s: %v", full, err)
		}
	}

	// Files.
	for _, name := range []string{rulesFileName, modelsFileName, policyFileName, "README.md", ManifestFileName} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Errorf("Scaffold: expected file %s to exist: %v", name, err)
		}
	}

	// The manifest parses and declares the requested name.
	m, err := ParseManifestFile(filepath.Join(dest, ManifestFileName))
	if err != nil {
		t.Fatalf("ParseManifestFile: unexpected error: %v", err)
	}
	if m.Name != "chatea-pro" {
		t.Errorf("manifest.Name = %q, want %q", m.Name, "chatea-pro")
	}
	if err := m.Validate(); err != nil {
		t.Errorf("scaffolded manifest failed Validate: %v", err)
	}

	// git init ran.
	if info, err := os.Stat(filepath.Join(dest, ".git")); err != nil || !info.IsDir() {
		t.Errorf("Scaffold: expected %s/.git to exist: %v", dest, err)
	}
}

func TestScaffold_DestinationNotEmpty(t *testing.T) {
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	err := Scaffold(dest, ScaffoldInput{Name: "chatea-pro"})
	if !errors.Is(err, ErrProfileExists) {
		t.Fatalf("Scaffold: expected ErrProfileExists, got %v", err)
	}

	// Nothing else was written alongside the pre-existing file.
	entries, readErr := os.ReadDir(dest)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Errorf("Scaffold: destination was modified despite non-empty check: %+v", entries)
	}
}

func TestScaffold_EmptyExistingDestinationSucceeds(t *testing.T) {
	dest := t.TempDir() // t.TempDir() is guaranteed empty.

	if err := Scaffold(dest, ScaffoldInput{Name: "chatea-pro"}); err != nil {
		t.Fatalf("Scaffold: unexpected error for empty existing destination: %v", err)
	}
}

func TestScaffold_UnsafeName(t *testing.T) {
	tests := []string{"../evil", "a/b", "", "Chatea-Pro"}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "dest")

			err := Scaffold(dest, ScaffoldInput{Name: name})
			if !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("Scaffold(%q): expected ErrInvalidManifest, got %v", name, err)
			}

			if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
				t.Errorf("Scaffold(%q): expected destination to remain untouched, stat err = %v", name, statErr)
			}
		})
	}
}
