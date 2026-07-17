package profile

import (
	"errors"
	"fmt"
	"path"
)

// ErrLayoutUnsupported is returned by PlanNewProject for a layout §7a does not
// yet assemble — today only LayoutMonorepo, whose composable engine (bootstrap
// + overlay + blueprints + wiring) arrives in §7b (SPEC-099). It is a distinct
// sentinel (not ErrInvalidLayout) precisely because the scaffold is VALID —
// only its assembly is not yet implemented.
var ErrLayoutUnsupported = errors.New("profile: scaffold layout not yet supported")

// BootstrapStep describes the one pinned, official-generator subprocess the
// assembly engine runs before the profile overlay (docs/profiles-design.md
// §15.4 layer 1). It is inert data — the leaf never spawns anything; the
// service's injected Bootstrapper executes it. §7a produces no BootstrapStep
// (single has no bootstrap); the type exists so §7b's monorepo planner and the
// service's execution seam are both ready.
type BootstrapStep struct {
	// Generator is the official generator package name (e.g. "create-turbo").
	Generator string

	// Version is the exact pinned version (e.g. "2.3.1") — guaranteed by
	// ParseScaffold's ErrBootstrapNotPinned invariant to never be "latest".
	Version string

	// Dest is the absolute destination directory the generator writes into.
	Dest string
}

// CopyStep describes one tree copied from the profile's filesystem into the
// new project, with per-file "{{<var>}}" substitution applied. Src is a path
// RELATIVE to the profile's contents FS (e.g. "scaffolds/library-go/skeleton")
// so the service copies it through the exact fs.FS abstraction a store
// checkout and the embedded OSS default profile share — the leaf never
// resolves a real path or reads a byte itself.
type CopyStep struct {
	// Src is the FS-relative source directory within the profile's contents.
	Src string

	// Dest is the absolute destination directory.
	Dest string

	// Vars is the fully-resolved substitution map applied to every copied
	// file's contents.
	Vars map[string]string

	// Optional, when true, tells the service to skip this copy silently if Src
	// does not exist in the profile FS. A single scaffold's skeleton is always
	// required (Optional=false); a monorepo's overlay/ and captured shell/ are
	// optional (a turborepo scaffold may ship neither, letting the bootstrap
	// provide the whole tree).
	Optional bool
}

// BlueprintStep describes one composable app copied from the profile's shared
// _blueprints/<name>/ into a monorepo's apps directory (docs/profiles-design.md
// §15.4 layer 3), with per-file "{{<var>}}" substitution. Src is FS-relative
// (resolved through the profile's contents FS); Dest is the absolute
// apps_dir/<app> directory. Blueprints are planned in /new-project (initial
// apps) and /new-app (one app added to an existing monorepo).
type BlueprintStep struct {
	// Src is the FS-relative _blueprints/<name> source directory.
	Src string

	// Dest is the absolute apps_dir/<app> destination directory.
	Dest string

	// Vars is the resolved substitution map applied to every copied file.
	Vars map[string]string
}

// AssemblyPlan is the pure, deterministic description of how to assemble one
// new project — computed by the leaf (no disk, no exec, no network) and
// executed by the service (SPEC-098 §7a §4.2, the leaf-plans/service-executes
// invariant). Given the same ScaffoldDef and ProjectChoices, PlanNewProject
// always returns the same plan, which is what makes the engine golden-testable
// without touching the filesystem.
//
// §7a populates Bootstrap (always nil for single), Copies, and GitInit. The
// composable monorepo fields (blueprints, wiring) are §7b — added to this
// struct there, never speculatively here.
type AssemblyPlan struct {
	// Bootstrap is the pinned generator step, or nil when the scaffold has no
	// bootstrap (single, or a captured custom shell). Populated for a monorepo
	// turborepo scaffold (§7b).
	Bootstrap *BootstrapStep

	// Copies are the trees to copy (with substitution), in order: a single
	// scaffold's skeleton, or a monorepo's shell + overlay.
	Copies []CopyStep

	// Blueprints are the composable apps to drop into a monorepo's apps
	// directory (§7b). Empty for a single scaffold and for /new-project runs
	// that select no initial apps.
	Blueprints []BlueprintStep

	// Wiring are the root-file edits that register each blueprint/app into the
	// monorepo (§7b). Empty for single and for a plan that adds no apps.
	Wiring []WiringEdit

	// GitInit requests a plain `git init` (no commit, no remote) once the tree
	// is materialized — the precedent set by profile.Scaffold (§5) and
	// Store.Add (§1). Set for /new-project (a fresh repo); false for /new-app
	// (an existing repo already has its .git).
	GitInit bool
}

