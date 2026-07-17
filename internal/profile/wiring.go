package profile

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnknownWiringAction is the closed-vocabulary invariant of a custom
// toolchain's [wiring].on_add list (docs/profiles-design.md §15.4, SPEC-099
// §7b R-wiring-DSL): on_add is a fixed, non-Turing-complete vocabulary —
// "workspace:", "json-merge:", "copy:" — never a DSL a scaffold author could
// grow into arbitrary code execution. Any other verb fails ParseScaffold at
// parse time (fail-fast), never at wiring runtime.
var ErrUnknownWiringAction = errors.New("profile: unknown wiring action")

// ErrAppAddNotApplicable is returned by PlanAddApp for a scaffold whose layout
// has no notion of composable apps — today only LayoutSingle. It is distinct
// from ErrLayoutUnsupported (a valid-but-unimplemented layout): a single
// scaffold is fully supported, adding an app to it simply "does not apply".
var ErrAppAddNotApplicable = errors.New("profile: app add does not apply to this layout")

// WiringActionKind is the closed vocabulary of on_add verbs a custom toolchain
// may declare. Every ScaffoldDef.Wiring.OnAdd entry parses to exactly one of
// these; anything else is ErrUnknownWiringAction.
type WiringActionKind string

// The three — and only three — wiring verbs (docs/profiles-design.md §15.4).
const (
	// WiringWorkspace ("workspace:<file>") appends apps_dir/<app> to the
	// packages: list of a pnpm-style workspace yaml file.
	WiringWorkspace WiringActionKind = "workspace"

	// WiringJSONMerge ("json-merge:<file>#<path>") inserts a member into a JSON
	// node addressed by <path> (e.g. turbo.json#pipeline).
	WiringJSONMerge WiringActionKind = "json-merge"

	// WiringCopy ("copy:<template>") copies a template fragment from the
	// scaffold's directory into the freshly added app.
	WiringCopy WiringActionKind = "copy"
)

// wiringAction is one parsed on_add entry: its verb plus the raw argument after
// the first ":" (a file, a "file#path", or a template path depending on Kind).
type wiringAction struct {
	Kind WiringActionKind
	Arg  string
}

// parseWiringAction splits a raw "verb:arg" on_add entry and validates the verb
// against the closed vocabulary. An unknown or empty verb is
// ErrUnknownWiringAction; a known verb with an empty argument is a plain
// (wrapped) error, since every verb needs a target.
func parseWiringAction(raw string) (wiringAction, error) {
	idx := strings.IndexByte(raw, ':')
	if idx <= 0 {
		return wiringAction{}, fmt.Errorf("profile: wiring action %q must be \"verb:arg\": %w", raw, ErrUnknownWiringAction)
	}
	verb, arg := WiringActionKind(raw[:idx]), raw[idx+1:]
	switch verb {
	case WiringWorkspace, WiringJSONMerge, WiringCopy:
	default:
		return wiringAction{}, fmt.Errorf("profile: wiring action %q: verb %q is not one of workspace/json-merge/copy: %w", raw, verb, ErrUnknownWiringAction)
	}
	if arg == "" {
		return wiringAction{}, fmt.Errorf("profile: wiring action %q: empty argument: %w", raw, ErrUnknownWiringAction)
	}
	return wiringAction{Kind: verb, Arg: arg}, nil
}

// WiringEditKind mirrors WiringActionKind at the plan level: the discriminator
// on a WiringEdit telling the service which file mutation to perform.
type WiringEditKind string

// The WiringEdit discriminators, one per wiring verb.
const (
	WiringEditWorkspace WiringEditKind = "workspace"
	WiringEditJSONMerge WiringEditKind = "json-merge"
	WiringEditCopy      WiringEditKind = "copy"
)

// WiringEdit is one pure, deterministic file mutation the assembly engine must
// perform to wire a freshly added app into a monorepo's root files
// (docs/profiles-design.md §15.4). The leaf computes these; the service reads,
// mutates, and writes the real files — the leaf never touches disk.
type WiringEdit struct {
	// Kind selects the mutation.
	Kind WiringEditKind

	// File is the monorepo-root-relative target file (workspace/json-merge:
	// e.g. "pnpm-workspace.yaml", "turbo.json"). Unused for copy.
	File string

	// Entry is the value the mutation ensures present: the packages glob/path
	// for workspace ("apps/<app>"), or the member key for json-merge.
	Entry string

	// JSONPath is the json-merge node the Entry is inserted under (e.g.
	// "pipeline"); empty means the document root.
	JSONPath string

	// Src is the FS-relative template source within the profile's contents
	// (copy only).
	Src string

	// Dest is the absolute destination (copy only).
	Dest string

	// Vars is the substitution map applied to a copy action's file contents.
	Vars map[string]string
}

// Wirer resolves the "where does an app go and which root files are touched"
// question at the heart of /new-app (docs/profiles-design.md §15.4). It is a
// Strategy: turborepoWirer bakes in Turborepo's convention, customWirer
// interprets a scaffold's declared [wiring]. Both return pure WiringEdits; the
// service executes them.
type Wirer interface {
	// AppsDir is the slash-form directory (no trailing slash) composable apps
	// live under.
	AppsDir() string

	// PlanWire computes the root-file edits needed to register appName (grown
	// from blueprint) into the monorepo rooted at monorepoRoot.
	PlanWire(monorepoRoot, appName, blueprint string, vars map[string]string) ([]WiringEdit, error)
}

