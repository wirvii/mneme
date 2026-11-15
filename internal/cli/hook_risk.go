package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/enforcement"
	"github.com/wirvii/mneme/internal/laneaudit"
	"github.com/wirvii/mneme/internal/risk"
	"github.com/wirvii/mneme/internal/subagents"
)

// Block messages of SPEC-185 layer B. D6: a message names a command or a tool
// only when its reader can run it and that resolves the block. An implementer
// cannot write the plan, so its messages name none; the orchestrator can, so
// its message names spec_doc_write.

// riskPathBlockMessage: rel, item, risk.Item(item).Title(), form, item.
const riskPathBlockMessage = "⛔ mneme: ruta de riesgo para un implementador: %s (ítem %d, %s; con: %s).\n" +
	"Ninguna spec standard en implementing declara ese riesgo para esta ruta, así que este cambio no es tuyo: para y repórtalo al orquestador. " +
	"La única salida es que el orquestador (o el arquitecto) declare \"Risk: item %d (razón)\" en la sección plan (o diseno) de tarea.md de la spec, o que reclasifique o replantee el encargo. " +
	"No busques otra vía de escritura: la denegación es por rol, no por herramienta.\n"

// riskTestBlockMessage: detail ("añade t.Skip( a", "deja sin funciones de prueba", "borra", "vacía"), rel, form.
const riskTestBlockMessage = "⛔ mneme: vigilancia de pruebas: %s %s (ítem 6, No test would catch the regression; con: %s).\n" +
	"Un subagente no borra ni salta pruebas por su cuenta. Si una prueba está en rojo, para y repórtala en tu entrega como prueba en rojo. " +
	"Si de verdad debe borrarse o saltarse, la única salida es que el orquestador (o el arquitecto) declare \"Risk: item 6 (razón)\" en la sección plan (o diseno) de tarea.md de la spec.\n"

// riskOrchestratorBlockMessage: rel, item, title, form, item. The orchestrator
// CAN write the plan, so this one names the tool that resolves it.
const riskOrchestratorBlockMessage = "⛔ mneme: ruta de riesgo sin dueño: %s (ítem %d, %s; con: %s).\n" +
	"Nadie delega esta ruta, pero tampoco ninguna spec standard en implementing declara ese riesgo para ella. " +
	"Si el cambio es parte del encargo, declara \"Risk: item %d (razón)\" en la sección plan de tarea.md de la spec (spec_doc_write kind task section plan) y repite la escritura; si no lo es, abre un ítem de backlog y no lo hagas en línea.\n"

// riskInlineBlockMessage: detail ("casa el ítem N en", "añade t.Skip( a", ...), rel.
const riskInlineBlockMessage = "⛔ mneme: permiso inline trivial: %s %s.\n" +
	"En línea solo cabe lo trivial sin riesgo, y una spec trivial no admite declarar ninguno. Reclasifica la spec a standard y delega el cambio.\n"

// Warnings of layer B (exit 0).
const (
	riskArchetypeUnresolvedMessage = "⚠ mneme: denegación por riesgo: no se pudo resolver el arquetipo del rol %q en el manifiesto; la capa no se aplica en esta llamada.\n"
	riskLookupFailedMessage        = "⚠ mneme: denegación por riesgo: no se pudo leer la declaración de riesgo de las specs (%v); la capa no se aplica en esta llamada.\n"
)

// blockMessagesD6 lists every SPEC-185 block message, so the D6 test checks
// the same texts the hook prints.
var blockMessagesD6 = []string{
	dataDirBlockMessage, riskPathBlockMessage, riskTestBlockMessage,
	riskOrchestratorBlockMessage, riskInlineBlockMessage,
}

// Enforcelog reasons of layer B's warnings.
const (
	riskArchetypeUnresolvedReason = "risk_archetype_unresolved"
	riskLookupFailedReason        = "risk_lookup_failed"
)

// riskDependencyTarget names a dependency command that rewrites no manifest
// the hook can name (pip, cargo, poetry).
const riskDependencyTarget = "<dependency>"

// riskEditTools are the file tools whose new content the test watch can
// rebuild; NotebookEdit is judged by its path only.
var riskEditTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true}

// homePrefixes are the spellings of the home directory a path can start with
// when ExpandHome could not expand it.
var homePrefixes = []string{"~", "$HOME", "${HOME}", "$USERPROFILE", "${USERPROFILE}"}

