package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"time"
)

// Artifact kind constants, shared between converge.go's pure comparisons and
// internal/service's materialization code — a single source of truth so the
// two packages never drift by comparing string literals independently
// (SPEC-105 P2).
const (
	LockArtifactKindAgent = "agent"
	LockArtifactKindSkill = "skill"
	LockArtifactKindBlock = "block"
)

// profileRuleScanCap mirrors the fail-safe cap the service layer enforces
// when scanning for rule ids by provenance (SPEC-105 DD3): Converged treats
// a truncated scan as always-divergent, so this constant exists here purely
// as documentation of the contract Observation.RulesTruncated encodes — the
// actual cap lives in internal/service (which does the I/O).

// Desired is the state a caller wants to see materialized in a workspace:
// a specific profile at a specific resolved commit.
type Desired struct {
	// Profile is the profile name the caller wants active.
	Profile string

	// Commit is the resolved SHA the caller wants active. For the embedded
	// default profile this is the synthetic "bundled:<...>" marker
	// ActivationInput.Commit already carries (SPEC-096 §6).
	Commit string
}

// Observation is what the service layer measured about the real world —
// filesystem stats, a managed-block read, and a rule-id scan — so that
// Converged can decide purely, without doing any I/O of its own (SPEC-105
// DD14: "measurements outside, decision inside" is the only split that keeps
// real decision logic inside the leaf while respecting the dependency rule).
type Observation struct {
	// PathExists maps an artifact's absolute Path to whether it currently
	// exists on disk. Populated for every "agent"/"skill" artifact in the
	// lock being checked.
	PathExists map[string]bool

	// BlockPresent maps a "block" artifact's Path to whether its managed
	// block marker is still present in the file (managedblock.Read's
	// `present` return).
	BlockPresent map[string]bool

	// BlockDigest maps a "block" artifact's Path to the sha256 hex of the
	// block's current content (BlockDigest of managedblock.Read's `content`
	// return), so drift in the block's content — not just its presence —
	// can be detected (SPEC-105 DD13).
	BlockDigest map[string]string

	// RuleIDs is the set of memory ids observed alive in the store with the
	// lock's profile provenance (source=profile:<lock.Profile>).
	RuleIDs []string

	// RulesTruncated is true when the rule-id scan hit its cap before
	// finishing. A truncated scan can never be trusted to represent the
	// true set, so Converged always treats it as divergent — the fail-safe
	// of SPEC-105 DD3: prefer extra reconciliation work over comparing
	// against data we know is incomplete.
	RulesTruncated bool
}

// Divergence explains ONE reason a workspace is not converged. Converged
// accumulates every applicable Divergence rather than short-circuiting on
// the first one, because callers (the orphan-lock report of DD20, the
// degradation report of D6) want the full list, not just proof that
// something is wrong.
type Divergence struct {
	// Kind is a short, stable machine-readable label — see the constants
	// below — so callers/tests can assert on the reason without parsing
	// Detail's prose.
	Kind string

	// Detail is a human-readable explanation, safe to surface in a report.
	Detail string
}

// Divergence.Kind values. Stable identifiers a caller can switch on; Detail
// carries the human-readable specifics.
const (
	DivergenceNoLock          = "no-lock"
	DivergenceProfile         = "profile"
	DivergenceCommit          = "commit"
	DivergenceMissingArtifact = "missing-artifact"
	DivergenceMissingBlock    = "missing-block"
	DivergenceBlockDrift      = "block-drift"
	DivergenceRules           = "rules"
	DivergenceRulesTruncated  = "rules-truncated"
)

