package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/subagents"
)

// TestDetectForeignAgents covers SPEC-163 D5: the verdict that moved from the
// retired MCP handler into the service layer must keep its exact semantics.
func TestDetectForeignAgents(t *testing.T) {
	t.Parallel()

	const ownedContent = "---\nname: x\n---\n<!-- mneme:agent-fixed:start v=5 -->\nbody\n<!-- mneme:agent-fixed:end -->\n"
	const handWritten = "---\nname: x\n---\nhand-authored agent\n"

	tests := []struct {
		name     string
		files    map[string]string // filename under .claude/agents; nil = no directory
		manifest []string          // roles recorded in the manifest
		want     []string
	}{
		{name: "no agents directory", files: nil, want: []string{}},
		{
			name:     "owned and in manifest is not foreign",
			files:    map[string]string{"backend.md": ownedContent},
			manifest: []string{"backend"},
			want:     []string{},
		},
		{
			name:  "no agent-fixed block is foreign",
			files: map[string]string{"mine.md": handWritten},
			want:  []string{".claude/agents/mine.md"},
		},
		{
			name:     "block present but role missing from manifest is foreign",
			files:    map[string]string{"ghost.md": ownedContent},
			manifest: []string{"backend"},
			want:     []string{".claude/agents/ghost.md"},
		},
		{
			name: "result is sorted and skips non-markdown",
			files: map[string]string{
				"zeta.md": handWritten, "alpha.md": handWritten, "notes.txt": handWritten,
			},
			want: []string{".claude/agents/alpha.md", ".claude/agents/zeta.md"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newSubagentTestService(t)
			ctx := context.Background()
			root := t.TempDir()

			if tc.files != nil {
				dir := filepath.Join(root, ".claude", "agents")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				for name, content := range tc.files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
						t.Fatalf("write %s: %v", name, err)
					}
				}
			}
			if len(tc.manifest) > 0 {
				entries := make([]service.ManifestEntry, 0, len(tc.manifest))
				for _, role := range tc.manifest {
					entries = append(entries, service.ManifestEntry{Role: subagents.Role(role)})
				}
				if _, err := svc.SaveManifest(ctx, "test/project", entries); err != nil {
					t.Fatalf("SaveManifest: %v", err)
				}
			}

			got, err := svc.DetectForeignAgents(ctx, "test/project", root)
			if err != nil {
				t.Fatalf("DetectForeignAgents: %v", err)
			}
			if got == nil || len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
