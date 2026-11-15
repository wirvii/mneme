package enforcement

import (
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wirvii/mneme/internal/risk"
)

// Reasons a risk verdict is logged with (SPEC-185 D8).
const (
	// RiskDenied is a write the risk guard rejected: nobody declared the item.
	RiskDenied = "denied_risk"
	// RiskAllowedByDeclaration is a write the guard let through because an
	// open standard spec declares the item.
	RiskAllowedByDeclaration = "allowed_by_declaration"
)

// Details of the test-watch signals, as logged and as EvaluateRisk reports
// them.
const (
	riskDetailLosesTests = "loses all tests"
	riskDetailRemoved    = "removed"
	riskDetailTruncated  = "truncated"
)

// Synthetic manifests a dependency command rewrites without naming them.
const (
	goManifest   = "go.mod"
	nodeManifest = "package.json"
)

// RiskSubject says who is writing, as the caller resolved it. Only the
// caller knows the archetype and how the write passed the delegation step,
// so it arrives resolved and the decision stays pure.
type RiskSubject struct {
	// Subagent is a resolved subagent archetype of any kind.
	Subagent bool
	// Implementer is archetype backend, frontend or bug-hunter.
	Implementer bool
	// Orchestrator is the main agent; its whitelisted writes never reach
	// EvaluateRisk.
	Orchestrator bool
	// Inline is an orchestrator write that passed through the trivial inline
	// grant: a trivial spec cannot declare any risk, so nothing is declarable.
	Inline bool
}

// RiskFacts is one write the tool call performs, with everything the caller
// could learn about it. Items are the path-detectable risk items whose
// patterns match Rel, with Implicit merged in when a dependency command
// carries item 5 (one item, never twice). Test is nil when Rel is not a test
// file.
type RiskFacts struct {
	// Tool is the tool name (Write, Edit, Bash...).
	Tool string
	// Form is how the write is done: the tool for a file tool, the redirection
	// operator or the program for Bash.
	Form string
	// Target is the path as the agent wrote it.
	Target string
	// Rel is Target relative to the working tree, slash-separated.
	Rel string
	// Items are the risk items the write touches.
	Items []risk.Item
	// Synthetic says Rel is not a path the agent wrote but the manifest a
	// dependency command rewrites (go.mod, package.json), or "" for pip,
	// cargo and poetry. The spec's scope never bounds a synthetic fact.
	Synthetic bool
	// Test carries the test-watch signals when Rel is a test file.
	Test *TestWatch
}

// TestWatch holds the text signals of the test watch (D3) for one write on a
// test file.
type TestWatch struct {
	// Language is the file's test convention.
	Language TestLanguage
	// MarkersAdded are the skip or focus markers whose count grows.
	MarkersAdded []string
	// LosesTests is true when the file had a test function before and has
	// none after.
	LosesTests bool
	// Removed is true for rm, git rm (not --cached), rmdir, mv to a non-test
	// file or out of the tree, and a truncating redirect: the decision signal.
	Removed bool
	// Truncated says the removal is an emptying redirect, which only changes
	// the reported detail.
	Truncated bool
}

// DeclaredFunc answers whether an open standard spec declares item for the
// write at rel. synthetic tells the source that the scope must not bound it.
// spec and source (plan, diseno or scope) name the declaration that matched.
type DeclaredFunc func(item risk.Item, rel string, synthetic bool) (ok bool, spec, source string)

// RiskVerdict is the judgement of one risk item for one write.
type RiskVerdict struct {
	// Block is true when the write must be rejected.
	Block bool
	// Item is the risk item judged.
	Item risk.Item
	// Reason is RiskDenied or RiskAllowedByDeclaration.
	Reason string
	// Detail is the marker, "loses all tests", "removed", "truncated", or the
	// form of the write.
	Detail string
	// Spec is the declaring spec, when there is one.
	Spec string
	// Source is where the declaration was found: plan, diseno or scope.
	Source string
}

