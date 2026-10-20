// Command check es el verificador determinista del arnés skill-comply
// (SPEC-172): lee la transcripción stream-json de `claude -p` y el estado
// que dejó mneme en el HOME aislado, y decide si el agente siguió el flujo.
//
// No importa ningún paquete interno de mneme: habla con mneme solo a través
// de su binario, igual que un usuario, y jamás invoca a claude.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// errUnreadable marca una transcripción sin ningún evento utilizable. Es un
// fallo del arnés (exit 2), nunca un incumplimiento del agente: si el formato
// de stream-json cambia, el verificador debe fallar a gritos y no dar PASS.
var errUnreadable = errors.New("transcripción ilegible: ningún evento assistant ni result")

// maxEventBytes acota una línea de la transcripción; los resultados de
// herramientas pueden ser grandes.
const maxEventBytes = 16 * 1024 * 1024

// Call es una llamada a herramienta hecha por el agente, en orden de aparición.
type Call struct {
	Seq   int
	Name  string
	Input map[string]any
}

// Result resume el evento final `result` de la corrida.
type Result struct {
	Text     string
	Subtype  string
	NumTurns int
	CostUSD  float64
	IsError  bool
}

// Transcript es lo que el verificador extrae de una corrida de claude.
type Transcript struct {
	Calls []Call
	// Result es nil si la corrida terminó sin evento `result` (p. ej. cortada).
	Result *Result
	// FinalText es el texto final: el de `result` o, si falta, el último
	// bloque de texto del asistente.
	FinalText string
	// Skipped cuenta las líneas que no eran JSON válido.
	Skipped int
}

type rawEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Content []rawBlock `json:"content"`
	} `json:"message"`
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

type rawBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ReadTranscript lee un flujo stream-json (un evento JSON por línea). Las
// llamadas hechas dentro de subagentes cuentan igual que las del agente
// principal. Devuelve errUnreadable si no hay ningún evento assistant ni result.
func ReadTranscript(r io.Reader) (Transcript, error) {
	var tr Transcript
	var lastText string
	events := 0

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxEventBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev rawEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			tr.Skipped++
			continue
		}
		switch ev.Type {
		case "assistant":
			events++
			for _, b := range ev.Message.Content {
				switch b.Type {
				case "tool_use":
					tr.Calls = append(tr.Calls, Call{Seq: len(tr.Calls), Name: b.Name, Input: b.Input})
				case "text":
					lastText = b.Text
				}
			}
		case "result":
			events++
			tr.Result = &Result{
				Text: ev.Result, Subtype: ev.Subtype, NumTurns: ev.NumTurns,
				CostUSD: ev.TotalCostUSD, IsError: ev.IsError,
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Transcript{}, fmt.Errorf("check: leer transcripción: %w", err)
	}
	if events == 0 {
		return Transcript{}, errUnreadable
	}
	tr.FinalText = lastText
	if tr.Result != nil && tr.Result.Text != "" {
		tr.FinalText = tr.Result.Text
	}
	return tr, nil
}

// mcpPrefix es el prefijo con que Claude Code nombra las herramientas del
// servidor MCP de mneme.
const mcpPrefix = "mcp__mneme__"

// cliForms traduce un verbo de mneme a su forma de terminal. Un verbo sin
// entrada solo existe como herramienta MCP.
var cliForms = map[string]string{
	"backlog_add":   "backlog add",
	"spec_advance":  "spec advance",
	"spec_quick":    "spec quick",
	"spec_status":   "spec status",
	"lane_audit":    "lane audit",
	"lane_override": "lane override",
	"spec_reject":   "spec reject",
}

// matchVerb dice si la llamada es el verbo de mneme dado, ya sea como
// herramienta MCP o como orden `mneme <grupo> <acción>` en Bash. Así el arnés
// sigue valiendo si un verbo pasa de MCP a la terminal.
func matchVerb(c Call, verb string) bool {
	if c.Name == mcpPrefix+verb {
		return true
	}
	form, ok := cliForms[verb]
	if !ok || c.Name != "Bash" {
		return false
	}
	cmd, _ := c.Input["command"].(string)
	return cliRegexp(form).MatchString(cmd)
}

var cliRegexps = map[string]*regexp.Regexp{}

// cliRegexp compila (con caché) el patrón de una orden de terminal de mneme.
func cliRegexp(form string) *regexp.Regexp {
	if re, ok := cliRegexps[form]; ok {
		return re
	}
	words := strings.Fields(form)
	re := regexp.MustCompile(`(^|[\s/])mneme\s+` + strings.Join(words, `\s+`) + `\b`)
	cliRegexps[form] = re
	return re
}

// editedGoFile devuelve la ruta y true si la llamada edita un archivo .go.
func editedGoFile(c Call) (string, bool) {
	switch c.Name {
	case "Edit", "Write", "MultiEdit":
	default:
		return "", false
	}
	p, _ := c.Input["file_path"].(string)
	return p, strings.HasSuffix(p, ".go")
}

// bashCommand devuelve el comando de una llamada Bash ("" si no lo es).
func bashCommand(c Call) string {
	if c.Name != "Bash" {
		return ""
	}
	cmd, _ := c.Input["command"].(string)
	return cmd
}
