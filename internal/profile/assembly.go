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
	// bootstrap (single, or a captured custom shell). Always nil in §7a.
	Bootstrap *BootstrapStep

	// Copies are the trees to copy (with substitution), in order.
	Copies []CopyStep

	// GitInit requests a plain `git init` (no commit, no remote) once the tree
	// is materialized — the precedent set by profile.Scaffold (§5) and
	// Store.Add (§1).
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
		return AssemblyPlan{}, fmt.Errorf("profile: plan new project: monorepo assembly arrives in §7b: %w", ErrLayoutUnsupported)
	default:
		return AssemblyPlan{}, fmt.Errorf("profile: plan new project: layout %q: %w", s.Layout, ErrInvalidLayout)
	}
}
