package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Códigos de salida: 0 todo bien, 1 incumplimiento del agente, 2 fallo del arnés.
const (
	exitOK      = 0
	exitFail    = 1
	exitHarness = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run despacha los subcomandos y devuelve el código de salida.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "uso: check snapshot|verify|selftest|summary [opciones]")
		return exitHarness
	}
	var code int
	var err error
	switch args[0] {
	case "snapshot":
		code, err = cmdSnapshot(args[1:])
	case "verify":
		code, err = cmdVerify(args[1:], stdout)
	case "selftest":
		code, err = cmdSelftest(args[1:], stdout)
	case "summary":
		code, err = cmdSummary(args[1:], stdout)
	default:
		err = fmt.Errorf("subcomando desconocido %q", args[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "skill-comply check: %v\n", err)
		return exitHarness
	}
	return code
}

func cmdSnapshot(args []string) (int, error) {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	var o snapshotOpts
	var out string
	fs.StringVar(&o.Mneme, "mneme", "", "binario mneme")
	fs.StringVar(&o.Home, "home", "", "HOME aislado")
	fs.StringVar(&o.Repo, "repo", "", "repositorio de prueba")
	fs.StringVar(&o.BaseSHA, "base", "", "SHA inicial (por defecto, el archivo base_sha junto al repo)")
	fs.StringVar(&o.SpecID, "spec-id", "", "spec sembrada (por defecto, el archivo spec_id junto al repo, si existe)")
	fs.StringVar(&out, "out", "", "state.json de salida")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if o.Mneme == "" || o.Home == "" || o.Repo == "" || out == "" {
		return 0, errors.New("snapshot necesita --mneme, --home, --repo y --out")
	}
	work := filepath.Dir(filepath.Clean(o.Repo))
	if o.BaseSHA == "" {
		o.BaseSHA = readTrimmed(filepath.Join(work, "base_sha"))
	}
	if o.SpecID == "" {
		o.SpecID = readTrimmed(filepath.Join(work, "spec_id"))
	}
	st, err := snapshot(o)
	if err != nil {
		return 0, err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("serializar state.json: %w", err)
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		return 0, fmt.Errorf("escribir %s: %w", out, err)
	}
	return exitOK, nil
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func cmdVerify(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	var scenario, transcript, state, markers, criteria string
	fs.StringVar(&scenario, "scenario", "", "escenario")
	fs.StringVar(&transcript, "transcript", "", "transcript.jsonl")
	fs.StringVar(&state, "state", "", "state.json")
	fs.StringVar(&markers, "markers", "", "markers.txt (present-spec)")
	fs.StringVar(&criteria, "criteria", "", "criteria.toml sembrado (present-spec)")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if scenario == "" || transcript == "" || state == "" {
		return 0, errors.New("verify necesita --scenario, --transcript y --state")
	}
	checks, tr, err := verifyFiles(scenario, transcript, state, markers, criteria)
	if err != nil {
		return 0, err
	}
	return printChecks(stdout, scenario, checks, tr), nil
}

// verifyFiles carga los archivos y corre las comprobaciones.
func verifyFiles(scenario, transcript, state, markers, criteria string) ([]Check, Transcript, error) {
	f, err := os.Open(transcript)
	if err != nil {
		return nil, Transcript{}, fmt.Errorf("abrir transcripción: %w", err)
	}
	defer f.Close()
	tr, err := ReadTranscript(f)
	if err != nil {
		return nil, Transcript{}, err
	}
	raw, err := os.ReadFile(state)
	if err != nil {
		return nil, Transcript{}, fmt.Errorf("leer estado: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, Transcript{}, fmt.Errorf("decodificar %s: %w", state, err)
	}
	in := Inputs{Scenario: scenario, Tr: tr, St: st}
	if markers != "" {
		if in.Markers, err = readMarkers(markers); err != nil {
			return nil, Transcript{}, err
		}
	}
	if criteria != "" {
		if in.CriteriaIDs, err = readCriteriaIDs(criteria); err != nil {
			return nil, Transcript{}, err
		}
	}
	checks, err := Run(in)
	return checks, tr, err
}

func readMarkers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("abrir markers: %w", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			out = append(out, w)
		}
	}
	return out, sc.Err()
}

