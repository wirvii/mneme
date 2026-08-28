package sddfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MarkerFileName is the enable marker's filename (D29): its presence,
// versioned and committed, is what turns the SDD mechanism on for every
// teammate who clones the repository — the same "encender es del equipo"
// posture team-memory's own .mneme-vault marker set (SPEC-053).
const MarkerFileName = ".mneme-sdd"

// Marker is the JSON structure of .mneme/sdd/.mneme-sdd (D29), mirroring
// vault.VaultMarker's shape: version, project, timestamps, counts.
type Marker struct {
	SDDVersion   int    `json:"sdd_version"`
	Project      string `json:"project"`
	CreatedAt    string `json:"created_at"`
	LastExportAt string `json:"last_export_at"`
	BacklogCount int    `json:"backlog_count"`
	SpecCount    int    `json:"spec_count"`
}

// MarkerPath returns the path to the enable marker file:
// <repoRoot>/.mneme/sdd/.mneme-sdd.
func MarkerPath(repoRoot string) string {
	return filepath.Join(RootDir(repoRoot), MarkerFileName)
}

// ReadMarker reads and parses the marker at repoRoot. Returns nil, nil
// (not an error) when the file does not exist — "not enabled" is a normal
// outcome, not a failure.
func ReadMarker(repoRoot string) (*Marker, error) {
	data, err := os.ReadFile(MarkerPath(repoRoot))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sddfile: read marker: %w", err)
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("sddfile: parse marker: %w", err)
	}
	return &m, nil
}

// WriteMarker writes m to repoRoot's marker path atomically (temp file +
// rename), creating .mneme/sdd if it does not exist yet.
func WriteMarker(repoRoot string, m Marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("sddfile: marshal marker: %w", err)
	}
	return WriteRecord(MarkerPath(repoRoot), data)
}
