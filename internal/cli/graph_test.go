package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestGraphRebuildCmd_Flags verifies that all flags on "mneme graph rebuild"
// are registered correctly and can be parsed by Cobra.
func TestGraphRebuildCmd_Flags(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantScope     string
		wantMinShared int
		wantMaxRels   int
		wantBatchSize int
		wantForce     bool
		wantDryRun    bool
	}{
		{
			name:          "defaults",
			args:          []string{},
			wantScope:     "project",
			wantMinShared: 2,
			wantMaxRels:   50,
			wantBatchSize: 500,
			wantForce:     false,
			wantDryRun:    false,
		},
		{
			name:          "--scope global",
			args:          []string{"--scope", "global"},
			wantScope:     "global",
			wantMinShared: 2,
			wantMaxRels:   50,
			wantBatchSize: 500,
			wantForce:     false,
			wantDryRun:    false,
		},
		{
			name:          "--min-shared 3 -k shorthand",
			args:          []string{"-k", "3"},
			wantScope:     "project",
			wantMinShared: 3,
			wantMaxRels:   50,
			wantBatchSize: 500,
			wantForce:     false,
			wantDryRun:    false,
		},
		{
			name:          "--force and --dry-run",
			args:          []string{"--force", "--dry-run"},
			wantScope:     "project",
			wantMinShared: 2,
			wantMaxRels:   50,
			wantBatchSize: 500,
			wantForce:     true,
			wantDryRun:    true,
		},
		{
			name:          "--batch-size 1000",
			args:          []string{"-b", "1000"},
			wantScope:     "project",
			wantMinShared: 2,
			wantMaxRels:   50,
			wantBatchSize: 1000,
			wantForce:     false,
			wantDryRun:    false,
		},
		{
			name:          "--max-relations 10",
			args:          []string{"--max-relations", "10"},
			wantScope:     "project",
			wantMinShared: 2,
			wantMaxRels:   10,
			wantBatchSize: 500,
			wantForce:     false,
			wantDryRun:    false,
		},
		{
			name:          "--scope all with --force",
			args:          []string{"--scope", "all", "-f"},
			wantScope:     "all",
			wantMinShared: 2,
			wantMaxRels:   50,
			wantBatchSize: 500,
			wantForce:     true,
			wantDryRun:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newGraphRebuildCmd()
			// Skip RunE — we only test flag parsing.
			cmd.RunE = nil
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatalf("ParseFlags(%v): %v", tc.args, err)
			}

			scope, _ := cmd.Flags().GetString("scope")
			minShared, _ := cmd.Flags().GetInt("min-shared")
			maxRels, _ := cmd.Flags().GetInt("max-relations")
			batchSize, _ := cmd.Flags().GetInt("batch-size")
			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			if scope != tc.wantScope {
				t.Errorf("--scope = %q, want %q", scope, tc.wantScope)
			}
			if minShared != tc.wantMinShared {
				t.Errorf("--min-shared = %d, want %d", minShared, tc.wantMinShared)
			}
			if maxRels != tc.wantMaxRels {
				t.Errorf("--max-relations = %d, want %d", maxRels, tc.wantMaxRels)
			}
			if batchSize != tc.wantBatchSize {
				t.Errorf("--batch-size = %d, want %d", batchSize, tc.wantBatchSize)
			}
			if force != tc.wantForce {
				t.Errorf("--force = %v, want %v", force, tc.wantForce)
			}
			if dryRun != tc.wantDryRun {
				t.Errorf("--dry-run = %v, want %v", dryRun, tc.wantDryRun)
			}
		})
	}
}

// TestGraphRebuildCmd_DryRunOutput verifies that when the command outputs
// its header, it includes the "Dry run" prefix when --dry-run is set.
// This is a shallow test that exercises the output formatting without needing
// a real database (we inspect the help text and command structure instead).
func TestGraphRebuildCmd_DryRunOutput(t *testing.T) {
	// Verify the command is registered under graph.
	graphCmd := newGraphCmd()
	var found bool
	for _, sub := range graphCmd.Commands() {
		if sub.Use == "rebuild" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'rebuild' subcommand under 'graph'")
	}

	// Verify the help output includes expected flags.
	var buf bytes.Buffer
	graphCmd.SetOut(&buf)
	graphCmd.SetArgs([]string{"rebuild", "--help"})
	_ = graphCmd.Execute() // --help exits 0 after printing
	helpText := buf.String()

	for _, wantFlag := range []string{"--scope", "--min-shared", "--force", "--dry-run", "--batch-size"} {
		if !strings.Contains(helpText, wantFlag) {
			t.Errorf("expected %q in help output, got:\n%s", wantFlag, helpText)
		}
	}
}
