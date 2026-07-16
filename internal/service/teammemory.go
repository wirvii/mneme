package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/vault"
)

// sharedVaultRelDir is the path, relative to a git repository root, where the
// git-native team-memory vault lives (SPEC-053 D1). Its marker file's
// presence there is what turns write-through materialization on for the
// current process (SPEC-053 D3) — there is no other config flag.
const sharedVaultRelDir = "shared"

// nonSharedTypes are the only memory types NEVER auto-shared to the team
// vault: synthesis (auto-generated cluster overviews each peer regenerates)
// and session_summary (ephemeral, verbose). Every other project-scoped type
// is human-authored persistent knowledge and shares by default (SPEC-071).
var nonSharedTypes = map[model.MemoryType]bool{
	model.TypeSynthesis:      true,
	model.TypeSessionSummary: true,
}

// autoSharedType reports whether a project-scoped memory of type t is
// auto-shared to the team vault by default. Policy is share-by-default
// (SPEC-071): only auto-generated/ephemeral types are excluded.
func autoSharedType(t model.MemoryType) bool { return !nonSharedTypes[t] }

// TeamMemoryState is resolved once, by DetectTeamMemory, and handed to
// MemoryService at construction time (SPEC-085 D1/D2) — the constructor never
// resolves it itself. It is cached for the process lifetime: repeated
// Save/Update calls never re-run "git rev-parse" or re-stat the marker.
type TeamMemoryState struct {
	// Enabled is true when the current repository has opted into team-memory
	// (the shared vault marker exists). When false, Shared is always baked to
	// 0 and nothing is ever materialized, regardless of memory type.
	Enabled bool

	// VaultRoot is the absolute path to <repoRoot>/.mneme/shared. Only
	// meaningful when Enabled is true.
	VaultRoot string
}

// DetectTeamMemory resolves the git repository root for the current process
// working directory and checks whether the team-memory marker file exists
// inside <repoRoot>/.mneme/shared/. Both steps are best-effort: any failure
// (not a git repository, git not installed, marker unreadable) simply yields
// a disabled state — team-memory is opt-in, never a hard requirement for
// mneme to function (SPEC-053 D3).
//
// DetectTeamMemory performs I/O ambient to the calling process (os.Getwd,
// exec.Command("git", …), os.Stat) and is deliberately NOT called by
// NewMemoryService (SPEC-085 D1): a constructor that resolves environment
// state itself is untestable by construction. Callers that want production
// auto-detection call DetectTeamMemory explicitly and pass the result via
// WithTeamMemory — currently the sole production call site is
// internal/cli.initService.
func DetectTeamMemory() TeamMemoryState {
	cwd, err := os.Getwd()
	if err != nil {
		return TeamMemoryState{}
	}

	root, err := gitRepoRoot(cwd)
	if err != nil {
		return TeamMemoryState{}
	}

	vaultRoot := filepath.Join(root, ".mneme", sharedVaultRelDir)
	markerPath := filepath.Join(vaultRoot, vault.MarkerFileName)

	if _, statErr := os.Stat(markerPath); statErr != nil {
		return TeamMemoryState{VaultRoot: vaultRoot}
	}

	return TeamMemoryState{Enabled: true, VaultRoot: vaultRoot}
}

// gitRepoRoot runs "git rev-parse --show-toplevel" in cwd and returns the
// absolute repository root. Returns an error when cwd is not inside a git
// repository or git is not on PATH — the caller treats this as "team-memory
// not applicable here", not a fatal condition.
func gitRepoRoot(cwd string) (string, error) {
	//nolint:gosec // fixed subcommand, no user input reaches exec.Command here.
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gitRepoRoot: not a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// suppressMaterializeKey is the context key used by WithSuppressMaterialize.
// A dedicated unexported type avoids collisions with keys from other packages
// (the standard Go context-key idiom).
type suppressMaterializeKey struct{}

// WithSuppressMaterialize returns a context that instructs Save/Update to
// skip team-memory write-through materialization for the duration of the
// call it decorates.
//
// This is the base of the anti-loop guard (SPEC-053 D5): the future
// vault-import path (SS-D) will call Save/Update while replaying memories
// read from the shared vault, and must not re-materialize them back into the
// very vault it just read from — an infinite write loop. SS-D is expected to
// wrap its Save/Update calls as:
//
//	ctx = service.WithSuppressMaterialize(ctx)
//	svc.Save(ctx, req) // or svc.Update(ctx, id, req)
func WithSuppressMaterialize(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressMaterializeKey{}, true)
}

// materializeSuppressed reports whether ctx carries the suppress-materialize
// marker set by WithSuppressMaterialize.
func materializeSuppressed(ctx context.Context) bool {
	v, _ := ctx.Value(suppressMaterializeKey{}).(bool)
	return v
}

// bakeSharedDefault computes the auto-share default (SPEC-053 D2, policy
// inverted to share-by-default by SPEC-071) for a memory of the given type
// and scope, used when the caller does not explicitly set
// SaveRequest.Shared. Global- and org-scoped memories (personal preferences,
// cross-project config) are never auto-shared regardless of type —
// team-memory only concerns project knowledge. Within project scope, every
// type shares by default except the two excluded by autoSharedType
// (synthesis, session_summary).
func bakeSharedDefault(t model.MemoryType, scope model.Scope) int {
	if scope == model.ScopeGlobal || scope == model.ScopeOrg {
		return 0
	}
	if autoSharedType(t) {
		return 1
	}
	return 0
}

