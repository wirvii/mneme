package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SPEECH_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if out := os.Getenv("SPEECH_HELPER_OUTPUT"); out != "" {
		_, _ = os.Stdout.WriteString(out)
	}
	if os.Getenv("SPEECH_HELPER_FAIL") == "1" {
		os.Exit(2)
	}
	os.Exit(0)
}

func withHelperCommands(t *testing.T, output string, fail bool) {
	t.Helper()
	t.Setenv("GO_WANT_SPEECH_HELPER", "1")
	t.Setenv("SPEECH_HELPER_OUTPUT", output)
	if fail {
		t.Setenv("SPEECH_HELPER_FAIL", "1")
	}
	old := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess", "--", name)
		return cmd
	}
	t.Cleanup(func() { commandContext = old })
}

func TestSupervisorAuthenticatedProtocol(t *testing.T) {
	dataDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Supervise(ctx, dataDir) }()

	var desc RuntimeDescriptor
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(RuntimeDir(dataDir), "runtime.json"))
		if err == nil && json.Unmarshal(data, &desc) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if desc.Address == "" || len(desc.Token) != 64 {
		t.Fatalf("invalid descriptor: %+v", desc)
	}

	response, err := Send(context.Background(), dataDir, Request{Action: "status"})
	if err != nil || !response.OK {
		t.Fatalf("authenticated status = %+v, %v", response, err)
	}

	conn, err := net.Dial("tcp", desc.Address)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(Request{Token: "wrong", Action: "status"}); err != nil {
		t.Fatal(err)
	}
	var denied Response
	if err := json.NewDecoder(conn).Decode(&denied); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if denied.OK || denied.Error != "unauthorized" {
		t.Fatalf("unauthorized response: %+v", denied)
	}

	if _, err := Send(context.Background(), dataDir, Request{Action: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestDescriptorValidationAndEngineNames(t *testing.T) {
	dir := t.TempDir()
	if _, err := readDescriptor(dir); err == nil {
		t.Fatal("missing descriptor accepted")
	}
	if err := os.MkdirAll(RuntimeDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDescriptor(dir); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if err := os.WriteFile(descriptorPath(dir), []byte(`{"address":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDescriptor(dir); err == nil {
		t.Fatal("incomplete descriptor accepted")
	}
	for goos, want := range map[string]string{"darwin": "say", "windows": "system.speech", "linux": "piper", "plan9": "unsupported"} {
		if got := engineName(goos); got != want {
			t.Errorf("engineName(%s)=%s", goos, got)
		}
	}
}

// TestSupervisorActionsReplaceAndStop used to assert that a second "speak"
// cancelled the first — exactly the behaviour D6 (SPEC-129) revokes: with a
// queue, the second waits its turn instead of silencing the first. Kept
// under its original name (it is not a regression, and exercises the same
// speak/status/stop/shutdown surface); only the middle assertion changed.
func TestSupervisorActionsReplaceAndStop(t *testing.T) {
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	var cancelled atomic.Int32
	release := make(chan struct{})
	s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", synth: func(ctx context.Context, _ Request) error {
		call := calls.Add(1)
		started <- struct{}{}
		if call == 1 {
			<-release
		}
		if ctx.Err() != nil {
			cancelled.Add(1)
		}
		return ctx.Err()
	}}
	if got := s.execute(Request{Action: "wat"}); got.OK || got.Error == "" {
		t.Fatalf("unknown=%+v", got)
	}
	if got := s.execute(Request{Action: "speak", Text: "one"}); !got.OK || !got.Speaking || !got.Started {
		t.Fatalf("speak=%+v", got)
	}
	<-started
	if got := s.execute(Request{Action: "status"}); !got.Speaking || got.Engine != "fake" {
		t.Fatalf("status=%+v", got)
	}
	second := s.execute(Request{Action: "speak", Text: "two"})
	if !second.OK || second.Started || second.Position != 1 {
		t.Fatalf("second speak = %+v, want accepted and queued at position 1", second)
	}
	select {
	case <-started:
		t.Fatal("second synthesis started before the first finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-started // the second now starts, once the first returns
	if cancelled.Load() != 0 {
		t.Fatalf("cancelled=%d, want 0: a second speak must never cancel the first (D6)", cancelled.Load())
	}
	if got := s.execute(Request{Action: "stop"}); !got.OK {
		t.Fatal("stop failed")
	}
	if got := s.execute(Request{Action: "shutdown"}); !got.OK || !s.isDone() {
		t.Fatal("shutdown failed")
	}
}

// TestSupervisorCancelIsSessionScoped is AC1, the central criterion of
// SPEC-129: writing in one session must never silence another's audio.
func TestSupervisorCancelIsSessionScoped(t *testing.T) {
	started := make(chan struct{}, 1)
	var cancelledA atomic.Int32
	s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", synth: func(ctx context.Context, _ Request) error {
		started <- struct{}{}
		<-ctx.Done()
		cancelledA.Add(1)
		return ctx.Err()
	}}
	if got := s.execute(Request{Action: "speak", Text: "a", SessionID: "A"}); !got.OK || !got.Started {
		t.Fatalf("speak A = %+v", got)
	}
	<-started

	if got := s.execute(Request{Action: "cancel", SessionID: "B"}); !got.OK {
		t.Fatalf("cancel B = %+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if cancelledA.Load() != 0 {
		t.Fatal("session B's cancel touched session A's audio")
	}
	if status := s.execute(Request{Action: "status"}); !status.Speaking {
		t.Fatal("A stopped speaking after an unrelated session's cancel")
	}

	if got := s.execute(Request{Action: "cancel", SessionID: "A"}); !got.OK {
		t.Fatalf("cancel A = %+v", got)
	}
	deadline := time.Now().Add(time.Second)
	for cancelledA.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cancelledA.Load() < 1 {
		t.Fatal("cancel A did not cancel A's own audio")
	}
}

// TestCancelDropsOnlyOwnQueued is AC2: cancelling a session removes only
// that session's queued entries, in place, leaving the rest untouched.
func TestCancelDropsOnlyOwnQueued(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", synth: func(context.Context, Request) error {
		started <- struct{}{}
		<-release
		return nil
	}}
	if got := s.execute(Request{Action: "speak", Text: "seed", SessionID: "seed"}); !got.Started {
		t.Fatalf("seed = %+v", got)
	}
	<-started
	if got := s.execute(Request{Action: "speak", Text: "a1", SessionID: "A"}); got.Started || got.Position != 1 {
		t.Fatalf("A1 = %+v", got)
	}
	if got := s.execute(Request{Action: "speak", Text: "b1", SessionID: "B"}); got.Started || got.Position != 2 {
		t.Fatalf("B1 = %+v", got)
	}
	if got := s.execute(Request{Action: "speak", Text: "a2", SessionID: "A"}); got.Started || got.Position != 3 {
		t.Fatalf("A2 = %+v", got)
	}
	if got := s.execute(Request{Action: "cancel", SessionID: "A"}); !got.OK {
		t.Fatalf("cancel A = %+v", got)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) != 1 || s.queue[0].sessionID != "B" {
		t.Fatalf("queue after cancelling A = %+v, want exactly B1", s.queue)
	}
}

// TestQueueSerializesEmissions is AC3: never two voices at once — the
// second speak waits for the first to finish before its synth is invoked.
func TestQueueSerializesEmissions(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", synth: func(context.Context, Request) error {
		started <- struct{}{}
		<-release
		return nil
	}}
	first := s.execute(Request{Action: "speak", Text: "one", SessionID: "A"})
	if !first.OK || !first.Started {
		t.Fatalf("first = %+v", first)
	}
	<-started
	second := s.execute(Request{Action: "speak", Text: "two", SessionID: "B"})
	if !second.OK || second.Started || second.Position != 1 {
		t.Fatalf("second = %+v, want accepted and queued at position 1", second)
	}
	select {
	case <-started:
		t.Fatal("second synthesis started before the first finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-started
}

// TestOriginPrefixIsSpoken is AC4 (the supervisor half): the text handed to
// synth carries the spoken origin prefix exactly when D10's four conditions
// all hold, and never otherwise.
func TestOriginPrefixIsSpoken(t *testing.T) {
	writeFocus := func(t *testing.T, dataDir, sessionID string) {
		t.Helper()
		if sessionID == "" {
			return
		}
		if err := os.MkdirAll(RuntimeDir(dataDir), 0o700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(struct {
			SessionID string `json:"session_id"`
		}{SessionID: sessionID})
		if err := os.WriteFile(focusPath(dataDir), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name       string
		focus      string
		sessionID  string
		origin     string
		wantPrefix bool
	}{
		{"all four conditions hold", "other-session", "mine", "mneme", true},
		{"empty focus: nobody has typed yet", "", "mine", "mneme", false},
		{"empty owner: legacy client", "other-session", "", "mneme", false},
		{"owner equals focus: user is looking at it", "mine", "mine", "mneme", false},
		{"empty label: nothing to attribute", "other-session", "mine", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			writeFocus(t, dataDir, tc.focus)
			var text string
			done := make(chan struct{})
			s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", dataDir: dataDir, synth: func(_ context.Context, req Request) error {
				text = req.Text
				close(done)
				return nil
			}}
			if got := s.execute(Request{Action: "speak", Text: "hola", Language: "es", SessionID: tc.sessionID, Origin: tc.origin}); !got.OK {
				t.Fatalf("speak = %+v", got)
			}
			<-done
			gotPrefix := strings.HasPrefix(text, "En mneme: ")
			if gotPrefix != tc.wantPrefix {
				t.Fatalf("text=%q, wantPrefix=%v", text, tc.wantPrefix)
			}
		})
	}
}

// TestExpiredEmissionIsDiscardedSilently is AC5: an emission that waited
// past EmissionTTL is dropped without ever reaching synth, and without
// producing any text — the whole point of a silent discard.
func TestExpiredEmissionIsDiscardedSilently(t *testing.T) {
	mockNow := time.Now()
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var lateCalled atomic.Bool
	s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", now: func() time.Time { return mockNow }, synth: func(_ context.Context, req Request) error {
		if req.SessionID == "seed" {
			started <- struct{}{}
			<-release
			return nil
		}
		lateCalled.Store(true)
		return nil
	}}
	if got := s.execute(Request{Action: "speak", Text: "seed", SessionID: "seed"}); !got.Started {
		t.Fatalf("seed = %+v", got)
	}
	<-started
	if got := s.execute(Request{Action: "speak", Text: "late", SessionID: "A"}); got.Started || got.Position != 1 {
		t.Fatalf("queued = %+v", got)
	}
	mockNow = mockNow.Add(EmissionTTL + time.Second)
	close(release)

	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		dropped, speaking := s.stats.DroppedExpired, s.speaking
		s.mu.Unlock()
		if dropped == 1 && !speaking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dropped_expired=%d speaking=%v, want 1 and false", dropped, speaking)
		}
		time.Sleep(time.Millisecond)
	}
	if lateCalled.Load() {
		t.Fatal("an expired emission reached synth")
	}
}

// TestQueueOverflowDropsOldestPending is AC6: with the queue full, encolar
// evicts the oldest PENDING entry, never the one currently playing.
func TestQueueOverflowDropsOldestPending(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", synth: func(context.Context, Request) error {
		started <- struct{}{}
		<-release
		return nil
	}}
	if got := s.execute(Request{Action: "speak", Text: "seed", SessionID: "seed"}); !got.Started {
		t.Fatalf("seed = %+v", got)
	}
	<-started
	for i := 0; i < MaxQueuedEmissions; i++ {
		sessionID := fmt.Sprintf("s%d", i)
		if got := s.execute(Request{Action: "speak", Text: "x", SessionID: sessionID}); got.Started {
			t.Fatalf("pending entry %d unexpectedly started: %+v", i, got)
		}
	}
	s.mu.Lock()
	queueLen, oldest := len(s.queue), s.queue[0].sessionID
	s.mu.Unlock()
	if queueLen != MaxQueuedEmissions || oldest != "s0" {
		t.Fatalf("queue = %d entries, oldest=%q, want %d and s0", queueLen, oldest, MaxQueuedEmissions)
	}

	if got := s.execute(Request{Action: "speak", Text: "y", SessionID: "overflow"}); got.Started {
		t.Fatalf("overflow entry unexpectedly started: %+v", got)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) != MaxQueuedEmissions {
		t.Fatalf("queue length after overflow = %d, want %d", len(s.queue), MaxQueuedEmissions)
	}
	if s.queue[0].sessionID != "s1" {
		t.Fatalf("oldest pending after overflow = %q, want s1 (s0 must have been evicted)", s.queue[0].sessionID)
	}
	if s.stats.DroppedOverflow != 1 {
		t.Fatalf("dropped_overflow = %d, want 1", s.stats.DroppedOverflow)
	}
	if !s.speaking || s.current == nil || s.current.sessionID != "seed" {
		t.Fatal("overflow evicted the entry that is playing instead of the oldest pending one")
	}
}

// TestLegacyClientActionsStillWork is AC14 — the "old client, new
// supervisor" half of D18's compatibility. The message is sent as raw JSON
// with exactly the fields a pre-SPEC-129 client emits (token, action, text,
// language, voice, rate, model), never session_id or origin: constructing a
// Request{} with the new fields left at zero would only prove Go's zero
// values work, not that the decoder accepts what an old binary really sends.
func TestLegacyClientActionsStillWork(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(RuntimeDir(dataDir), 0o700); err != nil {
		t.Fatal(err)
	}
	focusData, _ := json.Marshal(struct {
		SessionID string `json:"session_id"`
	}{SessionID: "someone-else"})
	if err := os.WriteFile(focusPath(dataDir), focusData, 0o600); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{}, 1)
	var gotText string
	s := &supervisor{token: "secret", done: make(chan struct{}), engine: "fake", dataDir: dataDir, synth: func(_ context.Context, req Request) error {
		gotText = req.Text
		started <- struct{}{}
		return nil
	}}

	sendRaw := func(t *testing.T, raw string) Response {
		t.Helper()
		server, client := net.Pipe()
		done := make(chan struct{})
		go func() { s.handle(server); close(done) }()
		if _, err := client.Write([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := json.NewDecoder(client).Decode(&response); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		<-done
		return response
	}

	legacySpeak := `{"token":"secret","action":"speak","text":"hola","language":"es","voice":"","rate":0,"model":""}`
	if resp := sendRaw(t, legacySpeak); !resp.OK || !resp.Started {
		t.Fatalf("legacy speak = %+v", resp)
	}
	<-started
	if strings.HasPrefix(gotText, "En ") || strings.HasPrefix(gotText, "From ") {
		t.Fatalf("legacy speak with no session_id got a spoken origin prefix: %q", gotText)
	}

	legacyStop := `{"token":"secret","action":"stop"}`
	if resp := sendRaw(t, legacyStop); !resp.OK {
		t.Fatalf("legacy stop = %+v", resp)
	}
	s.mu.Lock()
	speaking, queueLen := s.speaking, len(s.queue)
	s.mu.Unlock()
	if speaking || queueLen != 0 {
		t.Fatalf("stop without a session did not cancel and empty the whole queue: speaking=%v queue=%d", speaking, queueLen)
	}
}

func TestSupervisorSanitizesEngineFailure(t *testing.T) {
	s := &supervisor{done: make(chan struct{}), engine: "fake", synth: func(context.Context, Request) error { return errors.New("secret spoken text") }}
	_ = s.execute(Request{Action: "speak", Text: "private"})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := s.execute(Request{Action: "status"})
		if !status.Speaking {
			if status.Error != "engine_failed" {
				t.Fatalf("status=%+v", status)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("synthesis did not finish")
}

func TestSpeakForOSAndVoices(t *testing.T) {
	withHelperCommands(t, "Voice A\nVoice B\n", false)
	ctx := context.Background()
	req := Request{Text: "hola", Voice: "voice", Rate: 1.2, Model: "model.onnx"}
	if err := speakForOS(ctx, "darwin", req); err != nil {
		t.Fatal(err)
	}
	if err := speakForOS(ctx, "windows", req); err != nil {
		t.Fatal(err)
	}
	oldLook := lookPath
	lookPath = func(name string) (string, error) {
		if name == "aplay" {
			return "/fake/aplay", nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = oldLook })
	if err := speakForOS(ctx, "linux", req); err != nil {
		t.Fatal(err)
	}
	if err := speakForOS(ctx, "linux", Request{Text: "x"}); err == nil {
		t.Fatal("linux accepted missing model")
	}
	if err := speakForOS(ctx, "plan9", req); err == nil {
		t.Fatal("unsupported OS accepted")
	}
	for _, goos := range []string{"darwin", "windows"} {
		voices, err := listVoicesForOS(ctx, goos)
		if err != nil || len(voices) != 2 {
			t.Fatalf("voices %s=%v,%v", goos, voices, err)
		}
	}
	if _, err := listVoicesForOS(ctx, "linux"); err == nil {
		t.Fatal("linux voices unexpectedly listed")
	}
	if _, err := listVoicesForOS(ctx, "plan9"); err == nil {
		t.Fatal("unsupported voices accepted")
	}
}

func TestSpeakAndVoiceFailures(t *testing.T) {
	withHelperCommands(t, "", true)
	if err := speakForOS(context.Background(), "darwin", Request{Text: "x"}); err == nil {
		t.Fatal("command failure swallowed")
	}
	if _, err := listVoicesForOS(context.Background(), "darwin"); err == nil {
		t.Fatal("voice command failure swallowed")
	}
}

// TestSpokenTextNeverReachesArgv is the retirement's ported privacy
// invariant (DD14/AC14): spoken text must reach the native engine only
// through stdin, never as a process argument, on every supported OS.
func TestSpokenTextNeverReachesArgv(t *testing.T) {
	secret := "secreto-que-no-debe-filtrarse"

	tests := []struct {
		name string
		goos string
		req  Request
	}{
		{"darwin", "darwin", Request{Text: secret, Voice: "voice", Rate: 1}},
		{"windows", "windows", Request{Text: secret, Voice: "voice", Rate: 1}},
		{"linux", "linux", Request{Text: secret, Model: "model.onnx"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var invocations [][]string
			old := commandContext
			commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
				mu.Lock()
				invocations = append(invocations, append([]string{name}, args...))
				mu.Unlock()
				return exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
			}
			t.Cleanup(func() { commandContext = old })
			t.Setenv("GO_WANT_SPEECH_HELPER", "1")

			oldLook := lookPath
			lookPath = func(name string) (string, error) {
				if name == "aplay" {
					return "/fake/aplay", nil
				}
				return "", exec.ErrNotFound
			}
			t.Cleanup(func() { lookPath = oldLook })

			if err := speakForOS(context.Background(), tt.goos, tt.req); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(invocations) == 0 {
				t.Fatal("no process invoked")
			}
			for _, invocation := range invocations {
				if strings.Contains(strings.Join(invocation, " "), secret) {
					t.Fatalf("spoken text leaked into argv: %q", invocation)
				}
			}
		})
	}
}

func TestLinuxPlayerPreference(t *testing.T) {
	old := lookPath
	t.Cleanup(func() { lookPath = old })
	for _, want := range []string{"aplay", "paplay", "ffplay"} {
		lookPath = func(name string) (string, error) {
			if name == want {
				return name, nil
			}
			return "", errors.New("missing")
		}
		got, _ := linuxPlayer()
		if got != want {
			t.Fatalf("player=%s want %s", got, want)
		}
	}
	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if got, _ := linuxPlayer(); got != "" {
		t.Fatalf("player=%s", got)
	}
}

func TestSendProtocolErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(RuntimeDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	desc := RuntimeDescriptor{Address: listener.Addr().String(), Token: "secret"}
	data, _ := json.Marshal(desc)
	if err := os.WriteFile(descriptorPath(dir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, _ := listener.Accept()
		defer func() { _ = conn.Close() }()
		var req Request
		_ = json.NewDecoder(conn).Decode(&req)
		_ = json.NewEncoder(conn).Encode(Response{Error: "denied"})
	}()
	if _, err := Send(context.Background(), dir, Request{Action: "status"}); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("Send error=%v", err)
	}
}

// TestSendMapsUnknownAction is AC13's hoja half: a supervisor that replies
// with exactly "unknown action" (the text execute's default branch sends)
// must produce an error errors.Is-comparable to ErrUnknownAction — the
// convention this repository uses instead of matching raw error strings —
// and any other error text must NOT satisfy that comparison.
func TestSendMapsUnknownAction(t *testing.T) {
	respond := func(t *testing.T, errText string) error {
		t.Helper()
		dir := t.TempDir()
		if err := os.MkdirAll(RuntimeDir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		desc := RuntimeDescriptor{Address: listener.Addr().String(), Token: "secret"}
		data, _ := json.Marshal(desc)
		if err := os.WriteFile(descriptorPath(dir), data, 0o600); err != nil {
			t.Fatal(err)
		}
		go func() {
			conn, _ := listener.Accept()
			defer func() { _ = conn.Close() }()
			var req Request
			_ = json.NewDecoder(conn).Decode(&req)
			_ = json.NewEncoder(conn).Encode(Response{Error: errText})
		}()
		_, err = Send(context.Background(), dir, Request{Action: "cancel"})
		return err
	}

	if err := respond(t, "unknown action"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("Send error=%v, want errors.Is ErrUnknownAction", err)
	}
	if err := respond(t, "denied"); errors.Is(err, ErrUnknownAction) {
		t.Fatalf("Send error=%v unexpectedly matched ErrUnknownAction (control case)", err)
	}
}

func TestEnsureSupervisorStartsReusesAndRecoversStaleLock(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := supervisorStarter
	var starts atomic.Int32
	done := make(chan error, 2)
	supervisorStarter = func(_ context.Context, dataDir string) error {
		starts.Add(1)
		go func() { done <- Supervise(ctx, dataDir) }()
		return nil
	}
	t.Cleanup(func() { supervisorStarter = old })
	if err := EnsureSupervisor(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSupervisor(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 {
		t.Fatalf("starts=%d", starts.Load())
	}
	if _, err := Send(context.Background(), dir, Request{Action: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	<-done
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(descriptorPath(dir)); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descriptor not removed")
		}
		time.Sleep(time.Millisecond)
	}
	lock := filepath.Join(RuntimeDir(dir), "startup.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSupervisor(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 2 {
		t.Fatalf("starts after stale=%d", starts.Load())
	}
	_, _ = Send(context.Background(), dir, Request{Action: "shutdown"})
	<-done
}

func TestWaitForSupervisorTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := waitForSupervisor(ctx, t.TempDir()); err == nil {
		t.Fatal("timeout swallowed")
	}
}

func TestPublicPlatformWrappers(t *testing.T) {
	withHelperCommands(t, "Voice\n", false)
	oldGOOS := runtimeGOOS
	runtimeGOOS = "darwin"
	t.Cleanup(func() { runtimeGOOS = oldGOOS })
	if err := speak(context.Background(), Request{Text: "hello", Rate: 1}); err != nil {
		t.Fatal(err)
	}
	voices, err := ListVoices(context.Background())
	if err != nil || len(voices) != 1 {
		t.Fatalf("voices=%v err=%v", voices, err)
	}
}

func TestHandleInvalidJSON(t *testing.T) {
	s := &supervisor{token: "x", done: make(chan struct{}), synth: func(context.Context, Request) error { return nil }}
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() { s.handle(server); close(done) }()
	_, _ = client.Write([]byte("not-json\n"))
	var response Response
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	<-done
	if response.Error != "invalid request" {
		t.Fatalf("response=%+v", response)
	}
}
