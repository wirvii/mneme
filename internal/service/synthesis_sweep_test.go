package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// sweepSeed describes one memory the sweep fixture writes.
type sweepSeed struct {
	typ         model.MemoryType
	project     string
	title       string
	content     string
	accessCount int
}

// writeSweepDB creates a real database file at path, inserts seeds and
// closes it, so the sweep opens it from scratch like it would on disk.
func writeSweepDB(t *testing.T, path string, seeds ...sweepSeed) {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	s := store.NewMemoryStore(d)
	for _, sd := range seeds {
		scope := model.ScopeProject
		if sd.project == "" {
			scope = model.ScopeGlobal
		}
		if _, err := s.Create(context.Background(), &model.Memory{
			Type:        sd.typ,
			Scope:       scope,
			Project:     sd.project,
			Title:       sd.title,
			Content:     sd.content,
			Importance:  0.5,
			AccessCount: sd.accessCount,
		}); err != nil {
			t.Fatalf("seed %s in %s: %v", sd.title, path, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// sweepFixture builds the common data dir: two project databases, global.db
// and a code graph database that holds a synthesis but must be ignored.
// It returns the config pointing at it.
func sweepFixture(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	writeSweepDB(t, filepath.Join(dir, "projects", "org-a.db"),
		sweepSeed{model.TypeSynthesis, "org/a", "sa1", "1234567890", 0},
		sweepSeed{model.TypeSynthesis, "org/a", "sa2", "12345", 3},
		sweepSeed{model.TypeDecision, "org/a", "human a", "keep me", 0},
	)
	writeSweepDB(t, filepath.Join(dir, "projects", "org-b.db"),
		sweepSeed{model.TypeSynthesis, "org/b", "sb1", "abc", 0},
		sweepSeed{model.TypeConvention, "org/b", "human b", "keep me too", 0},
	)
	writeSweepDB(t, filepath.Join(dir, "global.db"),
		sweepSeed{model.TypeSynthesis, "", "sg1", "global", 0},
		sweepSeed{model.TypePreference, "", "human g", "global human", 0},
	)
	writeSweepDB(t, filepath.Join(dir, "projects", "org-a-codegraph.db"),
		sweepSeed{model.TypeSynthesis, "org/a", "codegraph", "must not be counted", 0},
	)
	cfg := config.Default()
	cfg.Storage.DataDir = dir
	return cfg
}

// dbFingerprint captures what must not change in a dry run: rows per type,
// a SHA-256 over every column of memories ordered by id, and the file mtime.
type dbFingerprint struct {
	perType  string
	checksum string
	mtime    time.Time
}

func fingerprint(t *testing.T, path string) dbFingerprint {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	d, err := db.OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer d.Close()

	var perType string
	if err := d.QueryRow(`SELECT COALESCE(group_concat(type || '=' || n, ','), '') FROM
		(SELECT type, COUNT(*) AS n FROM memories GROUP BY type ORDER BY type)`).Scan(&perType); err != nil {
		t.Fatalf("per type %s: %v", path, err)
	}
	rows, err := d.Query(`SELECT * FROM memories ORDER BY id`)
	if err != nil {
		t.Fatalf("rows %s: %v", path, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	h := sha256.New()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", path, err)
		}
		for _, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			fmt.Fprintf(h, "%v\x1f", v)
		}
		h.Write([]byte{'\x1e'})
	}
	return dbFingerprint{perType: perType, checksum: hex.EncodeToString(h.Sum(nil)), mtime: info.ModTime()}
}

// allDBs lists every database file of the fixture.
func allDBs(cfg *config.Config) []string {
	return []string{
		cfg.ProjectDBPath("org-a"), cfg.ProjectDBPath("org-b"),
		cfg.GlobalDBPath(), filepath.Join(cfg.Storage.DataDir, "projects", "org-a-codegraph.db"),
	}
}

// TestSynthesisSweep_DryRun_ReportsAndLeavesDBsUntouched verifies the
// default mode: the measure per (database, project) in PL4 order with
// global.db last, and every database identical afterwards.
func TestSynthesisSweep_DryRun_ReportsAndLeavesDBsUntouched(t *testing.T) {
	cfg := sweepFixture(t)
	before := map[string]dbFingerprint{}
	for _, p := range allDBs(cfg) {
		before[p] = fingerprint(t, p)
	}

	report, err := service.SweepSyntheses(context.Background(), cfg, model.SynthesisSweepRequest{})
	if err != nil {
		t.Fatalf("SweepSyntheses: %v", err)
	}
	want := []model.SynthesisSweepRow{
		{Project: "org/a", Database: "projects/org-a.db", Synthesis: 2, NeverAccessed: 1, Bytes: int64(len("sa1") + len("1234567890") + len("sa2") + len("12345"))},
		{Project: "org/b", Database: "projects/org-b.db", Synthesis: 1, NeverAccessed: 1, Bytes: int64(len("sb1") + len("abc"))},
		{Project: "", Database: "global.db", Synthesis: 1, NeverAccessed: 1, Bytes: int64(len("sg1") + len("global"))},
	}
	if report.Apply {
		t.Error("report.Apply = true in a dry run")
	}
	if len(report.Rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", report.Rows, want)
	}
	for i := range want {
		if report.Rows[i] != want[i] {
			t.Errorf("rows[%d] = %+v, want %+v", i, report.Rows[i], want[i])
		}
	}
	for _, p := range allDBs(cfg) {
		if after := fingerprint(t, p); after != before[p] {
			t.Errorf("%s changed in a dry run: %+v -> %+v", p, before[p], after)
		}
	}
}

// TestSynthesisSweep_DryRun_SkipsCodegraphDB verifies that *-codegraph.db
// files are never visited, even when they hold synthesis rows.
func TestSynthesisSweep_DryRun_SkipsCodegraphDB(t *testing.T) {
	cfg := sweepFixture(t)
	report, err := service.SweepSyntheses(context.Background(), cfg, model.SynthesisSweepRequest{})
	if err != nil {
		t.Fatalf("SweepSyntheses: %v", err)
	}
	for _, r := range report.Rows {
		if r.Database == "projects/org-a-codegraph.db" {
			t.Errorf("code graph database reported: %+v", r)
		}
	}
}

// TestSynthesisSweep_DryRun_PartialError verifies that an unreadable file
// is reported and the sweep still measures the healthy databases.
func TestSynthesisSweep_DryRun_PartialError(t *testing.T) {
	cfg := sweepFixture(t)
	broken := filepath.Join(cfg.Storage.DataDir, "projects", "broken.db")
	if err := os.WriteFile(broken, []byte("this is not a sqlite database, only garbage bytes"), 0o600); err != nil {
		t.Fatalf("write broken db: %v", err)
	}

	report, err := service.SweepSyntheses(context.Background(), cfg, model.SynthesisSweepRequest{})
	if !errors.Is(err, service.ErrSweepPartial) {
		t.Fatalf("err = %v, want ErrSweepPartial", err)
	}
	if report == nil || len(report.Rows) != 3 {
		t.Fatalf("report rows = %+v, want the 3 healthy rows", report)
	}
	if len(report.Errors) != 1 || report.Errors[0].Database != "projects/broken.db" {
		t.Errorf("errors = %+v, want one for projects/broken.db", report.Errors)
	}
}

// memoryColumns returns every column of every memories row keyed by id.
func memoryColumns(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	d, err := db.OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer d.Close()
	rows, err := d.Query(`SELECT * FROM memories ORDER BY id`)
	if err != nil {
		t.Fatalf("query %s: %v", path, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := map[string]map[string]string{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		row := map[string]string{}
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[c] = fmt.Sprintf("%v", v)
		}
		out[row["id"]] = row
	}
	return out
}

// TestSynthesisSweep_Apply_RetiresOnlySyntheses verifies --apply: every
// synthesis gets deleted_at and nothing else changes, human rows are
// identical, no row disappears, and each row reports Retired == Synthesis.
func TestSynthesisSweep_Apply_RetiresOnlySyntheses(t *testing.T) {
	cfg := sweepFixture(t)
	paths := []string{cfg.ProjectDBPath("org-a"), cfg.ProjectDBPath("org-b"), cfg.GlobalDBPath()}
	before := map[string]map[string]map[string]string{}
	for _, p := range paths {
		before[p] = memoryColumns(t, p)
	}

	report, err := service.SweepSyntheses(context.Background(), cfg, model.SynthesisSweepRequest{Apply: true})
	if err != nil {
		t.Fatalf("SweepSyntheses apply: %v", err)
	}
	if !report.Apply {
		t.Error("report.Apply = false with Apply")
	}
	for _, r := range report.Rows {
		if r.Retired != r.Synthesis {
			t.Errorf("row %+v: Retired != Synthesis", r)
		}
	}
	if tot := report.Totals(); tot.Retired != 4 {
		t.Errorf("total retired = %d, want 4", tot.Retired)
	}

	for _, p := range paths {
		after := memoryColumns(t, p)
		if len(after) != len(before[p]) {
			t.Fatalf("%s: rows %d -> %d", p, len(before[p]), len(after))
		}
		for id, b := range before[p] {
			a := after[id]
			isSynth := b["type"] == string(model.TypeSynthesis)
			for col, v := range b {
				if col == "deleted_at" && isSynth {
					continue
				}
				if a[col] != v {
					t.Errorf("%s row %s (%s) column %s: %q -> %q", p, id, b["type"], col, v, a[col])
				}
			}
			if isSynth && a["deleted_at"] == "<nil>" {
				t.Errorf("%s synthesis %s not retired", p, id)
			}
		}
	}
	codegraph := filepath.Join(cfg.Storage.DataDir, "projects", "org-a-codegraph.db")
	for id, row := range memoryColumns(t, codegraph) {
		if row["deleted_at"] != "<nil>" {
			t.Errorf("code graph row %s retired", id)
		}
	}
}

// TestSynthesisSweep_Apply_ProjectScoped verifies that --project (here in
// the file form) limits the retirement to that database.
func TestSynthesisSweep_Apply_ProjectScoped(t *testing.T) {
	cfg := sweepFixture(t)
	untouched := []string{cfg.ProjectDBPath("org-b"), cfg.GlobalDBPath()}
	before := map[string]dbFingerprint{}
	for _, p := range untouched {
		before[p] = fingerprint(t, p)
	}

	report, err := service.SweepSyntheses(context.Background(), cfg, model.SynthesisSweepRequest{Project: "org-a", Apply: true})
	if err != nil {
		t.Fatalf("SweepSyntheses: %v", err)
	}
	if len(report.Rows) != 1 || report.Rows[0].Project != "org/a" || report.Rows[0].Retired != 2 {
		t.Fatalf("rows = %+v, want one org/a row with 2 retired", report.Rows)
	}
	for _, p := range untouched {
		if after := fingerprint(t, p); after != before[p] {
			t.Errorf("%s changed by a sweep scoped to org-a", p)
		}
	}
	n := 0
	for _, row := range memoryColumns(t, cfg.ProjectDBPath("org-a")) {
		if row["type"] == string(model.TypeSynthesis) && row["deleted_at"] != "<nil>" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("retired syntheses in org-a = %d, want 2", n)
	}
}

// TestSynthesisSweep_MissingProjectDB verifies that --project with no file
// is an error wrapping ErrSweepNoProjectDB and creates nothing.
func TestSynthesisSweep_MissingProjectDB(t *testing.T) {
	cfg := sweepFixture(t)
	_, err := service.SweepSyntheses(context.Background(), cfg, model.SynthesisSweepRequest{Project: "nope/x"})
	if !errors.Is(err, service.ErrSweepNoProjectDB) {
		t.Fatalf("err = %v, want ErrSweepNoProjectDB", err)
	}
	if _, statErr := os.Stat(cfg.ProjectDBPath("nope/x")); !os.IsNotExist(statErr) {
		t.Errorf("the missing database was created: %v", statErr)
	}
}

// TestMemoryService_CountActiveSyntheses verifies the status count goes
// through the project store, by slug or over the whole database.
func TestMemoryService_CountActiveSyntheses(t *testing.T) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })
	ps := store.NewMemoryStore(projectDB)
	svc := service.NewMemoryService(ps, store.NewMemoryStore(globalDB), config.Default(), "org/a", embed.NopEmbedder{})
	ctx := context.Background()
	for _, p := range []string{"org/a", "org/a", "org/b"} {
		if _, err := ps.Create(ctx, &model.Memory{Type: model.TypeSynthesis, Scope: model.ScopeProject, Project: p, Title: "s", Content: "c"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	for _, tc := range []struct {
		slug string
		want int
	}{{"org/a", 2}, {"", 3}} {
		got, err := svc.CountActiveSyntheses(ctx, tc.slug)
		if err != nil {
			t.Fatalf("CountActiveSyntheses(%q): %v", tc.slug, err)
		}
		if got != tc.want {
			t.Errorf("CountActiveSyntheses(%q) = %d, want %d", tc.slug, got, tc.want)
		}
	}
}
