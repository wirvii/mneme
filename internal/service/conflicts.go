package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/wirvii/mneme/internal/conflicts"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// ConflictScanRequest parameterises a conflicts scan operation.
//
// Fields carry explicit json tags (SPEC-113 C4) so their canonical wire name
// matches the lowercase names already published in conflicts_scan's
// InputSchema (internal/mcp/tools.go) exactly, rather than relying on
// encoding/json's case-insensitive fallback against the bare Go field name
// (e.g. "Apply"). This changes no accepted input — the fallback still
// accepts any case variant either way — it only removes a capitalized
// spelling the schema never advertised.
type ConflictScanRequest struct {
	// Project restricts the scan to memories in this project. Defaults to the
	// service's configured project when empty.
	Project string `json:"project"`

	// Limit is the maximum number of candidate pairs to judge. Default 5, max 10.
	Limit int `json:"limit"`

	// Apply causes the scan to persist judged relations when true. When false
	// (the default) the scan is a dry-run: results are returned but not stored.
	Apply bool `json:"apply"`
}

// ConflictPairResult describes the judgment outcome for one memory pair.
type ConflictPairResult struct {
	// MemoryA and MemoryB are the IDs of the two memories in the pair.
	MemoryA string
	MemoryB string

	// TitleA and TitleB are the titles of the two memories for display.
	TitleA string
	TitleB string

	// Relation is the judged relation type, or empty when Skipped is true.
	Relation string

	// Rationale is the one-line explanation from the judge.
	Rationale string

	// Error holds an error description when the pair was skipped.
	Error string

	// Skipped is true when the pair could not be judged (e.g. timeout, load
	// failure). The scan continues with the next pair.
	Skipped bool
}

// ConflictScanResponse summarises the outcome of a scan operation.
type ConflictScanResponse struct {
	// Pairs contains one entry per candidate pair evaluated.
	Pairs []ConflictPairResult

	// Applied is true when the scan was run with Apply=true and results were persisted.
	Applied bool

	// Total is the count of pairs evaluated (including skipped ones).
	Total int

	// Errors is the count of pairs that were skipped due to errors.
	Errors int
}

// ConflictRelation is the external representation of a memory_relations row
// returned by ConflictList.
type ConflictRelation struct {
	FromID    string
	ToID      string
	Relation  string
	JudgedBy  string
	Rationale string
	CreatedAt string
}

// normalizePair returns a canonical (smaller, larger) ID ordering so that
// (a,b) and (b,a) map to the same logical pair.
func normalizePair(a, b string) (string, string) {
	if a <= b {
		return a, b
	}
	return b, a
}

// ConflictCandidates returns up to limit candidate memory IDs that share
// salient FTS5 terms with the memory identified by memoryID. The result is
// deterministic; no LLM is involved.
func (svc *MemoryService) ConflictCandidates(ctx context.Context, memoryID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 5
	}

	m, targetStore, err := svc.getFromEitherStore(ctx, memoryID)
	if err != nil {
		return nil, fmt.Errorf("service: conflict candidates: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("service: conflict candidates: %w", model.ErrNotFound)
	}

	terms := conflicts.ExtractSalientTerms(m.Title, m.Content, 10)
	query := conflicts.BuildCandidateQuery(terms)
	if query == "" {
		return nil, nil
	}

	ids, err := targetStore.FTS5Candidates(ctx, memoryID, query, m.Project, limit)
	if err != nil {
		return nil, fmt.Errorf("service: conflict candidates: fts5: %w", err)
	}
	return ids, nil
}

