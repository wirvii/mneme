package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/juanftp/mneme/internal/service"
)

// AppModel is the top-level tea.Model for the mneme TUI. It owns the service
// reference, routes messages between screens, and renders the global status bar.
// All user input flows through Update; all rendering through View — following
// the Elm architecture enforced by Bubbletea.
type AppModel struct {
	svc    *service.MemoryService
	active screen
	keys   KeyMap

	list   ListModel
	detail DetailModel
	stats  StatsModel
	help   HelpModel

	width  int
	height int

	err      error  // last service error, shown in status bar for a short time
	errTimer bool   // true while the error dismiss timer is running
	statusMsg string // informational (non-error) status bar message
}

// NewApp constructs the root AppModel. Call tea.NewProgram(NewApp(svc)).Run()
// to launch the TUI.
func NewApp(svc *service.MemoryService) *AppModel {
	keys := DefaultKeyMap()
	return &AppModel{
		svc:    svc,
		active: screenList,
		keys:   keys,
		list:   newListModel(svc, keys),
		detail: newDetailModel(svc, keys),
		stats:  newStatsModel(svc, svc.ProjectSlug(), keys),
		help:   newHelpModel(keys),
	}
}

// Init launches the initial data load (list of memories).
func (a *AppModel) Init() tea.Cmd {
	return a.list.Init()
}

// Update is the single entry point for all messages. It dispatches to the
// active sub-model and intercepts inter-screen navigation messages.
func (a *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.list.SetSize(msg.Width, msg.Height-1) // -1 for status bar
		a.detail.width = msg.Width
		a.detail.height = msg.Height - 1
		a.stats.width = msg.Width
		a.stats.height = msg.Height - 1
		a.help.width = msg.Width
		a.help.height = msg.Height - 1
		// Re-init viewport with new size if already showing a memory.
		if a.detail.memory != nil {
			a.detail.initViewport()
		}

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}

	case navigateMsg:
		return a.handleNavigate(msg)

	case memoriesLoadedMsg:
		if msg.err != nil {
			return a, a.setError(msg.err)
		}
		// Pass through to list.
		var cmd tea.Cmd
		a.list, cmd = a.list.Update(msg)
		return a, cmd

	case statsLoadedMsg:
		if msg.err != nil {
			return a, a.setError(msg.err)
		}
		a.stats.stats = msg.stats
		a.stats.loading = false
		return a, nil

	case forgetDoneMsg:
		if msg.err != nil {
			return a, a.setError(msg.err)
		}
		a.statusMsg = "Memory forgotten."
		// Navigate back to list and reload.
		a.active = screenList
		return a, a.list.Init()

	case errClearedMsg:
		a.err = nil
		a.errTimer = false
		return a, nil
	}

	// Delegate to the active screen.
	return a.updateActiveScreen(msg)
}

// handleNavigate switches the active screen in response to a navigateMsg.
func (a *AppModel) handleNavigate(msg navigateMsg) (tea.Model, tea.Cmd) {
	prev := a.active
	a.active = msg.target

	switch msg.target {
	case screenList:
		a.statusMsg = ""
		// Reload list after returning from detail (e.g. after forget).
		return a, a.list.Init()

	case screenDetail:
		if msg.memory != nil {
			a.detail.SetMemory(msg.memory)
		}
		return a, nil

	case screenStats:
		a.stats.loading = true
		return a, loadStats(a.svc, a.svc.ProjectSlug())

	case screenHelp:
		a.help.returnTo = prev
		return a, nil
	}

	return a, nil
}

// updateActiveScreen delegates the message to the current screen's Update.
func (a *AppModel) updateActiveScreen(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch a.active {
	case screenList:
		a.list, cmd = a.list.Update(msg)
	case screenDetail:
		a.detail, cmd = a.detail.Update(msg)
	case screenStats:
		a.stats, cmd = a.stats.Update(msg)
	case screenHelp:
		a.help, cmd = a.help.Update(msg)
	}
	return a, cmd
}

// View renders the active screen plus the global status bar.
func (a *AppModel) View() string {
	if a.width == 0 {
		return "Initializing…"
	}

	var content string
	switch a.active {
	case screenList:
		content = a.list.View()
	case screenDetail:
		content = a.detail.View()
	case screenStats:
		content = a.stats.View()
	case screenHelp:
		content = a.help.View()
	}

	statusBar := a.renderStatusBar()

	// Pad content to fill the terminal minus the status bar row.
	contentLines := strings.Split(content, "\n")
	targetLines := max(a.height-1, 0)
	for len(contentLines) < targetLines {
		contentLines = append(contentLines, "")
	}
	// Trim to exactly targetLines.
	if len(contentLines) > targetLines {
		contentLines = contentLines[:targetLines]
	}
	content = strings.Join(contentLines, "\n")

	return content + "\n" + statusBar
}

// renderStatusBar builds the bottom status line. When a confirmation is pending
// in the detail screen it shows the confirm prompt instead of the normal hints.
func (a *AppModel) renderStatusBar() string {
	// Error state takes priority.
	if a.err != nil {
		msg := styleDanger.Render("Error: " + a.err.Error())
		return styleStatusBar.Width(a.width).Render(msg)
	}

	// Confirm state in detail screen.
	if a.active == screenDetail && a.detail.confirming {
		return styleStatusBar.Width(a.width).Render(a.detail.ConfirmMessage())
	}

	// Informational message (e.g. "Memory forgotten.")
	if a.statusMsg != "" {
		return styleStatusBar.Width(a.width).Render(styleSuccess.Render(a.statusMsg))
	}

	// Normal hints vary by screen.
	hints := a.hintsByScreen()
	project := styleAccent.Render("[mneme]") + " " +
		styleSubtle.Render("project: "+a.svc.ProjectSlug())

	count := ""
	if a.active == screenList {
		n := len(a.list.memories)
		count = styleSubtle.Render(fmt.Sprintf(" | %d memories", n))
	}

	left := project + count
	right := styleSubtle.Render(hints)

	gap := max(a.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1) // 2 for padding
	bar := left + strings.Repeat(" ", gap) + right
	return styleStatusBar.Width(a.width).Render(bar)
}

// hintsByScreen returns the key-hint string appropriate for the active screen.
func (a *AppModel) hintsByScreen() string {
	switch a.active {
	case screenList:
		return "tab:type  shift+tab:scope  s:sort  /:search  S:stats  ?:help  q:quit"
	case screenDetail:
		return "j/k:scroll  d:forget  q:back  ?:help"
	case screenStats:
		return "r:refresh  q:back  ?:help"
	case screenHelp:
		return "any key:close"
	default:
		return "ctrl+c:quit"
	}
}

// setError stores the error and returns a tea.Cmd that clears it after 3 seconds.
// Callers must return this command from Update to wire up the auto-dismiss timer.
func (a *AppModel) setError(err error) tea.Cmd {
	a.err = err
	if a.errTimer {
		// Timer already running; don't stack multiple timers.
		return nil
	}
	a.errTimer = true
	return clearErrorAfter(3 * time.Second)
}

// clearErrorAfter returns a tea.Cmd that fires errClearedMsg after d.
func clearErrorAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return errClearedMsg{}
	})
}
