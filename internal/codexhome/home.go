// Package codexhome resolves Codex's host configuration directory.
// Keeping the policy in a leaf package prevents install and upgrade from
// silently disagreeing about where mneme's Codex integration lives.
package codexhome

import (
	"os"
	"path/filepath"
)

// Env names the Codex-supported override for its configuration directory.
const Env = "CODEX_HOME"

// Resolve returns CODEX_HOME when explicitly configured, otherwise the
// conventional .codex directory below home. A relative override is made
// absolute against home so every caller reports and writes the same path.
func Resolve(home string) string {
	if configured := os.Getenv(Env); configured != "" {
		if filepath.IsAbs(configured) {
			return filepath.Clean(configured)
		}
		return filepath.Clean(filepath.Join(home, configured))
	}
	return filepath.Join(home, ".codex")
}
