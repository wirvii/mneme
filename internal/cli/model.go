package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/service"
)

// newModelCmd returns the "mneme model" command group with list/set/reset
// subcommands for managing per-agent model assignments (SPEC-038).
//
// All operations are filesystem-only (read/write ~/.mneme/config.toml).
// No database connection is required. The assigned model takes effect on the
// next `mneme install claude-code` run.
func newModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Manage per-agent model assignments",
		Long: `Manage which model alias each bundled agent uses.

Model assignments are stored in ~/.mneme/config.toml under [models.overrides]
and applied to agent files during install.

  mneme model list              — show effective model for each agent
  mneme model set <agent> <model>  — override the model for an agent
  mneme model reset [<agent>]   — remove override for one or all agents

Changes take effect after running: mneme install claude-code`,
	}

	cmd.AddCommand(
		newModelListCmd(),
		newModelSetCmd(),
		newModelResetCmd(),
	)

	return cmd
}

// newModelListCmd returns the "mneme model list" subcommand.
func newModelListCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the effective model for each agent",
		Long: `Show the effective model for each bundled agent and whether it comes
from a config override or the built-in default.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := service.NewModelsService(config.DefaultPath())
			resp, err := svc.List(cmd.Context())
			if err != nil {
				return fmt.Errorf("model list: %w", err)
			}

			if flagJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "AGENT\tMODEL\tORIGIN")
			for _, ai := range resp.Agents {
				fmt.Fprintf(w, "%s\t%s\t%s\n", ai.Agent, ai.Model, ai.Origin)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// newModelSetCmd returns the "mneme model set" subcommand.
func newModelSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <agent> <model>",
		Short: "Override the model for an agent",
		Long: `Set the model alias for a specific bundled agent. The alias is written
to ~/.mneme/config.toml and applied on the next install.

Known aliases: opus, sonnet, haiku, inherit
Unknown aliases are accepted with a warning — verify the alias is valid.

Example:
  mneme model set bug-hunter opus`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := service.NewModelsService(config.DefaultPath())
			resp, err := svc.Set(cmd.Context(), service.ModelSetRequest{
				Agent: args[0],
				Model: args[1],
			})
			if err != nil {
				return fmt.Errorf("model set: %w", err)
			}
			if resp.Warning != "" {
				fmt.Fprintf(os.Stderr, "WARNING: %s\n", resp.Warning)
			}
			fmt.Fprintf(os.Stdout, "Set %s → %s\n", resp.Agent, resp.Model)
			if resp.Hint != "" {
				fmt.Fprintf(os.Stdout, "Hint: %s\n", resp.Hint)
			}
			return nil
		},
	}
}

// newModelResetCmd returns the "mneme model reset" subcommand.
func newModelResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset [<agent>]",
		Short: "Remove model override for one or all agents",
		Long: `Remove the config override for a specific agent, restoring its built-in
default. When agent is omitted, all overrides are cleared.

Example:
  mneme model reset bug-hunter   — restore bug-hunter to default
  mneme model reset              — restore all agents to defaults`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var agent string
			if len(args) > 0 {
				agent = args[0]
			}
			svc := service.NewModelsService(config.DefaultPath())
			resp, err := svc.Reset(cmd.Context(), service.ModelResetRequest{Agent: agent})
			if err != nil {
				return fmt.Errorf("model reset: %w", err)
			}
			if len(resp.Reset) == 0 {
				fmt.Fprintln(os.Stdout, "No overrides to reset.")
				return nil
			}
			for _, a := range resp.Reset {
				fmt.Fprintf(os.Stdout, "Reset %s to default\n", a)
			}
			if resp.Hint != "" {
				fmt.Fprintf(os.Stdout, "Hint: %s\n", resp.Hint)
			}
			return nil
		},
	}
}
