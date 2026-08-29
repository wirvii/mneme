// Package service — this file installs, removes, and reports on the SDD
// mechanism's own git hooks (SPEC-131 D34/D56/D57): a marked block inside
// post-merge and post-checkout that runs `mneme sdd hooks run-import` in
// the background after every git pull/merge/checkout.
//
// This file owns its OWN small append/remove-marked-block implementation
// rather than calling internal/cli/hookblock.go's generalized functions
// (D56's own "un solo mecanismo para dos consumidores"): EnableSDDRepo and
// DisableSDDRepo (sdd_enable.go) are what actually install/remove these
// hooks as part of `--apply` (D57), and BOTH are service-layer functions.
// internal/cli/hookblock.go's two functions are unexported — only code
// inside package cli can call them — and service must never import cli
// (the dependency rule running backwards). The shape of the algorithm is
// deliberately the SAME as hookblock.go's (append: idempotent on the begin
// marker, shebang only on a fresh file, 0755; remove: span from begin to
// end marker inclusive, preserving everything else) because it is a
// well-tested shape, not because the code is shared — it cannot be.
// internal/cli/sdd_hooks.go's `mneme sdd hooks install|remove` subcommands
// are thin wrappers over InstallSDDHooks/RemoveSDDHooks below, the SAME
// "CLI renders, service does the work" pattern every other SDD command in
// this package already follows (EnableSDDRepo, ExportSDDRepo, SDDStatus).
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SDDHooksMarkerBegin and SDDHooksMarkerEnd are the SDD mechanism's OWN
// sentinel lines (D34) — deliberately DIFFERENT strings from team-memory's
// own markers (teamMemoryHooksMarkerBegin/End, internal/cli/team_memory_hooks.go),
// so `mneme sdd hooks remove` can never touch team-memory's block and vice
// versa, even when both live in the SAME hook file (SPEC-131 AC17).
const (
	SDDHooksMarkerBegin = "# >>> mneme sdd (SPEC-131) >>>"
	SDDHooksMarkerEnd   = "# <<< mneme sdd (SPEC-131) <<<"
)

// sddHooksManagedBlock is the exact content injected between the markers.
// Runs detached in background (trailing &) so the git operation that
// triggered the hook is never blocked or slowed by the import (D62) —
// mirrors team-memory's own managed block in shape.
const sddHooksManagedBlock = SDDHooksMarkerBegin + `
# Import the repository's SDD backlog/specs after this git event. Managed by ` + "`mneme sdd hooks`" + `.
# Skipped during rebase/merge/cherry-pick to avoid storms.
"$(command -v mneme || echo mneme)" sdd hooks run-import >/dev/null 2>&1 &
` + SDDHooksMarkerEnd

// SDDHooksTargetHooks is the list of git hook file names InstallSDDHooks/
// RemoveSDDHooks manage — the same two moments team-memory's own hooks
// fire on (SPEC-053 D4): post-merge (including a fast-forward pull) and
// post-checkout (branch switches).
var SDDHooksTargetHooks = []string{"post-merge", "post-checkout"}

// InstallSDDHooks appends the SDD-managed block to post-merge and
// post-checkout under repoRoot's git hooks directory (resolved via
// sddGit.HooksDir, D60 — never os.Getwd). Idempotent: a hook that already
// carries the marker is left untouched. New hook files are created with a
// "#!/bin/sh" shebang and 0755 permissions.
func (svc *SDDService) InstallSDDHooks(repoRoot string) error {
	if repoRoot == "" {
		return fmt.Errorf("service: install sdd hooks: repoRoot is required")
	}
	hooksDir, err := (sddGit{RepoDir: repoRoot}).HooksDir()
	if err != nil {
		return fmt.Errorf("service: install sdd hooks: %w", err)
	}
	for _, name := range SDDHooksTargetHooks {
		if err := appendSDDHookBlock(filepath.Join(hooksDir, name)); err != nil {
			return fmt.Errorf("service: install sdd hooks: %s: %w", name, err)
		}
	}
	return nil
}

