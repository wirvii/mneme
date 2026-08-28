package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/service"
)

// newSDDCmd returns the "mneme sdd" parent command (SPEC-130 §2a): the
// backlog and specs travel as versioned files under the repository's own
// .mneme/sdd/, git-native, opt-in per repository.
func newSDDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdd",
		Short: "Backlog and specs travel as versioned files in this repository (SPEC-130 §2a)",
		Long: `Commands for the SDD git-native mechanism (SPEC-130 §2a): the same backlog
items and specs stored in the local database, ALSO written as reviewable
Markdown files under .mneme/sdd/ — so they can be reviewed in a pull request.

Opt-in per repository. A repository that never runs "mneme sdd enable" is
completely unaffected: no file is written, nothing changes.

This part does NOT read files back into the database, does NOT install git
hooks, and does NOT let a teammate's clone pick these files up automatically
— that is BL-201. Today these files exist to be reviewed in a pull request,
not yet to synchronize two machines.`,
	}
	cmd.AddCommand(
		newSDDEnableCmd(),
		newSDDDisableCmd(),
		newSDDExportCmd(),
		newSDDStatusCmd(),
		newSDDHooksCmd(),
	)
	return cmd
}

// newSDDEnableCmd returns "mneme sdd enable".
func newSDDEnableCmd() *cobra.Command {
	var flagApply bool

	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Turn on the SDD git-native mechanism for this repository (dry-run by default)",
		Long: `Preview (default) or apply turning on the SDD git-native mechanism.

Without --apply: prints the plan (how many backlog items and specs would be
exported) and the honest warnings below. Writes NOTHING — not even a probe
file.

With --apply: exports EVERY backlog item and spec (including archived items
and done specs) as Markdown files under .mneme/sdd/, writes the enable
marker (.mneme/sdd/.mneme-sdd, committed — this turns the mechanism on for
every teammate who clones the repository), and adds "sdd.off" to
.mneme/.gitignore.

Refuses, before writing anything, when:
  - the current directory is not a git repository;
  - the repository already carries SDD records this database cannot make
    sense of — an unreadable file, or one whose anchor is unknown here.
    Reading such files in is BL-201; reconciling them is BL-202.`,
		Example: `  mneme sdd enable
  mneme sdd enable --apply`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.EnableSDDRepo(cmd.Context(), svc.RepoDir(), flagApply)
			if err != nil {
				return fmt.Errorf("sdd enable: %w", err)
			}

			renderSDDEnableResult(cmd.OutOrStdout(), result)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagApply, "apply", false, "Execute (default: dry-run, mutates nothing)")
	return cmd
}

// newSDDDisableCmd returns "mneme sdd disable".
func newSDDDisableCmd() *cobra.Command {
	var flagApply bool

	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Turn off the SDD git-native mechanism LOCALLY for this repository (dry-run by default)",
		Long: `Preview (default) or apply turning off the SDD mechanism for THIS machine
only (D3/D19): the enable marker stays committed, so every OTHER teammate's
clone keeps the mechanism on.

Without --apply: prints what would happen. Writes nothing.

With --apply: writes the local, gitignored .mneme/sdd.off file. From then
on, this machine's own write-through wrappers become inert.

NEVER deletes anything under .mneme/sdd/. Removing those files from the
repository, if that is what you want, is a separate, explicit step of your
own ("git rm -r .mneme/sdd") — mneme never does it for you.`,
		Example: `  mneme sdd disable
  mneme sdd disable --apply`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.DisableSDDRepo(cmd.Context(), svc.RepoDir(), flagApply)
			if err != nil {
				return fmt.Errorf("sdd disable: %w", err)
			}

			out := cmd.OutOrStdout()
			if !result.Applied {
				fmt.Fprintf(out, "Would write %s/.mneme/sdd.off (dry-run — pass --apply to execute).\n", result.RepoRoot)
				fmt.Fprintln(out, "This does not delete anything under .mneme/sdd/.")
				return nil
			}
			fmt.Fprintf(out, "SDD mechanism disabled locally at %s.\n", result.RepoRoot)
			fmt.Fprintln(out, "Nothing under .mneme/sdd/ was deleted. Other teammates are unaffected.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagApply, "apply", false, "Execute (default: dry-run, mutates nothing)")
	return cmd
}

// newSDDExportCmd returns "mneme sdd export".
func newSDDExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "Re-materialize every backlog item and spec from the database",
		Long: `Re-writes EVERY backlog item's and spec's Markdown record from the current
database state — the idempotent repair path. Requires the mechanism to
already be enabled (this is a repair, not a second way to turn it on) and
the same convergence guard "mneme sdd enable" applies: refuses, before
writing anything, if the repository carries a record this database cannot
make sense of.`,
		Example: `  mneme sdd export`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.ExportSDDRepo(cmd.Context(), svc.RepoDir())
			if err != nil {
				return fmt.Errorf("sdd export: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Exported %d backlog item(s) and %d spec(s) to %s/.mneme/sdd.\n",
				result.Plan.BacklogCount, result.Plan.SpecCount, result.RepoRoot)
			fmt.Fprintln(cmd.OutOrStdout(), "These files are likely pending commit — review with `git status` before committing.")
			return nil
		},
	}
}

