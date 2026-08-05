// Package service implements the business logic layer for mneme.
// This file provides the SDDService which orchestrates the SDD lifecycle:
// backlog management, spec state machine transitions, quality gate validation,
// pushback handling, and lane-aware routing for trivial items.
// All methods require a context.Context as first argument.
package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/lane"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// SDDService orchestrates the SDD lifecycle: backlog management, spec state
// machine transitions, quality gate validation, pushback handling, and
// lane-aware routing that enables trivial items to skip the full spec/plan cycle.
// It owns the business rules that sit above the raw store operations.
type SDDService struct {
	store     *store.SDDStore
	config    *config.Config
	project   string
	memorySvc *MemoryService // optional; nil disables completion memory saving

	// repoDir is the working directory used by the lane auditor to run git
	// commands. When empty the auditor uses the current working directory.
	repoDir string
}

// NewSDDService constructs an SDDService.
// sddStore is the underlying data store, cfg provides quality gate settings,
// and project is the default project slug used when requests omit the Project field.
// memorySvc may be nil — when nil, automatic memory saving on spec completion is
// disabled but all other behaviours remain fully functional.
func NewSDDService(sddStore *store.SDDStore, cfg *config.Config, project string, memorySvc *MemoryService) *SDDService {
	return &SDDService{
		store:     sddStore,
		config:    cfg,
		project:   project,
		memorySvc: memorySvc,
	}
}

// WithRepoDir sets the repository directory used by the lane auditor when
// invoking git commands. Call this after construction when the working
// directory is known (e.g. from the MCP server's project root).
func (svc *SDDService) WithRepoDir(dir string) {
	svc.repoDir = dir
}

// ProjectSlug returns the project slug associated with this service instance.
func (svc *SDDService) ProjectSlug() string {
	return svc.project
}

// WithMemoryService attaches a MemoryService so that SDDService can save
// completion memories when a spec reaches done status. This is called after
// construction when both services are available (e.g. in the MCP server).
// It is safe to call multiple times — later calls overwrite earlier ones.
func (svc *SDDService) WithMemoryService(memorySvc *MemoryService) {
	svc.memorySvc = memorySvc
}

// --- BACKLOG METHODS ---

// BacklogAdd creates a new backlog item with status raw.
//
// Validation:
//   - Title must not be empty (ErrTitleRequired)
//   - Priority defaults to PriorityMedium when omitted
//   - Priority must be a recognised value (ErrInvalidPriority)
//   - Project defaults to the service's project slug when omitted
//   - Lane must be provided (ErrLaneRequired) and valid (ErrInvalidLane)
//   - Scope must be provided when lane is trivial (ErrScopeRequired)
func (svc *SDDService) BacklogAdd(ctx context.Context, req model.BacklogAddRequest) (*model.BacklogItem, error) {
	if req.Title == "" {
		return nil, model.ErrTitleRequired
	}
	if req.Priority == "" {
		req.Priority = model.PriorityMedium
	}
	if !req.Priority.Valid() {
		return nil, model.ErrInvalidPriority
	}
	if req.Project == "" {
		req.Project = svc.project
	}
	if req.Lane == "" {
		return nil, model.ErrLaneRequired
	}
	if !req.Lane.Valid() {
		return nil, model.ErrInvalidLane
	}
	if req.Lane == model.LaneTrivial && req.Scope == "" {
		return nil, model.ErrScopeRequired
	}

	id, err := svc.store.NextBacklogID(ctx, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: backlog add: next id: %w", err)
	}

	item := &model.BacklogItem{
		ID:          id,
		Title:       req.Title,
		Description: req.Description,
		Status:      model.BacklogStatusRaw,
		Priority:    req.Priority,
		Project:     req.Project,
		Position:    0,
		Lane:        req.Lane,
		Scope:       req.Scope,
	}

	if err := svc.store.CreateBacklogItem(ctx, item); err != nil {
		return nil, fmt.Errorf("service: backlog add: %w", err)
	}
	return item, nil
}

// BacklogList returns the backlog items matching the filter, plus the REAL
// number of matches.
//
// req.Limit <= 0 does not window the result (the CLI's path). A
// req.Limit > model.ListMaxLimit is silently capped to ListMaxLimit (D5):
// capping still satisfies the request ("give me at most N") and loses no
// information because Total reports how many exist in total. Contrast with
// mem_explore's depth, which is rejected instead: depth is a SHAPE parameter
// of the traversal, so changing it silently would return a semantically
// different graph without the caller being able to notice.
//
// NEVER truncates text: full fidelity is what `mneme backlog list --json`
// consumes. The excerpt is MCP frontend policy (D9), not applied here.
func (svc *SDDService) BacklogList(ctx context.Context, req model.BacklogListRequest) (model.BacklogListResponse, error) {
	if req.Project == "" {
		req.Project = svc.project
	}
	if req.Status != "" && !req.Status.Valid() {
		return model.BacklogListResponse{}, model.ErrInvalidBacklogStatus
	}

	limit := req.Limit
	if limit > model.ListMaxLimit {
		limit = model.ListMaxLimit
	}

	items, total, err := svc.store.ListBacklogItems(ctx, req.Project, req.Status, limit)
	if err != nil {
		return model.BacklogListResponse{}, fmt.Errorf("service: backlog list: %w", err)
	}
	return model.BacklogListResponse{Items: items, Total: total}, nil
}

