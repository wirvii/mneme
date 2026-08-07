package speech

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrEngineNotInstalled = errors.New("speech: managed engine is not installed")

// EngineState is privacy-safe managed-engine installation metadata.
type EngineState struct {
	Engine   string `json:"engine"`
	Active   string `json:"active,omitempty"`
	Rollback string `json:"rollback,omitempty"`
	Ready    bool   `json:"ready"`
}

// FetchArtifact copies one catalog artifact into dst without interpreting it.
type FetchArtifact func(context.Context, Artifact, io.Writer) error

// Healthcheck verifies a completely staged generation before activation.
type Healthcheck func(context.Context, string, string, EngineRelease) error

// Manager installs immutable managed-engine generations transactionally.
type Manager struct {
	root        string
	fetch       FetchArtifact
	healthcheck Healthcheck
	now         func() time.Time
	freeSpace   func(string) (int64, error)
}

// LauncherHealthcheck asks the staged launcher to validate imports, model, and voice offline.
func LauncherHealthcheck(ctx context.Context, generation, modelDir string, release EngineRelease) error {
	launcher := filepath.Join(generation, LauncherName(release.GOOS))
	cmd := exec.CommandContext(ctx, launcher)
	cmd.Env = append(os.Environ(), "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "NO_PROXY=*")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("{\"action\":\"healthcheck\",\"voice\":%q,\"model\":%q}\n", release.Voice, modelDir))
	if err := cmd.Run(); err != nil {
		return errors.New("managed launcher healthcheck failed")
	}
	return nil
}

// NewManager constructs a manager with injected network and execution seams.
func NewManager(root string, fetch FetchArtifact, healthcheck Healthcheck) *Manager {
	return &Manager{root: root, fetch: fetch, healthcheck: healthcheck, now: time.Now, freeSpace: availableDiskBytes}
}

// Setup verifies a consented plan in private staging and activates it atomically.
func (m *Manager) Setup(ctx context.Context, plan SetupPlan, consent bool, digest string) error {
	return m.install(ctx, plan, consent, digest, false)
}

// Repair reinstalls the consented generation after validating its replacement.
func (m *Manager) Repair(ctx context.Context, plan SetupPlan, consent bool, digest string) error {
	return m.install(ctx, plan, consent, digest, true)
}

