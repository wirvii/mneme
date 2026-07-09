package cli

import (
	"runtime/debug"
	"testing"
)

// TestResolveVersionFromBuildInfo covers SPEC-070 AC10/AC11: the build-info
// fallback only fires when Version is still the literal "dev" default, and it
// distinguishes a real `go install …@vX.Y.Z` build (Main.Version = "vX.Y.Z")
// from a local `go run`/`go build` without a tag (Main.Version = "(devel)").
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
			name:    "go install with a tag resolves the version, stripping the leading v",
			current: "dev",
			readBuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "v1.22.0"}}, true
			},
			want: "1.22.0",
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

// TestResolveVersionFromBuildInfo_RealBuildInfo exercises the real
// debug.ReadBuildInfo (not a stub) to guard against a signature mismatch
// between resolveVersionFromBuildInfo's parameter and the stdlib function it
// wraps in init(). Under `go test`, Main.Version is typically "(devel)" or
// empty, so the expected outcome is "dev" — the important thing is that the
// call succeeds without panicking.
func TestResolveVersionFromBuildInfo_RealBuildInfo(t *testing.T) {
	got := resolveVersionFromBuildInfo("dev", debug.ReadBuildInfo)
	if got == "" {
		t.Error("resolveVersionFromBuildInfo with real ReadBuildInfo returned an empty string")
	}
}
