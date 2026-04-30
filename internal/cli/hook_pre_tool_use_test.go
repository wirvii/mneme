package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/rules"
)

// insertTestRule inserts a rule into the given database for test purposes.
func insertTestRule(database *db.DB, id, title, content string, severity model.Severity, appliesTo []string) error {
	appliesToJSON, err := json.Marshal(appliesTo)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = database.Exec(
		`INSERT INTO memories
		 (id, type, scope, title, content, applies_to, severity, created_at, updated_at, importance, confidence, decay_rate)
		 VALUES (?, 'rule', 'project', ?, ?, ?, ?, ?, ?, 0.95, 0.8, 0.0)`,
		id, title, content, string(appliesToJSON), string(severity), now, now,
	)
	return err
}

// buildPreToolStdin returns a JSON-encoded PreToolUse hook stdin payload as a buffer.
func buildPreToolStdin(toolName, filePath string) *bytes.Buffer {
	payload := map[string]any{
		"tool_name": toolName,
		"tool_input": map[string]any{
			"file_path": filePath,
		},
	}
	data, _ := json.Marshal(payload)
	return bytes.NewBuffer(data)
}

// TestPreToolUse_BlockRule verifies that a block-severity rule renders [BLOCK]
// and "Action: BLOCKED" in the output.
func TestPreToolUse_BlockRule(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if insertErr := insertTestRule(database, "r1", "Protect source", "Do not edit source.",
		model.SeverityBlock, []string{"tool:Edit+internal/**"}); insertErr != nil {
		t.Fatalf("insertTestRule: %v", insertErr)
	}
	database.Close()

	rulesList, err := queryRulesFromDB(dbPath)
	if err != nil {
		t.Fatalf("queryRulesFromDB: %v", err)
	}

	cwd := dir
	filePath := filepath.Join(dir, "internal", "store", "memory.go")
	result := rules.Match(rulesList, "Edit", filePath, cwd)

	var stdout bytes.Buffer
	renderPreToolUseOutput(&stdout, "Edit", filePath, cwd, result)

	got := stdout.String()
	for _, want := range []string{"[BLOCK]", "Action: BLOCKED", "<!-- mneme:rules:start -->", "<!-- mneme:rules:end -->"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, got)
		}
	}
}

// TestPreToolUse_WarnRule verifies that a warn-severity match produces [WARN]
// and "Action: ALLOWED".
func TestPreToolUse_WarnRule(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if insertErr := insertTestRule(database, "r1", "SQL warning", "No inline SQL.",
		model.SeverityWarn, []string{"**/*.go"}); insertErr != nil {
		t.Fatalf("insertTestRule: %v", insertErr)
	}
	database.Close()

	rulesList, err := queryRulesFromDB(dbPath)
	if err != nil {
		t.Fatalf("queryRulesFromDB: %v", err)
	}

	cwd := dir
	filePath := filepath.Join(dir, "cmd", "main.go")
	result := rules.Match(rulesList, "Edit", filePath, cwd)

	var stdout bytes.Buffer
	renderPreToolUseOutput(&stdout, "Edit", filePath, cwd, result)

	got := stdout.String()
	if !strings.Contains(got, "[WARN]") {
		t.Errorf("output missing [WARN]\nfull:\n%s", got)
	}
	if !strings.Contains(got, "Action: ALLOWED") {
		t.Errorf("output missing 'Action: ALLOWED'\nfull:\n%s", got)
	}
}

// TestPreToolUse_InfoRule verifies that an info-severity match produces [INFO]
// and "Action: ALLOWED".
func TestPreToolUse_InfoRule(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if insertErr := insertTestRule(database, "r1", "Style tip", "Prefer value receivers.",
		model.SeverityInfo, []string{"**"}); insertErr != nil {
		t.Fatalf("insertTestRule: %v", insertErr)
	}
	database.Close()

	rulesList, err := queryRulesFromDB(dbPath)
	if err != nil {
		t.Fatalf("queryRulesFromDB: %v", err)
	}

	cwd := dir
	filePath := filepath.Join(dir, "foo.go")
	result := rules.Match(rulesList, "Write", filePath, cwd)

	var stdout bytes.Buffer
	renderPreToolUseOutput(&stdout, "Write", filePath, cwd, result)

	got := stdout.String()
	if !strings.Contains(got, "[INFO]") {
		t.Errorf("output missing [INFO]\nfull:\n%s", got)
	}
	if !strings.Contains(got, "Action: ALLOWED") {
		t.Errorf("output missing 'Action: ALLOWED'\nfull:\n%s", got)
	}
}

