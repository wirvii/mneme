package sddfile

import (
	"path/filepath"
	"testing"
)

// TestSDDFile_ClassifyRecordPath is AC1: the table names, verbatim, the two
// entregable rows (plan.md, spec.md) that the OLD heuristic
// (filepath.Base(path) == "record.md") misclassified as backlog items (W7).
func TestSDDFile_ClassifyRecordPath(t *testing.T) {
	repoRoot := "/repo"

	tests := []struct {
		name     string
		path     string
		wantKind RecordKind
		wantID   string
		wantOK   bool
	}{
		{
			name:     "backlog item",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", "backlog", "BL-205.md"),
			wantKind: KindBacklog,
			wantID:   "BL-205",
			wantOK:   true,
		},
		{
			name:     "spec record",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", "specs", "SPEC-131", "record.md"),
			wantKind: KindSpec,
			wantID:   "SPEC-131",
			wantOK:   true,
		},
		{
			name:     "spec plan.md is ignored, not backlog (W7)",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", "specs", "SPEC-131", "plan.md"),
			wantKind: KindIgnored,
			wantID:   "",
			wantOK:   false,
		},
		{
			name:     "spec spec.md is ignored, not backlog (W7)",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", "specs", "SPEC-131", "spec.md"),
			wantKind: KindIgnored,
			wantID:   "",
			wantOK:   false,
		},
		{
			name:     "stray backlog notes.md is ignored",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", "backlog", "notas.md"),
			wantKind: KindIgnored,
			wantID:   "",
			wantOK:   false,
		},
		{
			name:     "the marker file itself is ignored",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", MarkerFileName),
			wantKind: KindIgnored,
			wantID:   "",
			wantOK:   false,
		},
		{
			name:     "path outside RootDir is ignored",
			path:     filepath.Join(repoRoot, "README.md"),
			wantKind: KindIgnored,
			wantID:   "",
			wantOK:   false,
		},
		{
			name:     "malformed backlog filename is ignored",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", "backlog", "BL-abc.md"),
			wantKind: KindIgnored,
			wantID:   "",
			wantOK:   false,
		},
		{
			name:     "malformed spec dir name is ignored",
			path:     filepath.Join(repoRoot, ".mneme", "sdd", "specs", "SPEC-abc", "record.md"),
			wantKind: KindIgnored,
			wantID:   "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, id, ok := ClassifyRecordPath(repoRoot, tt.path)
			if kind != tt.wantKind || id != tt.wantID || ok != tt.wantOK {
				t.Errorf("ClassifyRecordPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.path, kind, id, ok, tt.wantKind, tt.wantID, tt.wantOK)
			}
		})
	}
}

// Mutacion exigida (AC1): volver a la heuristica de hoy
// (filepath.Base(path) == "record.md") pone en rojo las dos filas de
// plan.md/spec.md, que pasarian a clasificarse como backlog — exactamente
// el defecto latente W7 documenta. Ejecutada y revertida durante la
// implementacion; el resultado real se documenta en changes.md.
