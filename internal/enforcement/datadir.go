package enforcement

import (
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/shell"
)

// minChdirCommands is the fewest simple commands a level needs for rule (c): a
// cd alone changes nothing, a cd followed by anything else acts inside the root.
const minChdirCommands = 2

// DataDirContext is the per-call context of the data-directory guard (SPEC-185
// layer A): the path facts the .mneme/sdd guard already uses, plus the roots
// under which mneme keeps its own state.
//
// The roots are the data directory (databases and the hook's log), the workflow
// directory (every tarea.md) and the folder of config.toml. A subagent changes
// what lives there only through mneme's MCP tools, which validate and record
// who writes; a hand edit there would let it rewrite the very declarations and
// specs the other guards trust.
type DataDirContext struct {
	// SDDContext supplies PathContext, CWD and Canonical; HasSDDDir is unused.
	SDDContext

	// Roots are absolute, slash-separated and deduplicated: no root lies under
	// another one. Build them with NewDataDirContext.
	Roots []string
}

// NewDataDirContext normalises rawRoots the way IsDataDirPath normalises a
// path, so both sides of the comparison are spelled alike. Empty roots are
// dropped, a leading ~ or $HOME is expanded with base.Home, roots that are
// still relative afterwards are dropped (they could only be matched by text,
// against whatever directory the agent happens to be in), symlinks are resolved
// through base.Canonical, and a root that equals or lies under another one is
// dropped. Comparison ignores case on every host, as the .mneme/sdd guard does.
func NewDataDirContext(base SDDContext, rawRoots []string) DataDirContext {
	plain := SDDContext{PathContext: base.PathContext, Canonical: base.Canonical}
	var candidates []string
	for _, raw := range rawRoots {
		clean, abs := plain.normalizeSDD(raw)
		if clean == "" || !abs {
			continue
		}
		candidates = append(candidates, clean)
		if base.Canonical != nil {
			if resolved := plain.canonicalSDD(clean); resolved != "" && resolved != clean {
				candidates = append(candidates, resolved)
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return len(candidates[i]) < len(candidates[j]) })
	var roots []string
	for _, cand := range candidates {
		covered := false
		for _, kept := range roots {
			if underRoot(cand, kept) {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, cand)
		}
	}
	return DataDirContext{SDDContext: base, Roots: roots}
}

// ExpandHome rewrites a leading ~, $HOME or ${HOME} (and, on a Windows host,
// $USERPROFILE or ${USERPROFILE}) into pc.Home, unifying Windows separators
// first. It is exported so the hook's adapter expands a path exactly as the
// data-directory guard does; with an empty Home the path is returned as written.
func ExpandHome(p string, pc PathContext) string {
	c := SDDContext{PathContext: pc}
	return c.expandSDDHome(c.unifySDDSeparators(p))
}

// IsDataDirPath reports whether p, normalised like IsSDDPath (quotes, Windows
// separators, ~ and $HOME, the CWD join, path.Clean), equals one of c.Roots or
// lies under it. The comparison ignores case on every host, and when
// c.Canonical is set the symlink-resolved form is judged as well.
func IsDataDirPath(p string, c DataDirContext) bool {
	if len(c.Roots) == 0 {
		return false
	}
	clean, abs := c.normalizeSDD(p)
	if clean == "" || !abs {
		return false
	}
	if c.underAnyRoot(clean) {
		return true
	}
	if c.Canonical != nil {
		return c.underAnyRoot(c.canonicalSDD(clean))
	}
	return false
}

// underAnyRoot reports whether the clean absolute path is a root or lies under
// one.
func (c DataDirContext) underAnyRoot(clean string) bool {
	for _, root := range c.Roots {
		if underRoot(clean, root) {
			return true
		}
	}
	return false
}

// underRoot compares two clean slash paths without regard to case.
func underRoot(p, root string) bool {
	if strings.EqualFold(p, root) {
		return true
	}
	prefix := strings.ToLower(strings.TrimSuffix(root, "/")) + "/"
	return strings.HasPrefix(strings.ToLower(p), prefix)
}

// EvaluateDataDirFileTool rejects a file tool (Write, Edit, MultiEdit,
// NotebookEdit) whose target lies under a data-directory root. An empty
// filePath is allowed. The decision's Form is the tool name.
func EvaluateDataDirFileTool(tool, filePath string, c DataDirContext) SDDWriteDecision {
	if filePath == "" || !IsDataDirPath(filePath, c) {
		return SDDWriteDecision{}
	}
	return SDDWriteDecision{Block: true, Target: filePath, Form: tool}
}

// EvaluateDataDirApplyPatch rejects an apply_patch patch that names a path
// under a data-directory root in any of its file headers, the same headers the
// .mneme/sdd guard reads.
func EvaluateDataDirApplyPatch(patch string, c DataDirContext) SDDWriteDecision {
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		for _, header := range applyPatchHeaders {
			target, ok := strings.CutPrefix(line, header)
			if !ok {
				continue
			}
			target = strings.TrimSpace(target)
			if target != "" && IsDataDirPath(target, c) {
				return SDDWriteDecision{Block: true, Target: target, Form: applyPatchTool}
			}
		}
	}
	return SDDWriteDecision{}
}

