package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const usageSeed = `{"v":1,"ts":"2026-09-20T10:00:00Z","skill":"grill-me","session_id":"s1","role":"orchestrator","runtime":"claude-code"}
{"v":1,"ts":"2026-10-05T10:00:00Z","skill":"grill-me","session_id":"s2","role":"orchestrator","runtime":"claude-code"}
{"v":1,"ts":"2026-10-06T10:00:00Z","skill":"grill-me","session_id":"s2","role":"architect","runtime":"claude-code"}
{"v":1,"ts":"2026-10-07T10:00:00Z","skill":"plugin:other","session_id":"s3","role":"orchestrator","runtime":"claude-code"}
not json
`

// runSkillsUsage runs "mneme skills usage" against a seeded data dir from an
// isolated, non-git cwd (SPEC-085).
func runSkillsUsage(t *testing.T, argv ...string) (string, error) {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "skill-usage.jsonl"), []byte(usageSeed), 0o600); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	root := NewRootCmd()
	out := new(bytes.Buffer)
	root.SetOut(out)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(append([]string{"--data-dir", dataDir, "skills", "usage"}, argv...))
	err = root.Execute()
	return out.String(), err
}

func TestSkillsUsageCmd_TextAndJSON(t *testing.T) {
	out, err := runSkillsUsage(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path   string  `json:"path"`
		Since  *string `json:"since"`
		Skills []struct {
			Name     string         `json:"name"`
			Loads    int            `json:"loads"`
			Sessions int            `json:"sessions"`
			Roles    map[string]int `json:"roles"`
		} `json:"skills"`
		NeverUsed []string `json:"never_used"`
		Skipped   int      `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got.Since != nil || got.Skipped != 1 || len(got.Skills) != 2 {
		t.Fatalf("got = %+v", got)
	}
	g := got.Skills[0]
	if g.Name != "grill-me" || g.Loads != 3 || g.Sessions != 2 || g.Roles["orchestrator"] != 2 || g.Roles["architect"] != 1 {
		t.Fatalf("grill-me = %+v", g)
	}
	foundBundled := false
	for _, n := range got.NeverUsed {
		if n == "grill-me" {
			t.Fatalf("grill-me must not be never_used: %v", got.NeverUsed)
		}
		foundBundled = true
	}
	if !foundBundled {
		t.Fatal("expected bundled skills in never_used")
	}

	out, err = runSkillsUsage(t, "--since", "2026-10-01", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Since == nil || got.Skills[0].Loads != 2 {
		t.Fatalf("since filter: %v %+v", err, got)
	}

	out, err = runSkillsUsage(t, "--since", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Uso de skills desde 2026-10-01", "SKILL", "grill-me", "architect:1 orchestrator:1", "Nunca usadas:", "Aviso: 1 líneas ilegibles"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}

	if _, err := runSkillsUsage(t, "--since", "bogus"); err == nil || !strings.Contains(err.Error(), "--since") {
		t.Fatalf("bogus since: %v", err)
	}
}

func TestSkillsUsageCmd_EmptyLog(t *testing.T) {
	dataDir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	root := NewRootCmd()
	out := new(bytes.Buffer)
	root.SetOut(out)
	root.SetArgs([]string{"--data-dir", dataDir, "skills", "usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Todavía no hay cargas registradas") {
		t.Fatalf("out = %s", out.String())
	}
}

func TestParseUsageSince(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"", time.Time{}, false},
		{"30d", now.Add(-30 * 24 * time.Hour), false},
		{"12h", now.Add(-12 * time.Hour), false},
		{"2026-10-01", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), false},
		{"d", time.Time{}, true},
		{"-3d", time.Time{}, true},
		{"5x", time.Time{}, true},
		{"abc", time.Time{}, true},
	}
	for _, tc := range cases {
		got, err := parseUsageSince(tc.in, now)
		if (err != nil) != tc.wantErr || !got.Equal(tc.want) {
			t.Errorf("%q: got %v err %v", tc.in, got, err)
		}
	}
}