// Converged decides whether the workspace described by lock (the last
// recorded activation) and obs (what the service just measured about the
// real world) is already in the state want describes (SPEC-105 DD2). It is
// a pure function — no filesystem, no database, no git — which is what
// makes it exhaustively table-driven-testable and is the actual fix for the
// bug this spec addresses: SPEC-105's root cause was never "one extra
// INSERT", it was activation having no notion of a desired state to compare
// against at all.
//
// converged is true iff ALL of the following hold:
//  1. lock is non-nil (a lock exists and parsed).
//  2. lock.Profile == want.Profile.
//  3. lock.Commit == want.Commit.
//  4. Every "agent"/"skill" artifact's Path exists (obs.PathExists[Path]).
//  5. Every "block" artifact's marker is present AND its digest matches
//     (obs.BlockPresent[Path] && obs.BlockDigest[Path] == artifact.Digest).
//     A v1 lock's block artifact carries no Digest (empty, DD7) — in that
//     case the digest comparison is skipped and only presence is checked,
//     because "no digest recorded" is not the same claim as "digest is the
//     empty string".
//  6. The set of obs.RuleIDs equals the set of ids in lock.Rules — compared
//     as SETS, not lists (DD3): two RuleSpecs sharing a topic_key collapse
//     into one row via upsert, which can make lock.Rules list the same id
//     twice. Comparing lists would make the guard permanently divergent in
//     that case — the exact bug this spec fixes, wearing a different
//     disguise.
//     6b. obs.RulesTruncated is false — a truncated scan is always divergent
//     (fail-safe, DD3).
//
// Any failing condition is reported as a Divergence; Converged does not
// stop at the first one, since a full list is what the orphan-lock and
// degradation reports need.
func Converged(lock *Lock, want Desired, obs Observation) (bool, []Divergence) {
	if lock == nil {
		return false, []Divergence{{Kind: DivergenceNoLock, Detail: "no activation lock is present"}}
	}

	var divs []Divergence

	if lock.Profile != want.Profile {
		divs = append(divs, Divergence{
			Kind:   DivergenceProfile,
			Detail: "lock names profile " + lock.Profile + ", want " + want.Profile,
		})
	}
	if lock.Commit != want.Commit {
		divs = append(divs, Divergence{
			Kind:   DivergenceCommit,
			Detail: "lock commit " + lock.Commit + " differs from want " + want.Commit,
		})
	}

	for _, a := range lock.Artifacts {
		switch a.Kind {
		case LockArtifactKindAgent, LockArtifactKindSkill:
			if !obs.PathExists[a.Path] {
				divs = append(divs, Divergence{
					Kind:   DivergenceMissingArtifact,
					Detail: a.Kind + " artifact missing on disk: " + a.Path,
				})
			}
		case LockArtifactKindBlock:
			if !obs.BlockPresent[a.Path] {
				divs = append(divs, Divergence{
					Kind:   DivergenceMissingBlock,
					Detail: "managed block marker missing from " + a.Path,
				})
				continue
			}
			// A v1 lock never recorded a Digest — an empty Digest here means
			// "not tracked", not "the digest is the empty string", so the
			// content comparison is skipped and presence alone suffices
			// (DD7 backward compatibility).
			if a.Digest != "" && obs.BlockDigest[a.Path] != a.Digest {
				divs = append(divs, Divergence{
					Kind:   DivergenceBlockDrift,
					Detail: "managed block content changed since activation: " + a.Path,
				})
			}
		}
	}

	if obs.RulesTruncated {
		divs = append(divs, Divergence{
			Kind:   DivergenceRulesTruncated,
			Detail: "rule id scan hit its cap; treating as divergent (fail-safe)",
		})
	} else if !ruleSetsEqual(lock.Rules, obs.RuleIDs) {
		divs = append(divs, Divergence{
			Kind:   DivergenceRules,
			Detail: "rule ids in the database do not match the lock's rule set",
		})
	}

	return len(divs) == 0, divs
}

// ruleSetsEqual compares lockRules and observedIDs as deduplicated SETS of
// ids, not as lists (SPEC-105 DD3) — see Converged's condition 6 doc for why
// list comparison would reintroduce the bug this spec fixes.
func ruleSetsEqual(lockRules []LockRule, observedIDs []string) bool {
	want := make(map[string]struct{}, len(lockRules))
	for _, r := range lockRules {
		want[r.ID] = struct{}{}
	}
	got := make(map[string]struct{}, len(observedIDs))
	for _, id := range observedIDs {
		got[id] = struct{}{}
	}
	if len(want) != len(got) {
		return false
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			return false
		}
	}
	return true
}

// BlockDigest returns the sha256 hex digest of a managed block's content, so
// Converged can detect a dev editing inside the block by hand — content
// mneme considers itself the sole owner of rewriting (SPEC-105 DD13). The
// caller (internal/service) reads the content once via managedblock.Read to
// learn presence; this costs no extra I/O.
func BlockDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// BackupDir returns the deterministic directory a displaced artifact backup
// is written under for one activation run: <repoRoot>/.mneme/backups/<UTC>,
// where <UTC> is at formatted as "20060102T150405Z" (SPEC-105 DD12). Two
// backups produced by the same Activate call share this directory; two
// different activations (even in the same second, in practice) get distinct
// directories because at is always lock.ActivatedAt, which is fixed once per
// activation.
func BackupDir(repoRoot string, at time.Time) string {
	return filepath.Join(repoRoot, ".mneme", "backups", at.UTC().Format("20060102T150405Z"))
}
