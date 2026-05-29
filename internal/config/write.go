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
