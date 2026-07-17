package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePin(t *testing.T) {
	data := []byte(`
name     = "chatea-pro"
source   = "git@github.com:chateapro/mneme-profile.git"
ref      = "v3"
scaffold = "saas-multitenant"
`)
	p, err := ParsePin(data)
	if err != nil {
		t.Fatalf("ParsePin: unexpected error: %v", err)
	}
	if p.Name != "chatea-pro" || p.Source != "git@github.com:chateapro/mneme-profile.git" ||
		p.Ref != "v3" || p.Scaffold != "saas-multitenant" {
		t.Errorf("unexpected pin fields: %+v", p)
	}
	if p.IsDefault() {
		t.Error("IsDefault() = true, want false (Source is set)")
	}
}

func TestParsePin_DefaultProfile(t *testing.T) {
	p, err := ParsePin([]byte(`name = "internal-default"`))
	if err != nil {
		t.Fatalf("ParsePin: unexpected error: %v", err)
	}
	if !p.IsDefault() {
		t.Error("IsDefault() = false, want true (no Source)")
	}
}

func TestPin_Validate(t *testing.T) {
	tests := []struct {
		name    string
		p       Pin
		wantErr bool
	}{
		{"valid, default", Pin{Name: "chatea-pro"}, false},
		{"valid, with source", Pin{Name: "chatea-pro", Source: "git@github.com:x/y.git"}, false},
		{"empty name", Pin{}, true},
		{"traversal", Pin{Name: "../evil"}, true},
		{"embedded slash", Pin{Name: "a/b"}, true},
		{"uppercase", Pin{Name: "ChateaPro"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.Validate()
			if tt.wantErr && !errors.Is(err, ErrInvalidPin) {
				t.Errorf("Validate() = %v, want ErrInvalidPin", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestPin_Warnings(t *testing.T) {
	// No source at all: no warning (default profile, nothing to pin a ref to).
	p := Pin{Name: "chatea-pro"}
	if warnings := p.Warnings(); len(warnings) != 0 {
		t.Errorf("Warnings() = %v, want empty", warnings)
	}

	// Source without ref: warns about non-reproducibility.
	p = Pin{Name: "chatea-pro", Source: "git@github.com:x/y.git"}
	if warnings := p.Warnings(); len(warnings) != 1 {
		t.Errorf("Warnings() = %v, want 1 entry", warnings)
	}

	// Source with ref: no warning.
	p = Pin{Name: "chatea-pro", Source: "git@github.com:x/y.git", Ref: "v3"}
	if warnings := p.Warnings(); len(warnings) != 0 {
		t.Errorf("Warnings() = %v, want empty", warnings)
	}
}

func TestParsePinFile_NotFound(t *testing.T) {
	if _, err := ParsePinFile("/nonexistent/path/.mneme-profile"); err == nil {
		t.Error("ParsePinFile: expected error for nonexistent file")
	}
}

func TestParsePinFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PinFileName)
	if err := os.WriteFile(path, []byte(`name = "chatea-pro"`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	p, err := ParsePinFile(path)
	if err != nil {
		t.Fatalf("ParsePinFile: unexpected error: %v", err)
	}
	if p.Name != "chatea-pro" {
		t.Errorf("Name = %q, want %q", p.Name, "chatea-pro")
	}
}

func TestParsePin_MalformedTOML(t *testing.T) {
	if _, err := ParsePin([]byte("this is not valid = = toml")); err == nil {
		t.Error("ParsePin: expected error for malformed TOML")
	}
}

func TestParsePinFile_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PinFileName)
	if err := os.WriteFile(path, []byte("this is not valid = = toml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ParsePinFile(path); err == nil {
		t.Error("ParsePinFile: expected error for malformed TOML")
	}
}

func TestWritePin_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	pin := &Pin{Name: "chatea-pro", Source: "git@github.com:x/y.git", Ref: "v3"}

	if err := WritePin(dir, pin); err != nil {
		t.Fatalf("WritePin: unexpected error: %v", err)
	}

	got, err := ParsePinFile(filepath.Join(dir, PinFileName))
	if err != nil {
		t.Fatalf("ParsePinFile: unexpected error: %v", err)
	}
	if *got != *pin {
		t.Errorf("round-trip pin = %+v, want %+v", got, pin)
	}

	// Atomic write: no leftover .tmp file.
	if _, err := os.Stat(filepath.Join(dir, PinFileName+".tmp")); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file, stat err = %v", err)
	}
}

func TestWritePin_RejectsInvalidPinBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	pin := &Pin{Name: "../evil"}

	if err := WritePin(dir, pin); !errors.Is(err, ErrInvalidPin) {
		t.Errorf("WritePin: err = %v, want ErrInvalidPin", err)
	}

	if _, err := os.Stat(filepath.Join(dir, PinFileName)); !os.IsNotExist(err) {
		t.Errorf("expected no pin file written, stat err = %v", err)
	}
}

func TestWritePin_NilPin(t *testing.T) {
	if err := WritePin(t.TempDir(), nil); !errors.Is(err, ErrInvalidPin) {
		t.Errorf("WritePin(nil): err = %v, want ErrInvalidPin", err)
	}
}

func TestWritePin_PreservesExistingScaffold(t *testing.T) {
	dir := t.TempDir()
	existing := &Pin{Name: "old-profile", Scaffold: "saas-multitenant"}
	if err := WritePin(dir, existing); err != nil {
		t.Fatalf("WritePin (seed): unexpected error: %v", err)
	}

	replacement := &Pin{Name: "chatea-pro", Source: "git@github.com:x/y.git", Ref: "v3"}
	if err := WritePin(dir, replacement); err != nil {
		t.Fatalf("WritePin (replacement): unexpected error: %v", err)
	}

	got, err := ParsePinFile(filepath.Join(dir, PinFileName))
	if err != nil {
		t.Fatalf("ParsePinFile: unexpected error: %v", err)
	}
	if got.Name != "chatea-pro" || got.Scaffold != "saas-multitenant" {
		t.Errorf("got = %+v, want Name=chatea-pro Scaffold=saas-multitenant preserved", got)
	}
}

func TestWritePin_WriteTempFailure(t *testing.T) {
	nonexistentDir := filepath.Join(t.TempDir(), "does-not-exist")
	pin := &Pin{Name: "chatea-pro"}

	if err := WritePin(nonexistentDir, pin); err == nil {
		t.Error("WritePin: expected error when projectRoot does not exist")
	}
}

func TestWritePin_RenameFailure(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the destination AS A DIRECTORY: os.Rename(tmpFile, existingDir)
	// always fails (EISDIR on Unix), deterministically exercising the rename
	// error branch without any error-injection framework.
	if err := os.MkdirAll(filepath.Join(dir, PinFileName), 0o755); err != nil {
		t.Fatalf("seed conflicting directory: %v", err)
	}

	pin := &Pin{Name: "chatea-pro"}
	if err := WritePin(dir, pin); err == nil {
		t.Error("WritePin: expected error when the destination is an existing directory")
	}
	// The temp file must not be left behind on failure.
	if _, err := os.Stat(filepath.Join(dir, PinFileName+".tmp")); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file, stat err = %v", err)
	}
}

func TestWritePin_ExplicitScaffoldWins(t *testing.T) {
	dir := t.TempDir()
	existing := &Pin{Name: "old-profile", Scaffold: "saas-multitenant"}
	if err := WritePin(dir, existing); err != nil {
		t.Fatalf("WritePin (seed): unexpected error: %v", err)
	}

	replacement := &Pin{Name: "chatea-pro", Scaffold: "monorepo-turbo"}
	if err := WritePin(dir, replacement); err != nil {
		t.Fatalf("WritePin (replacement): unexpected error: %v", err)
	}

	got, err := ParsePinFile(filepath.Join(dir, PinFileName))
	if err != nil {
		t.Fatalf("ParsePinFile: unexpected error: %v", err)
	}
	if got.Scaffold != "monorepo-turbo" {
		t.Errorf("Scaffold = %q, want explicit value %q to win", got.Scaffold, "monorepo-turbo")
	}
}
