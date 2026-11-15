package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// stopHookFixture is an isolated HOME, a git repo without remote (so the
// slug is deterministic) and a real project database driven through the real
// SDDService. cwd is what the payloads carry.
type stopHookFixture struct {
	*sddFixture
	home   string
	slug   string
	dbPath string
	cwd    string
}

func newStopHookFixture(t *testing.T) *stopHookFixture {
	t.Helper()
	slug, dbPath := setupDelegationRepo(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return &stopHookFixture{
		sddFixture: newSDDFixture(t, dbPath, slug),
		home:       os.Getenv("HOME"),
		slug:       slug,
		dbPath:     dbPath,
		cwd:        cwd,
	}
}

func (f *stopHookFixture) registryPath(sessionID string) string {
	return filepath.Join(f.home, ".mneme", stopRegistryDir, sessionID+".json")
}

func (f *stopHookFixture) enforcelogPath() string {
	return filepath.Join(f.home, ".mneme", "projects", f.slug+"-enforce.jsonl")
}

// implementingStandardSpec creates a standard spec and leaves it in
// implementing. viaBacklog = true creates it through BacklogAdd(standard) +
// BacklogRefine + BacklogPromote and also returns the backlog item id (the
// frozen subtest archives it); false, through SpecNew(standard) and
// backlogID = "". Then setSpec(status = implementing) and an INSERT of ONE
// spec_history row, which is all DeliveryCheck needs for Current.
func implementingStandardSpec(f *sddFixture, title string, viaBacklog bool) (specID, backlogID string) {
	f.t.Helper()
	ctx := context.Background()
	if viaBacklog {
		item, err := f.svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title, Lane: model.LaneStandard})
		if err != nil {
			f.t.Fatalf("BacklogAdd: %v", err)
		}
		if _, err := f.svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r"}); err != nil {
			f.t.Fatalf("BacklogRefine: %v", err)
		}
		spec, err := f.svc.BacklogPromote(ctx, item.ID)
		if err != nil {
			f.t.Fatalf("BacklogPromote: %v", err)
		}
		specID, backlogID = spec.ID, item.ID
	} else {
		spec, err := f.svc.SpecNew(ctx, model.SpecNewRequest{Title: title, Lane: model.LaneStandard})
		if err != nil {
			f.t.Fatalf("SpecNew: %v", err)
		}
		specID = spec.ID
	}
	f.setSpec(specID, "status", string(model.SpecStatusImplementing))
	at := time.Now().UTC().Add(-2 * time.Second).Format(time.RFC3339Nano)
	if _, err := f.db.Exec(
		`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at) VALUES (?, ?, 'planned', 'implementing', 'test', '', ?)`,
		"h-"+specID, specID, at); err != nil {
		f.t.Fatalf("insert spec_history: %v", err)
	}
	return specID, backlogID
}

// writeWhatSection creates tarea.md through its "que" section, so the spec has
// a work document without a delivery block (writing entrega without a block
// in implementing is refused by SPEC-182).
func writeWhatSection(f *sddFixture, specID string) {
	f.t.Helper()
	if _, err := f.svc.SpecDocWrite(context.Background(), model.SpecDocWriteRequest{
		ID: specID, Kind: model.SpecDocKindTask, Section: model.TaskSectionWhat, Content: "qué",
	}); err != nil {
		f.t.Fatalf("SpecDocWrite que: %v", err)
	}
}

// writeDeliveryBlock writes the smallest valid delivery contract for estado.
func writeDeliveryBlock(f *sddFixture, specID, estado string) {
	f.t.Helper()
	body := "schema_version = 1\nestado = \"" + estado + "\"\nriesgo = \"none\"\n"
	if estado == "hecho" || estado == "hecho_con_dudas" {
		body += "arbol = \"0123abc\"\n\n[[evidencia]]\ncomando = \"go test ./...\"\nexit_code = 0\n"
	}
	content := "Entrega de prueba.\n\n<!-- mneme:entrega:contrato:start -->\n" + body + "<!-- mneme:entrega:contrato:end -->\n"
	if _, err := f.svc.SpecDocWrite(context.Background(), model.SpecDocWriteRequest{
		ID: specID, Kind: model.SpecDocKindTask, Section: model.TaskSectionDelivery, Content: content,
	}); err != nil {
		f.t.Fatalf("SpecDocWrite entrega (%s): %v", estado, err)
	}
}

