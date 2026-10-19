package laneaudit

import (
	"testing"

	"github.com/wirvii/mneme/internal/gitrepo"
)

func TestEvaluateTrivialBudget(t *testing.T) {
	tests := []struct {
		name      string
		files     []gitrepo.FileStat
		delta     SymbolDelta
		scope     string
		wantCount int
		wantText  string
	}{
		{
			name:      "too many files",
			files:     []gitrepo.FileStat{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}, {Path: "d.go"}},
			wantCount: 1,
			wantText:  "file count 4 exceeds trivial limit of 3",
		},
		{
			name:      "too many lines",
			files:     []gitrepo.FileStat{{Path: "a.go", Added: 21}},
			wantCount: 1,
			wantText:  "line count 21 exceeds trivial limit of 20",
		},
		{
			name:      "out of scope",
			files:     []gitrepo.FileStat{{Path: "internal/other/x.go"}},
			scope:     "internal/store/*.go",
			wantCount: 1,
			wantText:  "out of scope: internal/other/x.go",
		},
		{
			name:      "forbidden path",
			files:     []gitrepo.FileStat{{Path: "internal/db/migrations/001.sql"}},
			wantCount: 1,
			wantText:  "forbidden path modified: internal/db/migrations/001.sql",
		},
		{
			name:  "exported symbol created",
			files: []gitrepo.FileStat{{Path: "internal/store/x.go"}},
			delta: SymbolDelta{Created: []Symbol{
				{QualifiedName: "NewPublicFunc", File: "internal/store/x.go", Exported: true},
			}},
			scope:     "internal/store/*.go",
			wantCount: 1,
			wantText:  "public symbol changed: NewPublicFunc in internal/store/x.go",
		},
		{
			name:      "clean spec, zero breaches (positive)",
			files:     []gitrepo.FileStat{{Path: "internal/store/x.go", Added: 10}},
			scope:     "internal/store/*.go",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaches := EvaluateTrivialBudget(tt.files, tt.delta, tt.scope, DefaultTrivialBudget)
			if len(breaches) != tt.wantCount {
				t.Fatalf("EvaluateTrivialBudget() = %v, want %d breach(es)", breaches, tt.wantCount)
			}
			if tt.wantText != "" && string(breaches[0]) != tt.wantText {
				t.Errorf("breach = %q, want %q", breaches[0], tt.wantText)
			}
		})
	}
}

// TestEvaluateTrivialBudget_BreachTextsVerbatim pins every breach format
// string BYTE FOR BYTE, written here as a literal — not derived from the
// implementation — because these exact strings are concatenated into
// lane_audits.Breaches and read back by LaneStatus: changing them would
// break every historical audit's readability (SPEC-158 D1.b).
func TestEvaluateTrivialBudget_BreachTextsVerbatim(t *testing.T) {
	tests := []struct {
		name  string
		files []gitrepo.FileStat
		delta SymbolDelta
		scope string
		want  string
	}{
		{
			name:  "file count",
			files: []gitrepo.FileStat{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}, {Path: "d.go"}},
			want:  "file count 4 exceeds trivial limit of 3",
		},
		{
			name:  "line count",
			files: []gitrepo.FileStat{{Path: "a.go", Added: 21}},
			want:  "line count 21 exceeds trivial limit of 20",
		},
		{
			name:  "forbidden path",
			files: []gitrepo.FileStat{{Path: "cmd/x/main.go"}},
			want:  "forbidden path modified: cmd/x/main.go",
		},
		{
			name:  "out of scope",
			files: []gitrepo.FileStat{{Path: "internal/other/x.go"}},
			scope: "internal/store/*.go",
			want:  "out of scope: internal/other/x.go",
		},
		{
			name:  "public symbol created",
			files: []gitrepo.FileStat{{Path: "internal/store/x.go"}},
			delta: SymbolDelta{Created: []Symbol{
				{QualifiedName: "NewPublicFunc", File: "internal/store/x.go", Exported: true},
			}},
			scope: "internal/store/*.go",
			want:  "public symbol changed: NewPublicFunc in internal/store/x.go",
		},
		{
			name:  "public symbol deleted",
			files: []gitrepo.FileStat{{Path: "internal/store/x.go"}},
			delta: SymbolDelta{Deleted: []Symbol{
				{QualifiedName: "OldPublicFunc", File: "internal/store/x.go", Exported: true},
			}},
			scope: "internal/store/*.go",
			want:  "public symbol changed: OldPublicFunc in internal/store/x.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaches := EvaluateTrivialBudget(tt.files, tt.delta, tt.scope, DefaultTrivialBudget)
			if len(breaches) == 0 {
				t.Fatalf("EvaluateTrivialBudget() = no breaches, want one matching %q", tt.want)
			}
			found := false
			for _, b := range breaches {
				if string(b) == tt.want {
					found = true
				}
			}
			if !found {
				t.Errorf("breaches = %v, want one exactly equal to %q", breaches, tt.want)
			}
		})
	}
}
