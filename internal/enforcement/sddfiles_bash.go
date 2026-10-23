package enforcement

import (
	"path"
	"strings"

	"github.com/wirvii/mneme/internal/shell"
)

// Redirect operators that write (or may write) their target. The input
// operators "<" and "<&" are absent on purpose: reading .mneme/sdd is allowed.
const (
	redirOut       = ">"
	redirAppend    = ">>"
	redirClobber   = ">|"
	redirBoth      = "&>"
	redirBothApp   = "&>>"
	redirReadWrite = "<>"
	redirDupOut    = ">&"
)

const (
	// dashCArgc is the argv length of `bash -c <command>`.
	dashCArgc = 3

	// minCopyOperands is the fewest operands a copy or move needs to have a
	// source and a destination when no target directory is given.
	minCopyOperands = 2
)

// fdDigits are the characters of a file-descriptor prefix ("2" in "2>").
const fdDigits = "0123456789"

// sddWrite is what one simple command writes, as the operands that must not be
// inside .mneme/sdd. A command that only reads yields no sddWrite at all.
type sddWrite struct {
	// form is the label reported to the agent ("sed -i", "rm", ...).
	form string
	// operands are paths the command creates, changes or removes.
	operands []string
	// ancestors are operands that, when they are the .mneme directory or the
	// directory holding .mneme/sdd, take the tree with them (rm -r, mv).
	ancestors []string
	// roots are destinations that must not be the bare ".mneme" directory:
	// copying or moving a directory named sdd into it would create the tree.
	roots []string
}

// argSpec describes the flags of one program: which take a value, so that the
// value is not mistaken for an operand.
type argSpec struct {
	// longValue lists the long flags ("--target-directory") whose value is the
	// next word when written without "=".
	longValue map[string]bool
	// shortValue lists the short flag letters whose value is the rest of the
	// group when there is one ("-tDIR") or else the next word ("-t DIR").
	shortValue string
	// shortAttached lists the short flag letters whose rest of the group is an
	// optional suffix and never consume the next word (sed's "-i.bak").
	shortAttached string
}

// parsedArgs is an argv split into flags and operands.
type parsedArgs struct {
	// flags holds every flag seen, as "-x" for each short letter and "--name"
	// for a long one, without values.
	flags map[string]bool
	// values holds the values of the flags that take one.
	values map[string][]string
	// operands are the words that are not flags nor flag values; after a bare
	// "--" every word is an operand.
	operands []string
}

// parseArgs splits args according to spec. It is a deliberate approximation of
// each tool's real option parser: it only has to find the operands of the
// handful of write commands the guard knows.
func parseArgs(args []string, spec argSpec) parsedArgs {
	p := parsedArgs{flags: map[string]bool{}, values: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == argsEnd:
			p.operands = append(p.operands, args[i+1:]...)
			return p
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a, "=")
			p.flags[name] = true
			if spec.longValue[name] {
				if !hasVal && i+1 < len(args) {
					val = args[i+1]
					i++
				}
				p.values[name] = append(p.values[name], val)
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			i = parseShortGroup(args, i, spec, &p)
		default:
			p.operands = append(p.operands, a)
		}
	}
	return p
}

// parseShortGroup consumes the short-flag group args[i] and returns the index
// of the last word it used.
func parseShortGroup(args []string, i int, spec argSpec, p *parsedArgs) int {
	group := args[i][1:]
	for j := 0; j < len(group); j++ {
		letter := string(group[j])
		flag := "-" + letter
		p.flags[flag] = true
		rest := group[j+1:]
		switch {
		case strings.Contains(spec.shortAttached, letter):
			return i
		case strings.Contains(spec.shortValue, letter):
			if rest != "" {
				p.values[flag] = append(p.values[flag], rest)
				return i
			}
			if i+1 < len(args) {
				p.values[flag] = append(p.values[flag], args[i+1])
				return i + 1
			}
			return i
		}
	}
	return i
}

