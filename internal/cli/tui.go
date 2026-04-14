package cli

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/tui"
)

// newTUICmd returns the "mneme tui" subcommand. It launches the interactive
// terminal UI backed by Bubbletea. The UI provides list, search, detail, and
// stats screens for browsing and managing memories without leaving the terminal.
func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive terminal UI",
		Long:  "Open an interactive terminal interface for browsing, searching, and managing memories.",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			app := tui.NewApp(svc)
			p := tea.NewProgram(app, tea.WithAltScreen())
			_, err = p.Run()
			return err
		},
	}
}
