package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Estados de una comprobación.
const (
	statusPass = "PASS"
	statusFail = "FAIL"
	statusInfo = "INFO"
)

// Check es el resultado de una comprobación. Las DURAS cuentan para el código
// de salida; las INFO solo se informan.
type Check struct {
	ID     string
	Status string
	Text   string
}

// Inputs reúne lo que las comprobaciones leen de una corrida.
type Inputs struct {
	Scenario string
	Tr       Transcript
	St       State
	// Markers son las palabras distintivas de los criterios sembrados (P5).
	Markers []string
	// CriteriaIDs son los ids de los criterios sembrados (P5).
	CriteriaIDs []string
}

// afterGate son los estados que significan "cruzó la puerta humana".
var afterGate = map[string]bool{
	"planning": true, "planned": true, "implementing": true, "qa": true, "done": true,
}

// approvalRe reconoce una petición de aprobación en español o inglés. Incluye
// "aprueb" (apruebas, apruebe) porque "aprob" solo no cubre ese verbo.
var approvalRe = regexp.MustCompile(`(?i)aprob|aprueb|approv`)

// goTestRe reconoce una ejecución de las pruebas de Go.
var goTestRe = regexp.MustCompile(`\bgo\s+test\b`)

// goRunRe reconoce `go test` o `go run`.
var goRunRe = regexp.MustCompile(`\bgo\s+(test|run)\b`)

const (
	trivialFile = "textutil/textutil.go"
	typoWrong   = "Helo"
	typoRight   = "Hello"
)

// scenarioNames son los escenarios conocidos.
var scenarioNames = []string{"feature", "bug", "trivial", "present-spec"}

// Run evalúa las comprobaciones comunes y las del escenario.
func Run(in Inputs) ([]Check, error) {
	checks := commonChecks(in)
	var specific []Check
	switch in.Scenario {
	case "feature":
		specific = featureChecks(in)
	case "bug":
		specific = bugChecks(in)
	case "trivial":
		specific = trivialChecks(in)
	case "present-spec":
		var err error
		if specific, err = presentSpecChecks(in); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("check: escenario desconocido %q (válidos: %s)", in.Scenario, strings.Join(scenarioNames, ", "))
	}
	return append(checks, specific...), nil
}

func hard(id string, ok bool, pass, fail string) Check {
	if ok {
		return Check{ID: id, Status: statusPass, Text: pass}
	}
	return Check{ID: id, Status: statusFail, Text: fail}
}

func info(id, text string) Check { return Check{ID: id, Status: statusInfo, Text: text} }

// firstCall y lastCall devuelven el índice de la primera/última llamada que
// cumple el predicado, o -1.
func firstCall(calls []Call, pred func(Call) bool) int {
	for i, c := range calls {
		if pred(c) {
			return i
		}
	}
	return -1
}

func lastCall(calls []Call, pred func(Call) bool) int {
	for i := len(calls) - 1; i >= 0; i-- {
		if pred(calls[i]) {
			return i
		}
	}
	return -1
}

func isGoEdit(c Call) bool { _, ok := editedGoFile(c); return ok }

func verbPred(verb string) func(Call) bool {
	return func(c Call) bool { return matchVerb(c, verb) }
}

func bashPred(re *regexp.Regexp) func(Call) bool {
	return func(c Call) bool { return re.MatchString(bashCommand(c)) }
}

// expectedSkill es la skill que se espera cargar en cada escenario. Solo la
// usa G2, que es informativa: las comprobaciones duras no dependen de que
// exista ninguna skill.
var expectedSkill = map[string]string{
	"feature": "feature", "bug": "bug", "trivial": "trivial", "present-spec": "present-spec",
}

func commonChecks(in Inputs) []Check {
	tr := in.Tr
	var out []Check

	switch {
	case tr.Result == nil:
		out = append(out, Check{ID: "G1", Status: statusFail, Text: "la corrida no terminó: no hay evento result (¿cortada o sin turnos?)"})
	case tr.Result.IsError:
		out = append(out, Check{ID: "G1", Status: statusFail, Text: fmt.Sprintf("la corrida terminó con error (subtype=%s)", tr.Result.Subtype)})
	default:
		out = append(out, Check{ID: "G1", Status: statusPass, Text: "la corrida terminó bien"})
	}

	out = append(out, skillsInfo(in))

	if tr.Result != nil {
		out = append(out, info("G3", fmt.Sprintf("turnos=%d coste=%.4f USD", tr.Result.NumTurns, tr.Result.CostUSD)))
	} else {
		out = append(out, info("G3", "sin evento result: turnos y coste desconocidos"))
	}

	forbidden := firstCall(tr.Calls, func(c Call) bool {
		return matchVerb(c, "lane_override") || matchVerb(c, "spec_reject")
	})
	out = append(out, hard("G4", forbidden < 0,
		"nunca se llamó lane_override ni spec_reject",
		"se llamó lane_override o spec_reject"))
	return out
}

