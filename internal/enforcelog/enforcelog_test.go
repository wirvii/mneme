package enforcelog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enforce.jsonl")

	ev := Event{
		TS:         time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Project:    "wirvii/mneme",
		Caller:     "subagent",
		AgentID:    "abc123",
		Role:       "frontend",
		RoleSource: "payload",
		Tool:       "Edit",
		Target:     "internal/store/foo.go",
		Decision:   DecisionWouldBlock,
		Mode:       "warn",
		Reason:     "path outside frontend's declared areas",
		Owner:      "backend",
	}
	if err := Append(path, ev, DefaultMaxBytes); err != nil {
		t.Fatalf("Append: %v", err)
	}

	events, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Target != "internal/store/foo.go" || events[0].Owner != "backend" {
		t.Errorf("event = %+v, want target/owner round-tripped", events[0])
	}
}

func TestAppend_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enforce.jsonl")

	if err := Append(path, Event{TS: time.Now(), Project: "p"}, DefaultMaxBytes); err != nil {
		t.Fatalf("Append: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 0600", perm)
	}
}

func TestRead_MissingFile_ReturnsEmptyNotError(t *testing.T) {
	events, err := Read(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("len(events) = %d, want 0", len(events))
	}
}

func TestRead_SkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enforce.jsonl")
	content := "not json\n" + `{"ts":"2026-07-16T00:00:00Z","project":"p","decision":"allow"}` + "\n\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	events, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1 (corrupt/blank lines skipped)", len(events))
	}
}

func TestAppend_RotatesPastMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enforce.jsonl")

	ev := Event{TS: time.Now(), Project: "p", Target: "internal/x.go", Decision: DecisionAllow}
	// First append establishes the file; second, with a tiny maxBytes, must
	// trigger rotation.
	if err := Append(path, ev, DefaultMaxBytes); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := Append(path, ev, 10); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	if _, err := os.Stat(path + rotatedSuffix); err != nil {
		t.Errorf("expected rotated backup at %s: %v", path+rotatedSuffix, err)
	}
}

func TestAggregate_CountsByRoleAndUnresolved(t *testing.T) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{TS: since.Add(time.Hour), Role: "frontend", Decision: DecisionWouldBlock, Target: "a"},
		{TS: since.Add(2 * time.Hour), Role: "frontend", Decision: DecisionWouldBlock, Target: "b"},
		{TS: since.Add(3 * time.Hour), Role: "backend", Decision: DecisionAllow},
		{TS: since.Add(4 * time.Hour), Role: "backend", Decision: DecisionBlock},
		{TS: since.Add(5 * time.Hour), RoleSource: "unresolved"},
		{TS: since.Add(-time.Hour), Role: "frontend", Decision: DecisionWouldBlock}, // before window
	}

	report := Aggregate(events, since)

	if report.Total != 5 {
		t.Errorf("Total = %d, want 5 (event before `since` excluded)", report.Total)
	}
	if report.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", report.Unresolved)
	}
	if len(report.ByRole) != 2 {
		t.Fatalf("len(ByRole) = %d, want 2", len(report.ByRole))
	}
	var frontend, backend *RoleReport
	for i := range report.ByRole {
		switch report.ByRole[i].Role {
		case "frontend":
			frontend = &report.ByRole[i]
		case "backend":
			backend = &report.ByRole[i]
		}
	}
	if frontend == nil || frontend.WouldBlock != 2 {
		t.Errorf("frontend = %+v, want WouldBlock=2", frontend)
	}
	if backend == nil || backend.Allowed != 1 || backend.Blocked != 1 {
		t.Errorf("backend = %+v, want Allowed=1 Blocked=1", backend)
	}
}

// TestAggregate_SamplePathsCapped guards maxSamplePaths: deleting the cap
// would let SamplePaths grow unbounded for a busy role.
func TestAggregate_SamplePathsCapped(t *testing.T) {
	since := time.Unix(0, 0).UTC()
	var events []Event
	for i := 0; i < maxSamplePaths+5; i++ {
		events = append(events, Event{TS: since.Add(time.Duration(i) * time.Minute), Role: "frontend", Decision: DecisionWouldBlock, Target: "path"})
	}

	report := Aggregate(events, since)
	if len(report.ByRole) != 1 || len(report.ByRole[0].SamplePaths) != maxSamplePaths {
		t.Errorf("SamplePaths len = %d, want %d", len(report.ByRole[0].SamplePaths), maxSamplePaths)
	}
}