func readCriteriaIDs(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("leer criteria: %w", err)
	}
	var doc struct {
		Criterion []struct {
			ID string `toml:"id"`
		} `toml:"criterion"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decodificar %s: %w", path, err)
	}
	ids := make([]string, 0, len(doc.Criterion))
	for _, c := range doc.Criterion {
		ids = append(ids, c.ID)
	}
	return ids, nil
}

// printChecks imprime una línea por comprobación y el resumen; devuelve 1 si
// alguna DURA falló.
func printChecks(w io.Writer, scenario string, checks []Check, tr Transcript) int {
	pass, fail := 0, 0
	for _, c := range checks {
		fmt.Fprintf(w, "%s %s %s\n", c.Status, c.ID, c.Text)
		switch c.Status {
		case statusPass:
			pass++
		case statusFail:
			fail++
		}
	}
	if tr.Skipped > 0 {
		fmt.Fprintf(w, "%s %s %d línea(s) ilegibles de la transcripción se saltaron\n", statusInfo, "G0", tr.Skipped)
	}
	fmt.Fprintf(w, "RESUMEN escenario=%s pass=%d fail=%d\n", scenario, pass, fail)
	if fail > 0 {
		return exitFail
	}
	return exitOK
}

func cmdSelftest(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("selftest", flag.ContinueOnError)
	var dir, scenarios string
	fs.StringVar(&dir, "dir", "", "directorio testdata")
	fs.StringVar(&scenarios, "scenarios", "", "directorio scenarios (por defecto <dir>/../../scenarios)")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if dir == "" {
		return 0, errors.New("selftest necesita --dir")
	}
	if scenarios == "" {
		scenarios = filepath.Join(dir, "..", "..", "scenarios")
	}
	ok, err := selftest(dir, scenarios, stdout)
	if err != nil {
		return 0, err
	}
	if !ok {
		return exitFail, nil
	}
	return exitOK, nil
}

// selftest corre verify sobre cada caso de testdata: `pass*` debe dar exit 0 y
// cada `fail-*` debe dar exit 1 y fallar EXACTAMENTE las ids de su expect.txt.
func selftest(dir, scenariosDir string, w io.Writer) (bool, error) {
	all := true
	total := 0
	for _, scenario := range scenarioNames {
		cases, err := os.ReadDir(filepath.Join(dir, scenario))
		if err != nil {
			return false, fmt.Errorf("leer casos de %s: %w", scenario, err)
		}
		for _, c := range cases {
			if !c.IsDir() {
				continue
			}
			total++
			problem := runCase(filepath.Join(dir, scenario, c.Name()), scenario, scenariosDir)
			label := scenario + "/" + c.Name()
			if problem == "" {
				fmt.Fprintf(w, "PASS %s\n", label)
				continue
			}
			all = false
			fmt.Fprintf(w, "FAIL %s: %s\n", label, problem)
		}
	}
	if total == 0 {
		return false, errors.New("testdata sin casos")
	}
	return all, nil
}

// runCase evalúa un caso y devuelve "" si cuadra con lo esperado o el motivo.
func runCase(caseDir, scenario, scenariosDir string) string {
	var markers, criteria string
	if scenario == "present-spec" {
		markers = filepath.Join(scenariosDir, scenario, "markers.txt")
		criteria = filepath.Join(scenariosDir, scenario, "criteria.toml")
	}
	checks, _, err := verifyFiles(scenario, filepath.Join(caseDir, "transcript.jsonl"),
		filepath.Join(caseDir, "state.json"), markers, criteria)
	if err != nil {
		return err.Error()
	}
	got := failedIDs(checks)
	want := readExpect(filepath.Join(caseDir, "expect.txt"))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Sprintf("fallan %v pero se esperaba %v", got, want)
	}
	name := filepath.Base(caseDir)
	if strings.HasPrefix(name, "fail-") && len(want) == 0 {
		return "un caso fail-* debe declarar ids en expect.txt"
	}
	return ""
}

func failedIDs(checks []Check) []string {
	var ids []string
	for _, c := range checks {
		if c.Status == statusFail {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func readExpect(path string) []string {
	ids := strings.Fields(readTrimmed(path))
	sort.Strings(ids)
	return ids
}

// summaryDoc es el contenido de summary.json.
type summaryDoc struct {
	Scenarios map[string]map[string]string `json:"scenarios"`
}

func cmdSummary(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("summary", flag.ContinueOnError)
	var out string
	fs.StringVar(&out, "out", "", "directorio de salida de la corrida")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if out == "" {
		return 0, errors.New("summary necesita --out")
	}
	doc := summaryDoc{Scenarios: map[string]map[string]string{}}
	var cols []string
	for _, s := range scenarioNames {
		raw, err := os.ReadFile(filepath.Join(out, s, "verify.txt"))
		if err != nil {
			continue
		}
		doc.Scenarios[s] = parseVerify(string(raw))
		cols = append(cols, s)
	}
	table := renderTable(doc, cols)
	fmt.Fprint(stdout, table)
	if err := os.WriteFile(filepath.Join(out, "summary.txt"), []byte(table), 0o644); err != nil {
		return 0, fmt.Errorf("escribir summary.txt: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("serializar summary.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(out, "summary.json"), append(data, '\n'), 0o644); err != nil {
		return 0, fmt.Errorf("escribir summary.json: %w", err)
	}
	return exitOK, nil
}

// parseVerify convierte un verify.txt en id -> estado.
func parseVerify(raw string) map[string]string {
	res := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case statusPass, statusFail, statusInfo:
			res[f[1]] = f[0]
		}
	}
	return res
}

// renderTable arma la tabla comprobaciones x escenarios.
func renderTable(doc summaryDoc, cols []string) string {
	idSet := map[string]bool{}
	for _, c := range cols {
		for id := range doc.Scenarios[c] {
			idSet[id] = true
		}
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	fmt.Fprintf(&b, "%-6s", "id")
	for _, c := range cols {
		fmt.Fprintf(&b, " %-13s", c)
	}
	b.WriteString("\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "%-6s", id)
		for _, c := range cols {
			v := doc.Scenarios[c][id]
			if v == "" {
				v = "-"
			}
			fmt.Fprintf(&b, " %-13s", v)
		}
		b.WriteString("\n")
	}
	return b.String()
}
