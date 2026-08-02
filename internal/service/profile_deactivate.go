package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/managedblock"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// DeactivateInput parameterises DeactivateProject.
type DeactivateInput struct {
	// RepoRoot is the absolute path of the project's repository root.
	RepoRoot string

	// Apply, when true, executes the plan (SPEC-105 DD17). Default false:
	// DeactivateProject only computes and returns the plan, mutating
	// nothing — mirroring `mneme init`/`mneme conflicts scan`'s own
	// dry-run-by-default precedent.
	Apply bool
}

// DeactivatePlanArtifact describes what will happen (or happened) to one
// artifact the departing lock recorded.
type DeactivatePlanArtifact struct {
	// Kind is the artifact's kind: "agent", "skill", or "block".
	Kind string `json:"kind"`

	// Path is the artifact's absolute path.
	Path string `json:"path"`

	// Action is what Deactivate will do to Path: "remove" (delete it
	// outright — it belonged to the departing profile), "restore" (a
	// backup exists; the dev's own pre-activation file/directory is
	// restored, then the backup is deleted), or "remove-file" (a "block"
	// artifact whose containing file the activation itself created, and
	// which will end up empty once the block is removed).
	Action string `json:"action"`

	// Exists reports whether Path currently exists on disk.
	Exists bool `json:"exists"`

	// Backup is the pre-activation backup path this artifact will restore
	// from, or "" when Action != "restore".
	Backup string `json:"backup,omitempty"`
}

// DeactivateResult is the plan (dry-run) or outcome (--apply) of
// DeactivateProject — the SAME shape either way (Applied distinguishes
// them), so the CLI's --json output and the profile_deactivate MCP tool
// serialise one object regardless of mode (SPEC-105 DD18).
type DeactivateResult struct {
	// Applied is false in dry-run mode, true once the plan actually ran.
	Applied bool `json:"applied"`

	// Profile, Commit, Ref identify the departing activation.
	Profile string `json:"profile"`
	Commit  string `json:"commit"`
	Ref     string `json:"ref"`

	// ActivatedAt is when the departing activation ran.
	ActivatedAt time.Time `json:"activated_at"`

	// Artifacts is the per-artifact plan/outcome.
	Artifacts []DeactivatePlanArtifact `json:"artifacts,omitempty"`

	// RuleIDs is the ids of every rule memory alive in the project store
	// with the departing profile's provenance.
	RuleIDs []string `json:"rule_ids,omitempty"`

	// OrphanRuleIDs is the ids of rows with the departing profile's
	// provenance that leaked into the global store (SPEC-105 DD10) —
	// residue of a slug-empty activation, swept regardless of whether this
	// repo itself has a slug.
	OrphanRuleIDs []string `json:"orphan_rule_ids,omitempty"`

	// LockPath is the activation lock this operation will (or did) delete.
	LockPath string `json:"lock_path"`

	// NextSession explains what SessionStart will do on the NEXT session,
	// computed BEFORE applying anything (SPEC-105 DD19): deactivate never
	// touches the pin or the host default, so if either would reactivate
	// this same profile, the operator needs to know before relying on
	// "deactivated" meaning "gone for good".
	NextSession string `json:"next_session"`

	// ResidualBackups lists pre-activation backup run directories
	// (SPEC-105 DD12) belonging to OTHER activations that this call leaves
	// untouched — cleanup of those is the operator's call, not this one's.
	ResidualBackups []string `json:"residual_backups,omitempty"`

	// Warnings carries non-fatal advisories — e.g. no lock was present, or
	// rules could not be enumerated because no project slug resolved.
	Warnings []string `json:"warnings,omitempty"`
}

