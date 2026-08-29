package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/service"
)

// newSDDCmd returns the "mneme sdd" parent command (SPEC-130 §2a + SPEC-131
// §2b): the backlog and specs travel as versioned files under the
// repository's own .mneme/sdd/, git-native, opt-in per repository.
func newSDDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdd",
		Short: "Backlog and specs travel as versioned files in this repository (SPEC-130/SPEC-131)",
		Long: `Commands for the SDD git-native mechanism: the same backlog items and specs
stored in the local database, ALSO written as reviewable Markdown files under
.mneme/sdd/ — so they can be reviewed in a pull request.

Opt-in per repository. A repository that never runs "mneme sdd enable" is
completely unaffected: no file is written, nothing changes.

Files entered your OWN database automatically once "mneme sdd enable" ran
here, or the moment "mneme sdd hooks install" runs on a clone that already
has the marker committed: every "git pull"/checkout imports whatever the
repository already carries, in the background. Two people creating the same
correlative at the same time produce a collision this import detects and
reports, but does not yet resolve — reconciling is a separate, later part
of this project.`,
	}
	cmd.AddCommand(
		newSDDEnableCmd(),
		newSDDDisableCmd(),
		newSDDExportCmd(),
		newSDDStatusCmd(),
		newSDDHooksCmd(),
		newSDDImportCmd(),
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

Also installs THIS machine's own git hooks (post-merge, post-checkout) so
future pulls import automatically — see "mneme sdd hooks".

Refuses, before writing anything, when:
  - the current directory is not a git repository;
  - the repository already carries SDD records this database cannot make
    sense of — an unreadable file, or one whose anchor is unknown here.
    Run "mneme sdd import" first; reconciling a genuine collision is a
    separate, later part of this project.`,
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

With --apply, in this exact order: (1) imports once more, so anything a
teammate already published and this machine has not yet read is not lost;
(2) writes the local, gitignored .mneme/sdd.off file — from then on, this
machine's own write-through wrappers become inert; (3) removes this
machine's own git hooks for the SDD mechanism (installed by "mneme sdd
enable"/"mneme sdd hooks install").

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
				fmt.Fprintf(out, "Would (1) import once more, (2) write %s/.mneme/sdd.off, and (3) remove this "+
					"machine's own SDD git hooks (dry-run — pass --apply to execute).\n", result.RepoRoot)
				fmt.Fprintln(out, "This does not delete anything under .mneme/sdd/.")
				return nil
			}
			fmt.Fprintf(out, "SDD mechanism disabled locally at %s: imported once more, wrote sdd.off, "+
				"and removed this machine's own SDD git hooks.\n", result.RepoRoot)
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

// newSDDImportCmd returns "mneme sdd import" (SPEC-131 D58): the manual
// counterpart of "mneme sdd hooks run-import" — same underlying method
// (ImportSDDFromRepo), but this one EXECUTES by default (no --apply flag:
// D13 already guarantees the importer never deletes anything, so there is
// nothing a preview protects here that a dry-run flag would not already
// cover) and exits 1 when anything was skipped, so a script or a person
// notices without having to parse the report.
func newSDDImportCmd() *cobra.Command {
	var flagDryRun bool

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import this repository's SDD backlog/specs into the local database",
		Long: `Reads .mneme/sdd/ and creates/updates the local database accordingly —
the same read path the installed git hooks run automatically after every
pull/checkout (see "mneme sdd hooks"). Executes by default: this only
populates the LOCAL database, never publishes anything, and D13 guarantees
it never deletes a row — pass --dry-run to preview without writing.

Decides by ANCHOR, never by correlative (D50): a record whose correlative
is already claimed by a different anchor is SKIPPED and reported, never
overwritten — that collision is detected and reported here, not resolved
(reconciling it is a separate, later part of this project).

Exits 1 when anything was skipped (a broken file, a missing title, or a
genuine collision) — 0 otherwise.`,
		Example: `  mneme sdd import
  mneme sdd import --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSDDService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.ImportSDDFromRepo(cmd.Context(), svc.RepoDir(), !flagDryRun)
			if err != nil {
				return fmt.Errorf("sdd import: %w", err)
			}

			renderSDDImportResult(cmd.OutOrStdout(), result)

			if len(result.Skipped) > 0 {
				return fmt.Errorf("sdd import: %d record(s) skipped, see above", len(result.Skipped))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview without writing (default: executes)")
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
		fmt.Fprintf(out, "Would export everything to %s/.mneme/sdd, write the marker, "+
			"add sdd.off to .mneme/.gitignore, and install this machine's own SDD git hooks "+
			"(dry-run — pass --apply to execute).\n", result.RepoRoot)
		return
	}
	fmt.Fprintf(out, "Applied: exported everything to %s/.mneme/sdd, wrote the marker, "+
		"added sdd.off to .mneme/.gitignore, and installed this machine's own git hooks.\n", result.RepoRoot)
	fmt.Fprintln(out, "These files are likely pending commit — review with `git status` before committing.")
	fmt.Fprintln(out, "A teammate who clones needs only `mneme sdd hooks install` to start receiving imports too.")
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
		fmt.Fprintf(out, "%d file(s) carry an anchor this database does not know "+
			"(run `mneme sdd import` to read them in; reconciling a genuine collision is BL-202):\n", len(result.ForeignPaths))
		for _, p := range result.ForeignPaths {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
	if len(result.Conflicted) > 0 {
		fmt.Fprintf(out, "%d file(s) claim a correlative another anchor already holds — run `mneme sdd import` "+
			"for the full report; reconciling is BL-202:\n", len(result.Conflicted))
		for _, p := range result.Conflicted {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
	if len(result.Incomplete) > 0 {
		fmt.Fprintf(out, "%d file(s) are missing fields mneme fills in on the next import:\n", len(result.Incomplete))
		for _, p := range result.Incomplete {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
	if len(result.Divergent) > 0 {
		fmt.Fprintf(out, "%d file(s) differ from the database — `mneme sdd export` repairs this:\n", len(result.Divergent))
		for _, p := range result.Divergent {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
	if len(result.FrozenBlocked) > 0 {
		fmt.Fprintf(out, "%d file(s) bring a status change for a FROZEN spec — skipped, never applied "+
			"(SPEC-125, no unarchive):\n", len(result.FrozenBlocked))
		for _, p := range result.FrozenBlocked {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
	if result.Enabled {
		if result.HooksInstalled {
			fmt.Fprintln(out, "Git hooks: installed (post-merge, post-checkout import automatically).")
		} else {
			fmt.Fprintln(out, "Git hooks: NOT installed on this machine — run `mneme sdd hooks install` "+
				"to receive imports on every pull/checkout.")
		}
	}
	if result.OnlyInBaseCount > 0 {
		fmt.Fprintf(out, "%d correlative(s) exist in the database with no file on this branch "+
			"(normal on a working branch, not an error).\n", result.OnlyInBaseCount)
	}
	if result.OnlyInBaseError != "" {
		fmt.Fprintf(out, "Could not determine which correlatives exist only in the local database: %s\n", result.OnlyInBaseError)
	}
}

// renderSDDImportResult prints ImportSDDFromRepo's report (D43/D54) —
// every field names what happened, never silently.
func renderSDDImportResult(out io.Writer, result *service.SDDImportResult) {
	if result.NoOpReason != "" {
		fmt.Fprintf(out, "Nothing to import: %s.\n", result.NoOpReason)
		return
	}
	for _, c := range result.Created {
		fmt.Fprintf(out, "Created: %s\n", c)
	}
	for _, u := range result.Updated {
		fmt.Fprintf(out, "Updated: %s\n", u)
	}
	for _, c := range result.Completed {
		fmt.Fprintf(out, "Completed: %s (%s) — filled: %s\n", c.ID, c.Path, strings.Join(c.Fields, ", "))
	}
	for _, s := range result.Skipped {
		fmt.Fprintf(out, "Skipped: %s (%s) — %s\n", s.ID, s.Path, s.Reason)
	}
	if result.OnlyInBaseTotal > 0 {
		fmt.Fprintf(out, "%d correlative(s) exist only in the local database on this branch:\n", result.OnlyInBaseTotal)
		for _, id := range result.OnlyInBase {
			fmt.Fprintf(out, "  - %s\n", id)
		}
	}
	if result.OnlyInBaseError != "" {
		fmt.Fprintf(out, "Could not determine which correlatives exist only in the local database: %s\n", result.OnlyInBaseError)
	}
	if len(result.Created) == 0 && len(result.Updated) == 0 && len(result.Completed) == 0 && len(result.Skipped) == 0 {
		fmt.Fprintln(out, "Nothing changed — the database already matches every file on this branch.")
	}
}
