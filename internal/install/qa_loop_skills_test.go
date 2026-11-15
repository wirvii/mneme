package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/install"
)

// qaLoopSkillAnchors is the single list of anchors SPEC-188 adds to the
// bundled skills: the round cap, the review package, the "necesitas
// decidir" exit, who reopens a round after the cap and the visible
// "Decisiones que tomé por ti" section. Anchors are single-line so a
// re-wrap of a skill cannot silently break them (SPEC-169).
var qaLoopSkillAnchors = map[string][]string{
	"review-brief/SKILL.md": {
		"mneme spec review-package", "qa_rounds", "3 or more", "never call `spec_reject`",
		"Only the orchestrator reopens a round", "necesitas decidir", "Decisiones que tomé por ti",
		"spec/<ID>-rulings-qa", "## Orchestrator: closing with the person",
		"references/rounds.md", "references/qa-tester.md",
	},
	"review-brief/references/rounds.md": {
		"at most 3 rounds", "3 or more", "necesitas decidir", "never starts a round",
		"the orchestrator opens it", "the same message goes out again",
	},
	"review-brief/references/qa-tester.md": {
		"delivered end", "is-ancestor", "Never copy and run", "never call `spec_reject`",
		"backlog_add", "references/report.md", "review-package",
	},
	"present-spec/SKILL.md": {"Decisiones que tomé por ti", "spec/<ID>-rulings-gate"},
	"feature/SKILL.md":      {"Decisiones que tomé por ti"},
	"bug/SKILL.md":          {"Decisiones que tomé por ti"},
	"mneme-flow/SKILL.md":   {"mneme spec review-package", "qa_rounds"},
}

// skillAnchorSurfaces returns the content of every bundled skill file,
// keyed by its slash-separated path relative to assets/skills.
func skillAnchorSurfaces(t *testing.T) map[string]string {
	t.Helper()
	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}
	files := make(map[string]string, len(entries))
	for _, e := range entries {
		files[filepath.ToSlash(e.RelPath)] = string(e.Content)
	}
	return files
}

// skillAnchorFindings returns one "missing: <anchor>" per anchor absent
// from content.
func skillAnchorFindings(content string, anchors []string) []string {
	var findings []string
	for _, a := range anchors {
		if !strings.Contains(content, a) {
			findings = append(findings, "missing: "+a)
		}
	}
	return findings
}

// TestQALoopSkills_Anchors is SPEC-188 C12: every file of the table exists
// and carries all of its anchors.
func TestQALoopSkills_Anchors(t *testing.T) {
	files := skillAnchorSurfaces(t)
	for path, anchors := range qaLoopSkillAnchors {
		t.Run(path, func(t *testing.T) {
			content, ok := files[path]
			if !ok {
				t.Fatalf("%s is not a bundled skill file", path)
			}
			for _, f := range skillAnchorFindings(content, anchors) {
				t.Errorf("%s: %s", path, f)
			}
		})
	}
}

// TestQALoopSkills_AnchorsAreDiscriminating proves each anchor is checked on
// its own: removing it from a copy gives exactly that one finding.
func TestQALoopSkills_AnchorsAreDiscriminating(t *testing.T) {
	files := skillAnchorSurfaces(t)
	for path, anchors := range qaLoopSkillAnchors {
		content, ok := files[path]
		if !ok {
			t.Errorf("%s is not a bundled skill file", path)
			continue
		}
		for _, a := range anchors {
			got := skillAnchorFindings(strings.ReplaceAll(content, a, ""), anchors)
			if want := "missing: " + a; len(got) != 1 || got[0] != want {
				t.Errorf("%s: removing %q should give exactly %q, got %q", path, a, want, got)
			}
		}
	}
}
