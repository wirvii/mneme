package tui

import (
	"context"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// screen identifies which TUI screen is currently active.
type screen int

const (
	screenList screen = iota
	screenDetail
	screenStats
	screenHelp
)

// navigateMsg tells AppModel to switch to a different screen.
type navigateMsg struct {
	target screen
	memory *model.Memory // non-nil when navigating to screenDetail
}

// memoriesLoadedMsg carries the result of a List or Search call.
type memoriesLoadedMsg struct {
	memories []*model.Memory
	err      error
}

// statsLoadedMsg carries the result of a Stats call.
type statsLoadedMsg struct {
	stats *model.StatsResponse
	err   error
}

// forgetDoneMsg signals that a Forget call completed.
type forgetDoneMsg struct {
	id  string
	err error
}

// errClearedMsg is sent after the error display timer expires to clear the
// status bar error message.
type errClearedMsg struct{}

// loadMemories returns a tea.Cmd that fetches memories via svc.List.
// Results are delivered as a memoriesLoadedMsg.
func loadMemories(svc *service.MemoryService, opts store.ListOptions) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		memories, err := svc.List(ctx, opts)
		return memoriesLoadedMsg{memories: memories, err: err}
	}
}

// searchMemories returns a tea.Cmd that calls svc.Search and wraps the
// results as a memoriesLoadedMsg.
func searchMemories(svc *service.MemoryService, query string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		resp, err := svc.Search(ctx, model.SearchRequest{
			Query: query,
			Limit: 50,
		})
		if err != nil {
			return memoriesLoadedMsg{err: err}
		}
		memories := make([]*model.Memory, len(resp.Results))
		for i, r := range resp.Results {
			memories[i] = r.Memory
		}
		return memoriesLoadedMsg{memories: memories}
	}
}

// forgetMemory returns a tea.Cmd that calls svc.Forget and delivers a
// forgetDoneMsg.
func forgetMemory(svc *service.MemoryService, id string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		err := svc.Forget(ctx, id, "forgotten via TUI")
		return forgetDoneMsg{id: id, err: err}
	}
}

// loadStats returns a tea.Cmd that calls svc.Stats and delivers a
// statsLoadedMsg.
func loadStats(svc *service.MemoryService, project string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		stats, err := svc.Stats(ctx, project)
		return statsLoadedMsg{stats: stats, err: err}
	}
}
