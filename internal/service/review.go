package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// hoursPerDay converts a duration into whole review days.
const hoursPerDay = 24

// reviewNoteFormat is the fixed text of Memory.ReviewNote (SPEC-167 D5). It is
// addressed to the agent, and says so: the owner's requirement is that the
// warning is internal and never shown to the user.
const reviewNoteFormat = "Revisión vencida: última revisión hace %d días (plazo de %d días para %s). " +
	"Comprueba si sigue vigente: confírmala con mem_update confirm:true, corrígela, " +
	"o márcala reemplazada con supersedes. Aviso interno: no se lo muestres al usuario."

// reviewStatus decides whether a memory whose review clock started at ref is
// due for review at now, given its type's deadline in days. It returns the
// whole days elapsed too. A deadline <= 0 never expires. Pure: no clock, no I/O.
func reviewStatus(days int, ref, now time.Time) (due bool, ageDays int) {
	ageDays = int(now.Sub(ref).Hours() / hoursPerDay)
	return days > 0 && ageDays > days, ageDays
}

// annotateReviewDue fills Memory.ReviewDue and ReviewNote on mems for those
// past their type's review deadline (SPEC-167 D5). It only annotates: order
// and membership of the caller's results never change. It is best-effort --
// if the review dates cannot be read (for example a database in an anomalous
// state), it logs and leaves every memory unannotated, so search and context
// never fail because of it.
func (svc *MemoryService) annotateReviewDue(ctx context.Context, mems []*model.Memory) {
	byStore := make(map[*store.MemoryStore][]*model.Memory)
	for _, m := range mems {
		if m == nil || svc.config.Memory.ReviewDaysFor(string(m.Type)) <= 0 {
			continue
		}
		st := svc.storeFor(m.Scope)
		byStore[st] = append(byStore[st], m)
	}

	now := time.Now().UTC()
	for st, group := range byStore {
		ids := make([]string, len(group))
		for i, m := range group {
			ids[i] = m.ID
		}
		dates, err := st.ReviewDates(ctx, ids)
		if err != nil {
			slog.WarnContext(ctx, "service: review annotation skipped", "error", err)
			continue
		}
		for _, m := range group {
			ref, ok := dates[m.ID]
			if !ok {
				continue
			}
			days := svc.config.Memory.ReviewDaysFor(string(m.Type))
			if due, age := reviewStatus(days, ref, now); due {
				m.ReviewDue = true
				m.ReviewNote = fmt.Sprintf(reviewNoteFormat, age, days, m.Type)
			}
		}
	}
}
