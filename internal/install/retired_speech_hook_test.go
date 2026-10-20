package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const speechPromptCommand = "mneme hook speech-prompt"

// TestFreshInstall_NoSpeechPromptHook covers SPEC-161 C5: neither agent
// registers the retired speech-prompt hook on a clean host, and SessionStart
// stays registered.
func TestFreshInstall_NoSpeechPromptHook(t *testing.T) {
	const binPath = "/usr/local/bin/mneme"

	t.Run("claude-code", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		if err := Install(ClaudeCode(binPath), binPath); err != nil {
			t.Fatalf("Install: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(tmpHome, ".claude", "settings.json"))
		if err != nil {
			t.Fatalf("read settings: %v", err)
		}
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatalf("unmarshal settings: %v", err)
		}
		hooks, _ := settings["hooks"].(map[string]any)
		assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
		assertHookCount(t, hooks, "UserPromptSubmit", speechPromptCommand, 0)
	})

	t.Run("codex", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "hooks.json")
		if err := WriteCodexHooks(path); err != nil {
			t.Fatalf("WriteCodexHooks: %v", err)
		}
		hooks := parseCodexHooks(t, path)
		assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
		assertHookCount(t, hooks, "UserPromptSubmit", speechPromptCommand, 0)
	})
}

// TestInstall_RetiresSpeechPromptHook covers SPEC-161 C6: a host that still
// registers UserPromptSubmit -> speech-prompt next to a foreign
// UserPromptSubmit entry loses only the speech entry, and a second install
// leaves the file byte-identical.
func TestInstall_RetiresSpeechPromptHook(t *testing.T) {
	const binPath = "/usr/local/bin/mneme"
	const seed = `{
  "hooks": {
    "UserPromptSubmit": [
      {"hooks": [{"type": "command", "command": "mneme hook speech-prompt"}]},
      {"hooks": [{"type": "command", "command": "echo hi"}]}
    ]
  }
}`

	tests := []struct {
		name  string
		agent func() *Agent
		rel   string
		read  func(t *testing.T, path string) map[string]any
	}{
		{
			name:  "claude-code",
			agent: func() *Agent { return ClaudeCode(binPath) },
			rel:   filepath.Join(".claude", "settings.json"),
			read: func(t *testing.T, path string) map[string]any {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read settings: %v", err)
				}
				var settings map[string]any
				if err := json.Unmarshal(data, &settings); err != nil {
					t.Fatalf("unmarshal settings: %v", err)
				}
				hooks, _ := settings["hooks"].(map[string]any)
				return hooks
			},
		},
		{
			name:  "codex",
			agent: func() *Agent { return Codex(binPath) },
			rel:   filepath.Join(".codex", "hooks.json"),
			read: func(t *testing.T, path string) map[string]any {
				t.Helper()
				return parseCodexHooks(t, path)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)

			path := filepath.Join(tmpHome, tt.rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
				t.Fatalf("write seed: %v", err)
			}

			if err := Install(tt.agent(), binPath); err != nil {
				t.Fatalf("first Install: %v", err)
			}
			hooks := tt.read(t, path)
			assertHookCount(t, hooks, "UserPromptSubmit", speechPromptCommand, 0)
			assertHookEntry(t, hooks, "UserPromptSubmit", "echo hi")
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read after first install: %v", err)
			}

			if err := Install(tt.agent(), binPath); err != nil {
				t.Fatalf("second Install: %v", err)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read after second install: %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Errorf("%s not byte-identical across installs:\n1st:\n%s\n2nd:\n%s", tt.rel, first, second)
			}
		})
	}
}
