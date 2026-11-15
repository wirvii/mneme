package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/risk"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLaneRiskTable(t *testing.T) {
	t.Run("no_section_is_default", func(t *testing.T) {
		cfg, err := Load(writeConfig(t, ""))
		if err != nil {
			t.Fatal(err)
		}
		got, err := cfg.LaneRiskTable()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, risk.DefaultTable()) {
			t.Errorf("LaneRiskTable = %v, want DefaultTable", got)
		}
	})

	t.Run("item1_adds", func(t *testing.T) {
		cfg, err := Load(writeConfig(t, "[lane.risk_globs]\n\"1\" = [\"**/*.dbml\"]\n"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := cfg.LaneRiskTable()
		if err != nil {
			t.Fatal(err)
		}
		want := append(risk.DefaultTable()[risk.ItemIrreversibleData], "**/*.dbml")
		if !reflect.DeepEqual(got[risk.ItemIrreversibleData], want) {
			t.Errorf("item 1 = %v, want %v", got[risk.ItemIrreversibleData], want)
		}
		if !reflect.DeepEqual(got[risk.ItemDelivery], risk.DefaultTable()[risk.ItemDelivery]) {
			t.Error("item 5 changed although nothing was added to it")
		}
	})

	errorCases := []struct {
		name     string
		body     string
		globs    map[string][]string
		sentinel error
		contains string
	}{
		{"item3_rejected", "[lane.risk_globs]\n\"3\" = [\"x/**\"]\n", map[string][]string{"3": {"x/**"}}, risk.ErrItemNotPathDetectable, "lane.risk_globs: "},
		{"non_numeric_key", "[lane.risk_globs]\nx = [\"x/**\"]\n", map[string][]string{"x": {"x/**"}}, nil, `lane.risk_globs: key "x" is not an item number`},
		{"empty_glob", "[lane.risk_globs]\n\"2\" = [\"\"]\n", map[string][]string{"2": {""}}, risk.ErrEmptyGlob, "lane.risk_globs: "},
	}
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatal("Load succeeded, want a validation error")
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.sentinel)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("err = %q, want it to contain %q", err, tc.contains)
			}

			cfg := Default()
			cfg.Lane.RiskGlobs = tc.globs
			_, direct := cfg.LaneRiskTable()
			if direct == nil || !strings.HasPrefix(direct.Error(), "lane.risk_globs") {
				t.Errorf("LaneRiskTable err = %v, want it to start with lane.risk_globs", direct)
			}
		})
	}
}

func TestLoadWithOrigins_Lane(t *testing.T) {
	field := func(t *testing.T, body string) ConfigFieldInfo {
		t.Helper()
		_, origins, err := LoadWithOrigins(writeConfig(t, body))
		if err != nil {
			t.Fatal(err)
		}
		fields := origins.Sections["lane"]
		if len(fields) != 1 || fields[0].Key != "risk_globs" {
			t.Fatalf("lane origins = %+v, want one risk_globs field", fields)
		}
		return fields[0]
	}

	if f := field(t, ""); f.Origin != OriginDefault || f.Value != "" {
		t.Errorf("without section: %+v, want origin default and value \"\"", f)
	}
	if f := field(t, "[lane.risk_globs]\n\"1\" = [\"**/*.dbml\"]\n"); f.Origin != OriginFile || f.Value != "1=**/*.dbml" {
		t.Errorf("with section: %+v, want origin file and value 1=**/*.dbml", f)
	}
	if f := field(t, "[lane.risk_globs]\n\"5\" = [\"infra/**\"]\n\"1\" = [\"a\", \"b\"]\n"); f.Value != "1=a,b; 5=infra/**" {
		t.Errorf("two items: value %q, want 1=a,b; 5=infra/**", f.Value)
	}
}
