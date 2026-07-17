package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

func TestProfileService_NewProfile(t *testing.T) {
	t.Parallel()
	svc := service.NewProfileService(t.TempDir(), false)

	dest := filepath.Join(t.TempDir(), "chatea-pro")

	res, err := svc.NewProfile(service.NewProfileInput{Name: "chatea-pro", Dir: dest})
	if err != nil {
		t.Fatalf("NewProfile: unexpected error: %v", err)
	}
	if res.Name != "chatea-pro" || res.Path != dest {
		t.Errorf("NewProfile: unexpected result: %+v", res)
	}

	if info, statErr := os.Stat(filepath.Join(dest, ".git")); statErr != nil || !info.IsDir() {
		t.Errorf("NewProfile: expected %s/.git to exist: %v", dest, statErr)
	}
	if _, statErr := os.Stat(res.ManifestPath); statErr != nil {
		t.Errorf("NewProfile: expected manifest at %s: %v", res.ManifestPath, statErr)
	}

	// NewProfile never touches the host-level store (profilesDir): List
	// (which reads profilesDir) still reports nothing installed.
	infos, err := svc.List()
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("NewProfile: expected the host-level store to remain untouched, List returned: %+v", infos)
	}
}

func TestProfileService_NewProfile_DestinationNotEmpty(t *testing.T) {
	t.Parallel()
	svc := service.NewProfileService(t.TempDir(), false)

	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	_, err := svc.NewProfile(service.NewProfileInput{Name: "chatea-pro", Dir: dest})
	if !errors.Is(err, model.ErrProfileExists) {
		t.Fatalf("NewProfile: expected model.ErrProfileExists, got %v", err)
	}
}

func TestProfileService_NewProfile_UnsafeName(t *testing.T) {
	t.Parallel()
	svc := service.NewProfileService(t.TempDir(), false)

	_, err := svc.NewProfile(service.NewProfileInput{Name: "../evil", Dir: filepath.Join(t.TempDir(), "dest")})
	if !errors.Is(err, model.ErrInvalidProfile) {
		t.Fatalf("NewProfile: expected model.ErrInvalidProfile, got %v", err)
	}
}

func TestProfileService_NewProfile_NameRequired(t *testing.T) {
	t.Parallel()
	svc := service.NewProfileService(t.TempDir(), false)

	if _, err := svc.NewProfile(service.NewProfileInput{Dir: t.TempDir()}); err == nil {
		t.Fatal("NewProfile: expected error when name is empty")
	}
}

func TestProfileService_NewProfile_DefaultDirFromCwd(t *testing.T) {
	svc := service.NewProfileService(t.TempDir(), false)

	cwd := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	// Re-read cwd through os.Getwd() (rather than trusting t.TempDir()'s
	// literal value) — on macOS /tmp is a symlink to /private/tmp, and
	// os.Getwd() reports the resolved path.
	resolvedCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after Chdir: %v", err)
	}

	res, err := svc.NewProfile(service.NewProfileInput{Name: "chatea-pro"})
	if err != nil {
		t.Fatalf("NewProfile: unexpected error: %v", err)
	}
	want := filepath.Join(resolvedCwd, "chatea-pro")
	if res.Path != want {
		t.Errorf("NewProfile: Path = %q, want %q", res.Path, want)
	}
}
