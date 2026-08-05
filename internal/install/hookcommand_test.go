package install

import "testing"

// TestSameHookCommand_MatchesCanonicalSessionStart is the boundary table for
// SPEC-107: every row is checked via sameHookCommand(got, "mneme hook
// session-start") — the exact predicate PatchHooks/WriteCodexHooks/
// ProjectDelegationHookStatus/removeHookCommands now all share. This table IS
// the deliverable of this step (plan.md Step 1): it is not a happy-path
// smoke test, it is the specification of where the heuristic's confidence
// ends and the safe literal-equality fallback takes over.
func TestSameHookCommand_MatchesCanonicalSessionStart(t *testing.T) {
	const canonical = "mneme hook session-start"

	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"1 canonical form", "mneme hook session-start", true},
		{"2 surrounding whitespace", "   mneme hook session-start  ", true},
		{"3 posix absolute path", "/Users/x/.local/bin/mneme hook session-start", true},
		{"4 windows path, backslash basename", `C:\Users\x\go\bin\mneme.exe hook session-start`, true},
		{"5 bare .exe on any GOOS", "mneme.exe hook session-start", true},
		{"6 double-quoted windows path with parens", `"C:\Program Files (x86)\mneme\mneme.exe" hook session-start`, true},
		{"7 single-quoted path with space", `'/Users/x/my bin/mneme' hook session-start`, true},
		{"8 basename case-insensitive", "MNEME.EXE hook session-start", true},
		{"9 extra trailing arguments ignored", "mneme hook session-start --debug", true},
		{"10 quoted subcommand", `mneme hook "session-start"`, true},
		{"11 unrelated user wrapper script", "/Users/x/.codex/mi-script.sh", false},
		{"12 shell wrapper sh -c", `sh -c "mneme hook session-start"`, false},
		{"13 shell wrapper echo", `echo "mneme hook session-start"`, false},
		{"14 pipe outside quotes", "mneme hook session-start | tee /tmp/log", false},
		{"15 redirection outside quotes", "mneme hook session-start > /tmp/log", false},
		{"16 semicolon outside quotes", "mneme hook session-start; echo x", false},
		{"17 ampersand outside quotes", "mneme hook session-start && true", false},
		{"18 command substitution", "$(which mneme) hook session-start", false},
		{"19 basename exact, not suffix", "/opt/bin/mnemex hook session-start", false},
		{"20 basename exact, not prefix", "/opt/bin/my-mneme hook session-start", false},
		{"21 verb hooks not hook", "mneme hooks session-start", false},
		{"22 verb case-sensitive", "mneme HOOK session-start", false},
		{"23 subcommand case-sensitive", "mneme hook Session-Start", false},
		{"24 two words only", "mneme hook", false},
		{"25 flag in subcommand position", "mneme hook --help", false},
		{"26 no global-flag skipping", "mneme --data-dir /tmp hook session-start", false},
		{"27 unterminated quote", `"mneme hook session-start`, false},
		{"28 empty string", "", false},
		{"29 different subcommand", "mneme hook session-end", false},
		{"33 empty basename", "/ hook session-start", false},
		{"34 metacharacter newline outside quotes", "mneme hook session-start\necho x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameHookCommand(tc.input, canonical); got != tc.want {
				t.Errorf("sameHookCommand(%q, %q) = %v, want %v", tc.input, canonical, got, tc.want)
			}
		})
	}
}

// TestSameHookCommand_FallbackLiteralEquality covers table rows 30-31: a
// string that does NOT parse as a mneme hook subcommand still matches
// itself, via the byte-equality arm of sameHookCommand — the fallback that
// preserves the purge of the legacy enforce_delegation.sh registration and
// of any foreign command (SPEC-107 DD8).
func TestSameHookCommand_FallbackLiteralEquality(t *testing.T) {
	cases := []string{
		"/home/u/.claude/hooks/enforce_delegation.sh",
		"/Users/x/.codex/mi-script.sh",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			if _, ok := parseMnemeHookSubcommand(cmd); ok {
				t.Fatalf("precondition failed: %q parses as a mneme hook subcommand", cmd)
			}
			if !sameHookCommand(cmd, cmd) {
				t.Errorf("sameHookCommand(%q, %q) = false, want true (byte-identical fallback)", cmd, cmd)
			}
		})
	}
}

// TestParseMnemeHookSubcommand_LegacyCommandHasNoIdentity covers table row
// 32: the legacy enforce_delegation.sh registration carries no mneme
// executable and no subcommand to extract, so it must never parse — it is
// purged (or preserved) only through the literal-equality fallback,
// never through the identity match (SPEC-107 DD12).
func TestParseMnemeHookSubcommand_LegacyCommandHasNoIdentity(t *testing.T) {
	const legacy = "/home/u/.claude/hooks/enforce_delegation.sh"
	if sub, ok := parseMnemeHookSubcommand(legacy); ok {
		t.Errorf("parseMnemeHookSubcommand(%q) = (%q, true), want !ok", legacy, sub)
	}
}

// TestSameHookCommand_DiscriminatesBySubcommand verifies the identity
// distinguishes by subcommand and not merely by executable: a customised
// enforce-delegation registration must match the canonical
// enforce-delegation command but NOT the canonical session-start command.
func TestSameHookCommand_DiscriminatesBySubcommand(t *testing.T) {
	const customised = "/opt/bin/mneme hook enforce-delegation"

	if !sameHookCommand(customised, "mneme hook enforce-delegation") {
		t.Errorf("sameHookCommand(%q, %q) = false, want true", customised, "mneme hook enforce-delegation")
	}
	if sameHookCommand(customised, "mneme hook session-start") {
		t.Errorf("sameHookCommand(%q, %q) = true, want false (different subcommand)", customised, "mneme hook session-start")
	}
}
