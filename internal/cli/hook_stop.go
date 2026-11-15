package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/delivery"
	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// The stop hooks of SPEC-183. Claude Code runs `mneme hook stop` when the
// orchestrator is about to end its turn and `mneme hook subagent-stop` when a
// subagent is. Printing {"decision":"block","reason":...} on stdout with exit
// 0 keeps the agent working and hands it reason as its next instruction;
// printing nothing lets it stop. The hooks remind, they never judge: they
// check shape and state (is there a current delivery block, is there work
// after the last close), remind once, and stay quiet afterwards. Every
// failure lets the agent stop (fail-open), with at most one stderr line.

const (
	stopEventStop         = "stop"
	stopEventSubagent     = "subagent-stop"
	stopHookMaxPayload    = 1 << 20
	stopRegistrySchema    = 1
	stopRegistryDir       = "stop-hook"
	stopRegistryRetention = 7 * 24 * time.Hour
	stopRegistryDirPerm   = 0o700
	stopRegistryFilePerm  = 0o600
	stopReasonDelivery    = "delivery_contract_pending"
	stopReasonSummary     = "session_summary_pending"
)

// stopSessionIDPattern bounds the session_id that names the registry file:
// anything else could escape the registry directory, so it is never trusted.
var stopSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// stderr messages of the stop hooks. Every one is a single line that starts
// with "[mneme] <event> hook: "; stopWarn appends the newline and the event.
const (
	stopMsgInvalidStdin        = "[mneme] %s hook: invalid stdin JSON: %v"
	stopMsgStdinTooLarge       = "[mneme] %s hook: stdin exceeds %d bytes"
	stopMsgUnexpectedCaller    = "[mneme] %s hook: unexpected caller for this event (subagent=%t); nothing enforced"
	stopMsgInvalidSession      = "[mneme] %s hook: missing or invalid session_id; nothing enforced"
	stopMsgConfig              = "[mneme] %s hook: load config: %v"
	stopMsgNoProject           = "[mneme] %s hook: no project detected for cwd %q; nothing enforced"
	stopMsgOpenDB              = "[mneme] %s hook: open project database: %v"
	stopMsgUnresolvedRole      = "[mneme] %s hook: agent_id present without agent_type; delivery reminder not applied"
	stopMsgUnresolvedArchetype = "[mneme] %s hook: archetype of role %q not found in the manifest; delivery reminder not applied"
	stopMsgSpecList            = "[mneme] %s hook: list specs in implementing: %v"
	stopMsgDeliveryCheck       = "[mneme] %s hook: delivery check %s: %v"
	stopMsgSessionWindow       = "[mneme] %s hook: read session window: %v"
	stopMsgStdoutWrite         = "[mneme] %s hook: write decision to stdout: %v"
	stopMsgSaveRegistry        = "[mneme] %s hook: save stop registry: %v"
)

// hookStopInput decodes the one field of the Stop/SubagentStop payload that
// hookPreToolInput does not carry. hook_event_name is not read: the event is
// fixed by the subcommand (Dp2) and a field with no consumer would be dead
// code.
type hookStopInput struct {
	StopHookActive bool `json:"stop_hook_active"`
}

// stopRegistry is what the stop hooks already reminded in one session
// (<data dir>/stop-hook/<session_id>.json). It is what makes a reminder fire
// once instead of on every turn. Instants serialise as RFC3339Nano so the
// anchor compares with time.Equal against the store's created_at.
type stopRegistry struct {
	Schema                     int                    `json:"schema"`
	SessionID                  string                 `json:"session_id"`
	OrchestratorRemindedAt     *time.Time             `json:"orchestrator_reminded_at,omitempty"`
	OrchestratorRemindedAnchor *time.Time             `json:"orchestrator_reminded_anchor,omitempty"`
	DeliveryReminded           []stopDeliveryReminder `json:"delivery_reminded"`
}

// stopDeliveryReminder records that agent AgentID was reminded of SpecID.
// The key includes the agent: a relaunched implementer is a new attempt and
// deserves its own single reminder (Dp4).
type stopDeliveryReminder struct {
	AgentID string    `json:"agent_id"`
	SpecID  string    `json:"spec_id"`
	At      time.Time `json:"at"`
}

// stopDecision is the outcome of one rule: Block with the reason handed to
// the agent, the enforcelog label and the enforcelog targets.
type stopDecision struct {
	Block   bool
	Reason  string
	Label   string
	Targets []string
}

