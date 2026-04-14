package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// ListModel is the main screen that shows the memory list with search, filter,
// and sort capabilities. It owns the full memory data and computes the visible
// (filtered) view client-side without round-tripping to the store.
type ListModel struct {
	svc *service.MemoryService

	// allMemories holds the full dataset loaded from the store. Filters are
	// applied on top of this slice — not by re-querying the DB.
	allMemories []*model.Memory

	// memories is the filtered / searched view rendered to the user.
	memories []*model.Memory

	cursor int // index into memories of the selected row
	offset int // scroll offset: index of the first visible row

	search    textinput.Model
	searching bool // true when the search text-input is focused

	typeFilter  model.MemoryType
	scopeFilter model.Scope
	sortField   string // "importance" | "created_at" | "type"

	width  int
	height int

	keys KeyMap
}

// newListModel constructs a ListModel backed by svc.
func newListModel(svc *service.MemoryService, keys KeyMap) ListModel {
	ti := textinput.New()
	ti.Placeholder = "search memories…"
	ti.CharLimit = 200

	return ListModel{
		svc:       svc,
		sortField: "importance",
		search:    ti,
		keys:      keys,
	}
}

// Init returns the initial command that loads all memories from the store.
func (m ListModel) Init() tea.Cmd {
	return loadMemories(m.svc, store.ListOptions{Limit: 200, OrderBy: sortOrderBy(m.sortField)})
}

// sortOrderBy returns the SQL ORDER BY clause for the given sort field,
// applying DESC for importance and created_at (highest/newest first) and
// ASC for type (alphabetical).
func sortOrderBy(field string) string {
	switch field {
	case "type":
		return "type ASC"
	default:
		// "importance" and "created_at" both sort descending.
		return field + " DESC"
	}
}

// Update handles messages for the list screen. It returns the updated model
// and any commands to run. Navigation messages are emitted as navigateMsg so
// AppModel can switch screens.
func (m ListModel) Update(msg tea.Msg) (ListModel, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case memoriesLoadedMsg:
		if msg.err != nil {
			// Propagate the error up — AppModel will display it in the status bar.
			return m, func() tea.Msg { return msg }
		}
		m.allMemories = msg.memories
		m.refilter()

	case forgetDoneMsg:
		// After a successful forget, reload the list so the forgotten memory
		// disappears without a manual refresh.
		if msg.err == nil {
			return m, loadMemories(m.svc, store.ListOptions{Limit: 200, OrderBy: sortOrderBy(m.sortField)})
		}

	case tea.KeyMsg:
		if m.searching {
			return m.updateSearch(msg)
		}
		return m.updateNormal(msg)
	}

	return m, nil
}

// updateSearch handles key input while the search input is focused.
func (m ListModel) updateSearch(msg tea.KeyMsg) (ListModel, tea.Cmd) {
	switch {
	case msg.String() == "esc":
		// Clear search and restore the full filtered list.
		m.searching = false
		m.search.Blur()
		m.search.SetValue("")
		m.refilter()
		m.cursor = 0
		m.offset = 0
		return m, nil

	case msg.String() == "enter":
		// Commit search — keep results, close input.
		m.searching = false
		m.search.Blur()
		return m, nil
	}

	// Feed the key to the text input and trigger a search.
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)

	q := m.search.Value()
	if q == "" {
		m.refilter()
		return m, cmd
	}

	// Real search via the service — async tea.Cmd.
	return m, tea.Batch(cmd, searchMemories(m.svc, q))
}

// updateNormal handles key input in normal (non-search) mode.
func (m ListModel) updateNormal(msg tea.KeyMsg) (ListModel, tea.Cmd) {
	switch {
	case msg.String() == "/":
		m.searching = true
		m.search.Focus()
		return m, textinput.Blink

	case msg.String() == "r":
		return m, loadMemories(m.svc, store.ListOptions{Limit: 200, OrderBy: sortOrderBy(m.sortField)})

	case msg.String() == "s":
		m.sortField = cycleSortField(m.sortField)
		return m, loadMemories(m.svc, store.ListOptions{Limit: 200, OrderBy: sortOrderBy(m.sortField)})

	case msg.String() == "tab":
		m.typeFilter = cycleTypeFilter(m.typeFilter)
		m.refilter()
		m.cursor = 0
		m.offset = 0
		return m, nil

	case msg.String() == "shift+tab":
		m.scopeFilter = cycleScopeFilter(m.scopeFilter)
		m.refilter()
		m.cursor = 0
		m.offset = 0
		return m, nil

	case msg.String() == "up", msg.String() == "k":
		m.moveCursor(-1)

	case msg.String() == "down", msg.String() == "j":
		m.moveCursor(1)

	case msg.String() == "ctrl+u", msg.String() == "pgup":
		m.moveCursor(-m.visibleRows())

	case msg.String() == "ctrl+d", msg.String() == "pgdn":
		m.moveCursor(m.visibleRows())

	case msg.String() == "g", msg.String() == "home":
		m.cursor = 0
		m.offset = 0

	case msg.String() == "G", msg.String() == "end":
		if len(m.memories) > 0 {
			m.cursor = len(m.memories) - 1
			m.scrollToCursor()
		}

	case msg.String() == "enter":
		if len(m.memories) > 0 {
			return m, func() tea.Msg {
				return navigateMsg{target: screenDetail, memory: m.memories[m.cursor]}
			}
		}

	case msg.String() == "S":
		return m, func() tea.Msg {
			return navigateMsg{target: screenStats}
		}

	case msg.String() == "?":
		return m, func() tea.Msg {
			return navigateMsg{target: screenHelp}
		}

	case msg.String() == "q":
		return m, tea.Quit
	}

	return m, nil
}

