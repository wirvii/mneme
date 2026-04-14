// personal.go implements the --personal ecosystem installation for mneme.
// It copies the user's personal Claude Code configuration (agents, commands,
// templates, hooks, CLAUDE.md, settings.json) from a local directory or a
// git repository into ~/.claude, without destroying existing configuration.
package install

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PersonalOpts configures the personal ecosystem installation.
type PersonalOpts struct {
	// Source is a git URL or local path to the ecosystem directory.
	// Accepted git URL forms: git@host:..., https://...*.git, ssh://...,
	// http://...*.git. Anything else is treated as a local filesystem path.
	Source string

	// ClaudeDir is the target directory (typically ~/.claude).
	ClaudeDir string

	// Force overwrites existing files when true. settings.json is always
	// merged regardless of this flag — it is never blindly overwritten.
	Force bool
}

// PersonalResult reports what happened during a personal ecosystem installation.
type PersonalResult struct {
	// Installed lists relative paths of files that were copied to ClaudeDir.
	Installed []string

	// Skipped lists relative paths of files that already existed and were not
	// overwritten because Force was false.
	Skipped []string

	// Merged is true if settings.json was found in the source and merged into
	// the target settings.json.
	Merged bool
}

// dirMapping defines which source subdirectories map to which target
// subdirectories inside ClaudeDir. The mapping is ordered for deterministic
// output in PersonalResult.
var dirMapping = []string{"agents", "commands", "templates", "hooks"}

// InstallPersonal copies the user's personal ecosystem from source into
// claudeDir. If source is a git URL, it is cloned to a temporary directory
// that is cleaned up before returning.
//
// The function is idempotent: running it twice with Force=false skips
// already-existing files. With Force=true, files are overwritten except
// settings.json, which is always merged (source keys are added to target only
// when the key is absent — the local target always wins on conflicts).
func InstallPersonal(opts PersonalOpts) (*PersonalResult, error) {
	if opts.Source == "" {
		return nil, fmt.Errorf("install: personal: source must not be empty")
	}

	sourceDir, cleanup, err := resolveSource(opts.Source)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	result := &PersonalResult{}

	// Copy mapped subdirectories.
	for _, dir := range dirMapping {
		src := filepath.Join(sourceDir, dir)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}
		dst := filepath.Join(opts.ClaudeDir, dir)
		installed, skipped, err := copyTree(src, dst, opts.Force)
		if err != nil {
			// Non-fatal: report and continue so a single bad file does not
			// abort the whole installation (same pattern as Install()).
			fmt.Fprintf(os.Stderr, "install: personal: copy %s: %v\n", dir, err)
		}
		// Prefix with the subdirectory name so paths are meaningful to the caller.
		for _, f := range installed {
			result.Installed = append(result.Installed, filepath.Join(dir, f))
		}
		for _, f := range skipped {
			result.Skipped = append(result.Skipped, filepath.Join(dir, f))
		}
	}

	// Copy CLAUDE.md if present in source root.
	srcCLAUDE := filepath.Join(sourceDir, "CLAUDE.md")
	if _, err := os.Stat(srcCLAUDE); err == nil {
		dstCLAUDE := filepath.Join(opts.ClaudeDir, "CLAUDE.md")
		installed, err := copyClaudeMD(srcCLAUDE, dstCLAUDE, opts.Force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "install: personal: copy CLAUDE.md: %v\n", err)
		} else if installed {
			result.Installed = append(result.Installed, "CLAUDE.md")
		} else {
			result.Skipped = append(result.Skipped, "CLAUDE.md")
		}
	}

	// Merge settings.json if present in source root.
	srcSettings := filepath.Join(sourceDir, "settings.json")
	if _, err := os.Stat(srcSettings); err == nil {
		dstSettings := filepath.Join(opts.ClaudeDir, "settings.json")
		if err := mergeSettingsJSON(srcSettings, dstSettings); err != nil {
			fmt.Fprintf(os.Stderr, "install: personal: merge settings.json: %v\n", err)
		} else {
			result.Merged = true
		}
	}

	return result, nil
}

