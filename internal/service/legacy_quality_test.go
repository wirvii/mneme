package service

import (
	"os"
	"path/filepath"
	"testing"
)

// writeLegacyQualityFile writes an empty file at repoRoot/.mneme/<name>,
// creating .mneme/ as needed.
func writeLegacyQualityFile(t *testing.T, repoRoot, name string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("schema_version = 1\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestLegacyQualityFiles covers all four presence combinations: neither
// file, only quality.toml, only quality-baseline.toml, both — and the
// absence of .mneme/ entirely (the common case for a repository that never
// ran the retired mechanism).
func TestLegacyQualityFiles(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, repoRoot string)
		want  []string
	}{
		{
			name:  "no .mneme directory at all",
			setup: func(t *testing.T, repoRoot string) {},
			want:  nil,
		},
		{
			name: "neither file present",
			setup: func(t *testing.T, repoRoot string) {
				if err := os.MkdirAll(filepath.Join(repoRoot, ".mneme"), 0o755); err != nil {
					t.Fatalf("mkdir .mneme: %v", err)
				}
			},
			want: nil,
		},
		{
			name: "only quality.toml",
			setup: func(t *testing.T, repoRoot string) {
				writeLegacyQualityFile(t, repoRoot, "quality.toml")
			},
			want: []string{".mneme/quality.toml"},
		},
		{
			name: "only quality-baseline.toml",
			setup: func(t *testing.T, repoRoot string) {
				writeLegacyQualityFile(t, repoRoot, "quality-baseline.toml")
			},
			want: []string{".mneme/quality-baseline.toml"},
		},
		{
			name: "both present, fixed order",
			setup: func(t *testing.T, repoRoot string) {
				writeLegacyQualityFile(t, repoRoot, "quality-baseline.toml")
				writeLegacyQualityFile(t, repoRoot, "quality.toml")
			},
			want: []string{".mneme/quality.toml", ".mneme/quality-baseline.toml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			tt.setup(t, repoRoot)

			got, err := LegacyQualityFiles(repoRoot)
			if err != nil {
				t.Fatalf("LegacyQualityFiles: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("LegacyQualityFiles() = %v, want %v", got, tt.want)
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("LegacyQualityFiles()[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

// TestRemoveLegacyQualityFiles confirms only the named paths are removed —
// the .mneme/ directory (and any OTHER file inside it, e.g. .gitignore)
// survives.
func TestRemoveLegacyQualityFiles(t *testing.T) {
	repoRoot := t.TempDir()
	writeLegacyQualityFile(t, repoRoot, "quality.toml")
	writeLegacyQualityFile(t, repoRoot, "quality-baseline.toml")
	sentinel := filepath.Join(repoRoot, ".mneme", ".gitignore")
	if err := os.WriteFile(sentinel, []byte("shared/\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	if err := RemoveLegacyQualityFiles(repoRoot, []string{".mneme/quality.toml"}); err != nil {
		t.Fatalf("RemoveLegacyQualityFiles: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repoRoot, ".mneme", "quality.toml")); !os.IsNotExist(err) {
		t.Errorf("quality.toml still exists after removal (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".mneme", "quality-baseline.toml")); err != nil {
		t.Errorf("quality-baseline.toml removed, want it untouched (err=%v)", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf(".gitignore removed, want it untouched (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".mneme")); err != nil {
		t.Errorf(".mneme/ directory itself removed, want it left standing (err=%v)", err)
	}
}

// TestInit_LegacyQualityFiles_AdvisoryFindings covers SPEC-158 AC15's
// service half: with both legacy files present, LegacyQualityFindings
// returns two findings naming their paths and never touches either file;
// with neither present, it returns zero findings.
func TestInit_LegacyQualityFiles_AdvisoryFindings(t *testing.T) {
	svc := newTestInitService(t)

	t.Run("both files present", func(t *testing.T) {
		repoRoot := t.TempDir()
		writeLegacyQualityFile(t, repoRoot, "quality.toml")
		writeLegacyQualityFile(t, repoRoot, "quality-baseline.toml")

		findings, err := svc.LegacyQualityFindings(repoRoot)
		if err != nil {
			t.Fatalf("LegacyQualityFindings: %v", err)
		}
		if len(findings) != 2 {
			t.Fatalf("len(findings) = %d, want 2: %+v", len(findings), findings)
		}
		wantFiles := map[string]bool{
			filepath.Join(repoRoot, ".mneme", "quality.toml"):          true,
			filepath.Join(repoRoot, ".mneme", "quality-baseline.toml"): true,
		}
		for _, f := range findings {
			if !wantFiles[f.File] {
				t.Errorf("unexpected finding file %q", f.File)
			}
		}

		// Byte-for-byte untouched: LegacyQualityFindings never writes or deletes.
		for _, rel := range []string{"quality.toml", "quality-baseline.toml"} {
			if _, err := os.Stat(filepath.Join(repoRoot, ".mneme", rel)); err != nil {
				t.Errorf("%s missing after LegacyQualityFindings, want it untouched (err=%v)", rel, err)
			}
		}
	})

	t.Run("no legacy files", func(t *testing.T) {
		repoRoot := t.TempDir()
		findings, err := svc.LegacyQualityFindings(repoRoot)
		if err != nil {
			t.Fatalf("LegacyQualityFindings: %v", err)
		}
		if len(findings) != 0 {
			t.Errorf("len(findings) = %d, want 0: %+v", len(findings), findings)
		}
	})
}
