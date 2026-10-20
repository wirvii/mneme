package service

import (
	"testing"
	"time"
)

// TestReviewStatus pins the boundary of the pure review rule (SPEC-167 D5):
// due only strictly after the deadline, and never when the deadline is <= 0.
func TestReviewStatus(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	daysAgo := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }

	tests := []struct {
		name    string
		days    int
		ref     time.Time
		wantDue bool
		wantAge int
	}{
		{"disabled deadline", 0, daysAgo(5000), false, 5000},
		{"negative deadline", -3, daysAgo(5000), false, 5000},
		{"exactly the deadline", 60, daysAgo(60), false, 60},
		{"one day past", 60, daysAgo(61), true, 61},
		{"just under a day past", 60, daysAgo(60).Add(-23 * time.Hour), false, 60},
		{"fresh", 60, daysAgo(1), false, 1},
		{"future reference", 60, now.Add(48 * time.Hour), false, -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			due, age := reviewStatus(tt.days, tt.ref, now)
			if due != tt.wantDue || age != tt.wantAge {
				t.Errorf("reviewStatus() = (%v, %d), want (%v, %d)", due, age, tt.wantDue, tt.wantAge)
			}
		})
	}
}