// ProjectChoices carries the caller-supplied inputs PlanNewProject needs: the
// destination directory and the resolved substitution variables. Blueprint
// selection (monorepo) is a §7b field, added when that layout is assembled.
type ProjectChoices struct {
	// Dest is the absolute destination directory the project is assembled
	// into. Required.
	Dest string

	// Vars is the fully-resolved substitution map (ScaffoldDef.ResolveVars of
	// the caller's provided values over the scaffold's declared defaults).
	Vars map[string]string

	// Blueprints names the initial composable apps to include (monorepo only,
	// §7b). Each must be one the scaffold declares; every entry is copied into
	// the apps directory under its own name and wired in. Empty means "no
	// initial apps" — a bare monorepo shell.
	Blueprints []string
}

// PlanNewProject computes the AssemblyPlan for creating a new project from
// scaffold s (SPEC-098 §7a AC2). For LayoutSingle it plans a single copy of
// the scaffold's skeleton/ tree into choices.Dest with variable substitution,
// no bootstrap, and a trailing git init. LayoutMonorepo is a valid scaffold
// whose assembly is deferred to §7b — it returns ErrLayoutUnsupported so a
// caller gets an actionable "not yet" rather than a silent empty plan. An
// unrecognized layout (which ParseScaffold would already have rejected)
// returns ErrInvalidLayout defensively.
func PlanNewProject(s ScaffoldDef, choices ProjectChoices) (AssemblyPlan, error) {
	if choices.Dest == "" {
		return AssemblyPlan{}, fmt.Errorf("profile: plan new project: destination is required")
	}

	switch s.Layout {
	case LayoutSingle:
		return AssemblyPlan{
			Bootstrap: nil,
			Copies: []CopyStep{{
				Src:  path.Join(scaffoldsSubdir, s.Name, skeletonSubdir),
				Dest: choices.Dest,
				Vars: choices.Vars,
			}},
			GitInit: true,
		}, nil
	case LayoutMonorepo:
		return planMonorepo(s, choices)
	default:
		return AssemblyPlan{}, fmt.Errorf("profile: plan new project: layout %q: %w", s.Layout, ErrInvalidLayout)
	}
}

// planMonorepo computes the layered assembly of a monorepo project (SPEC-099
// §7b §4.2): the pinned bootstrap (turborepo) OR a captured shell/ copy
// (custom), the profile's overlay/ applied on top, then each selected blueprint
// dropped into the apps directory and wired into the root files. The overlay
// and shell copies are Optional (a turborepo scaffold may ship neither), while
// blueprints and their wiring only appear when choices.Blueprints is non-empty.
func planMonorepo(s ScaffoldDef, choices ProjectChoices) (AssemblyPlan, error) {
	plan := AssemblyPlan{GitInit: true}
	scaffoldDir := path.Join(scaffoldsSubdir, s.Name)

	if s.Bootstrap != "" {
		gen, ver, ok := s.BootstrapParts()
		if !ok {
			// ParseScaffold guarantees this never happens; defensive.
			return AssemblyPlan{}, fmt.Errorf("profile: plan monorepo: bootstrap %q is not pinned: %w", s.Bootstrap, ErrBootstrapNotPinned)
		}
		plan.Bootstrap = &BootstrapStep{Generator: gen, Version: ver, Dest: choices.Dest}
	} else {
		// No bootstrap → a captured custom shell provides the base tree.
		plan.Copies = append(plan.Copies, CopyStep{
			Src:      path.Join(scaffoldDir, shellSubdir),
			Dest:     choices.Dest,
			Vars:     choices.Vars,
			Optional: true,
		})
	}

	overlay := s.Overlay
	if overlay == "" {
		overlay = overlaySubdir
	}
	plan.Copies = append(plan.Copies, CopyStep{
		Src:      path.Join(scaffoldDir, overlay),
		Dest:     choices.Dest,
		Vars:     choices.Vars,
		Optional: true,
	})

	wirer, err := WirerFor(s)
	if err != nil {
		return AssemblyPlan{}, fmt.Errorf("profile: plan monorepo: %w", err)
	}

	for _, bp := range choices.Blueprints {
		if !s.declaresBlueprint(bp) {
			return AssemblyPlan{}, fmt.Errorf("profile: plan monorepo: blueprint %q is not offered by scaffold %q: %w", bp, s.Name, ErrScaffoldNotFound)
		}
		bpPlan, err := planBlueprint(s, wirer, bp, bp, choices.Dest, choices.Vars)
		if err != nil {
			return AssemblyPlan{}, err
		}
		plan.Blueprints = append(plan.Blueprints, bpPlan.step)
		plan.Wiring = append(plan.Wiring, bpPlan.edits...)
	}

	return plan, nil
}

