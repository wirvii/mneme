package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/speech"
)

// SpeechService orchestrates host-local speech for CLI and MCP frontends.
type SpeechService struct {
	configPath string
	dataDir    string
}

// SpeechStatus is privacy-safe operational metadata; it never contains text.
type SpeechStatus struct {
	Enabled          bool   `json:"enabled"`
	Mode             string `json:"mode"`
	Engine           string `json:"engine"`
	ConfiguredEngine string `json:"configured_engine"`
	Language         string `json:"language"`
	FallbackLanguage string `json:"fallback_language"`
	SetupReady       bool   `json:"setup_ready"`
	Speaking         bool   `json:"speaking"`
	Emitted          int    `json:"emitted"`
	Skipped          int    `json:"skipped"`
	MissedTurns      int    `json:"missed_turns"`
	LastError        string `json:"last_error,omitempty"`
	PreferredEngine  string `json:"preferred_engine,omitempty"`
	PreferredVoice   string `json:"preferred_voice,omitempty"`
	EffectiveVoice   string `json:"effective_voice,omitempty"`
	Degraded         bool   `json:"degraded"`
	PreferenceSource string `json:"preference_source,omitempty"`
}

type speechMetadata struct {
	Emitted     int    `json:"emitted"`
	Skipped     int    `json:"skipped"`
	MissedTurns int    `json:"missed_turns"`
	LastError   string `json:"last_error,omitempty"`
}

// NewSpeechService constructs a service over the host config and data paths.
func NewSpeechService(configPath, dataDir string) *SpeechService {
	return &SpeechService{configPath: configPath, dataDir: dataDir}
}

func (s *SpeechService) load() (*config.Config, error) {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return nil, err
	}
	if s.dataDir != "" {
		cfg.Storage.DataDir = s.dataDir
	}
	return cfg, nil
}

// SetEnabled persistently enables or disables speech. Disabling also stops audio.
func (s *SpeechService) SetEnabled(ctx context.Context, enabled bool) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}
	cfg.Speech.Enabled = enabled
	if err := config.SetSpeech(s.configPath, cfg.Speech); err != nil {
		return err
	}
	if !enabled {
		_ = s.Stop(ctx)
	}
	return nil
}

// SetMode persistently selects brief or full speech.
func (s *SpeechService) SetMode(mode speech.Mode) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}
	if mode != speech.ModeBrief && mode != speech.ModeFull {
		return speech.ErrInvalidMode
	}
	cfg.Speech.Mode = string(mode)
	return config.SetSpeech(s.configPath, cfg.Speech)
}

// SetLanguagePreference persists an engine and voice for one locale while
// preserving the legacy voice map for older mneme binaries.
func (s *SpeechService) SetLanguagePreference(language, engine, voice, fallbackEngine, fallbackVoice string) error {
	language = strings.TrimSpace(strings.ReplaceAll(language, "_", "-"))
	if language == "" {
		return errors.New("speech: language is required")
	}
	cfg, err := s.load()
	if err != nil {
		return err
	}
	if cfg.Speech.Languages == nil {
		cfg.Speech.Languages = make(map[string]config.SpeechLanguageConfig)
	}
	cfg.Speech.Languages[language] = config.SpeechLanguageConfig{
		Engine: engine, Voice: voice, FallbackEngine: fallbackEngine, FallbackVoice: fallbackVoice,
	}
	return config.SetSpeech(s.configPath, cfg.Speech)
}

// ListVoices returns voices installed on the current host.
func (s *SpeechService) ListVoices(ctx context.Context) ([]string, error) {
	return speech.ListVoices(ctx)
}

// ListVoicesFor returns the host voices for a native engine. Native engines
// are host globals, so the catalog does not depend on language.
func (s *SpeechService) ListVoicesFor(ctx context.Context, engine string) ([]string, error) {
	switch engine {
	case "", "auto", "system", "say", "sapi", "piper":
		return s.ListVoices(ctx)
	default:
		return nil, fmt.Errorf("%w: %s", speech.ErrUnknownEngine, engine)
	}
}

