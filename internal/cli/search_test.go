package cli

import (
	"testing"
)

// TestSearchCmd_GraphFlag verifies that the --graph and --no-graph flags on
// "mneme search" are registered correctly and produce the expected flag values
// when parsed by Cobra.
//
// Table-driven cases:
//   - default (no flags): --graph defaults to true, --no-graph to false
//   - --no-graph: flips graph expansion off
//   - --graph=false: explicit disable via the --graph flag
//   - --graph (explicit true): graph expansion on
func TestSearchCmd_GraphFlag(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantGraph    bool
		wantNoGraph  bool
		wantInclude  bool // expected effective include_graph after RunE logic
	}{
		{
			name:        "default no flags",
			args:        []string{"dummy-query"},
			wantGraph:   true,
			wantNoGraph: false,
			wantInclude: true,
		},
		{
			name:        "--no-graph disables expansion",
			args:        []string{"dummy-query", "--no-graph"},
			wantGraph:   true,  // default for --graph flag itself unchanged
			wantNoGraph: true,
			wantInclude: false,
		},
		{
			name:        "--graph=false disables expansion",
			args:        []string{"dummy-query", "--graph=false"},
			wantGraph:   false,
			wantNoGraph: false,
			wantInclude: false,
		},
		{
			name:        "--graph explicit true",
			args:        []string{"dummy-query", "--graph"},
			wantGraph:   true,
			wantNoGraph: false,
			wantInclude: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newSearchCmd()

			// Parse the flags only; skip RunE (it calls initService which needs a DB).
			cmd.RunE = nil
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatalf("ParseFlags(%v): %v", tc.args, err)
			}

			graphFlag, err := cmd.Flags().GetBool("graph")
			if err != nil {
				t.Fatalf("GetBool(graph): %v", err)
			}
			noGraphFlag, err := cmd.Flags().GetBool("no-graph")
			if err != nil {
				t.Fatalf("GetBool(no-graph): %v", err)
			}

			if graphFlag != tc.wantGraph {
				t.Errorf("--graph = %v, want %v", graphFlag, tc.wantGraph)
			}
			if noGraphFlag != tc.wantNoGraph {
				t.Errorf("--no-graph = %v, want %v", noGraphFlag, tc.wantNoGraph)
			}

			// Reproduce the same logic used in RunE to compute effective include_graph.
			effective := true
			if noGraphFlag {
				effective = false
			} else if cmd.Flags().Changed("graph") {
				effective = graphFlag
			}

			if effective != tc.wantInclude {
				t.Errorf("effective include_graph = %v, want %v", effective, tc.wantInclude)
			}
		})
	}
}