// riskWrite is one judged write: the pure facts plus whether it came through
// the trivial inline grant.
type riskWrite struct {
	facts  enforcement.RiskFacts
	inline bool
}

// checkRiskGuard is SPEC-185's layer B: it reports true, after printing the
// rejection and logging every verdict, when a non-whitelisted write every
// earlier guard allowed touches a risk path, changes a dependency or harms a
// test, and no open standard spec declares that risk. Subjects: any resolved
// subagent archetype (Bash), an implementer (file tools), and the
// orchestrator (any non-whitelisted write that reached this step).
//
// It runs only when the delegation step resolved its facts (facts.cfg set);
// a write with no risk item and no test signal returns before the database is
// opened. An archetype it cannot resolve, or declarations it cannot read,
// allow the call with a warning (fail-open); a missing database or no open
// standard spec means nobody declared, and blocks.
func checkRiskGuard(input hookPreToolInput, identity CallerIdentity, cwd string, facts *delegationFacts, errW io.Writer) bool {
	if facts == nil || facts.cfg == nil {
		return false
	}
	subject, ok := riskSubjectFor(input, identity, facts, errW)
	if !ok {
		return false
	}
	table, err := facts.cfg.LaneRiskTable()
	if err != nil {
		table = risk.DefaultTable()
	}
	data := enforcement.NewDataDirContext(newSDDContext(input), dataDirRoots(facts.cfg))
	writes := collectRiskWrites(input, subject, cwd, table, facts, data)
	if len(writes) == 0 {
		return false
	}
	specs, err := loadRiskDeclarations(facts.cfg, facts.slug)
	if err != nil {
		fmt.Fprintf(errW, riskLookupFailedMessage, err)
		logRiskEvent(input, identity, facts, enforcement.RiskFacts{Target: input.filePath()}, enforcelog.DecisionAllow, riskLookupFailedReason, enforcement.RiskVerdict{})
		return false
	}
	declared := declaredFrom(specs)
	for _, w := range writes {
		s := subject
		s.Inline = w.inline
		for _, v := range enforcement.EvaluateRisk(s, w.facts, declared) {
			decision := enforcelog.DecisionAllow
			if v.Block {
				decision = enforcelog.DecisionBlock
			}
			logRiskEvent(input, identity, facts, w.facts, decision, v.Reason, v)
			if v.Block {
				printRiskBlock(errW, s, w.facts, v)
				return true
			}
		}
	}
	return false
}

// riskSubjectFor resolves who is writing. A subagent's archetype comes from
// its role when built in, else from the manifest the delegation step already
// read; one it cannot resolve allows with a warning. A subagent is judged on
// Bash only when its archetype carries Bash, and on file tools only when it is
// an implementer: the capability layer already forbids the rest.
func riskSubjectFor(input hookPreToolInput, identity CallerIdentity, facts *delegationFacts, errW io.Writer) (enforcement.RiskSubject, bool) {
	if !identity.IsSubagent {
		return enforcement.RiskSubject{Orchestrator: true}, true
	}
	archetype, ok := riskArchetype(identity.Role, facts.manifestJSON)
	if !ok {
		fmt.Fprintf(errW, riskArchetypeUnresolvedMessage, identity.Role)
		logRiskEvent(input, identity, facts, enforcement.RiskFacts{Target: input.filePath()}, enforcelog.DecisionAllow, riskArchetypeUnresolvedReason, enforcement.RiskVerdict{})
		return enforcement.RiskSubject{}, false
	}
	implementer := subagents.IsImplementer(archetype)
	if input.ToolName == toolBash {
		if !slices.Contains(subagents.PermissionTable[archetype].Tools, toolBash) {
			return enforcement.RiskSubject{}, false
		}
	} else if !implementer {
		return enforcement.RiskSubject{}, false
	}
	return enforcement.RiskSubject{Subagent: true, Implementer: implementer}, true
}

// riskArchetype returns the archetype of role: itself when built in, else the
// effective archetype of its manifest entry, which must be built in too.
func riskArchetype(role, manifestJSON string) (subagents.Role, bool) {
	if builtinArchetypes[role] {
		return subagents.Role(role), true
	}
	var entries []hookManifestEntry
	if err := json.Unmarshal([]byte(manifestJSON), &entries); err != nil {
		return "", false
	}
	entry, ok := findManifestEntryByRole(entries, role)
	if !ok {
		return "", false
	}
	archetype := entry.effectiveArchetype()
	return archetype, builtinArchetypes[string(archetype)]
}

