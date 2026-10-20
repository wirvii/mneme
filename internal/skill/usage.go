package skill

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

const (
	// UsageRecordVersion is the version written into every usage record. A
	// reader skips records carrying a higher version.
	UsageRecordVersion = 1

	// maxUsageSkillBytes, maxUsageSessionBytes and maxUsageRoleBytes bound the
	// free-text fields so one record always stays well under PIPE_BUF and two
	// concurrent appenders never interleave their lines.
	maxUsageSkillBytes   = 200
	maxUsageSessionBytes = 128
	maxUsageRoleBytes    = 64

	// maxUsageLineBytes is the longest line ReadUsage accepts.
	maxUsageLineBytes = 64 * 1024
)

// UsageRecord is one line of the skill usage log: a single load of a skill.
// It deliberately carries no arguments, paths or content.
type UsageRecord struct {
	// V is the record format version.
	V int `json:"v"`

	// TS is the load time in UTC, second precision.
	TS time.Time `json:"ts"`

	// Skill is the skill name as the agent reported it (may carry a plugin
	// namespace such as "superpowers:brainstorming").
	Skill string `json:"skill"`

	// SessionID identifies the agent session; may be empty.
	SessionID string `json:"session_id"`

	// Role is the caller role: orchestrator, a subagent role, or unresolved.
	Role string `json:"role"`

	// Runtime names the agent runtime that produced the record.
	Runtime string `json:"runtime"`
}

// NewUsageRecord builds a version-1 record, truncating the free-text fields to
// their byte limits and normalizing the timestamp to UTC at second precision.
func NewUsageRecord(skill, sessionID, role, runtime string, now time.Time) UsageRecord {
	return UsageRecord{
		V:         UsageRecordVersion,
		TS:        now.UTC().Truncate(time.Second),
		Skill:     truncateBytes(skill, maxUsageSkillBytes),
		SessionID: truncateBytes(sessionID, maxUsageSessionBytes),
		Role:      truncateBytes(role, maxUsageRoleBytes),
		Runtime:   runtime,
	}
}

// truncateBytes cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

// EncodeUsage renders a record as one JSON line terminated by a newline.
func EncodeUsage(r UsageRecord) ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("skill: encode usage: %w", err)
	}
	return append(data, '\n'), nil
}

// ReadUsage reads every record from r. Blank lines, unreadable lines (a last
// line cut short included) and records with a version above UsageRecordVersion
// are skipped and counted; err is returned only for an I/O failure.
func ReadUsage(r io.Reader) (records []UsageRecord, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxUsageLineBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			skipped++
			continue
		}
		var rec UsageRecord
		if json.Unmarshal(line, &rec) != nil || rec.V < 1 || rec.V > UsageRecordVersion {
			skipped++
			continue
		}
		records = append(records, rec)
	}
	if scanErr := sc.Err(); scanErr != nil {
		return records, skipped, fmt.Errorf("skill: read usage: %w", scanErr)
	}
	return records, skipped, nil
}

// SkillUsage aggregates the loads of one skill.
type SkillUsage struct {
	// Name is the skill name.
	Name string `json:"name"`

	// Loads is the number of recorded loads.
	Loads int `json:"loads"`

	// Sessions is the number of distinct non-empty session ids.
	Sessions int `json:"sessions"`

	// FirstUsed and LastUsed bound the loads in the window.
	FirstUsed time.Time `json:"first_used"`
	LastUsed  time.Time `json:"last_used"`

	// Roles counts loads per caller role.
	Roles map[string]int `json:"roles"`
}

// UsageSummary is the aggregate SummarizeUsage returns.
type UsageSummary struct {
	// Since is the window start, nil when the whole history is covered.
	Since *time.Time

	// Skills is ordered by Loads descending, then Name ascending.
	Skills []SkillUsage

	// NeverUsed lists known skills with no load in the window, alphabetical.
	NeverUsed []string

	// Skipped is filled by the caller with the count ReadUsage returned.
	Skipped int
}

// SummarizeUsage aggregates records at or after since (a zero since keeps all)
// and lists the names in known that were never loaded in that window.
func SummarizeUsage(records []UsageRecord, since time.Time, known []string) UsageSummary {
	sum := UsageSummary{}
	if !since.IsZero() {
		s := since
		sum.Since = &s
	}
	byName := map[string]*SkillUsage{}
	sessions := map[string]map[string]bool{}
	for _, r := range records {
		if !since.IsZero() && r.TS.Before(since) {
			continue
		}
		u := byName[r.Skill]
		if u == nil {
			u = &SkillUsage{Name: r.Skill, FirstUsed: r.TS, LastUsed: r.TS, Roles: map[string]int{}}
			byName[r.Skill] = u
			sessions[r.Skill] = map[string]bool{}
		}
		u.Loads++
		u.Roles[r.Role]++
		if r.TS.Before(u.FirstUsed) {
			u.FirstUsed = r.TS
		}
		if r.TS.After(u.LastUsed) {
			u.LastUsed = r.TS
		}
		if r.SessionID != "" {
			sessions[r.Skill][r.SessionID] = true
		}
	}
	for name, u := range byName {
		u.Sessions = len(sessions[name])
		sum.Skills = append(sum.Skills, *u)
	}
	sort.Slice(sum.Skills, func(i, j int) bool {
		if sum.Skills[i].Loads != sum.Skills[j].Loads {
			return sum.Skills[i].Loads > sum.Skills[j].Loads
		}
		return sum.Skills[i].Name < sum.Skills[j].Name
	})
	for _, k := range known {
		if byName[k] == nil {
			sum.NeverUsed = append(sum.NeverUsed, k)
		}
	}
	sort.Strings(sum.NeverUsed)
	return sum
}
