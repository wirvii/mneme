// Package service — this file is SPEC-162's cleanup mechanism for the traces
// the now-retired team profiles left on disk: the repository pin
// (.mneme-profile), the activation lock (.mneme/profile.lock), and the
// host-level directory of profile checkouts (<data_dir>/profiles). Nothing
// reads any of them any more, so this mechanism only ever offers to remove
// them, never interprets their content.
//
// .mneme/backups/ is reported but never deleted: past activations moved the
// originals they displaced there, and those may be files the user still wants.
package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// legacyProfileRelPaths is the fixed, ordered set of repository files this
// mechanism looks for, relative to a repository's root.
var legacyProfileRelPaths = []string{
	".mneme-profile",
	".mneme/profile.lock",
}

// legacyProfileBackupsRel is the directory of originals that past profile
// activations displaced. It is listed, never deleted.
const legacyProfileBackupsRel = ".mneme/backups"

// legacyProfileStoreName is the sub-directory of the data directory that held
// the profile checkouts.
const legacyProfileStoreName = "profiles"

// LegacyProfileFiles returns, in the fixed order above, the relative paths of
// the retired pin and lock that actually exist under repoRoot. An empty result
// means the repository carries no trace of team profiles.
func LegacyProfileFiles(repoRoot string) ([]string, error) {
	var found []string
	for _, rel := range legacyProfileRelPaths {
		_, err := os.Stat(filepath.Join(repoRoot, rel))
		switch {
		case err == nil:
			found = append(found, rel)
		case os.IsNotExist(err):
			continue
		default:
			return nil, fmt.Errorf("service: legacy profile files: stat %s: %w", rel, err)
		}
	}
	return found, nil
}

// LegacyProfileBackups returns the path of the repository's .mneme/backups
// directory and whether it exists. It is reported so the user reviews it by
// hand; no function in this file deletes it.
func LegacyProfileBackups(repoRoot string) (string, bool, error) {
	return existingDir(filepath.Join(repoRoot, filepath.FromSlash(legacyProfileBackupsRel)))
}

// LegacyProfileStore returns the host-level directory of profile checkouts
// under dataDir and whether it exists.
func LegacyProfileStore(dataDir string) (string, bool, error) {
	return existingDir(filepath.Join(dataDir, legacyProfileStoreName))
}

// existingDir reports whether path exists, treating "does not exist" as a
// clean negative and any other stat failure as an error.
func existingDir(path string) (string, bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return path, true, nil
	case os.IsNotExist(err):
		return path, false, nil
	default:
		return "", false, fmt.Errorf("service: legacy profile: stat %s: %w", path, err)
	}
}

// RemoveLegacyProfileArtifacts removes exactly what it is told to: each file
// named in rel (resolved against repoRoot) and, when storeDir is not empty,
// that whole directory. It never touches .mneme/ itself or .mneme/backups/,
// and it never runs any git command: a removed tracked file is left as an
// unstaged change for the human to review.
func RemoveLegacyProfileArtifacts(repoRoot string, rel []string, storeDir string) error {
	for _, r := range rel {
		if err := os.Remove(filepath.Join(repoRoot, r)); err != nil {
			return fmt.Errorf("service: remove legacy profile file %s: %w", r, err)
		}
	}
	if storeDir != "" {
		if err := os.RemoveAll(storeDir); err != nil {
			return fmt.Errorf("service: remove legacy profile store %s: %w", storeDir, err)
		}
	}
	return nil
}

// LegacyProfileFindings returns one advisory DriftFinding per trace of team
// profiles: each file LegacyProfileFiles finds under repoRoot and the
// host-level store under dataDir. It uses the same shape RunDrift's own
// findings use, so mneme init and the MCP init tool surface it through the
// report they already print. Never writes or deletes anything.
func (s *InitService) LegacyProfileFindings(repoRoot, dataDir string) ([]DriftFinding, error) {
	found, err := LegacyProfileFiles(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: legacy profile findings: %w", err)
	}
	storeDir, storeExists, err := LegacyProfileStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("service: legacy profile findings: %w", err)
	}

	const message = "perfiles de equipo retirados; borra este archivo o ejecuta `mneme upgrade`"
	findings := make([]DriftFinding, 0, len(found)+1)
	for _, rel := range found {
		findings = append(findings, DriftFinding{File: filepath.Join(repoRoot, rel), Line: 1, Message: message})
	}
	if storeExists {
		findings = append(findings, DriftFinding{File: storeDir, Line: 1, Message: message})
	}
	return findings, nil
}
