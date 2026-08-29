package sddfile

// Missing reports which fields mneme itself fills in when a backlog record
// arrives incomplete (SPEC-131 D53): a hand-authored BL-NNN.md needs only a
// title and a description (D16) — everything else on this CLOSED list is
// something mneme mints or defaults at import time.
//
// title and description are deliberately NOT on this list: a record without
// a title is broken, not incomplete (Skipped, not Completed — AC10), and an
// empty description is legitimate content, not a gap to fill.
//
// schema is deliberately NOT on this list either: its absence means schema 1
// by D28's own rule, so a file without that line is already complete as far
// as this method is concerned — the exact property AC12's fixture relies on
// to distinguish "untouched" from "rewritten".
func (r *BacklogRecord) Missing() []string {
	if r == nil || r.Item == nil {
		return nil
	}
	item := r.Item
	var missing []string
	if item.UUID == "" {
		missing = append(missing, "uuid")
	}
	if item.Project == "" {
		missing = append(missing, "project")
	}
	if item.Status == "" {
		missing = append(missing, "status")
	}
	if item.Priority == "" {
		missing = append(missing, "priority")
	}
	if item.Lane == "" {
		missing = append(missing, "lane")
	}
	if item.CreatedAt.IsZero() {
		missing = append(missing, "created_at")
	}
	if item.UpdatedAt.IsZero() {
		missing = append(missing, "updated_at")
	}
	return missing
}

// Missing is BacklogRecord.Missing's sibling for a spec record. model.Spec
// carries no description field (D15/CF1 of SPEC-130) so title is the only
// human-authored minimum — the same "title required, everything else
// mintable" rule, with a shorter closed list because a spec has no
// priority.
func (r *SpecRecord) Missing() []string {
	if r == nil || r.Spec == nil {
		return nil
	}
	spec := r.Spec
	var missing []string
	if spec.UUID == "" {
		missing = append(missing, "uuid")
	}
	if spec.Project == "" {
		missing = append(missing, "project")
	}
	if spec.Status == "" {
		missing = append(missing, "status")
	}
	if spec.Lane == "" {
		missing = append(missing, "lane")
	}
	if spec.CreatedAt.IsZero() {
		missing = append(missing, "created_at")
	}
	if spec.UpdatedAt.IsZero() {
		missing = append(missing, "updated_at")
	}
	return missing
}