// SetupLocalModel configures an existing local Piper model without downloading.
func (s *SpeechService) SetupLocalModel(modelPath, expectedSHA256 string) error {
	if len(expectedSHA256) != 64 {
		return errors.New("speech: setup requires a 64-character SHA-256 digest")
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return errors.New("speech: setup SHA-256 must be hexadecimal")
	}
	absolute, err := filepath.Abs(modelPath)
	if err != nil {
		return fmt.Errorf("speech: resolve Piper model: %w", err)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return fmt.Errorf("speech: open Piper model: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return fmt.Errorf("speech: hash Piper model: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("speech: close Piper model: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	expectedSHA256 = strings.ToLower(expectedSHA256)
	if actual != expectedSHA256 {
		return fmt.Errorf("speech: Piper model checksum mismatch: got %s", actual)
	}
	cfg, err := s.load()
	if err != nil {
		return err
	}
	cfg.Speech.PiperModel = absolute
	cfg.Speech.PiperSHA256 = expectedSHA256
	cfg.Speech.Engine = "piper"
	return config.SetSpeech(s.configPath, cfg.Speech)
}

// Emit resolves one agent turn by speaking useful text or explicitly skipping it.
func (s *SpeechService) Emit(ctx context.Context, disposition speech.Disposition, mode speech.Mode, text, language string) error {
	return s.emit(ctx, disposition, mode, text, language, "")
}

// EmitWithOverrides speaks a test request without persisting a voice override.
func (s *SpeechService) EmitWithOverrides(ctx context.Context, disposition speech.Disposition, mode speech.Mode, text, language, voice string) error {
	return s.emit(ctx, disposition, mode, text, language, voice)
}

func (s *SpeechService) emit(ctx context.Context, disposition speech.Disposition, mode speech.Mode, text, language, voice string) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}
	if !cfg.Speech.Enabled {
		return speech.ErrDisabled
	}
	cleaned, err := speech.ValidateEmit(disposition, mode, text)
	if err != nil {
		return err
	}
	if disposition == speech.DispositionSkip {
		_ = s.updateMetadata(cfg, func(m *speechMetadata) { m.Skipped++ })
		return nil
	}
	if language == "" || language == "auto" {
		language = cfg.Speech.Language
	}
	if language == "" || language == "auto" {
		language = cfg.Speech.FallbackLanguage
	}
	if err := speech.EnsureSupervisor(ctx, cfg.Storage.DataDir); err != nil {
		return err
	}
	preference := resolveSpeechPreference(cfg, language)
	if voice != "" {
		preference.Voice = voice
	}
	request := speech.Request{Action: "speak", Engine: preference.Engine, Text: cleaned, Language: language, Voice: preference.Voice, Rate: cfg.Speech.Rate, Model: cfg.Speech.PiperModel}
	_, err = speech.Send(ctx, cfg.Storage.DataDir, request)
	if err != nil {
		_ = s.updateMetadata(cfg, func(m *speechMetadata) { m.LastError = "engine_failed" })
		return err
	}
	_ = s.updateMetadata(cfg, func(m *speechMetadata) { m.Emitted++; m.LastError = "" })
	return err
}

// Stop cancels current audio without changing the persistent enabled flag.
func (s *SpeechService) Stop(ctx context.Context) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}
	_, err = speech.Send(ctx, cfg.Storage.DataDir, speech.Request{Action: "stop"})
	if err != nil && (errors.Is(err, os.ErrNotExist) || errors.Is(err, context.DeadlineExceeded)) {
		return nil
	}
	// An absent supervisor is already stopped. Descriptor errors are normalized.
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(speech.RuntimeDir(cfg.Storage.DataDir), "runtime.json")); errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
	}
	return err
}

// Status returns current configuration plus live supervisor state when available.
func (s *SpeechService) Status(ctx context.Context) (SpeechStatus, error) {
	cfg, err := s.load()
	if err != nil {
		return SpeechStatus{}, err
	}
	needsPiper := cfg.Speech.Engine == "piper" || (cfg.Speech.Engine == "auto" && runtime.GOOS == "linux")
	setupReady := !needsPiper || (cfg.Speech.PiperModel != "" && cfg.Speech.PiperSHA256 != "")
	language := cfg.Speech.Language
	if language == "" || language == "auto" {
		language = cfg.Speech.FallbackLanguage
	}
	preference := resolveSpeechPreference(cfg, language)
	effectiveEngine := speech.EngineName(runtime.GOOS)
	status := SpeechStatus{Enabled: cfg.Speech.Enabled, Mode: cfg.Speech.Mode, Engine: effectiveEngine, ConfiguredEngine: cfg.Speech.Engine, Language: cfg.Speech.Language, FallbackLanguage: cfg.Speech.FallbackLanguage, SetupReady: setupReady, PreferredEngine: preference.Engine, PreferredVoice: preference.Voice, EffectiveVoice: preference.Voice, PreferenceSource: preference.Source, Degraded: preference.Engine != "" && preference.Engine != "auto" && preference.Engine != "system" && preference.Engine != effectiveEngine}
	var metadata speechMetadata
	if data, readErr := os.ReadFile(s.metadataPath(cfg)); readErr == nil {
		_ = json.Unmarshal(data, &metadata)
	}
	status.MissedTurns = metadata.MissedTurns
	status.Emitted, status.Skipped, status.LastError = metadata.Emitted, metadata.Skipped, metadata.LastError
	if response, sendErr := speech.Send(ctx, cfg.Storage.DataDir, speech.Request{Action: "status"}); sendErr == nil {
		status.Engine = response.Engine
		status.Speaking = response.Speaking
		if response.Error != "" {
			status.LastError = response.Error
		}
	}
	return status, nil
}