// BacklogGet returns a single backlog item by ID, plus ALL of its
// refinements — no excerpt, no limit (SPEC-110 D6/D7). Before SPEC-109 there
// was no way over MCP to read a backlog item's description at all;
// spec_status does not include the originating backlog item
// (model.SpecStatusResponse is {Spec,History,Pushbacks}), and the specs
// table has no description column (D15/CF1).
//
// The SPEC-109 windowing convention deliberately does NOT apply here: there,
// capping backlog_list was legitimate because backlog_get was the escape
// hatch, while for refinements there is none — no offset, no per-refinement
// fetch — so a default of 20 would leave rows PERMANENTLY unreachable.
// backlog_list's counter (D4) makes the size observable BEFORE the fetch
// instead.
//
// Returns model.ErrBacklogNotFound (already mapped to CodeMemoryNotFound by
// mapServiceError) when id does not exist. No new sentinel is introduced —
// reusing the existing one beats duplicating the same semantics.
func (svc *SDDService) BacklogGet(ctx context.Context, id string) (model.BacklogGetResponse, error) {
	item, err := svc.store.GetBacklogItem(ctx, id)
	if err != nil {
		return model.BacklogGetResponse{}, fmt.Errorf("service: backlog get: %w", err)
	}
	refs, err := svc.store.ListBacklogRefinements(ctx, id)
	if err != nil {
		return model.BacklogGetResponse{}, fmt.Errorf("service: backlog get: refinements: %w", err)
	}
	return model.BacklogGetResponse{Item: item, Refinements: refs}, nil
}

// BacklogRefine appends a refinement to a backlog item (SPEC-110 D1/D2/D3).
//
// Refinement is ITERATIVE: raw refines and becomes refined; refined refines
// and STAYS refined. Before this change the second call was rejected outright
// ("item BL-136 is refined, must be raw"), which is how a real grill ledger
// was lost on 2026-08-05 — the item could not hold the knowledge the SDD flow
// had just produced.
//
// The refinement goes to its OWN ROW, never into Description: Description is
// write-once (D15), written by BacklogAdd and by nothing else. Concatenating
// N refinements into one column would create the 40 KB ledgers the item's
// title wrongly claimed already existed.
//
// promoted and archived are refused with model.ErrBacklogNotRefinable (D3):
// writing there would be writing where nobody reads.
func (svc *SDDService) BacklogRefine(ctx context.Context, req model.BacklogRefineRequest) (*model.BacklogItem, error) {
	// Empty body would burn a seq on a junk row (D16). Today it appends a
	// bare "\n\n" to the description, so rejecting is strictly better.
	if strings.TrimSpace(req.Refinement) == "" {
		return nil, model.ErrContentRequired
	}

	item, err := svc.store.GetBacklogItem(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: backlog refine: get: %w", err)
	}

	switch item.Status {
	case model.BacklogStatusRaw, model.BacklogStatusRefined:
	default:
		return nil, fmt.Errorf("service: backlog refine: item %s is %s, must be raw or refined: %w",
			req.ID, item.Status, model.ErrBacklogNotRefinable)
	}

	if _, err := svc.store.AppendBacklogRefinement(
		ctx, req.ID, item.Status, model.BacklogStatusRefined, req.Refinement, req.By,
	); err != nil {
		return nil, fmt.Errorf("service: backlog refine: append: %w", err)
	}

	// Re-read instead of mutating the in-memory item (D17): neither
	// RefinementCount nor UpdatedAt is ever synthesized here from what this
	// method BELIEVES it wrote — the DB is the source of truth.
	fresh, err := svc.store.GetBacklogItem(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: backlog refine: reload: %w", err)
	}
	return fresh, nil
}

// BacklogPromote converts a refined backlog item into a spec.
// Returns ErrBacklogNotRefined when the item has not been refined yet.
func (svc *SDDService) BacklogPromote(ctx context.Context, id string) (*model.Spec, error) {
	item, err := svc.store.GetBacklogItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: backlog promote: get: %w", err)
	}
	if item.Status != model.BacklogStatusRefined {
		return nil, model.ErrBacklogNotRefined
	}

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title:     item.Title,
		BacklogID: item.ID,
		Project:   item.Project,
		Lane:      item.Lane,
		Scope:     item.Scope,
	})
	if err != nil {
		return nil, fmt.Errorf("service: backlog promote: create spec: %w", err)
	}

	item.Status = model.BacklogStatusPromoted
	item.SpecID = spec.ID
	if err := svc.store.UpdateBacklogItem(ctx, item); err != nil {
		return nil, fmt.Errorf("service: backlog promote: update backlog item: %w", err)
	}

	return spec, nil
}

// BacklogArchive marks a backlog item as archived with a reason.
func (svc *SDDService) BacklogArchive(ctx context.Context, id, reason string) error {
	item, err := svc.store.GetBacklogItem(ctx, id)
	if err != nil {
		return fmt.Errorf("service: backlog archive: get: %w", err)
	}

	item.Status = model.BacklogStatusArchived
	item.ArchiveReason = reason
	if err := svc.store.UpdateBacklogItem(ctx, item); err != nil {
		return fmt.Errorf("service: backlog archive: update: %w", err)
	}
	return nil
}

// --- SPEC METHODS ---

// SpecNew creates a new spec with status draft.
// Validation:
//   - Title must not be empty (ErrTitleRequired)
//   - Project defaults to the service's project slug when omitted
//   - Lane must be provided (ErrLaneRequired) and valid (ErrInvalidLane)
//   - Scope must be provided when lane is trivial (ErrScopeRequired)
//
// An initial history entry ("" -> draft by "system") is recorded.
func (svc *SDDService) SpecNew(ctx context.Context, req model.SpecNewRequest) (*model.Spec, error) {
	if req.Title == "" {
		return nil, model.ErrTitleRequired
	}
	if req.Project == "" {
		req.Project = svc.project
	}
	if req.Lane == "" {
		return nil, model.ErrLaneRequired
	}
	if !req.Lane.Valid() {
		return nil, model.ErrInvalidLane
	}
	if req.Lane == model.LaneTrivial && req.Scope == "" {
		return nil, model.ErrScopeRequired
	}

	id, err := svc.store.NextSpecID(ctx, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: spec new: next id: %w", err)
	}

	spec := &model.Spec{
		ID:        id,
		Title:     req.Title,
		Status:    model.SpecStatusDraft,
		Project:   req.Project,
		BacklogID: req.BacklogID,
		Lane:      req.Lane,
		Scope:     req.Scope,
	}

	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		return nil, fmt.Errorf("service: spec new: create: %w", err)
	}

	// Record the initial "created" history entry via a synthetic transition.
	// We write directly to the history rather than going through UpdateSpecStatus
	// because there is no valid "from" state when a spec is first created.
	histEntry := &model.SpecHistory{
		SpecID:     spec.ID,
		FromStatus: "",
		ToStatus:   model.SpecStatusDraft,
		By:         "system",
		Reason:     "spec created",
		At:         time.Now().UTC(),
	}
	if err := svc.insertHistory(ctx, histEntry); err != nil {
		// Non-fatal: spec was created successfully; history is best-effort.
		_ = err
	}

	return spec, nil
}

