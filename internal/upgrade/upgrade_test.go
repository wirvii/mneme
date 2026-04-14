package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- CompareVersions ---------------------------------------------------------

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b string
		want int
	}{
		{"0.3.0", "0.3.0", 0},
		{"0.4.0", "0.3.0", 1},
		{"0.3.0", "0.4.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"dev", "0.1.0", -1},
		{"0.1.0", "dev", 1},
		{"dev", "dev", 0},
		{"1.0", "1.0.0", 0},  // missing patch treated as 0
		{"1.0.1", "1.0", 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%s_vs_%s", tc.a, tc.b), func(t *testing.T) {
			t.Parallel()
			got := CompareVersions(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ---- VerifyChecksum ----------------------------------------------------------

func TestVerifyChecksum(t *testing.T) {
	t.Parallel()

	// Create a temp file with known content.
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.tar.gz")
	content := []byte("hello mneme")
	if err := os.WriteFile(archivePath, content, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	// Compute the correct hash.
	h := sha256.Sum256(content)
	correctHash := fmt.Sprintf("%x", h)

	cases := []struct {
		name     string
		checksum []byte
		wantErr  bool
	}{
		{
			name:     "correct checksum",
			checksum: []byte(correctHash + "  test.tar.gz\n"),
			wantErr:  false,
		},
		{
			name:     "wrong hash",
			checksum: []byte("0000000000000000000000000000000000000000000000000000000000000000  test.tar.gz\n"),
			wantErr:  true,
		},
		{
			name:     "empty checksum file",
			checksum: []byte(""),
			wantErr:  true,
		},
		{
			name:     "invalid hash length",
			checksum: []byte("deadbeef  test.tar.gz\n"),
			wantErr:  true,
		},
		{
			name:     "no filename field",
			checksum: []byte(correctHash + "\n"),
			wantErr:  false, // only first field is used
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := VerifyChecksum(archivePath, tc.checksum)
			if (err != nil) != tc.wantErr {
				t.Errorf("VerifyChecksum() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// ---- Checker.Check -----------------------------------------------------------

func TestChecker_Check(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		serverResponse string
		statusCode     int
		currentVersion string
		wantUpdateAvail bool
		wantLatestVer  string
		wantErr        bool
	}{
		{
			name:           "update available",
			serverResponse: `{"tag_name": "v0.4.0"}`,
			statusCode:     http.StatusOK,
			currentVersion: "0.3.0",
			wantUpdateAvail: true,
			wantLatestVer:  "0.4.0",
		},
		{
			name:           "already up to date",
			serverResponse: `{"tag_name": "v0.4.0"}`,
			statusCode:     http.StatusOK,
			currentVersion: "0.4.0",
			wantUpdateAvail: false,
			wantLatestVer:  "0.4.0",
		},
		{
			name:           "dev build",
			serverResponse: `{"tag_name": "v0.1.0"}`,
			statusCode:     http.StatusOK,
			currentVersion: "dev",
			wantUpdateAvail: true,
			wantLatestVer:  "0.1.0",
		},
		{
			name:           "server error",
			serverResponse: ``,
			statusCode:     http.StatusInternalServerError,
			currentVersion: "0.3.0",
			wantErr:        true,
		},
		{
			name:           "invalid JSON",
			serverResponse: `not json`,
			statusCode:     http.StatusOK,
			currentVersion: "0.3.0",
			wantErr:        true,
		},
		{
			name:           "empty tag_name",
			serverResponse: `{"tag_name": ""}`,
			statusCode:     http.StatusOK,
			currentVersion: "0.3.0",
			wantErr:        true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				fmt.Fprint(w, tc.serverResponse)
			}))
			defer srv.Close()

			checker := &Checker{
				Repo:       "wirvii/mneme",
				HTTPClient: newTestClient(srv.URL),
			}

			result, err := checker.Check(tc.currentVersion)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.UpdateAvail != tc.wantUpdateAvail {
				t.Errorf("UpdateAvail = %v, want %v", result.UpdateAvail, tc.wantUpdateAvail)
			}
			if result.Latest.Version != tc.wantLatestVer {
				t.Errorf("Latest.Version = %q, want %q", result.Latest.Version, tc.wantLatestVer)
			}
			if result.Current != tc.currentVersion {
				t.Errorf("Current = %q, want %q", result.Current, tc.currentVersion)
			}
		})
	}
}

// ---- Upgrader.Upgrade --------------------------------------------------------

func TestUpgrader_Upgrade(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Build a real tar.gz containing a fake "mneme" binary.
	fakeBinaryContent := []byte("#!/bin/sh\necho mneme v0.4.0")
	archiveBytes := buildTarGz(t, "mneme", fakeBinaryContent)

	// Compute SHA256 of the archive.
	h := sha256.Sum256(archiveBytes)
	checksumLine := fmt.Sprintf("%x  mneme-0.4.0-linux-amd64.tar.gz\n", h)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			fmt.Fprint(w, checksumLine)
		} else {
			w.Write(archiveBytes)
		}
	}))
	defer srv.Close()

	// Create a fake "current" binary that will be replaced.
	binaryPath := filepath.Join(dir, "mneme")
	if err := os.WriteFile(binaryPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("write old binary: %v", err)
	}

	upgrader := &Upgrader{
		Repo:       "wirvii/mneme",
		HTTPClient: newTestClient(srv.URL),
	}

	release := Release{TagName: "v0.4.0", Version: "0.4.0"}
	if err := upgrader.Upgrade(release, binaryPath, io.Discard); err != nil {
		t.Fatalf("Upgrade() error: %v", err)
	}

	// Verify the binary was replaced.
	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read new binary: %v", err)
	}
	if !bytes.Equal(got, fakeBinaryContent) {
		t.Errorf("binary content = %q, want %q", got, fakeBinaryContent)
	}

	// Verify permissions.
	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatalf("stat new binary: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("binary is not executable: mode %o", info.Mode().Perm())
	}
}