// stopGate is what the common rows (§3.1) resolved from the payload.
type stopGate struct {
	event     string
	sessionID string
	input     hookPreToolInput
	identity  CallerIdentity
	cfg       *config.Config
}

// stopWarn writes exactly one stderr line: format must not end in "\n".
func stopWarn(errW io.Writer, format, event string, args ...any) {
	fmt.Fprintf(errW, format+"\n", append([]any{event}, args...)...)
}

// runHookStop is the entry point of both stop hooks; event is stopEventStop
// or stopEventSubagent. It always returns nil: a hook error must never keep
// an agent from stopping. r is the payload, w receives the decision line, and
// now is the clock (injected for tests).
func runHookStop(ctx context.Context, event string, r io.Reader, w, errW io.Writer, now func() time.Time) error {
	gate, ok := stopCommonGate(event, r, errW)
	if !ok {
		return nil
	}
	t := now()
	regPath := stopRegistryPath(gate.cfg.Storage.DataDir, gate.sessionID)

	// Rows 5-6 (§3.2): the orchestrator's shortcuts, before any database.
	var orchReg stopRegistry
	threshold := gate.cfg.StopReminderAfter()
	if event == stopEventStop {
		if threshold == 0 {
			return nil
		}
		orchReg = loadStopRegistry(regPath, gate.sessionID)
		if at := orchReg.OrchestratorRemindedAt; at != nil && t.Sub(*at) < threshold {
			return nil
		}
	}

	database, slug, ok := openStopServices(gate, errW)
	if !ok {
		return nil
	}
	defer database.Close() //nolint:errcheck // read-only handle; nothing to flush

	var (
		dec stopDecision
		reg stopRegistry
	)
	switch event {
	case stopEventSubagent:
		dec, reg = runSubagentStopRule(ctx, gate, database, slug, regPath, errW, t)
	case stopEventStop:
		dec, reg = runOrchestratorStopRule(ctx, gate, database, slug, orchReg, threshold, errW, t)
	}
	if dec.Block {
		emitStopBlock(w, errW, gate, slug, regPath, dec, reg, t)
	}
	return nil
}

// runOrchestratorStopRule is the orchestrator rule of §3.5: read the
// session's current work window and decide. reg is the registry the
// shortcuts already read.
func runOrchestratorStopRule(ctx context.Context, gate stopGate, database *db.DB, slug string, reg stopRegistry, threshold time.Duration, errW io.Writer, now time.Time) (stopDecision, stopRegistry) {
	win, err := service.NewSessionProbe(store.NewMemoryStore(database)).Window(ctx, slug, gate.sessionID)
	if err != nil {
		stopWarn(errW, stopMsgSessionWindow, gate.event, err)
		return stopDecision{}, reg
	}
	return decideOrchestratorStop(win, reg, now, threshold)
}

// decideOrchestratorStop is the pure orchestrator decision: block once per
// work window when the window has work and its anchor (the first work
// memory after the last close) is at least threshold old. The window is
// identified by its anchor, compared with time.Equal, so a later close opens
// a new window that can be reminded again.
func decideOrchestratorStop(win *model.SessionWindow, reg stopRegistry, now time.Time, threshold time.Duration) (stopDecision, stopRegistry) {
	if win == nil || win.Count < 1 || now.Sub(win.Anchor) < threshold {
		return stopDecision{}, reg
	}
	if a := reg.OrchestratorRemindedAnchor; a != nil && a.Equal(win.Anchor) {
		return stopDecision{}, reg
	}
	at, anchor := now.UTC(), win.Anchor
	reg.OrchestratorRemindedAt = &at
	reg.OrchestratorRemindedAnchor = &anchor
	return stopDecision{
		Block:   true,
		Reason:  sessionSummaryPendingReason(win.SessionID, win.Count, now.Sub(win.Anchor)),
		Label:   stopReasonSummary,
		Targets: []string{win.SessionID},
	}, reg
}

// sessionSummaryPendingReason renders the orchestrator reason of §4. It names
// only mem_session_end, the tool whose call closes the window (D8); a
// checkpoint also closes it, but the reason names the action that closes the
// session.
func sessionSummaryPendingReason(sessionID string, n int, age time.Duration) string {
	return fmt.Sprintf("mneme: esta sesión (`%s`) tiene %d memorias posteriores a su último cierre y la ventana lleva %s sin resumen. Antes de terminar llama `mem_session_end` con session_id = %q y un summary de lo hecho. Este aviso se da una sola vez por ventana de trabajo.",
		sessionID, n, formatStopAge(age), sessionID)
}