// EvaluateSDDBash reports whether a Bash command writes inside .mneme/sdd. It
// is a second barrier, not a sandbox: it sees the common ways to write a file
// (redirections, sed -i, tee, cp, mv, rm, truncate and a few more) wherever they
// sit in the command line, and nothing that hides the destination (eval, a
// variable holding the path, xargs, a script in a file).
//
// Reading never blocks, nor do `mneme` and `git` themselves: the binary is the
// one allowed to write the tree, and a pull or checkout only imports what a
// teammate's mneme wrote. A command that does not parse is allowed, like
// EvaluateBash and EvaluateLifecycleCommand.
func EvaluateSDDBash(command string, c SDDContext) SDDWriteDecision {
	return evaluateSDDBash(command, c, 0)
}

func evaluateSDDBash(command string, c SDDContext, depth int) SDDWriteDecision {
	cmds, err := shell.SimpleCommands(command)
	if err != nil {
		return SDDWriteDecision{}
	}
	reds, err := shell.Redirects(command)
	if err != nil {
		return SDDWriteDecision{}
	}
	for _, r := range reds {
		if isWriteRedirect(r.Op) && sddOperand(r.Target, c) {
			return SDDWriteDecision{Block: true, Target: r.Target, Form: r.Op}
		}
	}
	for _, sc := range cmds {
		if d := evaluateSDDSimple(sc, c, depth); d.Block {
			return d
		}
	}
	return evaluateSDDChdir(cmds, command, c, depth)
}

// evaluateSDDChdir rejects `cd .mneme/sdd && echo x > a`: after the cd, every
// operand is relative to the tree and no longer looks like it. The rule is
// deliberately conservative: it does not simulate the working directory between
// commands, it only asks whether the same command line enters the tree (or
// .mneme) and also contains any write form. Reading after a cd is untouched.
func evaluateSDDChdir(cmds []shell.SimpleCommand, command string, c SDDContext, depth int) SDDWriteDecision {
	for _, sc := range cmds {
		args, _ := skipWrappers(sc.Args, nil)
		if len(args) == 0 {
			continue
		}
		prog := programName(args[0])
		if prog != "cd" && prog != "pushd" {
			continue
		}
		for _, operand := range parseArgs(args[1:], argSpec{}).operands {
			if (sddOperand(operand, c) || namesMnemeDir(operand, c)) && hasWriteForm(command, depth) {
				return SDDWriteDecision{Block: true, Target: operand, Form: prog}
			}
		}
	}
	return SDDWriteDecision{}
}

// hasWriteForm reports whether command contains any write form of the table,
// whatever its operands: the cd rule needs to know that something is written,
// not where.
func hasWriteForm(command string, depth int) bool {
	cmds, err := shell.SimpleCommands(command)
	if err != nil {
		return false
	}
	reds, err := shell.Redirects(command)
	if err != nil {
		return false
	}
	for _, r := range reds {
		if isWriteRedirect(r.Op) {
			return true
		}
	}
	for _, sc := range cmds {
		args, _ := skipWrappers(sc.Args, nil)
		if len(args) == 0 {
			continue
		}
		prog := programName(args[0])
		if isShellBinary(prog) {
			if depth < maxBashRecursionDepth && len(args) >= dashCArgc && args[1] == shellDashC && hasWriteForm(args[2], depth+1) {
				return true
			}
			continue
		}
		if w, ok := writeOf(prog, args[1:]); ok && len(w.operands) > 0 {
			return true
		}
	}
	return false
}

// isWriteRedirect reports whether op, with its descriptor prefix, can write.
func isWriteRedirect(op string) bool {
	switch strings.TrimLeft(op, fdDigits) {
	case redirOut, redirAppend, redirClobber, redirBoth, redirBothApp, redirReadWrite, redirDupOut:
		return true
	}
	return false
}

// sddOperand is the single place where a Bash operand is judged, so the rules
// that widen what counts as a protected operand live in one function.
func sddOperand(operand string, c SDDContext) bool {
	return isSDDPath(operand, c, globSegment)
}