// RemoveSDDHooks removes ONLY the SDD-managed block from post-merge and
// post-checkout under repoRoot's git hooks directory. Any other content in
// those files — including team-memory's own block, if both are installed
// (SPEC-131 AC17) — is left byte-for-byte untouched.
func (svc *SDDService) RemoveSDDHooks(repoRoot string) error {
	if repoRoot == "" {
		return fmt.Errorf("service: remove sdd hooks: repoRoot is required")
	}
	hooksDir, err := (sddGit{RepoDir: repoRoot}).HooksDir()
	if err != nil {
		return fmt.Errorf("service: remove sdd hooks: %w", err)
	}
	for _, name := range SDDHooksTargetHooks {
		if _, err := removeSDDHookBlock(filepath.Join(hooksDir, name)); err != nil {
			return fmt.Errorf("service: remove sdd hooks: %s: %w", name, err)
		}
	}
	return nil
}

// SDDHooksInstalled reports whether EVERY target hook under repoRoot's git
// hooks directory carries the SDD marker — the D54 "HooksInstalled" signal
// SDDStatus surfaces: a repository whose marker is committed (mechanism
// on, team-wide) but whose hooks were never installed locally imports
// nothing and, without this field, would look silently broken.
// Any failure to resolve the hooks directory (not a git repo, git absent)
// is reported as false, never propagated — status reporting must never
// hard-fail over this.
func (svc *SDDService) SDDHooksInstalled(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	hooksDir, err := (sddGit{RepoDir: repoRoot}).HooksDir()
	if err != nil {
		return false
	}
	for _, name := range SDDHooksTargetHooks {
		data, err := os.ReadFile(filepath.Join(hooksDir, name))
		if err != nil || !strings.Contains(string(data), SDDHooksMarkerBegin) {
			return false
		}
	}
	return true
}

// appendSDDHookBlock appends sddHooksManagedBlock to the hook file at
// hookPath. If the file does not exist it is created with a "#!/bin/sh"
// shebang. Idempotent: if SDDHooksMarkerBegin is already present the file
// is left untouched. The hook file is always left at 0755 (git requires
// hooks to be executable). Same shape as
// internal/cli/hookblock.go's appendMarkedHookBlock — see this file's own
// package godoc for why the code is not shared.
func appendSDDHookBlock(hookPath string) error {
	existing, readErr := os.ReadFile(hookPath)
	var content string
	if readErr == nil {
		content = string(existing)
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read hook file: %w", readErr)
	}

	if strings.Contains(content, SDDHooksMarkerBegin) {
		return nil
	}

	var sb strings.Builder
	if content == "" {
		sb.WriteString("#!/bin/sh\n")
	} else {
		sb.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(sddHooksManagedBlock)
	sb.WriteByte('\n')

	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks directory: %w", err)
	}
	if err := os.WriteFile(hookPath, []byte(sb.String()), 0o755); err != nil {
		return fmt.Errorf("write hook file: %w", err)
	}
	return os.Chmod(hookPath, 0o755)
}

// removeSDDHookBlock removes ONLY the region from SDDHooksMarkerBegin to
// SDDHooksMarkerEnd (inclusive) from the hook file at hookPath. All other
// content is preserved. Returns (true, nil) when the block was found and
// removed, (false, nil) when no SDD block was present (no-op). The file is
// never deleted, even if it becomes empty after removal.
func removeSDDHookBlock(hookPath string) (removed bool, err error) {
	data, readErr := os.ReadFile(hookPath)
	if os.IsNotExist(readErr) {
		return false, nil
	}
	if readErr != nil {
		return false, fmt.Errorf("read hook file: %w", readErr)
	}

	content := string(data)
	beginIdx := strings.Index(content, SDDHooksMarkerBegin)
	if beginIdx < 0 {
		return false, nil
	}

	endIdx := strings.Index(content, SDDHooksMarkerEnd)
	if endIdx < 0 {
		endIdx = len(content) - len(SDDHooksMarkerEnd)
	}
	afterEnd := endIdx + len(SDDHooksMarkerEnd)
	if afterEnd < len(content) && content[afterEnd] == '\n' {
		afterEnd++
	}

	newContent := content[:beginIdx] + content[afterEnd:]
	newContent = strings.TrimRight(newContent, "\n")
	if newContent != "" {
		newContent += "\n"
	}

	if writeErr := os.WriteFile(hookPath, []byte(newContent), 0o755); writeErr != nil {
		return false, fmt.Errorf("write hook file: %w", writeErr)
	}
	return true, nil
}
