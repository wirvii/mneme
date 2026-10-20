package enforcement

import (
	"path"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/shell"
)

// lifecycleCommands is the closed set of `mneme <noun> <verb>` pairs a
// subagent must not run from the terminal (SPEC-175): the author of a change
// does not authorize it. It mirrors the lifecycleTools MCP denials in
// internal/cli/hook.go and adds `sdd import`, which has no MCP surface.
var lifecycleCommands = []string{
	"backlog archive",
	"lane override",
	"sdd import",
	"spec advance",
	"spec quick",
}

const (
	// mnemeBinary is the program name, compared without case so that a
	// Windows "MNEME.EXE" is recognised from any host.
	mnemeBinary    = "mneme"
	mnemeBinaryExe = "mneme.exe"

	// mnemeMainPackage is the import-path tail of mneme's main package, the
	// one `go run` must name for the command to be mneme.
	mnemeMainPackage = "cmd/mneme"

	// lifecycleCommandWords is how many leading positional arguments name a
	// lifecycle command ("spec" then "advance").
	lifecycleCommandWords = 2

	// goRunVerb is the go subcommand that executes a main package.
	goRunVerb = "run"

	dataDirFlag = "--data-dir"
	helpFlag    = "--help"
	helpShort   = "-h"
	argsEnd     = "--"

	shellDashC = "-c"

	envHome        = "HOME"
	envUserProfile = "USERPROFILE"
	envDataDir     = "MNEME_DATA_DIR"

	// mnemeDirName is the data directory under the user's home.
	mnemeDirName = ".mneme"
)

// globalFlagsWithValue are mneme's persistent root flags that take a value as
// the next argument when written without "=" (internal/cli/root.go). They may
// appear anywhere cobra accepts flags, so the verb scan must skip their value.
var globalFlagsWithValue = map[string]bool{
	dataDirFlag:   true,
	"--project":   true,
	"-p":          true,
	"--log-level": true,
}

// wrapperSkipsDuration marks the wrappers that take a leading duration
// argument after their flags.
var wrapperSkipsDuration = map[string]bool{"timeout": true}

// flagOnlyWrappers are launchers that run the next word as a program and take
// only flags of their own. `env` is handled apart because it also takes
// NAME=VALUE words; `timeout` because it takes a duration.
var flagOnlyWrappers = map[string]bool{"command": true, "exec": true, "nohup": true}

// realHomeLiterals are the spellings of the user's own home directory that do
// not isolate anything, whatever the host.
var realHomeLiterals = []string{"~", "$HOME", "${HOME}", "$USERPROFILE", "${USERPROFILE}"}

// LifecycleCommandDecision reports whether a Bash command runs a mneme
// lifecycle verb against the real data directory.
type LifecycleCommandDecision struct {
	// Block is true when the command must be rejected for a subagent.
	Block bool
	// Command is the recognised pair, for example "spec advance"; empty when
	// Block is false.
	Command string
}

// LifecycleCommands returns the sorted set of mneme terminal commands that
// belong to the orchestrator. The slice is a copy; callers may keep it.
func LifecycleCommands() []string {
	out := append([]string(nil), lifecycleCommands...)
	sort.Strings(out)
	return out
}

// EvaluateLifecycleCommand reports whether command, a Bash command line, runs
// one of LifecycleCommands() against the real data directory. It is a
// heuristic that stops a subagent's mistake, not a sandbox: disguised
// invocations (eval, a variable holding the binary, xargs) are not seen.
//
// A command that points mneme at another data directory in the same simple
// command (--data-dir, or a HOME, USERPROFILE or MNEME_DATA_DIR prefix) is
// allowed so mneme itself can be tested; an empty value or one naming the real
// directory does not count as isolation. A command that does not parse fails
// open, like EvaluateBash. Windows semantics come from pc.GOOS, never from the
// host.
func EvaluateLifecycleCommand(command string, pc PathContext) LifecycleCommandDecision {
	return evaluateLifecycle(command, nil, pc, 0)
}

