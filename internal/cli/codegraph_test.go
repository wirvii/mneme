package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/service"
)

// TestCodegraphCmd_Help verifies that all query and index subcommands appear in the
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
	for _, sub := range []string{"index", "status", "search", "callers", "callees", "impact", "affected", "node", "trace", "files"} {
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
		"impact", "affected", "node", "trace", "files",
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

func TestCodegraphAffected_CLIParity(t *testing.T) {
	dataDir := t.TempDir()
	const slug = "test-codegraph-affected-cli"
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "changed.go"), []byte("package changed\n\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, err := service.NewCodeGraphService(filepath.Join(dataDir, "projects"), slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(codegraph.IndexOptions{RootDir: sourceDir}); err != nil {
		_ = svc.Close()
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	stdout := new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"--data-dir", dataDir, "--project", slug, "codegraph", "affected", "--path", "changed.go", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("codegraph affected: %v", err)
	}
	var result codegraph.AffectedResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if got := strings.Join(result.Inputs, ","); got != "changed.go" {
		t.Errorf("inputs = %q, want changed.go", got)
	}

	cmd := newCodegraphCmd()
	affected, _, err := cmd.Find([]string{"affected"})
	if err != nil {
		t.Fatalf("find affected: %v", err)
	}
	for _, flag := range []string{"path", "base", "head", "depth", "limit", "json"} {
		if affected.Flags().Lookup(flag) == nil {
			t.Errorf("affected subcommand missing --%s", flag)
		}
	}
}

