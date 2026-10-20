package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wirvii/mneme/internal/model"
)

// tareaVersionLine opens every tarea.md; it carries the format version so a
// future layout change can be detected.
const tareaVersionLine = "<!-- mneme:tarea v=1 -->"

// tareaMarkerPrefix is the substring every structural marker starts with.
// Caller content containing it is escaped (escapeTaskContent) so it can
// never be mistaken for a marker.
const tareaMarkerPrefix = "<!-- mneme:tarea"

// tareaEscapedPrefix is what tareaMarkerPrefix becomes inside content. It
// renders identically in Markdown and is deterministic; there is no
// unescape because the file is for reading, not for re-import.
const tareaEscapedPrefix = "&lt;!-- mneme:tarea"

// taskDocMu serialises tarea.md read-modify-write cycles inside one mneme
// process. Two processes writing different sections in the same instant can
// still lose a write; the real flow is sequential (architect, implementer,
// qa), so the risk is accepted and documented (SPEC-170 D4).
var taskDocMu sync.Mutex

// taskStartMarker returns the full-line start marker of section s.
func taskStartMarker(s model.TaskSection) string {
	return fmt.Sprintf("<!-- mneme:tarea:%s:start -->", s)
}

// taskEndMarker returns the full-line end marker of section s.
func taskEndMarker(s model.TaskSection) string {
	return fmt.Sprintf("<!-- mneme:tarea:%s:end -->", s)
}

// renderTaskSkeleton builds a fresh tarea.md: version line, title and the
// five regions, each holding only its heading, in canonical order.
func renderTaskSkeleton(specID, title string) string {
	var b strings.Builder
	b.WriteString(tareaVersionLine + "\n")
	fmt.Fprintf(&b, "# %s — %s\n", specID, title)
	for _, s := range model.TaskSections {
		heading, _ := s.Heading()
		b.WriteString("\n" + taskStartMarker(s) + "\n")
		b.WriteString(heading + "\n")
		b.WriteString(taskEndMarker(s) + "\n")
	}
	return b.String()
}

// escapeTaskContent neutralises any marker-looking text in caller content.
func escapeTaskContent(content string) string {
	return strings.ReplaceAll(content, tareaMarkerPrefix, tareaEscapedPrefix)
}

// renderTaskRegion renders the lines that live between a section's markers:
// its heading, then the escaped content. A leading duplicate of the heading
// in content is dropped so the heading never appears twice.
func renderTaskRegion(s model.TaskSection, content string) []string {
	heading, _ := s.Heading()
	body := strings.Trim(content, "\r\n")
	if first, rest, _ := strings.Cut(body, "\n"); strings.TrimSpace(first) == heading {
		body = strings.Trim(rest, "\r\n")
	}
	lines := []string{heading}
	if strings.TrimSpace(body) == "" {
		return lines
	}
	return append(lines, "", escapeTaskContent(body))
}

// taskSpan is the line index of a section's start and end markers.
type taskSpan struct{ start, end int }

// parseTaskDoc locates every section's marker pair. It demands each marker
// exactly once and the pairs in canonical order, start before end; anything
// else is ErrTaskDocMalformed naming the offending section.
func parseTaskDoc(doc string) (map[model.TaskSection]taskSpan, error) {
	lines := strings.Split(doc, "\n")
	spans := make(map[model.TaskSection]taskSpan, len(model.TaskSections))
	prevEnd := -1
	for _, s := range model.TaskSections {
		start, end, err := findMarkerPair(lines, s)
		if err != nil {
			return nil, err
		}
		if start <= prevEnd || end <= start {
			return nil, fmt.Errorf("service: tarea.md section %q out of order: %w", s, model.ErrTaskDocMalformed)
		}
		spans[s] = taskSpan{start: start, end: end}
		prevEnd = end
	}
	return spans, nil
}

// findMarkerPair returns the line indexes of s's start and end markers,
// failing when either is missing or duplicated.
func findMarkerPair(lines []string, s model.TaskSection) (start, end int, err error) {
	start, end = -1, -1
	startM, endM := taskStartMarker(s), taskEndMarker(s)
	for i, l := range lines {
		switch l {
		case startM:
			if start != -1 {
				return 0, 0, fmt.Errorf("service: tarea.md section %q start marker duplicated: %w", s, model.ErrTaskDocMalformed)
			}
			start = i
		case endM:
			if end != -1 {
				return 0, 0, fmt.Errorf("service: tarea.md section %q end marker duplicated: %w", s, model.ErrTaskDocMalformed)
			}
			end = i
		}
	}
	if start == -1 || end == -1 {
		return 0, 0, fmt.Errorf("service: tarea.md section %q marker missing: %w", s, model.ErrTaskDocMalformed)
	}
	return start, end, nil
}

// spliceTaskSection replaces only the lines between s's markers with the
// rendered content; every other byte of doc is preserved.
func spliceTaskSection(doc string, s model.TaskSection, content string) (string, error) {
	spans, err := parseTaskDoc(doc)
	if err != nil {
		return "", err
	}
	sp, ok := spans[s]
	if !ok {
		return "", fmt.Errorf("service: tarea.md splice %q: %w", s, model.ErrUnknownTaskSection)
	}
	lines := strings.Split(doc, "\n")
	out := make([]string, 0, len(lines))
	out = append(out, lines[:sp.start+1]...)
	out = append(out, renderTaskRegion(s, content)...)
	out = append(out, lines[sp.end:]...)
	return strings.Join(out, "\n"), nil
}

// validateTaskRequest enforces the section rules of SPEC-170 D4 before any
// I/O: a section is mandatory and known for kind task, and forbidden for
// every other kind.
func validateTaskRequest(req model.SpecDocWriteRequest) error {
	if req.Kind != model.SpecDocKindTask {
		if req.Section != "" {
			return fmt.Errorf("service: spec doc write: kind %q: %w", req.Kind, model.ErrSectionNotApplicable)
		}
		return nil
	}
	if req.Section == "" {
		return fmt.Errorf("service: spec doc write: %w", model.ErrTaskSectionRequired)
	}
	if _, ok := req.Section.Heading(); !ok {
		return fmt.Errorf("service: spec doc write: section %q (valid: %v): %w", req.Section, model.TaskSections, model.ErrUnknownTaskSection)
	}
	return nil
}

// writeTaskSection replaces one section of the spec's tarea.md at path,
// creating the file from the skeleton when absent. The write is atomic
// (temp file in the same directory, then rename); a malformed existing file
// is reported and left untouched.
func writeTaskSection(path string, spec *model.Spec, section model.TaskSection, content string) (created bool, err error) {
	taskDocMu.Lock()
	defer taskDocMu.Unlock()

	doc, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
	case errors.Is(readErr, os.ErrNotExist):
		created = true
		doc = []byte(renderTaskSkeleton(spec.ID, spec.Title))
	default:
		return false, fmt.Errorf("service: spec doc write: read %s: %w", path, readErr)
	}

	updated, err := spliceTaskSection(string(doc), section, content)
	if err != nil {
		return false, err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tarea-*")
	if err != nil {
		return false, fmt.Errorf("service: spec doc write: temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return false, fmt.Errorf("service: spec doc write: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return false, fmt.Errorf("service: spec doc write: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return false, fmt.Errorf("service: spec doc write: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return false, fmt.Errorf("service: spec doc write: rename to %s: %w", path, err)
	}
	return created, nil
}
