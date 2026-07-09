// Package vault implements the filesystem vault mirror for mneme: reading and
// writing memories as individual Markdown files with YAML frontmatter.
package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"

	"github.com/juanftp/mneme/internal/model"
)

// ParsedNote is the result of parsing a single vault .md file.
// It carries both the structured frontmatter and the raw Markdown body so
// callers can convert them to a model.SaveRequest or model.UpdateRequest
// without re-reading the file.
type ParsedNote struct {
	// Path is the absolute file path of the source .md file.
	Path string

	// FM is the parsed YAML frontmatter.
	FM Frontmatter

	// Body is the Markdown content after the closing --- delimiter.
	// It corresponds to model.Memory.Content and is stored as-is.
	Body string
}

// Reader walks a vault directory and parses all .md files under notes/.
// It is the import counterpart to Writer: Writer exports memories to disk,
// Reader reads them back.
type Reader struct {
	vaultRoot string
}

// NewReader creates a Reader for the given vault root directory.
// The vault root must contain a notes/ subdirectory for ReadAll to find files.
func NewReader(vaultRoot string) *Reader {
	return &Reader{vaultRoot: vaultRoot}
}

// ReadAll walks the notes/ subdirectory under the vault root, parses each .md
// file, and returns a slice of ParsedNotes. Files that fail parsing are
// collected in the returned errors slice but do not prevent other files from
// being parsed. Non-.md files are silently skipped.
func (r *Reader) ReadAll() ([]*ParsedNote, []error) {
	notesDir := filepath.Join(r.vaultRoot, "notes")

	var notes []*ParsedNote
	var errs []error

	err := filepath.WalkDir(notesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		note, parseErr := ParseFile(path)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, parseErr))
			return nil // skip, continue
		}
		notes = append(notes, note)
		return nil
	})

	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("walk notes dir: %w", err))
	}

	return notes, errs
}

// ParseFile reads a .md file at path, extracts its YAML frontmatter, and
// returns a ParsedNote. Returns an error if the file cannot be read, does not
// have valid frontmatter delimiters, or contains structurally unparseable content.
// Unknown frontmatter fields are silently ignored for forward compatibility.
func ParseFile(path string) (*ParsedNote, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vault: parse file: read %q: %w", path, err)
	}

	fm, fmEnd, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("vault: parse file: %q: %w", path, err)
	}

	body := extractBody(data, fmEnd)

	return &ParsedNote{
		Path: path,
		FM:   fm,
		Body: body,
	}, nil
}

// parseFrontmatter parses the YAML-like frontmatter block from raw bytes.
// The input must begin with "---\n". The closing "---\n" ends the block.
// Returns the populated Frontmatter, the byte offset immediately after the
// closing delimiter (for body extraction), and any parse error.
//
// The parser handles the fixed field set of Frontmatter. List fields (files,
// applies_to) use the "  - item" indented-list syntax. Unknown fields are
// silently ignored.
func parseFrontmatter(data []byte) (Frontmatter, int, error) {
	s := string(data)
	lines := strings.Split(s, "\n")

	if len(lines) == 0 || lines[0] != "---" {
		return Frontmatter{}, 0, fmt.Errorf("missing opening --- delimiter")
	}

	var fm Frontmatter
	var currentList *[]string // points to the slice currently being appended
	closingLine := -1

	for i := 1; i < len(lines); i++ {
		line := lines[i]

		if line == "---" {
			closingLine = i
			break
		}

		// List item: lines starting with "  - "
		if strings.HasPrefix(line, "  - ") {
			item := strings.TrimPrefix(line, "  - ")
			if currentList == nil {
				return Frontmatter{}, 0, fmt.Errorf("list item %q has no list header", line)
			}
			*currentList = append(*currentList, item)
			continue
		}

		// Any non-list line resets the list context.
		currentList = nil

		// Key-value: "key: value" or "key:" (empty value)
		colonIdx := strings.Index(line, ": ")
		var key, value string
		if colonIdx >= 0 {
			key = line[:colonIdx]
			value = line[colonIdx+2:]
		} else if strings.HasSuffix(line, ":") {
			// key with no value — this is a list header like "files:" or empty field
			key = strings.TrimSuffix(line, ":")
			value = ""
		} else {
			// malformed line — ignore for forward compat
			continue
		}

		switch key {
		case "id":
			fm.ID = value
		case "type":
			fm.Type = value
		case "scope":
			fm.Scope = value
		case "title":
			fm.Title = unquoteTitle(value)
		case "topic_key":
			fm.TopicKey = value
		case "project":
			fm.Project = value
		case "importance":
			fm.Importance = parseFloatField(value)
		case "confidence":
			fm.Confidence = parseFloatField(value)
		case "decay_rate":
			fm.DecayRate = parseFloatField(value)
		case "created_at":
			fm.CreatedAt = value
		case "updated_at":
			fm.UpdatedAt = value
		case "revision_count":
			fm.RevisionCount = parseIntField(value)
		case "created_by":
			fm.CreatedBy = value
		case "severity":
			fm.Severity = value
		case "superseded_by":
			fm.SupersededBy = value
		case "shared":
			fm.Shared = parseIntField(value)
		case "author":
			fm.Author = value
		case "files":
			currentList = &fm.Files
		case "applies_to":
			currentList = &fm.AppliesTo
		default:
			// Unknown field — ignore for forward compatibility.
		}
	}

	if closingLine < 0 {
		return Frontmatter{}, 0, fmt.Errorf("missing closing --- delimiter")
	}

	// Compute byte offset: sum of lengths of all lines up to and including the
	// closing "---" line, each line followed by "\n".
	offset := 0
	for i := 0; i <= closingLine; i++ {
		offset += len(lines[i]) + 1 // +1 for the "\n" that Split removed
	}

	return fm, offset, nil
}

