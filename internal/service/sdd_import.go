// Package service — this file is SPEC-131 §2b's read path: ImportSDDFromRepo
// walks .mneme/sdd/, decides by ANCHOR (never by correlative, D50), and
// creates/updates the local database while preserving identity and
// timestamps (D49). It is the read-side sibling of sdd_export.go's
// write-through — the two never call each other's writers.
//
// repoRoot is ALWAYS a caller-supplied parameter (D38/D60) — this file
// never resolves os.Getwd(), HOME, or git identity. See
// sdd_ambient_guard_test.go for the AST guardian that enforces this.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
)

// maxOnlyInBaseListed caps the number of correlatives SDDImportResult.OnlyInBase
// names directly — OnlyInBaseTotal always carries the real count, so capping
// the list loses nothing a caller could not derive (SPEC-131 D54, same
// convention model.ListMaxLimit already established for backlog_list/spec_list).
const maxOnlyInBaseListed = 20

// ErrSDDForeignProjectMarker is ImportSDDFromRepo's fatal, batch-aborting
// error (D50): the repository's own marker names a DIFFERENT project than
// this database. Overwriting one project's backlog/specs with another's
// would not be a broken file — it would be the wrong repository entirely.
var ErrSDDForeignProjectMarker = errors.New("sdd: repository marker belongs to a different project")

// isDuplicateAnchorInBatch recognizes ONE specific, real failure shape out
// of CreateBacklogItemFromRecord/CreateSpecFromRecord's own generic error:
// a genuine UNIQUE-constraint violation on the anchor column itself
// (idx_backlog_items_uuid/idx_specs_uuid, migration 019). This is the
// SAME accepted risk D16 already names (two machines completing the same
// hand-authored file and minting DIFFERENT anchors, or a file copied by
// hand) landing on its OTHER side: two files in the SAME import batch
// that happen to carry the IDENTICAL anchor — undetectable by anchorIndex
// (D50's own pre-batch snapshot, taken once before ANY write in this
// batch, exactly so an archived-item+moved-spec pair in the same batch is
// never torn apart — see D64) because neither file's anchor is known to
// the database until the FIRST one's own write commits, mid-batch.
// Recognized by matching the sqlite driver's own well-known error text
// for the exact column this spec's two anchor indexes protect — a driver
// upgrade that changes the message text degrades gracefully to the
// generic "roto" reason, never to a wrong diagnosis.
func isDuplicateAnchorInBatch(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed: backlog_items.uuid") ||
		strings.Contains(msg, "UNIQUE constraint failed: specs.uuid")
}

// SDDImportSkip names one record ImportSDDFromRepo declined to import, and
// why (SPEC-131 D50/D54). Reason is drawn from a closed vocabulary (roto,
// sin-titulo, correlativo-reclamado-por-dos-elementos,
// ancla-renumerada-en-otra-maquina, ancla-duplicada-en-la-misma-tanda,
// proyecto-distinto, spec-congelada) — except the correlativo-reclamado
// case, whose Reason carries a fuller, human-legible message naming both
// titles (never either anchor — SPEC-128 D9 stays in force here with no
// exception).
type SDDImportSkip struct {
	Path   string `json:"path"`
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
}

// SDDImportCompleted names a file that arrived with gaps (D53), was
// completed and rewritten in place (D46/D52) — the ONLY thing this
// mechanism ever writes back to disk. Fields lists exactly which gaps were
// filled, matching (*BacklogRecord).Missing()/(*SpecRecord).Missing()'s own
// vocabulary. Populated starting with the commit that adds
// rewriteCompletedRecord (D52).
type SDDImportCompleted struct {
	Path   string   `json:"path"`
	ID     string   `json:"id"`
	Fields []string `json:"fields"`
}

