package vault

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// Frontmatter holds the metadata serialized as YAML between the --- delimiters
// at the top of each vault note. Fields are exported so SPEC-M2 can use a YAML
// parser to read them back. The yaml struct tags are documentary only — this
// spec serializes manually for deterministic field order without a YAML dependency.
//
// Inclusion rules (see spec D5):
//   - id, type, scope, title, importance, confidence, decay_rate,
//     created_at, updated_at, revision_count — always present.
//   - topic_key, project, created_by, files, applies_to, severity,
//     superseded_by, shared, author, source — present only when non-empty
//     (shared is additionally omitted when it is the default/local value 0,
//     per SPEC-053 D2/D7 so peers without team-memory never see the field).
//
// Excluded: access_count, last_accessed, deleted_at, session_id.
type Frontmatter struct {
	ID            string   `yaml:"id"`
	Type          string   `yaml:"type"`
	Scope         string   `yaml:"scope"`
	Title         string   `yaml:"title"`
	TopicKey      string   `yaml:"topic_key,omitempty"`
	Project       string   `yaml:"project,omitempty"`
	Importance    float64  `yaml:"importance"`
	Confidence    float64  `yaml:"confidence"`
	DecayRate     float64  `yaml:"decay_rate"`
	CreatedAt     string   `yaml:"created_at"`
	UpdatedAt     string   `yaml:"updated_at"`
	RevisionCount int      `yaml:"revision_count"`
	CreatedBy     string   `yaml:"created_by,omitempty"`
	Files         []string `yaml:"files,omitempty"`
	AppliesTo     []string `yaml:"applies_to,omitempty"`
	Severity      string   `yaml:"severity,omitempty"`
	SupersededBy  string   `yaml:"superseded_by,omitempty"`
	Shared        int      `yaml:"shared,omitempty"`
	Author        string   `yaml:"author,omitempty"`

	// Source round-trips model.Memory.Source — the profile-provenance stamp
	// (SPEC-092), formatted "profile:<name>". Empty for hand-authored
	// memories. In a post-SPEC-094 repo the team-memory writer never emits a
	// non-empty Source (the write guard blocks it before materialization), so
	// this field is written purely for round-trip symmetry with the vault
	// reader/personal-vault export path; it is read by the team-memory
	// import guard (importSharedNote) to skip orphaned profile notes.
	Source string `yaml:"source,omitempty"`
}

// FromMemory builds a Frontmatter from m, applying the inclusion rules from
// spec decision D5. The caller does not need to filter fields — FromMemory
// does it here so WriteTo always sees a correctly populated struct.
func FromMemory(m *model.Memory) Frontmatter {
	fm := Frontmatter{
		ID:            m.ID,
		Type:          string(m.Type),
		Scope:         string(m.Scope),
		Title:         m.Title,
		Importance:    m.Importance,
		Confidence:    m.Confidence,
		DecayRate:     m.DecayRate,
		CreatedAt:     m.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     m.UpdatedAt.UTC().Format(time.RFC3339Nano),
		RevisionCount: m.RevisionCount,
	}

	if m.TopicKey != "" {
		fm.TopicKey = m.TopicKey
	}
	if m.Project != "" {
		fm.Project = m.Project
	}
	if m.CreatedBy != "" {
		fm.CreatedBy = m.CreatedBy
	}
	if len(m.Files) > 0 {
		fm.Files = m.Files
	}
	// applies_to and severity are only meaningful for rules (spec D5).
	if m.Type == model.TypeRule {
		if len(m.AppliesTo) > 0 {
			fm.AppliesTo = m.AppliesTo
		}
		if m.Severity != "" {
			fm.Severity = string(m.Severity)
		}
	}
	if m.SupersededBy != "" {
		fm.SupersededBy = m.SupersededBy
	}
	// shared=0 is the local/inert default (SPEC-053 D2) — omit it so notes
	// written before team-memory existed, or by peers without it enabled,
	// stay byte-identical. Only auto-shared (1) and team-curated (2) surface.
	if m.Shared > 0 {
		fm.Shared = m.Shared
	}
	if m.Author != "" {
		fm.Author = m.Author
	}
	if m.Source != "" {
		fm.Source = m.Source
	}

	return fm
}

