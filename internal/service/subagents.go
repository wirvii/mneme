package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/subagents"
)

// Topic keys for the two typed-memory records the agnostic-agents EPIC
// persists (SPEC-052 D4 / SPEC-056 §6). Both are upserted idempotently by
// topic_key — no dedicated migration/table is introduced.
const (
	// ProjectProfileTopicKey identifies the single project-profile memory
	// (type=architecture, scope=project) holding the repo/org knowledge
	// elicited once during the subagents grill plus the app->role mapping.
	ProjectProfileTopicKey = "project-profile/agnostic-agents"

	// SubagentManifestTopicKey identifies the single manifest memory
	// (type=config, scope=project) holding the list of generated subagent
	// profile files and their metadata.
	SubagentManifestTopicKey = "subagents/manifest"

	// selfHealingImportance is the importance value SaveProfile/SaveManifest
	// always pass explicitly to MemoryService.Save. Because Upsert resets the
	// stored importance column on every write (store.MemoryStore.Upsert),
	// re-running the grill (or any caller that re-saves the same topic_key)
	// self-heals the importance back to this high value, counteracting decay
	// (SPEC-052 D4 risk note, SPEC-056 §6).
	selfHealingImportance = 0.9
)

// ProjectProfile is the repo/org-level knowledge elicited once during the
// subagents grill (SPEC-052 §3.1/§4 phase 1-2). It is persisted as JSON
// content on a type=architecture memory keyed by ProjectProfileTopicKey.
type ProjectProfile struct {
	// SchemaVersion allows the stored JSON shape to evolve without a
	// migration: readers can branch on this field when the struct changes.
	SchemaVersion int `json:"schema_version"`

	// Repo captures repo-wide facts elicited once (commit convention,
	// dominant language, directory layout, cross-cutting rules).
	Repo ProjectProfileRepo `json:"repo"`

	// Org is a free-form organisation/team name, when elicited.
	Org string `json:"org,omitempty"`

	// Mapping lists which role owns which app/package (grill phase 2),
	// e.g. {"apps/core-srv", "backend"}.
	Mapping []ProjectProfileMapping `json:"mapping,omitempty"`
}

// ProjectProfileRepo holds the repo-wide facts elicited once during grill
// phase 1 (SPEC-052 §3.1).
type ProjectProfileRepo struct {
	// Commits describes the commit message convention (e.g. "Conventional Commits").
	Commits string `json:"commits,omitempty"`

	// Lang is the dominant language/stack summary (e.g. "Go 1.25 + sqlc").
	Lang string `json:"lang,omitempty"`

	// Layout describes the directory layout convention (e.g. "modular monolith, apps/*").
	Layout string `json:"layout,omitempty"`

	// CrossRules lists cross-cutting rules that apply to every role
	// (e.g. "no Claude signatures in git history").
	CrossRules []string `json:"cross_rules,omitempty"`
}

// ProjectProfileMapping links one app/package path to the subagent Role
// responsible for it, as elicited/confirmed during grill phase 2.
type ProjectProfileMapping struct {
	// App is the app/package path, e.g. "apps/core-srv".
	App string `json:"app"`

	// Role is the subagent role responsible for App.
	Role subagents.Role `json:"role"`
}

// ManifestEntry describes one generated subagent profile file (SPEC-052 §6).
// The manifest is the list of all currently-generated profiles for a project,
// persisted as a JSON array on a type=config memory keyed by
// SubagentManifestTopicKey.
type ManifestEntry struct {
	// Role is the subagent role this entry describes.
	Role subagents.Role `json:"role"`

	// Path is the absolute filesystem path of the generated profile
	// (e.g. "/repo/.claude/agents/backend.md").
	Path string `json:"path"`

	// Version is the agent-fixed managed-block version stamped into the
	// profile at generation time (see subagents.Compose).
	Version int `json:"version"`

	// Checksum is a content hash (e.g. sha256 hex) of the generated file,
	// used by callers to detect drift without re-reading the full file.
	Checksum string `json:"checksum"`

	// Areas lists the app/package paths this profile's role/area sections
	// cover (subset or all of ProjectProfile.Mapping for this Role).
	Areas []string `json:"areas,omitempty"`

	// Engine identifies the GenerationEngine used to draft capa-2/3 content
	// (e.g. "passthrough", "cli-claude", "cli-codex").
	Engine string `json:"engine,omitempty"`

	// GeneratedAt is when this profile was last (re)generated.
	GeneratedAt time.Time `json:"generated_at"`

	// EnforcementHook reports whether the delegation-enforcement hook is
	// enabled for this project (SPEC-052 D9).
	EnforcementHook bool `json:"enforcement_hook"`
}

