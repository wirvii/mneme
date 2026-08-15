package enforcement

import (
	"testing"

	"github.com/wirvii/mneme/internal/shell"
)

// testHome is the fixed home directory used across whitelist/tilde-expansion
// test cases. It never needs to exist on disk — IsWhitelisted only does
// string manipulation.
const testHome = "/home/tester"

// unixGOOSValues are the two Unix GOOS values every ported case (SPEC-075
// AC2) is re-invoked under, to prove the windows-mode branches added in
// IsWhitelisted/EvaluateFileTool/EvaluateBash never fire — and therefore
// never change a single assertion — on either Unix platform.
var unixGOOSValues = []string{"linux", "darwin"}

// pc builds a PathContext for goos, reusing testHome for every case in this
// file (mirrors the pre-SPEC-075 testHome-only parameter).
func pc(goos string) PathContext {
	return PathContext{Home: testHome, GOOS: goos}
}

// legacyBlockStub is an OwnershipFunc stub that always blocks with owner
// "legacy" — it reproduces the isolated $HOME/cwd test environment
// enforce_delegation_test.sh used (ISO_HOME/ISO_CWD, SPEC-068 D8's "manifest
// absent -> BLOCK legacy" branch), which is what the 34 pre-AC10 bash cases
// actually exercised. Every case below that expects a block through the
// ownership bridge (as opposed to a hard-block) uses this stub.
func legacyBlockStub(string) (bool, string) {
	return true, "legacy"
}

// backendOwnsInternalStub is an OwnershipFunc stub mirroring the AC10 bash
// cases' seeded manifest: `internal/**` is owned by "backend"; everything
// else is unowned (allow).
func backendOwnsInternalStub(target string) (bool, string) {
	if len(target) >= len("internal/") && target[:len("internal/")] == "internal/" {
		return true, "backend"
	}
	return false, ""
}

// TestEvaluateFileTool_PortedCases ports the enforce_delegation_test.sh
// file-tool cases (NR2, NR4, F5, F6, F8, F9, F11, AC10.1, AC10.2) — 9 of the
// 38 cases from that suite. The remaining file-tool cases in the original
// suite (B1-B5, AC10.3) test agent_id/subagent routing, which is not part of
// this leaf package's responsibility (it lives in
// internal/cli/hook_enforce_delegation_test.go, ahead of the call into
// EvaluateFileTool); NR3 (no tool_name) is a tool-name dispatch decision,
// also covered there.
func TestEvaluateFileTool_PortedCases(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		own       OwnershipFunc
		wantBlock bool
		wantOwner string
	}{
		{"NR2_edit_dotclaude", ".claude/settings.json", legacyBlockStub, false, ""},
		{"NR4_edit_docs_md", "docs/ARCHITECTURE.md", legacyBlockStub, false, ""},
		{"F5_write_tilde_mneme", "~/.mneme/workflows/x/specs/SPEC-043/spec.md", legacyBlockStub, false, ""},
		{"F6_write_tilde_dotclaude", "~/.claude/settings.json", legacyBlockStub, false, ""},
		{"F8_write_tilde_config_not_whitelisted", "~/.config/foo", legacyBlockStub, true, "legacy"},
		{"F9_write_tmp", "/tmp/hook_smoke.sh", legacyBlockStub, false, ""},
		{"F11_write_private_tmp", "/private/tmp/x", legacyBlockStub, false, ""},
		{"AC10_1_edit_owned_by_backend_blocks", "internal/foo.go", backendOwnsInternalStub, true, "backend"},
		{"AC10_2_edit_unowned_path_allows", "notes.txt", backendOwnsInternalStub, false, ""},
	}

	for _, goos := range unixGOOSValues {
		for _, tt := range tests {
			t.Run(goos+"/"+tt.name, func(t *testing.T) {
				got := EvaluateFileTool(tt.path, pc(goos), tt.own)
				if got.Block != tt.wantBlock {
					t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
				}
				if got.Owner != tt.wantOwner {
					t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
				}
			})
		}
	}
}