// saveMemoryAt stores m in the fixture's project and stamps created_at and
// updated_at with at: no store write path keeps a caller-supplied instant.
func (f *stopHookFixture) saveMemoryAt(t *testing.T, m *model.Memory, at time.Time) string {
	t.Helper()
	m.Project = f.slug
	m.Scope = model.ScopeProject
	if m.Title == "" {
		m.Title = "title"
	}
	m.Content = "content"
	m.DecayRate = 0.05
	saved, err := store.NewMemoryStore(f.db).Create(context.Background(), m)
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	if _, err := f.db.Exec("UPDATE memories SET created_at = ?, updated_at = ? WHERE id = ?", stamp, stamp, saved.ID); err != nil {
		t.Fatalf("backdate memory: %v", err)
	}
	return saved.ID
}

// work saves one work memory of session sid at at.
func (f *stopHookFixture) work(t *testing.T, sid string, at time.Time) {
	t.Helper()
	f.saveMemoryAt(t, &model.Memory{Type: model.TypeDiscovery, SessionID: sid}, at)
}

// summary saves the session summary SessionEnd writes for sid, at at.
func (f *stopHookFixture) summary(t *testing.T, sid string, at time.Time) {
	t.Helper()
	f.saveMemoryAt(t, &model.Memory{Type: model.TypeSessionSummary, SessionID: sid, TopicKey: model.SessionSummaryTopicKey(sid)}, at)
}

// checkpoint saves the project's checkpoint/latest at at.
func (f *stopHookFixture) checkpoint(t *testing.T, at time.Time) {
	t.Helper()
	f.saveMemoryAt(t, &model.Memory{Type: model.TypeSessionSummary, TopicKey: "checkpoint/latest"}, at)
}

// writeRegistry writes a registry file by hand.
func (f *stopHookFixture) writeRegistry(t *testing.T, reg stopRegistry) {
	t.Helper()
	path := f.registryPath(reg.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), stopRegistryDirPerm); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, stopRegistryFilePerm); err != nil {
		t.Fatal(err)
	}
}

// stopPayload builds a Stop/SubagentStop payload; an empty agentID is the
// orchestrator.
func stopPayload(event, sessionID, agentID, agentType, cwd string, active bool) string {
	hookEvent := "Stop"
	if event == stopEventSubagent {
		hookEvent = "SubagentStop"
	}
	p := map[string]any{
		"session_id":       sessionID,
		"hook_event_name":  hookEvent,
		"stop_hook_active": active,
		"cwd":              cwd,
	}
	if agentID != "" {
		p["agent_id"] = agentID
	}
	if agentType != "" {
		p["agent_type"] = agentType
	}
	data, _ := json.Marshal(p) //nolint:errcheck // a map of strings and bools always marshals
	return string(data)
}

// stopRun is one hook invocation's observable result.
type stopRun struct {
	err    error
	stdout string
	stderr string
}

func runStop(event, payload string, now time.Time) stopRun {
	var out, errb bytes.Buffer
	err := runHookStop(context.Background(), event, strings.NewReader(payload), &out, &errb, func() time.Time { return now })
	return stopRun{err: err, stdout: out.String(), stderr: errb.String()}
}

// decodeBlock asserts that stdout is exactly one block decision line and
// returns its reason.
func decodeBlock(t *testing.T, stdout string) string {
	t.Helper()
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("stdout must be exactly one line, got %q", stdout)
	}
	var dec struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &dec); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, stdout)
	}
	if dec.Decision != "block" || dec.Reason == "" {
		t.Fatalf("decision = %+v, want block with a reason", dec)
	}
	return dec.Reason
}

