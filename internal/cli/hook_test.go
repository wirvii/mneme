package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
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

// ---- SPEC-108 step 7: fail-open matrix, terminal gate, budget, convergence ----

// TestIsInteractive is a table test over isInteractive's single bit check
// (AC23, D15 row 5): a terminal is any os.FileMode with ModeCharDevice set,
// regardless of what else is set alongside it.
func TestIsInteractive(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{"character device set", os.ModeCharDevice, true},
		{"character device with permission bits", os.ModeCharDevice | 0o644, true},
		{"regular file mode, not a character device", 0o644, false},
		{"zero mode", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInteractive(tc.mode); got != tc.want {
				t.Errorf("isInteractive(%v) = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

// TestHookStdin_PipeIsConsumed verifies the "sí se lee" half of AC23: a
// non-terminal *os.File (a pipe, definitely not a character device) is
// forwarded by hookStdin, not replaced by an empty reader.
func TestHookStdin_PipeIsConsumed(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { r.Close() })

	payload := `{"session_id":"abc"}`
	go func() {
		_, _ = w.Write([]byte(payload))
		w.Close()
	}()

	got := decodeSessionStartInput(hookStdin(r), &bytes.Buffer{})
	if got != "abc" {
		t.Errorf("expected the pipe payload to be decoded, got session_id=%q", got)
	}
}

// TestSessionStart_EmptyStdin_FailOpen is D15 row 1 (AC19): empty stdin
// (io.EOF) emits no session block, no stderr output at all, and the context
// block still prints — the exact behaviour every pre-SPEC-108 SessionStart
// test in this package already exercises without a payload argument, which
// is the non-regression proof this row calls for.
func TestSessionStart_EmptyStdin_FailOpen(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	stdout, stderr := runSessionStartHook(t, root, dataDir)

	if strings.Contains(stdout, "mneme:session") {
		t.Errorf("expected no session block on empty stdin, got: %s", stdout)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr on empty stdin, got: %q", stderr)
	}
	if !strings.Contains(stdout, "<!-- mneme:context:start -->") {
		t.Errorf("expected the context block to still print, got: %s", stdout)
	}
}

// TestSessionStart_InvalidJSON_WarnsNoBlock is D15 row 2 (AC20): malformed
// stdin JSON warns on stderr, emits no session block, and still lets the
// context block through.
func TestSessionStart_InvalidJSON_WarnsNoBlock(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	stdout, stderr := runSessionStartHook(t, root, dataDir, "{not valid json")

	if strings.Contains(stdout, "mneme:session") {
		t.Errorf("expected no session block on invalid JSON, got: %s", stdout)
	}
	if !strings.Contains(stderr, "invalid stdin JSON") {
		t.Errorf("expected a WARN about invalid JSON, got: %q", stderr)
	}
	if !strings.Contains(stdout, "<!-- mneme:context:start -->") {
		t.Errorf("expected the context block to still print, got: %s", stdout)
	}
}

// TestSessionStart_ValidJSONNoSessionID_SilentNoWarn is D15 row 3 (AC21):
// valid JSON that simply omits session_id must stay silent — an absent
// session_id is indistinguishable from "this session genuinely has none",
// and warning here would falsely accuse the current session of a problem.
func TestSessionStart_ValidJSONNoSessionID_SilentNoWarn(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	stdout, stderr := runSessionStartHook(t, root, dataDir, "{}")

	if strings.Contains(stdout, "mneme:session") {
		t.Errorf("expected no session block without a session_id, got: %s", stdout)
	}
	if stderr != "" {
		t.Errorf("expected no warning when session_id is simply absent, got: %q", stderr)
	}
}

// TestMaybeEmitPendingSessionNotice_StoreFailure_WarnsNoBlock is D15 row 4
// (AC22), exercised directly against maybeEmitPendingSessionNotice rather
// than through the full runHookSessionStart integration: the project store's
// underlying connection is closed before the call, so
// PendingSessionSummaries fails deterministically. This isolates exactly the
// contract this row describes — no block, a WARN on errW — without
// entangling it with svc.Context's own memories-table query, which shares
// every column sessionWorkWhere touches and would fail identically if the
// corruption were done at the schema level instead of the connection level.
// runHookSessionStart calls this function before, and independently of,
// svc.Context — a failure here can never suppress the context block that
// follows (proven structurally: maybeEmitPendingSessionNotice returns
// nothing and never aborts the caller).
func TestMaybeEmitPendingSessionNotice_StoreFailure_WarnsNoBlock(t *testing.T) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { globalDB.Close() })
	// Closing the project DB immediately makes every subsequent query against
	// it fail deterministically ("sql: database is closed").
	if err := projectDB.Close(); err != nil {
		t.Fatalf("close project db: %v", err)
	}

	cfg := config.Default()
	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	var w, errW bytes.Buffer
	maybeEmitPendingSessionNotice(context.Background(), svc, "sess-current", &w, &errW)

	if w.Len() != 0 {
		t.Errorf("expected no block written on a store failure, got: %s", w.String())
	}
	if !strings.Contains(errW.String(), "pending session summaries error") {
		t.Errorf("expected a WARN about the failure, got: %q", errW.String())
	}
}

// seedOrphanSessions seeds n distinct orphaned sessions, each with exactly
// one work memory, using a fixed-width zero-padded id ("sess-orphan-000" …
// "sess-orphan-049") so every scenario this feeds produces session ids of
// identical byte length regardless of n — a precondition for the byte-budget
// comparison in TestSessionStart_NoticeBudget_ConstantSizeRegardlessOfOrphanCount.
func seedOrphanSessions(t *testing.T, root, dataDir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		seedSessionWork(t, root, dataDir, fmt.Sprintf("sess-orphan-%03d", i), 1)
	}
}

