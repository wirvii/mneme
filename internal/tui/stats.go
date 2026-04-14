package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// StatsModel renders a dashboard of aggregate statistics about the memory store.
// Data is loaded asynchronously via a tea.Cmd when the screen becomes active.
type StatsModel struct {
	svc     *service.MemoryService
	project string

	stats  *model.StatsResponse
	loading bool

	width  int
	height int

	keys KeyMap
}

// newStatsModel constructs a StatsModel that will call svc.Stats for project.
func newStatsModel(svc *service.MemoryService, project string, keys KeyMap) StatsModel {
	return StatsModel{
		svc:     svc,
		project: project,
		keys:    keys,
	}
}

// Load returns a tea.Cmd that fetches stats. Call this when entering the screen.
func (m StatsModel) Load() tea.Cmd {
	return loadStats(m.svc, m.project)
}

// Init is called by AppModel.Init — stats are loaded on demand when navigating.
func (m StatsModel) Init() tea.Cmd { return nil }

// Update handles messages for the stats screen.
func (m StatsModel) Update(msg tea.Msg) (StatsModel, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case statsLoadedMsg:
		m.loading = false
		if msg.err == nil {
			m.stats = msg.stats
		}
		// Errors are propagated via the original msg back to AppModel.
		return m, func() tea.Msg { return msg }

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, func() tea.Msg {
				return navigateMsg{target: screenList}
			}
		case "r":
			m.loading = true
			return m, loadStats(m.svc, m.project)
		case "?":
			return m, func() tea.Msg {
				return navigateMsg{target: screenHelp}
			}
		}
	}

	return m, nil
}

// View renders the stats dashboard using lipgloss boxes.
func (m StatsModel) View() string {
	if m.loading || m.stats == nil {
		return styleSubtle.Render("Loading statistics…")
	}

	s := m.stats
	var b strings.Builder

	b.WriteString(styleTitle.Render("Statistics") + "  " +
		styleSubtle.Render("project: "+s.Project) + "\n\n")

	// Row 1: Memories box + By Type box + By Scope box
	memoriesBox := m.renderMemoriesBox(s)
	typeBox := m.renderByTypeBox(s)
	scopeBox := m.renderByScopeBox(s)

	row := joinHorizontal(memoriesBox, typeBox, scopeBox, 4)
	b.WriteString(row)
	b.WriteString("\n\n")

	// Row 2: Miscellaneous stats
	b.WriteString(styleSubtle.Render("DB Size: ") + formatBytes(s.DBSizeBytes) + "  ")
	b.WriteString(styleSubtle.Render("Avg Importance: ") + formatImportance(s.AvgImportance) + "  ")
	b.WriteString(styleSubtle.Render("Embeddings: ") + fmt.Sprintf("%d", s.EmbeddingsCount))
	b.WriteString("\n")

	if s.OldestMemory != nil {
		b.WriteString(styleSubtle.Render("Oldest: ") + formatDate(*s.OldestMemory) + "  ")
	}
	if s.NewestMemory != nil {
		b.WriteString(styleSubtle.Render("Newest: ") + formatDate(*s.NewestMemory))
	}

	return b.String()
}

func (m StatsModel) renderMemoriesBox(s *model.StatsResponse) string {
	lines := []string{
		fmt.Sprintf("Total:     %4d", s.TotalMemories),
		fmt.Sprintf("Active:    %4d", s.Active),
		fmt.Sprintf("Superseded:%4d", s.Superseded),
		fmt.Sprintf("Forgotten: %4d", s.Forgotten),
	}
	return styleBorder.Render(styleHeader.Render("Memories") + "\n" + strings.Join(lines, "\n"))
}

func (m StatsModel) renderByTypeBox(s *model.StatsResponse) string {
	var rows []string
	for _, t := range model.AllMemoryTypes() {
		count := s.ByType[t]
		if count == 0 {
			continue
		}
		label := padRight(string(t), 14)
		rows = append(rows, typeColor(t).Render(label)+" "+fmt.Sprintf("%3d", count))
	}
	if len(rows) == 0 {
		rows = []string{styleSubtle.Render("(empty)")}
	}
	return styleBorder.Render(styleHeader.Render("By Type") + "\n" + strings.Join(rows, "\n"))
}

func (m StatsModel) renderByScopeBox(s *model.StatsResponse) string {
	scopes := []model.Scope{model.ScopeProject, model.ScopeGlobal, model.ScopeOrg}
	var rows []string
	for _, sc := range scopes {
		count := s.ByScope[sc]
		label := padRight(string(sc), 8)
		rows = append(rows, label+" "+fmt.Sprintf("%3d", count))
	}
	return styleBorder.Render(styleHeader.Render("By Scope") + "\n" + strings.Join(rows, "\n"))
}

// joinHorizontal places two or more rendered strings side-by-side with gap
// spaces between them.
func joinHorizontal(blocks ...interface{}) string {
	// Last arg is the gap (int), rest are strings.
	if len(blocks) < 2 {
		return ""
	}
	gap, ok := blocks[len(blocks)-1].(int)
	if !ok {
		gap = 2
	}
	parts := make([]string, 0, len(blocks)-1)
	for _, b := range blocks[:len(blocks)-1] {
		if s, ok := b.(string); ok {
			parts = append(parts, s)
		}
	}
	sep := strings.Repeat(" ", gap)
	return strings.Join(parts, sep)
}