// collectRiskWrites lists the writes of the call worth judging: those with a
// risk item or a test signal. Exempt targets (the orchestrator's whitelist; a
// subagent's scratch space and mneme's data) and paths outside the working
// tree are skipped.
func collectRiskWrites(input hookPreToolInput, subject enforcement.RiskSubject, cwd string, table risk.Table, facts *delegationFacts, data enforcement.DataDirContext) []riskWrite {
	var out []riskWrite
	add := func(f enforcement.RiskFacts) {
		if len(f.Items) > 0 || f.Test != nil {
			out = append(out, riskWrite{facts: f, inline: slices.Contains(facts.inlineGranted, f.Rel)})
		}
	}
	if input.ToolName != toolBash {
		target := input.filePath()
		if target == "" || enforcement.IsRiskExempt(target, subject, facts.pc, data) {
			return nil
		}
		rel, outOfTree := normalisePathForOwnership(enforcement.ExpandHome(target, facts.pc), cwd)
		if outOfTree || rel == "" {
			return nil
		}
		f := enforcement.RiskFacts{Tool: input.ToolName, Form: input.ToolName, Target: target, Rel: rel,
			Items: laneaudit.RiskItemsForPath(rel, table)}
		if lang, ok := enforcement.TestFile(rel); ok && riskEditTools[input.ToolName] {
			f.Test = fileTestWatch(input, lang, target, rel, cwd)
		}
		add(f)
		return out
	}
	for _, form := range enforcement.RiskWriteForms(input.ToolInput.Command) {
		if f, ok := bashRiskFacts(form, subject, cwd, table, facts.pc, data); ok {
			add(f)
		}
	}
	return out
}

// fileTestWatch rebuilds the file as the tool would leave it and compares the
// skip markers and test functions before and after. nil means no signal.
func fileTestWatch(input hookPreToolInput, lang enforcement.TestLanguage, target, rel, cwd string) *enforcement.TestWatch {
	path := target
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, filepath.FromSlash(rel))
	}
	data, _ := os.ReadFile(path) //nolint:errcheck // an absent or unreadable file is an empty "before"
	before := string(data)
	after := before
	switch input.ToolName {
	case "Write":
		after = input.ToolInput.Content
	case "Edit":
		after, _ = enforcement.ApplyEdit(before, input.ToolInput.OldString, input.ToolInput.NewString, input.ToolInput.ReplaceAll)
	case "MultiEdit":
		for _, e := range input.ToolInput.Edits {
			after, _ = enforcement.ApplyEdit(after, e.OldString, e.NewString, e.ReplaceAll)
		}
	}
	w := enforcement.TestWatch{
		Language:     lang,
		MarkersAdded: enforcement.SkipMarkersAdded(lang, before, after),
		LosesTests:   enforcement.HasTestFunction(lang, before) && !enforcement.HasTestFunction(lang, after),
	}
	if len(w.MarkersAdded) == 0 && !w.LosesTests {
		return nil
	}
	return &w
}

// bashRiskFacts turns one Bash write form into facts. A dependency command
// is a synthetic write: never exempt, its manifest (or <dependency>) is the
// target, and item 5 is merged in once.
func bashRiskFacts(form enforcement.RiskWriteForm, subject enforcement.RiskSubject, cwd string, table risk.Table, pc enforcement.PathContext, data enforcement.DataDirContext) (enforcement.RiskFacts, bool) {
	f := enforcement.RiskFacts{Tool: toolBash, Form: form.Form, Target: form.Operand, Synthetic: form.Synthetic}
	if form.Synthetic {
		f.Rel = form.Operand
		if f.Rel == "" {
			f.Rel, f.Target = riskDependencyTarget, riskDependencyTarget
		} else {
			f.Items = laneaudit.RiskItemsForPath(f.Rel, table)
		}
		if form.Implicit != 0 && !slices.Contains(f.Items, form.Implicit) {
			f.Items = append(f.Items, form.Implicit)
		}
		return f, true
	}
	if form.Operand == "" || enforcement.IsRiskExempt(form.Operand, subject, pc, data) {
		return f, false
	}
	rel, outOfTree := normalisePathForOwnership(enforcement.ExpandHome(form.Operand, pc), cwd)
	if outOfTree || rel == "" {
		return f, false
	}
	f.Rel = rel
	f.Items = laneaudit.RiskItemsForPath(rel, table)
	if lang, ok := enforcement.TestFile(rel); ok {
		removes := form.Removes
		if form.MovedTo != "" {
			removes = moveRemovesTest(form, cwd, pc)
		}
		w := enforcement.TestWatch{
			Language:     lang,
			Removed:      removes || form.Truncates,
			Truncated:    form.Truncates,
			MarkersAdded: enforcement.SkipMarkersAdded(lang, "", form.Script),
		}
		if w.Removed || len(w.MarkersAdded) > 0 {
			f.Test = &w
		}
	}
	return f, true
}

