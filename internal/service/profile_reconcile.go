package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/managedblock"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// ReconcileAction reports what Reconcile actually did to bring a workspace
// to its desired state (SPEC-105 DD1/DD15).
type ReconcileAction string

const (
	// ReconcileNoop means the workspace was already converged: nothing was
	// read from the profile store, nothing was written. This is the hot
	// path a repeated SessionStart takes — the entire fix this spec makes,
	// expressed as one value.
	ReconcileNoop ReconcileAction = "noop"

	// ReconcileActivated means no lock was present — this is the workspace's
	// first-ever activation of any profile.
	ReconcileActivated ReconcileAction = "activated"

	// ReconcileRepaired means the same profile at the same commit was
	// already the lock's target, but the workspace had drifted (a missing
	// artifact, an edited block, or a database that disagreed with the
	// lock's rule set — the exact shape of the 8 contaminated repos this
	// spec fixes) — so it was deactivated and reactivated to restore it.
	ReconcileRepaired ReconcileAction = "repaired"

	// ReconcileSwitched means the lock named a different profile, or the
	// same profile at a different commit: the departing state was
	// deactivated and the desired one activated in its place.
	ReconcileSwitched ReconcileAction = "switched"

	// ReconcileBlocked means Reconcile refused to touch anything because the
	// on-disk lock could not be safely interpreted (a schema_version newer
	// than this binary understands, DD7) — undoing a record this build
	// cannot read is not a safe operation.
	ReconcileBlocked ReconcileAction = "blocked"
)

// ReconcileResult reports what Reconcile found and did.
type ReconcileResult struct {
	// Action is what Reconcile actually performed. See the ReconcileAction
	// constants.
	Action ReconcileAction

	// Profile is the profile Reconcile leaves active (in.Name, or
	// profile.DefaultProfileName when in requests a default activation).
	Profile string

	// Commit is the resolved commit Reconcile leaves active.
	Commit string

	// Previous is the profile that was active before this call, or "" when
	// no lock was present (Action == activated).
	Previous string

	// Divergences lists the human-readable reasons Converged found the
	// workspace NOT to match Profile/Commit — empty when Action is noop or
	// activated (nothing to compare against in the activated case).
	Divergences []string

	// Degradations mirrors ActivateResult.Degradations — populated only
	// when Action performed a fresh Activate (activated/repaired/switched).
	Degradations []string

	// Activation is the ActivateResult of the Activate call this Reconcile
	// performed, or nil when Action is noop or blocked — nothing was
	// activated in either case.
	Activation *ActivateResult
}

// Reconcile brings the workspace rooted at repoRoot to the state described
// by in — SPEC-105's central fix: activation stops being an unconditional
// "materialize everything" event and becomes "compare against what's
// already there, and only act if it disagrees" (DD1).
//
// Algorithm (DD15):
//  1. Validate in the same way Activate does (repoRoot required; Default/
//     Name resolution identical).
//  2. Read the current lock. A parse failure or a schema_version this
//     binary does not understand (model.ErrProfileLockUnsupported) aborts
//     immediately with Action=blocked and NO mutation — undoing a record
//     this build cannot safely read is not a safe operation.
//  3. If a lock is present, measure the real world (observe) and ask
//     internal/profile.Converged whether it already matches in. If so,
//     Action=noop and Reconcile returns immediately: this is the hot path a
//     repeated SessionStart takes, and it does no I/O beyond the lock read,
//     the observation, and one indexed rule-id query (DD2's "cheap guard").
//  4. Otherwise, preflightDeactivate the current lock (DD16: fail before
//     touching anything), Deactivate it (restoring any backups, DD5), and
//     Activate in (whose own preflightActivate protects the write phase).
//  5. Action is activated (no prior lock), repaired (same profile+commit,
//     but the workspace had drifted), or switched (different profile or
//     commit).
func (s *ProfileService) Reconcile(ctx context.Context, repoRoot string, in ActivationInput) (*ReconcileResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: profile: reconcile: repo root is required")
	}
	in.RepoRoot = repoRoot

	isDefault := in.isDefaultActivation()
	if !isDefault && in.Name == "" {
		return nil, fmt.Errorf("service: profile: reconcile: profile name is required")
	}
	desiredName := in.Name
	if isDefault {
		desiredName = profile.DefaultProfileName
	}

	lock, present, err := s.ActiveLock(repoRoot)
	if err != nil {
		if errors.Is(err, model.ErrProfileLockUnsupported) {
			return &ReconcileResult{
				Action:  ReconcileBlocked,
				Profile: desiredName,
				Commit:  in.Commit,
			}, fmt.Errorf("service: profile: reconcile: %w", err)
		}
		return nil, fmt.Errorf("service: profile: reconcile: %w", err)
	}

	var divergences []string
	if present {
		obs, obsErr := s.observe(ctx, lock)
		if obsErr != nil {
			return nil, fmt.Errorf("service: profile: reconcile: %w", obsErr)
		}

		ok, divs := profile.Converged(lock, profile.Desired{Profile: desiredName, Commit: in.Commit}, obs)
		if ok {
			return &ReconcileResult{
				Action:   ReconcileNoop,
				Profile:  desiredName,
				Commit:   in.Commit,
				Previous: lock.Profile,
			}, nil
		}
		for _, d := range divs {
			divergences = append(divergences, d.Detail)
		}

		if failures := preflightDeactivate(lock); len(failures) > 0 {
			return nil, fmt.Errorf("service: profile: reconcile: preflight deactivate failed: %s", strings.Join(failures, "; "))
		}
		if err := s.Deactivate(ctx, lock); err != nil {
			return nil, fmt.Errorf("service: profile: reconcile: deactivate: %w", err)
		}
	}

	activation, err := s.Activate(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("service: profile: reconcile: %w", err)
	}

	action := ReconcileActivated
	previous := ""
	if present {
		previous = lock.Profile
		if lock.Profile != desiredName || lock.Commit != in.Commit {
			action = ReconcileSwitched
		} else {
			action = ReconcileRepaired
		}
	}

	return &ReconcileResult{
		Action:       action,
		Profile:      desiredName,
		Commit:       in.Commit,
		Previous:     previous,
		Divergences:  divergences,
		Degradations: activation.Degradations,
		Activation:   activation,
	}, nil
}

