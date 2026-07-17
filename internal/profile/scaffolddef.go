package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Scaffold-catalog layout constants (SPEC-098 §7a). A profile's project
// scaffolds live under scaffoldsSubdir; each named scaffold owns a
// scaffoldFileName plus, for a "single" layout, a flat skeletonSubdir tree
// copied verbatim (with variable substitution) into a brand-new repository.
// _blueprints/ (composable apps shared across scaffolds) is a "monorepo"
// concern deferred to §7b — ListScaffolds skips it, and every underscore-
// prefixed sibling, so it is never mistaken for a scaffold.
const (
	scaffoldsSubdir  = "scaffolds"
	scaffoldFileName = "scaffold.toml"
	skeletonSubdir   = "skeleton"
	overlaySubdir    = "overlay"
	shellSubdir      = "shell"
	blueprintsSubdir = "_blueprints"
)

// Layout is the assembly mode a scaffold declares (docs/profiles-design.md
// §15.3): "single" (a flat copy+substitute skeleton — the only layout §7a
// assembles) or "monorepo" (a composable shell + overlay + blueprints, whose
// engine arrives in §7b). single-app/single-module/library all collapse into
// "single" — same engine, different content.
type Layout string

// The two valid Layout values. Any other value fails ParseScaffold with
// ErrInvalidLayout.
const (
	LayoutSingle   Layout = "single"
	LayoutMonorepo Layout = "monorepo"
)

// Toolchain is the second axis (monorepo only, docs/profiles-design.md
// §15.4): "turborepo" (mneme's built-in wiring adapter) or "custom" (wiring
// declared by the author in [wiring]). Both the toolchain adapter and the
// wiring engine are §7b — §7a only validates the enum so a scaffold.toml that
// declares one parses cleanly.
type Toolchain string

// The two valid Toolchain values. Any other non-empty value fails
// ParseScaffold with ErrInvalidToolchain.
const (
	ToolchainTurborepo Toolchain = "turborepo"
	ToolchainCustom    Toolchain = "custom"
)

// Scaffold-schema sentinels (SPEC-098 §7a AC1/AC14). ProfileService
// translates these to their model-level equivalents so cli/mcp compare
// against model.*, never internal/profile directly.
var (
	// ErrInvalidLayout is returned when a scaffold.toml declares no layout or
	// one outside {single, monorepo}.
	ErrInvalidLayout = errors.New("profile: invalid scaffold layout")

	// ErrInvalidToolchain is returned when a scaffold.toml declares a
	// toolchain outside {turborepo, custom}.
	ErrInvalidToolchain = errors.New("profile: invalid scaffold toolchain")

	// ErrBootstrapNotPinned is the determinism invariant (decision #21, lesson
	// SPEC-088): a scaffold's bootstrap generator must carry an exact
	// "@<version>" (semver or sha) — never "@latest", never a bare name, never
	// a range. Two `mneme project new` of the same scaffold must produce the
	// same base tree; a floating generator version silently breaks that.
	ErrBootstrapNotPinned = errors.New("profile: scaffold bootstrap is not version-pinned")

	// ErrInvalidScaffold is returned for a structurally incoherent
	// scaffold.toml — most notably a "single" layout that also declares
	// monorepo-only fields (toolchain, bootstrap, blueprints, or [wiring]),
	// which is a hard error (decision A2, coherence over leniency).
	ErrInvalidScaffold = errors.New("profile: invalid scaffold definition")

	// ErrScaffoldNotFound is returned when a named scaffold does not exist in
	// the active profile's catalog.
	ErrScaffoldNotFound = errors.New("profile: scaffold not found")
)

// VarSpec is one entry of a scaffold's [vars] table: a substitution variable
// plus the prompt and default the /new-project grill uses to elicit it. The
// value the user supplies (or Default when they don't) replaces every
// "{{<key>}}" occurrence in the copied skeleton (ResolveVars + the service's
// substitution pass).
type VarSpec struct {
	// Prompt is the human-facing question the grill asks for this variable.
	Prompt string `toml:"prompt"`

	// Default is the value used when the caller supplies none. Empty is
	// allowed — it simply substitutes an empty string.
	Default string `toml:"default"`
}

// WiringSpec is a scaffold's [wiring] table (custom-toolchain monorepos only,
// docs/profiles-design.md §15.4). §7a parses and records it purely so a
// "single" layout can reject its presence (ErrInvalidScaffold) and so §7b can
// extend the same struct — the on_add vocabulary validation and the wiring
// engine itself are §7b, not §7a.
type WiringSpec struct {
	// AppsDir is where composable apps live (e.g. "services/").
	AppsDir string `toml:"apps_dir"`

	// OnAdd is the closed vocabulary of file-edit actions performed when an
	// app is added. Interpreted by §7b's customWirer, not §7a.
	OnAdd []string `toml:"on_add"`
}

