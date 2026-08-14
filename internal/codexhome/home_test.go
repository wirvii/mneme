package codexhome

import (
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	home := t.TempDir()

	t.Run("default", func(t *testing.T) {
		t.Setenv(Env, "")
		if got, want := Resolve(home), filepath.Join(home, ".codex"); got != want {
			t.Fatalf("Resolve() = %q, want %q", got, want)
		}
	})

	t.Run("absolute override", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "codex-config")
		t.Setenv(Env, want)
		if got := Resolve(home); got != want {
			t.Fatalf("Resolve() = %q, want %q", got, want)
		}
	})

	t.Run("relative override", func(t *testing.T) {
		t.Setenv(Env, "custom-codex")
		if got, want := Resolve(home), filepath.Join(home, "custom-codex"); got != want {
			t.Fatalf("Resolve() = %q, want %q", got, want)
		}
	})
}
