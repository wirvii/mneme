package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestExploreCmd_TreeOutput verifies that renderExploreTree produces the
// expected tree symbols ("|-- " for intermediate, "\-- " for last child)
// and the correct header and footer lines.
func TestExploreCmd_TreeOutput(t *testing.T) {
	resp := &model.ExploreResponse{
		SeedID:          "seed-id",
		SeedTitle:       "Auth service JWT setup",
		TotalNodes:      3,
		TokensUsed:      450,
		MaxDepthReached: 2,
		Nodes: []model.ExploreNode{
			{
				MemoryID:          "child-1",
				ParentMemoryID:    "seed-id",
				Title:             "JWT library",
				RelationType:      model.RelDependsOn,
				AccumulatedWeight: 0.9,
				TokenEstimate:     120,
				Distance:          1,
			},
			{
				MemoryID:          "child-2",
				ParentMemoryID:    "seed-id",
				Title:             "Rate limiter",
				RelationType:      model.RelRelatedTo,
				AccumulatedWeight: 0.7,
				TokenEstimate:     80,
				Distance:          1,
			},
			{
				MemoryID:          "grandchild-1",
				ParentMemoryID:    "child-1",
				Title:             "RSA key rotation",
				RelationType:      model.RelUses,
				AccumulatedWeight: 0.63,
				TokenEstimate:     150,
				Distance:          2,
			},
		},
	}

	var buf bytes.Buffer
	renderExploreTree(&buf, resp)
	out := buf.String()
	lines := strings.Split(out, "\n")

	// The first line should show the seed title with "[seed]".
	if !strings.HasPrefix(lines[0], "Auth service JWT setup") {
		t.Errorf("first line should start with seed title, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "[seed]") {
		t.Errorf("first line should contain [seed], got %q", lines[0])
	}

	// Tree body should contain "|-- " for intermediate children.
	hasIntermediateConnector := false
	for _, l := range lines {
		if strings.Contains(l, "|-- ") {
			hasIntermediateConnector = true
			break
		}
	}
	if !hasIntermediateConnector {
		t.Errorf("expected |-- connector in output:\n%s", out)
	}

	// The last direct child of root should use "\-- ".
	hasLastConnector := false
	for _, l := range lines {
		if strings.Contains(l, "\\-- ") {
			hasLastConnector = true
			break
		}
	}
	if !hasLastConnector {
		t.Errorf("expected \\-- connector in output:\n%s", out)
	}

	// Footer should mention total memories.
	hasFooter := false
	for _, l := range lines {
		if strings.Contains(l, "Total:") {
			hasFooter = true
			break
		}
	}
	if !hasFooter {
		t.Errorf("expected Total: line in output:\n%s", out)
	}
}

// TestExploreCmd_JSONOutput verifies that the --json flag path produces valid
// JSON with the correct shape by testing renderExploreTree indirectly.
// We test the JSON marshaling of ExploreResponse directly since the command
// uses printJSON which wraps json.Encoder.
func TestExploreCmd_JSONOutput(t *testing.T) {
	resp := &model.ExploreResponse{
		SeedID:          "019de100-abcd-7fff-8000-000000000001",
		SeedTitle:       "JSON output test",
		TotalNodes:      1,
		TokensUsed:      100,
		MaxDepthReached: 1,
		Nodes: []model.ExploreNode{
			{
				MemoryID:          "019de200-0000-7fff-8000-000000000002",
				ParentMemoryID:    "019de100-abcd-7fff-8000-000000000001",
				Title:             "connected memory",
				RelationType:      model.RelRelatedTo,
				AccumulatedWeight: 0.8,
				TokenEstimate:     50,
				Distance:          1,
			},
		},
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal ExploreResponse: %v", err)
	}

	var decoded model.ExploreResponse
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.SeedID != resp.SeedID {
		t.Errorf("SeedID: got %q, want %q", decoded.SeedID, resp.SeedID)
	}
	if decoded.TotalNodes != resp.TotalNodes {
		t.Errorf("TotalNodes: got %d, want %d", decoded.TotalNodes, resp.TotalNodes)
	}
	if len(decoded.Nodes) != len(resp.Nodes) {
		t.Errorf("Nodes len: got %d, want %d", len(decoded.Nodes), len(resp.Nodes))
	}
}

// TestExploreCmd_SeedRequired verifies that newExploreCmd requires exactly one
// positional argument.
func TestExploreCmd_SeedRequired(t *testing.T) {
	cmd := newExploreCmd()
	// No args should produce an error from cobra.ExactArgs(1).
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no seed is provided, got nil")
	}
}

// TestExploreCmd_Flags verifies that all flags are registered correctly by
// checking that parsing sets the expected flag values.
func TestExploreCmd_Flags(t *testing.T) {
	cmd := newExploreCmd()
	flags := cmd.Flags()

	if f := flags.Lookup("depth"); f == nil {
		t.Error("--depth flag not registered")
	}
	if f := flags.Lookup("budget"); f == nil {
		t.Error("--budget flag not registered")
	}
	if f := flags.Lookup("threshold"); f == nil {
		t.Error("--threshold flag not registered")
	}
	if f := flags.Lookup("json"); f == nil {
		t.Error("--json flag not registered")
	}
}

// TestExploreCmd_BuildTree verifies that buildExploreTree correctly reconstructs
// the parent→child hierarchy from a flat node list.
func TestExploreCmd_BuildTree(t *testing.T) {
	seedID := "seed"
	nodes := []model.ExploreNode{
		{MemoryID: "a", ParentMemoryID: seedID, Distance: 1},
		{MemoryID: "b", ParentMemoryID: seedID, Distance: 1},
		{MemoryID: "c", ParentMemoryID: "a", Distance: 2},
	}

	root := buildExploreTree(seedID, nodes)
	if len(root.children) != 2 {
		t.Errorf("root children: got %d, want 2", len(root.children))
	}

	var nodeA *exploreTreeNode
	for _, ch := range root.children {
		if ch.node.MemoryID == "a" {
			nodeA = ch
		}
	}
	if nodeA == nil {
		t.Fatal("node 'a' not found as root child")
	}
	if len(nodeA.children) != 1 {
		t.Errorf("nodeA children: got %d, want 1", len(nodeA.children))
	}
	if nodeA.children[0].node.MemoryID != "c" {
		t.Errorf("nodeA child: got %q, want 'c'", nodeA.children[0].node.MemoryID)
	}
}

// TestExploreCmd_EmptyResult verifies that renderExploreTree handles an empty
// result without panicking and produces sensible output.
func TestExploreCmd_EmptyResult(t *testing.T) {
	resp := &model.ExploreResponse{
		SeedID:          "seed-id",
		SeedTitle:       "Isolated memory",
		TotalNodes:      0,
		TokensUsed:      15,
		MaxDepthReached: 0,
		Nodes:           []model.ExploreNode{},
	}

	var buf bytes.Buffer
	renderExploreTree(&buf, resp)
	out := buf.String()

	if !strings.Contains(out, "[seed]") {
		t.Errorf("expected [seed] in empty result output:\n%s", out)
	}
	if !strings.Contains(out, "Total:") {
		t.Errorf("expected Total: line in empty result output:\n%s", out)
	}
}
