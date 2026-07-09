package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newConflictsCmd returns the "mneme conflicts" subcommand group.
// It provides operations for detecting, judging, and managing memory conflict
// relations using a deterministic FTS5 candidate phase and an optional LLM
// judgment phase via the local Claude CLI subprocess.
func newConflictsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "Detect and manage memory conflict relations",
		Long: `Detect and manage conflict relations between memories.

Two-phase workflow:
  1. Detection (deterministic, FTS5): find candidate memories that share terms.
  2. Judgment (LLM via claude CLI subprocess, $0 cost): classify each pair.

Relation types:
  supersedes     — A is the current version, B is obsolete (uses superseded_by).
  conflicts_with — A and B make contradictory claims without a clear winner.
  unrelated      — A and B are on different topics (negative cache).

Subcommands:
  candidates  Find FTS5 candidate IDs for a given memory.
  scan        Judge candidates using the Claude CLI (dry-run by default).
  link        Manually create a relation between two memories.
  unlink      Remove a relation between two memories.
  list        List all conflict relations for a project.`,
	}

	cmd.AddCommand(
		newConflictsCandidatesCmd(),
		newConflictsScanCmd(),
		newConflictsLinkCmd(),
		newConflictsUnlinkCmd(),
		newConflictsListCmd(),
	)

	return cmd
}

