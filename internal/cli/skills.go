package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newSkillsCmd returns the "mneme skills" subcommand group.
// It provides operations for listing, installing, pinning, removing, linting,
// and validating mneme skills in ~/.claude/skills/.
func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage mneme skills in ~/.claude/skills/",
		Long: `Manage mneme skills: list, install, pin, unpin, remove, lint, validate.

A skill is a directory under ~/.claude/skills/ containing SKILL.md and
optional scripts/, references/, and validation/run.sh. mneme is the package
manager for skills — it does NOT implement the Claude Code skill runtime.

Subcommands:
  list        List bundled and installed skills.
  install     Install a bundled skill to ~/.claude/skills/.
  pin         Protect an installed skill from overwrite/removal.
  unpin       Remove pin protection from an installed skill.
  remove      Remove an installed skill directory.
  lint        Run the deterministic structural linter on a skill.
  validate    Run the validation/run.sh script for a skill.`,
	}

	cmd.AddCommand(
		newSkillsListCmd(),
		newSkillsInstallCmd(),
		newSkillsPinCmd(),
		newSkillsUnpinCmd(),
		newSkillsRemoveCmd(),
		newSkillsLintCmd(),
		newSkillsValidateCmd(),
	)

	return cmd
}

// skillsSvc constructs a SkillsService targeting both supported runtime
// discovery directories. CLI commands call this inline so they do not depend on
// the shared initService() / initSDDService() infrastructure.
func newSkillsSvc() (*service.SkillsService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	return service.NewMirroredSkillsService(
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".agents", "skills"),
	), nil
}

// newSkillsListCmd returns the "mneme skills list" subcommand.
func newSkillsListCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List bundled and installed skills",
		Long: `List all available skills, showing name, version, installed status,
pinned status, and whether the structural lint check passes.`,
		Example: `  mneme skills list
  mneme skills list --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}

			infos, err := svc.List()
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, infos)
			}

			for _, info := range infos {
				installed := "-"
				if info.Installed {
					installed = "installed"
				}
				pinned := ""
				if info.Pinned {
					pinned = " [pinned]"
				}
				lintStatus := ""
				if info.Installed {
					if info.LintOK {
						lintStatus = " [lint:ok]"
					} else {
						lintStatus = " [lint:fail]"
					}
				}
				bundled := ""
				if info.Bundled {
					bundled = " [bundled]"
				}
				fmt.Fprintf(os.Stdout, "%-24s  v%-10s  %-12s%s%s%s\n",
					info.Name, info.Version, installed, pinned, lintStatus, bundled)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// newSkillsInstallCmd returns the "mneme skills install" subcommand.
func newSkillsInstallCmd() *cobra.Command {
	var flagForce bool

	cmd := &cobra.Command{
		Use:   "install <name>",
		Short: "Install a bundled skill to ~/.claude/skills/",
		Long: `Install a skill from the embedded bundle to ~/.claude/skills/<name>/.

If the skill is already installed with pinned:true, installation is skipped
unless --force is used.`,
		Example: `  mneme skills install example-skill
  mneme skills install example-skill --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}

			if err := svc.Install(args[0], flagForce); err != nil {
				if errors.Is(err, model.ErrSkillPinned) {
					return fmt.Errorf("%s is pinned; use --force to overwrite", args[0])
				}
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: installed\n", args[0])
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Overwrite even if the installed skill is pinned")

	return cmd
}

// newSkillsPinCmd returns the "mneme skills pin" subcommand.
func newSkillsPinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pin <name>",
		Short: "Protect an installed skill from overwrite or removal",
		Long: `Set pinned:true in the installed SKILL.md.

A pinned skill will not be overwritten by "mneme install claude-code" or
"mneme skills install" and cannot be removed without --force. This lets you
maintain a locally customised skill without future installs clobbering it.`,
		Example: `  mneme skills pin example-skill`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}

			if err := svc.Pin(args[0]); err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: pinned\n", args[0])
			return nil
		},
	}

	return cmd
}