// evaluateSDDSimple decides one simple command: it peels launcher wrappers,
// recurses into `bash -c`, and checks the program's write operands.
func evaluateSDDSimple(sc shell.SimpleCommand, c SDDContext, depth int) SDDWriteDecision {
	args, _ := skipWrappers(sc.Args, nil)
	if len(args) == 0 {
		return SDDWriteDecision{}
	}
	prog := programName(args[0])
	if isShellBinary(prog) {
		if depth >= maxBashRecursionDepth || len(args) < dashCArgc || args[1] != shellDashC {
			return SDDWriteDecision{}
		}
		return evaluateSDDBash(args[2], c, depth+1)
	}
	w, ok := writeOf(prog, args[1:])
	if !ok {
		return SDDWriteDecision{}
	}
	return w.check(c)
}

// programName is the lower-cased executable name without a Windows ".exe".
func programName(arg0 string) string {
	return strings.TrimSuffix(strings.ToLower(baseName(arg0)), ".exe")
}

// check judges every operand of w.
func (w sddWrite) check(c SDDContext) SDDWriteDecision {
	for _, op := range w.operands {
		if sddOperand(op, c) {
			return SDDWriteDecision{Block: true, Target: op, Form: w.form}
		}
	}
	for _, op := range w.ancestors {
		if isAncestorOperand(op, c) {
			return SDDWriteDecision{Block: true, Target: op, Form: w.form}
		}
	}
	for _, op := range w.roots {
		if namesMnemeDir(op, c) {
			return SDDWriteDecision{Block: true, Target: op, Form: w.form}
		}
	}
	return SDDWriteDecision{}
}

// isAncestorOperand reports whether removing or moving op takes .mneme/sdd with
// it: op is a directory named .mneme, or the directory that holds .mneme/sdd
// (HasSDDDir, answered by the caller from the real file system). A bare "*" is
// neither: the shell does not expand it to hidden names.
func isAncestorOperand(op string, c SDDContext) bool {
	clean, abs := c.normalizeSDD(op)
	if clean == "" {
		return false
	}
	if globSegment(path.Base(clean), sddRootSegment) {
		return true
	}
	return abs && c.HasSDDDir != nil && c.HasSDDDir(clean)
}

// namesMnemeDir reports whether op is a directory named ".mneme" exactly.
func namesMnemeDir(op string, c SDDContext) bool {
	clean, _ := c.normalizeSDD(op)
	return clean != "" && strings.EqualFold(path.Base(clean), sddRootSegment)
}

// writeOf maps a program and its arguments to what it writes. The second
// result is false for programs that do not write, which is most of them.
func writeOf(prog string, args []string) (sddWrite, bool) {
	switch prog {
	case "sed":
		return sedWrite(args)
	case "tee":
		return operandsWrite("tee", args, argSpec{}), true
	case "cp":
		return copyWrite("cp", args, false), true
	case "mv":
		return copyWrite("mv", args, true), true
	case "rm":
		return rmWrite(args), true
	case "rmdir":
		return rmdirWrite(args), true
	case "touch":
		return operandsWrite("touch", args, argSpec{
			longValue:  map[string]bool{"--date": true, "--reference": true, "--time": true},
			shortValue: "dtr",
		}), true
	case "ln":
		return copyWrite("ln", args, false), true
	case "install":
		return installWrite(args), true
	case "dd":
		return ddWrite(args), true
	case "perl":
		return perlWrite(args)
	case "node", "nodejs":
		return inlineScriptWrite("node -e", args, map[string]bool{"-e": true, "--eval": true}, hasNodeWriteSignal)
	case "truncate":
		return operandsWrite("truncate", args, argSpec{
			longValue:  map[string]bool{"--size": true, "--reference": true},
			shortValue: "sr",
		}), true
	default:
		if strings.HasPrefix(prog, "python") {
			return inlineScriptWrite("python -c", args, map[string]bool{"-c": true}, hasPythonWriteSignal)
		}
	}
	return sddWrite{}, false
}

// operandsWrite treats every operand as written.
func operandsWrite(form string, args []string, spec argSpec) sddWrite {
	return sddWrite{form: form, operands: parseArgs(args, spec).operands}
}

// rmWrite treats every operand as removed; with a recursive flag an operand
// that holds the tree removes it as well.
func rmWrite(args []string) sddWrite {
	p := parseArgs(args, argSpec{})
	w := sddWrite{form: "rm", operands: p.operands}
	if p.flags["-r"] || p.flags["-R"] || p.flags["--recursive"] {
		w.ancestors = p.operands
	}
	return w
}

