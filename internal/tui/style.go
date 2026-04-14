// Package tui provides the interactive terminal user interface for mneme.
// It is built on the Bubbletea framework using the Elm architecture (Model /
// Update / View). The AppModel is the root tea.Model that owns the service
// reference and routes between the list, detail, stats, and help screens.
package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/juanftp/mneme/internal/model"
)

// Adaptive colors work on both light and dark terminal themes. lipgloss /
// termenv detects the terminal background automatically and picks the
// appropriate value.
var (
	colorSubtle    = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#666666"}
	colorHighlight = lipgloss.AdaptiveColor{Light: "#1a1a2e", Dark: "#e0def4"}
	colorAccent    = lipgloss.AdaptiveColor{Light: "#0066cc", Dark: "#89b4fa"}
	colorDanger    = lipgloss.AdaptiveColor{Light: "#cc0000", Dark: "#f38ba8"}
	colorSuccess   = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#a6e3a1"}

	// typeColors maps each MemoryType to a distinct adaptive color so rows can
	// be scanned at a glance without reading the type label.
	typeColors = map[model.MemoryType]lipgloss.AdaptiveColor{
		model.TypeDecision:       {Light: "#7c3aed", Dark: "#cba6f7"},
		model.TypeDiscovery:      {Light: "#0891b2", Dark: "#89dceb"},
		model.TypeBugfix:         {Light: "#dc2626", Dark: "#f38ba8"},
		model.TypePattern:        {Light: "#059669", Dark: "#a6e3a1"},
		model.TypePreference:     {Light: "#d97706", Dark: "#fab387"},
		model.TypeConvention:     {Light: "#2563eb", Dark: "#89b4fa"},
		model.TypeArchitecture:   {Light: "#7c3aed", Dark: "#b4befe"},
		model.TypeConfig:         {Light: "#525252", Dark: "#a6adc8"},
		model.TypeSessionSummary: {Light: "#888888", Dark: "#6c7086"},
	}
)

// Styles used across all TUI screens. Defined at package level so every screen
// shares the same visual language without re-constructing styles on each render.
var (
	styleTitle = lipgloss.NewStyle().Bold(true)

	styleSubtle = lipgloss.NewStyle().
			Foreground(colorSubtle)

	styleSelected = lipgloss.NewStyle().
			Background(colorAccent).
			Foreground(lipgloss.Color("#ffffff"))

	styleDanger = lipgloss.NewStyle().
			Foreground(colorDanger)

	styleSuccess = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleAccent = lipgloss.NewStyle().
			Foreground(colorAccent)

	styleStatusBar = lipgloss.NewStyle().
			Padding(0, 1).
			Background(lipgloss.AdaptiveColor{Light: "#dddddd", Dark: "#313244"}).
			Foreground(colorHighlight)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	styleColumnHeader = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorSubtle).
				Underline(true)
)

// typeColor returns the lipgloss foreground style for the given MemoryType.
// Falls back to colorSubtle for unknown types so the UI never panics.
func typeColor(t model.MemoryType) lipgloss.Style {
	if c, ok := typeColors[t]; ok {
		return lipgloss.NewStyle().Foreground(c)
	}
	return styleSubtle
}
