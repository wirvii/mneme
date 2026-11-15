package install_test

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wirvii/mneme/internal/install"
)

// The two caps a bundled skill must respect on its worst real loading path:
// an agent holds SKILL.md and, at most, one reference at a time, so what
// counts is SKILL.md plus its LARGEST references/**/*.md, never the sum of
// all of them (SPEC-188 D5, amended). 20,000 characters is where Claude
// Code cuts a skill after compacting (release notes of version 7.0.1 of the
// superpowers skills plugin); 4096 bytes is the SKILL.md cap of
// docs/skills.md.
const (
	skillCharBudget   = 20000 // runes of SKILL.md + the largest references/**/*.md
	skillMainMaxBytes = 4096  // bytes of SKILL.md
)

// minBundledSkills is the population floor: below it the guard would pass
// over an embed that silently lost its skills.
const minBundledSkills = 10

// skillWorstPath returns the rune count of SKILL.md, the largest
// references/**/*.md (and its name, "-" when there is none).
func skillWorstPath(files map[string][]byte) (main, largest int, largestName string) {
	main = utf8.RuneCount(files["SKILL.md"])
	largestName = "-"
	for name, content := range files {
		if !strings.HasPrefix(name, "references/") || !strings.HasSuffix(name, ".md") {
			continue
		}
		if n := utf8.RuneCount(content); n > largest {
			largest, largestName = n, name
		}
	}
	return main, largest, largestName
}

// skillBudgetFindings is the pure check behind the two guards. files maps a
// path relative to the skill directory to its content; only .md files under
// references/ count towards the worst path (a license text or validation/
// is not an instruction an agent loads).
func skillBudgetFindings(files map[string][]byte) []string {
	main, largest, _ := skillWorstPath(files)
	worst := main + largest
	mainBytes := len(files["SKILL.md"])
	var findings []string
	if mainBytes > skillMainMaxBytes {
		findings = append(findings, fmt.Sprintf("SKILL.md: %d bytes > %d", mainBytes, skillMainMaxBytes))
	}
	if worst > skillCharBudget {
		findings = append(findings, fmt.Sprintf("skill: %d caracteres > %d", worst, skillCharBudget))
	}
	return findings
}

// skillFiles groups the bundled skill entries by skill name.
func skillFiles(t *testing.T) (names []string, files map[string]map[string][]byte) {
	t.Helper()
	names, err := install.BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}
	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}
	files = make(map[string]map[string][]byte, len(names))
	for _, e := range entries {
		skill, rest, ok := strings.Cut(filepath.ToSlash(e.RelPath), "/")
		if !ok {
			continue
		}
		if files[skill] == nil {
			files[skill] = map[string][]byte{}
		}
		files[skill][rest] = e.Content
	}
	sort.Strings(names)
	return names, files
}

// checkSkillBudget runs skillBudgetFindings over every bundled skill and
// reports the findings whose prefix is prefix.
func checkSkillBudget(t *testing.T, prefix string) {
	t.Helper()
	names, files := skillFiles(t)
	if len(names) < minBundledSkills {
		t.Fatalf("only %d bundled skills, want at least %d", len(names), minBundledSkills)
	}
	for _, n := range names {
		main, largest, largestName := skillWorstPath(files[n])
		t.Logf("%s: SKILL.md %d bytes; peor camino %d caracteres (SKILL.md %d + %s %d)",
			n, len(files[n]["SKILL.md"]), main+largest, main, largestName, largest)
		for _, f := range skillBudgetFindings(files[n]) {
			if strings.HasPrefix(f, prefix) {
				t.Errorf("%s: %s", n, f)
			}
		}
	}
}

// TestBundledSkills_CharBudget is SPEC-188 C11: no bundled skill's worst
// loading path exceeds skillCharBudget characters.
func TestBundledSkills_CharBudget(t *testing.T) {
	checkSkillBudget(t, "skill:")
}

// TestBundledSkills_MainFileBytes is SPEC-188 C11: no bundled SKILL.md
// exceeds skillMainMaxBytes bytes. Unlike the
// validation/run.sh scripts it needs no sh, so it runs on Windows too.
func TestBundledSkills_MainFileBytes(t *testing.T) {
	checkSkillBudget(t, "SKILL.md:")
}

// TestSkillBudget_FindingsDiscriminate pins skillBudgetFindings on synthetic
// skills, so each cap is proven to trigger alone.
func TestSkillBudget_FindingsDiscriminate(t *testing.T) {
	ascii := func(n int) []byte { return []byte(strings.Repeat("a", n)) }
	tests := []struct {
		name  string
		files map[string][]byte
		want  []string
	}{
		{"SKILL.md over 4096 bytes", map[string][]byte{"SKILL.md": ascii(4097)},
			[]string{"SKILL.md: 4097 bytes > 4096"}},
		{"characters, not bytes", map[string][]byte{
			"SKILL.md":        []byte(strings.Repeat("á", 2000)),
			"references/r.md": ascii(18001),
		}, []string{"skill: 20001 caracteres > 20000"}},
		{"largest reference, not the sum", map[string][]byte{
			"SKILL.md":        ascii(1000),
			"references/a.md": ascii(12000),
			"references/b.md": ascii(12000),
		}, nil},
		{"non-md reference ignored", map[string][]byte{
			"SKILL.md":               ascii(100),
			"references/LICENSE.txt": ascii(30000),
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := skillBudgetFindings(tt.files)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}
