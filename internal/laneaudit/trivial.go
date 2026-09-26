package laneaudit

import (
	"fmt"

	"github.com/wirvii/mneme/internal/gitrepo"
)

// Breach is a single human-readable trivial-lane threshold violation — a
// named string type, not a bare string, so a caller cannot accidentally
// pass an unrelated string where a Breach is expected.
type Breach string

// TrivialBudget is the trivial lane's own budget: fixed limits on file
// count and total changed lines, plus the paths a trivial-lane spec must
// never touch.
type TrivialBudget struct {
	MaxFiles       int
	MaxLines       int
	ForbiddenGlobs []string
}

// DefaultTrivialBudget is the DEFINITION of the trivial lane — 3 files, 20
// lines, and the five forbidden-path globs. It lives in the BINARY, not any
// configuration file: these numbers are mneme's own vocabulary for what
// "trivial" means in the SDD, documented in CLAUDE.md and docs/lanes.md —
// not a per-project threshold a team calibrates.
var DefaultTrivialBudget = TrivialBudget{
	MaxFiles: 3,
	MaxLines: 20,
	ForbiddenGlobs: []string{
		"**/*.sql",
		"**/migrations/**",
		"**/schema.*",
		"cmd/**",
		"internal/install/assets/**",
	},
}

// EvaluateTrivialBudget is the trivial lane's own auditor: four checks over
// FileStat/SymbolDelta, with breach texts kept BYTE FOR BYTE identical to
// their pre-SPEC-158 originals — those strings are concatenated verbatim
// into lane_audits.Breaches and read back by LaneStatus, so changing them
// would break every historical audit's readability.
func EvaluateTrivialBudget(files []gitrepo.FileStat, symDelta SymbolDelta, scope string, b TrivialBudget) []Breach {
	var breaches []Breach

	if len(files) > b.MaxFiles {
		breaches = append(breaches, Breach(fmt.Sprintf("file count %d exceeds trivial limit of %d", len(files), b.MaxFiles)))
	}

	lines := 0
	for _, f := range files {
		lines += f.Added + f.Removed
	}
	if lines > b.MaxLines {
		breaches = append(breaches, Breach(fmt.Sprintf("line count %d exceeds trivial limit of %d", lines, b.MaxLines)))
	}

	for _, f := range files {
		if MatchGlobs(f.Path, b.ForbiddenGlobs) {
			breaches = append(breaches, Breach(fmt.Sprintf("forbidden path modified: %s", f.Path)))
		}
		if scope != "" && !MatchGlobs(f.Path, []string{scope}) {
			breaches = append(breaches, Breach(fmt.Sprintf("out of scope: %s", f.Path)))
		}
	}

	for _, s := range symDelta.Created {
		if s.Exported {
			breaches = append(breaches, Breach(fmt.Sprintf("public symbol changed: %s in %s", s.QualifiedName, s.File)))
		}
	}
	for _, s := range symDelta.Deleted {
		if s.Exported {
			breaches = append(breaches, Breach(fmt.Sprintf("public symbol changed: %s in %s", s.QualifiedName, s.File)))
		}
	}

	return breaches
}