// SDDImportResult is ImportSDDFromRepo's nominal report (D43/D54): every
// field names WHAT happened, never in silence. No field ever carries an
// anchor — the same SPEC-128 D9 posture every other readable SDD response
// already follows.
type SDDImportResult struct {
	// Created lists "<ID> (<path relative to .mneme/sdd>)" for every record
	// this import inserted (CASE A of D50).
	Created []string `json:"created,omitempty"`

	// Updated lists "<ID>: <state change or 'sin cambio de estado'>" for
	// every record this import updated (CASE B of D50).
	Updated []string `json:"updated,omitempty"`

	// Completed lists the files that arrived incomplete, were completed,
	// and were rewritten (D46).
	Completed []SDDImportCompleted `json:"completed,omitempty"`

	// Skipped lists every record this import declined to touch, and why.
	Skipped []SDDImportSkip `json:"skipped,omitempty"`

	// OnlyInBase names up to maxOnlyInBaseListed correlatives that exist in
	// the local database but have no file on this branch (D13/D62) —
	// normal on a working branch, not an error.
	OnlyInBase []string `json:"only_in_base,omitempty"`

	// OnlyInBaseTotal is the REAL count behind OnlyInBase, before the
	// maxOnlyInBaseListed cap — the same acotado convention SPEC-109
	// established for backlog_list/spec_list.
	OnlyInBaseTotal int `json:"only_in_base_total,omitempty"`

	// OnlyInBaseError is set — and OnlyInBase/OnlyInBaseTotal are left at
	// their zero values — when computeOnlyInBase's own listing failed
	// (e.g. a pre-existing row this batch never touched carries a
	// timestamp the store cannot parse). D22 forbids that failure from
	// aborting the batch (the rest of this result is real and complete),
	// but a silent zero is indistinguishable from "genuinely nothing is
	// only in the base" — this field is the difference, following the
	// same nominal-reporting discipline NoOpReason already established
	// (D54: nothing is ever silent).
	OnlyInBaseError string `json:"only_in_base_error,omitempty"`

	// NoOpReason is set, and every other field left empty, when the import
	// did nothing: "mecanismo apagado" (the marker exists but sdd.off is
	// present) or "no hay directorio .mneme/sdd" (no marker at all).
	NoOpReason string `json:"no_op_reason,omitempty"`
}

