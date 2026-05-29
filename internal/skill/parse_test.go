package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/juanftp/mneme/internal/skill"
)

// conformantSKILLMD is a fully valid SKILL.md for use across test cases.
const conformantSKILLMD = `---
name: test-skill
description: "A test skill with sufficient description length for linting."
version: 1.2.3
pinned: false
license: MIT
---

## When to Use

Use this skill when testing.

## Critical Rules

1. Rule one.
2. Rule two.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| rule-one | Verifies rule one passes | Fix rule one |

## Verification

Run the validation script.

## Workflow

1. Step one.
2. Step two.
`

func TestParse_Conformant(t *testing.T) {
	s, err := skill.Parse([]byte(conformantSKILLMD))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}

	if s.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", s.Name, "test-skill")
	}
	if s.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", s.Version, "1.2.3")
	}
	if s.Pinned {
		t.Error("Pinned should be false")
	}
	if s.License != "MIT" {
		t.Errorf("License = %q, want %q", s.License, "MIT")
	}

	wantSections := []string{"When to Use", "Critical Rules", "Automated Checks", "Verification", "Workflow"}
	if len(s.Sections) != len(wantSections) {
		t.Fatalf("Sections count = %d, want %d", len(s.Sections), len(wantSections))
	}
	for i, want := range wantSections {
		if s.Sections[i].Heading != want {
			t.Errorf("Sections[%d].Heading = %q, want %q", i, s.Sections[i].Heading, want)
		}
	}
}

func TestParse_MissingOpeningDelimiter(t *testing.T) {
	data := []byte("name: bad\n---\n")
	_, err := skill.Parse(data)
	if err == nil {
		t.Fatal("expected error for missing opening ---")
	}
}

func TestParse_MissingClosingDelimiter(t *testing.T) {
	data := []byte("---\nname: bad\n")
	_, err := skill.Parse(data)
	if err == nil {
		t.Fatal("expected error for missing closing ---")
	}
}

func TestParse_EmptyFields(t *testing.T) {
	data := []byte("---\n---\n## When to Use\nBody.\n")
	s, err := skill.Parse(data)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if s.Name != "" {
		t.Errorf("Name should be empty, got %q", s.Name)
	}
	if s.Version != "" {
		t.Errorf("Version should be empty, got %q", s.Version)
	}
}

func TestParse_ExtraKeys(t *testing.T) {
	data := []byte("---\nname: my-skill\nfoo: bar\nbaz: qux\n---\n")
	s, err := skill.Parse(data)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if s.Extra["foo"] != "bar" {
		t.Errorf("Extra[foo] = %q, want %q", s.Extra["foo"], "bar")
	}
	if s.Extra["baz"] != "qux" {
		t.Errorf("Extra[baz] = %q, want %q", s.Extra["baz"], "qux")
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(conformantSKILLMD), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := skill.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: unexpected error: %v", err)
	}
	if s.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", s.Name, "test-skill")
	}
}

func TestRewritePinned_RoundTrip(t *testing.T) {
	data := []byte(conformantSKILLMD)

	pinned, err := skill.RewritePinned(data, true)
	if err != nil {
		t.Fatalf("RewritePinned(true): %v", err)
	}

	s, err := skill.Parse(pinned)
	if err != nil {
		t.Fatalf("Parse after RewritePinned(true): %v", err)
	}
	if !s.Pinned {
		t.Error("Pinned should be true after RewritePinned(true)")
	}

	// Sections must survive the round-trip.
	if len(s.Sections) != 5 {
		t.Errorf("Sections count = %d after RewritePinned, want 5", len(s.Sections))
	}

	// Unpin.
	unpinned, err := skill.RewritePinned(pinned, false)
	if err != nil {
		t.Fatalf("RewritePinned(false): %v", err)
	}
	s2, err := skill.Parse(unpinned)
	if err != nil {
		t.Fatalf("Parse after RewritePinned(false): %v", err)
	}
	if s2.Pinned {
		t.Error("Pinned should be false after RewritePinned(false)")
	}
}

func TestWriteFrontmatter_Deterministic(t *testing.T) {
	m := skill.Metadata{
		Name:        "my-skill",
		Description: "A short description.",
		Version:     "0.1.0",
		Pinned:      false,
		License:     "Apache-2.0",
		Extra:       map[string]string{"z": "last", "a": "first"},
	}

	fm := string(skill.WriteFrontmatter(m))

	// Extra keys must appear in sorted order.
	aIdx := indexOf(fm, "a: first")
	zIdx := indexOf(fm, "z: last")
	if aIdx < 0 || zIdx < 0 {
		t.Fatalf("WriteFrontmatter missing extra keys; got:\n%s", fm)
	}
	if aIdx > zIdx {
		t.Errorf("Extra keys not in sorted order; a at %d, z at %d", aIdx, zIdx)
	}
}

func indexOf(s, sub string) int {
	for i := range s {
		if len(s)-i >= len(sub) && s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
