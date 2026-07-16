package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
	"github.com/wirvii/mneme/internal/vault"
)

// TeamMemoryEnableResult summarises a "mneme team-memory enable" run
// (SPEC-053 SS-F / SPEC-065).
type TeamMemoryEnableResult struct {
	// VaultRoot is the absolute path to the shared vault
	// (<repoRoot>/.mneme/shared).
	VaultRoot string

	// AlreadyEnabled is true when the .mneme-vault marker already existed
	// before this call. The command is idempotent either way — callers use
	// this only to choose between an "enabled" and an "already enabled"
	// message.
	AlreadyEnabled bool

	// Baked is the number of pre-existing memories of an auto-shared type
	// (SPEC-071 share-by-default: every project-scoped type except synthesis
	// and session_summary) that were still local-only (shared=0) and were
	// upgraded to shared=1 by this run, so knowledge saved before team-memory
	// existed does not stay hidden from the team.
	Baked int

	// Exported is the number of memories (freshly baked this run, or already
	// shared from a prior run) written to notes/<uuid>.md.
	Exported int
}

// EnableTeamMemory activates the git-native team-memory vault (SPEC-053 D3)
// for the repository rooted at repoRoot:
//
//  1. Ensures <repoRoot>/.mneme/shared/ exists with its .mneme-vault marker
//     (idempotent — an existing marker is left untouched, matching
//     vault.Writer's project-mismatch guard).
//  2. Bakes shared=1 onto every pre-existing auto-shared-type memory that is
//     still local-only (SPEC-071 share-by-default: every project-scoped type
//     except synthesis and session_summary) — otherwise only memories saved
//     AFTER enabling would ever reach the team, leaving already-persistent
//     knowledge stranded.
//  3. Materializes every shared memory (freshly baked or already shared) to
//     notes/<uuid>.md using the same PathModeUUID layout write-through
//     materialization uses (SPEC-062 SS-B).
//  4. Updates the in-process team-memory state so subsequent Save/Update
//     calls in THIS process observe team-memory as active immediately,
//     without needing a process restart to re-run DetectTeamMemory.
//
// It does NOT install the git hooks that import teammates' shared knowledge
// — that is the CLI command's responsibility (it reuses the exact same
// append-marked-block logic "mneme team-memory hooks install" uses), keeping
// this method free of git-hook file I/O and independently testable.
//
// Best-effort per memory: a single memory's bake or export failure is logged
// via slog and does not abort the run, matching materializeTeamMemory's
// philosophy elsewhere in this file — a partially populated vault is still
// strictly better than none.
func (svc *MemoryService) EnableTeamMemory(ctx context.Context, repoRoot string) (*TeamMemoryEnableResult, error) {
	vaultRoot := filepath.Join(repoRoot, ".mneme", sharedVaultRelDir)
	markerPath := filepath.Join(vaultRoot, vault.MarkerFileName)

	result := &TeamMemoryEnableResult{VaultRoot: vaultRoot}
	if _, err := os.Stat(markerPath); err == nil {
		result.AlreadyEnabled = true
	}

	// vault.Writer.WriteMemory/WriteMarker assume the vault root already
	// exists (Export() normally creates it first) — ensure it does, even for
	// a brand-new repo with zero durable memories to export yet.
	if err := os.MkdirAll(vaultRoot, 0o755); err != nil {
		return nil, fmt.Errorf("service: enable team memory: create vault root: %w", err)
	}

	memories, err := svc.projectStore.List(ctx, store.ListOptions{
		Project: svc.project,
		Scope:   model.ScopeProject,
		OrderBy: "importance DESC",
		Limit:   100_000,
	})
	if err != nil {
		return nil, fmt.Errorf("service: enable team memory: list memories: %w", err)
	}

	writer := vault.NewWriter(vault.ExportOptions{
		VaultRoot: vaultRoot,
		Project:   svc.project,
		Scope:     "shared",
		PathMode:  vault.PathModeUUID,
	})

	for _, m := range memories {
		if !autoSharedType(m.Type) {
			continue
		}

		if m.Shared == 0 {
			author := m.Author
			if author == "" {
				author = gitident.Author()
			}
			if bakeErr := svc.projectStore.SetTeamMemoryFields(ctx, m.ID, 1, author); bakeErr != nil {
				slog.ErrorContext(ctx, "team_memory_enable_bake_error", "memory_id", m.ID, "error", bakeErr)
				continue
			}
			m.Shared = 1
			m.Author = author
			result.Baked++
		}

		if _, _, writeErr := writer.WriteMemory(m); writeErr != nil {
			slog.ErrorContext(ctx, "team_memory_enable_export_error", "memory_id", m.ID, "error", writeErr)
			continue
		}
		result.Exported++
	}

	// Always (re)write the marker, even when no durable memory existed yet, so
	// a brand-new project ends up with a valid, importable vault — both
	// checkImportMarker (this repo's future ImportFromShared runs) and a
	// peer's "mneme team-memory hooks install" require it to be present.
	if markerErr := writer.WriteMarker(&vault.ExportResult{Total: len(memories)}); markerErr != nil {
		return nil, fmt.Errorf("service: enable team memory: write marker: %w", markerErr)
	}

	// Make team-memory active for the REST of this process immediately —
	// without this, a Save/Update called later in the same process (a
	// long-lived MCP server session, or a test) would still see
	// teamMemory.enabled == false, since NewMemoryService only resolves it
	// once at construction time.
	svc.teamMemory = TeamMemoryState{Enabled: true, VaultRoot: vaultRoot}

	return result, nil
}
