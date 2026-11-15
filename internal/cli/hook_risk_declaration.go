package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/enforcement"
	"github.com/wirvii/mneme/internal/laneaudit"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/risk"
	"github.com/wirvii/mneme/internal/service"
)

// Sources of a risk declaration, as logged in the enforcelog.
const (
	riskSourcePlan   = "plan"
	riskSourceDiseno = "diseno"
	riskSourceScope  = "scope"
)

// riskSpecsQuery lists the open standard specs that may declare a risk: in
// implementing, standard lane (a trivial spec can only say "Risk: none"), and
// not frozen by an archived backlog item (the same clause as the trivial-scope
// lookup).
const riskSpecsQuery = `SELECT s.id, s.scope FROM specs s
WHERE s.project = ? AND s.status = 'implementing' AND s.lane = 'standard'
  AND (COALESCE(s.backlog_id, '') = ''
       OR EXISTS (SELECT 1 FROM backlog_items b
                  WHERE b.project = s.project AND b.id = s.backlog_id AND b.status <> 'archived'))
ORDER BY s.id`

// openStandardSpec is one open standard spec with the declarations of the
// two sections only the architect or the orchestrator write.
type openStandardSpec struct {
	ID    string
	Scope string
	// Plan and Diseno are the last valid Risk: line of each section outside
	// code fences, or nil when the section declares nothing readable.
	Plan, Diseno *risk.Declaration
}

// loadRiskDeclarations reads, for the risk guard (SPEC-185 layer B), every
// open standard spec of the project and the risk declaration of its plan and
// diseno sections. A missing project database means nobody declared anything
// (nil, nil), as queryManifestContent treats it as "no manifest"; so does a
// spec whose tarea.md does not exist. Any other failure (the database cannot
// be queried, a tarea.md that cannot be read or whose markers do not parse) is
// returned, and the caller fails open with a warning.
func loadRiskDeclarations(cfg *config.Config, slug string) ([]openStandardSpec, error) {
	database, err := db.OpenReadOnly(cfg.ProjectDBPath(slug))
	if err != nil {
		if strings.Contains(err.Error(), "file does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("risk declarations: %w", err)
	}
	defer database.Close() //nolint:errcheck // read-only handle; nothing to flush

	specs, err := queryOpenStandardSpecs(database, slug)
	if err != nil {
		return nil, err
	}
	for i := range specs {
		path := filepath.Join(cfg.ProjectWorkflowDir(slug), "specs", specs[i].ID, "tarea.md")
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("risk declarations: read %s: %w", path, err)
		}
		if specs[i].Plan, err = sectionDeclaration(string(data), model.TaskSectionPlan); err != nil {
			return nil, fmt.Errorf("risk declarations: %s: %w", path, err)
		}
		if specs[i].Diseno, err = sectionDeclaration(string(data), model.TaskSectionDesign); err != nil {
			return nil, fmt.Errorf("risk declarations: %s: %w", path, err)
		}
	}
	return specs, nil
}

// queryOpenStandardSpecs runs riskSpecsQuery.
func queryOpenStandardSpecs(database *db.DB, slug string) ([]openStandardSpec, error) {
	rows, err := database.Query(riskSpecsQuery, slug)
	if err != nil {
		return nil, fmt.Errorf("risk declarations: query specs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor

	var specs []openStandardSpec
	for rows.Next() {
		var id string
		var scope sql.NullString
		if err := rows.Scan(&id, &scope); err != nil {
			return nil, fmt.Errorf("risk declarations: scan spec: %w", err)
		}
		specs = append(specs, openStandardSpec{ID: id, Scope: scope.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("risk declarations: iterate specs: %w", err)
	}
	return specs, nil
}

// sectionDeclaration returns the declaration of one section, or nil when the
// section is empty, has no Risk: line outside fences, or its last one is
// malformed (a malformed line declares nothing; the next source is tried).
func sectionDeclaration(doc string, s model.TaskSection) (*risk.Declaration, error) {
	body, found, err := service.TaskSectionBody(doc, s)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	// A malformed line is "not declared", not a lookup failure: its error is
	// dropped on purpose.
	d, ok, parseErr := risk.FindDeclaration(body)
	if parseErr != nil || !ok {
		return nil, nil
	}
	return &d, nil
}

// declaredFrom answers DeclaredFunc over the loaded specs. Per spec, in order:
// a Risk: line in plan, then diseno, bounded by the spec's scope when it has
// one and the write is a real path (a dependency command is never bounded);
// then the scope itself, which declares items 1, 2 and 5 for the paths it
// covers but never item 6 and never a synthetic write. The first spec that
// declares wins.
func declaredFrom(specs []openStandardSpec) enforcement.DeclaredFunc {
	return func(item risk.Item, rel string, synthetic bool) (bool, string, string) {
		for _, s := range specs {
			inScope := s.Scope != "" && !synthetic && laneaudit.MatchGlobs(rel, []string{s.Scope})
			if s.Scope == "" || synthetic || inScope {
				if declaresItem(s.Plan, item) {
					return true, s.ID, riskSourcePlan
				}
				if declaresItem(s.Diseno, item) {
					return true, s.ID, riskSourceDiseno
				}
			}
			if inScope && item.PathDetectable() {
				return true, s.ID, riskSourceScope
			}
		}
		return false, "", ""
	}
}

// declaresItem reports whether d lists item.
func declaresItem(d *risk.Declaration, item risk.Item) bool {
	if d == nil {
		return false
	}
	for _, i := range d.Items {
		if i == item {
			return true
		}
	}
	return false
}
