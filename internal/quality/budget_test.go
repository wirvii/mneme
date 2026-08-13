package quality

import (
	"errors"
	"strings"
	"testing"
)

// validBudgetTOML is a complete, valid budget document reused across
// several tests as the positive baseline every negative row is diffed
// against.
const validBudgetTOML = `
schema_version = 1
margin = 2
radius = ["internal/quality/**", "internal/service/**"]

[[quota]]
dir = "internal/quality"
max_new_symbols = 18

[[modify]]
file = "internal/service/quality.go"
symbol = "runAllChecks"
`

// TestParseBudget_Strictness covers AC5: ParseBudget is as strict as the
// constitution and criteria parsers — ten negative rows plus the
// obligatory positive hermana (R-B).
func TestParseBudget_Strictness(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr error
		wantOK  bool
	}{
		{
			name:    "unknown key rejected",
			toml:    validBudgetTOML + "\nbogus = true\n",
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "missing schema_version",
			toml:    "margin = 1\nradius = [\"a/**\"]\n",
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "unsupported schema_version",
			toml:    "schema_version = 2\nmargin = 1\nradius = [\"a/**\"]\n",
			wantErr: ErrUnsupportedBudgetSchema,
		},
		{
			name:    "missing margin",
			toml:    "schema_version = 1\nradius = [\"a/**\"]\n",
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "margin negative rejected",
			toml:    "schema_version = 1\nmargin = -1\nradius = [\"a/**\"]\n",
			wantErr: ErrInvalidBudget,
		},
		{
			name:   "margin zero is OK (G4-adjacent positive)",
			toml:   "schema_version = 1\nmargin = 0\nradius = [\"a/**\"]\n",
			wantOK: true,
		},
		{
			name:    "missing radius rejected",
			toml:    "schema_version = 1\nmargin = 1\n",
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "empty radius rejected (G4)",
			toml:    "schema_version = 1\nmargin = 1\nradius = []\n",
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "invalid radius glob rejected",
			toml:    "schema_version = 1\nmargin = 1\nradius = [\"[\"]\n",
			wantErr: ErrInvalidBudget,
		},
		{
			name: "duplicate quota dir rejected",
			toml: `schema_version = 1
margin = 1
radius = ["a/**"]
[[quota]]
dir = "internal/x"
max_new_symbols = 1
[[quota]]
dir = "internal/x"
max_new_symbols = 2
`,
			wantErr: ErrInvalidBudget,
		},
		{
			name: "quota max_new_symbols negative rejected",
			toml: `schema_version = 1
margin = 1
radius = ["a/**"]
[[quota]]
dir = "internal/x"
max_new_symbols = -1
`,
			wantErr: ErrInvalidBudget,
		},
		{
			name: "duplicate modify pair rejected",
			toml: `schema_version = 1
margin = 1
radius = ["a/**"]
[[modify]]
file = "a.go"
symbol = "Foo"
[[modify]]
file = "a.go"
symbol = "Foo"
`,
			wantErr: ErrInvalidBudget,
		},
		{
			name:   "complete valid document (positive hermana of the ten negatives above)",
			toml:   validBudgetTOML,
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := ParseBudget([]byte(tt.toml))
			if tt.wantOK {
				if err != nil {
					t.Fatalf("ParseBudget() error = %v, want nil", err)
				}
				if doc == nil {
					t.Fatal("ParseBudget() doc = nil, want non-nil")
				}
				return
			}
			if err == nil {
				t.Fatal("ParseBudget() error = nil, want error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ParseBudget() error = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}

// TestParseBudget_Revision covers AC6: [revision] exigencies and its
// legitimate absence.
func TestParseBudget_Revision(t *testing.T) {
	base := "schema_version = 1\nmargin = 1\nradius = [\"a/**\"]\n"

	t.Run("absent revision is OK and Revision is nil", func(t *testing.T) {
		doc, err := ParseBudget([]byte(base))
		if err != nil {
			t.Fatalf("ParseBudget() error = %v", err)
		}
		if doc.Revision != nil {
			t.Fatalf("Revision = %+v, want nil", doc.Revision)
		}
	})

	t.Run("revision missing by rejected", func(t *testing.T) {
		toml := base + `
[revision]
at = "2026-08-14T09:12:00Z"
rationale = "reason"
margin = 2
`
		_, err := ParseBudget([]byte(toml))
		if !errors.Is(err, ErrInvalidBudget) {
			t.Fatalf("ParseBudget() error = %v, want ErrInvalidBudget", err)
		}
	})

	t.Run("revision missing rationale rejected (G5)", func(t *testing.T) {
		toml := base + `
[revision]
by = "architect"
at = "2026-08-14T09:12:00Z"
margin = 2
`
		_, err := ParseBudget([]byte(toml))
		if !errors.Is(err, ErrInvalidBudget) {
			t.Fatalf("ParseBudget() error = %v, want ErrInvalidBudget", err)
		}
	})

	t.Run("revision at not RFC3339 rejected", func(t *testing.T) {
		toml := base + `
[revision]
by = "architect"
at = "not-a-date"
rationale = "reason"
margin = 2
`
		_, err := ParseBudget([]byte(toml))
		if !errors.Is(err, ErrInvalidBudget) {
			t.Fatalf("ParseBudget() error = %v, want ErrInvalidBudget", err)
		}
	})

	t.Run("complete revision is OK and Revision is non-nil", func(t *testing.T) {
		toml := base + `
[revision]
by = "architect"
at = "2026-08-14T09:12:00Z"
rationale = "reason"
margin = 2
`
		doc, err := ParseBudget([]byte(toml))
		if err != nil {
			t.Fatalf("ParseBudget() error = %v", err)
		}
		if doc.Revision == nil {
			t.Fatal("Revision = nil, want non-nil")
		}
		if doc.Revision.By != "architect" || doc.Revision.Rationale != "reason" || doc.Revision.Margin != 2 {
			t.Errorf("Revision = %+v, unexpected fields", doc.Revision)
		}
	})
}

// TestValidateBudgetAnchors covers AC21's pure half: quota/modify anchors
// resolving or not against fabricated dirs/symbolsByFile facts, with the
// two positive rows required so a validator that rejected everything would
// not pass.
func TestValidateBudgetAnchors(t *testing.T) {
	symsByFile := map[string][]Symbol{
		"internal/service/quality.go": {{QualifiedName: "runAllChecks", File: "internal/service/quality.go"}},
	}
	dirs := []string{"internal/quality", "internal/service"}

	tests := []struct {
		name    string
		budget  *Budget
		wantErr bool
	}{
		{
			name: "modify file does not exist",
			budget: &Budget{Modify: []ModifyEntry{
				{File: "internal/service/missing.go", Symbol: "runAllChecks"},
			}},
			wantErr: true,
		},
		{
			name: "modify symbol not found in existing file",
			budget: &Budget{Modify: []ModifyEntry{
				{File: "internal/service/quality.go", Symbol: "NoSuchSymbol"},
			}},
			wantErr: true,
		},
		{
			name: "modify pair resolves (positive)",
			budget: &Budget{Modify: []ModifyEntry{
				{File: "internal/service/quality.go", Symbol: "runAllChecks"},
			}},
			wantErr: false,
		},
		{
			name:    "quota dir does not exist",
			budget:  &Budget{Quota: []Quota{{Dir: "internal/nope", MaxNewSymbols: 1}}},
			wantErr: true,
		},
		{
			name:    "quota dir exists (positive)",
			budget:  &Budget{Quota: []Quota{{Dir: "internal/quality", MaxNewSymbols: 1}}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBudgetAnchors(tt.budget, dirs, symsByFile)
			if tt.wantErr && err == nil {
				t.Fatal("ValidateBudgetAnchors() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateBudgetAnchors() error = %v, want nil", err)
			}
			if err != nil && !errors.Is(err, ErrInvalidBudget) {
				t.Errorf("ValidateBudgetAnchors() error = %v, want errors.Is ErrInvalidBudget", err)
			}
		})
	}
}

// TestSymbolKey_IsFileColonQualifiedName pins the exact key format DiffSymbols
// (P3) will rely on.
func TestSymbolKey_IsFileColonQualifiedName(t *testing.T) {
	got := SymbolKey("a/b.go", "Foo")
	want := "a/b.go:Foo"
	if got != want {
		t.Errorf("SymbolKey() = %q, want %q", got, want)
	}
	if !strings.Contains(got, ":") {
		t.Fatal("SymbolKey() must contain the file:name separator")
	}
}