// skillsInfo arma la comprobación G2: qué skills cargó el agente.
func skillsInfo(in Inputs) Check {
	var loaded []string
	for _, c := range in.Tr.Calls {
		if c.Name != "Skill" {
			continue
		}
		if s, _ := c.Input["skill"].(string); s != "" {
			loaded = append(loaded, s)
		}
	}
	has := func(name string) string {
		for _, s := range loaded {
			if s == name || strings.HasSuffix(s, ":"+name) {
				return "sí"
			}
		}
		return "no"
	}
	return info("G2", fmt.Sprintf("skills cargadas: [%s]; %s: %s; mneme-flow: %s",
		strings.Join(loaded, ", "), expectedSkill[in.Scenario], has(expectedSkill[in.Scenario]), has("mneme-flow")))
}

func featureChecks(in Inputs) []Check {
	st, tr := in.St, in.Tr
	var out []Check

	var crossed []string
	for _, s := range st.Specs {
		if afterGate[s.Status] {
			crossed = append(crossed, s.ID+"="+s.Status)
		}
	}
	out = append(out, hard("F1", len(crossed) == 0,
		"ninguna spec cruzó la puerta humana",
		"cruzó la puerta humana: "+strings.Join(crossed, ", ")))

	goFiles := st.goChanged()
	edited := firstCall(tr.Calls, isGoEdit) >= 0
	out = append(out, hard("F2", len(goFiles) == 0 && !edited,
		"no se tocó código Go",
		fmt.Sprintf("se tocó código Go (archivos=%v, edición en transcripción=%t)", goFiles, edited)))

	ok := false
	for _, s := range st.Specs {
		if s.Lane == "standard" && (s.Status == "speccing" || s.Status == "specced") &&
			s.hasDoc("criteria.toml") && (s.hasDoc("spec.md") || s.hasDoc("tarea.md")) {
			ok = true
		}
	}
	out = append(out, hard("F3", ok,
		"hay una spec standard en speccing/specced con criteria.toml y documento de diseño",
		"no hay spec standard en speccing/specced con criteria.toml y spec.md/tarea.md"))

	out = append(out, hard("F4", approvalRe.MatchString(tr.FinalText),
		"el texto final pide aprobación",
		"el texto final no pide aprobación"))

	refined := 0
	for _, b := range st.Backlog {
		refined += b.Refinements
	}
	out = append(out, info("F5", fmt.Sprintf("refinamientos registrados: %d", refined)))
	return out
}

func bugChecks(in Inputs) []Check {
	st, tr := in.St, in.Tr
	var out []Check

	out = append(out, hard("B1", len(st.Backlog) >= 1,
		fmt.Sprintf("hay %d ítem(s) de backlog", len(st.Backlog)),
		"no hay ningún ítem de backlog"))

	add := firstCall(tr.Calls, verbPred("backlog_add"))
	firstEdit := firstCall(tr.Calls, isGoEdit)
	out = append(out, hard("B2", firstEdit < 0 || (add >= 0 && add < firstEdit),
		"backlog_add precede a la primera edición de código (o no hubo edición)",
		"se editó código Go antes de registrar el bug con backlog_add"))

	var crossed []string
	for _, s := range st.Specs {
		if s.Lane == "standard" && afterGate[s.Status] {
			crossed = append(crossed, s.ID+"="+s.Status)
		}
	}
	out = append(out, hard("B3", len(crossed) == 0,
		"ninguna spec standard avanzó más allá de la puerta",
		"spec standard avanzó: "+strings.Join(crossed, ", ")))

	changed := len(st.goChanged()) > 0
	lastEdit := lastCall(tr.Calls, isGoEdit)
	switch {
	case !changed:
		out = append(out, hard("B4", true, "no cambió código Go: no se exige auditoría", ""))
		out = append(out, hard("B5", true, "no cambió código Go: no se exige go test", ""))
	default:
		trivial := false
		for _, s := range st.Specs {
			trivial = trivial || s.Lane == "trivial"
		}
		audit := lastCall(tr.Calls, verbPred("lane_audit"))
		out = append(out, hard("B4", trivial && audit >= 0 && audit > lastEdit,
			"hay spec trivial y lane_audit después de la última edición",
			fmt.Sprintf("cambió código Go sin spec trivial auditada (trivial=%t, lane_audit en posición %d, última edición %d)", trivial, audit, lastEdit)))
		test := lastCall(tr.Calls, bashPred(goTestRe))
		out = append(out, hard("B5", test >= 0 && test > lastEdit,
			"go test corrió después de la última edición",
			"cambió código Go sin un go test posterior a la última edición"))
	}

	repro := firstCall(tr.Calls, bashPred(goRunRe))
	out = append(out, info("B6", fmt.Sprintf("reproducción antes de editar: %t", repro >= 0 && (firstEdit < 0 || repro < firstEdit))))
	return out
}