// SubagentService orchestrates persistence for the agnostic-agents EPIC
// (SPEC-052) on top of the internal/subagents leaf. It never duplicates
// subagents' pure composition/validation/fingerprinting logic — it only
// reads/writes the project-profile and manifest typed-memory records (SPEC-052
// D4) and performs the atomic, rollback-capable filesystem write of composed
// .claude/agents/<role>.md profiles.
//
// SubagentService deliberately holds a *MemoryService rather than duplicating
// store access: all memory persistence goes through the same
// validation/upsert/embedding pipeline every other memory type uses.
type SubagentService struct {
	mem *MemoryService
}

// NewSubagentService constructs a SubagentService backed by mem. mem must be
// fully initialised (see NewMemoryService).
func NewSubagentService(mem *MemoryService) *SubagentService {
	return &SubagentService{mem: mem}
}

// ReadProfile loads the ProjectProfile for project (defaulting to the
// service's configured project slug when project is empty). Returns (nil, nil)
// when no project-profile memory exists yet — callers should treat this as
// "grill has not run yet", not as an error.
func (s *SubagentService) ReadProfile(ctx context.Context, project string) (*ProjectProfile, error) {
	if project == "" {
		project = s.mem.ProjectSlug()
	}

	m, err := s.mem.projectStore.GetByTopicKey(ctx, ProjectProfileTopicKey, project)
	if err != nil {
		return nil, fmt.Errorf("service: subagents: read profile: %w", err)
	}
	if m == nil {
		return nil, nil
	}

	var profile ProjectProfile
	if err := json.Unmarshal([]byte(m.Content), &profile); err != nil {
		return nil, fmt.Errorf("service: subagents: read profile: unmarshal: %w", err)
	}
	return &profile, nil
}

// SaveProfile upserts the ProjectProfile for project as a type=architecture,
// scope=project memory keyed by ProjectProfileTopicKey. Calling SaveProfile
// again with the same project updates the existing record (idempotent
// upsert) rather than creating a duplicate.
func (s *SubagentService) SaveProfile(ctx context.Context, project string, profile ProjectProfile) (*model.SaveResponse, error) {
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("service: subagents: save profile: marshal: %w", err)
	}

	importance := selfHealingImportance
	resp, err := s.mem.Save(ctx, model.SaveRequest{
		Title:      "Project profile — agnostic-agents",
		Content:    string(data),
		Type:       model.TypeArchitecture,
		Scope:      model.ScopeProject,
		TopicKey:   ProjectProfileTopicKey,
		Project:    project,
		Importance: &importance,
	})
	if err != nil {
		return nil, fmt.Errorf("service: subagents: save profile: %w", err)
	}
	return resp, nil
}

// ReadManifest loads the manifest entries for project (defaulting to the
// service's configured project slug when project is empty). Returns (nil, nil)
// when no manifest memory exists yet — callers should treat this as "no
// subagents generated yet", not as an error.
func (s *SubagentService) ReadManifest(ctx context.Context, project string) ([]ManifestEntry, error) {
	if project == "" {
		project = s.mem.ProjectSlug()
	}

	m, err := s.mem.projectStore.GetByTopicKey(ctx, SubagentManifestTopicKey, project)
	if err != nil {
		return nil, fmt.Errorf("service: subagents: read manifest: %w", err)
	}
	if m == nil {
		return nil, nil
	}

	var entries []ManifestEntry
	if err := json.Unmarshal([]byte(m.Content), &entries); err != nil {
		return nil, fmt.Errorf("service: subagents: read manifest: unmarshal: %w", err)
	}
	return entries, nil
}

