package querylog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mustAppend appends ev and fails the test on error.
func mustAppend(t *testing.T, path string, ev Event, maxBytes int64) {
	t.Helper()
	if err := Append(path, ev, maxBytes); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestAppendAndRead_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ql.jsonl")
	base := time.Date(2026, 7, 14, 22, 0, 0, 0, time.UTC)

	events := []Event{
		{TS: base, Session: "s1", Project: "wirvii/mneme", Kind: KindOpportunity, Tool: "Grep", Source: "hook"},
		{TS: base.Add(time.Second), Session: "s1", Project: "wirvii/mneme", Kind: KindOpportunity, Tool: "bash:rg", Source: "hook"},
		{TS: base.Add(2 * time.Second), Project: "wirvii/mneme", Kind: KindUse, Tool: "codegraph_search", Source: "mcp"},
	}
	for _, ev := range events {
		mustAppend(t, path, ev, DefaultMaxBytes)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("Read returned %d events, want %d", len(got), len(events))
	}
	for i, ev := range events {
		if got[i].Kind != ev.Kind || got[i].Tool != ev.Tool || got[i].Source != ev.Source {
			t.Errorf("event %d: got %+v, want %+v", i, got[i], ev)
		}
		if !got[i].TS.Equal(ev.TS) {
			t.Errorf("event %d: TS got %v, want %v", i, got[i].TS, ev.TS)
		}
	}
}

func TestAppend_Permissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ql.jsonl")
	mustAppend(t, path, Event{TS: time.Now().UTC(), Project: "p", Kind: KindUse, Tool: "codegraph_search", Source: "mcp"}, DefaultMaxBytes)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}
}

// TestAppend_OmitsSessionWhenEmpty verifies the JSON omits the session field
// entirely for MCP-side events (no session id).
func TestAppend_OmitsSessionWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ql.jsonl")
	mustAppend(t, path, Event{TS: time.Now().UTC(), Project: "p", Kind: KindUse, Tool: "codegraph_search", Source: "mcp"}, DefaultMaxBytes)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "\"session\"") {
		t.Errorf("empty session must be omitted, got line: %s", data)
	}
}

// TestAppend_NeverStoresSensitiveData is a privacy guard: the marshalled line
// only ever contains the declared fields, never a path/command/query.
func TestAppend_PrivacyFieldsOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ql.jsonl")
	mustAppend(t, path, Event{TS: time.Now().UTC(), Session: "s", Project: "p", Kind: KindOpportunity, Tool: "bash:grep", Source: "hook"}, DefaultMaxBytes)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	allowed := map[string]bool{"ts": true, "session": true, "project": true, "kind": true, "tool": true, "source": true}
	for k := range m {
		if !allowed[k] {
			t.Errorf("unexpected field %q in serialised event — privacy leak?", k)
		}
	}
}

func TestRead_SkipsCorruptLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ql.jsonl")
	valid := Event{TS: time.Now().UTC(), Project: "p", Kind: KindUse, Tool: "codegraph_search", Source: "mcp"}
	mustAppend(t, path, valid, DefaultMaxBytes)

	// Append a corrupt line and a blank line manually.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("{ this is not valid json\n\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	_ = f.Close()

	mustAppend(t, path, valid, DefaultMaxBytes)

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 valid events (corrupt/blank skipped), got %d", len(got))
	}
}

func TestRead_MissingFile(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err != nil {
		t.Fatalf("Read of missing file should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events, got %d", len(got))
	}
}

// TestAppend_RotatesOverCap verifies rotation to "<path>.1" once the cap is
// exceeded and that Read merges both files chronologically.
func TestAppend_RotatesOverCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ql.jsonl")
	base := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)

	// Tiny cap so the second append triggers rotation.
	const cap = 120
	first := Event{TS: base, Project: "p", Kind: KindOpportunity, Tool: "Grep", Source: "hook"}
	second := Event{TS: base.Add(time.Minute), Project: "p", Kind: KindUse, Tool: "codegraph_search", Source: "mcp"}

	mustAppend(t, path, first, cap)
	mustAppend(t, path, second, cap)

	// After exceeding the cap, the live file was rotated to <path>.1.
	if _, err := os.Stat(path + rotatedSuffix); err != nil {
		t.Fatalf("expected rotated backup at %s.1: %v", path, err)
	}

	// A third append re-creates the live file.
	third := Event{TS: base.Add(2 * time.Minute), Project: "p", Kind: KindUse, Tool: "codegraph_context", Source: "mcp"}
	mustAppend(t, path, third, cap)

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Read must merge backup (.1) then live, chronologically.
	if len(got) < 3 {
		t.Fatalf("expected >=3 events across rotation, got %d", len(got))
	}
	if !got[0].TS.Equal(base) {
		t.Errorf("first event should be the oldest (from backup), got %v", got[0].TS)
	}
}