// ScaffoldDef is a parsed scaffolds/<name>/scaffold.toml: the declarative
// description of one project archetype a profile can generate
// (docs/profiles-design.md §15). It is named ScaffoldDef rather than
// "Scaffold" only because internal/profile already exports the free function
// Scaffold (SPEC-095 §5, which scaffolds a profile REPO); Go forbids a type
// and a function sharing one identifier in a package. The design's technical
// content is unchanged — this is the "Scaffold struct" of SPEC-097 §4.1.
//
// ScaffoldDef is inert data: it holds the full schema (both layouts) so §7b
// only has to add engine, never re-parse. §7a assembles only LayoutSingle.
type ScaffoldDef struct {
	// Name is the scaffold's catalog identifier, derived from its directory
	// name by ListScaffolds (a safe-slug) — never read from the TOML body.
	Name string `toml:"-"`

	// Layout is the required assembly mode (single | monorepo).
	Layout Layout `toml:"layout"`

	// Toolchain is the monorepo wiring axis (turborepo | custom); empty for a
	// single layout.
	Toolchain Toolchain `toml:"toolchain"`

	// Bootstrap is the pinned official generator (e.g. "create-turbo@2.3.1")
	// run before the profile overlay — monorepo/turborepo only. Empty means
	// "no bootstrap" (single, or a custom shell captured verbatim).
	Bootstrap string `toml:"bootstrap"`

	// Overlay is the FS-relative subdirectory of team content applied on top
	// of the bootstrap. Optional; defaults to overlaySubdir when a monorepo
	// omits it (a §7b concern — §7a's single layout uses skeleton/, not
	// overlay/).
	Overlay string `toml:"overlay"`

	// Blueprints names the composable apps under _blueprints/ this scaffold
	// offers (monorepo only; §7b).
	Blueprints []string `toml:"blueprints"`

	// Vars is the substitution-variable table, keyed by variable name.
	Vars map[string]VarSpec `toml:"vars"`

	// Wiring is the custom-toolchain wiring declaration (§7b); nil unless the
	// scaffold.toml carries a [wiring] table.
	Wiring *WiringSpec `toml:"wiring"`
}

// ParseScaffold parses raw scaffold.toml bytes into a ScaffoldDef and
// validates every §7a invariant BEFORE the result is usable (SPEC-098 §7a
// AC1): the layout enum (ErrInvalidLayout), the toolchain enum
// (ErrInvalidToolchain), the bootstrap pinning invariant
// (ErrBootstrapNotPinned), and single-layout exclusivity (ErrInvalidScaffold
// when a single scaffold also declares any monorepo-only field). The returned
// ScaffoldDef's Name is left empty — only ListScaffolds, which knows the
// directory a scaffold.toml came from, can set it.
func ParseScaffold(data []byte) (ScaffoldDef, error) {
	var s ScaffoldDef
	if err := toml.Unmarshal(data, &s); err != nil {
		return ScaffoldDef{}, fmt.Errorf("profile: parse scaffold: %w", err)
	}
	if err := s.validate(); err != nil {
		return ScaffoldDef{}, err
	}
	return s, nil
}

// validate enforces the §7a schema invariants on an already-unmarshalled
// ScaffoldDef. Split out from ParseScaffold so ListScaffolds can report the
// offending scaffold name alongside the same errors.
func (s ScaffoldDef) validate() error {
	switch s.Layout {
	case LayoutSingle, LayoutMonorepo:
	default:
		return fmt.Errorf("profile: scaffold: layout %q must be %q or %q: %w",
			s.Layout, LayoutSingle, LayoutMonorepo, ErrInvalidLayout)
	}

	if s.Toolchain != "" {
		switch s.Toolchain {
		case ToolchainTurborepo, ToolchainCustom:
		default:
			return fmt.Errorf("profile: scaffold: toolchain %q must be %q or %q: %w",
				s.Toolchain, ToolchainTurborepo, ToolchainCustom, ErrInvalidToolchain)
		}
	}

	if s.Bootstrap != "" {
		if _, _, ok := splitBootstrap(s.Bootstrap); !ok {
			return fmt.Errorf("profile: scaffold: bootstrap %q must be pinned to an exact @<version> (never @latest): %w",
				s.Bootstrap, ErrBootstrapNotPinned)
		}
	}

	if s.Layout == LayoutSingle {
		switch {
		case s.Toolchain != "":
			return fmt.Errorf("profile: scaffold: single layout must not declare a toolchain: %w", ErrInvalidScaffold)
		case s.Bootstrap != "":
			return fmt.Errorf("profile: scaffold: single layout must not declare a bootstrap: %w", ErrInvalidScaffold)
		case len(s.Blueprints) > 0:
			return fmt.Errorf("profile: scaffold: single layout must not declare blueprints: %w", ErrInvalidScaffold)
		case s.Wiring != nil:
			return fmt.Errorf("profile: scaffold: single layout must not declare a [wiring] table: %w", ErrInvalidScaffold)
		}
	}

	// Fail-fast on an unknown wiring verb at PARSE time, never at wiring
	// runtime (SPEC-099 §7b AC9, the closed-vocabulary invariant): a custom
	// toolchain's on_add list may only use workspace:/json-merge:/copy:.
	if s.Wiring != nil {
		for _, raw := range s.Wiring.OnAdd {
			if _, err := parseWiringAction(raw); err != nil {
				return err
			}
		}
	}

	return nil
}

