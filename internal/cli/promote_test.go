package cli

import "testing"

// TestPromoteCmd_RequiresExactlyOneArg verifies that "mneme promote" is wired
// with cobra.ExactArgs(1), rejecting zero or multiple ids. RunE is skipped
// (never invoked) because it calls initService, which needs a real DB.
func TestPromoteCmd_RequiresExactlyOneArg(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", []string{}, true},
		{"one arg", []string{"01938f1b-abcd-7abc-8def-000000000001"}, false},
		{"two args", []string{"id-one", "id-two"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newPromoteCmd()
			cmd.RunE = nil

			err := cmd.Args(cmd, tc.args)
			if (err != nil) != tc.wantErr {
				t.Errorf("Args(%v) error = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
		})
	}
}

// TestPromoteCmd_Use verifies the command's Use string so cobra registers it
// under "mneme promote <id>" as documented.
func TestPromoteCmd_Use(t *testing.T) {
	cmd := newPromoteCmd()
	if cmd.Use != "promote <id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "promote <id>")
	}
}
