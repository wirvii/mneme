// Package upgrade provides functionality for checking and applying mneme
// binary upgrades from GitHub Releases. It handles version comparison,
// release detection, archive download, checksum verification, and atomic
// binary replacement.
//
// All network access goes through an injectable HTTPClient interface so that
// callers can substitute a test server without touching the real GitHub API.
package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"
	"github.com/wirvii/mneme/internal/codexhome"
)

// HTTPClient is the minimal interface required for HTTP GET requests.
// *http.Client satisfies this interface, making it easy to swap in a test
// server via httptest without wrapping the standard library.
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

// Release represents a GitHub release with the fields needed to perform an
// upgrade: the full tag name (e.g. "v0.4.0") and the version string without
// the "v" prefix (e.g. "0.4.0").
type Release struct {
	// TagName is the raw Git tag, e.g. "v0.4.0".
	TagName string

	// Version is the semver string without the leading "v", e.g. "0.4.0".
	Version string
}

// CheckResult holds the outcome of comparing the running binary's version
// against the latest available release.
type CheckResult struct {
	// Current is the version string of the running binary (e.g. "0.3.0").
	Current string

	// Latest is the most recent release found on GitHub.
	Latest Release

	// UpdateAvail is true when Latest.Version is strictly greater than Current.
	UpdateAvail bool
}

// Checker queries GitHub Releases to discover whether a newer version of
// mneme is available.
type Checker struct {
	// Repo is the GitHub repository in "owner/repo" format (e.g. "wirvii/mneme").
	Repo string

	// HTTPClient is the HTTP client used for the GitHub API request.
	// When nil, http.DefaultClient is used.
	HTTPClient HTTPClient
}

// httpClient returns the configured client, falling back to http.DefaultClient.
func (c *Checker) httpClient() HTTPClient {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// Check queries the GitHub Releases API for the latest release and compares
// its version against currentVersion. currentVersion must be a semver string
// without a leading "v" (e.g. "0.3.0") or the special value "dev".
//
// GitHub API endpoint:
//
//	GET https://api.github.com/repos/{owner}/{repo}/releases/latest
func (c *Checker) Check(currentVersion string) (*CheckResult, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.Repo)

	resp, err := c.httpClient().Get(url)
	if err != nil {
		return nil, fmt.Errorf("upgrade: check: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upgrade: check: GitHub API returned status %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("upgrade: check: decode response: %w", err)
	}

	if payload.TagName == "" {
		return nil, fmt.Errorf("upgrade: check: GitHub API returned empty tag_name")
	}

	latest := Release{
		TagName: payload.TagName,
		Version: strings.TrimPrefix(payload.TagName, "v"),
	}

	return &CheckResult{
		Current:     currentVersion,
		Latest:      latest,
		UpdateAvail: CompareVersions(latest.Version, currentVersion) > 0,
	}, nil
}

// Upgrader downloads and atomically replaces the running binary with a newer
// release from GitHub Releases.
type Upgrader struct {
	// Repo is the GitHub repository in "owner/repo" format.
	Repo string

	// HTTPClient is the HTTP client used for downloads.
	// When nil, http.DefaultClient is used.
	HTTPClient HTTPClient
}

// httpClient returns the configured client, falling back to http.DefaultClient.
func (u *Upgrader) httpClient() HTTPClient {
	if u.HTTPClient != nil {
		return u.HTTPClient
	}
	return http.DefaultClient
}

// Upgrade downloads the release archive for the given version, verifies its
// SHA256 checksum, extracts the binary, and atomically replaces the file at
// binaryPath. The original binary is untouched until the final rename succeeds.
//
// Progress messages are written to the provided writer w (typically os.Stdout).
// Pass io.Discard to suppress output.
//
// The archive URL format matches the release pipeline output:
//
//	https://github.com/{repo}/releases/download/v{VERSION}/mneme-{VERSION}-{GOOS}-{GOARCH}.tar.gz
func (u *Upgrader) Upgrade(release Release, binaryPath string, w io.Writer) error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	archiveName := fmt.Sprintf("mneme-%s-%s-%s.tar.gz", release.Version, goos, goarch)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", u.Repo, release.TagName)
	archiveURL := base + "/" + archiveName
	checksumURL := archiveURL + ".sha256"

	// Use the same directory as the binary for temp files so that the final
	// os.Rename is always within the same filesystem (guaranteeing atomicity).
	binaryDir := filepath.Dir(binaryPath)

	// Download archive to a temp file.
	archiveTmp, err := os.CreateTemp(binaryDir, "mneme-upgrade-*.tar.gz")
	if err != nil {
		return fmt.Errorf("upgrade: create temp archive: %w", err)
	}
	archiveTmpPath := archiveTmp.Name()
	defer os.Remove(archiveTmpPath)

	if err := u.download(archiveURL, archiveTmp); err != nil {
		archiveTmp.Close()
		return fmt.Errorf("upgrade: download archive: %w", err)
	}
	archiveTmp.Close()
	fmt.Fprintf(w, "  [ok] Downloaded %s\n", archiveName)

	// Download checksum.
	checksumBytes, err := u.downloadBytes(checksumURL)
	if err != nil {
		return fmt.Errorf("upgrade: download checksum: %w", err)
	}

	// Verify checksum.
	if err := VerifyChecksum(archiveTmpPath, checksumBytes); err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	fmt.Fprintf(w, "  [ok] Checksum verified\n")

	// Extract binary from archive.
	newBinaryTmp, err := os.CreateTemp(binaryDir, "mneme-upgrade-bin-*")
	if err != nil {
		return fmt.Errorf("upgrade: create temp binary: %w", err)
	}
	newBinaryTmpPath := newBinaryTmp.Name()
	defer os.Remove(newBinaryTmpPath)

	if err := extractBinary(archiveTmpPath, "mneme", newBinaryTmp); err != nil {
		newBinaryTmp.Close()
		return fmt.Errorf("upgrade: extract binary: %w", err)
	}
	newBinaryTmp.Close()

	if err := os.Chmod(newBinaryTmpPath, 0o755); err != nil {
		return fmt.Errorf("upgrade: chmod binary: %w", err)
	}

	// Atomic replace.
	if err := atomicReplace(newBinaryTmpPath, binaryPath); err != nil {
		return fmt.Errorf("upgrade: replace binary: %w", err)
	}
	fmt.Fprintf(w, "  [ok] Binary replaced\n")

	return nil
}

