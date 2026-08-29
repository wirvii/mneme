package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/service"
)

func TestSDDHooksInstall_FreshRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	if _, _, err := runSDDCmd(t, dir, "hooks", "install"); err != nil {
		t.Fatalf("sdd hooks install: %v", err)
	}

	hooksDir, hErr := gitHooksDir(dir)
	if hErr != nil {
		t.Fatalf("gitHooksDir: %v", hErr)
	}

	for _, hookName := range service.SDDHooksTargetHooks {
		hookPath := filepath.Join(hooksDir, hookName)
		if !hookFileExists(t, hookPath) {
			t.Errorf("%s: expected executable hook file, not found", hookName)
		}
		content := readHookFile(t, hookPath)
		if !strings.HasPrefix(content, "#!/bin/sh") {
			t.Errorf("%s: expected #!/bin/sh shebang, got: %q", hookName, content[:min(30, len(content))])
		}
		if !strings.Contains(content, service.SDDHooksMarkerBegin) {
			t.Errorf("%s: missing begin marker", hookName)
		}
		if !strings.Contains(content, service.SDDHooksMarkerEnd) {
			t.Errorf("%s: missing end marker", hookName)
		}
		if !strings.Contains(content, "run-import") {
			t.Errorf("%s: expected run-import invocation", hookName)
		}
	}
}

func TestSDDHooksInstall_Idempotent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	for i := 0; i < 2; i++ {
		if _, _, err := runSDDCmd(t, dir, "hooks", "install"); err != nil {
			t.Fatalf("sdd hooks install round %d: %v", i+1, err)
		}
	}

	hooksDir, _ := gitHooksDir(dir)
	for _, hookName := range service.SDDHooksTargetHooks {
		content := readHookFile(t, filepath.Join(hooksDir, hookName))
		count := strings.Count(content, service.SDDHooksMarkerBegin)
		if count != 1 {
			t.Errorf("%s: expected 1 begin-marker occurrence, got %d", hookName, count)
		}
	}
}

// TestSDDHooks_CoexistWithTeamMemoryBlock is SPEC-131 AC17: installing
// BOTH team-memory's and the SDD's own hooks into the SAME hook file
// leaves both blocks intact; removing the SDD block leaves team-memory's
// block byte-for-byte untouched, and vice versa; installing twice never
// duplicates; pre-existing foreign content survives all of it.
func TestSDDHooks_CoexistWithTeamMemoryBlock(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	hooksDir, hErr := gitHooksDir(dir)
	if hErr != nil {
		t.Fatalf("gitHooksDir: %v", hErr)
	}
	hookPath := filepath.Join(hooksDir, "post-merge")

	foreign := "#!/bin/sh\necho 'a teammate wrote this'\n"
	if err := os.WriteFile(hookPath, []byte(foreign), 0o755); err != nil {
		t.Fatalf("write foreign hook content: %v", err)
	}

	if _, _, err := runTeamMemoryHooksCmd(t, dir, "install"); err != nil {
		t.Fatalf("team-memory hooks install: %v", err)
	}
	if _, _, err := runSDDCmd(t, dir, "hooks", "install"); err != nil {
		t.Fatalf("sdd hooks install: %v", err)
	}

	afterBothInstalled := readHookFile(t, hookPath)
	for _, want := range []string{
		"echo 'a teammate wrote this'",
		teamMemoryHooksMarkerBegin, teamMemoryHooksMarkerEnd,
		service.SDDHooksMarkerBegin, service.SDDHooksMarkerEnd,
	} {
		if !strings.Contains(afterBothInstalled, want) {
			t.Fatalf("post-merge is missing %q after both installs:\n%s", want, afterBothInstalled)
		}
	}

	teamMemoryBlockBefore := extractBlock(t, afterBothInstalled, teamMemoryHooksMarkerBegin, teamMemoryHooksMarkerEnd)

	if _, _, err := runSDDCmd(t, dir, "hooks", "remove"); err != nil {
		t.Fatalf("sdd hooks remove: %v", err)
	}
	afterSDDRemoved := readHookFile(t, hookPath)
	if strings.Contains(afterSDDRemoved, service.SDDHooksMarkerBegin) {
		t.Errorf("SDD block still present after sdd hooks remove:\n%s", afterSDDRemoved)
	}
	teamMemoryBlockAfter := extractBlock(t, afterSDDRemoved, teamMemoryHooksMarkerBegin, teamMemoryHooksMarkerEnd)
	if teamMemoryBlockBefore != teamMemoryBlockAfter {
		t.Errorf("team-memory's block changed after `sdd hooks remove`:\nbefore=%q\nafter=%q",
			teamMemoryBlockBefore, teamMemoryBlockAfter)
	}
	if !strings.Contains(afterSDDRemoved, "echo 'a teammate wrote this'") {
		t.Errorf("foreign content lost after sdd hooks remove:\n%s", afterSDDRemoved)
	}

	// Reinstall the SDD block, then remove team-memory's — the reverse
	// direction must be equally safe.
	if _, _, err := runSDDCmd(t, dir, "hooks", "install"); err != nil {
		t.Fatalf("sdd hooks install (2nd): %v", err)
	}
	sddBlockBefore := extractBlock(t, readHookFile(t, hookPath), service.SDDHooksMarkerBegin, service.SDDHooksMarkerEnd)

	if _, _, err := runTeamMemoryHooksCmd(t, dir, "remove"); err != nil {
		t.Fatalf("team-memory hooks remove: %v", err)
	}
	afterTeamMemoryRemoved := readHookFile(t, hookPath)
	if strings.Contains(afterTeamMemoryRemoved, teamMemoryHooksMarkerBegin) {
		t.Errorf("team-memory block still present after team-memory hooks remove:\n%s", afterTeamMemoryRemoved)
	}
	sddBlockAfter := extractBlock(t, afterTeamMemoryRemoved, service.SDDHooksMarkerBegin, service.SDDHooksMarkerEnd)
	if sddBlockBefore != sddBlockAfter {
		t.Errorf("SDD's block changed after `team-memory hooks remove`:\nbefore=%q\nafter=%q",
			sddBlockBefore, sddBlockAfter)
	}
	if !strings.Contains(afterTeamMemoryRemoved, "echo 'a teammate wrote this'") {
		t.Errorf("foreign content lost after team-memory hooks remove:\n%s", afterTeamMemoryRemoved)
	}
}