// evaluateLifecycle walks every simple command in command. inherited carries
// the prefix assignments of an enclosing `bash -c`, which the child shell
// inherits.
func evaluateLifecycle(command string, inherited []shell.Assignment, pc PathContext, depth int) LifecycleCommandDecision {
	cmds, err := shell.SimpleCommands(command)
	if err != nil {
		return LifecycleCommandDecision{}
	}
	for _, sc := range cmds {
		if d := evaluateSimpleLifecycle(sc, inherited, pc, depth); d.Block {
			return d
		}
	}
	return LifecycleCommandDecision{}
}

// evaluateSimpleLifecycle decides one simple command: it peels launcher
// wrappers, identifies the program, and either recurses into `bash -c` or
// checks mneme's argv.
func evaluateSimpleLifecycle(sc shell.SimpleCommand, inherited []shell.Assignment, pc PathContext, depth int) LifecycleCommandDecision {
	assigns := append(append([]shell.Assignment(nil), inherited...), sc.Assigns...)
	args, assigns := skipWrappers(sc.Args, assigns)
	if len(args) == 0 {
		return LifecycleCommandDecision{}
	}

	prog := baseName(args[0])
	switch {
	case isMnemeBinary(prog):
		return checkMnemeArgv(args[1:], assigns, pc)
	case isGoBinary(prog):
		return checkGoRun(args[1:], assigns, pc)
	case isShellBinary(prog):
		if depth >= maxBashRecursionDepth || len(args) < lifecycleCommandWords+1 || args[1] != shellDashC {
			return LifecycleCommandDecision{}
		}
		return evaluateLifecycle(args[2], assigns, pc, depth+1)
	}
	return LifecycleCommandDecision{}
}

// skipWrappers removes leading launcher words (env, command, exec, nohup,
// timeout) and the NAME=VALUE words of env, returning the remaining argv and
// the assignments extended with the env ones. Only this closed set is
// recognised; any other wrapper is an accepted limit.
func skipWrappers(args []string, assigns []shell.Assignment) ([]string, []shell.Assignment) {
	for len(args) > 0 {
		name := baseName(args[0])
		switch {
		case name == "env":
			args = args[1:]
			for len(args) > 0 {
				if strings.HasPrefix(args[0], "-") {
					args = args[1:]
					continue
				}
				a, ok := parseAssignWord(args[0])
				if !ok {
					break
				}
				assigns = append(assigns, a)
				args = args[1:]
			}
		case flagOnlyWrappers[name], wrapperSkipsDuration[name]:
			args = skipFlags(args[1:])
			if wrapperSkipsDuration[name] && len(args) > 0 {
				args = args[1:]
			}
		default:
			return args, assigns
		}
	}
	return args, assigns
}

// skipFlags drops leading words that start with "-".
func skipFlags(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		args = args[1:]
	}
	return args
}

// parseAssignWord splits an `env` NAME=VALUE word.
func parseAssignWord(word string) (shell.Assignment, bool) {
	name, value, found := strings.Cut(word, "=")
	if !found || name == "" || strings.ContainsAny(name, "/\\\"'$ ") {
		return shell.Assignment{}, false
	}
	return shell.Assignment{Name: name, Value: value}, true
}

// checkGoRun recognises `go run [flags] <mneme main package> args...`.
func checkGoRun(args []string, assigns []shell.Assignment, pc PathContext) LifecycleCommandDecision {
	if len(args) == 0 || args[0] != goRunVerb {
		return LifecycleCommandDecision{}
	}
	for i, a := range args[1:] {
		if isMnemeMainPackage(a) {
			return checkMnemeArgv(args[i+2:], assigns, pc)
		}
	}
	return LifecycleCommandDecision{}
}

