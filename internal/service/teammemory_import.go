package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/juanftp/mneme/internal/vault"
)

// TeamMemoryImportResult summarises an ImportFromShared run (SPEC-053 SS-D).
type TeamMemoryImportResult struct {
	// VaultRoot is the absolute path to the shared vault that was imported
	// (<repoRoot>/.mneme/shared).
	VaultRoot string

	// Total is the number of .md files found under notes/.
	Total int

	// Created is the number of new memories inserted.
	Created int

	// Updated is the number of existing memories updated from files.
	Updated int

	// Skipped is the number of files skipped (local DB version is newer or
	// equal, per the merge strategy).
	Skipped int

	// Errors is the number of files that failed parsing, validation, or
	// team-memory field preservation.
	Errors int

	// Paths lists up to 20 file paths that were imported. Paths are relative
	// to VaultRoot.
	Paths []string

	// ConflictCandidates is the total number of deterministic FTS5 candidate
	// pairs found across every memory this import created or updated
	// (SPEC-053 D6). Zero means none were detected. This is a count only —
	// no LLM judgment ever runs here; judging remains a separate, manual
	// "mneme conflicts scan" step.
	ConflictCandidates int
}

// ImportFromShared imports memories from the git-native team-memory shared
// vault (<repoRoot>/.mneme/shared/) into the local project store (SPEC-053
// SS-D). repoRoot is the absolute path to the git repository root — the
// caller resolves it (e.g. via "git rev-parse --show-toplevel").
//
// ImportFromShared differs from the general-purpose VaultImport ("mneme
// vault import") in three ways:
//
//   - It is hard-wired to the project store with a merge strategy (by
//     updated_at). Team-memory has no global-scope vault equivalent.
//   - It suppresses write-through materialization for the entire operation
//     (SPEC-053 D5 anti-loop guard): the memories being imported were just
//     read from the shared vault, and re-materializing them during import
//     would write the very files being read right back to disk.
//   - It forces each imported memory's shared/author fields to match its
//     frontmatter exactly (via the SS-C store.SetTeamMemoryFields path),
//     instead of letting Save/Update's own bake logic — which derives shared
//     from the local type-based default and author from the local git
//     identity — win. A peer's local identity must never overwrite the
//     original author recorded when the memory was first materialized
//     (SPEC-053 D5/D7).
//
// After the import completes, ImportFromShared runs the same deterministic
// FTS5 candidate-detection used by "mneme conflicts candidates" against
// every memory it created or updated and returns an aggregate count
// (SPEC-053 D6). Judging a conflict is always a separate, explicit, manual
// step ("mneme conflicts scan") — this method never invokes an LLM.
//
// Fatal errors (abort immediately, matching VaultImport's contract): the
// vault directory is missing, or the .mneme-vault marker is absent or
// belongs to a different project.
//
// Recoverable errors (skip file, continue): invalid frontmatter, a missing
// or invalid id, or a service validation failure.
func (svc *MemoryService) ImportFromShared(ctx context.Context, repoRoot string) (*TeamMemoryImportResult, error) {
	vaultRoot := filepath.Join(repoRoot, ".mneme", sharedVaultRelDir)

	result := &TeamMemoryImportResult{VaultRoot: vaultRoot}

	if _, err := os.Stat(vaultRoot); os.IsNotExist(err) {
		return nil, fmt.Errorf("service: import from shared: vault directory %q does not exist", vaultRoot)
	}

	if err := svc.checkImportMarker(vaultRoot, "project"); err != nil {
		return nil, fmt.Errorf("service: import from shared: %w", err)
	}

	r := vault.NewReader(vaultRoot)
	notes, parseErrs := r.ReadAll()

	result.Errors += len(parseErrs)
	for _, e := range parseErrs {
		slog.WarnContext(ctx, "team_memory_import_parse_error", "error", e)
	}

	// SPEC-053 D5: suppress write-through materialization for every Save/
	// Update call this import makes — replaying memories read from the vault
	// must never write them straight back to it.
	suppressedCtx := WithSuppressMaterialize(ctx)

	var touchedIDs []string
	for _, note := range notes {
		result.Total++
		relPath := relativeToVaultRoot(note.Path, vaultRoot)

		id, action, err := svc.importSharedNote(suppressedCtx, note)
		if err != nil {
			result.Errors++
			slog.WarnContext(ctx, "team_memory_import_skipped",
				"file", relPath,
				"error", err,
			)
			continue
		}

		switch action {
		case "created":
			result.Created++
		case "updated":
			result.Updated++
		case "skipped":
			result.Skipped++
		}

		if action != "skipped" {
			if len(result.Paths) < 20 {
				result.Paths = append(result.Paths, relPath)
			}
			touchedIDs = append(touchedIDs, id)
		}

		slog.InfoContext(ctx, "team_memory_import",
			"event", action,
			"file", relPath,
		)
	}

	result.ConflictCandidates = svc.countConflictCandidates(ctx, touchedIDs)

	return result, nil
}

