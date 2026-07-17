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
