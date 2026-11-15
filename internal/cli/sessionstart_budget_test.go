package cli

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// sessionStartMaxBytes is the byte ceiling of `mneme hook session-start`
// over the deterministic 200-memory base below (SPEC-187, D4a). It is the
// measured output of the default configuration rounded up to the next
// multiple of 1024, and it becomes the baseline later specs measure
// against. The hard bound is 16384: if the measure ever exceeds it the
// ceiling is not raised; the budgets (DefaultBudget, RulesBudget) or the
// fixture are revised until it fits, and the change is recorded.
const sessionStartMaxBytes = 14336

// sessionStartHardCeiling is the bound the spec set for sessionStartMaxBytes.
const sessionStartHardCeiling = 16384

// sessionStartSlug is the project the fixture is written under.
const sessionStartSlug = "fixture/sessionstart"

// Fixture composition, in the proportion measured on 2026-11-14: 112
// syntheses (56 %) and 88 human memories.
const (
	fixtureSyntheses   = 112
	fixtureHumanBytes  = 600
	fixtureSynthBytes  = 1500
	fixtureRuleBytes   = 300
	fixtureSummarySize = 800
)

// fixtureParagraph is the fixed ASCII text every fixture body is cut from.
const fixtureParagraph = "mneme keeps what agents learn between sessions; this paragraph is filler of a fixed length. "

// fixtureBody returns fixtureParagraph repeated and cut to exactly n bytes.
func fixtureBody(n int) string {
	return strings.Repeat(fixtureParagraph, n/len(fixtureParagraph)+1)[:n]
}

