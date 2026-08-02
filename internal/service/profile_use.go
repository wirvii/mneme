package service

import (
	"context"
	"fmt"
	"os"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// UseResult reports the outcome of ProfileService.Use: the pin written and
// whether Reconcile materialized it.
type UseResult struct {
	// Name is the activated profile's name.
	Name string `json:"name"`

	// Source is the git remote the pin now points at — empty when the
	// checkout had no "origin" remote configured (see Warnings).
	Source string `json:"source"`

	// Ref is the tag/SHA the pin now points at.
	Ref string `json:"ref"`

	// ProjectRoot is the repository root the pin was written to.
	ProjectRoot string `json:"project_root"`

	// Materialized is true whenever Reconcile did not report
	// ReconcileBlocked — SPEC-105 DD15: Use always attempts immediate
	// reconciliation (see Use's doc comment for why), and a noop
	// (workspace already matched) is just as "materialized" as a fresh
	// activation from this caller's point of view.
	Materialized bool `json:"materialized"`

	// Action is Reconcile's own ReconcileAction ("activated", "noop",
	// "repaired", "switched"), so a caller that cares can distinguish a
	// fresh activation from a convergence no-op.
	Action string `json:"action"`

	// Warnings carries any non-blocking advisories from Store.PinFromStore
	// (e.g. no "origin" remote configured on the checkout).
	Warnings []string `json:"warnings,omitempty"`
}

// DefaultResult reports the host-level default profile, as set by
// SetDefault/ClearDefault, or as read by Default.
type DefaultResult struct {
	// Default is the safe-slug name of the host default profile, or "" for
	// vanilla.
	Default string `json:"default"`
}

// Use activates the profile named name for the project rooted at
// projectRoot (SPEC-093 §3.2, "= nvm use"): it reconstructs a
// self-describing pin from name's checkout in the host-level store
// (Store.PinFromStore), writes it to <projectRoot>/.mneme-profile
// (profile.WritePin — preserving any preexisting "scaffold" field), and
// immediately reconciles it (Reconcile, SPEC-105 DD15) — no longer a bare
// Activate: a repeated `profile use` against the same already-converged
// profile is now a cheap noop instead of a redundant re-materialization.
// Use NEVER clones — name must already be installed (model.ErrProfileNotFound
// otherwise, pointing the caller at `profile add`), keeping the add/use
// frontier strict.
//
// Unlike the SessionStart integration (§3.6), which is fail-open by
// contract, a reconciliation failure here IS propagated as an error (D7):
// the caller explicitly asked to activate a profile and must know if it
// failed — including a ReconcileBlocked outcome (a lock this build cannot
// safely interpret), which Reconcile itself already turns into an error.
func (s *ProfileService) Use(ctx context.Context, projectRoot, name string) (*UseResult, error) {
	if projectRoot == "" {
		return nil, fmt.Errorf("service: profile: use: project root is required")
	}
	if name == "" {
		return nil, fmt.Errorf("service: profile: use: profile name is required")
	}

	pinResult, err := s.store.PinFromStore(name)
	if err != nil {
		return nil, translateProfileError("service: profile: use", err)
	}

	if err := profile.WritePin(projectRoot, pinResult.Pin); err != nil {
		return nil, translateProfileError("service: profile: use", err)
	}

	result, err := s.Reconcile(ctx, projectRoot, ActivationInput{
		Name:   pinResult.Pin.Name,
		Source: pinResult.Pin.Source,
		Ref:    pinResult.Pin.Ref,
		Commit: pinResult.Commit,
	})
	if err != nil {
		return nil, fmt.Errorf("service: profile: use: reconcile: %w", err)
	}

	return &UseResult{
		Name:        pinResult.Pin.Name,
		Source:      pinResult.Pin.Source,
		Ref:         pinResult.Pin.Ref,
		ProjectRoot: projectRoot,
		// Reconcile only returns a nil error for activated/noop/repaired/
		// switched — ReconcileBlocked always comes back as a non-nil error
		// (handled above), so reaching this point means the workspace is
		// materialized one way or another.
		Materialized: true,
		Action:       string(result.Action),
		Warnings:     pinResult.Warnings,
	}, nil
}

// ResolveCommit returns the current HEAD commit SHA of the profile named
// name's checkout in the host-level store. Used by the SessionStart
// integration (§3.6) to populate ActivationInput.Commit when it already has
// a Pin from ResolveActive (which carries no Commit field), rather than one
// freshly reconstructed by Use/PinFromStore.
func (s *ProfileService) ResolveCommit(name string) (string, error) {
	commit, err := s.store.HeadCommit(name)
	if err != nil {
		return "", translateProfileError("service: profile: resolve commit", err)
	}
	return commit, nil
}

// SetDefault fixes the host-level default profile for sessions with no repo
// pin (SPEC-093 §3.3, "= nvm alias default"). name == "" behaves exactly
// like ClearDefault. Returns model.ErrProfileNotFound when name is not
// present in the host-level store — a default profile that resolves to
// nothing is a footgun the dev can avoid with `profile add` first (design
// decision A1). Never materializes anything and never touches a repository
// that already has its own pin — see ResolveActive for why that is safe.
func (s *ProfileService) SetDefault(name string) (*DefaultResult, error) {
	if name == "" {
		return s.ClearDefault()
	}
	if s.configPath == "" {
		return nil, fmt.Errorf("service: profile: set default: %w", model.ErrProfileServiceNotConfigured)
	}

	dir, err := s.store.ProfilePath(name)
	if err != nil {
		return nil, translateProfileError("service: profile: set default", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("service: profile: set default: %q not installed; run `mneme profile add` first: %w", name, model.ErrProfileNotFound)
		}
		return nil, fmt.Errorf("service: profile: set default: stat %s: %w", dir, statErr)
	}

	if err := config.SetProfilesDefault(s.configPath, name); err != nil {
		return nil, fmt.Errorf("service: profile: set default: %w", err)
	}
	return &DefaultResult{Default: name}, nil
}

// ClearDefault clears the host-level default profile (--clear), reverting
// sessions with no repo pin to vanilla.
func (s *ProfileService) ClearDefault() (*DefaultResult, error) {
	if s.configPath == "" {
		return nil, fmt.Errorf("service: profile: clear default: %w", model.ErrProfileServiceNotConfigured)
	}
	if err := config.SetProfilesDefault(s.configPath, ""); err != nil {
		return nil, fmt.Errorf("service: profile: clear default: %w", err)
	}
	return &DefaultResult{Default: ""}, nil
}

// Default reports the host-level default profile currently configured
// (read-only — "profile default" invoked with no arguments).
func (s *ProfileService) Default() (*DefaultResult, error) {
	if s.configPath == "" {
		return nil, fmt.Errorf("service: profile: default: %w", model.ErrProfileServiceNotConfigured)
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return nil, fmt.Errorf("service: profile: default: load config: %w", err)
	}
	return &DefaultResult{Default: cfg.Profiles.Default}, nil
}

// ResolveActive resolves the profile active for projectRoot under the
// precedence rule "pin > host default > vanilla" (SPEC-093 §3.5): it reads
// cfg.Profiles.Default here — and ONLY here in the whole runtime (see
// runHookSessionStart, AC10: the default is read-once-not-live, never
// re-consulted mid-session) — and composes it with the leaf's pure
// Store.ResolveActive.
func (s *ProfileService) ResolveActive(projectRoot string) (ProfileActiveResolution, error) {
	if s.configPath == "" {
		return ProfileActiveResolution{}, fmt.Errorf("service: profile: resolve active: %w", model.ErrProfileServiceNotConfigured)
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return ProfileActiveResolution{}, fmt.Errorf("service: profile: resolve active: load config: %w", err)
	}

	res, err := s.store.ResolveActive(projectRoot, cfg.Profiles.Default)
	if err != nil {
		return ProfileActiveResolution{}, fmt.Errorf("service: profile: resolve active: %w", err)
	}
	return res, nil
}
