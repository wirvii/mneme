package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// HelpModel renders an overlay with all key bindings. It is shown on top of the
// currently active screen when the user presses '?'.
type HelpModel struct {
	width  int
	height int
	keys   KeyMap

	// returnTo is the screen to go back to when the overlay is dismissed.
	returnTo screen
}

// newHelpModel constructs a HelpModel.
func newHelpModel(keys KeyMap) HelpModel {
	return HelpModel{keys: keys}
}

// Init is a no-op for the help overlay.
func (m HelpModel) Init() tea.Cmd { return nil }

// Update handles key input for the help overlay. Any key dismisses it.
func (m HelpModel) Update(msg tea.Msg) (HelpModel, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		ws := msg.(tea.WindowSizeMsg)
		m.width = ws.Width
		m.height = ws.Height
		return m, nil
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		_ = km
		return m, func() tea.Msg {
			return navigateMsg{target: m.returnTo}
		}
	}

	return m, nil
}

// View renders the full help table as a centred overlay.
func (m HelpModel) View() string {
	content := m.renderContent()
	return content
}

func (m HelpModel) renderContent() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("Key Bindings") + "\n\n")

	type row struct{ key, desc string }
	sections := []struct {
		title string
		rows  []row
	}{
		{
			title: "Navigation",
			rows: []row{
				{"↑ / k", "Move up"},
				{"↓ / j", "Move down"},
				{"ctrl+u / pgup", "Page up"},
				{"ctrl+d / pgdn", "Page down"},
				{"g / home", "Go to start"},
				{"G / end", "Go to end"},
			},
		},
		{
			title: "List",
			rows: []row{
				{"/", "Search"},
				{"esc", "Clear search / cancel"},
				{"tab", "Cycle type filter"},
				{"shift+tab", "Cycle scope filter"},
				{"s", "Cycle sort field"},
				{"r", "Refresh data"},
				{"S", "Open stats"},
			},
		},
		{
			title: "Detail",
			rows: []row{
				{"enter", "Open detail"},
				{"j / k", "Scroll content"},
				{"d", "Forget memory (confirm)"},
				{"q / esc", "Back to list"},
			},
		},
		{
			title: "Global",
			rows: []row{
				{"?", "Toggle help"},
				{"ctrl+c", "Quit immediately"},
			},
		},
	}

	for _, sec := range sections {
		b.WriteString(styleHeader.Render(sec.title) + "\n")
		for _, r := range sec.rows {
			b.WriteString("  " + styleAccent.Render(padRight(r.key, 18)) + "  " + r.desc + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(styleSubtle.Render("Press any key to close"))
	return b.String()
}
