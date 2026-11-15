package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// thirtyLines renders "L1\n".."L30\n", with line 15 replaced by l15 when
// l15 is not empty.
func thirtyLines(l15 string) string {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		if i == 15 && l15 != "" {
			b.WriteString(l15 + "\n")
			continue
		}
		fmt.Fprintf(&b, "L%d\n", i)
	}
	return b.String()
}

// writeRepoFile writes content to name inside dir, failing the test on error.
func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// packageSection returns the text of a review package between the line
// that starts with header and the next "## " heading (or the end).
func packageSection(t *testing.T, content, header string) string {
	t.Helper()
	idx := strings.Index(content, header)
	if idx < 0 {
		t.Fatalf("package lacks section %q:\n%s", header, content)
	}
	rest := content[idx+len(header):]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		return rest[:next]
	}
	return rest
}

// rejectAC1 rejects spec from qa with a single AC1 finding.
func rejectAC1(t *testing.T, svc *SDDService, ctx context.Context, id string) {
	t.Helper()
	if _, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID: id, Reason: "falla", By: "qa-agent",
		Findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "does not hold"}},
	}); err != nil {
		t.Fatalf("SpecReject: %v", err)
	}
}

// advanceOnce moves spec one step forward, failing the test on error.
func advanceOnce(t *testing.T, svc *SDDService, ctx context.Context, id string) {
	t.Helper()
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: id, By: "backend"}); err != nil {
		t.Fatalf("SpecAdvance: %v", err)
	}
}

// reloadSpec reads spec id back from the store: advanceToImplementing
// returns a spec whose BaseSHA is still empty.
func reloadSpec(t *testing.T, svc *SDDService, ctx context.Context, id string) *model.Spec {
	t.Helper()
	spec, err := svc.store.GetSpec(ctx, id)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	return spec
}

// TestSpecReviewPackage_WritesLogStatDiffOfExactRange is C5: the package
// holds the log, stat and -U10 diff of exactly base..delivered end, named
// after both SHAs, and is byte-identical across two runs.
func TestSpecReviewPackage_WritesLogStatDiffOfExactRange(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "wirvii/mneme")
	ctx := context.Background()
	dir := svc.repoDir

	writeRepoFile(t, dir, "lines.txt", thirtyLines(""))
	commitAllFixture(t, dir, "base: lines")
	writeRepoFile(t, dir, "antes.txt", "antes\n")
	commitAllFixture(t, dir, "antes del rango")
	spec := advanceToImplementing(t, svc, ctx, "review package exact range")
	spec = reloadSpec(t, svc, ctx, spec.ID)
	if spec.BaseSHA == "" {
		t.Fatal("setup: base_sha was not captured")
	}
	writeRepoFile(t, dir, "lines.txt", thirtyLines("L15-cambiada"))
	commitAllFixture(t, dir, "dentro: cambia L15")
	advanceOnce(t, svc, ctx, spec.ID)
	commitOneMore(t, dir, "despues.txt", "despues del extremo")

	pkg, err := svc.SpecReviewPackage(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecReviewPackage: %v", err)
	}
	wantPath := filepath.Join(svc.config.WorkflowDir(), "wirvii-mneme", "specs", spec.ID,
		"review-package-"+abbreviateSHA(spec.BaseSHA)+"-"+abbreviateSHA(pkg.To)+".md")
	if pkg.Path != wantPath {
		t.Errorf("Path = %q, want %q", pkg.Path, wantPath)
	}
	if pkg.From != spec.BaseSHA || pkg.FromKind != "base" || pkg.SpecID != spec.ID {
		t.Errorf("package = %+v, want From=base_sha, FromKind=base", pkg)
	}
	raw, err := os.ReadFile(pkg.Path)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	if pkg.Bytes != len(raw) {
		t.Errorf("Bytes = %d, want %d", pkg.Bytes, len(raw))
	}
	content := string(raw)

	commits := packageSection(t, content, "## Commits: git log --oneline "+pkg.From+".."+pkg.To)
	if got := regexp.MustCompile(`(?m)^[0-9a-f]{8} dentro: cambia L15$`).FindAllString(commits, -1); len(got) != 1 {
		t.Errorf("commits section: want one 8-hex line for the in-range commit, got %q:\n%s", got, commits)
	}
	for _, outside := range []string{"antes del rango", "base: lines", "despues del extremo"} {
		if strings.Contains(commits, outside) {
			t.Errorf("commits section lists %q, outside the range:\n%s", outside, commits)
		}
	}
	stat := packageSection(t, content, "## Archivos: git diff --stat")
	if !strings.Contains(stat, " changed") {
		t.Errorf("stat section lacks the summary line:\n%s", stat)
	}
	diff := packageSection(t, content, "## Diff: git diff -U10")
	for _, p := range []string{`(?m)^ L5$`, `(?m)^ L25$`} {
		if !regexp.MustCompile(p).MatchString(diff) {
			t.Errorf("diff section lacks %s:\n%s", p, diff)
		}
	}
	for _, p := range []string{`(?m)^[ +-]L4$`, `(?m)^[ +-]L26$`} {
		if regexp.MustCompile(p).MatchString(diff) {
			t.Errorf("diff section has %s:\n%s", p, diff)
		}
	}

	again, err := svc.SpecReviewPackage(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecReviewPackage (second run): %v", err)
	}
	raw2, err := os.ReadFile(again.Path)
	if err != nil {
		t.Fatalf("read package (second run): %v", err)
	}
	if again.Path != pkg.Path || !bytes.Equal(raw, raw2) {
		t.Errorf("second run differs: path %q vs %q, equal bytes=%v", again.Path, pkg.Path, bytes.Equal(raw, raw2))
	}
}

