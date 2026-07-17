package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newScaffoldCmd returns the "mneme scaffold" subcommand group: authoring a
// profile's project scaffolds (SPEC-100 §7c, the §15.6 half of docs/profiles-
// design.md §15). Today it carries "capture" — turning an exemplar repo into a
// draft scaffold the mneme-profile-author skill then curates.
func newScaffoldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scaffold",
		Short: "Author a profile's project scaffolds",
		Long: `Author the scaffolds a profile can generate projects from. The
deterministic half of the mneme-profile-author authoring grill (§15.6): capture
an exemplar repository into a draft scaffold the author then curates.`,
	}
	cmd.AddCommand(newScaffoldCaptureCmd())
	return cmd
}

// newScaffoldCaptureCmd returns the "mneme scaffold capture" subcommand
// (SPEC-100 §7c).
func newScaffoldCaptureCmd() *cobra.Command {
	var (
		flagName string
		flagInto string
	)

	cmd := &cobra.Command{
		Use:   "capture <exemplar-repo> [--name <scaffold>] [--into <profile-repo>]",
		Short: "Capture an exemplar repository into a draft scaffold",
		Long: `Capture an existing exemplar repository into a draft scaffold within a
profile repo you are authoring.

It auto-detects the exemplar's structure — apps/, packages/, turbo.json,
pnpm-workspace.yaml — to infer the scaffold's layout (single | monorepo) and
toolchain (turborepo | custom), and reads go.mod / package.json for the
exemplar's identity. It then writes scaffolds/<name>/scaffold.toml plus the
captured trees (shell/, overlay/, or a flat skeleton/, and each app under
_blueprints/), rewriting the exemplar's project name and Go module path to
{{PROJECT_NAME}} / {{MODULE_PATH}} placeholders so a generated project supplies
its own.

The result is a DRAFT: the mneme-profile-author skill drives the curation
(prune legacy content, refine [vars], elicit custom [wiring]). This command
never bootstraps, runs git, or commits. It writes into --into (default: the
current directory), which must be an existing profile repository.`,
		Example: `  mneme scaffold capture ../wirvii360r --name saas-multitenant --into .
  mneme scaffold capture ../my-lib`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newScaffoldingProfileSvc()
			res, err := svc.CaptureScaffold(service.ScaffoldCaptureInput{
				Repo: args[0],
				Name: flagName,
				Into: flagInto,
			})
			if err != nil {
				switch {
				case errors.Is(err, model.ErrNothingToCapture):
					return fmt.Errorf("scaffold capture: %w — is %q a project repository?", err, args[0])
				case errors.Is(err, model.ErrInvalidScaffold):
					return fmt.Errorf("scaffold capture: %w — pass a safe-slug --name (^[a-z0-9][a-z0-9-]*$)", err)
				case errors.Is(err, model.ErrProfileExists):
					return fmt.Errorf("scaffold capture: %w — choose another --name or remove the existing scaffold", err)
				case errors.Is(err, model.ErrProfileNotFound):
					return fmt.Errorf("scaffold capture: %w — run `mneme profile new` first, then --into that repo", err)
				}
				return fmt.Errorf("scaffold capture: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Captured scaffold %s (%s) -> %s\n", res.Scaffold, layoutToolchain(res), res.ScaffoldTOMLPath)
			if len(res.Blueprints) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Blueprints: %v\n", res.Blueprints)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Vars: %v\n", res.Vars)
			fmt.Fprintln(cmd.OutOrStdout(), "Next: curate the draft (prune legacy, refine [vars]/[wiring]) with the mneme-profile-author skill, then commit.")
			return nil
		},
	}

	cmd.Flags().StringVar(&flagName, "name", "", "Scaffold catalog name (a safe-slug; defaults to the exemplar's directory basename)")
	cmd.Flags().StringVar(&flagInto, "into", "", "Profile repository to write the scaffold into (defaults to the current directory)")
	return cmd
}

// layoutToolchain renders a capture result's layout, appending the toolchain
// for a monorepo (e.g. "monorepo/turborepo", or plain "single").
func layoutToolchain(res *service.ScaffoldCaptureResult) string {
	if res.Toolchain == "" {
		return res.Layout
	}
	return res.Layout + "/" + res.Toolchain
}
