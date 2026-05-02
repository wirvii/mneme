package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/vault"
)

// --- helpers ---

// writeVaultFile writes a minimal valid vault .md file to notesDir/<name>.
// updatedAt is formatted as RFC3339Nano in the frontmatter.
func writeVaultFile(t *testing.T, notesDir, name, id, topicKey string, updatedAt time.Time, body string) string {
	t.Helper()
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", notesDir, err)
	}
	fm := fmt.Sprintf(`---
id: %s
type: discovery
scope: project
title: "Test note %s"
topic_key: %s
project: test/project
importance: 0.80
confidence: 0.80
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: %s
revision_count: 0
---

%s
`, id, name, topicKey, updatedAt.UTC().Format(time.RFC3339Nano), body)

	path := filepath.Join(notesDir, name+".md")
	if err := os.WriteFile(path, []byte(fm), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// writeMarker writes a .mneme-vault JSON marker file at vaultRoot.
func writeMarker(t *testing.T, vaultRoot, project, scope string) {
	t.Helper()
	if err := os.MkdirAll(vaultRoot, 0o755); err != nil {
		t.Fatalf("mkdir vault root: %v", err)
	}
	marker := vault.VaultMarker{
		VaultVersion: 1,
		Project:      project,
		Scope:        scope,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		LastExportAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(marker, "", "  ")
	if err := os.WriteFile(filepath.Join(vaultRoot, ".mneme-vault"), data, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

// newTestServiceWithDataDir creates a service + sets a temp DataDir.
func newTestServiceWithDataDir(t *testing.T) (*service.MemoryService, string) {
	t.Helper()
	svc := newTestService(t)
	tmp := t.TempDir()
	svc.Config().Storage.DataDir = tmp
	return svc, tmp
}

// --- tests ---

func TestVaultImport_MergeFileNewer(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	// Save a memory to the DB with an older timestamp.
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Old title",
		Content:  "Old content",
		Type:     model.TypeDiscovery,
		Scope:    model.ScopeProject,
		TopicKey: "spec/test-note",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	memID := resp.ID

	// Get the DB record to know its updated_at.
	dbMem, err := svc.Get(ctx, memID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Write a vault file with a newer timestamp.
	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	notesDir := filepath.Join(vaultRoot, "notes", "spec")
	writeVaultFile(t, notesDir, "test-note", memID, "spec/test-note",
		dbMem.UpdatedAt.Add(2*time.Second), "Updated content from vault")

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
		Strategy: "merge",
	})
	if err != nil {
		t.Fatalf("VaultImport: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("Updated: got %d, want 1", result.Updated)
	}
	if result.Created != 0 {
		t.Errorf("Created: got %d, want 0", result.Created)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped: got %d, want 0", result.Skipped)
	}

	// Verify the DB record was updated.
	updated, _ := svc.Get(ctx, memID)
	if updated.Content != "Updated content from vault" {
		t.Errorf("Content not updated: %q", updated.Content)
	}
}

func TestVaultImport_MergeDBNewer(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Current title",
		Content:  "Current content",
		Type:     model.TypeDiscovery,
		Scope:    model.ScopeProject,
		TopicKey: "spec/db-newer",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	memID := resp.ID

	dbMem, _ := svc.Get(ctx, memID)

	// Write a vault file with an OLDER timestamp.
	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	notesDir := filepath.Join(vaultRoot, "notes", "spec")
	writeVaultFile(t, notesDir, "db-newer", memID, "spec/db-newer",
		dbMem.UpdatedAt.Add(-5*time.Second), "Old vault content")

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
		Strategy: "merge",
	})
	if err != nil {
		t.Fatalf("VaultImport: %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped: got %d, want 1", result.Skipped)
	}
	if result.Updated != 0 {
		t.Errorf("Updated: got %d, want 0", result.Updated)
	}

	// Verify DB content unchanged.
	check, _ := svc.Get(ctx, memID)
	if check.Content != "Current content" {
		t.Errorf("Content should not change: got %q", check.Content)
	}
}

func TestVaultImport_OverwriteAlways(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:    "DB content",
		Content:  "DB content",
		Type:     model.TypeDiscovery,
		Scope:    model.ScopeProject,
		TopicKey: "spec/overwrite-test",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	memID := resp.ID

	dbMem, _ := svc.Get(ctx, memID)

	// Vault file has an OLDER timestamp but we use overwrite strategy.
	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	notesDir := filepath.Join(vaultRoot, "notes", "spec")
	writeVaultFile(t, notesDir, "overwrite-test", memID, "spec/overwrite-test",
		dbMem.UpdatedAt.Add(-10*time.Second), "Overwritten by vault")

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
		Strategy: "overwrite",
	})
	if err != nil {
		t.Fatalf("VaultImport: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("Updated: got %d, want 1", result.Updated)
	}

	check, _ := svc.Get(ctx, memID)
	if check.Content != "Overwritten by vault" {
		t.Errorf("overwrite did not apply: %q", check.Content)
	}
}

func TestVaultImport_NewFileNoID(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	notesDir := filepath.Join(vaultRoot, "notes")

	// Write a file with no id field.
	noIDContent := `---
type: discovery
scope: project
title: "No id file"
topic_key: notes/no-id-note
project: test/project
importance: 0.70
confidence: 0.70
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---

Content of the no-id note.
`
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "no-id-note.md"), []byte(noIDContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
	})
	if err != nil {
		t.Fatalf("VaultImport: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created: got %d, want 1", result.Created)
	}
}

