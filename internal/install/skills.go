package install

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/juanftp/mneme/internal/skill"
)

// SkillsResult summarises the outcome of a WriteSkills call.
type SkillsResult struct {
	// Installed lists skill names that were written (created or updated).
	Installed []string

	// Skipped lists skill names whose target SKILL.md had pinned:true and were
	// not overwritten because force was false.
	Skipped []string
}

// WriteSkills installs all skill entries returned by agent.Skills into
// ~/.claude/skills/ (or whatever skillsDir is), respecting pin protection.
//
// Idempotency rules:
//   - If the destination SKILL.md exists and is pinned:true and force is false,
//     the entire skill directory is skipped and its name is added to Skipped.
//   - Otherwise, each file in the skill subtree is written, creating parent
//     directories as needed. Files marked IsExecutable receive mode 0o755;
//     all others receive 0o644.
//
// The function groups entries by top-level skill name (the first path component
// of SkillEntry.RelPath) to implement per-skill pin checking.
func WriteSkills(agent *Agent, skillsDir string, force bool) (*SkillsResult, error) {
	if agent.Skills == nil {
		return &SkillsResult{}, nil
	}

	entries, err := agent.Skills()
	if err != nil {
		return nil, fmt.Errorf("install: write skills: list entries: %w", err)
	}

	// Group entries by top-level skill name.
	type skillFiles struct {
		entries []SkillEntry
	}
	byName := make(map[string]*skillFiles)
	// Preserve insertion order to keep output deterministic.
	var nameOrder []string

	for _, e := range entries {
		// RelPath uses OS separator; normalise for splitting.
		fwd := filepath.ToSlash(e.RelPath)
		parts := strings.SplitN(fwd, "/", 2)
		name := parts[0]

		if _, ok := byName[name]; !ok {
			byName[name] = &skillFiles{}
			nameOrder = append(nameOrder, name)
		}
		byName[name].entries = append(byName[name].entries, e)
	}

	result := &SkillsResult{}

	for _, name := range nameOrder {
		skillDir := filepath.Join(skillsDir, name)
		targetSKILL := filepath.Join(skillDir, "SKILL.md")

		// Check pin status on the *installed* SKILL.md (not the bundled one).
		if !force {
			if existing, readErr := os.ReadFile(targetSKILL); readErr == nil {
				parsed, parseErr := skill.Parse(existing)
				if parseErr == nil && parsed.Pinned {
					slog.Info("install: skill pinned, skipping", "name", name)
					result.Skipped = append(result.Skipped, name)
					continue
				}
			}
		}

		// Write all files for this skill.
		for _, e := range byName[name].entries {
			destPath := filepath.Join(skillsDir, e.RelPath)
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return nil, fmt.Errorf("install: write skills: mkdir %s: %w", filepath.Dir(destPath), err)
			}

			perm := os.FileMode(0o644)
			if e.IsExecutable {
				perm = 0o755
			}

			if err := os.WriteFile(destPath, e.Content, perm); err != nil {
				return nil, fmt.Errorf("install: write skills: write %s: %w", destPath, err)
			}
		}

		result.Installed = append(result.Installed, name)
	}

	return result, nil
}
