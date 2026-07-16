package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/managedblock"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/subagents"
)

// buildStaleAgentProfile writes a synthetic v=1 profile (still instructing
// the agent to call spec_advance — the D4 shape `regen` exists to upgrade)
// with a hand-authored body, to path. Mirrors
// internal/subagents/regenerate_test.go's buildV1Fixture, duplicated here
// (rather than exported) since internal/subagents deliberately exposes no
// test-only helpers across package boundaries.
func buildStaleAgentProfile(t *testing.T, path string, role subagents.Role) {
	t.Helper()
	fm := "---\n" +
		"name: " + string(role) + "\n" +
		"description: \"Old description.\"\n" +
		"model: sonnet\n" +
		"permissionMode: bypassPermissions\n" +
		"tools: Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*\n" +
		"---\n"
	oldAgentFixed := "## Integracion con mneme\n\n5. Avanza el estado: `spec_advance(SPEC-XXX, by: \"" + string(role) + "\")`\n"
	withBlock := managedblock.UpsertText(fm, "agent-fixed", 1, oldAgentFixed)
	content := withBlock + "\n## Área: apps/core-srv\n\nHand-authored during the original grill.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write stale profile: %v", err)
	}
}

// TestRegenerateManifestEntries_UpgradesStaleEntry is AC10's core guard: a
// manifest entry pointing at a v=1 file with a hand-authored body gets
// regenerated in place — body preserved byte-for-byte, Version bumped,
// checksum changed, Changed=true reported.
func TestRegenerateManifestEntries_UpgradesStaleEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backend.md")
	buildStaleAgentProfile(t, path, subagents.RoleBackend)

	oldContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	entries := []service.ManifestEntry{{
		Role: subagents.RoleBackend, Archetype: subagents.RoleBackend,
		Path: path, Version: 1, Checksum: checksumOfSubagentContent(string(oldContent)),
	}}

	results, updated, changedAny, matched := regenerateManifestEntries(entries, "", false, dir)
	if !matched {
		t.Fatal("matched = false, want true")
	}
	if !changedAny {
		t.Fatal("changedAny = false, want true")
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("results = %+v, want one error-free result", results)
	}
	if !results[0].Changed {
		t.Error("Changed = false, want true")
	}
	if results[0].NewVersion != subagents.AgentFixedVersion {
		t.Errorf("NewVersion = %d, want %d", results[0].NewVersion, subagents.AgentFixedVersion)
	}
	if updated[0].Version != subagents.AgentFixedVersion {
		t.Errorf("updated manifest entry Version = %d, want %d", updated[0].Version, subagents.AgentFixedVersion)
	}
	if updated[0].Checksum == entries[0].Checksum {
		t.Error("updated manifest entry Checksum did not change")
	}
	if updated[0].GeneratedAt.IsZero() {
		t.Error("updated manifest entry GeneratedAt was not refreshed")
	}

	// The file on disk must reflect the regenerated content, body preserved.
	newContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read regenerated file: %v", err)
	}
	if !strings.Contains(string(newContent), "Hand-authored during the original grill.") {
		t.Error("hand-authored body not preserved on disk")
	}
	if strings.Contains(string(newContent), "spec_advance(SPEC-XXX") {
		t.Error("regenerated file on disk still contains the old spec_advance instruction")
	}
}