// ---- atomicReplace -----------------------------------------------------------

func TestAtomicReplace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, []byte("new content"), 0o755); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old content"), 0o644); err != nil {
		t.Fatalf("write dst: %v", err)
	}

	if err := atomicReplace(src, dst); err != nil {
		t.Fatalf("atomicReplace() error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "new content" {
		t.Errorf("dst content = %q, want %q", got, "new content")
	}

	// src should be gone after rename.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still exists after rename")
	}
}

// ---- DetectInstalledAgents ---------------------------------------------------

func TestDetectInstalledAgents(t *testing.T) {
	// t.Setenv requires sequential execution — no t.Parallel here.

	cases := []struct {
		name       string
		claudeJSON string
		want       []string
	}{
		{
			name: "claude-code installed",
			claudeJSON: `{
				"mcpServers": {
					"mneme": {"command": "/usr/local/bin/mneme", "args": ["mcp"]}
				}
			}`,
			want: []string{"claude-code"},
		},
		{
			name:       "no mcpServers",
			claudeJSON: `{"theme": "dark"}`,
			want:       nil,
		},
		{
			name: "mcpServers without mneme",
			claudeJSON: `{
				"mcpServers": {
					"other": {"command": "other"}
				}
			}`,
			want: nil,
		},
		{
			name:       "malformed JSON",
			claudeJSON: `not json`,
			want:       nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv requires sequential execution — no t.Parallel here.

			// Override HOME to a temp directory so we don't touch real config.
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)

			claudeJSONPath := filepath.Join(tmpHome, ".claude.json")
			if err := os.WriteFile(claudeJSONPath, []byte(tc.claudeJSON), 0o644); err != nil {
				t.Fatalf("write .claude.json: %v", err)
			}

			got, err := DetectInstalledAgents()
			if err != nil {
				t.Fatalf("DetectInstalledAgents() error: %v", err)
			}

			if len(got) != len(tc.want) {
				t.Errorf("DetectInstalledAgents() = %v, want %v", got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("DetectInstalledAgents()[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestDetectInstalledAgents_NoFile(t *testing.T) {
	// t.Setenv requires sequential execution — no t.Parallel here.

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	got, err := DetectInstalledAgents()
	if err != nil {
		t.Fatalf("expected nil error when file absent, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice when file absent, got: %v", got)
	}
}

// ---- helpers -----------------------------------------------------------------

// testRoundTripper rewrites request URLs to point to a test server base URL.
type testRoundTripper struct {
	baseURL string
}

func (rt *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace scheme+host with test server URL, keep path+query.
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = req.URL.Host
	// Replace the host with the test server host.
	srv := rt.baseURL[len("http://"):]
	req2.URL.Host = srv
	return http.DefaultTransport.RoundTrip(req2)
}

// newTestClient returns an HTTPClient that routes all requests to serverURL,
// preserving the original path and query. This lets the Checker/Upgrader use
// their real URL-building logic while hitting a local test server.
func newTestClient(serverURL string) HTTPClient {
	return &http.Client{
		Transport: &testRoundTripper{baseURL: serverURL},
	}
}

// buildTarGz creates an in-memory .tar.gz archive containing a single file
// named entryName with the given content.
func buildTarGz(t *testing.T, entryName string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: entryName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar write header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