// DeactivateProject computes the plan to undo whatever profile is active
// for in.RepoRoot and, when in.Apply is true, executes it (SPEC-105 DD17):
// every materialized artifact is restored/removed (Deactivate, DD5-aware),
// the activation lock is deleted, but — deliberately — <in.RepoRoot>/
// .mneme-profile (the pin) is NEVER touched (DD19): it is a committed,
// team-shared file, and this is a local "undo the materialization" op, not
// a decision to strip the team's profile choice from the repo. NextSession
// is computed and returned so the caller knows, before applying anything,
// whether the pin or the host default will simply reactivate the same
// profile on the next SessionStart.
func (s *ProfileService) DeactivateProject(ctx context.Context, in DeactivateInput) (*DeactivateResult, error) {
	if in.RepoRoot == "" {
		return nil, fmt.Errorf("service: profile: deactivate project: repo root is required")
	}

	lock, present, err := s.ActiveLock(in.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: profile: deactivate project: %w", err)
	}

	var warnings []string
	nextSession, nsErr := s.nextSessionText(in.RepoRoot)
	if nsErr != nil {
		if !errors.Is(nsErr, model.ErrProfileServiceNotConfigured) {
			return nil, fmt.Errorf("service: profile: deactivate project: %w", nsErr)
		}
		warnings = append(warnings, "no se pudo calcular NextSession: el config path del servicio no está configurado")
	}

	if !present {
		return &DeactivateResult{
			Applied:     false,
			LockPath:    profile.LockPath(in.RepoRoot),
			NextSession: nextSession,
			Warnings:    append(warnings, "no hay ningún lock de activación en "+profile.LockPath(in.RepoRoot)+" — nada que desactivar"),
		}, nil
	}

	result := &DeactivateResult{
		Profile:     lock.Profile,
		Commit:      lock.Commit,
		Ref:         lock.Ref,
		ActivatedAt: lock.ActivatedAt,
		LockPath:    profile.LockPath(in.RepoRoot),
		NextSession: nextSession,
	}

	for _, a := range lock.Artifacts {
		artifact := DeactivatePlanArtifact{Kind: a.Kind, Path: a.Path, Backup: a.Backup}
		if _, statErr := os.Stat(a.Path); statErr == nil {
			artifact.Exists = true
		}
		switch {
		case a.Backup != "":
			artifact.Action = "restore"
		case a.Kind == profile.LockArtifactKindBlock:
			artifact.Action = blockDeactivateAction(a)
		default:
			artifact.Action = "remove"
		}
		result.Artifacts = append(result.Artifacts, artifact)
	}

	if s.mem != nil {
		ids, _, ruleErr := s.mem.ListProfileRuleIDs(ctx, lock.Profile)
		switch {
		case ruleErr == nil:
			result.RuleIDs = ids
		case errors.Is(ruleErr, model.ErrProjectSlugRequired):
			warnings = append(warnings,
				"no se pudieron enumerar las rules de proyecto: este directorio no resuelve un slug de proyecto")
		default:
			return nil, fmt.Errorf("service: profile: deactivate project: %w", ruleErr)
		}

		orphanIDs, orphanErr := s.mem.ListOrphanProfileRuleIDs(ctx, lock.Profile)
		if orphanErr != nil {
			return nil, fmt.Errorf("service: profile: deactivate project: %w", orphanErr)
		}
		result.OrphanRuleIDs = orphanIDs
	}

	result.ResidualBackups = residualBackupDirs(in.RepoRoot, lock)
	result.Warnings = warnings

	if !in.Apply {
		return result, nil
	}

	if failures := preflightDeactivate(lock); len(failures) > 0 {
		return nil, fmt.Errorf("service: profile: deactivate project: preflight failed: %s", strings.Join(failures, "; "))
	}
	if err := s.Deactivate(ctx, lock); err != nil {
		return nil, fmt.Errorf("service: profile: deactivate project: %w", err)
	}
	if err := os.Remove(profile.LockPath(in.RepoRoot)); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("service: profile: deactivate project: remove lock: %w", err)
	}

	result.Applied = true
	result.ResidualBackups = residualBackupDirs(in.RepoRoot, lock)
	return result, nil
}

// nextSessionText computes DeactivateResult.NextSession's three cases
// (SPEC-105 DD19), reusing ResolveActive — the same pin>default>vanilla
// precedence SessionStart itself consults.
func (s *ProfileService) nextSessionText(repoRoot string) (string, error) {
	res, err := s.ResolveActive(repoRoot)
	if err != nil {
		return "", err
	}

	name := ""
	if res.Resolution.Pin != nil {
		name = res.Resolution.Pin.Name
	}

	switch res.Source {
	case profile.SourcePin:
		return fmt.Sprintf(
			"el próximo SessionStart reactivará %s (pin .mneme-profile); elimina el pin del repo si quieres desactivarlo de forma permanente",
			name), nil
	case profile.SourceGlobalDefault:
		return fmt.Sprintf(
			"el próximo SessionStart reactivará %s (default global); ejecuta `mneme profile default --clear`",
			name), nil
	default:
		return "el próximo SessionStart correrá en modo vanilla", nil
	}
}

// blockDeactivateAction previews removeBlockArtifact's decision for a
// "block" artifact WITHOUT mutating anything: "remove-file" when the
// artifact's Created flag is true and removing its managed block would
// leave the file empty or whitespace-only, "remove" otherwise (the block
// content goes, the surrounding file — CLAUDE.md prose that predates the
// activation — stays).
func blockDeactivateAction(a profile.LockArtifact) string {
	if !a.Created {
		return "remove"
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "remove"
	}
	if strings.TrimSpace(managedblock.RemoveText(string(data), a.Marker)) == "" {
		return "remove-file"
	}
	return "remove"
}

// residualBackupDirs lists every top-level directory under
// <repoRoot>/.mneme/backups/ EXCEPT the one lock's own artifacts point
// into — the run directories left behind by OTHER activations (SPEC-105
// DD12), which this operation deliberately never touches. Returns nil when
// no backups directory exists.
func residualBackupDirs(repoRoot string, lock *profile.Lock) []string {
	backupsRoot := filepath.Join(repoRoot, ".mneme", "backups")
	entries, err := os.ReadDir(backupsRoot)
	if err != nil {
		return nil
	}

	currentRun := ""
	for _, a := range lock.Artifacts {
		if a.Backup != "" {
			currentRun = backupRunDir(a.Backup)
			break
		}
	}

	var residual []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(backupsRoot, e.Name())
		if full == currentRun {
			continue
		}
		residual = append(residual, full)
	}
	return residual
}
