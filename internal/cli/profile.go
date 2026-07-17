package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newProfileCmd returns the "mneme profile" subcommand group (SPEC-091 §1):
// a team's working methodology packaged as a portable git repo, activated
// with nvm-like semantics. §1 only covers the foundation — the store
// (add/update/list) and read-only pin resolution (status). The verbs that
// WRITE a project's pin ("use"/"default") are a later spec (§3).
func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage mneme profiles: a team's methodology packaged as a portable git repo",
		Long: `Manage mneme profiles.

A profile is a team's working methodology (agents, skills, rules, templates)
packaged as a portable git repository, activated with nvm-like semantics: a
host-level store installed once (~/.mneme/profiles/<name>/), plus a
per-project pointer committed at the project's root (.mneme-profile).

Subcommands:
  add     Clone a profile into the host-level store (once).
  update  Fetch + checkout the latest state of an installed profile.
  list    List profiles in the host-level store.
  status  Report the current repo's pin resolution (read-only).

This command family does not write .mneme-profile itself — that is a later
verb ("profile use"/"profile default", not part of this release).`,
	}

	cmd.AddCommand(
		newProfileAddCmd(),
		newProfileUpdateCmd(),
		newProfileListCmd(),
		newProfileStatusCmd(),
	)

	return cmd
}

// newProfileSvc constructs a ProfileService targeting the host-level profile
// store (~/.mneme/profiles, or DataDir/profiles when --data-dir overrides
// it — mirroring initService's own precedence). noPrompt is always false for
// the CLI frontend: a developer at a terminal can authenticate interactively
// (design decision #11); the MCP frontend passes true instead (see
// internal/mcp/handlers_profile.go).
func newProfileSvc() *service.ProfileService {
	cfg := config.Default()
	if home, err := os.UserHomeDir(); err == nil {
		if loaded, loadErr := config.Load(filepath.Join(home, ".mneme", "config.toml")); loadErr == nil {
			cfg = loaded
		}
	}
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}
	return service.NewProfileService(cfg.ProfilesDir(), false)
}