func assertPass(t *testing.T, r stopRun, wantStderr bool) {
	t.Helper()
	if r.err != nil {
		t.Errorf("runHookStop returned %v, want nil", r.err)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
	if wantStderr {
		if strings.Count(r.stderr, "\n") != 1 || !strings.HasPrefix(r.stderr, "[mneme]") {
			t.Errorf("stderr = %q, want exactly one line starting with [mneme]", r.stderr)
		}
	} else if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
}

const bystanderPhrase = "Si no trabajaste en ninguna de estas specs, termina sin escribir nada"

// TestHookStop_StopHookActive covers C1: with stop_hook_active the hooks never
// write anything, whatever is pending.
func TestHookStop_StopHookActive(t *testing.T) {
	t.Run("subagent_implementer_pending", func(t *testing.T) {
		f := newStopHookFixture(t)
		implementingStandardSpec(f.sddFixture, "pending", false)
		r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s1", "a1", "backend", f.cwd, true), time.Now())
		assertPass(t, r, false)
	})

	t.Run("orchestrator_window_pending", func(t *testing.T) {
		f := newStopHookFixture(t)
		now := time.Now().UTC().Truncate(time.Second)
		f.work(t, "s1", now.Add(-45*time.Minute))
		f.work(t, "s1", now.Add(-30*time.Minute))
		f.work(t, "s1", now.Add(-10*time.Minute))
		r := runStop(stopEventStop, stopPayload(stopEventStop, "s1", "", "", f.cwd, true), now)
		assertPass(t, r, false)
	})
}

// TestHookSubagentStop_BlocksOncePerAgentAndSpec covers C2.
func TestHookSubagentStop_BlocksOncePerAgentAndSpec(t *testing.T) {
	f := newStopHookFixture(t)
	specID, _ := implementingStandardSpec(f.sddFixture, "pending", false)
	now := time.Now()

	first := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "sess-1", "agent-a", "backend", f.cwd, false), now)
	reason := decodeBlock(t, first.stdout)
	for _, want := range []string{specID, "`spec_doc_write`", bystanderPhrase, `id = "` + specID + `"`, "(bloque ausente"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason does not contain %q:\n%s", want, reason)
		}
	}
	if first.stderr != "" {
		t.Errorf("stderr = %q, want empty", first.stderr)
	}

	second := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "sess-1", "agent-a", "backend", f.cwd, false), now)
	assertPass(t, second, false)

	third := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "sess-1", "agent-b", "backend", f.cwd, false), now)
	decodeBlock(t, third.stdout)

	data, err := os.ReadFile(f.registryPath("sess-1"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var reg stopRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("decode registry: %v", err)
	}
	if reg.Schema != 1 || reg.SessionID != "sess-1" {
		t.Errorf("registry header = (%d, %q), want (1, sess-1)", reg.Schema, reg.SessionID)
	}
	got := map[string]bool{}
	for _, r := range reg.DeliveryReminded {
		got[r.AgentID+"|"+r.SpecID] = true
	}
	if len(reg.DeliveryReminded) != 2 || !got["agent-a|"+specID] || !got["agent-b|"+specID] {
		t.Errorf("delivery_reminded = %+v, want one entry per agent for %s", reg.DeliveryReminded, specID)
	}
}

// TestHookSubagentStop_PassesWhenSatisfied covers C3: any valid, current
// block lets the implementer stop, and trivial or frozen specs are never
// demanded one.
func TestHookSubagentStop_PassesWhenSatisfied(t *testing.T) {
	for _, estado := range []string{"hecho", "hecho_con_dudas", "falta_contexto", "bloqueado"} {
		t.Run(estado, func(t *testing.T) {
			f := newStopHookFixture(t)
			specID, _ := implementingStandardSpec(f.sddFixture, "done "+estado, false)
			writeDeliveryBlock(f.sddFixture, specID, estado)
			r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s1", "a1", "backend", f.cwd, false), time.Now())
			assertPass(t, r, false)
		})
	}

	t.Run("trivial_lane", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.openTrivialSpec("trivial", "docs/*.md")
		r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s1", "a1", "backend", f.cwd, false), time.Now())
		assertPass(t, r, false)
	})

	t.Run("frozen", func(t *testing.T) {
		f := newStopHookFixture(t)
		specID, backlogID := implementingStandardSpec(f.sddFixture, "frozen", true)
		if _, err := f.svc.BacklogArchive(context.Background(), model.BacklogArchiveRequest{ID: backlogID, Reason: "discarded"}); err != nil {
			t.Fatalf("BacklogArchive: %v", err)
		}
		if st, err := f.svc.SpecStatus(context.Background(), specID); err != nil || st.Frozen == nil {
			t.Fatalf("precondition: spec must be frozen (err=%v)", err)
		}
		r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s1", "a1", "backend", f.cwd, false), time.Now())
		assertPass(t, r, false)
	})
}

