package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
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

	// Fetch backlog items (exclude archived).
	backlogItems, _ := sddSvc.BacklogList(ctx, model.BacklogListRequest{
		Project: slug,
	})
	var activeBacklog []*model.BacklogItem
	for _, item := range backlogItems {
		if item.Status != model.BacklogStatusArchived {
			activeBacklog = append(activeBacklog, item)
		}
	}

	// Fetch all specs and split into in-progress vs done.
	allSpecs, _ := sddSvc.SpecList(ctx, model.SpecListRequest{Project: slug})
	var inProgressSpecs []*model.Spec
	for _, s := range allSpecs {
		if !s.Status.IsFinal() && s.Status != model.SpecStatusDraft {
			inProgressSpecs = append(inProgressSpecs, s)
		}
	}

	recentDone, _ := sddSvc.RecentlyCompletedSpecs(ctx, slug, 5)

	if asJSON {
		type fullOut struct {
			Version      string              `json:"version"`
			Project      string              `json:"project"`
			Database     string              `json:"database"`
			ProjectCount int                 `json:"project_memories"`
			GlobalCount  int                 `json:"global_memories"`
			Backlog      []*model.BacklogItem `json:"backlog"`
			InProgress   []*model.Spec        `json:"specs_in_progress"`
			RecentDone   []*model.Spec        `json:"recently_completed"`
		}
		var dbPath string
		if slug != "" {
			dbPath = cfg.ProjectDBPath(slug)
		} else {
			dbPath = cfg.GlobalDBPath()
		}
		return printJSON(os.Stdout, fullOut{
			Version:      Version,
			Project:      slug,
			Database:     shortenHome(dbPath),
			ProjectCount: projectCount,
			GlobalCount:  globalCount,
			Backlog:      activeBacklog,
			InProgress:   inProgressSpecs,
			RecentDone:   recentDone,
		})
	}

	// --- Human-readable output ---

	header := fmt.Sprintf("mneme v%s", Version)
	if slug != "" {
		header += fmt.Sprintf(" — %s", slug)
	}
	fmt.Fprintln(os.Stdout, header)
	fmt.Fprintln(os.Stdout)

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
			fmt.Fprintf(os.Stdout, "    updated %s ago\n\n", formatAge(age))
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
