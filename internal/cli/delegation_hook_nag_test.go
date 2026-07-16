package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/enforcelog"
)

// certifiedEvents builds a slice of promoteMinEvents+extra would_block
// events for role, spread from daysAgo to now, all inside the same window.
func certifiedEvents(role string, target string, daysAgo int, extra int) []enforcelog.Event {
	now := time.Now()
	var events []enforcelog.Event
	total := promoteMinEvents + extra
	for i := 0; i < total; i++ {
		events = append(events, enforcelog.Event{
			TS:       now.Add(-time.Duration(daysAgo)*24*time.Hour + time.Duration(i)*time.Minute),
			Role:     role,
			Decision: enforcelog.DecisionWouldBlock,
			Target:   target,
		})
	}
	return events
}

// --- SPEC-086 D7/AC15: evaluateStatusNag ------------------------------------

func TestEvaluateStatusNag_ModeNotWarn_NeverNags(t *testing.T) {
	events := certifiedEvents("frontend", "internal/x.go", 20, 5)
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", AreasComplete: true}}

	for _, mode := range []string{"off", "block", ""} {
		nag, ok := evaluateStatusNag(events, time.Now(), entries, mode, "wirvii/x")
		if ok {
			t.Errorf("mode=%q: ok = true, want false (nag: %+v)", mode, nag)
		}
	}
}

// TestEvaluateStatusNag_MutationGuard_WindowGate proves the 14-day
// threshold is load-bearing and STRICTER than the 7-day promote-gate
// window: a project satisfying the promote gate (>=7 days, >=20 events,
// areas_complete) but younger than 14 days must NOT nag yet. Deleting the
// nagMinWindow check (falling back to promoteMinWindow alone) would turn
// this red.
func TestEvaluateStatusNag_MutationGuard_WindowGate(t *testing.T) {
	events := certifiedEvents("frontend", "internal/x.go", 8, 5) // 8 days: gate-eligible, not nag-eligible
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", AreasComplete: true}}

	// Sanity: the promote gate itself is already green at 8 days.
	if ok, reasons, _ := evaluatePromoteGate(events, time.Now(), entries); !ok {
		t.Fatalf("precondition: promote gate should be green at 8 days, reasons: %v", reasons)
	}

	nag, ok := evaluateStatusNag(events, time.Now(), entries, "warn", "wirvii/x")
	if ok {
		t.Fatalf("ok = true, want false — 8 days is gate-eligible but not yet nag-eligible (14 days), nag: %+v", nag)
	}
}

// TestEvaluateStatusNag_MutationGuard_GateReuse proves the nag reuses
// evaluatePromoteGate's evidence+areas_complete criteria: a project well
// past 14 days but with an uncertified role must NOT nag (it isn't
// promotable at all). Deleting the evaluatePromoteGate call would turn
// this red.
func TestEvaluateStatusNag_MutationGuard_GateReuse(t *testing.T) {
	events := certifiedEvents("frontend", "internal/x.go", 20, 5)
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", AreasComplete: false}} // NOT certified

	nag, ok := evaluateStatusNag(events, time.Now(), entries, "warn", "wirvii/x")
	if ok {
		t.Fatalf("ok = true, want false — role is not areas_complete, gate should reject, nag: %+v", nag)
	}
}

// TestEvaluateStatusNag_HappyPath_RendersExpectedLine covers the intended
// case D7 illustrates.
func TestEvaluateStatusNag_HappyPath_RendersExpectedLine(t *testing.T) {
	events := certifiedEvents("frontend", "internal/x.go", 21, 11) // 21 days, 31 events
	entries := []hookManifestEntry{
		{Role: "frontend", Archetype: "frontend", AreasComplete: true},
		{Role: "backend", Archetype: "backend", AreasComplete: true},
		{Role: "bug-hunter", Archetype: "bug-hunter", AreasComplete: true},
		{Role: "diagnostician", Archetype: "diagnostician"}, // not an implementer — excluded from the count
	}

	nag, ok := evaluateStatusNag(events, time.Now(), entries, "warn", "wirvii/wirvii360r")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if nag.WouldBlock != promoteMinEvents+11 {
		t.Errorf("WouldBlock = %d, want %d", nag.WouldBlock, promoteMinEvents+11)
	}
	if nag.RolesTotal != 3 || nag.RolesComplete != 3 {
		t.Errorf("RolesTotal/RolesComplete = %d/%d, want 3/3 (diagnostician excluded, non-implementer)", nag.RolesTotal, nag.RolesComplete)
	}
	if nag.DaysInWarn < 20 {
		t.Errorf("DaysInWarn = %d, want >= 20", nag.DaysInWarn)
	}

	line := renderNagLine(nag)
	if !strings.Contains(line, "wirvii/wirvii360r") || !strings.Contains(line, "would_block") ||
		!strings.Contains(line, "3/3 roles completos") || !strings.Contains(line, "delegation-hook promote") {
		t.Errorf("renderNagLine = %q, missing expected content", line)
	}
}

func TestEvaluateStatusNag_NoEvents_NeverNags(t *testing.T) {
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", AreasComplete: true}}
	if _, ok := evaluateStatusNag(nil, time.Now(), entries, "warn", "wirvii/x"); ok {
		t.Error("ok = true, want false — no events at all")
	}
}

// --- CLI wiring: "mneme status" / "delegation-hook status" -----------------

// TestDelegationHookStatus_NoNagByDefault verifies a freshly-initialized
// project (no telemetry) prints only the registration status, no nag.
func TestDelegationHookStatus_NoNagByDefault(t *testing.T) {
	resetGlobalCLIFlags(t)
	repoRoot := t.TempDir()
	stdout, _, err := runDelegationHookCmd(t, "status", repoRoot)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(stdout, "listo para bloquear") {
		t.Errorf("stdout = %q, unexpected nag with no telemetry", stdout)
	}
}

// TestDelegationHookStatus_NagsWhenEligible seeds enough certified,
// old-enough telemetry and confirms the nag line appears in "delegation-hook
// status"'s output.
func TestDelegationHookStatus_NagsWhenEligible(t *testing.T) {
	// runDelegationHookCmd builds a minimal Cobra tree (no --data-dir/
	// --project flags registered on it) — without this reset, a leftover
	// global flagDataDir/flagProject from an earlier test in this package
	// (e.g. subagents_test.go, which runs through the real root command)
	// would leak into statusCmdNagLine's initSubagentService() call and
	// silently defeat the HOME-based isolation setupDelegationRepo sets up.
	resetGlobalCLIFlags(t)

	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[{"role":"frontend","archetype":"frontend","areas":["apps/web-ui/**"],"areas_complete":true}]`)
	writeContainmentConfig(t, "warn")

	home := os.Getenv("HOME")
	path := enforcelogPath(home+"/.mneme", slug)
	for _, ev := range certifiedEvents("frontend", "internal/x.go", 21, 11) {
		ev.Project = slug
		if err := enforcelog.Append(path, ev, enforcelog.DefaultMaxBytes); err != nil {
			t.Fatalf("seed enforcelog: %v", err)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	stdout, _, err := runDelegationHookCmd(t, "status", cwd)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout, "listo para bloquear") || !strings.Contains(stdout, "delegation-hook promote") {
		t.Errorf("stdout = %q, want the D7 nag line", stdout)
	}
}
