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
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
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

	// SDDWarnNoCrossMachineSyncYet: §2a's own limitation is REPLACED, not
	// deleted, once §2b (SPEC-131) lands the read path (W12): reading now
	// exists, but only on a machine that installed the git hooks, and a
	// correlative collision between two machines is detected, never
	// resolved (that is BL-202). The phrase "revisarse en un pull request"
	// is deliberately preserved verbatim — it stays true and a substring
	// check in this repository's own tests still relies on it.
	SDDWarnNoCrossMachineSyncYet = "hoy estos archivos sirven para revisarse en un pull request; ademas, entran en la base de otra maquina tras cada git pull o cambio de rama si esa maquina tiene los enganches instalados (mneme sdd hooks install); dos personas que crean el mismo correlativo a la vez producen un choque que mneme detecta y reporta, pero todavia no resuelve"
)

// SDDWarnings is the fixed, ordered list every dry-run preview and every
// applied enable prints — always all four, never a subset. Exported
// (SPEC-140 AC11/AC12) so a caller outside this package can derive its
// assertions by iterating the real list instead of copying the four
// literal strings by hand.
func SDDWarnings() []string {
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

	// Unreadable names every backlog/spec row this plan's own counting could
	// identify but not fully read (SPEC-133 D1/D6/D11). BacklogCount/
	// SpecCount are NOT reduced by it — they remain the true SQL totals,
	// counting these rows too — so a row appearing here is purely additive
	// information, never a discrepancy to reconcile against the counts.
	Unreadable []model.UnreadableRow
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

	// GitattrsFindings carries whatever EnsureGitattributes reported for
	// this --apply run (SPEC-140 D11): populated only when Applied is
	// true — the preview path never writes anything, .gitattributes
	// included (AC10). Empty when the repository already resolved eol=lf
	// everywhere.
	GitattrsFindings []DriftFinding

	// AlreadyEnabled is true when this call took the "ya encendido" early
	// branch (SPEC-140 D5): a committed marker was found and apply was
	// false. It is never true together with Applied — the early branch
	// always returns before --apply's own logic ever runs (D5's "con
	// --apply: no se cambia absolutamente nada"). When true, Plan and
	// Warnings are left at their zero values: this branch is not a plan
	// to publish anything, so there is nothing to warn about (AC12).
	AlreadyEnabled bool

	// EnabledSince is the marker's own CreatedAt (D6 point 1) — the
	// team-wide activation date, not this machine's. Set only when
	// AlreadyEnabled is true.
	EnabledSince string

	// HooksInstalled reports whether THIS machine's own SDD git hooks are
	// already installed (D6 point 2, via SDDHooksInstalled). Set only
	// when AlreadyEnabled is true.
	HooksInstalled bool

	// UnknownToThisBase lists SDD record paths whose anchor this
	// machine's local database does not know (D6 point 2) — the normal
	// state of a fresh clone that has never run `mneme sdd import`, never
	// a rejection here (that refusal is D45's, reserved for --apply and
	// for repositories with no marker at all — AC13's control). Set only
	// when AlreadyEnabled is true.
	UnknownToThisBase []string
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

// SDDStatusResult is SDDStatus's return value. Every field beyond
// RepoRoot/Enabled/Plan/PendingGit is DERIVED at the moment of the call —
// there is no dedicated state file behind any of them (SPEC-131 D54).
type SDDStatusResult struct {
	RepoRoot     string
	Enabled      bool
	Plan         SDDPlan
	PendingGit   string   // raw `git status --porcelain -- .mneme/sdd` output
	Broken       []string // files that failed to parse (D22/D28/D37)
	ForeignPaths []string // files whose anchor the local base does not know (D45)

	// Conflicted names files D50's CASE A-renumbered or CASE C would skip
	// right now — re-derived by running ImportSDDFromRepo in preview mode
	// (SPEC-131 D54).
	Conflicted []string

	// Incomplete names files Missing() reports gaps for — the same closed
	// vocabulary rewriteCompletedRecord fills on the next import (D53).
	Incomplete []string

	// Divergent names files whose bytes differ from the canonical
	// serialization of the row currently in the database — what `mneme sdd
	// export` repairs.
	Divergent []string

	// HooksInstalled reports whether EVERY target hook (post-merge,
	// post-checkout) under repoRoot already carries the SDD marker. A
	// repository that clones with the marker committed but never ran
	// `mneme sdd hooks install` imports nothing, silently, without this
	// signal.
	HooksInstalled bool

	// OnlyInBaseCount is the total number of correlatives that exist in
	// the local database but have no file on this branch (D13/D62) —
	// normal on a working branch, not an error.
	OnlyInBaseCount int

	// OnlyInBaseError mirrors SDDImportResult's own field of the same
	// name (SPEC-131 round 4): set when the preview import's own "only in
	// base" calculation failed, so OnlyInBaseCount==0 never has to be
	// read as "genuinely nothing" when it might mean "could not compute" —
	// the same distinction NoOpReason already makes for the whole import.
	OnlyInBaseError string

	// FrozenBlocked names spec files D64 would skip right now: a spec
	// frozen by SPEC-125 (its originating item archived) whose file brings
	// a different status than the one recorded locally.
	FrozenBlocked []string
}

// EnableSDDRepo previews (default) or applies (apply=true) turning the SDD
// mechanism on for repoRoot (D3/D8/D17). Without apply it writes NOTHING —
// not even a probe file — and returns the plan plus the four warnings
// (AC12/AC14). With apply it exports every backlog item and spec (D8,
// including archived/done ones), writes the marker, adds sdd.off to
// .mneme/.gitignore (D29/D41), and installs THIS machine's own git hooks
// (SPEC-131 D57) — without the last step the mechanism would turn on and
// stay mute for the very machine that enabled it. Refuses, before writing
// anything, when the repository is not a git repo or is not convergent
// with the local database (D45).
func (svc *SDDService) EnableSDDRepo(ctx context.Context, repoRoot string, apply bool) (*SDDEnableResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: sdd enable: repoRoot is required")
	}
	g := sddGit{RepoDir: repoRoot}
	if !g.IsWorkTree() {
		return nil, fmt.Errorf("service: sdd enable: %s: %w", repoRoot, ErrSDDNotGitRepo)
	}

	// SPEC-140 D5/D45: a committed marker means the team already decided
	// to publish — the situation this branch reports is "repository
	// already activated, new machine", never "publishing for the first
	// time". Read the marker BEFORE checkSDDConvergence: records this
	// base does not know are the ordinary state of a fresh clone, not a
	// convergence failure, and taking the early return here is what fixes
	// the reproduction where `enable` used to abort before printing
	// anything at all. Gated on !apply so `--apply` in any case — marker
	// present or not — keeps running today's reexport-and-refuse-on-
	// non-convergence path completely unchanged (D5's "con --apply: no se
	// cambia absolutamente nada").
	marker, err := sddfile.ReadMarker(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd enable: read marker: %w", err)
	}
	if marker != nil && !apply {
		return svc.alreadyEnabledSDDResult(ctx, repoRoot, marker)
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
		Warnings: SDDWarnings(),
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

	exportUnreadable, err := svc.exportAllSDD(ctx, repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd enable: export: %w", err)
	}
	// Replaces the pre-apply preview (SPEC-133 D12): the export just ran the
	// authoritative read, over the exact rows it materialized (or did not).
	result.Plan.Unreadable = exportUnreadable

	if err := ensureMnemeGitignore(repoRoot, "sdd.off"); err != nil {
		return nil, fmt.Errorf("service: sdd enable: gitignore: %w", err)
	}

	// SPEC-140 D11: `sdd enable --apply` is one of the two verbs (besides
	// `mneme init`) that writes repository files without ever passing
	// through init, so it ensures .gitattributes itself — ONLY on the
	// apply branch (AC10: the preview above must never write anything,
	// not even a probe file). Never fatal: a git failure here must not
	// undo the export that already happened above.
	gitattrsFindings, gitattrsErr := EnsureGitattributes(repoRoot, false)
	if gitattrsErr != nil {
		slog.WarnContext(ctx, "sdd_enable_gitattributes_error", "error", gitattrsErr)
	} else {
		result.GitattrsFindings = gitattrsFindings
	}

	if err := svc.InstallSDDHooks(repoRoot); err != nil {
		return nil, fmt.Errorf("service: sdd enable: install hooks: %w", err)
	}

	result.Applied = true
	return result, nil
}

// alreadyEnabledSDDResult builds EnableSDDRepo's "ya encendido" early-branch
// result (SPEC-140 D5/D6/T3.1): no convergence check runs (D45's refusal
// exists to protect a genuine first publication, not this case), no
// warnings are populated (nothing is being published here), and no
// plan/count header is computed — instead, exactly what THIS machine still
// needs: whether its own git hooks are installed, and which committed
// records its local database does not know yet (the ordinary state of a
// fresh clone). scanSDDRecords is used here to INFORM, never to refuse —
// unlike checkSDDConvergence's use of the same scan.
func (svc *SDDService) alreadyEnabledSDDResult(ctx context.Context, repoRoot string, marker *sddfile.Marker) (*SDDEnableResult, error) {
	_, foreign, err := svc.scanSDDRecords(ctx, repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd enable: scan records: %w", err)
	}
	return &SDDEnableResult{
		RepoRoot:          repoRoot,
		AlreadyEnabled:    true,
		EnabledSince:      marker.CreatedAt,
		HooksInstalled:    svc.SDDHooksInstalled(repoRoot),
		UnknownToThisBase: foreign,
	}, nil
}

// DisableSDDRepo previews (default) or applies (apply=true) turning the
// mechanism off LOCALLY for repoRoot (D3/D19/D29). It never deletes
// anything under .mneme/sdd/ — only writes .mneme/sdd.off (gitignored) so
// this machine's own wrappers become inert.
//
// With apply, THREE things happen in this exact order (SPEC-131 D19/D57):
//  1. Import once more (ImportSDDFromRepo), so anything a teammate already
//     published — and this machine has not yet read — is not lost.
//  2. Write .mneme/sdd.off.
//  3. Remove this machine's own git hooks.
//
// Reversing steps 1 and 2 would make the import a silent no-op: it would
// find the mechanism already disabled and do nothing, quietly dropping
// D19's whole point. A partial failure after step 1 still leaves whatever
// the import already wrote to the database — nothing here is undone by a
// later step's own failure.
func (svc *SDDService) DisableSDDRepo(ctx context.Context, repoRoot string, apply bool) (*SDDDisableResult, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("service: sdd disable: repoRoot is required")
	}
	result := &SDDDisableResult{RepoRoot: repoRoot}
	if !apply {
		return result, nil
	}

	if _, err := svc.ImportSDDFromRepo(ctx, repoRoot, true); err != nil {
		return nil, fmt.Errorf("service: sdd disable: final import: %w", err)
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

	if err := svc.RemoveSDDHooks(repoRoot); err != nil {
		return nil, fmt.Errorf("service: sdd disable: remove hooks: %w", err)
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

	exportUnreadable, err := svc.exportAllSDD(ctx, repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd export: %w", err)
	}

	plan, err := svc.sddPlan(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: sdd export: plan: %w", err)
	}
	// The authoritative read of what the export actually could not
	// materialize (SPEC-133 D12) — the same rows sddPlan's own read of the
	// unchanged database already names, made explicit rather than implicit.
	plan.Unreadable = exportUnreadable

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

	incomplete, err := scanSDDIncomplete(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd status: incomplete: %w", err)
	}
	divergent, err := svc.scanSDDDivergent(ctx, repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: sdd status: divergent: %w", err)
	}

	// Conflicted/FrozenBlocked/OnlyInBaseCount are all D50/D64's own
	// decision, re-derived by running the importer itself in preview mode
	// (apply=false) — never a second, independently-written copy of that
	// logic that could disagree with what an actual import would do.
	var conflicted, frozenBlocked []string
	dryRun, err := svc.ImportSDDFromRepo(ctx, repoRoot, false)
	if err != nil {
		return nil, fmt.Errorf("service: sdd status: preview import: %w", err)
	}
	for _, s := range dryRun.Skipped {
		switch {
		case s.Reason == "ancla-renumerada-en-otra-maquina",
			s.Reason == "ancla-duplicada-en-la-misma-tanda",
			strings.HasPrefix(s.Reason, "correlativo-reclamado-por-dos-elementos"):
			conflicted = append(conflicted, s.Path)
		case s.Reason == "spec-congelada":
			frozenBlocked = append(frozenBlocked, s.Path)
		}
	}
	return &SDDStatusResult{
		RepoRoot: repoRoot, Enabled: state.Enabled, Plan: plan,
		PendingGit: pending, Broken: broken, ForeignPaths: foreign,
		Conflicted: conflicted, Incomplete: incomplete, Divergent: divergent,
		HooksInstalled: svc.SDDHooksInstalled(repoRoot), OnlyInBaseCount: dryRun.OnlyInBaseTotal,
		OnlyInBaseError: dryRun.OnlyInBaseError, FrozenBlocked: frozenBlocked,
	}, nil
}

// scanSDDIncomplete walks every record repoRoot carries and names the ones
// Missing() reports a gap for — the pure classify+parse half of D54's
// Incomplete field, needing neither the database nor any decision logic
// (D53's Missing() is a method on the parsed record alone).
func scanSDDIncomplete(repoRoot string) ([]string, error) {
	paths, err := sddfile.ListRecords(sddfile.RootDir(repoRoot))
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}

	var incomplete []string
	for _, path := range paths {
		kind, _, ok := sddfile.ClassifyRecordPath(repoRoot, path)
		if !ok {
			continue
		}
		data, rErr := sddfile.ReadRecord(path)
		if rErr != nil {
			continue // already reported as Broken
		}
		switch kind {
		case sddfile.KindBacklog:
			rec, uErr := sddfile.UnmarshalBacklog(data)
			if uErr != nil {
				continue
			}
			if len(rec.Missing()) > 0 {
				incomplete = append(incomplete, path)
			}
		case sddfile.KindSpec:
			rec, uErr := sddfile.UnmarshalSpec(data)
			if uErr != nil {
				continue
			}
			if len(rec.Missing()) > 0 {
				incomplete = append(incomplete, path)
			}
		case sddfile.KindIgnored:
			// unreachable: ClassifyRecordPath never returns ok=true here.
		}
	}
	return incomplete, nil
}

// scanSDDDivergent walks every record repoRoot carries and names the ones
// whose on-disk bytes differ from the canonical serialization of the
// database row AT THE SAME CORRELATIVE — the same thing `mneme sdd export`
// repairs. A record with no matching local row (not yet imported, or
// foreign) is silently skipped: that case is already named by Broken/
// ForeignPaths, and comparing against a row that does not exist would not
// be a divergence, it would be a different problem.
func (svc *SDDService) scanSDDDivergent(ctx context.Context, repoRoot string) ([]string, error) {
	paths, err := sddfile.ListRecords(sddfile.RootDir(repoRoot))
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}

	var divergent []string
	for _, path := range paths {
		kind, id, ok := sddfile.ClassifyRecordPath(repoRoot, path)
		if !ok {
			continue
		}
		onDisk, rErr := sddfile.ReadRecord(path)
		if rErr != nil {
			continue
		}

		switch kind {
		case sddfile.KindBacklog:
			item, gErr := svc.store.GetBacklogItem(ctx, id)
			if gErr != nil {
				continue
			}
			refs, lErr := svc.store.ListBacklogRefinements(ctx, id)
			if lErr != nil {
				continue
			}
			canonical, mErr := sddfile.MarshalBacklog(&sddfile.BacklogRecord{Item: item, Refinements: refs})
			if mErr != nil {
				continue
			}
			if string(canonical) != string(onDisk) {
				divergent = append(divergent, path)
			}
		case sddfile.KindSpec:
			spec, gErr := svc.store.GetSpec(ctx, id)
			if gErr != nil {
				continue
			}
			history, hErr := svc.store.GetSpecHistory(ctx, id)
			if hErr != nil {
				continue
			}
			pushbacks, pErr := svc.store.GetAllPushbacks(ctx, id)
			if pErr != nil {
				continue
			}
			canonical, mErr := sddfile.MarshalSpec(&sddfile.SpecRecord{Spec: spec, History: history, Pushbacks: pushbacks})
			if mErr != nil {
				continue
			}
			if string(canonical) != string(onDisk) {
				divergent = append(divergent, path)
			}
		case sddfile.KindIgnored:
			// unreachable: ClassifyRecordPath never returns ok=true here.
		}
	}
	return divergent, nil
}

