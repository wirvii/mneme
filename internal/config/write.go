package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// The writers in this file edit config.toml as TEXT, touching only the one
// key or table they own (SPEC-184 Dp12). Before SPEC-184 they marshalled a
// whole Default()-based Config back to disk, which froze every default
// (orchestrator_trivial_inline = false, synthesis_enabled = true, ...) into
// the file of anyone who ran `mneme model set` or `mneme delegation-hook
// promote`, so a later change of default never reached them. Every write
// goes through writeAtomically, which refuses to replace the file unless the
// result still loads.

// errRootDottedSection is returned when the table to edit is written as
// dotted keys at the root of the file (`delegation.foo = 1` before any
// header): adding a [delegation] header there would redefine the table, so
// the file is left untouched and the person edits it by hand.
var errRootDottedSection = errors.New("config: section is written as root-level dotted keys; edit it by hand")

// errMalformedHeader reports a table header whose key cannot be split.
var errMalformedHeader = errors.New("config: malformed table header")

// bareKeyPattern matches a TOML bare key, which needs no quotes.
var bareKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SetModelsOverrides replaces the [models.overrides] table of the config
// file at path (creating file and table if absent) with one `name = "alias"`
// line per entry, in alphabetical order; an empty map leaves the table empty.
// Every other byte of the file is kept: comments, other sections, and keys
// the person never declared stay undeclared.
//
// This is the only write-path for model overrides — all other config fields
// are read-only from the perspective of the runtime. A file whose overrides
// are written in a shape this editor does not handle (an inline table, for
// instance) fails the post-write Load check and is left untouched.
func SetModelsOverrides(path string, overrides map[string]string) error {
	text, err := readConfigText(path)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	body := make([]string, 0, len(names))
	for _, name := range names {
		body = append(body, formatKeySegment(name)+" = "+tomlString(overrides[name]))
	}
	out, err := replaceTable(text, "models.overrides", body)
	if err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return writeAtomically(path, out)
}

// SetSubagentContainmentMode sets the per-project subagent-containment
// override for project (SPEC-086 D6/D7) in the config file at path, creating
// file and table if absent. It edits only the subagent_containment key of
// [delegation.projects."<project>"]; every other per-project setting (e.g.
// SPEC-171's trivial-inline) and every other byte stays as it was. This is
// the write path "mneme delegation-hook promote" uses to flip a project from
// "warn" to "block".
func SetSubagentContainmentMode(path, project, mode string) error {
	if err := validateContainmentMode("subagent_containment", mode); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	text, err := readConfigText(path)
	if err != nil {
		return err
	}
	header := "delegation.projects." + formatKeySegment(project)
	out, err := upsertKey(text, header, "subagent_containment", tomlString(mode), "")
	if err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return writeAtomically(path, out)
}

// EnsureDeclared writes `key = value` (value is a TOML literal) into the
// global [section] table of the config file at path, preceded by a
// `# comment` line, unless the key is already declared there; it returns
// whether it wrote. Only the global table counts as declared: a value set
// under [section.projects."<slug>"] or through an environment variable
// does not, because what is left in the file is what a person reads.
//
// It exists so that a change of default value is announced by the file
// itself (SPEC-184 D4: `mneme install` declares orchestrator_trivial_inline
// = true) without ever rewriting what the person already wrote. The file
// and its directory are created when absent; when [section] exists the two
// lines go right after its header, otherwise a [section] block is appended
// at the end (TOML lets a parent table follow its sub-tables). A section
// written as root-level dotted keys returns an error and changes nothing.
func EnsureDeclared(path, section, key, value, comment string) (bool, error) {
	text, err := readConfigText(path)
	if err != nil {
		return false, err
	}
	declared, err := isDeclared(text, section, key)
	if err != nil {
		return false, err
	}
	if declared {
		return false, nil
	}
	out, err := upsertKey(text, section, key, value, comment)
	if err != nil {
		return false, fmt.Errorf("config: ensure declared: %w", err)
	}
	if err := writeAtomically(path, out); err != nil {
		return false, err
	}
	return true, nil
}

