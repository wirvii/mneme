package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/skill"
)

var skillUsageTestNow = func() time.Time { return time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC) }

// runSkillUsageHook drives the hook and returns the records written, stderr and the error.
func runSkillUsageHook(t *testing.T, payload string) ([]skill.UsageRecord, string, error) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "skill-usage.jsonl")
	var errBuf bytes.Buffer
	err := runHookSkillUsage(strings.NewReader(payload), &errBuf, logPath, skillUsageTestNow)
	return readSkillUsageFile(t, logPath), errBuf.String(), err
}

func readSkillUsageFile(t *testing.T, path string) []skill.UsageRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	recs, _, err := skill.ReadUsage(f)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestHookSkillUsage_RecordsSkillLoad(t *testing.T) {
	recs, stderr, err := runSkillUsageHook(t, `{"tool_name":"Skill","tool_input":{"skill":"grill-me"},"session_id":"s1"}`)
	if err != nil || stderr != "" || len(recs) != 1 {
		t.Fatalf("recs %v stderr %q err %v", recs, stderr, err)
	}
	r := recs[0]
	if r.Skill != "grill-me" || r.SessionID != "s1" || r.Role != "orchestrator" || r.Runtime != "claude-code" || r.V != 1 {
		t.Fatalf("record = %+v", r)
	}
}

func TestHookSkillUsage_RoleFromAgentType(t *testing.T) {
	cases := []struct {
		name, payload, role string
	}{
		{"agent type", `{"tool_name":"Skill","tool_input":{"skill":"x"},"agent_id":"a1","agent_type":"architect"}`, "architect"},
		{"agent id only", `{"tool_name":"Skill","tool_input":{"skill":"x"},"agent_id":"a1"}`, "unresolved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, _, err := runSkillUsageHook(t, tc.payload)
			if err != nil || len(recs) != 1 || recs[0].Role != tc.role {
				t.Fatalf("recs %v err %v", recs, err)
			}
		})
	}
}

func TestHookSkillUsage_ToleratesMissingFields(t *testing.T) {
	cases := []struct {
		name, payload string
	}{
		{"other tool", `{"tool_name":"Bash","tool_input":{"skill":"x"}}`},
		{"no tool_input", `{"tool_name":"Skill"}`},
		{"no name", `{"tool_name":"Skill","tool_input":{"args":"y"}}`},
		{"non string name", `{"tool_name":"Skill","tool_input":{"skill":5}}`},
		{"broken json", `{"tool_name":`},
		{"empty stdin", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, stderr, err := runSkillUsageHook(t, tc.payload)
			if err != nil || stderr != "" || len(recs) != 0 {
				t.Fatalf("recs %v stderr %q err %v", recs, stderr, err)
			}
		})
	}

	t.Run("unwritable log", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		var errBuf bytes.Buffer
		err := runHookSkillUsage(strings.NewReader(`{"tool_name":"Skill","tool_input":{"skill":"x"}}`),
			&errBuf, filepath.Join(blocker, "x.jsonl"), skillUsageTestNow)
		if err != nil || strings.Count(errBuf.String(), "\n") != 1 || !strings.HasPrefix(errBuf.String(), "[mneme] skill-usage:") {
			t.Fatalf("err %v stderr %q", err, errBuf.String())
		}
	})
}

func TestHookSkillUsage_FallbackFieldNames(t *testing.T) {
	cases := []struct{ payload, want string }{
		{`{"tool_name":"Skill","tool_input":{"name":"by-name"}}`, "by-name"},
		{`{"tool_name":"Skill","tool_input":{"command":"by-command"}}`, "by-command"},
		{`{"tool_name":"Skill","tool_input":{"skill":"","name":"second","command":"third"}}`, "second"},
	}
	for _, tc := range cases {
		recs, _, err := runSkillUsageHook(t, tc.payload)
		if err != nil || len(recs) != 1 || recs[0].Skill != tc.want {
			t.Fatalf("%s: recs %v err %v", tc.payload, recs, err)
		}
	}
}

func TestHookSkillUsageCmd_UsesDataDir(t *testing.T) {
	dir := t.TempDir()
	old := flagDataDir
	flagDataDir = dir
	t.Cleanup(func() { flagDataDir = old })

	if err := runHookSkillUsageCmd(strings.NewReader(`{"tool_name":"Skill","tool_input":{"skill":"x"}}`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if recs := readSkillUsageFile(t, filepath.Join(dir, "skill-usage.jsonl")); len(recs) != 1 {
		t.Fatalf("recs = %v", recs)
	}
}
