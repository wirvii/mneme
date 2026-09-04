package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/codegraph"
)

// TestCodegraphCmd_Help verifies that all 9 subcommands appear in the
// help output of "mneme codegraph --help".
func TestCodegraphCmd_Help(t *testing.T) {
	cmd := newCodegraphCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})
	// ExecuteC returns the executed command; use Execute for simpler tests.
	err := cmd.Execute()
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	for _, sub := range []string{"index", "status", "search", "callers", "callees", "impact", "node", "trace", "files"} {
		if !strings.Contains(output, sub) {
			t.Errorf("help output missing subcommand %q", sub)
		}
	}
}

// TestCodegraphCmd_SubcmdHelp verifies that each subcommand registers itself
// and responds to --help without error.
func TestCodegraphCmd_SubcmdHelp(t *testing.T) {
	subcommands := []string{
		"index", "status", "search", "callers", "callees",
		"impact", "node", "trace", "files",
	}
	for _, sub := range subcommands {
		t.Run(sub, func(t *testing.T) {
			cmd := newCodegraphCmd()
			buf := new(bytes.Buffer)
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs([]string{sub, "--help"})
			err := cmd.Execute()
			if err != nil {
				t.Fatalf("%s --help returned error: %v", sub, err)
			}
		})
	}
}

// TestCodegraphCmd_IndexFlags verifies that the index subcommand has the
// expected flags: --force, --dry-run, --language.
func TestCodegraphCmd_IndexFlags(t *testing.T) {
	cmd := newCodegraphCmd()
	// Find the "index" subcommand.
	indexCmd, _, err := cmd.Find([]string{"index"})
	if err != nil {
		t.Fatalf("find index subcommand: %v", err)
	}
	for _, flagName := range []string{"force", "dry-run", "language"} {
		if indexCmd.Flags().Lookup(flagName) == nil {
			t.Errorf("index subcommand missing flag --%s", flagName)
		}
	}
}

// TestCodegraphCmd_SearchFlags verifies that the search subcommand has the
// expected flags: --kind, --language, --limit.
func TestCodegraphCmd_SearchFlags(t *testing.T) {
	cmd := newCodegraphCmd()
	searchCmd, _, err := cmd.Find([]string{"search"})
	if err != nil {
		t.Fatalf("find search subcommand: %v", err)
	}
	for _, flagName := range []string{"kind", "language", "limit"} {
		if searchCmd.Flags().Lookup(flagName) == nil {
			t.Errorf("search subcommand missing flag --%s", flagName)
		}
	}
}

// TestCodegraphCmd_TraversalFlags verifies that callers, callees, and impact
// all register --depth and --limit flags.
func TestCodegraphCmd_TraversalFlags(t *testing.T) {
	traversalCmds := []string{"callers", "callees", "impact"}
	for _, name := range traversalCmds {
		t.Run(name, func(t *testing.T) {
			cmd := newCodegraphCmd()
			sub, _, err := cmd.Find([]string{name})
			if err != nil {
				t.Fatalf("find %s subcommand: %v", name, err)
			}
			for _, flagName := range []string{"depth", "limit"} {
				if sub.Flags().Lookup(flagName) == nil {
					t.Errorf("%s subcommand missing flag --%s", name, flagName)
				}
			}
		})
	}
}

// TestCodegraphCmd_TraceFlags verifies that trace registers --max-depth.
func TestCodegraphCmd_TraceFlags(t *testing.T) {
	cmd := newCodegraphCmd()
	traceCmd, _, err := cmd.Find([]string{"trace"})
	if err != nil {
		t.Fatalf("find trace subcommand: %v", err)
	}
	if traceCmd.Flags().Lookup("max-depth") == nil {
		t.Error("trace subcommand missing flag --max-depth")
	}
}

// TestCodegraphCmd_FilesFlags verifies that files registers --language.
func TestCodegraphCmd_FilesFlags(t *testing.T) {
	cmd := newCodegraphCmd()
	filesCmd, _, err := cmd.Find([]string{"files"})
	if err != nil {
		t.Fatalf("find files subcommand: %v", err)
	}
	if filesCmd.Flags().Lookup("language") == nil {
		t.Error("files subcommand missing flag --language")
	}
}

