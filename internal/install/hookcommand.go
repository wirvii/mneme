package install

import "strings"

// parseMnemeHookSubcommand extracts the `mneme hook <sub>` subcommand from a
// registered hook command string, ignoring the executable's path.
//
// Why this exists (SPEC-107): the identity of a hook registration is the pair
// (executable, subcommand), not the literal string mneme wrote. A user who
// customises the command — most commonly to an absolute path because their
// shell does not resolve `mneme` on the PATH of the process that launches the
// agent — produces a string install no longer recognised as its own, and the
// next `mneme install` appended a duplicate registration alongside it
// (confirmed live 2026-08-04 against ~/.codex/hooks.json). This function is
// the parser half of that fix; sameHookCommand is the predicate that uses it.
//
// The parser is closed, positional and deterministic — not a shell parser.
// Any input it cannot classify with confidence falls back to !ok, which
// leaves sameHookCommand's literal-byte fallback as the safe default: this
// matters because the identity now authorises destructive purges
// (removeHookCommands / filterOutHookCommands), so the conservative failure
// mode is "treat as foreign", never "treat as mneme's own".
//
// Algorithm (see hookCommandWords for the tokenizer rules):
//  1. TrimSpace; empty -> !ok.
//  2. Tokenize with hookCommandWords; !ok on any shell metacharacter found
//     outside quotes, or an unterminated quote.
//  3. Fewer than 3 words -> !ok (need at least <exe> hook <sub>).
//  4. Basename of words[0]: the substring after the LAST '/' or '\', whichever
//     is further right. filepath.Base is deliberately not used: on
//     darwin/linux it does not cut on '\', which would stop Windows paths
//     (e.g. `C:\Users\x\go\bin\mneme.exe`) from being recognised — the
//     identity must be host-agnostic because a committed
//     .claude/settings.json travels between machines regardless of which OS
//     wrote it.
//  5. The basename must be "mneme" or "mneme.exe", compared with
//     strings.EqualFold — exact name equality, never a suffix or prefix
//     match ("mnemex", "my-mneme" must NOT match). ".exe" is accepted on any
//     GOOS: introducing runtime.GOOS here would make the identity depend on
//     which machine evaluates it, which is exactly what a portable settings
//     file must not do. runtime.GOOS is therefore never imported by this
//     file.
//  6. words[1] must be exactly "hook" (case-sensitive — Cobra's subcommand
//     dispatch is case-sensitive, so "hooks" or "HOOK" is never mneme's own
//     registration).
//  7. sub := words[2]; empty or starting with '-' -> !ok (a flag in the
//     subcommand position is not a subcommand).
//  8. words[3:] are ignored: the identity is (executable, subcommand) by
//     definition, so extra arguments (e.g. --debug) don't change it — a user
//     who added a flag to the registered command must not receive a
//     duplicate.
//  9. Return sub, true.
func parseMnemeHookSubcommand(command string) (sub string, ok bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}

	words, ok := hookCommandWords(command)
	if !ok || len(words) < 3 {
		return "", false
	}

	basename := lastPathBasename(words[0])
	if basename == "" {
		return "", false
	}
	if !strings.EqualFold(basename, "mneme") && !strings.EqualFold(basename, "mneme.exe") {
		return "", false
	}

	if words[1] != "hook" {
		return "", false
	}

	sub = words[2]
	if sub == "" || strings.HasPrefix(sub, "-") {
		return "", false
	}

	return sub, true
}

// lastPathBasename returns the substring of s after the last '/' or '\',
// whichever appears further right — a deliberately host-agnostic
// alternative to filepath.Base, which does not treat '\' as a separator on
// darwin/linux and would therefore fail to recognise a Windows path
// registered in a settings file evaluated on a non-Windows host (SPEC-107
// DD7).
func lastPathBasename(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		return s[i+1:]
	}
	return s
}

// sameHookCommand reports whether a and b identify the SAME hook
// registration: either by exact byte equality, or — when both parse as a
// mneme hook subcommand — by matching (executable, subcommand) identity.
//
// The identity check WIDENS, never REPLACES, byte equality (SPEC-107 DD8):
//   - it preserves the purge of the legacy `enforce_delegation.sh` absolute
//     path (which never parses — isLegacyDelegationScriptCommand handles it
//     separately) and of any unrelated foreign command, both of which only
//     ever match by the literal-equality arm;
//   - it keeps sameHookCommand reflexive without depending on the parser
//     (a == a always matches, even for strings the parser rejects);
//   - the caller's "want" side is always a canonical command mneme itself
//     generates (`mneme hook <sub>`), so no allowlist of valid subcommands is
//     needed here: `mneme hook banana` never matches a patch mneme writes.
func sameHookCommand(a, b string) bool {
	if a == b {
		return true
	}
	subA, okA := parseMnemeHookSubcommand(a)
	if !okA {
		return false
	}
	subB, okB := parseMnemeHookSubcommand(b)
	return okB && subA == subB
}

// hookCommandWords splits command into words the way a POSIX-ish shell would
// tokenize a simple command line, with a critical difference: it never
// interprets backslash as an escape character, because a Windows path
// (`C:\Users\x\go\bin\mneme.exe`) must survive untouched to be recognised.
//
// Rules (SPEC-107 DD7):
//   - Runes are scanned left to right, tracking whether we are inside a
//     single or double quote; a quote character toggles that state and is
//     itself discarded from the token (not written into the word).
//   - Outside quotes, any of the shell metacharacters `| & ; < > $ ` ( )` or
//     a newline (\n, \r) makes ok false immediately: a command containing a
//     pipeline, redirection, substitution or compound statement is a
//     compound expression mneme cannot safely reason about, and because the
//     identity now authorises destructive purges, the safe posture is to not
//     recognise it as mneme's own at all.
//   - Those same characters are literal inside quotes and do NOT trigger the
//     guard — this is what lets
//     `"C:\Program Files (x86)\mneme\mneme.exe" hook session-start` parse:
//     the parentheses and space are part of a real Windows path.
//   - Outside quotes, runs of space/tab separate words.
//   - No escape processing of any kind: '\' is always a literal rune.
//   - An unterminated quote at end of input makes ok false.
func hookCommandWords(command string) (words []string, ok bool) {
	const metacharacters = "|&;<>$`()"

	var current strings.Builder
	inWord := false
	var quote rune // 0 when not inside a quote, else '\'' or '"'

	flush := func() {
		if inWord {
			words = append(words, current.String())
			current.Reset()
			inWord = false
		}
	}

	for _, r := range command {
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			inWord = true
			continue
		}

		switch {
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == '\n' || r == '\r' || strings.ContainsRune(metacharacters, r):
			return nil, false
		case r == ' ' || r == '\t':
			flush()
		default:
			current.WriteRune(r)
			inWord = true
		}
	}

	if quote != 0 {
		return nil, false
	}
	flush()

	return words, true
}
