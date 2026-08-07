package speech

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestManagedReleasesFromReleaseLinkerPayload(t *testing.T) {
	originalJSON, originalBase64 := ManagedCatalogJSON, ManagedCatalogBase64
	t.Cleanup(func() { ManagedCatalogJSON, ManagedCatalogBase64 = originalJSON, originalBase64 })
	payload, err := json.Marshal([]EngineRelease{validRelease()})
	if err != nil {
		t.Fatal(err)
	}
	ManagedCatalogJSON = ""
	ManagedCatalogBase64 = base64.StdEncoding.EncodeToString(payload)
	releases, err := ManagedReleases()
	if err != nil || len(releases) != 1 || releases[0].Engine != "kokoro" {
		t.Fatalf("releases=%v err=%v", releases, err)
	}
}

func TestManagedReleasesRejectsMissingAndMalformedCatalog(t *testing.T) {
	originalJSON, originalBase64 := ManagedCatalogJSON, ManagedCatalogBase64
	t.Cleanup(func() { ManagedCatalogJSON, ManagedCatalogBase64 = originalJSON, originalBase64 })
	for _, test := range []struct{ json, encoded string }{{}, {encoded: "%%%"}, {json: "{"}, {json: `[{"engine":"kokoro"}]`}} {
		ManagedCatalogJSON, ManagedCatalogBase64 = test.json, test.encoded
		if _, err := ManagedReleases(); err == nil {
			t.Fatalf("catalog json=%q encoded=%q accepted", test.json, test.encoded)
		}
	}
}

func TestHostKokoroReleaseMatchesOnlyCurrentTarget(t *testing.T) {
	match := validRelease()
	match.GOOS, match.GOARCH = runtime.GOOS, runtime.GOARCH
	wrong := match
	wrong.Engine = "other"
	release, err := HostKokoroRelease([]EngineRelease{wrong, match})
	if err != nil || release.GOOS != runtime.GOOS || release.GOARCH != runtime.GOARCH {
		t.Fatalf("release=%+v err=%v", release, err)
	}
	if _, err := HostKokoroRelease([]EngineRelease{wrong}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err=%v", err)
	}
}

func TestSetupPlanDigestIsDeterministic(t *testing.T) {
	release := validRelease()
	release.Artifacts = append(release.Artifacts, Artifact{Name: "a-model", URL: "https://example.test/model", SHA256: digestA, Size: 20, License: "Apache-2.0", Kind: "model"})
	first, err := NewSetupPlan(release)
	if err != nil {
		t.Fatal(err)
	}
	release.Artifacts[0], release.Artifacts[1] = release.Artifacts[1], release.Artifacts[0]
	second, err := NewSetupPlan(release)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.FinalBytes != 30 || first.TempBytes != 30 {
		t.Fatalf("plans differ: %#v %#v", first, second)
	}
}

func TestSetupPlanConsent(t *testing.T) {
	plan, err := NewSetupPlan(validRelease())
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(plan.ValidateConsent(false, ""), ErrSetupRequired) {
		t.Fatal("missing consent should require setup")
	}
	if !errors.Is(plan.ValidateConsent(true, "stale"), ErrPlanChanged) {
		t.Fatal("stale digest should be rejected")
	}
	if err := plan.ValidateConsent(true, plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestSetupPlanRejectsUnsafeArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Artifact)
	}{
		{name: "path traversal", mutate: func(a *Artifact) { a.Name = "../runtime" }},
		{name: "http", mutate: func(a *Artifact) { a.URL = "http://example.test/runtime" }},
		{name: "credentials", mutate: func(a *Artifact) { a.URL = "https://secret@example.test/runtime" }},
		{name: "bad hash", mutate: func(a *Artifact) { a.SHA256 = "bad" }},
		{name: "no size", mutate: func(a *Artifact) { a.Size = 0 }},
		{name: "unsafe target", mutate: func(a *Artifact) { a.Target = "voices/../outside" }},
		{name: "duplicate", mutate: func(a *Artifact) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := validRelease()
			tt.mutate(&release.Artifacts[0])
			if tt.name == "duplicate" {
				release.Artifacts = append(release.Artifacts, release.Artifacts[0])
			}
			if _, err := NewSetupPlan(release); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSafeRelativeTarget(t *testing.T) {
	for _, target := range []string{"", "/absolute", ".", "a/../b", "a//b", `a\..\b`} {
		if safeRelativeTarget(target) {
			t.Fatalf("unsafe target accepted: %q", target)
		}
	}
	if !safeRelativeTarget(strings.Join([]string{"voices", "ef_dora.safetensors"}, "/")) {
		t.Fatal("safe target rejected")
	}
}

const digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validRelease() EngineRelease {
	return EngineRelease{Engine: "kokoro", Version: "1.0.0", GOOS: "linux", GOARCH: "amd64", Backend: "pytorch-cpu", Voice: "ef_dora", Artifacts: []Artifact{{Name: "runtime", URL: "https://example.test/runtime", SHA256: digestA, Size: 10, License: "Apache-2.0", Kind: "runtime", Executable: true}}}
}