func TestAppend_NoRotationWhenCapNonPositive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ql.jsonl")
	for i := 0; i < 50; i++ {
		mustAppend(t, path, Event{TS: time.Now().UTC(), Project: "p", Kind: KindUse, Tool: "codegraph_search", Source: "mcp"}, 0)
	}
	if _, err := os.Stat(path + rotatedSuffix); !os.IsNotExist(err) {
		t.Errorf("no rotation expected when maxBytes<=0, but backup exists (err=%v)", err)
	}
}

func TestAggregate_RatioAndTops(t *testing.T) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	mk := func(kind Kind, tool string, offsetHours int) Event {
		return Event{TS: since.Add(time.Duration(offsetHours) * time.Hour), Project: "p", Kind: kind, Tool: tool, Source: "x"}
	}
	events := []Event{
		mk(KindUse, "codegraph_search", 1),
		mk(KindUse, "codegraph_search", 2),
		mk(KindUse, "codegraph_context", 3),
		mk(KindOpportunity, "Grep", 4),
		mk(KindOpportunity, "Grep", 5),
		mk(KindOpportunity, "Read", 6),
		mk(KindOpportunity, "bash:rg", 7),
		// Before the window — must be excluded.
		{TS: since.Add(-time.Hour), Project: "p", Kind: KindUse, Tool: "codegraph_search", Source: "x"},
	}

	report := Aggregate(events, since)
	if report.Uses != 3 {
		t.Errorf("Uses = %d, want 3", report.Uses)
	}
	if report.Opportunities != 4 {
		t.Errorf("Opportunities = %d, want 4", report.Opportunities)
	}
	wantRatio := 3.0 / 7.0
	if report.AdoptionRatio < wantRatio-1e-9 || report.AdoptionRatio > wantRatio+1e-9 {
		t.Errorf("AdoptionRatio = %v, want %v", report.AdoptionRatio, wantRatio)
	}
	if len(report.TopUseTools) == 0 || report.TopUseTools[0].Tool != "codegraph_search" || report.TopUseTools[0].Count != 2 {
		t.Errorf("TopUseTools[0] = %+v, want codegraph_search x2", report.TopUseTools)
	}
	if len(report.TopMissedTools) == 0 || report.TopMissedTools[0].Tool != "Grep" || report.TopMissedTools[0].Count != 2 {
		t.Errorf("TopMissedTools[0] = %+v, want Grep x2", report.TopMissedTools)
	}
}

func TestAggregate_ZeroDenominator(t *testing.T) {
	report := Aggregate(nil, time.Now())
	if report.AdoptionRatio != 0 {
		t.Errorf("empty aggregate ratio = %v, want 0", report.AdoptionRatio)
	}
	if report.Uses != 0 || report.Opportunities != 0 {
		t.Errorf("empty aggregate should have zero counts, got uses=%d opps=%d", report.Uses, report.Opportunities)
	}
	if report.TopUseTools != nil || report.TopMissedTools != nil {
		t.Errorf("empty aggregate should have nil top slices")
	}
}

// TestAggregate_TieBreakDeterministic verifies equal counts sort by tool name.
func TestAggregate_TieBreakDeterministic(t *testing.T) {
	since := time.Time{}
	events := []Event{
		{Kind: KindOpportunity, Tool: "Read"},
		{Kind: KindOpportunity, Tool: "Grep"},
		{Kind: KindOpportunity, Tool: "Glob"},
	}
	report := Aggregate(events, since)
	got := []string{report.TopMissedTools[0].Tool, report.TopMissedTools[1].Tool, report.TopMissedTools[2].Tool}
	want := []string{"Glob", "Grep", "Read"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tie-break order = %v, want %v", got, want)
			break
		}
	}
}
