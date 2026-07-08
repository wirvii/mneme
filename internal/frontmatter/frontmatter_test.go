package frontmatter

import (
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

// TestSetFrontmatter_FixesKnownKeys verifies that each well-known key is
// replaced correctly when it already exists, table-driven over the 5 keys.
func TestSetFrontmatter_FixesKnownKeys(t *testing.T) {
	original := `---
name: backend
description: "Invocar UNICAMENTE cuando se requiera implementacion."
model: claude-sonnet-4-6
color: green
permissionMode: bypassPermissions
tools: Read, Edit, Write
---

# Backend Agent

Body content here.
`

	tests := []struct {
		name   string
		fields Fields
		want   string
		absent string
	}{
		{"name", Fields{Name: strPtr("frontend")}, "name: frontend", "name: backend"},
		{"description", Fields{Description: strPtr("new desc")}, `description: new desc`, "Invocar UNICAMENTE"},
		{"model", Fields{Model: strPtr("opus")}, "model: opus", "claude-sonnet-4-6"},
		{"tools", Fields{Tools: strPtr("Read")}, "tools: Read", "Edit, Write"},
		{"permissionMode", Fields{PermissionMode: strPtr("default")}, "permissionMode: default", "bypassPermissions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SetFrontmatter([]byte(original), tt.fields)
			if err != nil {
				t.Fatalf("SetFrontmatter error: %v", err)
			}
			result := string(got)
			if !strings.Contains(result, tt.want) {
				t.Errorf("expected %q in result, got:\n%s", tt.want, result)
			}
			if strings.Contains(result, tt.absent) {
				t.Errorf("expected %q to be gone, got:\n%s", tt.absent, result)
			}
			// Unset fields (color, body) must survive verbatim.
			if !strings.Contains(result, "color: green") {
				t.Error("unrequested key 'color' must be preserved")
			}
			if !strings.Contains(result, "Body content here.") {
				t.Error("body must be preserved")
			}
		})
	}
}

// TestSetFrontmatter_PreservesUnknownKeys verifies that keys not in the
// managed set (comments, custom keys, list values) are never touched.
func TestSetFrontmatter_PreservesUnknownKeys(t *testing.T) {
	original := `---
name: backend
description: "desc"
model: sonnet
color: green
# a helpful comment
custom_key: custom_value
applies_to:
  - foo/**
  - bar/**
tools: Read
---

Body.
`
	got, err := SetFrontmatter([]byte(original), Fields{Model: strPtr("opus")})
	if err != nil {
		t.Fatalf("SetFrontmatter error: %v", err)
	}
	result := string(got)

	for _, want := range []string{
		"color: green",
		"# a helpful comment",
		"custom_key: custom_value",
		"applies_to:",
		"  - foo/**",
		"  - bar/**",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected unrelated content %q to survive, got:\n%s", want, result)
		}
	}
	if !strings.Contains(result, "model: opus") {
		t.Error("requested key model must be updated")
	}
}

// TestSetFrontmatter_AllFieldsAtOnce verifies setting all 5 managed keys in a
// single call, replacing existing values and leaving unmanaged keys alone.
func TestSetFrontmatter_AllFieldsAtOnce(t *testing.T) {
	original := `---
name: old-name
description: "old desc"
model: old-model
color: blue
permissionMode: old-mode
tools: old-tools
---

Body.
`
	got, err := SetFrontmatter([]byte(original), Fields{
		Name:           strPtr("new-name"),
		Description:    strPtr("new desc"),
		Model:          strPtr("new-model"),
		Tools:          strPtr("new-tools"),
		PermissionMode: strPtr("new-mode"),
	})
	if err != nil {
		t.Fatalf("SetFrontmatter error: %v", err)
	}
	result := string(got)

	for _, want := range []string{
		"name: new-name",
		"description: new desc",
		"model: new-model",
		"tools: new-tools",
		"permissionMode: new-mode",
		"color: blue",
		"Body.",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result, got:\n%s", want, result)
		}
	}
	for _, absent := range []string{"old-name", "old desc", "old-model", "old-tools", "old-mode"} {
		if strings.Contains(result, absent) {
			t.Errorf("expected %q to be gone, got:\n%s", absent, result)
		}
	}
}

