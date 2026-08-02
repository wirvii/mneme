package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newProfileCmd returns the "mneme profile" subcommand group: a team's
// working methodology packaged as a portable git repo, activated with
// nvm-like semantics. §1 (SPEC-091) covers the foundation — the store
// (add/update/list) and read-only pin resolution (status). §3 (SPEC-093)
// adds the two write-verbs: "use" (per-repo, immediate) and "default"
// (host-level, sessions with no repo pin).
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
  new        Scaffold a brand-new profile repo (structure + manifest + git init).
  add        Clone a profile into the host-level store (once).
  update     Fetch + checkout the latest state of an installed profile.
  list       List profiles in the host-level store.
  status     Report the current repo's pin resolution (read-only).
  use        Activate a profile for THIS repo now (writes the pin + materializes).
  default    Set/clear/print the HOST-level default for repos with no pin.
  deactivate Undo THIS repo's active profile's materialization (dry-run by default).`,
	}

	cmd.AddCommand(
		newProfileNewCmd(),
		newProfileAddCmd(),
		newProfileUpdateCmd(),
		newProfileListCmd(),
		newProfileStatusCmd(),
		newProfileUseCmd(),
		newProfileDefaultCmd(),
		newProfileDeactivateCmd(),
	)

	return cmd
}

// newProfileSvc constructs a ProfileService targeting the host-level profile
// store (~/.mneme/profiles, or DataDir/profiles when --data-dir overrides
// it — mirroring initService's own precedence). noPrompt is always false for
// the CLI frontend: a developer at a terminal can authenticate interactively
// (design decision #11); the MCP frontend passes true instead (see
// internal/mcp/handlers_profile.go).
//
// configPath is always wired (SPEC-093 §3) so "profile default"/"profile
// status" (via ResolveActive, when a future caller needs it) can read/write
// [profiles].default — harmless no-op for add/update/list/status, which
// never touch it.
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
	return service.NewProfileService(cfg.ProfilesDir(), false,
		service.WithProfileConfigPath(config.DefaultPath()),
	)
}

// newActivatingProfileSvc constructs a ProfileService fully wired for
// Use/Activate (SPEC-093 §3.2): mem (rule provenance), sub (capa-2/3
// fusion), and the host-level skills directory, on top of the same
// MemoryService/database initService() builds for every other CLI command —
// same pattern as initSubagentService (internal/cli/subagents.go). Only
// "profile use" needs this heavier construction; add/update/list/status/
// default never materialize, so they keep the lighter newProfileSvc().
// WithDefaultProfileFS is wired for parity with the SessionStart integration
// (SPEC-096 §6) — "profile use mneme-default" is not an officially supported
// verb (the embedded default is never a store checkout PinFromStore can
// reconstruct a pin from), but a ProfileService fully capable of a default
// activation costs nothing extra here and keeps every Activate call site
// consistent.
func newActivatingProfileSvc() (*service.ProfileService, func(), error) {
	mem, cleanup, err := initService()
	if err != nil {
		return nil, nil, err
	}

	cfg := mem.Config()
	sub := service.NewSubagentService(mem)
	skillsDir := ""
	if home, herr := os.UserHomeDir(); herr == nil {
		skillsDir = filepath.Join(home, ".claude", "skills")
	}

	svc := service.NewProfileService(cfg.ProfilesDir(), false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
		service.WithProfileConfigPath(config.DefaultPath()),
		service.WithDefaultProfileFS(install.DefaultProfileFS()),
	)
	return svc, cleanup, nil
}

// newProfileNewCmd returns the "mneme profile new" subcommand (SPEC-095 §5).
func newProfileNewCmd() *cobra.Command {
	var flagDir string

	cmd := &cobra.Command{
		Use:   "new <name> [--dir <path>]",
		Short: "Scaffold a brand-new profile repo (structure + manifest + git init)",
		Long: `Scaffold a new profile REPOSITORY: the source a profile author curates,
commits, and pushes — BEFORE any consumer ever runs "profile add" against it.

Creates the standard directory tree (agents/ skills/ blocks/ templates/
scaffolds/_blueprints/), a stub mneme-profile.toml/rules.jsonl/models.toml/
policy.toml/README.md, and runs "git init" (no commit, no remote). Never
touches the host-level store (~/.mneme/profiles/) — that only happens later,
when a consumer runs "profile add" against the pushed repo.

