package profile

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Capture-authoring variable names (SPEC-100 §7c). `mneme scaffold capture`
// parametrizes an exemplar repo's identity so a generated project can supply
// its own: every literal occurrence of the exemplar's project name / Go module
// path in a copied file is rewritten to the matching "{{<VAR>}}" placeholder,
// and each becomes a [vars] entry the /new-project grill later elicits.
const (
	// CaptureVarProjectName is the placeholder for the exemplar's project
	// identity (its directory basename or package.json "name").
	CaptureVarProjectName = "PROJECT_NAME"

	// CaptureVarModulePath is the placeholder for the exemplar's Go module path
	// (from go.mod), present only when the exemplar declares one.
	CaptureVarModulePath = "MODULE_PATH"
)

// ErrNothingToCapture is returned by PlanCapture when a RepoStructure describes
// no capturable content — no root files, no apps, no packages. Its model-level
// twin (model.ErrNothingToCapture) is what cli/mcp match against.
var ErrNothingToCapture = errors.New("profile: nothing to capture from the exemplar repository")

// RepoStructure is the pure, already-detected description of an exemplar
// repository (SPEC-100 §7c): the facts `mneme scaffold capture` gleans on to
// infer a scaffold's layout/toolchain and plan the capture. The service does
// the filesystem detection (walk the repo, read go.mod/package.json) and hands
// this inert struct to the leaf, keeping PlanCapture pure and golden-testable
// without touching disk.
type RepoStructure struct {
	// ProjectName is the exemplar's identity literal to parametrize (its
	// directory basename, or the package.json "name"). Empty when undetectable.
	ProjectName string

	// ModulePath is the exemplar's Go module path (go.mod), or empty when the
	// repo is not a Go module.
	ModulePath string

	// Apps are the immediate subdirectory names of apps/ — each captured as a
	// composable blueprint (monorepo). Sorted by the caller or by PlanCapture.
	Apps []string

	// HasPackages reports whether a packages/ directory exists (captured into
	// the scaffold's overlay/ as shared team content).
	HasPackages bool

	// HasTurboJSON reports whether a root turbo.json exists — the signal for
	// the turborepo toolchain.
	HasTurboJSON bool

	// HasPnpmWorkspace reports whether a root pnpm-workspace.yaml exists — a
	// monorepo signal, and (absent turbo.json) the custom toolchain.
	HasPnpmWorkspace bool

	// RootFiles are the repo-root regular file names captured into the
	// scaffold's shell/ (monorepo) — the structural config the team shares
	// (turbo.json, package.json, tsconfig.json, .gitignore, …).
	RootFiles []string
}

// CaptureCopy is one pure copy instruction in a CapturePlan: a source relative
// to the exemplar repo root, and a destination relative to the PROFILE repo
// root (so the service only ever joins profileDir+Dest). The leaf never reads a
// byte — it computes WHERE things move; the service copies the trees, applying
// the plan's parametrization substitutions to each file's contents.
type CaptureCopy struct {
	// Src is the exemplar-repo-relative source. "." means the whole repo (the
	// single-layout skeleton capture); otherwise a file ("package.json") or a
	// directory ("apps/web", "packages").
	Src string

	// Dest is the profile-repo-relative destination
	// ("scaffolds/<name>/shell/package.json", "_blueprints/web").
	Dest string

	// IsDir is true when Src is a directory tree to copy recursively, false for
	// a single file.
	IsDir bool
}

// CapturePlan is the pure result of PlanCapture (SPEC-100 §7c): the draft
// ScaffoldDef, its rendered (and re-validated) scaffold.toml bytes, the copy
// instructions, and the parametrization map (literal exemplar value ->
// "{{VAR}}" placeholder) the service applies while copying. Given the same
// RepoStructure, PlanCapture always yields the same plan — deterministic and
// testable without disk or network.
type CapturePlan struct {
	// Name is the scaffold's catalog name (a validated safe-slug).
	Name string

	// Def is the inferred draft ScaffoldDef.
	Def ScaffoldDef

	// TOML is the rendered scaffold.toml, already round-tripped through
	// ParseScaffold so the service can write it verbatim knowing it is valid.
	TOML []byte

	// ScaffoldTOMLDest is the profile-repo-relative path the TOML is written to
	// ("scaffolds/<name>/scaffold.toml").
	ScaffoldTOMLDest string

	// Copies are the exemplar->profile tree/file copies, in a stable order.
	Copies []CaptureCopy

	// Params maps each detected literal (project name, module path) to its
	// "{{VAR}}" placeholder. The service rewrites file contents with it,
	// longest literal first so a module path containing the project name
	// substitutes before the bare name.
	Params map[string]string
}

