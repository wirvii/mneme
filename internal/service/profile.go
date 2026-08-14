package service

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// ProfileService wraps internal/profile (the leaf) so cli/mcp never call it
// directly — the dependency rule stays cli/mcp -> service -> leaf, exactly
// like SkillsService/ConflictsService wrap internal/skill/internal/conflicts.
// §1 needed no database access at all: profiles/pins/manifests were entirely
// filesystem + git state. §2 cables the seam §1 left documented and dormant:
// mem (to insert/purge provenance-marked rules) and sub (to read a project's
// capa-2/3 for the agent-fusion step) are both optional — nil unless a
// caller opts in via WithProfileMemoryService/WithProfileSubagentService —
// so every existing construction (`profile add/update/list/status`, which
// never touch either) keeps compiling unchanged. Activate/Switch/Deactivate
// fail fast with a clear error if called before those seams are wired.
type ProfileService struct {
	store *profile.Store

	// mem inserts/purges the provenance-marked rule memories a profile
	// activation materializes (SPEC-092 §2). nil until
	// WithProfileMemoryService is passed to NewProfileService.
	mem *MemoryService

	// sub reads a project's capa-2/3 (ReadProfile) for the agent-fusion step
	// (SPEC-092 §3.5). nil until WithProfileSubagentService is passed to
	// NewProfileService.
	sub *SubagentService

	// skillsDir is the primary host-level skills directory, injected the same way SkillsService's own
	// skillsDir is — this package never resolves HOME itself. Empty unless
	// WithProfileSkillsDir is passed; Activate errors if a profile declares
	// skills but this is unset.
	skillsDir    string
	skillMirrors []string

	// configPath is the config.toml path (typically config.DefaultPath())
	// this service reads/writes [profiles].default against (SPEC-093 §3).
	// Empty unless WithProfileConfigPath is passed; SetDefault/ClearDefault/
	// Default/ResolveActive all return model.ErrProfileServiceNotConfigured
	// when it is unset — mirrors Activate's mem/sub guard.
	configPath string

	// bootstrapper runs a scaffold's pinned official generator subprocess
	// (SPEC-098 §7a §4.4) during NewProject's assembly. nil until
	// WithProfileBootstrapper is passed — a single-layout scaffold never needs
	// it (single has no bootstrap), so every existing construction keeps
	// working; only a bootstrap-bearing plan errors when it is unset. Tests
	// inject a fake that writes a fixture tree with zero network.
	bootstrapper Bootstrapper

	// defaultFS is the embedded OSS "default profile" (install.DefaultProfileFS,
	// SPEC-096 §6) — the source Activate reads from for a PinDefault
	// activation, injected by the frontend so this package never imports
	// internal/install (preserves the SPEC-092 layering rule). nil until
	// WithDefaultProfileFS is passed; Activate/DefaultManifest return
	// model.ErrDefaultProfileUnavailable when a default activation is
	// attempted without it — should never happen in a real binary, every
	// production frontend wires it.
	defaultFS fs.FS
}

// ProfileOption configures a ProfileService at construction time, mirroring
// MemoryService's own Option pattern (service.Option). Every option added
// here is additive: existing NewProfileService call sites that pass no
// options keep compiling and behaving exactly as before.
type ProfileOption func(*ProfileService)

// WithProfileMemoryService wires mem into the ProfileService, enabling
// Activate/Switch/Deactivate to insert and purge provenance-marked rule
// memories (SPEC-092 §2). Required for any of those three methods to work —
// omitting it is only valid for a ProfileService that only ever calls
// Add/Update/List/ResolvePin (the §1 surface).
func WithProfileMemoryService(mem *MemoryService) ProfileOption {
	return func(s *ProfileService) { s.mem = mem }
}

// WithProfileSubagentService wires sub into the ProfileService, enabling
// Activate/Switch to read a project's capa-2/3 (SubagentService.ReadProfile)
// for the agent-fusion step (SPEC-092 §3.5). A ProfileService without this
// wired still activates agents — fuseAgent degrades cleanly to capa-1 alone
// when sub is nil, exactly like when the repo has no capa-2/3 yet.
func WithProfileSubagentService(sub *SubagentService) ProfileOption {
	return func(s *ProfileService) { s.sub = sub }
}

// WithProfileSkillsDir wires the primary host-level skills directory into
// the ProfileService, so Activate/Deactivate know where to materialize/remove
// a profile's skills/ entries.
func WithProfileSkillsDir(dir string) ProfileOption {
	return func(s *ProfileService) { s.skillsDir = dir }
}

// WithProfileSkillMirrors adds runtime discovery directories that receive
// the same profile-declared skills as the primary directory.
func WithProfileSkillMirrors(dirs ...string) ProfileOption {
	return func(s *ProfileService) {
		for _, dir := range dirs {
			if dir == "" || dir == s.skillsDir || slices.Contains(s.skillMirrors, dir) {
				continue
			}
			s.skillMirrors = append(s.skillMirrors, dir)
		}
	}
}

