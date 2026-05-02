package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
	"github.com/juanftp/mneme/internal/vault"
)

// VaultExportOptions parameterises a vault export operation.
type VaultExportOptions struct {
	// Scope controls which store(s) are queried: "project", "global", or "all".
	// Defaults to "project" when empty.
	Scope string

	// OutputDir overrides the vault root directory. When empty the default is
	// derived from cfg.Storage.DataDir:
	//   project → <DataDir>/vaults/<slug>
	//   global  → <DataDir>/vaults/_global
	OutputDir string

	// DryRun performs full analysis but writes nothing when true.
	DryRun bool

	// IncludeSuperseded exports superseded memories when true.
	IncludeSuperseded bool

	// Type filters exported memories to a specific MemoryType. Empty = all types.
	Type model.MemoryType
}

// VaultExportResult summarises a vault export, aggregating results from
// potentially two stores (project + global) when Scope is "all".
type VaultExportResult struct {
	// Project holds the result for the project-scope vault (nil when scope=global).
	Project *vault.ExportResult

	// Global holds the result for the global-scope vault (nil when scope=project).
	Global *vault.ExportResult
}

// VaultExport exports active memories from the appropriate store(s) as
// individual .md files with YAML frontmatter into a vault directory.
//
// Scope values:
//   - "project" (default): queries projectStore → <DataDir>/vaults/<slug>/
//   - "global": queries globalStore → <DataDir>/vaults/_global/
//   - "all": queries both stores; each gets its own vault root.
//
// Soft-deleted memories are never exported. Superseded memories are excluded
// unless opts.IncludeSuperseded is true.
func (svc *MemoryService) VaultExport(ctx context.Context, opts VaultExportOptions) (*VaultExportResult, error) {
	if opts.Scope == "" {
		opts.Scope = "project"
	}

	dataDir := svc.config.Storage.DataDir

	result := &VaultExportResult{}

	if opts.Scope == "project" || opts.Scope == "all" {
		res, err := svc.exportScope(ctx, opts, "project", dataDir)
		if err != nil {
			return nil, err
		}
		result.Project = res
	}

	if opts.Scope == "global" || opts.Scope == "all" {
		res, err := svc.exportScope(ctx, opts, "global", dataDir)
		if err != nil {
			return nil, err
		}
		result.Global = res
	}

	return result, nil
}

// exportScope queries a single store and writes its memories to a vault root.
func (svc *MemoryService) exportScope(ctx context.Context, opts VaultExportOptions, scopeName, dataDir string) (*vault.ExportResult, error) {
	vaultRoot, project := svc.vaultRoot(opts, scopeName, dataDir)

	listOpts := store.ListOptions{
		IncludeSuperseded: opts.IncludeSuperseded,
		OrderBy:           "importance DESC",
		Limit:             100_000, // practical upper bound for single-shot export
	}

	if opts.Type != "" {
		listOpts.Type = opts.Type
	}

	var targetStore interface {
		List(context.Context, store.ListOptions) ([]*model.Memory, error)
	}

	switch scopeName {
	case "global":
		targetStore = svc.globalStore
		listOpts.Scope = model.ScopeGlobal
	default:
		targetStore = svc.projectStore
		listOpts.Project = svc.project
		listOpts.Scope = model.ScopeProject
	}

	memories, err := targetStore.List(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("service: vault export (%s): list memories: %w", scopeName, err)
	}

	w := vault.NewWriter(vault.ExportOptions{
		VaultRoot:         vaultRoot,
		Project:           project,
		Scope:             scopeName,
		DryRun:            opts.DryRun,
		IncludeSuperseded: opts.IncludeSuperseded,
	})

	res, err := w.Export(memories)
	if err != nil {
		return nil, fmt.Errorf("service: vault export (%s): write vault: %w", scopeName, err)
	}

	return res, nil
}

// vaultRoot derives the vault root directory and project slug for a given scope.
// When opts.OutputDir is set it is used directly (for both scopes in "all" mode
// this means they share the same root — callers passing --output should use a
// dedicated directory per scope).
func (svc *MemoryService) vaultRoot(opts VaultExportOptions, scopeName, dataDir string) (root, project string) {
	if opts.OutputDir != "" {
		if scopeName == "global" {
			return filepath.Join(opts.OutputDir, "_global"), ""
		}
		return opts.OutputDir, svc.project
	}

	switch scopeName {
	case "global":
		return filepath.Join(dataDir, "vaults", "_global"), ""
	default:
		slug := svc.project
		if slug == "" {
			slug = "_unnamed"
		}
		return filepath.Join(dataDir, "vaults", slug), slug
	}
}
