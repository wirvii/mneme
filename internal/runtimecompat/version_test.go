package runtimecompat

import "testing"

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"codex-cli 0.147.0", "0.147.0"},
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
		{"0.147.0", "0.147.0", 0}, {"0.148.0", "0.147.0", 1}, {"0.146.9", "0.147.0", -1},
		{"2.1.232", "2.1.200", 1},
	} {
		if got := Compare(tc.left, tc.right); got != tc.want {
			t.Errorf("Compare(%s,%s)=%d want %d", tc.left, tc.right, got, tc.want)
		}
	}
}