// newSDDStatusCmd returns "mneme sdd status".
func newSDDStatusCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report the SDD mechanism's state for this repository",
		Long: `Reports whether the mechanism is on or off, how many backlog items/specs the
database has, what git reports as pending under .mneme/sdd, and — without
refusing anything — which files (if any) are broken or carry an anchor this
database does not know about.`,
		Example: `  mneme sdd status
  mneme sdd status --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.SDDStatus(cmd.Context(), svc.RepoDir())
			if err != nil {
				return fmt.Errorf("sdd status: %w", err)
			}

			if flagJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			renderSDDStatusResult(cmd.OutOrStdout(), result)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// renderSDDEnableResult prints EnableSDDRepo's result in plain text —
// shared by the dry-run and --apply paths, since AC14's four warnings are
// required in BOTH (they are the honest content of the preview itself).
func renderSDDEnableResult(out io.Writer, result *service.SDDEnableResult) {
	fmt.Fprintf(out, "Plan: %d backlog item(s), %d spec(s) would be exported to %s/.mneme/sdd.\n",
		result.Plan.BacklogCount, result.Plan.SpecCount, result.RepoRoot)
	if result.Remote != "" {
		fmt.Fprintf(out, "Remote (as git reports it locally): %s\n", result.Remote)
	}
	fmt.Fprintln(out)
	for _, w := range result.Warnings {
		fmt.Fprintf(out, "  - %s\n", w)
	}
	fmt.Fprintln(out)

	if !result.Applied {
		fmt.Fprintln(out, "Dry-run — nothing was written. Pass --apply to execute.")
		return
	}
	fmt.Fprintf(out, "Applied: exported everything to %s/.mneme/sdd, wrote the marker, "+
		"and added sdd.off to .mneme/.gitignore.\n", result.RepoRoot)
	fmt.Fprintln(out, "These files are likely pending commit — review with `git status` before committing.")
	fmt.Fprintln(out, "No git hooks were installed and no importer runs yet — that is BL-201.")
}

// renderSDDStatusResult prints SDDStatus's result in plain text.
func renderSDDStatusResult(out io.Writer, result *service.SDDStatusResult) {
	state := "disabled"
	if result.Enabled {
		state = "enabled"
	}
	fmt.Fprintf(out, "SDD mechanism: %s (%s)\n", state, result.RepoRoot)
	fmt.Fprintf(out, "Database has %d backlog item(s), %d spec(s).\n", result.Plan.BacklogCount, result.Plan.SpecCount)

	if result.PendingGit != "" {
		fmt.Fprintln(out, "Pending commit under .mneme/sdd:")
		fmt.Fprintln(out, result.PendingGit)
	}
	if len(result.Broken) > 0 {
		fmt.Fprintf(out, "%d file(s) could not be parsed:\n", len(result.Broken))
		for _, p := range result.Broken {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
	if len(result.ForeignPaths) > 0 {
		fmt.Fprintf(out, "%d file(s) carry an anchor this database does not know (reading them is BL-201, reconciling BL-202):\n", len(result.ForeignPaths))
		for _, p := range result.ForeignPaths {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
}
