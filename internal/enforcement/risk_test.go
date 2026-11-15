package enforcement

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/wirvii/mneme/internal/risk"
)

func TestRiskWriteForms(t *testing.T) {
	windows := runtime.GOOS == "windows"
	tests := []struct {
		cmd  string
		want []RiskWriteForm
		only bool // the row runs only when the host is Windows
	}{
		{cmd: "echo x > f", want: []RiskWriteForm{{Form: ">", Operand: "f", Truncates: true}}},
		{cmd: "echo x >> f", want: []RiskWriteForm{{Form: ">>", Operand: "f"}}},
		{cmd: "echo x >| f", want: []RiskWriteForm{{Form: ">|", Operand: "f", Truncates: true}}},
		{cmd: "tee f", want: []RiskWriteForm{{Form: "tee", Operand: "f"}}},
		{cmd: "cp a b", want: []RiskWriteForm{{Form: "cp", Operand: "b"}}},
		{cmd: "mv x_test.go y_test.go", want: []RiskWriteForm{
			{Form: "mv", Operand: "y_test.go"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "y_test.go"},
		}},
		{cmd: "mv x_test.go internal/other/", want: []RiskWriteForm{
			{Form: "mv", Operand: "internal/other/"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "internal/other/x_test.go"},
		}},
		{cmd: "mv -t internal/other x_test.go", want: []RiskWriteForm{
			{Form: "mv", Operand: "internal/other"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "internal/other/x_test.go"},
		}},
		{cmd: "mv x_test.go x.go", want: []RiskWriteForm{
			{Form: "mv", Operand: "x.go"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "x.go", Removes: true},
		}},
		{cmd: "mv x_test.go /tmp/", want: []RiskWriteForm{
			{Form: "mv", Operand: "/tmp/"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "/tmp/x_test.go", Removes: true},
		}},
		{cmd: "mv x_test.go ~/", want: []RiskWriteForm{
			{Form: "mv", Operand: "~/"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "~/x_test.go", Removes: true},
		}},
		{cmd: "mv x_test.go $HOME/", want: []RiskWriteForm{
			{Form: "mv", Operand: "$HOME/"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "$HOME/x_test.go", Removes: true},
		}},
		{cmd: "mv x_test.go '$USERPROFILE/'", want: []RiskWriteForm{
			{Form: "mv", Operand: "$USERPROFILE/"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "$USERPROFILE/x_test.go", Removes: windows},
		}},
		{cmd: "mv x_test.go ../y_test.go", want: []RiskWriteForm{
			{Form: "mv", Operand: "../y_test.go"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "../y_test.go", Removes: true},
		}},
		{cmd: "mv x_test.go internal/other", want: []RiskWriteForm{
			{Form: "mv", Operand: "internal/other"},
			{Form: "mv", Operand: "x_test.go", MovedTo: "internal/other", Removes: true},
		}},
		{cmd: `mv x_test.go 'C:\tmp\'`, only: true, want: []RiskWriteForm{
			{Form: "mv", Operand: `C:\tmp\`},
			{Form: "mv", Operand: "x_test.go", MovedTo: `C:\tmp\x_test.go`, Removes: true},
		}},
		{cmd: "rm -r d", want: []RiskWriteForm{{Form: "rm", Operand: "d", Removes: true}}},
		{cmd: "git rm f", want: []RiskWriteForm{{Form: "git rm", Operand: "f", Removes: true}}},
		{cmd: "git -C x rm f", want: []RiskWriteForm{{Form: "git rm", Operand: "f", Removes: true}}},
		{cmd: "git rm --cached f", want: []RiskWriteForm{{Form: "git rm", Operand: "f"}}},
		{cmd: "sed -i 's/a/b/' f", want: []RiskWriteForm{{Form: "sed -i", Operand: "f", Script: "s/a/b/"}}},
		{cmd: "sed -i -e 's/a/b/' f", want: []RiskWriteForm{{Form: "sed -i", Operand: "f", Script: "s/a/b/"}}},
		{cmd: "perl -pi -e 's/a/b/' f", want: []RiskWriteForm{{Form: "perl -i", Operand: "f", Script: "s/a/b/"}}},
		{cmd: "dd of=f", want: []RiskWriteForm{{Form: "dd", Operand: "f"}}},
		{cmd: `python3 -c "open('a/f','w')"`, want: []RiskWriteForm{{Form: "python -c", Operand: "a/f"}}},
		{cmd: `python3 -c "print(1)"`},
		{cmd: `bash -c "echo x > f"`, want: []RiskWriteForm{{Form: ">", Operand: "f", Truncates: true}}},
		{cmd: "cat a | tee b", want: []RiskWriteForm{{Form: "tee", Operand: "b"}}},
		{cmd: "cat a 2>&1"},
		{cmd: `echo "`},
		{cmd: "go test ./..."},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if tt.only && !windows {
				t.Skipf("Windows-only row on %s", runtime.GOOS)
			}
			if got := RiskWriteForms(tt.cmd); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RiskWriteForms(%q) =\n %+v\nwant\n %+v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestRiskWriteForms_Dependencies(t *testing.T) {
	forms := map[string]string{
		"go get m":                              goManifest,
		"go get -u m":                           goManifest,
		"go mod edit -require=m@v1":             goManifest,
		"go mod edit -droprequire m":            goManifest,
		"go mod edit -replace=a=b":              goManifest,
		"npm install p":                         nodeManifest,
		"npm i p":                               nodeManifest,
		"npm --prefix web install p":            nodeManifest,
		"npm install --registry https://r p":    nodeManifest,
		"npm install -w packages/a p":           nodeManifest,
		"npm update":                            nodeManifest,
		"npm upgrade p":                         nodeManifest,
		"npm uninstall p":                       nodeManifest,
		"pnpm add p":                            nodeManifest,
		"pnpm i p":                              nodeManifest,
		"pnpm -C web add p":                     nodeManifest,
		"pnpm --filter web add p":               nodeManifest,
		"pnpm -F web add p":                     nodeManifest,
		"pnpm uninstall p":                      nodeManifest,
		"yarn add p":                            nodeManifest,
		"yarn --cwd web add p":                  nodeManifest,
		"yarn workspace web add p":              nodeManifest,
		"yarn upgrade":                          nodeManifest,
		"bun remove p":                          nodeManifest,
		"bun i p":                               nodeManifest,
		"bun add --filter web p":                nodeManifest,
		"pip install p":                         "",
		"pip install -i https://x/simple p":     "",
		"pip install --target vendor p":         "",
		"pip3 uninstall p":                      "",
		"cargo add p":                           "",
		"cargo add --registry r p":              "",
		"cargo remove p":                        "",
		"cargo update":                          "",
		"poetry add p":                          "",
		"poetry add --source s p":               "",
		"poetry update":                         "",
		"env CI=1 npm install p":                nodeManifest,
		`bash -c "go get example.com/m@latest"`: goManifest,
	}
	for cmd, manifest := range forms {
		t.Run(cmd, func(t *testing.T) {
			got := RiskWriteForms(cmd)
			if len(got) != 1 {
				t.Fatalf("RiskWriteForms(%q) = %+v, want one dependency form", cmd, got)
			}
			f := got[0]
			if f.Implicit != risk.ItemDelivery || !f.Synthetic || f.Operand != manifest || f.Form == "" {
				t.Errorf("RiskWriteForms(%q) = %+v, want Implicit=5 Synthetic Operand=%q", cmd, f, manifest)
			}
		})
	}
	none := []string{
		"go get", "go mod edit -fmt", "npm ci", "npm install", "npm --prefix web install",
		"npm install --registry https://r", "npm install -w packages/a", "npm install --omit dev",
		"npm install -g p", "pnpm install", "pnpm install --filter web", "pnpm -C web install",
		"pnpm add -g p", "yarn", "yarn --cwd web install", "yarn global add p", "yarn workspace web",
		"bun add --global p", "bun install --filter web", "pip install -r r.txt",
		"pip install -c c.txt -r r.txt", "pip install -i https://x/simple -r r.txt",
		"pip install --index-url https://x -r r.txt", "pip install --no-binary :all: -r r.txt",
		"pip install -e .", "pip install --editable ./pkg", "pip install .", "cargo install rg",
		"cargo uninstall rg", "poetry install --with dev", "go mod tidy", "go mod download",
		"go mod verify", "go build ./...", "go test ./...", "cargo build", "npm",
	}
	for _, cmd := range none {
		t.Run("none/"+cmd, func(t *testing.T) {
			if got := RiskWriteForms(cmd); got != nil {
				t.Errorf("RiskWriteForms(%q) = %+v, want nil", cmd, got)
			}
		})
	}
}

// declares answers yes for the listed items, as SPEC-7's plan.
func declares(items ...risk.Item) DeclaredFunc {
	return func(item risk.Item, _ string, _ bool) (bool, string, string) {
		for _, i := range items {
			if i == item {
				return true, "SPEC-7", "plan"
			}
		}
		return false, "", ""
	}
}

func TestEvaluateRisk(t *testing.T) {
	orch := RiskSubject{Orchestrator: true}
	inline := RiskSubject{Orchestrator: true, Inline: true}
	impl := RiskSubject{Subagent: true, Implementer: true}
	qa := RiskSubject{Subagent: true}
	migration := RiskFacts{Tool: "Write", Form: "Write", Rel: "internal/db/migrations/024.sql", Items: []risk.Item{risk.ItemIrreversibleData}}
	env := RiskFacts{Tool: "Write", Form: "Write", Rel: ".env", Items: []risk.Item{risk.ItemSecurity}}
	skip := RiskFacts{Tool: "Edit", Form: "Edit", Rel: "tools/x_test.go", Test: &TestWatch{Language: TestGo, MarkersAdded: []string{"t.Skip("}}}

	type want struct {
		block  bool
		item   risk.Item
		reason string
		detail string
		source string
	}
	tests := []struct {
		name     string
		subject  RiskSubject
		facts    RiskFacts
		declared DeclaredFunc
		want     []want
	}{
		{"row02_orchestrator_fallback_declared", orch, env, declares(2), []want{{false, 2, RiskAllowedByDeclaration, "Write", "plan"}}},
		{"row03_orchestrator_fallback_undeclared", orch, env, declares(), []want{{true, 2, RiskDenied, "Write", ""}}},
		{"row04_orchestrator_test_marker", orch, skip, declares(), []want{{true, 6, RiskDenied, "t.Skip(", ""}}},
		{"row04_orchestrator_test_marker_declared", orch, skip, declares(6), []want{{false, 6, RiskAllowedByDeclaration, "t.Skip(", "plan"}}},
		{"row05_inline_blocks_even_if_declared", inline, skip, declares(6), []want{{true, 6, RiskDenied, "t.Skip(", ""}}},
		{"row05_inline_path_item", inline, env, declares(2), []want{{true, 2, RiskDenied, "Write", ""}}},
		{"row09_implementer_path_undeclared", impl, migration, declares(), []want{{true, 1, RiskDenied, "Write", ""}}},
		{"row10_implementer_every_item_declared", impl,
			RiskFacts{Form: "Write", Rel: "deploy/.env", Items: []risk.Item{risk.ItemDelivery, risk.ItemSecurity}},
			declares(2, 5), []want{{false, 2, RiskAllowedByDeclaration, "Write", "plan"}, {false, 5, RiskAllowedByDeclaration, "Write", "plan"}}},
		{"row11_implementer_marker", impl, skip, declares(), []want{{true, 6, RiskDenied, "t.Skip(", ""}}},
		{"row12_implementer_loses_tests", impl,
			RiskFacts{Form: "Write", Rel: "x_test.go", Test: &TestWatch{LosesTests: true}},
			declares(), []want{{true, 6, RiskDenied, riskDetailLosesTests, ""}}},
		{"row13_implementer_item6_declared", impl,
			RiskFacts{Form: "Edit", Rel: "x_test.go", Test: &TestWatch{LosesTests: true}},
			declares(6), []want{{false, 6, RiskAllowedByDeclaration, riskDetailLosesTests, "plan"}}},
		{"row14_bash_path_form", qa,
			RiskFacts{Tool: "Bash", Form: ">>", Rel: "internal/db/migrations/024.sql", Items: []risk.Item{risk.ItemIrreversibleData}},
			declares(), []want{{true, 1, RiskDenied, ">>", ""}}},
		{"row15_bash_removed", qa,
			RiskFacts{Tool: "Bash", Form: "rm", Rel: "x_test.go", Test: &TestWatch{Removed: true}},
			declares(), []want{{true, 6, RiskDenied, riskDetailRemoved, ""}}},
		{"row16_bash_harmless", qa,
			RiskFacts{Tool: "Bash", Form: "mv", Rel: "y_test.go", Test: &TestWatch{}},
			declares(), nil},
		{"row19_no_item_no_test", impl, RiskFacts{Form: "Write", Rel: "internal/x.go"}, nil, nil},
		{"synthetic_item5_once", qa,
			RiskFacts{Tool: "Bash", Form: "go get", Rel: "go.mod", Synthetic: true, Items: []risk.Item{risk.ItemDelivery, risk.ItemDelivery}},
			declares(5), []want{{false, 5, RiskAllowedByDeclaration, "go get", "plan"}}},
		{"first_declared_second_blocks", impl,
			RiskFacts{Form: "Write", Rel: "deploy/.env", Items: []risk.Item{risk.ItemDelivery, risk.ItemSecurity}},
			declares(2), []want{{false, 2, RiskAllowedByDeclaration, "Write", "plan"}, {true, 5, RiskDenied, "Write", ""}}},
		{"path_block_stops_before_test", impl,
			RiskFacts{Form: "Write", Rel: "migrations/a_test.go", Items: []risk.Item{risk.ItemIrreversibleData}, Test: &TestWatch{LosesTests: true}},
			declares(), []want{{true, 1, RiskDenied, "Write", ""}}},
		{"truncated_detail", qa,
			RiskFacts{Tool: "Bash", Form: ">", Rel: "x_test.go", Test: &TestWatch{Removed: true, Truncated: true}},
			declares(), []want{{true, 6, RiskDenied, riskDetailTruncated, ""}}},
		{"nil_declared_blocks", impl, migration, nil, []want{{true, 1, RiskDenied, "Write", ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateRisk(tt.subject, tt.facts, tt.declared)
			if len(got) != len(tt.want) {
				t.Fatalf("EvaluateRisk = %+v, want %d verdicts", got, len(tt.want))
			}
			for i, w := range tt.want {
				v := got[i]
				if v.Block != w.block || v.Item != w.item || v.Reason != w.reason || v.Detail != w.detail || v.Source != w.source {
					t.Errorf("verdict %d = %+v, want %+v", i, v, w)
				}
				if w.source != "" && v.Spec != "SPEC-7" {
					t.Errorf("verdict %d Spec = %q, want SPEC-7", i, v.Spec)
				}
			}
		})
	}
}

func TestIsRiskExempt(t *testing.T) {
	pc := PathContext{Home: "/home/u", GOOS: "linux"}
	data := NewDataDirContext(SDDContext{PathContext: pc, CWD: "/home/u/repo"}, []string{"/home/u/.mneme"})
	orch := RiskSubject{Orchestrator: true}
	sub := RiskSubject{Subagent: true, Implementer: true}
	tests := []struct {
		target  string
		subject RiskSubject
		want    bool
	}{
		{".claude/worktrees/x/internal/db/migrations/024.sql", orch, true},
		{"CLAUDE.md", orch, true},
		{"internal/db/migrations/024.sql", orch, false},
		{".claude/worktrees/x/internal/db/migrations/024.sql", sub, false},
		{"/home/u/repo/.claude/worktrees/x/internal/db/migrations/024.sql", sub, false},
		{"/tmp/a.sql", sub, true},
		{"/private/tmp/a.sql", sub, true},
		{"/tmp/../etc/a.sql", sub, false},
		{"tmp/a.sql", sub, false},
		{"/home/u/.mneme/projects/s.db", sub, true},
		{"~/.mneme/config.toml", sub, true},
		{"CLAUDE.md", sub, false},
	}
	for _, tt := range tests {
		if got := IsRiskExempt(tt.target, tt.subject, pc, data); got != tt.want {
			t.Errorf("IsRiskExempt(%q, %+v) = %v, want %v", tt.target, tt.subject, got, tt.want)
		}
	}
	win := PathContext{Home: `C:\Users\u`, TempDir: `C:\Users\u\AppData\Local\Temp`, GOOS: "windows"}
	if !IsRiskExempt(`C:\Users\u\AppData\Local\Temp\a.sql`, sub, win, DataDirContext{}) {
		t.Error("[windows] TempDir must be scratch for a subagent")
	}
}
