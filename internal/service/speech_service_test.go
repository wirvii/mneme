package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/speech"
)

// TestSpeechRegisterPromptOptInAndMissedTurns used to assert that
// CheckExpectation("turn-1") failed once "turn-2" registered — that
// assertion WAS the BL-198 bug (a single host-wide expectation file, so any
// other session's prompt clobbered this one's). SPEC-129 D2 gives every
// session its own file: two different sessions registering in sequence must
// both stay valid, and neither should count as a missed turn for the other
// (D4). See TestExpectationsAreIsolatedPerSession and
// TestMissedTurnsCountOnlyOwnSession for the dedicated regression coverage.
func TestSpeechRegisterPromptOptInAndMissedTurns(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	dataDir := filepath.Join(root, "data")
	svc := NewSpeechService(configPath, dataDir)

	protocol, err := svc.RegisterPrompt(context.Background(), "off-turn")
	if err != nil || protocol != "" {
		t.Fatalf("disabled RegisterPrompt = %q, %v", protocol, err)
	}

	speechCfg := config.Default().Speech
	speechCfg.Enabled = true
	if err := config.SetSpeech(configPath, speechCfg); err != nil {
		t.Fatal(err)
	}
	protocol, err = svc.RegisterPrompt(context.Background(), "turn-1")
	if err != nil || !strings.Contains(protocol, "speech_emit exactly once") || !strings.Contains(protocol, `session_id="turn-1"`) {
		t.Fatalf("enabled RegisterPrompt = %q, %v", protocol, err)
	}
	if _, err := svc.RegisterPrompt(context.Background(), "turn-2"); err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.MissedTurns != 0 {
		t.Fatalf("missed turns = %d, want 0: turn-2 is a different session, not a missed turn of turn-1", status.MissedTurns)
	}
	if err := svc.CheckExpectation("turn-1"); err != nil {
		t.Fatalf("turn-1's own expectation must survive turn-2 registering: %v", err)
	}
	if err := svc.CheckExpectation("turn-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveExpectation("turn-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveExpectation("turn-2"); err != nil {
		t.Fatal(err)
	}
}

// TestExpectationsAreIsolatedPerSession is AC7, the direct regression of
// BL-198 symptoms 1 and 3: resolving one session's expectation must never
// invalidate another's.
func TestExpectationsAreIsolatedPerSession(t *testing.T) {
	root := t.TempDir()
	svc := newEnabledSpeechService(t, root)

	if _, err := svc.RegisterPrompt(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterPrompt(context.Background(), "B"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckExpectation("A"); err != nil {
		t.Fatalf("CheckExpectation(A) = %v", err)
	}
	if err := svc.CheckExpectation("B"); err != nil {
		t.Fatalf("CheckExpectation(B) = %v", err)
	}
	if err := svc.ResolveExpectation("A"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckExpectation("B"); err != nil {
		t.Fatalf("resolving A invalidated B's expectation: %v", err)
	}
}

// TestMissedTurnsCountOnlyOwnSession is AC8: missed_turns only rises when a
// session's OWN previous expectation was left unresolved — never because a
// different session registered in between.
func TestMissedTurnsCountOnlyOwnSession(t *testing.T) {
	root := t.TempDir()
	svc := newEnabledSpeechService(t, root)

	if _, err := svc.RegisterPrompt(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterPrompt(context.Background(), "B"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterPrompt(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.MissedTurns != 1 {
		t.Fatalf("missed turns = %d, want 1 (A's own second prompt, not B's)", status.MissedTurns)
	}
}

// TestMissedTurnsResetOnMetadataMigration is AC9: the pre-SPEC-129
// missed_turns accumulator (391 on the owner's host, counting collisions
// between sessions, not real negligence — D4) does not survive the first
// write under the new definition.
func TestMissedTurnsResetOnMetadataMigration(t *testing.T) {
	root := t.TempDir()
	svc := newEnabledSpeechService(t, root)
	cfg, err := svc.load()
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"missed_turns":391}`)
	if err := os.MkdirAll(filepath.Dir(svc.metadataPath(cfg)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.metadataPath(cfg), legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.RegisterPrompt(context.Background(), "A"); err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.MissedTurns != 0 {
		t.Fatalf("missed turns after migration = %d, want 0", status.MissedTurns)
	}
	data, err := os.ReadFile(svc.metadataPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version":1`) {
		t.Fatalf("metadata after migration = %s, want version:1", data)
	}
}

// newEnabledSpeechService returns a SpeechService with speech persistently
// enabled, rooted under a fresh temp directory.
func newEnabledSpeechService(t *testing.T, root string) *SpeechService {
	t.Helper()
	configPath := filepath.Join(root, "config.toml")
	dataDir := filepath.Join(root, "data")
	speechCfg := config.Default().Speech
	speechCfg.Enabled = true
	if err := config.SetSpeech(configPath, speechCfg); err != nil {
		t.Fatal(err)
	}
	return NewSpeechService(configPath, dataDir)
}

func TestSpeechSetupLocalModelVerifiesChecksum(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "voice.onnx")
	content := []byte("local model")
	if err := os.WriteFile(model, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	expected := hex.EncodeToString(digest[:])
	svc := NewSpeechService(filepath.Join(root, "config.toml"), filepath.Join(root, "data"))
	if err := svc.SetupLocalModel(model, strings.Repeat("0", 64)); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if err := svc.SetupLocalModel(model, expected); err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.SetupReady || status.ConfiguredEngine != "piper" {
		t.Fatalf("status=%+v", status)
	}
}

func TestSpeechLanguagePreferencePersistsAndResolves(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	svc := NewSpeechService(configPath, filepath.Join(root, "data"))
	if err := svc.SetLanguagePreference("es_MX", "system", "Paulina", "", ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	preference := cfg.Speech.Languages["es-MX"]
	if preference.Engine != "system" || preference.Voice != "Paulina" {
		t.Fatalf("preference = %+v", preference)
	}
	cfg.Speech.Language = "es-MX"
	if err := config.SetSpeech(configPath, cfg.Speech); err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PreferredEngine != "system" || status.PreferredVoice != "Paulina" || status.PreferenceSource != "exact" {
		t.Fatalf("status = %+v", status)
	}
}

func TestSpeechListVoicesForRejectsUnknownEngine(t *testing.T) {
	t.Parallel()
	svc := NewSpeechService(filepath.Join(t.TempDir(), "config.toml"), t.TempDir())
	tests := []struct {
		name      string
		engine    string
		wantError bool
	}{
		{"empty", "", false},
		{"auto", "auto", false},
		{"system", "system", false},
		{"say", "say", false},
		{"sapi", "sapi", false},
		{"piper", "piper", false},
		{"retired kokoro", "kokoro", true},
		{"unknown", "banana", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.ListVoicesFor(context.Background(), tt.engine)
			if tt.wantError {
				if !errors.Is(err, speech.ErrUnknownEngine) {
					t.Fatalf("engine=%q err=%v, want ErrUnknownEngine", tt.engine, err)
				}
				return
			}
			// A recognized engine must never be rejected as unknown. It may
			// still fail for host-specific reasons (e.g. Linux voices are
			// Piper model files, not a listable system catalog) — that is
			// ListVoices' business, not ListVoicesFor's, so this asserts
			// only the contract under test.
			if errors.Is(err, speech.ErrUnknownEngine) {
				t.Fatalf("engine=%q unexpectedly rejected as unknown: %v", tt.engine, err)
			}
		})
	}
}

func TestSpeechEmitOverrideStillHonorsDisabled(t *testing.T) {
	svc := NewSpeechService(filepath.Join(t.TempDir(), "config.toml"), t.TempDir())
	_, err := svc.Emit(context.Background(), SpeechEmitRequest{
		Disposition: speech.DispositionEmit, Mode: speech.ModeBrief, Text: "Prueba.", Language: "es", Voice: "Paulina",
	})
	if !errors.Is(err, speech.ErrDisabled) {
		t.Fatalf("err=%v", err)
	}
}

// TestRegisterPromptSurvivesOldSupervisor is AC13's service half: a
// supervisor left running by an older mneme binary answers "cancel" with
// "unknown action", and RegisterPrompt must climb D18's compatibility
// ladder (cancel -> shutdown -> status -> cancel -> stop) and still return
// its protocol block with no error.
//
// The fake listener answers "status" with ok:true UNCONDITIONALLY, even
// after "shutdown" — it must keep doing so, or EnsureSupervisor's own
// initial status check fails and it tries to launch the real test binary as
// a supervisor, which then waits out its own 5s startup timeout. This test
// exercises the CALL LADDER, not a real process launch — that is already
// covered by TestEnsureSupervisorStartsReusesAndRecoversStaleLock in
// internal/speech.
func TestRegisterPromptSurvivesOldSupervisor(t *testing.T) {
	root := t.TempDir()
	svc := newEnabledSpeechService(t, root)
	cfg, err := svc.load()
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	var mu sync.Mutex
	var sequence []string
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				var req speech.Request
				if decodeErr := json.NewDecoder(conn).Decode(&req); decodeErr != nil {
					return
				}
				mu.Lock()
				sequence = append(sequence, req.Action)
				mu.Unlock()
				var resp speech.Response
				switch req.Action {
				case "status", "shutdown", "stop":
					resp = speech.Response{OK: true}
				case "cancel":
					resp = speech.Response{Error: "unknown action"}
				}
				_ = json.NewEncoder(conn).Encode(resp)
			}()
		}
	}()

	runtimeDir := speech.RuntimeDir(cfg.Storage.DataDir)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	desc := speech.RuntimeDescriptor{Address: listener.Addr().String(), Token: "secret", PID: 1, StartedAt: time.Now().UTC()}
	data, _ := json.Marshal(desc)
	if err := os.WriteFile(filepath.Join(runtimeDir, "runtime.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	protocol, err := svc.RegisterPrompt(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("RegisterPrompt = %v", err)
	}
	if !strings.Contains(protocol, "speech_emit exactly once") {
		t.Fatalf("protocol block missing: %q", protocol)
	}

	mu.Lock()
	got := strings.Join(sequence, ",")
	mu.Unlock()
	want := "cancel,shutdown,status,cancel,stop"
	if got != want {
		t.Fatalf("call sequence = %q, want %q", got, want)
	}
}