// formatStopAge renders an age rounded to the minute without the seconds
// suffix: 47m, 1h5m, 0m.
func formatStopAge(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "0m"
	}
	return strings.TrimSuffix(d.String(), "0s")
}

// stopCommonGate applies rows 0-4 of §3.1, none of which touches a database:
// payload size and shape, stop_hook_active, the (event, identity) pairing of
// Dp2, the session id and the configuration. ok is false when the hook must
// let the agent stop right away.
func stopCommonGate(event string, r io.Reader, errW io.Writer) (stopGate, bool) {
	data, err := io.ReadAll(io.LimitReader(r, stopHookMaxPayload+1))
	if err != nil {
		stopWarn(errW, stopMsgInvalidStdin, event, err)
		return stopGate{}, false
	}
	if len(data) == 0 {
		return stopGate{}, false
	}
	if len(data) > stopHookMaxPayload {
		stopWarn(errW, stopMsgStdinTooLarge, event, stopHookMaxPayload)
		return stopGate{}, false
	}

	var input hookPreToolInput
	var stop hookStopInput
	if err := json.Unmarshal(data, &input); err != nil {
		stopWarn(errW, stopMsgInvalidStdin, event, err)
		return stopGate{}, false
	}
	if err := json.Unmarshal(data, &stop); err != nil {
		stopWarn(errW, stopMsgInvalidStdin, event, err)
		return stopGate{}, false
	}
	if stop.StopHookActive {
		return stopGate{}, false
	}

	identity := input.resolveIdentity()
	if identity.IsSubagent != (event == stopEventSubagent) {
		stopWarn(errW, stopMsgUnexpectedCaller, event, identity.IsSubagent)
		return stopGate{}, false
	}
	if !stopSessionIDPattern.MatchString(input.SessionID) {
		stopWarn(errW, stopMsgInvalidSession, event)
		return stopGate{}, false
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		stopWarn(errW, stopMsgConfig, event, err)
		return stopGate{}, false
	}
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}
	return stopGate{event: event, sessionID: input.SessionID, input: input, identity: identity, cfg: cfg}, true
}

// openStopServices applies rows 7-8 of §3.3: it resolves the project slug
// (--project wins over the payload's cwd) and opens the project database
// read-only, without migrations, as the pre-tool-use hook does. The caller
// closes the database.
func openStopServices(gate stopGate, errW io.Writer) (*db.DB, string, bool) {
	slug := flagProject
	if slug == "" {
		wd, err := os.Getwd()
		if err != nil {
			wd = ""
		}
		cwd := resolveHookCWD(gate.input, wd)
		if cwd != "" {
			slug, _ = project.NewDetector(cwd).DetectProject()
		}
		if slug == "" {
			stopWarn(errW, stopMsgNoProject, gate.event, cwd)
			return nil, "", false
		}
	}

	database, err := db.OpenReadOnly(gate.cfg.ProjectDBPath(slug))
	if err != nil {
		stopWarn(errW, stopMsgOpenDB, gate.event, err)
		return nil, "", false
	}
	return database, slug, true
}

// runSubagentStopRule is the implementer rule of §3.4. Only an archetype that
// may write the entrega section is reminded; standard specs in implementing
// that are not frozen are checked with SDDService.DeliveryCheck, and a spec
// whose check fails is treated as satisfied (fail-open per spec).
func runSubagentStopRule(ctx context.Context, gate stopGate, database *db.DB, slug, regPath string, errW io.Writer, now time.Time) (stopDecision, stopRegistry) {
	identity := gate.identity
	if identity.RoleSource == "unresolved" {
		stopWarn(errW, stopMsgUnresolvedRole, gate.event)
		return stopDecision{}, stopRegistry{}
	}
	archetype := identity.Role
	if !builtinArchetypes[archetype] {
		resolved, ok := archetypeFromManifest(gate.cfg.ProjectDBPath(slug), slug, identity.Role)
		if !ok {
			stopWarn(errW, stopMsgUnresolvedArchetype, gate.event, identity.Role)
			return stopDecision{}, stopRegistry{}
		}
		archetype = resolved
	}
	if !model.TaskSectionAllows(model.TaskSectionDelivery, archetype) {
		return stopDecision{}, stopRegistry{}
	}

	sdd := service.NewSDDService(store.NewSDDStore(database), gate.cfg, slug, nil)
	resp, err := sdd.SpecList(ctx, model.SpecListRequest{Status: model.SpecStatusImplementing, Limit: model.ListMaxLimit})
	if err != nil {
		stopWarn(errW, stopMsgSpecList, gate.event, err)
		return stopDecision{}, stopRegistry{}
	}

	var checks []*model.DeliveryCheck
	for _, spec := range resp.Specs {
		if _, frozen := resp.Frozen[spec.ID]; frozen || spec.Lane != model.LaneStandard {
			continue
		}
		check, err := sdd.DeliveryCheck(ctx, spec.ID)
		if err != nil {
			stopWarn(errW, stopMsgDeliveryCheck, gate.event, spec.ID, err)
			continue
		}
		checks = append(checks, check)
	}
	if !anyDeliveryPending(checks) {
		return stopDecision{}, stopRegistry{}
	}
	return decideSubagentStop(checks, loadStopRegistry(regPath, gate.sessionID), identity.AgentID, now)
}

