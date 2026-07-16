package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/vault"
)

// VaultImportOptions parameterises a vault import operation.
type VaultImportOptions struct {
	// Scope controls which store receives the imports: "project" (default)
	// or "global".
	Scope string

	// InputDir is the vault root directory. When empty, the default is
	// derived from cfg.Storage.DataDir, mirroring the vault export default:
	//   project → <DataDir>/vaults/<slug>
	//   global  → <DataDir>/vaults/_global
	InputDir string

	// Strategy controls conflict resolution: "merge" (default) or "overwrite".
	//   merge:     file.updated_at > DB.updated_at → update; else skip.
	//   overwrite: file always wins regardless of timestamps.
	Strategy string

	// DryRun performs full parse + validate + conflict resolution but does not
	// write to the database when true.
	DryRun bool
}

// VaultImportResult summarises a vault import operation.
type VaultImportResult struct {
	// VaultRoot is the absolute path to the vault root that was imported.
	VaultRoot string

	// Total is the number of .md files found under notes/.
	Total int

	// Created is the number of new memories inserted.
	Created int

	// Updated is the number of existing memories updated from files.
	Updated int

	// Skipped is the number of files skipped (DB version is newer or equal).
	Skipped int

	// Errors is the number of files that failed parsing or validation.
	Errors int

	// Paths lists up to 20 file paths that were imported (or would be in
	// dry-run mode). Paths are relative to VaultRoot.
	Paths []string
}