// checkMnemeArgv decides mneme's own argv (everything after the program).
func checkMnemeArgv(argv []string, assigns []shell.Assignment, pc PathContext) LifecycleCommandDecision {
	var (
		positionals []string
		dataDirs    []string
	)
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == argsEnd {
			break
		}
		if a == helpFlag || a == helpShort {
			return LifecycleCommandDecision{}
		}
		if !strings.HasPrefix(a, "-") {
			positionals = append(positionals, a)
			continue
		}
		if v, ok := strings.CutPrefix(a, dataDirFlag+"="); ok {
			dataDirs = append(dataDirs, v)
			continue
		}
		if globalFlagsWithValue[a] {
			var v string
			if i+1 < len(argv) {
				v = argv[i+1]
				i++
			}
			if a == dataDirFlag {
				dataDirs = append(dataDirs, v)
			}
		}
	}
	if len(positionals) < lifecycleCommandWords {
		return LifecycleCommandDecision{}
	}
	verb := strings.Join(positionals[:lifecycleCommandWords], " ")
	if !isLifecycleCommand(verb) {
		return LifecycleCommandDecision{}
	}
	if isolatesDataDir(assigns, dataDirs, pc) {
		return LifecycleCommandDecision{}
	}
	return LifecycleCommandDecision{Block: true, Command: verb}
}

// isLifecycleCommand reports an exact, case-sensitive match, as cobra does.
func isLifecycleCommand(verb string) bool {
	for _, c := range lifecycleCommands {
		if c == verb {
			return true
		}
	}
	return false
}

// isolatesDataDir reports whether any signal in the same simple command points
// mneme at a data directory other than the user's real one.
func isolatesDataDir(assigns []shell.Assignment, dataDirs []string, pc PathContext) bool {
	for _, a := range assigns {
		switch a.Name {
		case envHome, envUserProfile:
			if !namesRealHome(a.Value, pc) {
				return true
			}
		case envDataDir:
			if !namesRealDataDir(a.Value, pc) {
				return true
			}
		}
	}
	for _, d := range dataDirs {
		if !namesRealDataDir(d, pc) {
			return true
		}
	}
	return false
}

// namesRealHome reports whether v is empty or spells the user's home. An empty
// pc.Home disables only the comparison with the absolute path, never the one
// with the literals.
func namesRealHome(v string, pc PathContext) bool {
	n := normalizeDir(v, pc)
	if n == "" {
		return true
	}
	for _, lit := range realHomeLiterals {
		if n == normalizeDir(lit, pc) {
			return true
		}
	}
	return pc.Home != "" && n == normalizeDir(pc.Home, pc)
}

// namesRealDataDir reports whether v is empty or spells <home>/.mneme.
func namesRealDataDir(v string, pc PathContext) bool {
	n := normalizeDir(v, pc)
	if n == "" {
		return true
	}
	for _, lit := range realHomeLiterals {
		if n == normalizeDir(lit+"/"+mnemeDirName, pc) {
			return true
		}
	}
	return pc.Home != "" && n == normalizeDir(pc.Home+"/"+mnemeDirName, pc)
}

// normalizeDir trims trailing slashes and, for a Windows host, unifies
// separators and case so "C:\Users\X\" and "c:/users/x" compare equal.
func normalizeDir(v string, pc PathContext) string {
	if pc.isWindows() {
		v = strings.ToLower(strings.ReplaceAll(v, `\`, "/"))
	}
	return strings.TrimRight(v, "/")
}

// baseName cuts at the last "/" or "\" on any host: a Windows path must be
// understood on darwin and a Unix one on Windows (the same reason SPEC-107 does
// not use filepath.Base).
func baseName(p string) string {
	return p[strings.LastIndexAny(p, `/\`)+1:]
}

func isMnemeBinary(base string) bool {
	return strings.EqualFold(base, mnemeBinary) || strings.EqualFold(base, mnemeBinaryExe)
}

func isGoBinary(base string) bool {
	return strings.EqualFold(base, "go") || strings.EqualFold(base, "go.exe")
}

func isShellBinary(base string) bool {
	return base == "bash" || base == "sh" || base == "zsh"
}

// isMnemeMainPackage reports whether a `go run` argument names mneme's main
// package: ./cmd/mneme, cmd/mneme/main.go, or a full import path with an
// optional @version.
func isMnemeMainPackage(arg string) bool {
	p := strings.ReplaceAll(arg, `\`, "/")
	if i := strings.Index(p, "@"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimRight(p, "/")
	if strings.HasSuffix(p, ".go") {
		p = path.Dir(p)
	}
	return p == mnemeMainPackage || strings.HasSuffix(p, "/"+mnemeMainPackage)
}
