package subagents

import (
	"strings"
	"testing"
)

func TestCutSection(t *testing.T) {
	asset := LayerOneAsset()

	tests := []struct {
		name         string
		wantContains string
		wantMissing  string
	}{
		{
			name:         "codegraph-policy-readonly",
			wantContains: "## Exploracion de codigo: grafo primero",
			wantMissing:  "NO uses `Bash`",
		},
		{
			name:         "codegraph-policy-implementer",
			wantContains: "NO uses `Bash` (grep/cat/find/rg/head/tail)",
		},
		{
			name:         "codegraph-policy-diagnostician",
			wantContains: "## Permisos de Bash",
		},
		{
			name:         "mneme-integration-generic",
			wantContains: `spec_advance(SPEC-XXX, by: "{{ROLE}}")`,
		},
		{
			name:         "mneme-integration-diagnostician",
			wantContains: "Al INICIO de cada investigacion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CutSection(asset, tt.name)
			if err != nil {
				t.Fatalf("CutSection(%q): %v", tt.name, err)
			}
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("CutSection(%q) missing expected content %q\ngot:\n%s", tt.name, tt.wantContains, got)
			}
			if tt.wantMissing != "" && strings.Contains(got, tt.wantMissing) {
				t.Errorf("CutSection(%q) unexpectedly contains %q\ngot:\n%s", tt.name, tt.wantMissing, got)
			}
		})
	}
}

func TestCutSection_UnknownSection(t *testing.T) {
	_, err := CutSection(LayerOneAsset(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown section")
	}
}

func TestCutSections_JoinsInOrder(t *testing.T) {
	got, err := CutSections(LayerOneAsset(), "codegraph-policy-readonly", "mneme-integration-generic")
	if err != nil {
		t.Fatalf("CutSections: %v", err)
	}

	idxCodegraph := strings.Index(got, "Exploracion de codigo")
	idxIntegration := strings.Index(got, "Integracion con mneme")
	if idxCodegraph == -1 || idxIntegration == -1 {
		t.Fatalf("expected both sections present, got:\n%s", got)
	}
	if idxCodegraph > idxIntegration {
		t.Errorf("expected codegraph-policy section before mneme-integration, got order reversed")
	}
}

func TestCutSections_PropagatesFirstError(t *testing.T) {
	_, err := CutSections(LayerOneAsset(), "codegraph-policy-readonly", "nonexistent")
	if err == nil {
		t.Fatal("expected error to propagate from CutSections")
	}
}
