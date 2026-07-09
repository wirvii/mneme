// Package enforcement implements the pure decision logic behind mneme's
// orchestrator-guard (Layer 2 of the enforcement model): whether a given
// file-mutating tool invocation or Bash command should be blocked because it
// targets a path outside a small static whitelist and is not otherwise owned
// by a delegated subagent.
//
// This package is a leaf: it imports only the standard library plus
// internal/shell (the tokenizer). It performs no I/O of its own — no
// database access, no config loading, no os.Exit — so every decision is a
// pure function of its inputs and is directly unit-testable. All I/O
// (config/project/manifest lookups, process termination, discovery logging)
// belongs to the caller (internal/cli/hook.go), which injects an
// OwnershipFunc closure over its own in-process manifest lookup.
//
// enforcement.go is a Go port of the bash logic that used to live entirely
// in internal/install/assets/hooks/enforce_delegation.sh (SPEC-032/033/040/
// 042/043/068). SPEC-069 moves that logic in-process so the hook no longer
// needs jq, a bash interpreter, or subprocess round-trips to itself
// (mneme hook tokenize / mneme hook path-owned) to make a decision.
package enforcement

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/juanftp/mneme/internal/shell"
)

// Decision is the result of evaluating a single tool invocation (a file path
// or a Bash command) against the orchestrator-guard's rules.
type Decision struct {
	// Block is true when the invocation must be rejected.
	Block bool

	// Reason is a human-readable explanation of why the invocation was
	// blocked. Empty when Block is false.
	Reason string

	// Owner is the manifest role that owns the blocked path, "legacy" when
	// no subagent manifest exists for the project (deny-by-default), or ""
	// when the block is a hard-block that never resolved to a single target
	// path (e.g. `sed -i` outside the whitelist, or an inline python/node
	// script that merely mentions a protected path).
	Owner string

	// Target is the path that triggered the block, or "" for hard-blocks
	// that have no single resolvable target (mirrors the bash TARGET_PATH
	// sentinel value "unknown", represented here as the empty string —
	// callers that render a message should default it to "unknown").
	Target string
}

// OwnershipFunc consults the project's subagent manifest for a single
// candidate target path that survived the static whitelist check. It
// returns block=true when an implementer subagent (or the manifest itself is
// absent — legacy deny-by-default) owns the path, together with the owning
// role ("legacy" for the manifest-absent case). Callers inject this closure
// so internal/enforcement stays a leaf package with no config/db/project
// dependency of its own (SPEC-069 D2); the real implementation lives in
// internal/cli's resolvePathOwnership (SPEC-068), called in-process.
type OwnershipFunc func(target string) (block bool, owner string)

// watchedCommands is the set of file-manipulating command names whose last
// non-flag argument is treated as a candidate write target (mirrors the bash
// check_bash_go command list).
var watchedCommands = map[string]bool{
	"tee": true, "mv": true, "cp": true, "rm": true, "rmdir": true,
	"touch": true, "chmod": true, "chown": true, "ln": true,
	"install": true, "patch": true, "truncate": true,
}

// maxBashRecursionDepth bounds the recursion into `bash|sh|zsh -c "..."` and
// `$(...)` command substitutions to a single level (mirrors D5 of SPEC-033:
// BASH_C_DEPTH in the original bash implementation).
const maxBashRecursionDepth = 1

// redirectOperatorPrefix strips a leading redirect operator glued to its
// target (e.g. "2>/dev/null", "&>/tmp/x") from a candidate path token
// extracted from an inline python/node script string. Mirrors the bash fix
// for '2>/dev/null' producing false positives (SPEC-042/043 Fix 1).
var redirectOperatorPrefix = regexp.MustCompile(`^[0-9]*&?>{1,2}\|?`)

// quoteAndBracketStripper removes the punctuation the bash implementation
// strips from a path candidate before checking it against the whitelist:
// single/double quotes, commas, and parens/brackets left over from Python or
// JS call syntax (e.g. "('src.go'," -> "src.go").
var quoteAndBracketStripper = strings.NewReplacer(
	"'", "", `"`, "", ",", "", ")", "", "(", "", "]", "", "[", "",
)