// EvaluateRisk applies the decision table of SPEC-185 §4 to one write and
// returns every item it judged, in order, stopping at the first block. Pure:
// items, contents and declarations arrive resolved. Path items are judged in
// ascending order, then item 6 when the write harms a test; under the inline
// grant any match blocks, because a trivial spec declares nothing. A write
// with no item and no test signal yields no verdict at all.
func EvaluateRisk(s RiskSubject, f RiskFacts, declared DeclaredFunc) []RiskVerdict {
	var out []RiskVerdict
	seen := map[risk.Item]bool{}
	for _, item := range sortedItems(f.Items) {
		if seen[item] {
			continue
		}
		seen[item] = true
		v := judgeItem(s, item, f.Form, f, declared)
		out = append(out, v)
		if v.Block {
			return out
		}
	}
	if detail, harmed := testSignal(f.Test); harmed {
		out = append(out, judgeItem(s, risk.ItemUntestable, detail, f, declared))
	}
	return out
}

// judgeItem decides one item.
func judgeItem(s RiskSubject, item risk.Item, detail string, f RiskFacts, declared DeclaredFunc) RiskVerdict {
	if !s.Inline && declared != nil {
		if ok, spec, source := declared(item, f.Rel, f.Synthetic); ok {
			return RiskVerdict{Item: item, Reason: RiskAllowedByDeclaration, Detail: detail, Spec: spec, Source: source}
		}
	}
	return RiskVerdict{Block: true, Item: item, Reason: RiskDenied, Detail: detail}
}

// testSignal reports the strongest test-watch signal: removal first, then an
// added marker, then a file left without tests.
func testSignal(w *TestWatch) (string, bool) {
	switch {
	case w == nil:
		return "", false
	case w.Removed && w.Truncated:
		return riskDetailTruncated, true
	case w.Removed:
		return riskDetailRemoved, true
	case len(w.MarkersAdded) > 0:
		return w.MarkersAdded[0], true
	case w.LosesTests:
		return riskDetailLosesTests, true
	}
	return "", false
}

