package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
)

// TestRunHookEnforceDelegation_TaskSection_Table covers criterion
// "hook-roles": the role -> section permission, fail-open branches and the
// orchestrator exemption, with a custom role resolved through a REAL
// manifest row in a real SQLite file.
func TestRunHookEnforceDelegation_TaskSection_Table(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, t.TempDir())

	dbPath := filepath.Join(dir, "p.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	manifest := `[{"role":"api-dev","archetype":"backend","areas":["internal/**"]}]`
	if err := insertTestManifest(database, "m1", "test/project", manifest); err != nil {
		t.Fatalf("insertTestManifest: %v", err)
	}
	database.Close()

	corrupt := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	realLookup := func(path string) archetypeLookup {
		return func(role string) (string, bool) { return archetypeFromManifest(path, "test/project", role) }
	}
	panicLookup := func(string) (string, bool) { panic("manifest must not be consulted for a built-in archetype") }

	tests := []struct {
		name      string
		agentID   string
		agentType string
		section   string
		lookup    archetypeLookup
		wantBlock bool
		wantMsg   string
	}{
		{"backend design", "a1", "backend", "diseno", panicLookup, true, `"diseno"`},
		{"backend delivery", "a1", "backend", "entrega", panicLookup, false, ""},
		{"bug-hunter delivery", "a1", "bug-hunter", "entrega", panicLookup, false, ""},
		{"frontend delivery", "a1", "frontend", "entrega", panicLookup, false, ""},
		{"qa review", "a1", "qa-tester", "revision", panicLookup, false, ""},
		{"qa delivery", "a1", "qa-tester", "entrega", panicLookup, true, "backend"},
		{"architect que", "a1", "architect", "que", panicLookup, false, ""},
		{"architect diseno", "a1", "architect", "diseno", panicLookup, false, ""},
		{"architect plan", "a1", "architect", "plan", panicLookup, false, ""},
		{"architect delivery", "a1", "architect", "entrega", panicLookup, true, "backend"},
		{"diagnostician anything", "a1", "diagnostician", "plan", panicLookup, true, "architect"},
		{"unresolved role", "a1", "", "diseno", panicLookup, false, "no se pudo resolver el rol"},
		{"orchestrator any section", "", "", "diseno", panicLookup, false, ""},
		{"empty section", "a1", "backend", "", panicLookup, false, ""},
		{"unknown section", "a1", "backend", "bogus", panicLookup, false, ""},
		{"custom role delivery", "a1", "api-dev", "entrega", realLookup(dbPath), false, ""},
		{"custom role review", "a1", "api-dev", "revision", realLookup(dbPath), true, "qa-tester"},
		{"custom role not in manifest", "a1", "ghost", "entrega", realLookup(dbPath), false, "no se pudo resolver el arquetipo"},
		{"unreadable manifest", "a1", "api-dev", "revision", realLookup(corrupt), false, "no se pudo resolver el arquetipo"},
		{"missing manifest db", "a1", "api-dev", "revision", realLookup(filepath.Join(dir, "none.db")), false, "no se pudo resolver el arquetipo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in hookPreToolInput
			in.ToolName = "mcp__mneme__spec_doc_write"
			in.ToolInput.Kind = "task"
			in.ToolInput.Section = tc.section
			in.AgentID = tc.agentID
			in.AgentType = tc.agentType

			var errW bytes.Buffer
			got := checkTaskSectionScope(in, in.resolveIdentity(), &errW, tc.lookup)
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

// TestCheckTaskSectionScope_OtherCallsUntouched proves the guard only looks
// at spec_doc_write kind=task.
func TestCheckTaskSectionScope_OtherCallsUntouched(t *testing.T) {
	chdirTemp(t, t.TempDir())
	tests := []struct{ tool, kind string }{
		{"mcp__mneme__spec_doc_write", "changes"},
		{"mcp__mneme__spec_doc_write", ""},
		{"mcp__mneme__spec_status", "task"},
		{"Edit", "task"},
	}
	for _, tc := range tests {
		var in hookPreToolInput
		in.ToolName, in.ToolInput.Kind, in.ToolInput.Section = tc.tool, tc.kind, "diseno"
		in.AgentID, in.AgentType = "a1", "backend"
		var errW bytes.Buffer
		if checkTaskSectionScope(in, in.resolveIdentity(), &errW, nil) {
			t.Errorf("%+v blocked", tc)
		}
	}
}

// TestManifestArchetypeLookup_NoProject exercises the production closure end
// to end under the sandboxed HOME: with no manifest it fails open.
func TestManifestArchetypeLookup_NoProject(t *testing.T) {
	chdirTemp(t, t.TempDir())
	var in hookPreToolInput
	if _, ok := manifestArchetypeLookup(in)("api-dev"); ok {
		t.Error("lookup without a manifest must report not found")
	}
}
