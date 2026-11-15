package delivery

import (
	"errors"
	"strings"
	"testing"
)

// wrap places body between the two delimiter lines, with prose around it, the
// way an implementer writes the entrega section.
func wrap(body string) string {
	return "Prosa antes del bloque.\n\n" + StartMarker + "\n" + body + "\n" + EndMarker + "\n\nProsa despues.\n"
}

// fenced wraps body in a backtick fence and then in the delimiters.
func fenced(body string) string {
	return wrap("```toml\n" + body + "\n```")
}

const doneBody = `schema_version = 1
estado = "hecho"
arbol = "2ba81d8c4e1f"
riesgo = { item = 3, razon = "spec_status gana campos" }

[[evidencia]]
comando = "go test ./internal/delivery/"
exit_code = 0
salida = "ok"
criterio = "C1"
`

const blockedBody = `schema_version = 1
estado = "bloqueado"
riesgo = "none"

[[evidencia]]
comando = "make test"
exit_code = 2
salida = "FAIL: TestX"
`

// lines returns n lines of "x" joined by newlines, with a trailing newline.
func lines(n int) string {
	return strings.Repeat("x\n", n)
}

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantErr   error
		wantInMsg string
		check     func(t *testing.T, c *Contract)
	}{
		{name: "R1/missing", text: "solo prosa, sin bloque\n", wantErr: ErrMissing, wantInMsg: "missing"},
		{name: "R1/start_only", text: "x\n" + StartMarker + "\n" + doneBody, wantErr: ErrInvalid, wantInMsg: "marker"},
		{name: "R1/end_only", text: doneBody + "\n" + EndMarker + "\n", wantErr: ErrInvalid, wantInMsg: "marker"},
		{name: "R1/duplicate_start", text: StartMarker + "\n" + wrap(doneBody), wantErr: ErrInvalid, wantInMsg: "marker"},
		{name: "R1/duplicate_end", text: wrap(doneBody) + EndMarker + "\n", wantErr: ErrInvalid, wantInMsg: "marker"},
		{
			name: "R1/duplicate_in_output",
			text: wrap(`schema_version = 1
estado = "bloqueado"
riesgo = "none"

[[evidencia]]
comando = "go test"
exit_code = 1
salida = """
` + StartMarker + `
"""
`),
			wantErr:   ErrInvalid,
			wantInMsg: "marker",
		},
		{name: "R1/end_before_start", text: EndMarker + "\n" + doneBody + "\n" + StartMarker + "\n", wantErr: ErrInvalid, wantInMsg: "marker"},
		{name: "R2/unclosed_fence", text: wrap("```toml\n" + doneBody), wantErr: ErrInvalid, wantInMsg: "fence"},
		{name: "R2/empty", text: wrap("\n   \n"), wantErr: ErrInvalid, wantInMsg: "empty"},
		{name: "R2/empty_fence", text: wrap("```toml\n\n```"), wantErr: ErrInvalid, wantInMsg: "empty"},
		{name: "R3/no_version", text: wrap("estado = \"bloqueado\"\nriesgo = \"none\""), wantErr: ErrInvalid, wantInMsg: "schema_version"},
		{name: "R3/other_version", text: wrap("schema_version = 2"), wantErr: ErrUnsupportedSchema, wantInMsg: "schema_version"},
		{name: "R3/broken_toml", text: wrap("schema_version = 1\nestado = "), wantErr: ErrInvalid, wantInMsg: "parse"},
		{
			name:      "R4/unknown_key",
			text:      wrap("schema_version = 1\nestado = \"bloqueado\"\nriesgo = \"none\"\nnotas = \"x\""),
			wantErr:   ErrInvalid,
			wantInMsg: `unknown key "notas"`,
		},
		{
			name:      "R4/unknown_keys",
			text:      wrap("schema_version = 1\nestado = \"bloqueado\"\nriesgo = \"none\"\na = 1\nb = 2"),
			wantErr:   ErrInvalid,
			wantInMsg: `unknown key "a", "b"`,
		},
		{
			name:      "R4/unknown_nested_key",
			text:      wrap(blockedBody + "otra = 1\n"),
			wantErr:   ErrInvalid,
			wantInMsg: `"evidencia.otra"`,
		},
		{
			name: "R4/wrong_type",
			text: wrap(`schema_version = 1
estado = "bloqueado"
riesgo = "none"

[[evidencia]]
comando = "make test"
exit_code = "0"
`),
			wantErr:   ErrInvalid,
			wantInMsg: "exit_code",
		},
		{name: "R5/unknown_state", text: wrap("schema_version = 1\nestado = \"terminado\"\nriesgo = \"none\""), wantErr: ErrInvalid, wantInMsg: `estado "terminado"`},
		{name: "R5/missing_state", text: wrap("schema_version = 1\nriesgo = \"none\""), wantErr: ErrInvalid, wantInMsg: "estado"},
		{name: "R6/done_without_tree", text: wrap("schema_version = 1\nestado = \"hecho\"\nriesgo = \"none\""), wantErr: ErrInvalid, wantInMsg: "arbol"},
		{name: "R6/bad_tree", text: wrap(strings.Replace(doneBody, "2ba81d8c4e1f", "xyz", 1)), wantErr: ErrInvalid, wantInMsg: "arbol"},
		{
			name:      "R6/bad_tree_with_blocked",
			text:      wrap("schema_version = 1\nestado = \"bloqueado\"\narbol = \"ABC1234\"\nriesgo = \"none\""),
			wantErr:   ErrInvalid,
			wantInMsg: "arbol",
		},
		{
			name:      "R7/done_without_evidence",
			text:      wrap("schema_version = 1\nestado = \"hecho_con_dudas\"\narbol = \"2ba81d8\"\nriesgo = \"none\""),
			wantErr:   ErrInvalid,
			wantInMsg: "evidencia",
		},
		{
			name: "R7/evidence_with_blocked",
			text: wrap(blockedBody),
			check: func(t *testing.T, c *Contract) {
				if c.State != StateBlocked || len(c.Evidence) != 1 || c.Evidence[0].ExitCode != 2 {
					t.Errorf("contract = %+v, want bloqueado with one evidencia exit 2", c)
				}
			},
		},
		{name: "R7/blank_command", text: wrap(strings.Replace(doneBody, `"go test ./internal/delivery/"`, `"   "`, 1)), wantErr: ErrInvalid, wantInMsg: "comando"},
		{name: "R7/missing_exit_code", text: wrap(strings.Replace(doneBody, "exit_code = 0\n", "", 1)), wantErr: ErrInvalid, wantInMsg: `evidencia[0]: missing required key "exit_code"`},
		{
			name:      "R7/too_many_lines",
			text:      wrap(strings.Replace(doneBody, `salida = "ok"`, `salida = """`+"\n"+lines(MaxOutputLines+1)+`"""`, 1)),
			wantErr:   ErrInvalid,
			wantInMsg: "salida has 41 lines",
		},
		{
			name:      "R7/too_many_bytes",
			text:      wrap(strings.Replace(doneBody, `salida = "ok"`, `salida = "`+strings.Repeat("a", MaxOutputBytes+1)+`"`, 1)),
			wantErr:   ErrInvalid,
			wantInMsg: "salida is 4097 bytes",
		},
		{name: "R7/bad_criterion", text: wrap(strings.Replace(doneBody, `criterio = "C1"`, `criterio = "x y"`, 1)), wantErr: ErrInvalid, wantInMsg: "criterio"},
		{
			name:      "R8/bad_action",
			text:      wrap(blockedBody + "\n[[prueba_en_rojo]]\nnombre = \"TestA\"\naccion = \"editada\"\nmotivo = \"m\"\n\n[[prueba_en_rojo]]\nnombre = \"TestB\"\naccion = \"borrada\"\nmotivo = \"m\"\n"),
			wantErr:   ErrInvalid,
			wantInMsg: "prueba_en_rojo[1]",
		},
		{
			name:      "R8/duplicate_name",
			text:      wrap(blockedBody + "\n[[prueba_en_rojo]]\nnombre = \"TestX\"\naccion = \"editada\"\nmotivo = \"m\"\n\n[[prueba_en_rojo]]\nnombre = \"TestX\"\naccion = \"editada\"\nmotivo = \"m\"\n"),
			wantErr:   ErrInvalid,
			wantInMsg: "nombre",
		},
		{
			name:      "R8/blank_name",
			text:      wrap(blockedBody + "\n[[prueba_en_rojo]]\nnombre = \" \"\naccion = \"editada\"\nmotivo = \"m\"\n"),
			wantErr:   ErrInvalid,
			wantInMsg: "nombre",
		},
		{
			name:      "R8/blank_reason",
			text:      wrap(blockedBody + "\n[[prueba_en_rojo]]\nnombre = \"TestX\"\naccion = \"editada\"\n"),
			wantErr:   ErrInvalid,
			wantInMsg: "motivo",
		},
		{name: "R9/missing", text: wrap("schema_version = 1\nestado = \"falta_contexto\""), wantErr: ErrInvalid, wantInMsg: "riesgo"},
		{name: "R9/bad_string", text: wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = \"nada\""), wantErr: ErrInvalid, wantInMsg: "riesgo"},
		{
			name:      "R9/item_out_of_range",
			text:      wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = { item = 7, razon = \"x\" }"),
			wantErr:   ErrInvalid,
			wantInMsg: "riesgo.item 7 out of range 1..6",
		},
		{
			name:      "R9/item_zero",
			text:      wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = { item = 0, razon = \"x\" }"),
			wantErr:   ErrInvalid,
			wantInMsg: "riesgo.item",
		},
		{
			name:      "R9/item_missing",
			text:      wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = { razon = \"x\" }"),
			wantErr:   ErrInvalid,
			wantInMsg: "riesgo.item",
		},
		{
			name:      "R9/item_not_integer",
			text:      wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = { item = \"3\", razon = \"x\" }"),
			wantErr:   ErrInvalid,
			wantInMsg: "riesgo.item",
		},
		{
			name:      "R9/blank_reason",
			text:      wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = { item = 2, razon = \"  \" }"),
			wantErr:   ErrInvalid,
			wantInMsg: "riesgo.razon",
		},
		{
			name:      "R9/reason_not_string",
			text:      wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = { item = 2, razon = 5 }"),
			wantErr:   ErrInvalid,
			wantInMsg: "riesgo.razon",
		},
		{
			name:      "R9/extra_key",
			text:      wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = { item = 2, razon = \"x\", nivel = \"alto\" }"),
			wantErr:   ErrInvalid,
			wantInMsg: "nivel",
		},
		{name: "R9/wrong_type", text: wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = 3"), wantErr: ErrInvalid, wantInMsg: "riesgo"},
		{
			name: "R9/standard_table",
			text: wrap("schema_version = 1\nestado = \"falta_contexto\"\n\n[riesgo]\nitem = 4\nrazon = \"tabla estandar\"\n"),
			check: func(t *testing.T, c *Contract) {
				if c.Risk.None || c.Risk.Item != 4 || c.Risk.Reason != "tabla estandar" {
					t.Errorf("risk = %+v, want item 4 tabla estandar", c.Risk)
				}
			},
		},
		{
			name:      "R10/stopped_with_done",
			text:      wrap(doneBody + "\n[[prueba_en_rojo]]\nnombre = \"TestX\"\naccion = \"parada_y_reportada\"\nmotivo = \"no prevista\"\n"),
			wantErr:   ErrInvalid,
			wantInMsg: "parada_y_reportada",
		},
		{
			name: "valid/hecho",
			text: wrap(doneBody),
			check: func(t *testing.T, c *Contract) {
				if c.SchemaVersion != SchemaVersion || c.State != StateDone || c.Tree != "2ba81d8c4e1f" {
					t.Errorf("contract = %+v", c)
				}
				if len(c.Evidence) != 1 || c.Evidence[0].ExitCode != 0 || c.Evidence[0].Criterion != "C1" ||
					c.Evidence[0].Command != "go test ./internal/delivery/" || c.Evidence[0].Output != "ok" {
					t.Errorf("evidence = %+v", c.Evidence)
				}
				if c.Risk != (Risk{Item: 3, Reason: "spec_status gana campos"}) {
					t.Errorf("risk = %+v", c.Risk)
				}
			},
		},
		{
			name: "valid/hecho_con_dudas",
			text: wrap(strings.Replace(doneBody, `"hecho"`, `"hecho_con_dudas"`, 1) +
				"\n[[prueba_en_rojo]]\nnombre = \"TestGolden\"\naccion = \"editada\"\nmotivo = \"previsto\"\n\n" +
				"[[prueba_en_rojo]]\nnombre = \"TestOtra\"\naccion = \"parada_y_reportada\"\nmotivo = \"no prevista\"\n"),
			check: func(t *testing.T, c *Contract) {
				if c.State != StateDoneConcerns || len(c.RedTests) != 2 {
					t.Fatalf("contract = %+v", c)
				}
				if c.RedTests[0] != (RedTest{Name: "TestGolden", Action: ActionEdited, Reason: "previsto"}) ||
					c.RedTests[1].Action != ActionStopped {
					t.Errorf("red tests = %+v", c.RedTests)
				}
			},
		},
		{
			name: "valid/falta_contexto_minimo",
			text: wrap("schema_version = 1\nestado = \"falta_contexto\"\nriesgo = \"none\""),
			check: func(t *testing.T, c *Contract) {
				if c.State != StateNeedsContext || c.Tree != "" || len(c.Evidence) != 0 || !c.Risk.None {
					t.Errorf("contract = %+v", c)
				}
			},
		},
		{
			name: "valid/bloqueado",
			text: wrap("schema_version = 1\nestado = \"bloqueado\"\narbol = \"abcdef0\"\nriesgo = \"none\""),
			check: func(t *testing.T, c *Contract) {
				if c.State != StateBlocked || c.Tree != "abcdef0" {
					t.Errorf("contract = %+v", c)
				}
			},
		},
		{
			name: "valid/with_fence",
			text: fenced(doneBody),
			check: func(t *testing.T, c *Contract) {
				if c.State != StateDone {
					t.Errorf("state = %q", c.State)
				}
			},
		},
		{
			name: "valid/with_bare_fence",
			text: wrap("\n```\n" + blockedBody + "```\n\n"),
			check: func(t *testing.T, c *Contract) {
				if c.State != StateBlocked {
					t.Errorf("state = %q", c.State)
				}
			},
		},
		{
			name: "valid/without_fence",
			text: wrap(blockedBody),
			check: func(t *testing.T, c *Contract) {
				if c.Evidence[0].Output != "FAIL: TestX" {
					t.Errorf("output = %q", c.Evidence[0].Output)
				}
			},
		},
		{
			name: "valid/empty_output",
			text: wrap(strings.Replace(doneBody, `salida = "ok"`, `salida = ""`, 1)),
			check: func(t *testing.T, c *Contract) {
				if c.Evidence[0].Output != "" {
					t.Errorf("output = %q", c.Evidence[0].Output)
				}
			},
		},
		{
			name: "valid/exactly_40_lines",
			text: wrap(strings.Replace(doneBody, `salida = "ok"`, `salida = """`+"\n"+lines(MaxOutputLines)+`"""`, 1)),
			check: func(t *testing.T, c *Contract) {
				if got := strings.Count(c.Evidence[0].Output, "\n"); got != MaxOutputLines {
					t.Errorf("output has %d newlines, want %d", got, MaxOutputLines)
				}
			},
		},
		{
			name: "valid/crlf",
			text: strings.ReplaceAll(wrap(doneBody), "\n", "\r\n"),
			check: func(t *testing.T, c *Contract) {
				if c.State != StateDone || c.Evidence[0].ExitCode != 0 {
					t.Errorf("contract = %+v", c)
				}
			},
		},
		{
			name: "valid/spaces_around_markers",
			text: "x\n   " + StartMarker + "  \n" + doneBody + "\n\t" + EndMarker + " \n",
			check: func(t *testing.T, c *Contract) {
				if c.State != StateDone {
					t.Errorf("state = %q", c.State)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(tc.text))
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("Parse() = %+v, want error wrapping %v", c, tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Parse() error = %v, want errors.Is %v", err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantInMsg) {
					t.Errorf("Parse() error = %q, want it to contain %q", err, tc.wantInMsg)
				}
				if !strings.HasPrefix(err.Error(), "delivery: ") {
					t.Errorf("Parse() error = %q, want prefix %q", err, "delivery: ")
				}
				if c != nil {
					t.Errorf("Parse() contract = %+v, want nil on error", c)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v, want nil", err)
			}
			tc.check(t, c)
		})
	}
}

// TestParse_SentinelsAreDistinct guards that a caller can tell the three
// outcomes apart with errors.Is: SPEC-182 maps ErrMissing to a warning and the
// other two to a refusal.
func TestParse_SentinelsAreDistinct(t *testing.T) {
	sentinels := []error{ErrMissing, ErrInvalid, ErrUnsupportedSchema}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v) = true, want distinct sentinels", a, b)
			}
		}
	}
}