// TestEvaluateBash_PortedCases ports the remaining 22 of the 38
// enforce_delegation_test.sh cases that exercise Bash command evaluation:
// A1-A3, NR1/NR5/NR6/NR7/NR8/NR9, F1-F4/F7/F10/F12/F13, S1-S4, AC10.4.
func TestEvaluateBash_PortedCases(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		own       OwnershipFunc
		wantBlock bool
		wantOwner string
	}{
		// A-cases: orchestrator bypass hardening (AC2). Owner is now "legacy"
		// (not "") because SPEC-086 D9 F3 routes these candidates through the
		// ownership bridge instead of hard-blocking without consulting it.
		{"A1_python_shutil_copy", `python3 -c "import shutil; shutil.copy('src.go', 'internal/dst.go')"`, legacyBlockStub, true, "legacy"},
		{"A2_python_subprocess_cp", `python3 -c "import subprocess; subprocess.run(['cp', 'src', 'internal/dst.go'])"`, legacyBlockStub, true, "legacy"},
		{"A3_python_os_rename", `python3 -c "import os; os.rename('src.go', 'internal/dst.go')"`, legacyBlockStub, true, "legacy"},

		// Non-regression: safe commands still allowed (AC3).
		{"NR1_python_print_only", `python3 -c "print(2+2)"`, legacyBlockStub, false, ""},
		{"NR5_redirect_to_internal", "echo hello > internal/foo.go", legacyBlockStub, true, "legacy"},
		{"NR6_redirect_to_dotclaude", "echo hello > .claude/settings.json", legacyBlockStub, false, ""},
		{"NR7_node_write_CLAUDE_md", `node -e "fs.writeFileSync('CLAUDE.md','x')"`, legacyBlockStub, false, ""},
		{"NR8_python_open_docs_md", `python3 -c "open('docs/x.md','w')"`, legacyBlockStub, false, ""},
		{"NR9_python_open_dotclaude", `python3 -c "open('.claude/x','w')"`, legacyBlockStub, false, ""},

		// F-cases: SPEC-043 regression fixes 1-4.
		{"F1_python_redirect_2devnull_no_path", `python3 -c "print(2+2)" 2>/dev/null`, legacyBlockStub, false, ""},
		{"F2_python_open_CLAUDE_md_with_redirect", `python3 -c "open('CLAUDE.md')" 2>/dev/null`, legacyBlockStub, false, ""},
		{"F3_python_open_protected_path_with_redirect", `python3 -c "open('internal/x.go','w')" 2>/dev/null`, legacyBlockStub, true, "legacy"},
		{"F4_node_redirect_2devnull_no_path", `node -e "console.log(1)" 2>/dev/null`, legacyBlockStub, false, ""},
		{"F7_redirect_tilde_mneme", "echo x > ~/.mneme/scratch.txt", legacyBlockStub, false, ""},
		{"F10_redirect_tmp", "echo x > /tmp/out.log", legacyBlockStub, false, ""},
		{"F12_redirect_var_not_whitelisted", "echo x > /var/foo", legacyBlockStub, true, "legacy"},
		{"F13_process_substitution_not_redirect", "while read l; do echo $l; done < <(jq -r .x /tmp/a.json)", legacyBlockStub, false, ""},

		// S-cases: /tmp is not a repo bridge (CONDICIÓN 2).
		{"S1_cp_tmp_to_protected", "cp /tmp/x.go internal/store/x.go", legacyBlockStub, true, "legacy"},
		{"S2_mv_tmp_to_apps", "mv /tmp/x apps/web/x.ts", legacyBlockStub, true, "legacy"},
		{"S3_cp_to_tmp", "cp internal/x.go /tmp/x.go", legacyBlockStub, false, ""},
		{"S4_tee_tmp", "echo x | tee /tmp/out", legacyBlockStub, false, ""},

		// AC10.4: manifest-aware ownership bridge over a Bash redirect.
		{"AC10_4_bash_redirect_owned_path_blocks", "echo x > internal/foo.go", backendOwnsInternalStub, true, "backend"},
	}

	for _, goos := range unixGOOSValues {
		for _, tt := range tests {
			t.Run(goos+"/"+tt.name, func(t *testing.T) {
				got := EvaluateBash(tt.command, pc(goos), tt.own)
				if got.Block != tt.wantBlock {
					t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
				}
				if got.Owner != tt.wantOwner {
					t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
				}
			})
		}
	}
}