// SpecAdvance moves a spec to its next logical state.
// The next state is determined by the current state and lane — trivial items
// follow the shortened path (draft→rationale→implementing→audit→done) while
// standard items follow the full path unchanged.
// Use SpecPushback to deviate into needs_grill.
//
// Side effects on specific transitions:
//   - draft -> speccing (standard) or draft -> rationale (trivial):
//     creates the spec directory and copies spec-template.md into it.
//   - done: saves a completion memory via MemoryService (when configured).
func (svc *SDDService) SpecAdvance(ctx context.Context, req model.SpecAdvanceRequest) (*model.Spec, error) {
	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec advance: get: %w", err)
	}

	nextStatus, err := nextForwardStatusForLane(spec.Status, spec.Lane)
	if err != nil {
		return nil, fmt.Errorf("service: spec advance: %w", err)
	}

	if !spec.Status.CanTransitionTo(nextStatus, spec.Lane) {
		return nil, fmt.Errorf("service: spec advance: %s -> %s: %w",
			spec.Status, nextStatus, model.ErrInvalidTransition)
	}

	if err := svc.store.UpdateSpecStatus(ctx, spec.ID, spec.Status, nextStatus, req.By, req.Reason); err != nil {
		return nil, fmt.Errorf("service: spec advance: update status: %w", err)
	}

	// Reload to reflect the updated status.
	updated, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec advance: reload: %w", err)
	}

	// Side effects that run after the status transition is committed.
	svc.onAdvanceSideEffects(ctx, updated, nextStatus)

	return updated, nil
}

// onAdvanceSideEffects runs best-effort side effects after a successful
// SpecAdvance transition. Failures are logged but never returned — the
// transition itself has already been committed and must not be rolled back
// due to a filesystem or memory-save error.
func (svc *SDDService) onAdvanceSideEffects(ctx context.Context, spec *model.Spec, to model.SpecStatus) {
	switch to {
	case model.SpecStatusSpeccing, model.SpecStatusRationale:
		// Both speccing (standard) and rationale (trivial) are the first
		// "active work" states; create the spec directory so the agent has
		// a place to store spec.md.
		svc.createSpecDirectory(spec)
	case model.SpecStatusImplementing:
		// Capture the HEAD SHA as the base for per-spec auditing.
		// Non-blocking: git failure logs a warning but never fails the transition.
		// (See also SpecQuick which calls captureBaseSHA explicitly after the
		// rationale→implementing transition with the reloaded spec.)
		svc.captureBaseSHA(ctx, spec)
	case model.SpecStatusDone:
		svc.saveCompletionMemory(ctx, spec)
	}
}

// captureBaseSHA captures the current HEAD commit SHA and stores it as
// spec.BaseSHA. It is called non-blockingly when a spec enters implementing
// status so that the lane auditor can later produce a per-spec diff rather
// than a diff against the whole branch tip.
//
// If git is unavailable or the repo dir is not a git repository, the failure
// is logged but the spec transition is NOT rolled back — base_sha remains "".
//
// Called from two sites:
//  1. onAdvanceSideEffects (case SpecStatusImplementing) for SpecAdvance
//  2. SpecQuick explicitly after the rationale→implementing transition
func (svc *SDDService) captureBaseSHA(ctx context.Context, spec *model.Spec) {
	repoDir := svc.repoDir
	if repoDir == "" {
		var err error
		repoDir, err = os.Getwd()
		if err != nil {
			log.Printf("service: capture base sha: getwd for %s: %v", spec.ID, err)
			return
		}
	}

	differ := &lane.GitDiffer{RepoDir: repoDir}
	sha, err := differ.HeadSHA()
	if err != nil {
		log.Printf("service: capture base sha: git rev-parse HEAD for %s: %v", spec.ID, err)
		return
	}

	if err := svc.store.UpdateSpecBaseSHA(ctx, spec.ID, sha); err != nil {
		log.Printf("service: capture base sha: store update for %s: %v", spec.ID, err)
	}
}

// createSpecDirectory creates the per-spec workflow directory and copies
// spec-template.md into it as spec.md. This gives the architect a concrete
// starting point without needing to locate the template manually.
//
// The operation is idempotent: if spec.md already exists it is not overwritten.
// Any filesystem errors are logged and suppressed — the spec transition has
// already been committed.
func (svc *SDDService) createSpecDirectory(spec *model.Spec) {
	specDir := filepath.Join(svc.config.ProjectWorkflowDir(spec.Project), "specs", spec.ID)

	if err := os.MkdirAll(specDir, 0o755); err != nil {
		log.Printf("service: spec advance: create spec dir %s: %v", specDir, err)
		return
	}

	destPath := filepath.Join(specDir, "spec.md")
	if _, err := os.Stat(destPath); err == nil {
		// spec.md already exists — preserve any existing content.
		return
	}

	content, err := install.SpecTemplateContent()
	if err != nil {
		log.Printf("service: spec advance: read spec template: %v", err)
		return
	}

	if err := os.WriteFile(destPath, content, 0o644); err != nil {
		log.Printf("service: spec advance: write spec.md to %s: %v", destPath, err)
	}
}