// isDeclared reports whether key exists in the global table section of the
// decoded text.
func isDeclared(text, section, key string) (bool, error) {
	var doc map[string]any
	if err := toml.Unmarshal([]byte(text), &doc); err != nil {
		return false, fmt.Errorf("config: ensure declared: parse toml: %w", err)
	}
	segs, err := splitHeaderKey(section)
	if err != nil {
		return false, err
	}
	table := doc
	for _, s := range segs {
		next, ok := table[s].(map[string]any)
		if !ok {
			return false, nil
		}
		table = next
	}
	_, ok := table[key]
	return ok, nil
}

// readConfigText returns the file's text, or "" when it does not exist.
func readConfigText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("config: write: read %s: %w", path, err)
	}
	return string(data), nil
}

// writeAtomically writes text to a .tmp sibling, checks that it still loads
// as a valid configuration, and renames it into place. A text that does not
// load leaves the original untouched and no .tmp behind.
func writeAtomically(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: write: mkdir: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(text), 0o644); err != nil {
		return fmt.Errorf("config: write: write tmp: %w", err)
	}
	if _, err := Load(tmpPath); err != nil {
		_ = os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup; the load error is what matters
		return fmt.Errorf("config: write: result would not load, file left unchanged: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup; the rename error is what matters
		return fmt.Errorf("config: write: rename: %w", err)
	}
	return nil
}

// configLines is a config text split into lines, remembering its newline
// style so inserted lines match the file (a CRLF file stays CRLF).
type configLines struct {
	lines []string // without the trailing "\r" of CRLF files
	nl    string
	final bool // whether the text ended with a newline
}

func splitConfigLines(text string) configLines {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	if text == "" {
		return configLines{nl: nl, final: true}
	}
	final := strings.HasSuffix(text, "\n")
	body := strings.TrimSuffix(text, "\n")
	parts := strings.Split(body, "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return configLines{lines: parts, nl: nl, final: final}
}

func (c configLines) String() string {
	if len(c.lines) == 0 {
		return ""
	}
	out := strings.Join(c.lines, c.nl)
	if c.final {
		out += c.nl
	}
	return out
}

// headerLine describes a line that opens a table. segs is nil for an array
// of tables or a header that cannot be split: such a line still ends the
// previous table but never matches a header we look for.
type headerLine struct {
	segs []string
}

// parseHeaderLine recognises `[a.b]` (optionally followed by a comment) and
// `[[a.b]]` lines.
func parseHeaderLine(line string) (headerLine, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") {
		return headerLine{}, false
	}
	if strings.HasPrefix(t, "[[") {
		return headerLine{}, true
	}
	end := closingBracket(t)
	if end < 0 {
		return headerLine{}, false
	}
	if rest := strings.TrimSpace(t[end+1:]); rest != "" && !strings.HasPrefix(rest, "#") {
		return headerLine{}, false
	}
	segs, err := splitHeaderKey(t[1:end])
	if err != nil {
		return headerLine{}, true
	}
	return headerLine{segs: segs}, true
}

// closingBracket returns the index of the "]" closing the header that
// starts at t[0], skipping quoted segments, or -1.
func closingBracket(t string) int {
	var quote byte
	for i := 1; i < len(t); i++ {
		c := t[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == ']':
			return i
		}
	}
	return -1
}

// splitHeaderKey splits a TOML key ("delegation", " delegation . projects ",
// `delegation.projects.'wirvii/mneme'`, `a."b.c"`) into its segments: dots
// outside quotes separate, spaces around segments are trimmed, and quotes
// are removed (basic-string escapes resolved). It is the one comparison
// every editor here uses, so a header written by go-toml's Marshal (single
// quotes) and one written by mneme (double quotes) are the same table.
func splitHeaderKey(s string) ([]string, error) {
	var segs []string
	i := 0
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return nil, fmt.Errorf("%w: %q", errMalformedHeader, s)
		}
		var seg string
		switch s[i] {
		case '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("%w: unclosed quote in %q", errMalformedHeader, s)
			}
			v, err := strconv.Unquote(s[i : j+1])
			if err != nil {
				return nil, fmt.Errorf("%w: %q: %w", errMalformedHeader, s, err)
			}
			seg, i = v, j+1
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("%w: unclosed quote in %q", errMalformedHeader, s)
			}
			seg, i = s[i+1:i+1+j], i+2+j
		default:
			j := i
			for j < len(s) && s[j] != '.' {
				j++
			}
			seg = strings.TrimSpace(s[i:j])
			if seg == "" {
				return nil, fmt.Errorf("%w: empty segment in %q", errMalformedHeader, s)
			}
			i = j
		}
		segs = append(segs, seg)
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return segs, nil
		}
		if s[i] != '.' {
			return nil, fmt.Errorf("%w: %q", errMalformedHeader, s)
		}
		i++
	}
}