// TestEvaluateBash_RecursionCoverage exercises the bash -c / $() one-level
// recursion (D5, SPEC-033) that D2 requires EvaluateBash to implement. None
// of the 38 ported enforce_delegation_test.sh cases happen to exercise this
// path (recursion was validated by the F/S/AC10 suites indirectly through
// other mechanisms), so these two cases are additional coverage beyond the
// literal port, added to satisfy AC6's parity requirement without silently
// leaving recursion untested.
func TestEvaluateBash_RecursionCoverage(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		wantBlock bool
		wantOwner string
	}{
		{"bash_dash_c_recurses_one_level", `bash -c "echo hi > internal/foo.go"`, true, "legacy"},
		{"command_substitution_recurses_one_level", "echo $(echo hi > internal/foo.go)", true, "legacy"},
		{"bash_dash_c_recursion_allows_safe_inner", `bash -c "echo hi > .claude/x"`, false, ""},
	}

	for _, goos := range unixGOOSValues {
		for _, tt := range tests {
			t.Run(goos+"/"+tt.name, func(t *testing.T) {
				got := EvaluateBash(tt.command, pc(goos), legacyBlockStub)
				if got.Block != tt.wantBlock {
					t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
				}
				if got.Owner != tt.wantOwner {
					t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
				}
			})
		}
	}
}

// TestIsWhitelisted covers the whitelist rules directly (basename matches,
// docs/*.md, absolute .claude//.mneme/ substrings, /tmp scratch, relative
// .claude//.mneme/ prefixes) independent of the Evaluate* entrypoints. Every
// case is re-invoked under both Unix GOOS values (SPEC-075 AC2/AC4): none of
// them trigger the windows-mode branches, so assertions are shared verbatim.
func TestIsWhitelisted(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"claude_md_anywhere", "some/deep/path/CLAUDE.md", true},
		{"claudeignore_root", ".claudeignore", true},
		{"docs_md_relative", "docs/ARCHITECTURE.md", true},
		{"docs_md_nested", "a/b/docs/x.md", true},
		{"non_docs_md", "internal/README.md", false},
		{"absolute_dotclaude", "/repo/.claude/settings.json", true},
		{"absolute_dotmneme", "/repo/.mneme/workflows/x", true},
		{"absolute_tmp", "/tmp/scratch.txt", true},
		{"absolute_private_tmp", "/private/tmp/scratch.txt", true},
		{"absolute_unrelated", "/etc/passwd", false},
		{"relative_dotclaude", ".claude/settings.json", true},
		{"relative_dotclaude_bare", ".claude", true},
		{"relative_dotmneme", ".mneme/workflows/x", true},
		{"relative_unrelated", "internal/foo.go", false},
	}

	for _, goos := range unixGOOSValues {
		for _, tt := range tests {
			t.Run(goos+"/"+tt.name, func(t *testing.T) {
				if got := IsWhitelisted(tt.path, pc(goos)); got != tt.want {
					t.Errorf("IsWhitelisted(%q) = %v, want %v", tt.path, got, tt.want)
				}
			})
		}
	}
}