// TestSpecReviewPackage_FrontierLostHeader is C6: after a rejection and an
// amend of the rejected end, the header carries mneme's notice once, as
// "> FRONTERA PERDIDA:", and never "Aviso de mneme:".
func TestSpecReviewPackage_FrontierLostHeader(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	dir := svc.repoDir

	spec := advanceToImplementing(t, svc, ctx, "frontier lost header")
	spec = reloadSpec(t, svc, ctx, spec.ID)
	commitOneMore(t, dir, "entrega.txt", "entrega")
	advanceOnce(t, svc, ctx, spec.ID)
	rejectAC1(t, svc, ctx, spec.ID)
	runGitFrontierTest(t, dir, "commit", "--amend", "-m", "entrega reescrita")
	advanceOnce(t, svc, ctx, spec.ID)

	r := svc.ReviewRange(ctx, reloadSpec(t, svc, ctx, spec.ID))
	if !r.FrontierLost {
		t.Fatalf("setup: frontier should be lost after the amend, range = %+v", r)
	}
	pkg, err := svc.SpecReviewPackage(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecReviewPackage: %v", err)
	}
	if !pkg.FrontierLost || pkg.From != spec.BaseSHA {
		t.Errorf("package = %+v, want FrontierLost and From = base %s", pkg, spec.BaseSHA)
	}
	raw, err := os.ReadFile(pkg.Path)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	content := string(raw)
	if !strings.Contains(r.Notice, "ya no es antepasada del extremo entregado") {
		t.Fatalf("setup: notice %q lacks the lost-frontier wording", r.Notice)
	}
	header := "\n> FRONTERA PERDIDA: " + r.Notice + "\n"
	hIdx := strings.Index(content, header)
	sIdx := strings.Index(content, "\nSpec: ")
	if hIdx < 0 || sIdx < 0 || hIdx > sIdx {
		t.Errorf("want the lost-frontier header before the Spec: line:\n%s", content)
	}
	if strings.Contains(content, "Aviso de mneme:") {
		t.Errorf("package repeats the notice as Aviso de mneme:\n%s", content)
	}
}