// moveRemovesTest decides, with the working directory, whether moving the
// source of an mv takes the test away: the destination leaves the tree or is
// not a test file. A destination still spelled from the home directory (no
// home to expand it with) keeps the leaf's answer; an existing directory
// receives the source's name.
func moveRemovesTest(form enforcement.RiskWriteForm, cwd string, pc enforcement.PathContext) bool {
	dest := enforcement.ExpandHome(form.MovedTo, pc)
	for _, h := range homePrefixes {
		if strings.HasPrefix(dest, h) {
			return form.Removes
		}
	}
	abs := filepath.FromSlash(dest)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		abs = filepath.Join(abs, filepath.Base(filepath.FromSlash(form.Operand)))
	}
	rel, outOfTree := normalisePathForOwnership(abs, cwd)
	if outOfTree {
		return true
	}
	_, isTest := enforcement.TestFile(rel)
	return !isTest
}

// printRiskBlock writes the message of the subject that was blocked.
func printRiskBlock(w io.Writer, s enforcement.RiskSubject, f enforcement.RiskFacts, v enforcement.RiskVerdict) {
	testHarm := v.Item == risk.ItemUntestable
	switch {
	case s.Inline && testHarm:
		fmt.Fprintf(w, riskInlineBlockMessage, testHarmPhrase(f.Test), f.Rel)
	case s.Inline:
		fmt.Fprintf(w, riskInlineBlockMessage, fmt.Sprintf("casa el ítem %d en", int(v.Item)), f.Rel)
	case s.Orchestrator:
		fmt.Fprintf(w, riskOrchestratorBlockMessage, f.Rel, int(v.Item), v.Item.Title(), f.Form, int(v.Item))
	case testHarm:
		fmt.Fprintf(w, riskTestBlockMessage, testHarmPhrase(f.Test), f.Rel, f.Form)
	default:
		fmt.Fprintf(w, riskPathBlockMessage, f.Rel, int(v.Item), v.Item.Title(), f.Form, int(v.Item))
	}
}

// testHarmPhrase words the strongest test signal, with the priority
// EvaluateRisk uses for its detail: emptied, removed, marker, no tests left.
func testHarmPhrase(t *enforcement.TestWatch) string {
	switch {
	case t == nil:
		return ""
	case t.Removed && t.Truncated:
		return "vacía"
	case t.Removed:
		return "borra"
	case len(t.MarkersAdded) > 0:
		return "añade " + t.MarkersAdded[0] + " a"
	}
	return "deja sin funciones de prueba"
}

// logRiskEvent appends one best-effort layer B event. Target is the path
// relative to the tree, or the manifest (or <dependency>) of a dependency
// command. Never a discovery memory.
func logRiskEvent(input hookPreToolInput, identity CallerIdentity, facts *delegationFacts, f enforcement.RiskFacts, decision enforcelog.Decision, reason string, v enforcement.RiskVerdict) {
	caller := callerOrchestrator
	if identity.IsSubagent {
		caller = callerSubagent
	}
	target := f.Rel
	if target == "" {
		target = f.Target
	}
	ev := enforcelog.Event{
		TS:         time.Now().UTC(),
		Session:    input.SessionID,
		Project:    facts.slug,
		Caller:     caller,
		AgentID:    identity.AgentID,
		Role:       identity.Role,
		RoleSource: identity.RoleSource,
		Tool:       input.ToolName,
		Target:     target,
		Decision:   decision,
		Reason:     reason,
		RiskItem:   int(v.Item),
		Spec:       v.Spec,
		Source:     v.Source,
		Detail:     v.Detail,
	}
	_ = enforcelog.Append(enforcelogPath(facts.cfg.Storage.DataDir, facts.slug), ev, enforcelog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
}
