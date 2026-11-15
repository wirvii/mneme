package laneaudit

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/gitrepo"
	"github.com/wirvii/mneme/internal/risk"
)

const notDeclaredWant = `risk not declared: the implementer or the orchestrator adds "Risk: none" to tarea.md section entrega (spec_doc_write kind task section entrega)`

func none() *risk.Declaration { return &risk.Declaration{None: true, Line: "Risk: none"} }

func files(paths ...string) []gitrepo.FileStat {
	out := make([]gitrepo.FileStat, len(paths))
	for i, p := range paths {
		out[i] = gitrepo.FileStat{Path: p, Added: 1}
	}
	return out
}

func TestEvaluate(t *testing.T) {
	exported := func(name, file string) Symbol {
		return Symbol{QualifiedName: name, File: file, Exported: true}
	}
	malformedErr := fmt.Errorf("%w: %q", risk.ErrMalformedDeclaration, "Risk: maybe")

	var many []gitrepo.FileStat
	for i := 0; i < 40; i++ {
		many = append(many, gitrepo.FileStat{Path: fmt.Sprintf("internal/store/f%02d.go", i), Added: 50})
	}

	tests := []struct {
		name string
		in   Input
		want []Breach
	}{
		{
			name: "out_of_scope",
			in:   Input{Files: files("internal/store/a.go", "internal/other/x.go"), Scope: "internal/store/**", Declaration: none()},
			want: []Breach{"out of scope: internal/other/x.go"},
		},
		{
			name: "risk_path_item1",
			in:   Input{Files: files("internal/db/migrations/001.sql"), Declaration: &risk.Declaration{Items: []risk.Item{1}}},
			want: []Breach{
				"risk item 1: path internal/db/migrations/001.sql",
				"risk declared: item 1; the trivial lane admits only Risk: none",
			},
		},
		{
			name: "risk_path_item2",
			in:   Input{Files: files("config/.env"), Declaration: none()},
			want: []Breach{
				"risk item 2: path config/.env",
				"risk declaration contradicts diff: Risk: none declared, items 2 detected",
			},
		},
		{
			name: "risk_path_item5_deploy",
			in:   Input{Files: files("deploy/x.sh"), Declaration: none()},
			want: []Breach{
				"risk item 5: path deploy/x.sh",
				"risk declaration contradicts diff: Risk: none declared, items 5 detected",
			},
		},
		{
			name: "risk_path_item5_package_json",
			in:   Input{Files: files("web/package.json"), Declaration: none()},
			want: []Breach{
				"risk item 5: path web/package.json",
				"risk declaration contradicts diff: Risk: none declared, items 5 detected",
			},
		},
		{
			name: "symbol_created",
			in:   Input{Symbols: SymbolDelta{Created: []Symbol{exported("NewX", "internal/store/x.go"), {QualifiedName: "newY", File: "internal/store/x.go"}}}, Declaration: none()},
			want: []Breach{
				"risk item 3: public symbol changed: NewX in internal/store/x.go",
				"risk declaration contradicts diff: Risk: none declared, items 3 detected",
			},
		},
		{
			name: "symbol_deleted",
			in:   Input{Symbols: SymbolDelta{Deleted: []Symbol{exported("NewX", "internal/store/x.go")}}, Declaration: none()},
			want: []Breach{
				"risk item 3: public symbol changed: NewX in internal/store/x.go",
				"risk declaration contradicts diff: Risk: none declared, items 3 detected",
			},
		},
		{
			name: "symbol_moved_not_counted",
			in: Input{Symbols: SymbolDelta{
				Moved:    []MovedSymbol{{QualifiedName: "NewX", OldFile: "a.go", NewFile: "b.go"}},
				Modified: []Symbol{exported("Kept", "internal/store/x.go")},
			}, Declaration: none()},
			want: nil,
		},
		{
			name: "not_declared",
			in:   Input{Files: files("internal/store/a.go")},
			want: []Breach{notDeclaredWant},
		},
		{
			name: "malformed",
			in:   Input{Files: files("internal/store/a.go"), Declaration: &risk.Declaration{None: true, Line: "Risk: maybe"}, DeclErr: malformedErr},
			want: []Breach{"risk declaration malformed: Risk: maybe"},
		},
		{
			name: "malformed_without_declaration_quotes_error",
			in:   Input{DeclErr: errors.New("boom")},
			want: []Breach{"risk declaration malformed: boom"},
		},
		{
			name: "declared_item",
			in:   Input{Files: files("internal/store/a.go"), Declaration: &risk.Declaration{Items: []risk.Item{2, 4}}},
			want: []Breach{
				"risk declared: item 2; the trivial lane admits only Risk: none",
				"risk declared: item 4; the trivial lane admits only Risk: none",
			},
		},
		{
			name: "none_but_detected",
			in:   Input{Files: files("internal/db/migrations/001.sql"), Scope: "internal/**", Declaration: none()},
			want: []Breach{
				"risk item 1: path internal/db/migrations/001.sql",
				"risk declaration contradicts diff: Risk: none declared, items 1 detected",
			},
		},
		{
			name: "none_but_detected_two_items",
			in:   Input{Files: files("go.mod", "internal/db/schema.sql"), Declaration: none()},
			want: []Breach{
				"risk item 5: path go.mod",
				"risk item 1: path internal/db/schema.sql",
				"risk declaration contradicts diff: Risk: none declared, items 1, 5 detected",
			},
		},
		{
			name: "large_in_scope_no_risk",
			in:   Input{Files: many, Scope: "internal/store/**", Declaration: none()},
			want: nil,
		},
		{
			name: "nil_table_uses_default",
			in:   Input{Files: files("cmd/mneme/main.go"), Table: nil, Declaration: none()},
			want: []Breach{
				"risk item 5: path cmd/mneme/main.go",
				"risk declaration contradicts diff: Risk: none declared, items 5 detected",
			},
		},
		{
			name: "extra_table_patterns_apply",
			in:   Input{Files: files("db/model.dbml"), Table: risk.Table{risk.ItemIrreversibleData: {"**/*.dbml"}}, Declaration: none()},
			want: []Breach{
				"risk item 1: path db/model.dbml",
				"risk declaration contradicts diff: Risk: none declared, items 1 detected",
			},
		},
		{
			name: "empty_scope_skips_scope_check",
			in:   Input{Files: files("anywhere/x.go"), Scope: "", Declaration: none()},
			want: nil,
		},
		{
			name: "fixed_order_of_four_blocks",
			in: Input{
				Files:       files("internal/db/migrations/1.sql", "docs/x.md"),
				Symbols:     SymbolDelta{Created: []Symbol{exported("NewX", "internal/db/x.go")}},
				Scope:       "internal/**",
				Declaration: none(),
			},
			want: []Breach{
				"out of scope: docs/x.md",
				"risk item 1: path internal/db/migrations/1.sql",
				"risk item 3: public symbol changed: NewX in internal/db/x.go",
				"risk declaration contradicts diff: Risk: none declared, items 1, 3 detected",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Evaluate =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}

	t.Run("not_declared_text_never_points_at_the_rationale", func(t *testing.T) {
		for _, word := range []string{"spec_quick", "rationale"} {
			if strings.Contains(notDeclaredWant, word) {
				t.Errorf("not-declared breach mentions %q", word)
			}
		}
	})
}

func TestRiskItemsForPath(t *testing.T) {
	tbl := risk.DefaultTable()
	tests := []struct {
		path string
		want []risk.Item
	}{
		{"internal/db/migrations/1.sql", []risk.Item{1}},
		{"internal/install/assets/secret.md", []risk.Item{2, 5}},
		{"deploy/migrations/1.sql", []risk.Item{1, 5}},
		{"internal/store/x.go", nil},
	}
	for _, tt := range tests {
		if got := RiskItemsForPath(tt.path, tbl); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("RiskItemsForPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestIsRiskBreach(t *testing.T) {
	tests := []struct {
		breach string
		want   bool
	}{
		{"risk item 1: path x.sql", true},
		{"risk declared: item 2; the trivial lane admits only Risk: none", true},
		{"risk declaration contradicts diff: Risk: none declared, items 1 detected", true},
		{"forbidden path modified: cmd/x.go", true},
		{"public symbol changed: NewX in x.go", true},
		{notDeclaredWant, false},
		{"risk declaration malformed: Risk: maybe", false},
		{"out of scope: y", false},
		{"file count 4 exceeds trivial limit of 3", false},
	}
	for _, tt := range tests {
		if got := IsRiskBreach(tt.breach); got != tt.want {
			t.Errorf("IsRiskBreach(%q) = %v, want %v", tt.breach, got, tt.want)
		}
	}
}

func TestIsCatchAllScope(t *testing.T) {
	tests := []struct {
		glob string
		want bool
	}{
		{"**", true},
		{"*", true},
		{"**/*", true},
		{"*/**", true},
		{"**/*.go", true},
		{"{internal,cmd}/**", true},
		{"{internal/cli,docs}/**", true},
		{"{docs,README.md}", true},
		{"", true},
		{"internal/**", false},
		{"docs/*.md", false},
		{"cmd/x/*.go", false},
		{"internal/store/**", false},
	}
	for _, tt := range tests {
		if got := IsCatchAllScope(tt.glob); got != tt.want {
			t.Errorf("IsCatchAllScope(%q) = %v, want %v", tt.glob, got, tt.want)
		}
	}
}
