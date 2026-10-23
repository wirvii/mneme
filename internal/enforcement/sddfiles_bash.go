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
	return SDDWriteDecision{}
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
	return isSDDPath(operand, c, exactSegment)
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
	for _, op := range w.roots {
		if namesMnemeDir(op, c) {
			return SDDWriteDecision{Block: true, Target: op, Form: w.form}
		}
	}
	return SDDWriteDecision{}
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
		return operandsWrite("rm", args, argSpec{}), true
	case "truncate":
		return operandsWrite("truncate", args, argSpec{
			longValue:  map[string]bool{"--size": true, "--reference": true},
			shortValue: "sr",
		}), true
	}
	return sddWrite{}, false
}

// operandsWrite treats every operand as written.
func operandsWrite(form string, args []string, spec argSpec) sddWrite {
	return sddWrite{form: form, operands: parseArgs(args, spec).operands}
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
	}
	return w
}
