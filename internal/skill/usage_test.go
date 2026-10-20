package skill_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/skill"
)

var usageNow = time.Date(2026, 10, 20, 3, 4, 5, 999, time.FixedZone("x", 3600))

// TestUsage_EncodeReadRoundTrip verifies exact round trip and the wire shape.
func TestUsage_EncodeReadRoundTrip(t *testing.T) {
	rec := skill.NewUsageRecord("grill-me", "s1", "orchestrator", "claude-code", usageNow)
	line, err := skill.EncodeUsage(rec)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"ts":"2026-10-20T02:04:05Z","skill":"grill-me","session_id":"s1","role":"orchestrator","runtime":"claude-code"}` + "\n"
	if string(line) != want {
		t.Fatalf("line = %q", line)
	}
	got, skipped, err := skill.ReadUsage(bytes.NewReader(append(line, line...)))
	if err != nil || skipped != 0 || len(got) != 2 || !reflect.DeepEqual(got[0], rec) {
		t.Fatalf("got %v skipped %d err %v", got, skipped, err)
	}
}

// TestUsage_ReadSkipsMalformedLines verifies bad lines are counted, not fatal.
func TestUsage_ReadSkipsMalformedLines(t *testing.T) {
	good := `{"v":1,"ts":"2026-10-20T02:04:05Z","skill":"a","session_id":"","role":"r","runtime":"c"}`
	in := strings.Join([]string{
		good,
		"",
		"not json",
		`{"v":2,"ts":"2026-10-20T02:04:05Z","skill":"future"}`,
		`{"ts":"2026-10-20T02:04:05Z","skill":"nov"}`,
		good[:40], // last line cut short
	}, "\n")
	got, skipped, err := skill.ReadUsage(strings.NewReader(in))
	if err != nil || len(got) != 1 || skipped != 5 {
		t.Fatalf("got %d records skipped %d err %v", len(got), skipped, err)
	}
}

// TestUsage_SummarizeOrderingAndNeverUsed verifies ordering, window and never-used.
func TestUsage_SummarizeOrderingAndNeverUsed(t *testing.T) {
	at := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	recs := []skill.UsageRecord{
		{V: 1, TS: at(1), Skill: "b", SessionID: "s1", Role: "orchestrator"},
		{V: 1, TS: at(2), Skill: "b", SessionID: "s1", Role: "architect"},
		{V: 1, TS: at(3), Skill: "a", SessionID: "s2", Role: "orchestrator"},
		{V: 1, TS: at(4), Skill: "c", SessionID: "s3", Role: "orchestrator"},
		{V: 1, TS: at(5), Skill: "c", SessionID: "", Role: "orchestrator"},
		{V: 1, TS: at(6), Skill: "c", SessionID: "s4", Role: "orchestrator"},
	}
	sum := skill.SummarizeUsage(recs, time.Time{}, []string{"z", "a", "m"})
	var names []string
	for _, s := range sum.Skills {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"c", "b", "a"}) {
		t.Fatalf("order = %v", names)
	}
	c := sum.Skills[0]
	if c.Loads != 3 || c.Sessions != 2 || !c.FirstUsed.Equal(at(4)) || !c.LastUsed.Equal(at(6)) {
		t.Fatalf("c = %+v", c)
	}
	if b := sum.Skills[1]; b.Roles["architect"] != 1 || b.Roles["orchestrator"] != 1 {
		t.Fatalf("b roles = %v", b.Roles)
	}
	if !reflect.DeepEqual(sum.NeverUsed, []string{"m", "z"}) || sum.Since != nil {
		t.Fatalf("never %v since %v", sum.NeverUsed, sum.Since)
	}
	win := skill.SummarizeUsage(recs, at(4), []string{"a", "c"})
	if len(win.Skills) != 1 || win.Skills[0].Name != "c" || !reflect.DeepEqual(win.NeverUsed, []string{"a"}) || win.Since == nil {
		t.Fatalf("window = %+v", win)
	}
}

// TestUsage_Truncation verifies the byte limits and UTF-8 safety.
func TestUsage_Truncation(t *testing.T) {
	rec := skill.NewUsageRecord(strings.Repeat("s", 500), strings.Repeat("i", 500), strings.Repeat("r", 500), "c", usageNow)
	if len(rec.Skill) != 200 || len(rec.SessionID) != 128 || len(rec.Role) != 64 {
		t.Fatalf("lens %d %d %d", len(rec.Skill), len(rec.SessionID), len(rec.Role))
	}
	multi := skill.NewUsageRecord(strings.Repeat("é", 151), "", "", "c", usageNow) // 302 bytes
	if len(multi.Skill) != 200 || strings.ContainsRune(multi.Skill, '�') {
		t.Fatalf("skill len %d", len(multi.Skill))
	}
	line, _ := skill.EncodeUsage(rec)
	if len(line) >= 4096 {
		t.Fatalf("line too long: %d", len(line))
	}
}
