package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ensureMnemeGitignore makes sure every one of entries is present, in order,
// inside <repoRoot>/.mneme/.gitignore (SPEC-130 uses it for sdd.off; it was
// generalised to any number of entries by SPEC-105 DD24 and survives
// SPEC-162 because the SDD mechanism still needs it). Idempotent per line: an
// entry already present anywhere in the file (exact match after trimming)
// is never duplicated; any other pre-existing content — hand-authored or
// from an earlier version of this function — is preserved untouched.
// Missing entries are appended in the order given.
func ensureMnemeGitignore(repoRoot string, entries ...string) error {
	path := filepath.Join(repoRoot, ".mneme", ".gitignore")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	present := make(map[string]bool)
	for _, line := range strings.Split(string(existing), "\n") {
		present[strings.TrimSpace(line)] = true
	}

	var b strings.Builder
	b.Write(existing)
	needsNewline := len(existing) > 0 && !strings.HasSuffix(string(existing), "\n")

	wroteAny := false
	for _, entry := range entries {
		if present[entry] {
			continue
		}
		if needsNewline {
			b.WriteString("\n")
			needsNewline = false
		}
		b.WriteString(entry)
		b.WriteString("\n")
		wroteAny = true
	}

	if !wroteAny {
		return nil
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