// BootstrapParts splits a scaffold's Bootstrap ("create-turbo@2.3.1") into its
// generator ("create-turbo") and pinned version ("2.3.1"). ok is false when
// Bootstrap is empty or not exactly pinned — the same condition ParseScaffold
// rejects with ErrBootstrapNotPinned, re-exposed so the assembly engine (§7b)
// can build a BootstrapStep without re-parsing.
func (s ScaffoldDef) BootstrapParts() (generator, version string, ok bool) {
	return splitBootstrap(s.Bootstrap)
}

// splitBootstrap parses "<generator>@<version>" on the LAST "@" so a scoped
// package ("@vercel/create-next-app@1.2.3") splits correctly, and reports ok
// only when both halves are non-empty AND the version is an exact pin (not
// "latest", not a range/operator). A bare name (no "@") or a leading-"@" scope
// with no trailing version yields ok=false.
func splitBootstrap(spec string) (generator, version string, ok bool) {
	at := strings.LastIndex(spec, "@")
	if at <= 0 || at == len(spec)-1 {
		return "", "", false
	}
	generator, version = spec[:at], spec[at+1:]
	if !versionPinned(version) {
		return "", "", false
	}
	return generator, version, true
}

// versionPinned reports whether v is an exact, reproducible pin: a concrete
// semver or commit sha, never "latest", never a range operator (^ ~ * > <)
// npm would resolve non-deterministically at install time.
func versionPinned(v string) bool {
	if v == "" || v == "latest" {
		return false
	}
	if strings.ContainsAny(v, "^~*") {
		return false
	}
	if strings.HasPrefix(v, ">") || strings.HasPrefix(v, "<") || strings.HasPrefix(v, "=") {
		return false
	}
	return true
}

// ResolveVars merges a scaffold's declared [vars] defaults with the values the
// caller supplied: every declared variable is present in the result (its
// provided value, or its Default when absent), and any extra provided key not
// declared in the scaffold is passed through unchanged (a caller may inject a
// value the scaffold's skeleton references without a formal [vars] entry). The
// result is what the service substitutes into copied files, so keeping this
// pure and deterministic makes the whole plan golden-testable.
func (s ScaffoldDef) ResolveVars(provided map[string]string) map[string]string {
	resolved := make(map[string]string, len(s.Vars)+len(provided))
	for name, spec := range s.Vars {
		resolved[name] = spec.Default
	}
	for k, v := range provided {
		resolved[k] = v
	}
	return resolved
}

// ListScaffolds enumerates every scaffolds/<name>/scaffold.toml in a profile's
// filesystem (a disk checkout via os.DirFS, or the embedded OSS default via
// LoadContentsFS's FS), returning each parsed and validated, sorted by Name.
// Underscore-prefixed directories (notably _blueprints/) and directories
// lacking a scaffold.toml are skipped silently; a scaffold.toml that fails to
// parse or validate fails the whole listing with an error naming it, so a
// malformed catalog surfaces loudly rather than half-loading. A profile with
// no scaffolds/ directory at all returns (nil, nil) — the clean "no scaffolds
// in the active profile" degradation the embedded OSS default relies on.
func ListScaffolds(fsys fs.FS) ([]ScaffoldDef, error) {
	entries, err := fs.ReadDir(fsys, scaffoldsSubdir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: list scaffolds: read %s: %w", scaffoldsSubdir, err)
	}

	var scaffolds []ScaffoldDef
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		name := e.Name()
		tomlPath := path.Join(scaffoldsSubdir, name, scaffoldFileName)
		data, readErr := fs.ReadFile(fsys, tomlPath)
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("profile: list scaffolds: read %s: %w", tomlPath, readErr)
		}
		if !isSafeSlug(name) {
			return nil, fmt.Errorf("profile: list scaffolds: %q must match %s: %w", name, safeSlugPattern.String(), ErrInvalidScaffold)
		}

		s, parseErr := ParseScaffold(data)
		if parseErr != nil {
			return nil, fmt.Errorf("profile: list scaffolds: %s: %w", name, parseErr)
		}
		s.Name = name
		scaffolds = append(scaffolds, s)
	}

	sort.Slice(scaffolds, func(i, j int) bool { return scaffolds[i].Name < scaffolds[j].Name })
	return scaffolds, nil
}

// FindScaffold returns the scaffold named name from fsys's catalog, or
// ErrScaffoldNotFound when the catalog has no such entry. A thin lookup over
// ListScaffolds so the service never re-implements the enumeration.
func FindScaffold(fsys fs.FS, name string) (ScaffoldDef, error) {
	scaffolds, err := ListScaffolds(fsys)
	if err != nil {
		return ScaffoldDef{}, err
	}
	for _, s := range scaffolds {
		if s.Name == name {
			return s, nil
		}
	}
	return ScaffoldDef{}, fmt.Errorf("profile: find scaffold %q: %w", name, ErrScaffoldNotFound)
}