Curate the scaffolded content with the mneme-profile-author skill (or by
hand): agents/<role>.md, skills/, rules.jsonl, blocks/, models.toml,
policy.toml, templates/.`,
		Example: `  mneme profile new chatea-pro
  mneme profile new chatea-pro --dir /path/to/repo`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newProfileSvc()

			res, err := svc.NewProfile(service.NewProfileInput{Name: args[0], Dir: flagDir})
			if err != nil {
				if errors.Is(err, model.ErrProfileExists) {
					return fmt.Errorf("profile new: destination not empty — choose another directory or empty it: %w", err)
				}
				return fmt.Errorf("profile new: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Scaffolded %s -> %s\n", res.Name, res.Path)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagDir, "dir", "", "Destination directory (default: <cwd>/<name>)")
	return cmd
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

// newProfileUseCmd returns the "mneme profile use" subcommand (SPEC-093
// §3.2, "= nvm use").
func newProfileUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Activate a profile for THIS repo now (writes the pin + materializes)",
		Long: `Activate an already-installed profile for the current repository,
immediately: reconstructs a self-describing pin from the profile's checkout
in the host-level store (name + the checkout's origin remote + its exact
tag/commit), writes it to .mneme-profile at the repo root, and materializes
it right away (agents/skills/blocks/rules).

"use" never clones — <name> must already be installed via "profile add".
A preexisting "scaffold" field in the current pin (if any) is preserved.`,
		Example: `  mneme profile use chatea-pro`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newActivatingProfileSvc()
			if err != nil {
				return fmt.Errorf("profile use: %w", err)
			}
			defer cleanup()

			root, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("profile use: %w", err)
			}

			res, err := svc.Use(cmd.Context(), root, args[0])
			if err != nil {
				if errors.Is(err, model.ErrProfileNotFound) {
					return fmt.Errorf("profile use: %q is not installed — run `mneme profile add` first: %w", args[0], err)
				}
				return fmt.Errorf("profile use: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Activated %s@%s -> %s (materialized)\n", res.Name, res.Ref, res.ProjectRoot)
			for _, w := range res.Warnings {
				fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", w)
			}
			return nil
		},
	}
	return cmd
}

// newProfileDefaultCmd returns the "mneme profile default" subcommand
// (SPEC-093 §3.3, "= nvm alias default").
func newProfileDefaultCmd() *cobra.Command {
	var flagClear bool

	cmd := &cobra.Command{
		Use:   "default [<name>]",
		Short: "Set/clear/print the HOST-level default profile for repos with no pin",
		Long: `Fix (or clear, or print) the host-level default profile
