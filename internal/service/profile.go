package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// ProfileService wraps internal/profile (the leaf) so cli/mcp never call it
// directly — the dependency rule stays cli/mcp -> service -> leaf, exactly
// like SkillsService/ConflictsService wrap internal/skill/internal/conflicts.
// §1 needs no database access at all: profiles/pins/manifests are entirely
// filesystem + git state. The seam §2 will need (a *MemoryService, to record
// provenance on rules/memories a profile writes) is intentionally NOT wired
// here — YAGNI until that spec actually lands.
type ProfileService struct {
	store *profile.Store
}

// NewProfileService constructs a ProfileService whose store operates on
// profilesDir (typically cfg.ProfilesDir()) — the leaf never resolves HOME
// itself. noPrompt, when true, disables git's interactive credential prompt
// (GIT_TERMINAL_PROMPT=0) for every git subprocess this service spawns: the
// MCP frontend passes true, since an unattended agent session must never
// hang waiting for a prompt no human will see (R1). The CLI frontend passes
// false so a developer present at a terminal can still authenticate
// interactively (design decision #11).
func NewProfileService(profilesDir string, noPrompt bool) *ProfileService {
	st := profile.NewStore(profilesDir)
	if noPrompt {
		st.GitEnv = []string{profile.GitTerminalPromptDisabled}
	}
	return &ProfileService{store: st}
}

// The following are re-exported as type aliases so cli/mcp only ever import
// internal/service for profile types — never internal/profile directly.
type (
	// ProfileAddResult reports the outcome of ProfileService.Add.
	ProfileAddResult = profile.AddResult

	// ProfileUpdateResult reports the outcome of ProfileService.Update.
	ProfileUpdateResult = profile.UpdateResult

	// ProfileInfo describes a single entry in the host-level store, as
	// reported by ProfileService.List.
	ProfileInfo = profile.ProfileInfo

	// ProfilePinState enumerates the four possible outcomes of resolving a
	// project's pin against the store.
	ProfilePinState = profile.PinState

	// ProfileResolution is the result of ProfileService.ResolvePin.
	ProfileResolution = profile.Resolution

	// ProfilePin is a project's parsed .mneme-profile pointer.
	ProfilePin = profile.Pin

	// ProfileManifest is a profile's parsed mneme-profile.toml identity.
	ProfileManifest = profile.Manifest
)

// The four PinState values, re-exported for cli/mcp.
const (
	ProfilePinAbsent    = profile.PinAbsent
	ProfilePinDefault   = profile.PinDefault
	ProfilePinInstalled = profile.PinInstalled
	ProfilePinMissing   = profile.PinMissing
)

// Add clones source into the host-level store as a new profile. See
// profile.Store.Add for the exact semantics (name derivation, atomic
// clone-to-temp-then-rename, --force overwrite).
func (s *ProfileService) Add(source, name, ref string, force bool) (*ProfileAddResult, error) {
	res, err := s.store.Add(source, name, ref, force)
	if err != nil {
		return nil, translateProfileError("service: profile: add", err)
	}
	return res, nil
}

// Update fetches and checks out ref (or pulls the current branch when ref is
// empty) for the profile named name in the host-level store.
func (s *ProfileService) Update(name, ref string) (*ProfileUpdateResult, error) {
	res, err := s.store.Update(name, ref)
	if err != nil {
		return nil, translateProfileError("service: profile: update", err)
	}
	return res, nil
}

// List enumerates every profile in the host-level store.
func (s *ProfileService) List() ([]ProfileInfo, error) {
	infos, err := s.store.List()
	if err != nil {
		return nil, fmt.Errorf("service: profile: list: %w", err)
	}
	return infos, nil
}

// ResolvePin reads the pin at projectRoot and cross-references it against
// the host-level store, without writing anything to either.
func (s *ProfileService) ResolvePin(projectRoot string) (ProfileResolution, error) {
	res, err := s.store.ResolvePin(projectRoot)
	if err != nil {
		return ProfileResolution{}, fmt.Errorf("service: profile: resolve pin: %w", err)
	}
	return res, nil
}

// translateProfileError maps internal/profile leaf sentinels to their
// model-level equivalents — the uniform contract cli/mcp compare against via
// errors.Is, matching how SkillsService/ConflictsService translate their own
// leaves' errors. When the underlying git subprocess failed because a
// disabled terminal prompt blocked an interactive credential request (R1),
// an actionable hint is appended regardless of which frontend disabled
// prompting, since the underlying git diagnostic text is the same either way.
func translateProfileError(op string, err error) error {
	switch {
	case errors.Is(err, profile.ErrProfileExists):
		return fmt.Errorf("%s: %w", op, model.ErrProfileExists)
	case errors.Is(err, profile.ErrProfileNotFound):
		return fmt.Errorf("%s: %w", op, model.ErrProfileNotFound)
	case errors.Is(err, profile.ErrProfileNameMismatch):
		return fmt.Errorf("%s: %w", op, model.ErrProfileNameMismatch)
	case errors.Is(err, profile.ErrInvalidManifest), errors.Is(err, profile.ErrInvalidPin):
		return fmt.Errorf("%s: %w: %s", op, model.ErrInvalidProfile, err)
	}

	if hint := gitAuthHint(err); hint != "" {
		return fmt.Errorf("%s: %w — %s", op, err, hint)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// gitAuthHint returns a Spanish, actionable hint when err's message indicates
// a disabled terminal prompt (GIT_TERMINAL_PROMPT=0) blocked an interactive
// git credential request — the exact condition R1 exists to fail fast on.
// Returns "" for any other kind of error.
func gitAuthHint(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "terminal prompts disabled") ||
		strings.Contains(msg, "could not read Username") ||
		strings.Contains(msg, "Authentication failed") {
		return "credenciales git requeridas; ejecuta `mneme profile add <url>` en tu terminal"
	}
	return ""
}
