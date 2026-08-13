package quality

import (
	"reflect"
	"testing"
)

// TestParseUnifiedDiff covers AC9's table of raw-bytes edge cases — no git
// process involved.
func TestParseUnifiedDiff(t *testing.T) {
	tests := []struct {
		name string
		diff string
		want map[string][]int
	}{
		{
			name: "pure addition, no-comma count implied by comma form",
			diff: "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n@@ -1,0 +5,3 @@\n+a\n+b\n+c\n",
			want: map[string][]int{"f.go": {5, 6, 7}},
		},
		{
			name: "pure deletion contributes nothing",
			diff: "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n@@ -3,2 +0,0 @@\n-a\n-b\n",
			want: map[string][]int{},
		},
		{
			name: "no-comma shorthand form means a single line",
			diff: "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n@@ -4 +4 @@\n-old\n+new\n",
			want: map[string][]int{"f.go": {4}},
		},
		{
			name: "dev-null new side (deleted file) contributes nothing",
			diff: "diff --git a/f.go b/f.go\n--- a/f.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n",
			want: map[string][]int{},
		},
		{
			name: "path with a space",
			diff: "diff --git a/my file.go b/my file.go\n--- a/my file.go\n+++ b/my file.go\n@@ -1,0 +2,1 @@\n+x\n",
			want: map[string][]int{"my file.go": {2}},
		},
		{
			name: "renamed file attributes lines to the NEW path",
			diff: "diff --git a/old.go b/new.go\nsimilarity index 90%\nrename from old.go\nrename to new.go\n--- a/old.go\n+++ b/new.go\n@@ -1,0 +2,1 @@\n+x\n",
			want: map[string][]int{"new.go": {2}},
		},
		{
			name: "empty diff yields an empty map without error",
			diff: "",
			want: map[string][]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUnifiedDiff([]byte(tt.diff))
			if err != nil {
				t.Fatalf("ParseUnifiedDiff: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseUnifiedDiff() = %v, want %v", got, tt.want)
			}
			for path, wantLines := range tt.want {
				if !reflect.DeepEqual(got[path], wantLines) {
					t.Errorf("ParseUnifiedDiff()[%q] = %v, want %v", path, got[path], wantLines)
				}
			}
		})
	}
}

// TestParseUnifiedDiff_MalformedHunkHeader is the error path's anchor: a
// hunk header ParseUnifiedDiff cannot make sense of is an error, never a
// silently empty result.
func TestParseUnifiedDiff_MalformedHunkHeader(t *testing.T) {
	_, err := ParseUnifiedDiff([]byte("+++ b/f.go\n@@ garbage @@\n"))
	if err == nil {
		t.Fatal("ParseUnifiedDiff(malformed hunk header): want error, got nil")
	}
}
