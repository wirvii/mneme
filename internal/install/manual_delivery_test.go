package install

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func requireDeliveryAnchors(t *testing.T, surface string, text string, anchors []string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, anchor := range anchors {
		if !strings.Contains(lower, strings.ToLower(anchor)) {
			t.Errorf("%s does not explain %q", surface, anchor)
		}
	}
}

func readDeliveryDocument(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestDeliveryWorkflowReferenceIsCompleteAndHonest(t *testing.T) {
	text := readDeliveryDocument(t, "docs/delivery-workflow.md")
	requireDeliveryAnchors(t, "docs/delivery-workflow.md", text, []string{
		"workflow", "organic", "sdd", "lane", "trivial", "standard", "método", "tdd",
		"profundidad", "contrato", "revisión", "huella", "contract_violation", "regression",
		"architecture_violation", "discovery", "improvement", "work_begin", "work_get", "work_lock",
		"work_amend", "work_review", "work_verify", "work_complete", "work_resume", "work_metrics",
		"| `work_metrics` |",
		"mneme config show workflow", "engine = \"legacy\"", "configuración personal", "todo el host",
		"Comprueba la resolución efectiva", "partial", "not_started", "fase 10",
	})
	for _, forbidden := range []string{
		"borrar la base", "borrar .mneme/sdd/work", "ahorro de", "dinero ahorrado", "tokens ahorrados",
		"deep_quality = \"always\" ejecuta automáticamente", "deep_quality = \"always\" automatically runs",
		"instalar mneme activa delivery_v2",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("docs/delivery-workflow.md contains forbidden claim %q", forbidden)
		}
	}
}

func TestDeliveryOperatingManualsShareOperationalRules(t *testing.T) {
	common := []string{
		"mneme config show workflow", "legacy", "delivery_v2", "organic", "sdd", "standard", "tdd", "manual", "always",
		"work_begin", "work_get", "work_lock", "work_amend", "work_review", "work_verify", "work_complete", "work_resume", "work_metrics",
		"coordinator", "qa-tester", "fail closed", "broad review", "one correction", "targeted review", "parallel execution cycles",
		"engine = \"legacy\"", "deep_quality", "quality verify", "docs/delivery-workflow.md", "spec_advance",
	}
	manuals := map[string]string{
		"claude-code": operatingManual(),
		"codex":       operatingManualCodex(),
	}
	for name, text := range manuals {
		t.Run(name, func(t *testing.T) {
			requireDeliveryAnchors(t, name+" operating manual", text, common)
			lower := strings.ToLower(text)
			for _, forbidden := range []string{
				"work_verify completa", "work_verify completes", "targeted review -> correction", "targeted review → correction",
				"revisión dirigida → corrección", "deep_quality = \"always\" automatically runs", "ahorro de", "dinero ahorrado", "tokens ahorrados",
			} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("%s operating manual contains forbidden claim %q", name, forbidden)
				}
			}
		})
	}
	if len(operatingManualCodex()) >= 32*1024 {
		t.Fatalf("codex operating manual is %d bytes, must remain below 32 KiB", len(operatingManualCodex()))
	}
}

func TestDeliveryTransportAndHTTPBoundariesAreExplicit(t *testing.T) {
	transport := readDeliveryDocument(t, "docs/sdd-git-native.md")
	requireDeliveryAnchors(t, "docs/sdd-git-native.md", transport, []string{
		".mneme/sdd/work/WORK-###.md", "UUIDv7", "do **not** travel", "complete", "partial", "not_started",
		"does not create", ".mneme/sdd/.mneme-sdd", "does not activate delivery-v2",
	})
	for _, forbidden := range []string{"certificates travel", "engine creates the marker"} {
		if strings.Contains(strings.ToLower(transport), forbidden) {
			t.Errorf("docs/sdd-git-native.md contains false transport claim %q", forbidden)
		}
	}

	public := map[string][]string{
		"README.md":            {"10 HTTP endpoints do not", "delivery-v2"},
		"docs/API.md":          {"HTTP does not expose SDD or delivery-v2", "10 REST endpoints"},
		"docs/ARCHITECTURE.md": {"HTTP gap", "no SDD or delivery-v2 WORK endpoints", "10 route registrations"},
	}
	for path, anchors := range public {
		requireDeliveryAnchors(t, path, readDeliveryDocument(t, path), anchors)
	}
}

