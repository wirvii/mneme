package speech

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"sort"
	"strings"
)

var (
	// ErrSetupRequired means the selected managed engine is not ready locally.
	ErrSetupRequired = errors.New("speech: managed engine setup required")
	// ErrPlanChanged means consent was issued for a different setup plan.
	ErrPlanChanged = errors.New("speech: setup plan changed")
	// ErrUnsupportedPlatform means no managed engine release exists for this host.
	ErrUnsupportedPlatform = errors.New("speech: managed engine unsupported on this platform")
)

// Artifact describes one immutable file needed by a managed speech engine.
type Artifact struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	License    string `json:"license"`
	Kind       string `json:"kind"`
	Target     string `json:"target,omitempty"`
	Executable bool   `json:"executable,omitempty"`
}

// EngineRelease pins the runtime and model artifacts for one host platform.
type EngineRelease struct {
	Engine       string     `json:"engine"`
	Version      string     `json:"version"`
	GOOS         string     `json:"goos"`
	GOARCH       string     `json:"goarch"`
	Backend      string     `json:"backend"`
	Voice        string     `json:"voice"`
	ModelVersion string     `json:"model_version,omitempty"`
	Artifacts    []Artifact `json:"artifacts"`
}

// SetupPlan is the complete immutable download plan shown before consent.
type SetupPlan struct {
	Release    EngineRelease `json:"release"`
	FinalBytes int64         `json:"final_bytes"`
	TempBytes  int64         `json:"temp_bytes"`
	Digest     string        `json:"digest"`
}

// ManagedCatalogJSON is populated by release builds with the verified sidecar
// catalog. Development builds leave it empty and therefore never download.
var ManagedCatalogJSON string

// ManagedCatalogBase64 is set by release builds to avoid shell-sensitive JSON
// in linker flags. ManagedCatalogJSON remains available for tests and tooling.
var ManagedCatalogBase64 string

// ManagedReleases decodes and validates the catalog embedded by the release pipeline.
func ManagedReleases() ([]EngineRelease, error) {
	payload := strings.TrimSpace(ManagedCatalogJSON)
	if payload == "" && strings.TrimSpace(ManagedCatalogBase64) != "" {
		decoded, err := base64.StdEncoding.DecodeString(ManagedCatalogBase64)
		if err != nil {
			return nil, fmt.Errorf("speech: decode managed engine catalog: %w", err)
		}
		payload = string(decoded)
	}
	if payload == "" {
		return nil, ErrUnsupportedPlatform
	}
	var releases []EngineRelease
	if err := json.Unmarshal([]byte(payload), &releases); err != nil {
		return nil, fmt.Errorf("speech: parse managed engine catalog: %w", err)
	}
	for _, release := range releases {
		if err := validateRelease(release); err != nil {
			return nil, err
		}
	}
	return releases, nil
}

// NewSetupPlan validates a release and calculates its deterministic consent digest.
func NewSetupPlan(release EngineRelease) (SetupPlan, error) {
	if err := validateRelease(release); err != nil {
		return SetupPlan{}, err
	}
	sort.Slice(release.Artifacts, func(i, j int) bool { return release.Artifacts[i].Name < release.Artifacts[j].Name })
	plan := SetupPlan{Release: release}
	for _, artifact := range release.Artifacts {
		plan.FinalBytes += artifact.Size
	}
	plan.TempBytes = plan.FinalBytes
	payload, err := json.Marshal(plan)
	if err != nil {
		return SetupPlan{}, fmt.Errorf("speech: encode setup plan: %w", err)
	}
	digest := sha256.Sum256(payload)
	plan.Digest = hex.EncodeToString(digest[:])
	return plan, nil
}

// ValidateConsent ensures explicit consent applies to this exact plan.
func (p SetupPlan) ValidateConsent(consent bool, digest string) error {
	if !consent {
		return ErrSetupRequired
	}
	if digest == "" || digest != p.Digest {
		return ErrPlanChanged
	}
	return nil
}

// HostKokoroRelease returns the catalog entry for a supported host.
func HostKokoroRelease(releases []EngineRelease) (EngineRelease, error) {
	for _, release := range releases {
		if release.Engine == "kokoro" && release.GOOS == runtime.GOOS && release.GOARCH == runtime.GOARCH {
			return release, nil
		}
	}
	return EngineRelease{}, ErrUnsupportedPlatform
}

func validateRelease(release EngineRelease) error {
	if release.Engine == "" || release.Version == "" || release.GOOS == "" || release.GOARCH == "" || release.Backend == "" || release.Voice == "" {
		return errors.New("speech: incomplete engine release")
	}
	if len(release.Artifacts) == 0 {
		return errors.New("speech: engine release has no artifacts")
	}
	seen := make(map[string]struct{}, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
		if artifact.Name == "" || strings.Contains(artifact.Name, "..") || strings.ContainsAny(artifact.Name, `/\\`) {
			return fmt.Errorf("speech: invalid artifact name %q", artifact.Name)
		}
		if artifact.Target != "" && !safeRelativeTarget(artifact.Target) {
			return fmt.Errorf("speech: artifact %q has invalid target", artifact.Name)
		}
		if _, exists := seen[artifact.Name]; exists {
			return fmt.Errorf("speech: duplicate artifact name %q", artifact.Name)
		}
		seen[artifact.Name] = struct{}{}
		parsed, err := url.Parse(artifact.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("speech: artifact %q requires an authenticated-free HTTPS URL", artifact.Name)
		}
		if artifact.Size <= 0 || artifact.License == "" || artifact.Kind == "" {
			return fmt.Errorf("speech: artifact %q has incomplete metadata", artifact.Name)
		}
		decoded, err := hex.DecodeString(artifact.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("speech: artifact %q has invalid SHA-256", artifact.Name)
		}
	}
	return nil
}

func safeRelativeTarget(target string) bool {
	target = strings.ReplaceAll(target, "\\", "/")
	if target == "" || strings.HasPrefix(target, "/") {
		return false
	}
	for _, part := range strings.Split(target, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