func (m *Manager) install(ctx context.Context, plan SetupPlan, consent bool, digest string, replace bool) error {
	if err := plan.ValidateConsent(consent, digest); err != nil {
		return err
	}
	if m.fetch == nil || m.healthcheck == nil {
		return errors.New("speech: manager requires fetch and healthcheck")
	}
	releaseLock, err := m.acquireLock()
	if err != nil {
		return err
	}
	defer releaseLock()
	available, err := m.freeSpace(m.root)
	if err != nil {
		return err
	}
	required := plan.FinalBytes + plan.TempBytes
	if available < required {
		return fmt.Errorf("%w: need %d bytes, have %d", ErrInsufficientDisk, required, available)
	}
	if err := os.MkdirAll(filepath.Join(m.root, "staging"), 0o700); err != nil {
		return fmt.Errorf("speech: create staging root: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Join(m.root, "staging"), "setup-")
	if err != nil {
		return fmt.Errorf("speech: create staging: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return fmt.Errorf("speech: secure staging: %w", err)
	}
	modelDir, err := m.prepareModel(ctx, plan, stage)
	if err != nil {
		return err
	}
	for _, artifact := range plan.Release.Artifacts {
		if artifact.Kind == "model" {
			continue
		}
		if err := m.fetchOne(ctx, stage, artifact); err != nil {
			return err
		}
	}
	manifest, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("speech: encode generation manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), manifest, 0o600); err != nil {
		return fmt.Errorf("speech: write generation manifest: %w", err)
	}
	if err := m.healthcheck(ctx, stage, modelDir, plan.Release); err != nil {
		return fmt.Errorf("speech: healthcheck staged generation: %w", err)
	}
	return m.activate(stage, plan, replace)
}

func (m *Manager) prepareModel(ctx context.Context, plan SetupPlan, stagingRoot string) (string, error) {
	var artifacts []Artifact
	for _, artifact := range plan.Release.Artifacts {
		if artifact.Kind == "model" {
			artifacts = append(artifacts, artifact)
		}
	}
	if len(artifacts) == 0 {
		return stagingRoot, nil
	}
	if plan.Release.ModelVersion == "" || !safeRelativeTarget(plan.Release.ModelVersion) {
		return "", errors.New("speech: managed model version is invalid")
	}
	modelDir := filepath.Join(m.root, "models", plan.Release.Engine, plan.Release.ModelVersion)
	if modelArtifactsValid(modelDir, artifacts) {
		return modelDir, nil
	}
	modelStage := filepath.Join(stagingRoot, ".model")
	if err := os.MkdirAll(modelStage, 0o700); err != nil {
		return "", fmt.Errorf("speech: create model staging: %w", err)
	}
	for _, artifact := range artifacts {
		if err := m.fetchOne(ctx, modelStage, artifact); err != nil {
			return "", err
		}
	}
	manifest, err := json.MarshalIndent(artifacts, "", "  ")
	if err != nil {
		return "", fmt.Errorf("speech: encode model manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(modelStage, "manifest.json"), manifest, 0o600); err != nil {
		return "", fmt.Errorf("speech: write model manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(modelDir), 0o700); err != nil {
		return "", fmt.Errorf("speech: create model store: %w", err)
	}
	_ = os.RemoveAll(modelDir)
	if err := os.Rename(modelStage, modelDir); err != nil {
		return "", fmt.Errorf("speech: commit managed model: %w", err)
	}
	return modelDir, nil
}

func modelArtifactsValid(modelDir string, artifacts []Artifact) bool {
	if _, err := os.Stat(filepath.Join(modelDir, "manifest.json")); err != nil {
		return false
	}
	for _, artifact := range artifacts {
		target := artifactTarget(artifact)
		file, err := os.Open(filepath.Join(modelDir, filepath.FromSlash(target)))
		if err != nil {
			return false
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
			return false
		}
	}
	return true
}

func (m *Manager) acquireLock() (func(), error) {
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return nil, fmt.Errorf("speech: create manager root: %w", err)
	}
	lock := filepath.Join(m.root, "manager.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("speech: acquire manager lock: %w", err)
		}
		info, statErr := os.Stat(lock)
		if statErr == nil && m.now().Sub(info.ModTime()) > 30*time.Minute {
			if removeErr := os.Remove(lock); removeErr == nil {
				return m.acquireLock()
			}
		}
		return nil, errors.New("speech: another engine operation is running")
	}
	return func() { _ = os.Remove(lock) }, nil
}

func (m *Manager) fetchOne(ctx context.Context, stage string, artifact Artifact) error {
	path := filepath.Join(stage, filepath.FromSlash(artifactTarget(artifact)))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("speech: create artifact directory %q: %w", artifact.Name, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("speech: create artifact %q: %w", artifact.Name, err)
	}
	hash := sha256.New()
	limited := &limitWriter{writer: io.MultiWriter(file, hash), remaining: artifact.Size}
	fetchErr := m.fetch(ctx, artifact, limited)
	closeErr := file.Close()
	if fetchErr != nil {
		return fmt.Errorf("speech: fetch artifact %q: %w", artifact.Name, fetchErr)
	}
	if closeErr != nil {
		return fmt.Errorf("speech: close artifact %q: %w", artifact.Name, closeErr)
	}
	if limited.remaining != 0 {
		return fmt.Errorf("speech: artifact %q size mismatch", artifact.Name)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		return fmt.Errorf("speech: artifact %q checksum mismatch", artifact.Name)
	}
	if artifact.Executable {
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("speech: mark artifact %q executable: %w", artifact.Name, err)
		}
	}
	return nil
}

func artifactTarget(artifact Artifact) string {
	if artifact.Target != "" {
		return artifact.Target
	}
	return artifact.Name
}

func (m *Manager) activate(stage string, plan SetupPlan, replace bool) error {
	engineRoot := filepath.Join(m.root, "engines", plan.Release.Engine)
	generations := filepath.Join(engineRoot, "generations")
	if err := os.MkdirAll(generations, 0o700); err != nil {
		return fmt.Errorf("speech: create generations: %w", err)
	}
	id := plan.Release.Version + "-" + plan.Digest[:12]
	destination := filepath.Join(generations, id)
	if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(stage, destination); err != nil {
			return fmt.Errorf("speech: commit generation: %w", err)
		}
	} else if err == nil && replace {
		backup := destination + ".repair-backup"
		_ = os.RemoveAll(backup)
		if err := os.Rename(destination, backup); err != nil {
			return fmt.Errorf("speech: preserve generation for repair: %w", err)
		}
		if err := os.Rename(stage, destination); err != nil {
			_ = os.Rename(backup, destination)
			return fmt.Errorf("speech: commit repaired generation: %w", err)
		}
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("speech: remove repaired generation backup: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("speech: inspect generation: %w", err)
	}
	active, _ := readPointer(engineRoot, "active")
	if active != "" && active != id {
		if err := writePointer(engineRoot, "rollback", active); err != nil {
			return err
		}
	}
	if err := writePointer(engineRoot, "active", id); err != nil {
		return err
	}
	return pruneGenerations(generations, id, active)
}

// Rollback swaps the active and rollback generation pointers.
func (m *Manager) Rollback(engine string) error {
	root := filepath.Join(m.root, "engines", engine)
	active, err := readPointer(root, "active")
	if err != nil {
		return err
	}
	rollback, err := readPointer(root, "rollback")
	if err != nil || rollback == "" {
		return ErrEngineNotInstalled
	}
	if err := writePointer(root, "active", rollback); err != nil {
		return err
	}
	return writePointer(root, "rollback", active)
}

// ActiveDir returns the verified active generation directory.
func (m *Manager) ActiveDir(engine string) (string, error) {
	root := filepath.Join(m.root, "engines", engine)
	id, err := readPointer(root, "active")
	if err != nil || id == "" {
		return "", ErrEngineNotInstalled
	}
	dir := filepath.Join(root, "generations", id)
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		return "", ErrEngineNotInstalled
	}
	return dir, nil
}

// ActiveModelDir returns the separate model directory referenced by the active generation.
func (m *Manager) ActiveModelDir(engine string) (string, error) {
	dir, err := m.ActiveDir(engine)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", ErrEngineNotInstalled
	}
	var plan SetupPlan
	if json.Unmarshal(data, &plan) != nil || plan.Release.ModelVersion == "" {
		return "", ErrEngineNotInstalled
	}
	modelDir := filepath.Join(m.root, "models", engine, plan.Release.ModelVersion)
	if _, err := os.Stat(filepath.Join(modelDir, "manifest.json")); err != nil {
		return "", ErrEngineNotInstalled
	}
	return modelDir, nil
}

