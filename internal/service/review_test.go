package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// reviewEnv bundles a service with direct handles on its project database and
// store, so tests can age memories with MarkReviewed (never by sleeping) and
// break the schema on purpose.
type reviewEnv struct {
	svc    *service.MemoryService
	st     *store.MemoryStore
	global *store.MemoryStore
	raw    *db.DB
}

func newReviewEnv(t *testing.T) reviewEnv {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	// One connection: an in-memory database is private to its connection.
	projectDB.SetMaxOpenConns(1)
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	globalDB.SetMaxOpenConns(1)
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })
	e := reviewEnv{
		st:     store.NewMemoryStore(projectDB),
		global: store.NewMemoryStore(globalDB),
		raw:    projectDB,
	}
	e.svc = e.serviceWith(config.Default())
	return e
}

// serviceWith builds another service over the same two databases.
func (e reviewEnv) serviceWith(cfg *config.Config) *service.MemoryService {
	return service.NewMemoryService(e.st, e.global, cfg, "test/project", embed.NopEmbedder{})
}

// saveAged saves a memory about "quartz scheduler" of the given type and
// backdates its review clock by ageDays.
func (e reviewEnv) saveAged(t *testing.T, typ model.MemoryType, title string, ageDays int) string {
	t.Helper()
	req := model.SaveRequest{
		Title:   title,
		Content: "quartz scheduler misfire handling notes",
		Type:    typ,
	}
	if typ == model.TypeRule {
		req.AppliesTo = []string{"**"}
	}
	resp, err := e.svc.Save(context.Background(), req)
	if err != nil {
		t.Fatalf("Save %q: %v", title, err)
	}
	at := time.Now().UTC().Add(-time.Duration(ageDays) * 24 * time.Hour)
	if err := e.st.MarkReviewed(context.Background(), resp.ID, at); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	return resp.ID
}

func searchQuartz(t *testing.T, svc *service.MemoryService) []model.SearchResult {
	t.Helper()
	resp, err := svc.Search(context.Background(), model.SearchRequest{Query: "quartz scheduler misfire", Limit: 20})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return resp.Results
}

func byID(results []model.SearchResult) map[string]model.SearchResult {
	out := make(map[string]model.SearchResult, len(results))
	for _, r := range results {
		out[r.ID] = r
	}
	return out
}

// TestSearch_ReviewDueAnnotation covers SPEC-167 AC5 (search half).
func TestSearch_ReviewDueAnnotation(t *testing.T) {
	e := newReviewEnv(t)
	stale := e.saveAged(t, model.TypeDiscovery, "stale discovery", 61)
	fresh := e.saveAged(t, model.TypeDiscovery, "fresh discovery", 59)
	pref := e.saveAged(t, model.TypePreference, "old preference", 1000)
	conv := e.saveAged(t, model.TypeConvention, "old convention", 1000)

	results := searchQuartz(t, e.svc)
	got := byID(results)
	for _, id := range []string{stale, fresh, pref, conv} {
		if _, ok := got[id]; !ok {
			t.Fatalf("memory %s missing from search results", id)
		}
	}
	if !got[stale].ReviewDue {
		t.Error("61-day-old discovery must be review_due")
	}
	note := got[stale].ReviewNote
	if !strings.Contains(note, "mem_update confirm:true") || !strings.Contains(note, "no se lo muestres al usuario") {
		t.Errorf("review_note lacks the required guidance: %q", note)
	}
	for name, id := range map[string]string{"59-day discovery": fresh, "old preference": pref, "old convention": conv} {
		if got[id].ReviewDue || got[id].ReviewNote != "" {
			t.Errorf("%s must not be flagged: %+v", name, got[id].Memory)
		}
	}

	// The annotation only decorates: the same databases searched with every
	// deadline disabled return the same results in the same order.
	cfg := config.Default()
	cfg.Memory.ReviewDays = map[string]int{}
	for typ := range config.DefaultReviewDays {
		cfg.Memory.ReviewDays[typ] = 0
	}
	plain := searchQuartz(t, e.serviceWith(cfg))
	if len(plain) != len(results) {
		t.Fatalf("result count changed: %d vs %d", len(plain), len(results))
	}
	for i := range plain {
		if plain[i].ID != results[i].ID {
			t.Errorf("order differs at %d", i)
		}
		if plain[i].ReviewDue {
			t.Error("nothing may be flagged when every deadline is disabled")
		}
	}

	// A user override changes the deadline: discovery = 30 flags the 59-day one.
	cfg = config.Default()
	cfg.Memory.ReviewDays = map[string]int{"discovery": 30}
	if !byID(searchQuartz(t, e.serviceWith(cfg)))[fresh].ReviewDue {
		t.Error("discovery deadline of 30 days must flag a 59-day-old discovery")
	}
}