// TestPreToolUse_NoMatch verifies that when no rules match the path, the
// matched slice is empty and no output is rendered.
func TestPreToolUse_NoMatch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if insertErr := insertTestRule(database, "r1", "Protect internal", "Source-only.",
		model.SeverityBlock, []string{"tool:Edit+internal/**"}); insertErr != nil {
		t.Fatalf("insertTestRule: %v", insertErr)
	}
	database.Close()

	rulesList, err := queryRulesFromDB(dbPath)
	if err != nil {
		t.Fatalf("queryRulesFromDB: %v", err)
	}

	cwd := dir
	// Editing a docs file must not match "tool:Edit+internal/**".
	filePath := filepath.Join(dir, "docs", "README.md")
	result := rules.Match(rulesList, "Edit", filePath, cwd)

	if len(result.Matched) != 0 {
		t.Errorf("expected 0 matches for docs file, got %d", len(result.Matched))
	}
}

// TestPreToolUse_NonMutatingTool verifies that runHookPreToolUse returns nil
// with empty stdout for non-mutating tools (e.g. "Read").
func TestPreToolUse_NonMutatingTool(t *testing.T) {
	stdin := buildPreToolStdin("Read", "/some/file.go")
	var stdout, stderr bytes.Buffer

	if err := runHookPreToolUse(stdin, &stdout, &stderr); err != nil {
		t.Fatalf("runHookPreToolUse returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout for non-mutating tool, got: %s", stdout.String())
	}
}

// TestPreToolUse_MalformedJSON verifies that malformed stdin JSON produces an
// empty stdout, nil error, and a warning on stderr (fail open).
func TestPreToolUse_MalformedJSON(t *testing.T) {
	stdin := bytes.NewBufferString("{not valid json}")
	var stdout, stderr bytes.Buffer

	if err := runHookPreToolUse(stdin, &stdout, &stderr); err != nil {
		t.Fatalf("runHookPreToolUse returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout for malformed JSON, got: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid stdin JSON") {
		t.Errorf("expected stderr to contain 'invalid stdin JSON', got: %s", stderr.String())
	}
}

// TestPreToolUse_EmptyStdin verifies that an empty stdin results in an empty
// stdout, nil error, and no stderr output (silent fail open).
func TestPreToolUse_EmptyStdin(t *testing.T) {
	stdin := bytes.NewBufferString("")
	var stdout, stderr bytes.Buffer

	if err := runHookPreToolUse(stdin, &stdout, &stderr); err != nil {
		t.Fatalf("runHookPreToolUse returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout for empty stdin, got: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("expected empty stderr for empty stdin, got: %s", stderr.String())
	}
}

// TestPreToolUse_MultipleSeverities verifies that when block+warn+info rules all
// match, MaxSev is block and the output lists the block rule first.
func TestPreToolUse_MultipleSeverities(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	for _, r := range []struct {
		id, title string
		sev       model.Severity
		patterns  []string
	}{
		{"r1", "Info rule", model.SeverityInfo, []string{"**"}},
		{"r2", "Warn rule", model.SeverityWarn, []string{"**/*.go"}},
		{"r3", "Block rule", model.SeverityBlock, []string{"tool:Edit+internal/**"}},
	} {
		if insertErr := insertTestRule(database, r.id, r.title, r.title+" content.", r.sev, r.patterns); insertErr != nil {
			t.Fatalf("insertTestRule %s: %v", r.id, insertErr)
		}
	}
	database.Close()

	rulesList, err := queryRulesFromDB(dbPath)
	if err != nil {
		t.Fatalf("queryRulesFromDB: %v", err)
	}

	cwd := dir
	filePath := filepath.Join(dir, "internal", "store", "memory.go")
	result := rules.Match(rulesList, "Edit", filePath, cwd)

	if result.MaxSev != model.SeverityBlock {
		t.Errorf("MaxSev = %q, want block", result.MaxSev)
	}
	if len(result.Matched) != 3 {
		t.Errorf("expected 3 matched rules, got %d", len(result.Matched))
	}
	if result.Matched[0].Rule.Severity != model.SeverityBlock {
		t.Errorf("first matched rule should be block, got %q", result.Matched[0].Rule.Severity)
	}
}

// TestQueryRulesFromDB_FileNotExist verifies that queryRulesFromDB returns an
// empty slice and nil error when the database file is absent.
func TestQueryRulesFromDB_FileNotExist(t *testing.T) {
	rulesList, err := queryRulesFromDB("/tmp/mneme-missing-test-db-12345.db")
	if err != nil {
		t.Errorf("expected nil error for missing DB, got: %v", err)
	}
	if len(rulesList) != 0 {
		t.Errorf("expected 0 rules for missing DB, got %d", len(rulesList))
	}
}

// TestQueryRulesFromDB_EmptyDB verifies that an empty (migrated but no rules)
// database returns an empty slice without error.
func TestQueryRulesFromDB_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	database.Close()

	rulesList, err := queryRulesFromDB(dbPath)
	if err != nil {
		t.Errorf("expected nil error for empty DB, got: %v", err)
	}
	if len(rulesList) != 0 {
		t.Errorf("expected 0 rules for empty DB, got %d", len(rulesList))
	}
}
