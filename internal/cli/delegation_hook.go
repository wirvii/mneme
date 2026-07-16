package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/service"
)

// newDelegationHookCmd returns the "mneme delegation-hook" subcommand group.
// It manages PROJECT-scoped, opt-in registration of the delegation
// enforcement hook, independent of the GLOBAL registration
// "mneme install claude-code" still performs in ~/.claude/settings.json
// during the agnostic-agents transition (SPEC-052 §5.2/§8.2/§9, EPIC
// agnostic-agents SS-6).
func newDelegationHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delegation-hook",
		Short: "Manage project-scoped delegation-enforcement hook registration",
		Long: `Manage the OPT-IN, per-project registration of the delegation-enforcement
PreToolUse hooks in <repo-root>/.claude/settings.json.

This is independent of the GLOBAL registration "mneme install claude-code"
performs in ~/.claude/settings.json (which every Claude Code session on this
machine picks up regardless of project). A project can opt in here without
affecting any other project on the machine.

Per SPEC-052 D9, this is the mechanism the mneme-init skill offers after
generating implementer subagents (backend/frontend/bug-hunter): a project
with no implementer subagents should stay single-agent and never enable
this hook (same precedent as Codex/SPEC-049, which never installs it).

The bash script itself (enforce_delegation.sh) is not duplicated per
project — it must already exist at ~/.claude/hooks/enforce_delegation.sh,
written once by "mneme install claude-code". Only the settings.json
REGISTRATION becomes project-scoped.

Subcommands:
  enable [path]   Register the hook in <path>/.claude/settings.json (default: cwd).
  disable [path]  Remove the hook registration, leaving everything else untouched.
  status [path]   Report whether the hook is currently registered.`,
	}

	cmd.AddCommand(
		newDelegationHookEnableCmd(),
		newDelegationHookDisableCmd(),
		newDelegationHookStatusCmd(),
		newDelegationHookReportCmd(),
		newDelegationHookPromoteCmd(),
	)

	return cmd
}

// delegationHookRepoRoot resolves the target repo root: args[0] when given,
// otherwise the current working directory.
func delegationHookRepoRoot(args []string) (string, error) {
	if len(args) == 1 {
		return filepath.Abs(args[0])
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	return cwd, nil
}

func newDelegationHookEnableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable [path]",
		Short: "Register the delegation-enforcement hook for a project",
		Long: `Merges the two delegation-enforcement PreToolUse entries (the Go rules hook
and the bash enforce_delegation.sh script) into <path>/.claude/settings.json.
Idempotent — running it twice does not duplicate entries.`,
		Example: `  mneme delegation-hook enable
  mneme delegation-hook enable /path/to/repo`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := delegationHookRepoRoot(args)
			if err != nil {
				return fmt.Errorf("delegation-hook enable: %w", err)
			}
			path, err := install.EnableProjectDelegationHook(root)
			if err != nil {
				return fmt.Errorf("delegation-hook enable: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enabled: %s\n", path)
			return nil
		},
	}
	return cmd
}

func newDelegationHookDisableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable [path]",
		Short: "Remove the delegation-enforcement hook registration for a project",
		Long: `Removes the two delegation-enforcement PreToolUse entries from
<path>/.claude/settings.json, leaving every other hook entry and setting
untouched. A missing file, or one that never had the hook registered, is a
no-op success.`,
		Example: `  mneme delegation-hook disable
  mneme delegation-hook disable /path/to/repo`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := delegationHookRepoRoot(args)
			if err != nil {
				return fmt.Errorf("delegation-hook disable: %w", err)
			}
			path, err := install.DisableProjectDelegationHook(root)
			if err != nil {
				return fmt.Errorf("delegation-hook disable: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled: %s\n", path)
			return nil
		},
	}
	return cmd
}

func newDelegationHookStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [path]",
		Short: "Report whether the delegation-enforcement hook is registered for a project",
		Example: `  mneme delegation-hook status
  mneme delegation-hook status /path/to/repo`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := delegationHookRepoRoot(args)
			if err != nil {
				return fmt.Errorf("delegation-hook status: %w", err)
			}
			enabled, path, err := install.ProjectDelegationHookStatus(root)
			if err != nil {
				return fmt.Errorf("delegation-hook status: %w", err)
			}
			state := "disabled"
			if enabled {
				state = "enabled"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", state, path)
			return nil
		},
	}
	return cmd
}

// --- report (SPEC-086 D3/D7) -------------------------------------------------