// IsWhitelisted reports whether path is inside the small set of locations the
// orchestrator may always write to without delegating or consulting a
// subagent manifest, mirroring is_allowed_path from enforce_delegation.sh
// verbatim:
//
//   - CLAUDE.md (any location, matched by basename)
//   - .claudeignore (any location, matched by basename)
//   - any *.md path under a /docs/ directory
//   - absolute paths containing /.claude/ or /.mneme/
//   - /tmp/** and /private/tmp/** (macOS) scratch space
//   - relative paths under .claude/ or .mneme/
//
// home expands a leading "~/" the same way the bash version used $HOME —
// callers pass os.UserHomeDir() (or "" to disable expansion, in which case a
// "~/"-prefixed path is left as-is and will not match any of the above).
func IsWhitelisted(path, home string) bool {
	path = strings.TrimPrefix(path, "./")
	path = strings.ReplaceAll(path, "'", "")
	path = strings.ReplaceAll(path, `"`, "")

	if home != "" && strings.HasPrefix(path, "~/") {
		path = home + "/" + strings.TrimPrefix(path, "~/")
	}

	basename := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		basename = path[idx+1:]
	}

	if basename == "CLAUDE.md" || basename == ".claudeignore" {
		return true
	}
	if strings.HasSuffix(basename, ".md") && strings.Contains("/"+path, "/docs/") {
		return true
	}

	if strings.HasPrefix(path, "/") {
		if strings.Contains(path, "/.claude/") || strings.Contains(path, "/.mneme/") {
			return true
		}
		if strings.HasPrefix(path, "/tmp/") || strings.HasPrefix(path, "/private/tmp/") {
			return true
		}
		return false
	}

	if path == ".claude" || strings.HasPrefix(path, ".claude/") {
		return true
	}
	if path == ".mneme" || strings.HasPrefix(path, ".mneme/") {
		return true
	}
	return false
}

// evaluateTarget consults own for a single non-whitelisted candidate path and
// converts the result into a Decision. It is the Go equivalent of the bash
// check_target_or_block bridge (minus the "mneme not on PATH" guard, which is
// moot in-process — the caller of this package IS mneme).
func evaluateTarget(target, reason string, own OwnershipFunc) Decision {
	block, owner := own(target)
	if !block {
		return Decision{}
	}
	return Decision{Block: true, Reason: reason, Owner: owner, Target: target}
}

// EvaluateFileTool decides whether a Write/Edit/MultiEdit/NotebookEdit
// invocation targeting filePath should be blocked. Mirrors check_file_tool:
// an empty path or a whitelisted path is always allowed; otherwise own is
// consulted for the single candidate target.
func EvaluateFileTool(filePath, home string, own OwnershipFunc) Decision {
	if filePath == "" {
		return Decision{}
	}
	if IsWhitelisted(filePath, home) {
		return Decision{}
	}
	return evaluateTarget(filePath, fmt.Sprintf("Ruta bloqueada: '%s'", filePath), own)
}

// EvaluateBash decides whether a Bash command should be blocked. It tokenizes
// command with shell.Tokenize and walks the resulting tokens applying the
// same rules as check_bash_go in enforce_delegation.sh: redirect targets,
// watched file-manipulation commands, sed/perl -i, dd of=, inline python/node
// scripts that mention a protected path, and one level of recursion into
// `bash|sh|zsh -c "..."` and `$(...)` command substitutions. Evaluation stops
// at the first offending candidate or hard-block, mirroring the bash
// short-circuit semantics. A tokenizer failure (parse error or empty input)
// fails open (returns an empty Decision), matching check_bash_go's
// "tokenizador no disponible" fallback — shell.Tokenize is always available
// in-process, so this only fires for genuinely unparsable input.
func EvaluateBash(command, home string, own OwnershipFunc) Decision {
	return evaluateBash(command, home, own, 0)
}

func evaluateBash(command, home string, own OwnershipFunc, depth int) Decision {
	if strings.TrimSpace(command) == "" {
		return Decision{}
	}

	tokens, err := shell.Tokenize(command)
	if err != nil || len(tokens) == 0 {
		return Decision{} // fail-open: parse error or empty token stream.
	}

	for i, tok := range tokens {
		switch tok.Type {
		case shell.TypeRedirectTarget:
			if d := evaluateRedirectTarget(tok.Value, home, own); d.Block {
				return d
			}

		case shell.TypeWord:
			if tok.Quoted {
				continue
			}
			if d := evaluateWordToken(tokens, i, tok.Value, command, home, own, depth); d.Block {
				return d
			}

		case shell.TypeCommandSubstitution:
			if depth < maxBashRecursionDepth && tok.Value != "" {
				if d := evaluateBash(tok.Value, home, own, depth+1); d.Block {
					return d
				}
			}

		case shell.TypeHeredocBody, shell.TypeSeparator, shell.TypeRedirect:
			// Heredoc bodies are data, not commands (skip); redirect operator
			// and separator tokens carry no path information by themselves.
		}
	}

	return Decision{}
}

