package speech

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

func TestLauncherHealthcheckUsesStdinAndOfflineProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	generation := t.TempDir()
	launcher := filepath.Join(generation, LauncherName(runtime.GOOS))
	script := "#!/bin/sh\ninput=$(cat)\ncase \"$input\" in *healthcheck*ef_dora*) exit 0;; *) exit 9;; esac\n"
	if err := os.WriteFile(launcher, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	release := EngineRelease{GOOS: runtime.GOOS, Voice: "ef_dora"}
	if err := LauncherHealthcheck(context.Background(), generation, t.TempDir(), release); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := LauncherHealthcheck(context.Background(), generation, t.TempDir(), release); err == nil {
		t.Fatal("failing launcher accepted")
	}
}

func TestManagerSetupActivatesAndRollsBack(t *testing.T) {
	root := t.TempDir()
	contents := map[string]string{"runtime": "first", "model": "model"}
	manager := testManager(root, contents, nil)
	first := testPlan(t, "1", contents)
	if err := manager.Setup(context.Background(), first, true, first.Digest); err != nil {
		t.Fatal(err)
	}
	firstDir, err := manager.ActiveDir("kokoro")
	if err != nil {
		t.Fatal(err)
	}

	contents = map[string]string{"runtime": "second", "model": "model"}
	manager = testManager(root, contents, nil)
	second := testPlan(t, "2", contents)
	if err := manager.Setup(context.Background(), second, true, second.Digest); err != nil {
		t.Fatal(err)
	}
	secondDir, _ := manager.ActiveDir("kokoro")
	if firstDir == secondDir {
		t.Fatal("generation did not change")
	}
	if err := manager.Rollback("kokoro"); err != nil {
		t.Fatal(err)
	}
	active, _ := manager.ActiveDir("kokoro")
	if active != firstDir {
		t.Fatalf("active = %q, want %q", active, firstDir)
	}
}