func resolveSpeechPreference(cfg *config.Config, language string) speech.ResolvedPreference {
	preferences := make(map[string]speech.Preference, len(cfg.Speech.Languages))
	for locale, preference := range cfg.Speech.Languages {
		preferences[locale] = speech.Preference{
			Engine: preference.Engine, Voice: preference.Voice,
			FallbackEngine: preference.FallbackEngine, FallbackVoice: preference.FallbackVoice,
		}
	}
	defaultEngine := cfg.Speech.Engine
	if defaultEngine == "" || defaultEngine == "auto" {
		defaultEngine = "system"
	}
	return speech.ResolvePreference(language, preferences, cfg.Speech.Voices, defaultEngine)
}

// RegisterPrompt cancels audio and records an unresolved turn expectation.
func (s *SpeechService) RegisterPrompt(ctx context.Context, sessionID string) (string, error) {
	cfg, err := s.load()
	if err != nil {
		return "", err
	}
	if !cfg.Speech.Enabled {
		return "", nil
	}
	_ = s.Stop(ctx)
	path := filepath.Join(speech.RuntimeDir(cfg.Storage.DataDir), "expectations.json")
	type expectation struct {
		SessionID string    `json:"session_id"`
		CreatedAt time.Time `json:"created_at"`
	}
	var previous expectation
	if data, readErr := os.ReadFile(path); readErr == nil && json.Unmarshal(data, &previous) == nil && previous.SessionID != "" {
		_ = s.incrementMissed(cfg)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	data, _ := json.Marshal(expectation{SessionID: sessionID, CreatedAt: time.Now().UTC()})
	if err := writePrivateAtomic(path, data); err != nil {
		return "", fmt.Errorf("speech: save expectation: %w", err)
	}
	return fmt.Sprintf("<mneme:speech>Speech is enabled in %s mode. Before your final response, call speech_emit exactly once with disposition=emit and a concise semantic spoken summary, or disposition=skip when nothing adds value. Use session_id=%q. Never read raw tool output or code.</mneme:speech>", cfg.Speech.Mode, sessionID), nil
}

// ResolveExpectation clears a matching turn expectation without persisting text.
func (s *SpeechService) ResolveExpectation(sessionID string) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}
	path := filepath.Join(speech.RuntimeDir(cfg.Storage.DataDir), "expectations.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var current struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.SessionID != sessionID {
		return errors.New("speech: session does not match active expectation")
	}
	return os.Remove(path)
}

// CheckExpectation verifies that sessionID owns the unresolved current turn.
func (s *SpeechService) CheckExpectation(sessionID string) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(speech.RuntimeDir(cfg.Storage.DataDir), "expectations.json"))
	if err != nil {
		return fmt.Errorf("speech: read expectation: %w", err)
	}
	var current struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return fmt.Errorf("speech: parse expectation: %w", err)
	}
	if current.SessionID != sessionID {
		return errors.New("speech: session does not match active expectation")
	}
	return nil
}

func (s *SpeechService) metadataPath(cfg *config.Config) string {
	return filepath.Join(speech.RuntimeDir(cfg.Storage.DataDir), "status.json")
}
func (s *SpeechService) incrementMissed(cfg *config.Config) error {
	return s.updateMetadata(cfg, func(metadata *speechMetadata) { metadata.MissedTurns++ })
}

func (s *SpeechService) updateMetadata(cfg *config.Config, update func(*speechMetadata)) error {
	path := s.metadataPath(cfg)
	var metadata speechMetadata
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &metadata)
	}
	update(&metadata)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(metadata)
	return writePrivateAtomic(path, data)
}

func writePrivateAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