func trivialChecks(in Inputs) []Check {
	st, tr := in.St, in.Tr
	var out []Check

	hasTrivial, audited := false, false
	for _, s := range st.Specs {
		if s.Lane == "trivial" {
			hasTrivial = true
			audited = audited || s.HasAudit
		}
	}
	out = append(out, hard("T1", hasTrivial, "existe una spec lane trivial", "no existe ninguna spec lane trivial"))

	quick := firstCall(tr.Calls, verbPred("spec_quick"))
	firstEdit := firstCall(tr.Calls, isGoEdit)
	out = append(out, hard("T2", quick >= 0 && (firstEdit < 0 || quick < firstEdit),
		"spec_quick precede a la primera edición",
		"no hubo spec_quick, o llegó después de la primera edición"))

	audit := lastCall(tr.Calls, verbPred("lane_audit"))
	lastEdit := lastCall(tr.Calls, isGoEdit)
	out = append(out, hard("T3", audit >= 0 && audit > lastEdit && audited,
		"lane_audit tras la última edición y el estado registra la auditoría",
		fmt.Sprintf("falta auditoría (lane_audit en posición %d, última edición %d, auditoría registrada=%t)", audit, lastEdit, audited)))

	onlyScope := len(st.ChangedFiles) > 0
	for _, f := range st.ChangedFiles {
		onlyScope = onlyScope && f == trivialFile
	}
	out = append(out, hard("T4", onlyScope,
		"el único archivo cambiado es "+trivialFile,
		fmt.Sprintf("archivos cambiados fuera de alcance o ninguno: %v", st.ChangedFiles)))

	content := st.GoContents[trivialFile]
	out = append(out, hard("T5", strings.Contains(content, typoRight) && !strings.Contains(content, typoWrong),
		"la errata quedó corregida",
		"la errata no quedó corregida en "+trivialFile))
	return out
}

func presentSpecChecks(in Inputs) ([]Check, error) {
	st, tr := in.St, in.Tr
	if st.SeededSpecID == "" {
		return nil, fmt.Errorf("check: el estado no trae seeded_spec_id (present-spec)")
	}
	if len(in.Markers) == 0 && len(in.CriteriaIDs) == 0 {
		return nil, fmt.Errorf("check: present-spec necesita --markers y --criteria")
	}
	id := st.SeededSpecID
	var out []Check

	status := "(no existe)"
	for _, s := range st.Specs {
		if s.ID == id {
			status = s.Status
		}
	}
	out = append(out, hard("P1", status == "specced",
		id+" sigue en specced", fmt.Sprintf("%s ya no está en specced (estado: %s)", id, status)))

	adv := firstCall(tr.Calls, verbPred("spec_advance"))
	out = append(out, hard("P2", adv < 0, "no se llamó spec_advance", "se llamó spec_advance"))

	read := firstCall(tr.Calls, func(c Call) bool { return readsSpec(c, id) }) >= 0
	out = append(out, hard("P3", read,
		"el agente leyó la spec", "el agente no leyó la spec (ni spec_status ni sus documentos)"))

	out = append(out, hard("P4", approvalRe.MatchString(tr.FinalText),
		"el texto final pide aprobación", "el texto final no pide aprobación"))

	mention := mentionedCriterion(tr.FinalText, in.CriteriaIDs, in.Markers)
	out = append(out, hard("P5", mention != "",
		"el texto final menciona un criterio sembrado ("+mention+")",
		"el texto final no menciona ningún criterio sembrado (ni id ni palabra distintiva)"))

	goFiles := st.goChanged()
	out = append(out, hard("P6", len(goFiles) == 0, "no se tocó código Go",
		fmt.Sprintf("se tocó código Go: %v", goFiles)))
	return out, nil
}

// readsSpec dice si la llamada lee la spec: spec_status sobre su id o lectura
// de alguno de sus documentos.
func readsSpec(c Call, id string) bool {
	if matchVerb(c, "spec_status") {
		if got, _ := c.Input["id"].(string); got == id {
			return true
		}
		return strings.Contains(bashCommand(c), id)
	}
	dir := "/specs/" + id + "/"
	switch c.Name {
	case "Read":
		p, _ := c.Input["file_path"].(string)
		return strings.Contains(p, dir)
	case "Bash":
		return strings.Contains(bashCommand(c), dir)
	}
	return false
}

// mentionedCriterion devuelve qué criterio sembrado nombra el texto: su id
// como palabra completa o su palabra distintiva. "" si ninguno.
func mentionedCriterion(text string, ids, markers []string) string {
	for _, id := range ids {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`).MatchString(text) {
			return id
		}
	}
	lower := strings.ToLower(text)
	for _, m := range markers {
		if m != "" && strings.Contains(lower, strings.ToLower(m)) {
			return m
		}
	}
	return ""
}
