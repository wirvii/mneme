package subagents

import (
	"strings"
	"testing"
)

// TestIsForeignAgentFile_G6 is G6: a profile carrying the agent-fixed
// managed block is never foreign; one lacking it always is. Table-driven so
// both branches are pinned in one guardian.
func TestIsForeignAgentFile_G6(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "mneme-composed profile with the agent-fixed block is not foreign",
			content: "---\nname: backend\n---\n<!-- mneme:agent-fixed:start v=2 -->\nx\n<!-- mneme:agent-fixed:end -->\n\n## Área: x\n\ny\n",
			want:    false,
		},
		{
			name:    "hand-authored profile with no agent-fixed block is foreign",
			content: "---\nname: security-auditor\ndescription: A dev-authored agent.\n---\n\nSome custom instructions.\n",
			want:    true,
		},
		{
			name:    "empty content is foreign",
			content: "",
			want:    true,
		},
		{
			name:    "content mentioning agent-fixed in prose but without the real marker is foreign",
			content: "This document mentions agent-fixed but has no real managed block.",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsForeignAgentFile(tt.content); got != tt.want {
				t.Errorf("IsForeignAgentFile(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

// TestIsForeignAgentFile_ComposedProfileWithCRLFIsNotForeign is SPEC-140
// AC3: a profile mneme itself generated must not read as foreign just
// because the file was checked out with core.autocrlf=true's "\r\n" line
// endings. The input is derived from Compose — never a hand-typed string —
// so the case tracks whatever mneme's own writer actually produces. The
// negative control in the same table (no managed block at all) must keep
// returning true regardless of line endings, proving the CRLF tolerance
// does not accidentally widen what counts as "not foreign".
func TestIsForeignAgentFile_ComposedProfileWithCRLFIsNotForeign(t *testing.T) {
	composed, err := Compose("", ComposeInput{
		Role:        RoleBackend,
		Description: "Invocar para implementar backend.",
		Model:       "sonnet",
		Body:        "# Backend Agent\n\nHaz las cosas bien.\n",
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "mneme-composed profile with CRLF line endings is not foreign",
			content: strings.ReplaceAll(composed, "\n", "\r\n"),
			want:    false,
		},
		{
			name:    "control: hand-authored text with no managed block, also CRLF, stays foreign",
			content: strings.ReplaceAll("---\nname: security-auditor\n---\n\nSome custom instructions.\n", "\n", "\r\n"),
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsForeignAgentFile(tt.content); got != tt.want {
				t.Errorf("IsForeignAgentFile(...) = %v, want %v", got, tt.want)
			}
		})
	}
}