// AddAppChoices carries PlanAddApp's inputs: the blueprint to instantiate, the
// name the new app takes in the monorepo, the monorepo root, and the resolved
// substitution variables.
type AddAppChoices struct {
	// Blueprint is the _blueprints/<name> archetype to copy. Required.
	Blueprint string

	// AppName is the directory name the app takes under the apps directory.
	// Required; must be a safe-slug (anti-traversal).
	AppName string

	// MonorepoRoot is the absolute root of the existing monorepo the app is
	// added to. Required.
	MonorepoRoot string

	// Vars is the resolved substitution map applied to the copied blueprint.
	Vars map[string]string
}

// PlanAddApp computes the AssemblyPlan for adding one composable app to an
// existing monorepo (SPEC-099 §7b AC10): it selects the scaffold's Wirer
// (turborepo built-in or custom [wiring]), copies _blueprints/<blueprint> into
// apps_dir/<app>, and emits the root-file wiring edits. GitInit is false — the
// monorepo already has its .git. A single-layout scaffold yields
// ErrAppAddNotApplicable ("does not apply"); an unknown blueprint the scaffold
// does not declare yields ErrScaffoldNotFound.
func PlanAddApp(s ScaffoldDef, choices AddAppChoices) (AssemblyPlan, error) {
	if choices.Blueprint == "" {
		return AssemblyPlan{}, fmt.Errorf("profile: plan add app: blueprint is required")
	}
	if choices.AppName == "" {
		return AssemblyPlan{}, fmt.Errorf("profile: plan add app: app name is required")
	}
	if choices.MonorepoRoot == "" {
		return AssemblyPlan{}, fmt.Errorf("profile: plan add app: monorepo root is required")
	}
	if !isSafeSlug(choices.AppName) {
		return AssemblyPlan{}, fmt.Errorf("profile: plan add app: app name %q must match %s: %w", choices.AppName, safeSlugPattern.String(), ErrInvalidScaffold)
	}

	wirer, err := WirerFor(s)
	if err != nil {
		return AssemblyPlan{}, fmt.Errorf("profile: plan add app: %w", err)
	}
	if !s.declaresBlueprint(choices.Blueprint) {
		return AssemblyPlan{}, fmt.Errorf("profile: plan add app: blueprint %q is not offered by scaffold %q: %w", choices.Blueprint, s.Name, ErrScaffoldNotFound)
	}

	bpPlan, err := planBlueprint(s, wirer, choices.Blueprint, choices.AppName, choices.MonorepoRoot, choices.Vars)
	if err != nil {
		return AssemblyPlan{}, err
	}
	return AssemblyPlan{
		Blueprints: []BlueprintStep{bpPlan.step},
		Wiring:     bpPlan.edits,
		GitInit:    false,
	}, nil
}

// blueprintPlan bundles a single blueprint's copy step with its wiring edits.
type blueprintPlan struct {
	step  BlueprintStep
	edits []WiringEdit
}

// planBlueprint computes the copy + wiring for one blueprint instantiated as
// appName under the destination monorepo. Shared by planMonorepo (initial apps)
// and PlanAddApp (one added app) so both compose the apps directory identically.
func planBlueprint(s ScaffoldDef, wirer Wirer, blueprint, appName, monorepoRoot string, vars map[string]string) (blueprintPlan, error) {
	appDest := joinDest(monorepoRoot, wirer.AppsDir(), appName)
	edits, err := wirer.PlanWire(monorepoRoot, appName, blueprint, vars)
	if err != nil {
		return blueprintPlan{}, fmt.Errorf("profile: plan blueprint %q: %w", blueprint, err)
	}
	return blueprintPlan{
		step: BlueprintStep{
			Src:  path.Join(blueprintsSubdir, blueprint),
			Dest: appDest,
			Vars: vars,
		},
		edits: edits,
	}, nil
}

// declaresBlueprint reports whether blueprint is in the scaffold's declared
// Blueprints list — the offering the /new-app grill presents. An app can only
// be grown from a blueprint the archetype explicitly offers.
func (s ScaffoldDef) declaresBlueprint(blueprint string) bool {
	for _, b := range s.Blueprints {
		if b == blueprint {
			return true
		}
	}
	return false
}
