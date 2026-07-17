package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ErrProfileExists is returned by Store.Add when the destination
// <profilesDir>/<name> already exists and force was not requested.
var ErrProfileExists = errors.New("profile: already installed")

// ErrProfileNotFound is returned by Store.Update when <profilesDir>/<name>
// does not exist in the store.
var ErrProfileNotFound = errors.New("profile: not installed")

// ErrProfileNameMismatch is returned by Store.Add when an explicit name was
// requested but the cloned repository's manifest declares a different name.
// Without this check, the store directory (named after the request) could
// silently disagree with the profile's own declared identity (R4).
var ErrProfileNameMismatch = errors.New("profile: requested name does not match manifest name")

// Store operates on a host-level profile store rooted at profilesDir
// (typically ~/.mneme/profiles — see Config.ProfilesDir). It never resolves
// HOME itself; the directory is always injected by the caller.
type Store struct {
	profilesDir string

	// GitEnv holds extra "KEY=VALUE" entries appended to every git
	// subprocess this Store spawns, on top of the current process
	// environment. Empty by default (interactive CLI use: credential
	// prompts are allowed). Set to []string{GitTerminalPromptDisabled} for
	// non-interactive callers (MCP) so a private repository without cached
	// credentials fails fast instead of hanging the process (R1).
	GitEnv []string
}

// NewStore constructs a Store rooted at profilesDir. The directory is
// created lazily — the constructor itself performs no filesystem I/O.
func NewStore(profilesDir string) *Store {
	return &Store{profilesDir: profilesDir}
}

// profilePath returns <profilesDir>/<name> after re-validating name as a
// safe-slug. This is a defense-in-depth check: even though callers normally
// only ever pass a name that already came from a validated Pin or Manifest,
// profilePath never trusts that invariant blindly before it touches the
// filesystem (R2).
func (s *Store) profilePath(name string) (string, error) {
	if !isSafeSlug(name) {
		return "", fmt.Errorf("profile: store: invalid profile name %q: %w", name, ErrInvalidPin)
	}
	return filepath.Join(s.profilesDir, name), nil
}

// AddResult reports the outcome of Store.Add.
type AddResult struct {
	// Name is the profile's identifier (derived from the manifest when the
	// caller did not request one explicitly).
	Name string

	// Version is the profile's version, as declared by its manifest.
	Version string

	// Ref is the effective ref checked out (the requested ref, or the
	// default branch's current HEAD ref-description when none was given).
	Ref string

	// Path is the absolute path the profile was installed to
	// (<profilesDir>/<name>).
	Path string
}

