package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/codegraph"
	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/project"
	"github.com/juanftp/mneme/internal/service"
)

// hooksMarkerBegin and hooksMarkerEnd are the sentinel lines that delimit the
// mneme-managed block inside a git hook script. The remove subcommand only
// strips the region between these two lines (inclusive), so any other content
// in the hook is preserved.
const (
	hooksMarkerBegin = "# >>> mneme codegraph (SPEC-044) >>>"
	hooksMarkerEnd   = "# <<< mneme codegraph (SPEC-044) <<<"
)

// hooksManagedBlock is the exact content injected between the markers. The
// shebang is prepended separately when the file is created from scratch; this
// block is appended to existing hooks that already carry a shebang.
//
// The command is run detached in background (trailing &) so the git operation
// that triggered the hook is never blocked or slowed by the re-index. Using
// "$(command -v mneme || echo mneme)" makes the script degrade gracefully when
// mneme is not on PATH rather than causing a syntax error.
const hooksManagedBlock = hooksMarkerBegin + `
# Auto-reindex the code graph after this git event. Managed by ` + "`mneme codegraph hooks`" + `.
# Skipped during rebase/merge/cherry-pick to avoid storms.
"$(command -v mneme || echo mneme)" codegraph hooks run-reindex >/dev/null 2>&1 &
` + hooksMarkerEnd

// hooksTargetHooks is the list of git hook file names that the install command
// manages. post-commit fires after every commit; post-checkout fires after
// branch switches and file-level checkouts.
var hooksTargetHooks = []string{"post-commit", "post-checkout"}

// newCodegraphHooksCmd returns the "mneme codegraph hooks" parent command that
// groups the install, remove, and (hidden) run-reindex subcommands.
func newCodegraphHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage git hooks for automatic code-graph re-indexing",
		Long: `Install or remove git hooks that keep the code graph fresh automatically.

After installation, every git commit and branch checkout triggers an incremental
re-index of the code graph in the background. The git operation itself is never
blocked; the re-index is detached with &.

The hooks are appended with markers so any pre-existing hook content is
preserved. Running install twice is safe (idempotent).`,
	}
	cmd.AddCommand(
		newCodegraphHooksInstallCmd(),
		newCodegraphHooksRemoveCmd(),
		newCodegraphHooksRunReindexCmd(),
	)
	return cmd
}

// newCodegraphHooksInstallCmd returns the "mneme codegraph hooks install"
// subcommand. It adds the mneme-managed re-index block to the post-commit and
// post-checkout hooks of the current repository, creating the hook files and
// the #!/bin/sh shebang if they do not already exist. The operation is
// idempotent: if the block is already present the command succeeds without
// duplicating it.
func newCodegraphHooksInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install auto-reindex git hooks in the current repository",
		Long: `Append the mneme re-index block to post-commit and post-checkout hooks.

The current working directory must be inside a git repository. Hooks are
located via "git rev-parse --git-path hooks" so custom core.hooksPath settings
and git worktrees are respected.

If a hook file already exists its content is preserved; the mneme block is
appended. Running install a second time is a no-op (the block is not
duplicated). New hook files are created with a #!/bin/sh shebang and 0755
permissions.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("codegraph hooks install: cannot determine cwd: %w", err)
			}

			hooksDir, err := gitHooksDir(cwd)
			if err != nil {
				return fmt.Errorf("codegraph hooks install: %w", err)
			}

			for _, hookName := range hooksTargetHooks {
				hookPath := filepath.Join(hooksDir, hookName)
				if appendErr := appendMarkedBlock(hookPath); appendErr != nil {
					return fmt.Errorf("codegraph hooks install: %s: %w", hookName, appendErr)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Installed hook: %s\n", hookPath)
			}
			return nil
		},
	}
}

// newCodegraphHooksRemoveCmd returns the "mneme codegraph hooks remove"
// subcommand. It removes only the mneme-managed block (delimited by the
// hooksMarkerBegin/End sentinels) from each hook file. The rest of the hook
// is untouched. If a hook has no mneme block the command prints a no-op
// message and exits successfully.
func newCodegraphHooksRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the auto-reindex block from git hooks",
		Long: `Strip the mneme-managed block from post-commit and post-checkout hooks.

Only the region between the mneme markers is removed; all other content in the
hook file is preserved. The hook file itself is not deleted. If no mneme block
is present the command exits successfully without modifying anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("codegraph hooks remove: cannot determine cwd: %w", err)
			}

			hooksDir, err := gitHooksDir(cwd)
			if err != nil {
				return fmt.Errorf("codegraph hooks remove: %w", err)
			}

			for _, hookName := range hooksTargetHooks {
				hookPath := filepath.Join(hooksDir, hookName)
				removed, removeErr := removeMarkedBlock(hookPath)
				if removeErr != nil {
					return fmt.Errorf("codegraph hooks remove: %s: %w", hookName, removeErr)
				}
				if removed {
					fmt.Fprintf(cmd.OutOrStdout(), "Removed mneme block from: %s\n", hookPath)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "No mneme block found in: %s (no-op)\n", hookPath)
				}
			}
			return nil
		},
	}
}