// seedRepresentativeBase writes the deterministic 200-memory base into the
// project database under dataDir: 112 syntheses (5 of them linked to the 5
// communities fixture-01..fixture-05, the other 107 orphaned as in a real
// base whose communities moved), 30 decisions, 20 discoveries, 12 bugfixes,
// 8 conventions, 6 architecture, 7 patterns, 4 rules and one session
// summary. Every importance is distinct, also after the x1.5 the context
// ranking gives architecture, so the ordering is stable.
func seedRepresentativeBase(t *testing.T, dataDir, slug string) {
	t.Helper()
	cfg := config.Default()
	cfg.Storage.DataDir = dataDir
	projectDB, err := db.Open(cfg.ProjectDBPath(slug))
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.Open(cfg.GlobalDBPath())
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	defer globalDB.Close()
	defer projectDB.Close()
	ps := store.NewMemoryStore(projectDB)
	ctx := context.Background()

	scores := map[int64]string{}
	addScore := func(title string, score float64) {
		key := int64(math.Round(score * 1e9))
		if prev, ok := scores[key]; ok {
			t.Fatalf("fixture tie: %s and %s rank at %.9f", prev, title, score)
		}
		scores[key] = title
	}
	upsert := func(m *model.Memory) {
		t.Helper()
		m.Scope = model.ScopeProject
		m.Project = slug
		if _, _, err := ps.Upsert(ctx, m); err != nil {
			t.Fatalf("Upsert %s: %v", m.Title, err)
		}
	}

	for j := 0; j < fixtureSyntheses; j++ {
		nn := fmt.Sprintf("%02d", j+1)
		imp := 0.700 + 0.001*float64(j)
		title := "fixture-synthesis-" + nn
		addScore(title, imp)
		upsert(&model.Memory{
			Type: model.TypeSynthesis, Title: title, Content: fixtureBody(fixtureSynthBytes),
			TopicKey: "synthesis/community-fixture-" + nn, Importance: imp,
		})
	}

	humans := []struct {
		typ   model.MemoryType
		count int
	}{
		{model.TypeDecision, 30}, {model.TypeDiscovery, 20}, {model.TypeBugfix, 12},
		{model.TypeConvention, 8}, {model.TypePattern, 7},
	}
	m := 0
	for _, h := range humans {
		for i := 0; i < h.count; i++ {
			imp := 0.300 + 0.003*float64(m)
			m++
			title := fmt.Sprintf("fixture-%s-%02d", h.typ, i+1)
			addScore(title, imp)
			upsert(&model.Memory{Type: h.typ, Title: title, Content: fixtureBody(fixtureHumanBytes), Importance: imp})
		}
	}
	for k := 0; k < 6; k++ {
		imp := 0.3005 + 0.003*float64(k)
		title := fmt.Sprintf("fixture-%s-%02d", model.TypeArchitecture, k+1)
		addScore(title, imp*1.5)
		upsert(&model.Memory{Type: model.TypeArchitecture, Title: title, Content: fixtureBody(fixtureHumanBytes), Importance: imp})
	}
	for r := 0; r < 4; r++ {
		upsert(&model.Memory{
			Type: model.TypeRule, Title: fmt.Sprintf("fixture-rule-%02d", r+1), Content: fixtureBody(fixtureRuleBytes),
			Importance: 0.900 + 0.010*float64(r), AppliesTo: []string{"**"}, Severity: model.SeverityWarn,
		})
	}

	memberCounts := []int{9, 7, 5, 4, 3}
	now := time.Now().UTC()
	communities := make([]*model.Community, 0, len(memberCounts))
	for i, n := range memberCounts {
		nn := fmt.Sprintf("%02d", i+1)
		communities = append(communities, &model.Community{
			ID: "fixture-" + nn, Project: slug, Scope: model.ScopeProject,
			MembershipHash: "fixture-hash-" + nn, MemberCount: n, Modularity: 0.4,
			Label: "fixture community " + nn, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := ps.SaveCommunitiesTx(ctx, communities, nil, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx: %v", err)
	}

	svc := service.NewMemoryService(ps, store.NewMemoryStore(globalDB), cfg, slug, embed.NopEmbedder{})
	if _, err := svc.SessionEnd(ctx, model.SessionEndRequest{Summary: fixtureBody(fixtureSummarySize)}); err != nil {
		t.Fatalf("SessionEnd: %v", err)
	}
}

// TestSessionStart_OutputBytesCeiling pins the session-start cost: with
// the new default the output carries no Cluster Overviews and no synthesis
// and fits in sessionStartMaxBytes; the communities mode, which keeps
// syntheses, is measured alongside as the difference this spec removes.
func TestSessionStart_OutputBytesCeiling(t *testing.T) {
	if sessionStartMaxBytes > sessionStartHardCeiling {
		t.Fatalf("sessionStartMaxBytes = %d exceeds the hard ceiling %d", sessionStartMaxBytes, sessionStartHardCeiling)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	origProject := flagProject
	flagProject = sessionStartSlug
	t.Cleanup(func() { flagProject = origProject })

	dataDir := t.TempDir()
	seedRepresentativeBase(t, dataDir, sessionStartSlug)

	var defaultLen int
	t.Run("default", func(t *testing.T) {
		out, _ := runSessionStartHook(t, t.TempDir(), dataDir)
		defaultLen = len(out)
		t.Logf("session-start default: %d bytes (ceiling %d)", defaultLen, sessionStartMaxBytes)
		if defaultLen > sessionStartMaxBytes {
			t.Errorf("session-start is %d bytes, ceiling sessionStartMaxBytes = %d", defaultLen, sessionStartMaxBytes)
		}
		for _, banned := range []string{"## Cluster Overviews", "[synthesis]"} {
			if strings.Contains(out, banned) {
				t.Errorf("default output contains %q", banned)
			}
		}
	})

	t.Run("communities", func(t *testing.T) {
		cfgDir := filepath.Join(home, ".mneme")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		cfgPath := filepath.Join(cfgDir, "config.toml")
		if err := os.WriteFile(cfgPath, []byte("[context]\ncontext_packing_mode = \"communities\"\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(cfgPath) })

		out, _ := runSessionStartHook(t, t.TempDir(), dataDir)
		t.Logf("session-start communities: %d bytes (+%d over default)", len(out), len(out)-defaultLen)
		for _, want := range []string{"## Cluster Overviews", "fixture-synthesis-01"} {
			if !strings.Contains(out, want) {
				t.Errorf("communities output lacks %q", want)
			}
		}
		if len(out) <= defaultLen {
			t.Errorf("communities output %d bytes is not larger than default %d", len(out), defaultLen)
		}
	})
}