// ImportSDDFromRepo is the read path's entry point (D48): it walks
// .mneme/sdd/ under repoRoot, decides each record's fate by ANCHOR
// (D50 — never by correlative, which would silently overwrite a
// teammate's item), and creates or updates the local database accordingly.
//
// apply=false is a preview: the SAME decisions are computed (including
// which records would be Created/Updated/Skipped) but nothing is written,
// neither to the database nor to disk. apply=true executes.
//
// ImportSDDFromRepo NEVER compares timestamps (D59 — abandoning the
// updated_at comparison ImportFromShared, MemoryService's own shared-vault
// importer, still relies on): every write here originates from a FILE the
// local git history already tracks, so a conflicting local edit would
// already have produced a merge conflict in git before ever reaching this
// function. ImportFromShared cannot make that same assumption — a memory's
// local row can carry edits that never passed through a file at all, so
// its own updated_at remains the only arbiter there. See
// MemoryService.ImportFromShared's own godoc, which points back here.
func (svc *SDDService) ImportSDDFromRepo(ctx context.Context, repoRoot string, apply bool) (*SDDImportResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: sdd import: repoRoot is required")
	}

	result := &SDDImportResult{}

	state := ResolveSDDState(repoRoot)
	if !state.Enabled {
		marker, mErr := sddfile.ReadMarker(repoRoot)
		if mErr == nil && marker == nil {
			result.NoOpReason = "no hay directorio .mneme/sdd"
		} else {
			result.NoOpReason = "mecanismo apagado"
		}
		return result, nil
	}

	marker, err := sddfile.ReadMarker(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd import: read marker: %w", err)
	}
	if marker != nil && marker.Project != "" && marker.Project != svc.project {
		return nil, fmt.Errorf(
			"service: sdd import: repository marker belongs to project %q, this database is %q: %w",
			marker.Project, svc.project, ErrSDDForeignProjectMarker)
	}

	paths, err := sddfile.ListRecords(sddfile.RootDir(repoRoot))
	if err != nil {
		return nil, fmt.Errorf("service: sdd import: list records: %w", err)
	}

	type backlogParsed struct {
		path string
		rec  *sddfile.BacklogRecord
	}
	type specParsed struct {
		path string
		rec  *sddfile.SpecRecord
	}
	var backlogFiles []backlogParsed
	var specFiles []specParsed
	covered := make(map[string]bool)

	for _, path := range paths {
		kind, id, ok := sddfile.ClassifyRecordPath(repoRoot, path)
		if !ok {
			continue
		}
		covered[id] = true

		data, rErr := sddfile.ReadRecord(path)
		if rErr != nil {
			result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: id, Reason: "roto"})
			continue
		}

		switch kind {
		case sddfile.KindBacklog:
			rec, uErr := sddfile.UnmarshalBacklog(data)
			if uErr != nil {
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: id, Reason: "roto"})
				continue
			}
			// The FILENAME is the correlative (D4/D25) — mneme never
			// invents one, but it also never trusts the frontmatter's own
			// `id:` field over it: the name is what RESERVED the number,
			// so it always wins, even for a record the frontmatter itself
			// names differently or not at all (D53's minimal fixture: a
			// hand-authored file carrying only title+description has no
			// `id:` line at all).
			rec.Item.ID = id
			if rec.Item.Title == "" {
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: id, Reason: "sin-titulo"})
				continue
			}
			if rec.Item.Project != "" && rec.Item.Project != svc.project {
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: id, Reason: "proyecto-distinto"})
				continue
			}
			backlogFiles = append(backlogFiles, backlogParsed{path: path, rec: rec})
		case sddfile.KindSpec:
			rec, uErr := sddfile.UnmarshalSpec(data)
			if uErr != nil {
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: id, Reason: "roto"})
				continue
			}
			rec.Spec.ID = id // same rule as above, D4/D25.
			if rec.Spec.Title == "" {
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: id, Reason: "sin-titulo"})
				continue
			}
			if rec.Spec.Project != "" && rec.Spec.Project != svc.project {
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: id, Reason: "proyecto-distinto"})
				continue
			}
			specFiles = append(specFiles, specParsed{path: path, rec: rec})
		case sddfile.KindIgnored:
			// unreachable: ClassifyRecordPath never returns ok=true with
			// KindIgnored, kept for exhaustiveness against the closed enum.
		}
	}

	// Two snapshots, taken BEFORE writing anything (D64's instantaneity
	// requirement): anchorIndex resolves D50's CASE A/C decision, freezeIndex
	// resolves D64's frozen-spec check. Evaluating the freeze against a
	// snapshot taken here — rather than against the live state a few writes
	// later in this same batch — is what keeps a pair (archived item + its
	// spec moved) that arrives TOGETHER from being torn apart: since backlog
	// records are processed before specs, checking the live state would see
	// the item already archived by the time its spec's turn comes, and skip
	// a change that was perfectly legitimate at the start of this import.
	var anchors []string
	for _, bp := range backlogFiles {
		if bp.rec.Item.UUID != "" {
			anchors = append(anchors, bp.rec.Item.UUID)
		}
	}
	for _, sp := range specFiles {
		if sp.rec.Spec.UUID != "" {
			anchors = append(anchors, sp.rec.Spec.UUID)
		}
	}
	anchorIndex, err := svc.store.RefsForUUIDs(ctx, anchors)
	if err != nil {
		return nil, fmt.Errorf("service: sdd import: refs for uuids: %w", err)
	}
	freezeIndex, err := svc.store.BacklogStatusIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: sdd import: backlog status index: %w", err)
	}

	// Backlog first, then specs: a spec names its originating item
	// (backlog_id); the reverse order would leave that link pointing at
	// nothing for the duration of this batch.
	for _, bp := range backlogFiles {
		if iErr := svc.importBacklogRecord(ctx, repoRoot, bp.path, bp.rec, anchorIndex, apply, result); iErr != nil {
			return nil, fmt.Errorf("service: sdd import: %s: %w", bp.path, iErr)
		}
	}
	for _, sp := range specFiles {
		if iErr := svc.importSpecRecord(ctx, repoRoot, sp.path, sp.rec, anchorIndex, freezeIndex, apply, result); iErr != nil {
			return nil, fmt.Errorf("service: sdd import: %s: %w", sp.path, iErr)
		}
	}

	// A failure here (e.g. a pre-existing row this import batch never
	// touched, with a timestamp the store cannot parse) must NEVER discard
	// the batch's own Created/Updated/Skipped work already recorded in
	// result — that would be exactly the abort-the-whole-thing D22
	// forbids, just relocated to this auxiliary reporting step instead of
	// a single file's own parse error. Logged AND surfaced instead (never
	// silently): the import itself already succeeded; only the "only in
	// base" summary is degraded for this run, and OnlyInBaseError is what
	// tells "genuinely nothing is only in the base" apart from "the
	// calculation itself failed" — a zero that meant two different things
	// until this field existed.
	onlyInBase, total, err := svc.computeOnlyInBase(ctx, covered)
	if err != nil {
		slog.ErrorContext(ctx, "sdd_import_error", "step", "only-in-base", "error", err)
		result.OnlyInBaseError = fmt.Sprintf("no se pudo calcular: %v", err)
	} else {
		result.OnlyInBase = onlyInBase
		result.OnlyInBaseTotal = total
	}

	return result, nil
}