// TestRegenerateManifestEntries_MigratesLegacyAbsolutePath_ZeroBehaviorDiff
// is AC6 (SPEC-089 Part 2's hard restriction): a legacy manifest entry with
// an absolute-IN-REPO Path (the correct, pre-Part-2 shape on the owner's own
// machine) — already at AgentFixedVersion, so no content regeneration is
// needed — must end up, after regen, at EXACTLY the same file in the same
// location as today. The ONLY thing that changes is the PERSISTED Path
// representation (absolute -> root-relative). Diff of on-disk behavior = 0.
func TestRegenerateManifestEntries_MigratesLegacyAbsolutePath_ZeroBehaviorDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "agents", "backend.md")

	current, err := subagents.Compose("", subagents.ComposeInput{
		Role:        subagents.RoleBackend,
		Archetype:   subagents.RoleBackend,
		Description: "Implements server-side logic",
		Model:       "sonnet",
		Body:        "## Área: apps/core-srv\n\nHand-authored, already current.",
	})
	if err != nil {
		t.Fatalf("compose fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(current), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// The legacy shape: Path absolute-in-repo, Checksum already matching the
	// current on-disk content (simulating a manifest entry that was already
	// regenerated once under Part 1 alone, before Part 2's migration existed).
	entries := []service.ManifestEntry{{
		Role: subagents.RoleBackend, Archetype: subagents.RoleBackend,
		Path: path, Version: subagents.AgentFixedVersion,
		Checksum: checksumOfSubagentContent(current),
	}}

	results, updated, changedAny, matched := regenerateManifestEntries(entries, "", false, root)
	if !matched {
		t.Fatal("matched = false, want true")
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("results = %+v, want one error-free result", results)
	}
	if results[0].Changed {
		t.Error("results[0].Changed = true, want false — content was already current, only Path migrates")
	}

	// The hard restriction: the file on disk is byte-for-byte unchanged, at
	// the exact same path.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read file: %v", err)
	}
	if string(after) != string(before) {
		t.Error("file content changed — AC6 requires zero behavior diff for a content-current legacy entry")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("file must still exist at the exact same location: %v", statErr)
	}

	// Only the PERSISTED Path representation changes, to the relative form.
	if updated[0].Path != ".claude/agents/backend.md" {
		t.Errorf("updated[0].Path = %q, want %q (migrated to relative)", updated[0].Path, ".claude/agents/backend.md")
	}
	// changedAny must be true so the caller actually calls SaveManifest and
	// persists the migration — even though content itself did not change.
	if !changedAny {
		t.Error("changedAny = false, want true — a Path-only migration must still trigger a manifest save")
	}
}

// TestRegenerateManifestEntries_DryRunNeverWrites verifies --dry-run leaves
// both the file on disk and the returned manifest entries untouched, while
// still reporting what WOULD change.
func TestRegenerateManifestEntries_DryRunNeverWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backend.md")
	buildStaleAgentProfile(t, path, subagents.RoleBackend)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	entries := []service.ManifestEntry{{
		Role: subagents.RoleBackend, Archetype: subagents.RoleBackend,
		Path: path, Version: 1, Checksum: checksumOfSubagentContent(string(before)),
	}}

	results, updated, changedAny, _ := regenerateManifestEntries(entries, "", true, dir)
	if changedAny {
		t.Error("changedAny = true, want false under --dry-run")
	}
	if len(results) != 1 || !results[0].Changed {
		t.Errorf("results = %+v, want Changed=true reported even under dry-run", results)
	}
	if updated[0].Version != 1 {
		t.Errorf("dry-run must not mutate the returned manifest entry, Version = %d, want 1", updated[0].Version)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after dry-run: %v", err)
	}
	if string(after) != string(before) {
		t.Error("dry-run must never write to disk")
	}
}

// TestRegenerateManifestEntries_RoleFilter verifies passing a non-empty role
// only regenerates the matching entry and leaves the rest untouched.
func TestRegenerateManifestEntries_RoleFilter(t *testing.T) {
	dir := t.TempDir()
	backendPath := filepath.Join(dir, "backend.md")
	frontendPath := filepath.Join(dir, "frontend.md")
	buildStaleAgentProfile(t, backendPath, subagents.RoleBackend)
	buildStaleAgentProfile(t, frontendPath, subagents.RoleFrontend)

	entries := []service.ManifestEntry{
		{Role: subagents.RoleBackend, Archetype: subagents.RoleBackend, Path: backendPath, Version: 1},
		{Role: subagents.RoleFrontend, Archetype: subagents.RoleFrontend, Path: frontendPath, Version: 1},
	}

	results, updated, _, matched := regenerateManifestEntries(entries, "backend", false, dir)
	if !matched {
		t.Fatal("matched = false, want true")
	}
	if len(results) != 1 || results[0].Role != "backend" {
		t.Errorf("results = %+v, want exactly one result for role=backend", results)
	}
	if updated[1].Version != 1 {
		t.Errorf("frontend entry Version = %d, want unchanged 1 (not selected)", updated[1].Version)
	}
}

// TestRegenerateManifestEntries_UnknownRoleNotMatched verifies a role filter
// that matches nothing reports matched=false so the caller can error out.
func TestRegenerateManifestEntries_UnknownRoleNotMatched(t *testing.T) {
	entries := []service.ManifestEntry{{Role: subagents.RoleBackend, Path: "/x.md", Version: 1}}
	_, _, _, matched := regenerateManifestEntries(entries, "nonexistent-role", false, t.TempDir())
	if matched {
		t.Error("matched = true, want false for a role absent from the manifest")
	}
}