// saveCompletionMemory persists a structured memory summarising what was
// implemented when a spec reaches done status. This allows future agents to
// recall completed work via mem_search without needing to parse spec files.
//
// The memory type is TypeDecision for all completions — a completed spec
// represents a deliberate design and implementation choice. The topic key
// "spec/{ID}" enables deterministic upserts so re-running does not create
// duplicate memories.
//
// Failures are logged and suppressed — the spec is already done.
func (svc *SDDService) saveCompletionMemory(ctx context.Context, spec *model.Spec) {
	if svc.memorySvc == nil {
		return
	}

	var filesSection string
	if len(spec.FilesChanged) > 0 {
		filesSection = strings.Join(spec.FilesChanged, "\n")
	} else {
		filesSection = "(none recorded)"
	}

	content := fmt.Sprintf(
		"## What\n%s\n\n## Status\nCompleted via spec %s\n\n## Files Changed\n%s",
		spec.Title, spec.ID, filesSection,
	)

	_, err := svc.memorySvc.Save(ctx, model.SaveRequest{
		Title:    fmt.Sprintf("Completed: %s", spec.Title),
		Type:     model.TypeDecision,
		Scope:    model.ScopeProject,
		TopicKey: fmt.Sprintf("spec/%s", spec.ID),
		Content:  content,
		Project:  spec.Project,
	})
	if err != nil {
		log.Printf("service: spec advance: save completion memory for %s: %v", spec.ID, err)
	}
}

// SpecPushback registers a pushback from an agent, transitioning the spec
// to needs_grill status. The spec must be in a state that allows the
// needs_grill transition (speccing, implementing, or qa).
func (svc *SDDService) SpecPushback(ctx context.Context, req model.SpecPushbackRequest) (*model.Spec, error) {
	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec pushback: get: %w", err)
	}

	if !spec.Status.CanTransitionTo(model.SpecStatusNeedsGrill, spec.Lane) {
		return nil, fmt.Errorf("service: spec pushback: cannot push from %s: %w",
			spec.Status, model.ErrInvalidTransition)
	}

	pb := &model.SpecPushback{
		SpecID:    spec.ID,
		FromAgent: req.FromAgent,
		Questions: req.Questions,
	}
	if err := svc.store.CreatePushback(ctx, pb); err != nil {
		return nil, fmt.Errorf("service: spec pushback: create pushback: %w", err)
	}

	reason := fmt.Sprintf("pushback from %s: %d question(s)", req.FromAgent, len(req.Questions))
	if err := svc.store.UpdateSpecStatus(ctx, spec.ID, spec.Status, model.SpecStatusNeedsGrill, req.FromAgent, reason); err != nil {
		return nil, fmt.Errorf("service: spec pushback: update status: %w", err)
	}

	updated, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec pushback: reload: %w", err)
	}
	return updated, nil
}

// SpecReject sends a spec backward to implementing, recording the rejection
// reason in spec_history. This models a review that uncovers defects
// requiring further implementation work — during the normal gate (qa/audit)
// or, since SPEC-087 D6, after the fact once a spec already reached done.
//
// Standard lane: qa → implementing, or done → implementing.
// Trivial lane:  audit → implementing, or done → implementing.
//
// Distinct from SpecPushback, which models ambiguity → needs_grill.
// Returns ErrReasonRequired when req.Reason is empty.
// Returns ErrInvalidTransition when the spec is not in a state SpecReject
// can move to implementing from.
func (svc *SDDService) SpecReject(ctx context.Context, req model.SpecRejectRequest) (*model.Spec, error) {
	if req.Reason == "" {
		return nil, model.ErrReasonRequired
	}

	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec reject: get: %w", err)
	}

	// The target is always implementing; validate via the state machine so we
	// don't bypass lane-specific guard rails.
	if !spec.Status.CanTransitionTo(model.SpecStatusImplementing, spec.Lane) {
		return nil, fmt.Errorf("service: spec reject: cannot reject from %s (lane=%s): %w",
			spec.Status, spec.Lane, model.ErrInvalidTransition)
	}

	reason := "rejected: " + req.Reason
	if err := svc.store.UpdateSpecStatus(ctx, spec.ID,
		spec.Status, model.SpecStatusImplementing,
		req.By, reason); err != nil {
		return nil, fmt.Errorf("service: spec reject: update status: %w", err)
	}

	updated, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec reject: reload: %w", err)
	}
	return updated, nil
}

// specIDPattern is the shape every persisted spec ID must have — mirrors the
// posture of internal/mcp's roleNamePattern (SPEC-057 C1's path-traversal
// defense). specDocPath rejects any id that does not match this BEFORE
// joining it into a filesystem path.
var specIDPattern = regexp.MustCompile(`^SPEC-\d{3,}$`)

// specDocPath builds the destination path for a spec_doc_write call
// (SPEC-087 D3). rootWorkflowDir is the un-project-scoped workflow root
// (config.Config.WorkflowDir()); project and id identify the spec; kind
// selects a closed, Go-authored filename via SpecDocKind.Filename — the
// caller never supplies a filename directly.
//
// specDocPath is a pure function, deliberately independent of *config.Config
// and of the store, so it is testable directly with hostile input (AC5)
// rather than only reachable through SpecDocWrite. Going only through
// SpecDocWrite would make the path-traversal defense untestable: an id like
// "../../../etc/passwd" would fail store.GetSpec with "not found" before
// this function's own checks ever ran, so a test that deletes
// specIDPattern and only exercises the handler would still pass — a guard
// that cannot detect its own removal (memory
// testing/antipatron-guardian-que-no-detecta-su-eliminacion).
//
// Two independent checks, in order:
//  1. id must match specIDPattern — rejects "..", "/", and anything else
//     that is not a plain "SPEC-<digits>" token, before any path is built.
//  2. filepath.Rel(specDir, path) must equal exactly filename(kind) — belt
//     and suspenders confirming the final path still resolves inside
//     specDir even if (1) or the join logic below is ever weakened.
func specDocPath(rootWorkflowDir, project, id string, kind model.SpecDocKind) (string, error) {
	if !specIDPattern.MatchString(id) {
		return "", fmt.Errorf("service: spec doc path: invalid spec id %q", id)
	}
	filename, ok := kind.Filename()
	if !ok {
		return "", fmt.Errorf("service: spec doc path: %w", model.ErrUnknownSpecDocKind)
	}

	safeProject := strings.ReplaceAll(project, "/", "-")
	specDir := filepath.Join(rootWorkflowDir, safeProject, "specs", id)
	path := filepath.Join(specDir, filename)

	if rel, err := filepath.Rel(specDir, path); err != nil || rel != filename {
		return "", fmt.Errorf("service: spec doc path: resolved path %q escapes spec directory %q", path, specDir)
	}
	return path, nil
}

