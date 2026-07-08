package subagents

import (
	"os"
	"path/filepath"
	"testing"
)

// mkfile creates an empty file at path, creating parent directories as
// needed. Test helper only.
func mkfile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindProjectRoot_PriorityOrder(t *testing.T) {
	t.Run("monorepo marker wins over strong marker higher up and weak marker deeper", func(t *testing.T) {
		root := t.TempDir()
		mkfile(t, filepath.Join(root, "go.mod")) // strong marker, would win if alone
		sub := filepath.Join(root, "packages", "app")
		mkfile(t, filepath.Join(sub, "package.json")) // weak marker
		mkfile(t, filepath.Join(root, "turbo.json"))  // monorepo marker, must win

		got, found := findProjectRoot(sub)
		if !found {
			t.Fatal("expected root to be found")
		}
		if want := root; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("strong marker wins over weak marker at same or deeper level", func(t *testing.T) {
		root := t.TempDir()
		mkfile(t, filepath.Join(root, ".git", "HEAD"))
		mkfile(t, filepath.Join(root, "package.json"))

		got, found := findProjectRoot(root)
		if !found {
			t.Fatal("expected root to be found")
		}
		if got != root {
			t.Errorf("got %q, want %q", got, root)
		}
	})

	t.Run("weak marker records highest ancestor with package.json", func(t *testing.T) {
		root := t.TempDir()
		mkfile(t, filepath.Join(root, "package.json"))
		sub := filepath.Join(root, "packages", "app")
		mkfile(t, filepath.Join(sub, "package.json"))

		got, found := findProjectRoot(sub)
		if !found {
			t.Fatal("expected root to be found")
		}
		if got != root {
			t.Errorf("got %q, want %q (highest ancestor with package.json)", got, root)
		}
	})

	t.Run("no markers found returns false", func(t *testing.T) {
		root := t.TempDir()
		sub := filepath.Join(root, "a", "b", "c")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		_, found := findProjectRoot(sub)
		if found {
			t.Error("expected no root to be found")
		}
	})

	t.Run("empty dir returns false", func(t *testing.T) {
		_, found := findProjectRoot("")
		if found {
			t.Error("expected false for empty dir")
		}
	})

	t.Run("each strong marker is individually authoritative", func(t *testing.T) {
		for _, marker := range strongProjectMarkers {
			t.Run(marker, func(t *testing.T) {
				root := t.TempDir()
				mkfile(t, filepath.Join(root, marker))

				got, found := findProjectRoot(root)
				if !found || got != root {
					t.Errorf("marker %q: got (%q, %v), want (%q, true)", marker, got, found, root)
				}
			})
		}
	})

	t.Run("each monorepo marker is individually authoritative", func(t *testing.T) {
		for _, marker := range monorepoRootMarkers {
			t.Run(marker, func(t *testing.T) {
				root := t.TempDir()
				mkfile(t, filepath.Join(root, marker))

				got, found := findProjectRoot(root)
				if !found || got != root {
					t.Errorf("marker %q: got (%q, %v), want (%q, true)", marker, got, found, root)
				}
			})
		}
	})
}

func TestStackFingerprinter_Fingerprint(t *testing.T) {
	t.Run("detects apps and packages one level deep", func(t *testing.T) {
		root := t.TempDir()
		mkfile(t, filepath.Join(root, "go.mod"))
		mkfile(t, filepath.Join(root, "apps", "core-srv", "main.go"))
		mkfile(t, filepath.Join(root, "apps", "ai-srv", "index.ts"))
		mkfile(t, filepath.Join(root, "packages", "logger-go", "logger.go"))
		mkfile(t, filepath.Join(root, "apps", ".hidden", "x"))

		f := NewStackFingerprinter()
		fp, err := f.Fingerprint(filepath.Join(root, "apps", "core-srv"))
		if err != nil {
			t.Fatalf("Fingerprint: %v", err)
		}

		if fp.Root != root {
			t.Errorf("Root = %q, want %q", fp.Root, root)
		}
		wantApps := []string{"apps/ai-srv", "apps/core-srv", "packages/logger-go"}
		if !equalStrings(fp.Apps, wantApps) {
			t.Errorf("Apps = %v, want %v", fp.Apps, wantApps)
		}
	})

	t.Run("stack markers reflect what is present at root", func(t *testing.T) {
		root := t.TempDir()
		mkfile(t, filepath.Join(root, "go.mod"))
		mkfile(t, filepath.Join(root, "package.json"))
		mkfile(t, filepath.Join(root, "turbo.json"))

		f := NewStackFingerprinter()
		fp, err := f.Fingerprint(root)
		if err != nil {
			t.Fatalf("Fingerprint: %v", err)
		}

		want := []string{"go.mod", "package.json", "turbo.json"}
		if !equalStrings(fp.StackMarkers, want) {
			t.Errorf("StackMarkers = %v, want %v", fp.StackMarkers, want)
		}
	})

	t.Run("no apps/packages directories yields empty Apps", func(t *testing.T) {
		root := t.TempDir()
		mkfile(t, filepath.Join(root, "go.mod"))

		f := NewStackFingerprinter()
		fp, err := f.Fingerprint(root)
		if err != nil {
			t.Fatalf("Fingerprint: %v", err)
		}
		if len(fp.Apps) != 0 {
			t.Errorf("Apps = %v, want empty", fp.Apps)
		}
	})

	t.Run("no project root returns ErrProjectRootNotFound", func(t *testing.T) {
		root := t.TempDir()
		sub := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		f := NewStackFingerprinter()
		_, err := f.Fingerprint(sub)
		if err != ErrProjectRootNotFound {
			t.Errorf("err = %v, want ErrProjectRootNotFound", err)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