// anyDeliveryPending reports whether some check is not satisfied, so the
// registry is read only when the decision depends on it.
func anyDeliveryPending(checks []*model.DeliveryCheck) bool {
	for _, c := range checks {
		if !c.Satisfied() {
			return true
		}
	}
	return false
}

// decideSubagentStop is the pure implementer decision: block when some
// unsatisfied spec was not yet reminded to agentID. The reason lists every
// unsatisfied spec, reminded or not, so the list is complete; the returned
// registry records the new reminders.
func decideSubagentStop(checks []*model.DeliveryCheck, reg stopRegistry, agentID string, now time.Time) (stopDecision, stopRegistry) {
	reminded := make(map[string]bool, len(reg.DeliveryReminded))
	for _, r := range reg.DeliveryReminded {
		if r.AgentID == agentID {
			reminded[r.SpecID] = true
		}
	}

	var pending []*model.DeliveryCheck
	var fresh []string
	for _, c := range checks {
		if c.Satisfied() {
			continue
		}
		pending = append(pending, c)
		if !reminded[c.SpecID] {
			fresh = append(fresh, c.SpecID)
		}
	}
	if len(fresh) == 0 {
		return stopDecision{}, reg
	}

	sortDeliveryChecks(pending)
	targets := make([]string, 0, len(pending))
	for _, c := range pending {
		targets = append(targets, c.SpecID)
	}
	for _, id := range fresh {
		reg.DeliveryReminded = append(reg.DeliveryReminded, stopDeliveryReminder{AgentID: agentID, SpecID: id, At: now.UTC()})
	}
	return stopDecision{
		Block:   true,
		Reason:  deliveryPendingReason(pending),
		Label:   stopReasonDelivery,
		Targets: targets,
	}, reg
}

// sortDeliveryChecks orders checks by spec id, numerically within the same
// prefix (SPEC-99 before SPEC-100).
func sortDeliveryChecks(checks []*model.DeliveryCheck) {
	sort.Slice(checks, func(i, j int) bool {
		a, b := checks[i].SpecID, checks[j].SpecID
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return a < b
	})
}

// deliveryPendingReason renders the implementer reason of §4. It names only
// spec_doc_write, the tool whose call resolves the block (D8), and tells a
// bystander whose spec is not listed to finish without writing.
func deliveryPendingReason(checks []*model.DeliveryCheck) string {
	sorted := append([]*model.DeliveryCheck(nil), checks...)
	sortDeliveryChecks(sorted)

	var b strings.Builder
	b.WriteString("mneme: hay specs en implementing cuya sección entrega de tarea.md no tiene un bloque de contrato de entrega vigente:\n")
	for _, c := range sorted {
		fmt.Fprintf(&b, "- %s (%s)\n", c.SpecID, deliveryCheckLabel(c))
	}
	id := "<SPEC>"
	if len(sorted) == 1 {
		id = sorted[0].SpecID
	}
	fmt.Fprintf(&b, "Solo si una de estas specs es la que te asignaron: escribe tu entrega con `spec_doc_write` (id = %q, kind = \"task\", section = \"entrega\") incluyendo el bloque entre %s y %s (formato: skill mneme-flow, references/entrega.md); si no terminaste, declara estado falta_contexto o bloqueado, que también es una entrega válida. Si no trabajaste en ninguna de estas specs, termina sin escribir nada. Este aviso se da una sola vez por spec.",
		id, delivery.StartMarker, delivery.EndMarker)
	return b.String()
}

// deliveryCheckLabel is the parenthesis of one spec line in the reason.
func deliveryCheckLabel(c *model.DeliveryCheck) string {
	switch {
	case c.Contract == model.DeliveryContractInvalid:
		return "bloque inválido: " + c.Detail
	case c.Contract == model.DeliveryContractValid && !c.Current:
		return "bloque anterior a la última entrada a implementing"
	case c.Detail != "":
		return "bloque ausente: " + c.Detail
	default:
		return "bloque ausente"
	}
}