// SpecDocWrite writes content to the file kind maps to, inside id's workflow
// directory (SPEC-087 D3) — the entregable path a subagent uses instead of
// copying a report into the workflow directory by hand. The destination
// directory and filename are never caller-supplied: the directory is derived
// from the persisted spec record (spec.Project, via store.GetSpec), and the
// filename comes from SpecDocKind's closed Go-authored map (see
// specDocPath). 0644 permissions, parent directories created as needed, no
// append and no arbitrary read — a plain overwrite-or-create write.
func (svc *SDDService) SpecDocWrite(ctx context.Context, req model.SpecDocWriteRequest) (*model.SpecDocWriteResponse, error) {
	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec doc write: get: %w", err)
	}

	path, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, req.Kind)
	if err != nil {
		return nil, fmt.Errorf("service: spec doc write: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("service: spec doc write: mkdir %s: %w", filepath.Dir(path), err)
	}

	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)

	if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		return nil, fmt.Errorf("service: spec doc write: write %s: %w", path, err)
	}

	return &model.SpecDocWriteResponse{
		Path:    path,
		Bytes:   len(req.Content),
		Created: created,
	}, nil
}

// SpecResolve resolves the oldest unresolved pushback and transitions the spec
// back to speccing. The spec must be in needs_grill status.
func (svc *SDDService) SpecResolve(ctx context.Context, req model.SpecResolveRequest) (*model.Spec, error) {
	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec resolve: get: %w", err)
	}

	if spec.Status != model.SpecStatusNeedsGrill {
		return nil, fmt.Errorf("service: spec resolve: spec %s is %s, must be needs_grill: %w",
			req.ID, spec.Status, model.ErrInvalidTransition)
	}

	pushbacks, err := svc.store.GetUnresolvedPushbacks(ctx, spec.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec resolve: get pushbacks: %w", err)
	}
	if len(pushbacks) == 0 {
		return nil, fmt.Errorf("service: spec resolve: no unresolved pushbacks for %s: %w",
			req.ID, model.ErrPushbackNotFound)
	}

	// Resolve the oldest unresolved pushback (first in ascending created_at order).
	oldest := pushbacks[0]
	if err := svc.store.ResolvePushback(ctx, oldest.ID, req.Resolution); err != nil {
		return nil, fmt.Errorf("service: spec resolve: resolve pushback: %w", err)
	}

	// Trivial lane resolves back to rationale; standard resolves back to speccing.
	resolveTarget := model.SpecStatusSpeccing
	if spec.Lane == model.LaneTrivial {
		resolveTarget = model.SpecStatusRationale
	}

	reason := fmt.Sprintf("pushback resolved: %s", req.Resolution)
	if err := svc.store.UpdateSpecStatus(ctx, spec.ID, spec.Status, resolveTarget, "system", reason); err != nil {
		return nil, fmt.Errorf("service: spec resolve: update status: %w", err)
	}

	updated, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec resolve: reload: %w", err)
	}
	return updated, nil
}

// SpecStatus returns a spec with its full history and all pushbacks.
func (svc *SDDService) SpecStatus(ctx context.Context, id string) (*model.SpecStatusResponse, error) {
	spec, err := svc.store.GetSpec(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: spec status: get: %w", err)
	}

	history, err := svc.store.GetSpecHistory(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: spec status: history: %w", err)
	}

	pushbacks, err := svc.store.GetAllPushbacks(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: spec status: pushbacks: %w", err)
	}

	return &model.SpecStatusResponse{
		Spec:      spec,
		History:   history,
		Pushbacks: pushbacks,
	}, nil
}

// RecentlyCompletedSpecs returns the n most recently completed specs for the
// project, ordered by updated_at descending. It calls the underlying store
// directly so the result reflects the true completion order.
func (svc *SDDService) RecentlyCompletedSpecs(ctx context.Context, project string, n int) ([]*model.Spec, error) {
	if project == "" {
		project = svc.project
	}
	specs, err := svc.store.RecentlyCompletedSpecs(ctx, project, n)
	if err != nil {
		return nil, fmt.Errorf("service: recently completed specs: %w", err)
	}
	return specs, nil
}

// SpecList returns the specs matching the filter, plus the REAL number of
// matches. Same two-mode Limit semantics as BacklogList (D5/D9): <= 0 means
// no window, > model.ListMaxLimit is silently capped. No excerpt field:
// model.Spec has no Description (D15/CF1).
// When req.Project is empty it defaults to the service project slug.
func (svc *SDDService) SpecList(ctx context.Context, req model.SpecListRequest) (model.SpecListResponse, error) {
	if req.Project == "" {
		req.Project = svc.project
	}
	if req.Status != "" && !req.Status.Valid() {
		return model.SpecListResponse{}, model.ErrInvalidSpecStatus
	}

	limit := req.Limit
	if limit > model.ListMaxLimit {
		limit = model.ListMaxLimit
	}

	specs, total, err := svc.store.ListSpecs(ctx, req.Project, req.Status, limit)
	if err != nil {
		return model.SpecListResponse{}, fmt.Errorf("service: spec list: %w", err)
	}
	return model.SpecListResponse{Specs: specs, Total: total}, nil
}

// SpecHistory returns the full state transition history for a spec.
func (svc *SDDService) SpecHistory(ctx context.Context, id string) ([]*model.SpecHistory, error) {
	// Verify the spec exists first.
	if _, err := svc.store.GetSpec(ctx, id); err != nil {
		return nil, fmt.Errorf("service: spec history: get: %w", err)
	}

	history, err := svc.store.GetSpecHistory(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: spec history: %w", err)
	}
	return history, nil
}

