package sddfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// --- Marshal nil-guard branches ---

func TestMarshalBacklog_NilRecordOrItem(t *testing.T) {
	if _, err := MarshalBacklog(nil); err == nil {
		t.Fatal("MarshalBacklog(nil) must fail")
	}
	if _, err := MarshalBacklog(&BacklogRecord{}); err == nil {
		t.Fatal("MarshalBacklog with a nil Item must fail")
	}
}

func TestMarshalSpec_NilRecordOrSpec(t *testing.T) {
	if _, err := MarshalSpec(nil); err == nil {
		t.Fatal("MarshalSpec(nil) must fail")
	}
	if _, err := MarshalSpec(&SpecRecord{}); err == nil {
		t.Fatal("MarshalSpec with a nil Spec must fail")
	}
}

// --- equality helper branches ---

func TestEqualBacklogItem_EveryFieldMismatch(t *testing.T) {
	base := &model.BacklogItem{
		ID: "BL-001", UUID: "u1", Project: "p", Title: "t", Description: "d",
		Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Lane: model.LaneStandard,
		Scope: "s", SpecID: "SPEC-001", ArchiveReason: "r", Position: 1,
		PreviousIDs: []model.PreviousID{{ID: "BL-000", Origin: "local", Reason: "enable-collision", At: time.Unix(0, 0).UTC()}},
		CreatedAt:   time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(200, 0).UTC(),
	}
	clone := func() *model.BacklogItem {
		c := *base
		c.PreviousIDs = append([]model.PreviousID{}, base.PreviousIDs...)
		return &c
	}

	if !equalBacklogItem(base, clone()) {
		t.Fatal("identical clones must compare equal")
	}

	mutators := []func(*model.BacklogItem){
		func(i *model.BacklogItem) { i.ID = "BL-999" },
		func(i *model.BacklogItem) { i.UUID = "other" },
		func(i *model.BacklogItem) { i.Project = "other" },
		func(i *model.BacklogItem) { i.Title = "other" },
		func(i *model.BacklogItem) { i.Description = "other" },
		func(i *model.BacklogItem) { i.Status = model.BacklogStatusRefined },
		func(i *model.BacklogItem) { i.Priority = model.PriorityHigh },
		func(i *model.BacklogItem) { i.Lane = model.LaneTrivial },
		func(i *model.BacklogItem) { i.Scope = "other" },
		func(i *model.BacklogItem) { i.SpecID = "SPEC-999" },
		func(i *model.BacklogItem) { i.ArchiveReason = "other" },
		func(i *model.BacklogItem) { i.Position = 99 },
		func(i *model.BacklogItem) { i.PreviousIDs = nil },
		func(i *model.BacklogItem) { i.PreviousIDs[0].ID = "BL-777" },
		func(i *model.BacklogItem) { i.CreatedAt = time.Unix(999, 0).UTC() },
		func(i *model.BacklogItem) { i.UpdatedAt = time.Unix(999, 0).UTC() },
	}
	for i, mutate := range mutators {
		mutant := clone()
		mutate(mutant)
		if equalBacklogItem(base, mutant) {
			t.Errorf("mutator %d: expected mismatch to be detected", i)
		}
	}
}

func TestEqualSpec_EveryFieldMismatch(t *testing.T) {
	base := &model.Spec{
		ID: "SPEC-001", UUID: "u1", Project: "p", Title: "t", Status: model.SpecStatusDraft,
		Lane: model.LaneStandard, Scope: "s", BacklogID: "BL-001", BaseSHA: "abc",
		AssignedAgents: []string{"backend"}, FilesChanged: []string{"a.go"},
		PreviousIDs: []model.PreviousID{{ID: "SPEC-000", Origin: "repo", Reason: "add-add", At: time.Unix(0, 0).UTC()}},
		CreatedAt:   time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(200, 0).UTC(),
	}
	clone := func() *model.Spec {
		c := *base
		c.AssignedAgents = append([]string{}, base.AssignedAgents...)
		c.FilesChanged = append([]string{}, base.FilesChanged...)
		c.PreviousIDs = append([]model.PreviousID{}, base.PreviousIDs...)
		return &c
	}
	if !equalSpec(base, clone()) {
		t.Fatal("identical clones must compare equal")
	}

	mutators := []func(*model.Spec){
		func(s *model.Spec) { s.ID = "SPEC-999" },
		func(s *model.Spec) { s.UUID = "other" },
		func(s *model.Spec) { s.Project = "other" },
		func(s *model.Spec) { s.Title = "other" },
		func(s *model.Spec) { s.Status = model.SpecStatusDone },
		func(s *model.Spec) { s.Lane = model.LaneTrivial },
		func(s *model.Spec) { s.Scope = "other" },
		func(s *model.Spec) { s.BacklogID = "BL-999" },
		func(s *model.Spec) { s.BaseSHA = "def" },
		func(s *model.Spec) { s.AssignedAgents = []string{"frontend"} },
		func(s *model.Spec) { s.FilesChanged = []string{"b.go"} },
		func(s *model.Spec) { s.PreviousIDs = nil },
		func(s *model.Spec) { s.PreviousIDs[0].Reason = "enable-collision" },
		func(s *model.Spec) { s.CreatedAt = time.Unix(999, 0).UTC() },
		func(s *model.Spec) { s.UpdatedAt = time.Unix(999, 0).UTC() },
	}
	for i, mutate := range mutators {
		mutant := clone()
		mutate(mutant)
		if equalSpec(base, mutant) {
			t.Errorf("mutator %d: expected mismatch to be detected", i)
		}
	}
}

