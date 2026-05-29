package skill_test

import (
	"testing"

	"github.com/juanftp/mneme/internal/skill"
)

// TestLint_FixturePasses verifies that a fully conformant skill passes without
// any error findings.
func TestLint_FixturePasses(t *testing.T) {
	s, err := skill.Parse([]byte(conformantSKILLMD))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	result := skill.Lint(s, "test-skill")

	if !result.Passed {
		t.Errorf("expected Passed=true, got errors: %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

// TestLint_Violations tests each individual violation in isolation.
func TestLint_Violations(t *testing.T) {
	cases := []struct {
		name        string
		md          string
		dirName     string
		wantErrMsg  string // substring to find in any error message
	}{
		{
			name:       "missing name",
			md:         makeSkillMD("", "Valid description that is longer than 20 chars.", "1.0.0", false, ""),
			dirName:    "my-skill",
			wantErrMsg: "name is required",
		},
		{
			name:       "missing description",
			md:         makeSkillMD("my-skill", "", "1.0.0", false, ""),
			dirName:    "my-skill",
			wantErrMsg: "description is required",
		},
		{
			name:       "missing version",
			md:         makeSkillMD("my-skill", "Valid description that is longer than 20 chars.", "", false, ""),
			dirName:    "my-skill",
			wantErrMsg: "version is required",
		},
		{
			name:       "name != dirName",
			md:         makeSkillMD("different-name", "Valid description that is longer than 20 chars.", "1.0.0", false, ""),
			dirName:    "my-skill",
			wantErrMsg: "does not match directory name",
		},
		{
			name:       "invalid semver",
			md:         makeSkillMD("my-skill", "Valid description that is longer than 20 chars.", "notasemver", false, ""),
			dirName:    "my-skill",
			wantErrMsg: "not a valid semver",
		},
		{
			name: "missing section When to Use",
			md: "---\nname: my-skill\ndescription: \"Valid description that is longer than 20 chars.\"\nversion: 1.0.0\n---\n" +
				"## Critical Rules\n1. rule\n## Automated Checks\n| Check | What it verifies | How to fix |\n|---|---|---|\n| a | b | c |\n## Verification\nok\n## Workflow\n1. step\n",
			dirName:    "my-skill",
			wantErrMsg: "When To Use",
		},
		{
			name: "missing Automated Checks section",
			md: "---\nname: my-skill\ndescription: \"Valid description that is longer than 20 chars.\"\nversion: 1.0.0\n---\n" +
				"## When to Use\nuse it\n## Critical Rules\n1. rule\n## Verification\nok\n## Workflow\n1. step\n",
			dirName:    "my-skill",
			wantErrMsg: "Automated Checks",
		},
		{
			name: "Automated Checks table malformed",
			md: "---\nname: my-skill\ndescription: \"Valid description that is longer than 20 chars.\"\nversion: 1.0.0\n---\n" +
				"## When to Use\nuse it\n## Critical Rules\n1. rule\n## Automated Checks\n| Wrong | Headers | Here |\n|---|---|---|\n## Verification\nok\n## Workflow\n1. step\n",
			dirName:    "my-skill",
			wantErrMsg: "does not match required",
		},
		{
			name: "Automated Checks no table",
			md: "---\nname: my-skill\ndescription: \"Valid description that is longer than 20 chars.\"\nversion: 1.0.0\n---\n" +
				"## When to Use\nuse it\n## Critical Rules\n1. rule\n## Automated Checks\nNo table here.\n## Verification\nok\n## Workflow\n1. step\n",
			dirName:    "my-skill",
			wantErrMsg: "no markdown table found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := skill.Parse([]byte(tc.md))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			result := skill.Lint(s, tc.dirName)

			if result.Passed {
				t.Errorf("expected Passed=false for %q", tc.name)
				return
			}

			found := false
			for _, f := range result.Errors {
				if contains(f.Message, tc.wantErrMsg) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("want error containing %q, got errors: %v", tc.wantErrMsg, result.Errors)
			}
		})
	}
}

// TestLint_DescriptionWarnings verifies advisory warnings for short/long descriptions.
func TestLint_DescriptionWarnings(t *testing.T) {
	t.Run("short description", func(t *testing.T) {
		s, _ := skill.Parse([]byte(makeSkillMD("my-skill", "short", "1.0.0", false, conformantBody)))
		r := skill.Lint(s, "my-skill")
		found := false
		for _, w := range r.Warnings {
			if contains(w.Message, "short") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected short description warning, got warnings: %v", r.Warnings)
		}
	})

	t.Run("long description", func(t *testing.T) {
		long := repeatChar('x', 501)
		s, _ := skill.Parse([]byte(makeSkillMD("my-skill", long, "1.0.0", false, conformantBody)))
		r := skill.Lint(s, "my-skill")
		found := false
		for _, w := range r.Warnings {
			if contains(w.Message, "500") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected long description warning, got warnings: %v", r.Warnings)
		}
	})
}

// TestLint_ExtraKeysInfo verifies that unknown frontmatter keys produce Info findings.
func TestLint_ExtraKeysInfo(t *testing.T) {
	md := "---\nname: my-skill\ndescription: \"Valid description that is longer than 20 chars.\"\nversion: 1.0.0\nunknown_key: value\n---\n" +
		conformantBody
	s, _ := skill.Parse([]byte(md))
	r := skill.Lint(s, "my-skill")

	found := false
	for _, info := range r.Infos {
		if contains(info.Message, "unknown_key") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected info finding for unknown_key, got infos: %v", r.Infos)
	}
}

// --- helpers ---

const conformantBody = `
## When to Use

Use this skill when testing.

## Critical Rules

1. Rule one.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| rule-one | Verifies rule one | Fix it |

## Verification

Run the script.

## Workflow

1. Step one.
`

func makeSkillMD(name, description, version string, pinned bool, body string) string {
	pinnedStr := "false"
	if pinned {
		pinnedStr = "true"
	}
	desc := description
	if desc != "" {
		desc = `"` + desc + `"`
	}
	return "---\n" +
		"name: " + name + "\n" +
		"description: " + desc + "\n" +
		"version: " + version + "\n" +
		"pinned: " + pinnedStr + "\n" +
		"---\n" +
		body
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := range s {
		if len(s)-i >= len(sub) && s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func repeatChar(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