// TestIsWhitelisted_Windows is the new GOOS="windows" matrix (SPEC-075 AC3):
// drive-absolute paths, UNC paths, %TEMP%-prefixed scratch paths, a blocked
// non-whitelisted path, and paths mixing "/" and "\" separators.
func TestIsWhitelisted_Windows(t *testing.T) {
	winPC := PathContext{
		Home:    `C:\Users\x`,
		TempDir: `C:\Users\x\AppData\Local\Temp`,
		GOOS:    "windows",
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"drive_absolute_claude_md", `C:\proj\CLAUDE.md`, true},
		{"drive_absolute_dotclaude_settings", `C:\Users\x\.claude\settings.json`, true},
		{"unc_dotmneme", `\\server\share\.mneme\x`, true},
		{"under_tempdir_backslash", `C:\Users\x\AppData\Local\Temp\scratch.txt`, true},
		{"under_tempdir_forward_slash", `C:/Users/x/AppData/Local/Temp/scratch.txt`, true},
		{"drive_absolute_blocked_src", `C:\proj\src\main.go`, false},
		{"mixed_separators_dotclaude", `C:\proj/.claude\settings.json`, true},
		{"mixed_separators_blocked", `C:/proj\src/main.go`, false},
		{"lowercase_drive_letter_claude_md", `c:\proj\CLAUDE.md`, true},
		{"lowercase_claude_md_basename", `C:\proj\claude.md`, true},
		{"tilde_expansion_dotclaude", `~\.claude\settings.json`, true},
		{"docs_md_backslash", `docs\ARCHITECTURE.md`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWhitelisted(tt.path, winPC); got != tt.want {
				t.Errorf("IsWhitelisted(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestEvaluateFileTool_Windows exercises EvaluateFileTool end-to-end
// (whitelist + OwnershipFunc bridge) under GOOS="windows" (SPEC-075 AC3),
// including the blocked case going through the ownership bridge.
func TestEvaluateFileTool_Windows(t *testing.T) {
	winPC := PathContext{
		Home:    `C:\Users\x`,
		TempDir: `C:\Users\x\AppData\Local\Temp`,
		GOOS:    "windows",
	}

	tests := []struct {
		name      string
		path      string
		wantBlock bool
		wantOwner string
	}{
		{"whitelisted_claude_md", `C:\proj\CLAUDE.md`, false, ""},
		{"whitelisted_dotclaude", `C:\Users\x\.claude\settings.json`, false, ""},
		{"whitelisted_unc_dotmneme", `\\server\share\.mneme\x`, false, ""},
		{"whitelisted_tempdir", `C:\Users\x\AppData\Local\Temp\scratch.txt`, false, ""},
		{"blocked_src_main_go", `C:\proj\src\main.go`, true, "legacy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateFileTool(tt.path, winPC, legacyBlockStub)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
	}
}

// TestEvaluateFileTool_EmptyPath covers the empty-filePath fast path in
// EvaluateFileTool (a malformed tool payload with no file_path field).
func TestEvaluateFileTool_EmptyPath(t *testing.T) {
	got := EvaluateFileTool("", pc("linux"), legacyBlockStub)
	if got.Block {
		t.Fatalf("Block = true, want false for empty path (decision: %+v)", got)
	}
}

// TestEvaluateBash_EmptyOrUnparsableCommand covers EvaluateBash's two
// fail-open branches ahead of any token evaluation: a whitespace-only
// command (never reaches the tokenizer) and a command the tokenizer cannot
// parse (unterminated quote), matching check_bash_go's "tokenizador no
// disponible" fallback.
func TestEvaluateBash_EmptyOrUnparsableCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"whitespace_only", "   \n\t "},
		{"unterminated_quote", `echo "hi`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateBash(tt.command, pc("linux"), legacyBlockStub)
			if got.Block {
				t.Fatalf("Block = true, want false (fail-open) for %q (decision: %+v)", tt.command, got)
			}
		})
	}
}

// TestEvaluateBash_SedPerlInPlaceAndDD ports coverage for evaluateInPlaceEditor
// and evaluateDD/findDDTarget: neither is exercised by the 38 ported
// enforce_delegation_test.sh cases (SPEC-075 AC6, >85% coverage).
func TestEvaluateBash_SedPerlInPlaceAndDD(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		wantBlock bool
		wantOwner string
	}{
		{"sed_i_outside_whitelist_blocks", "sed -i 's/a/b/' internal/foo.go", true, ""},
		{"perl_i_outside_whitelist_blocks", "perl -i -pe 's/a/b/' internal/foo.go", true, ""},
		{"sed_i_inside_dotclaude_allows", "sed -i 's/a/b/' .claude/settings.json", false, ""},
		{"sed_i_mentions_claude_md_allows", "sed -i 's/a/b/' CLAUDE.md", false, ""},
		{"sed_i_comment_cannot_exempt_target", "sed -i 's/a/b/' internal/foo.go # .claude/", true, ""},
		{"sed_i_later_command_cannot_exempt_target", `sed -i 's/a/b/' internal/foo.go && echo "ver .claude/"`, true, ""},
		{"perl_i_comment_cannot_exempt_target", "perl -i -pe 's/a/b/' cmd/mneme/main.go # ref: CLAUDE.md", true, ""},
		{"sed_without_i_flag_allows", "sed 's/a/b/' internal/foo.go", false, ""},
		{"sed_flag_without_i_letter_allows", "sed -n 'p' internal/foo.go", false, ""},
		{"sed_no_following_token_allows", "sed", false, ""},
		{"dd_of_protected_blocks", "dd if=/dev/zero of=internal/foo.go", true, "legacy"},
		{"dd_of_whitelisted_allows", "dd if=/dev/zero of=.claude/scratch", false, ""},
		{"dd_without_of_allows", "dd if=/dev/zero bs=1M count=1", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateBash(tt.command, pc("linux"), legacyBlockStub)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
	}
}

// TestEvaluateBash_ShellDashCEdgeCases exercises evaluateShellDashC's
// early-return branches beyond the two RecursionCoverage happy-path cases:
// no token after "bash" at all, a following token that isn't "-c", a "-c"
// with no body at all, an empty "-c" body, and recursion attempted beyond
// maxBashRecursionDepth (a nested bash -c inside bash -c fails open on the
// inner level, SPEC-033 D5).
func TestEvaluateBash_ShellDashCEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		wantBlock bool
	}{
		{"bash_alone_no_next_token", "bash", false},
		{"bash_script_not_dash_c", "bash foo.sh", false},
		{"bash_dash_c_no_body_token", "bash -c", false},
		{"bash_dash_c_empty_body", `bash -c ""`, false},
		{"nested_dash_c_beyond_depth_fails_open", `bash -c "bash -c 'echo hi > internal/foo.go'"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateBash(tt.command, pc("linux"), legacyBlockStub)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
		})
	}
}

// TestEvaluateBash_WatchedCommandBoundaries covers findLastWordTarget's
// segment-boundary and skip logic: a "&&" statement boundary stops the scan
// before crossing into the next command, and flags/quoted words in between
// the watched command and its real target are skipped rather than picked as
// the candidate.
func TestEvaluateBash_WatchedCommandBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		wantBlock bool
		wantOwner string
	}{
		{"mv_stops_at_and_boundary", "mv src.go internal/dst.go && echo done", true, "legacy"},
		{"mv_skips_flag_and_quoted_arg", `mv -v "note" internal/dst.go`, true, "legacy"},
		{"rm_only_flags_no_target_allows", "rm -rf", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateBash(tt.command, pc("linux"), legacyBlockStub)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
	}
}

// TestCommandMentionsProtectedPath covers commandMentionsProtectedPath's
// candidate-filtering branches directly (http(s):// skip, a redirect
// operator that strips down to nothing, and the happy path that does find a
// protected candidate) — white-box, since this heuristic is only reachable
// indirectly through EvaluateBash's python/node dispatch.
func TestCommandMentionsProtectedPath(t *testing.T) {
	p := pc("linux")
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"http_url_candidate_skipped", "fetch http://example.com/internal/secret", false},
		{"https_url_candidate_skipped", "fetch https://example.com/internal/secret", false},
		{"redirect_operator_strips_to_empty_candidate", "run .claude/x 2>>", false},
		{"protected_relative_path_detected", "open internal/foo.go", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandMentionsProtectedPath(tt.cmd, p); got != tt.want {
				t.Errorf("commandMentionsProtectedPath(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

// --- SPEC-086 D9: inline-script false positive (F1/F2/F3) -------------------

// TestEvaluateBash_InlineScriptFP_AC3Repro is the literal owner repro (AC3):
// a read-only python3 -c JSON parser fed by a SQL query whose string literal
// merely contains a path-shaped substring inside another (already-terminated)
// Bash command. Before F1+F2 this blocked (exit 2) because
// commandMentionsProtectedPath inspected the WHOLE command line, including
// the sqlite3 argument that precedes the pipe. Mutation check: deleting F1
// (scoping to the -c argument) or F2 (the write-signal gate) turns this red —
// see TestEvaluateInlineScript_MutationGuards below for the isolated proof.
func TestEvaluateBash_InlineScriptFP_AC3Repro(t *testing.T) {
	cmd := `sqlite3 ~/.mneme/projects/wirvii-mneme.db "SELECT id FROM memories WHERE files LIKE '%internal/cli/hook.go%'" | python3 -c "import sys,json; data=sys.stdin.read(); print(data)"`

	got := EvaluateBash(cmd, pc("linux"), legacyBlockStub)

	if got.Block {
		t.Fatalf("Block = true, want false (decision: %+v) — AC3 repro must not block", got)
	}
}

// TestEvaluateBash_InlineScriptFP_NonRegression covers AC4: a bare read-only
// inline script, and a script whose print() statement mentions no path at
// all, both stay allowed.
func TestEvaluateBash_InlineScriptFP_NonRegression(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{"cat_pipe_python_json_parser", `cat internal/cli/hook.go | python3 -c "import sys,json"`},
		{"python_arithmetic_only", `python3 -c "print(2+2)"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateBash(tt.cmd, pc("linux"), legacyBlockStub)
			if got.Block {
				t.Errorf("Block = true, want false (decision: %+v)", got)
			}
		})
	}
}

