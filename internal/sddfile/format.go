package sddfile

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// BacklogRecord is the on-disk aggregate for one backlog item (D26): the
// item plus every refinement it has accumulated, in the order the caller
// supplies (the store already returns them ordered by seq — SPEC-110 D21).
// It is an aggregate over model's own types, not a copy of them: sddfile
// serializes *model.BacklogItem directly, which is why migration 020 had to
// land before this format could be written (§8 of the spec).
type BacklogRecord struct {
	Item        *model.BacklogItem
	Refinements []*model.BacklogRefinement
}

// SpecRecord is the on-disk aggregate for one spec (D26): the spec plus its
// full history and every pushback, in the store's own deterministic order
// (D36 — GetSpecHistory/GetAllPushbacks now tie-break by id).
type SpecRecord struct {
	Spec      *model.Spec
	History   []*model.SpecHistory
	Pushbacks []*model.SpecPushback
}

// ErrRoundTripMismatch is returned by MarshalBacklog/MarshalSpec when the
// bytes just produced do not parse back to an EQUAL record (D27(b)). This
// is a refusal to write, not a corrupted write: the caller (sddfile.io.go's
// WriteRecord) must not persist bytes this package cannot itself read back
// faithfully.
var ErrRoundTripMismatch = errors.New("sddfile: marshal round-trip mismatch")

// ErrConflictMarkers is returned by UnmarshalBacklog/UnmarshalSpec when the
// input contains an unresolved git conflict marker (D37): a line starting
// with "<<<<<<< ", "=======", or ">>>>>>> " at column 0. Without this
// check such a file would parse "successfully" — the marker lines would
// simply fall inside a content block — and mneme would treat merge-conflict
// debris as legitimate content.
var ErrConflictMarkers = errors.New("sddfile: file contains unresolved git conflict markers")

// conflictMarkerPrefixes is git's own three-line conflict marker
// vocabulary. Checked as a column-0 prefix match, matching D37's literal
// wording.
var conflictMarkerPrefixes = []string{"<<<<<<< ", "=======", ">>>>>>> "}

