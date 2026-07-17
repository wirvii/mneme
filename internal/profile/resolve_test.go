package profile

import (
	"os"
	"path/filepath"
	"testing"
)

// writePin writes a .mneme-profile pin file at projectRoot's root.
func writePin(t *testing.T, projectRoot, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectRoot, PinFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}
}

func TestResolvePin_Absent(t *testing.T) {
	s := NewStore(t.TempDir())
	res, err := s.ResolvePin(t.TempDir())
	if err != nil {
		t.Fatalf("ResolvePin: unexpected error: %v", err)
	}
	if res.State != PinAbsent {
		t.Errorf("State = %v, want PinAbsent", res.State)
	}
	if res.Pin != nil {
		t.Errorf("Pin = %+v, want nil", res.Pin)
	}
}

func TestResolvePin_Default(t *testing.T) {
	projectRoot := t.TempDir()
	writePin(t, projectRoot, `name = "internal-default"`)

	s := NewStore(t.TempDir())
	res, err := s.ResolvePin(projectRoot)
	if err != nil {
		t.Fatalf("ResolvePin: unexpected error: %v", err)
	}
	if res.State != PinDefault {
		t.Errorf("State = %v, want PinDefault", res.State)
	}
	if res.Pin == nil || res.Pin.Name != "internal-default" {
		t.Errorf("Pin = %+v", res.Pin)
	}
}

func TestResolvePin_Installed(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	profilesDir := t.TempDir()
	s := NewStore(profilesDir)
	if _, err := s.Add(source, "", "", false); err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	projectRoot := t.TempDir()
	writePin(t, projectRoot, `
name   = "chatea-pro"
source = "`+source+`"
ref    = "v1"
`)

	res, err := s.ResolvePin(projectRoot)
	if err != nil {
		t.Fatalf("ResolvePin: unexpected error: %v", err)
	}
	if res.State != PinInstalled {
		t.Errorf("State = %v, want PinInstalled", res.State)
	}
	if res.Manifest == nil || res.Manifest.Version != "1.0.0" {
		t.Errorf("Manifest = %+v", res.Manifest)
	}
	if res.Path != filepath.Join(profilesDir, "chatea-pro") {
		t.Errorf("Path = %q", res.Path)
	}
}

func TestResolvePin_Missing(t *testing.T) {
	projectRoot := t.TempDir()
	writePin(t, projectRoot, `
name   = "not-installed"
source = "git@github.com:someone/not-installed.git"
`)

	s := NewStore(t.TempDir())
	res, err := s.ResolvePin(projectRoot)
	if err != nil {
		t.Fatalf("ResolvePin: unexpected error: %v", err)
	}
	if res.State != PinMissing {
		t.Errorf("State = %v, want PinMissing", res.State)
	}
	if res.Pin == nil || res.Pin.Name != "not-installed" {
		t.Errorf("Pin = %+v", res.Pin)
	}
}

func TestResolvePin_InvalidPin(t *testing.T) {
	projectRoot := t.TempDir()
	writePin(t, projectRoot, `name = "../evil"`)

	s := NewStore(t.TempDir())
	if _, err := s.ResolvePin(projectRoot); err == nil {
		t.Error("ResolvePin: expected error for invalid pin")
	}
}

func TestPinState_String(t *testing.T) {
	tests := map[PinState]string{
		PinAbsent:    "absent",
		PinDefault:   "default",
		PinInstalled: "installed",
		PinMissing:   "missing",
		PinState(99): "unknown",
	}
	for state, want := range tests {
		if got := state.String(); got != want {
			t.Errorf("PinState(%d).String() = %q, want %q", state, got, want)
		}
	}
}