// TestSetFrontmatter_InsertsMissingKeys verifies that a requested key absent
// from the frontmatter is inserted, anchored after the nearest preceding
// canonical key.
func TestSetFrontmatter_InsertsMissingKeys(t *testing.T) {
	tests := []struct {
		name          string
		original      string
		fields        Fields
		wantLine      string
		wantAfterLine string
	}{
		{
			name: "model missing, description present",
			original: `---
name: frontend
description: "Frontend agent"
color: red
tools: Read
---

Body.
`,
			fields:        Fields{Model: strPtr("haiku")},
			wantLine:      "model: haiku",
			wantAfterLine: `description: "Frontend agent"`,
		},
		{
			name: "tools missing, model present",
			original: `---
name: qa
description: "QA agent"
model: sonnet
---

Body.
`,
			fields:        Fields{Tools: strPtr("Read, Grep")},
			wantLine:      "tools: Read, Grep",
			wantAfterLine: "model: sonnet",
		},
		{
			name: "permissionMode missing, no preceding canonical key present",
			original: `---
color: red
---

Body.
`,
			fields:   Fields{PermissionMode: strPtr("bypassPermissions")},
			wantLine: "permissionMode: bypassPermissions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SetFrontmatter([]byte(tt.original), tt.fields)
			if err != nil {
				t.Fatalf("SetFrontmatter error: %v", err)
			}
			result := string(got)
			if !strings.Contains(result, tt.wantLine) {
				t.Errorf("expected inserted line %q, got:\n%s", tt.wantLine, result)
			}
			if !strings.Contains(result, "Body.") {
				t.Error("body must be preserved")
			}
			if tt.wantAfterLine != "" {
				lines := strings.Split(result, "\n")
				anchorIdx, insertedIdx := -1, -1
				for i, l := range lines {
					if l == tt.wantAfterLine {
						anchorIdx = i
					}
					if l == tt.wantLine {
						insertedIdx = i
					}
				}
				if anchorIdx == -1 || insertedIdx == -1 {
					t.Fatalf("could not locate anchor/inserted lines in result:\n%s", result)
				}
				if insertedIdx != anchorIdx+1 {
					t.Errorf("expected inserted line immediately after anchor: anchor=%d inserted=%d", anchorIdx, insertedIdx)
				}
			}
		})
	}
}

// TestSetFrontmatter_Idempotent verifies that applying SetFrontmatter twice
// with the same fields produces byte-identical output the second time.
func TestSetFrontmatter_Idempotent(t *testing.T) {
	original := `---
name: backend
description: "desc"
color: green
---

Body.
`
	fields := Fields{Model: strPtr("opus"), Tools: strPtr("Read, Edit")}

	first, err := SetFrontmatter([]byte(original), fields)
	if err != nil {
		t.Fatalf("first SetFrontmatter error: %v", err)
	}
	second, err := SetFrontmatter(first, fields)
	if err != nil {
		t.Fatalf("second SetFrontmatter error: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// Exactly one occurrence of each set key.
	for _, key := range []string{"model:", "tools:"} {
		if n := strings.Count(string(second), key); n != 1 {
			t.Errorf("expected exactly 1 occurrence of %q, got %d", key, n)
		}
	}
}

// TestSetFrontmatter_NilFieldsLeavesUntouched verifies that Fields{} (no
// requested changes) leaves the content byte-for-byte identical.
func TestSetFrontmatter_NilFieldsLeavesUntouched(t *testing.T) {
	original := `---
name: backend
description: "desc"
model: sonnet
---

Body.
`
	got, err := SetFrontmatter([]byte(original), Fields{})
	if err != nil {
		t.Fatalf("SetFrontmatter error: %v", err)
	}
	if string(got) != original {
		t.Errorf("expected byte-identical output with no fields set:\ngot:\n%s\nwant:\n%s", got, original)
	}
}

// TestSetFrontmatter_MissingOpenDelimiter returns an error when the opening
// --- delimiter is absent.
func TestSetFrontmatter_MissingOpenDelimiter(t *testing.T) {
	content := "name: foo\nmodel: x\n"
	_, err := SetFrontmatter([]byte(content), Fields{Model: strPtr("opus")})
	if err == nil {
		t.Error("expected error when opening --- is missing")
	}
}

// TestSetFrontmatter_MissingCloseDelimiter returns an error when the closing
// --- delimiter is absent.
func TestSetFrontmatter_MissingCloseDelimiter(t *testing.T) {
	content := "---\nname: foo\nmodel: x\n"
	_, err := SetFrontmatter([]byte(content), Fields{Model: strPtr("opus")})
	if err == nil {
		t.Error("expected error when closing --- is missing")
	}
}