// SaveManifest upserts the manifest for project as a type=config,
// scope=project memory keyed by SubagentManifestTopicKey. Calling
// SaveManifest again with the same project replaces the existing record's
// content wholesale (the caller is responsible for computing the full desired
// entry list — SaveManifest performs no merge).
func (s *SubagentService) SaveManifest(ctx context.Context, project string, entries []ManifestEntry) (*model.SaveResponse, error) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("service: subagents: save manifest: marshal: %w", err)
	}

	importance := selfHealingImportance
	resp, err := s.mem.Save(ctx, model.SaveRequest{
		Title:      "Subagent manifest",
		Content:    string(data),
		Type:       model.TypeConfig,
		Scope:      model.ScopeProject,
		TopicKey:   SubagentManifestTopicKey,
		Project:    project,
		Importance: &importance,
	})
	if err != nil {
		return nil, fmt.Errorf("service: subagents: save manifest: %w", err)
	}
	return resp, nil
}

// WriteAgentFile is a single composed subagent profile destined for the
// filesystem (typically ".claude/agents/<role>.md").
type WriteAgentFile struct {
	// Role is the subagent role this file represents.
	Role subagents.Role

	// Path is the absolute destination path.
	Path string

	// Content is the full composed profile content (see subagents.Compose).
	Content string
}

// WriteAgentsResult summarises a successful WriteAgentProfiles call.
type WriteAgentsResult struct {
	// Written lists the absolute paths written, in the order given.
	Written []string
}

// agentFileBackup records the pre-write state of a single path so
// WriteAgentProfiles can restore it if a later file in the same batch fails
// to write.
type agentFileBackup struct {
	path     string
	existed  bool
	original []byte
}

// WriteAgentProfiles writes each file in files to disk. The write is atomic
// across the whole batch: if any file fails to write (mkdir failure, disk
// full, invalid path, etc.), every file already written earlier in this call
// is rolled back to its exact pre-call state — restored to its original
// content if it existed, or removed if it did not — leaving the filesystem as
// if WriteAgentProfiles had never been called. This mirrors the gentle-ai
// installer pattern referenced in SPEC-052 D5/SPEC-056: write, collect
// written, rollback() on partial failure.
//
// WriteAgentProfiles does not validate content (see subagents.Validate for
// that) and does not touch the manifest — callers save the manifest
// separately via SaveManifest once the write succeeds.
func (s *SubagentService) WriteAgentProfiles(files []WriteAgentFile) (*WriteAgentsResult, error) {
	var backups []agentFileBackup

	rollback := func() {
		for i := len(backups) - 1; i >= 0; i-- {
			b := backups[i]
			if b.existed {
				_ = os.WriteFile(b.path, b.original, 0o644)
			} else {
				_ = os.Remove(b.path)
			}
		}
	}

	written := make([]string, 0, len(files))
	for _, f := range files {
		original, readErr := os.ReadFile(f.Path)
		existed := readErr == nil
		if readErr != nil && !os.IsNotExist(readErr) {
			rollback()
			return nil, fmt.Errorf("service: subagents: write profiles: read existing %s: %w", f.Path, readErr)
		}

		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			rollback()
			return nil, fmt.Errorf("service: subagents: write profiles: mkdir %s: %w", filepath.Dir(f.Path), err)
		}

		if err := os.WriteFile(f.Path, []byte(f.Content), 0o644); err != nil {
			rollback()
			return nil, fmt.Errorf("service: subagents: write profiles: write %s: %w", f.Path, err)
		}

		backups = append(backups, agentFileBackup{path: f.Path, existed: existed, original: original})
		written = append(written, f.Path)
	}

	return &WriteAgentsResult{Written: written}, nil
}
