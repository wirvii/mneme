package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/runtimecompat"
)

// newInstallCmd returns the "mneme install" subcommand. It configures a
// supported AI coding agent to use mneme for persistent memory by wiring up
// the MCP server, hook handlers, protocol injection, and slash commands.
//
// The command is idempotent: running it multiple times on the same agent
// produces the same result without duplicating entries or clobbering user config.
//
// Behaviour change (v1.8.0): the CLI now uses collect-all error semantics
// (consistent with Install() / the upgrade path). Previously it was fail-fast.
// All steps are attempted; errors are printed as [fail] lines and returned
// as a combined error at the end.
func newInstallCmd() *cobra.Command {
	var flagDryRun bool
	var flagPersonal bool
	var flagForce bool
	var flagSource string
	var flagReinstallHooks bool

	cmd := &cobra.Command{
		Use:   "install <agent>",
		Short: "Configure an AI coding agent to use mneme",
		Long: `Configure a supported AI coding agent to use mneme as its persistent
memory system. This command:

  1. Registers the mneme MCP server in the agent's MCP config
  2. Installs the session-start hook
  3. Injects the memory protocol into the agent's system prompt file
  4. Installs workflow templates and bundled skills
  5. Installs role-enforcement hooks for both supported runtimes

Optionally, pass --personal to also copy your personal Claude Code ecosystem
(agents, commands, templates, hooks, CLAUDE.md, settings.json) from a git
repository or local directory configured in ~/.mneme/config.toml.
The --personal flag has no effect when installing for codex.

Supported agents:
  claude-code  — Claude Code (full multi-agent setup with delegation hook)
  codex        — OpenAI Codex CLI (native project roles and enforcement)

The install is non-destructive and idempotent — running it multiple times
produces the same result without clobbering existing configuration.`,
		Example: `  mneme install claude-code
  mneme install claude-code --dry-run
  mneme install claude-code --reinstall-hooks
  mneme install claude-code --personal
  mneme install claude-code --personal --source /path/to/my/dotfiles
  mneme install claude-code --personal --force
  mneme install codex
  mneme install codex --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			compat, compatErr := runtimecompat.Detect(slug)
			if compatErr != nil {
				return fmt.Errorf("install: verify agent version: %w", compatErr)
			}
			if compat.Installed && !compat.Supported {
				return fmt.Errorf("install: %s %s is below the supported minimum %s; update %s before installing mneme", compat.Command, compat.Version, compat.Minimum, compat.Command)
			}
			if !compat.Installed {
				fmt.Fprintf(os.Stdout, "  [info] %s CLI is not installed; host configuration will be prepared but runtime verification is not run.\n", compat.Command)
			}

			// Resolve the mneme binary path so the MCP config points to the
			// exact binary the user is running, not a PATH lookup at runtime.
			binaryPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("install: resolve binary path: %w", err)
			}

			agent, err := agentBySlug(slug, binaryPath)
			if err != nil {
				return err
			}

			// Resolve personal source eagerly so both the dry-run and the live
			// path share the same opts construction logic.
			var personalSource string
			if flagPersonal {
				personalSource, err = resolvePersonalSource(flagSource)
				if err != nil {
					return err
				}
			}

			opts := install.InstallOptions{
				Force:          flagForce,
				ReinstallHooks: flagReinstallHooks,
				Personal:       flagPersonal,
				PersonalSource: personalSource,
				BinaryPath:     binaryPath,
			}

			if flagDryRun {
				description, dryErr := install.DryRun(agent, opts)
				if dryErr != nil {
					return dryErr
				}
				fmt.Fprintln(os.Stdout, "Dry run — no changes will be made.")
				fmt.Fprintln(os.Stdout, "")
				fmt.Fprintln(os.Stdout, description)

				if flagPersonal {
					home, homeErr := os.UserHomeDir()
					if homeErr != nil {
						return fmt.Errorf("install: home dir: %w", homeErr)
					}
					dryDesc, dryPersonalErr := install.DryRunPersonal(install.PersonalOpts{
						Source:    personalSource,
						ClaudeDir: filepath.Join(home, ".claude"),
						Force:     flagForce,
					})
					if dryPersonalErr != nil {
						return dryPersonalErr
					}
					fmt.Fprintln(os.Stdout, "")
					fmt.Fprintln(os.Stdout, dryDesc)
				}
				return nil
			}

			fmt.Fprintf(os.Stdout, "Installing mneme for %s...\n\n", agent.Name)

			steps := agent.InstallSteps(opts)

			// progress prints [ok] or [fail] for each step.
			// collect-all: we continue on errors and aggregate them.
			var installErrs []string
			progress := func(name, detail string, stepErr error) {
				if stepErr != nil {
					fmt.Fprintf(os.Stdout, "  [fail] %s: %s\n", name, stepErr)
					installErrs = append(installErrs, stepErr.Error())
					return
				}
				if detail != "" {
					fmt.Fprintf(os.Stdout, "  [ok]   %s: %s\n", name, detail)
				} else {
					fmt.Fprintf(os.Stdout, "  [ok]   %s\n", name)
				}

				// Post-step side effects for user experience.
				switch name {
				case "Session hooks":
					// D3b (SPEC-049): Codex hooks require explicit trust from the user.
					// If they have not been trusted via /hooks in the Codex TUI, the
					// session-lifecycle discipline in AGENTS.md (§5) covers memory hygiene
					// without automation. Emit a reminder only for the codex slug.
					if slug == "codex" {
						fmt.Fprintln(os.Stdout, "")
						fmt.Fprintln(os.Stdout, "  [info] Codex hooks require trust before they run automatically.")
						fmt.Fprintln(os.Stdout, "  [info] Open the Codex TUI and run /hooks to review and trust them.")
						fmt.Fprintln(os.Stdout, "  [info] Until then, follow the memory lifecycle in ~/.codex/AGENTS.md §5.")
					}
				case "Delegation hook (reinstall)":
					fmt.Fprintln(os.Stdout, "")
					fmt.Fprintln(os.Stdout, "Migration complete. Your hooks have been updated.")
					fmt.Fprintln(os.Stdout, "")
					fmt.Fprintln(os.Stdout, "To recreate your protected paths as rules, run:")
					fmt.Fprintln(os.Stdout, "")
					fmt.Fprintln(os.Stdout, `  mneme save --type rule --severity block \`)
					fmt.Fprintln(os.Stdout, `    --applies-to "tool:Edit+cmd/**" --applies-to "tool:Write+cmd/**" --applies-to "tool:MultiEdit+cmd/**" \`)
					fmt.Fprintln(os.Stdout, `    --applies-to "tool:Edit+internal/**" --applies-to "tool:Write+internal/**" --applies-to "tool:MultiEdit+internal/**" \`)
					fmt.Fprintln(os.Stdout, `    --title "Delegation: protect source paths" \`)
					fmt.Fprintln(os.Stdout, `    "Delegate code edits in protected paths to the appropriate subagent."`)
					fmt.Fprintln(os.Stdout, "")
					fmt.Fprintln(os.Stdout, "Your old config.toml [delegation] section is still active for the legacy hook.")
					fmt.Fprintln(os.Stdout, "Once you've created rules and verified they work, you can set delegation.enabled=false in config.toml.")
				}
			}

			install.RunInstallSteps(steps, progress)

			fmt.Fprintln(os.Stdout, "")
			fmt.Fprintf(os.Stdout, "Done. Restart %s for changes to take effect.\n", agent.Name)

			if len(installErrs) > 0 {
				return fmt.Errorf("install: some steps failed: %s", strings.Join(installErrs, "; "))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would be configured without making changes")
	cmd.Flags().BoolVar(&flagPersonal, "personal", false,
		"Install personal ecosystem from configured source")
	cmd.Flags().BoolVar(&flagForce, "force", false,
		"Overwrite existing files (settings.json is always merged, never overwritten)")
	cmd.Flags().StringVar(&flagSource, "source", "",
		"Override personal ecosystem source (git URL or local path)")
	cmd.Flags().BoolVar(&flagReinstallHooks, "reinstall-hooks", false,
		"Replace all existing PreToolUse hook entries with mneme hook pre-tool-use (migration from enforce-delegation)")

	return cmd
}

// resolvePersonalSource returns the source to use for the personal ecosystem.
// It returns flagSource if non-empty, otherwise reads Personal.Source from the
// default config. Returns an error with instructions when no source is found.
func resolvePersonalSource(flagSource string) (string, error) {
	if flagSource != "" {
		return flagSource, nil
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return "", fmt.Errorf("install: load config: %w", err)
	}

	if cfg.Personal.Source != "" {
		return cfg.Personal.Source, nil
	}

	return "", fmt.Errorf(`install: --personal requires a source.

Configure it in ~/.mneme/config.toml:

  [personal]
  source = "git@github.com:user/dotfiles-claude.git"

Or pass --source directly:

  mneme install claude-code --personal --source /path/to/ecosystem`)
}

// agentBySlug returns the *install.Agent for the given slug. It returns a
// descriptive error when the slug is not recognised.
func agentBySlug(slug, binaryPath string) (*install.Agent, error) {
	switch slug {
	case "claude-code":
		return install.ClaudeCode(binaryPath), nil
	case "codex":
		return install.Codex(binaryPath), nil
	default:
		return nil, fmt.Errorf("install: unknown agent %q — supported agents: claude-code, codex", slug)
	}
}