// importBacklogRecord applies D50's three-branch decision to a single
// parsed backlog record, appending the outcome to result. A non-nil return
// is an INFRASTRUCTURE failure (e.g. a database error unrelated to this
// file's own content) that aborts the whole batch — distinct from a
// skip, which is an expected per-record outcome.
func (svc *SDDService) importBacklogRecord(
	ctx context.Context, repoRoot, path string, rec *sddfile.BacklogRecord,
	anchorIndex map[string]string, apply bool, result *SDDImportResult,
) error {
	item := rec.Item
	missing := rec.Missing() // computed BEFORE any defaulting (D53) — see reportIfCompleted

	row, err := svc.store.GetBacklogItem(ctx, item.ID)
	switch {
	case errors.Is(err, model.ErrBacklogNotFound):
		// CASE A.
		if item.UUID != "" {
			if owner, known := anchorIndex[item.UUID]; known && owner != item.ID {
				result.Skipped = append(result.Skipped, SDDImportSkip{
					Path: path, ID: item.ID, Reason: "ancla-renumerada-en-otra-maquina",
				})
				return nil
			}
		}
		if !apply {
			result.Created = append(result.Created, fmt.Sprintf("%s (%s)", item.ID, relSDDPath(repoRoot, path)))
			return nil
		}
		applyBacklogDefaults(svc.project, item, nil)
		if cErr := svc.store.CreateBacklogItemFromRecord(ctx, item); cErr != nil {
			slog.ErrorContext(ctx, "sdd_import_error", "kind", "backlog", "id", item.ID, "step", "create", "error", cErr)
			reason := "roto"
			if isDuplicateAnchorInBatch(cErr) {
				reason = "ancla-duplicada-en-la-misma-tanda"
			}
			result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: item.ID, Reason: reason})
			return nil
		}
		if mErr := svc.store.MergeBacklogRefinements(ctx, item.ID, rec.Refinements); mErr != nil {
			slog.ErrorContext(ctx, "sdd_import_error", "kind", "backlog", "id", item.ID, "step", "merge-refinements", "error", mErr)
		}
		result.Created = append(result.Created, fmt.Sprintf("%s (%s)", item.ID, relSDDPath(repoRoot, path)))
		svc.reportIfCompleted(ctx, repoRoot, sddfile.KindBacklog, item.ID, path, missing, result)
		return nil

	case err != nil:
		slog.ErrorContext(ctx, "sdd_import_error", "kind", "backlog", "id", item.ID, "step", "read-existing", "error", err)
		result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: item.ID, Reason: "roto"})
		return nil

	default:
		if row.UUID != "" && row.UUID == item.UUID {
			// CASE B.
			applyBacklogDefaults(svc.project, item, row)
			nominal := nominalStatusChange(item.ID, string(row.Status), string(item.Status))
			if !apply {
				result.Updated = append(result.Updated, nominal)
				return nil
			}
			if uErr := svc.store.UpdateBacklogItemFromRecord(ctx, item); uErr != nil {
				slog.ErrorContext(ctx, "sdd_import_error", "kind", "backlog", "id", item.ID, "step", "update", "error", uErr)
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: item.ID, Reason: "roto"})
				return nil
			}
			if mErr := svc.store.MergeBacklogRefinements(ctx, item.ID, rec.Refinements); mErr != nil {
				slog.ErrorContext(ctx, "sdd_import_error", "kind", "backlog", "id", item.ID, "step", "merge-refinements", "error", mErr)
			}
			result.Updated = append(result.Updated, nominal)
			svc.reportIfCompleted(ctx, repoRoot, sddfile.KindBacklog, item.ID, path, missing, result)
			return nil
		}

		// CASE C: the correlative is claimed by two different anchors.
		// Neither anchor is ever printed (SPEC-128 D9) — the two titles are
		// what a person can actually use to tell them apart.
		result.Skipped = append(result.Skipped, SDDImportSkip{
			Path: path, ID: item.ID,
			Reason: fmt.Sprintf(
				"correlativo-reclamado-por-dos-elementos: local=%q archivo=%q (ver BL-202)",
				row.Title, item.Title,
			),
		})
		return nil
	}
}

