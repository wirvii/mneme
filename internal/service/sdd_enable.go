// Package service — this file is the SDD git-native mechanism's operator
// surface (SPEC-130 §2a §9): enable, disable, export, and status. All four
// take repoRoot as an explicit parameter (D38) — none resolves the working
// directory, HOME, or git identity itself; sddGit's own RepoDir field is
// always set from that same parameter.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/sddfile"
)

// ErrSDDNotConverged is D45's refusal: the repository already carries SDD
// records this local database cannot make sense of — either an unparseable
// file, or a record whose anchor (UUIDv7) is not known here. Enable and
// Export both refuse, WITHOUT writing a single byte, when this error would
// apply — the alternative (exporting over it) would silently overwrite a
// teammate's item.
var ErrSDDNotConverged = errors.New("sdd: repository is not convergent with the local database")

// ErrSDDNotEnabled is returned by ExportSDDRepo when repoRoot has no
// enable marker yet — export is a repair operation over an ALREADY
// enabled mechanism, not a way to enable one.
var ErrSDDNotEnabled = errors.New("sdd: mechanism is not enabled for this repository")

// ErrSDDNotGitRepo is returned when repoRoot is not the root of a git
// working tree.
var ErrSDDNotGitRepo = errors.New("sdd: not a git repository")

// The honest warnings D17/AC14 requires the operator to read before
// `--apply`. Defined once here so the CLI (commit 7) never risks
// paraphrasing them differently in two places.
const (
	// SDDWarnHistoryIrreversible: D17 — publishing to git is not undone by
	// deleting a later commit; the content stays reachable in history.
	SDDWarnHistoryIrreversible = "publicar en git no se deshace: la historia del repositorio conserva lo publicado, incluso si se borra despues"

	// SDDWarnRemotePrivacy: D17 — mneme never makes a network call to learn
	// whether a remote is public; it only reports what git already knows
	// locally.
	SDDWarnRemotePrivacy = "mneme no puede determinar si el remoto es publico o privado sin hacer una llamada de red que deliberadamente NO hace"

	// SDDWarnNoContentScan: D17 rejected scanning content for sensitive
	// data — a search that found nothing would read as "clean" when it
	// might just be a miss.
	SDDWarnNoContentScan = "mneme no ha escaneado el contenido de estos archivos buscando datos delicados; revisarlo es responsabilidad del operador"

	// SDDWarnNoCrossMachineSyncYet: §2a's own limitation (2.4) — no
	// importer, no git hooks. The files exist to be reviewed in a pull
	// request; they do not yet let a teammate's clone pick them up.
	SDDWarnNoCrossMachineSyncYet = "hoy estos archivos sirven para revisarse en un pull request; que entren en la base de otra maquina (leerlos) llega con BL-201"
)

// sddWarnings is the fixed, ordered list every dry-run preview and every
// applied enable prints — always all four, never a subset.
func sddWarnings() []string {
	return []string{
		SDDWarnHistoryIrreversible,
		SDDWarnRemotePrivacy,
		SDDWarnNoContentScan,
		SDDWarnNoCrossMachineSyncYet,
	}
}

// SDDPlan summarises what an enable or export would (or did) write.
type SDDPlan struct {
	BacklogCount int
	SpecCount    int
}

// SDDEnableResult is EnableSDDRepo's return value. Applied mirrors
// DeactivateResult's own shape (SPEC-105 DD18): the same struct serves
// both the preview and the executed outcome, distinguished only by this
// field, so a caller reads one type regardless of --apply.
type SDDEnableResult struct {
	Applied  bool
	RepoRoot string
	Plan     SDDPlan
	Warnings []string
	Remote   string // whatever `git remote get-url origin` reports locally, "" if none
}

// SDDDisableResult is DisableSDDRepo's return value.
type SDDDisableResult struct {
	Applied  bool
	RepoRoot string
}

// SDDExportResult is ExportSDDRepo's return value.
type SDDExportResult struct {
	RepoRoot string
	Plan     SDDPlan
}

