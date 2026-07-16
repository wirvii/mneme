package subagents

import (
	"strings"
	"testing"
)

// TestScanLayer23Leaks_TableDriven is AC1: the three lifecycle tokens
// (including the "mcp__mneme__"-prefixed form), the two capability keys at
// start of line, and a clean region that must report no leaks at all.
func TestScanLayer23Leaks_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		region    string
		wantKinds []Layer23LeakKind
	}{
		{
			name:      "clean project knowledge has no leaks",
			region:    "## Área: apps/core-srv\n\nStack Go + sqlc. Usa pgx/v5.",
			wantKinds: nil,
		},
		{
			name:      "literal spec_advance token",
			region:    "Cuando termines, llama spec_advance.",
			wantKinds: []Layer23LeakKind{Layer23LeakLifecycleToken},
		},
		{
			name:      "literal spec_quick token",
			region:    "Usa spec_quick para lo trivial.",
			wantKinds: []Layer23LeakKind{Layer23LeakLifecycleToken},
		},
		{
			name:      "literal spec_reject token",
			region:    "Si falla, spec_reject.",
			wantKinds: []Layer23LeakKind{Layer23LeakLifecycleToken},
		},
		{
			name:      "mcp-prefixed token still caught by substring",
			region:    "Llama a mcp__mneme__spec_advance cuando termines.",
			wantKinds: []Layer23LeakKind{Layer23LeakLifecycleToken},
		},
		{
			name:      "tools key at start of line",
			region:    "## Área: x\n\ntools: Read, Grep, Edit, Write, Bash\n",
			wantKinds: []Layer23LeakKind{Layer23LeakCapabilityKey},
		},
		{
			name:      "permissionMode key at start of line, indented",
			region:    "## Área: x\n\n  permissionMode: bypassPermissions\n",
			wantKinds: []Layer23LeakKind{Layer23LeakCapabilityKey},
		},
		{
			name:      "word tools appearing mid-sentence is not a leak",
			region:    "Usa las herramientas y tools disponibles para el stack.",
			wantKinds: nil,
		},
		{
			name:      "both classes on separate lines",
			region:    "tools: Read\nCuando termines llama spec_advance.",
			wantKinds: []Layer23LeakKind{Layer23LeakCapabilityKey, Layer23LeakLifecycleToken},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScanLayer23Leaks(tt.region)
			if len(got) != len(tt.wantKinds) {
				t.Fatalf("ScanLayer23Leaks(%q) = %+v, want %d leak(s) of kinds %v", tt.region, got, len(tt.wantKinds), tt.wantKinds)
			}
			for i, leak := range got {
				if leak.Kind != tt.wantKinds[i] {
					t.Errorf("leak[%d].Kind = %q, want %q", i, leak.Kind, tt.wantKinds[i])
				}
				if leak.Line < 1 {
					t.Errorf("leak[%d].Line = %d, want >= 1", i, leak.Line)
				}
			}
		})
	}
}

// TestScanLayer23Leaks_ReportsLineNumber pins that Line points at the actual
// offending line, not just at leak presence — the guard error message
// (D2) names token+line.
func TestScanLayer23Leaks_ReportsLineNumber(t *testing.T) {
	region := "## Área: apps/x\n\nStack Go.\n\nAl terminar llama spec_advance.\n"
	got := ScanLayer23Leaks(region)
	if len(got) != 1 {
		t.Fatalf("leaks = %+v, want exactly 1", got)
	}
	if got[0].Line != 5 {
		t.Errorf("Line = %d, want 5", got[0].Line)
	}
	if got[0].Token != "spec_advance" {
		t.Errorf("Token = %q, want spec_advance", got[0].Token)
	}
}

// TestExtractGrillRegion_RoundTrip is G4: ExtractGrillRegion must always
// recover exactly what was placed between GrillContentWrapStart/End.
// Mutation: desync the constants ExtractGrillRegion searches for from the
// ones a wrap step writes with -> this test goes red.
func TestExtractGrillRegion_RoundTrip(t *testing.T) {
	x := "## Área: apps/core-srv\n\nConocimiento de proyecto arbitrario sin marcadores especiales."

	wrapped := GrillContentWrapStart + "\n\n" + x + "\n\n" + GrillContentWrapEnd

	got, ok := ExtractGrillRegion(wrapped)
	if !ok {
		t.Fatalf("ExtractGrillRegion(%q) ok = false, want true", wrapped)
	}
	if got != x {
		t.Errorf("ExtractGrillRegion round-trip = %q, want %q", got, x)
	}
}

// TestExtractGrillRegion_RoundTrip_EmbeddedInFullProfile confirms the
// extraction works when the wrapped block sits inside a larger document
// (frontmatter + agent-fixed block + layer-2 + wrapped layer-3), which is
// the actual shape subagent_write's composed_md arrives in.
func TestExtractGrillRegion_RoundTrip_EmbeddedInFullProfile(t *testing.T) {
	x := "## Área: apps/core-srv\n\nStack Go + sqlc."
	fileBody := "---\nname: backend\n---\n" +
		"<!-- mneme:agent-fixed:start v=2 -->\nNUNCA llames spec_advance.\n<!-- mneme:agent-fixed:end -->\n\n" +
		GrillContentWrapStart + "\n\n" + x + "\n\n" + GrillContentWrapEnd + "\n"

	got, ok := ExtractGrillRegion(fileBody)
	if !ok {
		t.Fatalf("ExtractGrillRegion ok = false, want true")
	}
	if got != x {
		t.Errorf("ExtractGrillRegion = %q, want %q", got, x)
	}
}

func TestExtractGrillRegion_MissingDelimiters(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no start marker", "some content\n" + GrillContentWrapEnd},
		{"no end marker", GrillContentWrapStart + "\nsome content\n"},
		{"neither marker", "no markers at all here"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := ExtractGrillRegion(tt.body)
			if ok {
				t.Errorf("ExtractGrillRegion(%q) ok = true, want false", tt.body)
			}
		})
	}
}

// TestExtractGrillRegion_G2NeverScansOutsideTheRegion is the structural
// half of G2 (the AC4 property): the layer-1 block legitimately says "NUNCA
// llames spec_advance" OUTSIDE the wrap markers. ExtractGrillRegion must
// never return content spanning that layer-1 text — only what sits strictly
// between the markers.
func TestExtractGrillRegion_G2NeverScansOutsideTheRegion(t *testing.T) {
	fileBody := "<!-- mneme:agent-fixed:start v=2 -->\n" +
		"NUNCA llames spec_advance, spec_quick o spec_reject.\n" +
		"<!-- mneme:agent-fixed:end -->\n\n" +
		GrillContentWrapStart + "\n\n" +
		"## Área: apps/x\n\nStack limpio, sin lifecycle." + "\n\n" +
		GrillContentWrapEnd + "\n"

	region, ok := ExtractGrillRegion(fileBody)
	if !ok {
		t.Fatalf("ExtractGrillRegion ok = false, want true")
	}
	if strings.Contains(region, "spec_advance") {
		t.Errorf("region leaked layer-1 content: %q", region)
	}
	if leaks := ScanLayer23Leaks(region); len(leaks) != 0 {
		t.Errorf("ScanLayer23Leaks(region) = %+v, want no leaks — the layer-1 prohibition must never be scanned", leaks)
	}
}
