package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// State es la foto del mundo que dejó el agente: lo que mneme guardó en el
// HOME aislado y lo que cambió en el repositorio de prueba. Tiene forma propia
// y estable, independiente de la salida exacta de mneme.
type State struct {
	BaseSHA string `json:"base_sha"`
	// SeededSpecID es la spec que sembró el escenario present-spec ("" si no aplica).
	SeededSpecID string        `json:"seeded_spec_id,omitempty"`
	Backlog      []BacklogItem `json:"backlog"`
	Specs        []SpecState   `json:"specs"`
	// GitStatus es `git status --porcelain` del repositorio de prueba.
	GitStatus []string `json:"git_status"`
	// ChangedFiles son los archivos cambiados respecto a BaseSHA, confirmados
	// o no, sin contar el directorio .claude/ que escribe el propio cliente.
	ChangedFiles []string `json:"changed_files"`
	// GoContents guarda el contenido final de los .go cambiados.
	GoContents map[string]string `json:"go_contents,omitempty"`
}

// BacklogItem es un ítem de backlog tal como lo ve el verificador.
type BacklogItem struct {
	ID          string `json:"id"`
	Lane        string `json:"lane"`
	Status      string `json:"status"`
	Refinements int    `json:"refinements"`
}

// SpecState es una spec con su estado, su lane y los documentos que tiene.
type SpecState struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Lane   string `json:"lane"`
	Scope  string `json:"scope,omitempty"`
	// HasAudit es true si `lane status` registra al menos una auditoría.
	HasAudit bool `json:"has_audit"`
	// Docs lista los documentos presentes (spec.md, tarea.md, criteria.toml).
	Docs []string `json:"docs,omitempty"`
}

// hasDoc dice si la spec tiene el documento dado.
func (s SpecState) hasDoc(name string) bool {
	for _, d := range s.Docs {
		if d == name {
			return true
		}
	}
	return false
}

// goChanged devuelve los .go entre los archivos cambiados.
func (st State) goChanged() []string {
	var out []string
	for _, f := range st.ChangedFiles {
		if strings.HasSuffix(f, ".go") {
			out = append(out, f)
		}
	}
	return out
}

// snapshotOpts agrupa las entradas de `check snapshot`.
type snapshotOpts struct {
	Mneme   string
	Home    string
	Repo    string
	BaseSHA string
	SpecID  string
}

// isolatedEnv construye el entorno de las órdenes: el HOME aislado y nada de
// las variables MNEME_* heredadas (D4).
func isolatedEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(name, "MNEME_"), name == "HOME", name == "USERPROFILE",
			name == "CLAUDE_CONFIG_DIR", name == "CODEX_HOME":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "USERPROFILE="+home)
}

