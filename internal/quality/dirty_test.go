package quality

import "testing"

func TestClassifyDirtyPaths_SplitsMnemeFromProject(t *testing.T) {
	lines := []string{
		"?? .mneme/shared/notes/abc123.md",
		"?? .mneme/sdd/backlog/BL-999.md",
		" M internal/service/quality.go",
		"?? docs/new-file.md",
	}

	got := ClassifyDirtyPaths(lines)

	if len(got.Mneme) != 2 {
		t.Fatalf("Mneme = %v, want 2 entries", got.Mneme)
	}
	if len(got.Project) != 2 {
		t.Fatalf("Project = %v, want 2 entries", got.Project)
	}
	for _, line := range got.Mneme {
		if line != lines[0] && line != lines[1] {
			t.Errorf("unexpected line in Mneme group: %q", line)
		}
	}
	for _, line := range got.Project {
		if line != lines[2] && line != lines[3] {
			t.Errorf("unexpected line in Project group: %q", line)
		}
	}
}

func TestClassifyDirtyPaths_EmptyInputProducesEmptyGroups(t *testing.T) {
	got := ClassifyDirtyPaths(nil)
	if len(got.Mneme) != 0 || len(got.Project) != 0 {
		t.Errorf("ClassifyDirtyPaths(nil) = %+v, want both groups empty", got)
	}
}

// TestClassifyDirtyPaths_QualityTomlStaysProject guards a specific,
// deliberate exclusion (D9): .mneme/quality.toml itself is a project file
// a human edits and commits, so a dirty change to it must classify as
// "project", never "mneme" — even though it lives under .mneme/.
func TestClassifyDirtyPaths_QualityTomlStaysProject(t *testing.T) {
	got := ClassifyDirtyPaths([]string{" M .mneme/quality.toml"})
	if len(got.Project) != 1 || len(got.Mneme) != 0 {
		t.Errorf("ClassifyDirtyPaths(quality.toml) = %+v, want it classified as project", got)
	}
}

func TestPorcelainPath_ExtractsPathAfterStatusCode(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"?? .mneme/shared/notes/x.md", ".mneme/shared/notes/x.md"},
		{" M internal/service/quality.go", "internal/service/quality.go"},
		{"A  docs/new.md", "docs/new.md"},
	}
	for _, tt := range tests {
		if got := PorcelainPath(tt.line); got != tt.want {
			t.Errorf("PorcelainPath(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// TestPorcelainPath_TooShortLineReturnedUnchanged is defensive: git never
// actually emits a line shorter than 4 characters, but the function must
// not panic if one somehow arrives.
func TestPorcelainPath_TooShortLineReturnedUnchanged(t *testing.T) {
	if got := PorcelainPath("?"); got != "?" {
		t.Errorf("PorcelainPath(short) = %q, want unchanged input", got)
	}
}
