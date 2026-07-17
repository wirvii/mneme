package profile

import (
	"fmt"
	"os"
	"path/filepath"
)

// PinState enumerates the four possible outcomes of resolving a project's
// pin against the host-level store. §1 only reads and reports this state —
// converting PinMissing into an actionable nudge/gate is §3's job.
type PinState int

const (
	// PinAbsent means the project has no .mneme-profile at all.
	PinAbsent PinState = iota

	// PinDefault means a pin is present but carries no Source — the project
	// resolves to mneme's internal default profile.
	PinDefault

	// PinInstalled means the pin has a Source and the corresponding profile
	// is present (and valid) in the host-level store.
	PinInstalled

	// PinMissing means the pin has a Source but the corresponding profile is
	// NOT present in the host-level store — `profile add` is needed.
	PinMissing
)

// String renders a PinState as a lowercase label suitable for logs and CLI
// output.
func (s PinState) String() string {
	switch s {
	case PinAbsent:
		return "absent"
	case PinDefault:
		return "default"
	case PinInstalled:
		return "installed"
	case PinMissing:
		return "missing"
	default:
		return "unknown"
	}
}

// Resolution is the result of resolving a project's pin against the store.
type Resolution struct {
	// State is one of the four PinState values.
	State PinState

	// Pin is the parsed pin, or nil when State is PinAbsent.
	Pin *Pin

	// Manifest is the installed profile's manifest, populated only when
	// State is PinInstalled.
	Manifest *Manifest

	// Path is the installed profile's absolute path, populated only when
	// State is PinInstalled.
	Path string
}

// ResolvePin reads the pin at projectRoot and cross-references it against
// the store, without writing anything to either. This is the read-only core
// of §1 — the pin-writing verbs ("use"/"default") and any precedence logic
// belong to §3.
func (s *Store) ResolvePin(projectRoot string) (Resolution, error) {
	pinPath := filepath.Join(projectRoot, PinFileName)

	data, err := os.ReadFile(pinPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Resolution{State: PinAbsent}, nil
		}
		return Resolution{}, fmt.Errorf("profile: resolve pin: read %s: %w", pinPath, err)
	}

	pin, err := ParsePin(data)
	if err != nil {
		return Resolution{}, fmt.Errorf("profile: resolve pin: parse %s: %w", pinPath, err)
	}
	if err := pin.Validate(); err != nil {
		return Resolution{}, fmt.Errorf("profile: resolve pin: validate %s: %w", pinPath, err)
	}

	if pin.IsDefault() {
		return Resolution{State: PinDefault, Pin: pin}, nil
	}

	dir, err := s.profilePath(pin.Name)
	if err != nil {
		return Resolution{}, fmt.Errorf("profile: resolve pin: %w", err)
	}

	manifest, err := ParseManifestFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		return Resolution{State: PinMissing, Pin: pin}, nil
	}
	if err := manifest.Validate(); err != nil {
		return Resolution{State: PinMissing, Pin: pin}, nil
	}

	return Resolution{State: PinInstalled, Pin: pin, Manifest: manifest, Path: dir}, nil
}