// InferLayout derives a scaffold's layout and toolchain from a detected
// RepoStructure (SPEC-100 §7c): any monorepo signal (apps/, turbo.json, or
// pnpm-workspace.yaml) yields LayoutMonorepo — turborepo when a turbo.json is
// present, custom otherwise; anything else is LayoutSingle with no toolchain.
// Pure and total: every RepoStructure maps to exactly one (layout, toolchain).
func InferLayout(rs RepoStructure) (Layout, Toolchain) {
	monorepo := len(rs.Apps) > 0 || rs.HasTurboJSON || rs.HasPnpmWorkspace
	if !monorepo {
		return LayoutSingle, ""
	}
	if rs.HasTurboJSON {
		return LayoutMonorepo, ToolchainTurborepo
	}
	return LayoutMonorepo, ToolchainCustom
}

// PlanCapture computes the CapturePlan for turning an exemplar RepoStructure
// into a new scaffold named name (SPEC-100 §7c). It validates name as a
// safe-slug, infers the layout/toolchain, drafts the ScaffoldDef (with [vars]
// for the parametrized identity and, for a custom toolchain, a [wiring] block),
// renders and re-validates the scaffold.toml, and enumerates the copies:
// single -> the whole repo into skeleton/; monorepo -> root files into shell/,
// packages/ into overlay/packages, each apps/<app> into _blueprints/<app>. It
// is pure — the service performs the detection that fills RepoStructure and the
// I/O that executes the plan.
func PlanCapture(name string, rs RepoStructure) (CapturePlan, error) {
	if !isSafeSlug(name) {
		return CapturePlan{}, fmt.Errorf("profile: capture: scaffold name %q must match %s: %w", name, safeSlugPattern.String(), ErrInvalidScaffold)
	}
	if !rs.hasContent() {
		return CapturePlan{}, fmt.Errorf("profile: capture %q: %w", name, ErrNothingToCapture)
	}

	layout, toolchain := InferLayout(rs)
	def := ScaffoldDef{
		Name:      name,
		Layout:    layout,
		Toolchain: toolchain,
		Vars:      captureVars(rs),
	}

	scaffoldDir := path.Join(scaffoldsSubdir, name)
	var copies []CaptureCopy

	if layout == LayoutSingle {
		copies = append(copies, CaptureCopy{
			Src:   ".",
			Dest:  path.Join(scaffoldDir, skeletonSubdir),
			IsDir: true,
		})
	} else {
		apps := append([]string(nil), rs.Apps...)
		sort.Strings(apps)
		def.Blueprints = apps

		if toolchain == ToolchainCustom {
			def.Wiring = &WiringSpec{
				AppsDir: "apps/",
				OnAdd:   []string{"workspace:pnpm-workspace.yaml"},
			}
		}

		for _, f := range sortedUnique(rs.RootFiles) {
			copies = append(copies, CaptureCopy{
				Src:   f,
				Dest:  path.Join(scaffoldDir, shellSubdir, f),
				IsDir: false,
			})
		}
		if rs.HasPackages {
			copies = append(copies, CaptureCopy{
				Src:   "packages",
				Dest:  path.Join(scaffoldDir, overlaySubdir, "packages"),
				IsDir: true,
			})
		}
		for _, app := range apps {
			copies = append(copies, CaptureCopy{
				Src:   path.Join("apps", app),
				Dest:  path.Join(blueprintsSubdir, app),
				IsDir: true,
			})
		}
	}

	tomlBytes, err := RenderScaffoldTOML(def)
	if err != nil {
		return CapturePlan{}, err
	}

	return CapturePlan{
		Name:             name,
		Def:              def,
		TOML:             tomlBytes,
		ScaffoldTOMLDest: path.Join(scaffoldDir, scaffoldFileName),
		Copies:           copies,
		Params:           captureParams(rs),
	}, nil
}