(~/.mneme/config.toml's [profiles].default): the profile a session activates
at SessionStart when the repository has NO .mneme-profile pin.

This does NOT materialize anything and does NOT re-point sessions already
running — it only affects sessions started AFTER this call, in repos with no
pin of their own. Use "profile use" to activate a profile in THIS repo now.`,
		Example: `  mneme profile default chatea-pro
  mneme profile default --clear
  mneme profile default`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newProfileSvc()

			switch {
			case flagClear:
				if _, err := svc.ClearDefault(); err != nil {
					return fmt.Errorf("profile default: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Default global limpiado — vuelve a vanilla.")
				return nil

			case len(args) == 1:
				res, err := svc.SetDefault(args[0])
				if err != nil {
					if errors.Is(err, model.ErrProfileNotFound) {
						return fmt.Errorf("profile default: %q is not installed — run `mneme profile add` first: %w", args[0], err)
					}
					return fmt.Errorf("profile default: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(),
					"Default global = %s. Afecta sesiones NUEVAS. Para activar en ESTE repo ahora: `mneme profile use %s`.\n",
					res.Default, res.Default)
				return nil

			default:
				res, err := svc.Default()
				if err != nil {
					return fmt.Errorf("profile default: %w", err)
				}
				if res.Default == "" {
					fmt.Fprintln(cmd.OutOrStdout(), "No default global configurado (vanilla).")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), res.Default)
				}
				return nil
			}
		},
	}

	cmd.Flags().BoolVar(&flagClear, "clear", false, "Clear the default (revert to vanilla)")
	return cmd
}

// newProfileDeactivateCmd returns the "mneme profile deactivate" subcommand
// (SPEC-105 DD17): dry-run by default — prints the plan, mutates nothing —
// and executes only with --apply, mirroring `mneme init --apply`/`mneme
// conflicts scan --apply`'s precedent. Deliberately does NOT touch
// .mneme-profile (DD19): the pin is a committed, team-shared file; this
// undoes the LOCAL materialization only.
func newProfileDeactivateCmd() *cobra.Command {
	var flagApply bool
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "deactivate",
		Short: "Undo THIS repo's active profile's materialization (dry-run by default)",
		Long: `Compute the plan to undo whatever profile is active for the current
repository and, with --apply, execute it: every materialized agent/skill is
restored (if it displaced a dev's own file) or removed, the "profile" managed
block is removed from CLAUDE.md, every rule with this profile's provenance is
purged, and the activation lock (.mneme/profile.lock) is deleted.

Deliberately never touches .mneme-profile (the pin) — it is a committed,
team-shared file. If the repo's pin (or the host default) still points at
this profile, the NEXT SessionStart will simply reactivate it; the report's
"NextSession" line says so explicitly, before anything is applied.

Without --apply: prints the plan, mutates nothing.
With --apply: executes it.`,
		Example: `  mneme profile deactivate
  mneme profile deactivate --apply
  mneme profile deactivate --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newActivatingProfileSvc()
			if err != nil {
				return fmt.Errorf("profile deactivate: %w", err)
			}
			defer cleanup()

			root, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("profile deactivate: %w", err)
			}

			result, err := svc.DeactivateProject(cmd.Context(), service.DeactivateInput{RepoRoot: root, Apply: flagApply})
			if err != nil {
				if errors.Is(err, model.ErrProfileLockUnsupported) {
					return fmt.Errorf("profile deactivate: lock escrito por una versión más nueva de mneme; ejecuta `mneme upgrade`: %w", err)
				}
				return fmt.Errorf("profile deactivate: %w", err)
			}

			if flagJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			renderDeactivatePlan(cmd.OutOrStdout(), result)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagApply, "apply", false, "Execute the plan (default: dry-run, mutates nothing)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// renderDeactivatePlan renders a service.DeactivateResult as human-readable
// text for `profile deactivate`'s default (non-JSON) output — the same
// object dry-run and --apply share (Applied distinguishes them).
func renderDeactivatePlan(w io.Writer, result *service.DeactivateResult) {
	for _, msg := range result.Warnings {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
	if len(result.Artifacts) == 0 && !result.Applied && result.Profile == "" {
		return // nothing to deactivate — the warning above already said so.
	}

	verb := "Plan"
	if result.Applied {
		verb = "Aplicado"
	}
	fmt.Fprintf(w, "%s: desactivar %s@%s (activado %s)\n", verb, result.Profile, result.Commit, result.ActivatedAt.Format("2006-01-02 15:04:05 MST"))

	for _, a := range result.Artifacts {
		exists := "no existe"
		if a.Exists {
			exists = "existe"
		}
		fmt.Fprintf(w, "  - [%s] %s (%s, %s)\n", a.Kind, a.Path, a.Action, exists)
	}

	fmt.Fprintf(w, "Rules del proyecto con esta proveniencia: %d\n", len(result.RuleIDs))
	if len(result.OrphanRuleIDs) > 0 {
		fmt.Fprintf(w, "Rules huérfanas en el store global: %d\n", len(result.OrphanRuleIDs))
	}
	if len(result.ResidualBackups) > 0 {
		fmt.Fprintf(w, "Directorios de respaldo residuales (de otras corridas, no tocados): %d\n", len(result.ResidualBackups))
		for _, dir := range result.ResidualBackups {
			fmt.Fprintf(w, "  - %s\n", dir)
		}
	}

	fmt.Fprintf(w, "\nNextSession: %s\n", result.NextSession)

	if !result.Applied {
		fmt.Fprintf(w, "\nEjecuta con --apply para aplicar este plan.\n")
	}
}
