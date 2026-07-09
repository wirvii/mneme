package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// --- parseFrontmatter unit tests ---

func TestParseFrontmatter_AllFields(t *testing.T) {
	input := `---
id: 019ddc45-a39b-76da-9ab7-c4546f962418
type: decision
scope: project
title: "My decision title"
topic_key: architecture/decision-1
project: wirvii/mneme
importance: 0.90
confidence: 0.80
decay_rate: 0.005
created_at: 2026-04-30T02:44:04Z
updated_at: 2026-04-30T20:41:14Z
revision_count: 3
created_by: claude-code
files:
  - internal/model/memory.go
  - README.md
---
`
	fm, end, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	if end <= 0 {
		t.Fatal("fmEnd should be > 0")
	}

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"ID", fm.ID, "019ddc45-a39b-76da-9ab7-c4546f962418"},
		{"Type", fm.Type, "decision"},
		{"Scope", fm.Scope, "project"},
		{"Title", fm.Title, "My decision title"},
		{"TopicKey", fm.TopicKey, "architecture/decision-1"},
		{"Project", fm.Project, "wirvii/mneme"},
		{"CreatedAt", fm.CreatedAt, "2026-04-30T02:44:04Z"},
		{"UpdatedAt", fm.UpdatedAt, "2026-04-30T20:41:14Z"},
		{"CreatedBy", fm.CreatedBy, "claude-code"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if fm.Importance != 0.90 {
		t.Errorf("Importance: got %v, want 0.90", fm.Importance)
	}
	if fm.Confidence != 0.80 {
		t.Errorf("Confidence: got %v, want 0.80", fm.Confidence)
	}
	if fm.DecayRate != 0.005 {
		t.Errorf("DecayRate: got %v, want 0.005", fm.DecayRate)
	}
	if fm.RevisionCount != 3 {
		t.Errorf("RevisionCount: got %d, want 3", fm.RevisionCount)
	}
	if len(fm.Files) != 2 {
		t.Fatalf("Files: got %d items, want 2", len(fm.Files))
	}
	if fm.Files[0] != "internal/model/memory.go" {
		t.Errorf("Files[0]: got %q", fm.Files[0])
	}
	if fm.Files[1] != "README.md" {
		t.Errorf("Files[1]: got %q", fm.Files[1])
	}
}

func TestParseFrontmatter_SharedAuthor(t *testing.T) {
	input := `---
id: 019ddc45-0000-0000-0000-000000000006
type: decision
scope: project
title: "Shared decision"
importance: 0.90
confidence: 0.80
decay_rate: 0.005
created_at: 2026-04-30T02:44:04Z
updated_at: 2026-04-30T20:41:14Z
revision_count: 0
shared: 2
author: Jane Doe <jane@example.com>
---
`
	fm, _, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	if fm.Shared != 2 {
		t.Errorf("Shared: got %d, want 2", fm.Shared)
	}
	if fm.Author != "Jane Doe <jane@example.com>" {
		t.Errorf("Author: got %q, want %q", fm.Author, "Jane Doe <jane@example.com>")
	}
}

func TestParseFrontmatter_OmittedOptional(t *testing.T) {
	input := `---
id: 019ddc45-0000-0000-0000-000000000001
type: discovery
scope: global
title: "Simple"
importance: 0.50
confidence: 0.70
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---
`
	fm, _, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	if fm.TopicKey != "" {
		t.Errorf("TopicKey should be empty, got %q", fm.TopicKey)
	}
	if fm.Project != "" {
		t.Errorf("Project should be empty, got %q", fm.Project)
	}
	if fm.CreatedBy != "" {
		t.Errorf("CreatedBy should be empty, got %q", fm.CreatedBy)
	}
	if len(fm.Files) != 0 {
		t.Errorf("Files should be empty, got %v", fm.Files)
	}
	if len(fm.AppliesTo) != 0 {
		t.Errorf("AppliesTo should be empty, got %v", fm.AppliesTo)
	}
	if fm.Severity != "" {
		t.Errorf("Severity should be empty, got %q", fm.Severity)
	}
	if fm.Shared != 0 {
		t.Errorf("Shared should be 0, got %d", fm.Shared)
	}
	if fm.Author != "" {
		t.Errorf("Author should be empty, got %q", fm.Author)
	}
	if fm.SupersededBy != "" {
		t.Errorf("SupersededBy should be empty, got %q", fm.SupersededBy)
	}
}

