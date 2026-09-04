package install

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// containsStr reports whether s is present in list. Test-only helper shared
// by the ownership tests below.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestWriteCommands_AdoptsLegacyContent is AC12: a HOME whose
// ~/.claude/commands/ already holds mneme's own historical, pre-marker
// content for a command (captured in testdata/legacy-commands/, real
// content this repository's own git history produced — not a synthetic
// fixture) converges to the current thin wrapper on the next install, and
// every fixture's digest is genuinely backed by a legacyCommandDigests
// entry, not merely shaped like one.
func TestWriteCommands_AdoptsLegacyContent(t *testing.T) {
	entries, err := os.ReadDir("testdata/legacy-commands")
	if err != nil {
		t.Fatalf("read testdata/legacy-commands: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("testdata/legacy-commands is empty — nothing to verify")
	}

	const binPath = "/usr/local/bin/mneme"
	claudeCmds, err := ClaudeCode(binPath).Commands()
	if err != nil {
		t.Fatalf("ClaudeCode Commands: %v", err)
	}
	installedByBase := map[string]CommandFile{}
	for _, c := range claudeCmds {
		installedByBase[filepath.Base(c.Path)] = c
	}

	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		legacy, err := os.ReadFile(filepath.Join("testdata", "legacy-commands", e.Name()))
		if err != nil {
			t.Fatalf("read fixture %s: %v", e.Name(), err)
		}

		sum := sha256.Sum256(legacy)
		digest := hex.EncodeToString(sum[:])
		if !isKnownLegacyDigest(name, digest) {
			t.Errorf("%s: fixture digest %s is not recorded in legacyCommandDigests[%q]", e.Name(), digest, name)
		}

		cmd, installed := installedByBase[name+".md"]
		if !installed {
			// Not yet installable at this point in the change (e.g.
			// grill-me before its thin wrapper lands) — the digest check
			// above already ran; nothing more applies to this name yet.
			continue
		}

		home := t.TempDir()
		dest := filepath.Join(home, ".claude", "commands", name+".md")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(dest, legacy, 0o644); err != nil {
			t.Fatalf("write legacy fixture: %v", err)
		}

		agent := &Agent{Commands: func() ([]CommandFile, error) {
			return []CommandFile{{Path: dest, Content: cmd.Content}}, nil
		}}

		result, err := WriteCommands(agent, false)
		if err != nil {
			t.Fatalf("WriteCommands: %v", err)
		}
		if !containsStr(result.Adopted, name+".md") {
			t.Errorf("%s: expected Adopted to contain %q, got %+v", e.Name(), name+".md", result)
		}

		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read dest after install: %v", err)
		}
		if string(got) != string(cmd.Content) {
			t.Errorf("%s: destination did not converge to the current asset content", e.Name())
		}
	}
}

// TestWriteCommands_KeepsForeignFile is AC13: a destination file that is
// neither marked nor a known legacy digest is a person's own file. It must
// survive byte-for-byte, and WriteCommands must report it in KeptYours.
func TestWriteCommands_KeepsForeignFile(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "commands", "hunt-bug.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("# my own /hunt-bug shortcut, written by hand — do not touch\n")
	if err := os.WriteFile(dest, original, 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	agent := &Agent{Commands: func() ([]CommandFile, error) {
		return []CommandFile{{Path: dest, Content: []byte("mneme's own content\n")}}, nil
	}}

	result, err := WriteCommands(agent, false)
	if err != nil {
		t.Fatalf("WriteCommands: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("foreign file was modified: got %q, want %q", got, original)
	}
	if !containsStr(result.KeptYours, "hunt-bug.md") {
		t.Errorf("expected KeptYours to contain hunt-bug.md, got %+v", result)
	}
}

// TestSlashCommandsStep_KeptYoursDetail proves the "Slash commands" install
// step's detail string surfaces a kept-yours foreign file using
// CommandKeptYoursLabel — the exported constant internal/cli/install.go
// keys off, never a literal reconstructed independently in either place.
func TestSlashCommandsStep_KeptYoursDetail(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "commands", "hunt-bug.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, []byte("foreign content\n"), 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	agent := &Agent{Commands: func() ([]CommandFile, error) {
		return []CommandFile{{Path: dest, Content: []byte("mneme's own content\n")}}, nil
	}}

	var detail string
	found := false
	for _, step := range agent.installSteps(InstallOptions{}) {
		if step.Name == "Slash commands" {
			found = true
			var err error
			detail, err = step.Run()
			if err != nil {
				t.Fatalf("Slash commands step: %v", err)
			}
		}
	}
	if !found {
		t.Fatal("\"Slash commands\" step not found")
	}
	if !strings.Contains(detail, CommandKeptYoursLabel) {
		t.Errorf("detail %q does not contain CommandKeptYoursLabel %q", detail, CommandKeptYoursLabel)
	}
}

// TestWriteCommands_ForceBacksUpForeignFile is AC14: --force replaces a
// foreign file but never destroys it — a ".bak" copy of the original,
// byte-for-byte, is written alongside it first.
func TestWriteCommands_ForceBacksUpForeignFile(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "commands", "hunt-bug.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("# my own /hunt-bug shortcut — do not touch\n")
	if err := os.WriteFile(dest, original, 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}
	newContent := []byte("mneme's own content\n")

	agent := &Agent{Commands: func() ([]CommandFile, error) {
		return []CommandFile{{Path: dest, Content: newContent}}, nil
	}}

	result, err := WriteCommands(agent, true)
	if err != nil {
		t.Fatalf("WriteCommands: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("force did not replace the foreign file: got %q, want %q", got, newContent)
	}

	backupPath := dest + ".bak"
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup %s: %v", backupPath, err)
	}
	if string(backup) != string(original) {
		t.Errorf("backup content = %q, want original %q", backup, original)
	}
	if result.BackupPaths["hunt-bug.md"] != backupPath {
		t.Errorf("BackupPaths[\"hunt-bug.md\"] = %q, want %q", result.BackupPaths["hunt-bug.md"], backupPath)
	}
}

// TestWriteCommands_UpdatesOwnFile is AC16: a destination that already
// carries commandBlockMarker but whose body differs from the current asset
// (a stale mneme-written version) IS updated. This is the control that a
// naive "recognise by byte-equality against the current asset" design would
// fail: it would classify this exact scenario as foreign and freeze the
// shortcut forever between versions.
func TestWriteCommands_UpdatesOwnFile(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "commands", "mneme-init.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := []byte("---\nname: mneme-init\ndescription: old\n---\n" +
		"<!-- mneme:command:start v=1 -->\nstale body, not the current asset\n<!-- mneme:command:end -->\n")
	if err := os.WriteFile(dest, stale, 0o644); err != nil {
		t.Fatalf("write stale marked file: %v", err)
	}

	current := []byte("---\nname: mneme-init\ndescription: new\n---\n" +
		"<!-- mneme:command:start v=1 -->\ncurrent body\n<!-- mneme:command:end -->\n")
	agent := &Agent{Commands: func() ([]CommandFile, error) {
		return []CommandFile{{Path: dest, Content: current}}, nil
	}}

	result, err := WriteCommands(agent, false)
	if err != nil {
		t.Fatalf("WriteCommands: %v", err)
	}
	if !containsStr(result.Updated, "mneme-init.md") {
		t.Errorf("expected Updated to contain mneme-init.md, got %+v", result)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(current) {
		t.Errorf("marked file with a stale body was not updated: got %q, want %q", got, current)
	}
}
