package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/juanftp/mneme/internal/model"
)

func TestSearch_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	memories := []model.SaveRequest{
		{
			Title:   "SQLite FTS5 fulltext search",
			Content: "SQLite FTS5 supports BM25 ranking for fulltext search queries using porter tokenizer.",
		},
		{
			Title:   "PostgreSQL connection pooling",
			Content: "Use pgbouncer for connection pooling with PostgreSQL in production.",
		},
		{
			Title:   "Redis cache eviction policy",
			Content: "Set maxmemory policy to allkeys lru for general purpose caching.",
		},
	}

	for _, req := range memories {
		if _, err := svc.Save(ctx, req); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "SQLite fulltext search",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	if resp.Query != "SQLite fulltext search" {
		t.Errorf("expected query echoed, got %q", resp.Query)
	}
	// The SQLite FTS5 memory should be the top result.
	if resp.Results[0].Memory.Title != "SQLite FTS5 fulltext search" {
		t.Errorf("expected SQLite memory first, got %q", resp.Results[0].Memory.Title)
	}
}

func TestSearch_Validation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Search(ctx, model.SearchRequest{Query: ""})
	if !errors.Is(err, model.ErrQueryRequired) {
		t.Errorf("expected ErrQueryRequired, got %v", err)
	}
}

func TestSearch_LimitCap(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save enough memories to exercise the cap.
	for i := 0; i < 5; i++ {
		_, err := svc.Save(ctx, model.SaveRequest{
			Title:   "test memory",
			Content: "content for limit test",
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// A limit over 50 should be silently capped at 50. We cannot exceed 50
	// results from 5 rows, but we verify no error occurs and Total <= 50.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "test memory content",
		Limit: 200,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Total > 50 {
		t.Errorf("expected Total <= 50, got %d", resp.Total)
	}
}

func TestSearch_ProjectFilter(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save a memory explicitly in a different project.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "other project memory",
		Content: "this belongs to another project",
		Project: "other/project",
	})
	if err != nil {
		t.Fatalf("Save (other project): %v", err)
	}

	// Search within the default test project — should not return the above memory.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query:   "other project memory",
		Project: "test/project",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range resp.Results {
		if r.Memory.Project == "other/project" {
			t.Errorf("got memory from wrong project: %q", r.Memory.Project)
		}
	}
}