// observe measures the real world for every artifact/rule lock names, with
// NO decision-making of its own (SPEC-105 DD14: "measurements outside,
// decision inside" — Converged, the leaf, makes the decision; this impure
// half only gathers the facts it needs).
func (s *ProfileService) observe(ctx context.Context, lock *profile.Lock) (profile.Observation, error) {
	obs := profile.Observation{
		PathExists:   make(map[string]bool),
		BlockPresent: make(map[string]bool),
		BlockDigest:  make(map[string]string),
	}

	for _, a := range lock.Artifacts {
		switch a.Kind {
		case profile.LockArtifactKindAgent, profile.LockArtifactKindSkill:
			_, statErr := os.Stat(a.Path)
			obs.PathExists[a.Path] = statErr == nil
		case profile.LockArtifactKindBlock:
			content, _, present, err := managedblock.Read(a.Path, a.Marker)
			if err != nil {
				return profile.Observation{}, fmt.Errorf("observe block %s: %w", a.Path, err)
			}
			obs.BlockPresent[a.Path] = present
			if present {
				obs.BlockDigest[a.Path] = profile.BlockDigest(content)
			}
		}
	}

	ids, truncated, err := s.mem.ListProfileRuleIDs(ctx, lock.Profile)
	switch {
	case err == nil:
		obs.RuleIDs = ids
		obs.RulesTruncated = truncated
	case errors.Is(err, model.ErrProjectSlugRequired):
		// No project slug resolved: there is no project-scoped set to
		// compare against. obs.RuleIDs stays nil — if lock.Rules is
		// non-empty, Converged correctly reports divergence on condition 6;
		// this is not a failure of the guard, it mirrors materializeRules'
		// own degrade-not-fail posture (DD8 layer 4).
	default:
		return profile.Observation{}, fmt.Errorf("observe rule ids: %w", err)
	}

	return obs, nil
}

// preflightDeactivate checks every filesystem precondition Deactivate will
// need against lock — BEFORE any of it runs (SPEC-105 DD16): each artifact's
// parent directory is removable/writable, each registered Backup is
// readable, and a block artifact's containing file is itself writable.
// Returns the list of unmet preconditions; an empty list means Deactivate
// may proceed. A nil lock (nothing to deactivate) always returns nil.
func preflightDeactivate(lock *profile.Lock) []string {
	if lock == nil {
		return nil
	}

	var failures []string
	for _, a := range lock.Artifacts {
		if msg := checkDirWritable(filepath.Dir(a.Path)); msg != "" {
			failures = append(failures, msg)
		}
		if a.Kind == profile.LockArtifactKindBlock {
			if msg := checkFileWritableOrCreatable(a.Path); msg != "" {
				failures = append(failures, msg)
			}
		}
		if a.Backup != "" {
			if _, err := os.Stat(a.Backup); err != nil {
				failures = append(failures, fmt.Sprintf("registered backup %s is not readable: %v", a.Backup, err))
			}
		}
	}
	return failures
}
