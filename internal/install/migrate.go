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

// MigrateResult holds the outcome of a MigrateWorkflowDir call. It reports
// which files were copied and which were skipped because they already existed
// at the destination, enabling callers to surface a human-readable summary.
type MigrateResult struct {
	// Copied is the list of destination paths for files that were successfully
	// copied from legacyDir to newDir.
	Copied []string

	// Skipped is the list of destination paths that already existed in newDir
	// and were therefore left unchanged.
	Skipped []string
}

// MigrateWorkflowDir copies workflow artifacts from legacyDir to newDir
// without deleting the source. It is safe to run multiple times — files that
// already exist in newDir are skipped so user modifications in the new
// location are never overwritten.
//
// The function walks legacyDir recursively and copies each file to the
// corresponding path under newDir, creating parent directories as needed.
// The legacyDir structure is preserved: a project at legacyDir/myproject/
// is migrated to newDir/myproject/ with all nested content intact.
// The legacyDir is never modified or deleted; cleanup is left to the user.
func MigrateWorkflowDir(legacyDir, newDir string) (MigrateResult, error) {
	var result MigrateResult

	err := filepath.Walk(legacyDir, func(src string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip unreadable entries rather than aborting the whole migration.
			return nil
		}

		// Compute relative path to reconstruct under newDir.
		rel, err := filepath.Rel(legacyDir, src)
		if err != nil {
			return fmt.Errorf("install: migrate: rel path for %s: %w", src, err)
		}

		// Skip the root entry itself — it maps to "." which is newDir itself.
		if rel == "." {
			return nil
		}

		dst := filepath.Join(newDir, rel)

		if info.IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("install: migrate: mkdir %s: %w", dst, err)
			}
			return nil
		}

		// Skip files that already exist in the destination.
		if _, statErr := os.Stat(dst); statErr == nil {
			result.Skipped = append(result.Skipped, dst)
			return nil
		}

		if err := migrateFileCopy(src, dst); err != nil {
			return fmt.Errorf("install: migrate: copy %s -> %s: %w", src, dst, err)
		}
		result.Copied = append(result.Copied, dst)
		return nil
	})

	return result, err
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