// WithProfileConfigPath wires the config.toml path into the ProfileService
// (SPEC-093 §3), enabling SetDefault/ClearDefault/Default/ResolveActive to
// read/write [profiles].default. A ProfileService without this wired still
// supports Add/Update/List/ResolvePin/Activate/Switch — only the §3 verbs
// that touch the host default need it.
func WithProfileConfigPath(path string) ProfileOption {
	return func(s *ProfileService) { s.configPath = path }
}

// WithProfileBootstrapper wires the scaffold bootstrapper into the
// ProfileService (SPEC-098 §7a): the Strategy NewProject invokes to run a
// scaffold's pinned official generator (e.g. `pnpm dlx create-turbo@2.3.1`).
// Production frontends pass service.NewExecBootstrapper(); tests pass a fake
// that materializes a fixture tree with zero network. A ProfileService without
// this wired still assembles any single-layout scaffold (which has no
// bootstrap); only a bootstrap-bearing plan needs it.
func WithProfileBootstrapper(b Bootstrapper) ProfileOption {
	return func(s *ProfileService) { s.bootstrapper = b }
}

// WithDefaultProfileFS wires the embedded OSS default profile into the
// ProfileService (SPEC-096 §6): the fs.FS Activate reads from when
// activating the sourceless PinDefault profile (profile.DefaultProfileName)
// instead of a checkout under the host-level store. Production frontends
// pass install.DefaultProfileFS() — this package itself never imports
// internal/install, keeping the cli/mcp -> service -> leaf dependency rule
// intact. A ProfileService without this wired still activates any
// store-backed profile unchanged; only a PinDefault activation needs it
// (model.ErrDefaultProfileUnavailable otherwise).
func WithDefaultProfileFS(fsys fs.FS) ProfileOption {
	return func(s *ProfileService) { s.defaultFS = fsys }
}

// NewProfileService constructs a ProfileService whose store operates on
// profilesDir (typically cfg.ProfilesDir()) — the leaf never resolves HOME
// itself. noPrompt, when true, disables git's interactive credential prompt
// (GIT_TERMINAL_PROMPT=0) for every git subprocess this service spawns: the
// MCP frontend passes true, since an unattended agent session must never
// hang waiting for a prompt no human will see (R1). The CLI frontend passes
// false so a developer present at a terminal can still authenticate
// interactively (design decision #11).
func NewProfileService(profilesDir string, noPrompt bool, opts ...ProfileOption) *ProfileService {
	st := profile.NewStore(profilesDir)
	if noPrompt {
		st.GitEnv = []string{profile.GitTerminalPromptDisabled}
	}
	svc := &ProfileService{store: st}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
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

	// ProfileActiveSource explains which precedence tier (pin / host
	// default / vanilla) decided a project's active profile
	// (ProfileService.ResolveActive, SPEC-093 §3.5).
	ProfileActiveSource = profile.ActiveSource

	// ProfileActiveResolution is the result of ProfileService.ResolveActive.
	ProfileActiveResolution = profile.ActiveResolution
)

// The four PinState values, re-exported for cli/mcp.
const (
	ProfilePinAbsent    = profile.PinAbsent
	ProfilePinDefault   = profile.PinDefault
	ProfilePinInstalled = profile.PinInstalled
	ProfilePinMissing   = profile.PinMissing
)

// The three ActiveSource values, re-exported for cli/mcp (SPEC-093 §3.5).
const (
	ProfileSourceVanilla       = profile.SourceVanilla
	ProfileSourcePin           = profile.SourcePin
	ProfileSourceGlobalDefault = profile.SourceGlobalDefault
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
	case errors.Is(err, profile.ErrUnknownWiringAction):
		return fmt.Errorf("%s: %w: %s", op, model.ErrUnknownWiringAction, err)
	case errors.Is(err, profile.ErrInvalidLayout),
		errors.Is(err, profile.ErrInvalidToolchain),
		errors.Is(err, profile.ErrBootstrapNotPinned),
		errors.Is(err, profile.ErrInvalidScaffold):
		return fmt.Errorf("%s: %w: %s", op, model.ErrInvalidScaffold, err)
	case errors.Is(err, profile.ErrScaffoldNotFound):
		return fmt.Errorf("%s: %w", op, model.ErrScaffoldNotFound)
	case errors.Is(err, profile.ErrLayoutUnsupported):
		return fmt.Errorf("%s: %w", op, model.ErrLayoutUnsupported)
	case errors.Is(err, profile.ErrAppAddNotApplicable):
		return fmt.Errorf("%s: %w", op, model.ErrAppAddNotApplicable)
	case errors.Is(err, profile.ErrNothingToCapture):
		return fmt.Errorf("%s: %w", op, model.ErrNothingToCapture)
	case errors.Is(err, profile.ErrInvalidLock):
		return fmt.Errorf("%s: %w", op, model.ErrProfileLockUnsupported)
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