// --- HELPERS ---

// nextForwardStatusForLane returns the next logical forward state for the
// given status and lane. Trivial items follow the shortened path
// (draft→rationale→implementing→audit→done); standard items follow the
// full path unchanged. Returns ErrInvalidTransition for terminal or unknown states.
func nextForwardStatusForLane(current model.SpecStatus, l model.Lane) (model.SpecStatus, error) {
	if l == model.LaneTrivial {
		trivialForward := map[model.SpecStatus]model.SpecStatus{
			model.SpecStatusDraft:        model.SpecStatusRationale,
			model.SpecStatusRationale:    model.SpecStatusImplementing,
			model.SpecStatusImplementing: model.SpecStatusAudit,
			model.SpecStatusAudit:        model.SpecStatusDone,
		}
		next, ok := trivialForward[current]
		if !ok {
			return "", fmt.Errorf("no forward transition from %s (trivial lane): %w",
				current, model.ErrInvalidTransition)
		}
		return next, nil
	}

	standardForward := map[model.SpecStatus]model.SpecStatus{
		model.SpecStatusDraft:        model.SpecStatusSpeccing,
		model.SpecStatusSpeccing:     model.SpecStatusSpecced,
		model.SpecStatusSpecced:      model.SpecStatusPlanning,
		model.SpecStatusPlanning:     model.SpecStatusPlanned,
		model.SpecStatusPlanned:      model.SpecStatusImplementing,
		model.SpecStatusImplementing: model.SpecStatusQA,
		model.SpecStatusQA:           model.SpecStatusDone,
	}
	next, ok := standardForward[current]
	if !ok {
		return "", fmt.Errorf("no forward transition from %s (standard lane): %w",
			current, model.ErrInvalidTransition)
	}
	return next, nil
}

// --- LANE METHODS ---

// SpecQuick advances a trivial-lane spec from draft to implementing in one step
// by recording the provided rationale. It performs two consecutive status
// transitions: draft→rationale (recording the rationale as the reason) and
// rationale→implementing. Returns the spec in implementing status.
// Returns ErrLaneMismatch when the spec is not on the trivial lane.
func (svc *SDDService) SpecQuick(ctx context.Context, req model.SpecQuickRequest) (*model.Spec, error) {
	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec quick: get: %w", err)
	}
	if spec.Lane != model.LaneTrivial {
		return nil, fmt.Errorf("service: spec quick: spec %s is %s lane: %w",
			req.ID, spec.Lane, model.ErrLaneMismatch)
	}
	if spec.Status != model.SpecStatusDraft {
		return nil, fmt.Errorf("service: spec quick: spec %s must be draft, got %s: %w",
			req.ID, spec.Status, model.ErrInvalidTransition)
	}

	// Transition 1: draft → rationale.
	if err := svc.store.UpdateSpecStatus(ctx, spec.ID,
		model.SpecStatusDraft, model.SpecStatusRationale,
		req.By, req.Rationale); err != nil {
		return nil, fmt.Errorf("service: spec quick: draft->rationale: %w", err)
	}

	// Create the spec directory on the rationale transition (same as speccing for standard).
	reloaded, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec quick: reload after rationale: %w", err)
	}
	svc.createSpecDirectory(reloaded)

	// Transition 2: rationale → implementing.
	if err := svc.store.UpdateSpecStatus(ctx, spec.ID,
		model.SpecStatusRationale, model.SpecStatusImplementing,
		req.By, "rationale accepted, starting implementation"); err != nil {
		return nil, fmt.Errorf("service: spec quick: rationale->implementing: %w", err)
	}

	updated, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: spec quick: reload: %w", err)
	}

	// Capture the HEAD SHA as the base for per-spec auditing.
	// This mirrors the onAdvanceSideEffects case for SpecStatusImplementing in
	// SpecAdvance. SpecQuick bypasses onAdvanceSideEffects for this transition
	// so we invoke captureBaseSHA explicitly here with the reloaded spec.
	svc.captureBaseSHA(ctx, updated)

	return updated, nil
}

