package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/install"
)

// newInstallCmd returns the "mneme install" subcommand. It configures a
// supported AI coding agent to use mneme for persistent memory by wiring up
// the MCP server, hook handlers, protocol injection, and slash commands.
//
// The command is idempotent: running it multiple times on the same agent
// produces the same result without duplicating entries or clobbering user config.
func newInstallCmd() *cobra.Command {
	var flagDryRun  bool
	var flagPersonal bool
	var flagForce   bool
	var flagSource  string

	cmd := &cobra.Command{
		Use:   "install <agent>",
		Short: "Configure an AI coding agent to use mneme",
		Long: `Configure a supported AI coding agent to use mneme as its persistent
memory system. This command:

  1. Registers the mneme MCP server in the agent's MCP config
  2. Installs session-start and session-end hooks
  3. Injects the memory protocol into the agent's system prompt file
  4. Installs the /mneme-init slash command

Optionally, pass --personal to also copy your personal Claude Code ecosystem
(agents, commands, templates, hooks, CLAUDE.md, settings.json) from a git
repository or local directory configured in ~/.mneme/config.toml.

Supported agents: claude-code

The install is non-destructive and idempotent — running it multiple times
produces the same result without clobbering existing configuration.`,
		Example: `  mneme install claude-code
  mneme install claude-code --dry-run
  mneme install claude-code --personal
  mneme install claude-code --personal --source /path/to/my/dotfiles
  mneme install claude-code --personal --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]

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

			if flagDryRun {
				description, err := install.DryRun(agent, binaryPath)
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, "Dry run — no changes will be made.")
				fmt.Fprintln(os.Stdout, "")
				fmt.Fprintln(os.Stdout, description)

				if flagPersonal {
					source, err := resolvePersonalSource(flagSource)
					if err != nil {
						return err
					}
					home, err := os.UserHomeDir()
					if err != nil {
						return fmt.Errorf("install: home dir: %w", err)
					}
					dryDesc, err := install.DryRunPersonal(install.PersonalOpts{
						Source:    source,
						ClaudeDir: filepath.Join(home, ".claude"),
						Force:     flagForce,
					})
					if err != nil {
						return err
					}
					fmt.Fprintln(os.Stdout, "")
					fmt.Fprintln(os.Stdout, dryDesc)
				}
				return nil
			}

			fmt.Fprintf(os.Stdout, "Installing mneme for %s...\n\n", agent.Name)

			if err := install.WriteMCPConfig(agent, binaryPath); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "  [ok] MCP server registered")

			if err := install.PatchHooks(agent); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "  [ok] Session hooks installed")

			if err := install.InjectProtocol(agent); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "  [ok] Memory protocol injected")

			if err := install.WriteCommands(agent); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "  [ok] Slash commands installed")

			if agent.Agents != nil {
				if err := install.WriteAgents(agent); err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, "  [ok] Agent profiles installed")
			}

			if agent.Templates != nil {
				if err := install.WriteTemplates(agent); err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, "  [ok] Workflow templates installed")
			}

			if agent.DelegationHook != nil {
				if err := install.PatchDelegationHook(agent); err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, "  [ok] Delegation enforcement hook installed")
			}

			if err := install.CreateWorkflowDirs(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "  [ok] Workflow directories created")

			if flagPersonal {
				source, err := resolvePersonalSource(flagSource)
				if err != nil {
					return err
				}
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("install: home dir: %w", err)
				}

				fmt.Fprintln(os.Stdout, "")
				fmt.Fprintln(os.Stdout, "Installing personal ecosystem...")
				fmt.Fprintln(os.Stdout, "")

				result, err := install.InstallPersonal(install.PersonalOpts{
					Source:    source,
					ClaudeDir: filepath.Join(home, ".claude"),
					Force:     flagForce,
				})
				if err != nil {
					return err
				}

				for _, f := range result.Installed {
					fmt.Fprintf(os.Stdout, "  [ok]   %s\n", f)
				}
				for _, f := range result.Skipped {
					fmt.Fprintf(os.Stdout, "  [skip] %s (already exists)\n", f)
				}
				if result.Merged {
					fmt.Fprintln(os.Stdout, "  [merge] settings.json")
				}
			}

			fmt.Fprintln(os.Stdout, "")
			fmt.Fprintf(os.Stdout, "Done. Restart %s for changes to take effect.\n", agent.Name)
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
	default:
		return nil, fmt.Errorf("install: unknown agent %q — supported agents: claude-code", slug)
	}
}