// newConflictsCandidatesCmd returns the "mneme conflicts candidates" subcommand.
func newConflictsCandidatesCmd() *cobra.Command {
	var flagLimit int

	cmd := &cobra.Command{
		Use:   "candidates <id>",
		Short: "Find FTS5 candidate memories that may conflict with the given memory",
		Long: `Find candidate memory IDs that share salient terms with the given memory.

Uses deterministic FTS5 term matching — no LLM involved. Already-judged pairs
and the source memory itself are excluded. Run 'conflicts scan' to judge the
candidates with the Claude CLI.`,
		Example: `  mneme conflicts candidates 01910000-0000-7000-8000-000000000001
  mneme conflicts candidates 01910000-0000-7000-8000-000000000001 --limit 10`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			ids, err := svc.ConflictCandidates(cmd.Context(), args[0], flagLimit)
			if err != nil {
				if errors.Is(err, model.ErrNotFound) {
					return fmt.Errorf("memory %q not found", args[0])
				}
				return fmt.Errorf("conflicts candidates: %w", err)
			}

			if len(ids) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No conflict candidates found.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Candidates for %s:\n", args[0])
			for _, id := range ids {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", id)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&flagLimit, "limit", 5, "Maximum number of candidates to return")
	return cmd
}

// newConflictsScanCmd returns the "mneme conflicts scan" subcommand.
func newConflictsScanCmd() *cobra.Command {
	var (
		flagProject string
		flagLimit   int
		flagApply   bool
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan memories for conflicts using the Claude CLI as judge",
		Long: `Scan project memories for conflicts using the local Claude CLI as judge.

Dry-run by default: results are printed but not persisted. Pass --apply to
write the judged relations to the database.

The Claude CLI must be installed and on PATH. When absent, the command reports
the condition and exits without calling any metered API.

Each pair is judged with a subprocess call ($0 cost on Claude subscription).
Already-judged pairs (in memory_relations or superseded_by) are skipped.`,
		Example: `  mneme conflicts scan
  mneme conflicts scan --apply
  mneme conflicts scan --project my-project --limit 10 --apply`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := service.ConflictScanRequest{
				Project: flagProject,
				Limit:   flagLimit,
				Apply:   flagApply,
			}

			resp, err := svc.ConflictScan(cmd.Context(), req)
			if err != nil {
				if errors.Is(err, model.ErrCLIUnavailable) {
					fmt.Fprintln(os.Stderr, "claude CLI not found on PATH — install it to enable conflict judgment.")
					fmt.Fprintln(os.Stderr, "Tip: run 'which claude' to check, or install from https://claude.ai/download.")
					os.Exit(1)
				}
				return fmt.Errorf("conflicts scan: %w", err)
			}

			if len(resp.Pairs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No conflict candidate pairs found.")
				return nil
			}

			mode := "dry-run"
			if flagApply {
				mode = "applied"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Scan results (%s): %d pairs, %d errors\n\n", mode, resp.Total, resp.Errors)

			for _, p := range resp.Pairs {
				if p.Skipped {
					fmt.Fprintf(cmd.OutOrStdout(), "  SKIPPED %s ↔ %s: %s\n", p.MemoryA, p.MemoryB, p.Error)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n    A: %s (%s)\n    B: %s (%s)\n    relation: %s — %s\n",
					relationSymbol(p.Relation),
					p.MemoryA, p.TitleA,
					p.MemoryB, p.TitleB,
					p.Relation, p.Rationale,
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagProject, "project", "", "Project slug to scan (default: auto-detect)")
	cmd.Flags().IntVar(&flagLimit, "limit", 5, "Maximum number of candidate pairs to judge (max 10)")
	cmd.Flags().BoolVar(&flagApply, "apply", false, "Persist judged relations to the database")
	return cmd
}

// newConflictsLinkCmd returns the "mneme conflicts link" subcommand.
func newConflictsLinkCmd() *cobra.Command {
	var flagRationale string

	cmd := &cobra.Command{
		Use:   "link <from-id> <to-id> <supersedes|conflicts_with|unrelated>",
		Short: "Manually create a memory conflict relation",
		Long: `Create a manually-set relation between two memories.

Relation types:
  supersedes     from supersedes to (to becomes superseded_by from)
  conflicts_with symmetric conflict, no clear winner
  unrelated      negative cache entry; these two are on different topics

Manual relations override CLI-judged ones.`,
		Example: `  mneme conflicts link mem-abc mem-def supersedes --rationale "Updated auth design"
  mneme conflicts link mem-abc mem-def conflicts_with`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromID, toID, relation := args[0], args[1], args[2]

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			if err := svc.ConflictLink(cmd.Context(), fromID, toID, relation, flagRationale); err != nil {
				if errors.Is(err, model.ErrInvalidRelation) {
					return fmt.Errorf("invalid relation %q: must be supersedes, conflicts_with, or unrelated", relation)
				}
				if errors.Is(err, model.ErrNotFound) {
					return fmt.Errorf("one or both memories not found")
				}
				return fmt.Errorf("conflicts link: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Linked: %s -[%s]-> %s\n", fromID, relation, toID)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagRationale, "rationale", "", "One-line explanation for the relation")
	return cmd
}

// newConflictsUnlinkCmd returns the "mneme conflicts unlink" subcommand.
func newConflictsUnlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <from-id> <to-id>",
		Short: "Remove a memory conflict relation",
		Long: `Remove the conflict relation between two memories (in either direction).
Also clears superseded_by when the relation was a supersedes link.`,
		Example: `  mneme conflicts unlink mem-abc mem-def`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromID, toID := args[0], args[1]

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			if err := svc.ConflictUnlink(cmd.Context(), fromID, toID); err != nil {
				return fmt.Errorf("conflicts unlink: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Unlinked: %s ↔ %s\n", fromID, toID)
			return nil
		},
	}
}

// newConflictsListCmd returns the "mneme conflicts list" subcommand.
func newConflictsListCmd() *cobra.Command {
	var (
		flagProject string
		flagJSON    bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memory conflict relations for the current project",
		Long: `List all conflicts_with and unrelated memory relation edges for a project.

supersedes relations are stored as memories.superseded_by and are not listed
here; use 'mneme search --include-superseded' to see them.`,
		Example: `  mneme conflicts list
  mneme conflicts list --project my-project
  mneme conflicts list --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			rels, err := svc.ConflictList(cmd.Context(), flagProject)
			if err != nil {
				return fmt.Errorf("conflicts list: %w", err)
			}

			if flagJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rels)
			}

			if len(rels) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No conflict relations found.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-12s  %-36s  %-36s  %-14s  %s\n",
				"relation", "from_id", "to_id", "judged_by", "rationale")
			fmt.Fprintf(cmd.OutOrStdout(), "%-12s  %-36s  %-36s  %-14s  %s\n",
				"------------", "------------------------------------",
				"------------------------------------", "--------------", "---------")

			for _, r := range rels {
				fmt.Fprintf(cmd.OutOrStdout(), "%-12s  %-36s  %-36s  %-14s  %s\n",
					r.Relation, r.FromID, r.ToID, r.JudgedBy, r.Rationale)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagProject, "project", "", "Project slug (default: auto-detect)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")
	return cmd
}

// relationSymbol returns a short visual indicator for the given relation type.
func relationSymbol(relation string) string {
	switch relation {
	case "supersedes_a_over_b":
		return "[A→B supersedes]"
	case "supersedes_b_over_a":
		return "[B→A supersedes]"
	case "conflicts_with":
		return "[conflicts_with]"
	case "unrelated":
		return "[unrelated]"
	default:
		return "[" + relation + "]"
	}
}
