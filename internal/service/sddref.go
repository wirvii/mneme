package service

import (
	"context"
	"log/slog"

	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
)

// mayAnchor is the single predicate every anchoring path uses — Save,
// Update, and the one-shot backfill (SPEC-128 D5 rule 2). A memory is
// anchorable on THIS machine when it has no author yet (a brand-new local
// memory, never touched by import) or when its recorded author matches this
// machine's own git identity.
//
// A memory imported from a peer keeps THEIR author, so mayAnchor correctly
// refuses to anchor it here even after a local edit — the invariant that
// only the machine that wrote a mention may create its anchor (D5) would
// otherwise be defeated by "edit locally, re-save, and it becomes ours".
//
// When gitident.Author() cannot resolve (no git config), every memory that
// DOES carry an author becomes unattributable and is left unanchored — the
// honest failure D5 requires, never the false-positive of anchoring
// something this machine cannot actually vouch for.
func mayAnchor(m *model.Memory) bool {
	if m.Author == "" {
		return true
	}
	local := gitident.Author()
	if local == "" {
		return false
	}
	return m.Author == local
}

// computeSDDRefs resolves every BL-<n>/SPEC-<n> mention in title+content
// against THIS machine's local SDD store and returns the anchored subset,
// in the SAME order model.ParseSDDRefs produced (D4's single mention
// definition). A mention with no local anchor is simply omitted — an
// unanchored mention is represented by its ABSENCE from memory_sdd_refs
// (D8), never by a row with an empty target_uuid.
//
// Always returns a non-nil slice (possibly empty) so callers can
// distinguish "computed, zero anchored mentions" (still worth writing, to
// prune stale rows) from "not computed at all" (nil, when mayAnchor was
// false or no SDD store is wired) — see bakeSDDRefsForUpdate.
func (svc *MemoryService) computeSDDRefs(ctx context.Context, title, content string) []model.SDDRef {
	mentions := model.ParseSDDRefs(title + "\n" + content)
	refs := make([]model.SDDRef, 0, len(mentions))
	if len(mentions) == 0 {
		return refs
	}

	anchors, err := svc.sddStore.UUIDsForRefs(ctx, mentions)
	if err != nil {
		slog.WarnContext(ctx, "sdd_ref_anchor_lookup_failed", "error", err)
		return refs
	}

	for _, refID := range mentions {
		target, ok := anchors[refID]
		if !ok {
			continue
		}
		refs = append(refs, model.SDDRef{RefID: refID, TargetUUID: target})
	}
	return refs
}

// bakeSDDRefs computes m's anchored SDD references from its current
// title+content and mutates m.SDDRefs in place, so the store write that
// follows (Upsert or insertMemory) picks them up exactly the way it already
// picks up m.Files. Called BEFORE the store write, in Save — never after —
// so a vault materialization triggered by the SAME call sees the anchors on
// the note's first write (D6).
//
// A no-op (m.SDDRefs left untouched, i.e. nil on a freshly built Memory)
// when there is no SDD store wired (svc.sddStore == nil — e.g. a test
// service, or global.db per D12) or when mayAnchor(m) is false.
func (svc *MemoryService) bakeSDDRefs(ctx context.Context, m *model.Memory) {
	if svc.sddStore == nil || !mayAnchor(m) {
		return
	}
	m.SDDRefs = svc.computeSDDRefs(ctx, m.Title, m.Content)
}

// bakeSDDRefsForUpdate computes the anchored SDD reference set for the
// RESULTANT title+content an update would produce — existing's fields
// where req leaves them unchanged, req's fields where it doesn't — and, if
// anything should be written, points req.SDDRefs at it so store.Update's
// own `req.SDDRefs != nil` guard fires.
//
// The pointer distinction matters (unlike bakeSDDRefs' direct field
// mutation): req.SDDRefs must be able to express "zero anchored mentions,
// but WRITE that, to prune stale rows" — a plain nil-vs-empty on a value
// slice can't be told apart by store.Update, but a nil-vs-non-nil pointer
// can (SPEC-128 D5 rule 3: a mention that disappears from the text loses
// its row).
//
// A no-op when there's nothing to recompute: no SDD store wired, existing
// is nil (should not happen — Update's caller always loads it first), the
// memory is not attributable to this machine (mayAnchor), or the update
// touches neither Title nor Content (mentions cannot have changed).
func (svc *MemoryService) bakeSDDRefsForUpdate(ctx context.Context, existing *model.Memory, req *model.UpdateRequest) {
	if svc.sddStore == nil || existing == nil || !mayAnchor(existing) {
		return
	}
	if req.Title == nil && req.Content == nil {
		return
	}

	title := existing.Title
	if req.Title != nil {
		title = *req.Title
	}
	content := existing.Content
	if req.Content != nil {
		content = *req.Content
	}

	refs := svc.computeSDDRefs(ctx, title, content)
	req.SDDRefs = &refs
}

// resolveSDDRefs is the ONLY place in mneme that completes a reference's
// Status/LocalID (SPEC-128 D8) — called at the end of MemoryService.Get,
// never from search, mem_context, or any other multi-memory read path,
// which would each pay one resolution query per reference per result for a
// value the caller is not looking at yet.
//
// The returned list is the UNION of what memory_sdd_refs already stored on
// m (RefID+TargetUUID, loaded by the store layer) and whatever
// model.ParseSDDRefs finds by re-scanning m's CURRENT text — the second
// half is what makes "unanchored" representable without ever needing a row
// with an empty target_uuid. Order: stored refs first (already
// ref_id-ordered by the store), then any additional live mention not yet
// anchored.
//
// A reference whose anchor resolves to no local row is left exactly at
// SDDRefForeign, with NO LocalID — this is the honest failure the whole
// mechanism exists to produce; resolveSDDRefs never falls back to looking
// the bare correlative up directly.
func (svc *MemoryService) resolveSDDRefs(ctx context.Context, m *model.Memory) {
	if m == nil || svc.sddStore == nil {
		return
	}

	targetByRef := make(map[string]string, len(m.SDDRefs))
	order := make([]string, 0, len(m.SDDRefs))
	for _, ref := range m.SDDRefs {
		if _, seen := targetByRef[ref.RefID]; !seen {
			order = append(order, ref.RefID)
		}
		targetByRef[ref.RefID] = ref.TargetUUID
	}
	for _, refID := range model.ParseSDDRefs(m.Title + "\n" + m.Content) {
		if _, seen := targetByRef[refID]; seen {
			continue
		}
		targetByRef[refID] = ""
		order = append(order, refID)
	}

	if len(order) == 0 {
		m.SDDRefs = nil
		return
	}

	anchoredUUIDs := make([]string, 0, len(order))
	for _, refID := range order {
		if target := targetByRef[refID]; target != "" {
			anchoredUUIDs = append(anchoredUUIDs, target)
		}
	}
	localIDs, err := svc.sddStore.RefsForUUIDs(ctx, anchoredUUIDs)
	if err != nil {
		slog.WarnContext(ctx, "sdd_ref_resolve_failed", "memory_id", m.ID, "error", err)
		localIDs = map[string]string{}
	}

	resolved := make([]model.SDDRef, 0, len(order))
	for _, refID := range order {
		target := targetByRef[refID]
		ref := model.SDDRef{RefID: refID, TargetUUID: target}
		if target == "" {
			ref.Status = model.SDDRefUnanchored
		} else if local, ok := localIDs[target]; ok {
			ref.Status = model.SDDRefLocal
			ref.LocalID = local
		} else {
			ref.Status = model.SDDRefForeign
		}
		resolved = append(resolved, ref)
	}
	m.SDDRefs = resolved
}
