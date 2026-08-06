package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
)

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
	if status.MissedTurns != 1 {
		t.Fatalf("missed turns = %d, want 1", status.MissedTurns)
	}
	if err := svc.CheckExpectation("turn-1"); err == nil {
		t.Fatal("stale session unexpectedly owns current expectation")
	}
	if err := svc.CheckExpectation("turn-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveExpectation("turn-2"); err != nil {
		t.Fatal(err)
	}
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
