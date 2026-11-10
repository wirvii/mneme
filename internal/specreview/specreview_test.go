package specreview

import (
	"errors"
	"strings"
	"testing"
)

const validSpec = `schema_version = 1
re_revision = false

[[revisor]]
papel = "comprension"
estado = "completo"
parafrasis_propuesta = "Propone revisar la spec sin contexto."
parafrasis_hoy = "Hoy nadie la revisa antes del gate."

[[revisor]]
papel = "diseno"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"

[[hallazgo]]
id = "H1"
revisor = "hechos"
severidad = "blocker"
texto = "La ruta no existe."
salida = "corregir"
resolucion = "Ruta corregida."
`

const validPlan = `schema_version = 1

[[revisor]]
papel = "preparacion"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"
`

const allNotRun = `schema_version = 1

[[revisor]]
papel = "comprension"
estado = "no_corrio"
nota = "sin revisor"

[[revisor]]
papel = "diseno"
estado = "no_corrio"
nota = "sin revisor"

[[revisor]]
papel = "hechos"
estado = "no_corrio"
nota = "sin revisor"
`

func finding(extra string) string {
	return validSpec + "\n[[hallazgo]]\n" + extra
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		moment  Moment
		doc     string
		wantErr error
	}{
		{"valid spec moment", MomentSpec, validSpec, nil},
		{"valid plan moment", MomentPlan, validPlan, nil},
		{"zero findings", MomentSpec, strings.Split(validSpec, "[[hallazgo]]")[0], nil},
		{"all roles did not run", MomentSpec, allNotRun, nil},
		{"completed role with note", MomentSpec, strings.Replace(validSpec, `papel = "diseno"
estado = "completo"`, `papel = "diseno"
estado = "completo"
nota = "re-revision no corrio"`, 1), nil},
		{"schema 2", MomentSpec, strings.Replace(validSpec, "schema_version = 1", "schema_version = 2", 1), ErrUnsupportedSchema},
		{"schema 2 with unknown key", MomentSpec, "schema_version = 2\nfuturo = true\n", ErrUnsupportedSchema},
		{"schema missing", MomentSpec, strings.Replace(validSpec, "schema_version = 1\n", "", 1), ErrInvalid},
		{"broken toml", MomentSpec, "schema_version = = 1", ErrInvalid},
		{"unknown key intentos", MomentSpec, strings.Replace(validSpec, "re_revision = false", "re_revision = false\nintentos = 2", 1), ErrInvalid},
		{"unknown moment", Moment("otro"), validSpec, ErrInvalid},
		{"missing role", MomentSpec, strings.Replace(validSpec, "[[revisor]]\npapel = \"diseno\"\nestado = \"completo\"\n\n", "", 1), ErrInvalid},
		{"duplicate role", MomentSpec, strings.Replace(validSpec, `papel = "diseno"`, `papel = "hechos"`, 1), ErrInvalid},
		{"role of other moment", MomentSpec, validPlan, ErrInvalid},
		{"plan with spec roles", MomentPlan, validSpec, ErrInvalid},
		{"unknown estado", MomentSpec, strings.Replace(validSpec, `papel = "diseno"
estado = "completo"`, `papel = "diseno"
estado = "quizas"`, 1), ErrInvalid},
		{"no_corrio without nota", MomentSpec, strings.Replace(allNotRun, `nota = "sin revisor"`, `nota = ""`, 1), ErrInvalid},
		{"comprension without proposal", MomentSpec, strings.Replace(validSpec, `parafrasis_propuesta = "Propone revisar la spec sin contexto."`, "", 1), ErrInvalid},
		{"comprension without today", MomentSpec, strings.Replace(validSpec, `parafrasis_hoy = "Hoy nadie la revisa antes del gate."`, "", 1), ErrInvalid},
		{"paraphrase on other role", MomentSpec, strings.Replace(validSpec, `papel = "diseno"
estado = "completo"`, `papel = "diseno"
estado = "completo"
parafrasis_hoy = "x"`, 1), ErrInvalid},
		{"duplicate finding id", MomentSpec, finding(`id = "H1"
revisor = "hechos"
severidad = "nit"
texto = "otro"
salida = "escalar"
`), ErrInvalid},
		{"finding id bad pattern", MomentSpec, finding(`id = "-bad"
revisor = "hechos"
severidad = "nit"
texto = "x"
salida = "escalar"
`), ErrInvalid},
		{"finding undeclared reviewer", MomentSpec, finding(`id = "H2"
revisor = "preparacion"
severidad = "nit"
texto = "x"
salida = "escalar"
`), ErrInvalid},
		{"finding from role that did not run", MomentSpec, allNotRun + `
[[hallazgo]]
id = "H1"
revisor = "hechos"
severidad = "nit"
texto = "x"
salida = "escalar"
`, ErrInvalid},
		{"empty texto", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "nit"
texto = ""
salida = "escalar"
`), ErrInvalid},
		{"unknown severidad", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "critica"
texto = "x"
salida = "escalar"
`), ErrInvalid},
		{"unknown salida", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "nit"
texto = "x"
salida = "ignorar"
`), ErrInvalid},
		{"refutar without evidencia", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "major"
texto = "x"
salida = "refutar"
`), ErrInvalid},
		{"corregir without resolucion", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "major"
texto = "x"
salida = "corregir"
`), ErrInvalid},
		{"descartar non nit", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "major"
texto = "x"
salida = "descartar"
`), ErrInvalid},
		{"descartar nit ok", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "nit"
texto = "x"
salida = "descartar"
`), nil},
		{"riesgo_aceptado without re_revision", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "major"
texto = "x"
salida = "riesgo_aceptado"
`), ErrInvalid},
		{"riesgo_aceptado with re_revision", MomentSpec, strings.Replace(finding(`id = "H2"
revisor = "hechos"
severidad = "major"
texto = "x"
salida = "riesgo_aceptado"
`), "re_revision = false", "re_revision = true", 1), nil},
		{"refutar with evidencia", MomentSpec, finding(`id = "H2"
revisor = "hechos"
severidad = "major"
texto = "x"
salida = "refutar"
evidencia = "src/a.go:10"
`), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := Parse([]byte(tc.doc), tc.moment)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Parse: unexpected error: %v", err)
				}
				if r == nil {
					t.Fatal("Parse: nil report without error")
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Parse error = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

func TestParse_PopulatesReport(t *testing.T) {
	t.Parallel()
	r, err := Parse([]byte(validSpec), MomentSpec)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.SchemaVersion != 1 || r.ReReview || len(r.Reviewers) != 3 || len(r.Findings) != 1 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if r.Reviewers[0].ParaphraseToday == "" || r.Findings[0].Resolution == "" {
		t.Fatalf("fields not populated: %+v", r)
	}
}

func TestReport_Counts(t *testing.T) {
	t.Parallel()
	doc := `schema_version = 1
re_revision = true

[[revisor]]
papel = "comprension"
estado = "completo"
parafrasis_propuesta = "a"
parafrasis_hoy = "b"

[[revisor]]
papel = "diseno"
estado = "no_corrio"
nota = "fallo"

[[revisor]]
papel = "hechos"
estado = "completo"

[[hallazgo]]
id = "H1"
revisor = "hechos"
severidad = "blocker"
texto = "t"
salida = "corregir"
resolucion = "r"

[[hallazgo]]
id = "H2"
revisor = "hechos"
severidad = "major"
texto = "t"
salida = "refutar"
evidencia = "e"

[[hallazgo]]
id = "H3"
revisor = "comprension"
severidad = "major"
texto = "t"
salida = "escalar"

[[hallazgo]]
id = "H4"
revisor = "hechos"
severidad = "nit"
texto = "t"
salida = "descartar"

[[hallazgo]]
id = "H5"
revisor = "hechos"
severidad = "blocker"
texto = "t"
salida = "riesgo_aceptado"

[[hallazgo]]
id = "H6"
revisor = "comprension"
severidad = "nit"
texto = "t"
salida = "corregir"
resolucion = "r"
`
	r, err := Parse([]byte(doc), MomentSpec)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Counts{
		Requested: 3, Completed: 2, DidNotRun: 1,
		Blocker: 2, Major: 2, Nit: 2,
		Fix: 2, Refute: 1, Escalate: 1, Discard: 1, AcceptedRisk: 1,
		ReReview: true,
	}
	if got := r.Counts(); got != want {
		t.Fatalf("Counts() = %+v, want %+v", got, want)
	}
}
