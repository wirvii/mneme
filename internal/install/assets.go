package install

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

//go:embed assets/agents/*.md
var builtinAgents embed.FS

//go:embed assets/skills
var builtinSkills embed.FS

//go:embed assets/commands/*.md
var builtinCommands embed.FS

//go:embed assets/templates/*.md
var builtinTemplates embed.FS

//go:embed assets/hooks/enforce_delegation.sh
var delegationHookScript []byte

//go:embed assets/operating-manual.md
var operatingManualAsset []byte

//go:embed assets/operating-manual-codex.md
var operatingManualCodexAsset []byte

// DelegationHookContent returns the raw bytes of the embedded
// enforce_delegation.sh bash hook. The caller is responsible for writing
// these bytes to the desired destination with executable permissions.
// Returns an error when the embedded script is empty (should never happen
// in a correctly built binary).
func DelegationHookContent() ([]byte, error) {
	if len(delegationHookScript) == 0 {
		return nil, fmt.Errorf("install: delegation hook script is empty")
	}
	return delegationHookScript, nil
}

// SpecTemplateContent returns the raw bytes of the embedded spec-template.md.
// The caller is responsible for writing these bytes to the desired destination.
// Returns an error when the embedded file cannot be read (should never happen
// in a correctly built binary).
func SpecTemplateContent() ([]byte, error) {
	content, err := builtinTemplates.ReadFile("assets/templates/spec-template.md")
	if err != nil {
		return nil, fmt.Errorf("install: read spec template: %w", err)
	}
	return content, nil
}

// SkillEntry represents a single file within an embedded skill subtree.
// It carries the path relative to the skill directory root (e.g.
// "example-skill/validation/run.sh"), the raw file content, and whether
// the file should be written with executable permissions (0o755).
type SkillEntry struct {
	// RelPath is the path relative to "assets/skills" (e.g. "example-skill/SKILL.md").
	RelPath string

	// Content is the raw file content.
	Content []byte

	// IsExecutable reports whether the file should be installed with 0o755
	// permissions. True for .sh files and any file under scripts/ or validation/.
	IsExecutable bool
}

// BundledSkillEntries returns all files embedded under assets/skills as a flat
// slice of SkillEntry values. Directories are skipped. Files whose path ends
// in .sh or whose path contains a /scripts/ or /validation/ component are
// marked IsExecutable=true so the installer can set the correct permissions.
func BundledSkillEntries() ([]SkillEntry, error) {
	var entries []SkillEntry

	err := fs.WalkDir(builtinSkills, "assets/skills", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		data, err := builtinSkills.ReadFile(p)
		if err != nil {
			return fmt.Errorf("install: read embedded skill %q: %w", p, err)
		}

		relPath, err := filepath.Rel(filepath.FromSlash("assets/skills"), filepath.FromSlash(p))
		if err != nil {
			return fmt.Errorf("install: rel path for %q: %w", p, err)
		}

		// Normalise to forward slashes for consistent cross-platform comparison.
		relFwd := filepath.ToSlash(relPath)
		isExec := strings.HasSuffix(relFwd, ".sh") ||
			strings.Contains(relFwd, "/scripts/") ||
			strings.Contains(relFwd, "/validation/")

		entries = append(entries, SkillEntry{
			RelPath:      relPath,
			Content:      data,
			IsExecutable: isExec,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("install: walk embedded skills: %w", err)
	}
	return entries, nil
}

// BundledSkillNames returns the names of all skill directories embedded under
// assets/skills. Each name is the direct subdirectory name (e.g. "example-skill").
func BundledSkillNames() ([]string, error) {
	dirEntries, err := builtinSkills.ReadDir("assets/skills")
	if err != nil {
		return nil, fmt.Errorf("install: read embedded skills dir: %w", err)
	}
	var names []string
	for _, e := range dirEntries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// filesFromEmbed extracts all files from an embed.FS subdirectory and returns
// them as CommandFiles targeted to destDir. Only direct children are returned
// (directories are skipped). Paths within the embed.FS always use forward
// slashes regardless of OS.
func filesFromEmbed(fs embed.FS, subdir, destDir string) ([]CommandFile, error) {
	entries, err := fs.ReadDir(subdir)
	if err != nil {
		return nil, fmt.Errorf("install: read embedded %s: %w", subdir, err)
	}

	var files []CommandFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// embed.FS always uses forward slashes — use path.Join, not filepath.Join.
		content, err := fs.ReadFile(path.Join(subdir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("install: read embedded %s/%s: %w", subdir, entry.Name(), err)
		}
		files = append(files, CommandFile{
			Path:    filepath.Join(destDir, entry.Name()),
			Content: content,
		})
	}
	return files, nil
}
