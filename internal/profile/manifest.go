package profile

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ManifestFileName is the filename of a profile's manifest. It always lives
// at the root of the profile's own git repository (never the project's).
const ManifestFileName = "mneme-profile.toml"

// ErrInvalidManifest is the sentinel returned by Manifest.Validate when a
// required field is missing or a field fails its format check. Wrapped with
// %w so callers can use errors.Is regardless of the specific message.
var ErrInvalidManifest = errors.New("profile: invalid manifest")

// Manifest is the identity of a profile: name, version, description, and an
// optional mneme compatibility constraint. Parsed from mneme-profile.toml.
type Manifest struct {
	// Name is the profile's canonical identifier. Required; must be a
	// safe-slug (^[a-z0-9][a-z0-9-]*$) because it becomes a path component
	// under the host-level store (<profilesDir>/<name>).
	Name string `toml:"name"`

	// Version is the profile's version string. Required, but not strictly
	// validated as semver — the same lax posture as model aliases: advise,
	// never block on format.
	Version string `toml:"version"`

	// Description is an optional human-readable summary.
	Description string `toml:"description"`

	// Compat carries an optional mneme version constraint.
	Compat ManifestCompat `toml:"compat"`

	// Extends is RESERVED for future profile inheritance (decision #5,
	// YAGNI in v1). It is parsed and preserved for forward-compat, but never
	// acted upon — Manifest.Warnings reports it as an advisory, never as a
	// validation error, so an older mneme binary reading a manifest authored
	// against a newer schema never breaks.
	Extends string `toml:"extends"`
}

// ManifestCompat holds the optional mneme-version compatibility constraint
// of a profile manifest.
type ManifestCompat struct {
	// Mneme is a version constraint string such as ">=1.28.0". Empty means
	// "no constraint" — Manifest.CheckCompat always reports ok=true in that case.
	Mneme string `toml:"mneme"`
}

// ParseManifest parses raw TOML bytes into a Manifest. It does not validate
// the result — call Validate() separately, so a caller that only wants to
// inspect fields (e.g. for a warning) is not forced through validation first.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("profile: parse manifest: %w", err)
	}
	return &m, nil
}

// RenderManifest serializes m to TOML bytes — the write-path counterpart of
// ParseManifest (symmetric to Pin's ParsePin/WritePin pairing, §3), used by
// Scaffold (§5) to produce a brand-new profile's mneme-profile.toml. m is
// validated first: an invalid manifest (missing name/version, or a name
// failing the safe-slug check) is rejected with ErrInvalidManifest before any
// bytes are produced, so a caller can never render a manifest that would
// fail its own round-trip through ParseManifest+Validate.
func RenderManifest(m Manifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("profile: render manifest: %w", err)
	}
	data, err := toml.Marshal(&m)
	if err != nil {
		return nil, fmt.Errorf("profile: render manifest: marshal: %w", err)
	}
	return data, nil
}

// ParseManifestFile reads path and parses it as a Manifest.
func ParseManifestFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("profile: parse manifest file: read %s: %w", path, err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("profile: parse manifest file %s: %w", path, err)
	}
	return m, nil
}

// Validate checks that m carries every field required for a usable profile
// identity. Returns an error wrapping ErrInvalidManifest when Name or Version
// is missing, or when Name is not a safe-slug. Extends being non-empty is
// NOT a validation error (see Manifest.Warnings) — forward-compat is
// preserved by design.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("profile: manifest: name is required: %w", ErrInvalidManifest)
	}
	if !isSafeSlug(m.Name) {
		return fmt.Errorf("profile: manifest: name %q must match %s: %w", m.Name, safeSlugPattern.String(), ErrInvalidManifest)
	}
	if m.Version == "" {
		return fmt.Errorf("profile: manifest: version is required: %w", ErrInvalidManifest)
	}
	return nil
}

// Warnings returns non-blocking advisories about m: forward-compat fields
// that are parsed and preserved but not acted on in §1. Callers (CLI/service)
// are expected to surface these to the user without treating them as errors.
func (m *Manifest) Warnings() []string {
	var warnings []string
	if m.Extends != "" {
		warnings = append(warnings, fmt.Sprintf(
			"extends %q: herencia de profiles no implementada en v1; el campo se conserva pero se ignora", m.Extends))
	}
	return warnings
}

// CheckCompat reports whether mnemeVersion satisfies m.Compat.Mneme's version
// constraint, plus a human-readable message when it does not. ok is always
// true when no constraint is set (Compat.Mneme == ""). The comparison is
// intentionally minimal — a single >=/>/<=/</= operator against a semver
// MAJOR.MINOR.PATCH triple, ignoring any pre-release/build suffix — because
// in §1 compat is purely informative (R5): callers WARN on a mismatch, they
// never block. A full range-constraint engine is a follow-up, not a blocker.
func (m *Manifest) CheckCompat(mnemeVersion string) (ok bool, msg string) {
	constraint := strings.TrimSpace(m.Compat.Mneme)
	if constraint == "" {
		return true, ""
	}

	op, version := splitConstraintOperator(constraint)
	cmp, err := compareSemver(mnemeVersion, version)
	if err != nil {
		// Malformed constraint or mneme version — informative only, never
		// blocking (R5): report as satisfied so a bad string in either side
		// can never brick `profile add`/`list`.
		return true, fmt.Sprintf("no se pudo evaluar el constraint de compatibilidad %q: %s", constraint, err)
	}

	if evalOperator(op, cmp) {
		return true, ""
	}
	return false, fmt.Sprintf("mneme %s no satisface el constraint %q declarado por el profile", mnemeVersion, constraint)
}

// splitConstraintOperator splits a constraint string such as ">=1.28.0" into
// its comparison operator and the bare version. A constraint with no
// recognised operator prefix is treated as an exact-match ("=").
func splitConstraintOperator(constraint string) (op, version string) {
	for _, candidate := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(constraint, candidate) {
			return candidate, strings.TrimSpace(strings.TrimPrefix(constraint, candidate))
		}
	}
	return "=", constraint
}

// evalOperator applies op to the result of compareSemver (cmp: -1/0/1 for
// a<b/a==b/a>b, where a is the left-hand "actual" version).
func evalOperator(op string, cmp int) bool {
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case "<":
		return cmp < 0
	case "=":
		return cmp == 0
	default:
		return true
	}
}

// compareSemver compares two MAJOR.MINOR.PATCH-shaped version strings,
// ignoring any pre-release/build metadata suffix (after '-' or '+') and an
// optional leading "v". Returns -1/0/1 for a<b, a==b, a>b.
func compareSemver(a, b string) (int, error) {
	pa, err := parseSemverTriple(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemverTriple(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

// parseSemverTriple parses v into a [MAJOR, MINOR, PATCH] int triple. Missing
// trailing segments default to 0 (e.g. "1.28" -> [1, 28, 0]).
func parseSemverTriple(v string) ([3]int, error) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	if v == "" {
		return out, fmt.Errorf("profile: parse semver: empty version")
	}
	parts := strings.SplitN(v, ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, fmt.Errorf("profile: parse semver %q: %w", v, err)
		}
		out[i] = n
	}
	return out, nil
}