// hasContent reports whether a RepoStructure holds anything worth capturing —
// at least one root file, one app, or a packages/ directory. An exemplar with
// none is ErrNothingToCapture.
func (rs RepoStructure) hasContent() bool {
	return len(rs.RootFiles) > 0 || len(rs.Apps) > 0 || rs.HasPackages
}

// captureVars builds the [vars] table for a capture: PROJECT_NAME is always
// offered (the generated project needs its own identity), MODULE_PATH only when
// the exemplar is a Go module. The defaults seed the exemplar's own values so a
// re-run of /new-project without overrides reproduces the exemplar.
func captureVars(rs RepoStructure) map[string]VarSpec {
	vars := map[string]VarSpec{
		CaptureVarProjectName: {Prompt: "Project name", Default: ""},
	}
	if rs.ModulePath != "" {
		vars[CaptureVarModulePath] = VarSpec{Prompt: "Go module path", Default: "github.com/"}
	}
	return vars
}

// captureParams builds the literal->placeholder substitution map the service
// applies to every copied file: the exemplar's project name and module path
// become their "{{VAR}}" placeholders. An empty literal is skipped so an
// undetectable identity never rewrites the empty string across every file.
func captureParams(rs RepoStructure) map[string]string {
	params := map[string]string{}
	if rs.ModulePath != "" {
		params[rs.ModulePath] = "{{" + CaptureVarModulePath + "}}"
	}
	if rs.ProjectName != "" {
		params[rs.ProjectName] = "{{" + CaptureVarProjectName + "}}"
	}
	return params
}

// RenderScaffoldTOML marshals a draft ScaffoldDef to scaffold.toml bytes
// (SPEC-100 §7c), prefixed with a banner marking it a capture draft, and
// re-parses the result through ParseScaffold so a returned byte slice is
// guaranteed valid (AC12) — a capture never emits a scaffold.toml its own
// engine would reject. The omitempty tags on the optional fields keep a single
// scaffold's output free of the monorepo-only keys its validation forbids.
func RenderScaffoldTOML(def ScaffoldDef) ([]byte, error) {
	body, err := toml.Marshal(def)
	if err != nil {
		return nil, fmt.Errorf("profile: render scaffold toml: %w", err)
	}

	const banner = "# scaffold.toml — draft captured by `mneme scaffold capture`.\n" +
		"# Curate before use: prune legacy content, refine [vars], and (for a\n" +
		"# turborepo shell) consider replacing shell/ with a pinned bootstrap.\n\n"
	out := append([]byte(banner), body...)

	if _, err := ParseScaffold(out); err != nil {
		return nil, fmt.Errorf("profile: render scaffold toml: draft failed validation: %w", err)
	}
	return out, nil
}

// sortedUnique returns the input's distinct entries in lexical order, so a
// plan's copy list is deterministic regardless of directory-read order.
func sortedUnique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ParametrizeContent rewrites every literal in params with its placeholder in
// data (SPEC-100 §7c, the reverse of substituteVars): the exemplar's concrete
// identity values become "{{VAR}}" placeholders. Literals are applied
// longest-first so a module path that contains the project name is rewritten
// before the bare name, never leaving a half-substituted string. Exported so
// the service's copy pass and the leaf's tests share one substitution
// semantics.
func ParametrizeContent(data []byte, params map[string]string) []byte {
	if len(params) == 0 {
		return data
	}
	literals := make([]string, 0, len(params))
	for lit := range params {
		if lit != "" {
			literals = append(literals, lit)
		}
	}
	sort.Slice(literals, func(i, j int) bool {
		if len(literals[i]) != len(literals[j]) {
			return len(literals[i]) > len(literals[j])
		}
		return literals[i] < literals[j]
	})
	s := string(data)
	for _, lit := range literals {
		s = strings.ReplaceAll(s, lit, params[lit])
	}
	return []byte(s)
}