func TestCodegraphAffected_HumanOutputNamesIncompleteResults(t *testing.T) {
	result := codegraph.AffectedResult{
		Inputs: []string{"changed.go"}, MissingPaths: []string{"gone.go"},
		UntrackedPaths: []string{"new.go"}, Total: 5, Truncated: true,
		Stale: true, MissingGraph: true, IndexedSHA: "old",
	}
	buf := new(bytes.Buffer)
	printCodegraphAffectedHuman(buf, result)
	for _, phrase := range []string{"graph is missing", "graph is stale", "Missing paths", "Untracked paths", "truncated"} {
		if !strings.Contains(buf.String(), phrase) {
			t.Errorf("human output missing %q:\n%s", phrase, buf.String())
		}
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

// TestCodegraphIndex_StampsHeadOnlyForCompleteSuccess protects freshness: a
// revision must describe a complete, successful scan of the repository root,
// never a dry run, partial path, or degraded result.
func TestCodegraphIndex_StampsHeadOnlyForCompleteSuccess(t *testing.T) {
	t.Run("same directory identity ignores path spelling", func(t *testing.T) {
		repo := newCodegraphIndexGitRepo(t)
		spelledDifferently := repo + string(filepath.Separator) + "."
		if repo == spelledDifferently {
			t.Fatal("test paths unexpectedly have identical spelling")
		}
		if !shouldStampCodegraphIndex(false, spelledDifferently, repo, "abc123", &codegraph.IndexResult{}) {
			t.Fatal("same directory with different path spelling was not eligible to stamp HEAD")
		}
	})

	t.Run("complete repository root stamps HEAD", func(t *testing.T) {
		repo := newCodegraphIndexGitRepo(t)
		dataDir := t.TempDir()

		root := NewRootCmd()
		root.SetOut(new(bytes.Buffer))
		root.SetErr(new(bytes.Buffer))
		root.SetArgs([]string{"--data-dir", dataDir, "--project", "codegraph-index-stamp", "codegraph", "index", repo})
		if err := root.Execute(); err != nil {
			t.Fatalf("codegraph index: %v", err)
		}

		head := gitHead(t, repo)
		if got, ok := codegraphLastIndexedSHA(t, dataDir, "codegraph-index-stamp"); !ok || got != head {
			t.Errorf("last indexed SHA = %q, want HEAD %q", got, head)
		}
	})

	for _, tc := range []struct {
		name       string
		args       func(t *testing.T, repo string) []string
		prepare    func(t *testing.T, repo string)
		wantErr    bool
		wantOutput string
	}{
		{
			name: "dry run",
			args: func(_ *testing.T, repo string) []string {
				return []string{"--dry-run", repo}
			},
		},
		{
			name: "subdirectory",
			prepare: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(repo, "partial"), 0o755); err != nil {
					t.Fatalf("create partial directory: %v", err)
				}
				if err := os.WriteFile(filepath.Join(repo, "partial", "partial.go"), []byte("package partial\n"), 0o644); err != nil {
					t.Fatalf("write partial Go source: %v", err)
				}
			},
			args: func(_ *testing.T, repo string) []string {
				return []string{filepath.Join(repo, "partial")}
			},
		},
		{
			name: "path outside Git",
			args: func(t *testing.T, _ string) []string {
				t.Helper()
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "outside.go"), []byte("package outside\n"), 0o644); err != nil {
					t.Fatalf("write outside Go source: %v", err)
				}
				return []string{outside}
			},
		},
		{
			name: "errored file",
			prepare: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "broken.go"), []byte("package broken\nfunc ("), 0o644); err != nil {
					t.Fatalf("write broken Go source: %v", err)
				}
			},
			args: func(_ *testing.T, repo string) []string {
				return []string{repo}
			},
			wantOutput: "Files errored:  1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newCodegraphIndexGitRepo(t)
			if tc.prepare != nil {
				tc.prepare(t, repo)
			}
			dataDir := t.TempDir()
			out := new(bytes.Buffer)
			root := NewRootCmd()
			root.SetOut(out)
			root.SetErr(new(bytes.Buffer))
			root.SetArgs(append([]string{"--data-dir", dataDir, "--project", "codegraph-index-no-stamp", "codegraph", "index"}, tc.args(t, repo)...))
			err := root.Execute()
			if tc.wantErr && err == nil {
				t.Fatal("codegraph index succeeded, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("codegraph index: %v", err)
			}
			if tc.wantOutput != "" && !strings.Contains(out.String(), tc.wantOutput) {
				t.Errorf("index output = %q, want %q", out.String(), tc.wantOutput)
			}
			if got, ok := codegraphLastIndexedSHA(t, dataDir, "codegraph-index-no-stamp"); ok {
				t.Errorf("last indexed SHA = %q, want no stamp", got)
			}
		})
	}

	realRoot := t.TempDir()
	partialRoot := filepath.Join(realRoot, "partial")
	if err := os.Mkdir(partialRoot, 0o755); err != nil {
		t.Fatalf("create partial root: %v", err)
	}
	if !shouldStampCodegraphIndex(false, realRoot, realRoot, "abc123", &codegraph.IndexResult{}) {
		t.Fatal("complete index of an existing repository root was not eligible to stamp HEAD")
	}

	degradedLanguages := []codegraph.DegradedLanguage{{Language: "typescript"}}
	cases := []struct {
		name      string
		dryRun    bool
		repoRoot  string
		requested string
		head      string
		result    *codegraph.IndexResult
	}{
		{
			name:      "dry run",
			dryRun:    true,
			repoRoot:  realRoot,
			requested: realRoot,
			head:      "abc123",
			result:    &codegraph.IndexResult{},
		},
		{
			name:      "subdirectory",
			repoRoot:  realRoot,
			requested: partialRoot,
			head:      "abc123",
			result:    &codegraph.IndexResult{},
		},
		{
			name:      "empty HEAD",
			repoRoot:  realRoot,
			requested: realRoot,
			result:    &codegraph.IndexResult{},
		},
		{
			name:      "index error",
			repoRoot:  realRoot,
			requested: realRoot,
			head:      "abc123",
		},
		{
			name:      "errored files",
			repoRoot:  realRoot,
			requested: realRoot,
			head:      "abc123",
			result:    &codegraph.IndexResult{FilesErrored: 1},
		},
		{
			name:      "degraded files",
			repoRoot:  realRoot,
			requested: realRoot,
			head:      "abc123",
			result:    &codegraph.IndexResult{FilesDegraded: 1},
		},
		{
			name:      "degraded languages",
			repoRoot:  realRoot,
			requested: realRoot,
			head:      "abc123",
			result:    &codegraph.IndexResult{DegradedLanguages: degradedLanguages},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if shouldStampCodegraphIndex(tc.dryRun, tc.requested, tc.repoRoot, tc.head, tc.result) {
				t.Fatal("incomplete index was eligible to stamp HEAD")
			}
		})
	}
}

func newCodegraphIndexGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "hello.go"), []byte("package hello\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write Go source: %v", err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"add", "hello.go"},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	return repo
}

func gitHead(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func codegraphLastIndexedSHA(t *testing.T, dataDir, slug string) (string, bool) {
	t.Helper()
	db, err := codegraph.OpenDB(codegraph.DBPath(filepath.Join(dataDir, "projects"), slug))
	if err != nil {
		t.Fatalf("open codegraph DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var sha string
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM project_metadata WHERE key = 'last_indexed_sha'`).Scan(&count); err != nil {
		t.Fatalf("get last indexed SHA: %v", err)
	}
	if count == 0 {
		return "", false
	}
	if err := db.DB.QueryRow(`SELECT value FROM project_metadata WHERE key = 'last_indexed_sha'`).Scan(&sha); err != nil {
		t.Fatalf("get last indexed SHA: %v", err)
	}
	return sha, true
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
