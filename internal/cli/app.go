package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newAppCmd returns the "mneme app" subcommand group: adding a composable app
// to an existing monorepo grown from the active profile's scaffold (SPEC-099
// §7b, the /new-app half of docs/profiles-design.md §15). The grill skill
// (/new-app) is the primary consumer of "app add" — it reads the monorepo's
// pin, offers the archetype's blueprints, and defers the deterministic copy +
// wiring here.
func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Add a composable app to a monorepo grown from the active profile",
		Long: `Add a composable app (a blueprint of the monorepo's originating
scaffold) to an existing monorepo, auto-wiring it into the workspace and
toolchain root files. The deterministic half of /new-app.`,
	}
	cmd.AddCommand(newAppAddCmd())
	return cmd
}

// newAppAddCmd returns the "mneme app add" subcommand (SPEC-099 §7b).
func newAppAddCmd() *cobra.Command {
	var (
		flagName     string
		flagDir      string
		flagScaffold string
		flagVars     []string
	)

	cmd := &cobra.Command{
		Use:   "add <blueprint> --name <app> [--dir <monorepo>] [--var k=v ...]",
		Short: "Drop a blueprint into a monorepo and auto-wire it",
		Long: `Add a composable app to an existing monorepo from a blueprint the
monorepo's scaffold offers.

The monorepo's own pin (.mneme-profile) records which scaffold generated it;
that archetype's blueprint catalog is read from the active profile. The named
blueprint is copied into the scaffold's apps directory under --name (with
{{var}} substitution) and auto-wired: the Turborepo built-in adapter updates
pnpm-workspace.yaml (a no-op when a glob already covers apps/*), or a custom
toolchain applies its declared [wiring] actions.

This command does NOT run git init (the monorepo already has its .git), commit,
or set a remote. A single-layout project has no apps to add.`,
		Example: `  mneme app add go-core-srv --name billing
  mneme app add next-web-ui --name admin --var org_name=acme`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagName == "" {
				return fmt.Errorf("app add: --name is required")
			}
			vars, err := parseVarFlags(flagVars)
			if err != nil {
				return fmt.Errorf("app add: %w", err)
			}

			svc := newScaffoldingProfileSvc()
			res, err := svc.AddApp(cmd.Context(), service.AppAddInput{
				Blueprint: args[0],
				Name:      flagName,
				Dir:       flagDir,
				Vars:      vars,
				Scaffold:  flagScaffold,
			})
			if err != nil {
				switch {
				case errors.Is(err, model.ErrAppAddNotApplicable):
					return fmt.Errorf("app add: %w — this project is single-layout; there are no apps to add", err)
				case errors.Is(err, model.ErrScaffoldNotFound):
					return fmt.Errorf("app add: %q is not a blueprint of this monorepo's scaffold (or the repo records no scaffold): %w", args[0], err)
				case errors.Is(err, model.ErrProfileExists):
					return fmt.Errorf("app add: the app directory already exists and is not empty — choose another --name: %w", err)
				case errors.Is(err, model.ErrUnknownWiringAction):
					return fmt.Errorf("app add: %w", err)
				}
				return fmt.Errorf("app add: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added app %s (from blueprint %s) -> %s\n", res.App, res.Blueprint, res.AppPath)
			if len(res.Wired) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Wired: %v\n", res.Wired)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Next: run `pnpm install` to link the new workspace.")
			return nil
		},
	}

	cmd.Flags().StringVar(&flagName, "name", "", "Name the app takes in the monorepo (required; a safe-slug)")
	cmd.Flags().StringVar(&flagDir, "dir", "", "Monorepo root (defaults to the current working directory)")
	cmd.Flags().StringVar(&flagScaffold, "scaffold", "", "Override which scaffold archetype supplies the blueprint catalog (defaults to the pin's scaffold)")
	cmd.Flags().StringArrayVar(&flagVars, "var", nil, "Substitution variable as key=value (repeatable)")
	return cmd
}