// rmdirWrite removes directories; like rm -r, an ancestor of the tree takes it
// along.
func rmdirWrite(args []string) sddWrite {
	operands := parseArgs(args, argSpec{}).operands
	return sddWrite{form: "rmdir", operands: operands, ancestors: operands}
}

// installWrite handles install: with -d every operand is a directory to create,
// otherwise it copies like cp.
func installWrite(args []string) sddWrite {
	p := parseArgs(args, argSpec{
		longValue:  map[string]bool{"--target-directory": true, "--suffix": true, "--mode": true, "--owner": true, "--group": true},
		shortValue: "mogtS",
	})
	if p.flags["-d"] || p.flags["--directory"] {
		return sddWrite{form: "install", operands: p.operands}
	}
	return copyWrite("install", args, false)
}

// ddWrite reads the destination from the of=<path> operand.
func ddWrite(args []string) sddWrite {
	w := sddWrite{form: "dd"}
	for _, a := range args {
		if dest, ok := strings.CutPrefix(a, "of="); ok {
			w.operands = append(w.operands, dest)
		}
	}
	return w
}

// perlWrite recognises perl in place (-i, -i<suffix>, a group such as -pi). The
// -e script is not an operand.
func perlWrite(args []string) (sddWrite, bool) {
	p := parseArgs(args, argSpec{shortValue: "eEIMm", shortAttached: "i"})
	if !p.flags["-i"] {
		return sddWrite{}, false
	}
	return sddWrite{form: "perl -i", operands: p.operands}, true
}

// inlineScriptWrite looks into the script of `python -c` or `node -e`. Only a
// script with a write signal counts (a reader that merely names the tree is
// fine), and then the path-shaped words of the script are its operands. This is
// a text heuristic, as in EvaluateBash, not an interpreter.
func inlineScriptWrite(form string, args []string, scriptFlags map[string]bool, hasWriteSignal func(string) bool) (sddWrite, bool) {
	for i, a := range args {
		if !scriptFlags[a] || i+1 >= len(args) {
			continue
		}
		script := args[i+1]
		if !hasWriteSignal(script) {
			return sddWrite{}, false
		}
		return sddWrite{form: form, operands: pathShapedFields(script)}, true
	}
	return sddWrite{}, false
}

// sedWrite recognises sed in place: -i, -i<suffix>, --in-place[=<suffix>] or a
// group such as -Ei. The script is not an operand: without -e or -f the first
// word is the script, which could mention the tree without touching it.
func sedWrite(args []string) (sddWrite, bool) {
	p := parseArgs(args, argSpec{
		longValue:     map[string]bool{"--expression": true, "--file": true, "--line-length": true},
		shortValue:    "efl",
		shortAttached: "i",
	})
	if !p.flags["-i"] && !p.flags["--in-place"] {
		return sddWrite{}, false
	}
	operands := p.operands
	hasScript := p.flags["-e"] || p.flags["-f"] || p.flags["--expression"] || p.flags["--file"]
	if !hasScript && len(operands) > 0 {
		operands = operands[1:]
	}
	return sddWrite{form: "sed -i", operands: operands}, true
}

// copyWrite handles cp and mv. The destination is -t/--target-directory when
// given, else the last operand. cp never changes its sources; mv removes them,
// so they are written too.
func copyWrite(form string, args []string, movesSources bool) sddWrite {
	p := parseArgs(args, argSpec{
		longValue:  map[string]bool{"--target-directory": true, "--suffix": true},
		shortValue: "tS",
	})
	var sources []string
	var dests []string
	switch targets := append(p.values["-t"], p.values["--target-directory"]...); {
	case len(targets) > 0:
		sources, dests = p.operands, targets
	case len(p.operands) >= minCopyOperands:
		sources, dests = p.operands[:len(p.operands)-1], p.operands[len(p.operands)-1:]
	default:
		return sddWrite{form: form}
	}
	w := sddWrite{form: form, operands: dests, roots: dests}
	if movesSources {
		w.operands = append(w.operands, sources...)
		w.ancestors = sources
	}
	return w
}