// TestEvaluateBash_InlineScriptFP_F6RestoresFallback covers AC6: a
// write-signal-carrying inline script targeting a path with NO owner in the
// manifest is now allowed (F3 routes the candidate through own instead of
// hard-blocking), where the pre-SPEC-086 implementation would have blocked
// unconditionally.
func TestEvaluateBash_InlineScriptFP_F6RestoresFallback(t *testing.T) {
	unownedStub := func(string) (bool, string) { return false, "" }

	got := EvaluateBash(`python3 -c "open('internal/x.go','w').write('x')"`, pc("linux"), unownedStub)

	if got.Block {
		t.Fatalf("Block = true, want false (decision: %+v) — unowned candidate must fall through to allow", got)
	}
}

// TestEvaluateInlineScript_MutationGuards isolates each of F1/F2/F3 with a
// direct mutation check: temporarily reproducing what the OLD, unscoped
// heuristic would have done — inspecting the full command line with no
// write-signal gate — and confirming today's implementation diverges from it
// exactly where the fix requires (per the mutation discipline in
// [[testing/antipatron-guardian-que-no-detecta-su-eliminacion]]).
func TestEvaluateInlineScript_MutationGuards(t *testing.T) {
	p := pc("linux")

	// F1 guard: without a -c/-e flag at all, there is no inline script to
	// inspect. This case is deliberately built so a WRITE-SIGNAL-CARRYING
	// string sits among the trailing words — if F1 were deleted (falling
	// back to joining every trailing word into "the script" instead of
	// requiring -c/-e), F2's write-signal check would find shutil.copy(
	// and pass, and F3 would then reach own() with the 'internal/dst.go'
	// candidate and block. An earlier version of this guard used a
	// trailing word with NO write signal (`python3 internal/x.go`), which
	// stayed green under this exact F1 deletion because F2 masked it — a
	// confounded, blind guardian. This is the corrected, isolated version:
	// verified live by actually deleting F1's -c/-e requirement in
	// enforcement.go and confirming this exact case turns red (restored
	// immediately after).
	tokens, err := shell.Tokenize(`python3 script.py "shutil.copy('a', 'internal/dst.go')"`)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	if got := evaluateInlineScript(tokens, 0, "Python", hasPythonWriteSignal, p, legacyBlockStub); got.Block {
		t.Errorf("F1 guard: Block = true, want false — no -c/-e flag means no inline script (decision: %+v)", got)
	}

	// F2 guard: a -c script that mentions a protected path but has no write
	// API call must not block. If F2 were deleted (mention alone were
	// sufficient), this would block.
	readOnlyTokens, err := shell.Tokenize(`python3 -c "print(open('internal/x.go').read())"`)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	if got := evaluateInlineScript(readOnlyTokens, 0, "Python", hasPythonWriteSignal, p, legacyBlockStub); got.Block {
		t.Errorf("F2 guard: Block = true, want false — open() with no write mode has no write signal (decision: %+v)", got)
	}

	// F3 guard: a write-signal script targeting an EXPLICITLY unowned path
	// (own returns false) must allow. If F3 were deleted (hard-block
	// regardless of own), this would block even though own says allow.
	writeTokens, err := shell.Tokenize(`python3 -c "open('internal/x.go','w')"`)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	allowStub := func(string) (bool, string) { return false, "" }
	if got := evaluateInlineScript(writeTokens, 0, "Python", hasPythonWriteSignal, p, allowStub); got.Block {
		t.Errorf("F3 guard: Block = true, want false — own() returned allow, the hard-block must be gone (decision: %+v)", got)
	}
	// Sanity: the SAME script blocks when own says block, proving the
	// candidate really does reach the ownership bridge (F3's positive case).
	if got := evaluateInlineScript(writeTokens, 0, "Python", hasPythonWriteSignal, p, legacyBlockStub); !got.Block {
		t.Errorf("F3 guard: Block = false, want true — own() returned block, the bridge must be consulted (decision: %+v)", got)
	}
}