// newSkillsUnpinCmd returns the "mneme skills unpin" subcommand.
func newSkillsUnpinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unpin <name>",
		Short: "Remove pin protection from an installed skill",
		Long: `Set pinned:false in the installed SKILL.md.

After unpinning, the skill can be overwritten by "mneme install claude-code"
or "mneme skills install" and removed without --force.`,
		Example: `  mneme skills unpin example-skill`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}

			if err := svc.Unpin(args[0]); err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: unpinned\n", args[0])
			return nil
		},
	}

	return cmd
}

// newSkillsRemoveCmd returns the "mneme skills remove" subcommand.
func newSkillsRemoveCmd() *cobra.Command {
	var flagForce bool

	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed skill directory",
		Long: `Remove the skill directory ~/.claude/skills/<name>/ from the filesystem.

If the skill has pinned:true in its SKILL.md, removal is refused unless
--force is supplied. Bundled skills can be reinstalled with "skills install".`,
		Example: `  mneme skills remove example-skill
  mneme skills remove example-skill --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}

			if err := svc.Remove(args[0], flagForce); err != nil {
				if errors.Is(err, model.ErrSkillPinned) {
					return fmt.Errorf("%s is pinned; use --force to remove", args[0])
				}
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: removed\n", args[0])
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Remove even if the skill is pinned")

	return cmd
}

// newSkillsLintCmd returns the "mneme skills lint" subcommand.
func newSkillsLintCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "lint [<name>]",
		Short: "Run the deterministic structural linter on a skill",
		Long: `Run the structural linter against a single skill or all installed skills.

The linter checks:
  - Required frontmatter fields: name, description, version.
  - name == directory name.
  - version is a valid semver string (X.Y.Z).
  - All 5 required H2 sections are present.
  - The Automated Checks section contains a 3-column table with the correct headers.

No scripts are executed during linting. Exit code 1 if any skill has errors.`,
		Example: `  mneme skills lint example-skill
  mneme skills lint
  mneme skills lint --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}

			name := ""
			if len(args) > 0 {
				name = args[0]
			}

			results, err := svc.Lint(name)
			if err != nil {
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, results)
			}

			hasErrors := false
			for _, r := range results {
				fmt.Fprintf(os.Stdout, "skill: %s — passed=%v\n", r.Name, r.Passed)
				for _, f := range r.Errors {
					fmt.Fprintf(os.Stderr, "  ERROR:   %s\n", f.Message)
					hasErrors = true
				}
				for _, f := range r.Warnings {
					fmt.Fprintf(os.Stdout, "  WARNING: %s\n", f.Message)
				}
				for _, f := range r.Infos {
					fmt.Fprintf(os.Stdout, "  INFO:    %s\n", f.Message)
				}
			}

			if hasErrors {
				return fmt.Errorf("lint: one or more skills have errors")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// newSkillsValidateCmd returns the "mneme skills validate" subcommand.
func newSkillsValidateCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "validate <name>",
		Short: "Run the validation/run.sh script for a skill",
		Long: `Run the skill's validation/run.sh with the skill directory as cwd.

A timeout of 120s is applied. Exit code 0 = pass; non-zero = fail.
If no validation/run.sh exists, reports an informational message and exits 1.`,
		Example: `  mneme skills validate example-skill
  mneme skills validate example-skill --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}

			result, err := svc.Validate(context.Background(), args[0])
			if err != nil {
				if errors.Is(err, model.ErrSkillNoValidation) {
					fmt.Fprintf(os.Stdout, "%s: no validation/run.sh found\n", args[0])
					return fmt.Errorf("no validation script")
				}
				return err
			}

			if flagJSON {
				return printJSON(os.Stdout, result)
			}

			if result.Passed {
				fmt.Fprintf(os.Stdout, "%s: validation passed (exit %d)\n%s", args[0], result.ExitCode, result.Output)
			} else {
				fmt.Fprintf(os.Stderr, "%s: validation failed (exit %d)\n%s", args[0], result.ExitCode, result.Output)
				return fmt.Errorf("validation failed with exit code %d", result.ExitCode)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}
