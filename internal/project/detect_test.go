package project_test

import (
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/project"
)

func TestNormalizeRemoteURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "SSH shorthand with .git",
			input: "git@github.com:org/repo.git",
			want:  "org/repo",
		},
		{
			name:  "HTTPS with .git",
			input: "https://github.com/org/repo.git",
			want:  "org/repo",
		},
		{
			name:  "HTTPS without .git",
			input: "https://github.com/org/repo",
			want:  "org/repo",
		},
		{
			name:  "SSH with port",
			input: "ssh://git@github.com:22/org/repo.git",
			want:  "org/repo",
		},
		{
			name:  "GitLab nested path",
			input: "git@gitlab.com:team/sub/repo.git",
			want:  "team/sub/repo",
		},
		{
			name:  "trailing spaces",
			input: "  git@github.com:org/repo.git  ",
			want:  "org/repo",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "URL with uppercase letters",
			input: "https://github.com/Org/Repo.git",
			want:  "org/repo",
		},
		{
			name:  "SSH shorthand confio-pagos",
			input: "git@github.com:confio-pagos/platform.git",
			want:  "confio-pagos/platform",
		},
		{
			name:  "HTTPS nested three levels",
			input: "https://github.com/org/sub/repo.git",
			want:  "org/sub/repo",
		},
		{
			name:  "HTTPS without .git and uppercase",
			input: "https://github.com/Juan/Mneme",
			want:  "juan/mneme",
		},
		{
			name:  "SSH shorthand mneme",
			input: "git@github.com:juan/mneme.git",
			want:  "juan/mneme",
		},
		{
			name:  "SSH with port nested path",
			input: "ssh://git@github.com:22/org/sub/repo.git",
			want:  "org/sub/repo",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := project.NormalizeRemoteURL(tc.input)
			if got != tc.want {
				t.Errorf("NormalizeRemoteURL(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSlugToFilename(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "org with hyphen",
			input: "confio-pagos/platform",
			want:  "confio-pagos-platform",
		},
		{
			name:  "simple slug",
			input: "juan/mneme",
			want:  "juan-mneme",
		},
		{
			name:  "nested three levels",
			input: "team/sub/repo",
			want:  "team-sub-repo",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "no slashes",
			input: "standalone",
			want:  "standalone",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := project.SlugToFilename(tc.input)
			if got != tc.want {
				t.Errorf("SlugToFilename(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestDetectProject verifies that DetectProject returns a non-empty slug when
// run from a directory that is inside a git repository (the mneme repo itself
// satisfies this condition in CI and local dev). The exact value is checked
// only for structural correctness — it must be non-empty and lowercase.
func TestDetectProject(t *testing.T) {
	t.Parallel()

	// Use the module root, which is guaranteed to be a git repo.
	d := project.NewDetector("../../")
	slug, err := d.DetectProject()
	if err != nil {
		t.Fatalf("DetectProject() error: %v", err)
	}
	if slug == "" {
		t.Fatal("DetectProject() returned empty slug; expected a non-empty project slug")
	}
	if slug != strings.ToLower(slug) {
		t.Errorf("DetectProject() returned non-lowercase slug: %q", slug)
	}
	t.Logf("detected slug: %q", slug)
}
