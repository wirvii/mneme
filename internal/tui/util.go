package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// truncate shortens s to maxLen runes, appending "..." if truncation occurred.
// It works on rune boundaries to avoid splitting multi-byte characters.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// formatDate formats t as "2006-01-02" in UTC. Returns "" for zero times.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// formatDateTime formats t as "2006-01-02 15:04:05 UTC". Returns "" for zero times.
func formatDateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// formatImportance renders importance as a two-decimal string (e.g. "0.85").
func formatImportance(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

// formatBytes formats a byte count as a human-readable string with appropriate
// units (B, KB, MB). mneme DBs are typically small so MB is the ceiling.
func formatBytes(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// sortFields is the ordered list of sort field identifiers that the list screen
// cycles through when the user presses 's'.
var sortFields = []string{"importance", "created_at", "type"}

// cycleSortField returns the next sort field after current in the canonical cycle.
func cycleSortField(current string) string {
	for i, f := range sortFields {
		if f == current {
			return sortFields[(i+1)%len(sortFields)]
		}
	}
	return sortFields[0]
}

// sortFieldLabel returns a human-readable label for the sort field.
func sortFieldLabel(field string) string {
	switch field {
	case "importance":
		return "importance"
	case "created_at":
		return "date"
	case "type":
		return "type"
	default:
		return field
	}
}

// filterByType returns the subset of memories that match t. When t is empty
// all memories are returned unchanged.
func filterByType(memories []*model.Memory, t model.MemoryType) []*model.Memory {
	if t == "" {
		return memories
	}
	out := make([]*model.Memory, 0, len(memories))
	for _, m := range memories {
		if m.Type == t {
			out = append(out, m)
		}
	}
	return out
}

// filterByScope returns the subset of memories that match s. When s is empty
// all memories are returned unchanged.
func filterByScope(memories []*model.Memory, s model.Scope) []*model.Memory {
	if s == "" {
		return memories
	}
	out := make([]*model.Memory, 0, len(memories))
	for _, m := range memories {
		if m.Scope == s {
			out = append(out, m)
		}
	}
	return out
}

// applyFilters applies both type and scope filters client-side on allMemories.
func applyFilters(all []*model.Memory, t model.MemoryType, s model.Scope) []*model.Memory {
	result := filterByType(all, t)
	result = filterByScope(result, s)
	return result
}

// cycleTypeFilter advances typeFilter to the next value in AllMemoryTypes().
// The cycle is: "" -> decision -> discovery -> ... -> session_summary -> "".
func cycleTypeFilter(current model.MemoryType) model.MemoryType {
	types := model.AllMemoryTypes()
	if current == "" {
		return types[0]
	}
	for i, t := range types {
		if t == current {
			next := i + 1
			if next >= len(types) {
				return ""
			}
			return types[next]
		}
	}
	return ""
}

// cycleScopeFilter advances scopeFilter through the known scopes.
// The cycle is: "" -> project -> global -> org -> "".
func cycleScopeFilter(current model.Scope) model.Scope {
	scopes := []model.Scope{model.ScopeProject, model.ScopeGlobal, model.ScopeOrg}
	if current == "" {
		return scopes[0]
	}
	for i, s := range scopes {
		if s == current {
			next := i + 1
			if next >= len(scopes) {
				return ""
			}
			return scopes[next]
		}
	}
	return ""
}

// typeFilterLabel returns a human-readable label for the current type filter.
func typeFilterLabel(t model.MemoryType) string {
	if t == "" {
		return "all types"
	}
	return string(t)
}

// scopeFilterLabel returns a human-readable label for the current scope filter.
func scopeFilterLabel(s model.Scope) string {
	if s == "" {
		return "all scopes"
	}
	return string(s)
}

// renderFiles formats the files slice as a comma-separated string for display.
func renderFiles(files []string) string {
	if len(files) == 0 {
		return "(none)"
	}
	return strings.Join(files, ", ")
}
