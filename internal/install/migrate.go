package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/juanftp/mneme/internal/model"
)

// MigrateWorkflowDir copies workflow artifacts from legacyDir to newDir
// without deleting the source. It is safe to run multiple times — files that
// already exist in newDir are skipped so user modifications in the new
// location are never overwritten.
//
// The function walks legacyDir recursively and copies each file to the
// corresponding path under newDir, creating parent directories as needed.
// Directories in legacyDir that do not exist in newDir are created.
// The legacyDir is never modified or deleted; cleanup is left to the user.
func MigrateWorkflowDir(legacyDir, newDir string) error {
	return filepath.Walk(legacyDir, func(src string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip unreadable entries rather than aborting the whole migration.
			return nil
		}

		// Compute relative path to reconstruct under newDir.
		rel, err := filepath.Rel(legacyDir, src)
		if err != nil {
			return fmt.Errorf("install: migrate: rel path for %s: %w", src, err)
		}
		dst := filepath.Join(newDir, rel)

		if info.IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("install: migrate: mkdir %s: %w", dst, err)
			}
			return nil
		}

		// Skip files that already exist in the destination.
		if _, err := os.Stat(dst); err == nil {
			return nil
		}

		if err := migrateFileCopy(src, dst); err != nil {
			return fmt.Errorf("install: migrate: copy %s -> %s: %w", src, dst, err)
		}
		return nil
	})
}

// migrateFileCopy copies a single file from src to dst, creating parent
// directories as needed. The destination file receives the same permissions as
// the source. It is used exclusively by MigrateWorkflowDir to avoid a name
// collision with the copyFile helper in personal.go.
func migrateFileCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat src: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir dst: %w", err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
	if err != nil {
		if os.IsExist(err) {
			// Concurrent write or race — skip gracefully.
			return nil
		}
		return fmt.Errorf("create dst: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// reBacklogRaw matches a Markdown task list item that is not yet done:
// "- [ ] Some title here"
var reBacklogRaw = regexp.MustCompile(`^- \[ \] (.+)$`)

// ParseBacklogMD parses a legacy backlog.md file and returns a list of
// BacklogAddRequests for uncompleted items. Lines matching "- [ ] ..."
// become raw backlog items. Completed items ("- [x] ...") and all other
// lines are ignored.
func ParseBacklogMD(content string) []model.BacklogAddRequest {
	var items []model.BacklogAddRequest
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := reBacklogRaw.FindStringSubmatch(line); len(m) == 2 {
			items = append(items, model.BacklogAddRequest{
				Title: strings.TrimSpace(m[1]),
			})
		}
	}
	return items
}
