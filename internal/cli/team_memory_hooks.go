package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// teamMemoryHooksMarkerBegin and teamMemoryHooksMarkerEnd are the sentinel
// lines that delimit the mneme-managed block inside a git hook script. The
// remove subcommand only strips the region between these two lines
// (inclusive), so any other content in the hook is preserved.
const (
	teamMemoryHooksMarkerBegin = "# >>> mneme team-memory (SPEC-053) >>>"
	teamMemoryHooksMarkerEnd   = "# <<< mneme team-memory (SPEC-053) <<<"
)

// teamMemoryHooksManagedBlock is the exact content injected between the
// markers. The shebang is prepended separately when the file is created from
// scratch; this block is appended to existing hooks that already carry one.
//
// The command is run detached in background (trailing &) so the git
// operation that triggered the hook is never blocked or slowed by the
// import. Using "$(command -v mneme || echo mneme)" makes the script degrade
// gracefully when mneme is not on PATH rather than causing a syntax error.
const teamMemoryHooksManagedBlock = teamMemoryHooksMarkerBegin + `
# Import team-memory shared knowledge after this git event. Managed by ` + "`mneme team-memory hooks`" + `.
# Skipped during rebase/merge/cherry-pick to avoid storms.
"$(command -v mneme || echo mneme)" team-memory hooks run-import >/dev/null 2>&1 &
` + teamMemoryHooksMarkerEnd

// teamMemoryHooksTargetHooks is the list of git hook file names that the
// install command manages. post-merge fires after every merge (including a
// fast-forward pull); post-checkout fires after branch switches — both are
// the natural "I might have new shared knowledge" moments for a git-native
// vault (SPEC-053 D4).
var teamMemoryHooksTargetHooks = []string{"post-merge", "post-checkout"}

// newTeamMemoryCmd returns the "mneme team-memory" parent command.
func newTeamMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team-memory",
		Short: "Manage git-native team memory sharing",
		Long: `Commands for the git-native team-memory vault (SPEC-053): knowledge shared
between team members through .mneme/shared/ in the repository itself.`,
	}
	cmd.AddCommand(newTeamMemoryEnableCmd())
	cmd.AddCommand(newTeamMemoryHooksCmd())
	cmd.AddCommand(newTeamMemoryImportCmd())
	cmd.AddCommand(newTeamMemoryStatusCmd())
	return cmd
}

// newTeamMemoryHooksCmd returns the "mneme team-memory hooks" subcommand
// group, mirroring the "mneme codegraph hooks" pattern (SPEC-044).
func newTeamMemoryHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage git hooks that import shared team memory",
		Long: `Install or remove git hooks that import team-memory knowledge automatically.

After installation, every git merge (including "git pull") and branch
checkout triggers an import of .mneme/shared/ into the local database in the
background. The git operation itself is never blocked; the import is
detached with &.

The hooks are appended with markers so any pre-existing hook content is
preserved. Running install twice is safe (idempotent).`,
	}
	cmd.AddCommand(
		newTeamMemoryHooksInstallCmd(),
		newTeamMemoryHooksRemoveCmd(),
		newTeamMemoryHooksRunImportCmd(),
	)
	return cmd
}

