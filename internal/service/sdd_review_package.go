// Package service — sdd_review_package.go is SPEC-188's home for the review
// package: the file `mneme spec review-package` writes with the log, stat
// and -U10 diff of a spec's current review range, so the orchestrator hands
// the qa-tester a PATH and the code itself never enters its context (D1);
// and for qa_rounds, the per-stay count of qa rejections the round cap of
// the review-brief skill reads (D2).
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wirvii/mneme/internal/gitrepo"
	"github.com/wirvii/mneme/internal/model"
)

// reviewPackageContextLines is the -U width of the package's diff: enough
// surrounding code to judge a change without opening the file.
const reviewPackageContextLines = 10

// minFenceLen is the shortest Markdown code fence CommonMark accepts.
const minFenceLen = 3

// reviewSHAPattern accepts the abbreviated or full lowercase hex SHAs a
// review range can carry; anything else never reaches a file name.
var reviewSHAPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// reviewPackagePath resolves where the review package of id's from..to
// range lives: the same spec directory specDocPath uses, with a name built
// from both abbreviated SHAs so a new range never overwrites an old one. It
// applies specDocPath's two defences, the id pattern and a filepath.Rel
// check, because the name is dynamic and cannot come from a closed set.
func reviewPackagePath(root, project, id, from, to string) (string, error) {
	if !specIDPattern.MatchString(id) {
		return "", fmt.Errorf("service: review package path: invalid spec id %q", id)
	}
	if !reviewSHAPattern.MatchString(from) || !reviewSHAPattern.MatchString(to) {
		return "", fmt.Errorf("service: review package path: invalid range %q..%q", from, to)
	}
	specDir := filepath.Join(root, strings.ReplaceAll(project, "/", "-"), "specs", id)
	name := "review-package-" + abbreviateSHA(from) + "-" + abbreviateSHA(to) + ".md"
	path := filepath.Join(specDir, name)
	if rel, err := filepath.Rel(specDir, path); err != nil || rel != name {
		return "", fmt.Errorf("service: review package path: resolved path %q escapes spec directory %q", path, specDir)
	}
	return path, nil
}

// fenceFor returns a backtick fence longer than the longest backtick run
// ANYWHERE in content: in a diff every line carries a +, - or space prefix,
// and CommonMark closes a fence indented up to three spaces, so counting
// only at line starts would not protect the block.
func fenceFor(content []byte) string {
	longest, run := 0, 0
	for _, c := range content {
		if c == '`' {
			run++
			longest = max(longest, run)
			continue
		}
		run = 0
	}
	return strings.Repeat("`", max(minFenceLen, longest+1))
}

// writeFencedBlock appends one fenced block with info string lang, holding
// content or "(vacio)" when there is none.
func writeFencedBlock(b *strings.Builder, lang string, content []byte) {
	if len(content) == 0 {
		content = []byte("(vacio)\n")
	}
	if content[len(content)-1] != '\n' {
		content = append(append([]byte{}, content...), '\n')
	}
	fence := fenceFor(content)
	b.WriteString(fence + lang + "\n")
	b.Write(content)
	b.WriteString(fence + "\n")
}

// renderReviewPackage builds the package text (design §1.2). It is pure and
// carries no generation date, so the same range always yields the same
// bytes. mneme's notice appears exactly once: as the "> FRONTERA PERDIDA:"
// header when the frontier was lost, as "Aviso de mneme:" otherwise.
func renderReviewPackage(spec *model.Spec, r model.ReviewRange, log, stat, diff []byte) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Paquete de revision de %s\n\n", spec.ID)
	if r.FrontierLost {
		fmt.Fprintf(&b, "> FRONTERA PERDIDA: %s\n\n", r.Notice)
	}
	fmt.Fprintf(&b, "Spec: %s - %s\n", spec.ID, spec.Title)
	fmt.Fprintf(&b, "Rango: %s..%s\n", r.From, r.To)
	if r.FromKind == "frontier" {
		b.WriteString("Desde: frontera anterior\n")
	} else {
		b.WriteString("Desde: commit base\n")
	}
	if !r.FrontierLost {
		fmt.Fprintf(&b, "Aviso de mneme: %s\n", r.Notice)
	}
	b.WriteString("Generado por `mneme spec review-package`. No lo edites: se reescribe entero.\n\n")

	fmt.Fprintf(&b, "## Commits: git log --oneline %s..%s\n\n", r.From, r.To)
	writeFencedBlock(&b, "text", log)
	fmt.Fprintf(&b, "\n## Archivos: git diff --stat %s..%s\n\n", r.From, r.To)
	writeFencedBlock(&b, "text", stat)
	fmt.Fprintf(&b, "\n## Diff: git diff -U%d %s..%s\n\n", reviewPackageContextLines, r.From, r.To)
	writeFencedBlock(&b, "diff", diff)
	return []byte(b.String())
}

