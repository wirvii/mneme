package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/enforcelog"
)

// --- SPEC-086 D7: evaluatePromoteGate ---------------------------------------

func TestEvaluatePromoteGate_NoEvents_Rejected(t *testing.T) {
	ok, reasons, _ := evaluatePromoteGate(nil, time.Now(), nil)
	if ok {
		t.Fatal("ok = true, want false — no events at all")
	}
	if len(reasons) == 0 {
		t.Error("expected at least one reason")
	}
}

// TestEvaluatePromoteGate_MutationGuard_WindowGate proves the elapsed-time
// criterion is load-bearing: 20+ events all within the last hour must still
// be rejected. Deleting the elapsed-time check would turn this red.
func TestEvaluatePromoteGate_MutationGuard_WindowGate(t *testing.T) {
	now := time.Now()
	var events []enforcelog.Event
	for i := 0; i < promoteMinEvents+5; i++ {
		events = append(events, enforcelog.Event{TS: now.Add(-time.Duration(i) * time.Minute), Role: "backend", Decision: enforcelog.DecisionAllow})
	}

	ok, reasons, _ := evaluatePromoteGate(events, now, nil)
	if ok {
		t.Fatal("ok = true, want false — all events are recent, window requirement not met")
	}
	if len(reasons) == 0 {
		t.Error("expected a window-related reason")
	}
}

// TestEvaluatePromoteGate_MutationGuard_EventCountGate proves the minimum
// event-count criterion is load-bearing: a long enough window with too few
// events must still be rejected. Deleting the count check would turn this
// red.
func TestEvaluatePromoteGate_MutationGuard_EventCountGate(t *testing.T) {
	now := time.Now()
	events := []enforcelog.Event{
		{TS: now.Add(-30 * 24 * time.Hour), Role: "backend", Decision: enforcelog.DecisionAllow},
		{TS: now.Add(-1 * time.Hour), Role: "backend", Decision: enforcelog.DecisionAllow},
	}

	ok, reasons, _ := evaluatePromoteGate(events, now, nil)
	if ok {
		t.Fatal("ok = true, want false — only 2 events, need promoteMinEvents")
	}
	if len(reasons) == 0 {
		t.Error("expected an event-count-related reason")
	}
}

// TestEvaluatePromoteGate_MutationGuard_AreasCompleteGate proves criterion 2
// is load-bearing: a role with would_block events but NO areas_complete:true
// in the current manifest blocks promotion, even with a sufficient window
// and event count. Deleting this check would turn this red.
func TestEvaluatePromoteGate_MutationGuard_AreasCompleteGate(t *testing.T) {
	now := time.Now()
	var events []enforcelog.Event
	for i := 0; i < promoteMinEvents+5; i++ {
		events = append(events, enforcelog.Event{
			TS: now.Add(-8*24*time.Hour + time.Duration(i)*time.Minute),
			Role: "frontend", Decision: enforcelog.DecisionWouldBlock, Target: "internal/store/foo.go",
		})
	}
	// No manifest entry at all for "frontend" -> gate must reject.
	ok, reasons, _ := evaluatePromoteGate(events, now, nil)
	if ok {
		t.Fatal("ok = true, want false — frontend has would_block events but no manifest entry")
	}
	if len(reasons) == 0 {
		t.Error("expected an areas_complete-related reason")
	}

	// Now with a certified entry -> gate must pass.
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", AreasComplete: true}}
	ok2, reasons2, pairs2 := evaluatePromoteGate(events, now, entries)
	if !ok2 {
		t.Fatalf("ok = false, want true (reasons: %v)", reasons2)
	}
	if len(pairs2) != 1 || pairs2[0].Role != "frontend" || pairs2[0].Path != "internal/store/foo.go" {
		t.Errorf("pairs = %+v, want a single frontend/internal-store pair", pairs2)
	}
}

