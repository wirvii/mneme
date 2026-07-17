package profile

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseManifest(t *testing.T) {
	data := []byte(`
name        = "chatea-pro"
version     = "3.1.0"
description = "Metodología de Chatea Pro"
extends     = "base-profile"

[compat]
mneme = ">=1.28.0"
`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: unexpected error: %v", err)
	}
	if m.Name != "chatea-pro" || m.Version != "3.1.0" || m.Description != "Metodología de Chatea Pro" {
		t.Errorf("unexpected manifest fields: %+v", m)
	}
	if m.Compat.Mneme != ">=1.28.0" {
		t.Errorf("Compat.Mneme = %q, want %q", m.Compat.Mneme, ">=1.28.0")
	}
	if m.Extends != "base-profile" {
		t.Errorf("Extends = %q, want %q", m.Extends, "base-profile")
	}
}

func TestParseManifest_MalformedTOML(t *testing.T) {
	if _, err := ParseManifest([]byte("this is not valid = = toml")); err == nil {
		t.Error("ParseManifest: expected error for malformed TOML")
	}
}

func TestParseManifestFile_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/" + ManifestFileName
	if err := os.WriteFile(path, []byte("this is not valid = = toml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ParseManifestFile(path); err == nil {
		t.Error("ParseManifestFile: expected error for malformed TOML")
	}
}

func TestParseManifestFile_NotFound(t *testing.T) {
	if _, err := ParseManifestFile("/nonexistent/path/mneme-profile.toml"); err == nil {
		t.Error("ParseManifestFile: expected error for nonexistent file")
	}
}

// TestParseManifestFS_ValidManifest verifies that ParseManifestFS reads and
// parses ManifestFileName from the root of an fs.FS — the accessor
// DefaultManifest (SPEC-096 §6) uses to read the embedded OSS default
// profile's identity.
func TestParseManifestFS_ValidManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/"+ManifestFileName, "name=\"mneme-default\"\nversion=\"1.0.0\"\n")

	m, err := ParseManifestFS(os.DirFS(dir))
	if err != nil {
		t.Fatalf("ParseManifestFS: unexpected error: %v", err)
	}
	if m.Name != "mneme-default" || m.Version != "1.0.0" {
		t.Errorf("unexpected manifest fields: %+v", m)
	}
}

// TestParseManifestFS_NotFound verifies that ParseManifestFS surfaces the
// underlying fs.ErrNotExist-wrapped error when the manifest is absent.
func TestParseManifestFS_NotFound(t *testing.T) {
	dir := t.TempDir()
	if _, err := ParseManifestFS(os.DirFS(dir)); err == nil {
		t.Error("ParseManifestFS: expected error for missing manifest")
	}
}

func TestManifest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		m       Manifest
		wantErr bool
	}{
		{"valid", Manifest{Name: "chatea-pro", Version: "1.0.0"}, false},
		{"missing name", Manifest{Version: "1.0.0"}, true},
		{"missing version", Manifest{Name: "chatea-pro"}, true},
		{"unsafe name traversal", Manifest{Name: "../evil", Version: "1.0.0"}, true},
		{"unsafe name slash", Manifest{Name: "a/b", Version: "1.0.0"}, true},
		{"unsafe name uppercase", Manifest{Name: "ChateaPro", Version: "1.0.0"}, true},
		{"extends set is still valid", Manifest{Name: "chatea-pro", Version: "1.0.0", Extends: "base"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Validate()
			if tt.wantErr && !errors.Is(err, ErrInvalidManifest) {
				t.Errorf("Validate() = %v, want ErrInvalidManifest", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestManifest_Warnings(t *testing.T) {
	m := Manifest{Name: "chatea-pro", Version: "1.0.0"}
	if warnings := m.Warnings(); len(warnings) != 0 {
		t.Errorf("Warnings() = %v, want empty", warnings)
	}

	m.Extends = "base-profile"
	warnings := m.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("Warnings() = %v, want 1 entry", warnings)
	}
	if !strings.Contains(warnings[0], "base-profile") {
		t.Errorf("Warnings()[0] = %q, want mention of base-profile", warnings[0])
	}
}

func TestManifest_CheckCompat(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		mneme      string
		wantOK     bool
	}{
		{"no constraint", "", "1.28.0", true},
		{"gte satisfied", ">=1.28.0", "1.28.0", true},
		{"gte satisfied higher", ">=1.28.0", "1.30.0", true},
		{"gte not satisfied", ">=1.28.0", "1.27.0", false},
		{"gt not satisfied equal", ">1.28.0", "1.28.0", false},
		{"gt satisfied", ">1.28.0", "1.28.1", true},
		{"exact match implicit", "1.28.0", "1.28.0", true},
		{"exact mismatch", "1.28.0", "1.29.0", false},
		{"lte satisfied", "<=2.0.0", "1.9.0", true},
		{"lt satisfied", "<2.0.0", "1.9.9", true},
		{"v-prefixed both sides", ">=v1.0.0", "v1.0.0", true},
		{"malformed constraint never blocks", ">=not-a-version", "1.28.0", true},
		{"malformed actual version never blocks", ">=1.0.0", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Manifest{Compat: ManifestCompat{Mneme: tt.constraint}}
			ok, msg := m.CheckCompat(tt.mneme)
			if ok != tt.wantOK {
				t.Errorf("CheckCompat(%q) against constraint %q = (%v, %q), want ok=%v",
					tt.mneme, tt.constraint, ok, msg, tt.wantOK)
			}
			if !ok && msg == "" {
				t.Error("CheckCompat: expected a non-empty message when ok=false")
			}
		})
	}
}
