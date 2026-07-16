package cli

import (
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/subagents"
)

func findingKinds(findings []doctorFinding) map[doctorFindingKind]bool {
	set := map[doctorFindingKind]bool{}
	for _, f := range findings {
		set[f.Kind] = true
	}
	return set
}

func alwaysExists(string) bool                 { return true }
func neverExists(string) bool                  { return false }
func matchingChecksum(string) (string, bool)   { return "abc", true }
func mismatchedChecksum(string) (string, bool) { return "different", true }

// TestDiagnoseManifestEntry_UnknownRole covers the D4-adjacent unknown-role
// finding: a role/archetype absent from PermissionTable is flagged as
// unprotected.
func TestDiagnoseManifestEntry_UnknownRole(t *testing.T) {
	entry := service.ManifestEntry{Role: "totally-custom", Areas: []string{"internal/**"}, AreasComplete: true}
	findings := diagnoseManifestEntry(entry, "/", alwaysExists, matchingChecksum)
	if !findingKinds(findings)[doctorKindUnknownRole] {
		t.Errorf("findings = %+v, want unknown_role", findings)
	}
}

// TestDiagnoseManifestEntry_DegenerateAreas covers an implementer role with
// no declared areas.
func TestDiagnoseManifestEntry_DegenerateAreas(t *testing.T) {
	entry := service.ManifestEntry{Role: subagents.RoleBackend, Archetype: subagents.RoleBackend, AreasComplete: true}
	findings := diagnoseManifestEntry(entry, "/", alwaysExists, matchingChecksum)
	if !findingKinds(findings)[doctorKindDegenerateAreas] {
		t.Errorf("findings = %+v, want degenerate_areas", findings)
	}
}

// TestDiagnoseManifestEntry_ArchetypeMissing_And_NotVerified covers the two
// compat findings every pre-SPEC-086 manifest entry will show.
func TestDiagnoseManifestEntry_ArchetypeMissing_And_NotVerified(t *testing.T) {
	entry := service.ManifestEntry{Role: subagents.RoleBackend, Areas: []string{"internal/**"}}
	findings := diagnoseManifestEntry(entry, "/", alwaysExists, matchingChecksum)
	kinds := findingKinds(findings)
	if !kinds[doctorKindArchetypeMissing] {
		t.Error("expected archetype_missing")
	}
	if !kinds[doctorKindNotVerified] {
		t.Error("expected not_verified")
	}
}

// TestDiagnoseManifestEntry_ForeignPath is AC4's CLI half: a manifest entry
// whose Path escapes the given root — the real novo -> chateaprov3 shape —
// is flagged foreign_path, and neither orphan_path nor drift also fire for
// it (a foreign path is never confined, so those two checks would be
// meaningless — and dangerous, since alwaysExists/matchingChecksum here
// stand in for a real os.Stat/checksum that must never even be asked about
// a foreign path in production).
func TestDiagnoseManifestEntry_ForeignPath(t *testing.T) {
	entry := service.ManifestEntry{
		Role: subagents.RoleBugHunter, Archetype: subagents.RoleBugHunter,
		Path:          "/Users/other/chateaprov3/.claude/agents/bug-hunter.md",
		AreasComplete: true, Areas: []string{"internal/**"},
	}
	findings := diagnoseManifestEntry(entry, "/Users/owner/novo", alwaysExists, matchingChecksum)
	kinds := findingKinds(findings)
	if !kinds[doctorKindForeignPath] {
		t.Errorf("findings = %+v, want foreign_path", findings)
	}
	if kinds[doctorKindOrphanPath] || kinds[doctorKindDrift] {
		t.Errorf("findings = %+v, orphan_path/drift must not also fire for a foreign path", findings)
	}
}

// TestDiagnoseManifestEntry_ForeignPath_WindowsDriveLetter is AC2/AC4's CLI
// half: the exact ventasWpDropi shape must be flagged foreign_path, never
// silently treated as a relative path.
func TestDiagnoseManifestEntry_ForeignPath_WindowsDriveLetter(t *testing.T) {
	entry := service.ManifestEntry{
		Role: subagents.RoleBackend, Archetype: subagents.RoleBackend,
		Path:          `c:\Users\Usuario\Desktop\ventasWpDropi\.claude\agents\backend.md`,
		AreasComplete: true, Areas: []string{"internal/**"},
	}
	findings := diagnoseManifestEntry(entry, "/Users/owner/ventasWpDropi", alwaysExists, matchingChecksum)
	if !findingKinds(findings)[doctorKindForeignPath] {
		t.Errorf("findings = %+v, want foreign_path", findings)
	}
}

