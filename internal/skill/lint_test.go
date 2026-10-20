package skill_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wirvii/mneme/internal/skill"
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
		name       string
		md         string
		dirName    string
		wantErrMsg string // substring to find in any error message
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
			name:       "semver with trailing garbage rejected",
			md:         makeSkillMD("my-skill", "Valid description that is longer than 20 chars.", "1.2.3garbage", false, ""),
			dirName:    "my-skill",
			wantErrMsg: "not a valid semver",
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

// TestLint_SemverPreRelease verifies that a pre-release version like "1.2.3-rc.1"
// is accepted by the semver check.
func TestLint_SemverPreRelease(t *testing.T) {
	md := makeSkillMD("my-skill", "Valid description that is longer than 20 chars.", "1.2.3-rc.1", false, conformantBody)
	s, err := skill.Parse([]byte(md))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result := skill.Lint(s, "my-skill")
	for _, f := range result.Errors {
		if contains(f.Message, "not a valid semver") {
			t.Errorf("pre-release version 1.2.3-rc.1 should be accepted, got error: %s", f.Message)
		}
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

const longDesc = "Use when you need a thing done properly."

// skillOfSize builds a SKILL.md of exactly size bytes with the given extra body.
func skillOfSize(t *testing.T, size int, body string) string {
	t.Helper()
	head := "---\nname: my-skill\ndescription: " + longDesc + "\n---\n" + body
	if len(head) > size {
		t.Fatalf("head larger than requested size %d", size)
	}
	return head + strings.Repeat("x", size-len(head))
}

func lintMD(t *testing.T, md string) skill.LintResult {
	t.Helper()
	s, err := skill.Parse([]byte(md))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return skill.Lint(s, "my-skill")
}

func hasMsg(fs []skill.Finding, sub string) bool {
	for _, f := range fs {
		if strings.Contains(f.Message, sub) {
			return true
		}
	}
	return false
}

// TestLint_MinimalFormatPasses verifies a front matter plus one paragraph passes.
func TestLint_MinimalFormatPasses(t *testing.T) {
	r := lintMD(t, "---\nname: my-skill\ndescription: "+longDesc+"\n---\nHello.\n")
	if !r.Passed || len(r.Errors) != 0 {
		t.Fatalf("errors: %v", r.Errors)
	}
}

// TestLint_LegacyFiveSectionFormatStillPasses verifies the five-section format.
func TestLint_LegacyFiveSectionFormatStillPasses(t *testing.T) {
	r := lintMD(t, makeSkillMD("my-skill", "Valid description for use when testing.", "1.0.0", false, conformantBody))
	if !r.Passed || len(r.Warnings) != 0 {
		t.Fatalf("errors %v warnings %v", r.Errors, r.Warnings)
	}
}

// TestLint_SizeLimits covers the soft and hard size limits and the legacy exemption.
func TestLint_SizeLimits(t *testing.T) {
	legacy := strings.TrimPrefix(conformantBody, "\n")
	cases := []struct {
		name      string
		md        string
		wantWarn  string
		wantError bool
	}{
		{"at soft limit", skillOfSize(t, skill.MainFileSoftLimit, ""), "", false},
		{"over soft limit", skillOfSize(t, skill.MainFileSoftLimit+1, ""), "references/", false},
		{"at hard limit", skillOfSize(t, skill.MainFileHardLimit, ""), "references/", false},
		{"over hard limit flexible", skillOfSize(t, skill.MainFileHardLimit+1, ""), "", true},
		{"over hard limit legacy", skillOfSize(t, skill.MainFileHardLimit+1, legacy), "exento por formato heredado", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := lintMD(t, tc.md)
			if tc.wantError != (len(r.Errors) > 0) {
				t.Fatalf("wantError=%v errors=%v", tc.wantError, r.Errors)
			}
			if tc.wantWarn == "" && !tc.wantError && hasMsg(r.Warnings, "mide") {
				t.Fatalf("unexpected size warning: %v", r.Warnings)
			}
			if tc.wantWarn != "" && !hasMsg(r.Warnings, tc.wantWarn) {
				t.Fatalf("want warning %q, got %v", tc.wantWarn, r.Warnings)
			}
		})
	}
}

// TestLint_VersionOptional verifies absent version is info and a bad one an error.
func TestLint_VersionOptional(t *testing.T) {
	r := lintMD(t, "---\nname: my-skill\ndescription: "+longDesc+"\n---\nx\n")
	if len(r.Errors) != 0 || !hasMsg(r.Infos, "version") {
		t.Fatalf("errors %v infos %v", r.Errors, r.Infos)
	}
	r = lintMD(t, "---\nname: my-skill\ndescription: "+longDesc+"\nversion: 1.2\n---\nx\n")
	if !hasMsg(r.Errors, "not a valid semver") {
		t.Fatalf("errors %v", r.Errors)
	}
}

// TestLint_DescriptionWhenAdvisory verifies the "when/cuando" advisory.
func TestLint_DescriptionWhenAdvisory(t *testing.T) {
	cases := []struct {
		desc string
		warn bool
	}{
		{"Does a thing with files and folders", true},
		{"Use WHEN the user asks for a thing", false},
		{"Úsala cuando el usuario pida algo", false},
	}
	for _, tc := range cases {
		r := lintMD(t, "---\nname: my-skill\ndescription: "+tc.desc+"\n---\nx\n")
		if got := hasMsg(r.Warnings, "WHEN"); got != tc.warn {
			t.Errorf("%q: warn=%v want %v", tc.desc, got, tc.warn)
		}
	}
}

// TestLint_AutomatedChecksTableIsWarning verifies a bad table is only a warning.
func TestLint_AutomatedChecksTableIsWarning(t *testing.T) {
	r := lintMD(t, "---\nname: my-skill\ndescription: "+longDesc+"\n---\n## Automated Checks\n| Wrong | Headers | Here |\n|---|---|---|\n")
	if len(r.Errors) != 0 || !hasMsg(r.Warnings, "does not match required") {
		t.Fatalf("errors %v warnings %v", r.Errors, r.Warnings)
	}
	r = lintMD(t, "---\nname: my-skill\ndescription: "+longDesc+"\n---\n## Automated Checks\nno table\n")
	if len(r.Errors) != 0 || !hasMsg(r.Warnings, "no markdown table") {
		t.Fatalf("errors %v warnings %v", r.Errors, r.Warnings)
	}
}

// TestLintFS_References covers existing, missing and escaping references.
func TestLintFS_References(t *testing.T) {
	front := "---\nname: my-skill\ndescription: " + longDesc + "\n---\n"
	cases := []struct {
		name    string
		body    string
		files   []string
		wantErr string
	}{
		{"existing", "Open references/a.md now.\n", []string{"references/a.md"}, ""},
		{"missing names the path", "Open references/missing.md now.\n", nil, "references/missing.md"},
		{"escapes with dotdot", "See references/../x here.\n", []string{"x"}, "escapa"},
		{"trailing dot trimmed", "Read references/a.md.\n", []string{"references/a.md"}, ""},
		{"code block checked", "```\ncat references/nope.md\n```\n", nil, "references/nope.md"},
		{"bare prefix ignored", "the references/ folder\n", nil, ""},
		{"duplicate reported once", "references/z.md and references/z.md\n", nil, "references/z.md"},
		{"not a reference path", "see myreferences/q.md\n", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"SKILL.md": {Data: []byte(front + tc.body)}}
			for _, f := range tc.files {
				fsys[f] = &fstest.MapFile{Data: []byte("x")}
			}
			r := skill.LintFS(fsys, "my-skill")
			if tc.wantErr == "" {
				if !r.Passed {
					t.Fatalf("errors: %v", r.Errors)
				}
				return
			}
			if !hasMsg(r.Errors, tc.wantErr) || len(r.Errors) != 1 {
				t.Fatalf("want one error containing %q, got %v", tc.wantErr, r.Errors)
			}
		})
	}
	r := skill.LintFS(fstest.MapFS{}, "my-skill")
	if r.Passed || !hasMsg(r.Errors, "cannot parse SKILL.md") {
		t.Fatalf("missing SKILL.md: %v", r.Errors)
	}
}