// WriteTo serializes fm as YAML frontmatter to w, enclosed in --- delimiters.
// It implements io.WriterTo so it can be composed with other writers.
//
// Serialization is manual (no yaml library) to guarantee deterministic field
// order and to avoid adding a new dependency. Titles are always double-quoted
// to handle colons, dashes, and other YAML-special characters safely (spec D5).
//
// Returns the number of bytes written and the first write error encountered.
func (fm Frontmatter) WriteTo(w io.Writer) (int64, error) {
	var total int64
	write := func(format string, args ...any) error {
		n, err := fmt.Fprintf(w, format, args...)
		total += int64(n)
		return err
	}

	if err := write("---\n"); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write open delimiter: %w", err)
	}

	// Always-present fields.
	if err := write("id: %s\n", fm.ID); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write id: %w", err)
	}
	if err := write("type: %s\n", fm.Type); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write type: %w", err)
	}
	if err := write("scope: %s\n", fm.Scope); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write scope: %w", err)
	}
	if err := write("title: %q\n", fm.Title); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write title: %w", err)
	}

	// Conditional fields — omitted when zero/empty.
	if fm.TopicKey != "" {
		if err := write("topic_key: %s\n", fm.TopicKey); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write topic_key: %w", err)
		}
	}
	if fm.Project != "" {
		if err := write("project: %s\n", fm.Project); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write project: %w", err)
		}
	}

	// Numeric fields — always present; %.4g gives compact representation.
	if err := write("importance: %.2f\n", fm.Importance); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write importance: %w", err)
	}
	if err := write("confidence: %.2f\n", fm.Confidence); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write confidence: %w", err)
	}
	if err := write("decay_rate: %g\n", fm.DecayRate); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write decay_rate: %w", err)
	}

	if err := write("created_at: %s\n", fm.CreatedAt); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write created_at: %w", err)
	}
	if err := write("updated_at: %s\n", fm.UpdatedAt); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write updated_at: %w", err)
	}
	if err := write("revision_count: %d\n", fm.RevisionCount); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write revision_count: %w", err)
	}

	if fm.CreatedBy != "" {
		if err := write("created_by: %s\n", fm.CreatedBy); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write created_by: %w", err)
		}
	}

	if len(fm.Files) > 0 {
		if err := write("files:\n"); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write files header: %w", err)
		}
		for _, f := range fm.Files {
			if err := write("  - %s\n", f); err != nil {
				return total, fmt.Errorf("vault: frontmatter: write file entry: %w", err)
			}
		}
	}

	if len(fm.AppliesTo) > 0 {
		if err := write("applies_to:\n"); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write applies_to header: %w", err)
		}
		for _, a := range fm.AppliesTo {
			if err := write("  - %s\n", a); err != nil {
				return total, fmt.Errorf("vault: frontmatter: write applies_to entry: %w", err)
			}
		}
	}

	if fm.Severity != "" {
		if err := write("severity: %s\n", fm.Severity); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write severity: %w", err)
		}
	}

	if fm.SupersededBy != "" {
		if err := write("superseded_by: %s\n", fm.SupersededBy); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write superseded_by: %w", err)
		}
	}

	if fm.Shared != 0 {
		if err := write("shared: %d\n", fm.Shared); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write shared: %w", err)
		}
	}

	if fm.Author != "" {
		if err := write("author: %s\n", fm.Author); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write author: %w", err)
		}
	}

	if fm.Source != "" {
		if err := write("source: %s\n", fm.Source); err != nil {
			return total, fmt.Errorf("vault: frontmatter: write source: %w", err)
		}
	}

	if err := write("---\n"); err != nil {
		return total, fmt.Errorf("vault: frontmatter: write close delimiter: %w", err)
	}

	return total, nil
}

// parseUpdatedAt extracts the updated_at timestamp from the first 512 bytes of
// a vault note's content. It scans line-by-line looking for "updated_at: " and
// parses the RFC3339 value. Returns the zero time and false when not found or
// when the value cannot be parsed.
//
// This is the idempotency probe: reading 512 bytes from an existing file and
// calling parseUpdatedAt is cheaper than comparing full file contents.
func parseUpdatedAt(header []byte) (time.Time, bool) {
	prefix := "updated_at: "
	for _, line := range strings.Split(string(header), "\n") {
		after, found := strings.CutPrefix(line, prefix)
		if !found {
			continue
		}
		// Try RFC3339Nano first (sub-second precision written by FromMemory),
		// then fall back to RFC3339 for files written before this fix.
		v := strings.TrimSpace(after)
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