// turborepoWirer is the built-in adapter for toolchain="turborepo": it knows
// Turborepo's convention (apps under apps/, a packages glob in
// pnpm-workspace.yaml, task pipeline driven by globs in turbo.json), so it
// emits exactly one workspace edit and never touches turbo.json — the service
// makes that edit a no-op when an existing glob already covers apps/*.
type turborepoWirer struct{}

// AppsDir is Turborepo's fixed convention.
func (turborepoWirer) AppsDir() string { return "apps" }

// PlanWire emits the single workspace edit Turborepo needs (add apps/<app> to
// pnpm-workspace.yaml) and nothing else — turbo.json is glob-driven, so a new
// app under apps/ requires no pipeline edit.
func (turborepoWirer) PlanWire(_, appName, _ string, _ map[string]string) ([]WiringEdit, error) {
	return []WiringEdit{{
		Kind:  WiringEditWorkspace,
		File:  "pnpm-workspace.yaml",
		Entry: path.Join("apps", appName),
	}}, nil
}

// customWirer interprets a scaffold's declared [wiring] block (toolchain=
// "custom"): the author spelled out the on_add actions Turborepo would perform
// built-in, and this wirer turns each into a WiringEdit. Its vocabulary is the
// closed set validated by ParseScaffold, so PlanWire never meets an unknown
// verb.
type customWirer struct {
	scaffoldName string
	appsDir      string
	onAdd        []string
}

// AppsDir is the custom-declared apps directory (normalised).
func (w customWirer) AppsDir() string { return w.appsDir }

// PlanWire turns each on_add action into a WiringEdit. workspace/json-merge
// target root files; copy drops a scaffold template fragment into the new app
// directory. The actions were validated at parse time, so a re-parse here can
// only fail on a genuinely corrupt entry (defensive).
func (w customWirer) PlanWire(monorepoRoot, appName, _ string, vars map[string]string) ([]WiringEdit, error) {
	appDest := joinDest(monorepoRoot, w.appsDir, appName)
	edits := make([]WiringEdit, 0, len(w.onAdd))
	for _, raw := range w.onAdd {
		act, err := parseWiringAction(raw)
		if err != nil {
			return nil, err
		}
		switch act.Kind {
		case WiringWorkspace:
			edits = append(edits, WiringEdit{
				Kind:  WiringEditWorkspace,
				File:  act.Arg,
				Entry: path.Join(w.appsDir, appName),
			})
		case WiringJSONMerge:
			file, jsonPath := splitJSONMergeArg(act.Arg)
			edits = append(edits, WiringEdit{
				Kind:     WiringEditJSONMerge,
				File:     file,
				Entry:    appName,
				JSONPath: jsonPath,
			})
		case WiringCopy:
			edits = append(edits, WiringEdit{
				Kind: WiringEditCopy,
				Src:  path.Join(scaffoldsSubdir, w.scaffoldName, act.Arg),
				Dest: appDest,
				Vars: vars,
			})
		}
	}
	return edits, nil
}

// WirerFor selects the wiring Strategy for a scaffold: turborepoWirer for the
// built-in toolchain, customWirer (seeded from the [wiring] block) for the
// declarative one. A single layout has no composable-app notion and yields
// ErrAppAddNotApplicable; a monorepo missing its required axis is a
// (defensive) ErrInvalidScaffold — ParseScaffold already rejects that shape.
func WirerFor(s ScaffoldDef) (Wirer, error) {
	if s.Layout == LayoutSingle {
		return nil, fmt.Errorf("profile: wirer: scaffold %q is single-layout: %w", s.Name, ErrAppAddNotApplicable)
	}
	switch s.Toolchain {
	case ToolchainTurborepo:
		return turborepoWirer{}, nil
	case ToolchainCustom:
		appsDir := "apps"
		var onAdd []string
		if s.Wiring != nil {
			appsDir = normalizeAppsDir(s.Wiring.AppsDir)
			onAdd = s.Wiring.OnAdd
		}
		return customWirer{scaffoldName: s.Name, appsDir: appsDir, onAdd: onAdd}, nil
	default:
		return nil, fmt.Errorf("profile: wirer: scaffold %q monorepo declares no toolchain: %w", s.Name, ErrInvalidScaffold)
	}
}

// joinDest joins a monorepo root (a real, absolute OS path) with a slash-form
// apps directory and app name into an absolute destination path, translating
// slashes to the host separator.
func joinDest(monorepoRoot, appsDir, appName string) string {
	return filepath.Join(monorepoRoot, filepath.FromSlash(appsDir), appName)
}

// splitJSONMergeArg splits a json-merge argument "<file>#<path>" into its file
// and node path. A missing "#" yields an empty path (the document root).
func splitJSONMergeArg(arg string) (file, jsonPath string) {
	if i := strings.IndexByte(arg, '#'); i >= 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// normalizeAppsDir defaults an empty apps directory to "apps" and strips any
// trailing slash so callers can path.Join without doubling separators.
func normalizeAppsDir(raw string) string {
	d := strings.TrimSuffix(strings.TrimSpace(raw), "/")
	if d == "" {
		return "apps"
	}
	return d
}