// download fetches the resource at url and writes it to dst.
func (u *Upgrader) download(url string, dst io.Writer) error {
	resp, err := u.httpClient().Get(url)
	if err != nil {
		return fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}

	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("copy response body: %w", err)
	}
	return nil
}

// downloadBytes fetches the resource at url and returns its body as bytes.
func (u *Upgrader) downloadBytes(url string) ([]byte, error) {
	resp, err := u.httpClient().Get(url)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return data, nil
}

// extractBinary extracts a single file named entryName from the .tar.gz archive
// at archivePath and writes its content to dst.
func extractBinary(archivePath, entryName string, dst io.Writer) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Name == entryName || filepath.Base(hdr.Name) == entryName {
			if _, err := io.Copy(dst, tr); err != nil {
				return fmt.Errorf("copy entry: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("%q not found in archive", entryName)
}

// VerifyChecksum reads the SHA256 checksum file content (output of sha256sum:
// "<hash>  <filename>\n"), computes the SHA256 of archivePath, and returns nil
// if they match or an error describing the mismatch.
func VerifyChecksum(archivePath string, checksumContent []byte) error {
	// Parse the expected hash: first whitespace-delimited field.
	line := strings.TrimSpace(string(checksumContent))
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return fmt.Errorf("upgrade: verify checksum: empty checksum file")
	}
	expected := strings.ToLower(fields[0])
	if len(expected) != 64 {
		return fmt.Errorf("upgrade: verify checksum: unexpected hash length %d (want 64)", len(expected))
	}

	// Compute actual hash.
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("upgrade: verify checksum: open archive: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("upgrade: verify checksum: hash archive: %w", err)
	}
	actual := fmt.Sprintf("%x", h.Sum(nil))

	if actual != expected {
		return fmt.Errorf("upgrade: verify checksum: mismatch (expected %s, got %s)", expected, actual)
	}
	return nil
}

// atomicReplace replaces dst with the file at src. Both paths must be on the
// same filesystem for the rename to be atomic. When the kernel returns EXDEV
// (cross-device link), the function falls back to a byte-copy followed by a
// rename of a sibling temp file.
func atomicReplace(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	// Check for cross-device link error (EXDEV).
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || !errors.Is(linkErr.Err, syscall.EXDEV) {
		return fmt.Errorf("rename: %w", err)
	}

	// Fallback: copy bytes to a temp file in dst's directory, then rename.
	dstDir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dstDir, "mneme-replace-*")
	if err != nil {
		return fmt.Errorf("cross-device fallback: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	srcF, err := os.Open(src)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("cross-device fallback: open src: %w", err)
	}
	defer srcF.Close()

	if _, err := io.Copy(tmp, srcF); err != nil {
		tmp.Close()
		return fmt.Errorf("cross-device fallback: copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cross-device fallback: close temp: %w", err)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("cross-device fallback: chmod: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("cross-device fallback: rename: %w", err)
	}
	return nil
}

// CompareVersions compares two semver strings without a leading "v".
// It returns -1 if a < b, 0 if a == b, and 1 if a > b.
//
// The special value "dev" is always considered older than any release version.
// Both strings are expected to follow MAJOR.MINOR.PATCH; extra segments are
// compared component by component and missing segments are treated as 0.
func CompareVersions(a, b string) int {
	if a == "dev" && b == "dev" {
		return 0
	}
	if a == "dev" {
		return -1
	}
	if b == "dev" {
		return 1
	}

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(aParts) {
			av, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bv, _ = strconv.Atoi(bParts[i])
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// DetectInstalledAgents returns the slugs of agents that have mneme
// configured, resolving HOME and delegating to detectAgentsInHome for the
// actual per-agent checks. It is the ONLY function in this pair that can
// fail — and only if HOME itself cannot be resolved — because
// detectAgentsInHome (SPEC-106 D7) treats every other failure mode
// (missing file, unreadable file, malformed content) as "this agent is not
// installed", not as an upgrade-aborting error: `mneme upgrade` must never
// stop re-provisioning agent B just because agent A's config happens to be
// unreadable.
//
// Two criteria, evaluated independently (DD15):
//   - "claude-code": <home>/.claude.json has a top-level mcpServers.mneme entry.
//   - "codex": <home>/.codex/config.toml has a [mcp_servers.mneme] table.
//
// Returned in the fixed order ["claude-code", "codex"] when both are present.
func DetectInstalledAgents() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("upgrade: detect agents: home dir: %w", err)
	}
	return detectAgentsInHome(home), nil
}

// detectAgentsInHome is the pure, injectable core of agent detection
// (SPEC-106 DD13): it takes home directly instead of calling
// os.UserHomeDir(), and never returns an error — so tests can drive every
// combination of present/absent/malformed config files via t.TempDir()
// without ever touching HOME or a real filesystem path (SPEC-085).
//
// Each candidate agent is evaluated in isolation (DD14): a problem with ONE
// agent's config file (absent, unreadable, malformed) only rules out that
// agent, never the others. The return order is always
// ["claude-code", "codex"] when both are detected — deterministic, so
// callers (postUpgradeHooks) re-provision agents in a stable sequence.
func detectAgentsInHome(home string) []string {
	var agents []string
	if hasClaudeCodeMCPEntry(home) {
		agents = append(agents, "claude-code")
	}
	if hasCodexMCPEntry(home) {
		agents = append(agents, "codex")
	}
	return agents
}

// hasClaudeCodeMCPEntry reports whether <home>/.claude.json declares a
// top-level mcpServers.mneme entry — the same file and key `mneme install
// claude-code` itself writes to (WriteMCPConfig). Any read or parse failure
// (missing file, unreadable, malformed JSON) is treated as "not installed",
// never propagated as an error — this mirrors the pre-SPEC-106 behaviour for
// malformed JSON ("don't fail the whole upgrade") and extends the same
// tolerance to a missing/unreadable file, which used to abort ALL detection
// (see DetectInstalledAgents' old %w on os.ReadFile) instead of just this one
// agent.
func hasClaudeCodeMCPEntry(home string) bool {
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return false
	}

	var root struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return false
	}
	_, ok := root.MCPServers["mneme"]
	return ok
}

// hasCodexMCPEntry reports whether <home>/.codex/config.toml declares a
// [mcp_servers.mneme] table — the exact path install.Codex() writes to
// (WriteCodexConfig), deliberately NOT $CODEX_HOME (DD16: "detect where I
// wrote"; honouring CODEX_HOME would require reworking the installer as a
// whole, out of scope here). Symmetric with hasClaudeCodeMCPEntry: presence
// of the table is the criterion (DD15) — `enabled=false` or a missing
// `[mcp_servers.mneme.tools]` sub-table are NOT interpreted, and any read or
// parse failure is "not installed", never an error.
func hasCodexMCPEntry(home string) bool {
	data, err := os.ReadFile(filepath.Join(codexhome.Resolve(home), "config.toml"))
	if err != nil {
		return false
	}

	var root struct {
		MCPServers map[string]any `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(data, &root); err != nil {
		return false
	}
	_, ok := root.MCPServers["mneme"]
	return ok
}
