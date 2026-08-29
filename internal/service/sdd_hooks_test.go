// Package service — tests for InstallSDDHooks/RemoveSDDHooks/
// SDDHooksInstalled (SPEC-131 §2b commit 8).
package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSDDHooks_WritesBothHooksWithMarker(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")

	if err := svc.InstallSDDHooks(repoDir); err != nil {
		t.Fatalf("InstallSDDHooks: %v", err)
	}

	hooksDir, err := (sddGit{RepoDir: repoDir}).HooksDir()
	if err != nil {
		t.Fatalf("HooksDir: %v", err)
	}
	for _, name := range SDDHooksTargetHooks {
		data, rErr := os.ReadFile(filepath.Join(hooksDir, name))
		if rErr != nil {
			t.Fatalf("read %s: %v", name, rErr)
		}
		if !strings.Contains(string(data), SDDHooksMarkerBegin) {
			t.Errorf("%s: missing begin marker", name)
		}
		if !strings.HasPrefix(string(data), "#!/bin/sh") {
			t.Errorf("%s: missing shebang", name)
		}
	}

	if !svc.SDDHooksInstalled(repoDir) {
		t.Error("SDDHooksInstalled = false after InstallSDDHooks, want true")
	}
}

func TestInstallSDDHooks_Idempotent(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")

	if err := svc.InstallSDDHooks(repoDir); err != nil {
		t.Fatalf("InstallSDDHooks (1st): %v", err)
	}
	if err := svc.InstallSDDHooks(repoDir); err != nil {
		t.Fatalf("InstallSDDHooks (2nd): %v", err)
	}

	hooksDir, _ := (sddGit{RepoDir: repoDir}).HooksDir()
	data, err := os.ReadFile(filepath.Join(hooksDir, "post-merge"))
	if err != nil {
		t.Fatalf("read post-merge: %v", err)
	}
	if n := strings.Count(string(data), SDDHooksMarkerBegin); n != 1 {
		t.Errorf("begin marker occurrences = %d, want 1", n)
	}
}

func TestRemoveSDDHooks_LeavesForeignContentAlone(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")

	hooksDir, err := (sddGit{RepoDir: repoDir}).HooksDir()
	if err != nil {
		t.Fatalf("HooksDir: %v", err)
	}
	hookPath := filepath.Join(hooksDir, "post-merge")
	foreign := "#!/bin/sh\necho foreign\n"
	if wErr := os.WriteFile(hookPath, []byte(foreign), 0o755); wErr != nil {
		t.Fatalf("write foreign content: %v", wErr)
	}

	if err := svc.InstallSDDHooks(repoDir); err != nil {
		t.Fatalf("InstallSDDHooks: %v", err)
	}
	if err := svc.RemoveSDDHooks(repoDir); err != nil {
		t.Fatalf("RemoveSDDHooks: %v", err)
	}

	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read post-merge: %v", err)
	}
	if strings.Contains(string(data), SDDHooksMarkerBegin) {
		t.Error("SDD marker still present after RemoveSDDHooks")
	}
	if !strings.Contains(string(data), "echo foreign") {
		t.Error("foreign content lost after RemoveSDDHooks")
	}

	if svc.SDDHooksInstalled(repoDir) {
		t.Error("SDDHooksInstalled = true after RemoveSDDHooks, want false")
	}
}

func TestSDDHooksInstalled_FalseWhenAbsentOrEmptyRepoRoot(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")

	if svc.SDDHooksInstalled(repoDir) {
		t.Error("SDDHooksInstalled = true before InstallSDDHooks, want false")
	}
	if svc.SDDHooksInstalled("") {
		t.Error("SDDHooksInstalled(\"\") = true, want false")
	}
}

func TestInstallSDDHooks_EmptyRepoRootFails(t *testing.T) {
	svc, _ := newSDDMaterializeService(t, "wirvii/mneme")
	if err := svc.InstallSDDHooks(""); err == nil {
		t.Error("InstallSDDHooks(\"\") must fail")
	}
	if err := svc.RemoveSDDHooks(""); err == nil {
		t.Error("RemoveSDDHooks(\"\") must fail")
	}
}

// TestInstallSDDHooks_NotAGitRepoFails is a targeted addition (QA
// rejection fix): InstallSDDHooks/RemoveSDDHooks's own HooksDir() error
// branch — a real, ordinary condition (repoRoot is a plain directory, no
// .git at all — exactly what running "mneme sdd hooks install" from the
// wrong directory would hit) — was never exercised by anything.
func TestInstallSDDHooks_NotAGitRepoFails(t *testing.T) {
	svc, _ := newSDDMaterializeService(t, "wirvii/mneme")
	plainDir := t.TempDir()

	if err := svc.InstallSDDHooks(plainDir); err == nil {
		t.Error("InstallSDDHooks against a non-git directory must fail")
	}
	if err := svc.RemoveSDDHooks(plainDir); err == nil {
		t.Error("RemoveSDDHooks against a non-git directory must fail")
	}
}

// TestRemoveSDDHooks_TruncatedBlockStillRemoved is removeSDDHookBlock's
// own targeted addition (QA rejection fix): its endIdx<0 fallback — a
// begin marker present with NO matching end marker at all, a real
// condition a partially-written or hand-truncated hook file could be in
// — was never exercised.
func TestRemoveSDDHooks_TruncatedBlockStillRemoved(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	hooksDir, err := (sddGit{RepoDir: repoDir}).HooksDir()
	if err != nil {
		t.Fatalf("HooksDir: %v", err)
	}
	hookPath := filepath.Join(hooksDir, "post-merge")
	truncated := "#!/bin/sh\n" + SDDHooksMarkerBegin + "\nsome content with no end marker\n"
	if wErr := os.WriteFile(hookPath, []byte(truncated), 0o755); wErr != nil {
		t.Fatalf("write truncated fixture: %v", wErr)
	}

	if err := svc.RemoveSDDHooks(repoDir); err != nil {
		t.Fatalf("RemoveSDDHooks: %v", err)
	}

	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read post-merge: %v", err)
	}
	if strings.Contains(string(data), SDDHooksMarkerBegin) {
		t.Error("begin marker still present after removing a truncated block")
	}
}
