package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// makeTestGap is a helper for constructing a Gap with sensible defaults.
func makeTestGap(key string, mentions, sources int, lastSeen time.Time) model.Gap {
	return model.Gap{
		TargetTopicKey: key,
		TotalMentions:  mentions,
		SourceCount:    sources,
		FirstSeenAt:    lastSeen.Add(-24 * time.Hour),
		LastSeenAt:     lastSeen,
	}
}

// --------------------------------------------------------------------------
// printGapsTable
// --------------------------------------------------------------------------

// TestPrintGapsTable verifies that the table renders the correct four columns
// and the summary footer.
func TestPrintGapsTable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	resp := &model.GapsResponse{
		Gaps: []model.Gap{
			makeTestGap("architecture/auth-model", 12, 5, now.Add(-3*24*time.Hour)),
			makeTestGap("config/rate-limiting", 8, 3, now.Add(-1*24*time.Hour)),
		},
		Total:   2,
		Project: "test/project",
	}

	var buf bytes.Buffer
	if err := printGapsTable(&buf, resp); err != nil {
		t.Fatalf("printGapsTable: %v", err)
	}
	out := buf.String()

	// Header columns must appear.
	if !strings.Contains(out, "TARGET_TOPIC_KEY") {
		t.Error("expected TARGET_TOPIC_KEY header")
	}
	if !strings.Contains(out, "MENTIONS") {
		t.Error("expected MENTIONS header")
	}
	if !strings.Contains(out, "SOURCES") {
		t.Error("expected SOURCES header")
	}
	if !strings.Contains(out, "LAST SEEN") {
		t.Error("expected LAST SEEN header")
	}

	// Gap topic keys must appear.
	if !strings.Contains(out, "architecture/auth-model") {
		t.Error("expected 'architecture/auth-model' in output")
	}
	if !strings.Contains(out, "config/rate-limiting") {
		t.Error("expected 'config/rate-limiting' in output")
	}

	// Footer must include gap count.
	if !strings.Contains(out, "2 knowledge gaps") {
		t.Errorf("expected '2 knowledge gaps' in footer:\n%s", out)
	}
}

// TestPrintGapsTable_Truncation verifies that topic_key values longer than 35
// chars are truncated with "...".
func TestPrintGapsTable_Truncation(t *testing.T) {
	t.Parallel()

	longKey := "very/long/topic/key/that/exceeds/thirty/five/characters/total"
	resp := &model.GapsResponse{
		Gaps:    []model.Gap{makeTestGap(longKey, 1, 1, time.Now())},
		Total:   1,
		Project: "test/project",
	}

	var buf bytes.Buffer
	if err := printGapsTable(&buf, resp); err != nil {
		t.Fatalf("printGapsTable: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, longKey) {
		t.Error("expected long key to be truncated, but full key appears in output")
	}
	if !strings.Contains(out, "...") {
		t.Error("expected '...' truncation marker in output")
	}
}

// TestPrintGapsTable_Empty verifies that an empty gaps list renders the
// "No knowledge gaps found." message with the hint about wikilinks.
func TestPrintGapsTable_Empty(t *testing.T) {
	t.Parallel()

	resp := &model.GapsResponse{
		Gaps:    []model.Gap{},
		Total:   0,
		Project: "test/project",
	}

	var buf bytes.Buffer
	if err := printGapsTable(&buf, resp); err != nil {
		t.Fatalf("printGapsTable empty: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "No knowledge gaps found.") {
		t.Errorf("expected empty message, got:\n%s", out)
	}
	if !strings.Contains(out, "[[topic_keys]]") {
		t.Error("expected wikilinks hint in empty message")
	}
}

// TestPrintGapsTable_SingleGap verifies that the footer uses the singular "gap"
// when exactly one gap is present.
func TestPrintGapsTable_SingleGap(t *testing.T) {
	t.Parallel()

	resp := &model.GapsResponse{
		Gaps:    []model.Gap{makeTestGap("single/gap", 5, 2, time.Now())},
		Total:   1,
		Project: "test/project",
	}

	var buf bytes.Buffer
	if err := printGapsTable(&buf, resp); err != nil {
		t.Fatalf("printGapsTable: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "1 knowledge gap") {
		t.Errorf("expected singular 'knowledge gap', got:\n%s", out)
	}
}

// --------------------------------------------------------------------------
// printGapsJSON
// --------------------------------------------------------------------------

// TestPrintGapsJSON verifies that the JSON output has the expected versioned
// envelope structure.
func TestPrintGapsJSON(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	resp := &model.GapsResponse{
		Gaps: []model.Gap{
			{TargetTopicKey: "arch/auth", TotalMentions: 12, SourceCount: 5,
				FirstSeenAt: now.Add(-24 * time.Hour), LastSeenAt: now},
		},
		Total:   1,
		Project: "test/project",
	}

	var buf bytes.Buffer
	if err := printGapsJSON(&buf, resp); err != nil {
		t.Fatalf("printGapsJSON: %v", err)
	}

	var got gapsListJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v (output: %s)", err, buf.String())
	}

	if got.Version != "1" {
		t.Errorf("version: got %q, want %q", got.Version, "1")
	}
	if got.Total != 1 {
		t.Errorf("total: got %d, want 1", got.Total)
	}
	if len(got.Gaps) != 1 {
		t.Fatalf("len(gaps): got %d, want 1", len(got.Gaps))
	}
	if got.Gaps[0].TargetTopicKey != "arch/auth" {
		t.Errorf("gaps[0].target_topic_key: got %q, want arch/auth", got.Gaps[0].TargetTopicKey)
	}
	if got.Gaps[0].TotalMentions != 12 {
		t.Errorf("gaps[0].total_mentions: got %d, want 12", got.Gaps[0].TotalMentions)
	}
}

// TestPrintGapsJSON_Empty verifies that an empty gaps list produces a valid JSON
// envelope with gaps=[] and total=0.
func TestPrintGapsJSON_Empty(t *testing.T) {
	t.Parallel()

	resp := &model.GapsResponse{
		Gaps:    []model.Gap{},
		Total:   0,
		Project: "test/project",
	}

	var buf bytes.Buffer
	if err := printGapsJSON(&buf, resp); err != nil {
		t.Fatalf("printGapsJSON empty: %v", err)
	}

	var got gapsListJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Version != "1" {
		t.Errorf("version: got %q, want 1", got.Version)
	}
	if got.Total != 0 {
		t.Errorf("total: got %d, want 0", got.Total)
	}
	if len(got.Gaps) != 0 {
		t.Errorf("len(gaps): got %d, want 0", len(got.Gaps))
	}
}

// --------------------------------------------------------------------------
// relativeTime
// --------------------------------------------------------------------------

// TestRelativeTime verifies edge cases for the relativeTime helper.
func TestRelativeTime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		ago      time.Duration
		wantPfx  string
	}{
		{"seconds", 30 * time.Second, "just now"},
		{"minutes", 45 * time.Minute, "45m ago"},
		{"hours", 5 * time.Hour, "5h ago"},
		{"days", 7 * 24 * time.Hour, "7d ago"},
		{"months", 60 * 24 * time.Hour, "2mo ago"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := relativeTime(time.Now().Add(-tc.ago))
			if got != tc.wantPfx {
				t.Errorf("relativeTime(-%v) = %q, want %q", tc.ago, got, tc.wantPfx)
			}
		})
	}
}

