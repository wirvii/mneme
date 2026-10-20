package service

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeLegacyProfileFile writes a small file at repoRoot/<rel>, creating
// parent directories as needed.
func writeLegacyProfileFile(t *testing.T, repoRoot, rel string) {
	t.Helper()
	path := filepath.Join(repoRoot, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestLegacyProfileFiles covers every presence combination of the pin and the
// lock, including a repository with no .mneme/ at all.
func TestLegacyProfileFiles(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, repoRoot string)
		want  []string
	}{
		{"nothing present", func(t *testing.T, repoRoot string) {}, nil},
		{"only the pin", func(t *testing.T, repoRoot string) {
			writeLegacyProfileFile(t, repoRoot, ".mneme-profile")
		}, []string{".mneme-profile"}},
		{"only the lock", func(t *testing.T, repoRoot string) {
			writeLegacyProfileFile(t, repoRoot, ".mneme/profile.lock")
		}, []string{".mneme/profile.lock"}},
		{"both, fixed order", func(t *testing.T, repoRoot string) {
			writeLegacyProfileFile(t, repoRoot, ".mneme/profile.lock")
			writeLegacyProfileFile(t, repoRoot, ".mneme-profile")
		}, []string{".mneme-profile", ".mneme/profile.lock"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			tt.setup(t, repoRoot)
			got, err := LegacyProfileFiles(repoRoot)
			if err != nil {
				t.Fatalf("LegacyProfileFiles: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("LegacyProfileFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLegacyProfileStoreAndBackups checks the two directory probes report
// existence without creating anything.
func TestLegacyProfileStoreAndBackups(t *testing.T) {
	dataDir := t.TempDir()
	path, exists, err := LegacyProfileStore(dataDir)
	if err != nil || exists {
		t.Fatalf("LegacyProfileStore on empty dir = (%q, %v, %v), want not-exists", path, exists, err)
	}
	if path != filepath.Join(dataDir, "profiles") {
		t.Errorf("store path = %q, want <dataDir>/profiles", path)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "profiles", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, exists, err = LegacyProfileStore(dataDir); err != nil || !exists {
		t.Errorf("LegacyProfileStore after mkdir = (%v, %v), want exists", exists, err)
	}

	repoRoot := t.TempDir()
	if _, exists, err = LegacyProfileBackups(repoRoot); err != nil || exists {
		t.Errorf("LegacyProfileBackups on empty repo = (%v, %v), want not-exists", exists, err)
	}
	writeLegacyProfileFile(t, repoRoot, ".mneme/backups/x")
	if _, exists, err = LegacyProfileBackups(repoRoot); err != nil || !exists {
		t.Errorf("LegacyProfileBackups after seeding = (%v, %v), want exists", exists, err)
	}
}

// TestRemoveLegacyProfileArtifacts proves it removes exactly the named files
// and the store directory and nothing else: .mneme/ and .mneme/backups/ stay.
func TestRemoveLegacyProfileArtifacts(t *testing.T) {
	repoRoot := t.TempDir()
	dataDir := t.TempDir()
	writeLegacyProfileFile(t, repoRoot, ".mneme-profile")
	writeLegacyProfileFile(t, repoRoot, ".mneme/profile.lock")
	writeLegacyProfileFile(t, repoRoot, ".mneme/backups/x")
	writeLegacyProfileFile(t, dataDir, "profiles/acme/mneme-profile.toml")

	storeDir, _, err := LegacyProfileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveLegacyProfileArtifacts(repoRoot, []string{".mneme-profile", ".mneme/profile.lock"}, storeDir); err != nil {
		t.Fatalf("RemoveLegacyProfileArtifacts: %v", err)
	}

	for _, gone := range []string{
		filepath.Join(repoRoot, ".mneme-profile"),
		filepath.Join(repoRoot, ".mneme", "profile.lock"),
		storeDir,
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be gone (err=%v)", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".mneme", "backups", "x")); err != nil {
		t.Errorf(".mneme/backups/x must survive (err=%v)", err)
	}
}

// TestRemoveLegacyProfileArtifacts_MissingFileErrors keeps the failure
// visible instead of swallowing it.
func TestRemoveLegacyProfileArtifacts_MissingFileErrors(t *testing.T) {
	if err := RemoveLegacyProfileArtifacts(t.TempDir(), []string{".mneme-profile"}, ""); err == nil {
		t.Fatal("expected an error removing a file that does not exist")
	}
}

// TestInit_LegacyProfileFindings covers SPEC-162 C7's service half: one
// advisory finding per trace, and nothing is touched.
func TestInit_LegacyProfileFindings(t *testing.T) {
	svc := newTestInitService(t)

	t.Run("pin, lock and store present", func(t *testing.T) {
		repoRoot := t.TempDir()
		dataDir := t.TempDir()
		writeLegacyProfileFile(t, repoRoot, ".mneme-profile")
		writeLegacyProfileFile(t, repoRoot, ".mneme/profile.lock")
		writeLegacyProfileFile(t, dataDir, "profiles/acme/mneme-profile.toml")

		findings, err := svc.LegacyProfileFindings(repoRoot, dataDir)
		if err != nil {
			t.Fatalf("LegacyProfileFindings: %v", err)
		}
		var files []string
		for _, f := range findings {
			files = append(files, f.File)
		}
		slices.Sort(files)
		want := []string{
			filepath.Join(dataDir, "profiles"),
			filepath.Join(repoRoot, ".mneme", "profile.lock"),
			filepath.Join(repoRoot, ".mneme-profile"),
		}
		slices.Sort(want)
		if !slices.Equal(files, want) {
			t.Errorf("finding files = %v, want %v", files, want)
		}
		for _, p := range want {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s missing after LegacyProfileFindings, want it untouched (err=%v)", p, err)
			}
		}
	})

	t.Run("nothing present", func(t *testing.T) {
		findings, err := svc.LegacyProfileFindings(t.TempDir(), t.TempDir())
		if err != nil {
			t.Fatalf("LegacyProfileFindings: %v", err)
		}
		if len(findings) != 0 {
			t.Errorf("findings = %+v, want none", findings)
		}
	})
}