// SDDStatusResult is SDDStatus's return value.
type SDDStatusResult struct {
	RepoRoot     string
	Enabled      bool
	Plan         SDDPlan
	PendingGit   string   // raw `git status --porcelain -- .mneme/sdd` output
	Broken       []string // files that failed to parse (D22/D28/D37)
	ForeignPaths []string // files whose anchor the local base does not know (D45)
}

// EnableSDDRepo previews (default) or applies (apply=true) turning the SDD
// mechanism on for repoRoot (D3/D8/D17). Without apply it writes NOTHING —
// not even a probe file — and returns the plan plus the four warnings
// (AC12/AC14). With apply it exports every backlog item and spec (D8,
// including archived/done ones), writes the marker, and adds sdd.off to
// .mneme/.gitignore (D29/D41). Refuses, before writing anything, when the
// repository is not a git repo or is not convergent with the local
// database (D45).
func (svc *SDDService) EnableSDDRepo(ctx context.Context, repoRoot string, apply bool) (*SDDEnableResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: sdd enable: repoRoot is required")
	}
	g := sddGit{RepoDir: repoRoot}
	if !g.IsWorkTree() {
		return nil, fmt.Errorf("service: sdd enable: %s: %w", repoRoot, ErrSDDNotGitRepo)
	}

	if err := svc.checkSDDConvergence(ctx, repoRoot); err != nil {
		return nil, err
	}

	plan, err := svc.sddPlan(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: sdd enable: plan: %w", err)
	}

	result := &SDDEnableResult{
		RepoRoot: repoRoot,
		Plan:     plan,
		Warnings: sddWarnings(),
		Remote:   g.RemoteURL(),
	}

	if !apply {
		return result, nil
	}

	// The marker is written BEFORE exporting, not after: materializeBacklogItem/
	// materializeSpec both gate on ResolveSDDState(repoRoot).Enabled, which
	// reads the marker's presence. Writing it first is what makes the
	// export below actually write anything, rather than reading as
	// "mechanism still off" for every one of its own calls.
	now := time.Now().UTC().Format(time.RFC3339)
	existing, err := sddfile.ReadMarker(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd enable: read marker: %w", err)
	}
	createdAt := now
	if existing != nil && existing.CreatedAt != "" {
		createdAt = existing.CreatedAt
	}
	if err := sddfile.WriteMarker(repoRoot, sddfile.Marker{
		SDDVersion: 1, Project: svc.project,
		CreatedAt: createdAt, LastExportAt: now,
		BacklogCount: plan.BacklogCount, SpecCount: plan.SpecCount,
	}); err != nil {
		return nil, fmt.Errorf("service: sdd enable: write marker: %w", err)
	}

	if err := svc.exportAllSDD(ctx, repoRoot); err != nil {
		return nil, fmt.Errorf("service: sdd enable: export: %w", err)
	}

	if err := ensureMnemeGitignore(repoRoot, "sdd.off"); err != nil {
		return nil, fmt.Errorf("service: sdd enable: gitignore: %w", err)
	}

	result.Applied = true
	return result, nil
}

// DisableSDDRepo previews (default) or applies (apply=true) turning the
// mechanism off LOCALLY for repoRoot (D3/D19/D29). It never deletes
// anything under .mneme/sdd/ — only writes .mneme/sdd.off (gitignored) so
// this machine's own wrappers become inert.
func (svc *SDDService) DisableSDDRepo(_ context.Context, repoRoot string, apply bool) (*SDDDisableResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: sdd disable: repoRoot is required")
	}
	result := &SDDDisableResult{RepoRoot: repoRoot}
	if !apply {
		return result, nil
	}

	offPath := sddOffPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(offPath), 0o755); err != nil {
		return nil, fmt.Errorf("service: sdd disable: mkdir: %w", err)
	}
	if err := os.WriteFile(offPath, nil, 0o644); err != nil {
		return nil, fmt.Errorf("service: sdd disable: write sdd.off: %w", err)
	}
	if err := ensureMnemeGitignore(repoRoot, "sdd.off"); err != nil {
		return nil, fmt.Errorf("service: sdd disable: gitignore: %w", err)
	}

	result.Applied = true
	return result, nil
}

