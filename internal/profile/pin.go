package profile

import (
	"errors"
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// PinFileName is the name of the pin file committed at the root of a
// project's repository. It has no extension (like .nvmrc) but its content is
// TOML (unlike .nvmrc's plain text) for richer, self-describing fields.
const PinFileName = ".mneme-profile"

// ErrInvalidPin is the sentinel returned by Pin.Validate when a required
// field is missing or Name fails its safe-slug check.
var ErrInvalidPin = errors.New("profile: invalid pin")

// Pin is a committed, self-describing pointer to the profile a project uses.
// Analogous to .nvmrc/package.json's "engines" field. Parsed from
// PinFileName at the root of a project's repository.
type Pin struct {
	// Name is the profile's identifier. Required; must be a safe-slug
	// (^[a-z0-9][a-z0-9-]*$) — it feeds a path lookup in the host-level
	// store, so a value with "/", "..", or similar must never validate.
	Name string `toml:"name"`

	// Source is the git remote the profile was (or should be) cloned from.
	// Empty means "no source" — IsDefault reports true, meaning this project
	// uses mneme's internal default profile rather than an external one.
	Source string `toml:"source"`

	// Ref is the tag/branch/commit pinned for reproducibility. Optional;
	// when Source is set but Ref is empty, Warnings reports that HEAD of the
	// default branch will be used (not reproducible).
	Ref string `toml:"ref"`

	// Scaffold records which /new-project scaffold (if any) generated this
	// pin. Parsed and preserved but not acted upon in §1 — scaffolding is §7.
	Scaffold string `toml:"scaffold"`
}

// IsDefault reports whether p has no Source — i.e. the project resolves to
// mneme's internal default profile rather than an externally sourced one.
func (p *Pin) IsDefault() bool {
	return p.Source == ""
}

// ParsePin parses raw TOML bytes into a Pin. It does not validate the
// result — call Validate() separately.
func ParsePin(data []byte) (*Pin, error) {
	var p Pin
	if err := toml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("profile: parse pin: %w", err)
	}
	return &p, nil
}

// ParsePinFile reads path and parses it as a Pin.
func ParsePinFile(path string) (*Pin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("profile: parse pin file: read %s: %w", path, err)
	}
	p, err := ParsePin(data)
	if err != nil {
		return nil, fmt.Errorf("profile: parse pin file %s: %w", path, err)
	}
	return p, nil
}

// Validate checks that p carries a usable Name. Returns an error wrapping
// ErrInvalidPin when Name is empty or is not a safe-slug — the latter is the
// primary anti-path-traversal defense (names like "../evil" or "a/b" can
// never validate, since safeSlugPattern disallows "/" and ".").
func (p *Pin) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("profile: pin: name is required: %w", ErrInvalidPin)
	}
	if !isSafeSlug(p.Name) {
		return fmt.Errorf("profile: pin: name %q must match %s: %w", p.Name, safeSlugPattern.String(), ErrInvalidPin)
	}
	return nil
}

// Warnings returns non-blocking advisories about p. Currently: a Source
// without a Ref means the pin is not reproducible (it will always resolve to
// whatever HEAD of the default branch happens to be at update time).
func (p *Pin) Warnings() []string {
	var warnings []string
	if p.Source != "" && p.Ref == "" {
		warnings = append(warnings, "ref no pinneado; se usará HEAD del branch por defecto — no reproducible")
	}
	return warnings
}
