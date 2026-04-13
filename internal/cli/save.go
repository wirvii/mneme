package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// newSaveCmd returns the "mneme save" subcommand. It persists a new memory
// or upserts an existing one (when --topic-key is provided) and prints a
// one-line confirmation to stdout.
func newSaveCmd() *cobra.Command {
	var (
		flagTitle      string
		flagContent    string
		flagType       string
		flagScope      string
		flagTopicKey   string
		flagFiles      []string
		flagImportance float64
		flagStdin      bool
	)

	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save a memory",
		Long: `Save a structured observation to persistent memory.

The memory is stored in the project database (auto-detected from the current
git repository) or the global database when no project is detected.

If --topic-key is provided and a memory with the same key already exists for
the same project and scope, the existing memory is updated instead of creating
a duplicate.`,
		Example: `  mneme save --title "Auth uses JWT RS256" --content "..." --type decision
  echo "content" | mneme save --title "My note" --stdin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagTitle == "" {
				return fmt.Errorf("--title is required")
			}

			// Prefer --stdin over --content when both are provided.
			content := flagContent
			if flagStdin {
				data, err := io.ReadAll(bufio.NewReader(os.Stdin))
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				content = string(data)
			}

			if content == "" {
				return fmt.Errorf("--content is required (or use --stdin to read from stdin)")
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.SaveRequest{
				Title:    flagTitle,
				Content:  content,
				TopicKey: flagTopicKey,
				Files:    flagFiles,
			}
			if flagType != "" {
				req.Type = model.MemoryType(flagType)
			}
			if flagScope != "" {
				req.Scope = model.Scope(flagScope)
			}
			if cmd.Flags().Changed("importance") {
				req.Importance = &flagImportance
			}

			resp, err := svc.Save(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "Saved: %s (%s) — %s\n", resp.ID, resp.Action, resp.Title)
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagTitle, "title", "t", "", "Memory title (required)")
	cmd.Flags().StringVarP(&flagContent, "content", "c", "", "Memory content")
	cmd.Flags().StringVarP(&flagType, "type", "T", "", "Memory type (default \"discovery\")")
	cmd.Flags().StringVarP(&flagScope, "scope", "s", "", "Memory scope (default \"project\")")
	cmd.Flags().StringVarP(&flagTopicKey, "topic-key", "k", "", "Topic key for upserts")
	cmd.Flags().StringArrayVarP(&flagFiles, "file", "f", nil, "Referenced file paths (repeatable)")
	cmd.Flags().Float64VarP(&flagImportance, "importance", "i", 0, "Importance override (0.0-1.0)")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "Read content from stdin")

	return cmd
}
