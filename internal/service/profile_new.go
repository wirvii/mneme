package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wirvii/mneme/internal/profile"
)

// NewProfileInput configures ProfileService.NewProfile (SPEC-095 §5): the
// scaffolder half of a profile's creation, paired with the
// mneme-profile-author skill that curates content afterwards.
type NewProfileInput struct {
	// Name is the new profile's canonical identifier — safe-slug required
	// (profile.Scaffold rejects anything else via Manifest.Validate). Becomes
	// the rendered manifest's Name and, when Dir is empty, the destination
	// subdirectory name.
	Name string

	// Dir is the destination directory for the new profile repo. Empty means
	// "<cwd>/<Name>".
	Dir string
}

// NewProfileResult reports the outcome of ProfileService.NewProfile.
type NewProfileResult struct {
	// Name is the scaffolded profile's identifier.
	Name string `json:"name"`

	// Path is the absolute destination directory the profile was scaffolded
	// into.
	Path string `json:"path"`

	// ManifestPath is the absolute path of the written mneme-profile.toml.
	ManifestPath string `json:"manifest_path"`
}

// NewProfile scaffolds a brand-new profile REPOSITORY — the source a profile
// author curates, commits, and pushes, BEFORE any consumer ever installs it
// via Add ("profile add"). Unlike Add/Update/List/ResolvePin, NewProfile
// never touches s.store's profilesDir at all: it wraps the leaf's free
// function profile.Scaffold, which operates entirely on the caller-chosen
// destination (see Scaffold's own doc comment for why that frontier is
// deliberate — "authoring a profile" vs "installing one").
func (s *ProfileService) NewProfile(in NewProfileInput) (*NewProfileResult, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("service: profile: new: name is required")
	}

	dest := in.Dir
	if dest == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("service: profile: new: %w", err)
		}
		dest = filepath.Join(cwd, in.Name)
	}

	if err := profile.Scaffold(dest, profile.ScaffoldInput{Name: in.Name}); err != nil {
		return nil, translateProfileError("service: profile: new", err)
	}

	return &NewProfileResult{
		Name:         in.Name,
		Path:         dest,
		ManifestPath: filepath.Join(dest, profile.ManifestFileName),
	}, nil
}