// Status reports active and rollback generations without exposing paths or text.
func (m *Manager) Status(engine string) EngineState {
	root := filepath.Join(m.root, "engines", engine)
	active, _ := readPointer(root, "active")
	rollback, _ := readPointer(root, "rollback")
	state := EngineState{Engine: engine, Active: active, Rollback: rollback}
	if active != "" {
		_, err := os.Stat(filepath.Join(root, "generations", active, "manifest.json"))
		state.Ready = err == nil
	}
	return state
}

// Remove deletes an engine only when apply is explicit. Models are separate.
func (m *Manager) Remove(engine string, apply, removeModels bool) (EngineState, error) {
	before := m.Status(engine)
	if !apply {
		return before, nil
	}
	releaseLock, err := m.acquireLock()
	if err != nil {
		return before, err
	}
	defer releaseLock()
	if err := os.RemoveAll(filepath.Join(m.root, "engines", engine)); err != nil {
		return before, fmt.Errorf("speech: remove managed engine: %w", err)
	}
	if removeModels {
		if err := os.RemoveAll(filepath.Join(m.root, "models", engine)); err != nil {
			return before, fmt.Errorf("speech: remove managed engine models: %w", err)
		}
	}
	return before, nil
}

type limitWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("artifact exceeds declared size")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func writePointer(root, name, value string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("speech: create engine root: %w", err)
	}
	tmp := filepath.Join(root, "."+name+".tmp")
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("speech: write %s pointer: %w", name, err)
	}
	if err := os.Rename(tmp, filepath.Join(root, name)); err != nil {
		return fmt.Errorf("speech: activate %s pointer: %w", name, err)
	}
	return nil
}

func readPointer(root, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func pruneGenerations(root, keepA, keepB string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != keepA && entry.Name() != keepB {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