// emitStopBlock prints the decision and, only when the agent actually
// received it, records it in the registry and in the enforcelog. A failed
// stdout write records nothing, so the next Stop can remind again.
func emitStopBlock(w, errW io.Writer, gate stopGate, slug, regPath string, dec stopDecision, reg stopRegistry, now time.Time) {
	if err := renderStopDecision(w, dec.Reason); err != nil {
		stopWarn(errW, stopMsgStdoutWrite, gate.event, err)
		return
	}
	reg.Schema = stopRegistrySchema
	reg.SessionID = gate.sessionID
	if err := saveStopRegistry(regPath, reg, now); err != nil {
		stopWarn(errW, stopMsgSaveRegistry, gate.event, err)
	}

	ev := stopEnforcelogEvent(gate, slug, dec, now)
	_ = enforcelog.Append(enforcelogPath(gate.cfg.Storage.DataDir, slug), ev, enforcelog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
}

// stopEnforcelogEvent builds the enforcelog line of one block (§7). Its Tool
// is the lifecycle event, which every containment consumer filters out.
func stopEnforcelogEvent(gate stopGate, slug string, dec stopDecision, now time.Time) enforcelog.Event {
	ev := enforcelog.Event{
		TS:         now.UTC(),
		Session:    gate.sessionID,
		Project:    slug,
		Caller:     "orchestrator",
		RoleSource: gate.identity.RoleSource,
		Tool:       enforcelog.ToolStop,
		Target:     strings.Join(dec.Targets, ","),
		Decision:   enforcelog.DecisionBlock,
		Reason:     dec.Label,
	}
	if gate.event == stopEventSubagent {
		ev.Caller = "subagent"
		ev.AgentID = gate.identity.AgentID
		ev.Role = gate.identity.Role
		ev.Tool = enforcelog.ToolSubagentStop
	}
	return ev
}

// renderStopDecision writes the block decision as exactly one JSON line.
// HTML escaping is off so the reason stays readable in the transcript.
func renderStopDecision(w io.Writer, reason string) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{Decision: "block", Reason: reason}); err != nil {
		return fmt.Errorf("cli: stop hook: render decision: %w", err)
	}
	return nil
}

// stopRegistryPath is the per-session registry file under the data dir, so it
// follows --data-dir, MNEME_DATA_DIR and HOME like every other mneme file.
func stopRegistryPath(dataDir, sessionID string) string {
	return filepath.Join(dataDir, stopRegistryDir, sessionID+".json")
}

// loadStopRegistry reads the registry at path. A missing, corrupt or
// foreign-schema file is an empty registry: the worst case is one repeated
// reminder, never a loop.
func loadStopRegistry(path, sessionID string) stopRegistry {
	empty := stopRegistry{Schema: stopRegistrySchema, SessionID: sessionID}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var reg stopRegistry
	if json.Unmarshal(data, &reg) != nil || reg.Schema != stopRegistrySchema {
		return empty
	}
	reg.SessionID = sessionID
	return reg
}

// saveStopRegistry writes reg atomically (temporary file in the same
// directory, then rename) with owner-only permissions, and then removes the
// registry files older than stopRegistryRetention. It runs only on the rare
// block path, so the cleanup costs nothing on the happy path.
func saveStopRegistry(path string, reg stopRegistry, now time.Time) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, stopRegistryDirPerm); err != nil {
		return fmt.Errorf("cli: stop registry: create dir: %w", err)
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("cli: stop registry: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("cli: stop registry: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()        //nolint:errcheck // the write error is the one reported
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup of the temp file
		return fmt.Errorf("cli: stop registry: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup of the temp file
		return fmt.Errorf("cli: stop registry: close: %w", err)
	}
	if err := os.Chmod(tmpName, stopRegistryFilePerm); err != nil {
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup of the temp file
		return fmt.Errorf("cli: stop registry: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup of the temp file
		return fmt.Errorf("cli: stop registry: rename: %w", err)
	}
	pruneStopRegistry(dir, path, now)
	return nil
}

// pruneStopRegistry removes the files in dir last modified before the
// retention window, except keep. Best-effort: a file that cannot be read or
// removed stays for the next block.
func pruneStopRegistry(dir, keep string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-stopRegistryRetention)
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() || full == keep {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		os.Remove(full) //nolint:errcheck // best-effort retention
	}
}
