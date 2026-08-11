package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// SetModelsOverrides reads the config file at path (creating it if absent),
// updates the [models.overrides] section with the supplied map, and writes the
// result back atomically using a .tmp sibling file followed by os.Rename.
//
// The function normalises the TOML representation but never touches any other
// section of the file. If path does not exist the full config (with only the
// models section populated) is written from scratch.
//
// This is the only write-path for model overrides — all other config fields
// are read-only from the perspective of the runtime.
func SetModelsOverrides(path string, overrides map[string]string) error {
	// Load existing config, or start from defaults when the file is absent.
	cfg := Default()
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("config: write: read %s: %w", path, err)
		}
		if err := toml.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("config: write: parse toml: %w", err)
		}
	}

	// Apply the new overrides.
	cfg.Models.Overrides = overrides

	// Marshal the updated config to TOML.
	out, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: write: marshal: %w", err)
	}

	// Write atomically: tmp file + rename so readers never see a partial write.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: write: mkdir: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
		return fmt.Errorf("config: write: write tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Best-effort cleanup of the tmp file.
		_ = os.Remove(tmpPath)
		return fmt.Errorf("config: write: rename: %w", err)
	}

	return nil
}

// SetSubagentContainmentMode reads the config file at path (creating it if
// absent), sets a per-project subagent-containment override for project
// (SPEC-086 D6/D7), and writes the result back atomically — mirrors
// SetModelsOverrides' load/mutate/marshal/rename-into-place pattern exactly.
// This is the write path "mneme delegation-hook promote" uses to flip a
// project from "warn" to "block".
func SetSubagentContainmentMode(path, project, mode string) error {
	if err := validateContainmentMode("subagent_containment", mode); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}

	cfg := Default()
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("config: write: read %s: %w", path, err)
		}
		if err := toml.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("config: write: parse toml: %w", err)
		}
	}

	if cfg.Delegation.Projects == nil {
		cfg.Delegation.Projects = map[string]DelegationProjectConfig{}
	}
	cfg.Delegation.Projects[project] = DelegationProjectConfig{SubagentContainment: mode}

	out, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: write: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: write: mkdir: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
		return fmt.Errorf("config: write: write tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("config: write: rename: %w", err)
	}

	return nil
}

// SetProfilesDefault reads the config file at path (creating it if absent),
// sets [profiles].default to name, and writes the result back atomically —
// mirrors SetModelsOverrides' load/mutate/marshal/rename-into-place pattern
// exactly. This is the sole write-path "mneme profile default <name>" /
// "--clear" uses (SPEC-093 §3.3/§3.4). name == "" clears the default,
// reverting sessions with no repo pin to vanilla.
func SetProfilesDefault(path, name string) error {
	cfg := Default()
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("config: write: read %s: %w", path, err)
		}
		if err := toml.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("config: write: parse toml: %w", err)
		}
	}

	cfg.Profiles.Default = name

	out, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: write: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: write: mkdir: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
		return fmt.Errorf("config: write: write tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("config: write: rename: %w", err)
	}

	return nil
}

// SetSpeech reads the host configuration, replaces its speech section, and
// writes it atomically. It preserves every unrelated configuration section.
func SetSpeech(path string, speech SpeechConfig) error {
	cfg := Default()
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("config: write speech: read %s: %w", path, err)
		}
		if err := toml.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("config: write speech: parse toml: %w", err)
		}
	}
	NormalizeSpeechEngines(&speech)
	cfg.Speech = speech
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config: write speech: %w", err)
	}
	out, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: write speech: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: write speech: mkdir: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
		return fmt.Errorf("config: write speech: write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("config: write speech: rename: %w", err)
	}
	return nil
}