// newCodegraphHooksRunReindexCmd returns the hidden "mneme codegraph hooks
// run-reindex" subcommand. It is invoked by the installed git hooks and must
// never propagate errors (exit 0 always), so git commits and checkouts are
// never affected by index failures. Index failures are appended to
// ~/.mneme/codegraph-hooks.log for post-hoc inspection.
//
// The command skips silently when any of the following in-progress git
// operations are detected (checked via <git-dir>/rebase-merge,
// rebase-apply, MERGE_HEAD, CHERRY_PICK_HEAD):
//   - interactive or non-interactive rebase
//   - merge in progress
//   - cherry-pick in progress
//
// This prevents storms of redundant re-index runs during a multi-commit rebase.
func newCodegraphHooksRunReindexCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run-reindex",
		Short:  "Run incremental code-graph re-index (invoked by git hooks)",
		Hidden: true, // internal sub-command; not shown in --help
		Args:   cobra.NoArgs,
		// RunE must never return an error — callers rely on exit-0-always semantics.
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runCodegraphHooksReindex(); err != nil {
				// Log but do not propagate — the git operation must not be affected.
				_ = logHookFailure(err)
			}
			return nil
		},
	}
}

// runCodegraphHooksReindex performs the actual incremental re-index. It is a
// standalone function (not a method) so it can be called and its return value
// inspected in tests without needing a full Cobra invocation.
func runCodegraphHooksReindex() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("run-reindex: cannot determine cwd: %w", err)
	}

	// 1. Detect git-dir and skip if a rebase/merge/cherry-pick is in progress.
	gd, err := gitDir(cwd)
	if err != nil {
		return fmt.Errorf("run-reindex: git dir: %w", err)
	}
	if reindexInProgress(gd) {
		return nil // skip silently during rebase/merge/cherry-pick
	}

	// 2. Resolve repo root for indexing.
	root, err := repoRoot(cwd)
	if err != nil {
		return fmt.Errorf("run-reindex: repo root: %w", err)
	}

	// 3. Init service with project detection from cwd.
	svc, err := initCodeGraphServiceForCWD(cwd)
	if err != nil {
		return fmt.Errorf("run-reindex: init service: %w", err)
	}
	defer func() { _ = svc.Close() }()

	// 4. Incremental index (Force=false so unchanged files are skipped).
	_, err = svc.Index(codegraph.IndexOptions{
		RootDir: root,
		Force:   false,
	})
	return err
}

// initCodeGraphServiceForCWD is a variant of initCodeGraphService that derives
// the project slug from an explicit cwd rather than os.Getwd(). This makes the
// run-reindex path explicit and testable.
func initCodeGraphServiceForCWD(cwd string) (*service.CodeGraphService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cfgPath := filepath.Join(home, ".mneme", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}

	slug := flagProject
	if slug == "" {
		det := project.NewDetector(cwd)
		detected, _ := det.DetectProject()
		slug = detected
	}

	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
	return service.NewCodeGraphService(projectsDir, slug)
}

// gitHooksDir resolves the hooks directory for the repository rooted at (or
// containing) cwd. It uses "git rev-parse --git-path hooks" so that custom
// core.hooksPath configurations and git worktrees are handled correctly. If
// cwd is not inside a git repository an error is returned.
func gitHooksDir(cwd string) (string, error) {
	out, err := runGitCmd(cwd, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", fmt.Errorf("not a git repository (or any of the parent directories): %w", err)
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		// git may return a path relative to cwd when inside a worktree.
		dir = filepath.Join(cwd, dir)
	}
	return dir, nil
}