func TestDeliveryManualInstallationIsIdempotentAndPreservesWorkflowConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".mneme", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	wantConfig := []byte("[workflow]\nengine = \"legacy\"\nowner_key = \"keep-me\"\n")
	if err := os.WriteFile(configPath, wantConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	agents := map[string]*Agent{
		"claude-code": ClaudeCode("/usr/local/bin/mneme"),
		"codex":       Codex("/usr/local/bin/mneme"),
	}
	for name, agent := range agents {
		t.Run(name, func(t *testing.T) {
			if err := InjectManual(agent); err != nil {
				t.Fatalf("first InjectManual: %v", err)
			}
			path, _, err := agent.Manual()
			if err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			requireDeliveryAnchors(t, path, string(first), []string{"delivery_v2", "work_begin", "work_review", "work_complete"})
			if err := InjectManual(agent); err != nil {
				t.Fatalf("second InjectManual: %v", err)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("second manual installation changed %s", path)
			}
		})
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotConfig, wantConfig) {
		t.Fatalf("manual installation changed workflow config:\ngot: %s\nwant: %s", gotConfig, wantConfig)
	}
}

// manualMentions reports whether text mentions name at a word boundary — not
// as a substring of a longer token (SPEC-141 §5.1: "grill-me" must not match
// inside a hypothetical "grill-me-fuerte", nor the reverse).
func manualMentions(text, name string) bool {
	re := regexp.MustCompile(`(?:^|[^a-z0-9-])` + regexp.QuoteMeta(name) + `(?:[^a-z0-9-]|$)`)
	return re.MatchString(text)
}

// TestOperatingManuals_NameOnlyDeliveredTools is G3 (SPEC-141 §5.1, D14): for
// every name that is either a bundled skill or an embedded command asset, if
// a manual mentions that name by word boundary, the runtime that manual
// governs must actually deliver it — a skill (both runtimes) or an
// installed slash command (Claude Code only, since Codex never installs
// commands). This is the guardian BL-228 exists to make permanent: the day
// the operating manual names a tool again without the install actually
// providing it, this test fails naming exactly which manual and which name.
//
// D14: this test was first written and run RED against the tree that
// predates SPEC-141 (grill-me named in both manuals, delivered by neither) —
// see changes.md for that transcript.
func TestOperatingManuals_NameOnlyDeliveredTools(t *testing.T) {
	skillNames, err := BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}
	assetFiles, err := builtinCommands.ReadDir("assets/commands")
	if err != nil {
		t.Fatalf("read assets/commands: %v", err)
	}

	nameSet := map[string]bool{}
	for _, n := range skillNames {
		nameSet[n] = true
	}
	for _, f := range assetFiles {
		nameSet[strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))] = true
	}

	claudeCmds, err := ClaudeCode("/usr/local/bin/mneme").Commands()
	if err != nil {
		t.Fatalf("ClaudeCode Commands: %v", err)
	}
	claudeDelivered := map[string]bool{}
	for _, n := range skillNames {
		claudeDelivered[n] = true
	}
	for _, c := range claudeCmds {
		base := filepath.Base(c.Path)
		claudeDelivered[strings.TrimSuffix(base, filepath.Ext(base))] = true
	}

	// Codex never installs slash commands (Commands is nil for Codex) — only
	// bundled skills travel there.
	codexDelivered := map[string]bool{}
	for _, n := range skillNames {
		codexDelivered[n] = true
	}

	claudeManual := operatingManual()
	codexManual := operatingManualCodex()

	for name := range nameSet {
		if manualMentions(claudeManual, name) && !claudeDelivered[name] {
			t.Errorf("claude-code operating manual mentions %q but claude-code does not deliver it", name)
		}
		if manualMentions(codexManual, name) && !codexDelivered[name] {
			t.Errorf("codex operating manual mentions %q but codex does not deliver it", name)
		}
	}
}
