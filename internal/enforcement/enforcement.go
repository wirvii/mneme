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

	"github.com/wirvii/mneme/internal/shell"
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

// PathContext carries the host-OS facts IsWhitelisted, EvaluateFileTool and
// EvaluateBash need to reason about paths, without this leaf package ever
// calling os/runtime itself (SPEC-075 D2). Threading these three values as
// an explicit parameter — rather than reading them from the environment —
// is what keeps the package a pure function of its inputs: tests exercise
// both Unix and Windows path semantics from any host by constructing a
// PathContext with the GOOS they want, and the single production caller
// (internal/cli/hook.go:evaluateDelegation) builds one real PathContext per
// invocation from os.UserHomeDir, os.TempDir and runtime.GOOS.
type PathContext struct {
	// Home is the user's home directory, used to expand a leading "~/" the
	// same way the original bash implementation used $HOME. Empty disables
	// expansion — a "~/"-prefixed path is left as-is and will not match any
	// whitelist rule.
	Home string

	// TempDir is the OS scratch directory (os.TempDir()): %TEMP% on
	// Windows, /tmp-family on Unix. It is only consulted in windows-mode
	// (GOOS == "windows") to whitelist the platform's actual scratch
	// location, since the hardcoded Unix literals ("/tmp/", "/private/tmp/")
	// don't apply there. Empty disables that extra check — it never
	// widens the Unix literals, which stay hardcoded regardless of TempDir.
	TempDir string

	// GOOS is runtime.GOOS. It gates every Windows-specific code path in
	// this package (backslash normalization, drive-letter/UNC absolute
	// detection, ASCII-literal case-insensitivity, TempDir-based scratch
	// matching): when GOOS != "windows" none of that logic runs and
	// behavior is byte-for-byte identical to the pre-SPEC-075 Unix-only
	// implementation.
	GOOS string
}

