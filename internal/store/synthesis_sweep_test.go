package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// sweepFixtureMemory builds an active memory for the sweep tests.
func sweepFixtureMemory(typ model.MemoryType, project, title, content string) *model.Memory {
	m := makeMemory()
	m.Type = typ
	m.Project = project
	m.Title = title
	m.Content = content
	return m
}

// memoryRowSnapshot returns every column of every memories row, keyed by id,
// so a test can prove which columns an UPDATE touched.
func memoryRowSnapshot(t *testing.T, s *MemoryStore) map[string]map[string]string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `SELECT * FROM memories ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("snapshot columns: %v", err)
	}
	out := map[string]map[string]string{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("snapshot scan: %v", err)
		}
		row := map[string]string{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				row[c] = string(b)
				continue
			}
			row[c] = fmt.Sprintf("%v", vals[i])
		}
		out[row["id"]] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows: %v", err)
	}
	return out
}

// TestSynthesisCounts_BytesNotRunes verifies the per-project measure: bytes
// are counted as bytes (multibyte text weighs more than its rune count),
// superseded syntheses are counted, soft-deleted ones and other types are
// not, and rows come grouped and ordered by project.
func TestSynthesisCounts_BytesNotRunes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	titleA, contentA := "síntesis ñ", "contenido con acentos: áéíóú — y un emoji 🧠"
	a, err := s.Create(ctx, sweepFixtureMemory(model.TypeSynthesis, "org/b", titleA, contentA))
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	superseded := sweepFixtureMemory(model.TypeSynthesis, "org/b", "plain", "ascii")
	superseded.SupersededBy = a.ID
	superseded.AccessCount = 4
	if _, err := s.Create(ctx, superseded); err != nil {
		t.Fatalf("Create superseded: %v", err)
	}
	gone, err := s.Create(ctx, sweepFixtureMemory(model.TypeSynthesis, "org/b", "gone", "deleted synthesis"))
	if err != nil {
		t.Fatalf("Create gone: %v", err)
	}
	if err := s.SoftDelete(ctx, gone.ID); err != nil {
		t.Fatalf("SoftDelete gone: %v", err)
	}
	if _, err := s.Create(ctx, sweepFixtureMemory(model.TypeSynthesis, "org/a", "x", "yy")); err != nil {
		t.Fatalf("Create org/a: %v", err)
	}
	if _, err := s.Create(ctx, sweepFixtureMemory(model.TypeDecision, "org/a", "human", "never counted")); err != nil {
		t.Fatalf("Create human: %v", err)
	}

	rows, err := s.SynthesisCounts(ctx)
	if err != nil {
		t.Fatalf("SynthesisCounts: %v", err)
	}
	want := []model.SynthesisSweepRow{
		{Project: "org/a", Synthesis: 1, NeverAccessed: 1, Bytes: 3},
		{Project: "org/b", Synthesis: 2, NeverAccessed: 1,
			Bytes: int64(len([]byte(titleA)) + len([]byte(contentA)) + len("plain") + len("ascii"))},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("rows[%d] = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if runes := int64(len([]rune(titleA)) + len([]rune(contentA)) + len("plain") + len("ascii")); rows[1].Bytes == runes {
		t.Errorf("Bytes = %d equals the rune count; it must count bytes", rows[1].Bytes)
	}
}

// TestSoftDeleteSyntheses_OnlyTouchesSynthesisDeletedAt verifies the only
// write of the sweep: it sets deleted_at on active synthesis rows and on
// nothing else, never deletes a row, and a project list limits it.
func TestSoftDeleteSyntheses_OnlyTouchesSynthesisDeletedAt(t *testing.T) {
	tests := []struct {
		name     string
		projects []string
		want     int
		retired  map[string]bool // project -> its syntheses must be retired
	}{
		{"whole database", nil, 3, map[string]bool{"org/a": true, "org/b": true}},
		{"one project", []string{"org/a"}, 2, map[string]bool{"org/a": true}},
		{"unknown project", []string{"org/zzz"}, 0, map[string]bool{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			for _, f := range []struct {
				typ     model.MemoryType
				project string
			}{
				{model.TypeSynthesis, "org/a"}, {model.TypeSynthesis, "org/a"}, {model.TypeSynthesis, "org/b"},
				{model.TypeDecision, "org/a"}, {model.TypeRule, "org/b"}, {model.TypeArchitecture, "org/a"},
			} {
				if _, err := s.Create(ctx, sweepFixtureMemory(f.typ, f.project, string(f.typ)+" "+f.project, "body")); err != nil {
					t.Fatalf("Create: %v", err)
				}
			}
			before := memoryRowSnapshot(t, s)

			n, err := s.SoftDeleteSyntheses(ctx, tc.projects)
			if err != nil {
				t.Fatalf("SoftDeleteSyntheses: %v", err)
			}
			if n != tc.want {
				t.Errorf("affected = %d, want %d", n, tc.want)
			}

			after := memoryRowSnapshot(t, s)
			if len(after) != len(before) {
				t.Fatalf("row count %d -> %d: a row was deleted", len(before), len(after))
			}
			for id, b := range before {
				a := after[id]
				shouldRetire := b["type"] == string(model.TypeSynthesis) && tc.retired[b["project"]]
				for col, v := range b {
					if col == "deleted_at" {
						continue
					}
					if a[col] != v {
						t.Errorf("row %s column %s changed: %q -> %q", id, col, v, a[col])
					}
				}
				retired := a["deleted_at"] != "<nil>" && a["deleted_at"] != ""
				if retired != shouldRetire {
					t.Errorf("row %s (%s, %s) retired = %v, want %v", id, b["type"], b["project"], retired, shouldRetire)
				}
			}
		})
	}
}

// TestCountActiveSyntheses verifies the count mneme status uses: active,
// not superseded, synthesis only, optionally limited to one project.
func TestCountActiveSyntheses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, err := s.Create(ctx, sweepFixtureMemory(model.TypeSynthesis, "org/a", "a", "a"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sup := sweepFixtureMemory(model.TypeSynthesis, "org/a", "sup", "sup")
	sup.SupersededBy = a.ID
	if _, err := s.Create(ctx, sup); err != nil {
		t.Fatalf("Create superseded: %v", err)
	}
	gone, err := s.Create(ctx, sweepFixtureMemory(model.TypeSynthesis, "org/a", "gone", "gone"))
	if err != nil {
		t.Fatalf("Create gone: %v", err)
	}
	if err := s.SoftDelete(ctx, gone.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	for _, f := range []*model.Memory{
		sweepFixtureMemory(model.TypeSynthesis, "org/b", "b", "b"),
		sweepFixtureMemory(model.TypeDecision, "org/a", "human", "human"),
	} {
		if _, err := s.Create(ctx, f); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	for _, tc := range []struct {
		project string
		want    int
	}{{"", 2}, {"org/a", 1}, {"org/b", 1}, {"org/none", 0}} {
		got, err := s.CountActiveSyntheses(ctx, tc.project)
		if err != nil {
			t.Fatalf("CountActiveSyntheses(%q): %v", tc.project, err)
		}
		if got != tc.want {
			t.Errorf("CountActiveSyntheses(%q) = %d, want %d", tc.project, got, tc.want)
		}
	}
}