// newTeamMemoryHooksInstallCmd returns the "mneme team-memory hooks install"
// subcommand. It adds the mneme-managed import block to the post-merge and
// post-checkout hooks of the current repository, creating the hook files and
// the #!/bin/sh shebang if they do not already exist. The operation is
// idempotent: if the block is already present the command succeeds without
// duplicating it.
func newTeamMemoryHooksInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install auto-import git hooks in the current repository",
		Long: `Append the mneme import block to post-merge and post-checkout hooks.

The current working directory must be inside a git repository. Hooks are
located via "git rev-parse --git-path hooks" so custom core.hooksPath
settings and git worktrees are respected.

If a hook file already exists its content is preserved; the mneme block is
appended. Running install a second time is a no-op (the block is not
duplicated). New hook files are created with a #!/bin/sh shebang and 0755
permissions.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("team-memory hooks install: cannot determine cwd: %w", err)
			}

			hooksDir, err := gitHooksDir(cwd)
			if err != nil {
				return fmt.Errorf("team-memory hooks install: %w", err)
			}

			for _, hookName := range teamMemoryHooksTargetHooks {
				hookPath := filepath.Join(hooksDir, hookName)
				if appendErr := appendTeamMemoryMarkedBlock(hookPath); appendErr != nil {
					return fmt.Errorf("team-memory hooks install: %s: %w", hookName, appendErr)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Installed hook: %s\n", hookPath)
			}
			return nil
		},
	}
}

// newTeamMemoryHooksRemoveCmd returns the "mneme team-memory hooks remove"
// subcommand. It removes only the mneme-managed block (delimited by the
// teamMemoryHooksMarkerBegin/End sentinels) from each hook file. The rest of
// the hook is untouched. If a hook has no mneme block the command prints a
// no-op message and exits successfully.
func newTeamMemoryHooksRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the auto-import block from git hooks",
		Long: `Strip the mneme-managed block from post-merge and post-checkout hooks.

Only the region between the mneme markers is removed; all other content in
the hook file is preserved. The hook file itself is not deleted. If no mneme
block is present the command exits successfully without modifying anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("team-memory hooks remove: cannot determine cwd: %w", err)
			}

			hooksDir, err := gitHooksDir(cwd)
			if err != nil {
				return fmt.Errorf("team-memory hooks remove: %w", err)
			}

			for _, hookName := range teamMemoryHooksTargetHooks {
				hookPath := filepath.Join(hooksDir, hookName)
				removed, removeErr := removeTeamMemoryMarkedBlock(hookPath)
				if removeErr != nil {
					return fmt.Errorf("team-memory hooks remove: %s: %w", hookName, removeErr)
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

// newTeamMemoryHooksRunImportCmd returns the hidden "mneme team-memory hooks
// run-import" subcommand. It is invoked by the installed git hooks and must
// never propagate errors (exit 0 always), so git merges and checkouts are
// never affected by import failures. Failures are appended to
// ~/.mneme/team-memory-hooks.log for post-hoc inspection; when the import
// finds potential conflicts (SPEC-053 D6) a summary line is appended there
// too, since the git hook discards stdout/stderr.
//
// The command skips silently when any of the following in-progress git
// operations are detected (checked via <git-dir>/rebase-merge,
// rebase-apply, MERGE_HEAD, CHERRY_PICK_HEAD):
//   - interactive or non-interactive rebase
//   - merge in progress
//   - cherry-pick in progress
//
// This prevents storms of redundant import runs during a multi-commit rebase.
func newTeamMemoryHooksRunImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run-import",
		Short:  "Import team-memory shared knowledge (invoked by git hooks)",
		Hidden: true, // internal sub-command; not shown in --help
		Args:   cobra.NoArgs,
		// RunE must never return an error — callers rely on exit-0-always semantics.
		RunE: func(cmd *cobra.Command, args []string) error {
			runTeamMemoryHooksImport()
			return nil
		},
	}
}