// importSpecRecord is importBacklogRecord's sibling for specs, adding D64's
// frozen-spec check inside CASE B: a spec frozen (per freezeIndex, the
// snapshot taken before this import started) whose file brings a DIFFERENT
// status is skipped rather than moved — SPEC-125's archived-item freeze has
// no path back by design, and this importer does not go through
// loadMutableSpec (it writes status directly), so nothing else would catch
// this.
func (svc *SDDService) importSpecRecord(
	ctx context.Context, repoRoot, path string, rec *sddfile.SpecRecord,
	anchorIndex map[string]string, freezeIndex map[string]model.BacklogIndexEntry,
	apply bool, result *SDDImportResult,
) error {
	spec := rec.Spec
	missing := rec.Missing() // computed BEFORE any defaulting (D53) — see reportIfCompleted

	row, err := svc.store.GetSpec(ctx, spec.ID)
	switch {
	case errors.Is(err, model.ErrSpecNotFound):
		// CASE A.
		if spec.UUID != "" {
			if owner, known := anchorIndex[spec.UUID]; known && owner != spec.ID {
				result.Skipped = append(result.Skipped, SDDImportSkip{
					Path: path, ID: spec.ID, Reason: "ancla-renumerada-en-otra-maquina",
				})
				return nil
			}
		}
		if !apply {
			result.Created = append(result.Created, fmt.Sprintf("%s (%s)", spec.ID, relSDDPath(repoRoot, path)))
			return nil
		}
		applySpecDefaults(svc.project, spec, nil)
		if cErr := svc.store.CreateSpecFromRecord(ctx, spec); cErr != nil {
			slog.ErrorContext(ctx, "sdd_import_error", "kind", "spec", "id", spec.ID, "step", "create", "error", cErr)
			reason := "roto"
			if isDuplicateAnchorInBatch(cErr) {
				reason = "ancla-duplicada-en-la-misma-tanda"
			}
			result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: spec.ID, Reason: reason})
			return nil
		}
		if hErr := svc.store.MergeSpecHistory(ctx, spec.ID, rec.History); hErr != nil {
			slog.ErrorContext(ctx, "sdd_import_error", "kind", "spec", "id", spec.ID, "step", "merge-history", "error", hErr)
		}
		if pErr := svc.store.MergeSpecPushbacks(ctx, spec.ID, rec.Pushbacks); pErr != nil {
			slog.ErrorContext(ctx, "sdd_import_error", "kind", "spec", "id", spec.ID, "step", "merge-pushbacks", "error", pErr)
		}
		result.Created = append(result.Created, fmt.Sprintf("%s (%s)", spec.ID, relSDDPath(repoRoot, path)))
		svc.reportIfCompleted(ctx, repoRoot, sddfile.KindSpec, spec.ID, path, missing, result)
		return nil

	case err != nil:
		slog.ErrorContext(ctx, "sdd_import_error", "kind", "spec", "id", spec.ID, "step", "read-existing", "error", err)
		result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: spec.ID, Reason: "roto"})
		return nil

	default:
		if row.UUID != "" && row.UUID == spec.UUID {
			// CASE B.
			applySpecDefaults(svc.project, spec, row)
			if specImportFrozen(row, freezeIndex) && spec.Status != row.Status {
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: spec.ID, Reason: "spec-congelada"})
				return nil
			}

			nominal := nominalStatusChange(spec.ID, string(row.Status), string(spec.Status))
			if !apply {
				result.Updated = append(result.Updated, nominal)
				return nil
			}
			if uErr := svc.store.UpdateSpecFromRecord(ctx, spec); uErr != nil {
				slog.ErrorContext(ctx, "sdd_import_error", "kind", "spec", "id", spec.ID, "step", "update", "error", uErr)
				result.Skipped = append(result.Skipped, SDDImportSkip{Path: path, ID: spec.ID, Reason: "roto"})
				return nil
			}
			if hErr := svc.store.MergeSpecHistory(ctx, spec.ID, rec.History); hErr != nil {
				slog.ErrorContext(ctx, "sdd_import_error", "kind", "spec", "id", spec.ID, "step", "merge-history", "error", hErr)
			}
			if pErr := svc.store.MergeSpecPushbacks(ctx, spec.ID, rec.Pushbacks); pErr != nil {
				slog.ErrorContext(ctx, "sdd_import_error", "kind", "spec", "id", spec.ID, "step", "merge-pushbacks", "error", pErr)
			}
			result.Updated = append(result.Updated, nominal)
			svc.reportIfCompleted(ctx, repoRoot, sddfile.KindSpec, spec.ID, path, missing, result)
			return nil
		}

		// CASE C.
		result.Skipped = append(result.Skipped, SDDImportSkip{
			Path: path, ID: spec.ID,
			Reason: fmt.Sprintf(
				"correlativo-reclamado-por-dos-elementos: local=%q archivo=%q (ver BL-202)",
				row.Title, spec.Title,
			),
		})
		return nil
	}
}

