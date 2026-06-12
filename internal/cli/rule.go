package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/rules"
	"github.com/juanftp/mneme/internal/service"
)

// Adaptive colors for severity levels in the rule list table.
// These mirror the values used by internal/tui/style.go for visual consistency.
var (
	severityColorBlock = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f38ba8"}
	severityColorWarn  = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#fab387"}
	severityColorInfo  = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#89dceb"}
)

// newRuleCmd returns the "mneme rule" subcommand group, which provides
// ergonomic creation, listing, and testing of rule memories. The underlying
// rule model (type, applies_to, severity) was introduced in SPEC-001; this
// command group is the user-facing interface for managing it.
func newRuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "Manage rules",
		Long: `Manage rules — binding constraints that are enforced by the pre-tool-use hook.

Subcommands:
  add   Create a new rule with applies_to patterns and severity.
  list  Display all active rules in a colour-coded table.
  test  Evaluate rules against a simulated tool + file invocation.`,
	}

	cmd.AddCommand(
		newRuleAddCmd(),
		newRuleListCmd(),
		newRuleTestCmd(),
	)

	return cmd
}

// newRuleAddCmd returns the "mneme rule add" subcommand. It creates a rule
// memory with the specified applies_to patterns and severity, automatically
// generating a topic_key from the title when one is not provided.
func newRuleAddCmd() *cobra.Command {
	var (
		flagTitle      string
		flagContent    string
		flagAppliesTo  []string
		flagSeverity   string
		flagScope      string
		flagTopicKey   string
		flagImportance float64
		flagStdin      bool
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a new rule",
		Long: `Create a new rule memory with applies_to patterns and a severity level.

Applies-to patterns can be:
  **                    Match any tool and any path (global wildcard)
  tool:Edit             Match a specific tool by name (case-sensitive)
  internal/**/*.go      Match files using a doublestar path glob
  tool:Edit+internal/** AND selector: both tool and path must match
  !docs/**              Negation: veto the rule when this pattern matches

Content can be provided inline via --content or piped via --stdin.
The --topic-key is auto-generated from --title when omitted (enabling
idempotent upserts when you run the same command twice).`,
		Example: `  mneme rule add -t "No vendor edits" -c "Never edit vendor/ files." -a "vendor/**" -s block
  mneme rule add -t "SQL in .sql files" -c "..." -a "**/*.go" --scope global
  echo "Long instruction." | mneme rule add -t "My rule" -a "**" --stdin`,
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
				return fmt.Errorf("--content is required (or use --stdin)")
			}

			if len(flagAppliesTo) == 0 {
				return fmt.Errorf("--applies-to is required (at least one pattern)")
			}

			// Validate severity before calling the service.
			sev := model.Severity(flagSeverity)
			if flagSeverity != "" && !sev.Valid() {
				return fmt.Errorf("invalid severity %q: must be info, warn, or block", flagSeverity)
			}

			// Validate each applies_to pattern syntactically.
			for _, p := range flagAppliesTo {
				if err := rules.ValidatePattern(p); err != nil {
					return err
				}
			}

			// Auto-generate topic_key from the title when not provided.
			topicKey := flagTopicKey
			if topicKey == "" {
				topicKey = slugifyTitle(flagTitle)
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			req := model.SaveRequest{
				Title:     flagTitle,
				Content:   content,
				Type:      model.TypeRule,
				AppliesTo: flagAppliesTo,
				TopicKey:  topicKey,
			}
			if flagSeverity != "" {
				req.Severity = sev
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

			// Print a confirmation with the rule details so the user can verify.
			fmt.Fprintf(os.Stdout, "Rule saved: %s (%s) — %s\n", resp.ID, resp.Action, resp.Title)
			fmt.Fprintf(os.Stdout, "  Severity:   %s\n", flagSeverity)
			fmt.Fprintf(os.Stdout, "  Applies to: %s\n", strings.Join(flagAppliesTo, ", "))
			fmt.Fprintf(os.Stdout, "  Topic key:  %s\n", resp.TopicKey)
			if flagScope != "" {
				fmt.Fprintf(os.Stdout, "  Scope:      %s\n", flagScope)
			} else {
				fmt.Fprintf(os.Stdout, "  Scope:      project\n")
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&flagTitle, "title", "t", "", "Rule title (required)")
	cmd.Flags().StringVarP(&flagContent, "content", "c", "", "Rule content/instruction")
	cmd.Flags().StringArrayVarP(&flagAppliesTo, "applies-to", "a", nil, "Pattern(s) this rule applies to (repeatable, required)")
	cmd.Flags().StringVarP(&flagSeverity, "severity", "s", "warn", "Severity: info, warn, or block")
	cmd.Flags().StringVar(&flagScope, "scope", "", "Scope: project or global (default \"project\")")
	cmd.Flags().StringVarP(&flagTopicKey, "topic-key", "k", "", "Topic key for upserts (auto-generated from title when omitted)")
	cmd.Flags().Float64VarP(&flagImportance, "importance", "i", 0.95, "Importance override (0.0-1.0)")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "Read content from stdin")

	_ = cmd.RegisterFlagCompletionFunc("severity", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"info", "warn", "block"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("scope", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"project", "global"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// ruleListJSON is the versioned JSON wrapper emitted by "mneme rule list --json".
// The version field allows consumers to detect schema changes without field introspection.
type ruleListJSON struct {
	Version string         `json:"version"`
	Rules   []ruleJSONItem `json:"rules"`
}

// ruleJSONItem is a compact rule representation for JSON output. It omits
// content to keep payloads small; callers that need full content should use
// "mneme get <id>".
type ruleJSONItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Severity  string    `json:"severity"`
	AppliesTo []string  `json:"applies_to"`
	Scope     string    `json:"scope"`
	TopicKey  string    `json:"topic_key,omitempty"`
	Importance float64  `json:"importance"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// newRuleListCmd returns the "mneme rule list" subcommand. It queries all active
// rules and renders them either as a colour-coded table (TTY) or a versioned JSON
// envelope (--json flag). lipgloss handles ANSI stripping automatically when
// stdout is not a terminal.
func newRuleListCmd() *cobra.Command {
	var (
		flagScope    string
		flagSeverity string
		flagJSON     bool
		flagLimit    int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all active rules",
		Long: `List all active rules from the project and/or global store.

Results are sorted by severity (block > warn > info), then by importance,
then by most recently updated. Use --json to get a versioned JSON envelope
suitable for programmatic consumption.`,
		Example: `  mneme rule list
  mneme rule list --scope global
  mneme rule list --severity block
  mneme rule list --json | jq '.rules[].title'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			opts := service.ListRulesOptions{
				Scope: flagScope,
				Limit: flagLimit,
			}
			if flagSeverity != "" {
				opts.Severity = model.Severity(flagSeverity)
			}

			list, err := svc.ListRules(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if flagJSON {
				return printRulesJSON(os.Stdout, list)
			}

			return printRulesTable(os.Stdout, list)
		},
	}

	cmd.Flags().StringVarP(&flagScope, "scope", "s", "all", "Filter by scope: project, global, or all")
	cmd.Flags().StringVar(&flagSeverity, "severity", "", "Filter by severity: info, warn, or block")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as versioned JSON")
	cmd.Flags().IntVarP(&flagLimit, "limit", "n", 50, "Maximum number of rules to return")

	_ = cmd.RegisterFlagCompletionFunc("severity", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"info", "warn", "block"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("scope", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"project", "global", "all"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// printRulesTable renders the rules as a fixed-width table with colour-coded
// severity tags. lipgloss/termenv strips ANSI codes automatically when stdout
// is not a terminal, so callers do not need to check for TTY explicitly.
func printRulesTable(w io.Writer, list []*model.Memory) error {
	if len(list) == 0 {
		fmt.Fprintln(w, "No rules found.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Create one with: mneme rule add --title \"...\" --content \"...\" --applies-to \"pattern\"")
		return nil
	}

	boldStyle := lipgloss.NewStyle().Bold(true)
	blockStyle := lipgloss.NewStyle().Foreground(severityColorBlock).Bold(true)
	warnStyle := lipgloss.NewStyle().Foreground(severityColorWarn).Bold(true)
	infoStyle := lipgloss.NewStyle().Foreground(severityColorInfo).Bold(true)

	// Print table header.
	fmt.Fprintf(w, "%s  %s  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-5s", "SEV")),
		boldStyle.Render(fmt.Sprintf("%-8s", "ID")),
		boldStyle.Render(fmt.Sprintf("%-30s", "TITLE")),
		boldStyle.Render(fmt.Sprintf("%-30s", "APPLIES_TO")),
		boldStyle.Render(fmt.Sprintf("%-7s", "SCOPE")),
	)
	fmt.Fprintf(w, "%s  %s  %s  %s  %s\n",
		"─────", "────────", "──────────────────────────────",
		"──────────────────────────────", "───────",
	)

	counts := map[model.Severity]int{}

	for _, m := range list {
		counts[m.Severity]++

		shortID := m.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		title := truncate(m.Title, 30)
		appliesTo := truncate(strings.Join(m.AppliesTo, ", "), 30)
		scope := string(m.Scope)
		if len(scope) > 7 {
			scope = scope[:7]
		}

		var sevTag string
		switch m.Severity {
		case model.SeverityBlock:
			sevTag = blockStyle.Render("BLOCK")
		case model.SeverityWarn:
			sevTag = warnStyle.Render("WARN ")
		case model.SeverityInfo:
			sevTag = infoStyle.Render("INFO ")
		default:
			sevTag = "     "
		}

		fmt.Fprintf(w, "%s  %-8s  %-30s  %-30s  %-7s\n",
			sevTag, shortID, title, appliesTo, scope,
		)
	}

	// Summary line.
	fmt.Fprintln(w)
	summary := fmt.Sprintf("%d rule", len(list))
	if len(list) != 1 {
		summary += "s"
	}
	parts := []string{}
	if n := counts[model.SeverityBlock]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d block", n))
	}
	if n := counts[model.SeverityWarn]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d warn", n))
	}
	if n := counts[model.SeverityInfo]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d info", n))
	}
	if len(parts) > 0 {
		summary += " (" + strings.Join(parts, ", ") + ")"
	}
	fmt.Fprintln(w, summary)

	return nil
}