// runTeamMemoryHooksImport performs the actual import. It is a standalone
// function (not a method) so it can be called and its side effects inspected
// in tests without needing a full Cobra invocation. Every failure is logged
// via logTeamMemoryHookEvent and swallowed — the calling git operation must
// never be affected.
func runTeamMemoryHooksImport() {
	cwd, err := os.Getwd()
	if err != nil {
		logTeamMemoryHookEvent("", fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: cannot determine cwd: %v", err)))
		return
	}

	// 1. Detect git-dir and skip if a rebase/merge/cherry-pick is in progress.
	gd, err := gitDir(cwd)
	if err != nil {
		logTeamMemoryHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: git dir: %v", err)))
		return
	}
	if reindexInProgress(gd) {
		return // skip silently during rebase/merge/cherry-pick
	}

	// 2. Resolve repo root — ImportFromShared reads <repoRoot>/.mneme/shared.
	root, err := repoRoot(cwd)
	if err != nil {
		logTeamMemoryHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: repo root: %v", err)))
		return
	}

	// 3. Init service with project detection from cwd.
	svc, cleanup, err := initService()
	if err != nil {
		logTeamMemoryHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: init service: %v", err)))
		return
	}
	defer cleanup()

	result, err := svc.ImportFromShared(context.Background(), root)
	if err != nil {
		logTeamMemoryHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: %v", err)))
		return
	}

	if result.ConflictCandidates > 0 {
		logTeamMemoryHookEvent(cwd, fmt.Sprintf(
			"event=conflict_report count=%d hint=%q",
			result.ConflictCandidates,
			"run `mneme conflicts scan`",
		))
	}
}

// TeamMemoryHooksInstalled reports whether every hook in
// teamMemoryHooksTargetHooks already carries the mneme-managed team-memory
// import block for the repository rooted at repoRoot (SPEC-140 D8) — calcada
// de SDDHooksInstalled (internal/service/sdd_hooks.go:106), placed here
// rather than in internal/service because the hooks install/remove logic
// itself already lives in this file's constants
// (teamMemoryHooksMarkerBegin/End): EnableTeamMemory's own godoc declares
// the service deliberately does no hook I/O so it stays testable without
// touching the filesystem's git-hooks directory.
func TeamMemoryHooksInstalled(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	hooksDir, err := gitHooksDir(repoRoot)
	if err != nil {
		return false
	}
	for _, name := range teamMemoryHooksTargetHooks {
		data, readErr := os.ReadFile(filepath.Join(hooksDir, name))
		if readErr != nil || !strings.Contains(string(data), teamMemoryHooksMarkerBegin) {
			return false
		}
	}
	return true
}

// appendTeamMemoryMarkedBlock appends the mneme-managed block to the hook
// file at hookPath. If the file does not exist it is created with a
// #!/bin/sh shebang. The function is idempotent: if the marker begin line is
// already present the file is not modified. The hook file is always set to
// 0755 (git requires hooks to be executable).
//
// The algorithm itself lives in hookblock.go's appendMarkedHookBlock
// (SPEC-131 D56) — generalized so sdd_hooks.go's own consumer can reuse it
// without duplicating ~40 lines of edge-case handling. This wrapper's own
// constants (teamMemoryHooksMarkerBegin/End) are untouched: they are the
// only way `remove` can find what `install` wrote, in every repository
// that already installed them (W8).
func appendTeamMemoryMarkedBlock(hookPath string) error {
	return appendMarkedHookBlock(hookPath, teamMemoryHooksMarkerBegin, teamMemoryHooksMarkerEnd, teamMemoryHooksManagedBlock)
}

// removeTeamMemoryMarkedBlock removes the mneme-managed block (from
// teamMemoryHooksMarkerBegin to teamMemoryHooksMarkerEnd inclusive) from the
// hook file at hookPath. All content outside the markers is preserved.
// Returns (true, nil) when the block was found and removed, or (false, nil)
// when no block was present (no-op). The file is not deleted even if it
// becomes empty after removal.
//
// See appendTeamMemoryMarkedBlock's godoc: the algorithm is
// hookblock.go's removeMarkedHookBlock (SPEC-131 D56).
func removeTeamMemoryMarkedBlock(hookPath string) (removed bool, err error) {
	return removeMarkedHookBlock(hookPath, teamMemoryHooksMarkerBegin, teamMemoryHooksMarkerEnd)
}

// logTeamMemoryHookEvent appends a single timestamped line to
// ~/.mneme/team-memory-hooks.log. Used both for failures (event=error) and
// for the SPEC-053 D6 conflict-candidate report (event=conflict_report),
// since the installed git hook discards stdout/stderr. Failures to write the
// log itself are silently ignored — logging must never be the reason a git
// operation fails.
func logTeamMemoryHookEvent(cwd, msg string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	logPath := filepath.Join(home, ".mneme", "team-memory-hooks.log")

	line := fmt.Sprintf("[%s] repo=%s %s\n",
		time.Now().UTC().Format(time.RFC3339),
		cwd,
		msg,
	)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
}