// extractBlock returns the substring from begin to end (inclusive),
// failing the test if either marker is absent.
func extractBlock(t *testing.T, content, begin, end string) string {
	t.Helper()
	bi := strings.Index(content, begin)
	if bi < 0 {
		t.Fatalf("begin marker %q not found in:\n%s", begin, content)
	}
	ei := strings.Index(content, end)
	if ei < 0 {
		t.Fatalf("end marker %q not found in:\n%s", end, content)
	}
	return content[bi : ei+len(end)]
}

func TestSDDHooksRunImport_Hidden(t *testing.T) {
	cmd := newSDDHooksRunImportCmd()
	if !cmd.Hidden {
		t.Error("run-import must be Hidden")
	}
}

// TestSDDHooksInstallRemove_NotAGitRepoPropagatesError is a targeted
// addition (QA rejection fix): `sdd hooks install`/`sdd hooks remove`'s
// own error-propagation branch — initSDDService() itself succeeds even
// outside a git repository (project detection failure there is silently
// ignored, falling back to the global database), so the ONLY way this
// branch fires in production is exactly what it is here: a real repo
// whose git hooks directory cannot be resolved.
func TestSDDHooksInstallRemove_NotAGitRepoPropagatesError(t *testing.T) {
	dir := t.TempDir() // deliberately NOT a git repository
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	if _, stderr, err := runSDDCmd(t, dir, "hooks", "install"); err == nil {
		t.Fatalf("sdd hooks install must fail outside a git repository (stderr=%s)", stderr)
	}
	if _, stderr, err := runSDDCmd(t, dir, "hooks", "remove"); err == nil {
		t.Fatalf("sdd hooks remove must fail outside a git repository (stderr=%s)", stderr)
	}
}

// Mutacion exigida (AC17): hacer que service.SDDHooksMarkerBegin/End sean iguales
// a teamMemoryHooksMarkerBegin/End pone en rojo
// TestSDDHooks_CoexistWithTeamMemoryBlock, porque `sdd hooks remove` se
// lleva por delante el bloque de team-memory (las dos comparaciones de
// "antes"/"despues" del bloque ajeno dejan de coincidir). Ejecutada y
// revertida durante la implementacion; resultado real en changes.md.