// detectDelegationProjectSlug resolves the project slug for cwd — every
// delegation-hook subcommand added by SPEC-086 needs this exact resolution
// (config.Load + project.NewDetector), mirroring evaluateDelegation's own
// project-detection step in hook.go.
func detectDelegationProjectSlug() (slug string, cfg *config.Config, err error) {
	cfg, err = config.Load(config.DefaultPath())
	if err != nil {
		return "", nil, fmt.Errorf("load config: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, fmt.Errorf("resolve current directory: %w", err)
	}
	det := project.NewDetector(cwd)
	slug, _ = det.DetectProject()
	if slug == "" {
		return "", nil, errors.New("could not detect a project for the current directory")
	}
	return slug, cfg, nil
}

func newDelegationHookReportCmd() *cobra.Command {
	var (
		flagSince string
		flagJSON  bool
	)

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Report subagent containment adoption (would_block/blocked counts by role)",
		Long: `Reads the local enforcelog telemetry (SPEC-086 D3) for the current project
and reports what would have been blocked, by role and by reason, plus the
"unresolved" health counter (D2: agent_id arrived without agent_type).

Mirrors "mneme codegraph adoption" — the same shape, applied to subagent
containment instead of code graph adoption.`,
		Example: `  mneme delegation-hook report
  mneme delegation-hook report --since 30d --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, cfg, err := detectDelegationProjectSlug()
			if err != nil {
				return fmt.Errorf("delegation-hook report: %w", err)
			}
			window, err := parseSinceWindow(flagSince)
			if err != nil {
				return fmt.Errorf("delegation-hook report: %w", err)
			}

			events, err := enforcelog.Read(enforcelogPath(cfg.Storage.DataDir, slug))
			if err != nil {
				return fmt.Errorf("delegation-hook report: read telemetry: %w", err)
			}
			report := enforcelog.Aggregate(events, time.Now().Add(-window))

			if flagJSON {
				return printJSON(cmd.OutOrStdout(), report)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Subagent containment adoption (last %s) — %s — mode: %s\n", flagSince, slug, cfg.SubagentContainmentMode(slug))
			if report.Total == 0 {
				fmt.Fprintln(out, "  No containment telemetry yet for this window.")
				return nil
			}
			fmt.Fprintf(out, "  %d events, %d unresolved (agent_id sin agent_type)\n", report.Total, report.Unresolved)
			for _, rr := range report.ByRole {
				fmt.Fprintf(out, "  %-16s  would_block=%-4d  blocked=%-4d  allowed=%-4d\n", rr.Role, rr.WouldBlock, rr.Blocked, rr.Allowed)
				for _, p := range rr.SamplePaths {
					fmt.Fprintf(out, "      - %s\n", p)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagSince, "since", "7d", "Time window to aggregate (e.g. 24h, 7d, 30d)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Emit the report as JSON")
	return cmd
}

// --- promote (SPEC-086 D7) ---------------------------------------------------

// promoteMinWindow and promoteMinEvents are D7's evidence gate: a project
// must have observed containment telemetry for at least this long, over at
// least this many events, before "mneme delegation-hook promote" will even
// consider flipping the mode to "block".
const (
	promoteMinWindow = 7 * 24 * time.Hour
	promoteMinEvents = 20
)

// promoteBreakingPair is one (role, path) combination that will start
// actually blocking once a project promotes to "block" mode — the exact
// list D7 requires a human to see and confirm before promotion happens.
type promoteBreakingPair struct {
	Role string
	Path string
}

// serviceEntriesToHook converts service.ManifestEntry (the persisted shape)
// into hookManifestEntry (the shape evaluatePromoteGate/findManifestEntryByRole
// already operate on) — avoids a third parallel struct for the same data.
func serviceEntriesToHook(entries []service.ManifestEntry) []hookManifestEntry {
	out := make([]hookManifestEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, hookManifestEntry{
			Role:          string(e.Role),
			Areas:         e.Areas,
			Archetype:     string(e.Archetype),
			AreasComplete: e.AreasComplete,
		})
	}
	return out
}

// evaluatePromoteGate implements D7's three-part promotion gate as a pure
// function (no I/O, directly unit-testable):
//
//  1. Evidence: at least promoteMinWindow has elapsed since the EARLIEST
//     recorded event, AND at least promoteMinEvents events exist.
//  2. Every role that has a would_block/block event must have
//     areas_complete:true in the CURRENT manifest — evidence collected while
//     the data was still uncertified does not count.
//  3. reasons is empty (ok=true) only when both gates pass; pairs always
//     lists every distinct (role, path) that would start actually blocking,
//     sorted deterministically, so the caller can print it and require
//     explicit confirmation regardless of whether the gate passed.
func evaluatePromoteGate(events []enforcelog.Event, now time.Time, entries []hookManifestEntry) (ok bool, reasons []string, pairs []promoteBreakingPair) {
	if len(events) == 0 {
		return false, []string{"no hay eventos de telemetría todavía — deja el proyecto en modo warn más tiempo"}, nil
	}

	earliest := events[0].TS
	for _, ev := range events {
		if ev.TS.Before(earliest) {
			earliest = ev.TS
		}
	}
	if elapsed := now.Sub(earliest); elapsed < promoteMinWindow {
		reasons = append(reasons, fmt.Sprintf("modo warn lleva %s, se requieren al menos %s", elapsed.Round(time.Hour), promoteMinWindow))
	}
	if len(events) < promoteMinEvents {
		reasons = append(reasons, fmt.Sprintf("%d eventos registrados, se requieren al menos %d", len(events), promoteMinEvents))
	}

	wouldBlockRoles := map[string]bool{}
	seenPairs := map[string]bool{}
	for _, ev := range events {
		if ev.Role == "" || (ev.Decision != enforcelog.DecisionWouldBlock && ev.Decision != enforcelog.DecisionBlock) {
			continue
		}
		wouldBlockRoles[ev.Role] = true
		key := ev.Role + "|" + ev.Target
		if !seenPairs[key] {
			seenPairs[key] = true
			pairs = append(pairs, promoteBreakingPair{Role: ev.Role, Path: ev.Target})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Role != pairs[j].Role {
			return pairs[i].Role < pairs[j].Role
		}
		return pairs[i].Path < pairs[j].Path
	})

	roleNames := make([]string, 0, len(wouldBlockRoles))
	for role := range wouldBlockRoles {
		roleNames = append(roleNames, role)
	}
	sort.Strings(roleNames)
	for _, role := range roleNames {
		entry, found := findManifestEntryByRole(entries, role)
		if !found || !entry.AreasComplete {
			reasons = append(reasons, fmt.Sprintf("rol %q tiene eventos would_block pero areas_complete no está en true en el manifest actual", role))
		}
	}

	return len(reasons) == 0, reasons, pairs
}

func newDelegationHookPromoteCmd() *cobra.Command {
	var flagYes bool

	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote the current project's subagent containment mode from warn to block",
		Long: `Evaluates D7's promotion gate against the local enforcelog telemetry
(SPEC-086 D3): at least 7 days and 20 events of evidence, and every role
with would_block events certified areas_complete:true in the current
manifest. Prints the exact list of (role, path) pairs that will start
actually blocking and requires --yes to confirm before writing
[delegation.projects."<slug>"] subagent_containment = "block" to
~/.mneme/config.toml.`,
		Example: `  mneme delegation-hook promote
  mneme delegation-hook promote --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, cfg, err := detectDelegationProjectSlug()
			if err != nil {
				return fmt.Errorf("delegation-hook promote: %w", err)
			}

			events, err := enforcelog.Read(enforcelogPath(cfg.Storage.DataDir, slug))
			if err != nil {
				return fmt.Errorf("delegation-hook promote: read telemetry: %w", err)
			}

			subSvc, subCleanup, err := initSubagentService()
			if err != nil {
				return fmt.Errorf("delegation-hook promote: %w", err)
			}
			defer subCleanup()
			manifest, err := subSvc.ReadManifest(cmd.Context(), slug)
			if err != nil {
				return fmt.Errorf("delegation-hook promote: read manifest: %w", err)
			}

			ok, reasons, pairs := evaluatePromoteGate(events, time.Now(), serviceEntriesToHook(manifest))
			out := cmd.OutOrStdout()

			if !ok {
				fmt.Fprintln(out, "No se puede promover a block todavía:")
				for _, r := range reasons {
					fmt.Fprintf(out, "  - %s\n", r)
				}
				return errors.New("delegation-hook promote: promotion gate not satisfied")
			}

			fmt.Fprintln(out, "Rutas que empezarán a bloquearse al promover:")
			if len(pairs) == 0 {
				fmt.Fprintln(out, "  (ninguna — no hay eventos would_block registrados)")
			}
			for _, p := range pairs {
				fmt.Fprintf(out, "  - %s: %s\n", p.Role, p.Path)
			}

			if !flagYes {
				fmt.Fprintln(out, "\nRevisá la lista y volvé a ejecutar con --yes para confirmar.")
				return errors.New("delegation-hook promote: confirmation required (--yes)")
			}

			if err := config.SetSubagentContainmentMode(config.DefaultPath(), slug, "block"); err != nil {
				return fmt.Errorf("delegation-hook promote: %w", err)
			}

			saveDecisionMemory(cmd, slug, pairs)

			fmt.Fprintf(out, "\npromoted: %s subagent_containment -> block\n", slug)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagYes, "yes", false, "Confirm the promotion (required — the gate alone never writes anything)")
	return cmd
}

// saveDecisionMemory persists a best-effort "decision" memory recording the
// exact reviewed list of breaking (role, path) pairs at promotion time
// (D7). Failure is non-fatal — the config write already succeeded, and the
// memory is a record of the decision, not the decision itself.
func saveDecisionMemory(cmd *cobra.Command, slug string, pairs []promoteBreakingPair) {
	svc, cleanup, err := initService()
	if err != nil {
		return
	}
	defer cleanup()

	var b strings.Builder
	fmt.Fprintf(&b, "Subagent containment promoted to \"block\" for %s.\n\n## Rutas revisadas que empezaron a bloquearse\n\n", slug)
	if len(pairs) == 0 {
		b.WriteString("(ninguna)\n")
	}
	for _, p := range pairs {
		fmt.Fprintf(&b, "- %s: %s\n", p.Role, p.Path)
	}

	_, _ = svc.Save(cmd.Context(), model.SaveRequest{
		Title:   fmt.Sprintf("Subagent containment promoted to block: %s", slug),
		Content: b.String(),
		Type:    model.TypeDecision,
		Project: slug,
	})
}