// Add clones source into the store as a new profile named name (or, when
// name is empty, whatever the cloned manifest declares). The clone happens in
// a temporary sibling directory inside profilesDir and is only made visible
// via an atomic os.Rename — an interrupted clone can never leave a partial
// directory at the final destination (R3). The clone is full (not
// --depth=1) so any tag or commit named by ref is reachable.
//
// Returns ErrProfileNameMismatch when name is non-empty and differs from the
// cloned manifest's declared name. Returns ErrProfileExists when the
// destination already exists and force is false — in that case the caller is
// pointed at Store.Update instead of silently clobbering an install.
func (s *Store) Add(source, name, ref string, force bool) (*AddResult, error) {
	if err := os.MkdirAll(s.profilesDir, 0o755); err != nil {
		return nil, fmt.Errorf("profile: store: add: mkdir %s: %w", s.profilesDir, err)
	}

	// The temp clone target lives INSIDE profilesDir (not the OS-wide temp
	// dir) so the final os.Rename is guaranteed to be same-filesystem, and
	// therefore atomic — a cross-device rename can fail midway and defeat
	// the whole point of R3's mitigation.
	tmpDir, err := os.MkdirTemp(s.profilesDir, ".tmp-clone-*")
	if err != nil {
		return nil, fmt.Errorf("profile: store: add: create temp dir: %w", err)
	}
	moved := false
	defer func() {
		if !moved {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if _, err := runGit("", s.GitEnv, "clone", source, tmpDir); err != nil {
		return nil, fmt.Errorf("profile: store: add: clone %s: %w", source, err)
	}

	// Checkout BEFORE reading the manifest: the manifest reported in
	// AddResult (and used for the name-mismatch check below) must reflect
	// the ref actually being installed, not whatever the default branch's
	// tip happened to be at clone time.
	if ref != "" {
		if _, err := runGit(tmpDir, s.GitEnv, "checkout", ref); err != nil {
			return nil, fmt.Errorf("profile: store: add: checkout %s: %w", ref, err)
		}
	}

	manifest, err := ParseManifestFile(filepath.Join(tmpDir, ManifestFileName))
	if err != nil {
		return nil, fmt.Errorf("profile: store: add: read manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("profile: store: add: invalid manifest: %w", err)
	}

	effectiveName := name
	if effectiveName == "" {
		effectiveName = manifest.Name
	} else if effectiveName != manifest.Name {
		return nil, fmt.Errorf("profile: store: add: requested name %q, manifest declares %q: %w",
			effectiveName, manifest.Name, ErrProfileNameMismatch)
	}

	dest, err := s.profilePath(effectiveName)
	if err != nil {
		return nil, fmt.Errorf("profile: store: add: %w", err)
	}

	if _, statErr := os.Stat(dest); statErr == nil {
		if !force {
			return nil, fmt.Errorf("profile: store: add: %q: %w", effectiveName, ErrProfileExists)
		}
		if err := os.RemoveAll(dest); err != nil {
			return nil, fmt.Errorf("profile: store: add: remove existing %s: %w", dest, err)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("profile: store: add: stat %s: %w", dest, statErr)
	}

	if err := os.Rename(tmpDir, dest); err != nil {
		return nil, fmt.Errorf("profile: store: add: rename %s -> %s: %w", tmpDir, dest, err)
	}
	moved = true

	effectiveRef := ref
	if effectiveRef == "" {
		if resolved, err := currentRef(dest); err == nil {
			effectiveRef = resolved
		}
	}

	return &AddResult{
		Name:    effectiveName,
		Version: manifest.Version,
		Ref:     effectiveRef,
		Path:    dest,
	}, nil
}

// UpdateResult reports the outcome of Store.Update.
type UpdateResult struct {
	// Name is the profile's identifier.
	Name string

	// OldRef is the ref the profile was on before updating (best-effort;
	// empty when it could not be determined).
	OldRef string

	// NewRef is the ref the profile is on after updating.
	NewRef string

	// Version is the profile's version after updating, as declared by its
	// (re-read) manifest.
	Version string
}

// Update fetches and checks out ref (or, when ref is empty, pulls the
// current branch fast-forward-only) for the profile named name. Returns
// ErrProfileNotFound when the profile is not present in the store.
func (s *Store) Update(name, ref string) (*UpdateResult, error) {
	dir, err := s.profilePath(name)
	if err != nil {
		return nil, fmt.Errorf("profile: store: update: %w", err)
	}

	if _, statErr := os.Stat(dir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("profile: store: update: %q: %w", name, ErrProfileNotFound)
		}
		return nil, fmt.Errorf("profile: store: update: stat %s: %w", dir, statErr)
	}

	oldRef, _ := currentRef(dir)

	if _, err := runGit(dir, s.GitEnv, "fetch", "--tags", "--prune"); err != nil {
		return nil, fmt.Errorf("profile: store: update: fetch: %w", err)
	}

	if ref != "" {
		if _, err := runGit(dir, s.GitEnv, "checkout", ref); err != nil {
			return nil, fmt.Errorf("profile: store: update: checkout %s: %w", ref, err)
		}
	}

	if onBranch(dir) {
		if _, err := runGit(dir, s.GitEnv, "pull", "--ff-only"); err != nil {
			return nil, fmt.Errorf("profile: store: update: pull: %w", err)
		}
	}

	manifest, err := ParseManifestFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		return nil, fmt.Errorf("profile: store: update: read manifest: %w", err)
	}

	newRef, err := currentRef(dir)
	if err != nil {
		return nil, fmt.Errorf("profile: store: update: %w", err)
	}

	return &UpdateResult{
		Name:    name,
		OldRef:  oldRef,
		NewRef:  newRef,
		Version: manifest.Version,
	}, nil
}

// ProfileInfo describes a single entry in the store, as reported by
// Store.List.
type ProfileInfo struct {
	// Name is the directory name under profilesDir. Equal to Manifest.Name
	// when Valid is true.
	Name string

	// Version is the profile's declared version. Empty when Valid is false.
	Version string

	// Description is the profile's declared description. Empty when Valid
	// is false.
	Description string

	// Ref is the current checked-out ref-description. Empty when Valid is
	// false.
	Ref string

	// Path is the absolute path to the profile's directory.
	Path string

	// Valid is false when the directory's mneme-profile.toml is missing,
	// unparsable, or fails Manifest.Validate. An invalid entry never breaks
	// the rest of the listing — see Error for why.
	Valid bool

	// Error explains why Valid is false. Empty when Valid is true.
	Error string
}

// List enumerates every entry under profilesDir. Directories without a valid
// manifest are still listed (Valid=false, Error populated) rather than
// silently skipped or aborting the whole call — a single corrupted profile
// directory must never hide every other one. Returns an empty, nil slice
// (not an error) when profilesDir does not exist yet — nothing has been
// installed.
func (s *Store) List() ([]ProfileInfo, error) {
	entries, err := os.ReadDir(s.profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: store: list: read dir %s: %w", s.profilesDir, err)
	}

	var infos []ProfileInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.profilesDir, e.Name())

		manifest, err := ParseManifestFile(filepath.Join(dir, ManifestFileName))
		if err != nil {
			infos = append(infos, ProfileInfo{Name: e.Name(), Path: dir, Error: err.Error()})
			continue
		}
		if err := manifest.Validate(); err != nil {
			infos = append(infos, ProfileInfo{Name: e.Name(), Path: dir, Error: err.Error()})
			continue
		}

		ref, _ := currentRef(dir)
		infos = append(infos, ProfileInfo{
			Name:        manifest.Name,
			Version:     manifest.Version,
			Description: manifest.Description,
			Ref:         ref,
			Path:        dir,
			Valid:       true,
		})
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}