// TestEvaluatePromoteGate_PairsDeduplicatedAndSorted verifies the breaking
// pairs list is both deduplicated and deterministically ordered — important
// for the "review this exact list" UX D7 requires.
func TestEvaluatePromoteGate_PairsDeduplicatedAndSorted(t *testing.T) {
	now := time.Now()
	var events []enforcelog.Event
	for i := 0; i < promoteMinEvents+2; i++ {
		events = append(events, enforcelog.Event{
			TS: now.Add(-8*24*time.Hour + time.Duration(i)*time.Minute),
			Role: "frontend", Decision: enforcelog.DecisionWouldBlock, Target: "internal/b.go",
		})
	}
	events = append(events,
		enforcelog.Event{TS: now.Add(-8 * 24 * time.Hour), Role: "frontend", Decision: enforcelog.DecisionWouldBlock, Target: "internal/a.go"},
		enforcelog.Event{TS: now.Add(-7 * 24 * time.Hour), Role: "frontend", Decision: enforcelog.DecisionWouldBlock, Target: "internal/b.go"}, // dup target
	)
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", AreasComplete: true}}

	_, _, pairs := evaluatePromoteGate(events, now, entries)
	if len(pairs) != 2 {
		t.Fatalf("len(pairs) = %d, want 2 (deduplicated)", len(pairs))
	}
	if pairs[0].Path != "internal/a.go" || pairs[1].Path != "internal/b.go" {
		t.Errorf("pairs = %+v, want sorted [a.go, b.go]", pairs)
	}
}

// --- CLI wiring: "delegation-hook report" / "delegation-hook promote" ------

// TestDelegationHookReport_NoTelemetry_PrintsNoData covers the CLI wiring
// end-to-end for a project with no enforcelog file yet.
func TestDelegationHookReport_NoTelemetry_PrintsNoData(t *testing.T) {
	slug, _ := setupDelegationRepo(t)
	_ = slug

	stdout, _, err := runDelegationHookCmd(t, "report")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(stdout, "No containment telemetry") {
		t.Errorf("stdout = %q, want a no-data message", stdout)
	}
}

// TestDelegationHookReport_WithTelemetry_ShowsRoleBreakdown seeds a real
// enforcelog file and verifies the CLI surfaces the aggregated counts.
func TestDelegationHookReport_WithTelemetry_ShowsRoleBreakdown(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	_ = dbPath
	home := os.Getenv("HOME")
	path := enforcelogPath(home+"/.mneme", slug)
	ev := enforcelog.Event{TS: time.Now(), Project: slug, Role: "frontend", Decision: enforcelog.DecisionWouldBlock, Target: "internal/x.go"}
	if err := enforcelog.Append(path, ev, enforcelog.DefaultMaxBytes); err != nil {
		t.Fatalf("seed enforcelog: %v", err)
	}

	stdout, _, err := runDelegationHookCmd(t, "report")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(stdout, "frontend") || !strings.Contains(stdout, "would_block=1") {
		t.Errorf("stdout = %q, want a frontend would_block=1 line", stdout)
	}
}

// TestDelegationHookPromote_GateFails_RejectsWithoutWriting verifies that a
// project with no telemetry never gets promoted — no config file is written
// at all.
func TestDelegationHookPromote_GateFails_RejectsWithoutWriting(t *testing.T) {
	setupDelegationRepo(t)
	home := os.Getenv("HOME")

	_, stderr, err := runDelegationHookCmd(t, "promote", "--yes")
	if err == nil {
		t.Fatal("promote: want error when the gate is not satisfied, got nil")
	}
	_ = stderr

	if _, statErr := os.Stat(home + "/.mneme/config.toml"); statErr == nil {
		data, _ := os.ReadFile(home + "/.mneme/config.toml")
		if strings.Contains(string(data), "subagent_containment") {
			t.Error("config.toml must not have been written when the gate failed")
		}
	}
}

// TestDelegationHookPromote_RequiresYesFlag verifies the confirmation gate:
// even when D7's evidence gate passes, promotion is refused without --yes.
func TestDelegationHookPromote_RequiresYesFlag(t *testing.T) {
	slug, _ := setupDelegationRepo(t)
	home := os.Getenv("HOME")
	path := enforcelogPath(home+"/.mneme", slug)

	now := time.Now()
	for i := 0; i < promoteMinEvents+2; i++ {
		ev := enforcelog.Event{
			TS: now.Add(-8*24*time.Hour + time.Duration(i)*time.Minute), Project: slug,
			Role: "frontend", Decision: enforcelog.DecisionAllow,
		}
		if err := enforcelog.Append(path, ev, enforcelog.DefaultMaxBytes); err != nil {
			t.Fatalf("seed enforcelog: %v", err)
		}
	}

	_, _, err := runDelegationHookCmd(t, "promote")
	if err == nil {
		t.Fatal("promote (no --yes): want error, got nil")
	}

	if _, statErr := os.Stat(home + "/.mneme/config.toml"); statErr == nil {
		data, _ := os.ReadFile(home + "/.mneme/config.toml")
		if strings.Contains(string(data), "subagent_containment") {
			t.Error("config.toml must not have been written without --yes")
		}
	}
}