// EvaluateDataDirBash rejects any simple command, except mneme itself, that
// names a path under a root as an operand or write-redirect target, and a
// cd/pushd into a root followed by any other command. Programs are not
// classified as readers or writers: sqlite3, python, cp and cat are treated
// alike, because a subagent reads these files with the Read tool, never Bash.
//
// The rules, one level into `bash -c`: (a) a write redirection whose target is
// under a root (Form = the operator); (b) any argument under a root (Form = the
// program), unless the program is mneme, `go run` of mneme's main package, or
// cd/pushd; (c) cd or pushd into a root when the same level runs any other
// command (Form = cd or pushd). The mneme exemption does not reach rule (c):
// the guard does not follow the working directory between commands, and mneme
// never needs that cd. A Codex here-document patch is read as a patch first. A
// command that does not parse is allowed.
func EvaluateDataDirBash(command string, c DataDirContext) SDDWriteDecision {
	if strings.Contains(command, patchBeginMarker) || strings.Contains(command, applyPatchTool) {
		if d := EvaluateDataDirApplyPatch(command, c); d.Block {
			return d
		}
	}
	var decision SDDWriteDecision
	walkWriteForms(command, 0,
		func(level string, _ int) bool {
			decision = dataDirChdir(level, c)
			return decision.Block
		},
		func(op, target string) bool {
			if IsDataDirPath(target, c) {
				decision = SDDWriteDecision{Block: true, Target: target, Form: op}
			}
			return decision.Block
		},
		func(prog string, args []string) bool {
			decision = dataDirArgs(prog, args, c)
			return decision.Block
		},
	)
	return decision
}

// dataDirChdir applies rule (c) to one level of the command line.
func dataDirChdir(level string, c DataDirContext) SDDWriteDecision {
	cmds, err := shell.SimpleCommands(level)
	if err != nil || len(cmds) < minChdirCommands {
		return SDDWriteDecision{}
	}
	for _, sc := range cmds {
		args, _ := skipWrappers(sc.Args, nil)
		if len(args) == 0 {
			continue
		}
		prog := programName(args[0])
		if !isChdir(prog) {
			continue
		}
		for _, operand := range parseArgs(args[1:], argSpec{}).operands {
			if IsDataDirPath(operand, c) {
				return SDDWriteDecision{Block: true, Target: operand, Form: prog}
			}
		}
	}
	return SDDWriteDecision{}
}

// dataDirArgs applies rule (b) to one simple command.
func dataDirArgs(prog string, args []string, c DataDirContext) SDDWriteDecision {
	if isChdir(prog) || isMnemeBinary(prog) || isGoRunOfMneme(prog, args) {
		return SDDWriteDecision{}
	}
	for _, a := range args {
		if IsDataDirPath(a, c) {
			return SDDWriteDecision{Block: true, Target: a, Form: prog}
		}
	}
	return SDDWriteDecision{}
}

// isChdir reports the two builtins that change the working directory.
func isChdir(prog string) bool {
	return prog == "cd" || prog == "pushd"
}

// isGoRunOfMneme reports `go run [flags] <mneme main package> ...`.
func isGoRunOfMneme(prog string, args []string) bool {
	if !isGoBinary(prog) || len(args) == 0 || args[0] != goRunVerb {
		return false
	}
	for _, a := range args[1:] {
		if isMnemeMainPackage(a) {
			return true
		}
	}
	return false
}