// DryRunPersonal returns a human-readable description of what InstallPersonal
// would do without making any filesystem changes. If source is a git URL, the
// repository is cloned to a temp dir to enumerate files, then cleaned up.
func DryRunPersonal(opts PersonalOpts) (string, error) {
	if opts.Source == "" {
		return "", fmt.Errorf("install: personal: dry-run: source must not be empty")
	}

	sourceDir, cleanup, err := resolveSource(opts.Source)
	if err != nil {
		return "", err
	}
	if cleanup != nil {
		defer cleanup()
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Personal source: %s", opts.Source))
	lines = append(lines, fmt.Sprintf("Target:          %s", opts.ClaudeDir))
	lines = append(lines, "")

	for _, dir := range dirMapping {
		src := filepath.Join(sourceDir, dir)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}
		lines = append(lines, fmt.Sprintf("  [copy] %s/ → %s/", dir, filepath.Join(opts.ClaudeDir, dir)))
		_ = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(src, path)
			lines = append(lines, fmt.Sprintf("           %s", rel))
			return nil
		})
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "CLAUDE.md")); err == nil {
		lines = append(lines, fmt.Sprintf("  [copy] CLAUDE.md → %s", filepath.Join(opts.ClaudeDir, "CLAUDE.md")))
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "settings.json")); err == nil {
		lines = append(lines, fmt.Sprintf("  [merge] settings.json → %s", filepath.Join(opts.ClaudeDir, "settings.json")))
	}

	return strings.Join(lines, "\n"), nil
}

// resolveSource returns the directory to use as the ecosystem source.
// For git URLs it clones to a temp dir and returns a cleanup function.
// For local paths it validates the directory exists and returns nil cleanup.
func resolveSource(source string) (dir string, cleanup func(), err error) {
	if isGitURL(source) {
		tmpDir, err := cloneToTemp(source)
		if err != nil {
			return "", nil, err
		}
		return tmpDir, func() { _ = os.RemoveAll(tmpDir) }, nil
	}

	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("install: personal: source path does not exist: %s", source)
		}
		return "", nil, fmt.Errorf("install: personal: stat source: %w", err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("install: personal: source is not a directory: %s", source)
	}
	return source, nil, nil
}

// isGitURL returns true if source looks like a git remote URL.
// Recognised forms: git@host:path, ssh://host/path, https://host/repo.git,
// http://host/repo.git. Plain HTTPS URLs without the .git suffix are treated
// as local paths to avoid ambiguity.
func isGitURL(source string) bool {
	return strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "ssh://") ||
		(strings.HasPrefix(source, "https://") && strings.HasSuffix(source, ".git")) ||
		(strings.HasPrefix(source, "http://") && strings.HasSuffix(source, ".git"))
}

// cloneToTemp clones a git repository to a temporary directory and returns the
// path. The caller is responsible for removing it (typically via a deferred
// os.RemoveAll). Uses os/exec to invoke "git clone --depth=1 --single-branch".
// If the clone fails the temporary directory is removed before the error is
// returned.
func cloneToTemp(gitURL string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "mneme-personal-*")
	if err != nil {
		return "", fmt.Errorf("install: personal: create temp dir: %w", err)
	}

	// #nosec G204 — gitURL comes from user config, not from untrusted input.
	cmd := exec.Command("git", "clone", "--depth=1", "--single-branch", gitURL, tmpDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("install: personal: git clone %s: %w", gitURL, err)
	}
	return tmpDir, nil
}

// copyTree walks srcDir recursively and copies each file to the corresponding
// relative path under dstDir, creating parent directories as needed.
// If force is false, existing target files are skipped.
// Returns the lists of installed (copied) and skipped relative paths.
// An error from a single file is returned immediately; previous copies are
// already on disk.
func copyTree(srcDir, dstDir string, force bool) (installed, skipped []string, err error) {
	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("install: personal: walk %s: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("install: personal: rel path %s: %w", path, err)
		}

		dst := filepath.Join(dstDir, rel)

		if !force {
			if _, statErr := os.Stat(dst); statErr == nil {
				skipped = append(skipped, rel)
				return nil
			}
		}

		if err := copyFile(path, dst); err != nil {
			return err
		}
		installed = append(installed, rel)
		return nil
	})
	return installed, skipped, walkErr
}

