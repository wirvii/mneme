package mcp

import (
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// backlogListItem is the context-window-safe projection of a
// model.BacklogItem: identical to the item except that Description (a full
// grill ledger in this repo — 461 KB in one line for 139 items) is replaced
// by Excerpt+Truncated.
//
// The different field name is deliberate: a field literally called
// "description" holding 200 of 40,000 characters would be a false datum
// that looks real — the same pathology mem_timeline's old Total used to
// have. Truncated is the per-item analogue of BacklogListView.Total — it
// answers "is there more?", exactly what an agent needs to decide whether to
// call backlog_get. Without the bool, an excerpt from a short description
// (which IS the full text) would be indistinguishable from a truncated one.
type backlogListItem struct {
	ID            string              `json:"id"`
	Title         string              `json:"title"`
	Excerpt       string              `json:"excerpt,omitempty"`
	Truncated     bool                `json:"truncated,omitempty"`
	Status        model.BacklogStatus `json:"status"`
	Priority      model.Priority      `json:"priority"`
	Project       string              `json:"project"`
	SpecID        string              `json:"spec_id,omitempty"`
	ArchiveReason string              `json:"archive_reason,omitempty"`
	Position      int                 `json:"position"`
	Lane          model.Lane          `json:"lane"`
	Scope         string              `json:"scope,omitempty"`

	// Refinements is how many refinements the item has (SPEC-110 D4).
	//
	// NO omitempty, deliberately: an absent field would be ambiguous with
	// "this binary has no counter" — the same false-datum reasoning that keeps
	// omitempty off Total and Truncated.
	//
	// Wire name `refinements` (not the domain's refinement_count) is what D4
	// fixed; the MCP view is already a projection with its own names (excerpt
	// vs description).
	Refinements int `json:"refinements"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// backlogListView is the MCP wire shape for backlog_list: a page of
// projected items plus the true match count. Unexported — this projection
// is a contract of the MCP frontend (SPEC-109 D9), not domain shape.
//
// Unreadable/UnreadableTotal (SPEC-133 D14) follow the SAME acotado
// convention Total/Items already establish: the store's full relation is
// truncated to model.MaxUnreadableListed HERE, in the MCP frontend, never
// in the store — the same split SPEC-109 drew for the backlog excerpt
// ("The excerpt is MCP frontend policy (D9), not applied here",
// service/sdd.go). Both fields are absent (omitempty) when nothing was
// skipped.
type backlogListView struct {
	Items           []backlogListItem     `json:"items"`
	Total           int                   `json:"total"`
	Unreadable      []model.UnreadableRow `json:"unreadable,omitempty"`
	UnreadableTotal int                   `json:"unreadable_total,omitempty"`
}

// truncateUnreadable caps rows to model.MaxUnreadableListed (SPEC-133 D14)
// and returns the real total alongside. The store/service layer never
// truncates this relation — its own accounting identity
// (model.BacklogListResponse.Unreadable) has to stay exact — so this cap is
// MCP frontend policy applied once here, shared by every view that carries
// the relation.
func truncateUnreadable(rows []model.UnreadableRow) ([]model.UnreadableRow, int) {
	total := len(rows)
	if total == 0 {
		return nil, 0
	}
	if total <= model.MaxUnreadableListed {
		return rows, total
	}
	return rows[:model.MaxUnreadableListed], total
}

// newBacklogListView projects resp into the MCP wire shape using excerptRunes
// as the excerpt length. It never mutates the entities it receives: resp.Items
// holds pointers, and overwriting Description in-place on a shared entity
// would be a time bomb for any future code that re-reads that same pointer.
// A brand-new backlogListItem value is built for every row instead.
func newBacklogListView(resp model.BacklogListResponse, excerptRunes int) backlogListView {
	items := make([]backlogListItem, 0, len(resp.Items))
	for _, item := range resp.Items {
		excerpt, truncated := model.Excerpt(item.Description, excerptRunes)
		items = append(items, backlogListItem{
			ID:            item.ID,
			Title:         item.Title,
			Excerpt:       excerpt,
			Truncated:     truncated,
			Status:        item.Status,
			Priority:      item.Priority,
			Project:       item.Project,
			SpecID:        item.SpecID,
			ArchiveReason: item.ArchiveReason,
			Position:      item.Position,
			Lane:          item.Lane,
			Scope:         item.Scope,
			Refinements:   item.RefinementCount,
			CreatedAt:     item.CreatedAt,
			UpdatedAt:     item.UpdatedAt,
		})
	}
	unreadable, unreadableTotal := truncateUnreadable(resp.Unreadable)
	return backlogListView{Items: items, Total: resp.Total, Unreadable: unreadable, UnreadableTotal: unreadableTotal}
}

// specListView is the MCP wire shape for spec_list: the raw specs plus the
// true match count. No per-field projection — model.Spec has no Description
// field (SPEC-109 D15/CF1), so there is nothing to excerpt.
//
// Frozen (SPEC-126 DD6) carries a SpecFreeze entry for every spec in Specs
// that can no longer change status, keyed by spec ID. Absent from the wire
// (nil map, omitempty) when nothing is frozen — a caller that has never seen
// this key sees no change in the common case.
type specListView struct {
	Specs  []*model.Spec               `json:"specs"`
	Total  int                         `json:"total"`
	Frozen map[string]model.SpecFreeze `json:"frozen,omitempty"`

	// Unreadable/UnreadableTotal (SPEC-133 D14) — see backlogListView's own
	// godoc for the truncate-in-the-frontend rationale, identical here.
	Unreadable      []model.UnreadableRow `json:"unreadable,omitempty"`
	UnreadableTotal int                   `json:"unreadable_total,omitempty"`
}

// newSpecListView projects resp into the MCP wire shape, truncating its
// Unreadable relation the same way newBacklogListView does (SPEC-133 D14).
func newSpecListView(resp model.SpecListResponse) specListView {
	unreadable, unreadableTotal := truncateUnreadable(resp.Unreadable)
	return specListView{
		Specs: resp.Specs, Total: resp.Total, Frozen: resp.Frozen,
		Unreadable: unreadable, UnreadableTotal: unreadableTotal,
	}
}

// laneStatsView is the MCP wire shape for lane_stats: identical to
// model.LaneStatsResponse except Unreadable is truncated to
// model.MaxUnreadableListed and UnreadableTotal carries the real count
// (SPEC-133 D14) — lane_stats had no dedicated view type before this spec
// (the handler returned *model.LaneStatsResponse directly), so this is the
// first one.
type laneStatsView struct {
	TrivialCount    int                   `json:"trivial_count"`
	AuditFailCount  int                   `json:"audit_fail_count"`
	AuditFailRate   float64               `json:"audit_fail_rate"`
	OverrideCount   int                   `json:"override_count"`
	ReclassifyCount int                   `json:"reclassify_count"`
	Unreadable      []model.UnreadableRow `json:"unreadable,omitempty"`
	UnreadableTotal int                   `json:"unreadable_total,omitempty"`
}

// newLaneStatsView projects resp into the MCP wire shape, truncating its
// Unreadable relation the same way newBacklogListView/newSpecListView do.
func newLaneStatsView(resp *model.LaneStatsResponse) laneStatsView {
	unreadable, unreadableTotal := truncateUnreadable(resp.Unreadable)
	return laneStatsView{
		TrivialCount:    resp.TrivialCount,
		AuditFailCount:  resp.AuditFailCount,
		AuditFailRate:   resp.AuditFailRate,
		OverrideCount:   resp.OverrideCount,
		ReclassifyCount: resp.ReclassifyCount,
		Unreadable:      unreadable,
		UnreadableTotal: unreadableTotal,
	}
}