func TestEqualPushback_EveryFieldMismatch(t *testing.T) {
	resolvedAt := time.Unix(300, 0).UTC()
	base := &model.SpecPushback{
		ID: "pb1", SpecID: "SPEC-001", FromAgent: "architect",
		Questions: []string{"q1", "q2"}, Resolved: true, Resolution: "ok",
		CreatedAt: time.Unix(100, 0).UTC(), ResolvedAt: &resolvedAt,
	}
	clone := func() *model.SpecPushback {
		c := *base
		c.Questions = append([]string{}, base.Questions...)
		ra := *base.ResolvedAt
		c.ResolvedAt = &ra
		return &c
	}
	if !equalPushback(base, clone()) {
		t.Fatal("identical clones must compare equal")
	}

	mutators := []func(*model.SpecPushback){
		func(p *model.SpecPushback) { p.ID = "other" },
		func(p *model.SpecPushback) { p.SpecID = "other" },
		func(p *model.SpecPushback) { p.FromAgent = "other" },
		func(p *model.SpecPushback) { p.Resolved = false },
		func(p *model.SpecPushback) { p.Resolution = "other" },
		func(p *model.SpecPushback) { p.Questions = []string{"different"} },
		func(p *model.SpecPushback) { p.CreatedAt = time.Unix(999, 0).UTC() },
		func(p *model.SpecPushback) { p.ResolvedAt = nil },
		func(p *model.SpecPushback) { ra := time.Unix(999, 0).UTC(); p.ResolvedAt = &ra },
	}
	for i, mutate := range mutators {
		mutant := clone()
		mutate(mutant)
		if equalPushback(base, mutant) {
			t.Errorf("mutator %d: expected mismatch to be detected", i)
		}
	}
}