// gitDir resolves the .git directory path for the repository containing cwd
// using "git rev-parse --git-dir". The result is needed to check for in-progress
// rebase/merge/cherry-pick state files.
func gitDir(cwd string) (string, error) {
	out, err := runGitCmd(cwd, "rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return dir, nil
}

// repoRoot returns the top-level directory of the repository containing cwd,
// using "git rev-parse --show-toplevel". This is the directory that is passed
// to the indexer as its root when the hooks fire.
func repoRoot(cwd string) (string, error) {
	out, err := runGitCmd(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// runGitCmd runs git with the provided arguments in the given working directory
// and returns the combined stdout output. A non-zero exit code is treated as an
// error with the stderr message included.
func runGitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// appendMarkedBlock appends the mneme-managed block to the hook file at
// hookPath. If the file does not exist it is created with a #!/bin/sh shebang.
// The function is idempotent: if the marker begin line is already present the
// file is not modified. The hook file is always set to 0755 (git requires hooks
// to be executable).
func appendMarkedBlock(hookPath string) error {
	// Read existing content (if any).
	existing, readErr := os.ReadFile(hookPath)
	var content string
	if readErr == nil {
		content = string(existing)
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read hook file: %w", readErr)
	}

	// Idempotence: do not append if the begin marker is already present.
	if strings.Contains(content, hooksMarkerBegin) {
		return nil
	}

	// Build the new content.
	var sb strings.Builder
	if content == "" {
		// New file: add shebang.
		sb.WriteString("#!/bin/sh\n")
	} else {
		sb.WriteString(content)
		// Ensure there is a newline before our block.
		if !strings.HasSuffix(content, "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(hooksManagedBlock)
	sb.WriteByte('\n')

	// Write atomically enough for our use case: write and chmod.
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks directory: %w", err)
	}
	if err := os.WriteFile(hookPath, []byte(sb.String()), 0o755); err != nil {
		return fmt.Errorf("write hook file: %w", err)
	}
	// Ensure executable bit is set even when the file already existed.
	return os.Chmod(hookPath, 0o755)
}

// removeMarkedBlock removes the mneme-managed block (from hooksMarkerBegin to
// hooksMarkerEnd inclusive) from the hook file at hookPath. All content outside
// the markers is preserved. Returns (true, nil) when the block was found and
// removed, or (false, nil) when no block was present (no-op). The file is not
// deleted even if it becomes empty after removal.
func removeMarkedBlock(hookPath string) (removed bool, err error) {
	data, readErr := os.ReadFile(hookPath)
	if os.IsNotExist(readErr) {
		return false, nil // file absent → nothing to remove
	}
	if readErr != nil {
		return false, fmt.Errorf("read hook file: %w", readErr)
	}

	content := string(data)
	beginIdx := strings.Index(content, hooksMarkerBegin)
	if beginIdx < 0 {
		return false, nil // no block present
	}

	endIdx := strings.Index(content, hooksMarkerEnd)
	if endIdx < 0 {
		// Malformed: begin marker present but no end marker. Remove from begin to EOF.
		endIdx = len(content) - len(hooksMarkerEnd)
	}
	// endIdx points to the start of the end marker; advance past the full line.
	afterEnd := endIdx + len(hooksMarkerEnd)
	if afterEnd < len(content) && content[afterEnd] == '\n' {
		afterEnd++ // consume the trailing newline of the end marker line
	}

	newContent := content[:beginIdx] + content[afterEnd:]

	// Trim a spurious leading blank line that may appear if the block was at the
	// very start of the file (just after the shebang newline).
	newContent = strings.TrimRight(newContent, "\n")
	if newContent != "" {
		newContent += "\n"
	}

	if writeErr := os.WriteFile(hookPath, []byte(newContent), 0o755); writeErr != nil {
		return false, fmt.Errorf("write hook file: %w", writeErr)
	}
	return true, nil
}

// reindexInProgress reports whether any in-progress git operation that spawns
// many post-commit/post-checkout events is currently active. It checks for the
// presence of sentinel files inside the git directory that git creates during
// rebase, merge, and cherry-pick operations.
//
// Returning true causes run-reindex to skip the index without error, preventing
// storms of redundant re-index runs during an interactive rebase that may fire
// dozens of post-checkout hooks.
func reindexInProgress(gitDir string) bool {
	sentinels := []string{
		"rebase-merge",       // git rebase -i (interactive) in progress
		"rebase-apply",       // git rebase (non-interactive) / git am in progress
		"MERGE_HEAD",         // git merge in progress
		"CHERRY_PICK_HEAD",   // git cherry-pick in progress
	}
	for _, s := range sentinels {
		if _, err := os.Stat(filepath.Join(gitDir, s)); err == nil {
			return true
		}
	}
	return false
}

// logHookFailure appends a single timestamped line to ~/.mneme/codegraph-hooks.log.
// The caller is responsible for passing the error value; the function silently
// ignores any write failure so it never itself causes a non-zero exit.
func logHookFailure(cause error) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil // fail silently
	}
	logPath := filepath.Join(home, ".mneme", "codegraph-hooks.log")

	cwd, _ := os.Getwd() // best-effort

	line := fmt.Sprintf("[%s] repo=%s error=%v\n",
		time.Now().UTC().Format(time.RFC3339),
		cwd,
		cause,
	)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil // fail silently
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
	return nil
}