// evaluateRedirectTarget handles a single TypeRedirectTarget token: process
// substitution and /dev/* targets are never candidates; anything else is
// checked against the whitelist and, if not whitelisted, against own.
func evaluateRedirectTarget(value, home string, own OwnershipFunc) Decision {
	if isProcessSubstitution(value) {
		return Decision{}
	}
	if strings.HasPrefix(value, "/dev/") {
		return Decision{}
	}
	if IsWhitelisted(value, home) {
		return Decision{}
	}
	return evaluateTarget(value, fmt.Sprintf("Redirect a ruta protegida: '%s'", value), own)
}

// isProcessSubstitution reports whether a reconstructed redirect-target
// value is a process substitution ("<(...)" or ">(...)") rather than a file
// path — mirrors the bash Fix 4 (SPEC-043) exclusion.
func isProcessSubstitution(value string) bool {
	return strings.HasPrefix(value, "(") || strings.HasPrefix(value, "<(") || strings.HasPrefix(value, ">(")
}

// evaluateWordToken dispatches on an unquoted word token's value, mirroring
// the case statement inside check_bash_go's `word` branch.
func evaluateWordToken(tokens []shell.Token, i int, value, fullCommand, home string, own OwnershipFunc, depth int) Decision {
	switch value {
	case "bash", "sh", "zsh":
		return evaluateShellDashC(tokens, i, home, own, depth)
	case "sed", "perl":
		return evaluateInPlaceEditor(tokens, i, value, fullCommand)
	case "dd":
		return evaluateDD(tokens, i, home, own)
	case "python", "python2", "python3":
		if commandMentionsProtectedPath(fullCommand, home) {
			return Decision{Block: true, Reason: "Script Python inline menciona ruta fuera de whitelist"}
		}
	case "node":
		if commandMentionsProtectedPath(fullCommand, home) {
			return Decision{Block: true, Reason: "Script Node inline menciona ruta fuera de whitelist"}
		}
	default:
		if watchedCommands[value] {
			return evaluateWatchedCommand(tokens, i, value, home, own)
		}
	}
	return Decision{}
}

// evaluateShellDashC recurses one level into `bash|sh|zsh -c "<cmd>"` (D5,
// SPEC-033), guarded by depth so nested -c invocations are not walked
// indefinitely.
func evaluateShellDashC(tokens []shell.Token, i int, home string, own OwnershipFunc, depth int) Decision {
	if depth >= maxBashRecursionDepth {
		return Decision{}
	}
	next, ok := tokenValueAt(tokens, i+1)
	if !ok || next != "-c" {
		return Decision{}
	}
	inner, ok := tokenValueAt(tokens, i+2)
	if !ok || inner == "" {
		return Decision{}
	}
	return evaluateBash(inner, home, own, depth+1)
}

// evaluateInPlaceEditor hard-blocks sed/perl invocations using an in-place
// flag (e.g. -i, -i.bak) outside the .claude/ or CLAUDE.md whitelist. The
// target of an in-place edit cannot be resolved to a single path from the
// token stream alone, so this is a hard-block (no OwnershipFunc consultation,
// Owner stays "").
func evaluateInPlaceEditor(tokens []shell.Token, i int, name, fullCommand string) Decision {
	next, ok := tokenValueAt(tokens, i+1)
	if !ok || !strings.HasPrefix(next, "-") || !strings.Contains(next, "i") {
		return Decision{}
	}
	if strings.Contains(fullCommand, ".claude/") || strings.Contains(fullCommand, "CLAUDE.md") {
		return Decision{}
	}
	return Decision{Block: true, Reason: fmt.Sprintf("%s -i fuera de .claude/", name)}
}

// evaluateDD looks for a `dd of=<target>` argument after the "dd" token and
// checks it like any other candidate target.
func evaluateDD(tokens []shell.Token, i int, home string, own OwnershipFunc) Decision {
	target := findDDTarget(tokens, i)
	if target == "" {
		return Decision{}
	}
	if IsWhitelisted(target, home) {
		return Decision{}
	}
	return evaluateTarget(target, fmt.Sprintf("'dd' a ruta protegida: '%s'", target), own)
}

// findDDTarget scans the tokens after cmdIdx for the first unquoted word
// starting with "of=" and returns the value after that prefix.
func findDDTarget(tokens []shell.Token, cmdIdx int) string {
	for _, t := range tokens[cmdIdx+1:] {
		if t.Type != shell.TypeWord || t.Quoted {
			continue
		}
		if strings.HasPrefix(t.Value, "of=") {
			return strings.TrimPrefix(t.Value, "of=")
		}
	}
	return ""
}