func TestVaultImport_NewFileWithTopicKey(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	// Pre-existing memory with topic_key.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Original",
		Content:  "Original content",
		Type:     model.TypeDecision,
		Scope:    model.ScopeProject,
		TopicKey: "arch/existing-note",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File has no id but has the same topic_key — should upsert.
	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	notesDir := filepath.Join(vaultRoot, "notes", "arch")

	noIDWithTopicKey := `---
type: decision
scope: project
title: "Original (edited)"
topic_key: arch/existing-note
project: test/project
importance: 0.80
confidence: 0.80
decay_rate: 0.005
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---

Updated content via topic_key upsert.
`
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "existing-note.md"), []byte(noIDWithTopicKey), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
		Strategy: "merge",
	})
	if err != nil {
		t.Fatalf("VaultImport: %v", err)
	}
	// service.Save with matching topic_key triggers upsert → counts as "created"
	// (the internal Create action maps to resp.Action="created" or "updated" from
	// service.Save, but importNote returns "created" for all Save paths).
	if result.Created+result.Updated < 1 {
		t.Errorf("Expected at least 1 action, got created=%d updated=%d", result.Created, result.Updated)
	}
}

func TestVaultImport_ParseError(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	notesDir := filepath.Join(vaultRoot, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Valid file.
	validContent := `---
id: 019ddc45-a39b-76da-9ab7-c4546f962418
type: discovery
scope: project
title: "Valid"
topic_key: notes/valid
project: test/project
importance: 0.70
confidence: 0.70
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---

Valid body.
`
	if err := os.WriteFile(filepath.Join(notesDir, "valid.md"), []byte(validContent), 0o644); err != nil {
		t.Fatalf("write valid: %v", err)
	}

	// Invalid file — no frontmatter.
	if err := os.WriteFile(filepath.Join(notesDir, "broken.md"), []byte("# No frontmatter\n"), 0o644); err != nil {
		t.Fatalf("write broken: %v", err)
	}

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
	})
	if err != nil {
		t.Fatalf("VaultImport: %v", err)
	}
	if result.Errors != 1 {
		t.Errorf("Errors: got %d, want 1", result.Errors)
	}
	// Valid file should still be processed.
	if result.Created < 1 {
		t.Errorf("valid file should have been created; created=%d", result.Created)
	}
}

func TestVaultImport_DryRun(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	notesDir := filepath.Join(vaultRoot, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	content := `---
type: discovery
scope: project
title: "Dry run note"
topic_key: notes/dry-run-note
project: test/project
importance: 0.70
confidence: 0.70
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---

Body.
`
	if err := os.WriteFile(filepath.Join(notesDir, "dry-run-note.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("VaultImport dry-run: %v", err)
	}
	if result.Created < 1 {
		t.Errorf("dry-run Created: got %d, want >= 1", result.Created)
	}

	// Verify no actual writes happened.
	n, err := svc.Count(ctx, "test/project")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 0 {
		t.Errorf("dry-run should not write to DB; count = %d", n)
	}
}

func TestVaultImport_ProjectMismatch(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	// Marker for a different project.
	vaultRoot := filepath.Join(tmp, "vaults", "other-project")
	writeMarker(t, vaultRoot, "other/project", "project")

	// The service has project "test/project" (from newTestService).
	_, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
	})
	if err == nil {
		t.Fatal("expected error for project mismatch")
	}
	if !containsString(err.Error(), "other/project") && !containsString(err.Error(), "belongs to project") {
		t.Errorf("error should mention project mismatch: %v", err)
	}
}

func TestVaultImport_EmptyVault(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	vaultRoot := filepath.Join(tmp, "vaults", "test-project")
	writeMarker(t, vaultRoot, "test/project", "project")
	// Create notes/ dir but leave it empty.
	if err := os.MkdirAll(filepath.Join(vaultRoot, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "project",
		InputDir: vaultRoot,
	})
	if err != nil {
		t.Fatalf("VaultImport empty vault: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("Total: got %d, want 0", result.Total)
	}
	if result.Created != 0 || result.Updated != 0 || result.Errors != 0 {
		t.Errorf("unexpected counts: %+v", result)
	}
}

func TestVaultImport_GlobalScope(t *testing.T) {
	svc, tmp := newTestServiceWithDataDir(t)
	ctx := context.Background()

	// Set up a global vault.
	vaultRoot := filepath.Join(tmp, "vaults", "_global")
	writeMarker(t, vaultRoot, "", "global")
	notesDir := filepath.Join(vaultRoot, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	globalContent := `---
type: preference
scope: global
title: "Global preference"
topic_key: prefs/global-pref
importance: 0.70
confidence: 0.70
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---

Global preference body.
`
	if err := os.WriteFile(filepath.Join(notesDir, "global-pref.md"), []byte(globalContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := svc.VaultImport(ctx, service.VaultImportOptions{
		Scope:    "global",
		InputDir: vaultRoot,
	})
	if err != nil {
		t.Fatalf("VaultImport global: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created: got %d, want 1", result.Created)
	}

	// Verify memory appears in global store by checking with VaultExport global scope.
	exportResult, err := svc.VaultExport(ctx, service.VaultExportOptions{
		Scope:  "global",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("VaultExport global: %v", err)
	}
	if exportResult.Global == nil || exportResult.Global.Total < 1 {
		t.Errorf("global store should have at least 1 memory after import; got %+v", exportResult.Global)
	}
}

// containsString is a helper to avoid importing strings in the test file.
func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