// TestIsUnderScratchDir white-box tests isUnderScratchDir directly: an
// empty tempDir disables the check, a tempDir that normalizes down to
// nothing (a bare separator) also disables it, and both an exact match and
// a case-insensitive prefix match are recognized.
func TestIsUnderScratchDir(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		tempDir string
		want    bool
	}{
		{"empty_tempdir_disables", "C:/Users/x/AppData/Local/Temp/f", "", false},
		{"tempdir_normalizes_to_empty", "C:/Users/x/f", "/", false},
		{"exact_match_case_insensitive", "c:/users/x/temp", "C:/Users/x/Temp", true},
		{"prefix_match_case_insensitive", "C:/Users/x/Temp/sub/file.txt", `C:\Users\x\Temp\`, true},
		{"not_under_tempdir", "C:/other/file.txt", "C:/Users/x/Temp", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnderScratchDir(tt.path, tt.tempDir); got != tt.want {
				t.Errorf("isUnderScratchDir(%q, %q) = %v, want %v", tt.path, tt.tempDir, got, tt.want)
			}
		})
	}
}

// TestTokenValueAt white-box tests tokenValueAt's bounds checking directly
// (negative index, past-the-end index, in-range index).
func TestTokenValueAt(t *testing.T) {
	tokens := []shell.Token{{Value: "a"}, {Value: "b"}}

	if v, ok := tokenValueAt(tokens, -1); ok || v != "" {
		t.Errorf("tokenValueAt(-1) = (%q, %v), want (\"\", false)", v, ok)
	}
	if v, ok := tokenValueAt(tokens, 5); ok || v != "" {
		t.Errorf("tokenValueAt(5) = (%q, %v), want (\"\", false)", v, ok)
	}
	if v, ok := tokenValueAt(tokens, 1); !ok || v != "b" {
		t.Errorf("tokenValueAt(1) = (%q, %v), want (\"b\", true)", v, ok)
	}
}
