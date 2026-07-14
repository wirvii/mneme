package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/querylog"
)

// seedAdoptionLog writes uses "use" events and opps "opportunity" events for the
// given slug under dataDir, all timestamped now.
func seedAdoptionLog(t *testing.T, dataDir, slug string, uses, opps int) {
	t.Helper()
	path := codegraph.QuerylogPath(filepath.Join(dataDir, "projects"), slug)
	now := time.Now().UTC()
	for i := 0; i < uses; i++ {
		if err := querylog.Append(path, querylog.Event{TS: now, Project: slug, Kind: querylog.KindUse, Tool: "codegraph_search", Source: "mcp"}, querylog.DefaultMaxBytes); err != nil {
			t.Fatalf("Append use: %v", err)
		}
	}
	for i := 0; i < opps; i++ {
		if err := querylog.Append(path, querylog.Event{TS: now, Project: slug, Kind: querylog.KindOpportunity, Tool: "Grep", Source: "hook"}, querylog.DefaultMaxBytes); err != nil {
			t.Fatalf("Append opp: %v", err)
		}
	}
}

// runAdoption executes the adoption command against dataDir/slug with the given
// extra args and returns captured stdout.
func runAdoption(t *testing.T, dataDir, slug string, args ...string) string {
	t.Helper()
	oldProject, oldDataDir := flagProject, flagDataDir
	flagProject, flagDataDir = slug, dataDir
	t.Cleanup(func() { flagProject, flagDataDir = oldProject, oldDataDir })

	cmd := newCodegraphAdoptionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("adoption Execute: %v", err)
	}
	return buf.String()
}

func TestAdoption_Report(t *testing.T) {
	dataDir := t.TempDir()
	slug := "wirvii/mneme"
	seedAdoptionLog(t, dataDir, slug, 3, 7)

	out := runAdoption(t, dataDir, slug, "--since", "30d")

	if !strings.Contains(out, "0.30") {
		t.Errorf("expected adoption ratio 0.30 (3/10), got: %s", out)
	}
	if !strings.Contains(out, "uses 3 / opportunities 7") {
		t.Errorf("expected use/opportunity counts, got: %s", out)
	}
	if !strings.Contains(out, "codegraph_search") || !strings.Contains(out, "Grep") {
		t.Errorf("expected top tool breakdowns, got: %s", out)
	}
}

func TestAdoption_JSON(t *testing.T) {
	dataDir := t.TempDir()
	slug := "wirvii/mneme"
	seedAdoptionLog(t, dataDir, slug, 2, 2)

	out := runAdoption(t, dataDir, slug, "--json", "--since", "30d")

	var report querylog.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("json output not parseable: %v\n%s", err, out)
	}
	if report.Uses != 2 || report.Opportunities != 2 {
		t.Errorf("json report = %+v, want uses=2 opps=2", report)
	}
	if report.AdoptionRatio != 0.5 {
		t.Errorf("json ratio = %v, want 0.5", report.AdoptionRatio)
	}
}

func TestAdoption_NoData(t *testing.T) {
	dataDir := t.TempDir()
	slug := "wirvii/mneme"

	out := runAdoption(t, dataDir, slug, "--since", "7d")

	if !strings.Contains(out, "No adoption data") {
		t.Errorf("expected no-data message, got: %s", out)
	}
}

func TestParseSinceWindow(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"", 7 * 24 * time.Hour, false},
		{"banana", 0, true},
		{"-3d", 0, true},
	}
	for _, tc := range cases {
		got, err := parseSinceWindow(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseSinceWindow(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSinceWindow(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSinceWindow(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
