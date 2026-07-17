package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestActiveSource_String(t *testing.T) {
	tests := []struct {
		s    ActiveSource
		want string
	}{
		{SourceVanilla, "vanilla"},
		{SourcePin, "pin"},
		{SourceGlobalDefault, "global-default"},
		{ActiveSource(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

// TestResolveActive_PinWinsIgnoringDefault covers AC6's headline invariant:
// a pin present in the project — even with a global default set — always
// wins, and the default is never consulted (pure replacement, decision #5).
func TestResolveActive_PinWinsIgnoringDefault(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	profilesDir := t.TempDir()
	s := NewStore(profilesDir)
	if _, err := s.Add(source, "", "v1", false); err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	// A DIFFERENT profile is the global default; it must be entirely ignored
	// once a pin is present, even though it is (or is not) itself installed.
	projectRoot := t.TempDir()
	pin := "name   = \"chatea-pro\"\nsource = \"" + source + "\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, PinFileName), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	res, err := s.ResolveActive(projectRoot, "some-other-default-not-installed")
	if err != nil {
		t.Fatalf("ResolveActive: unexpected error: %v", err)
	}
	if res.Source != SourcePin {
		t.Errorf("Source = %v, want SourcePin", res.Source)
	}
	if res.Resolution.State != PinInstalled {
		t.Errorf("State = %v, want PinInstalled", res.Resolution.State)
	}
	if res.Resolution.Pin == nil || res.Resolution.Pin.Name != "chatea-pro" {
		t.Errorf("Pin = %+v, want chatea-pro", res.Resolution.Pin)
	}
}

// TestResolveActive_GlobalDefaultInstalled covers the no-pin,
// default-in-store branch.
func TestResolveActive_GlobalDefaultInstalled(t *testing.T) {
	source := newFixtureRepo(t, "acme", "2.0.0")
	profilesDir := t.TempDir()
	s := NewStore(profilesDir)
	if _, err := s.Add(source, "", "v1", false); err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	projectRoot := t.TempDir() // no pin at all

	res, err := s.ResolveActive(projectRoot, "acme")
	if err != nil {
		t.Fatalf("ResolveActive: unexpected error: %v", err)
	}
	if res.Source != SourceGlobalDefault {
		t.Errorf("Source = %v, want SourceGlobalDefault", res.Source)
	}
	if res.Resolution.State != PinInstalled {
		t.Errorf("State = %v, want PinInstalled", res.Resolution.State)
	}
	if res.Resolution.Manifest == nil || res.Resolution.Manifest.Version != "2.0.0" {
		t.Errorf("Manifest = %+v", res.Resolution.Manifest)
	}
	if res.Resolution.Pin == nil || res.Resolution.Pin.Name != "acme" {
		t.Errorf("Pin = %+v, want acme", res.Resolution.Pin)
	}
}

// TestResolveActive_GlobalDefaultMissing covers the no-pin,
// default-names-an-absent-profile branch.
func TestResolveActive_GlobalDefaultMissing(t *testing.T) {
	s := NewStore(t.TempDir())
	projectRoot := t.TempDir()

	res, err := s.ResolveActive(projectRoot, "nonexistent")
	if err != nil {
		t.Fatalf("ResolveActive: unexpected error: %v", err)
	}
	if res.Source != SourceGlobalDefault {
		t.Errorf("Source = %v, want SourceGlobalDefault", res.Source)
	}
	if res.Resolution.State != PinMissing {
		t.Errorf("State = %v, want PinMissing", res.Resolution.State)
	}
	if res.Resolution.Pin == nil || res.Resolution.Pin.Name != "nonexistent" {
		t.Errorf("Pin = %+v, want nonexistent", res.Resolution.Pin)
	}
}

// TestResolveActive_Vanilla covers the no-pin, no-default branch.
func TestResolveActive_Vanilla(t *testing.T) {
	s := NewStore(t.TempDir())
	projectRoot := t.TempDir()

	res, err := s.ResolveActive(projectRoot, "")
	if err != nil {
		t.Fatalf("ResolveActive: unexpected error: %v", err)
	}
	if res.Source != SourceVanilla {
		t.Errorf("Source = %v, want SourceVanilla", res.Source)
	}
	if res.Resolution.State != PinAbsent {
		t.Errorf("State = %v, want PinAbsent", res.Resolution.State)
	}
}

// TestResolveActive_InvalidGlobalDefaultName covers the defense-in-depth
// safe-slug re-validation on globalDefault (mirrors profilePath's own
// R2 guard) — an unsafe name must error, never reach filepath.Join.
func TestResolveActive_InvalidGlobalDefaultName(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.ResolveActive(t.TempDir(), "../evil"); err == nil {
		t.Error("ResolveActive: expected error for unsafe global default name")
	}
}
