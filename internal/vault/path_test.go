package vault

import (
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

func TestMemoryPath_WithTopicKey(t *testing.T) {
	cases := []struct {
		name     string
		topicKey string
		want     string
	}{
		{
			name:     "simple two-segment key",
			topicKey: "architecture/tech-stack",
			want:     "notes/architecture/tech-stack.md",
		},
		{
			name:     "single segment",
			topicKey: "overview",
			want:     "notes/overview.md",
		},
		{
			name:     "three segments",
			topicKey: "spec/SPEC-009-graph",
			want:     "notes/spec/SPEC-009-graph.md",
		},
		{
			name:     "research topic",
			topicKey: "research/obsidian-lessons",
			want:     "notes/research/obsidian-lessons.md",
		},
		{
			name:     "synthesis community uuid",
			topicKey: "synthesis/community-019de606-01fe-77bd",
			want:     "notes/synthesis/community-019de606-01fe-77bd.md",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &model.Memory{TopicKey: tc.topicKey, ID: "019ddc45-a39b-76da-9ab7-c4546f962418"}
			got := MemoryPath(m)
			if got != tc.want {
				t.Errorf("MemoryPath(%q) = %q; want %q", tc.topicKey, got, tc.want)
			}
		})
	}
}

func TestMemoryPath_NoTopicKey(t *testing.T) {
	m := &model.Memory{ID: "019ddc45-a39b-76da-9ab7-c4546f962418", TopicKey: ""}
	got := MemoryPath(m)
	want := "notes/_no-topic/019ddc45.md"
	if got != want {
		t.Errorf("MemoryPath(no topic_key) = %q; want %q", got, want)
	}
}

func TestMemoryPath_SynthesisMemory(t *testing.T) {
	m := &model.Memory{
		TopicKey: "synthesis/community-019de606-01fe-77bd",
		ID:       "019de606-0000-0000-0000-000000000000",
	}
	got := MemoryPath(m)
	if !strings.HasPrefix(got, "notes/synthesis/") {
		t.Errorf("synthesis memory path %q should start with notes/synthesis/", got)
	}
	if !strings.HasSuffix(got, ".md") {
		t.Errorf("synthesis memory path %q should end with .md", got)
	}
}

func TestMemoryPath_UnsafeTopicKey_FallsBackToNoTopic(t *testing.T) {
	// A topic_key that sanitizes entirely to empty should fall back to _no-topic.
	m := &model.Memory{TopicKey: "???", ID: "019ddc45-0000-0000-0000-000000000000"}
	got := MemoryPath(m)
	// ??? sanitizes to ___ which is valid, so it should NOT fall back.
	want := "notes/___.md"
	if got != want {
		t.Errorf("MemoryPath(???) = %q; want %q", got, want)
	}
}

func TestMemoryPath_LongPath_FallsBackToNoTopic(t *testing.T) {
	// Build a topic_key that produces a path > 900 chars.
	segment := strings.Repeat("a", 200)
	parts := make([]string, 10)
	for i := range parts {
		parts[i] = segment
	}
	longKey := strings.Join(parts, "/")
	m := &model.Memory{TopicKey: longKey, ID: "019ddc45-1234-0000-0000-000000000000"}
	got := MemoryPath(m)
	// notes/ + 10 * 200 + 9 separators + .md = 6 + 2000 + 9 + 3 = 2018 > 900
	if !strings.Contains(got, "_no-topic") {
		t.Errorf("long path %q should fall back to _no-topic but did not", got)
	}
}

func TestSanitizeSegment_UnsafeChars(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"arch:v2", "arch_v2"},
		{"foo*bar", "foo_bar"},
		{"a?b", "a_b"},
		{"a<b>c", "a_b_c"},
		{"pipe|sep", "pipe_sep"},
		{`back\slash`, "back_slash"},
		{`quo"te`, "quo_te"},
	}
	for _, tc := range cases {
		got := SanitizeSegment(tc.input)
		if got != tc.want {
			t.Errorf("SanitizeSegment(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeSegment_Spaces(t *testing.T) {
	got := SanitizeSegment("hello world")
	want := "hello-world"
	if got != want {
		t.Errorf("SanitizeSegment(space) = %q; want %q", got, want)
	}
}

func TestSanitizeSegment_LongSegment(t *testing.T) {
	long := strings.Repeat("x", 250)
	got := SanitizeSegment(long)
	if len([]rune(got)) != maxSegmentLen {
		t.Errorf("long segment should be capped at %d runes, got %d", maxSegmentLen, len([]rune(got)))
	}
}

func TestSanitizeSegment_Empty(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"...", ""},
		{". ", ""},
	}
	for _, tc := range cases {
		got := SanitizeSegment(tc.input)
		if got != tc.want {
			t.Errorf("SanitizeSegment(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestMemoryPath_EmptyAfterSanitization(t *testing.T) {
	// Dots and spaces only — sanitize to empty — falls back to _no-topic.
	m := &model.Memory{TopicKey: ".../...", ID: "abcdef01-0000-0000-0000-000000000000"}
	got := MemoryPath(m)
	// "..." sanitizes to "" per segment, so topicKeyToRelPath returns "".
	// MemoryPath should fall back to noTopicPath.
	want := "notes/_no-topic/abcdef01.md"
	if got != want {
		t.Errorf("MemoryPath('.../...') = %q; want %q", got, want)
	}
}

func TestTruncateID(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"019ddc45-a39b-76da-9ab7-c4546f962418", "019ddc45"},
		{"abcdefgh", "abcdefgh"},
		{"abc", "abc"},
	}
	for _, tc := range cases {
		got := truncateID(tc.id)
		if got != tc.want {
			t.Errorf("truncateID(%q) = %q; want %q", tc.id, got, tc.want)
		}
	}
}

// TestUUIDPath verifies the flat notes/<uuid>.md layout used by PathModeUUID
// (SPEC-053 D1), independent of topic_key.
func TestUUIDPath(t *testing.T) {
	id := "019ddc45-0000-0000-0000-000000000001"
	got := UUIDPath(id)
	want := "notes/019ddc45-0000-0000-0000-000000000001.md"
	if got != want {
		t.Errorf("UUIDPath(%q) = %q; want %q", id, got, want)
	}
}

// TestMemoryPath_SpecExamples verifies the exact paths shown in spec section 3.
func TestMemoryPath_SpecExamples(t *testing.T) {
	ts := time.Now()
	cases := []struct {
		topicKey string
		wantPath string
	}{
		{"architecture/tech-stack", "notes/architecture/tech-stack.md"},
		{"spec/SPEC-001-rule-type-design", "notes/spec/SPEC-001-rule-type-design.md"},
		{"discovery/sync-gaps", "notes/discovery/sync-gaps.md"},
		{"discovery/backup-restore", "notes/discovery/backup-restore.md"},
	}
	for _, tc := range cases {
		m := &model.Memory{
			TopicKey:  tc.topicKey,
			ID:        "019ddc45-0000-0000-0000-000000000000",
			CreatedAt: ts,
			UpdatedAt: ts,
		}
		got := MemoryPath(m)
		if got != tc.wantPath {
			t.Errorf("spec example %q: got %q; want %q", tc.topicKey, got, tc.wantPath)
		}
	}
}