// extractSessionBlock returns the substring of stdout between (and
// including) the mneme:session markers, or "" if either marker is absent.
func extractSessionBlock(stdout string) string {
	start := strings.Index(stdout, "<!-- mneme:session:start -->")
	end := strings.Index(stdout, "<!-- mneme:session:end -->")
	if start == -1 || end == -1 {
		return ""
	}
	return stdout[start : end+len("<!-- mneme:session:end -->")]
}

// contentLines returns block's non-blank, non-marker lines — the lines a
// reader actually needs to read, excluding wrapper markers and spacing.
func contentLines(block string) []string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// TestSessionStart_NoticeBudget_ConstantSizeRegardlessOfOrphanCount is AC24:
// with 50 orphaned sessions the block still has <= 6 content lines, and its
// byte length differs from the 2-orphan case by EXACTLY the extra digit
// OlderCount gains ("1" -> "49", +1 byte) — every other field (session id
// width, per-session memory count, timestamp format, current session id) is
// held constant by construction so no other byte can move.
func TestSessionStart_NoticeBudget_ConstantSizeRegardlessOfOrphanCount(t *testing.T) {
	currentSessionID := "sess-current-999"

	root2 := t.TempDir()
	dataDir2 := t.TempDir()
	seedOrphanSessions(t, root2, dataDir2, 2)
	stdout2, _ := runSessionStartHook(t, root2, dataDir2, sessionStartPayload(currentSessionID))
	block2 := extractSessionBlock(stdout2)
	if block2 == "" {
		t.Fatal("expected a session block for the 2-orphan case")
	}

	root50 := t.TempDir()
	dataDir50 := t.TempDir()
	seedOrphanSessions(t, root50, dataDir50, 50)
	stdout50, _ := runSessionStartHook(t, root50, dataDir50, sessionStartPayload(currentSessionID))
	block50 := extractSessionBlock(stdout50)
	if block50 == "" {
		t.Fatal("expected a session block for the 50-orphan case")
	}

	for name, block := range map[string]string{"2-orphan": block2, "50-orphan": block50} {
		if lines := contentLines(block); len(lines) > 6 {
			t.Errorf("%s block has %d content lines, want <= 6: %v", name, len(lines), lines)
		}
	}

	if diff := len(block50) - len(block2); diff != 1 {
		t.Errorf("byte length diff = %d, want exactly 1 (the extra OlderCount digit): block2=%q block50=%q",
			diff, block2, block50)
	}
}

// closeOrphanSession chdirs into root, sets --data-dir=dataDir, and calls the
// real SessionEnd for sessionID — the same close path mem_session_end drives
// in production.
func closeOrphanSession(t *testing.T, root, dataDir, sessionID string) {
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

	if _, err := svc.SessionEnd(context.Background(), model.SessionEndRequest{
		Summary:   "closing the orphan session",
		SessionID: sessionID,
	}); err != nil {
		t.Fatalf("SessionEnd: %v", err)
	}
}

// TestSessionStart_ConvergesAfterSessionEnd is AC25: two consecutive runs
// with the orphan unresolved both emit the notice; after mem_session_end
// closes it, a third run no longer emits the orphan section (only the
// current-session line remains).
func TestSessionStart_ConvergesAfterSessionEnd(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	orphanID := "sess-orphan-conv"
	currentID := "sess-current-conv"
	seedSessionWork(t, root, dataDir, orphanID, 2)

	first, _ := runSessionStartHook(t, root, dataDir, sessionStartPayload(currentID))
	if !strings.Contains(first, "## Sesión anterior sin resumen") || !strings.Contains(first, orphanID) {
		t.Fatalf("expected the orphan section on the first run, got: %s", first)
	}

	second, _ := runSessionStartHook(t, root, dataDir, sessionStartPayload(currentID))
	if !strings.Contains(second, "## Sesión anterior sin resumen") || !strings.Contains(second, orphanID) {
		t.Fatalf("expected the orphan section to repeat on the second (still unresolved) run, got: %s", second)
	}

	closeOrphanSession(t, root, dataDir, orphanID)

	third, _ := runSessionStartHook(t, root, dataDir, sessionStartPayload(currentID))
	if strings.Contains(third, "## Sesión anterior sin resumen") {
		t.Errorf("expected the orphan section to be gone after mem_session_end closed it, got: %s", third)
	}
	if !strings.Contains(third, "Sesión actual: `"+currentID+"`") {
		t.Errorf("expected the current session line to still print, got: %s", third)
	}
}