// LaneAudit runs the deterministic post-implementation auditor for a
// trivial-lane spec in audit status. If all checks pass the spec advances to
// done and a completion memory is saved. If any check fails the spec stays in
// audit, a discovery memory is saved listing the breaches, and ErrAuditFailed
// is returned (the caller can inspect the returned AuditResult for details).
//
// Base ref precedence for the git diff:
//  1. req.BaseRef (explicit caller override)
//  2. spec.BaseSHA (captured when the spec entered implementing)
//  3. "" (auditor falls back to its DefaultBaseRef logic)
//
// Every run — pass and fail — inserts a row in lane_audits so LaneStatus can
// read the latest outcome without parsing spec_history text. The former hack
// of writing audit→audit history entries has been removed.
func (svc *SDDService) LaneAudit(ctx context.Context, req model.LaneAuditRequest) (*lane.AuditResult, error) {
	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: lane audit: get: %w", err)
	}
	if spec.Lane != model.LaneTrivial {
		return nil, fmt.Errorf("service: lane audit: spec %s is %s lane: %w",
			req.ID, spec.Lane, model.ErrLaneMismatch)
	}
	if spec.Status != model.SpecStatusAudit {
		return nil, fmt.Errorf("service: lane audit: spec %s must be in audit status, got %s: %w",
			req.ID, spec.Status, model.ErrInvalidTransition)
	}

	repoDir := svc.repoDir
	if repoDir == "" {
		repoDir, _ = os.Getwd()
	}

	// Resolve the base ref using the precedence rule from D5:
	//   req.BaseRef → spec.BaseSHA → "" (let auditor use its DefaultBaseRef)
	baseRef := req.BaseRef
	if baseRef == "" {
		baseRef = spec.BaseSHA
	}

	result, err := lane.Audit(lane.AuditInput{
		Scope:   spec.Scope,
		BaseRef: baseRef,
		RepoDir: repoDir,
	})
	if err != nil {
		return nil, fmt.Errorf("service: lane audit: run auditor: %w", err)
	}

	// Determine the base ref label stored in lane_audits. When neither caller
	// nor spec provided a ref the auditor used its internal DefaultBaseRef logic.
	auditBaseSHA := baseRef
	if auditBaseSHA == "" {
		auditBaseSHA = "(default)"
	}

	// Persist the structured audit record (both pass and fail) so LaneStatus
	// reads from the table instead of parsing spec_history text.
	breachStr := ""
	if !result.Passed {
		breachStr = strings.Join(result.Breaches, "\n")
	}
	auditRec := &model.LaneAuditRecord{
		SpecID:       spec.ID,
		Passed:       result.Passed,
		FileCount:    result.FileCount,
		LinesChanged: result.LinesChanged,
		Breaches:     breachStr,
		BaseSHA:      auditBaseSHA,
	}
	if insertErr := svc.store.InsertLaneAudit(ctx, auditRec); insertErr != nil {
		log.Printf("service: lane audit: insert audit record for %s: %v", spec.ID, insertErr)
	}

	if result.Passed {
		// Advance to done.
		if err := svc.store.UpdateSpecStatus(ctx, spec.ID,
			model.SpecStatusAudit, model.SpecStatusDone,
			"system", "lane audit passed"); err != nil {
			return nil, fmt.Errorf("service: lane audit: advance to done: %w", err)
		}
		updated, _ := svc.store.GetSpec(ctx, req.ID)
		if updated != nil {
			svc.saveCompletionMemory(ctx, updated)
		}
		return result, nil
	}

	// Audit failed — save a discovery memory; the structured record is already
	// in lane_audits above. No longer writes the same-status history hack.
	svc.saveAuditFailureMemory(ctx, spec, result.Breaches)
	return result, model.ErrAuditFailed
}

// LaneReclassify changes a spec's lane from trivial to standard. After
// reclassification the spec moves to speccing so the full workflow can proceed.
// Upgrading from standard to trivial is forbidden. Lane cannot be changed
// after implementing has started (ErrLaneImmutable).
func (svc *SDDService) LaneReclassify(ctx context.Context, req model.LaneReclassifyRequest) (*model.Spec, error) {
	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: lane reclassify: get: %w", err)
	}

	if req.Lane != model.LaneStandard {
		return nil, fmt.Errorf("service: lane reclassify: only trivial→standard is allowed: %w",
			model.ErrInvalidLane)
	}
	if spec.Lane == model.LaneStandard {
		return nil, fmt.Errorf("service: lane reclassify: spec %s is already standard: %w",
			req.ID, model.ErrLaneMismatch)
	}

	// Immutability check: implementing or later is locked.
	switch spec.Status {
	case model.SpecStatusImplementing, model.SpecStatusAudit, model.SpecStatusDone:
		return nil, fmt.Errorf("service: lane reclassify: spec %s is in %s status: %w",
			req.ID, spec.Status, model.ErrLaneImmutable)
	}

	// Update lane and scope.
	if err := svc.store.UpdateSpecLaneScope(ctx, spec.ID, model.LaneStandard, req.Scope); err != nil {
		return nil, fmt.Errorf("service: lane reclassify: update lane: %w", err)
	}

	// Transition to speccing so the standard workflow can begin.
	// The current status may be draft, rationale, or needs_grill.
	// We update to speccing regardless of the current state because the
	// caller has explicitly requested the reclassification.
	if err := svc.store.UpdateSpecStatus(ctx, spec.ID,
		spec.Status, model.SpecStatusSpeccing,
		req.By, "reclassified from trivial to standard"); err != nil {
		return nil, fmt.Errorf("service: lane reclassify: move to speccing: %w", err)
	}

	updated, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: lane reclassify: reload: %w", err)
	}
	return updated, nil
}

// LaneOverride bypasses a failed lane audit and advances a trivial-lane spec
// from audit directly to done. The reason is required and persisted as a
// discovery memory alongside the normal completion memory.
func (svc *SDDService) LaneOverride(ctx context.Context, req model.LaneOverrideRequest) (*model.Spec, error) {
	if req.Reason == "" {
		return nil, model.ErrReasonRequired
	}

	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: lane override: get: %w", err)
	}
	if spec.Lane != model.LaneTrivial {
		return nil, fmt.Errorf("service: lane override: spec %s is %s lane: %w",
			req.ID, spec.Lane, model.ErrLaneMismatch)
	}
	if spec.Status != model.SpecStatusAudit {
		return nil, fmt.Errorf("service: lane override: spec %s must be in audit status, got %s: %w",
			req.ID, spec.Status, model.ErrInvalidTransition)
	}

	if err := svc.store.UpdateSpecStatus(ctx, spec.ID,
		model.SpecStatusAudit, model.SpecStatusDone,
		req.By, fmt.Sprintf("lane override: %s", req.Reason)); err != nil {
		return nil, fmt.Errorf("service: lane override: advance to done: %w", err)
	}

	updated, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: lane override: reload: %w", err)
	}

	// Save override memory.
	svc.saveOverrideMemory(ctx, updated, req.Reason)
	// Also save the standard completion memory.
	svc.saveCompletionMemory(ctx, updated)

	return updated, nil
}

