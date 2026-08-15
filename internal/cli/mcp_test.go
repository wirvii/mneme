package cli

import (
	"strings"
	"testing"
)

func TestMCPCmdCallerPolicyFlagsFailBeforeServerStart(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing archetype", []string{"--caller-role", "backend"}, "must be provided together"},
		{"missing role", []string{"--caller-archetype", "backend"}, "must be provided together"},
		{"unsafe role", []string{"--caller-role", "../backend", "--caller-archetype", "backend"}, "invalid caller role"},
		{"unknown archetype", []string{"--caller-role", "payments", "--caller-archetype", "unknown"}, "invalid caller archetype"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newMCPCmd()
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
