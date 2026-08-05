package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// seedSessionWork chdirs into root, sets --data-dir=dataDir, and saves n
// memories attributed to sessionID via the real service — resolving to the
// exact same project database runSessionStartHook itself will open for the
// same root/dataDir pair, so the orphan notice sees this work as belonging
// to sessionID.
func seedSessionWork(t *testing.T, root, dataDir, sessionID string, n int) {
	t.Helper()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	oldDataDir := flagDataDir
	flagDataDir = dataDir
	t.Cleanup(func() { flagDataDir = oldDataDir })

	svc, cleanup, err := initService()
	if err != nil {
		t.Fatalf("initService: %v", err)
	}
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := svc.Save(ctx, model.SaveRequest{
			Title:     "work",
			Content:   "some discovery",
			SessionID: sessionID,
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
}

// sessionStartPayload builds the raw JSON stdin body runSessionStartHook
// passes through to runHookSessionStart.
func sessionStartPayload(sessionID string) string {
	return `{"session_id":"` + sessionID + `"}`
}

// TestSessionStart_EmitsOrphanNotice verifies the happy path (AC17): a
// previous session left work without a summary, and the current session_id
// is announced regardless.
func TestSessionStart_EmitsOrphanNotice(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	orphanSessionID := "sess-orphan-001"
	currentSessionID := "sess-current-001"
	seedSessionWork(t, root, dataDir, orphanSessionID, 7)

	stdout, _ := runSessionStartHook(t, root, dataDir, sessionStartPayload(currentSessionID))

	if !strings.Contains(stdout, "<!-- mneme:session:start -->") || !strings.Contains(stdout, "<!-- mneme:session:end -->") {
		t.Fatalf("expected both mneme:session markers, got: %s", stdout)
	}
	if !strings.Contains(stdout, orphanSessionID) {
		t.Errorf("expected the orphaned session id %q in the block, got: %s", orphanSessionID, stdout)
	}
	if !strings.Contains(stdout, "7 memorias") {
		t.Errorf("expected the memory count (7) in the block, got: %s", stdout)
	}
	if !strings.Contains(stdout, "mem_session_end` (`session_id="+orphanSessionID+"`)") {
		t.Errorf("expected the mem_session_end instruction with the orphaned session_id, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Sesión actual: `"+currentSessionID+"`") {
		t.Errorf("expected the current session line, got: %s", stdout)
	}

	sessionIdx := strings.Index(stdout, "<!-- mneme:session:start -->")
	contextIdx := strings.Index(stdout, "<!-- mneme:context:start -->")
	if sessionIdx == -1 || contextIdx == -1 || sessionIdx > contextIdx {
		t.Errorf("expected the session block before the context block, got: %s", stdout)
	}
}

// TestSessionStart_NeverReportsCurrentSession verifies AC18: when the CURRENT
// session already owns memories under its own session_id (the resume/compact
// case, where the agent reenvía the same id), it must never be reported as
// its own orphan.
func TestSessionStart_NeverReportsCurrentSession(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	currentSessionID := "sess-resumed-001"
	seedSessionWork(t, root, dataDir, currentSessionID, 3)

	stdout, _ := runSessionStartHook(t, root, dataDir, sessionStartPayload(currentSessionID))

	if strings.Contains(stdout, "## Sesión anterior sin resumen") {
		t.Errorf("expected no orphan section when the only work belongs to the current session, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Sesión actual: `"+currentSessionID+"`") {
		t.Errorf("expected the current session line, got: %s", stdout)
	}
}

// TestRunHookSessionEnd_EmitsEmptyJSONObject covers SPEC-106 AC1 — the first
// test this handler has ever had. runHookSessionEnd must emit exactly `{}\n`
// (no prefix, no suffix) and return nil, and that output must round-trip
// through json.Unmarshal into an empty map: valid JSON with zero decision
// fields, so neither Claude Code nor Codex ever interprets it as a
// block/continue instruction (D2 — the subcommand survives its contract's
// retirement strictly as a no-op).
func TestRunHookSessionEnd_EmitsEmptyJSONObject(t *testing.T) {
	var buf bytes.Buffer
	if err := runHookSessionEnd(&buf); err != nil {
		t.Fatalf("runHookSessionEnd returned error: %v", err)
	}

	got := buf.String()
	if got != "{}\n" {
		t.Fatalf("runHookSessionEnd output = %q, want %q", got, "{}\n")
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("decoded output has %d fields, want 0 (no decision fields): %#v", len(decoded), decoded)
	}
}

// TestPrintContextHook_WithRules verifies that when resp.Rules is non-empty the
// output contains the "## Active Rules" heading with [SEVERITY] tags and the
// _Applies to:_ line for each rule.
func TestPrintContextHook_WithRules(t *testing.T) {
	blockRule := model.Memory{
		ID:       "rule-1",
		Title:    "Never store plain passwords",
		Content:  "Always use bcrypt with cost >= 12.",
		Type:     model.TypeRule,
		Severity: model.SeverityBlock,
		AppliesTo: []string{"internal/**/*.go"},
	}
	warnRule := model.Memory{
		ID:        "rule-2",
		Title:     "SQL in .sql files only",
		Content:   "No inline SQL in Go code.",
		Type:      model.TypeRule,
		Severity:  model.SeverityWarn,
		AppliesTo: []string{"**/*.go", "!**/*_test.go"},
	}

	resp := &model.ContextResponse{
		Project:     "test/project",
		Rules:       []model.Memory{blockRule, warnRule},
		RulesCount:  2,
		RulesTokens: 42,
		Memories:    []model.Memory{},
		Included:    0,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	checks := []struct {
		name    string
		want    string
	}{
		{"active rules heading", "## Active Rules (2 rules, ~42 tokens)"},
		{"block tag", "[BLOCK] Never store plain passwords"},
		{"warn tag", "[WARN] SQL in .sql files only"},
		{"applies to block", "_Applies to: internal/**/*.go_"},
		{"applies to warn", "_Applies to: **/*.go, !**/*_test.go_"},
		{"separator", "---"},
		{"context start", "<!-- mneme:context:start -->"},
		{"context end", "<!-- mneme:context:end -->"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(got, tc.want) {
				t.Errorf("output does not contain %q\nfull output:\n%s", tc.want, got)
			}
		})
	}

	// Rules section must appear before Memories section.
	rulesPos := strings.Index(got, "## Active Rules")
	memoriesPos := strings.Index(got, "## Loaded Memories")
	if memoriesPos != -1 && rulesPos > memoriesPos {
		t.Error("## Active Rules section appears after ## Loaded Memories")
	}
}

// TestPrintContextHook_WithoutRules verifies that when resp.Rules is empty the
// "## Active Rules" heading does not appear in the output.
func TestPrintContextHook_WithoutRules(t *testing.T) {
	resp := &model.ContextResponse{
		Project: "test/project",
		Memories: []model.Memory{
			{
				ID:      "mem-1",
				Title:   "Architecture decision",
				Content: "Hexagonal architecture.",
				Type:    model.TypeArchitecture,
			},
		},
		Included:       1,
		TotalAvailable: 5,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	if strings.Contains(got, "## Active Rules") {
		t.Errorf("output should not contain '## Active Rules' when resp.Rules is empty")
	}
	if !strings.Contains(got, "## Loaded Memories") {
		t.Error("expected ## Loaded Memories heading")
	}
}

// TestPrintContextHook_RulesTruncated verifies that when RulesTruncated > 0 the
// output includes the truncation notice so the user knows to increase rules_budget.
func TestPrintContextHook_RulesTruncated(t *testing.T) {
	resp := &model.ContextResponse{
		Project: "test/project",
		Rules: []model.Memory{
			{
				ID:       "rule-1",
				Title:    "Block rule",
				Content:  "Reject this.",
				Type:     model.TypeRule,
				Severity: model.SeverityBlock,
			},
		},
		RulesCount:     1,
		RulesTokens:    10,
		RulesTruncated: 3,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	want := "3 rules truncated"
	if !strings.Contains(got, want) {
		t.Errorf("output does not contain %q\nfull output:\n%s", want, got)
	}
	if !strings.Contains(got, "rules_budget") {
		t.Error("truncation notice should mention rules_budget config")
	}
}

// TestPrintContextHook_LastSessionBeforeRules verifies that the Last Session
// section appears before Active Rules in the rendered output — matching the
// documented output order.
func TestPrintContextHook_LastSessionBeforeRules(t *testing.T) {
	ended := time.Now()
	resp := &model.ContextResponse{
		Project: "test/project",
		LastSession: &model.SessionSummary{
			ID:      "sess-1",
			Summary: "Previous session summary.",
			EndedAt: &ended,
		},
		Rules: []model.Memory{
			{
				ID:       "rule-1",
				Title:    "Block rule",
				Content:  "Must not do X.",
				Type:     model.TypeRule,
				Severity: model.SeverityBlock,
			},
		},
		RulesCount:  1,
		RulesTokens: 8,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	lastPos := strings.Index(got, "## Last Session")
	rulesPos := strings.Index(got, "## Active Rules")

	if lastPos == -1 {
		t.Fatal("expected ## Last Session in output")
	}
	if rulesPos == -1 {
		t.Fatal("expected ## Active Rules in output")
	}
	if lastPos > rulesPos {
		t.Error("## Last Session should appear before ## Active Rules")
	}
}

// TestPrintContextHook_NoMemoriesNoRules verifies the "no memories" message
// is shown when both Memories and Rules are empty.
func TestPrintContextHook_NoMemoriesNoRules(t *testing.T) {
	resp := &model.ContextResponse{
		Project:        "test/project",
		TotalAvailable: 0,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	if !strings.Contains(got, "No Memories Found") {
		t.Errorf("expected 'No Memories Found', got:\n%s", got)
	}
}

// TestPrintContextHook_WithClusters verifies that when PackingMode=="communities"
// and ClusterOverviews are present, the output contains the expected headings.
func TestPrintContextHook_WithClusters(t *testing.T) {
	resp := &model.ContextResponse{
		Project:     "test/project",
		PackingMode: "communities",
		ClusterOverviews: []model.Memory{
			{
				ID:      "syn-1",
				Title:   "Auth + JWT + Token Validation",
				Content: "## Cluster Overview\nAuth cluster.",
				Type:    model.TypeSynthesis,
			},
			{
				ID:      "syn-2",
				Title:   "Database Schema + Migrations",
				Content: "## Cluster Overview\nDB cluster.",
				Type:    model.TypeSynthesis,
			},
		},
		ClusterOverviewsCount:  2,
		ClusterOverviewsTokens: 600,
		TopCluster:             "Auth + JWT + Token Validation",
		TopClusterMembers:      2,
		TotalAvailable:         15,
		Included:               4,
		Memories: []model.Memory{
			// First TopClusterMembers entries are from the top cluster.
			{ID: "mem-1", Title: "JWT auth model", Content: "RS256 with refresh.", Type: model.TypeArchitecture},
			{ID: "mem-2", Title: "Token rotation policy", Content: "15min access.", Type: model.TypeDecision},
			// Remaining are "Other Memories".
			{ID: "mem-3", Title: "Other memory A", Content: "content A.", Type: model.TypeDiscovery},
			{ID: "mem-4", Title: "Other memory B", Content: "content B.", Type: model.TypePattern},
		},
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	checks := []string{
		"## Cluster Overviews (2 clusters, ~600 tokens)",
		"### Cluster: Auth + JWT + Token Validation",
		"### Cluster: Database Schema + Migrations",
		"## Top Cluster Detail: Auth + JWT + Token Validation (2 members)",
		"### [architecture] JWT auth model",
		"## Other Memories",
		"### [discovery] Other memory A",
		"<!-- mneme:context:start -->",
		"<!-- mneme:context:end -->",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}

	// "Loaded Memories" must NOT appear in community mode.
	if strings.Contains(got, "## Loaded Memories") {
		t.Error("'## Loaded Memories' should not appear in community packing mode")
	}
}

// TestPrintContextHook_FlatMode verifies that when PackingMode is empty (flat),
// the output uses "## Loaded Memories" and has no cluster sections.
func TestPrintContextHook_FlatMode(t *testing.T) {
	resp := &model.ContextResponse{
		Project:        "test/project",
		TotalAvailable: 5,
		Included:       2,
		Memories: []model.Memory{
			{ID: "m1", Title: "Flat mem 1", Content: "flat content 1.", Type: model.TypeDecision},
			{ID: "m2", Title: "Flat mem 2", Content: "flat content 2.", Type: model.TypePattern},
		},
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	if !strings.Contains(got, "## Loaded Memories (2 of 5)") {
		t.Error("expected '## Loaded Memories' heading in flat mode")
	}
	if strings.Contains(got, "## Cluster Overviews") {
		t.Error("'## Cluster Overviews' must not appear in flat mode")
	}
	if strings.Contains(got, "## Top Cluster Detail") {
		t.Error("'## Top Cluster Detail' must not appear in flat mode")
	}
	if strings.Contains(got, "## Other Memories") {
		t.Error("'## Other Memories' must not appear in flat mode")
	}
}

// TestPrintContextHook_ClustersNoTopCluster verifies that when ClusterOverviews
// are present but TopClusterMembers == 0, no Top Cluster Detail section appears.
func TestPrintContextHook_ClustersNoTopCluster(t *testing.T) {
	resp := &model.ContextResponse{
		Project:     "test/project",
		PackingMode: "communities",
		ClusterOverviews: []model.Memory{
			{ID: "syn-1", Title: "Cluster A", Content: "overview A.", Type: model.TypeSynthesis},
		},
		ClusterOverviewsCount:  1,
		ClusterOverviewsTokens: 300,
		TopClusterMembers:      0, // no top cluster members packed
		TotalAvailable:         10,
		Included:               0,
	}

	var buf bytes.Buffer
	printContextHook(&buf, resp)
	got := buf.String()

	if !strings.Contains(got, "## Cluster Overviews") {
		t.Error("expected '## Cluster Overviews' heading")
	}
	if strings.Contains(got, "## Top Cluster Detail") {
		t.Error("'## Top Cluster Detail' must not appear when TopClusterMembers == 0")
	}
}
