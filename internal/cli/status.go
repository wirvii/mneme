package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// newStatusCmd returns the "mneme status" subcommand. It displays a unified
// dashboard combining memory stats with the SDD backlog and spec pipeline.
// When the SDD store is unavailable (e.g. migration 004 not yet applied),
// it falls back to a minimal view showing only memory statistics.
func newStatusCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show mneme status and project dashboard",
		Long: `Show the current mneme status including the detected project,
database path, memory counts, backlog items, and spec pipeline state.

Falls back to basic memory stats if the SDD engine is not yet initialised.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx := context.Background()

			sddSvc, sddCleanup, sddErr := initSDDService()
			if sddErr != nil {
				// SDD not available — show basic status only.
				return renderBasicStatus(ctx, svc, flagJSON)
			}
			defer sddCleanup()

			return renderFullStatus(ctx, svc, sddSvc, flagJSON)
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// renderBasicStatus displays memory-only statistics. This is the fallback
// when the SDD engine tables are not present.
func renderBasicStatus(ctx context.Context, svc *service.MemoryService, asJSON bool) error {
	cfg := svc.Config()
	slug := svc.ProjectSlug()

	projectCount, _ := svc.Count(ctx, slug)
	globalCount := 0
	if n, err := svc.CountGlobal(ctx); err == nil {
		globalCount = n
	}

	var dbPath string
	if slug != "" {
		dbPath = cfg.ProjectDBPath(slug)
	} else {
		dbPath = cfg.GlobalDBPath()
	}
	dbPath = shortenHome(dbPath)

	if asJSON {
		type basicOut struct {
			Version      string `json:"version"`
			Project      string `json:"project"`
			Database     string `json:"database"`
			ProjectCount int    `json:"project_memories"`
			GlobalCount  int    `json:"global_memories"`
		}
		return printJSON(os.Stdout, basicOut{
			Version:      Version,
			Project:      slug,
			Database:     dbPath,
			ProjectCount: projectCount,
			GlobalCount:  globalCount,
		})
	}

	fmt.Fprintf(os.Stdout, "mneme v%s\n\n", Version)
	if slug != "" {
		fmt.Fprintf(os.Stdout, "Project:  %s\n", slug)
	} else {
		fmt.Fprintf(os.Stdout, "Project:  (none detected — using global database)\n")
	}
	fmt.Fprintf(os.Stdout, "Database: %s\n\n", dbPath)
	fmt.Fprintf(os.Stdout, "Memories: %d active\n", projectCount)
	fmt.Fprintf(os.Stdout, "Global:   %d memories\n", globalCount)
	return nil
}

// renderFullStatus displays the unified SDD dashboard with backlog, specs, and
// memory statistics.
func renderFullStatus(ctx context.Context, svc *service.MemoryService, sddSvc *service.SDDService, asJSON bool) error {
	cfg := svc.Config()
	slug := svc.ProjectSlug()

	projectCount, _ := svc.Count(ctx, slug)
	globalCount := 0
	if n, err := svc.CountGlobal(ctx); err == nil {
		globalCount = n
	}

	// Fetch backlog items (exclude archived). Limit stays zero (no window,
	// SPEC-109 D9) — the dashboard wants the same full-fidelity list as
	// before. Discarding the error is safe because BacklogListResponse is
	// returned BY VALUE (D10): its zero value has a nil Items slice, and
	// ranging over nil is zero iterations, not a nil-deref.
	blResp, _ := sddSvc.BacklogList(ctx, model.BacklogListRequest{
		Project: slug,
	})
	var activeBacklog []*model.BacklogItem
	for _, item := range blResp.Items {
		if item.Status != model.BacklogStatusArchived {
			activeBacklog = append(activeBacklog, item)
		}
	}

	// Fetch all specs and split into in-progress vs done.
	spResp, _ := sddSvc.SpecList(ctx, model.SpecListRequest{Project: slug})
	var inProgressSpecs []*model.Spec
	for _, s := range spResp.Specs {
		if !s.Status.IsFinal() && s.Status != model.SpecStatusDraft {
			inProgressSpecs = append(inProgressSpecs, s)
		}
	}

	// SPEC-126 AC20: filter spResp.Frozen down to only the specs actually
	// shown in SPECS IN PROGRESS below — this dashboard NEVER recomputes the
	// freeze itself (that would be a second definition of "archived" outside
	// specFreeze's structural guardian, DD3); it only decorates what SpecList
	// already decided.
	var frozenInProgress map[string]model.SpecFreeze
	for _, s := range inProgressSpecs {
		freeze, ok := spResp.Frozen[s.ID]
		if !ok {
			continue
		}
		if frozenInProgress == nil {
			frozenInProgress = make(map[string]model.SpecFreeze, len(inProgressSpecs))
		}
		frozenInProgress[s.ID] = freeze
	}

	recentDone, recentUnreadable, _ := sddSvc.RecentlyCompletedSpecs(ctx, slug, 5)
	// TODO(SPEC-133 paso 3): recentUnreadable is not yet surfaced anywhere in
	// this panel — cli/status.go's own announcement (D9) is step 3's job,
	// not this commit's. Captured under its own name (never `_`) so this
	// call stays visible to AC12's guardian in the meantime.
	_ = recentUnreadable

	// SPEC-086 D7/AC15: best-effort delegation-hook promotion nag. Reuses
	// svc's already-open connection (service.NewSubagentService(svc)) rather
	// than opening a second one — any failure resolves to "" (no nag),
	// consistent with the rest of this command's fail-open-on-warnings
	// posture (backlog/spec fetches above also swallow their own errors).
	nagLine := delegationNagLine(ctx, cfg, slug, service.NewSubagentService(svc))

	if asJSON {
		type fullOut struct {
			Version       string               `json:"version"`
			Project       string               `json:"project"`
			Database      string               `json:"database"`
			DelegationNag string               `json:"delegation_nag,omitempty"`
			ProjectCount  int                  `json:"project_memories"`
			GlobalCount   int                  `json:"global_memories"`
			Backlog       []*model.BacklogItem `json:"backlog"`
			InProgress    []*model.Spec        `json:"specs_in_progress"`
			RecentDone    []*model.Spec        `json:"recently_completed"`
			// FrozenSpecs is a sibling key (SPEC-126 AC20/AC21), NOT a field
			// inside each spec: an entry for every spec in InProgress that
			// can no longer change status, keyed by spec ID. Absent (nil
			// map, omitempty) when none is — Backlog and InProgress stay
			// bare arrays exactly as TestStatus_JSONBacklogAndSpecsAreBareArrays
			// already pins, and this is the only new key.
			FrozenSpecs map[string]model.SpecFreeze `json:"frozen_specs,omitempty"`
		}
		var dbPath string
		if slug != "" {
			dbPath = cfg.ProjectDBPath(slug)
		} else {
			dbPath = cfg.GlobalDBPath()
		}
		return printJSON(os.Stdout, fullOut{
			Version:       Version,
			Project:       slug,
			Database:      shortenHome(dbPath),
			DelegationNag: nagLine,
			ProjectCount:  projectCount,
			GlobalCount:   globalCount,
			Backlog:       activeBacklog,
			InProgress:    inProgressSpecs,
			RecentDone:    recentDone,
			FrozenSpecs:   frozenInProgress,
		})
	}

	// --- Human-readable output ---

	header := fmt.Sprintf("mneme v%s", Version)
	if slug != "" {
		header += fmt.Sprintf(" — %s", slug)
	}
	fmt.Fprintln(os.Stdout, header)
	fmt.Fprintln(os.Stdout)

	// DELEGATION NAG (SPEC-086 D7/AC15) — printed right after the header,
	// before backlog/specs, so it is impossible to miss: this is the
	// antidote to a project sitting in "warn" forever with evidence nobody
	// looked at.
	if nagLine != "" {
		fmt.Fprintln(os.Stdout, section("DELEGATION", 50))
		fmt.Fprintf(os.Stdout, "  %s\n", nagLine)
		fmt.Fprintln(os.Stdout)
	}

	// BACKLOG section
	if len(activeBacklog) > 0 {
		fmt.Fprintln(os.Stdout, section("BACKLOG", 50))
		for _, item := range activeBacklog {
			fmt.Fprintf(os.Stdout, "  %-8s  %-12s  %-40s  %s\n",
				item.ID,
				statusTag(string(item.Status)),
				truncate(item.Title, 40),
				string(item.Priority),
			)
		}
		fmt.Fprintln(os.Stdout)
	}

	// SPECS IN PROGRESS section
	if len(inProgressSpecs) > 0 {
		fmt.Fprintln(os.Stdout, section("SPECS IN PROGRESS", 50))
		for _, s := range inProgressSpecs {
			age := time.Since(s.UpdatedAt).Truncate(time.Minute)
			fmt.Fprintf(os.Stdout, "  %-10s  %-16s  %s\n",
				s.ID,
				statusTag(string(s.Status)),
				s.Title,
			)
			// SPEC-126 AC20: the mark is added to the SAME second line this
			// section already prints — a frozen spec is NOT pulled out of
			// this section (hiding it would be the very defect this spec
			// fixes, from the other side).
			mark := ""
			if freeze, ok := frozenInProgress[s.ID]; ok {
				mark = "  ·  " + frozenDashboardNote(freeze)
			}
			fmt.Fprintf(os.Stdout, "    updated %s ago%s\n\n", formatAge(age), mark)
		}
	}

	// RECENTLY COMPLETED section
	if len(recentDone) > 0 {
		fmt.Fprintln(os.Stdout, section("RECENTLY COMPLETED", 50))
		for _, s := range recentDone {
			fmt.Fprintf(os.Stdout, "  %-10s  %-8s  %-40s  %s\n",
				s.ID,
				statusTag(string(s.Status)),
				truncate(s.Title, 40),
				s.UpdatedAt.Format("2006-01-02"),
			)
		}
		fmt.Fprintln(os.Stdout)
	}

	// MEMORIES section
	fmt.Fprintln(os.Stdout, section("MEMORIES", 50))
	fmt.Fprintf(os.Stdout, "  %d project - %d global\n", projectCount, globalCount)
	fmt.Fprintln(os.Stdout)

	return nil
}

// frozenDashboardNote is the short mark SPECS IN PROGRESS adds to a frozen
// spec's second line (SPEC-126 DD7/AC20) — dense enough to fit alongside
// "updated Xd ago", never a date (backlog_items has no archived-at instant
// to show), and pointing at "mneme spec status" for the full explanation
// rather than repeating it here.
func frozenDashboardNote(freeze model.SpecFreeze) string {
	if freeze.State == model.SpecFreezeMissing {
		return fmt.Sprintf("frozen: %s is missing from this database, so this status can no longer change", freeze.BacklogID)
	}
	return fmt.Sprintf("frozen: %s was archived, so this status can no longer change", freeze.BacklogID)
}

// section returns a section header line with a fixed width divider.
func section(title string, width int) string {
	dashes := strings.Repeat("-", width-len(title)-4)
	return fmt.Sprintf("--- %s %s", title, dashes)
}

// statusTag returns a fixed-width bracketed status string.
func statusTag(status string) string {
	return fmt.Sprintf("[%s]", status)
}

// shortenHome replaces the home directory prefix with "~" for readability.
func shortenHome(path string) string {
	home, _ := os.UserHomeDir()
	if home == "" || len(path) <= len(home) {
		return path
	}
	if path[:len(home)] == home {
		return "~" + path[len(home):]
	}
	return path
}

// formatAge formats a duration as a human-readable string.
func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "< 1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
