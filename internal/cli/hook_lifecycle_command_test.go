package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/enforcement"
)

// --- SPEC-175: lifecycle commands spoken through Bash -------------------------
//
// Same constraint as hook_lifecycle_test.go: the block path calls os.Exit(2),
// so every row re-executes the test binary and asserts the real exit code.

// bashPayload builds a PreToolUse payload for the Bash tool. agentFields is the
// raw JSON object body (without braces) that identifies the caller, empty for
// the orchestrator.
func bashPayload(t *testing.T, agentFields, command string) string {
	t.Helper()
	cmd, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	prefix := ""
	if agentFields != "" {
		prefix = agentFields + ","
	}
	return `{` + prefix + `"tool_name":"Bash","tool_input":{"command":` + string(cmd) + `}}`
}

const (
	resolvedSubagent = `"agent_id":"x","agent_type":"backend"`
	unresolvedAgent  = `"agent_id":"x"`
	// codexChild mirrors the shape of a Codex child's hook input: the extra
	// turn_id/model/permission_mode fields must not change the decision.
	codexChild = `"agent_id":"x","agent_type":"backend","turn_id":"t1","model":"gpt-5","permission_mode":"default"`
)

func TestRunHookEnforceDelegation_LifecycleCommands_Table(t *testing.T) {
	isolated := t.TempDir()
	tests := []struct {
		name     string
		agent    string
		command  string
		wantExit int
		wantVerb string
	}{
		{"spec advance", resolvedSubagent, `mneme spec advance SPEC-1`, 2, "spec advance"},
		{"sdd import", resolvedSubagent, `./mneme sdd import`, 2, "sdd import"},
		{"go run backlog archive", resolvedSubagent, `go run ./cmd/mneme backlog archive BL-1`, 2, "backlog archive"},
		{"lane override", resolvedSubagent, `mneme lane override S`, 2, "lane override"},
		{"agent_type absent", unresolvedAgent, `mneme spec advance SPEC-1`, 2, "spec advance"},
		{"codex-shaped payload", codexChild, `mneme spec advance S`, 2, "spec advance"},
		{"orchestrator", "", `mneme spec advance SPEC-1`, 0, ""},
		{"isolated HOME", resolvedSubagent, `HOME=` + isolated + ` mneme spec advance S`, 0, ""},
		{"isolated --data-dir", resolvedSubagent, `mneme --data-dir ` + isolated + ` spec advance S`, 0, ""},
		{"spec reject allowed", resolvedSubagent, `mneme spec reject S`, 0, ""},
		{"unrelated command", resolvedSubagent, `ls -la`, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode, stderr := runHookLifecycleSubprocess(t, bashPayload(t, tt.agent, tt.command))
			if exitCode != tt.wantExit {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", exitCode, tt.wantExit, stderr)
			}
			if tt.wantExit != 2 {
				return
			}
			for _, want := range []string{
				"`mneme " + tt.wantVerb + "` es del orquestador",
				"devuélvelo al orquestador",
				"--data-dir",
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q: %q", want, stderr)
				}
			}
		})
	}
}

func TestRunHookEnforceDelegation_LaneOverride_NamesItsOwnReason(t *testing.T) {
	_, stderr := runHookLifecycleSubprocess(t,
		`{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__lane_override","tool_input":{"id":"SPEC-1"}}`)
	for _, want := range []string{"lane_override", "devuélvelo al orquestador"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q: %q", want, stderr)
		}
	}
	if strings.Contains(stderr, "el lifecycle SDD lo gobierna el orquestador") {
		t.Errorf("lane_override reused the generic reason: %q", stderr)
	}
}

// TestRunHookEnforceDelegation_LifecycleCommand_NeverLogsDiscoveryMemory checks
// that a Bash block writes only the enforcelog event: no project database, and
// one JSONL event with the SPEC-175 reason.
func TestRunHookEnforceDelegation_LifecycleCommand_NeverLogsDiscoveryMemory(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".mneme")
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_RunHookEnforceDelegation$", "-test.v")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		hookLifecycleHelperProcessEnv+"=1",
		hookLifecyclePayloadEnv+"="+bashPayload(t, resolvedSubagent, `mneme spec advance S`),
		// The subprocess's TestMain re-sandboxes HOME, so the data directory is
		// pinned explicitly to find the enforcelog the hook writes.
		"MNEME_DATA_DIR="+dataDir,
	)
	_ = cmd.Run()

	projectsDir := filepath.Join(dataDir, "projects")
	if entries, err := os.ReadDir(projectsDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".db") {
				t.Errorf("found project database %s after a lifecycle command block", e.Name())
			}
		}
	}

	logs, err := filepath.Glob(filepath.Join(projectsDir, "*-enforce.jsonl"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("enforcelog files = %v (err %v), want exactly one", logs, err)
	}
	events, err := enforcelog.Read(logs[0])
	if err != nil {
		t.Fatalf("read enforcelog: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("enforcelog events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Tool != "Bash" || ev.Decision != enforcelog.DecisionBlock ||
		ev.Target != "mneme spec advance" || ev.Reason != "lifecycle_command_denied_to_subagent" {
		t.Errorf("unexpected enforcelog event: %+v", ev)
	}
}

// TestLifecycleTools_MirroredByLifecycleCommands keeps the MCP denial list and
// the Bash command set from drifting: every tool name, minus its prefix and
// with the first "_" read as a space, must be a lifecycle command.
func TestLifecycleTools_MirroredByLifecycleCommands(t *testing.T) {
	commands := map[string]bool{}
	for _, c := range enforcement.LifecycleCommands() {
		commands[c] = true
	}
	for tool := range lifecycleTools {
		name := strings.Replace(strings.TrimPrefix(tool, "mcp__mneme__"), "_", " ", 1)
		if !commands[name] {
			t.Errorf("lifecycleTools entry %q has no lifecycle command %q", tool, name)
		}
	}
}

// TestLifecycleCommands_ResolveToRealCobraCommands stops the Bash guard from
// watching a dead name: each entry must resolve to a real subcommand whose
// Name() is the verb.
func TestLifecycleCommands_ResolveToRealCobraCommands(t *testing.T) {
	root := NewRootCmd()
	for _, c := range enforcement.LifecycleCommands() {
		words := strings.Fields(c)
		cmd, rest, err := root.Find(words)
		if err != nil {
			t.Errorf("Find(%q): %v", c, err)
			continue
		}
		if len(rest) != 0 || cmd.Name() != words[1] {
			t.Errorf("Find(%q) = %q with leftovers %v, want a command named %q", c, cmd.Name(), rest, words[1])
		}
	}
}