// sortedItems returns items in ascending order without touching the input.
func sortedItems(items []risk.Item) []risk.Item {
	out := append([]risk.Item(nil), items...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// IsRiskExempt reports whether the risk guard leaves a write on target
// unjudged. For the orchestrator that is the whole whitelist: those writes
// never reach the delegation bridge and are not code. A subagent only skips
// scratch space (/tmp, /private/tmp and, on Windows, pc.TempDir) and the
// data-directory roots, which layer A already guards: the rest of the
// whitelist (.claude/, CLAUDE.md, docs) would exempt a subagent working in a
// .claude/worktrees checkout from the whole layer. A relative target is never
// scratch. Synthetic facts are not passed here; they have no real target.
func IsRiskExempt(target string, s RiskSubject, pc PathContext, data DataDirContext) bool {
	if !s.Subagent {
		return IsWhitelisted(target, pc)
	}
	return isScratchPath(target, pc) || IsDataDirPath(target, data)
}

// isScratchPath reports an absolute target under the OS scratch space.
func isScratchPath(target string, pc PathContext) bool {
	p := strings.Trim(ExpandHome(target, pc), `"'`)
	windowsAbs := pc.isWindows() && hasWindowsDrivePrefix(p)
	if !strings.HasPrefix(p, "/") && !windowsAbs {
		return false
	}
	p = path.Clean(p)
	for _, dir := range []string{"/tmp", "/private/tmp"} {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return pc.isWindows() && isUnderScratchDir(p, pc.TempDir)
}

// RiskWriteForm is one write a Bash command performs.
type RiskWriteForm struct {
	// Form is the redirection operator or the program ("rm", "git rm",
	// "npm install"...).
	Form string
	// Operand is the path written, or the rewritten manifest for a dependency
	// command, or "".
	Operand string
	// Synthetic says Operand was not written by the agent (dependency
	// command).
	Synthetic bool
	// Removes says the operand is deleted (see TestWatch.Removed).
	Removes bool
	// Truncates is ">" or ">|": the file is emptied.
	Truncates bool
	// Script is the sed -i or perl -i script text.
	Script string
	// Implicit is risk.ItemDelivery for a dependency command, 0 otherwise.
	Implicit risk.Item
	// MovedTo is, for the source of an mv, where it goes: the destination,
	// with the source's name joined when the destination is a known
	// directory (trailing "/" or -t). The adapter refines Removes with the
	// working directory.
	MovedTo string
}

// RiskWriteForms lists what a Bash command writes over shell.SimpleCommands
// and shell.Redirects, one level into bash -c, behind the wrappers of
// skipWrappers: one form per written operand, plus one per command that adds,
// removes or updates a named dependency. A parse error yields nil.
func RiskWriteForms(command string) []RiskWriteForm {
	var out []RiskWriteForm
	walkWriteForms(command, 0, nil,
		func(op, target string) bool {
			bare := strings.TrimLeft(op, fdDigits)
			out = append(out, RiskWriteForm{
				Form:      op,
				Operand:   target,
				Truncates: bare == redirOut || bare == redirClobber || bare == redirBoth,
			})
			return false
		},
		func(prog string, args []string) bool {
			if dep, ok := dependencyForm(prog, args); ok {
				out = append(out, dep)
				return false
			}
			if w, ok := writeOf(prog, args); ok {
				out = append(out, formsOf(w)...)
			}
			return false
		},
	)
	return out
}

// formsOf splits one sddWrite into a form per operand.
func formsOf(w sddWrite) []RiskWriteForm {
	sources := map[string]bool{}
	for _, s := range w.sources {
		sources[s] = true
	}
	var out []RiskWriteForm
	for _, op := range w.operands {
		f := RiskWriteForm{Form: w.form, Operand: op, Removes: w.removes, Script: w.script}
		if sources[op] {
			f.MovedTo = movedTo(op, w.dest)
			f.Removes = moveLeavesTree(f.MovedTo) || !isTestPath(f.MovedTo)
		}
		out = append(out, f)
	}
	return out
}

// movedTo joins the source's name to a destination known to be a directory.
func movedTo(source, dest string) string {
	if strings.HasSuffix(dest, "/") || strings.HasSuffix(dest, `\`) {
		return dest + path.Base(strings.ReplaceAll(source, `\`, "/"))
	}
	return dest
}

// isTestPath applies TestFile to a destination written by hand.
func isTestPath(p string) bool {
	_, ok := TestFile(strings.ReplaceAll(p, `\`, "/"))
	return ok
}

// moveLeavesTree reports, from its text alone, whether an mv destination
// takes the file out of the working tree: it is absolute or starts at the
// home directory (~, $HOME, ${HOME}, and on Windows $USERPROFILE), or it is
// relative and climbs out with "..". A relative destination inside the tree
// returns false; the hook's adapter refines it with the real working
// directory.
func moveLeavesTree(dest string) bool {
	homes := []string{"/", "~", "$HOME", "${HOME}"}
	if runtime.GOOS == "windows" {
		homes = append(homes, "$USERPROFILE", "${USERPROFILE}")
	}
	for _, h := range homes {
		if strings.HasPrefix(dest, h) {
			return true
		}
	}
	if filepath.IsAbs(dest) || filepath.VolumeName(dest) != "" {
		return true
	}
	clean := path.Clean(strings.ReplaceAll(dest, `\`, "/"))
	return clean == ".." || strings.HasPrefix(clean, "../")
}

// dependencyTool is how one package manager is read: the flags whose value is
// not a package, and the manifest it rewrites.
type dependencyTool struct {
	spec     argSpec
	manifest string
	// global marks managers whose -g/--global installs a tool, not a project
	// dependency.
	global bool
}

// Subcommand families of the dependency rule (Dp6, E8).
var (
	// depNamedSubs change the project's dependencies only when they name at
	// least one package.
	depNamedSubs = map[string]bool{"install": true, "i": true, "add": true, "remove": true, "uninstall": true}
	// depAlwaysSubs always rewrite the manifest or the lockfile.
	depAlwaysSubs = map[string]bool{"update": true, "upgrade": true}
	// cargoPoetryNamedSubs are the named forms of cargo and poetry, whose
	// install only provisions tools or the locked environment.
	cargoPoetryNamedSubs = map[string]bool{"add": true, "remove": true}
)

// dependencyTools are the package managers the dependency rule recognises.
var dependencyTools = map[string]dependencyTool{
	"npm":    {spec: argSpec{longValue: map[string]bool{"--prefix": true, "--registry": true, "--workspace": true, "--omit": true}, shortValue: "w"}, manifest: nodeManifest, global: true},
	"pnpm":   {spec: argSpec{longValue: map[string]bool{"--dir": true, "--registry": true, "--filter": true}, shortValue: "CF"}, manifest: nodeManifest, global: true},
	"yarn":   {spec: argSpec{longValue: map[string]bool{"--cwd": true, "--registry": true}}, manifest: nodeManifest, global: true},
	"bun":    {spec: argSpec{longValue: map[string]bool{"--cwd": true, "--filter": true}}, manifest: nodeManifest, global: true},
	"pip":    {spec: pipArgSpec},
	"pip3":   {spec: pipArgSpec},
	"cargo":  {spec: argSpec{longValue: map[string]bool{"--registry": true, "--manifest-path": true}}},
	"poetry": {spec: argSpec{longValue: map[string]bool{"--source": true, "--directory": true}, shortValue: "C"}},
}

// pipArgSpec lists pip's flags whose value is a file, an index or a
// directory, never a package.
var pipArgSpec = argSpec{
	longValue: map[string]bool{
		"--editable": true, "--requirement": true, "--constraint": true, "--index-url": true,
		"--extra-index-url": true, "--target": true, "--prefix": true, "--root": true,
		"--no-binary": true, "--only-binary": true,
	},
	shortValue: "ercit",
}

// goModEditFlags are the go mod edit flags that change a requirement; they are
// matched as a prefix of the raw word because parseArgs would read
// -require=m@v1 as a group of short flags.
var goModEditFlags = []string{"-require", "-droprequire", "-replace"}

// dependencyForm recognises the commands that add, remove or update a named
// dependency, which rewrite a manifest and a lockfile without naming them
// (Dp6). Installing what the lockfile already pins (npm ci, npm install with
// no package, pip install -r, go mod tidy|download|verify) is not one: it is
// the routine preparation before running tests. args excludes the program.
// A flag with a value missing from the program's argSpec may have its value
// read as a package; the accepted remedy is declaring item 5.
func dependencyForm(prog string, args []string) (RiskWriteForm, bool) {
	if isGoBinary(prog) {
		return goDependencyForm(args)
	}
	tool, ok := dependencyTools[prog]
	if !ok {
		return RiskWriteForm{}, false
	}
	p := parseArgs(args, tool.spec)
	if tool.global && (p.flags["-g"] || p.flags["--global"]) {
		return RiskWriteForm{}, false
	}
	ops := p.operands
	if prog == "yarn" && len(ops) > 0 && ops[0] == "workspace" {
		ops = ops[min(len(ops), 2):]
	}
	if len(ops) == 0 {
		return RiskWriteForm{}, false
	}
	sub, pkgs := ops[0], ops[1:]
	named := depNamedSubs
	if prog == "cargo" || prog == "poetry" {
		named = cargoPoetryNamedSubs
	}
	switch {
	case depAlwaysSubs[sub]:
	case named[sub] && namesPackage(pkgs):
	default:
		return RiskWriteForm{}, false
	}
	return RiskWriteForm{
		Form:      prog + " " + sub,
		Operand:   tool.manifest,
		Synthetic: true,
		Implicit:  risk.ItemDelivery,
	}, true
}

// namesPackage reports whether any operand is a package rather than the
// current directory or a stray flag.
func namesPackage(ops []string) bool {
	for _, o := range ops {
		if o != "." && !strings.HasPrefix(o, "-") {
			return true
		}
	}
	return false
}

// goDependencyForm recognises `go get <module>` and the requirement-changing
// forms of `go mod edit`. go mod tidy|download|verify and go build|test|vet
// never count.
func goDependencyForm(args []string) (RiskWriteForm, bool) {
	ops := parseArgs(args, argSpec{}).operands
	form := ""
	switch {
	case len(ops) >= 2 && ops[0] == "get":
		form = "go get"
	case len(ops) >= 2 && ops[0] == "mod" && ops[1] == "edit" && editsRequirement(args):
		form = "go mod edit"
	default:
		return RiskWriteForm{}, false
	}
	return RiskWriteForm{Form: form, Operand: goManifest, Synthetic: true, Implicit: risk.ItemDelivery}, true
}

// editsRequirement reports a raw word starting with a requirement-changing
// go mod edit flag.
func editsRequirement(args []string) bool {
	for _, a := range args {
		for _, f := range goModEditFlags {
			if strings.HasPrefix(a, f) {
				return true
			}
		}
	}
	return false
}
