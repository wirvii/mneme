package sddfile

import (
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// specLiteralFixture builds the exact SpecRecord captured in SPEC-157's
// paso 0.2, against main @ a9a2f02, BEFORE renderSpec touched the history
// marker line. None of its history rows sets ReviewedSHA (zero value
// ""), so the produced bytes must be byte-identical to what renderSpec
// produced before this spec existed.
func specLiteralFixture() *SpecRecord {
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	spec := &model.Spec{
		ID:        "SPEC-999",
		Project:   "wirvii/mneme",
		Title:     "Capture fixture",
		Status:    model.SpecStatusDone,
		Lane:      model.LaneStandard,
		BacklogID: "BL-999",
		BaseSHA:   "abc123",
		CreatedAt: base,
		UpdatedAt: base.Add(3 * time.Hour),
	}
	return &SpecRecord{
		Spec: spec,
		History: []*model.SpecHistory{
			{ID: "h1", SpecID: spec.ID, FromStatus: "", ToStatus: model.SpecStatusDraft,
				By: "system", Reason: "spec created", At: base},
			{ID: "h2", SpecID: spec.ID, FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA,
				By: "backend", Reason: "ready for review", At: base.Add(time.Hour)},
			{ID: "h3", SpecID: spec.ID, FromStatus: model.SpecStatusQA, ToStatus: model.SpecStatusImplementing,
				By: "qa-tester",
				Reason: "rejected: missing tests\n\n[AC1] the coverage gate is red\nevidencia: go test output",
				At:     base.Add(2 * time.Hour)},
		},
		Pushbacks: []*model.SpecPushback{
			{ID: "p1", SpecID: spec.ID, FromAgent: "architect",
				Questions: []string{"which field carries this?"}, Resolved: false, CreatedAt: base.Add(30 * time.Minute)},
		},
	}
}

// specLiteralBytes is the EXACT literal captured in paso 0.2, against
// main @ a9a2f02 (SHA recorded in changes.md), BEFORE renderSpec touched
// the history marker line at all.
const specLiteralBytes = `---
schema: 1
kind: spec
id: SPEC-999
project: wirvii/mneme
title: "Capture fixture"
status: done
lane: standard
backlog_id: BL-999
base_sha: abc123
created_at: 2026-09-24T10:00:00Z
updated_at: 2026-09-24T13:00:00Z
---

<!-- mneme:history id="h1" from="" to="draft" by="system" at="2026-09-24T10:00:00Z" -->
spec created
<!-- mneme:history id="h2" from="implementing" to="qa" by="backend" at="2026-09-24T11:00:00Z" -->
ready for review
<!-- mneme:history id="h3" from="qa" to="implementing" by="qa-tester" at="2026-09-24T12:00:00Z" -->
rejected: missing tests

[AC1] the coverage gate is red
evidencia: go test output
<!-- mneme:pushback id="p1" from_agent="architect" at="2026-09-24T10:30:00Z" resolved=false resolved_at="" -->
<!-- mneme:question -->
which field carries this?
<!-- mneme:resolution -->

`

// TestMarshalSpec_HistoryWithoutFrontierIsByteIdentical is SPEC-157 AC10
// (spec.md) / AC8 (criteria.toml): a spec whose history rows carry no
// reviewed_sha produces EXACTLY the bytes it produced before this spec
// touched renderSpec — captured in paso 0.2 against main @ a9a2f02, before
// any of SPEC-157's code existed. A dedicated literal comparison, not an
// inspection: if renderSpec ever appends the reviewed_sha attribute
// unconditionally, or changes its position, this test catches it exactly.
func TestMarshalSpec_HistoryWithoutFrontierIsByteIdentical(t *testing.T) {
	rec := specLiteralFixture()

	got, err := MarshalSpec(rec)
	if err != nil {
		t.Fatalf("MarshalSpec: %v", err)
	}
	if string(got) != specLiteralBytes {
		t.Fatalf("bytes changed for a record with no reviewed_sha data:\nwant=%q\ngot =%q", specLiteralBytes, got)
	}
	if strings.Contains(string(got), "reviewed_sha") {
		t.Fatalf("output must never mention reviewed_sha when every row's ReviewedSHA is empty:\n%s", got)
	}
}

// TestEqualSpecRecord_DetectsReviewedSHAMismatch is SPEC-157 AC11
// (spec.md) / AC7 (criteria.toml): equalSpecRecord must treat two records
// differing ONLY in one history row's ReviewedSHA as UNEQUAL. Losing this
// comparison is the single easiest way to silently lose the field — the
// round-trip check in MarshalSpec would then keep "succeeding" while
// discarding the SHA a caller asked it to write.
func TestEqualSpecRecord_DetectsReviewedSHAMismatch(t *testing.T) {
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	mk := func(sha string) *SpecRecord {
		return &SpecRecord{
			Spec: &model.Spec{ID: "SPEC-500", Title: "x", Status: model.SpecStatusDone,
				Project: "p", Lane: model.LaneStandard, CreatedAt: base, UpdatedAt: base},
			History: []*model.SpecHistory{
				{ID: "h1", SpecID: "SPEC-500", FromStatus: model.SpecStatusQA, ToStatus: model.SpecStatusDone,
					By: "qa-tester", Reason: "ok", At: base, ReviewedSHA: sha},
			},
		}
	}

	a := mk("9a3c1234")
	b := mk("9a3c1234")
	if !equalSpecRecord(a, b) {
		t.Fatal("records identical including ReviewedSHA must compare equal")
	}

	c := mk("deadbeef")
	if equalSpecRecord(a, c) {
		t.Fatal("records differing ONLY in one history row's ReviewedSHA must compare UNEQUAL")
	}
}

// TestSDDFile_SpecHistory_ReviewedSHARoundTrip covers a record whose
// history rows are a mix: two carry a reviewed_sha, one does not. The
// empty one must NOT carry the attribute in the produced bytes (D3); the
// two non-empty ones must round-trip their exact value.
func TestSDDFile_SpecHistory_ReviewedSHARoundTrip(t *testing.T) {
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	rec := &SpecRecord{
		Spec: &model.Spec{ID: "SPEC-600", Title: "mixed frontier", Status: model.SpecStatusDone,
			Project: "p", Lane: model.LaneStandard, CreatedAt: base, UpdatedAt: base},
		History: []*model.SpecHistory{
			{ID: "h1", SpecID: "SPEC-600", FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA,
				By: "backend", Reason: "entrega", At: base, ReviewedSHA: "aaa111"},
			{ID: "h2", SpecID: "SPEC-600", FromStatus: model.SpecStatusQA, ToStatus: model.SpecStatusImplementing,
				By: "qa-tester", Reason: "rechazo", At: base.Add(time.Hour), ReviewedSHA: ""},
			{ID: "h3", SpecID: "SPEC-600", FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA,
				By: "backend", Reason: "reentrega", At: base.Add(2 * time.Hour), ReviewedSHA: "bbb222"},
		},
	}

	data, err := MarshalSpec(rec)
	if err != nil {
		t.Fatalf("MarshalSpec: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	var historyLines []string
	for _, l := range lines {
		if strings.HasPrefix(l, "<!-- mneme:history ") {
			historyLines = append(historyLines, l)
		}
	}
	if len(historyLines) != 3 {
		t.Fatalf("expected 3 history marker lines, got %d:\n%s", len(historyLines), data)
	}
	if !strings.Contains(historyLines[0], `reviewed_sha="aaa111"`) {
		t.Errorf("h1 line missing reviewed_sha: %s", historyLines[0])
	}
	if strings.Contains(historyLines[1], "reviewed_sha") {
		t.Errorf("h2 line (empty ReviewedSHA) must not carry the attribute: %s", historyLines[1])
	}
	if !strings.Contains(historyLines[2], `reviewed_sha="bbb222"`) {
		t.Errorf("h3 line missing reviewed_sha: %s", historyLines[2])
	}

	roundTripSpec(t, rec)
}

// TestSDDFile_ReviewedSHA_UnknownAttributeToleratesOlderReader documents
// D3's compatibility guarantee at the parser level directly (parseMarkerLine
// already tolerates ANY unknown key=value pair generically — see
// sections.go — this test fixes that guarantee specifically for
// reviewed_sha, since it is the concrete case D3/docs/sdd-git-native.md
// name): a hand-built marker line carrying reviewed_sha parses cleanly, and
// a marker line with a genuinely unknown extra attribute ALSO parses
// cleanly (simulating an older mneme reading a file written by a newer
// one that added yet another attribute after this one).
func TestSDDFile_ReviewedSHA_UnknownAttributeToleratesOlderReader(t *testing.T) {
	data := []byte("---\n" +
		"schema: 1\nkind: spec\nid: SPEC-700\ntitle: \"x\"\nstatus: done\nlane: standard\n" +
		"created_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n" +
		"---\n\n" +
		`<!-- mneme:history id="h1" from="qa" to="done" by="qa-tester" at="2026-01-01T00:00:00Z" reviewed_sha="cafe" future_field="x" -->` + "\n" +
		"ok\n")

	rec, err := UnmarshalSpec(data)
	if err != nil {
		t.Fatalf("UnmarshalSpec with reviewed_sha + an unknown future attribute: %v", err)
	}
	if len(rec.History) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(rec.History))
	}
	if rec.History[0].ReviewedSHA != "cafe" {
		t.Errorf("ReviewedSHA = %q, want %q", rec.History[0].ReviewedSHA, "cafe")
	}
}
