package speech

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestSupervisorActionsReplaceAndStop(t *testing.T) {
	started := make(chan struct{}, 2)
	var cancelled atomic.Int32
	s := &supervisor{token: "token", done: make(chan struct{}), engine: "fake", synth: func(ctx context.Context, _ Request) error {
		started <- struct{}{}
		<-ctx.Done()
		cancelled.Add(1)
		return ctx.Err()
	}}
	if got := s.execute(Request{Action: "wat"}); got.OK || got.Error == "" {
		t.Fatalf("unknown=%+v", got)
	}
	if got := s.execute(Request{Action: "speak", Text: "one"}); !got.OK || !got.Speaking {
		t.Fatalf("speak=%+v", got)
	}
	<-started
	if got := s.execute(Request{Action: "status"}); !got.Speaking || got.Engine != "fake" {
		t.Fatalf("status=%+v", got)
	}
	_ = s.execute(Request{Action: "speak", Text: "two"})
	<-started
	deadline := time.Now().Add(time.Second)
	for cancelled.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cancelled.Load() < 1 {
		t.Fatal("replacement did not cancel first synthesis")
	}
	if got := s.execute(Request{Action: "stop"}); !got.OK {
		t.Fatal("stop failed")
	}
	if got := s.execute(Request{Action: "shutdown"}); !got.OK || !s.isDone() {
		t.Fatal("shutdown failed")
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