// runCmd ejecuta una orden y devuelve su salida estándar.
func runCmd(dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// snapshot lee el estado de mneme y de git y lo devuelve como State.
func snapshot(o snapshotOpts) (State, error) {
	env := isolatedEnv(o.Home)
	st := State{BaseSHA: o.BaseSHA, SeededSpecID: o.SpecID, GoContents: map[string]string{}}

	out, err := runCmd(o.Repo, env, o.Mneme, "backlog", "list", "--json")
	if err != nil {
		return State{}, fmt.Errorf("check: backlog list: %w", err)
	}
	if st.Backlog, err = decodeBacklog(out); err != nil {
		return State{}, err
	}

	out, err = runCmd(o.Repo, env, o.Mneme, "spec", "list", "--json")
	if err != nil {
		return State{}, fmt.Errorf("check: spec list: %w", err)
	}
	ids, err := decodeSpecIDs(out)
	if err != nil {
		return State{}, err
	}
	for _, id := range ids {
		sp, err := snapshotSpec(o, env, id)
		if err != nil {
			return State{}, err
		}
		st.Specs = append(st.Specs, sp)
	}

	if err := snapshotGit(o, env, &st); err != nil {
		return State{}, err
	}
	return st, nil
}

// snapshotSpec reúne estado, lane, auditoría y documentos de una spec.
func snapshotSpec(o snapshotOpts, env []string, id string) (SpecState, error) {
	out, err := runCmd(o.Repo, env, o.Mneme, "spec", "status", id, "--json")
	if err != nil {
		return SpecState{}, fmt.Errorf("check: spec status %s: %w", id, err)
	}
	sp, err := decodeSpec(out)
	if err != nil {
		return SpecState{}, err
	}
	if sp.ID == "" {
		sp.ID = id
	}
	// Sin auditoría no es un error: una spec estándar no la tiene.
	if out, err := runCmd(o.Repo, env, o.Mneme, "lane", "status", id, "--json"); err == nil {
		sp.HasAudit = decodeHasAudit(out)
	}
	sp.Docs = findSpecDocs(o.Home, id)
	return sp, nil
}

// snapshotGit llena el estado del repositorio de prueba.
func snapshotGit(o snapshotOpts, env []string, st *State) error {
	out, err := runCmd(o.Repo, env, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	changed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		st.GitStatus = append(st.GitStatus, line)
		if len(line) > 3 {
			changed[unquoteGitPath(strings.TrimSpace(line[3:]))] = true
		}
	}
	if o.BaseSHA != "" {
		out, err = runCmd(o.Repo, env, "git", "diff", "--name-only", o.BaseSHA)
		if err != nil {
			return fmt.Errorf("check: %w", err)
		}
		for _, f := range strings.Fields(string(out)) {
			changed[f] = true
		}
	}
	for f := range changed {
		if strings.HasPrefix(f, ".claude/") {
			continue
		}
		st.ChangedFiles = append(st.ChangedFiles, f)
	}
	sort.Strings(st.ChangedFiles)
	for _, f := range st.goChanged() {
		if b, err := os.ReadFile(filepath.Join(o.Repo, f)); err == nil {
			st.GoContents[f] = string(b)
		}
	}
	return nil
}

// unquoteGitPath quita las comillas con que git rodea rutas con caracteres raros.
func unquoteGitPath(p string) string {
	return strings.Trim(p, `"`)
}

// findSpecDocs localiza los documentos de la spec bajo
// $HOME/.mneme/workflows/*/specs/<id>/.
func findSpecDocs(home, id string) []string {
	var docs []string
	for _, name := range []string{"spec.md", "tarea.md", "criteria.toml"} {
		matches, _ := filepath.Glob(filepath.Join(home, ".mneme", "workflows", "*", "specs", id, name))
		if len(matches) > 0 {
			docs = append(docs, name)
		}
	}
	return docs
}

// pickStr lee una clave de texto en la raíz del objeto o, si no está, bajo
// la clave `spec`: los decodificadores toleran ambas formas de salida.
func pickStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	if sub, ok := m["spec"].(map[string]any); ok {
		if v, ok := sub[key].(string); ok {
			return v
		}
	}
	return ""
}

func decodeBacklog(raw []byte) ([]BacklogItem, error) {
	var list []map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &list); err != nil {
		return nil, fmt.Errorf("check: decodificar backlog list: %w", err)
	}
	items := make([]BacklogItem, 0, len(list))
	for _, m := range list {
		n, _ := m["refinement_count"].(float64)
		items = append(items, BacklogItem{
			ID: pickStr(m, "id"), Lane: pickStr(m, "lane"),
			Status: pickStr(m, "status"), Refinements: int(n),
		})
	}
	return items, nil
}

func decodeSpecIDs(raw []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if string(trimmed) == "null" || len(trimmed) == 0 {
		return nil, nil
	}
	var list []map[string]any
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil, fmt.Errorf("check: decodificar spec list: %w", err)
	}
	var ids []string
	for _, m := range list {
		if id := pickStr(m, "id"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func decodeSpec(raw []byte) (SpecState, error) {
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &m); err != nil {
		return SpecState{}, fmt.Errorf("check: decodificar spec status: %w", err)
	}
	sp := SpecState{
		ID: pickStr(m, "id"), Status: pickStr(m, "status"),
		Lane: pickStr(m, "lane"), Scope: pickStr(m, "scope"),
	}
	if sp.Status == "" {
		return SpecState{}, errors.New("check: spec status sin campo status")
	}
	return sp, nil
}

// decodeHasAudit dice si la salida de `lane status` trae una auditoría.
func decodeHasAudit(raw []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &m); err != nil {
		return false
	}
	a, ok := m["latest_audit"]
	return ok && a != nil
}
