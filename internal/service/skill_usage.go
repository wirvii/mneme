package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wirvii/mneme/internal/skill"
)

const (
	usageDirPerm  = 0o755
	usageFilePerm = 0o600
)

// SkillUsageLog is the append-only local record of skill loads
// (skill-usage.jsonl). It needs no database: the hook that feeds it must stay
// cheap and must never block the agent.
type SkillUsageLog struct {
	path string
}

// NewSkillUsageLog returns a log backed by the JSONL file at path.
func NewSkillUsageLog(path string) *SkillUsageLog {
	return &SkillUsageLog{path: path}
}

// Path returns the file the log reads and writes.
func (l *SkillUsageLog) Path() string { return l.path }

// Record appends rec as one line. The whole line goes out in a single write
// on an O_APPEND handle, so concurrent sessions never interleave partial lines.
func (l *SkillUsageLog) Record(rec skill.UsageRecord) error {
	line, err := skill.EncodeUsage(rec)
	if err != nil {
		return fmt.Errorf("service: skill usage: record: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.path), usageDirPerm); err != nil {
		return fmt.Errorf("service: skill usage: record: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, usageFilePerm)
	if err != nil {
		return fmt.Errorf("service: skill usage: record: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("service: skill usage: record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("service: skill usage: record: %w", err)
	}
	return nil
}

// Summary aggregates the log from since (zero = whole history). A missing file
// yields an empty summary, not an error.
func (l *SkillUsageLog) Summary(since time.Time, known []string) (skill.UsageSummary, error) {
	f, err := os.Open(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return skill.SummarizeUsage(nil, since, known), nil
		}
		return skill.UsageSummary{}, fmt.Errorf("service: skill usage: summary: %w", err)
	}
	defer f.Close()
	records, skipped, err := skill.ReadUsage(f)
	if err != nil {
		return skill.UsageSummary{}, fmt.Errorf("service: skill usage: summary: %w", err)
	}
	sum := skill.SummarizeUsage(records, since, known)
	sum.Skipped = skipped
	return sum, nil
}
