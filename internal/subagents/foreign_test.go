package subagents

import "testing"

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
