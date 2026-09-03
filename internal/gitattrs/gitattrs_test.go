package gitattrs

import (
	"strings"
	"testing"
)

func TestPatterns_CoverAllThreeInOrder(t *testing.T) {
	got := Patterns()
	want := []Pattern{PatternSDD, PatternClaudeAgents, PatternCodexAgents}
	if len(got) != len(want) {
		t.Fatalf("Patterns() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Patterns()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProbePath_NonEmptyForEveryPattern(t *testing.T) {
	for _, p := range Patterns() {
		if ProbePath(p) == "" {
			t.Errorf("ProbePath(%q) is empty", p)
		}
	}
}

func TestBlock_ContainsMarkersAndAllThreePatterns(t *testing.T) {
	block := Block()
	if !strings.Contains(block, startComment) {
		t.Error("Block() missing start marker")
	}
	if !strings.Contains(block, endComment) {
		t.Error("Block() missing end marker")
	}
	for _, p := range Patterns() {
		if !strings.Contains(block, string(p)) {
			t.Errorf("Block() missing pattern %q", p)
		}
	}
	if !strings.Contains(block, "eol=lf") {
		t.Error("Block() missing eol=lf")
	}
}

func TestDecide_ThreeRowsOfD10(t *testing.T) {
	tests := []struct {
		name       string
		probes     map[Pattern]string
		wantAction Action
	}{
		{
			name: "explicit crlf on one pattern -> conflict",
			probes: map[Pattern]string{
				PatternSDD:          "crlf",
				PatternClaudeAgents: "lf",
				PatternCodexAgents:  "lf",
			},
			wantAction: ActionConflict,
		},
		{
			name: "unspecified on one, none explicit-other-than-lf -> write",
			probes: map[Pattern]string{
				PatternSDD:          "unspecified",
				PatternClaudeAgents: "lf",
				PatternCodexAgents:  "lf",
			},
			wantAction: ActionWrite,
		},
		{
			name: "lf on all three -> skip",
			probes: map[Pattern]string{
				PatternSDD:          "lf",
				PatternClaudeAgents: "lf",
				PatternCodexAgents:  "lf",
			},
			wantAction: ActionSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.probes)
			if got.Action != tt.wantAction {
				t.Errorf("Decide(%v).Action = %v, want %v", tt.probes, got.Action, tt.wantAction)
			}
		})
	}
}

func TestDecide_ConflictNamesThePatternAndValue(t *testing.T) {
	got := Decide(map[Pattern]string{
		PatternSDD:          "lf",
		PatternClaudeAgents: "crlf",
		PatternCodexAgents:  "unspecified",
	})
	if got.Action != ActionConflict {
		t.Fatalf("Action = %v, want ActionConflict", got.Action)
	}
	if got.Pattern != PatternClaudeAgents {
		t.Errorf("Pattern = %q, want %q", got.Pattern, PatternClaudeAgents)
	}
	if got.Value != "crlf" {
		t.Errorf("Value = %q, want %q", got.Value, "crlf")
	}
}

func TestUpsert_AppendsToEmptyOrNonEmptyText(t *testing.T) {
	empty := Upsert("")
	if empty != Block() {
		t.Errorf("Upsert(\"\") = %q, want exactly Block()", empty)
	}

	withProse := Upsert("* text=auto\n")
	if !strings.HasPrefix(withProse, "* text=auto\n\n"+startComment) {
		t.Errorf("Upsert did not preserve+append: %q", withProse)
	}
}

func TestUpsert_ReplacesExistingBlockNeverDuplicates(t *testing.T) {
	first := Upsert("* text=auto\n")
	second := Upsert(first)

	if strings.Count(second, startComment) != 1 {
		t.Fatalf("expected exactly 1 start marker after replace, got result:\n%q", second)
	}
	if !strings.Contains(second, "* text=auto") {
		t.Errorf("Upsert must preserve content outside the block: %q", second)
	}
}

func TestUpsert_NeverTouchesLinesOutsideBlock(t *testing.T) {
	existing := "*.png binary\n" + Block() + "*.jpg binary\n"
	got := Upsert(existing)
	if !strings.Contains(got, "*.png binary") || !strings.Contains(got, "*.jpg binary") {
		t.Errorf("Upsert removed unrelated lines: %q", got)
	}
	if strings.Count(got, startComment) != 1 {
		t.Errorf("Upsert duplicated the block: %q", got)
	}
}
