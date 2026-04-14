package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds all key bindings used across TUI screens. A single KeyMap
// instance is shared via the AppModel so every screen speaks the same controls.
type KeyMap struct {
	// Navigation
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Home     key.Binding
	End      key.Binding

	// Actions
	Enter  key.Binding
	Back   key.Binding
	Quit   key.Binding
	Forget key.Binding
	Help   key.Binding
	Refresh key.Binding

	// List-specific
	Search      key.Binding
	ClearSearch key.Binding
	Sort        key.Binding
	TypeFilter  key.Binding
	ScopeFilter key.Binding
	Stats       key.Binding
}

// DefaultKeyMap returns the standard key bindings for mneme TUI. All bindings
// use lower-case letters (vim-style) plus terminal-standard arrow/control keys.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("ctrl+u", "pgup"),
			key.WithHelp("ctrl+u/pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("ctrl+d", "pgdn"),
			key.WithHelp("ctrl+d/pgdn", "page down"),
		),
		Home: key.NewBinding(
			key.WithKeys("g", "home"),
			key.WithHelp("g/home", "go to start"),
		),
		End: key.NewBinding(
			key.WithKeys("G", "end"),
			key.WithHelp("G/end", "go to end"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "open detail"),
		),
		Back: key.NewBinding(
			key.WithKeys("q", "esc"),
			key.WithHelp("q/esc", "back/quit"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Forget: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "forget memory"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		ClearSearch: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "clear search"),
		),
		Sort: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "cycle sort"),
		),
		TypeFilter: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "filter by type"),
		),
		ScopeFilter: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "filter by scope"),
		),
		Stats: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "stats"),
		),
	}
}
