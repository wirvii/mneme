package mcp

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/speech"
)

// runFakeSupervisor starts a loopback listener that answers "status" and
// "cancel" with a bare OK (so RegisterPrompt's cancel and Emit's
// EnsureSupervisor both succeed without spawning the real test binary as a
// supervisor process), and "speak" with speakResponse — letting each test
// case decide whether the emission started now or had to wait its turn. It
// writes the runtime descriptor under dataDir so speech.Send finds it, and
// closes the listener when the subtest ends.
func runFakeSupervisor(t *testing.T, dataDir string, speakResponse speech.Response) {
	t.Helper()

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
				resp := speech.Response{OK: true}
				if req.Action == "speak" {
					resp = speakResponse
				}
				_ = json.NewEncoder(conn).Encode(resp)
			}()
		}
	}()

	runtimeDir := speech.RuntimeDir(dataDir)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	desc := speech.RuntimeDescriptor{Address: listener.Addr().String(), Token: "test-token", PID: os.Getpid()}
	data, err := json.Marshal(desc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "runtime.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestSpeechEmitReportsStartedOrQueued is AC10: speech_emit reports
// spoken:true (with no queued/queue_position keys) when the emission
// started playing right now, and spoken:false with queued:true plus
// queue_position when it had to wait its turn instead (D15) — never both.
func TestSpeechEmitReportsStartedOrQueued(t *testing.T) {
	srv := newTestServer(t)

	cfg := config.Default()
	speechCfg := cfg.Speech
	speechCfg.Enabled = true
	if err := config.SetSpeech(config.DefaultPath(), speechCfg); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		fakeResponse speech.Response
		wantSpoken   bool
		wantQueued   bool
		wantPosition float64
	}{
		{"started", speech.Response{OK: true, Started: true}, true, false, 0},
		{"queued", speech.Response{OK: true, Started: false, Position: 1}, false, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionID := "session-" + tt.name
			runFakeSupervisor(t, cfg.Storage.DataDir, tt.fakeResponse)

			speechSvc := service.NewSpeechService(config.DefaultPath(), cfg.Storage.DataDir)
			if _, err := speechSvc.RegisterPrompt(context.Background(), sessionID); err != nil {
				t.Fatalf("RegisterPrompt: %v", err)
			}

			resp := process(t, srv, "tools/call", 1, ToolCallParams{
				Name: "speech_emit",
				Arguments: mustMarshal(t, map[string]any{
					"disposition": "emit",
					"mode":        "brief",
					"text":        "Terminé la tarea.",
					"session_id":  sessionID,
				}),
			})
			if resp.Error != nil {
				t.Fatalf("speech_emit: %s", resp.Error.Message)
			}
			var result map[string]any
			unmarshalToolText(t, resp, &result)

			if got, _ := result["spoken"].(bool); got != tt.wantSpoken {
				t.Fatalf("spoken=%v, want %v (result=%v)", got, tt.wantSpoken, result)
			}
			_, hasQueued := result["queued"]
			if hasQueued != tt.wantQueued {
				t.Fatalf("queued key present=%v, want %v (result=%v)", hasQueued, tt.wantQueued, result)
			}
			_, hasPosition := result["queue_position"]
			if hasPosition != tt.wantQueued {
				t.Fatalf("queue_position key present=%v, want %v (result=%v)", hasPosition, tt.wantQueued, result)
			}
			if tt.wantQueued {
				if got, _ := result["queue_position"].(float64); got != tt.wantPosition {
					t.Fatalf("queue_position=%v, want %v", got, tt.wantPosition)
				}
			}
		})
	}
}