// TestRegenerateManifestEntries_RefusesNonMnemeFile verifies a manifest
// entry pointing at a file with no agent-fixed block is reported as an
// error, not silently skipped or overwritten.
func TestRegenerateManifestEntries_RefusesNonMnemeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-profile.md")
	if err := os.WriteFile(path, []byte("# Just prose\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries := []service.ManifestEntry{{Role: subagents.RoleBackend, Archetype: subagents.RoleBackend, Path: path, Version: 1}}
	results, _, changedAny, _ := regenerateManifestEntries(entries, "", false, dir)
	if changedAny {
		t.Error("changedAny = true, want false — the entry errored, nothing to save")
	}
	if len(results) != 1 || results[0].Error == "" {
		t.Errorf("results = %+v, want a non-empty Error for a non-mneme file", results)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != "# Just prose\n" {
		t.Error("a file that Regenerate refused must never be overwritten")
	}
}

// TestRegenerateManifestEntries_NovoFixture_RejectsForeignChateaprov3Path is
// AC1 and mutation guardian 1 (SPEC-089 Part 1): reproduces novo's real
// manifest — 4 legitimate in-repo entries plus a 5th (bug-hunter) whose Path
// points at an absolute location OUTSIDE root, into a REAL sibling tempdir
// standing in for chateaprov3. Before the D2 confinement, `regen --all`
// would have written inside that sibling checkout; this test asserts the
// sibling file is neither created nor modified via os.Stat/os.ReadFile
// against the actual foreign path, not against a local constant — the
// antipattern this spec's design explicitly warns against (mem
// 019f686b).
func TestRegenerateManifestEntries_NovoFixture_RejectsForeignChateaprov3Path(t *testing.T) {
	novoRoot := t.TempDir()
	chateaprov3Root := t.TempDir() // a REAL sibling tempdir, not a mock.

	inRepoRoles := []subagents.Role{subagents.RoleBackend, subagents.RoleFrontend, subagents.RoleArchitect, subagents.RoleQATester}
	entries := make([]service.ManifestEntry, 0, len(inRepoRoles)+1)
	for _, role := range inRepoRoles {
		p := filepath.Join(novoRoot, ".claude", "agents", string(role)+".md")
		buildStaleAgentProfile(t, p, role)
		entries = append(entries, service.ManifestEntry{Role: role, Archetype: role, Path: p, Version: 1})
	}

	// The 5th, corrupted entry: bug-hunter pointing INTO the sibling repo —
	// the exact shape found in novo's real manifest (BL-111).
	foreignPath := filepath.Join(chateaprov3Root, ".claude", "agents", "bug-hunter.md")
	buildStaleAgentProfile(t, foreignPath, subagents.RoleBugHunter)
	foreignBefore, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatalf("read foreign fixture: %v", err)
	}
	entries = append(entries, service.ManifestEntry{
		Role: subagents.RoleBugHunter, Archetype: subagents.RoleBugHunter, Path: foreignPath, Version: 1,
	})

	results, _, _, matched := regenerateManifestEntries(entries, "", false, novoRoot)
	if !matched {
		t.Fatal("matched = false, want true")
	}
	if len(results) != len(entries) {
		t.Fatalf("results = %+v, want %d (one per entry)", results, len(entries))
	}

	last := results[len(results)-1]
	if last.Role != string(subagents.RoleBugHunter) {
		t.Fatalf("last result Role = %q, want bug-hunter", last.Role)
	}
	if last.Error != regenErrForeignPath {
		t.Errorf("last result Error = %q, want %q", last.Error, regenErrForeignPath)
	}
	for i := 0; i < len(inRepoRoles); i++ {
		if results[i].Error != "" {
			t.Errorf("results[%d].Error = %q, want no error for an in-repo entry", i, results[i].Error)
		}
	}

	// AC1's load-bearing assertion: os.Stat/os.ReadFile of the ACTUAL
	// foreign path (the real tempdir sibling), never a mock or constant.
	info, statErr := os.Stat(foreignPath)
	if statErr != nil {
		t.Fatalf("foreign file must still exist on disk: %v", statErr)
	}
	if info.IsDir() {
		t.Fatal("foreign path unexpectedly became a directory")
	}
	foreignAfter, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatalf("re-read foreign file: %v", err)
	}
	if string(foreignAfter) != string(foreignBefore) {
		t.Error("the foreign file in the sibling repo was modified — the confinement guardian failed")
	}
}