func hasConflictMarkers(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		for _, prefix := range conflictMarkerPrefixes {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

// --- content-block wrap/unwrap ---
//
// Every content blob (a description, a refinement body, a history reason, a
// pushback question or resolution) is written as escapeContent(blob) + "\n"
// and read back by stripping exactly ONE trailing "\n" (TrimSuffix, not
// TrimRight) before unescaping. This is internal/vault/writer.go's own
// writeNote/extractBody technique ("\n%s\n", trim one prefix + one
// suffix), applied per content block instead of once per file: it is what
// makes an arbitrary number of trailing newlines inside the ORIGINAL blob
// round-trip exactly, because the wrap only ever adds ONE newline and the
// unwrap only ever removes ONE.

func wrapBlock(content string) string {
	return escapeContent(content) + "\n"
}

func unwrapBlock(raw string) string {
	return unescapeContent(strings.TrimSuffix(raw, "\n"))
}

// --- schema/kind bareKeys for marker attributes ---

var refinementBareKeys = map[string]bool{"seq": true}
var pushbackBareKeys = map[string]bool{"resolved": true}

// formatTime renders t as RFC3339Nano in UTC — the one timestamp format
// this whole package uses, matching store/sdd.go's own convention.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTimeField parses an RFC3339Nano timestamp from a frontmatter or
// marker attribute value. Returns the zero time and false on any failure.
func parseTimeField(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// --- MarshalBacklog / UnmarshalBacklog ---

// MarshalBacklog serializes rec into the on-disk record format (D26), then
// immediately parses the bytes it just produced and compares the result
// against rec field by field (D27(b)). A mismatch returns
// ErrRoundTripMismatch and NO bytes — the caller must never write on a
// mismatch, since that would mean the file does not represent what mneme
// believes it wrote.
func MarshalBacklog(rec *BacklogRecord) ([]byte, error) {
	if rec == nil || rec.Item == nil {
		return nil, fmt.Errorf("sddfile: marshal backlog: record has no item")
	}

	data := renderBacklog(rec)

	parsed, err := UnmarshalBacklog(data)
	if err != nil {
		return nil, fmt.Errorf("sddfile: marshal backlog: round-trip parse: %w", err)
	}
	if !equalBacklogRecord(rec, parsed) {
		return nil, fmt.Errorf("sddfile: marshal backlog %s: %w", rec.Item.ID, ErrRoundTripMismatch)
	}

	return data, nil
}

// renderBacklog is the pure serialization half of MarshalBacklog, split out
// so the round-trip check can call UnmarshalBacklog on its own output
// without re-entering the round-trip check itself.
func renderBacklog(rec *BacklogRecord) []byte {
	item := rec.Item

	w := &fmWriter{}
	w.scalar("schema", strconv.Itoa(CurrentFileSchema))
	w.scalar("kind", "backlog")
	w.scalar("id", item.ID)
	w.omitScalar("uuid", item.UUID)
	w.omitScalar("project", item.Project)
	w.quoted("title", item.Title)
	w.scalar("status", string(item.Status))
	w.scalar("priority", string(item.Priority))
	w.scalar("lane", string(item.Lane))
	w.omitScalar("scope", item.Scope)
	w.omitScalar("spec_id", item.SpecID)
	w.omitQuoted("archive_reason", item.ArchiveReason)
	w.integer("position", item.Position)
	if len(item.PreviousIDs) > 0 {
		lines := make([]string, len(item.PreviousIDs))
		for i, p := range item.PreviousIDs {
			lines[i] = p.String()
		}
		w.list("previous_ids", lines)
	}
	w.scalar("created_at", formatTime(item.CreatedAt))
	w.scalar("updated_at", formatTime(item.UpdatedAt))

	var b strings.Builder
	b.WriteString(writeFrontmatterBlock(w.String()))
	b.WriteString(wrapBlock(item.Description))

	for _, r := range rec.Refinements {
		b.WriteString(buildMarkerLine(markerKindRefinement, refinementBareKeys,
			"seq", strconv.Itoa(r.Seq), "by", r.By, "at", formatTime(r.At)))
		b.WriteString("\n")
		b.WriteString(wrapBlock(r.Body))
	}

	return []byte(b.String())
}

// UnmarshalBacklog parses data (as produced by MarshalBacklog, or a
// hand-edited variant of it) into a BacklogRecord. Rejects a schema outside
// [MinFileSchema, CurrentFileSchema] (D28) and any file carrying an
// unresolved git conflict marker (D37) BEFORE attempting to parse anything
// else.
func UnmarshalBacklog(data []byte) (*BacklogRecord, error) {
	if hasConflictMarkers(data) {
		return nil, ErrConflictMarkers
	}

	fields, bodyOffset, err := parseFrontmatterBlock(data)
	if err != nil {
		return nil, fmt.Errorf("sddfile: unmarshal backlog: %w", err)
	}

	schema := CurrentFileSchema
	if raw, ok := fields.scalars["schema"]; ok {
		schema = parseIntField(raw)
	}
	if err := checkSchema(schema); err != nil {
		return nil, fmt.Errorf("sddfile: unmarshal backlog: %w", err)
	}

	item := &model.BacklogItem{
		ID:            fields.scalars["id"],
		UUID:          fields.scalars["uuid"],
		Project:       fields.scalars["project"],
		Title:         unquote(fields.scalars["title"]),
		Status:        model.BacklogStatus(fields.scalars["status"]),
		Priority:      model.Priority(fields.scalars["priority"]),
		Lane:          model.Lane(fields.scalars["lane"]),
		Scope:         fields.scalars["scope"],
		SpecID:        fields.scalars["spec_id"],
		ArchiveReason: unquote(fields.scalars["archive_reason"]),
		Position:      parseIntField(fields.scalars["position"]),
	}
	for _, line := range fields.lists["previous_ids"] {
		if p, ok := model.ParsePreviousID(line); ok {
			item.PreviousIDs = append(item.PreviousIDs, p)
		}
	}
	if t, ok := parseTimeField(fields.scalars["created_at"]); ok {
		item.CreatedAt = t
	}
	if t, ok := parseTimeField(fields.scalars["updated_at"]); ok {
		item.UpdatedAt = t
	}

	rest := string(data[bodyOffset:])
	bodyRaw, sections := splitSections(rest)
	item.Description = unwrapBlock(bodyRaw)

	rec := &BacklogRecord{Item: item}
	for _, sec := range sections {
		if sec.kind != markerKindRefinement {
			return nil, fmt.Errorf("sddfile: unmarshal backlog %s: unexpected section kind %q", item.ID, sec.kind)
		}
		r := &model.BacklogRefinement{
			ItemID: item.ID,
			Seq:    parseIntField(sec.attrs["seq"]),
			Body:   unwrapBlock(sec.raw),
			By:     sec.attrs["by"],
		}
		if t, ok := parseTimeField(sec.attrs["at"]); ok {
			r.At = t
		}
		rec.Refinements = append(rec.Refinements, r)
	}

	return rec, nil
}

// equalBacklogRecord compares two BacklogRecords field by field for
// MarshalBacklog's round-trip check (D27(b)). Times compare with
// time.Time.Equal (instant equality — immune to monotonic-reading and
// location differences that would make reflect.DeepEqual over-strict).
func equalBacklogRecord(a, b *BacklogRecord) bool {
	if !equalBacklogItem(a.Item, b.Item) {
		return false
	}
	if len(a.Refinements) != len(b.Refinements) {
		return false
	}
	for i := range a.Refinements {
		ra, rb := a.Refinements[i], b.Refinements[i]
		if ra.ItemID != rb.ItemID || ra.Seq != rb.Seq || ra.Body != rb.Body || ra.By != rb.By {
			return false
		}
		if !ra.At.Equal(rb.At) {
			return false
		}
	}
	return true
}

func equalBacklogItem(a, b *model.BacklogItem) bool {
	if a.ID != b.ID || a.UUID != b.UUID || a.Project != b.Project || a.Title != b.Title ||
		a.Description != b.Description || a.Status != b.Status || a.Priority != b.Priority ||
		a.Lane != b.Lane || a.Scope != b.Scope || a.SpecID != b.SpecID ||
		a.ArchiveReason != b.ArchiveReason || a.Position != b.Position {
		return false
	}
	if len(a.PreviousIDs) != len(b.PreviousIDs) {
		return false
	}
	for i := range a.PreviousIDs {
		pa, pb := a.PreviousIDs[i], b.PreviousIDs[i]
		if pa.ID != pb.ID || pa.Origin != pb.Origin || pa.Reason != pb.Reason || !pa.At.Equal(pb.At) {
			return false
		}
	}
	return a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

// --- MarshalSpec / UnmarshalSpec ---

// MarshalSpec is MarshalBacklog's sibling for specs: same round-trip
// verification, same refusal to return bytes on mismatch (D27(b)).
func MarshalSpec(rec *SpecRecord) ([]byte, error) {
	if rec == nil || rec.Spec == nil {
		return nil, fmt.Errorf("sddfile: marshal spec: record has no spec")
	}

	data := renderSpec(rec)

	parsed, err := UnmarshalSpec(data)
	if err != nil {
		return nil, fmt.Errorf("sddfile: marshal spec: round-trip parse: %w", err)
	}
	if !equalSpecRecord(rec, parsed) {
		return nil, fmt.Errorf("sddfile: marshal spec %s: %w", rec.Spec.ID, ErrRoundTripMismatch)
	}

	return data, nil
}

func renderSpec(rec *SpecRecord) []byte {
	spec := rec.Spec

	w := &fmWriter{}
	w.scalar("schema", strconv.Itoa(CurrentFileSchema))
	w.scalar("kind", "spec")
	w.scalar("id", spec.ID)
	w.omitScalar("uuid", spec.UUID)
	w.omitScalar("project", spec.Project)
	w.quoted("title", spec.Title)
	w.scalar("status", string(spec.Status))
	w.scalar("lane", string(spec.Lane))
	w.omitScalar("scope", spec.Scope)
	w.omitScalar("backlog_id", spec.BacklogID)
	w.omitScalar("base_sha", spec.BaseSHA)
	w.list("assigned_agents", spec.AssignedAgents)
	w.list("files_changed", spec.FilesChanged)
	if len(spec.PreviousIDs) > 0 {
		lines := make([]string, len(spec.PreviousIDs))
		for i, p := range spec.PreviousIDs {
			lines[i] = p.String()
		}
		w.list("previous_ids", lines)
	}
	w.scalar("created_at", formatTime(spec.CreatedAt))
	w.scalar("updated_at", formatTime(spec.UpdatedAt))

	var b strings.Builder
	b.WriteString(writeFrontmatterBlock(w.String()))
	// model.Spec carries no description (D15/CF1) — the body block is
	// always empty, but still written through wrapBlock for the same
	// invertible-newline reason every other content block uses.
	b.WriteString(wrapBlock(""))

	for _, h := range rec.History {
		b.WriteString(buildMarkerLine(markerKindHistory, nil,
			"id", h.ID, "from", string(h.FromStatus), "to", string(h.ToStatus),
			"by", h.By, "at", formatTime(h.At)))
		b.WriteString("\n")
		b.WriteString(wrapBlock(h.Reason))
	}

	for _, pb := range rec.Pushbacks {
		resolvedAt := ""
		if pb.ResolvedAt != nil {
			resolvedAt = formatTime(*pb.ResolvedAt)
		}
		b.WriteString(buildMarkerLine(markerKindPushback, pushbackBareKeys,
			"id", pb.ID, "from_agent", pb.FromAgent, "at", formatTime(pb.CreatedAt),
			"resolved", strconv.FormatBool(pb.Resolved), "resolved_at", resolvedAt))
		b.WriteString("\n")

		for _, q := range pb.Questions {
			b.WriteString(buildMarkerLine(markerKindQuestion, nil))
			b.WriteString("\n")
			b.WriteString(wrapBlock(q))
		}

		// The resolution section is ALWAYS emitted, even for an unresolved
		// pushback (content is simply empty then) — a fixed shape (N
		// questions + exactly 1 resolution block) is what lets the parser
		// walk a pushback's sections without needing extra bookkeeping to
		// tell "no resolution yet" apart from "resolution section missing
		// because the writer forgot it". Resolved/ResolvedAt are the
		// source of truth for whether it is actually resolved — read from
		// the pushback marker's own attributes, never inferred from
		// whether this section is merely present.
		b.WriteString(buildMarkerLine(markerKindResolution, nil))
		b.WriteString("\n")
		b.WriteString(wrapBlock(pb.Resolution))
	}

	return []byte(b.String())
}

// UnmarshalSpec is MarshalSpec's parsing half — UnmarshalBacklog's sibling
// for specs.
func UnmarshalSpec(data []byte) (*SpecRecord, error) {
	if hasConflictMarkers(data) {
		return nil, ErrConflictMarkers
	}

	fields, bodyOffset, err := parseFrontmatterBlock(data)
	if err != nil {
		return nil, fmt.Errorf("sddfile: unmarshal spec: %w", err)
	}

	schema := CurrentFileSchema
	if raw, ok := fields.scalars["schema"]; ok {
		schema = parseIntField(raw)
	}
	if err := checkSchema(schema); err != nil {
		return nil, fmt.Errorf("sddfile: unmarshal spec: %w", err)
	}

	spec := &model.Spec{
		ID:             fields.scalars["id"],
		UUID:           fields.scalars["uuid"],
		Project:        fields.scalars["project"],
		Title:          unquote(fields.scalars["title"]),
		Status:         model.SpecStatus(fields.scalars["status"]),
		Lane:           model.Lane(fields.scalars["lane"]),
		Scope:          fields.scalars["scope"],
		BacklogID:      fields.scalars["backlog_id"],
		BaseSHA:        fields.scalars["base_sha"],
		AssignedAgents: fields.lists["assigned_agents"],
		FilesChanged:   fields.lists["files_changed"],
	}
	for _, line := range fields.lists["previous_ids"] {
		if p, ok := model.ParsePreviousID(line); ok {
			spec.PreviousIDs = append(spec.PreviousIDs, p)
		}
	}
	if t, ok := parseTimeField(fields.scalars["created_at"]); ok {
		spec.CreatedAt = t
	}
	if t, ok := parseTimeField(fields.scalars["updated_at"]); ok {
		spec.UpdatedAt = t
	}

	rest := string(data[bodyOffset:])
	_, sections := splitSections(rest) // body is always empty for a spec (D15/CF1)

	rec := &SpecRecord{Spec: spec}

	i := 0
	for i < len(sections) {
		sec := sections[i]
		switch sec.kind {
		case markerKindHistory:
			h := &model.SpecHistory{
				ID:         sec.attrs["id"],
				SpecID:     spec.ID,
				FromStatus: model.SpecStatus(sec.attrs["from"]),
				ToStatus:   model.SpecStatus(sec.attrs["to"]),
				By:         sec.attrs["by"],
				Reason:     unwrapBlock(sec.raw),
			}
			if t, ok := parseTimeField(sec.attrs["at"]); ok {
				h.At = t
			}
			rec.History = append(rec.History, h)
			i++
		case markerKindPushback:
			pb := &model.SpecPushback{
				ID:        sec.attrs["id"],
				SpecID:    spec.ID,
				FromAgent: sec.attrs["from_agent"],
				Resolved:  parseBoolField(sec.attrs["resolved"]),
			}
			if t, ok := parseTimeField(sec.attrs["at"]); ok {
				pb.CreatedAt = t
			}
			if raw := sec.attrs["resolved_at"]; raw != "" {
				if t, ok := parseTimeField(raw); ok {
					pb.ResolvedAt = &t
				}
			}
			i++
			for i < len(sections) && sections[i].kind == markerKindQuestion {
				pb.Questions = append(pb.Questions, unwrapBlock(sections[i].raw))
				i++
			}
			if i < len(sections) && sections[i].kind == markerKindResolution {
				pb.Resolution = unwrapBlock(sections[i].raw)
				i++
			}
			rec.Pushbacks = append(rec.Pushbacks, pb)
		default:
			return nil, fmt.Errorf("sddfile: unmarshal spec %s: unexpected section kind %q at position %d",
				spec.ID, sec.kind, i)
		}
	}

	return rec, nil
}

func equalSpecRecord(a, b *SpecRecord) bool {
	if !equalSpec(a.Spec, b.Spec) {
		return false
	}
	if len(a.History) != len(b.History) {
		return false
	}
	for i := range a.History {
		ha, hb := a.History[i], b.History[i]
		if ha.ID != hb.ID || ha.SpecID != hb.SpecID || ha.FromStatus != hb.FromStatus ||
			ha.ToStatus != hb.ToStatus || ha.By != hb.By || ha.Reason != hb.Reason {
			return false
		}
		if !ha.At.Equal(hb.At) {
			return false
		}
	}
	if len(a.Pushbacks) != len(b.Pushbacks) {
		return false
	}
	for i := range a.Pushbacks {
		pa, pb := a.Pushbacks[i], b.Pushbacks[i]
		if !equalPushback(pa, pb) {
			return false
		}
	}
	return true
}

func equalSpec(a, b *model.Spec) bool {
	if a.ID != b.ID || a.UUID != b.UUID || a.Project != b.Project || a.Title != b.Title ||
		a.Status != b.Status || a.Lane != b.Lane || a.Scope != b.Scope ||
		a.BacklogID != b.BacklogID || a.BaseSHA != b.BaseSHA {
		return false
	}
	if !equalStringSlices(a.AssignedAgents, b.AssignedAgents) {
		return false
	}
	if !equalStringSlices(a.FilesChanged, b.FilesChanged) {
		return false
	}
	if len(a.PreviousIDs) != len(b.PreviousIDs) {
		return false
	}
	for i := range a.PreviousIDs {
		pa, pb := a.PreviousIDs[i], b.PreviousIDs[i]
		if pa.ID != pb.ID || pa.Origin != pb.Origin || pa.Reason != pb.Reason || !pa.At.Equal(pb.At) {
			return false
		}
	}
	return a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

func equalPushback(a, b *model.SpecPushback) bool {
	if a.ID != b.ID || a.SpecID != b.SpecID || a.FromAgent != b.FromAgent ||
		a.Resolved != b.Resolved || a.Resolution != b.Resolution {
		return false
	}
	if !equalStringSlices(a.Questions, b.Questions) {
		return false
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	if (a.ResolvedAt == nil) != (b.ResolvedAt == nil) {
		return false
	}
	if a.ResolvedAt != nil && !a.ResolvedAt.Equal(*b.ResolvedAt) {
		return false
	}
	return true
}

func equalStringSlices(a, b []string) bool {
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

// --- section splitting ---

// section is one marked block found after the frontmatter/body: its kind,
// its parsed header attributes, and the raw (still escaped, still
// newline-wrapped) content between this marker's own line and the next
// marker (or EOF).
type section struct {
	kind  string
	attrs map[string]string
	raw   string
}

// splitSections walks rest (everything after the frontmatter block) and
// separates the leading body block from the section list. bodyRaw is
// everything up to the first real marker line (isMarkerLine) or the whole
// of rest if there are none; each subsequent section's raw content is
// everything up to the NEXT marker line or EOF.
//
// Markers are found by a straightforward line scan, using cumulative byte
// offsets (the same technique parseFrontmatterBlock uses) so that
// reconstructing raw slices never goes through strings.Split/Join and risks
// losing an edge byte.
func splitSections(rest string) (bodyRaw string, sections []section) {
	lines := strings.Split(rest, "\n")

	type markerPos struct {
		lineIdx int
		offset  int
		kind    string
		attrs   map[string]string
	}
	var markers []markerPos

	offset := 0
	for i, line := range lines {
		if isMarkerLine(line) {
			if kind, attrs, ok := parseMarkerLine(line); ok {
				markers = append(markers, markerPos{lineIdx: i, offset: offset, kind: kind, attrs: attrs})
			}
		}
		offset += len(line) + 1
	}

	if len(markers) == 0 {
		return rest, nil
	}

	bodyRaw = rest[:markers[0].offset]

	for idx, m := range markers {
		// contentStart is the byte right after this marker line's own "\n".
		contentStart := m.offset + len(lines[m.lineIdx]) + 1
		contentEnd := len(rest)
		if idx+1 < len(markers) {
			contentEnd = markers[idx+1].offset
		}
		raw := ""
		if contentStart <= contentEnd {
			raw = rest[contentStart:contentEnd]
		}
		sections = append(sections, section{kind: m.kind, attrs: m.attrs, raw: raw})
	}

	return bodyRaw, sections
}
