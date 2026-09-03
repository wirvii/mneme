package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/gitattrs"
)

// EnsureGitattributes materializes or upserts the SPEC-140 .gitattributes
// block (D9) at repoRoot when it is safe to do so (D10/D11). It is a
// package-level function, not a method on any one service, precisely
// because D11 calls for ONE function and THREE callers spanning two
// different services (SDDService.EnableSDDRepo, MemoryService's
// team-memory enable) plus the CLI/MCP init path — a method on either
// service would force the other two to depend on it.
//
// The policy asks git, never the file's own content (D10): for each of
// gitattrs.Patterns()'s three path sets, `git check-attr eol` is run
// against a representative probe path (gitattrs.ProbePath — the path need
// not exist, AC6). gitattrs.Decide then turns those three answers into one
// of three verdicts:
//   - an explicit rule other than "lf" for some pattern: nothing is
//     written, and a DriftFinding names the pattern and the value found —
//     it is someone's deliberate rule and mneme does not override it;
//   - at least one "unspecified" and no explicit conflict: the block is
//     written (or replaced via gitattrs.Upsert), and a DriftFinding names
//     the path plus the three lines that were added (D11's "nunca sea
//     silencioso" contrapartida);
//   - "lf" already on all three: nothing is written and nothing is
//     reported — there is nothing to warn about.
//
// checkMode mirrors every other init step's --check contract: no file is
// ever written, and git is never even asked — AC10 verifies the second
// half of that promise via `git status --porcelain`, not by reading this
// function's source.
func EnsureGitattributes(repoRoot string, checkMode bool) ([]DriftFinding, error) {
	if checkMode {
		return nil, nil
	}

	g := sddGit{RepoDir: repoRoot}
	probes := make(map[gitattrs.Pattern]string, len(gitattrs.Patterns()))
	for _, p := range gitattrs.Patterns() {
		out, err := g.run("check-attr", "eol", "--", gitattrs.ProbePath(p))
		if err != nil {
			return nil, fmt.Errorf("service: gitattributes: check-attr %s: %w", p, err)
		}
		probes[p] = parseCheckAttrEOL(out, gitattrs.ProbePath(p))
	}

	decision := gitattrs.Decide(probes)
	path := filepath.Join(repoRoot, ".gitattributes")

	switch decision.Action {
	case gitattrs.ActionSkip:
		return nil, nil
	case gitattrs.ActionConflict:
		return []DriftFinding{{
			File: path,
			Line: 1,
			Message: fmt.Sprintf(
				"a .gitattributes rule for %s already sets eol=%s — left untouched (SPEC-140 D10)",
				decision.Pattern, decision.Value),
		}}, nil
	default: // gitattrs.ActionWrite
		existing, err := readFileOrEmptyString(path)
		if err != nil {
			return nil, fmt.Errorf("service: gitattributes: read %s: %w", path, err)
		}
		result := gitattrs.Upsert(existing)
		if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
			return nil, fmt.Errorf("service: gitattributes: write %s: %w", path, err)
		}
		return []DriftFinding{{
			File: path,
			Line: 1,
			Message: "wrote .gitattributes: " + strings.Join(gitattrsBlockLines(), " / "),
		}}, nil
	}
}

// parseCheckAttrEOL extracts the attribute value from a single line of
// `git check-attr eol -- <path>` output, shaped "<path>: eol: <value>".
// Returns "unspecified" for any line that does not parse — the safe
// default under gitattrs.Decide, since "unspecified" never blocks a write.
func parseCheckAttrEOL(out, probePath string) string {
	prefix := probePath + ": eol: "
	line := strings.TrimSpace(out)
	if !strings.HasPrefix(line, prefix) {
		return "unspecified"
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix))
}

// readFileOrEmptyString reads path, returning "" (not an error) when the
// file does not exist yet.
func readFileOrEmptyString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// gitattrsBlockLines returns gitattrs.Block()'s non-empty lines — used only
// to compose the human-readable "wrote .gitattributes: ..." finding text
// (D11), never to reconstruct the block itself.
func gitattrsBlockLines() []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(gitattrs.Block(), "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
