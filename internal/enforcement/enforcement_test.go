package enforcement

import "testing"

// testHome is the fixed home directory used across whitelist/tilde-expansion
// test cases. It never needs to exist on disk — IsWhitelisted only does
// string manipulation.
const testHome = "/home/tester"

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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateFileTool(tt.path, testHome, tt.own)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
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
		// A-cases: orchestrator bypass hardening (AC2).
		{"A1_python_shutil_copy", `python3 -c "import shutil; shutil.copy('src.go', 'internal/dst.go')"`, legacyBlockStub, true, ""},
		{"A2_python_subprocess_cp", `python3 -c "import subprocess; subprocess.run(['cp', 'src', 'internal/dst.go'])"`, legacyBlockStub, true, ""},
		{"A3_python_os_rename", `python3 -c "import os; os.rename('src.go', 'internal/dst.go')"`, legacyBlockStub, true, ""},

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
		{"F3_python_open_protected_path_with_redirect", `python3 -c "open('internal/x.go','w')" 2>/dev/null`, legacyBlockStub, true, ""},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateBash(tt.command, testHome, tt.own)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateBash(tt.command, testHome, legacyBlockStub)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
	}
}

// TestIsWhitelisted covers the whitelist rules directly (basename matches,
// docs/*.md, absolute .claude//.mneme/ substrings, /tmp scratch, relative
// .claude//.mneme/ prefixes) independent of the Evaluate* entrypoints.
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWhitelisted(tt.path, testHome); got != tt.want {
				t.Errorf("IsWhitelisted(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