// ExportSDDRepo re-materializes every backlog item and spec from the
// database (the idempotent repair path, D45's same convergence guard).
// Requires the mechanism to already be enabled — export repairs an
// enabled repository, it does not turn one on.
func (svc *SDDService) ExportSDDRepo(ctx context.Context, repoRoot string) (*SDDExportResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: sdd export: repoRoot is required")
	}

	marker, err := sddfile.ReadMarker(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd export: read marker: %w", err)
	}
	if marker == nil {
		return nil, fmt.Errorf("service: sdd export: %s: %w", repoRoot, ErrSDDNotEnabled)
	}

	if err := svc.checkSDDConvergence(ctx, repoRoot); err != nil {
		return nil, err
	}

	if err := svc.exportAllSDD(ctx, repoRoot); err != nil {
		return nil, fmt.Errorf("service: sdd export: %w", err)
	}

	plan, err := svc.sddPlan(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: sdd export: plan: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	marker.LastExportAt = now
	marker.BacklogCount = plan.BacklogCount
	marker.SpecCount = plan.SpecCount
	if err := sddfile.WriteMarker(repoRoot, *marker); err != nil {
		return nil, fmt.Errorf("service: sdd export: write marker: %w", err)
	}

	return &SDDExportResult{RepoRoot: repoRoot, Plan: plan}, nil
}

// SDDStatus reports the mechanism's state for repoRoot: enabled or not,
// how many backlog items/specs the database has, what git reports as
// pending under .mneme/sdd, and — without refusing anything, unlike
// Enable/Export — which files are broken or carry a foreign anchor
// (D22/D45), so an operator can see the problem before deciding to fix it.
func (svc *SDDService) SDDStatus(ctx context.Context, repoRoot string) (*SDDStatusResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: sdd status: repoRoot is required")
	}

	state := ResolveSDDState(repoRoot)

	plan, err := svc.sddPlan(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: sdd status: plan: %w", err)
	}

	broken, foreign, err := svc.scanSDDRecords(ctx, repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd status: scan: %w", err)
	}

	g := sddGit{RepoDir: repoRoot}
	pending := ""
	if g.IsWorkTree() {
		if out, err := g.PorcelainStatus(); err == nil {
			pending = out
		}
	}

	return &SDDStatusResult{
		RepoRoot: repoRoot, Enabled: state.Enabled, Plan: plan,
		PendingGit: pending, Broken: broken, ForeignPaths: foreign,
	}, nil
}

// sddPlan counts how many backlog items and specs THIS project has in the
// database — the "cuantos items y specs se exportarian" plan (D17).
func (svc *SDDService) sddPlan(ctx context.Context) (SDDPlan, error) {
	_, backlogTotal, err := svc.store.ListBacklogItems(ctx, svc.project, "", 0)
	if err != nil {
		return SDDPlan{}, fmt.Errorf("count backlog items: %w", err)
	}
	_, specTotal, err := svc.store.ListSpecs(ctx, svc.project, "", 0)
	if err != nil {
		return SDDPlan{}, fmt.Errorf("count specs: %w", err)
	}
	return SDDPlan{BacklogCount: backlogTotal, SpecCount: specTotal}, nil
}

// exportAllSDD re-materializes every backlog item and every spec of
// svc.project — D8's "al encender se exporta TODO", including archived
// items and done specs. It calls the SAME materializeBacklogItem/
// materializeSpec every wrapper uses, so this is not a second writer: it
// is the first writer, called once per record instead of once per
// mutation.
func (svc *SDDService) exportAllSDD(ctx context.Context, repoRoot string) error {
	prevRepoDir := svc.repoDir
	svc.repoDir = repoRoot
	defer func() { svc.repoDir = prevRepoDir }()

	items, _, err := svc.store.ListBacklogItems(ctx, svc.project, "", 0)
	if err != nil {
		return fmt.Errorf("list backlog items: %w", err)
	}
	for _, item := range items {
		svc.materializeBacklogItem(ctx, item.ID)
	}

	specs, _, err := svc.store.ListSpecs(ctx, svc.project, "", 0)
	if err != nil {
		return fmt.Errorf("list specs: %w", err)
	}
	for _, spec := range specs {
		svc.materializeSpec(ctx, spec.ID)
	}
	return nil
}

