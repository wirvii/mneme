package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// newExploreCmd returns the "mneme explore" subcommand. It performs a BFS
// traversal of the knowledge graph starting from a seed memory and prints the
// result as an ASCII tree (default) or JSON (--json flag).
func newExploreCmd() *cobra.Command {
	var (
		flagDepth     int
		flagBudget    int
		flagThreshold float64
		flagJSON      bool
	)

	cmd := &cobra.Command{
		Use:   "explore <seed>",
		Short: "Explore the knowledge graph from a seed memory",
		Long: `Explore the knowledge graph starting from a seed memory using
prioritised BFS traversal. The seed can be a full UUID, a short UUID prefix
(8+ hex chars), or a topic_key (e.g. "architecture/auth-model").

The result is printed as an ASCII tree by default. Use --json for structured output.`,
		Example: `  mneme explore "architecture/auth-model" --depth 3
  mneme explore 019de100 --depth 2
  mneme explore "019de100-abcd-7fff-8000-000000000001" --json
  mneme explore "ops/key-rotation" --budget 2000 --threshold 0.5`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			seed := args[0]

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.ExploreRequest{
				Seed:      seed,
				Budget:    flagBudget,
				Threshold: flagThreshold,
			}
			if cmd.Flags().Changed("depth") {
				req.Depth = &flagDepth
			}

			resp, err := svc.Explore(cmd.Context(), req)
			if err != nil {
				_, _ = fmt.Fprintln(os.Stderr, "error:", err)
				return fmt.Errorf("explore: %w", err)
			}

			if flagJSON {
				return printJSON(os.Stdout, resp)
			}

			renderExploreTree(os.Stdout, resp)
			return nil
		},
	}

	cmd.Flags().IntVarP(&flagDepth, "depth", "d", 2, "Maximum hops from seed (0-5)")
	cmd.Flags().IntVarP(&flagBudget, "budget", "b", 4000, "Token budget for returned memories")
	cmd.Flags().Float64VarP(&flagThreshold, "threshold", "t", 0.3, "Minimum relation weight to follow (0.0-1.0)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON instead of tree")

	return cmd
}

// renderExploreTree prints the ExploreResponse as an ASCII tree to w.
// The seed is shown as the root, and each discovered memory is a child node.
// Intermediate nodes use "|-- " and the last child at each level uses "\-- ".
// Vertical continuation lines use "|   " and empty continuation uses "    ".
func renderExploreTree(w io.Writer, resp *model.ExploreResponse) {
	seedLabel := resp.SeedTitle
	if seedLabel == "" {
		seedLabel = resp.SeedID
	}
	fmt.Fprintf(w, "%s [seed]\n", truncate(seedLabel, 50))
	if len(resp.Nodes) == 0 {
		fmt.Fprintf(w, "\nTotal: 0 memories | %d tokens\n", resp.TokensUsed)
		return
	}

	root := buildExploreTree(resp.SeedID, resp.Nodes)
	for i, child := range root.children {
		isLast := i == len(root.children)-1
		printExploreNode(w, child, "", isLast)
	}

	maxDepth := resp.MaxDepthReached
	levelsStr := "level"
	if maxDepth != 1 {
		levelsStr = "levels"
	}
	fmt.Fprintf(w, "\nTotal: %d memories | %d tokens | %d %s\n",
		resp.TotalNodes, resp.TokensUsed, maxDepth, levelsStr,
	)
}

// exploreTreeNode is a node in the CLI tree reconstruction.
type exploreTreeNode struct {
	node     model.ExploreNode
	children []*exploreTreeNode
}

// buildExploreTree reconstructs the tree from the flat sorted ExploreResponse
// Nodes slice using ParentMemoryID. Nodes with no known parent are attached to
// the root (fallback for orphan nodes caused by budget pruning).
func buildExploreTree(seedID string, nodes []model.ExploreNode) *exploreTreeNode {
	root := &exploreTreeNode{}
	byID := map[string]*exploreTreeNode{seedID: root}

	// Nodes are already sorted by (distance ASC, weight DESC), so parents are
	// always registered before their children.
	for i := range nodes {
		tn := &exploreTreeNode{node: nodes[i]}
		byID[nodes[i].MemoryID] = tn
		parent, ok := byID[nodes[i].ParentMemoryID]
		if !ok {
			parent = root
		}
		parent.children = append(parent.children, tn)
	}
	return root
}

// printExploreNode recursively prints a tree node and its children with proper
// ASCII tree connectors.
func printExploreNode(w io.Writer, tn *exploreTreeNode, prefix string, isLast bool) {
	connector := "|-- "
	if isLast {
		connector = "\\-- "
	}

	name := truncate(tn.node.Title, 40)
	if tn.node.TopicKey != "" {
		name = tn.node.TopicKey
	}

	fmt.Fprintf(w, "%s%s%s (%s, w=%.2f, %d tok)\n",
		prefix, connector, name,
		tn.node.RelationType,
		tn.node.AccumulatedWeight,
		tn.node.TokenEstimate,
	)

	childPrefix := prefix + "|   "
	if isLast {
		childPrefix = prefix + "    "
	}
	for i, child := range tn.children {
		printExploreNode(w, child, childPrefix, i == len(tn.children)-1)
	}
}

// isAnyPrefix is used by tests to verify tree output contains expected strings.
func isAnyPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}
