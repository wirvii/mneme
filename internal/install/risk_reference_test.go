package install

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/risk"
)

// riskItemLine matches one numbered item of risk.md: `N. **Title.** text`.
var riskItemLine = regexp.MustCompile(`^(\d+)\. \*\*(.+?)\.\*\* `)

// riskTableRow matches one row of risk.md's path table: `| N | patterns |`.
var riskTableRow = regexp.MustCompile(`^\| (\d+) \| (.+) \|$`)

// TestRiskReference_MatchesPackage is SPEC-184 Dp2's drift guard: the
// embedded mneme-flow/references/risk.md that agents read and the
// internal/risk constants the auditor, SPEC-182 and SPEC-185 read must say
// the same thing — six numbered items titled exactly as risk.Item.Title(),
// and every default path pattern quoted in its item's table row.
func TestRiskReference_MatchesPackage(t *testing.T) {
	data, err := builtinSkills.ReadFile("assets/skills/mneme-flow/references/risk.md")
	if err != nil {
		t.Fatalf("read embedded risk.md: %v", err)
	}

	titles := map[int]string{}
	rows := map[int]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if m := riskItemLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			if _, dup := titles[n]; dup {
				t.Errorf("item %d listed twice", n)
			}
			titles[n] = m[2]
		}
		if m := riskTableRow.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			rows[n] = m[2]
		}
	}

	if len(titles) != len(risk.Items()) {
		t.Fatalf("risk.md lists %d numbered items, want exactly %d: %v", len(titles), len(risk.Items()), titles)
	}
	for _, item := range risk.Items() {
		if got := titles[int(item)]; got != item.Title() {
			t.Errorf("item %d title = %q, want %q", item, got, item.Title())
		}
	}

	for item, globs := range risk.DefaultTable() {
		row, ok := rows[int(item)]
		if !ok {
			t.Errorf("risk.md has no table row for item %d", item)
			continue
		}
		for _, g := range globs {
			if !strings.Contains(row, "`"+g+"`") {
				t.Errorf("item %d row lacks `%s`", item, g)
			}
		}
	}
	for n := range rows {
		if !risk.Item(n).PathDetectable() {
			t.Errorf("risk.md has a path row for item %d, which no path can reveal", n)
		}
	}
}
