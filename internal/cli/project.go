package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newProjectCmd returns the "mneme project" subcommand group: growing a
// brand-new project repository from a scaffold in the active profile's catalog
// (SPEC-098 §7a, the /new-project half of docs/profiles-design.md §15). The
// grill skill (/new-project) is the primary consumer of "project new" — it
// elicits the scaffold + variables and defers the deterministic assembly here.
func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Grow a new project repository from the active profile's scaffolds",
		Long: `Grow a brand-new project repository from a scaffold declared by the
active profile (pin > host default > vanilla). The deterministic half of
/new-project: it copies the scaffold's skeleton, substitutes variables, runs
"git init" (no commit, no remote), and writes the fresh repo's .mneme-profile
pin recording which scaffold generated it.`,
	}
	cmd.AddCommand(newProjectNewCmd())
	return cmd
}

// newScaffoldingProfileSvc constructs a ProfileService wired for NewProject:
// the host-level store (for a store-backed active profile's identity), the
// config path (ResolveActive precedence), the embedded OSS default profile FS,
// and the real bootstrapper. NewProject never activates, so — unlike
// newActivatingProfileSvc — it needs neither mem/sub nor the skills directory.
func newScaffoldingProfileSvc() *service.ProfileService {
	cfg := config.Default()
	if home, err := os.UserHomeDir(); err == nil {
		if loaded, loadErr := config.Load(filepath.Join(home, ".mneme", "config.toml")); loadErr == nil {
			cfg = loaded
		}
	}
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}
	return service.NewProfileService(cfg.ProfilesDir(), false,
		service.WithProfileConfigPath(config.DefaultPath()),
		service.WithDefaultProfileFS(install.DefaultProfileFS()),
		service.WithProfileBootstrapper(service.NewExecBootstrapper()),
	)
}

// newProjectNewCmd returns the "mneme project new" subcommand (SPEC-098 §7a).
func newProjectNewCmd() *cobra.Command {
	var (
		flagDir  string
		flagVars []string
	)

	cmd := &cobra.Command{
		Use:   "new <scaffold> --dir <path> [--var k=v ...]",
		Short: "Assemble a new project from a scaffold of the active profile",
		Long: `Assemble a brand-new project repository from the named scaffold of the
active profile's catalog.

The active profile (pin > host default > vanilla) supplies the scaffold
catalog and the identity recorded in the fresh repo's pin. The destination
must be empty or absent. For a "single" layout the scaffold's skeleton is
copied with {{var}} substitution; "git init" runs with no commit and no
remote. The pin is written with scaffold=<name> so the repo is born pointing
at the profile that generated it.

This command does NOT commit, set a remote, or activate the profile — the
/new-project skill chains "mneme-init" over the fresh repo to materialize
agents and seed memory.`,
		Example: `  mneme project new library-go --dir ./my-lib --var module_path=github.com/acme/my-lib
  mneme project new saas-multitenant --dir ./newco`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagDir == "" {
				return fmt.Errorf("project new: --dir is required")
			}
			vars, err := parseVarFlags(flagVars)
			if err != nil {
				return fmt.Errorf("project new: %w", err)
			}

			svc := newScaffoldingProfileSvc()
			res, err := svc.NewProject(cmd.Context(), service.ProjectNewInput{
				Scaffold: args[0],
				Dir:      flagDir,
				Vars:     vars,
			})
			if err != nil {
				switch {
				case errors.Is(err, model.ErrScaffoldNotFound):
					return fmt.Errorf("project new: %q is not a scaffold in the active profile — run `mneme profile status` to see the active profile: %w", args[0], err)
				case errors.Is(err, model.ErrProfileExists):
					return fmt.Errorf("project new: destination not empty — choose another directory or empty it: %w", err)
				case errors.Is(err, model.ErrLayoutUnsupported):
					return fmt.Errorf("project new: %w — monorepo scaffolds arrive in a later mneme", err)
				case errors.Is(err, model.ErrBootstrapToolMissing):
					return fmt.Errorf("project new: %w", err)
				}
				return fmt.Errorf("project new: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Assembled %s (%s) from profile %s -> %s\n", res.Scaffold, res.Layout, res.Profile, res.Path)
			fmt.Fprintf(cmd.OutOrStdout(), "Pin written: %s (scaffold=%s)\n", res.PinPath, res.Scaffold)
			fmt.Fprintln(cmd.OutOrStdout(), "Next: run mneme-init in the new repo to materialize agents and seed memory.")
			return nil
		},
	}

	cmd.Flags().StringVar(&flagDir, "dir", "", "Destination directory for the new project (required; must be empty or absent)")
	cmd.Flags().StringArrayVar(&flagVars, "var", nil, "Substitution variable as key=value (repeatable)")
	return cmd
}

// parseVarFlags turns repeated "--var key=value" flags into a map. A flag
// without "=" or with an empty key is an error, so a typo fails loudly rather
// than silently dropping a substitution.
func parseVarFlags(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	vars := make(map[string]string, len(raw))
	for _, kv := range raw {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("invalid --var %q: expected key=value", kv)
		}
		vars[kv[:idx]] = kv[idx+1:]
	}
	return vars, nil
}