// ConflictScan judges candidate pairs using the Claude CLI and optionally
// persists the results. Returns ErrCLIUnavailable when the CLI is not on PATH.
//
// The scan is idempotent: pairs already present in memory_relations are skipped.
// Each pair failure (load error, judgment error) sets Skipped=true and the scan
// continues.
func (svc *MemoryService) ConflictScan(ctx context.Context, req ConflictScanRequest) (*ConflictScanResponse, error) {
	cfg, err := conflicts.NewJudgeConfig()
	if err != nil {
		// Translate the leaf-package sentinel to the model-layer sentinel so
		// the MCP handler can use errors.Is(err, model.ErrCLIUnavailable).
		if errors.Is(err, conflicts.ErrCLIUnavailable) {
			return nil, fmt.Errorf("service: conflict scan: %w", model.ErrCLIUnavailable)
		}
		return nil, fmt.Errorf("service: conflict scan: %w", err)
	}

	project := req.Project
	if project == "" {
		project = svc.project
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	// Load up to 200 active memories for the project.
	memories, err := svc.projectStore.List(ctx, store.ListOptions{
		Project:           project,
		IncludeSuperseded: false,
		Limit:             200,
	})
	if err != nil {
		return nil, fmt.Errorf("service: conflict scan: list memories: %w", err)
	}

	// Build the candidate pair set: for each memory, find FTS5 candidates and
	// normalise pairs to avoid duplicates.
	type pairKey struct{ a, b string }
	seen := make(map[pairKey]bool)
	type pair struct{ aID, bID string }
	var pairs []pair

	for _, m := range memories {
		if len(pairs) >= limit {
			break
		}

		terms := conflicts.ExtractSalientTerms(m.Title, m.Content, 10)
		query := conflicts.BuildCandidateQuery(terms)
		if query == "" {
			continue
		}

		candidates, candErr := svc.projectStore.FTS5Candidates(ctx, m.ID, query, project, 5)
		if candErr != nil {
			continue
		}

		for _, candID := range candidates {
			if len(pairs) >= limit {
				break
			}
			// Normalize pair to avoid judging (a,b) and (b,a) separately.
			fa, fb := normalizePair(m.ID, candID)
			k := pairKey{fa, fb}
			if seen[k] {
				continue
			}
			seen[k] = true
			pairs = append(pairs, pair{fa, fb})
		}
	}

	resp := &ConflictScanResponse{
		Applied: req.Apply,
	}

	for _, p := range pairs {
		result := svc.judgeOnePair(ctx, cfg, p.aID, p.bID, req.Apply)
		resp.Pairs = append(resp.Pairs, result)
		resp.Total++
		if result.Skipped {
			resp.Errors++
		}
	}

	return resp, nil
}

// judgeOnePair loads both memories, calls JudgePair, and optionally persists
// the result. On any failure it returns a ConflictPairResult with Skipped=true.
func (svc *MemoryService) judgeOnePair(
	ctx context.Context,
	cfg *conflicts.JudgeConfig,
	aID, bID string,
	apply bool,
) ConflictPairResult {
	result := ConflictPairResult{MemoryA: aID, MemoryB: bID}

	mA, _, err := svc.getFromEitherStore(ctx, aID)
	if err != nil || mA == nil {
		result.Skipped = true
		result.Error = fmt.Sprintf("load memory A: %v", err)
		return result
	}

	mB, _, err := svc.getFromEitherStore(ctx, bID)
	if err != nil || mB == nil {
		result.Skipped = true
		result.Error = fmt.Sprintf("load memory B: %v", err)
		return result
	}

	result.TitleA = mA.Title
	result.TitleB = mB.Title

	verdict, err := conflicts.JudgePair(ctx, cfg, aID, mA.Title, mA.Content, bID, mB.Title, mB.Content)
	if err != nil {
		result.Skipped = true
		result.Error = fmt.Sprintf("judge pair: %v", err)
		return result
	}

	result.Relation = verdict.Relation
	result.Rationale = verdict.Rationale

	if apply {
		if persistErr := svc.persistVerdict(ctx, verdict, aID, bID); persistErr != nil {
			result.Skipped = true
			result.Error = fmt.Sprintf("persist verdict: %v", persistErr)
		}
	}

	return result
}

// persistVerdict writes the judgment to the appropriate storage: supersedes
// relations go to memories.superseded_by via SetSupersededBy; conflicts_with
// and unrelated go to memory_relations.
//
// Both memory IDs are resolved to their owning store. If they live in different
// stores (one global, one project) persistVerdict returns ErrCrossStoreRelation
// without writing anything — judgeOnePair converts this into Skipped=true.
func (svc *MemoryService) persistVerdict(ctx context.Context, v *conflicts.Verdict, aID, bID string) error {
	_, storeA, err := svc.getFromEitherStore(ctx, aID)
	if err != nil {
		return fmt.Errorf("persist verdict: resolve memory A: %w", err)
	}
	_, storeB, err := svc.getFromEitherStore(ctx, bID)
	if err != nil {
		return fmt.Errorf("persist verdict: resolve memory B: %w", err)
	}
	if storeA != nil && storeB != nil && storeA != storeB {
		return fmt.Errorf("persist verdict: %w", model.ErrCrossStoreRelation)
	}

	// Use whichever store resolved (prefer A).
	ts := storeA
	if ts == nil {
		ts = storeB
	}
	if ts == nil {
		ts = svc.projectStore
	}

	switch v.Relation {
	case "supersedes_a_over_b", "supersedes_b_over_a":
		if err := ts.SetSupersededBy(ctx, v.LoserID, v.WinnerID); err != nil {
			return fmt.Errorf("set superseded_by: %w", err)
		}
	case "conflicts_with", "unrelated":
		if err := ts.CreateMemoryRelation(ctx, aID, bID, v.Relation, "cli", v.Rationale); err != nil {
			return fmt.Errorf("create memory relation: %w", err)
		}
	}
	return nil
}

// ConflictLink manually creates a memory relation between from and to.
//
// When relation is "supersedes", SetSupersededBy(to, from) is called (from
// supersedes to; to becomes obsolete). For "conflicts_with" and "unrelated"
// a row is inserted in memory_relations with judged_by="manual".
//
// Both memories must live in the same store (both project or both global).
// Mixing stores returns ErrCrossStoreRelation without any write.
//
// Sentinel errors:
//   - model.ErrInvalidRelation when relation is not in the accepted set
//   - model.ErrNotFound when either memory does not exist
//   - model.ErrCrossStoreRelation when the memories live in different stores
func (svc *MemoryService) ConflictLink(ctx context.Context, from, to, relation, rationale string) error {
	validRelations := map[string]bool{
		"supersedes":     true,
		"conflicts_with": true,
		"unrelated":      true,
	}
	if !validRelations[relation] {
		return fmt.Errorf("service: conflict link: %w: %q", model.ErrInvalidRelation, relation)
	}

	mFrom, storeFrom, err := svc.getFromEitherStore(ctx, from)
	if err != nil {
		return fmt.Errorf("service: conflict link: load from: %w", err)
	}
	if mFrom == nil {
		return fmt.Errorf("service: conflict link: from memory: %w", model.ErrNotFound)
	}

	mTo, storeTo, err := svc.getFromEitherStore(ctx, to)
	if err != nil {
		return fmt.Errorf("service: conflict link: load to: %w", err)
	}
	if mTo == nil {
		return fmt.Errorf("service: conflict link: to memory: %w", model.ErrNotFound)
	}

	if storeFrom != storeTo {
		return fmt.Errorf("service: conflict link: %w", model.ErrCrossStoreRelation)
	}

	targetStore := storeFrom

	switch relation {
	case "supersedes":
		// from supersedes to: mark to as superseded by from.
		if err := targetStore.SetSupersededBy(ctx, to, from); err != nil {
			return fmt.Errorf("service: conflict link: set superseded_by: %w", err)
		}
	default:
		if err := targetStore.CreateMemoryRelation(ctx, from, to, relation, "manual", rationale); err != nil {
			return fmt.Errorf("service: conflict link: create relation: %w", err)
		}
	}

	return nil
}

// ConflictUnlink removes a memory relation between from and to. For conflicts_with
// and unrelated, the memory_relations row is deleted. Additionally, if either
// memory has superseded_by pointing to the other, that column is cleared.
//
// When both memories resolve successfully but live in different stores
// (one global, one project), ErrCrossStoreRelation is returned before any write.
// When a memory is not found (deleted), the operation continues best-effort
// using whichever store resolved (with projectStore as final fallback), so that
// orphaned relation rows can still be cleaned up.
func (svc *MemoryService) ConflictUnlink(ctx context.Context, from, to string) error {
	mFrom, storeFrom, err := svc.getFromEitherStore(ctx, from)
	if err != nil {
		return fmt.Errorf("service: conflict unlink: load from: %w", err)
	}

	mTo, storeTo, err := svc.getFromEitherStore(ctx, to)
	if err != nil {
		return fmt.Errorf("service: conflict unlink: load to: %w", err)
	}

	// Both memories exist: check they live in the same store.
	if storeFrom != nil && storeTo != nil && storeFrom != storeTo {
		return fmt.Errorf("service: conflict unlink: %w", model.ErrCrossStoreRelation)
	}

	// Determine target store: prefer the resolved one; fall back to projectStore
	// for orphaned records when one or both memories were deleted.
	targetStore := storeFrom
	if targetStore == nil {
		targetStore = storeTo
	}
	if targetStore == nil {
		targetStore = svc.projectStore
	}

	// Attempt to delete from memory_relations (best-effort; may not exist).
	delErr := targetStore.DeleteMemoryRelation(ctx, from, to)
	if delErr != nil && !errors.Is(delErr, model.ErrNotFound) {
		return fmt.Errorf("service: conflict unlink: delete relation: %w", delErr)
	}

	// Clear superseded_by if from→to or to→from supersede relation exists.
	if mFrom != nil && mFrom.SupersededBy == to {
		if clearErr := targetStore.ClearSupersededBy(ctx, from); clearErr != nil {
			return fmt.Errorf("service: conflict unlink: clear superseded_by (from): %w", clearErr)
		}
	}

	if mTo != nil && mTo.SupersededBy == from {
		if clearErr := targetStore.ClearSupersededBy(ctx, to); clearErr != nil {
			return fmt.Errorf("service: conflict unlink: clear superseded_by (to): %w", clearErr)
		}
	}

	return nil
}

// ConflictListResponse wraps a page of relations with the REAL number of
// matches. Mirrors model.BacklogListResponse's two-mode Limit semantics
// (SPEC-109 D5/D16): limit<=0 means no window, limit>model.ListMaxLimit is
// silently capped. No excerpt: Rationale is a single line by design.
type ConflictListResponse struct {
	Relations []ConflictRelation
	Total     int
}

// ConflictList returns the memory relation rows matching project, plus the
// REAL number of matches. Same latent problem mem_timeline had (D3/D18):
// handleConflictsList used to report count=len(rels), which is only correct
// by accident because there was no limit yet — the instant a limit exists,
// count stops saying how many there are. Total fixes that; count is kept
// (SPEC-109 D16) because it does not break any existing caller.
func (svc *MemoryService) ConflictList(ctx context.Context, project string, limit int) (ConflictListResponse, error) {
	if project == "" {
		project = svc.project
	}
	if limit > model.ListMaxLimit {
		limit = model.ListMaxLimit
	}

	opts := store.MemoryRelationListOptions{Project: project, Limit: limit}

	total, err := svc.projectStore.CountMemoryRelations(ctx, opts)
	if err != nil {
		return ConflictListResponse{}, fmt.Errorf("service: conflict list: count: %w", err)
	}

	rows, err := svc.projectStore.ListMemoryRelations(ctx, opts)
	if err != nil {
		return ConflictListResponse{}, fmt.Errorf("service: conflict list: %w", err)
	}

	result := make([]ConflictRelation, len(rows))
	for i, r := range rows {
		result[i] = ConflictRelation{
			FromID:    r.FromID,
			ToID:      r.ToID,
			Relation:  r.Relation,
			JudgedBy:  r.JudgedBy,
			Rationale: r.Rationale,
			CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	return ConflictListResponse{Relations: result, Total: total}, nil
}

// logConflictHint is called after a successful Save as a non-blocking hint.
// It finds FTS5 candidates for the newly saved memory and logs a slog.Info
// when candidates exist. It NEVER writes relations, NEVER judges, and NEVER
// propagates an error — a panic or any failure is silently recovered.
func (svc *MemoryService) logConflictHint(ctx context.Context, result *model.SaveResponse) {
	defer func() {
		if r := recover(); r != nil {
			slog.WarnContext(ctx, "conflict hint panic recovered",
				"memory_id", result.ID,
				"panic", r,
			)
		}
	}()

	// Reload the memory to get its title and content for term extraction.
	m, _, err := svc.getFromEitherStore(ctx, result.ID)
	if err != nil || m == nil {
		return
	}

	terms := conflicts.ExtractSalientTerms(m.Title, m.Content, 10)
	query := conflicts.BuildCandidateQuery(terms)
	if query == "" {
		return
	}

	targetStore := svc.storeFor(m.Scope)
	candidates, err := targetStore.FTS5Candidates(ctx, m.ID, query, m.Project, 3)
	if err != nil || len(candidates) == 0 {
		return
	}

	slog.InfoContext(ctx, "conflict_hint",
		"memory_id", result.ID,
		"title", result.Title,
		"candidates", candidates,
		"hint", "run 'mneme conflicts candidates "+result.ID+"' or 'mneme conflicts scan' to review",
	)
}