func TestManagerFailurePreservesActiveGeneration(t *testing.T) {
	root := t.TempDir()
	contents := map[string]string{"runtime": "first", "model": "model"}
	manager := testManager(root, contents, nil)
	first := testPlan(t, "1", contents)
	if err := manager.Setup(context.Background(), first, true, first.Digest); err != nil {
		t.Fatal(err)
	}
	want, _ := manager.ActiveDir("kokoro")

	contents = map[string]string{"runtime": "second", "model": "model"}
	manager = testManager(root, contents, errors.New("broken runtime"))
	second := testPlan(t, "2", contents)
	if err := manager.Setup(context.Background(), second, true, second.Digest); err == nil {
		t.Fatal("expected healthcheck failure")
	}
	got, _ := manager.ActiveDir("kokoro")
	if got != want {
		t.Fatalf("active changed after failure: %q", got)
	}
	entries, err := os.ReadDir(filepath.Join(root, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging leaked: %v", entries)
	}
}

func TestManagerRepairReplacesOnlyAfterHealthcheck(t *testing.T) {
	root := t.TempDir()
	contents := map[string]string{"runtime": "verified", "model": "model"}
	plan := testPlan(t, "1", contents)
	manager := testManager(root, contents, nil)
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err != nil {
		t.Fatal(err)
	}
	active, err := manager.ActiveDir("kokoro")
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(active, "runtime")
	if err := os.WriteFile(runtimePath, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := testManager(root, contents, errors.New("healthcheck failed"))
	if err := broken.Repair(context.Background(), plan, true, plan.Digest); err == nil {
		t.Fatal("expected failed repair")
	}
	if got, err := os.ReadFile(runtimePath); err != nil || string(got) != "damaged" {
		t.Fatalf("failed repair changed active runtime: %q err=%v", got, err)
	}
	if err := manager.Repair(context.Background(), plan, true, plan.Digest); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(runtimePath); err != nil || string(got) != "verified" {
		t.Fatalf("repair runtime=%q err=%v", got, err)
	}
}

func TestManagerRejectsOversizedArtifact(t *testing.T) {
	contents := map[string]string{"runtime": "too-long", "model": "model"}
	plan := testPlan(t, "1", map[string]string{"runtime": "x", "model": "model"})
	manager := testManager(t.TempDir(), contents, nil)
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err == nil {
		t.Fatal("expected size error")
	}
}

func TestManagerLifecycleMissingAndInvalidDependencies(t *testing.T) {
	root := t.TempDir()
	plan := testPlan(t, "1", map[string]string{"runtime": "runtime"})
	manager := NewManager(root, nil, nil)
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err == nil {
		t.Fatal("manager without dependencies accepted")
	}
	if err := manager.Rollback("kokoro"); !errors.Is(err, ErrEngineNotInstalled) {
		t.Fatalf("rollback err=%v", err)
	}
	if _, err := manager.ActiveDir("kokoro"); !errors.Is(err, ErrEngineNotInstalled) {
		t.Fatalf("active dir err=%v", err)
	}
	if _, err := manager.ActiveModelDir("kokoro"); !errors.Is(err, ErrEngineNotInstalled) {
		t.Fatalf("model dir err=%v", err)
	}
	if _, err := manager.Remove("kokoro", true, true); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRejectsInvalidModelVersionAndCorruptManifest(t *testing.T) {
	root := t.TempDir()
	contents := map[string]string{"runtime": "runtime", "model": "model"}
	plan := testPlan(t, "1", contents)
	plan.Release.ModelVersion = "../outside"
	for index := range plan.Release.Artifacts {
		if plan.Release.Artifacts[index].Name == "model" {
			plan.Release.Artifacts[index].Kind = "model"
		}
	}
	manager := testManager(root, contents, nil)
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err == nil {
		t.Fatal("invalid model version accepted")
	}
	plan = testPlan(t, "2", map[string]string{"runtime": "runtime"})
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err != nil {
		t.Fatal(err)
	}
	active, err := manager.ActiveDir("kokoro")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "manifest.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ActiveModelDir("kokoro"); !errors.Is(err, ErrEngineNotInstalled) {
		t.Fatalf("corrupt manifest err=%v", err)
	}
}

func TestManagerRejectsCollidingArtifactTargets(t *testing.T) {
	contents := map[string]string{"parent": "file", "child": "nested"}
	release := validRelease()
	release.Artifacts = nil
	for _, item := range []struct{ name, target string }{{"parent", "shared"}, {"child", "shared/child"}} {
		sum := sha256.Sum256([]byte(contents[item.name]))
		release.Artifacts = append(release.Artifacts, Artifact{Name: item.name, Target: item.target, URL: "https://example.test/" + item.name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(contents[item.name])), License: "Apache-2.0", Kind: "runtime"})
	}
	plan, err := NewSetupPlan(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := testManager(t.TempDir(), contents, nil).Setup(context.Background(), plan, true, plan.Digest); err == nil {
		t.Fatal("colliding artifact targets accepted")
	}
}

func TestManagerLockAndRemoveLifecycle(t *testing.T) {
	root := t.TempDir()
	contents := map[string]string{"runtime": "first", "model": "model"}
	manager := testManager(root, contents, nil)
	plan := testPlan(t, "1", contents)
	if err := os.MkdirAll(filepath.Join(root, "manager.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err == nil {
		t.Fatal("concurrent setup should be rejected")
	}
	if err := os.Remove(filepath.Join(root, "manager.lock")); err != nil {
		t.Fatal(err)
	}
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err != nil {
		t.Fatal(err)
	}
	if state := manager.Status("kokoro"); !state.Ready || state.Active == "" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if _, err := manager.Remove("kokoro", false, false); err != nil {
		t.Fatal(err)
	}
	if !manager.Status("kokoro").Ready {
		t.Fatal("dry-run removed engine")
	}
	if _, err := manager.Remove("kokoro", true, false); err != nil {
		t.Fatal(err)
	}
	if manager.Status("kokoro").Ready {
		t.Fatal("applied remove retained engine")
	}
}

func TestManagerReusesVerifiedSeparateModel(t *testing.T) {
	root := t.TempDir()
	contents := map[string]string{"launcher": "runtime", "weights": "weights", "voice": "dora"}
	var modelFetches atomic.Int32
	fetch := func(_ context.Context, artifact Artifact, dst io.Writer) error {
		if artifact.Kind == "model" {
			modelFetches.Add(1)
		}
		_, err := io.WriteString(dst, contents[artifact.Name])
		return err
	}
	health := func(_ context.Context, _, modelDir string, _ EngineRelease) error {
		for _, path := range []string{"weights", filepath.Join("voices", "ef_dora")} {
			if _, err := os.Stat(filepath.Join(modelDir, path)); err != nil {
				return err
			}
		}
		return nil
	}
	manager := NewManager(root, fetch, health)
	for _, version := range []string{"1", "2"} {
		release := EngineRelease{Engine: "kokoro", Version: version, ModelVersion: "mlx-a71e4d", GOOS: "darwin", GOARCH: "arm64", Backend: "mlx", Voice: "ef_dora"}
		for _, item := range []struct{ name, kind, target string }{{"launcher", "runtime", "launcher"}, {"weights", "model", "weights"}, {"voice", "model", "voices/ef_dora"}} {
			sum := sha256.Sum256([]byte(contents[item.name]))
			release.Artifacts = append(release.Artifacts, Artifact{Name: item.name, Target: item.target, Kind: item.kind, URL: "https://example.test/" + item.name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(contents[item.name])), License: "Apache-2.0", Executable: item.kind == "runtime"})
		}
		plan, err := NewSetupPlan(release)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Setup(context.Background(), plan, true, plan.Digest); err != nil {
			t.Fatal(err)
		}
	}
	if got := modelFetches.Load(); got != 2 {
		t.Fatalf("model fetches=%d, want 2 from first setup only", got)
	}
	modelDir, err := manager.ActiveModelDir("kokoro")
	if err != nil || filepath.Base(modelDir) != "mlx-a71e4d" {
		t.Fatalf("modelDir=%q err=%v", modelDir, err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "weights"), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := EngineRelease{Engine: "kokoro", Version: "3", ModelVersion: "mlx-a71e4d", GOOS: "darwin", GOARCH: "arm64", Backend: "mlx", Voice: "ef_dora"}
	for _, item := range []struct{ name, kind, target string }{{"launcher", "runtime", "launcher"}, {"weights", "model", "weights"}, {"voice", "model", "voices/ef_dora"}} {
		sum := sha256.Sum256([]byte(contents[item.name]))
		release.Artifacts = append(release.Artifacts, Artifact{Name: item.name, Target: item.target, Kind: item.kind, URL: "https://example.test/" + item.name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(contents[item.name])), License: "Apache-2.0", Executable: item.kind == "runtime"})
	}
	plan, _ := NewSetupPlan(release)
	if err := manager.Setup(context.Background(), plan, true, plan.Digest); err != nil {
		t.Fatal(err)
	}
	if got := modelFetches.Load(); got != 4 {
		t.Fatalf("model fetches after corruption=%d, want 4", got)
	}
}

func TestManagerRejectsInsufficientDiskBeforeFetch(t *testing.T) {
	contents := map[string]string{"runtime": "runtime"}
	var fetched atomic.Bool
	manager := testManager(t.TempDir(), contents, nil)
	manager.fetch = func(context.Context, Artifact, io.Writer) error { fetched.Store(true); return nil }
	manager.freeSpace = func(string) (int64, error) { return 1, nil }
	plan := testPlan(t, "1", contents)
	err := manager.Setup(context.Background(), plan, true, plan.Digest)
	if !errors.Is(err, ErrInsufficientDisk) || fetched.Load() {
		t.Fatalf("err=%v fetched=%t", err, fetched.Load())
	}
}

func testManager(root string, contents map[string]string, healthErr error) *Manager {
	fetch := func(_ context.Context, artifact Artifact, dst io.Writer) error {
		_, err := io.WriteString(dst, contents[artifact.Name])
		return err
	}
	health := func(context.Context, string, string, EngineRelease) error { return healthErr }
	return NewManager(root, fetch, health)
}

func testPlan(t *testing.T, version string, contents map[string]string) SetupPlan {
	t.Helper()
	artifacts := make([]Artifact, 0, len(contents))
	for name, content := range contents {
		sum := sha256.Sum256([]byte(content))
		artifacts = append(artifacts, Artifact{Name: name, URL: "https://example.test/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)), License: "Apache-2.0", Kind: "runtime", Executable: name == "runtime"})
	}
	plan, err := NewSetupPlan(EngineRelease{Engine: "kokoro", Version: version, GOOS: "linux", GOARCH: "amd64", Backend: "pytorch-cpu", Voice: "ef_dora", Artifacts: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