// --------------------------------------------------------------------------
// printStats with KnowledgeGaps
// --------------------------------------------------------------------------

// TestPrintStats_WithGaps verifies that printStats includes the "Knowledge gaps"
// section when KnowledgeGaps is non-nil.
func TestPrintStats_WithGaps(t *testing.T) {
	t.Parallel()

	resp := &model.StatsResponse{
		Project: "test/project",
		KnowledgeGaps: &model.KnowledgeGaps{
			Total: 8,
			Top: []model.Gap{
				{TargetTopicKey: "architecture/auth-model", TotalMentions: 12, SourceCount: 5},
				{TargetTopicKey: "config/rate-limiting", TotalMentions: 8, SourceCount: 3},
			},
		},
		ByType:  map[model.MemoryType]int{},
		ByScope: map[model.Scope]int{},
	}

	var buf bytes.Buffer
	printStats(io.Writer(&buf), resp)
	out := buf.String()

	if !strings.Contains(out, "Knowledge gaps") {
		t.Error("expected 'Knowledge gaps' section header")
	}
	if !strings.Contains(out, "Total gaps: 8") {
		t.Errorf("expected 'Total gaps: 8' in output:\n%s", out)
	}
	if !strings.Contains(out, "architecture/auth-model") {
		t.Error("expected top gap key in output")
	}
	if !strings.Contains(out, "12 mentions") {
		t.Errorf("expected '12 mentions' in output:\n%s", out)
	}
}

// TestPrintStats_NoGaps verifies that the "Knowledge gaps" section is absent
// when KnowledgeGaps is nil.
func TestPrintStats_NoGaps(t *testing.T) {
	t.Parallel()

	resp := &model.StatsResponse{
		Project:       "test/project",
		KnowledgeGaps: nil,
		ByType:        map[model.MemoryType]int{},
		ByScope:       map[model.Scope]int{},
	}

	var buf bytes.Buffer
	printStats(io.Writer(&buf), resp)
	out := buf.String()

	if strings.Contains(out, "Knowledge gaps") {
		t.Errorf("expected no 'Knowledge gaps' section when nil, got:\n%s", out)
	}
}

// --------------------------------------------------------------------------
// newGapsCmd flags
// --------------------------------------------------------------------------

// TestGapsCmd_Flags verifies that all expected flags are registered.
func TestGapsCmd_Flags(t *testing.T) {
	cmd := newGapsCmd()
	flags := cmd.Flags()

	for _, name := range []string{"scope", "limit", "min-count", "json"} {
		if f := flags.Lookup(name); f == nil {
			t.Errorf("--%s flag not registered", name)
		}
	}
}
