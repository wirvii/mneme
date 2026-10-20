package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMemoryConfig_ReviewDays covers SPEC-167 AC4.
func TestMemoryConfig_ReviewDays(t *testing.T) {
	defaults := map[string]int{
		"discovery": 60, "config": 60, "decision": 120, "architecture": 120, "pattern": 120,
		"bugfix": 180, "rule": 180,
		"preference": 0, "convention": 0, "session_summary": 0, "synthesis": 0, "banana": 0,
	}

	t.Run("defaults", func(t *testing.T) {
		cfg := Default()
		for typ, want := range defaults {
			if got := cfg.Memory.ReviewDaysFor(typ); got != want {
				t.Errorf("ReviewDaysFor(%q) = %d, want %d", typ, got, want)
			}
		}
	})

	t.Run("toml override keeps the rest", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		body := "[memory.review_days]\ndiscovery = 30\npreference = 10\nbanana = 5\nbugfix = -4\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := map[string]int{
			"discovery":  30,  // overridden
			"decision":   120, // untouched default
			"preference": 10,  // a never-expiring type can be given a deadline
			"bugfix":     0,   // negative disables
			"banana":     0,   // unknown type ignored
		}
		for typ, w := range want {
			if got := cfg.Memory.ReviewDaysFor(typ); got != w {
				t.Errorf("ReviewDaysFor(%q) = %d, want %d", typ, got, w)
			}
		}
	})
}
