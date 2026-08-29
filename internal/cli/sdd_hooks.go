package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// newSDDHooksCmd returns the "mneme sdd hooks" subcommand group (SPEC-131
// D58), mirroring the "mneme team-memory hooks" pattern (SPEC-053): the
// actual install/remove/run-import work is a thin wrapper over
// SDDService.InstallSDDHooks/RemoveSDDHooks/ImportSDDFromRepo — the same
// "CLI renders, service does the work" shape every other `mneme sdd`
// subcommand already follows.
func newSDDHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage git hooks that import this repository's SDD backlog/specs",
		Long: `Install or remove git hooks that import the repository's SDD backlog/specs
(the local database picks up whatever .mneme/sdd/ already carries) automatically.

After installation, every git merge (including "git pull") and branch
checkout triggers an import in the background. The git operation itself is
never blocked; the import is detached with &.

Two people creating the same correlative at the same time produce a
collision this import detects and reports, but does not yet resolve
(reconciling is BL-202) — see "mneme sdd import" and "mneme sdd status".

The hooks are appended with markers so any pre-existing hook content
(including team-memory's own block, if installed) is preserved. Running
install twice is safe (idempotent).`,
	}
	cmd.AddCommand(
		newSDDHooksInstallCmd(),
		newSDDHooksRemoveCmd(),
		newSDDHooksRunImportCmd(),
	)
	return cmd
}

// newSDDHooksInstallCmd returns "mneme sdd hooks install".
func newSDDHooksInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install auto-import git hooks in the current repository",
		Long: `Append the mneme-managed SDD import block to post-merge and post-checkout
hooks. The current working directory must be inside a git repository.
Running install a second time is a no-op (the block is not duplicated).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			if err := svc.InstallSDDHooks(svc.RepoDir()); err != nil {
				return fmt.Errorf("sdd hooks install: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed SDD import hooks in %s.\n", svc.RepoDir())
			return nil
		},
	}
}

// newSDDHooksRemoveCmd returns "mneme sdd hooks remove".
func newSDDHooksRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the auto-import block from git hooks",
		Long: `Strip ONLY the mneme-managed SDD block from post-merge and post-checkout.
Any other content in the hook file — including team-memory's own block, if
installed — is left byte-for-byte untouched.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			if err := svc.RemoveSDDHooks(svc.RepoDir()); err != nil {
				return fmt.Errorf("sdd hooks remove: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed SDD import hooks from %s.\n", svc.RepoDir())
			return nil
		},
	}
}

// newSDDHooksRunImportCmd returns the hidden "mneme sdd hooks run-import"
// subcommand invoked by the installed git hooks. It must NEVER propagate
// an error (exit 0 always, D62) so a git pull/checkout is never affected
// by an import failure.
func newSDDHooksRunImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run-import",
		Short:  "Import this repository's SDD backlog/specs (invoked by git hooks)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runSDDHooksImport()
			return nil
		},
	}
}

// runSDDHooksImport performs the actual import. It is a standalone
// function (not a method) so it can be called and its side effects
// inspected in tests without needing a full Cobra invocation — mirroring
// runTeamMemoryHooksImport's own shape. Every failure is logged via
// logSDDHookEvent and swallowed: the calling git operation must never be
// affected (D62).
func runSDDHooksImport() {
	cwd, err := os.Getwd()
	if err != nil {
		logSDDHookEvent("", fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: cannot determine cwd: %v", err)))
		return
	}

	// 1. Detect git-dir and skip if a rebase/merge/cherry-pick is in progress.
	gd, err := gitDir(cwd)
	if err != nil {
		logSDDHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: git dir: %v", err)))
		return
	}
	if reindexInProgress(gd) {
		return // skip silently during rebase/merge/cherry-pick
	}

	// 2. Resolve repo root — ImportSDDFromRepo reads <repoRoot>/.mneme/sdd.
	root, err := repoRoot(cwd)
	if err != nil {
		logSDDHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: repo root: %v", err)))
		return
	}

	// 3. Init service with project detection from cwd.
	svc, cleanup, err := initSDDService()
	if err != nil {
		logSDDHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: init service: %v", err)))
		return
	}
	defer cleanup()

	result, err := svc.ImportSDDFromRepo(context.Background(), root, true)
	if err != nil {
		logSDDHookEvent(cwd, fmt.Sprintf("event=error error=%q", fmt.Sprintf("run-import: %v", err)))
		return
	}

	if len(result.Skipped) > 0 {
		logSDDHookEvent(cwd, fmt.Sprintf(
			"event=skipped count=%d hint=%q",
			len(result.Skipped),
			"run `mneme sdd status` or `mneme sdd import` for details",
		))
	}
}

// logSDDHookEvent appends a single timestamped line to
// ~/.mneme/sdd-hooks.log — mirroring logTeamMemoryHookEvent's own shape
// (team_memory_hooks.go), since the installed git hook discards
// stdout/stderr. This file is constancy of what happened, NEVER a source
// of truth (D54): `mneme sdd status` never reads it, and deleting it
// changes no answer. Failures to write the log itself are silently
// ignored — logging must never be the reason a git operation fails.
func logSDDHookEvent(cwd, msg string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	logPath := filepath.Join(home, ".mneme", "sdd-hooks.log")

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
