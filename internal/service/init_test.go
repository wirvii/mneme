package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// newTestInitService creates an InitService backed by in-memory SQLite databases.
// writeDir is a temp directory that writeFile calls target instead of the real fs.
func newTestInitService(t *testing.T) *InitService {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open sdd db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	cfg := config.Default()
	// Override workflow dir to a temp dir so spec dirs land somewhere writable.
	tmpWorkflow := t.TempDir()
	cfg.Workflow.Dir = tmpWorkflow

	sddStore := store.NewSDDStore(database)
	sddSvc := NewSDDService(sddStore, cfg, "test-project", nil)

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	memSvc := NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	svc := NewInitService(cfg, sddSvc, memSvc, "test-project")
	return svc
}

// --- TestParseFrontmatter ---

func TestParseFrontmatter(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		expectedKeys map[string]string
		bodyContains string
	}{
		{
			name:         "standard frontmatter",
			input:        "---\ntitle: Foo\nstatus: done\n---\nBody",
			expectedKeys: map[string]string{"title": "Foo", "status": "done"},
			bodyContains: "Body",
		},
		{
			name:         "no frontmatter",
			input:        "No frontmatter, just body",
			expectedKeys: map[string]string{},
			bodyContains: "No frontmatter, just body",
		},
		{
			name:         "malformed lines ignored",
			input:        "---\nmalformed\n---\nBody",
			expectedKeys: map[string]string{},
			bodyContains: "Body",
		},
		{
			name:         "quoted value stripped",
			input:        "---\ntitle: \"Quoted\"\n---\n",
			expectedKeys: map[string]string{"title": "Quoted"},
			bodyContains: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm, body := parseFrontmatter([]byte(tc.input))
			for k, want := range tc.expectedKeys {
				if got := fm[k]; got != want {
					t.Errorf("key %q: got %q, want %q", k, got, want)
				}
			}
			if tc.bodyContains != "" && !contains(body, tc.bodyContains) {
				t.Errorf("body %q does not contain %q", body, tc.bodyContains)
			}
		})
	}
}

// --- TestParseBacklogFile ---

func TestParseBacklogFile(t *testing.T) {
	content := `# Backlog

- [ ] Agregar soporte OAuth
- [x] Crear tabla users
- [ ] Integrar con Stripe
random non-backlog line
- [X] Deploy inicial (capital X)
`
	svc := newTestInitService(t)
	arts := svc.parseBacklogFile("/tmp/backlog.md", []byte(content))

	if len(arts) != 4 {
		t.Fatalf("expected 4 artifacts, got %d", len(arts))
	}

	// Items 0,2 unchecked (+5); items 1,3 checked (-5).
	uncheckedIdx := []int{0, 2}
	checkedIdx := []int{1, 3}

	for _, i := range uncheckedIdx {
		if !hasSignal(arts[i].Signals, "unchecked", 5) {
			t.Errorf("artifact[%d] should have signal unchecked(+5), got %v", i, arts[i].Signals)
		}
	}
	for _, i := range checkedIdx {
		if !hasSignal(arts[i].Signals, "checked", -5) {
			t.Errorf("artifact[%d] should have signal checked(-5), got %v", i, arts[i].Signals)
		}
	}
}

// --- TestClassifyArtifact_TableDriven ---

