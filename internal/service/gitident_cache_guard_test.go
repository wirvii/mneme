package service_test

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestAuthor_ResolvesDistinctIdentitiesAcrossSubtests is the SPEC-085 AC5
// guard: two subtests in the SAME test binary, each with a DIFFERENT git
// identity, must each resolve their OWN identity via gitident.Author() (baked
// into mem.Author by bakeTeamMemoryFields) — not whichever identity happened
// to be cached first by gitident's process-wide sync.Once.
//
// This deliberately does NOT reuse "Team Member <team@example.com>", the
// identity every other newRepoTestService(...) call site in this package
// shares. Sharing one identity across all fixtures is exactly why the
// original newRepoTestService change (SPEC-085 commit cf3b3bd, adding
// gitident.Reset()) had no test proving it does anything: with every fixture
// expecting the same Author() value, a stale cache and a fresh one are
// observationally identical — deleting gitident.Reset() from
// newRepoTestServiceWithIdentity makes no existing test go red. Two
// subtests with two DIFFERENT identities close that gap: the second subtest
// can only see its own identity if the cache was actually reset between
// subtests, not memoized from the first.
//
// Mental test: delete gitident.Reset() (both call sites in
// newRepoTestServiceWithIdentity) and re-run — the second subtest must fail,
// asserting "Committer One" where it expected "Committer Two". Verified
// manually while writing this test.
func TestAuthor_ResolvesDistinctIdentitiesAcrossSubtests(t *testing.T) {
	t.Run("first identity", func(t *testing.T) {
		svc, _ := newRepoTestServiceWithIdentity(t, true, "Committer One", "one@example.com")
		ctx := context.Background()

		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   "Decision under the first identity",
			Content: "Content",
			Type:    model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save: unexpected error: %v", err)
		}

		mem, err := svc.Get(ctx, resp.ID)
		if err != nil {
			t.Fatalf("Get: unexpected error: %v", err)
		}
		const want = "Committer One <one@example.com>"
		if mem.Author != want {
			t.Errorf("Author = %q, want %q", mem.Author, want)
		}
	})

	t.Run("second identity", func(t *testing.T) {
		svc, _ := newRepoTestServiceWithIdentity(t, true, "Committer Two", "two@example.com")
		ctx := context.Background()

		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   "Decision under the second identity",
			Content: "Content",
			Type:    model.TypeDecision,
		})
		if err != nil {
			t.Fatalf("Save: unexpected error: %v", err)
		}

		mem, err := svc.Get(ctx, resp.ID)
		if err != nil {
			t.Fatalf("Get: unexpected error: %v", err)
		}
		// This is the assertion that catches a missing gitident.Reset(): a
		// stale cache from the first subtest would still say "Committer One".
		const want = "Committer Two <two@example.com>"
		if mem.Author != want {
			t.Errorf("Author = %q, want %q (a stale process-wide gitident cache would report the first subtest's identity instead)", mem.Author, want)
		}
	})
}