// copyFile copies the file at src to dst, creating all parent directories.
// The destination file is created with permission 0o644.
func copyFile(src, dst string) (err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("install: personal: mkdir %s: %w", filepath.Dir(dst), err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("install: personal: open %s: %w", src, err)
	}
	defer in.Close() //nolint:errcheck // read-only file; Close error is not meaningful

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("install: personal: create %s: %w", dst, err)
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("install: personal: copy %s → %s: %w", src, dst, err)
	}
	return nil
}

// copyClaudeMD copies CLAUDE.md from srcFile to dstFile.
// If the target already exists and force is false, the copy is skipped.
// Special case: if the target has mneme protocol markers, and the copy
// proceeds (force=true or target is absent), the existing protocol block is
// re-injected into the new file so the mneme protocol is never lost.
// Returns true when the file was copied, false when it was skipped.
func copyClaudeMD(srcFile, dstFile string, force bool) (installed bool, err error) {
	const startMarker = "<!-- mneme:protocol:start -->"
	const endMarker = "<!-- mneme:protocol:end -->"

	// Read the current destination (may not exist).
	existing, readErr := os.ReadFile(dstFile)

	dstExists := readErr == nil
	if !dstExists && !os.IsNotExist(readErr) {
		return false, fmt.Errorf("install: personal: read %s: %w", dstFile, readErr)
	}

	// Skip if destination exists and we are not forcing.
	if dstExists && !force {
		return false, nil
	}

	// Extract the existing protocol block before overwriting, so it can be
	// re-injected after the source CLAUDE.md is copied.
	var protocolBlock []byte
	if dstExists {
		text := string(existing)
		startIdx := strings.Index(text, startMarker)
		endIdx := strings.Index(text, endMarker)
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			protocolBlock = []byte(text[startIdx : endIdx+len(endMarker)])
		}
	}

	// Copy source to destination.
	if err := copyFile(srcFile, dstFile); err != nil {
		return false, err
	}

	// Re-inject protocol block if the old destination had one.
	if len(protocolBlock) > 0 {
		newContent, readErr := os.ReadFile(dstFile)
		if readErr != nil {
			return true, fmt.Errorf("install: personal: read %s after copy: %w", dstFile, readErr)
		}
		merged := mergeProtocol(newContent, protocolBlock, startMarker, endMarker)
		if writeErr := os.WriteFile(dstFile, merged, 0o644); writeErr != nil {
			return true, fmt.Errorf("install: personal: re-inject protocol %s: %w", dstFile, writeErr)
		}
	}

	return true, nil
}

// mergeSettingsJSON reads the source settings.json and performs a shallow merge
// into the target settings.json: keys from source are added to target only when
// the key does not already exist in target (destination always wins on conflicts).
// If source does not exist the function is a no-op. If destination does not
// exist, it is created with the source content.
//
// The merge is intentionally shallow (top-level keys only) because settings.json
// keys in Claude Code are independent top-level objects. A deep merge would risk
// blending local hook configuration with source hook configuration — the user's
// local settings must always take absolute precedence.
func mergeSettingsJSON(srcPath, dstPath string) error {
	srcData, err := os.ReadFile(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // source absent — nothing to merge
		}
		return fmt.Errorf("install: personal: merge settings.json: read src: %w", err)
	}

	var src map[string]any
	if err := json.Unmarshal(srcData, &src); err != nil {
		return fmt.Errorf("install: personal: merge settings.json: parse src: %w", err)
	}

	// Load destination, or start from an empty map if it does not exist yet.
	dst := map[string]any{}
	dstData, err := os.ReadFile(dstPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("install: personal: merge settings.json: read dst: %w", err)
	}
	if len(dstData) > 0 {
		if err := json.Unmarshal(dstData, &dst); err != nil {
			return fmt.Errorf("install: personal: merge settings.json: parse dst: %w", err)
		}
	}

	// Shallow merge: source provides defaults for absent keys.
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}

	out, err := json.MarshalIndent(dst, "", "  ")
	if err != nil {
		return fmt.Errorf("install: personal: merge settings.json: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("install: personal: merge settings.json: mkdir: %w", err)
	}
	if err := os.WriteFile(dstPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("install: personal: merge settings.json: write: %w", err)
	}
	return nil
}