func TestClassifyArtifact_TableDriven(t *testing.T) {
	svc := newTestInitService(t)

	cases := []struct {
		name     string
		artifact LegacyArtifact
		want     Classification
	}{
		{
			name: "spec with qa_report_present signal",
			artifact: LegacyArtifact{
				Kind:    KindSpecDir,
				Signals: []Signal{{Name: "qa_report_present", Weight: -10}},
			},
			want: ClassHistorical,
		},
		{
			name: "spec.md with status:done frontmatter",
			artifact: LegacyArtifact{
				Kind:        KindSpecDir,
				Frontmatter: map[string]string{"status": "done"},
			},
			want: ClassHistorical,
		},
		{
			name: "spec with status:in_progress frontmatter",
			artifact: LegacyArtifact{
				Kind:        KindSpecDir,
				Frontmatter: map[string]string{"status": "in_progress"},
			},
			want: ClassActive,
		},
		{
			name: "backlog line checked [x]",
			artifact: LegacyArtifact{
				Kind:    KindBacklogItem,
				Signals: []Signal{{Name: "checked", Weight: -5}},
			},
			want: ClassHistorical,
		},
		{
			name: "backlog line unchecked [ ]",
			artifact: LegacyArtifact{
				Kind:    KindBacklogItem,
				Signals: []Signal{{Name: "unchecked", Weight: 5}},
			},
			want: ClassActive,
		},
		{
			name: "issue with TODO body signal",
			artifact: LegacyArtifact{
				Kind:        KindIssue,
				Frontmatter: map[string]string{},
				Signals:     []Signal{{Name: "todo:in_body", Weight: 1}, {Name: "todo:in_body", Weight: 1}},
			},
			want: ClassActive,
		},
		{
			name: "issue with resolved in body",
			artifact: LegacyArtifact{
				Kind:        KindIssue,
				Frontmatter: map[string]string{},
				Signals:     []Signal{{Name: "resolved:in_body", Weight: -2}},
			},
			want: ClassHistorical,
		},
		{
			name: "issue without signals",
			artifact: LegacyArtifact{
				Kind:        KindIssue,
				Frontmatter: map[string]string{},
				Signals:     []Signal{},
			},
			want: ClassAmbiguous,
		},
		{
			name: "qa report always historical",
			artifact: LegacyArtifact{
				Kind: KindQAReport,
			},
			want: ClassHistorical,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := svc.ClassifyArtifact(tc.artifact)
			if got != tc.want {
				t.Errorf("ClassifyArtifact: got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- TestPlan_NoSideEffects ---

func TestPlan_NoSideEffects(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal legacy issue.
	issueDir := filepath.Join(tmpDir, ".workflow", "issues")
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	issuePath := filepath.Join(issueDir, "ISSUE-001.md")
	if err := os.WriteFile(issuePath, []byte("# Issue One\n\nTODO: implement"), 0o644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	svc := newTestInitService(t)
	ctx := context.Background()

	report, err := svc.Plan(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// File must still exist after Plan.
	if _, statErr := os.Stat(issuePath); os.IsNotExist(statErr) {
		t.Error("Plan deleted ISSUE-001.md — should be a no-op")
	}

	// DB should have no backlog items (Plan never writes).
	items, err := svc.sdd.BacklogList(ctx, model.BacklogListRequest{})
	if err != nil {
		t.Fatalf("BacklogList: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Plan created %d backlog items, want 0", len(items))
	}

	_ = report
}

// --- TestApply_Idempotent ---

func TestApply_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal legacy issue.
	issueDir := filepath.Join(tmpDir, ".workflow", "issues")
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(issueDir, "ISSUE-001.md"),
		[]byte("# Issue One\n\nTODO: implement"), 0o644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	svc := newTestInitService(t)
	ctx := context.Background()

	// First Apply.
	report1, err := svc.Apply(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Apply (1st): %v", err)
	}
	count1 := len(report1.MigratedBacklog)

	// Second Apply — filesystem is already clean, should produce nothing new.
	report2, err := svc.Apply(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Apply (2nd): %v", err)
	}

	// Second run should have fewer or equal items (filesystem was deleted).
	if len(report2.MigratedBacklog) > count1 {
		t.Errorf("2nd Apply produced more backlog items (%d) than 1st (%d)",
			len(report2.MigratedBacklog), count1)
	}
}

// --- TestRewriteClaudeLocal_Backup ---

func TestRewriteClaudeLocal_Backup(t *testing.T) {
	t.Run("existing file gets backed up", func(t *testing.T) {
		tmpDir := t.TempDir()
		claudeLocal := filepath.Join(tmpDir, "CLAUDE.local.md")
		originalContent := []byte("# Old config\n\nold content here\n")
		if err := os.WriteFile(claudeLocal, originalContent, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		svc := newTestInitService(t)
		res, err := svc.RewriteClaudeLocal(context.Background(), tmpDir, "my-project")
		if err != nil {
			t.Fatalf("RewriteClaudeLocal: %v", err)
		}

		if !res.ExistedBefore {
			t.Error("ExistedBefore should be true")
		}
		if res.BackupPath == "" {
			t.Error("BackupPath should not be empty")
		}

		// Verify backup has original content.
		backed, err := os.ReadFile(res.BackupPath)
		if err != nil {
			t.Fatalf("read backup: %v", err)
		}
		if string(backed) != string(originalContent) {
			t.Errorf("backup content mismatch: got %q, want %q", backed, originalContent)
		}

		// Verify new file has SDD template.
		newContent, err := os.ReadFile(claudeLocal)
		if err != nil {
			t.Fatalf("read new: %v", err)
		}
		if !contains(string(newContent), "my-project") {
			t.Error("new CLAUDE.local.md should contain the project slug")
		}
		if !contains(string(newContent), "SDD engine") {
			t.Error("new CLAUDE.local.md should mention SDD engine")
		}
	})

	t.Run("no existing file no backup", func(t *testing.T) {
		tmpDir := t.TempDir()

		svc := newTestInitService(t)
		res, err := svc.RewriteClaudeLocal(context.Background(), tmpDir, "my-project")
		if err != nil {
			t.Fatalf("RewriteClaudeLocal: %v", err)
		}

		if res.ExistedBefore {
			t.Error("ExistedBefore should be false")
		}
		if res.BackupPath != "" {
			t.Error("BackupPath should be empty when no previous file existed")
		}
	})
}

// --- TestApply_E2E_Fixture ---

func TestApply_E2E_Fixture(t *testing.T) {
	tmpDir := t.TempDir()

	// Build fixture project structure.
	mustMkdirAll(t, filepath.Join(tmpDir, ".workflow", "specs", "P001"))
	mustWriteFile(t, filepath.Join(tmpDir, ".workflow", "specs", "P001", "spec.md"),
		"---\nstatus: done\n---\n# Feature X\n\nsome content")
	mustWriteFile(t, filepath.Join(tmpDir, ".workflow", "specs", "P001", "qa-report.md"),
		"# QA Report\n\nResult: approved")

	mustMkdirAll(t, filepath.Join(tmpDir, ".workflow", "bugs", "BUG-003"))
	mustWriteFile(t, filepath.Join(tmpDir, ".workflow", "bugs", "BUG-003", "bug-report.md"),
		"# Bug: crash on startup")
	mustWriteFile(t, filepath.Join(tmpDir, ".workflow", "bugs", "BUG-003", "diagnosis.md"),
		"# Diagnosis\n\nNull pointer in main")

	mustMkdirAll(t, filepath.Join(tmpDir, ".workflow", "issues"))
	mustWriteFile(t, filepath.Join(tmpDir, ".workflow", "issues", "ISSUE-007.md"),
		"# Push notif\n\nTODO: implementar")

	mustMkdirAll(t, filepath.Join(tmpDir, ".workflow", "plans"))
	mustWriteFile(t, filepath.Join(tmpDir, ".workflow", "plans", "backlog.md"),
		"# Backlog\n\n- [x] Done item\n- [ ] Open item\n")

	mustWriteFile(t, filepath.Join(tmpDir, "CLAUDE.local.md"),
		"# Old config\n\nWORKFLOW_DIR: ~/.workflows/old/\n")

	svc := newTestInitService(t)
	ctx := context.Background()

	report, err := svc.Apply(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// P001 had status:done + qa-report → Historical → memory.
	// BUG-003 has no status signals → treated based on body signals.
	// ISSUE-007 has TODO → Active → backlog.
	// backlog open item → Active → backlog; done item → historical → memory.

	if report.Applied != true {
		t.Error("Applied should be true")
	}

	// CLAUDE.local.md should be rewritten.
	if report.CLAUDELocal.WrittenPath == "" {
		t.Error("CLAUDELocal.WrittenPath should be set")
	}
	newContent, err := os.ReadFile(report.CLAUDELocal.WrittenPath)
	if err != nil {
		t.Fatalf("read new CLAUDE.local.md: %v", err)
	}
	if !contains(string(newContent), "SDD engine") {
		t.Error("new CLAUDE.local.md should mention SDD engine")
	}

	// Backup should exist.
	if report.CLAUDELocal.BackupPath == "" {
		t.Error("backup should exist since CLAUDE.local.md existed before")
	}

	// .workflow/ should be deleted after apply.
	if _, err := os.Stat(filepath.Join(tmpDir, ".workflow")); !os.IsNotExist(err) {
		t.Error(".workflow/ should have been deleted")
	}
}

// --- TestFindArtifact_MultipleBacklogItems (regression for Issue 1) ---
//
// Verifies that when a single backlog.md contains both checked and unchecked items,
// Apply migrates each item to its correct destination. Before the fix, findArtifact
// matched by SourcePath and always returned the first item, causing subsequent items
// to receive stale data.

func TestFindArtifact_MultipleBacklogItems(t *testing.T) {
	tmpDir := t.TempDir()

	plansDir := filepath.Join(tmpDir, ".workflow", "plans")
	mustMkdirAll(t, plansDir)
	// Two items: one unchecked (→ active → backlog), one checked (→ historical → memory).
	mustWriteFile(t, filepath.Join(plansDir, "backlog.md"),
		"- [ ] only open task\n- [x] only done task\n")

	svc := newTestInitService(t)
	ctx := context.Background()

	report, err := svc.Apply(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Exactly 1 backlog item (the open task).
	if len(report.MigratedBacklog) != 1 {
		t.Errorf("MigratedBacklog: got %d entries, want 1", len(report.MigratedBacklog))
	}
	if len(report.MigratedBacklog) > 0 && !strings.Contains(report.MigratedBacklog[0].ID, "BL-") {
		t.Errorf("MigratedBacklog[0].ID %q does not look like a BL-XXX ID", report.MigratedBacklog[0].ID)
	}

	// Exactly 1 memory (the done task).
	if len(report.MigratedMemories) != 1 {
		t.Errorf("MigratedMemories: got %d entries, want 1", len(report.MigratedMemories))
	}

	// Verify the memory source corresponds to the backlog.md (done task came from there).
	if len(report.MigratedMemories) > 0 {
		src := report.MigratedMemories[0].Source
		if !strings.Contains(src, "backlog.md") {
			t.Errorf("memory source %q should contain 'backlog.md'", src)
		}
	}

	// Verify the backlog item is the open task, not the done task.
	// This is the core regression: before the fix, the done-task item received
	// data from the open-task item (the first one with the same SourcePath).
	items, err := svc.sdd.BacklogList(ctx, model.BacklogListRequest{})
	if err != nil {
		t.Fatalf("BacklogList: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 backlog item in DB, got %d", len(items))
	}
	if items[0].Title != "only open task" {
		t.Errorf("backlog item title: got %q, want %q — findArtifact aliased to wrong item", items[0].Title, "only open task")
	}
}

// --- TestMigratedReport_SourceAlignment (regression for Issue 4) ---
//
// Verifies that the "Migrado a backlog" table in the rendered report shows the
// correct source for each migrated item. Before the fix, the renderer used the
// plan artifact index which does not map 1:1 to backlog entries.

func TestMigratedReport_SourceAlignment(t *testing.T) {
	tmpDir := t.TempDir()

	// Create both an issue (→ backlog) and a backlog item (→ backlog), so there
	// are multiple entries and misalignment would be visible.
	issueDir := filepath.Join(tmpDir, ".workflow", "issues")
	mustMkdirAll(t, issueDir)
	issuePath := filepath.Join(issueDir, "ISSUE-042.md")
	mustWriteFile(t, issuePath, "# My Issue\n\nTODO: implement me")

	plansDir := filepath.Join(tmpDir, ".workflow", "plans")
	mustMkdirAll(t, plansDir)
	backlogPath := filepath.Join(plansDir, "backlog.md")
	mustWriteFile(t, backlogPath, "- [ ] open backlog item\n")

	svc := newTestInitService(t)
	report, err := svc.Apply(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(report.MigratedBacklog) != 2 {
		t.Fatalf("expected 2 MigratedBacklog entries, got %d", len(report.MigratedBacklog))
	}

	// Each entry's Source must point to the actual source of that specific item.
	for _, entry := range report.MigratedBacklog {
		if entry.Source == "" {
			t.Errorf("MigratedEntry has empty Source for ID %s", entry.ID)
		}
	}

	// The rendered report must not contain a misaligned source.
	rendered := renderInitReport(report)
	// Both sources must appear in the report table.
	if !strings.Contains(rendered, "ISSUE-042.md") {
		t.Error("rendered report should contain ISSUE-042.md as a source")
	}
	if !strings.Contains(rendered, "backlog.md") {
		t.Error("rendered report should contain backlog.md as a source")
	}
}

// --- TestCollectBodySignals_Mtime (regression for Issue 3) ---
//
// Verifies that collectBodySignals emits mtime-based signals when a real filesystem
// path is provided. Tests both the "recent" (<30d) and "old" (>180d) branches using
// os.Chtimes to simulate different modification times.

func TestCollectBodySignals_Mtime(t *testing.T) {
	now := time.Now()

	t.Run("recent file gets +1", func(t *testing.T) {
		f := writeTempFile(t, "# content without keywords\n")
		// mtime = 5 days ago → recent.
		recentMtime := now.Add(-5 * 24 * time.Hour)
		if err := os.Chtimes(f, recentMtime, recentMtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		sigs := collectBodySignals("", f, now)
		if !hasSignal(sigs, "mtime:recent_30d", 1) {
			t.Errorf("expected mtime:recent_30d(+1) signal, got %v", sigs)
		}
	})

	t.Run("old file gets -1", func(t *testing.T) {
		f := writeTempFile(t, "# content without keywords\n")
		// mtime = 200 days ago → old.
		oldMtime := now.Add(-200 * 24 * time.Hour)
		if err := os.Chtimes(f, oldMtime, oldMtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		sigs := collectBodySignals("", f, now)
		if !hasSignal(sigs, "mtime:old_180d", -1) {
			t.Errorf("expected mtime:old_180d(-1) signal, got %v", sigs)
		}
	})

	t.Run("no path skips mtime", func(t *testing.T) {
		sigs := collectBodySignals("", "", now)
		for _, s := range sigs {
			if strings.HasPrefix(s.Name, "mtime:") {
				t.Errorf("unexpected mtime signal with empty path: %v", s)
			}
		}
	})
}

// writeTempFile writes content to a temp file and returns its path.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "mtime-test-*.md")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}
	return f.Name()
}

// --- Helpers ---

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) &&
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

func hasSignal(sigs []Signal, name string, weight int) bool {
	for _, s := range sigs {
		if s.Name == name && s.Weight == weight {
			return true
		}
	}
	return false
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestCollectBodySignals_IssuesMtimeUsesFilePath verifies that collectBodySignals
// receives the individual file path (not the parent directory) when scanning issues,
// so that mtime-based signals reflect the actual file age rather than the directory age.
//
// Regression for the bug where the issues case passed `base` (dir) instead of
// `path` (file), causing all issues to inherit the directory's mtime and receive
// a false recent_30d(+1) signal.
func TestCollectBodySignals_IssuesMtimeUsesFilePath(t *testing.T) {
	dir := t.TempDir()

	freshPath := filepath.Join(dir, "fresh-issue.md")
	oldPath := filepath.Join(dir, "old-issue.md")

	mustWriteFile(t, freshPath, "# Fresh issue\nstill open")
	mustWriteFile(t, oldPath, "# Old issue\nstill open")

	now := time.Now()
	old := now.Add(-200 * 24 * time.Hour)

	// Back-date the old issue to 200 days ago.
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	freshSignals := collectBodySignals("still open", freshPath, now)
	oldSignals := collectBodySignals("still open", oldPath, now)

	if !hasSignal(freshSignals, "mtime:recent_30d", 1) {
		t.Errorf("fresh issue: want mtime:recent_30d(+1), got %v", freshSignals)
	}
	if hasSignal(freshSignals, "mtime:old_180d", -1) {
		t.Errorf("fresh issue: must NOT have mtime:old_180d(-1)")
	}

	if !hasSignal(oldSignals, "mtime:old_180d", -1) {
		t.Errorf("old issue: want mtime:old_180d(-1), got %v", oldSignals)
	}
	if hasSignal(oldSignals, "mtime:recent_30d", 1) {
		t.Errorf("old issue: must NOT have mtime:recent_30d(+1)")
	}
}
