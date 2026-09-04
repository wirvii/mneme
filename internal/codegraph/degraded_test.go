package codegraph

import (
	"errors"
	"strings"
	"testing"
)

// TestNotice_EmptyShowsNothing verifies the common case (SPEC-142 AC9): a
// healthy graph with no degraded languages and no read error produces no
// notice at all, not an empty-but-present line.
func TestNotice_EmptyShowsNothing(t *testing.T) {
	line, show := Notice(nil, nil)
	if show {
		t.Errorf("show = true, want false for empty langs and nil readErr")
	}
	if line != "" {
		t.Errorf("line = %q, want empty", line)
	}
}

// TestNotice_SingleLanguage verifies the exact anchor and shape of the
// one-line banner (SPEC-142 D10).
func TestNotice_SingleLanguage(t *testing.T) {
	line, show := Notice([]DegradedLanguage{
		{Language: "typescript", Cause: CauseToolchainIncompatible},
	}, nil)
	if !show {
		t.Fatal("show = false, want true")
	}
	if !strings.HasPrefix(line, NoticeToken) {
		t.Errorf("line does not start with NoticeToken: %q", line)
	}
	if !strings.Contains(line, "typescript") {
		t.Errorf("line does not name the degraded language: %q", line)
	}
	if !strings.Contains(line, "toolchain-incompatible") {
		t.Errorf("line does not name the cause: %q", line)
	}
	if strings.Contains(line, "files_skipped_last_run") || strings.ContainsAny(line, "0123456789") {
		t.Errorf("line must never carry a count (SPEC-142 D2): %q", line)
	}
}

// TestNotice_MultipleLanguagesSortedAlphabetically verifies languages are
// listed alphabetically regardless of input order, so the banner is
// deterministic across runs (SPEC-142 D2/D10).
func TestNotice_MultipleLanguagesSortedAlphabetically(t *testing.T) {
	line, show := Notice([]DegradedLanguage{
		{Language: "javascript", Cause: CauseToolchainIncompatible},
		{Language: "typescript", Cause: CauseToolchainIncompatible},
	}, nil)
	if !show {
		t.Fatal("show = false, want true")
	}
	wantOrder := "javascript, typescript"
	if !strings.Contains(line, wantOrder) {
		t.Errorf("line = %q, want languages in alphabetical order %q", line, wantOrder)
	}
}

// TestNotice_MixedCauses verifies that a heterogeneous set of causes renders
// as "mixed" rather than picking one arbitrarily.
func TestNotice_MixedCauses(t *testing.T) {
	line, _ := Notice([]DegradedLanguage{
		{Language: "typescript", Cause: CauseToolchainIncompatible},
		{Language: "unknown", Cause: CauseUnreadableMark},
	}, nil)
	if !strings.Contains(line, "mixed") {
		t.Errorf("line = %q, want cause \"mixed\" for heterogeneous causes", line)
	}
}

// TestNotice_ReadErrFailsClosed verifies SPEC-142 D16's fail-closed exception:
// when the caller's own attempt to read the degraded-languages record itself
// failed, Notice shows a notice regardless of langs, because it cannot know
// the true state.
func TestNotice_ReadErrFailsClosed(t *testing.T) {
	line, show := Notice(nil, errors.New("boom"))
	if !show {
		t.Fatal("show = false, want true when readErr is non-nil")
	}
	if !strings.HasPrefix(line, NoticeToken) {
		t.Errorf("line does not start with NoticeToken: %q", line)
	}
}

// TestNotice_HasPrefixNotContains guards against the "substring collision"
// form of dead criterion (SPEC-142 plan step 1, form 5): NoticeToken must
// anchor the START of the line, not appear anywhere within it.
func TestNotice_HasPrefixNotContains(t *testing.T) {
	line, _ := Notice([]DegradedLanguage{{Language: "typescript", Cause: CauseToolchainIncompatible}}, nil)
	if strings.Index(line, NoticeToken) != 0 {
		t.Errorf("NoticeToken is not at index 0 of line: %q", line)
	}
}