// sddPlan counts how many backlog items and specs THIS project has in the
// database — the "cuantos items y specs se exportarian" plan (D17).
//
// BacklogCount/SpecCount are, and remain, the true SQL COUNT(*) totals
// (SPEC-133 D6/D11) — a row this call cannot fully read still counts. The
// two Unreadable relations are merged into one, since SDDStatusResult's
// caller (SDDStatus) presents backlog and spec rows together (D8).
func (svc *SDDService) sddPlan(ctx context.Context) (SDDPlan, error) {
	_, backlogTotal, backlogUnreadable, err := svc.store.ListBacklogItems(ctx, svc.project, "", 0)
	if err != nil {
		return SDDPlan{}, fmt.Errorf("count backlog items: %w", err)
	}
	_, specTotal, specUnreadable, err := svc.store.ListSpecs(ctx, svc.project, "", 0)
	if err != nil {
		return SDDPlan{}, fmt.Errorf("count specs: %w", err)
	}

	var unreadable []model.UnreadableRow
	unreadable = append(unreadable, backlogUnreadable...)
	unreadable = append(unreadable, specUnreadable...)

	return SDDPlan{BacklogCount: backlogTotal, SpecCount: specTotal, Unreadable: unreadable}, nil
}

// exportAllSDD re-materializes every backlog item and every spec of
// svc.project — D8's "al encender se exporta TODO", including archived
// items and done specs. It calls the SAME materializeBacklogItem/
// materializeSpec every wrapper uses, so this is not a second writer: it
// is the first writer, called once per record instead of once per
// mutation.
//
// The returned relation (SPEC-133 D12) names every row this call could
// identify but not fully read — and therefore never handed to
// materializeBacklogItem/materializeSpec at all. This is not a new failure
// mode: materializeBacklogItem/materializeSpec (sdd_export.go) were already
// tolerant per-item, registering and returning without propagating
// anything on their own error. The one point that used to tear down the
// whole export was the LISTING itself, which is exactly what this spec
// fixes.
func (svc *SDDService) exportAllSDD(ctx context.Context, repoRoot string) ([]model.UnreadableRow, error) {
	prevRepoDir := svc.repoDir
	svc.repoDir = repoRoot
	defer func() { svc.repoDir = prevRepoDir }()

	items, _, itemsUnreadable, err := svc.store.ListBacklogItems(ctx, svc.project, "", 0)
	if err != nil {
		return nil, fmt.Errorf("list backlog items: %w", err)
	}
	for _, item := range items {
		svc.materializeBacklogItem(ctx, item.ID)
	}

	specs, _, specsUnreadable, err := svc.store.ListSpecs(ctx, svc.project, "", 0)
	if err != nil {
		return nil, fmt.Errorf("list specs: %w", err)
	}
	for _, spec := range specs {
		svc.materializeSpec(ctx, spec.ID)
	}

	var unreadable []model.UnreadableRow
	unreadable = append(unreadable, itemsUnreadable...)
	unreadable = append(unreadable, specsUnreadable...)
	return unreadable, nil
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
				"ejecuta `mneme sdd import` primero; reconciliarlos con BL-202: %w",
			len(foreign), strings.Join(foreign, ", "), ErrSDDNotConverged)
	}
	return nil
}
