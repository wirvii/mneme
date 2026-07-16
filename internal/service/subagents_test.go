package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/subagents"
)

func newSubagentTestService(t *testing.T) *service.SubagentService {
	t.Helper()
	mem := newTestService(t)
	return service.NewSubagentService(mem)
}

func TestReadProfile_NotFoundReturnsNil(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)

	profile, err := svc.ReadProfile(context.Background(), "test/project")
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if profile != nil {
		t.Fatalf("ReadProfile: want nil for missing profile, got %+v", profile)
	}
}

func TestReadManifest_NotFoundReturnsNil(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)

	entries, err := svc.ReadManifest(context.Background(), "test/project")
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if entries != nil {
		t.Fatalf("ReadManifest: want nil for missing manifest, got %+v", entries)
	}
}

func TestSaveProfile_RoundTrip(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	ctx := context.Background()

	profile := service.ProjectProfile{
		SchemaVersion: 1,
		Repo: service.ProjectProfileRepo{
			Commits:    "Conventional Commits",
			Lang:       "Go 1.25 + sqlc",
			Layout:     "modular monolith",
			CrossRules: []string{"no Claude signatures in git history"},
		},
		Org: "wirvii",
		Mapping: []service.ProjectProfileMapping{
			{App: "apps/core-srv", Role: subagents.RoleBackend},
			{App: "apps/web-ui", Role: subagents.RoleFrontend},
		},
	}

	resp, err := svc.SaveProfile(ctx, "test/project", profile)
	if err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if resp.Action != "created" {
		t.Fatalf("SaveProfile: want action=created, got %q", resp.Action)
	}
	if resp.TopicKey != service.ProjectProfileTopicKey {
		t.Fatalf("SaveProfile: want topic_key=%q, got %q", service.ProjectProfileTopicKey, resp.TopicKey)
	}

	got, err := svc.ReadProfile(ctx, "test/project")
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if got == nil {
		t.Fatal("ReadProfile: want non-nil profile after save")
	}
	if got.SchemaVersion != profile.SchemaVersion || got.Org != profile.Org {
		t.Fatalf("ReadProfile: round-trip mismatch: got %+v, want %+v", got, profile)
	}
	if got.Repo.Commits != profile.Repo.Commits || got.Repo.Lang != profile.Repo.Lang || got.Repo.Layout != profile.Repo.Layout {
		t.Fatalf("ReadProfile: repo round-trip mismatch: got %+v, want %+v", got.Repo, profile.Repo)
	}
	if len(got.Repo.CrossRules) != len(profile.Repo.CrossRules) {
		t.Fatalf("ReadProfile: repo.cross_rules length mismatch: got %v, want %v", got.Repo.CrossRules, profile.Repo.CrossRules)
	}
	for i, r := range profile.Repo.CrossRules {
		if got.Repo.CrossRules[i] != r {
			t.Fatalf("ReadProfile: repo.cross_rules[%d] mismatch: got %q, want %q", i, got.Repo.CrossRules[i], r)
		}
	}
	if len(got.Mapping) != len(profile.Mapping) {
		t.Fatalf("ReadProfile: mapping length mismatch: got %d, want %d", len(got.Mapping), len(profile.Mapping))
	}
	for i, m := range profile.Mapping {
		if got.Mapping[i] != m {
			t.Fatalf("ReadProfile: mapping[%d] mismatch: got %+v, want %+v", i, got.Mapping[i], m)
		}
	}
}

func TestSaveProfile_IdempotentUpsertByTopicKey(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	ctx := context.Background()

	first := service.ProjectProfile{SchemaVersion: 1, Org: "wirvii"}
	resp1, err := svc.SaveProfile(ctx, "test/project", first)
	if err != nil {
		t.Fatalf("SaveProfile (1st): %v", err)
	}
	if resp1.Action != "created" {
		t.Fatalf("SaveProfile (1st): want action=created, got %q", resp1.Action)
	}

	second := service.ProjectProfile{SchemaVersion: 1, Org: "wirvii-renamed"}
	resp2, err := svc.SaveProfile(ctx, "test/project", second)
	if err != nil {
		t.Fatalf("SaveProfile (2nd): %v", err)
	}
	if resp2.Action != "updated" {
		t.Fatalf("SaveProfile (2nd): want action=updated, got %q", resp2.Action)
	}
	if resp2.ID != resp1.ID {
		t.Fatalf("SaveProfile: want same memory ID across upserts, got %q then %q", resp1.ID, resp2.ID)
	}

	got, err := svc.ReadProfile(ctx, "test/project")
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if got.Org != "wirvii-renamed" {
		t.Fatalf("ReadProfile: want updated content org=%q, got %q", "wirvii-renamed", got.Org)
	}
}