// TestCodegraphCmd_IndexDryRun runs "mneme codegraph index --dry-run <tmpdir>"
// against a temporary directory that contains a .go file. The dry-run must
// succeed without error and print the dry-run notice. The codegraph DB may be
// opened (it is a thin schema-only create), but no nodes are written.
func TestCodegraphCmd_IndexDryRun(t *testing.T) {
	// Create a temp dir with a minimal Go source file so the indexer has
	// something to scan.
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "hello.go")
	if err := os.WriteFile(goFile, []byte("package hello\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write temp go file: %v", err)
	}

	// Create a separate data dir so we don't accidentally touch the real DB.
	dataDir := t.TempDir()

	// Provide the data-dir and project via the persistent flags on a synthetic
	// root so the test remains hermetic.
	root := NewRootCmd()
	outBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"--data-dir", dataDir, "--project", "test-codegraph-cli", "codegraph", "index", "--dry-run", "--language", "go", tmpDir})

	if err := root.Execute(); err != nil {
		t.Fatalf("codegraph index --dry-run: %v", err)
	}

	// The output must contain the dry-run notice.
	output := outBuf.String()
	if !strings.Contains(output, "Dry run") {
		t.Errorf("expected 'Dry run' notice in output, got: %s", output)
	}

	// The output must contain "Index complete" — confirming the summary was printed.
	if !strings.Contains(output, "Index complete") {
		t.Errorf("expected 'Index complete' in output, got: %s", output)
	}
}

// TestCodegraphCmd_NoSubcommandShadowsPersistentPreRun is SPEC-142 AC10:
// cobra runs only the CLOSEST PersistentPreRun/PersistentPreRunE in a command
// tree, so a "codegraph" subcommand that defined its own would silently
// disable the D12 graph-incompleteness notice for that one subcommand. This
// walks the REAL command tree recursively — the population is derived, never
// hand-listed, so a future subcommand can never go unwatched.
func TestCodegraphCmd_NoSubcommandShadowsPersistentPreRun(t *testing.T) {
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			if sub.PersistentPreRunE != nil || sub.PersistentPreRun != nil {
				t.Errorf("subcommand %q defines its own PersistentPreRun(E), which would silently disable the SPEC-142 D12 notice for it", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(newCodegraphCmd())
}

// TestCodegraphCLI_NoticeFollowsProjectFlag is SPEC-142 AC11: the
// PersistentPreRunE notice must resolve EXACTLY the same project the command
// itself answers from — never a slug it resolves independently. Two
// projects ("marked" and "clean") share one --data-dir; only "marked" is
// degraded, so only "--project marked" may print the notice.
func TestCodegraphCLI_NoticeFollowsProjectFlag(t *testing.T) {
	// Isolated, non-git cwd (SPEC-085 rule 3) — this test drives real cobra
	// commands via root.Execute().
	isolatedCwd := t.TempDir()
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if err := os.Chdir(isolatedCwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	dataDir := t.TempDir()
	projectsDir := filepath.Join(dataDir, "projects")

	markedPath := codegraph.DBPath(projectsDir, "marked")
	markedDB, err := codegraph.OpenDB(markedPath)
	if err != nil {
		t.Fatalf("OpenDB(marked): %v", err)
	}
	if err := codegraph.NewStore(markedDB).SetDegradedLanguages([]codegraph.DegradedLanguage{
		{Language: "typescript", Cause: codegraph.CauseToolchainIncompatible, Reason: "fixture"},
	}); err != nil {
		t.Fatalf("SetDegradedLanguages: %v", err)
	}
	markedDB.Close()

	cleanPath := codegraph.DBPath(projectsDir, "clean")
	cleanDB, err := codegraph.OpenDB(cleanPath)
	if err != nil {
		t.Fatalf("OpenDB(clean): %v", err)
	}
	cleanDB.Close()

	runSearch := func(project string) (stdout, stderr string) {
		root := NewRootCmd()
		outBuf := new(bytes.Buffer)
		errBuf := new(bytes.Buffer)
		root.SetOut(outBuf)
		root.SetErr(errBuf)
		root.SetArgs([]string{"--data-dir", dataDir, "--project", project, "codegraph", "search", "foo"})
		_ = root.Execute()
		return outBuf.String(), errBuf.String()
	}

	_, stderrMarked := runSearch("marked")
	if !strings.Contains(stderrMarked, codegraph.NoticeToken) {
		t.Errorf("--project marked: notice missing from stderr: %q", stderrMarked)
	}

	_, stderrClean := runSearch("clean")
	if strings.Contains(stderrClean, codegraph.NoticeToken) {
		t.Errorf("--project clean: notice unexpectedly present in stderr: %q", stderrClean)
	}
}