// bakeTeamMemoryFields mutates m in place to resolve its Shared level and
// Author identity before the first persistence of a new memory (SPEC-053
// D2/D7). It is a no-op when team-memory is not active for this process —
// m.Shared/m.Author are left at their zero values, matching Save's behaviour
// before this feature existed (SPEC-061 SS-A's inertness guarantee).
//
// reqShared mirrors SaveRequest.Shared: nil defers to the type-based default;
// a non-nil pointer is an explicit override (opt-out of an auto-shared type,
// or opt-in of one that would otherwise default to local-only).
func (svc *MemoryService) bakeTeamMemoryFields(m *model.Memory, reqShared *int) {
	if !svc.teamMemory.Enabled {
		return
	}

	if reqShared != nil {
		m.Shared = *reqShared
	} else {
		m.Shared = bakeSharedDefault(m.Type, m.Scope)
	}

	if m.Shared > 0 && m.Author == "" {
		m.Author = gitident.Author()
	}
}

// applyTeamMemoryAuthor assigns the local git identity to m.Author when
// team-memory is active, m is marked shared, and it does not already carry an
// author (SPEC-053 D7 "solo si vacío"). Used by Update, which — unlike
// Save/Create — cannot persist a freshly baked Shared value (the store's
// partial-update path intentionally never touches shared/author, see
// SPEC-061 SS-A), so only the already-persisted Shared level is honoured
// here; this only affects the in-memory snapshot handed to materialization,
// not a second bake of Shared itself.
func (svc *MemoryService) applyTeamMemoryAuthor(m *model.Memory) {
	if !svc.teamMemory.Enabled || m.Shared <= 0 {
		return
	}
	if m.Author == "" {
		m.Author = gitident.Author()
	}
}

// sharedTeamCurated is the Shared level Promote assigns (SPEC-053 D2/D8):
// explicit, human-curated sharing, as opposed to 1 (type-based auto-share).
const sharedTeamCurated = 2

// Promote marks the memory identified by id as team-curated (Shared=2,
// SPEC-053 D8) and persists the change directly onto its existing row via
// store.SetTeamMemoryFields — the dedicated write path SS-C adds because
// Update/Upsert intentionally never rewrite shared/author on an existing
// memory (SPEC-061 SS-A, carried forward as the critical design constraint
// documented in SS-B). This makes shared=2 durable in the database itself,
// not just materialized to the vault.
//
// Author is assigned from the local git identity only when the memory does
// not already carry one, matching bakeTeamMemoryFields/applyTeamMemoryAuthor's
// "solo si vacío" rule (SPEC-053 D7) — Promote never overwrites an existing
// author (e.g. one that arrived via SS-D's future vault import).
//
// Promote is idempotent: calling it again on an already-promoted memory
// re-persists shared=2 (harmless) and leaves author untouched.
//
// When team-memory is active for this process, Promote also immediately
// (re)materializes the memory to the shared git vault by reusing
// materializeTeamMemory — the caller does not need to wait for a subsequent
// Save/Update to see the promoted memory appear as a file. When team-memory
// is inactive, shared=2 is still persisted in the database but nothing is
// written to disk, matching Save/Update's existing inert-when-disabled
// behaviour (SPEC-053 D3).
//
// Returns model.ErrNotFound when no active memory exists with that id in
// either store.
func (svc *MemoryService) Promote(ctx context.Context, id string) (*model.Memory, error) {
	m, targetStore, err := svc.getFromEitherStore(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: promote: %w", err)
	}
	if targetStore == nil {
		return nil, fmt.Errorf("service: promote: %w", model.ErrNotFound)
	}

	author := m.Author
	if author == "" {
		author = gitident.Author()
	}

	if err := targetStore.SetTeamMemoryFields(ctx, id, sharedTeamCurated, author); err != nil {
		return nil, fmt.Errorf("service: promote: %w", err)
	}

	updated, err := targetStore.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: promote: reload: %w", err)
	}
	if updated == nil {
		return nil, fmt.Errorf("service: promote: reload: %w", model.ErrNotFound)
	}

	svc.materializeTeamMemory(ctx, updated)

	return updated, nil
}

// materializeTeamMemory writes m to the shared git-native vault when
// team-memory is active, m is marked shared (Shared > 0), and materialization
// has not been suppressed by WithSuppressMaterialize (the SS-D anti-loop
// guard). It is a no-op otherwise.
//
// Best-effort, matching the embedMemory/processWikilinks pattern already
// established in this package: any failure (marker unreadable, disk full,
// permission denied, .mneme/shared/notes not a directory) is logged via slog
// and never propagated — a materialization failure must never fail the
// caller's Save or Update.
func (svc *MemoryService) materializeTeamMemory(ctx context.Context, m *model.Memory) {
	if !svc.teamMemory.Enabled || m == nil || m.Shared <= 0 {
		return
	}
	if materializeSuppressed(ctx) {
		return
	}

	writer := vault.NewWriter(vault.ExportOptions{
		VaultRoot: svc.teamMemory.VaultRoot,
		Project:   svc.project,
		Scope:     "shared",
		PathMode:  vault.PathModeUUID,
	})

	if _, _, err := writer.WriteMemory(m); err != nil {
		slog.ErrorContext(ctx, "team_memory_materialize_error",
			"memory_id", m.ID,
			"shared", m.Shared,
			"error", err,
		)
	}
}
