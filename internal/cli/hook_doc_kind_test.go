package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
)

// TestHook_SpecDocKindScope covers SPEC-181 C11's Claude Code half: the
// by-kind restriction resolved by archetype, with a REAL manifest row for a
// custom role, the orchestrator exemption and the fail-open branches.
func TestHook_SpecDocKindScope(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, t.TempDir())

	dbPath := filepath.Join(dir, "p.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	manifest := `[{"role":"arch-lead","archetype":"architect","areas":[]},{"role":"api-dev","archetype":"backend","areas":["internal/**"]}]`
	if err := insertTestManifest(database, "m1", "test/project", manifest); err != nil {
		t.Fatalf("insertTestManifest: %v", err)
	}
	database.Close()

	realLookup := archetypeLookup(func(role string) (string, bool) {
		return archetypeFromManifest(dbPath, "test/project", role)
	})
	panicLookup := archetypeLookup(func(string) (string, bool) { panic("manifest must not be consulted for a built-in archetype") })

	type row struct {
		name      string
		agentID   string
		agentType string
		kind      string
		lookup    archetypeLookup
		wantBlock bool
		wantMsg   string
	}
	var tests []row
	restricted := []string{"criteria", "review-spec", "review-plan"}
	for _, kind := range restricted {
		tests = append(tests,
			row{"architect " + kind, "a1", "architect", kind, panicLookup, false, ""},
			row{"orchestrator " + kind, "", "", kind, panicLookup, false, ""},
			row{"custom architect " + kind, "a1", "arch-lead", kind, realLookup, false, ""},
			row{"custom backend " + kind, "a1", "api-dev", kind, realLookup, true, "architect"},
		)
		for _, role := range []string{"backend", "frontend", "bug-hunter", "qa-tester", "diagnostician", "spec-reviewer"} {
			tests = append(tests, row{role + " " + kind, "a1", role, kind, panicLookup, true, kind})
		}
	}
	tests = append(tests,
		row{"qa-tester qa-report", "a1", "qa-tester", "qa-report", panicLookup, false, ""},
		row{"backend changes", "a1", "backend", "changes", panicLookup, false, ""},
		row{"backend task", "a1", "backend", "task", panicLookup, false, ""},
		row{"unresolved role", "a1", "", "review-spec", panicLookup, false, "rol de este subagente no se pudo resolver"},
		row{"custom role not in manifest", "a1", "ghost", "criteria", realLookup, false, "no se pudo resolver el arquetipo"},
	)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in hookPreToolInput
			in.ToolName = "mcp__mneme__spec_doc_write"
			in.ToolInput.Kind = tc.kind
			in.AgentID = tc.agentID
			in.AgentType = tc.agentType

			var errW bytes.Buffer
			got := checkSpecDocKindScope(in, in.resolveIdentity(), &errW, tc.lookup)
			if got != tc.wantBlock {
				t.Errorf("block = %v, want %v (stderr %q)", got, tc.wantBlock, errW.String())
			}
			if tc.wantMsg != "" && !strings.Contains(errW.String(), tc.wantMsg) {
				t.Errorf("stderr %q does not contain %q", errW.String(), tc.wantMsg)
			}
			if tc.wantBlock && !strings.Contains(errW.String(), "⛔") {
				t.Errorf("block message missing: %q", errW.String())
			}
		})
	}
}

// TestCheckSpecDocKindScope_OtherCallsUntouched proves the guard only looks
// at spec_doc_write.
func TestCheckSpecDocKindScope_OtherCallsUntouched(t *testing.T) {
	chdirTemp(t, t.TempDir())
	for _, tool := range []string{"mcp__mneme__spec_status", "Edit"} {
		var in hookPreToolInput
		in.ToolName, in.ToolInput.Kind = tool, "criteria"
		in.AgentID, in.AgentType = "a1", "backend"
		var errW bytes.Buffer
		if checkSpecDocKindScope(in, in.resolveIdentity(), &errW, nil) {
			t.Errorf("tool %s blocked", tool)
		}
	}
}