// scanSDDRecords walks every record repoRoot already carries and splits
// them into "broken" (fails to parse — D22/D28/D37) and "foreign" (parses
// fine but its anchor is not known to this database — D45). Both slices
// are file paths, so a caller can name them directly.
//
// Classification is delegated to sddfile.ClassifyRecordPath (SPEC-131 D63),
// replacing the old inline heuristic (filepath.Base(path) == "record.md",
// W7): a path ClassifyRecordPath does not recognise — an entregable like
// plan.md/spec.md that BL-196 will deposit inside specs/SPEC-NNN/, or a
// stray .md with no valid correlative — is IGNORED here, never reported as
// broken. Ignored is not an error; it is content this mechanism does not
// read yet.
func (svc *SDDService) scanSDDRecords(ctx context.Context, repoRoot string) (broken, foreign []string, err error) {
	paths, err := sddfile.ListRecords(sddfile.RootDir(repoRoot))
	if err != nil {
		return nil, nil, fmt.Errorf("list records: %w", err)
	}
	if len(paths) == 0 {
		return nil, nil, nil
	}

	type okRecord struct {
		path string
		uuid string
	}
	var ok []okRecord

	for _, path := range paths {
		kind, _, classified := sddfile.ClassifyRecordPath(repoRoot, path)
		if !classified {
			continue
		}

		data, rErr := sddfile.ReadRecord(path)
		if rErr != nil {
			broken = append(broken, path)
			continue
		}

		var uuid string
		switch kind {
		case sddfile.KindSpec:
			rec, uErr := sddfile.UnmarshalSpec(data)
			if uErr != nil {
				broken = append(broken, path)
				continue
			}
			uuid = rec.Spec.UUID
		case sddfile.KindBacklog:
			rec, uErr := sddfile.UnmarshalBacklog(data)
			if uErr != nil {
				broken = append(broken, path)
				continue
			}
			uuid = rec.Item.UUID
		case sddfile.KindIgnored:
			continue
		}
		if uuid != "" {
			ok = append(ok, okRecord{path: path, uuid: uuid})
		}
	}

	if len(ok) == 0 {
		return broken, nil, nil
	}

	anchors := make([]string, len(ok))
	for i, r := range ok {
		anchors[i] = r.uuid
	}
	known, kErr := svc.store.RefsForUUIDs(ctx, anchors)
	if kErr != nil {
		return nil, nil, fmt.Errorf("resolve anchors: %w", kErr)
	}
	for _, r := range ok {
		if _, present := known[r.uuid]; !present {
			foreign = append(foreign, r.path)
		}
	}
	return broken, foreign, nil
}

// checkSDDConvergence is D45: refuse, before writing a single byte, when
// repoRoot's existing SDD records contain a broken file or a foreign
// anchor. See scanSDDRecords for the underlying scan and ErrSDDNotConverged
// for the sentinel every case wraps.
func (svc *SDDService) checkSDDConvergence(ctx context.Context, repoRoot string) error {
	broken, foreign, err := svc.scanSDDRecords(ctx, repoRoot)
	if err != nil {
		return fmt.Errorf("service: sdd convergence: %w", err)
	}
	if len(broken) > 0 {
		return fmt.Errorf(
			"service: sdd convergence: %d archivo(s) no se pueden leer (%s): %w",
			len(broken), strings.Join(broken, ", "), ErrSDDNotConverged)
	}
	if len(foreign) > 0 {
		return fmt.Errorf(
			"service: sdd convergence: %d registro(s) tienen un ancla que la base local no conoce (%s) — "+
				"leerlos llega con BL-201, reconciliarlos con BL-202: %w",
			len(foreign), strings.Join(foreign, ", "), ErrSDDNotConverged)
	}
	return nil
}
