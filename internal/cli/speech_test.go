package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/speech"
)

func TestSpeechCommandSurfaceIsNativeOnly(t *testing.T) {
	root := newSpeechCmd()

	t.Run("engine subcommand does not exist", func(t *testing.T) {
		command, _, err := root.Find([]string{"engine"})
		if err == nil && command != nil && command.Name() == "engine" {
			t.Fatal("speech engine subcommand still exists")
		}
	})

	t.Run("surviving subcommands exist", func(t *testing.T) {
		for _, name := range []string{"on", "off", "stop", "status", "mode", "voice", "test", "voices", "setup", "supervise"} {
			if command, _, err := root.Find([]string{name}); err != nil || command.Name() != name {
				t.Fatalf("speech subcommand %q missing: command=%v err=%v", name, command, err)
			}
		}
	})

	t.Run("test keeps mode and voice, drops engine", func(t *testing.T) {
		testCmd, _, err := root.Find([]string{"test"})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"mode", "voice"} {
			if testCmd.Flags().Lookup(name) == nil {
				t.Fatalf("test --%s missing", name)
			}
		}
		if testCmd.Flags().Lookup("engine") != nil {
			t.Fatal("test --engine should have been removed")
		}
	})

	t.Run("voices keeps engine and json, drops language", func(t *testing.T) {
		voices, _, err := root.Find([]string{"voices"})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"engine", "json"} {
			if voices.Flags().Lookup(name) == nil {
				t.Fatalf("voices --%s missing", name)
			}
		}
		if voices.Flags().Lookup("language") != nil {
			t.Fatal("voices --language should have been removed")
		}
	})

	t.Run("on drops yes and native", func(t *testing.T) {
		on, _, err := root.Find([]string{"on"})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"yes", "native"} {
			if on.Flags().Lookup(name) != nil {
				t.Fatalf("on --%s should have been removed", name)
			}
		}
	})
}

// TestSpeechStatusCommandPrintsQueue is AC22: `mneme speech status` prints
// the new queue/degradation lines in text mode, and exposes the same data
// under "queue"/"degraded_reasons" in --json mode. flagDataDir is pinned to
// an isolated temp directory and restored in t.Cleanup, the pattern
// internal/cli/hook_test.go and profile_sessionstart_test.go already use.
func TestSpeechStatusCommandPrintsQueue(t *testing.T) {
	dataDir := t.TempDir()
	oldDataDir := flagDataDir
	flagDataDir = dataDir
	t.Cleanup(func() { flagDataDir = oldDataDir })

	speechCfg := config.Default().Speech
	speechCfg.Enabled = true
	if err := config.SetSpeech(config.DefaultPath(), speechCfg); err != nil {
		t.Fatal(err)
	}

	// A fake supervisor whose status reply reports one overflow discard —
	// enough to make Degraded true via cause 3 (D17).
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
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
				resp := speech.Response{OK: true, Queue: &speech.QueueStats{Pending: 1, DroppedOverflow: 1}}
				_ = json.NewEncoder(conn).Encode(resp)
			}()
		}
	}()
	runtimeDir := speech.RuntimeDir(dataDir)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	desc := speech.RuntimeDescriptor{Address: listener.Addr().String(), Token: "test-token"}
	data, err := json.Marshal(desc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "runtime.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	var textOut bytes.Buffer
	textCmd := newSpeechStatusCmd()
	textCmd.SetOut(&textOut)
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	text := textOut.String()
	for _, want := range []string{"queued: 1", "discarded before playing: 1", "degraded because:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status text missing %q:\n%s", want, text)
		}
	}

	var jsonOut bytes.Buffer
	jsonCmd := newSpeechStatusCmd()
	jsonCmd.SetOut(&jsonOut)
	jsonCmd.SetArgs([]string{"--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var status service.SpeechStatus
	if err := json.Unmarshal(jsonOut.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status --json: %v", err)
	}
	if status.Queue == nil || status.Queue.Pending != 1 || status.Queue.DroppedOverflow != 1 {
		t.Fatalf("status.Queue = %+v, want Pending:1 DroppedOverflow:1", status.Queue)
	}
	if !status.Degraded || len(status.DegradedReasons) == 0 {
		t.Fatalf("status.Degraded=%v DegradedReasons=%v, want degraded with at least one reason", status.Degraded, status.DegradedReasons)
	}
}
