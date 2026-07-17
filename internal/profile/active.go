package profile

import (
	"fmt"
	"path/filepath"
)

// ActiveSource explains WHY a profile is (or is not) active for a project —
// which precedence tier decided the outcome (SPEC-093 §3.5, design
// decision #5): a project's own pin always wins over the host-level
// default; the two are never merged.
type ActiveSource int

const (
	// SourceVanilla means no pin and no host default applied — mneme's
	// out-of-box behaviour, unchanged.
	SourceVanilla ActiveSource = iota

	// SourcePin means the project's own .mneme-profile pin decided the
	// outcome — it wins 100% over any host default, even one that is set.
	SourcePin

	// SourceGlobalDefault means the project has no pin at all, so the
	// host-level [profiles].default applied instead.
	SourceGlobalDefault
)

// String renders an ActiveSource as a lowercase label, mirroring PinState's
// own String method.
func (s ActiveSource) String() string {
	switch s {
	case SourceVanilla:
		return "vanilla"
	case SourcePin:
		return "pin"
	case SourceGlobalDefault:
		return "global-default"
	default:
		return "unknown"
	}
}

// ActiveResolution is the composed precedence result of Store.ResolveActive:
// Source names which tier decided the outcome; Resolution carries the same
// shape §1's ResolvePin already returns (State/Pin/Manifest/Path), so any
// existing consumer of a Resolution (rendering, materialization) works
// unchanged regardless of which tier produced it.
type ActiveResolution struct {
	// Source names which precedence tier decided the outcome.
	Source ActiveSource

	// Resolution is the resulting pin-state (State/Pin/Manifest/Path) —
	// identical in shape to what ResolvePin returns.
	Resolution Resolution
}

// ResolveActive composes a project's own pin (ResolvePin, §1) with the
// host-level default (SPEC-093 §3.5): a pin — in ANY of its three
// non-absent states (PinDefault/PinInstalled/PinMissing) — wins outright and
// globalDefault is never consulted; only when the project has NO pin at all
// does globalDefault apply. This is always a pure replacement, never a merge
// (decision #5): a project resolves to its own pin, OR the host default, OR
// vanilla — never a mix of the two.
//
// globalDefault is injected by the caller (ProfileService.ResolveActive,
// which reads it from config exactly once) — this leaf never resolves
// config itself, keeping internal/profile's stdlib-only import posture
// intact (SPEC-056 D5 import-guard).
func (s *Store) ResolveActive(projectRoot, globalDefault string) (ActiveResolution, error) {
	r, err := s.ResolvePin(projectRoot)
	if err != nil {
		return ActiveResolution{}, fmt.Errorf("profile: resolve active: %w", err)
	}
	if r.State != PinAbsent {
		return ActiveResolution{Source: SourcePin, Resolution: r}, nil
	}

	if globalDefault == "" {
		return ActiveResolution{Source: SourceVanilla, Resolution: Resolution{State: PinAbsent}}, nil
	}

	dir, err := s.profilePath(globalDefault)
	if err != nil {
		return ActiveResolution{}, fmt.Errorf("profile: resolve active: %w", err)
	}

	manifest, mErr := ParseManifestFile(filepath.Join(dir, ManifestFileName))
	if mErr != nil {
		return ActiveResolution{
			Source:     SourceGlobalDefault,
			Resolution: Resolution{State: PinMissing, Pin: &Pin{Name: globalDefault}},
		}, nil
	}
	if vErr := manifest.Validate(); vErr != nil {
		return ActiveResolution{
			Source:     SourceGlobalDefault,
			Resolution: Resolution{State: PinMissing, Pin: &Pin{Name: globalDefault}},
		}, nil
	}

	return ActiveResolution{
		Source: SourceGlobalDefault,
		Resolution: Resolution{
			State:    PinInstalled,
			Pin:      &Pin{Name: globalDefault},
			Manifest: manifest,
			Path:     dir,
		},
	}, nil
}
