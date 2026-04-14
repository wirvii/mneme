package install

import (
	"embed"
	"fmt"
	"path"
	"path/filepath"
)

//go:embed assets/agents/*.md
var builtinAgents embed.FS

//go:embed assets/commands/*.md
var builtinCommands embed.FS

//go:embed assets/templates/*.md
var builtinTemplates embed.FS

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
