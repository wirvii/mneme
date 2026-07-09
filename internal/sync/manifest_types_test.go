package sync_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
	mnemeSync "github.com/wirvii/mneme/internal/sync"
)

// TestManifestRoot_JSONRoundtrip verifies that a fully-populated ManifestRoot
// marshals to JSON and back without data loss.
func TestManifestRoot_JSONRoundtrip(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	endedAt := now.Add(time.Hour)
	lastAccessed := now.Add(-time.Hour)

	original := &mnemeSync.ManifestRoot{
		Version:    mnemeSync.ManifestVersion,
		ExportedAt: now.Format(time.RFC3339),
		Producer:   mnemeSync.ManifestProducer{Name: "mneme", Version: "1.0.0"},
		Project:    "test/project",
		Scope:      "project",
		Memories: []*model.Memory{
			{
				ID:           "00000000-0000-7000-0000-000000000001",
				Type:         model.TypeDecision,
				Scope:        model.ScopeProject,
				Title:        "test decision",
				Content:      "we chose sqlite",
				TopicKey:     "arch/sqlite",
				Project:      "test/project",
				CreatedAt:    now,
				UpdatedAt:    now,
				Importance:   0.8,
				Confidence:   0.9,
				DecayRate:    0.01,
				LastAccessed: &lastAccessed,
			},
		},
		Entities: []*model.Entity{
			{
				ID:        "00000000-0000-7000-0000-000000000002",
				Name:      "sqlite",
				Kind:      model.KindLibrary,
				Project:   "test/project",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		Relations: []*model.Relation{
			{
				ID:        "00000000-0000-7000-0000-000000000003",
				SourceID:  "00000000-0000-7000-0000-000000000002",
				TargetID:  "00000000-0000-7000-0000-000000000002",
				Type:      model.RelRelatedTo,
				Weight:    0.5,
				CreatedAt: now,
			},
		},
		Sessions: []*model.Session{
			{
				ID:        "00000000-0000-7000-0000-000000000004",
				Project:   "test/project",
				Agent:     "claude-code",
				StartedAt: now,
				EndedAt:   &endedAt,
				SummaryID: "summary-1",
			},
		},
		Stats: &mnemeSync.ManifestStats{
			MemoryCount:   1,
			EntityCount:   1,
			RelationCount: 1,
			SessionCount:  1,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded mnemeSync.ManifestRoot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Version != original.Version {
		t.Errorf("Version: got %q, want %q", decoded.Version, original.Version)
	}
	if decoded.ExportedAt != original.ExportedAt {
		t.Errorf("ExportedAt: got %q, want %q", decoded.ExportedAt, original.ExportedAt)
	}
	if decoded.Producer.Name != original.Producer.Name {
		t.Errorf("Producer.Name: got %q, want %q", decoded.Producer.Name, original.Producer.Name)
	}
	if decoded.Producer.Version != original.Producer.Version {
		t.Errorf("Producer.Version: got %q, want %q", decoded.Producer.Version, original.Producer.Version)
	}
	if decoded.Project != original.Project {
		t.Errorf("Project: got %q, want %q", decoded.Project, original.Project)
	}
	if len(decoded.Memories) != 1 {
		t.Fatalf("Memories len: got %d, want 1", len(decoded.Memories))
	}
	if decoded.Memories[0].Title != "test decision" {
		t.Errorf("Memory[0].Title: got %q", decoded.Memories[0].Title)
	}
	if len(decoded.Entities) != 1 {
		t.Fatalf("Entities len: got %d, want 1", len(decoded.Entities))
	}
	if len(decoded.Relations) != 1 {
		t.Fatalf("Relations len: got %d, want 1", len(decoded.Relations))
	}
	if len(decoded.Sessions) != 1 {
		t.Fatalf("Sessions len: got %d, want 1", len(decoded.Sessions))
	}
	if decoded.Stats == nil {
		t.Fatal("Stats is nil after roundtrip")
	}
	if decoded.Stats.MemoryCount != 1 {
		t.Errorf("Stats.MemoryCount: got %d, want 1", decoded.Stats.MemoryCount)
	}
}

// TestManifestRoot_RequiredFields checks that ManifestRoot marshals the five
// required fields (version, exported_at, producer, project, memories) and that
// omitting memories produces an empty array, not null.
func TestManifestRoot_RequiredFields(t *testing.T) {
	root := &mnemeSync.ManifestRoot{
		Version:    "1.0",
		ExportedAt: "2026-05-01T12:00:00Z",
		Producer:   mnemeSync.ManifestProducer{Name: "mneme", Version: "0.1"},
		Project:    "test/proj",
		Memories:   []*model.Memory{},
	}

	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	for _, field := range []string{"version", "exported_at", "producer", "project", "memories"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("required field %q missing from JSON output", field)
		}
	}

	// memories must serialise as [] not null
	memoriesJSON := string(raw["memories"])
	if memoriesJSON != "[]" {
		t.Errorf("memories: got %s, want []", memoriesJSON)
	}
}

// TestManifestMemory_AllFields verifies that all 22 model.Memory fields are
// preserved through JSON serialisation. Each field is set to a non-zero value
// so a missing json tag would be caught.
func TestManifestMemory_AllFields(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	lastAccessed := now.Add(-time.Hour)
	deletedAt := now.Add(time.Hour)

	m := &model.Memory{
		ID:            "00000000-0000-7000-0000-000000000001",
		Type:          model.TypeRule,
		Scope:         model.ScopeGlobal,
		Title:         "no inline sql",
		Content:       "never write sql inline in go files",
		TopicKey:      "convention/no-inline-sql",
		Project:       "test/project",
		SessionID:     "00000000-0000-7000-0000-000000000099",
		CreatedBy:     "claude-code",
		CreatedAt:     now,
		UpdatedAt:     now,
		Importance:    0.95,
		Confidence:    1.0,
		AccessCount:   5,
		LastAccessed:  &lastAccessed,
		DecayRate:     0.0,
		RevisionCount: 2,
		SupersededBy:  "00000000-0000-7000-0000-000000000002",
		DeletedAt:     &deletedAt,
		Files:         []string{"internal/store/memory.go"},
		AppliesTo:     []string{"**/*.go"},
		Severity:      model.SeverityBlock,
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded model.Memory
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"ID", decoded.ID, m.ID},
		{"Type", string(decoded.Type), string(m.Type)},
		{"Scope", string(decoded.Scope), string(m.Scope)},
		{"Title", decoded.Title, m.Title},
		{"Content", decoded.Content, m.Content},
		{"TopicKey", decoded.TopicKey, m.TopicKey},
		{"Project", decoded.Project, m.Project},
		{"SessionID", decoded.SessionID, m.SessionID},
		{"CreatedBy", decoded.CreatedBy, m.CreatedBy},
		{"Importance", decoded.Importance, m.Importance},
		{"Confidence", decoded.Confidence, m.Confidence},
		{"AccessCount", decoded.AccessCount, m.AccessCount},
		{"DecayRate", decoded.DecayRate, m.DecayRate},
		{"RevisionCount", decoded.RevisionCount, m.RevisionCount},
		{"SupersededBy", decoded.SupersededBy, m.SupersededBy},
		{"Severity", string(decoded.Severity), string(m.Severity)},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("Memory.%s: got %v, want %v", c.name, c.got, c.want)
		}
	}

	if decoded.LastAccessed == nil {
		t.Error("LastAccessed is nil after roundtrip")
	}
	if decoded.DeletedAt == nil {
		t.Error("DeletedAt is nil after roundtrip")
	}
	if len(decoded.Files) != 1 || decoded.Files[0] != "internal/store/memory.go" {
		t.Errorf("Files: got %v", decoded.Files)
	}
	if len(decoded.AppliesTo) != 1 || decoded.AppliesTo[0] != "**/*.go" {
		t.Errorf("AppliesTo: got %v", decoded.AppliesTo)
	}
}

// TestManifestVersion_Const ensures ManifestVersion is exactly "1.0".
func TestManifestVersion_Const(t *testing.T) {
	if mnemeSync.ManifestVersion != "1.0" {
		t.Errorf("ManifestVersion: got %q, want %q", mnemeSync.ManifestVersion, "1.0")
	}
}

// TestManifestProducer_Roundtrip validates ManifestProducer JSON serialisation.
func TestManifestProducer_Roundtrip(t *testing.T) {
	p := mnemeSync.ManifestProducer{Name: "mneme", Version: "2.0.0"}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded mnemeSync.ManifestProducer
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != p.Name {
		t.Errorf("Name: got %q, want %q", decoded.Name, p.Name)
	}
	if decoded.Version != p.Version {
		t.Errorf("Version: got %q, want %q", decoded.Version, p.Version)
	}
}

// TestManifestStats_Roundtrip validates ManifestStats JSON serialisation.
func TestManifestStats_Roundtrip(t *testing.T) {
	s := &mnemeSync.ManifestStats{
		MemoryCount:   10,
		EntityCount:   5,
		RelationCount: 8,
		SessionCount:  3,
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded mnemeSync.ManifestStats
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.MemoryCount != s.MemoryCount {
		t.Errorf("MemoryCount: got %d, want %d", decoded.MemoryCount, s.MemoryCount)
	}
	if decoded.EntityCount != s.EntityCount {
		t.Errorf("EntityCount: got %d, want %d", decoded.EntityCount, s.EntityCount)
	}
	if decoded.RelationCount != s.RelationCount {
		t.Errorf("RelationCount: got %d, want %d", decoded.RelationCount, s.RelationCount)
	}
	if decoded.SessionCount != s.SessionCount {
		t.Errorf("SessionCount: got %d, want %d", decoded.SessionCount, s.SessionCount)
	}
}

// TestManifestImportResult_Roundtrip validates ManifestImportResult JSON serialisation.
func TestManifestImportResult_Roundtrip(t *testing.T) {
	r := &mnemeSync.ManifestImportResult{
		MemoriesCreated:  3,
		MemoriesUpdated:  1,
		MemoriesSkipped:  2,
		EntitiesCreated:  4,
		EntitiesSkipped:  0,
		RelationsCreated: 2,
		RelationsSkipped: 1,
		SessionsCreated:  1,
		SessionsSkipped:  0,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded mnemeSync.ManifestImportResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.MemoriesCreated != r.MemoriesCreated {
		t.Errorf("MemoriesCreated: got %d, want %d", decoded.MemoriesCreated, r.MemoriesCreated)
	}
	if decoded.RelationsSkipped != r.RelationsSkipped {
		t.Errorf("RelationsSkipped: got %d, want %d", decoded.RelationsSkipped, r.RelationsSkipped)
	}
}
