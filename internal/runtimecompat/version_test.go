package runtimecompat

import "testing"

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"codex-cli 0.148.0-alpha.19", "0.148.0-alpha.19"},
		{"2.1.232 (Claude Code)", "2.1.232"},
	} {
		got, err := ParseVersion(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("ParseVersion(%q) = %q, %v", tc.input, got, err)
		}
	}
	if _, err := ParseVersion("development"); err == nil {
		t.Fatal("expected unparseable version error")
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"0.148.0-alpha.19", "0.148.0-alpha.19", 0},
		{"0.148.0-alpha.18", "0.148.0-alpha.19", -1},
		{"0.148.0-alpha.20", "0.148.0-alpha.19", 1},
		{"0.148.0", "0.148.0-alpha.19", 1}, {"0.147.0", "0.148.0-alpha.19", -1},
		{"2.1.232", "2.1.200", 1},
	} {
		if got := Compare(tc.left, tc.right); got != tc.want {
			t.Errorf("Compare(%s,%s)=%d want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestContract_SeparatesInstallAndCapabilityFloors(t *testing.T) {
	tests := []struct {
		slug, command, install, full string
	}{
		{"claude-code", "claude", "2.1.232", "2.1.232"},
		{"codex", "codex", "0.147.0", "0.148.0-alpha.19"},
	}
	for _, tt := range tests {
		command, install, full, err := contract(tt.slug)
		if err != nil || command != tt.command || install != tt.install || full != tt.full {
			t.Fatalf("contract(%q) = %q, %q, %q, %v", tt.slug, command, install, full, err)
		}
	}
}

func TestCodexCompatibilityFloors(t *testing.T) {
	for _, tt := range []struct {
		version                string
		installable, fullySafe bool
	}{
		{"0.146.9", false, false},
		{"0.147.0", true, false},
		{"0.148.0-alpha.18", true, false},
		{"0.148.0-alpha.19", true, true},
		{"0.148.0", true, true},
	} {
		installable := Compare(tt.version, MinimumCodex) >= 0
		fullySafe := Compare(tt.version, MinimumCodexFull) >= 0
		if installable != tt.installable || fullySafe != tt.fullySafe {
			t.Errorf("%s: installable=%v fullySafe=%v", tt.version, installable, fullySafe)
		}
	}
}