// formatKeySegment writes one key segment: bare when TOML allows it,
// otherwise double-quoted with " and \ escaped.
func formatKeySegment(seg string) string {
	if bareKeyPattern.MatchString(seg) {
		return seg
	}
	return tomlString(seg)
}

// tomlString renders s as a TOML basic string.
func tomlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func equalSegs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lineKey returns the key segments of a `key = value` line, or nil.
func lineKey(line string) []string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return nil
	}
	eq := strings.IndexByte(t, '=')
	if eq <= 0 {
		return nil
	}
	segs, err := splitHeaderKey(t[:eq])
	if err != nil {
		return nil
	}
	return segs
}

// locateTable finds the header of the table named by segs: its line index
// and the index of the next header (or len(lines)). It applies rule B: a
// root-level dotted key that already defines the table is an error.
func locateTable(c configLines, segs []string) (start, end int, err error) {
	start, end = -1, len(c.lines)
	seenHeader := false
	for i, line := range c.lines {
		h, isHeader := parseHeaderLine(line)
		if !isHeader {
			if !seenHeader {
				if k := lineKey(line); len(k) > 1 {
					defined := k[:len(k)-1]
					if len(segs) <= len(defined) && equalSegs(defined[:len(segs)], segs) {
						return 0, 0, errRootDottedSection
					}
				}
			}
			continue
		}
		seenHeader = true
		if start >= 0 {
			return start, i, nil
		}
		if h.segs != nil && equalSegs(h.segs, segs) {
			start = i
		}
	}
	return start, end, nil
}

// appendTable adds a new table at the end of c, separated by a blank line
// from any existing content.
func appendTable(c configLines, segs []string, body []string) configLines {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = formatKeySegment(s)
	}
	if len(c.lines) > 0 {
		c.lines = append(c.lines, "")
	}
	c.lines = append(c.lines, "["+strings.Join(parts, ".")+"]")
	c.lines = append(c.lines, body...)
	c.final = true
	return c
}

// upsertKey sets `key = valueLiteral` in the table named header: it
// replaces an existing key line, or inserts `# comment` (when comment is
// not empty) and the key right after the header, or appends the whole
// table at the end when it does not exist.
func upsertKey(text, header, key, valueLiteral, comment string) (string, error) {
	segs, err := splitHeaderKey(header)
	if err != nil {
		return "", err
	}
	c := splitConfigLines(text)
	start, end, err := locateTable(c, segs)
	if err != nil {
		return "", err
	}
	keyLine := formatKeySegment(key) + " = " + valueLiteral
	inserted := []string{keyLine}
	if comment != "" {
		inserted = []string{"# " + comment, keyLine}
	}
	if start < 0 {
		return appendTable(c, segs, inserted).String(), nil
	}
	for i := start + 1; i < end; i++ {
		if k := lineKey(c.lines[i]); len(k) == 1 && k[0] == key {
			c.lines[i] = keyLine
			return c.String(), nil
		}
	}
	lines := make([]string, 0, len(c.lines)+len(inserted))
	lines = append(lines, c.lines[:start+1]...)
	lines = append(lines, inserted...)
	lines = append(lines, c.lines[start+1:]...)
	c.lines = lines
	return c.String(), nil
}

// replaceTable replaces the whole body of the table named header with body
// (comments inside it included), or appends the table when it does not
// exist. A blank line is kept before the next header.
func replaceTable(text, header string, body []string) (string, error) {
	segs, err := splitHeaderKey(header)
	if err != nil {
		return "", err
	}
	c := splitConfigLines(text)
	start, end, err := locateTable(c, segs)
	if err != nil {
		return "", err
	}
	if start < 0 {
		return appendTable(c, segs, body).String(), nil
	}
	lines := make([]string, 0, len(c.lines)+len(body))
	lines = append(lines, c.lines[:start+1]...)
	lines = append(lines, body...)
	if end < len(c.lines) {
		lines = append(lines, "")
	}
	lines = append(lines, c.lines[end:]...)
	c.lines = lines
	return c.String(), nil
}