// TestHookSubagentStop_NonImplementersNeverBlocked covers C4.
func TestHookSubagentStop_NonImplementersNeverBlocked(t *testing.T) {
	f := newStopHookFixture(t)
	implementingStandardSpec(f.sddFixture, "pending", false)
	seedManifest(t, f.dbPath, f.slug, `[{"role":"revisor","archetype":"qa-tester","areas":[]},{"role":"constructor","archetype":"backend","areas":[]}]`)

	for _, role := range []string{"qa-tester", "architect", "spec-reviewer", "diagnostician", "revisor"} {
		t.Run(role, func(t *testing.T) {
			r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s-"+role, "a-"+role, role, f.cwd, false), time.Now())
			assertPass(t, r, false)
		})
	}

	t.Run("custom_role_resolves_to_implementer", func(t *testing.T) {
		r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s-custom", "a-custom", "constructor", f.cwd, false), time.Now())
		decodeBlock(t, r.stdout)
	})

	t.Run("custom_role_not_in_manifest", func(t *testing.T) {
		r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s-ghost", "a-ghost", "fantasma", f.cwd, false), time.Now())
		assertPass(t, r, true)
		if !strings.Contains(r.stderr, `role "fantasma" not found in the manifest`) {
			t.Errorf("stderr = %q, want the unresolved-archetype message", r.stderr)
		}
	})

	t.Run("agent_id_without_agent_type", func(t *testing.T) {
		r := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "s-unres", "a-unres", "", f.cwd, false), time.Now())
		assertPass(t, r, true)
		if !strings.Contains(r.stderr, "agent_id present without agent_type") {
			t.Errorf("stderr = %q, want the unresolved-role message", r.stderr)
		}
	})
}

// failingWriter is a stdout that always fails.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

// TestHookStop_FailOpen covers C6: every failure lets the agent stop with at
// most one [mneme] line on stderr.
func TestHookStop_FailOpen(t *testing.T) {
	t.Run(stopEventSubagent, func(t *testing.T) {
		failOpenSubtests(t, stopEventSubagent, "a1", "backend", func(f *stopHookFixture) {
			implementingStandardSpec(f.sddFixture, "pending", false)
		})
	})
	t.Run(stopEventStop, func(t *testing.T) {
		failOpenSubtests(t, stopEventStop, "", "", func(f *stopHookFixture) {
			now := time.Now().UTC().Truncate(time.Second)
			f.work(t, "fail-1", now.Add(-45*time.Minute))
		})
	})
}

// failOpenSubtests runs the fail-open table for one event. Every payload
// carries the identity the event expects, so each case fails on the row it
// measures and not on the identity row. arrange leaves something pending.
func failOpenSubtests(t *testing.T, event, agentID, agentType string, arrange func(f *stopHookFixture)) {
	t.Helper()
	payload := func(cwd string) string { return stopPayload(event, "fail-1", agentID, agentType, cwd, false) }

	t.Run("invalid_json", func(t *testing.T) {
		f := newStopHookFixture(t)
		arrange(f)
		assertPass(t, runStop(event, "{not json", time.Now()), true)
	})

	t.Run("empty_stdin", func(t *testing.T) {
		newStopHookFixture(t)
		assertPass(t, runStop(event, "", time.Now()), false)
	})

	t.Run("oversized_stdin", func(t *testing.T) {
		newStopHookFixture(t)
		r := runStop(event, strings.Repeat(" ", stopHookMaxPayload+1), time.Now())
		assertPass(t, r, true)
		if !strings.Contains(r.stderr, "stdin exceeds") {
			t.Errorf("stderr = %q, want the size message", r.stderr)
		}
	})

	t.Run("db_missing", func(t *testing.T) {
		_, _ = setupDelegationRepo(t)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		assertPass(t, runStop(event, payload(cwd), time.Now()), true)
	})

	t.Run("db_unreadable", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("file permissions do not deny reads on Windows or as root")
		}
		f := newStopHookFixture(t)
		arrange(f)
		if err := f.db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f.dbPath, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(f.dbPath, 0o600) }) //nolint:errcheck // lets t.TempDir remove it
		assertPass(t, runStop(event, payload(f.cwd), time.Now()), true)
	})

	t.Run("no_project_slug", func(t *testing.T) {
		f := newStopHookFixture(t)
		arrange(f)
		r := runStop(event, payload(t.TempDir()), time.Now())
		assertPass(t, r, true)
		if !strings.Contains(r.stderr, "no project detected") {
			t.Errorf("stderr = %q, want the no-project message", r.stderr)
		}
	})

	t.Run("stdout_write_fails", func(t *testing.T) {
		f := newStopHookFixture(t)
		arrange(f)
		var errb bytes.Buffer
		err := runHookStop(context.Background(), event, strings.NewReader(payload(f.cwd)), failingWriter{}, &errb, time.Now)
		if err != nil {
			t.Errorf("runHookStop returned %v, want nil", err)
		}
		if strings.Count(errb.String(), "\n") != 1 || !strings.Contains(errb.String(), "write decision to stdout") {
			t.Errorf("stderr = %q, want one stdout-write line", errb.String())
		}
		if _, err := os.Stat(f.registryPath("fail-1")); !os.IsNotExist(err) {
			t.Errorf("registry must not exist after a failed stdout write (stat err = %v)", err)
		}
		if _, err := os.Stat(f.enforcelogPath()); !os.IsNotExist(err) {
			t.Errorf("enforcelog must not exist after a failed stdout write (stat err = %v)", err)
		}
	})
}