// isWindows reports whether pc describes a Windows host — the single gate
// every Windows-specific branch in this package checks.
func (pc PathContext) isWindows() bool {
	return pc.GOOS == "windows"
}

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
// verbatim on Unix (pc.GOOS != "windows"):
//
//   - CLAUDE.md (any location, matched by basename)
//   - .claudeignore (any location, matched by basename)
//   - any *.md path under a /docs/ directory
//   - absolute paths containing /.claude/ or /.mneme/
//   - /tmp/** and /private/tmp/** (macOS) scratch space
//   - relative paths under .claude/ or .mneme/
//
// pc.Home expands a leading "~/" the same way the bash version used $HOME —
// callers pass os.UserHomeDir() via PathContext (or "" to disable expansion,
// in which case a "~/"-prefixed path is left as-is and will not match any of
// the above).
//
// When pc.GOOS == "windows" (SPEC-075 D3), four additional, windows-only
// behaviors kick in — none of them alter the Unix path taken above, so Unix
// callers (pc.GOOS == "linux"/"darwin"/anything else) get byte-for-byte
// identical results to the pre-SPEC-075 implementation:
//
//   - Backslashes are normalized to "/" before any other check, so
//     "C:\Users\x\.claude\settings.json" is evaluated the same as its
//     forward-slash form.
//   - A drive-absolute path ("C:/...") or a UNC path ("//server/share/...",
//     already "/"-prefixed after normalization) is routed into the same
//     branch as a Unix absolute path, so the "/.claude/"/"/.mneme/"
//     substring checks apply to it.
//   - A fixed set of ASCII literals is matched case-insensitively:
//     "CLAUDE.md", "/.claude/", "/.mneme/", "/docs/", and the drive letter.
//     User-supplied path segments are never folded.
//   - pc.TempDir (the OS scratch directory, e.g. %TEMP%) is matched as a
//     prefix, in addition to the hardcoded Unix /tmp literals which stay in
//     place unconditionally.
func IsWhitelisted(path string, pc PathContext) bool {
	windows := pc.isWindows()

	path = strings.TrimPrefix(path, "./")
	path = strings.ReplaceAll(path, "'", "")
	path = strings.ReplaceAll(path, `"`, "")

	home := pc.Home
	if windows {
		path = strings.ReplaceAll(path, `\`, "/")
		home = strings.ReplaceAll(home, `\`, "/")
	}

	if home != "" && strings.HasPrefix(path, "~/") {
		path = home + "/" + strings.TrimPrefix(path, "~/")
	}

	basename := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		basename = path[idx+1:]
	}

	if basename == "CLAUDE.md" || (windows && strings.EqualFold(basename, "CLAUDE.md")) {
		return true
	}
	if basename == ".claudeignore" {
		return true
	}
	if strings.HasSuffix(basename, ".md") && pathHasDocsDir(path, windows) {
		return true
	}

	absolute := strings.HasPrefix(path, "/")
	driveAbsolute := windows && hasWindowsDrivePrefix(path)
	if absolute || driveAbsolute {
		if pathHasProtectedDir(path, windows) {
			return true
		}
		if strings.HasPrefix(path, "/tmp/") || strings.HasPrefix(path, "/private/tmp/") {
			return true
		}
		if windows && isUnderScratchDir(path, pc.TempDir) {
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

// hasWindowsDrivePrefix reports whether path starts with a drive letter
// absolute prefix ("C:/", "d:/", ...). Only called in windows-mode. UNC
// paths ("//server/share/...") don't need a separate detector here: after
// IsWhitelisted's backslash normalization they already satisfy
// strings.HasPrefix(path, "/") and take the same absolute-path branch.
func hasWindowsDrivePrefix(path string) bool {
	return len(path) >= 3 && isASCIILetter(path[0]) && path[1] == ':' && path[2] == '/'
}

// isASCIILetter reports whether b is an ASCII letter — used for the
// windows-mode drive-letter check, which must accept either case ("C:" or
// "c:") without folding any other part of the path.
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// pathHasDocsDir reports whether path (already "/"-normalized) has a
// "/docs/" directory segment. In windows-mode the "docs" literal is matched
// case-insensitively (SPEC-075 D3); on Unix it stays an exact match.
func pathHasDocsDir(path string, windows bool) bool {
	haystack := "/" + path
	if windows {
		return strings.Contains(strings.ToLower(haystack), "/docs/")
	}
	return strings.Contains(haystack, "/docs/")
}

// pathHasProtectedDir reports whether the (already "/"-normalized) absolute
// path contains a "/.claude/" or "/.mneme/" directory segment. In
// windows-mode those two literals are matched case-insensitively (SPEC-075
// D3); on Unix the check is byte-for-byte the original exact-match.
func pathHasProtectedDir(path string, windows bool) bool {
	if windows {
		lower := strings.ToLower(path)
		return strings.Contains(lower, "/.claude/") || strings.Contains(lower, "/.mneme/")
	}
	return strings.Contains(path, "/.claude/") || strings.Contains(path, "/.mneme/")
}

// isUnderScratchDir reports whether the (already "/"-normalized) absolute
// path falls under tempDir, the OS scratch directory injected via
// PathContext.TempDir. Only consulted in windows-mode: it lets a real
// %TEMP% (e.g. "C:\Users\x\AppData\Local\Temp") whitelist paths under it
// the same way the hardcoded Unix /tmp literals do, without widening those
// Unix literals themselves. An empty tempDir disables the check (matches
// PathContext.TempDir's documented "" == disabled contract).
func isUnderScratchDir(path, tempDir string) bool {
	if tempDir == "" {
		return false
	}
	normalized := strings.TrimSuffix(strings.ReplaceAll(tempDir, `\`, "/"), "/")
	if normalized == "" {
		return false
	}
	lowerPath := strings.ToLower(path)
	lowerTemp := strings.ToLower(normalized)
	return lowerPath == lowerTemp || strings.HasPrefix(lowerPath, lowerTemp+"/")
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
func EvaluateFileTool(filePath string, pc PathContext, own OwnershipFunc) Decision {
	if filePath == "" {
		return Decision{}
	}
	if IsWhitelisted(filePath, pc) {
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
func EvaluateBash(command string, pc PathContext, own OwnershipFunc) Decision {
	return evaluateBash(command, pc, own, 0)
}

func evaluateBash(command string, pc PathContext, own OwnershipFunc, depth int) Decision {
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
			if d := evaluateRedirectTarget(tok.Value, pc, own); d.Block {
				return d
			}

		case shell.TypeWord:
			if tok.Quoted {
				continue
			}
			if d := evaluateWordToken(tokens, i, tok.Value, command, pc, own, depth); d.Block {
				return d
			}

		case shell.TypeCommandSubstitution:
			if depth < maxBashRecursionDepth && tok.Value != "" {
				if d := evaluateBash(tok.Value, pc, own, depth+1); d.Block {
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
func evaluateRedirectTarget(value string, pc PathContext, own OwnershipFunc) Decision {
	if isProcessSubstitution(value) {
		return Decision{}
	}
	if strings.HasPrefix(value, "/dev/") {
		return Decision{}
	}
	if IsWhitelisted(value, pc) {
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
func evaluateWordToken(tokens []shell.Token, i int, value, fullCommand string, pc PathContext, own OwnershipFunc, depth int) Decision {
	switch value {
	case "bash", "sh", "zsh":
		return evaluateShellDashC(tokens, i, pc, own, depth)
	case "sed", "perl":
		return evaluateInPlaceEditor(tokens, i, value, fullCommand)
	case "dd":
		return evaluateDD(tokens, i, pc, own)
	case "python", "python2", "python3":
		if commandMentionsProtectedPath(fullCommand, pc) {
			return Decision{Block: true, Reason: "Script Python inline menciona ruta fuera de whitelist"}
		}
	case "node":
		if commandMentionsProtectedPath(fullCommand, pc) {
			return Decision{Block: true, Reason: "Script Node inline menciona ruta fuera de whitelist"}
		}
	default:
		if watchedCommands[value] {
			return evaluateWatchedCommand(tokens, i, value, pc, own)
		}
	}
	return Decision{}
}

// evaluateShellDashC recurses one level into `bash|sh|zsh -c "<cmd>"` (D5,
// SPEC-033), guarded by depth so nested -c invocations are not walked
// indefinitely.
func evaluateShellDashC(tokens []shell.Token, i int, pc PathContext, own OwnershipFunc, depth int) Decision {
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
	return evaluateBash(inner, pc, own, depth+1)
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
func evaluateDD(tokens []shell.Token, i int, pc PathContext, own OwnershipFunc) Decision {
	target := findDDTarget(tokens, i)
	if target == "" {
		return Decision{}
	}
	if IsWhitelisted(target, pc) {
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
func evaluateWatchedCommand(tokens []shell.Token, i int, name string, pc PathContext, own OwnershipFunc) Decision {
	target := findLastWordTarget(tokens, i)
	if target == "" || strings.HasPrefix(target, "-") {
		return Decision{}
	}
	if IsWhitelisted(target, pc) {
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
func commandMentionsProtectedPath(cmd string, pc PathContext) bool {
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
		if !IsWhitelisted(cleaned, pc) {
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