// newProfileAddCmd returns the "mneme profile add" subcommand.
func newProfileAddCmd() *cobra.Command {
	var (
		flagName  string
		flagRef   string
		flagForce bool
	)

	cmd := &cobra.Command{
		Use:   "add <git-url>",
		Short: "Clone a profile into the host-level store (once)",
		Long: `Clone a profile's git repository into the host-level store
(~/.mneme/profiles/<name>/), shared by every project on this host.

The profile's name is derived from its mneme-profile.toml manifest unless
--name is passed explicitly, in which case it must match the manifest's
declared name.`,
		Example: `  mneme profile add git@github.com:chateapro/mneme-profile.git
  mneme profile add https://github.com/chateapro/mneme-profile.git --ref v3
  mneme profile add git@github.com:chateapro/mneme-profile.git --name chatea-pro --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newProfileSvc()

			res, err := svc.Add(args[0], flagName, flagRef, flagForce)
			if err != nil {
				if errors.Is(err, model.ErrProfileExists) {
					return fmt.Errorf("profile add: already installed — use `mneme profile update` or pass --force: %w", err)
				}
				return fmt.Errorf("profile add: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s@%s (version %s) -> %s\n", res.Name, res.Ref, res.Version, res.Path)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagName, "name", "", "Override the profile name (must match the manifest's declared name)")
	cmd.Flags().StringVar(&flagRef, "ref", "", "Tag/branch/commit to check out after cloning")
	cmd.Flags().BoolVar(&flagForce, "force", false, "Overwrite an existing installation")
	return cmd
}

// newProfileUpdateCmd returns the "mneme profile update" subcommand.
func newProfileUpdateCmd() *cobra.Command {
	var flagRef string

	cmd := &cobra.Command{
		Use:   "update [<name>] [--ref R]",
		Short: "Update a profile in the store (fetch + checkout)",
		Long: `Fetch and check out the latest state of an installed profile.

When <name> is omitted, the current repository's pin (.mneme-profile) is
resolved and its name (and, absent --ref, its ref) is used instead.`,
		Example: `  mneme profile update chatea-pro
  mneme profile update chatea-pro --ref v4
  mneme profile update`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newProfileSvc()

			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			ref := flagRef

			if name == "" {
				root, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("profile update: %w", err)
				}
				resolution, err := svc.ResolvePin(root)
				if err != nil {
					return fmt.Errorf("profile update: resolve pin: %w", err)
				}
				if resolution.Pin == nil || resolution.Pin.IsDefault() {
					return fmt.Errorf("profile update: no pinned profile with a source in this repo — pass <name> explicitly")
				}
				name = resolution.Pin.Name
				if ref == "" {
					ref = resolution.Pin.Ref
				}
			}

			result, err := svc.Update(name, ref)
			if err != nil {
				if errors.Is(err, model.ErrProfileNotFound) {
					return fmt.Errorf("profile update: %q is not installed — run `mneme profile add` first: %w", name, err)
				}
				return fmt.Errorf("profile update: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s: %s -> %s (version %s)\n", result.Name, result.OldRef, result.NewRef, result.Version)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagRef, "ref", "", "Tag/branch/commit to check out")
	return cmd
}

// newProfileListCmd returns the "mneme profile list" subcommand.
func newProfileListCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List profiles in the host-level store",
		Long: `List every profile installed in the host-level store
(~/.mneme/profiles/), marking the one that matches the current repo's pin.`,
		Example: `  mneme profile list
  mneme profile list --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newProfileSvc()

			infos, err := svc.List()
			if err != nil {
				return fmt.Errorf("profile list: %w", err)
			}

			activeName := currentPinnedName(svc)

			if flagJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(infos)
			}

			if len(infos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No profiles installed.")
				return nil
			}

			for _, info := range infos {
				marker := "  "
				if info.Name == activeName {
					marker = "* "
				}
				if !info.Valid {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%s [invalid]: %s\n", marker, info.Name, info.Error)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s@%s (v%s) -> %s\n", marker, info.Name, info.Ref, info.Version, info.Path)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// currentPinnedName returns the pin name for the current working directory's
// repo, or "" when it cannot be resolved (no pin, error, etc). Best-effort —
// used only to mark the active profile in `profile list`'s human-readable
// output, never to fail the command.
func currentPinnedName(svc *service.ProfileService) string {
	root, err := os.Getwd()
	if err != nil {
		return ""
	}
	res, err := svc.ResolvePin(root)
	if err != nil || res.Pin == nil {
		return ""
	}
	return res.Pin.Name
}

// newProfileStatusCmd returns the "mneme profile status" subcommand.
func newProfileStatusCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report the current repo's pin resolution (read-only)",
		Long: `Report the pin resolution (.mneme-profile) for the current repository:
installed, missing (needs "profile add"), default (mneme's internal
profile), or absent (no pin at all). Read-only — never writes anything.`,
		Example: `  mneme profile status
  mneme profile status --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newProfileSvc()

			root, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("profile status: %w", err)
			}
			res, err := svc.ResolvePin(root)
			if err != nil {
				return fmt.Errorf("profile status: %w", err)
			}

			if flagJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintln(cmd.OutOrStdout(), profileStatusLine(res))
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// profileStatusLine renders a service.ProfileResolution as a single
// human-readable line for `profile status`'s default (non-JSON) output.
func profileStatusLine(res service.ProfileResolution) string {
	switch res.State {
	case service.ProfilePinAbsent:
		return "No profile pin (.mneme-profile) in this repo."
	case service.ProfilePinDefault:
		return fmt.Sprintf("Pinned to the default (internal) profile — %s", res.Pin.Name)
	case service.ProfilePinInstalled:
		return fmt.Sprintf("Installed: %s@%s (version %s) -> %s", res.Pin.Name, res.Pin.Ref, res.Manifest.Version, res.Path)
	case service.ProfilePinMissing:
		return fmt.Sprintf("Pinned to %s (source %s) but NOT installed — run `mneme profile add %s`",
			res.Pin.Name, res.Pin.Source, res.Pin.Source)
	default:
		return "unknown pin state"
	}
}