// extractBody returns the Markdown content after the frontmatter block.
// fmEnd is the byte offset immediately after the closing "---\n" delimiter,
// as returned by parseFrontmatter. extractBody trims one leading blank line
// to match the "\n%s\n" format used by writeNote in writer.go.
func extractBody(data []byte, fmEnd int) string {
	if fmEnd >= len(data) {
		return ""
	}
	body := string(data[fmEnd:])
	// Trim exactly one leading newline, matching writeNote output.
	body = strings.TrimPrefix(body, "\n")
	// Trim trailing newline added by writeNote.
	body = strings.TrimSuffix(body, "\n")
	return body
}

// unquoteTitle strips surrounding double quotes from a title value and
// un-escapes Go-style escape sequences. WriteTo writes titles using %q
// (frontmatter.go:119), so the parser must reverse that encoding.
// Handles:
//   - `"Simple title"` → `Simple title`
//   - `"Title with \"quotes\""` → `Title with "quotes"`
//   - `Unquoted title` → `Unquoted title` (forward compat for manual files)
func unquoteTitle(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		// strconv.Unquote handles Go's %q format exactly.
		unquoted, err := strconv.Unquote(s)
		if err == nil {
			return unquoted
		}
	}
	// Not double-quoted or malformed — return as-is.
	return s
}

// parseFloatField parses a float64 from a frontmatter value string.
// Returns 0 on any parse failure (forward compat, non-fatal).
func parseFloatField(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// parseIntField parses an int from a frontmatter value string.
// Returns 0 on any parse failure.
func parseIntField(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

// IsValidUUID reports whether s is a syntactically valid UUID string.
// It uses the gofrs/uuid parser (already in go.mod) which handles all UUID
// variants. An empty string returns false.
func IsValidUUID(s string) bool {
	if s == "" {
		return false
	}
	_, err := uuid.FromString(s)
	return err == nil
}

// ParseUpdatedAtFromFM parses the updated_at field from a Frontmatter into a
// time.Time. It tries RFC3339Nano first (sub-second precision used by
// FromMemory), then falls back to RFC3339 for files created before the
// precision fix. Returns the zero time and false when the field is empty or
// unparseable.
func ParseUpdatedAtFromFM(fm Frontmatter) (time.Time, bool) {
	if fm.UpdatedAt == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, fm.UpdatedAt); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, fm.UpdatedAt); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// ToSaveRequest converts a Frontmatter and body content into a model.SaveRequest
// suitable for passing to service.Save(). Fields not present in SaveRequest
// (id, created_at, revision_count) are handled by the import orchestrator.
// Importance is forwarded as a pointer so the service honours the file value
// rather than recomputing a type-based default.
func (fm Frontmatter) ToSaveRequest(body string) model.SaveRequest {
	imp := fm.Importance
	req := model.SaveRequest{
		Title:      fm.Title,
		Content:    body,
		Type:       model.MemoryType(fm.Type),
		Scope:      model.Scope(fm.Scope),
		TopicKey:   fm.TopicKey,
		Project:    fm.Project,
		CreatedBy:  fm.CreatedBy,
		Files:      fm.Files,
		Importance: &imp,
	}
	// Rule-specific fields: only set when type=rule so service.Save()
	// validation accepts them.
	if fm.Type == string(model.TypeRule) {
		req.AppliesTo = fm.AppliesTo
		req.Severity = model.Severity(fm.Severity)
	}
	return req
}

// ToUpdateRequest converts a Frontmatter and body content into a
// model.UpdateRequest for updating an existing memory in the DB. Only fields
// that are meaningful for updates are included; operational parameters
// (decay_rate, created_at) are intentionally excluded per spec D10 / open
// question 1.
func (fm Frontmatter) ToUpdateRequest(body string) model.UpdateRequest {
	title := fm.Title
	content := body
	memType := model.MemoryType(fm.Type)
	importance := fm.Importance
	confidence := fm.Confidence

	req := model.UpdateRequest{
		Title:      &title,
		Content:    &content,
		Type:       &memType,
		Importance: &importance,
		Confidence: &confidence,
	}

	if len(fm.Files) > 0 {
		files := make([]string, len(fm.Files))
		copy(files, fm.Files)
		req.Files = &files
	}
	if len(fm.AppliesTo) > 0 {
		ap := make([]string, len(fm.AppliesTo))
		copy(ap, fm.AppliesTo)
		req.AppliesTo = &ap
	}
	if fm.Severity != "" {
		sev := model.Severity(fm.Severity)
		req.Severity = &sev
	}

	return req
}