// TestHookStop_Under1s covers C9: a warm subagent-stop that blocks over three
// pending specs answers in well under a second, measured in process.
func TestHookStop_Under1s(t *testing.T) {
	f := newStopHookFixture(t)
	for _, title := range []string{"uno", "dos", "tres"} {
		id, _ := implementingStandardSpec(f.sddFixture, title, false)
		writeWhatSection(f.sddFixture, id)
	}

	warm := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "perf", "warm", "backend", f.cwd, false), time.Now())
	decodeBlock(t, warm.stdout)

	start := time.Now()
	measured := runStop(stopEventSubagent, stopPayload(stopEventSubagent, "perf", "measured", "backend", f.cwd, false), time.Now())
	elapsed := time.Since(start)

	reason := decodeBlock(t, measured.stdout)
	if strings.Count(reason, "\n- SPEC-") != 3 {
		t.Errorf("reason must list the three specs:\n%s", reason)
	}
	if elapsed >= time.Second {
		t.Errorf("warm subagent-stop took %v, want < 1s", elapsed)
	}
	t.Logf("warm subagent-stop with 3 pending specs: %v", elapsed)
}

// readEnforcelog returns the enforcelog lines of the fixture's project.
func readEnforcelog(t *testing.T, f *stopHookFixture) []string {
	t.Helper()
	data, err := os.ReadFile(f.enforcelogPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read enforcelog: %v", err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// TestHookStop_EnforcelogOnBlock covers C10: one line per block, none per
// pass.
func TestHookStop_EnforcelogOnBlock(t *testing.T) {
	t.Run("SubagentStop", func(t *testing.T) {
		f := newStopHookFixture(t)
		specID, _ := implementingStandardSpec(f.sddFixture, "pending", false)
		payload := stopPayload(stopEventSubagent, "log-1", "a1", "backend", f.cwd, false)

		decodeBlock(t, runStop(stopEventSubagent, payload, time.Now()).stdout)
		assertPass(t, runStop(stopEventSubagent, payload, time.Now()), false)

		lines := readEnforcelog(t, f)
		if len(lines) != 1 {
			t.Fatalf("enforcelog has %d lines, want 1: %v", len(lines), lines)
		}
		for _, want := range []string{`"tool":"SubagentStop"`, `"decision":"block"`, `"reason":"delivery_contract_pending"`, `"role":"backend"`, `"target":"` + specID + `"`, `"caller":"subagent"`} {
			if !strings.Contains(lines[0], want) {
				t.Errorf("enforcelog line lacks %s: %s", want, lines[0])
			}
		}
	})

	t.Run("Stop", func(t *testing.T) {
		f := newStopHookFixture(t)
		now := time.Now().UTC().Truncate(time.Second)
		f.work(t, "log-2", now.Add(-45*time.Minute))
		payload := stopPayload(stopEventStop, "log-2", "", "", f.cwd, false)

		decodeBlock(t, runStop(stopEventStop, payload, now).stdout)
		assertPass(t, runStop(stopEventStop, payload, now), false)

		lines := readEnforcelog(t, f)
		if len(lines) != 1 {
			t.Fatalf("enforcelog has %d lines, want 1: %v", len(lines), lines)
		}
		for _, want := range []string{`"tool":"Stop"`, `"caller":"orchestrator"`, `"decision":"block"`, `"reason":"session_summary_pending"`, `"target":"log-2"`} {
			if !strings.Contains(lines[0], want) {
				t.Errorf("enforcelog line lacks %s: %s", want, lines[0])
			}
		}
	})
}

// TestStopRegistry_CorruptOrWrongSchema: a corrupt or foreign-schema registry
// is treated as empty, so the hook blocks and overwrites it.
func TestStopRegistry_CorruptOrWrongSchema(t *testing.T) {
	for name, content := range map[string]string{
		"corrupt":      "{not json",
		"wrong_schema": `{"schema":2,"session_id":"reg-1","delivery_reminded":[{"agent_id":"a1","spec_id":"SPEC-001"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newStopHookFixture(t)
			implementingStandardSpec(f.sddFixture, "pending", false)
			path := f.registryPath("reg-1")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			decodeBlock(t, runStop(stopEventSubagent, stopPayload(stopEventSubagent, "reg-1", "a1", "backend", f.cwd, false), time.Now()).stdout)
			reg := loadStopRegistry(path, "reg-1")
			if len(reg.DeliveryReminded) != 1 {
				t.Errorf("registry not overwritten: %+v", reg)
			}
		})
	}
}

// TestStopRegistry_Retention: a block prunes registry files older than seven
// days and keeps the recent ones.
func TestStopRegistry_Retention(t *testing.T) {
	f := newStopHookFixture(t)
	implementingStandardSpec(f.sddFixture, "pending", false)
	dir := filepath.Dir(f.registryPath("x"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old, recent := filepath.Join(dir, "old.json"), filepath.Join(dir, "recent.json")
	for path, age := range map[string]time.Duration{old: 8 * 24 * time.Hour, recent: 24 * time.Hour} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}

	decodeBlock(t, runStop(stopEventSubagent, stopPayload(stopEventSubagent, "ret-1", "a1", "backend", f.cwd, false), now).stdout)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("8-day-old registry must be removed (stat err = %v)", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("1-day-old registry must survive: %v", err)
	}
}

// TestStopRegistry_Permissions: owner-only directory and file.
func TestStopRegistry_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	f := newStopHookFixture(t)
	implementingStandardSpec(f.sddFixture, "pending", false)
	decodeBlock(t, runStop(stopEventSubagent, stopPayload(stopEventSubagent, "perm-1", "a1", "backend", f.cwd, false), time.Now()).stdout)

	path := f.registryPath("perm-1")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != stopRegistryFilePerm {
		t.Errorf("registry file mode = %o, want %o", got, stopRegistryFilePerm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != stopRegistryDirPerm {
		t.Errorf("registry dir mode = %o, want %o", got, stopRegistryDirPerm)
	}
}

// runHookCobra runs `mneme hook <event>` through the real command tree with
// stdin as the payload and returns stdout.
func runHookCobra(t *testing.T, event, stdin string) string {
	t.Helper()
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(stdin); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = r

	var execErr error
	out := captureStdout(t, func() {
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"hook", event})
		execErr = cmd.Execute()
	})
	if execErr != nil {
		t.Fatalf("hook %s returned error: %v", event, execErr)
	}
	return out
}

// TestHookSubagentStop_Cobra: the subcommand is wired and honours
// stop_hook_active end to end.
func TestHookSubagentStop_Cobra(t *testing.T) {
	newStopHookFixture(t)
	if out := runHookCobra(t, stopEventSubagent, `{"stop_hook_active":true}`); out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// TestHookStop_OrchestratorReminderPerWindow covers C5: one reminder per work
// window, the window starting at the first work memory after the latest
// close, and only past the threshold (default 30m). Every instant falls in its
// own second (D20 caveat).
func TestHookStop_OrchestratorReminderPerWindow(t *testing.T) {
	const sid = "orch-1"
	now := time.Now().UTC().Truncate(time.Second)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	payload := func(f *stopHookFixture) string { return stopPayload(stopEventStop, sid, "", "", f.cwd, false) }

	t.Run("first_window", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.work(t, sid, ago(45*time.Minute))
		f.work(t, sid, ago(30*time.Minute))
		f.work(t, sid, ago(10*time.Minute))

		first := runStop(stopEventStop, payload(f), now)
		reason := decodeBlock(t, first.stdout)
		for _, want := range []string{"`mem_session_end`", sid, "3 memorias", "lleva 45m sin resumen"} {
			if !strings.Contains(reason, want) {
				t.Errorf("reason does not contain %q:\n%s", want, reason)
			}
		}
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("shortcut_without_db", func(t *testing.T) {
		f := newStopHookFixture(t)
		reminded := ago(5 * time.Minute)
		f.writeRegistry(t, stopRegistry{Schema: stopRegistrySchema, SessionID: sid, OrchestratorRemindedAt: &reminded})
		if err := f.db.Close(); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(f.dbPath + suffix); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("closed_no_new_work", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.work(t, sid, ago(45*time.Minute))
		f.work(t, sid, ago(30*time.Minute))
		f.summary(t, sid, ago(5*time.Minute))
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("rearm_after_close", func(t *testing.T) {
		f := newStopHookFixture(t)
		oldAnchor, oldReminder := ago(90*time.Minute), ago(50*time.Minute)
		f.writeRegistry(t, stopRegistry{Schema: stopRegistrySchema, SessionID: sid, OrchestratorRemindedAt: &oldReminder, OrchestratorRemindedAnchor: &oldAnchor})
		f.work(t, sid, oldAnchor)
		f.summary(t, sid, ago(40*time.Minute))
		anchor := ago(35 * time.Minute).Add(117554 * time.Microsecond)
		f.work(t, sid, anchor)
		f.work(t, sid, ago(20*time.Minute))

		reason := decodeBlock(t, runStop(stopEventStop, payload(f), now).stdout)
		if !strings.Contains(reason, "2 memorias") {
			t.Errorf("reason does not count 2 memorias:\n%s", reason)
		}
		reg := loadStopRegistry(f.registryPath(sid), sid)
		if reg.OrchestratorRemindedAnchor == nil || !reg.OrchestratorRemindedAnchor.Equal(anchor) {
			t.Errorf("registry anchor = %v, want %v", reg.OrchestratorRemindedAnchor, anchor)
		}
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
		// Past the shortcut window the anchor itself (nanoseconds included)
		// still identifies the window as reminded.
		assertPass(t, runStop(stopEventStop, payload(f), now.Add(time.Hour)), false)
	})

	t.Run("checkpoint_counts_as_close", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.work(t, sid, ago(45*time.Minute))
		f.work(t, sid, ago(30*time.Minute))
		f.checkpoint(t, ago(5*time.Minute))
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("stale_checkpoint_before_session", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.checkpoint(t, ago(72*time.Hour))
		f.work(t, sid, ago(10*time.Minute))
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("close_then_long_pause", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.work(t, sid, ago(73*time.Hour))
		f.summary(t, sid, ago(72*time.Hour))
		f.work(t, sid, ago(10*time.Minute))
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("young_window", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.work(t, sid, ago(10*time.Minute))
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("no_work", func(t *testing.T) {
		f := newStopHookFixture(t)
		f.work(t, "", ago(45*time.Minute))
		f.work(t, "another-session", ago(45*time.Minute))
		assertPass(t, runStop(stopEventStop, payload(f), now), false)
	})

	t.Run("disabled", func(t *testing.T) {
		_, _ = setupDelegationRepo(t)
		home := os.Getenv("HOME")
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		cfgPath := filepath.Join(home, ".mneme", "config.toml")
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte("[hooks]\nstop_reminder_after = \"0\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertPass(t, runStop(stopEventStop, stopPayload(stopEventStop, sid, "", "", cwd, false), now), false)
	})
}

// backtickIdent extracts what each reason puts between backticks.
var backtickIdent = regexp.MustCompile("`([^`]+)`")

// backtickSet returns the backticked identifiers of reason minus ignore.
func backtickSet(reason, ignore string) map[string]bool {
	set := map[string]bool{}
	for _, m := range backtickIdent.FindAllStringSubmatch(reason, -1) {
		if m[1] != ignore {
			set[m[1]] = true
		}
	}
	return set
}

// TestStopReasons_OnlyNameResolvingTools covers C7 (D8): a reason names only
// the tool whose call resolves the block, and doing what it says (or, for a
// bystander, nothing) lets the agent stop.
func TestStopReasons_OnlyNameResolvingTools(t *testing.T) {
	missing := &model.DeliveryCheck{SpecID: "SPEC-007", Contract: model.DeliveryContractMissing}
	missingDetail := &model.DeliveryCheck{SpecID: "SPEC-008", Contract: model.DeliveryContractMissing, Detail: "tarea.md not readable: no such file"}
	invalid := &model.DeliveryCheck{SpecID: "SPEC-009", Contract: model.DeliveryContractInvalid, Detail: "delivery: riesgo.item 7 out of range 1..6"}
	stale := &model.DeliveryCheck{SpecID: "SPEC-010", Contract: model.DeliveryContractValid, Current: false}

	implementerCases := map[string]struct {
		checks []*model.DeliveryCheck
		want   []string
	}{
		"missing":        {[]*model.DeliveryCheck{missing}, []string{"- SPEC-007 (bloque ausente)", `id = "SPEC-007"`}},
		"missing_detail": {[]*model.DeliveryCheck{missingDetail}, []string{"(bloque ausente: tarea.md not readable: no such file)"}},
		"invalid":        {[]*model.DeliveryCheck{invalid}, []string{"(bloque inválido: delivery: riesgo.item 7 out of range 1..6)"}},
		"stale":          {[]*model.DeliveryCheck{stale}, []string{"(bloque anterior a la última entrada a implementing)"}},
		"several":        {[]*model.DeliveryCheck{stale, invalid, missing, missingDetail}, []string{`id = "<SPEC>"`, "- SPEC-007", "- SPEC-010"}},
	}
	for name, tc := range implementerCases {
		t.Run("implementer_"+name, func(t *testing.T) {
			reason := deliveryPendingReason(tc.checks)
			if got := backtickSet(reason, ""); len(got) != 1 || !got["spec_doc_write"] {
				t.Errorf("backticked identifiers = %v, want exactly {spec_doc_write}:\n%s", got, reason)
			}
			for _, w := range append(tc.want, bystanderPhrase) {
				if !strings.Contains(reason, w) {
					t.Errorf("reason does not contain %q:\n%s", w, reason)
				}
			}
		})
	}
	if r := deliveryPendingReason([]*model.DeliveryCheck{stale, missing}); strings.Index(r, "SPEC-007") > strings.Index(r, "SPEC-010") {
		t.Errorf("specs must be listed by id:\n%s", r)
	}

	for _, tc := range []struct {
		sid string
		n   int
		age time.Duration
	}{
		{"abc123", 3, 47 * time.Minute},
		{"mem_session_end_like.id-1", 1, 65 * time.Minute},
	} {
		t.Run("orchestrator_"+tc.sid, func(t *testing.T) {
			reason := sessionSummaryPendingReason(tc.sid, tc.n, tc.age)
			if got := backtickSet(reason, tc.sid); len(got) != 1 || !got["mem_session_end"] {
				t.Errorf("backticked identifiers = %v, want exactly {mem_session_end}:\n%s", got, reason)
			}
		})
	}

	t.Run("resolves_owner", func(t *testing.T) {
		f := newStopHookFixture(t)
		specID, _ := implementingStandardSpec(f.sddFixture, "owned", false)
		payload := stopPayload(stopEventSubagent, "d8-owner", "owner", "backend", f.cwd, false)
		decodeBlock(t, runStop(stopEventSubagent, payload, time.Now()).stdout)

		writeDeliveryBlock(f.sddFixture, specID, "hecho")
		if err := os.Remove(f.registryPath("d8-owner")); err != nil {
			t.Fatal(err)
		}
		assertPass(t, runStop(stopEventSubagent, payload, time.Now()), false)
	})

	t.Run("resolves_bystander", func(t *testing.T) {
		f := newStopHookFixture(t)
		implementingStandardSpec(f.sddFixture, "someone else's", false)
		payload := stopPayload(stopEventSubagent, "d8-bystander", "bystander", "bug-hunter", f.cwd, false)
		reason := decodeBlock(t, runStop(stopEventSubagent, payload, time.Now()).stdout)
		if !strings.Contains(reason, bystanderPhrase) {
			t.Errorf("reason lacks the bystander phrase:\n%s", reason)
		}
		assertPass(t, runStop(stopEventSubagent, payload, time.Now()), false)
	})

	t.Run("resolves_orchestrator", func(t *testing.T) {
		f := newStopHookFixture(t)
		now := time.Now().UTC().Truncate(time.Second)
		f.work(t, "d8-orch", now.Add(-45*time.Minute))
		f.work(t, "d8-orch", now.Add(-10*time.Minute))
		payload := stopPayload(stopEventStop, "d8-orch", "", "", f.cwd, false)
		decodeBlock(t, runStop(stopEventStop, payload, now).stdout)

		f.summary(t, "d8-orch", now.Add(-5*time.Minute))
		if err := os.Remove(f.registryPath("d8-orch")); err != nil {
			t.Fatal(err)
		}
		assertPass(t, runStop(stopEventStop, payload, now), false)
	})
}

// TestHookStop_Cobra: the stop subcommand is wired and honours
// stop_hook_active end to end.
func TestHookStop_Cobra(t *testing.T) {
	newStopHookFixture(t)
	if out := runHookCobra(t, stopEventStop, `{"stop_hook_active":true}`); out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// TestFormatStopAge pins the age format of the orchestrator reason.
func TestFormatStopAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		47*time.Minute + 10*time.Second: "47m",
		time.Hour + 5*time.Minute:       "1h5m",
		20 * time.Second:                "0m",
		0:                               "0m",
		2 * time.Hour:                   "2h0m",
	} {
		if got := formatStopAge(d); got != want {
			t.Errorf("formatStopAge(%v) = %q, want %q", d, got, want)
		}
	}
}