func TestSaveManifest_RoundTrip(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	entries := []service.ManifestEntry{
		{
			Role:            subagents.RoleBackend,
			Path:            "/repo/.claude/agents/backend.md",
			Version:         1,
			Checksum:        "abc123",
			Areas:           []string{"apps/core-srv"},
			Engine:          "passthrough",
			GeneratedAt:     now,
			EnforcementHook: true,
		},
		{
			Role:        subagents.RoleFrontend,
			Path:        "/repo/.claude/agents/frontend.md",
			Version:     1,
			Checksum:    "def456",
			GeneratedAt: now,
		},
	}

	resp, err := svc.SaveManifest(ctx, "test/project", entries)
	if err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if resp.Action != "created" {
		t.Fatalf("SaveManifest: want action=created, got %q", resp.Action)
	}
	if resp.TopicKey != service.SubagentManifestTopicKey {
		t.Fatalf("SaveManifest: want topic_key=%q, got %q", service.SubagentManifestTopicKey, resp.TopicKey)
	}

	got, err := svc.ReadManifest(ctx, "test/project")
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("ReadManifest: want %d entries, got %d", len(entries), len(got))
	}
	for i, e := range entries {
		if got[i].Role != e.Role || got[i].Path != e.Path || got[i].Checksum != e.Checksum ||
			got[i].Version != e.Version || got[i].Engine != e.Engine || got[i].EnforcementHook != e.EnforcementHook {
			t.Fatalf("ReadManifest: entry[%d] mismatch: got %+v, want %+v", i, got[i], e)
		}
		if !got[i].GeneratedAt.Equal(e.GeneratedAt) {
			t.Fatalf("ReadManifest: entry[%d] GeneratedAt mismatch: got %v, want %v", i, got[i].GeneratedAt, e.GeneratedAt)
		}
	}
}

func TestSaveManifest_IdempotentUpsertByTopicKey(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	ctx := context.Background()

	resp1, err := svc.SaveManifest(ctx, "test/project", []service.ManifestEntry{{Role: subagents.RoleBackend, Path: "a"}})
	if err != nil {
		t.Fatalf("SaveManifest (1st): %v", err)
	}

	resp2, err := svc.SaveManifest(ctx, "test/project", []service.ManifestEntry{
		{Role: subagents.RoleBackend, Path: "a"},
		{Role: subagents.RoleFrontend, Path: "b"},
	})
	if err != nil {
		t.Fatalf("SaveManifest (2nd): %v", err)
	}
	if resp2.ID != resp1.ID {
		t.Fatalf("SaveManifest: want same memory ID across upserts, got %q then %q", resp1.ID, resp2.ID)
	}
	if resp2.Action != "updated" {
		t.Fatalf("SaveManifest (2nd): want action=updated, got %q", resp2.Action)
	}

	got, err := svc.ReadManifest(ctx, "test/project")
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ReadManifest: want 2 entries after replace, got %d", len(got))
	}
}

func TestWriteAgentProfiles_WritesAllFiles(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	dir := t.TempDir()

	files := []service.WriteAgentFile{
		{Role: subagents.RoleBackend, Path: filepath.Join(dir, "backend.md"), Content: "backend content"},
		{Role: subagents.RoleFrontend, Path: filepath.Join(dir, "frontend.md"), Content: "frontend content"},
	}

	result, err := svc.WriteAgentProfiles(files)
	if err != nil {
		t.Fatalf("WriteAgentProfiles: %v", err)
	}
	if len(result.Written) != 2 {
		t.Fatalf("WriteAgentProfiles: want 2 written paths, got %d", len(result.Written))
	}

	for _, f := range files {
		got, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		if string(got) != f.Content {
			t.Fatalf("content mismatch for %s: got %q, want %q", f.Path, got, f.Content)
		}
	}
}

func TestWriteAgentProfiles_MkdirSubdirectories(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	dir := t.TempDir()

	target := filepath.Join(dir, "nested", "deep", "backend.md")
	files := []service.WriteAgentFile{{Role: subagents.RoleBackend, Path: target, Content: "x"}}

	if _, err := svc.WriteAgentProfiles(files); err != nil {
		t.Fatalf("WriteAgentProfiles: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected %s to exist: %v", target, err)
	}
}