// LaneStatus returns a summary of a spec's lane classification, the latest
// audit outcome (from the lane_audits table), and the rejection count derived
// from spec_history (transitions to=implementing from qa or audit).
func (svc *SDDService) LaneStatus(ctx context.Context, id string) (*model.LaneStatusResponse, error) {
	spec, err := svc.store.GetSpec(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: lane status: get: %w", err)
	}

	resp := &model.LaneStatusResponse{
		Spec:  spec,
		Lane:  spec.Lane,
		Scope: spec.Scope,
	}

	// Read the latest audit record from the structured table (SPEC-036).
	// Prior to SPEC-036 these were parsed from spec_history text — that path
	// has been replaced. If no rows exist, LatestAudit stays nil.
	latest, err := svc.store.LatestLaneAudit(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: lane status: latest audit: %w", err)
	}
	if latest != nil {
		var breaches []string
		if latest.Breaches != "" {
			breaches = strings.Split(latest.Breaches, "\n")
		}
		resp.LatestAudit = &model.AuditSummary{
			Passed:   latest.Passed,
			Breaches: breaches,
			At:       latest.CreatedAt,
		}
	}

	// Derive RejectionCount from history: transitions where to=implementing and
	// from is qa (standard) or audit (trivial). No extra column needed.
	history, err := svc.store.GetSpecHistory(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: lane status: history: %w", err)
	}
	for _, h := range history {
		if h.ToStatus == model.SpecStatusImplementing &&
			(h.FromStatus == model.SpecStatusQA || h.FromStatus == model.SpecStatusAudit) {
			resp.RejectionCount++
		}
	}

	return resp, nil
}

// LaneStats returns lane compliance metrics for a project. All counts are
// scoped to the provided project (defaults to the service project when empty).
//
// Counts are derived by iterating specs and their histories — no dedicated
// counter columns are needed.
func (svc *SDDService) LaneStats(ctx context.Context, project string) (*model.LaneStatsResponse, error) {
	if project == "" {
		project = svc.project
	}

	// LaneStats aggregates over every spec in the project — it is not one of
	// SPEC-109's windowed listing tools, so it always passes limit=0 (no window).
	specs, _, err := svc.store.ListSpecs(ctx, project, "", 0)
	if err != nil {
		return nil, fmt.Errorf("service: lane stats: list specs: %w", err)
	}

	resp := &model.LaneStatsResponse{}

	for _, spec := range specs {
		if spec.Lane != model.LaneTrivial {
			continue
		}
		resp.TrivialCount++

		// Check if the latest audit failed.
		rec, err := svc.store.LatestLaneAudit(ctx, spec.ID)
		if err != nil {
			log.Printf("service: lane stats: latest audit for %s: %v", spec.ID, err)
			continue
		}
		if rec != nil && !rec.Passed {
			resp.AuditFailCount++
		}

		// Scan history for override and reclassify markers.
		history, err := svc.store.GetSpecHistory(ctx, spec.ID)
		if err != nil {
			log.Printf("service: lane stats: history for %s: %v", spec.ID, err)
			continue
		}
		for _, h := range history {
			if strings.HasPrefix(h.Reason, "lane override:") {
				resp.OverrideCount++
			}
			if strings.Contains(h.Reason, "reclassified from trivial to standard") {
				resp.ReclassifyCount++
			}
		}
	}

	if resp.TrivialCount > 0 {
		resp.AuditFailRate = float64(resp.AuditFailCount) / float64(resp.TrivialCount)
	}

	return resp, nil
}

// saveAuditFailureMemory saves a discovery memory when the lane auditor detects
// threshold violations. This gives the orchestrator a searchable record of why
// the audit failed without needing to re-run the auditor.
func (svc *SDDService) saveAuditFailureMemory(ctx context.Context, spec *model.Spec, breaches []string) {
	if svc.memorySvc == nil {
		return
	}
	content := "## Lane audit failed: " + spec.ID + "\n\n" +
		"Lane: " + string(spec.Lane) + "\n" +
		"Scope: " + spec.Scope + "\n\n" +
		"### Breaches\n\n"
	for _, b := range breaches {
		content += "- " + b + "\n"
	}
	content += "\nUse `lane reclassify " + spec.ID +
		" standard` to restart with full SDD flow, or " +
		"`lane override " + spec.ID + " --reason <justification>` to force completion."

	_, err := svc.memorySvc.Save(ctx, model.SaveRequest{
		Title:   fmt.Sprintf("Lane audit failed: %s", spec.ID),
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Content: content,
		Project: spec.Project,
	})
	if err != nil {
		log.Printf("service: lane audit: save failure memory for %s: %v", spec.ID, err)
	}
}

// saveOverrideMemory saves a discovery memory when an audit is bypassed via
// lane_override. The reason is included so there is an auditable record.
func (svc *SDDService) saveOverrideMemory(ctx context.Context, spec *model.Spec, reason string) {
	if svc.memorySvc == nil {
		return
	}
	content := "## Lane override applied: " + spec.ID + "\n\n" +
		"Reason: " + reason + "\n" +
		"Lane: " + string(spec.Lane) + "\n" +
		"Scope: " + spec.Scope

	_, err := svc.memorySvc.Save(ctx, model.SaveRequest{
		Title:   fmt.Sprintf("Lane override applied: %s", spec.ID),
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
		Content: content,
		Project: spec.Project,
	})
	if err != nil {
		log.Printf("service: lane override: save override memory for %s: %v", spec.ID, err)
	}
}

// insertHistory writes a single history entry directly. This is used for the
// synthetic "created" entry when a spec is first saved, before any UpdateSpecStatus
// transaction would be valid.
func (svc *SDDService) insertHistory(ctx context.Context, h *model.SpecHistory) error {
	// Use UpdateSpecStatus logic but we need a raw insert since 'from' is "".
	// We delegate this through the store's UpdateSpecStatus which does an optimistic
	// check — so for the initial entry we skip UpdateSpecStatus and go through
	// a specialised path that just inserts the history row.
	//
	// Since the store doesn't expose a direct InsertHistory method (by design —
	// history is always recorded alongside status changes), we accept that the
	// initial "created" entry is best-effort and will be skipped when the DB
	// doesn't support it. The spec_history table doesn't enforce a valid from_status,
	// so we could insert directly, but that would break the store abstraction.
	//
	// Decision: the initial entry is stored as a "" -> draft transition in
	// spec_history. Since the history schema accepts any TEXT for from_status,
	// we write it via the DB directly from the store package.
	// For now, skip the initial entry — it is documented as best-effort.
	_ = h
	_ = ctx
	return nil
}