// TestContext_ReviewDueAnnotation covers SPEC-167 AC5 (context half).
func TestContext_ReviewDueAnnotation(t *testing.T) {
	e := newReviewEnv(t)
	staleDiscovery := e.saveAged(t, model.TypeDiscovery, "stale discovery", 61)
	staleRule := e.saveAged(t, model.TypeRule, "stale rule", 181)
	freshRule := e.saveAged(t, model.TypeRule, "fresh rule", 10)
	oldPref := e.saveAged(t, model.TypePreference, "old preference", 1000)

	resp, err := e.svc.Context(context.Background(), model.ContextRequest{Budget: 8000})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	flagged := map[string]bool{}
	seen := map[string]bool{}
	for _, m := range append(append([]model.Memory{}, resp.Memories...), resp.Rules...) {
		seen[m.ID] = true
		flagged[m.ID] = m.ReviewDue
		if m.ReviewDue && m.ReviewNote == "" {
			t.Errorf("%s flagged without a note", m.ID)
		}
	}
	for _, id := range []string{staleDiscovery, staleRule, freshRule, oldPref} {
		if !seen[id] {
			t.Fatalf("memory %s missing from the context bundle", id)
		}
	}
	if !flagged[staleDiscovery] || !flagged[staleRule] {
		t.Errorf("stale discovery/rule must be flagged: %v", flagged)
	}
	if flagged[freshRule] || flagged[oldPref] {
		t.Errorf("fresh rule / preference must not be flagged: %v", flagged)
	}
}

// TestUpdate_ConfirmResetsReviewWithoutRevision covers SPEC-167 AC6.
func TestUpdate_ConfirmResetsReviewWithoutRevision(t *testing.T) {
	e := newReviewEnv(t)
	ctx := context.Background()
	id := e.saveAged(t, model.TypeDiscovery, "stale discovery", 61)

	before, err := e.svc.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp, err := e.svc.Update(ctx, id, model.UpdateRequest{Confirm: true})
	if err != nil {
		t.Fatalf("Update confirm: %v", err)
	}
	if resp.ReviewedAt == nil {
		t.Fatal("ReviewedAt must be set when confirming")
	}
	after, err := e.svc.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if after.RevisionCount != before.RevisionCount || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("confirm must not count as an edit: revision %d->%d updated_at %v->%v",
			before.RevisionCount, after.RevisionCount, before.UpdatedAt, after.UpdatedAt)
	}
	if byID(searchQuartz(t, e.svc))[id].ReviewDue {
		t.Error("a confirmed memory must no longer be review_due")
	}

	// Confirm together with a content edit applies both.
	if err := e.st.MarkReviewed(ctx, id, time.Now().UTC().Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("age again: %v", err)
	}
	content := "quartz scheduler misfire handling notes, revised"
	resp, err = e.svc.Update(ctx, id, model.UpdateRequest{Content: &content, Confirm: true})
	if err != nil {
		t.Fatalf("Update confirm+content: %v", err)
	}
	if resp.ReviewedAt == nil || resp.RevisionCount <= before.RevisionCount {
		t.Errorf("combined update not applied: %+v", resp)
	}
	merged, err := e.svc.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get merged: %v", err)
	}
	if merged.Content != content {
		t.Errorf("content = %q", merged.Content)
	}
	if byID(searchQuartz(t, e.svc))[id].ReviewDue {
		t.Error("combined update must restart the review clock")
	}

	if (model.UpdateRequest{Confirm: true}).HasFieldChanges() {
		t.Error("Confirm must not count in HasFieldChanges")
	}
}

// TestReviewAnnotation_SurvivesMissingColumn covers SPEC-167 AC7: a database
// without reviewed_at still serves Search and Context, just unannotated.
func TestReviewAnnotation_SurvivesMissingColumn(t *testing.T) {
	e := newReviewEnv(t)
	id := e.saveAged(t, model.TypeDiscovery, "stale discovery", 61)
	if _, err := e.raw.Exec(`ALTER TABLE memories DROP COLUMN reviewed_at`); err != nil {
		t.Fatalf("drop column: %v", err)
	}

	results := searchQuartz(t, e.svc)
	got, ok := byID(results)[id]
	if !ok {
		t.Fatal("search lost the memory")
	}
	if got.ReviewDue {
		t.Error("nothing can be flagged without the column")
	}

	resp, err := e.svc.Context(context.Background(), model.ContextRequest{Budget: 8000})
	if err != nil {
		t.Fatalf("Context must not fail: %v", err)
	}
	for _, m := range resp.Memories {
		if m.ReviewDue {
			t.Errorf("%s flagged without the column", m.ID)
		}
	}
}