// importSharedNote resolves the conflict for a single parsed vault note and
// applies it, always finishing by forcing the memory's shared/author
// columns to match the frontmatter exactly, overriding whatever the
// team-memory bake logic would otherwise compute (SPEC-053 D5/D7).
//
// Critically, a note not yet present locally is created via
// store.CreateWithID, preserving fm.ID rather than letting the store assign
// a fresh UUIDv7. This is required by the vault's one-file-per-UUID design
// (SPEC-053 D1): every peer must converge on the SAME id for the same piece
// of shared knowledge, both so concurrent edits merge correctly at the git
// level and so a later re-import of the same note is recognized by id
// alone — without depending on an optional topic_key. Before this fix,
// Save's fresh-id Create meant a note without a topic_key would duplicate
// on every subsequent post-merge/post-checkout run.
//
// Returns the id of the affected memory, the action taken ("created",
// "updated", or "skipped"), and any error. A note without a syntactically
// valid UUID id is treated as a recoverable error — every note materialized
// by mneme's write-through path carries its own memory id, so a missing or
// invalid one signals a hand-edited or corrupted file.
func (svc *MemoryService) importSharedNote(ctx context.Context, note *vault.ParsedNote) (id, action string, err error) {
	fm := note.FM

	if !vault.IsValidUUID(fm.ID) {
		return "", "", fmt.Errorf("note %q has no valid id, skipping", note.Path)
	}

	existing, targetStore, err := svc.getFromEitherStore(ctx, fm.ID)
	if err != nil {
		return "", "", fmt.Errorf("look up %s: %w", fm.ID, err)
	}

	if existing != nil {
		fileTS, tsOK := vault.ParseUpdatedAtFromFM(fm)
		if !tsOK || !fileTS.After(existing.UpdatedAt) {
			// DB is newer or same — skip, matching VaultImport's merge semantics.
			return fm.ID, "skipped", nil
		}

		updateReq := fm.ToUpdateRequest(note.Body)
		if _, updErr := svc.Update(ctx, fm.ID, updateReq); updErr != nil {
			return "", "", updErr
		}
		if setErr := targetStore.SetTeamMemoryFields(ctx, fm.ID, fm.Shared, fm.Author); setErr != nil {
			return "", "", fmt.Errorf("preserve shared/author for %s: %w", fm.ID, setErr)
		}
		return fm.ID, "updated", nil
	}

	// Not found locally by id — create it, preserving fm.ID (see doc comment
	// above). validateAndBuildMemory applies the exact same validation and
	// defaulting rules service.Save uses, so imported notes are held to the
	// same bar (required title/content, valid type/scope, rule invariants).
	saveReq := fm.ToSaveRequest(note.Body)
	m, buildErr := svc.validateAndBuildMemory(&saveReq)
	if buildErr != nil {
		return "", "", buildErr
	}
	m.ID = fm.ID

	newStore := svc.storeFor(m.Scope)
	created, createErr := newStore.CreateWithID(ctx, m)
	if createErr != nil {
		return "", "", fmt.Errorf("create with id %s: %w", fm.ID, createErr)
	}

	if setErr := newStore.SetTeamMemoryFields(ctx, created.ID, fm.Shared, fm.Author); setErr != nil {
		return "", "", fmt.Errorf("preserve shared/author for %s: %w", created.ID, setErr)
	}

	// Mirror Save's post-persist best-effort steps (embedding, wikilinks,
	// deferred-link resolution). Materialization and the async conflict-hint
	// goroutine are intentionally skipped here: materialization must never
	// fire during import (the D5 anti-loop guard), and ImportFromShared runs
	// its own batched conflict-candidate pass after every note (D6).
	svc.embedMemory(ctx, newStore, created)
	svc.processWikilinks(ctx, created, newStore)
	svc.autoResolveUnresolved(ctx, created, newStore)

	return created.ID, "created", nil
}

// countConflictCandidates runs the deterministic FTS5 conflict-candidate
// detection (SPEC-039, reused per SPEC-053 D6) against every id in ids and
// returns the total number of candidate pairs found. Best-effort: a failure
// looking up one memory is logged and does not stop the count for the rest.
// This never invokes LLM judgment — it only counts candidates, exactly as
// "mneme conflicts candidates" does for a single memory.
func (svc *MemoryService) countConflictCandidates(ctx context.Context, ids []string) int {
	total := 0
	for _, id := range ids {
		candidates, err := svc.ConflictCandidates(ctx, id, 5)
		if err != nil {
			slog.WarnContext(ctx, "team_memory_import_conflict_check_error",
				"memory_id", id,
				"error", err,
			)
			continue
		}
		total += len(candidates)
	}
	return total
}