// View renders the list screen. Column widths are computed dynamically from
// the available terminal width so the layout adapts to any terminal size.
func (m ListModel) View() string {
	if m.width == 0 {
		return "Loading…"
	}

	var b strings.Builder

	// --- Header / search bar ---
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// --- Column headers ---
	b.WriteString(m.renderColumnHeaders())
	b.WriteString("\n")

	// --- Rows ---
	rows := m.renderRows()
	b.WriteString(rows)

	return b.String()
}

func (m ListModel) renderHeader() string {
	typeLabel := typeFilterLabel(m.typeFilter)
	scopeLabel := scopeFilterLabel(m.scopeFilter)
	sort := sortFieldLabel(m.sortField)

	filters := fmt.Sprintf("type:%s  scope:%s  sort:%s", typeLabel, scopeLabel, sort)

	if m.searching {
		return styleAccent.Render("Search: ") + m.search.View()
	}

	total := fmt.Sprintf("%d memories", len(m.memories))
	right := styleSubtle.Render(filters)
	left := styleTitle.Render("Memories") + "  " + styleSubtle.Render(total)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	gap = max(gap, 1)
	return left + strings.Repeat(" ", gap) + right
}

func (m ListModel) renderColumnHeaders() string {
	typeW, titleW, impW, dateW := m.columnWidths()
	header := styleColumnHeader.Render(padRight("TYPE", typeW)) + "  " +
		styleColumnHeader.Render(padRight("TITLE", titleW)) + "  " +
		styleColumnHeader.Render(padLeft("IMP", impW)) + "  " +
		styleColumnHeader.Render(padLeft("DATE", dateW))
	return header
}

func (m ListModel) renderRows() string {
	if len(m.memories) == 0 {
		return styleSubtle.Render("  (no memories)")
	}

	typeW, titleW, impW, dateW := m.columnWidths()
	visible := m.visibleRows()
	end := m.offset + visible
	end = min(end, len(m.memories))

	var b strings.Builder
	for i := m.offset; i < end; i++ {
		mem := m.memories[i]
		row := m.renderRow(mem, typeW, titleW, impW, dateW)
		if i == m.cursor {
			row = styleSelected.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return b.String()
}

func (m ListModel) renderRow(mem *model.Memory, typeW, titleW, impW, dateW int) string {
	typeStr := padRight(string(mem.Type), typeW)
	titleStr := padRight(truncate(mem.Title, titleW), titleW)
	impStr := padLeft(formatImportance(mem.Importance), impW)
	dateStr := padLeft(formatDate(mem.CreatedAt), dateW)

	coloredType := typeColor(mem.Type).Render(typeStr)
	return coloredType + "  " + titleStr + "  " + impStr + "  " + dateStr
}

// columnWidths returns the display widths for type, title, importance, date.
// Title gets all the remaining space after the fixed-width columns.
func (m ListModel) columnWidths() (typeW, titleW, impW, dateW int) {
	typeW = 14
	impW = 5
	dateW = 10
	// "  " separators: 3 separators = 6 chars
	titleW = m.width - typeW - impW - dateW - 6
	titleW = max(titleW, 10)
	return
}

// visibleRows returns how many rows fit in the current terminal height.
// It reserves 4 lines: header + column headers + status bar + separator.
func (m ListModel) visibleRows() int {
	return max(m.height-4, 1)
}

// moveCursor moves the cursor by delta, clamping to valid range, and adjusts
// the scroll offset to keep the cursor visible.
func (m *ListModel) moveCursor(delta int) {
	m.cursor += delta
	m.cursor = max(m.cursor, 0)
	if m.cursor >= len(m.memories) {
		m.cursor = max(len(m.memories)-1, 0)
	}
	m.scrollToCursor()
}

// scrollToCursor ensures the scroll offset keeps the cursor inside the visible
// window.
func (m *ListModel) scrollToCursor() {
	visible := m.visibleRows()
	m.offset = min(m.offset, m.cursor)
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
}

// refilter applies the active typeFilter and scopeFilter on allMemories and
// stores the result in memories. Call this whenever filters or allMemories change.
func (m *ListModel) refilter() {
	m.memories = applyFilters(m.allMemories, m.typeFilter, m.scopeFilter)
}

// SetSize propagates the terminal size to the list model.
func (m *ListModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// padRight pads or truncates s to exactly width using spaces on the right.
func padRight(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return string(runes[:width])
	}
	return s + strings.Repeat(" ", width-len(runes))
}

// padLeft pads s to exactly width using spaces on the left.
func padLeft(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return string(runes[:width])
	}
	return strings.Repeat(" ", width-len(runes)) + s
}