// TestSpecReviewPackage_ReReviewOnlyFix is C7: after a rejection and a fix
// commit, the new package starts at the rejected end and lists only the fix.
func TestSpecReviewPackage_ReReviewOnlyFix(t *testing.T) {
	svc := newTestSDDServiceWithGitRepo(t, "project")
	ctx := context.Background()
	dir := svc.repoDir

	spec := advanceToImplementing(t, svc, ctx, "re-review only the fix")
	delivered := commitOneMore(t, dir, "entrega.txt", "entrega")
	advanceOnce(t, svc, ctx, spec.ID)
	rejectAC1(t, svc, ctx, spec.ID)
	commitOneMore(t, dir, "arreglo.txt", "arreglo")
	advanceOnce(t, svc, ctx, spec.ID)

	pkg, err := svc.SpecReviewPackage(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecReviewPackage: %v", err)
	}
	if pkg.From != delivered || pkg.FromKind != "frontier" {
		t.Errorf("From = %s (%s), want the rejected end %s (frontier)", pkg.From, pkg.FromKind, delivered)
	}
	raw, err := os.ReadFile(pkg.Path)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	commits := strings.TrimSpace(packageSection(t, string(raw), "## Commits: git log --oneline"))
	lines := regexp.MustCompile(`(?m)^[0-9a-f]{8,} .*$`).FindAllString(commits, -1)
	if len(lines) != 1 || !strings.HasSuffix(lines[0], " arreglo") {
		t.Errorf("commits = %q, want exactly the fix commit", lines)
	}
}

// TestSpecReviewPackage_Unavailable is C8's service half: in every case
// where there is no package to write, the error wraps
// ErrReviewPackageUnavailable and no file appears.
func TestSpecReviewPackage_Unavailable(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) (*SDDService, *model.Spec, string)
		wantMsg string
	}{
		{
			name: "outside qa",
			setup: func(t *testing.T) (*SDDService, *model.Spec, string) {
				svc := newTestSDDServiceWithGitRepo(t, "project")
				spec := advanceToImplementing(t, svc, context.Background(), "outside qa")
				return svc, spec, svc.config.WorkflowDir()
			},
			wantMsg: "el paquete solo existe mientras la spec esta en qa",
		},
		{
			name: "range unavailable",
			setup: func(t *testing.T) (*SDDService, *model.Spec, string) {
				svc := newTestSDDService(t, "project")
				spec := advanceToQA(t, svc, context.Background(), "range unavailable")
				svc.WithRepoDir(newTestGitRepo(t))
				return svc, spec, svc.config.WorkflowDir()
			},
			wantMsg: "el rango a revisar no esta disponible",
		},
		{
			name: "nil config",
			setup: func(t *testing.T) (*SDDService, *model.Spec, string) {
				svc := newTestSDDServiceWithGitRepo(t, "project")
				spec := advanceToQA(t, svc, context.Background(), "nil config")
				root := svc.config.WorkflowDir()
				svc.config = nil
				return svc, spec, root
			},
			wantMsg: "directorio de workflow sin configurar",
		},
		{
			name: "empty workflow dir",
			setup: func(t *testing.T) (*SDDService, *model.Spec, string) {
				svc := newTestSDDServiceWithGitRepo(t, "project")
				spec := advanceToQA(t, svc, context.Background(), "empty workflow dir")
				root := svc.config.WorkflowDir()
				svc.config.Workflow.Dir = ""
				return svc, spec, root
			},
			wantMsg: "directorio de workflow sin configurar",
		},
		{
			name: "empty repo dir",
			setup: func(t *testing.T) (*SDDService, *model.Spec, string) {
				svc := newTestSDDServiceWithGitRepo(t, "project")
				spec := advanceToQA(t, svc, context.Background(), "empty repo dir")
				svc.repoDir = ""
				return svc, spec, svc.config.WorkflowDir()
			},
			wantMsg: "directorio del repositorio sin configurar",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, spec, root := tt.setup(t)
			pkg, err := svc.SpecReviewPackage(context.Background(), spec.ID)
			if !errors.Is(err, model.ErrReviewPackageUnavailable) {
				t.Fatalf("err = %v, want ErrReviewPackageUnavailable", err)
			}
			if pkg != nil {
				t.Errorf("package = %+v, want nil", pkg)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err, tt.wantMsg)
			}
			matches, globErr := filepath.Glob(filepath.Join(root, "project", "specs", spec.ID, "review-package-*.md"))
			if globErr != nil {
				t.Fatalf("glob: %v", globErr)
			}
			if len(matches) != 0 {
				t.Errorf("files written: %v", matches)
			}
		})
	}
}