func TestParseFrontmatter_ListFields(t *testing.T) {
	input := `---
id: 019ddc45-0000-0000-0000-000000000002
type: rule
scope: project
title: "No globals"
importance: 0.95
confidence: 0.80
decay_rate: 0
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
applies_to:
  - internal/**/*.go
  - tool:Edit
severity: block
files:
  - internal/cli/vault.go
---
`
	fm, _, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	if len(fm.AppliesTo) != 2 {
		t.Fatalf("AppliesTo: got %d items, want 2: %v", len(fm.AppliesTo), fm.AppliesTo)
	}
	if fm.AppliesTo[0] != "internal/**/*.go" {
		t.Errorf("AppliesTo[0]: got %q", fm.AppliesTo[0])
	}
	if fm.AppliesTo[1] != "tool:Edit" {
		t.Errorf("AppliesTo[1]: got %q", fm.AppliesTo[1])
	}
	if len(fm.Files) != 1 || fm.Files[0] != "internal/cli/vault.go" {
		t.Errorf("Files: got %v", fm.Files)
	}
	if fm.Severity != "block" {
		t.Errorf("Severity: got %q, want block", fm.Severity)
	}
}

func TestParseFrontmatter_TitleQuoted(t *testing.T) {
	input := `---
id: 019ddc45-0000-0000-0000-000000000003
type: decision
scope: project
title: "Title with \"inner quotes\" and colons: yes"
importance: 0.80
confidence: 0.80
decay_rate: 0.005
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---
`
	fm, _, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	want := `Title with "inner quotes" and colons: yes`
	if fm.Title != want {
		t.Errorf("Title: got %q, want %q", fm.Title, want)
	}
}

func TestParseFrontmatter_TitleUnquoted(t *testing.T) {
	// Manually created files may not use %q formatting.
	input := `---
id: 019ddc45-0000-0000-0000-000000000004
type: discovery
scope: project
title: Unquoted simple title
importance: 0.50
confidence: 0.50
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---
`
	fm, _, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	if fm.Title != "Unquoted simple title" {
		t.Errorf("Title: got %q, want 'Unquoted simple title'", fm.Title)
	}
}

