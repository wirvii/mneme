package cli

import (
	"runtime/debug"
	"testing"
)

// TestResolveVersionFromBuildInfo covers SPEC-070 AC10/AC11: the build-info
// fallback only fires when Version is still the literal "dev" default, and it
// distinguishes a real `go install …@vX.Y.Z` build (Main.Version = a clean
// semver tag) from anything Go's automatic VCS stamping produces for a local
// build from within the module's own working tree — "(devel)", empty, a
// pseudo-version, or either of those with "+dirty" build metadata. All of
// those must stay "dev": letting a pseudo-version or a dirty build through
// would defeat runUpgrade's `Version == "dev"` guard (see root.go and
// upgrade_test.go's TestRunUpgrade_RefusesPseudoVersionAndDirtyBuilds).
func TestResolveVersionFromBuildInfo(t *testing.T) {
	tests := []struct {
		name          string
		current       string
		readBuildInfo func() (*debug.BuildInfo, bool)
		want          string
	}{
		{
			name:    "ldflags already set a real version — build info never consulted",
			current: "1.21.0",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				t.Fatal("readBuildInfo must not be called when current != \"dev\"")
				return nil, false
			},
			want: "1.21.0",
		},
		{
			name:    "go install with a clean tag resolves the version, stripping the leading v",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "v1.22.0"}}, true
			},
			want: "1.22.0",
		},
		{
			name:    "go install with a clean prerelease tag resolves, stripping the leading v",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "v1.22.0-rc.1"}}, true
			},
			want: "1.22.0-rc.1",
		},
		{
			name:    "go run/go build without a tag reports (devel) — stays dev",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
			},
			want: "dev",
		},
		{
			name:    "empty Main.Version — stays dev",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: ""}}, true
			},
			want: "dev",
		},
		{
			name:    "local build from the module's own git checkout: VCS pseudo-version — stays dev",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "v1.21.1-0.20260709120000-abcdef123456"}}, true
			},
			want: "dev",
		},
		{
			name:    "local build, dirty working tree: pseudo-version + build metadata — stays dev",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "v1.21.1-0.20260709120000-abcdef123456+dirty"}}, true
			},
			want: "dev",
		},
		{
			name:    "checked-out exactly at a tag but with local uncommitted changes: tag + dirty — stays dev",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "v1.22.0+dirty"}}, true
			},
			want: "dev",
		},
		{
			name:    "ReadBuildInfo reports ok=false — stays dev",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return nil, false
			},
			want: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVersionFromBuildInfo(tt.current, tt.readBuildInfo)
			if got != tt.want {
				t.Errorf("resolveVersionFromBuildInfo(%q) = %q, want %q", tt.current, got, tt.want)
			}
		})
	}
}

// TestIsCleanSemverTag exercises isCleanSemverTag directly against every case
// called out by SPEC-070's QA rejection: only a plain "vX.Y.Z" or
// "vX.Y.Z-prerelease" tag is clean; "(devel)", empty strings, "+dirty" build
// metadata, and Go module pseudo-versions (with or without "+dirty") are all
// rejected.
func TestIsCleanSemverTag(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"", false},
		{"(devel)", false},
		{"v1.21.1-0.20260709120000-abcdef123456", false},
		{"v1.21.1-0.20260709120000-abcdef123456+dirty", false},
		{"v1.22.0+dirty", false},
		{"v1.0.0-20260709120000-abcdef123456", false}, // no-earlier-tag pseudo-version form
		{"v1.22.0", true},
		{"v1.22.0-rc.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := isCleanSemverTag(tt.version); got != tt.want {
				t.Errorf("isCleanSemverTag(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

// TestResolveVersionFromBuildInfo_RealBuildInfo exercises the real
// debug.ReadBuildInfo (not a stub) to guard against a signature mismatch
// between resolveVersionFromBuildInfo's parameter and the stdlib function it
// wraps in init(). `go test` binaries report Main.Version = "(devel)"
// (verified: `go test -c` + `go version -m` on the resulting binary), so the
// expected outcome here is deterministically "dev".
func TestResolveVersionFromBuildInfo_RealBuildInfo(t *testing.T) {
	got := resolveVersionFromBuildInfo("dev", debug.ReadBuildInfo)
	if got != "dev" {
		t.Errorf("resolveVersionFromBuildInfo with real ReadBuildInfo = %q, want \"dev\" (go test binaries report Main.Version=\"(devel)\")", got)
	}
}