func TestEqualStringSlices(t *testing.T) {
	if !equalStringSlices(nil, nil) {
		t.Error("two nil slices must compare equal")
	}
	if !equalStringSlices([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("identical slices must compare equal")
	}
	if equalStringSlices([]string{"a"}, []string{"a", "b"}) {
		t.Error("different-length slices must not compare equal")
	}
	if equalStringSlices([]string{"a"}, []string{"b"}) {
		t.Error("different-content slices must not compare equal")
	}
}

// --- frontmatter writer edge cases ---

func TestFmWriter_ListWithNoItems(t *testing.T) {
	w := &fmWriter{}
	w.list("empty_list", nil)
	if w.String() != "" {
		t.Errorf("list() with no items must write nothing, got %q", w.String())
	}
}

func TestFmWriter_OmitQuotedEmpty(t *testing.T) {
	w := &fmWriter{}
	w.omitQuoted("archive_reason", "")
	if w.String() != "" {
		t.Errorf("omitQuoted() with an empty value must write nothing, got %q", w.String())
	}
}

// --- parseFrontmatterBlock error paths ---

func TestParseFrontmatterBlock_MissingOpeningDelimiter(t *testing.T) {
	_, _, err := parseFrontmatterBlock([]byte("id: x\n---\n"))
	if err == nil {
		t.Fatal("expected an error for a missing opening delimiter")
	}
}

func TestParseFrontmatterBlock_MissingClosingDelimiter(t *testing.T) {
	_, _, err := parseFrontmatterBlock([]byte("---\nid: x\n"))
	if err == nil {
		t.Fatal("expected an error for a missing closing delimiter")
	}
}

func TestParseFrontmatterBlock_MalformedLine(t *testing.T) {
	_, _, err := parseFrontmatterBlock([]byte("---\nnot a valid line at all\n---\n"))
	if err == nil {
		t.Fatal("expected an error for a malformed frontmatter line")
	}
}

func TestParseFrontmatterBlock_ListItemWithoutHeader(t *testing.T) {
	_, _, err := parseFrontmatterBlock([]byte("---\n  - orphan item\n---\n"))
	if err == nil {
		t.Fatal("expected an error for a list item with no preceding header")
	}
}

// --- schema range, the "too old" branch ---

func TestCheckSchema_TooOld(t *testing.T) {
	err := checkSchema(MinFileSchema - 1)
	if err == nil {
		t.Fatal("a schema below MinFileSchema must be rejected")
	}
	if !strings.Contains(err.Error(), "older than supported") {
		t.Errorf("error does not mention the range violated: %v", err)
	}
}

// --- conflict markers, D37 ---

func TestUnmarshalBacklog_RejectsConflictMarkers(t *testing.T) {
	data := []byte("<<<<<<< HEAD\nsome content\n=======\nother content\n>>>>>>> branch\n")
	if _, err := UnmarshalBacklog(data); err == nil {
		t.Fatal("a file with conflict markers must be rejected")
	}
}

func TestUnmarshalSpec_RejectsConflictMarkers(t *testing.T) {
	data := []byte("<<<<<<< HEAD\nsome content\n=======\nother content\n>>>>>>> branch\n")
	if _, err := UnmarshalSpec(data); err == nil {
		t.Fatal("a file with conflict markers must be rejected")
	}
}

// --- Unmarshal unexpected section kinds ---

func TestUnmarshalBacklog_UnexpectedSectionKind(t *testing.T) {
	base := mustTime(t, "2026-01-01T00:00:00Z")
	spec := &SpecRecord{Spec: &model.Spec{
		ID: "SPEC-777", Title: "x", Status: model.SpecStatusDraft, Project: "p",
		Lane: model.LaneStandard, CreatedAt: base, UpdatedAt: base,
	}, History: []*model.SpecHistory{
		{ID: "h1", SpecID: "SPEC-777", FromStatus: model.SpecStatusDraft, ToStatus: model.SpecStatusSpeccing, By: "x", At: base},
	}}
	specData, err := MarshalSpec(spec)
	if err != nil {
		t.Fatalf("MarshalSpec fixture: %v", err)
	}

	// Splice the spec's history-marker section into a backlog frontmatter:
	// UnmarshalBacklog only ever expects "refinement" sections, so a
	// "history" section must be rejected.
	idx := strings.Index(string(specData), "<!-- mneme:history")
	if idx < 0 {
		t.Fatal("fixture does not contain a history marker")
	}
	backlogHead := "---\nschema: 1\nkind: backlog\nid: BL-777\ntitle: \"x\"\nstatus: raw\npriority: medium\nlane: standard\nposition: 0\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n---\n\n"
	hostile := backlogHead + string(specData[idx:])

	if _, err := UnmarshalBacklog([]byte(hostile)); err == nil {
		t.Fatal("a backlog record with a history section must be rejected")
	}
}

func TestUnmarshalSpec_UnexpectedSectionKind(t *testing.T) {
	base := mustTime(t, "2026-01-01T00:00:00Z")
	backlog := &BacklogRecord{
		Item: &model.BacklogItem{
			ID: "BL-777", Title: "x", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
			Project: "p", Lane: model.LaneStandard, CreatedAt: base, UpdatedAt: base,
		},
		Refinements: []*model.BacklogRefinement{{ItemID: "BL-777", Seq: 1, Body: "x", At: base}},
	}
	backlogData, err := MarshalBacklog(backlog)
	if err != nil {
		t.Fatalf("MarshalBacklog fixture: %v", err)
	}
	idx := strings.Index(string(backlogData), "<!-- mneme:refinement")
	if idx < 0 {
		t.Fatal("fixture does not contain a refinement marker")
	}
	specHead := "---\nschema: 1\nkind: spec\nid: SPEC-777\ntitle: \"x\"\nstatus: draft\nlane: standard\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n---\n\n"
	hostile := specHead + string(backlogData[idx:])

	if _, err := UnmarshalSpec([]byte(hostile)); err == nil {
		t.Fatal("a spec record with a refinement section must be rejected")
	}
}

// --- schema field present but garbage ---

func TestUnmarshalBacklog_GarbageSchemaField(t *testing.T) {
	data := []byte("---\nschema: not-a-number\nkind: backlog\nid: BL-1\ntitle: \"x\"\nstatus: raw\npriority: medium\nlane: standard\nposition: 0\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n---\n\n")
	// parseIntField defaults an unparseable value to 0, which is BELOW
	// MinFileSchema — this must be rejected, not silently treated as 1.
	if _, err := UnmarshalBacklog(data); err == nil {
		t.Fatal("a non-numeric schema field must be rejected")
	}
}

// --- parseTimeField ---

func TestParseTimeField_Invalid(t *testing.T) {
	if _, ok := parseTimeField("not-a-time"); ok {
		t.Fatal("an unparseable timestamp must return ok=false")
	}
}

// --- io.go error paths ---

func TestWriteRecord_MkdirFailsWhenParentIsAFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	target := filepath.Join(blocker, "sub", "file.md")
	if err := WriteRecord(target, []byte("data")); err == nil {
		t.Fatal("WriteRecord must fail when a path component is a regular file, not a directory")
	}
}

func TestReadRecord_MissingFile(t *testing.T) {
	if _, err := ReadRecord(filepath.Join(t.TempDir(), "does-not-exist.md")); err == nil {
		t.Fatal("ReadRecord on a missing file must fail")
	}
}

func TestCleanStaleTmp_NonExistentDirIsNotAnError(t *testing.T) {
	if err := CleanStaleTmp(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("CleanStaleTmp on a missing dir must not error, got: %v", err)
	}
}

// --- marker.go error paths ---

func TestReadMarker_CorruptJSON(t *testing.T) {
	repoRoot := t.TempDir()
	path := MarkerPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}
	if _, err := ReadMarker(repoRoot); err == nil {
		t.Fatal("ReadMarker on corrupt JSON must fail")
	}
}