// TestRegenerateManifestEntries_VentasWpDropiFixture_RejectsAllWindowsPaths
// is AC2 and mutation guardian 2 (SPEC-089 Part 1): reproduces
// ventasWpDropi's real manifest — every entry stores a Windows path
// ("c:\Users\Usuario\Desktop\...") from a Windows machine that is not this
// one. On a non-Windows GOOS, all four entries must be skipped as foreign
// with zero os.WriteFile calls (asserted indirectly: no file appears
// anywhere under root after the call).
func TestRegenerateManifestEntries_VentasWpDropiFixture_RejectsAllWindowsPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scoped to non-Windows GOOS — the case that materialized in production (SPEC-089)")
	}
	root := t.TempDir()

	roles := []subagents.Role{subagents.RoleBackend, subagents.RoleFrontend, subagents.RoleQATester, subagents.RoleBugHunter}
	entries := make([]service.ManifestEntry, 0, len(roles))
	for _, role := range roles {
		windowsPath := `c:\Users\Usuario\Desktop\ventasWpDropi\.claude\agents\` + string(role) + `.md`
		entries = append(entries, service.ManifestEntry{Role: role, Archetype: role, Path: windowsPath, Version: 1})
	}

	results, updated, changedAny, matched := regenerateManifestEntries(entries, "", false, root)
	if !matched {
		t.Fatal("matched = false, want true")
	}
	if changedAny {
		t.Error("changedAny = true, want false — every entry was rejected as foreign, nothing written")
	}
	if len(results) != len(roles) {
		t.Fatalf("results = %+v, want %d", results, len(roles))
	}
	for i, r := range results {
		if r.Error != regenErrForeignPath {
			t.Errorf("results[%d].Error = %q, want %q", i, r.Error, regenErrForeignPath)
		}
		if updated[i].Version != 1 {
			t.Errorf("updated[%d].Version = %d, want unchanged 1 — a foreign entry must never be regenerated", i, updated[i].Version)
		}
	}

	// No file must have been written anywhere under root.
	entriesOnDisk, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entriesOnDisk) != 0 {
		t.Errorf("root = %v, want empty — no os.WriteFile must have occurred for any foreign entry", entriesOnDisk)
	}
}

// --- CLI wiring --------------------------------------------------------------

// TestSubagentsRegen_RequiresRoleOrAll verifies the CLI rejects a call with
// neither --role nor --all, and one with both.
func TestSubagentsRegen_RequiresRoleOrAll(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := runSubagentsCmd(t, dataDir, "test-regen-flags", "subagents", "regen"); err == nil {
		t.Error("expected an error when neither --role nor --all is given")
	}
	if _, _, err := runSubagentsCmd(t, dataDir, "test-regen-flags",
		"subagents", "regen", "--role", "backend", "--all"); err == nil {
		t.Error("expected an error when both --role and --all are given")
	}
}

// TestSubagentsRegen_NoManifest_RoleNotFound verifies --role against an
// empty manifest reports an error rather than silently doing nothing.
func TestSubagentsRegen_NoManifest_RoleNotFound(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := runSubagentsCmd(t, dataDir, "test-regen-empty",
		"subagents", "regen", "--role", "backend"); err == nil {
		t.Error("expected an error for --role against an empty manifest")
	}
}

// TestSubagentsRegen_EndToEnd exercises compose -> write -> regen --all
// through the real CLI wiring: a freshly written profile is already at
// AgentFixedVersion, so --all must report it unchanged (idempotent, no
// spurious rewrite).
func TestSubagentsRegen_EndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	repoRoot := t.TempDir()
	areasFile := filepath.Join(repoRoot, "areas.md")
	if err := os.WriteFile(areasFile, []byte("## Área: apps/core-srv\n"), 0o644); err != nil {
		t.Fatalf("write areas file: %v", err)
	}

	composedOut := filepath.Join(repoRoot, "composed.md")
	if _, _, err := runSubagentsCmd(t, dataDir, "test-regen-e2e",
		"subagents", "compose", "--role", "backend", "--archetype", "backend",
		"--areas-file", areasFile, "--out", composedOut); err != nil {
		t.Fatalf("compose: %v", err)
	}
	if _, _, err := runSubagentsCmd(t, dataDir, "test-regen-e2e",
		"subagents", "write", "--role", "backend", "--archetype", "backend",
		"--composed-file", composedOut, "--repo-root", repoRoot, "--areas", "apps/core-srv"); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, stderr, err := runSubagentsCmd(t, dataDir, "test-regen-e2e", "subagents", "regen", "--all", "--repo-root", repoRoot)
	if err != nil {
		t.Fatalf("regen --all: %v (stderr=%s)", err, stderr)
	}
	if !bytes.Contains([]byte(stdout), []byte("unchanged")) {
		t.Errorf("expected 'unchanged' for a freshly written (already current) profile, got: %s", stdout)
	}
}