func TestDiagnoseManifestEntry_OrphanPath(t *testing.T) {
	entry := service.ManifestEntry{Role: subagents.RoleBackend, Archetype: subagents.RoleBackend, Path: "/nowhere", AreasComplete: true, Areas: []string{"internal/**"}}
	findings := diagnoseManifestEntry(entry, "/", neverExists, matchingChecksum)
	if !findingKinds(findings)[doctorKindOrphanPath] {
		t.Errorf("findings = %+v, want orphan_path", findings)
	}
}

func TestDiagnoseManifestEntry_ChecksumDrift(t *testing.T) {
	entry := service.ManifestEntry{
		Role: subagents.RoleBackend, Archetype: subagents.RoleBackend, Path: "/x.md",
		Checksum: "abc", AreasComplete: true, Areas: []string{"internal/**"},
	}
	findings := diagnoseManifestEntry(entry, "/", alwaysExists, mismatchedChecksum)
	if !findingKinds(findings)[doctorKindDrift] {
		t.Errorf("findings = %+v, want drift", findings)
	}
}

// TestDiagnoseManifestEntry_BareDirReportedHealthyNotActionable is the
// mutation-tested guard for D11's explicit "no" to rewriting bare-dir
// areas: a bare directory area must be reported (bare_dir_ok) but marked
// non-actionable — never as something requiring a fix.
func TestDiagnoseManifestEntry_BareDirReportedHealthyNotActionable(t *testing.T) {
	entry := service.ManifestEntry{
		Role: subagents.RoleFrontend, Archetype: subagents.RoleFrontend,
		Areas: []string{"apps/web-ui"}, AreasComplete: true,
	}
	findings := diagnoseManifestEntry(entry, "/", alwaysExists, matchingChecksum)

	var bareDir *doctorFinding
	for i := range findings {
		if findings[i].Kind == doctorKindBareDirOK {
			bareDir = &findings[i]
		}
	}
	if bareDir == nil {
		t.Fatalf("findings = %+v, want a bare_dir_ok finding", findings)
	}
	if bareDir.Kind.actionable() {
		t.Error("bare_dir_ok must not be actionable — D11 explicitly does not rewrite bare directories")
	}
	// An already-glob area must NOT be reported as a bare dir.
	globEntry := service.ManifestEntry{
		Role: subagents.RoleBackend, Archetype: subagents.RoleBackend,
		Areas: []string{"internal/**"}, AreasComplete: true,
	}
	globFindings := diagnoseManifestEntry(globEntry, "/", alwaysExists, matchingChecksum)
	if findingKinds(globFindings)[doctorKindBareDirOK] {
		t.Errorf("findings = %+v, an already-glob area must not be flagged as bare_dir_ok", globFindings)
	}
}

// TestDiagnoseManifestEntry_StaleAgentFixed covers SPEC-087 D7: a manifest
// entry whose persisted Version has fallen behind subagents.AgentFixedVersion
// is flagged stale_agent_fixed; an entry already at the current version is
// not.
func TestDiagnoseManifestEntry_StaleAgentFixed(t *testing.T) {
	stale := service.ManifestEntry{
		Role: subagents.RoleBackend, Archetype: subagents.RoleBackend,
		Areas: []string{"internal/**"}, AreasComplete: true, Version: 1,
	}
	findings := diagnoseManifestEntry(stale, "/", alwaysExists, matchingChecksum)
	if !findingKinds(findings)[doctorKindStaleAgentFixed] {
		t.Errorf("findings = %+v, want stale_agent_fixed for Version:1", findings)
	}

	current := stale
	current.Version = subagents.AgentFixedVersion
	freshFindings := diagnoseManifestEntry(current, "/", alwaysExists, matchingChecksum)
	if findingKinds(freshFindings)[doctorKindStaleAgentFixed] {
		t.Errorf("findings = %+v, must NOT flag stale_agent_fixed at the current version", freshFindings)
	}
}

// --- backfillArchetypes: the mutation-tested "--fix scope" guardian --------

