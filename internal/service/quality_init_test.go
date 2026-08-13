package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/quality"
)

// TestEnsureQualityConstitution_WritesTemplateWhenAbsent covers AC23: an
// absent constitution is written with quality.Template() (enabled=false,
// parses without error).
func TestEnsureQualityConstitution_WritesTemplateWhenAbsent(t *testing.T) {
	svc := newTestInitService(t)
	repoRoot := t.TempDir()

	findings, err := svc.EnsureQualityConstitution(repoRoot, false)
	if err != nil {
		t.Fatalf("EnsureQualityConstitution: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %v, want none for a freshly materialized constitution", findings)
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot, ".mneme", "quality.toml"))
	if err != nil {
		t.Fatalf("read written constitution: %v", err)
	}
	c, err := quality.Parse(raw)
	if err != nil {
		t.Fatalf("written constitution does not parse: %v", err)
	}
	if c.Enabled {
		t.Error("written constitution has enabled=true, want false")
	}
}

// TestEnsureQualityConstitution_CheckMode_NeverWrites covers --check: no
// file is written even when absent.
func TestEnsureQualityConstitution_CheckMode_NeverWrites(t *testing.T) {
	svc := newTestInitService(t)
	repoRoot := t.TempDir()

	findings, err := svc.EnsureQualityConstitution(repoRoot, true)
	if err != nil {
		t.Fatalf("EnsureQualityConstitution: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %v, want none", findings)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".mneme", "quality.toml")); !os.IsNotExist(err) {
		t.Errorf("expected no file to be written in --check mode, stat err = %v", err)
	}
}

// TestEnsureQualityConstitution_PresentIsNeverTouched covers idempotency: a
// second pass over a constitution modified BY HAND must be byte-identical —
// the file is never rewritten once it exists.
func TestEnsureQualityConstitution_PresentIsNeverTouched(t *testing.T) {
	svc := newTestInitService(t)
	repoRoot := t.TempDir()

	dir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	handCrafted := "schema_version = 1\nenabled = true\n[execution]\noutput_tail_bytes = 8192\n"
	path := filepath.Join(dir, "quality.toml")
	if err := os.WriteFile(path, []byte(handCrafted), 0o644); err != nil {
		t.Fatalf("write hand-crafted constitution: %v", err)
	}

	if _, err := svc.EnsureQualityConstitution(repoRoot, false); err != nil {
		t.Fatalf("EnsureQualityConstitution: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != handCrafted {
		t.Errorf("constitution was modified: got %q, want byte-identical %q", got, handCrafted)
	}
}

// TestEnsureQualityConstitution_SchemaDrift_ReportsAdvisoryFinding covers
// D15's drift channel: a present constitution with a schema_version other
// than quality.CurrentSchemaVersion produces exactly one advisory
// DriftFinding, and the file is still never written.
func TestEnsureQualityConstitution_SchemaDrift_ReportsAdvisoryFinding(t *testing.T) {
	svc := newTestInitService(t)
	repoRoot := t.TempDir()

	dir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	futureSchema := "schema_version = 99\nenabled = true\n"
	path := filepath.Join(dir, "quality.toml")
	if err := os.WriteFile(path, []byte(futureSchema), 0o644); err != nil {
		t.Fatalf("write future-schema constitution: %v", err)
	}

	findings, err := svc.EnsureQualityConstitution(repoRoot, false)
	if err != nil {
		t.Fatalf("EnsureQualityConstitution: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want exactly 1", findings)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != futureSchema {
		t.Error("constitution was modified despite schema drift — it must never be written")
	}
}
