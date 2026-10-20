package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/skill"
)

func TestSkillUsageLog_RecordAndSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "skill-usage.jsonl")
	log := NewSkillUsageLog(path)

	empty, err := log.Summary(time.Time{}, []string{"a"})
	if err != nil || len(empty.Skills) != 0 || len(empty.NeverUsed) != 1 {
		t.Fatalf("missing file: %+v err %v", empty, err)
	}

	now := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := log.Record(skill.NewUsageRecord("a", "s", "orchestrator", "claude-code", now)); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("garbage\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	sum, err := log.Summary(time.Time{}, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Skills) != 1 || sum.Skills[0].Loads != 2 || sum.Skipped != 1 || len(sum.NeverUsed) != 1 || sum.NeverUsed[0] != "b" {
		t.Fatalf("summary = %+v", sum)
	}
	if log.Path() != path {
		t.Fatal("path")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("perm: %v %v", info, err)
	}
}

func TestSkillUsageLog_RecordError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewSkillUsageLog(filepath.Join(blocker, "x.jsonl")).Record(skill.UsageRecord{V: 1}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := NewSkillUsageLog(dir).Summary(time.Time{}, nil); err == nil {
		t.Fatal("expected error reading a directory")
	}
}
