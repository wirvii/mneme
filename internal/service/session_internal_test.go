package service

import (
	"testing"
	"time"
)

// TestFormatSessionDuration verifies the numeric correctness of the pure
// duration formatter with fixed times, avoiding the "0s" trap an integration
// test would fall into (every fixture memory is created "now" — SPEC-108
// plan §0.3).
func TestFormatSessionDuration(t *testing.T) {
	firstAt := time.Date(2026, 8, 4, 21, 32, 19, 0, time.UTC)
	now := firstAt.Add(3*time.Minute + 38*time.Second)

	got := formatSessionDuration(firstAt, now)
	want := "3m38s"
	if got != want {
		t.Errorf("formatSessionDuration = %q, want %q", got, want)
	}
}

// TestFormatSessionDuration_LegitimateZero verifies that a real "0s" (all
// memories created within the same rounded second) is a true value, not the
// literal this spec removed.
func TestFormatSessionDuration_LegitimateZero(t *testing.T) {
	firstAt := time.Date(2026, 8, 4, 21, 32, 19, 0, time.UTC)
	now := firstAt.Add(400 * time.Millisecond)

	got := formatSessionDuration(firstAt, now)
	want := "0s"
	if got != want {
		t.Errorf("formatSessionDuration = %q, want %q", got, want)
	}
}