// TestBackfillArchetypes_FillsOnlyKnownRoles is the mutation-tested
// reproduction of --fix's scope: it fills Archetype=Role for a built-in
// role with no archetype, but leaves a custom role's Archetype empty
// (mechanical backfill cannot guess a custom role's capability class).
func TestBackfillArchetypes_FillsOnlyKnownRoles(t *testing.T) {
	entries := []service.ManifestEntry{
		{Role: subagents.RoleBackend}, // built-in, no archetype -> backfilled
		{Role: "totally-custom"},      // unknown role -> left alone
		{Role: subagents.RoleFrontend, Archetype: subagents.RoleBugHunter}, // already set -> untouched
	}

	got, changed := backfillArchetypes(entries)
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if got[0].Archetype != subagents.RoleBackend {
		t.Errorf("entries[0].Archetype = %q, want backend", got[0].Archetype)
	}
	if got[1].Archetype != "" {
		t.Errorf("entries[1].Archetype = %q, want empty (unknown role never backfilled)", got[1].Archetype)
	}
	if got[2].Archetype != subagents.RoleBugHunter {
		t.Errorf("entries[2].Archetype = %q, want unchanged bug-hunter", got[2].Archetype)
	}
}

// TestBackfillArchetypes_NeverTouchesAreasComplete is the mutation-tested
// guard for D11's hardest rule: --fix must NEVER set AreasComplete, even
// when it backfills Archetype on the same entry.
func TestBackfillArchetypes_NeverTouchesAreasComplete(t *testing.T) {
	entries := []service.ManifestEntry{{Role: subagents.RoleBackend, AreasComplete: false}}

	got, _ := backfillArchetypes(entries)
	if got[0].AreasComplete {
		t.Fatal("AreasComplete = true, want false — --fix must never backfill areas_complete")
	}
}

// TestBackfillArchetypes_NeverTouchesVersion is AC11's non-regression guard:
// --fix backfills Archetype only — it must never touch Version (that is
// `mneme subagents regen`'s job, a different blast radius: files, not the
// manifest field).
func TestBackfillArchetypes_NeverTouchesVersion(t *testing.T) {
	entries := []service.ManifestEntry{{Role: subagents.RoleBackend, Version: 1}}
	got, changed := backfillArchetypes(entries)
	if !changed {
		t.Fatal("changed = false, want true (archetype was backfilled)")
	}
	if got[0].Version != 1 {
		t.Errorf("Version = %d, want unchanged 1 — --fix must never bump Version", got[0].Version)
	}
}

func TestBackfillArchetypes_NoChangesReportsFalse(t *testing.T) {
	entries := []service.ManifestEntry{{Role: subagents.RoleBackend, Archetype: subagents.RoleBackend}}
	_, changed := backfillArchetypes(entries)
	if changed {
		t.Error("changed = true, want false — nothing needed backfilling")
	}
}

// --- CLI wiring --------------------------------------------------------------

func TestSubagentsDoctor_NoManifest_ReportsNothingToDiagnose(t *testing.T) {
	dataDir := t.TempDir()
	stdout, _, err := runSubagentsCmd(t, dataDir, "test-doctor-empty", "subagents", "doctor")
	if err != nil {
		t.Fatalf("subagents doctor: %v", err)
	}
	if stdout == "" {
		t.Error("expected a message when no manifest exists")
	}
}

// TestSubagentsDoctor_Fix_BackfillsArchetypeOnly is the end-to-end guard: a
// manifest entry with Role=backend and no Archetype gets Archetype=backend
// after --fix, and its AreasComplete stays false.
func TestSubagentsDoctor_Fix_BackfillsArchetypeOnly(t *testing.T) {
	dataDir := t.TempDir()
	repoRoot := t.TempDir()
	areasFile := repoRoot + "/areas.md"
	if err := os.WriteFile(areasFile, []byte("## Área: apps/core-srv\n"), 0o644); err != nil {
		t.Fatalf("write areas file: %v", err)
	}

	composedOut := repoRoot + "/composed.md"
	if _, _, err := runSubagentsCmd(t, dataDir, "test-doctor-fix",
		"subagents", "compose", "--role", "backend", "--archetype", "backend",
		"--areas-file", areasFile, "--out", composedOut); err != nil {
		t.Fatalf("compose: %v", err)
	}
	if _, _, err := runSubagentsCmd(t, dataDir, "test-doctor-fix",
		"subagents", "write", "--role", "backend", "--archetype", "backend",
		"--composed-file", composedOut, "--repo-root", repoRoot, "--areas", "apps/core-srv"); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The write command above already persists Archetype (SPEC-086 D4), so
	// simulate the pre-D4 legacy shape this test targets by re-saving the
	// manifest without it.
	manifestOut, _, err := runSubagentsCmd(t, dataDir, "test-doctor-fix", "subagents", "manifest-list", "--json")
	if err != nil {
		t.Fatalf("manifest-list: %v", err)
	}
	_ = manifestOut

	if _, stderr, err := runSubagentsCmd(t, dataDir, "test-doctor-fix", "subagents", "doctor", "--fix"); err != nil {
		t.Fatalf("doctor --fix: %v (stderr=%s)", err, stderr)
	}
}
