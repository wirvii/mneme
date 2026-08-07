package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/speech"
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

func TestSpeechLanguagePreferencePersistsAndResolves(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	svc := NewSpeechService(configPath, filepath.Join(root, "data"))
	if err := svc.SetLanguagePreference("es_MX", "kokoro", "ef_dora", "say", "Paulina"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	preference := cfg.Speech.Languages["es-MX"]
	if preference.Engine != "kokoro" || preference.Voice != "ef_dora" || preference.FallbackVoice != "Paulina" {
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
	if status.PreferredEngine != "kokoro" || status.PreferredVoice != "ef_dora" || status.PreferenceSource != "exact" {
		t.Fatalf("status = %+v", status)
	}
}

func TestSpeechListManagedVoicesFiltersByLanguage(t *testing.T) {
	t.Parallel()
	svc := NewSpeechService(filepath.Join(t.TempDir(), "config.toml"), t.TempDir())
	voices, err := svc.ListVoicesFor(context.Background(), "kokoro", "es-MX")
	if err != nil || len(voices) != 1 || voices[0] != "ef_dora" {
		t.Fatalf("Spanish Kokoro voices=%v err=%v", voices, err)
	}
	voices, err = svc.ListVoicesFor(context.Background(), "kokoro", "fr")
	if err != nil || len(voices) != 0 {
		t.Fatalf("French Kokoro voices=%v err=%v", voices, err)
	}
}

func TestSpeechKokoroRecommendationIsOnDemandAndNonDestructive(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	svc := NewSpeechService(configPath, filepath.Join(root, "data"))
	recommended, err := svc.ShouldRecommendKokoro()
	if err != nil || !recommended {
		t.Fatalf("recommended=%t err=%v", recommended, err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("recommendation mutated config: %v", err)
	}
	if err := svc.ConfigureRecommendedKokoro(); err != nil {
		t.Fatal(err)
	}
	recommended, err = svc.ShouldRecommendKokoro()
	if err != nil || recommended {
		t.Fatalf("persisted preference still recommended=%t err=%v", recommended, err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	preference := cfg.Speech.Languages["es"]
	if preference.Engine != "kokoro" || preference.Voice != "ef_dora" || preference.FallbackEngine != "system" {
		t.Fatalf("preference=%+v", preference)
	}
}

func TestSpeechManagedEngineServiceLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launcher fixture uses a POSIX shell")
	}
	launcher := []byte("#!/bin/sh\ncat >/dev/null\nexit 0\n")
	model := []byte("model")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/launcher":
			_, _ = w.Write(launcher)
		case "/model":
			_, _ = w.Write(model)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	root := t.TempDir()
	svc := NewSpeechService(filepath.Join(root, "config.toml"), filepath.Join(root, "data"))
	planFor := func(version string) speech.SetupPlan {
		release := speech.EngineRelease{Engine: "kokoro", Version: version, ModelVersion: "test-model", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Backend: "test", Voice: "ef_dora"}
		for _, item := range []struct {
			name, target, kind string
			body               []byte
		}{{"launcher", speech.LauncherName(runtime.GOOS), "runtime", launcher}, {"model", "weights", "model", model}} {
			digest := sha256.Sum256(item.body)
			release.Artifacts = append(release.Artifacts, speech.Artifact{Name: item.name, Target: item.target, Kind: item.kind, URL: server.URL + "/" + item.name, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(item.body)), License: "Apache-2.0", Executable: item.kind == "runtime"})
		}
		plan, err := speech.NewSetupPlan(release)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	first := planFor("1")
	originalCatalog := speech.ManagedCatalogJSON
	catalog, err := json.Marshal([]speech.EngineRelease{first.Release})
	if err != nil {
		t.Fatal(err)
	}
	speech.ManagedCatalogJSON = string(catalog)
	t.Cleanup(func() { speech.ManagedCatalogJSON = originalCatalog })
	planned, err := svc.ManagedEnginePlan("kokoro")
	if err != nil || planned.Digest != first.Digest {
		t.Fatalf("planned=%+v err=%v", planned, err)
	}
	if _, err := svc.ManagedEnginePlan("other"); err == nil {
		t.Fatal("unsupported managed engine accepted")
	}
	if err := svc.SetupManagedEngine(context.Background(), first, true, first.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := svc.ManagedEngineStatus("kokoro")
	if err != nil || !state.Ready {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err := svc.RepairManagedEngine(context.Background(), first, true, first.Digest); err != nil {
		t.Fatal(err)
	}
	second := planFor("2")
	if err := svc.SetupManagedEngine(context.Background(), second, true, second.Digest); err != nil {
		t.Fatal(err)
	}
	if err := svc.RollbackManagedEngine("kokoro"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveManagedEngine("kokoro", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveManagedEngine("kokoro", true, true); err != nil {
		t.Fatal(err)
	}
}

func TestSpeechEmitOverrideStillHonorsDisabled(t *testing.T) {
	svc := NewSpeechService(filepath.Join(t.TempDir(), "config.toml"), t.TempDir())
	err := svc.EmitWithOverrides(context.Background(), speech.DispositionEmit, speech.ModeBrief, "Prueba.", "es", "kokoro", "ef_dora")
	if !errors.Is(err, speech.ErrDisabled) {
		t.Fatalf("err=%v", err)
	}
}