// printRulesJSON emits a versioned JSON envelope for programmatic consumption.
// The "version" field allows consumers to detect schema changes without field
// introspection.
func printRulesJSON(w io.Writer, list []*model.Memory) error {
	items := make([]ruleJSONItem, 0, len(list))
	for _, m := range list {
		applies := m.AppliesTo
		if applies == nil {
			applies = []string{}
		}
		items = append(items, ruleJSONItem{
			ID:         m.ID,
			Title:      m.Title,
			Severity:   string(m.Severity),
			AppliesTo:  applies,
			Scope:      string(m.Scope),
			TopicKey:   m.TopicKey,
			Importance: m.Importance,
			CreatedAt:  m.CreatedAt,
			UpdatedAt:  m.UpdatedAt,
		})
	}

	envelope := ruleListJSON{
		Version: "1",
		Rules:   items,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope)
}

// newRuleTestCmd returns the "mneme rule test" subcommand. It evaluates all
// active rules against a simulated tool + file invocation and explains which
// rules would fire and why.
func newRuleTestCmd() *cobra.Command {
	var (
		flagTool string
		flagPath string
		flagJSON bool
	)

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test rules against a simulated tool invocation",
		Long: `Evaluate all active rules against a simulated tool and path invocation.

Useful for debugging which rules would fire (and which would not) before
running the actual tool. Prints each matched rule with the applies_to
entries that caused the match, the effective severity, and the resulting
action (ALLOWED / BLOCKED).`,
		Example: `  mneme rule test --tool Edit --path internal/store/memory.go
  mneme rule test --tool Write --path vendor/foo/bar.go
  mneme rule test --tool Edit --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			allRules, err := svc.ListRules(cmd.Context(), service.ListRulesOptions{Limit: 200})
			if err != nil {
				return fmt.Errorf("load rules: %w", err)
			}

			// Convert []*model.Memory to []model.Memory for the matching engine.
			ruleSlice := make([]model.Memory, len(allRules))
			for i, r := range allRules {
				ruleSlice[i] = *r
			}

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("cannot determine working directory: %w", err)
			}

			// mneme rule test is a human-initiated simulation; default to
			// CallerOrchestrator so block rules show as block (strictest view).
			inv := rules.Invocation{
				Tool:     flagTool,
				FilePath: flagPath,
				CWD:      cwd,
				Caller:   rules.CallerOrchestrator,
			}
			result := rules.Match(ruleSlice, inv)

			if flagJSON {
				return printTestJSON(os.Stdout, flagTool, flagPath, len(ruleSlice), result)
			}

			return printTestOutput(os.Stdout, flagTool, flagPath, len(ruleSlice), result)
		},
	}

	cmd.Flags().StringVarP(&flagTool, "tool", "T", "Edit", "Tool name to simulate (e.g. Edit, Write, MultiEdit)")
	cmd.Flags().StringVar(&flagPath, "path", "", "File path to simulate (absolute or relative to CWD)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output as JSON")

	return cmd
}

// printTestOutput writes the human-readable test result to w.
func printTestOutput(w io.Writer, toolName, filePath string, evaluated int, result rules.MatchResult) error {
	// Header line.
	fmt.Fprintf(w, "Testing: tool=%-12s  path=%s\n\n", toolName, filePath)

	if filePath == "" {
		fmt.Fprintln(w, "Note: No --path specified. Only tool selectors and ** wildcards will match.")
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "Evaluated: %d rules\n", evaluated)
	fmt.Fprintf(w, "Matched:   %d rules\n", len(result.Matched))

	if len(result.Matched) > 0 {
		fmt.Fprintln(w)
		for _, mr := range result.Matched {
			sevStr := strings.ToUpper(string(mr.Rule.Severity))
			fmt.Fprintf(w, "  [%s] %s\n", sevStr, mr.Rule.Title)
			if mr.Rule.Content != "" {
				// Print first line of content (rules should be concise).
				firstLine := strings.SplitN(mr.Rule.Content, "\n", 2)[0]
				fmt.Fprintf(w, "         %s\n", firstLine)
			}
			fmt.Fprintf(w, "         Matched by: %s\n", strings.Join(mr.Entries, ", "))

			// Identify negation entries that did NOT veto.
			var negations []string
			for _, p := range mr.Rule.AppliesTo {
				if strings.HasPrefix(p, "!") {
					negations = append(negations, p)
				}
			}
			if len(negations) > 0 {
				fmt.Fprintf(w, "         Negation %s: did not veto\n", strings.Join(negations, ", "))
			}
			fmt.Fprintln(w)
		}
	}

	// Result line.
	switch result.MaxSev {
	case model.SeverityBlock:
		fmt.Fprintln(w, "Effective severity: block")
		fmt.Fprintln(w, "Result: BLOCKED")
	case model.SeverityWarn:
		fmt.Fprintf(w, "Effective severity: warn\n")
		fmt.Fprintf(w, "Result: ALLOWED (with %d warning", len(result.Matched))
		if len(result.Matched) != 1 {
			fmt.Fprint(w, "s")
		}
		fmt.Fprintln(w, ")")
	default:
		if len(result.Matched) == 0 {
			fmt.Fprintln(w, "Result: ALLOWED (no rules matched)")
		} else {
			fmt.Fprintf(w, "Effective severity: info\n")
			fmt.Fprintf(w, "Result: ALLOWED (advisory: %d info rule", len(result.Matched))
			if len(result.Matched) != 1 {
				fmt.Fprint(w, "s")
			}
			fmt.Fprintln(w, ")")
		}
	}

	return nil
}

// ruleTestJSON is the JSON structure emitted by "mneme rule test --json".
type ruleTestJSON struct {
	Tool      string             `json:"tool"`
	Path      string             `json:"path,omitempty"`
	Evaluated int                `json:"evaluated"`
	Matched   []ruleTestMatchJSON `json:"matched"`
	MaxSev    string             `json:"max_severity"`
	Result    string             `json:"result"`
}

// ruleTestMatchJSON represents a single matched rule in JSON output.
type ruleTestMatchJSON struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Severity string  `json:"severity"`
	Entries []string `json:"matched_entries"`
}

// printTestJSON writes the test result as JSON to w.
func printTestJSON(w io.Writer, toolName, filePath string, evaluated int, result rules.MatchResult) error {
	matched := make([]ruleTestMatchJSON, 0, len(result.Matched))
	for _, mr := range result.Matched {
		entries := mr.Entries
		if entries == nil {
			entries = []string{}
		}
		matched = append(matched, ruleTestMatchJSON{
			ID:       mr.Rule.ID,
			Title:    mr.Rule.Title,
			Severity: string(mr.Rule.Severity),
			Entries:  entries,
		})
	}

	resultStr := "ALLOWED"
	if result.MaxSev == model.SeverityBlock {
		resultStr = "BLOCKED"
	}

	out := ruleTestJSON{
		Tool:      toolName,
		Path:      filePath,
		Evaluated: evaluated,
		Matched:   matched,
		MaxSev:    string(result.MaxSev),
		Result:    resultStr,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// slugifyTitle converts a rule title into a valid topic_key with the prefix
// "rule/". The algorithm is deterministic: lowercase, replace non-[a-z0-9]
// with hyphens, collapse consecutive hyphens, trim, truncate to 60 chars.
// When the resulting slug is empty (title contains only special characters),
// a fallback UUID-based suffix is NOT applied — callers should pass --topic-key
// explicitly when the title produces an empty slug.
func slugifyTitle(title string) string {
	// Convert to lowercase.
	s := strings.ToLower(title)

	// Replace any sequence of non-alphanumeric, non-hyphen runes with a hyphen.
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")

	// Trim leading and trailing hyphens.
	s = strings.Trim(s, "-")

	// Truncate to 60 characters (safety cap, without splitting mid-word).
	const maxSlugLen = 60
	if len(s) > maxSlugLen {
		s = s[:maxSlugLen]
		// Walk back to the last hyphen so we don't cut mid-word.
		if idx := strings.LastIndex(s, "-"); idx > 0 {
			s = s[:idx]
		}
		s = strings.Trim(s, "-")
	}

	return "rule/" + s
}

