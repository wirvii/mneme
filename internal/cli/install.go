package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/install"
)

// newInstallCmd returns the "mneme install" subcommand. It configures a
// supported AI coding agent to use mneme for persistent memory by wiring up
// the MCP server, hook handlers, protocol injection, and slash commands.
//
// The command is idempotent: running it multiple times on the same agent
// produces the same result without duplicating entries or clobbering user config.
func newInstallCmd() *cobra.Command {
	var flagDryRun bool

	cmd := &cobra.Command{
		Use:   "install <agent>",
		Short: "Configure an AI coding agent to use mneme",
		Long: `Configure a supported AI coding agent to use mneme as its persistent
memory system. This command:

  1. Registers the mneme MCP server in the agent's MCP config
  2. Installs session-start and session-end hooks
  3. Injects the memory protocol into the agent's system prompt file
  4. Installs the /mneme-init slash command

Supported agents: claude-code

The install is non-destructive and idempotent — running it multiple times
produces the same result without clobbering existing configuration.`,
		Example: `  mneme install claude-code
  mneme install claude-code --dry-run`,
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

			fmt.Fprintln(os.Stdout, "")
			fmt.Fprintf(os.Stdout, "Done. Restart %s for changes to take effect.\n", agent.Name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would be configured without making changes")

	return cmd
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