// VaultImport imports memories from a vault directory into the SQLite store.
//
// Fatal errors (abort immediately): vault directory missing, .mneme-vault
// marker absent, project mismatch.
//
// Recoverable errors (skip file, continue): invalid frontmatter, service
// validation failures (e.g. missing applies_to for rules).
//
// The merge strategy (default) uses updated_at timestamps to decide whether
// to update the DB. The overwrite strategy always writes the file's content.
// Both strategies are idempotent: re-running produces the same outcome.
func (svc *MemoryService) VaultImport(ctx context.Context, opts VaultImportOptions) (*VaultImportResult, error) {
	if opts.Scope == "" {
		opts.Scope = "project"
	}
	if opts.Strategy == "" {
		opts.Strategy = "merge"
	}

	dataDir := svc.config.Storage.DataDir
	vaultRoot := vaultImportRoot(opts, svc.project, dataDir)

	result := &VaultImportResult{VaultRoot: vaultRoot}

	// Fatal: vault directory must exist.
	if _, err := os.Stat(vaultRoot); os.IsNotExist(err) {
		return nil, fmt.Errorf("service: vault import: vault directory %q does not exist", vaultRoot)
	}

	// Fatal: .mneme-vault marker must be present and match the project.
	if err := svc.checkImportMarker(vaultRoot, opts.Scope); err != nil {
		return nil, fmt.Errorf("service: vault import: %w", err)
	}

	r := vault.NewReader(vaultRoot)
	notes, parseErrs := r.ReadAll()

	// Count parse errors as per-file errors (recoverable).
	result.Errors += len(parseErrs)
	for _, e := range parseErrs {
		slog.WarnContext(ctx, "vault import: parse error",
			"error", e,
		)
	}

	for _, note := range notes {
		result.Total++

		relPath := relativeToVaultRoot(note.Path, vaultRoot)
		action, err := svc.importNote(ctx, note, opts)
		if err != nil {
			result.Errors++
			slog.WarnContext(ctx, "vault import: skipped",
				"file", relPath,
				"reason", "validation_error",
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

		if action != "skipped" && len(result.Paths) < 20 {
			result.Paths = append(result.Paths, relPath)
		}

		slog.InfoContext(ctx, "vault import",
			"event", action,
			"file", relPath,
		)
	}

	return result, nil
}

// importNote resolves the conflict for a single parsed note and (unless
// dry-run is set) calls service.Save or service.Update.
//
// Returns the action string ("created", "updated", "skipped") or an error
// when the note fails service-level validation.
func (svc *MemoryService) importNote(ctx context.Context, note *vault.ParsedNote, opts VaultImportOptions) (string, error) {
	fm := note.FM
	relPath := relativeToVaultRoot(note.Path, opts.InputDir) // used only for logging below

	// SPEC-089 Part 3 (D4/AC9): the subagent manifest is a registry of THIS
	// machine's materialization (paths, checksums, generated_at) — it must
	// never be imported from a peer's vault, regardless of strategy or
	// whether it already exists locally. Unconditional and first: importing
	// it (even under "merge" when the file happens to be newer) is exactly
	// the clobber SPEC-089 exists to close (novo's manifest overwritten with
	// a teammate's paths). The local manifest, if any, survives untouched.
	if fm.TopicKey == SubagentManifestTopicKey {
		slog.InfoContext(ctx, "vault import",
			"event", "skipped",
			"file", relPath,
			"reason", "subagent_manifest_never_imported",
		)
		return "skipped", nil
	}

	// Case 1: the file carries a valid UUID id.
	if vault.IsValidUUID(fm.ID) {
		existing, _, err := svc.getFromEitherStore(ctx, fm.ID)
		if err != nil {
			return "", fmt.Errorf("vault import: look up %s: %w", fm.ID, err)
		}

		if existing != nil {
			// Memory exists in DB — apply strategy.
			fileTS, tsOK := vault.ParseUpdatedAtFromFM(fm)

			if opts.Strategy == "merge" {
				if !tsOK || !fileTS.After(existing.UpdatedAt) {
					// DB is newer or same — skip.
					slog.InfoContext(ctx, "vault import",
						"event", "conflict",
						"file", relPath,
						"strategy", "merge",
						"winner", "db",
						"file_updated_at", fm.UpdatedAt,
						"db_updated_at", existing.UpdatedAt,
					)
					return "skipped", nil
				}
			}
			// overwrite OR file is newer: update the DB record.
			if opts.DryRun {
				return "updated", nil
			}
			updateReq := fm.ToUpdateRequest(note.Body)
			if _, updateErr := svc.Update(ctx, fm.ID, updateReq); updateErr != nil {
				return "", updateErr
			}
			return "updated", nil
		}

		// ID not found in DB: fall through to create. The file's topic_key (if
		// present) will trigger Upsert dedup in service.Save.
	} else if fm.ID != "" {
		// Non-empty but invalid UUID — warn and treat as new.
		slog.WarnContext(ctx, "vault import: invalid id, treating as new memory",
			"file", relPath,
			"id", fm.ID,
		)
	}

	// Case 2: no id (or invalid id) — create new memory.
	if opts.DryRun {
		return "created", nil
	}

	saveReq := fm.ToSaveRequest(note.Body)
	if _, saveErr := svc.Save(ctx, saveReq); saveErr != nil {
		return "", saveErr
	}
	return "created", nil
}

// checkImportMarker reads the .mneme-vault JSON marker from vaultRoot and
// verifies that it belongs to the expected project. For scope=project the
// marker's project must match svc.project. For scope=global the marker's
// project must be empty or "_global".
//
// Returns a fatal error when the marker is missing or belongs to a different
// project.
func (svc *MemoryService) checkImportMarker(vaultRoot, scope string) error {
	markerPath := filepath.Join(vaultRoot, ".mneme-vault")
	data, err := os.ReadFile(markerPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("marker file .mneme-vault not found in %q — only directories exported by mneme can be imported", vaultRoot)
	}
	if err != nil {
		return fmt.Errorf("read marker: %w", err)
	}

	// Parse via vault.ReadMarkerBytes to avoid duplicating the JSON logic.
	marker, parseErr := vault.ReadMarkerBytes(data)
	if parseErr != nil {
		return fmt.Errorf("parse marker: %w", parseErr)
	}

	if scope == "global" {
		if marker.Project != "" && marker.Project != "_global" {
			return fmt.Errorf("vault at %q has project marker %q but scope=global expects empty or _global", vaultRoot, marker.Project)
		}
		return nil
	}

	// scope=project: marker.Project must match svc.project.
	if marker.Project != "" && svc.project != "" && marker.Project != svc.project {
		return fmt.Errorf("vault at %q belongs to project %q, not %q — use a different --input directory", vaultRoot, marker.Project, svc.project)
	}
	return nil
}

// vaultImportRoot derives the vault root directory for the given import options,
// mirroring the logic in vaultRoot() (vault_export.go:136-154).
func vaultImportRoot(opts VaultImportOptions, project, dataDir string) string {
	if opts.InputDir != "" {
		return opts.InputDir
	}
	switch opts.Scope {
	case "global":
		return filepath.Join(dataDir, "vaults", "_global")
	default:
		slug := project
		if slug == "" {
			slug = "_unnamed"
		}
		return filepath.Join(dataDir, "vaults", slug)
	}
}

// relativeToVaultRoot returns path relative to vaultRoot, or the base name
// if the path is not under vaultRoot.
func relativeToVaultRoot(path, vaultRoot string) string {
	rel, err := filepath.Rel(vaultRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(path)
	}
	return rel
}