// TestRenderReviewPackage_FenceLongerThanContent is C8: the fence of each
// block outgrows the longest backtick run anywhere in its content, and an
// empty section is written as "(vacio)".
func TestRenderReviewPackage_FenceLongerThanContent(t *testing.T) {
	spec := &model.Spec{ID: "SPEC-001", Title: "fence"}
	r := model.ReviewRange{Available: true, From: strings.Repeat("a", 40), To: strings.Repeat("b", 40), FromKind: "base", Notice: "aviso"}
	diff := []byte(" ```\n+x````y\n")
	out := string(renderReviewPackage(spec, r, []byte("abcdef12 commit\n"), []byte(" f | 1 +\n 1 file changed\n"), diff))

	five := strings.Repeat("`", 5)
	if !strings.Contains(out, "\n"+five+"diff\n ```\n+x````y\n"+five+"\n") {
		t.Errorf("diff block is not fenced with five backticks:\n%s", out)
	}
	if !strings.Contains(out, "\n```text\nabcdef12 commit\n```\n") {
		t.Errorf("log block is not fenced with three backticks:\n%s", out)
	}
	if !strings.Contains(out, "\n```text\n f | 1 +\n 1 file changed\n```\n") {
		t.Errorf("stat block is not fenced with three backticks:\n%s", out)
	}
	if !strings.Contains(out, "Aviso de mneme: aviso\n") || strings.Contains(out, "FRONTERA PERDIDA") {
		t.Errorf("header notice wrong:\n%s", out)
	}

	empty := string(renderReviewPackage(spec, r, nil, nil, nil))
	if strings.Count(empty, "\n(vacio)\n") != 3 {
		t.Errorf("want three (vacio) sections:\n%s", empty)
	}
}

// TestReviewPackagePath_RejectsHostileInput is C8: a hostile id or a
// non-hex SHA never produces a path.
func TestReviewPackagePath_RejectsHostileInput(t *testing.T) {
	root := t.TempDir()
	from, to := strings.Repeat("a", 40), strings.Repeat("b", 40)
	tests := []struct {
		name     string
		id       string
		from, to string
	}{
		{"parent id", "../SPEC-001", from, to},
		{"nested id", "SPEC-001/../x", from, to},
		{"non-hex from", "SPEC-001", "zz", to},
		{"short to", "SPEC-001", from, "abc"},
		{"dotdot to", "SPEC-001", from, "../" + to},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if p, err := reviewPackagePath(root, "o/p", tt.id, tt.from, tt.to); err == nil {
				t.Errorf("reviewPackagePath = %q, want error", p)
			}
		})
	}
	got, err := reviewPackagePath(root, "o/p", "SPEC-001", from, to)
	if err != nil {
		t.Fatalf("valid input: %v", err)
	}
	want := filepath.Join(root, "o-p", "specs", "SPEC-001", "review-package-aaaaaaaa-bbbbbbbb.md")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

// TestQARoundsOf_NoHistory covers the pure counter on an empty history.
func TestQARoundsOf_NoHistory(t *testing.T) {
	if got := qaRoundsOf(nil); got != 0 {
		t.Errorf("qaRoundsOf(nil) = %d, want 0", got)
	}
}
