package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/store"
)

// codegraphDBSuffix marks the code graph databases that live next to the
// project databases; they hold no memories and the sweep skips them.
const codegraphDBSuffix = "-codegraph.db"

// ErrSweepPartial reports that at least one database could not be read or
// updated. The report still carries every database that worked, so the
// caller can show what was measured before naming what failed.
var ErrSweepPartial = errors.New("synthesis sweep: some databases failed")

// ErrSweepNoProjectDB reports that --project names a project whose database
// file does not exist. The sweep never creates it.
var ErrSweepNoProjectDB = errors.New("synthesis sweep: project database does not exist")

// sweepTarget is one database file the sweep visits.
type sweepTarget struct {
	path string // absolute path, used to open the file
	rel  string // path relative to data_dir with forward slashes, for output
}

// SweepSyntheses measures, and with req.Apply retires, the synthesis
// memories of every database under cfg.Storage.DataDir (SPEC-187, D3).
//
// It is a package function and not a MemoryService method on purpose, and
// it is the first function of this package that opens databases itself
// (a conscious deviation, DS12.11): the sweep spans every projects/*.db and
// global.db, not the database of the detected project that MemoryService
// is built on. service -> db still points inward. Moving the enumeration
// and the read-only opening into the CLI would put that logic in a
// frontend, which is what the layering rule forbids.
//
// The dry run opens every file with db.OpenReadOnly, so SQLite itself
// guarantees nothing is written and no migration runs; a database locked by
// a live writer for longer than its busy timeout is reported in Errors and
// the others are still read. With req.Apply the same measure runs first,
// then only the databases holding at least one kept synthesis are opened
// with db.Open (which, like any writer, applies pending migrations) and get
// a single soft-delete UPDATE each.
//
// If the UPDATE of a database touches a number of rows N different from the
// M its rows counted (another process wrote in between), every row of that
// database reports Retired = 0, the database is listed in Errors with
// "retired N rows, expected M" and the sweep returns ErrSweepPartial. The
// UPDATE is already committed and stays: the error only says the per-row
// breakdown is not reliable. That branch has no test, because there is no
// natural seam to force a write between the count and the UPDATE and none
// is added to production code.
//
// With req.Project set, only that project's database is visited (both slug
// forms are accepted), rows are kept when their project maps to the same
// file name, and global.db is left out. A missing file is an error wrapping
// ErrSweepNoProjectDB and nothing is created.
func SweepSyntheses(ctx context.Context, cfg *config.Config, req model.SynthesisSweepRequest) (*model.SynthesisSweepReport, error) {
	targets, err := sweepTargets(cfg, req.Project)
	if err != nil {
		return nil, err
	}

	report := &model.SynthesisSweepReport{Apply: req.Apply, Rows: []model.SynthesisSweepRow{}}
	for _, tg := range targets {
		rows, err := measureSyntheses(ctx, tg.path)
		if err != nil {
			report.Errors = append(report.Errors, model.SynthesisSweepError{Database: tg.rel, Error: err.Error()})
			continue
		}

		kept := make([]model.SynthesisSweepRow, 0, len(rows))
		for _, r := range rows {
			if req.Project != "" && project.SlugToFilename(r.Project) != project.SlugToFilename(req.Project) {
				continue
			}
			r.Database = tg.rel
			kept = append(kept, r)
		}

		if req.Apply {
			if errMsg := retireSyntheses(ctx, tg.path, req.Project != "", kept); errMsg != "" {
				report.Errors = append(report.Errors, model.SynthesisSweepError{Database: tg.rel, Error: errMsg})
			}
		}
		report.Rows = append(report.Rows, kept...)
	}

	if len(report.Errors) > 0 {
		return report, fmt.Errorf("service: synthesis sweep: %w", ErrSweepPartial)
	}
	return report, nil
}

// sweepTargets lists the databases to visit: with a project, only its file
// (which must exist); without one, projects/*.db in lexical order minus the
// code graph files, and then global.db, always last. Missing folders or a
// missing global.db are skipped silently.
func sweepTargets(cfg *config.Config, projectFlag string) ([]sweepTarget, error) {
	dataDir := cfg.Storage.DataDir
	if projectFlag != "" {
		path := cfg.ProjectDBPath(projectFlag)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("service: synthesis sweep: %s: %w", path, ErrSweepNoProjectDB)
			}
			return nil, fmt.Errorf("service: synthesis sweep: stat %s: %w", path, err)
		}
		return []sweepTarget{newSweepTarget(dataDir, path)}, nil
	}

	matches, err := filepath.Glob(filepath.Join(dataDir, "projects", "*.db"))
	if err != nil {
		return nil, fmt.Errorf("service: synthesis sweep: list project databases: %w", err)
	}
	sort.Strings(matches)
	targets := make([]sweepTarget, 0, len(matches)+1)
	for _, m := range matches {
		if strings.HasSuffix(m, codegraphDBSuffix) {
			continue
		}
		targets = append(targets, newSweepTarget(dataDir, m))
	}
	if global := cfg.GlobalDBPath(); fileExists(global) {
		targets = append(targets, newSweepTarget(dataDir, global))
	}
	return targets, nil
}

// newSweepTarget pairs an absolute path with its slash-separated form
// relative to the data directory, so the report reads the same on Windows.
func newSweepTarget(dataDir, path string) sweepTarget {
	rel, err := filepath.Rel(dataDir, path)
	if err != nil {
		rel = path
	}
	return sweepTarget{path: path, rel: filepath.ToSlash(rel)}
}

// fileExists reports whether path names an existing file.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// measureSyntheses opens one database read-only and returns its synthesis
// measure, closing it before returning.
func measureSyntheses(ctx context.Context, path string) ([]model.SynthesisSweepRow, error) {
	database, err := db.OpenReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	return store.NewMemoryStore(database).SynthesisCounts(ctx)
}

// retireSyntheses soft-deletes the kept syntheses of one database and fills
// each row's Retired. It returns a non-empty message when the database
// could not be updated or the count does not match (see SweepSyntheses).
// Without a project filter the whole database is retired; with one, only
// the memories.project values that were kept.
func retireSyntheses(ctx context.Context, path string, scoped bool, rows []model.SynthesisSweepRow) string {
	expected := 0
	var projects []string
	for _, r := range rows {
		expected += r.Synthesis
		projects = append(projects, r.Project)
	}
	if expected == 0 {
		return ""
	}
	if !scoped {
		projects = nil
	}

	database, err := db.Open(path)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = database.Close() }()

	n, err := store.NewMemoryStore(database).SoftDeleteSyntheses(ctx, projects)
	if err != nil {
		return err.Error()
	}
	if n != expected {
		return fmt.Sprintf("retired %d rows, expected %d", n, expected)
	}
	for i := range rows {
		rows[i].Retired = rows[i].Synthesis
	}
	return ""
}

// CountActiveSyntheses counts the active, non-superseded synthesis memories
// of the open project database, for the warning mneme status prints while
// generation is off (SPEC-187, DS8). An empty slug counts the whole
// database, which is what status needs when no project was detected and the
// global database stands in as the project store.
func (svc *MemoryService) CountActiveSyntheses(ctx context.Context, slug string) (int, error) {
	n, err := svc.projectStore.CountActiveSyntheses(ctx, slug)
	if err != nil {
		return 0, fmt.Errorf("service: count active syntheses: %w", err)
	}
	return n, nil
}
