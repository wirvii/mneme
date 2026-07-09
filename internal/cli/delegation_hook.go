package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/install"
)

// newDelegationHookCmd returns the "mneme delegation-hook" subcommand group.
// It manages PROJECT-scoped, opt-in registration of the delegation
// enforcement hook, independent of the GLOBAL registration
// "mneme install claude-code" still performs in ~/.claude/settings.json
// during the agnostic-agents transition (SPEC-052 §5.2/§8.2/§9, EPIC
// agnostic-agents SS-6).
func newDelegationHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delegation-hook",
		Short: "Manage project-scoped delegation-enforcement hook registration",
		Long: `Manage the OPT-IN, per-project registration of the delegation-enforcement
PreToolUse hooks in <repo-root>/.claude/settings.json.

This is independent of the GLOBAL registration "mneme install claude-code"
performs in ~/.claude/settings.json (which every Claude Code session on this
machine picks up regardless of project). A project can opt in here without
affecting any other project on the machine.

Per SPEC-052 D9, this is the mechanism the mneme-init skill offers after
generating implementer subagents (backend/frontend/bug-hunter): a project
with no implementer subagents should stay single-agent and never enable
this hook (same precedent as Codex/SPEC-049, which never installs it).

The bash script itself (enforce_delegation.sh) is not duplicated per
project — it must already exist at ~/.claude/hooks/enforce_delegation.sh,
written once by "mneme install claude-code". Only the settings.json
REGISTRATION becomes project-scoped.

Subcommands:
  enable [path]   Register the hook in <path>/.claude/settings.json (default: cwd).
  disable [path]  Remove the hook registration, leaving everything else untouched.
  status [path]   Report whether the hook is currently registered.`,
	}

	cmd.AddCommand(
		newDelegationHookEnableCmd(),
		newDelegationHookDisableCmd(),
		newDelegationHookStatusCmd(),
	)

	return cmd
}

// delegationHookRepoRoot resolves the target repo root: args[0] when given,
// otherwise the current working directory.
func delegationHookRepoRoot(args []string) (string, error) {
	if len(args) == 1 {
		return filepath.Abs(args[0])
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	return cwd, nil
}

func newDelegationHookEnableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable [path]",
		Short: "Register the delegation-enforcement hook for a project",
		Long: `Merges the two delegation-enforcement PreToolUse entries (the Go rules hook
and the bash enforce_delegation.sh script) into <path>/.claude/settings.json.
Idempotent — running it twice does not duplicate entries.`,
		Example: `  mneme delegation-hook enable
  mneme delegation-hook enable /path/to/repo`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := delegationHookRepoRoot(args)
			if err != nil {
				return fmt.Errorf("delegation-hook enable: %w", err)
			}
			path, err := install.EnableProjectDelegationHook(root)
			if err != nil {
				return fmt.Errorf("delegation-hook enable: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enabled: %s\n", path)
			return nil
		},
	}
	return cmd
}

func newDelegationHookDisableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable [path]",
		Short: "Remove the delegation-enforcement hook registration for a project",
		Long: `Removes the two delegation-enforcement PreToolUse entries from
<path>/.claude/settings.json, leaving every other hook entry and setting
untouched. A missing file, or one that never had the hook registered, is a
no-op success.`,
		Example: `  mneme delegation-hook disable
  mneme delegation-hook disable /path/to/repo`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := delegationHookRepoRoot(args)
			if err != nil {
				return fmt.Errorf("delegation-hook disable: %w", err)
			}
			path, err := install.DisableProjectDelegationHook(root)
			if err != nil {
				return fmt.Errorf("delegation-hook disable: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled: %s\n", path)
			return nil
		},
	}
	return cmd
}

func newDelegationHookStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [path]",
		Short: "Report whether the delegation-enforcement hook is registered for a project",
		Example: `  mneme delegation-hook status
  mneme delegation-hook status /path/to/repo`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := delegationHookRepoRoot(args)
			if err != nil {
				return fmt.Errorf("delegation-hook status: %w", err)
			}
			enabled, path, err := install.ProjectDelegationHookStatus(root)
			if err != nil {
				return fmt.Errorf("delegation-hook status: %w", err)
			}
			state := "disabled"
			if enabled {
				state = "enabled"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", state, path)
			return nil
		},
	}
	return cmd
}
