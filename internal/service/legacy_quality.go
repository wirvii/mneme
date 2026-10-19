// Package service — this file is SPEC-158 D5's cleanup mechanism for the
// two files the now-retired EPIC-calidad certification mechanism used to
// own: .mneme/quality.toml and .mneme/quality-baseline.toml. Neither file
// does anything any more — no production code reads them — so this
// mechanism only ever offers to remove them, never writes or interprets
// their content.
package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// legacyQualityRelPaths is the fixed, ordered set of paths this mechanism
// looks for, relative to a repository's root.
var legacyQualityRelPaths = []string{
	".mneme/quality.toml",
	".mneme/quality-baseline.toml",
}

// LegacyQualityFiles returns, in the fixed order above, the relative paths
// among legacyQualityRelPaths that actually exist under repoRoot. An empty
// result means the repository carries no trace of the retired mechanism.
func LegacyQualityFiles(repoRoot string) ([]string, error) {
	var found []string
	for _, rel := range legacyQualityRelPaths {
		_, err := os.Stat(filepath.Join(repoRoot, rel))
		switch {
		case err == nil:
			found = append(found, rel)
		case os.IsNotExist(err):
			continue
		default:
			return nil, fmt.Errorf("service: legacy quality files: stat %s: %w", rel, err)
		}
	}
	return found, nil
}

// RemoveLegacyQualityFiles removes exactly the paths named in rel (each
// resolved against repoRoot), and nothing else — in particular, it never
// removes the .mneme/ directory itself, even if this leaves it empty.
func RemoveLegacyQualityFiles(repoRoot string, rel []string) error {
	for _, r := range rel {
		if err := os.Remove(filepath.Join(repoRoot, r)); err != nil {
			return fmt.Errorf("service: remove legacy quality file %s: %w", r, err)
		}
	}
	return nil
}

// LegacyQualityFindings returns one advisory DriftFinding per legacy file
// LegacyQualityFiles finds under repoRoot — the SAME shape RunDrift's own
// CLAUDE.md findings use, so a caller (mneme init, the MCP init tool) can
// surface it through the report it already prints, without inventing a
// second channel. Never writes or deletes anything.
func (s *InitService) LegacyQualityFindings(repoRoot string) ([]DriftFinding, error) {
	found, err := LegacyQualityFiles(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: legacy quality findings: %w", err)
	}

	findings := make([]DriftFinding, 0, len(found))
	for _, rel := range found {
		findings = append(findings, DriftFinding{
			File: filepath.Join(repoRoot, rel),
			Line: 1,
			Message: "mecanismo de certificación retirado; borra este archivo o ejecuta " +
				"`mneme upgrade` en este repositorio",
		})
	}
	return findings, nil
}
