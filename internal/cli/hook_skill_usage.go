package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/skill"
)

const (
	// skillUsageFile is the JSONL file inside the data directory.
	skillUsageFile = "skill-usage.jsonl"

	// skillUsageRuntime is the runtime recorded for hook-fed entries.
	skillUsageRuntime = "claude-code"

	// skillUsageMaxPayload bounds how much of stdin the hook reads.
	skillUsageMaxPayload = 1 << 20

	// skillToolName is the Claude Code tool that loads a skill.
	skillToolName = "Skill"
)

// skillUsagePayload is the minimal slice of a PostToolUse payload this hook
// reads. The field that carries the skill name is not documented, so the
// candidates are tried in order (see skillNameFromInput).
type skillUsagePayload struct {
	ToolName  string                     `json:"tool_name"`
	SessionID string                     `json:"session_id"`
	ToolInput map[string]json.RawMessage `json:"tool_input"`
}

// skillNameFromInput returns the first non-empty string among tool_input
// skill, name and command, or "" when none is present.
func skillNameFromInput(in map[string]json.RawMessage) string {
	for _, key := range []string{"skill", "name", "command"} {
		raw, ok := in[key]
		if !ok {
			continue
		}
		var v string
		if json.Unmarshal(raw, &v) == nil && v != "" {
			return v
		}
	}
	return ""
}

// skillUsageLogPath resolves <data dir>/skill-usage.jsonl with the same
// precedence as the rest of the CLI: config file and environment, then
// --data-dir.
func skillUsageLogPath() (string, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return "", fmt.Errorf("cli: skill usage: load config: %w", err)
	}
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}
	return filepath.Join(cfg.Storage.DataDir, skillUsageFile), nil
}

// runHookSkillUsage is the PostToolUse hook that records a skill load. It
// never fails the agent: every problem (unreadable payload, another tool,
// missing name, write error) ends with a nil return and nothing on stdout; a
// write failure is reported as one line on errW. in is the hook payload,
// logPath the JSONL file and now the clock (injected for tests).
func runHookSkillUsage(in io.Reader, errW io.Writer, logPath string, now func() time.Time) error {
	data, err := io.ReadAll(io.LimitReader(in, skillUsageMaxPayload))
	if err != nil || len(data) == 0 {
		return nil
	}
	var payload skillUsagePayload
	if json.Unmarshal(data, &payload) != nil || payload.ToolName != skillToolName {
		return nil
	}
	name := skillNameFromInput(payload.ToolInput)
	if name == "" {
		return nil
	}
	var ident hookPreToolInput
	_ = json.Unmarshal(data, &ident)
	role := "orchestrator"
	if id := ident.resolveIdentity(); id.IsSubagent {
		role = id.Role
		if role == "" {
			role = "unresolved"
		}
	}
	rec := skill.NewUsageRecord(name, payload.SessionID, role, skillUsageRuntime, now())
	if err := service.NewSkillUsageLog(logPath).Record(rec); err != nil {
		fmt.Fprintf(errW, "[mneme] skill-usage: %v\n", err)
	}
	return nil
}

// runHookSkillUsageCmd resolves the log path and runs the hook, swallowing a
// configuration error the same way the hook swallows everything else.
func runHookSkillUsageCmd(in io.Reader, errW io.Writer) error {
	path, err := skillUsageLogPath()
	if err != nil {
		return nil
	}
	return runHookSkillUsage(in, errW, path, time.Now)
}
