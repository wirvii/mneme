package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/skill"
)

const (
	hoursPerDay     = 24
	usageDateLayout = "2006-01-02"
)

// parseUsageSince turns a --since value (30d, 12h or YYYY-MM-DD) into the
// start of the window. An empty value means the whole history (zero time).
func parseUsageSince(v string, now time.Time) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	invalid := fmt.Errorf("--since: valor no válido %q (usa 30d, 12h o 2026-10-01)", v)
	if t, err := time.Parse(usageDateLayout, v); err == nil {
		return t.UTC(), nil
	}
	if len(v) < 2 {
		return time.Time{}, invalid
	}
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n < 0 {
		return time.Time{}, invalid
	}
	switch v[len(v)-1] {
	case 'd':
		return now.Add(-time.Duration(n) * hoursPerDay * time.Hour), nil
	case 'h':
		return now.Add(-time.Duration(n) * time.Hour), nil
	}
	return time.Time{}, invalid
}

// skillsUsageJSON is the --json shape of "mneme skills usage".
type skillsUsageJSON struct {
	Path      string             `json:"path"`
	Since     *string            `json:"since"`
	Skills    []skill.SkillUsage `json:"skills"`
	NeverUsed []string           `json:"never_used"`
	Skipped   int                `json:"skipped"`
}

// newSkillsUsageCmd returns the "mneme skills usage" subcommand.
func newSkillsUsageCmd() *cobra.Command {
	var (
		flagSince string
		flagJSON  bool
	)

	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Muestra qué skills se han cargado y cuáles nunca",
		Long: `Agrega el registro local de cargas de skills (skill-usage.jsonl en el
directorio de datos de mneme). Lo escribe el hook PostToolUse que instala
"mneme install claude-code"; en Codex no se registra.

--since acepta 30d, 12h o una fecha (2026-10-01). Sin él, todo el historial.`,
		Example: `  mneme skills usage
  mneme skills usage --since 30d
  mneme skills usage --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := parseUsageSince(flagSince, time.Now())
			if err != nil {
				return err
			}
			logPath, err := skillUsageLogPath()
			if err != nil {
				return err
			}
			svc, err := newSkillsSvc()
			if err != nil {
				return err
			}
			infos, err := svc.List()
			if err != nil {
				return err
			}
			known := make([]string, 0, len(infos))
			for _, info := range infos {
				known = append(known, info.Name)
			}
			log := service.NewSkillUsageLog(logPath)
			sum, err := log.Summary(since, known)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd.OutOrStdout(), usageJSON(log.Path(), sum))
			}
			return writeUsageText(cmd.OutOrStdout(), log.Path(), sum)
		},
	}

	cmd.Flags().StringVar(&flagSince, "since", "", "Solo cargas desde: 30d, 12h o YYYY-MM-DD (vacío = todo)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Salida en JSON")

	return cmd
}

// usageJSON maps a summary to its stable JSON shape (never null lists).
func usageJSON(path string, sum skill.UsageSummary) skillsUsageJSON {
	out := skillsUsageJSON{Path: path, Skills: sum.Skills, NeverUsed: sum.NeverUsed, Skipped: sum.Skipped}
	if out.Skills == nil {
		out.Skills = []skill.SkillUsage{}
	}
	if out.NeverUsed == nil {
		out.NeverUsed = []string{}
	}
	if sum.Since != nil {
		s := sum.Since.UTC().Format(time.RFC3339)
		out.Since = &s
	}
	return out
}

// writeUsageText prints the human-readable report (Spanish, for a person).
func writeUsageText(w io.Writer, path string, sum skill.UsageSummary) error {
	if len(sum.Skills) == 0 {
		fmt.Fprintln(w, `Todavía no hay cargas registradas. El registro lo escribe el hook PostToolUse que instala "mneme install claude-code"; en Codex no se registra.`)
	} else {
		header := "Uso de skills"
		if sum.Since != nil {
			header += " desde " + sum.Since.UTC().Format(usageDateLayout)
		}
		fmt.Fprintf(w, "%s (registro: %s)\n", header, path)
		fmt.Fprintf(w, "%-24s %-7s %-9s %-11s %s\n", "SKILL", "CARGAS", "SESIONES", "ÚLTIMA", "ROLES")
		for _, u := range sum.Skills {
			fmt.Fprintf(w, "%-24s %-7d %-9d %-11s %s\n",
				u.Name, u.Loads, u.Sessions, u.LastUsed.UTC().Format(usageDateLayout), formatUsageRoles(u.Roles))
		}
	}
	if len(sum.NeverUsed) > 0 {
		fmt.Fprintf(w, "Nunca usadas: %s\n", strings.Join(sum.NeverUsed, ", "))
	}
	if sum.Skipped > 0 {
		fmt.Fprintf(w, "Aviso: %d líneas ilegibles ignoradas.\n", sum.Skipped)
	}
	return nil
}

// formatUsageRoles renders role:count pairs, most frequent first.
func formatUsageRoles(roles map[string]int) string {
	names := make([]string, 0, len(roles))
	for r := range roles {
		names = append(names, r)
	}
	sort.Slice(names, func(i, j int) bool {
		if roles[names[i]] != roles[names[j]] {
			return roles[names[i]] > roles[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, len(names))
	for i, r := range names {
		parts[i] = fmt.Sprintf("%s:%d", r, roles[r])
	}
	return strings.Join(parts, " ")
}