// specImportFrozen reports whether spec is frozen according to freezeIndex
// — the SAME predicate loadMutableSpec's own freeze check applies
// (specFreeze, sdd.go), reused verbatim rather than reimplemented, so the
// importer can never disagree with what spec_advance/spec_status already
// report. A spec with no BacklogID is never frozen (there is nothing to
// look up).
func specImportFrozen(spec *model.Spec, freezeIndex map[string]model.BacklogIndexEntry) bool {
	if spec.BacklogID == "" {
		return false
	}
	entry, found := freezeIndex[spec.BacklogID]
	return specFreeze(spec, entry, found) != nil
}

// nominalStatusChange renders D54's Updated line: "<id>: sin cambio de
// estado" when from == to, or "<id>: <from> → <to>" otherwise.
func nominalStatusChange(id, from, to string) string {
	if from == to {
		return fmt.Sprintf("%s: sin cambio de estado", id)
	}
	return fmt.Sprintf("%s: %s → %s", id, from, to)
}

// relSDDPath renders path relative to repoRoot's SDD root
// (sddfile.RootDir), slash-separated, for the "<id> (<path>)" shape
// Created entries use. Falls back to the absolute path on the (practically
// unreachable, since every path here was produced by sddfile.ListRecords
// walking that same root) error case.
func relSDDPath(repoRoot, path string) string {
	rel, err := filepath.Rel(sddfile.RootDir(repoRoot), path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// computeOnlyInBase lists every backlog item and spec of svc.project whose
// ID is absent from covered — the set of correlatives THIS import found a
// file for, populated regardless of whether that file parsed, had a title,
// or matched its own project (D62): the question this answers is "does a
// file exist for this correlative on this branch", not "did it import
// cleanly". Sorted for determinism, then capped to maxOnlyInBaseListed —
// total always reports the real count.
func (svc *SDDService) computeOnlyInBase(ctx context.Context, covered map[string]bool) ([]string, int, error) {
	items, _, err := svc.store.ListBacklogItems(ctx, svc.project, "", 0)
	if err != nil {
		return nil, 0, fmt.Errorf("list backlog items: %w", err)
	}
	specs, _, err := svc.store.ListSpecs(ctx, svc.project, "", 0)
	if err != nil {
		return nil, 0, fmt.Errorf("list specs: %w", err)
	}

	var missing []string
	for _, item := range items {
		if !covered[item.ID] {
			missing = append(missing, item.ID)
		}
	}
	for _, spec := range specs {
		if !covered[spec.ID] {
			missing = append(missing, spec.ID)
		}
	}
	sort.Strings(missing)

	total := len(missing)
	if total > maxOnlyInBaseListed {
		missing = missing[:maxOnlyInBaseListed]
	}
	return missing, total, nil
}

// applyBacklogDefaults fills the zero-value fields D53 declares fillable
// (project, status, priority, lane — uuid/created_at/updated_at are filled
// by CreateBacklogItemFromRecord itself; position's zero value already IS
// its default) directly on item, BEFORE it is written. When existing is
// non-nil (CASE B, an update) a gap falls back to the row ALREADY in the
// database, never to a fresh default — a hand-edited file that dropped a
// field must not blank out data that update would otherwise overwrite
// verbatim. When existing is nil (CASE A, a brand-new row) a gap falls
// back to D53's fixed defaults: raw / medium / standard, and svc.project.
func applyBacklogDefaults(project string, item, existing *model.BacklogItem) {
	if item.Project == "" {
		item.Project = project
	}
	if item.Status == "" {
		if existing != nil {
			item.Status = existing.Status
		} else {
			item.Status = model.BacklogStatusRaw
		}
	}
	if item.Priority == "" {
		if existing != nil {
			item.Priority = existing.Priority
		} else {
			item.Priority = model.PriorityMedium
		}
	}
	if item.Lane == "" {
		if existing != nil {
			item.Lane = existing.Lane
		} else {
			item.Lane = model.LaneStandard
		}
	}
}

// applySpecDefaults is applyBacklogDefaults' sibling for specs: project,
// status, lane — a spec has no priority.
func applySpecDefaults(project string, spec, existing *model.Spec) {
	if spec.Project == "" {
		spec.Project = project
	}
	if spec.Status == "" {
		if existing != nil {
			spec.Status = existing.Status
		} else {
			spec.Status = model.SpecStatusDraft
		}
	}
	if spec.Lane == "" {
		if existing != nil {
			spec.Lane = existing.Lane
		} else {
			spec.Lane = model.LaneStandard
		}
	}
}

// reportIfCompleted is the D46/D52 seam: when missing (computed BEFORE
// defaulting, from the record AS PARSED) is non-empty, it rewrites id's
// on-disk record via rewriteCompletedRecord — the ONLY site that ever
// calls a materializer from this file — and appends the outcome to
// result.Completed. A record that arrived complete (missing is empty) is
// left byte-for-byte alone: this is the entire distinguishing behaviour
// AC12's fixture exists to prove.
func (svc *SDDService) reportIfCompleted(
	ctx context.Context, repoRoot string, kind sddfile.RecordKind, id, path string, missing []string, result *SDDImportResult,
) {
	if len(missing) == 0 {
		return
	}
	svc.rewriteCompletedRecord(ctx, repoRoot, kind, id)
	result.Completed = append(result.Completed, SDDImportCompleted{Path: path, ID: id, Fields: missing})
}

// rewriteCompletedRecord rewrites id's on-disk record through the SAME
// materializer sdd_export.go's nine wrappers already use
// (materializeBacklogItem/materializeSpec) — deliberately the ONLY call
// this file ever makes to either (D52). Doing so through the existing
// materializer, rather than a bespoke write here, is what keeps the
// completed file byte-identical to whatever mneme itself would produce for
// the same row: one serializer, one writer, everywhere.
//
// svc.repoDir is swapped to repoRoot for the duration of this call and
// restored after — the same pattern exportAllSDD already uses, because
// materializeBacklogItem/materializeSpec read svc.repoDir rather than
// taking it as a parameter (a constructor-level exception to D38 that
// predates this spec; see sdd_export.go's own godoc for why).
func (svc *SDDService) rewriteCompletedRecord(ctx context.Context, repoRoot string, kind sddfile.RecordKind, id string) {
	prevRepoDir := svc.repoDir
	svc.repoDir = repoRoot
	defer func() { svc.repoDir = prevRepoDir }()

	switch kind {
	case sddfile.KindBacklog:
		svc.materializeBacklogItem(ctx, id)
	case sddfile.KindSpec:
		svc.materializeSpec(ctx, id)
	case sddfile.KindIgnored:
		// unreachable: only KindBacklog/KindSpec ever reach this function.
	}
}