func TestParseFrontmatter_NumericFormats(t *testing.T) {
	cases := []struct {
		name  string
		input string
		field func(Frontmatter) float64
		want  float64
	}{
		{"importance 0.90", "importance: 0.90", func(fm Frontmatter) float64 { return fm.Importance }, 0.90},
		{"decay_rate 0.005", "decay_rate: 0.005", func(fm Frontmatter) float64 { return fm.DecayRate }, 0.005},
		{"decay_rate 0", "decay_rate: 0", func(fm Frontmatter) float64 { return fm.DecayRate }, 0},
		{"importance 1", "importance: 1", func(fm Frontmatter) float64 { return fm.Importance }, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full := "---\nid: x\ntype: discovery\nscope: project\ntitle: \"t\"\n" + tc.input + "\nconfidence: 0.5\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\nrevision_count: 0\n---\n"
			fm, _, err := parseFrontmatter([]byte(full))
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			got := tc.field(fm)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseFrontmatter_MissingOpeningDelimiter(t *testing.T) {
	input := `id: abc
type: decision
---
`
	_, _, err := parseFrontmatter([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing opening delimiter")
	}
}

func TestParseFrontmatter_MissingClosingDelimiter(t *testing.T) {
	input := `---
id: abc
type: decision
scope: project
`
	_, _, err := parseFrontmatter([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
	if !strings.Contains(err.Error(), "closing") {
		t.Errorf("error message should mention 'closing', got: %v", err)
	}
}

func TestParseFrontmatter_UnknownFields(t *testing.T) {
	// Unknown fields must be silently ignored.
	input := `---
id: 019ddc45-0000-0000-0000-000000000005
type: discovery
scope: project
title: "Known"
custom_field: some value
another_unknown: 42
importance: 0.50
confidence: 0.50
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---
`
	fm, _, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter should not error on unknown fields: %v", err)
	}
	if fm.Title != "Known" {
		t.Errorf("Title: got %q", fm.Title)
	}
}

// --- extractBody tests ---

func TestExtractBody_Normal(t *testing.T) {
	input := `---
id: abc
type: discovery
scope: project
title: "Test"
importance: 0.5
confidence: 0.5
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---

This is the body content.
Second line.
`
	_, fmEnd, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	body := extractBody([]byte(input), fmEnd)
	if body != "This is the body content.\nSecond line." {
		t.Errorf("extractBody: got %q", body)
	}
}

func TestExtractBody_Empty(t *testing.T) {
	input := `---
id: abc
type: discovery
scope: project
title: "Test"
importance: 0.5
confidence: 0.5
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---
`
	_, fmEnd, err := parseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
	body := extractBody([]byte(input), fmEnd)
	if body != "" {
		t.Errorf("extractBody on empty content: got %q, want empty", body)
	}
}

// --- ParseFile integration tests ---

func TestParseFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	content := `---
id: 019ddc45-a39b-76da-9ab7-c4546f962418
type: architecture
scope: project
title: "Test memory"
topic_key: arch/test
project: test/project
importance: 0.90
confidence: 0.80
decay_rate: 0.005
created_at: 2026-04-30T02:44:04Z
updated_at: 2026-04-30T20:41:14Z
revision_count: 1
---

This is the body content of the memory.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	note, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}
	if note.Path != path {
		t.Errorf("Path: got %q, want %q", note.Path, path)
	}
	if note.FM.ID != "019ddc45-a39b-76da-9ab7-c4546f962418" {
		t.Errorf("ID: got %q", note.FM.ID)
	}
	if note.FM.Title != "Test memory" {
		t.Errorf("Title: got %q", note.FM.Title)
	}
	if note.Body != "This is the body content of the memory." {
		t.Errorf("Body: got %q", note.Body)
	}
}

func TestParseFile_NonExistent(t *testing.T) {
	_, err := ParseFile("/tmp/nonexistent-file-mneme-test.md")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestParseFile_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.md")
	content := `# Just a plain markdown file

No frontmatter here.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected error for file without frontmatter")
	}
}

// --- ReadAll integration tests ---

func TestReadAll_MixedFiles(t *testing.T) {
	vaultRoot := t.TempDir()
	notesDir := filepath.Join(vaultRoot, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Valid .md file
	validContent := `---
id: 019ddc45-a39b-76da-9ab7-c4546f962418
type: discovery
scope: project
title: "Valid note"
importance: 0.80
confidence: 0.80
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
revision_count: 0
---

Valid body.
`
	if err := os.WriteFile(filepath.Join(notesDir, "valid.md"), []byte(validContent), 0o644); err != nil {
		t.Fatalf("write valid file: %v", err)
	}

	// Invalid .md file (no frontmatter)
	if err := os.WriteFile(filepath.Join(notesDir, "invalid.md"), []byte("# No frontmatter\n"), 0o644); err != nil {
		t.Fatalf("write invalid file: %v", err)
	}

	// Non-.md file (should be silently ignored)
	if err := os.WriteFile(filepath.Join(notesDir, "image.png"), []byte("fake png"), 0o644); err != nil {
		t.Fatalf("write png file: %v", err)
	}

	r := NewReader(vaultRoot)
	notes, errs := r.ReadAll()

	if len(notes) != 1 {
		t.Errorf("ReadAll: got %d notes, want 1", len(notes))
	}
	if len(errs) != 1 {
		t.Errorf("ReadAll: got %d errors, want 1 (for invalid.md)", len(errs))
	}
	if len(notes) > 0 && notes[0].FM.Title != "Valid note" {
		t.Errorf("note title: got %q", notes[0].FM.Title)
	}
}

// --- IsValidUUID tests ---

func TestIsValidUUID(t *testing.T) {
	valid := []string{
		"019ddc45-a39b-76da-9ab7-c4546f962418",
		"00000000-0000-0000-0000-000000000000",
		"ffffffff-ffff-ffff-ffff-ffffffffffff",
	}
	for _, s := range valid {
		if !IsValidUUID(s) {
			t.Errorf("IsValidUUID(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		"not-a-uuid",
		"12345",
		"019ddc45-a39b-76da-9ab7",
		"019ddc45-a39b-76da-9ab7-c4546f962418-extra",
	}
	for _, s := range invalid {
		if IsValidUUID(s) {
			t.Errorf("IsValidUUID(%q) = true, want false", s)
		}
	}
}

// --- ToSaveRequest tests ---

func TestToSaveRequest_Full(t *testing.T) {
	fm := Frontmatter{
		ID:        "019ddc45-a39b-76da-9ab7-c4546f962418",
		Type:      "decision",
		Scope:     "project",
		Title:     "My decision",
		TopicKey:  "arch/decision",
		Project:   "test/project",
		CreatedBy: "claude-code",
		Files:     []string{"internal/service/memory.go"},
		Importance: 0.85,
	}
	body := "The decision content"

	req := fm.ToSaveRequest(body)

	if req.Title != "My decision" {
		t.Errorf("Title: %q", req.Title)
	}
	if req.Content != body {
		t.Errorf("Content: %q", req.Content)
	}
	if req.Type != model.TypeDecision {
		t.Errorf("Type: %q", req.Type)
	}
	if req.Scope != model.ScopeProject {
		t.Errorf("Scope: %q", req.Scope)
	}
	if req.TopicKey != "arch/decision" {
		t.Errorf("TopicKey: %q", req.TopicKey)
	}
	if req.Project != "test/project" {
		t.Errorf("Project: %q", req.Project)
	}
	if req.CreatedBy != "claude-code" {
		t.Errorf("CreatedBy: %q", req.CreatedBy)
	}
	if len(req.Files) != 1 || req.Files[0] != "internal/service/memory.go" {
		t.Errorf("Files: %v", req.Files)
	}
	if req.Importance == nil || *req.Importance != 0.85 {
		t.Errorf("Importance pointer mismatch")
	}
	// Non-rule: applies_to must be nil/empty.
	if len(req.AppliesTo) != 0 {
		t.Errorf("AppliesTo should be empty for non-rule, got %v", req.AppliesTo)
	}
}

func TestToSaveRequest_Rule(t *testing.T) {
	fm := Frontmatter{
		Type:       "rule",
		Scope:      "project",
		Title:      "No globals",
		Importance: 0.95,
		AppliesTo:  []string{"internal/**/*.go", "tool:Edit"},
		Severity:   "block",
	}

	req := fm.ToSaveRequest("Rule content.")

	if req.Type != model.TypeRule {
		t.Errorf("Type: %q", req.Type)
	}
	if len(req.AppliesTo) != 2 {
		t.Fatalf("AppliesTo len: %d", len(req.AppliesTo))
	}
	if req.AppliesTo[0] != "internal/**/*.go" {
		t.Errorf("AppliesTo[0]: %q", req.AppliesTo[0])
	}
	if req.Severity != model.SeverityBlock {
		t.Errorf("Severity: %q", req.Severity)
	}
}

// --- ToUpdateRequest tests ---

func TestToUpdateRequest_Full(t *testing.T) {
	fm := Frontmatter{
		Type:       "architecture",
		Scope:      "project",
		Title:      "Updated title",
		Importance: 0.75,
		Confidence: 0.90,
		Files:      []string{"internal/vault/reader.go"},
	}
	body := "Updated content"

	req := fm.ToUpdateRequest(body)

	if req.Title == nil || *req.Title != "Updated title" {
		t.Errorf("Title: %v", req.Title)
	}
	if req.Content == nil || *req.Content != body {
		t.Errorf("Content: %v", req.Content)
	}
	if req.Type == nil || *req.Type != model.TypeArchitecture {
		t.Errorf("Type: %v", req.Type)
	}
	if req.Importance == nil || *req.Importance != 0.75 {
		t.Errorf("Importance: %v", req.Importance)
	}
	if req.Confidence == nil || *req.Confidence != 0.90 {
		t.Errorf("Confidence: %v", req.Confidence)
	}
	if req.Files == nil || len(*req.Files) != 1 || (*req.Files)[0] != "internal/vault/reader.go" {
		t.Errorf("Files: %v", req.Files)
	}
	// Severity empty → should not be set
	if req.Severity != nil {
		t.Errorf("Severity should be nil for non-rule, got %v", req.Severity)
	}
}
