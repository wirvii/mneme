package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// DetailModel shows the full content and metadata of a single memory. The
// metadata section is static at the top; the content area is scrollable via
// a bubbles viewport.
type DetailModel struct {
	svc    *service.MemoryService
	memory *model.Memory

	viewport      viewport.Model
	viewportReady bool

	// confirming is true when the user has pressed 'd' and is waiting for a y/n
	// confirmation before the forget is executed.
	confirming bool

	width  int
	height int

	keys KeyMap
}

// newDetailModel constructs a DetailModel. The memory is set later via SetMemory.
func newDetailModel(svc *service.MemoryService, keys KeyMap) DetailModel {
	return DetailModel{
		svc:  svc,
		keys: keys,
	}
}

// SetMemory loads a new memory into the detail view and resets scroll position.
func (m *DetailModel) SetMemory(mem *model.Memory) {
	m.memory = mem
	m.confirming = false
	m.viewportReady = false
	if m.width > 0 && m.height > 0 {
		m.initViewport()
	}
}

// Init is a no-op for detail — data is passed in via SetMemory.
func (m DetailModel) Init() tea.Cmd { return nil }

// Update handles messages for the detail screen.
func (m DetailModel) Update(msg tea.Msg) (DetailModel, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.initViewport()

	case forgetDoneMsg:
		if msg.err == nil {
			// Navigate back to list after a successful forget.
			return m, func() tea.Msg {
				return navigateMsg{target: screenList}
			}
		}
		// On error, fall through — AppModel will handle the error in status bar.
		return m, func() tea.Msg { return msg }

	case tea.KeyMsg:
		if m.confirming {
			return m.updateConfirm(msg)
		}
		return m.updateNormal(msg)
	}

	// Delegate scrolling to the viewport.
	if m.viewportReady {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

// updateConfirm handles the y/n confirmation for forget.
func (m DetailModel) updateConfirm(msg tea.KeyMsg) (DetailModel, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.confirming = false
		if m.memory != nil {
			return m, forgetMemory(m.svc, m.memory.ID)
		}
	default:
		m.confirming = false
	}
	return m, nil
}

// updateNormal handles key input in normal detail mode.
func (m DetailModel) updateNormal(msg tea.KeyMsg) (DetailModel, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, func() tea.Msg {
			return navigateMsg{target: screenList}
		}

	case "d":
		if m.memory != nil {
			m.confirming = true
		}
		return m, nil

	case "?":
		return m, func() tea.Msg {
			return navigateMsg{target: screenHelp}
		}
	}

	// Pass navigation keys to the viewport for scrolling.
	if m.viewportReady {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

// View renders the detail screen with a static metadata header and a scrollable
// content viewport below.
func (m DetailModel) View() string {
	if m.memory == nil {
		return styleSubtle.Render("No memory selected.")
	}

	header := m.renderHeader()
	if !m.viewportReady {
		return header
	}

	return header + "\n" + m.viewport.View()
}

// renderHeader renders the fixed metadata section at the top of the detail view.
func (m DetailModel) renderHeader() string {
	mem := m.memory
	typeLabel := typeColor(mem.Type).Render(fmt.Sprintf("[ %s ]", mem.Type))
	title := styleTitle.Render(mem.Title)

	var b strings.Builder
	b.WriteString(typeLabel + " " + title + "\n")
	b.WriteString(strings.Repeat("─", m.width) + "\n")
	b.WriteString(styleSubtle.Render("ID:         ") + mem.ID + "\n")
	b.WriteString(styleSubtle.Render("Scope:      ") + string(mem.Scope) + "\n")
	b.WriteString(styleSubtle.Render("Importance: ") + formatImportance(mem.Importance) +
		"  " + styleSubtle.Render("Confidence: ") + formatImportance(mem.Confidence) + "\n")
	if mem.TopicKey != "" {
		b.WriteString(styleSubtle.Render("Topic Key:  ") + mem.TopicKey + "\n")
	}
	b.WriteString(styleSubtle.Render("Created:    ") + formatDateTime(mem.CreatedAt) + "\n")
	b.WriteString(styleSubtle.Render("Updated:    ") + formatDateTime(mem.UpdatedAt) + "\n")
	b.WriteString(styleSubtle.Render("Accesses:   ") + fmt.Sprintf("%d", mem.AccessCount) + "\n")
	if len(mem.Files) > 0 {
		b.WriteString(styleSubtle.Render("Files:      ") + renderFiles(mem.Files) + "\n")
	}
	b.WriteString(strings.Repeat("─", m.width) + "\n")
	return b.String()
}

// initViewport (re)initialises the viewport with the correct dimensions and
// content. Must be called whenever width/height or memory changes.
func (m *DetailModel) initViewport() {
	if m.memory == nil {
		return
	}

	headerLines := m.headerLineCount()
	// Reserve 1 line for the status bar.
	vpHeight := max(m.height-headerLines-1, 3)

	m.viewport = viewport.New(m.width, vpHeight)
	m.viewport.SetContent(m.memory.Content)
	m.viewportReady = true
}

// headerLineCount returns the number of lines the metadata header occupies.
func (m DetailModel) headerLineCount() int {
	base := 9 // type+title, separator, ID, Scope, Importance, Created, Updated, Accesses, separator
	if m.memory != nil {
		if m.memory.TopicKey != "" {
			base++
		}
		if len(m.memory.Files) > 0 {
			base++
		}
	}
	return base
}

// ConfirmMessage returns the confirmation prompt shown in the status bar when
// confirming is true.
func (m DetailModel) ConfirmMessage() string {
	if m.memory == nil {
		return ""
	}
	title := truncate(m.memory.Title, 40)
	return styleDanger.Render(fmt.Sprintf(`Forget "%s"? (y/n)`, title))
}