// TestWriteAgentProfiles_RollbackOnPartialFailure_NewFiles verifies that when
// a later file in the batch fails to write, an earlier file that did NOT
// exist before the call is removed by rollback — leaving the filesystem as if
// WriteAgentProfiles had never been called.
func TestWriteAgentProfiles_RollbackOnPartialFailure_NewFiles(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	dir := t.TempDir()

	firstPath := filepath.Join(dir, "backend.md")
	// A path with an embedded NUL byte is rejected by the OS at write time,
	// deterministically forcing a failure regardless of platform or
	// filesystem permissions.
	invalidPath := filepath.Join(dir, "bad\x00path", "frontend.md")

	files := []service.WriteAgentFile{
		{Role: subagents.RoleBackend, Path: firstPath, Content: "new backend content"},
		{Role: subagents.RoleFrontend, Path: invalidPath, Content: "frontend content"},
	}

	_, err := svc.WriteAgentProfiles(files)
	if err == nil {
		t.Fatal("WriteAgentProfiles: want error for invalid second path, got nil")
	}

	if _, statErr := os.Stat(firstPath); !os.IsNotExist(statErr) {
		t.Fatalf("rollback: want %s removed (did not exist before call), stat err=%v", firstPath, statErr)
	}
}

// TestWriteAgentProfiles_RollbackRestoresOriginalContent verifies that when a
// later file in the batch fails, an earlier file that DID exist before the
// call is restored to its exact original content, not left with the new
// (partially-applied) content.
func TestWriteAgentProfiles_RollbackRestoresOriginalContent(t *testing.T) {
	t.Parallel()
	svc := newSubagentTestService(t)
	dir := t.TempDir()

	firstPath := filepath.Join(dir, "backend.md")
	const original = "original backend content"
	if err := os.WriteFile(firstPath, []byte(original), 0o644); err != nil {
		t.Fatalf("seed original file: %v", err)
	}

	invalidPath := filepath.Join(dir, "bad\x00path", "frontend.md")

	files := []service.WriteAgentFile{
		{Role: subagents.RoleBackend, Path: firstPath, Content: "new backend content"},
		{Role: subagents.RoleFrontend, Path: invalidPath, Content: "frontend content"},
	}

	_, err := svc.WriteAgentProfiles(files)
	if err == nil {
		t.Fatal("WriteAgentProfiles: want error for invalid second path, got nil")
	}

	got, readErr := os.ReadFile(firstPath)
	if readErr != nil {
		t.Fatalf("read %s after rollback: %v", firstPath, readErr)
	}
	if string(got) != original {
		t.Fatalf("rollback: want original content %q restored, got %q", original, got)
	}
}

// --- SPEC-086 D4: Archetype/AreasComplete on ManifestEntry ------------------

// TestManifestEntry_IsImplementer_UsesArchetypeOverRole is the mutation-tested
// reproduction of the D4 bug: a custom role (role != archetype, e.g.
// subagent_compose(role:"qa-tester", archetype:"bug-hunter")) must be
// recognised as an implementer via its Archetype, even though "qa-tester" on
// its own is a read-only archetype in subagents.PermissionTable. Deleting the
// EffectiveArchetype indirection (falling back to comparing entry.Role
// directly) turns this red — that IS the bug this field fixes.
func TestManifestEntry_IsImplementer_UsesArchetypeOverRole(t *testing.T) {
	entry := service.ManifestEntry{Role: "qa-tester", Archetype: subagents.RoleBugHunter}

	if !entry.IsImplementer() {
		t.Fatal("IsImplementer() = false, want true — custom role's Archetype (bug-hunter) is an implementer archetype")
	}
	if entry.EffectiveArchetype() != subagents.RoleBugHunter {
		t.Errorf("EffectiveArchetype() = %q, want %q", entry.EffectiveArchetype(), subagents.RoleBugHunter)
	}
}

// TestManifestEntry_IsImplementer_FallsBackToRole covers the compat path: an
// entry with no Archetype (every manifest written before this field existed)
// falls back to Role, so an old "backend"-role entry is still recognised as
// an implementer with zero migration.
func TestManifestEntry_IsImplementer_FallsBackToRole(t *testing.T) {
	entry := service.ManifestEntry{Role: subagents.RoleBackend}

	if !entry.IsImplementer() {
		t.Fatal("IsImplementer() = false, want true — Role=backend with no Archetype must fall back to Role")
	}
	if entry.EffectiveArchetype() != subagents.RoleBackend {
		t.Errorf("EffectiveArchetype() = %q, want %q", entry.EffectiveArchetype(), subagents.RoleBackend)
	}
}

// TestManifestEntry_IsImplementer_ReadOnlyArchetypeNeverImplementer verifies
// the negative case: a genuinely read-only archetype (architect) is never an
// implementer, regardless of what Role says.
func TestManifestEntry_IsImplementer_ReadOnlyArchetypeNeverImplementer(t *testing.T) {
	entry := service.ManifestEntry{Role: "architect", Archetype: subagents.RoleArchitect}

	if entry.IsImplementer() {
		t.Fatal("IsImplementer() = true, want false — architect archetype is read-only")
	}
}