// evaluateWatchedCommand handles tee/mv/cp/rm/rmdir/touch/chmod/chown/ln/
// install/patch/truncate: the last non-flag unquoted word before the next
// redirect or separator is the candidate write target.
func evaluateWatchedCommand(tokens []shell.Token, i int, name, home string, own OwnershipFunc) Decision {
	target := findLastWordTarget(tokens, i)
	if target == "" || strings.HasPrefix(target, "-") {
		return Decision{}
	}
	if IsWhitelisted(target, home) {
		return Decision{}
	}
	return evaluateTarget(target, fmt.Sprintf("'%s' a ruta protegida: '%s'", name, target), own)
}

// findLastWordTarget mirrors _find_last_word_target: given the index of a
// watched command's token, it looks at the tokens that follow, stops at the
// first redirect or separator (never crossing a pipeline/&&/|| boundary or a
// redirect), and returns the last unquoted, non-flag word in that segment.
func findLastWordTarget(tokens []shell.Token, cmdIdx int) string {
	segment := tokens[cmdIdx+1:]
	stop := len(segment)
	for i, t := range segment {
		if t.Type == shell.TypeRedirect || t.Type == shell.TypeSeparator {
			stop = i
			break
		}
	}
	segment = segment[:stop]

	target := ""
	for _, t := range segment {
		if t.Type != shell.TypeWord || t.Quoted {
			continue
		}
		if strings.HasPrefix(t.Value, "-") {
			continue
		}
		target = t.Value
	}
	return target
}

// tokenValueAt returns tokens[i].Value and true when i is in range,
// regardless of the token's Type or Quoted flag — mirrors the bash
// `.tokens[$i].value // empty` jq lookups, which never filter on type.
func tokenValueAt(tokens []shell.Token, i int) (string, bool) {
	if i < 0 || i >= len(tokens) {
		return "", false
	}
	return tokens[i].Value, true
}

// commandMentionsProtectedPath reports whether cmd (the full, untokenized
// command string) mentions a path outside the whitelist, using the same
// heuristic as the bash implementation: split the command on whitespace and
// the punctuation Python/JS call syntax typically uses (parens, brackets,
// commas), then check every path-shaped candidate against the whitelist.
//
// This is deliberately a text-level heuristic (not an AST walk of the inline
// script) — it exists to catch inline python -c / node -e scripts that write
// to or otherwise reference protected paths via APIs the tokenizer cannot see
// into (shutil.copy, subprocess.run, os.rename, fs.writeFileSync, etc.),
// trading a higher false-positive rate on reads for a deny-by-default
// posture on writes (D3, SPEC-042).
func commandMentionsProtectedPath(cmd, home string) bool {
	if !strings.Contains(cmd, "/") && !strings.Contains(cmd, "~/") && !strings.Contains(cmd, `.\`) {
		return false // fast path: no path-shaped substring anywhere.
	}

	fields := strings.FieldsFunc(cmd, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '(', ')', '[', ']', ',':
			return true
		}
		return false
	})

	for _, candidate := range fields {
		if candidate == "" {
			continue
		}
		if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
			continue
		}
		if strings.HasPrefix(candidate, "-") {
			continue
		}

		// Strip a redirect operator glued to the candidate (e.g. "2>/dev/null").
		for {
			loc := redirectOperatorPrefix.FindStringIndex(candidate)
			if loc == nil || loc[1] == 0 {
				break
			}
			candidate = candidate[loc[1]:]
		}
		if candidate == "" {
			continue
		}
		if strings.HasPrefix(candidate, "/dev/") {
			continue
		}

		if !looksLikePath(candidate) {
			continue
		}

		cleaned := quoteAndBracketStripper.Replace(candidate)
		if cleaned == "" {
			continue
		}
		if !IsWhitelisted(cleaned, home) {
			return true
		}
	}
	return false
}

// looksLikePath reports whether candidate resembles a filesystem path:
// absolute, explicitly relative, home-relative, or simply containing a "/"
// anywhere (mirrors the bash `"$candidate" == */*` catch-all).
func looksLikePath(candidate string) bool {
	return strings.HasPrefix(candidate, "/") ||
		strings.HasPrefix(candidate, "./") ||
		strings.HasPrefix(candidate, "~/") ||
		strings.Contains(candidate, "/")
}
