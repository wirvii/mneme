package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/install"
)

// Byte caps for the two operating manuals (SPEC-169 D1). The Codex manual
// carries the hooks-trust warning Claude Code has no equivalent for, hence the
// larger cap. A manual that does not fit is compressed in wording, never
// stripped of a rule and never given a bigger cap.
const (
	claudeManualMaxBytes = 2700
	codexManualMaxBytes  = 3000
	presentSpecMaxBytes  = 4096
)

// sharedManualAnchors are the strings both manuals must carry (SPEC-169 §3.3).
// They are literal on purpose: the human approval gate and the plain-language
// rule must keep living in the always-loaded text, never only in a skill.
var sharedManualAnchors = []string{
	"Human approval gate (unbreakable)",
	"explicit yes",
	"is not approval",
	"never inherited, never a default",
	"Plain language",
	"MANDATORY",
	"binding on every message a person reads",
	"Channels that reach a person",
	"Relaying is authoring",
	"The exemption never travels with the text",
	"mem_context",
	"mem_search",
	"mem_save",
	"mem_session_end",
	"no hook that reminds you",
	"grill-me",
	"Do NOT use `superpowers:brainstorming`",
	"mneme-flow",
	"present-spec",
}

// forbiddenManualAnchors are surfaces and numbers that must not come back into
// the manuals: retired tools and skills, and the lane thresholds, which live
// only in mneme-flow's references/depth.md (SPEC-169 D5).
var forbiddenManualAnchors = []string{
	"speech_emit",
	"profile_",
	"mem_checkpoint",
	"codegraph_callees",
	"sdd_import",
	"hunt-bug",
	"bug-to-issue",
	"example-skill",
	"3 files",
	"20 lines",
}

// contentSurface is one text with its byte cap and anchors.
type contentSurface struct {
	name      string
	content   string
	maxBytes  int
	required  []string
	forbidden []string
}

func appendAnchors(base []string, extra ...string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	return append(out, extra...)
}

func manualSurfaces() []contentSurface {
	return []contentSurface{
		{
			name:      "operating-manual.md",
			content:   install.OperatingManual(),
			maxBytes:  claudeManualMaxBytes,
			required:  appendAnchors(sharedManualAnchors, "--dangerously-skip-permissions"),
			forbidden: forbiddenManualAnchors,
		},
		{
			name:     "operating-manual-codex.md",
			content:  install.OperatingManualCodex(),
			maxBytes: codexManualMaxBytes,
			required: appendAnchors(sharedManualAnchors,
				".codex/agents/*.toml", "PreToolUse hooks enforce", "AGENTS.md"),
			forbidden: forbiddenManualAnchors,
		},
	}
}

func presentSpecContent(t *testing.T) string {
	t.Helper()
	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}
	for _, e := range entries {
		if filepath.ToSlash(e.RelPath) == "present-spec/SKILL.md" {
			return string(e.Content)
		}
	}
	t.Fatal("present-spec/SKILL.md not found among bundled entries")
	return ""
}

// surfaceFindings returns one line per violation of s: over the cap, a missing
// required anchor, a present forbidden anchor. It returns the lines instead of
// failing so the discriminating sibling can prove it can fail.
func surfaceFindings(s contentSurface) []string {
	var out []string
	if len(s.content) > s.maxBytes {
		out = append(out, "too big")
	}
	for _, a := range s.required {
		if !strings.Contains(s.content, a) {
			out = append(out, "missing: "+a)
		}
	}
	for _, a := range s.forbidden {
		if strings.Contains(s.content, a) {
			out = append(out, "forbidden: "+a)
		}
	}
	return out
}

// TestOperatingManuals_Size is SPEC-169 C1: the always-loaded text stays small.
func TestOperatingManuals_Size(t *testing.T) {
	for _, s := range manualSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			if got := len(s.content); got == 0 || got > s.maxBytes {
				t.Errorf("%s is %d bytes, want 1..%d", s.name, got, s.maxBytes)
			}
		})
	}
}

// TestOperatingManuals_CoreAnchors is SPEC-169 C2: the approval gate, the
// plain-language rule and the memory loop stay in both manuals, none of the
// retired surfaces comes back, and present-spec states the gate as well.
func TestOperatingManuals_CoreAnchors(t *testing.T) {
	surfaces := append(manualSurfaces(), contentSurface{
		name:     "present-spec/SKILL.md",
		content:  presentSpecContent(t),
		maxBytes: presentSpecMaxBytes,
		required: []string{"explicit yes", "is not approval", "never inherited", "this skill never relaxes it", "Plain language"},
	})
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			if got := surfaceFindings(s); len(got) > 0 {
				t.Errorf("%s: %q", s.name, got)
			}
		})
	}
}

// TestOperatingManuals_AnchorsAreDiscriminating is the sibling that fails: it
// drops each required anchor from an in-memory copy, and adds each forbidden
// one, and expects exactly that one finding. If surfaceFindings stopped
// discriminating, CoreAnchors would stay green and this test would not.
func TestOperatingManuals_AnchorsAreDiscriminating(t *testing.T) {
	for _, s := range manualSurfaces() {
		for _, anchor := range s.required {
			mutated := s
			mutated.content = strings.ReplaceAll(s.content, anchor, "")
			want := "missing: " + anchor
			if got := surfaceFindings(mutated); len(got) != 1 || got[0] != want {
				t.Errorf("%s: removing %q should give exactly %q, got %q", s.name, anchor, want, got)
			}
		}
		for _, anchor := range s.forbidden {
			mutated := s
			mutated.content = s.content + "\n" + anchor + "\n"
			// Keep the size check out of this probe: the manual sits close to
			// its cap, so the appended anchor alone could trip it.
			mutated.maxBytes = len(mutated.content)
			if got := surfaceFindings(mutated); len(got) != 1 || got[0] != "forbidden: "+anchor {
				t.Errorf("%s: adding %q should give exactly one forbidden finding, got %q", s.name, anchor, got)
			}
		}
		oversize := s
		oversize.content = s.content + strings.Repeat("x", s.maxBytes)
		if got := surfaceFindings(oversize); len(got) != 1 || got[0] != "too big" {
			t.Errorf("%s: an oversize copy should give exactly \"too big\", got %q", s.name, got)
		}
	}
}