// qaRoundsOf counts the qa -> implementing rows of the current stay: those
// after the latest done -> implementing reopening by (At, ID), or all of
// them when the spec was never reopened (SPEC-188 D2, amended).
func qaRoundsOf(history []*model.SpecHistory) int {
	reopen := latestHistoryRow(history, func(h *model.SpecHistory) bool {
		return h.FromStatus == model.SpecStatusDone && h.ToStatus == model.SpecStatusImplementing
	})
	rounds := 0
	for _, h := range history {
		if h.FromStatus != model.SpecStatusQA || h.ToStatus != model.SpecStatusImplementing {
			continue
		}
		if reopen != nil && !historyRowAfter(h, reopen) {
			continue
		}
		rounds++
	}
	return rounds
}

// historyRowAfter reports whether h comes after ref in (At, ID) order, the
// same order latestHistoryRow uses to break ties between equal timestamps.
func historyRowAfter(h, ref *model.SpecHistory) bool {
	return h.At.After(ref.At) || (h.At.Equal(ref.At) && h.ID > ref.ID)
}

// SpecReviewPackage writes the review package of spec id's current
// review_range (SPEC-188 D1) and returns where it went. Nothing is written
// unless the spec is in qa, the workflow and repository directories are
// configured and the range is available; each of those is an
// ErrReviewPackageUnavailable. It reads the spec directly and changes no
// status, so a frozen spec in qa gets its package too.
func (svc *SDDService) SpecReviewPackage(ctx context.Context, id string) (*model.SpecReviewPackage, error) {
	spec, err := svc.store.GetSpec(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: spec review package: get: %w", err)
	}
	if spec.Status != model.SpecStatusQA {
		return nil, fmt.Errorf("service: spec review package: %s esta en %s; el paquete solo existe mientras la spec esta en qa: %w",
			id, spec.Status, model.ErrReviewPackageUnavailable)
	}
	if svc.config == nil || svc.config.WorkflowDir() == "" {
		return nil, fmt.Errorf("service: spec review package: directorio de workflow sin configurar: %w", model.ErrReviewPackageUnavailable)
	}
	if svc.repoDir == "" {
		return nil, fmt.Errorf("service: spec review package: directorio del repositorio sin configurar: %w", model.ErrReviewPackageUnavailable)
	}
	r := svc.ReviewRange(ctx, spec)
	if !r.Available {
		return nil, fmt.Errorf("service: spec review package: %s: %w", r.Notice, model.ErrReviewPackageUnavailable)
	}

	g := &gitrepo.Git{RepoDir: svc.repoDir}
	log, err := g.LogOneline(r.From, r.To)
	if err != nil {
		return nil, fmt.Errorf("service: spec review package: %w", err)
	}
	stat, err := g.DiffStat(r.From, r.To)
	if err != nil {
		return nil, fmt.Errorf("service: spec review package: %w", err)
	}
	diff, err := g.DiffWithContext(r.From, r.To, reviewPackageContextLines)
	if err != nil {
		return nil, fmt.Errorf("service: spec review package: %w", err)
	}

	path, err := reviewPackagePath(svc.config.WorkflowDir(), spec.Project, spec.ID, r.From, r.To)
	if err != nil {
		return nil, err
	}
	content := renderReviewPackage(spec, r, log, stat, diff)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("service: spec review package: mkdir: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return nil, fmt.Errorf("service: spec review package: write: %w", err)
	}
	return &model.SpecReviewPackage{
		Path:         path,
		SpecID:       spec.ID,
		From:         r.From,
		To:           r.To,
		FromKind:     r.FromKind,
		FrontierLost: r.FrontierLost,
		Notice:       r.Notice,
		Bytes:        len(content),
	}, nil
}
