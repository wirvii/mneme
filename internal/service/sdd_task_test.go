package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// newTaskSpec creates a real standard spec and returns it with the path its
// tarea.md resolves to.
func newTaskSpec(t *testing.T) (*SDDService, *model.Spec, string) {
	t.Helper()
	svc, workflowDir := newTestSDDServiceWithWorkflowDir(t, "wirvii/mneme")
	spec, err := svc.SpecNew(context.Background(), model.SpecNewRequest{Title: "Mi spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	return svc, spec, filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "tarea.md")
}

func writeTask(t *testing.T, svc *SDDService, id string, s model.TaskSection, content string) *model.SpecDocWriteResponse {
	t.Helper()
	resp, err := svc.SpecDocWrite(context.Background(), model.SpecDocWriteRequest{
		ID: id, Kind: model.SpecDocKindTask, Section: s, Content: content,
	})
	if err != nil {
		t.Fatalf("SpecDocWrite(task, %s): %v", s, err)
	}
	return resp
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestSpecDocWrite_Task_CreatesSkeleton covers criterion "skeleton".
func TestSpecDocWrite_Task_CreatesSkeleton(t *testing.T) {
	svc, spec, path := newTaskSpec(t)

	resp := writeTask(t, svc, spec.ID, model.TaskSectionDelivery, "Hecho.")
	if !resp.Created || resp.Section != "entrega" || resp.Path != path || resp.Bytes != len("Hecho.") {
		t.Errorf("response = %+v", resp)
	}
	doc := readFile(t, path)

	want := tareaVersionLine + "\n# " + spec.ID + " — Mi spec\n"
	if !strings.HasPrefix(doc, want) {
		t.Errorf("doc does not start with version line and title:\n%s", doc)
	}
	last := -1
	for _, s := range model.TaskSections {
		i := strings.Index(doc, taskStartMarker(s))
		if i <= last {
			t.Errorf("section %q out of order or missing", s)
		}
		last = i
		if !strings.Contains(doc, taskEndMarker(s)) {
			t.Errorf("section %q end marker missing", s)
		}
	}
	if !strings.Contains(doc, "## Entrega\n\nHecho.\n") {
		t.Errorf("entrega content missing:\n%s", doc)
	}
	for _, h := range []string{"## Qué y por qué\n" + taskEndMarker(model.TaskSectionWhat), "## Revisión\n" + taskEndMarker(model.TaskSectionReview)} {
		if !strings.Contains(doc, h) {
			t.Errorf("empty region %q expected", h)
		}
	}
	if resp := writeTask(t, svc, spec.ID, model.TaskSectionPlan, "p"); resp.Created {
		t.Error("second write must not report Created")
	}
}

// TestSpecDocWrite_Task_ReplacesOnlyItsSection covers criterion "isolation".
func TestSpecDocWrite_Task_ReplacesOnlyItsSection(t *testing.T) {
	svc, spec, path := newTaskSpec(t)

	writeTask(t, svc, spec.ID, model.TaskSectionDesign, "diseno v1")
	writeTask(t, svc, spec.ID, model.TaskSectionDelivery, "entrega intacta")
	before := readFile(t, path)
	writeTask(t, svc, spec.ID, model.TaskSectionDesign, "## Diseño\n\ndiseno v2")
	after := readFile(t, path)

	want := strings.Replace(before, "diseno v1", "diseno v2", 1)
	if after != want {
		t.Errorf("only the design region may change.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if strings.Count(after, "## Diseño") != 1 {
		t.Errorf("duplicate heading kept:\n%s", after)
	}
}

// TestSpecDocWrite_Task_Errors covers criterion "errors" at service level.
func TestSpecDocWrite_Task_Errors(t *testing.T) {
	svc, spec, path := newTaskSpec(t)
	writeTask(t, svc, spec.ID, model.TaskSectionPlan, "x")

	tests := []struct {
		name string
		req  model.SpecDocWriteRequest
		want error
	}{
		{"missing section", model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindTask, Content: "x"}, model.ErrTaskSectionRequired},
		{"unknown section", model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindTask, Section: "bogus", Content: "x"}, model.ErrUnknownTaskSection},
		{"section with spec kind", model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindSpec, Section: model.TaskSectionPlan, Content: "x"}, model.ErrSectionNotApplicable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.SpecDocWrite(context.Background(), tc.req); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}

	good := readFile(t, path)
	malformed := map[string]string{
		"missing":    strings.Replace(good, taskStartMarker(model.TaskSectionPlan), "", 1),
		"duplicated": good + "\n" + taskEndMarker(model.TaskSectionReview) + "\n",
	}
	for name, doc := range malformed {
		t.Run("malformed "+name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := svc.SpecDocWrite(context.Background(), model.SpecDocWriteRequest{
				ID: spec.ID, Kind: model.SpecDocKindTask, Section: model.TaskSectionDelivery, Content: "y",
			})
			if !errors.Is(err, model.ErrTaskDocMalformed) {
				t.Errorf("err = %v, want ErrTaskDocMalformed", err)
			}
			if got := readFile(t, path); got != doc {
				t.Error("malformed file must stay byte for byte unchanged")
			}
		})
	}
}

// TestSpecDocWrite_LegacyKindsUnchanged covers criterion "legacy-kinds".
func TestSpecDocWrite_LegacyKindsUnchanged(t *testing.T) {
	tests := []struct {
		kind     model.SpecDocKind
		filename string
		content  string
	}{
		{model.SpecDocKindSpec, "spec.md", "# spec\n"},
		{model.SpecDocKindPlan, "plan.md", "# plan\n"},
		{model.SpecDocKindQAReport, "qa-report.md", "# qa\n"},
		{model.SpecDocKindChanges, "changes.md", "# changes\n"},
		{model.SpecDocKindCriteria, "criteria.toml", validCriteriaTOML},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			svc, spec, taskPath := newTaskSpec(t)
			resp, err := svc.SpecDocWrite(context.Background(), model.SpecDocWriteRequest{ID: spec.ID, Kind: tc.kind, Content: tc.content})
			if err != nil {
				t.Fatalf("SpecDocWrite: %v", err)
			}
			if filepath.Base(resp.Path) != tc.filename || resp.Section != "" {
				t.Errorf("response = %+v", resp)
			}
			if got := readFile(t, resp.Path); got != tc.content {
				t.Errorf("content = %q, want verbatim %q", got, tc.content)
			}
			if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
				t.Error("legacy kind must not create tarea.md")
			}
		})
	}
}

// TestSpliceTaskSection exercises the pure splice over the skeleton.
func TestSpliceTaskSection(t *testing.T) {
	skel := renderTaskSkeleton("SPEC-001", "T")
	tests := []struct {
		name    string
		section model.TaskSection
		content string
		want    string
	}{
		{"plain", model.TaskSectionPlan, "a\nb", "## Plan\n\na\nb\n" + taskEndMarker(model.TaskSectionPlan)},
		{"surrounding newlines trimmed", model.TaskSectionPlan, "\n\na\n\n", "## Plan\n\na\n" + taskEndMarker(model.TaskSectionPlan)},
		{"duplicate heading dropped", model.TaskSectionPlan, "## Plan\n\na", "## Plan\n\na\n" + taskEndMarker(model.TaskSectionPlan)},
		{"heading only", model.TaskSectionPlan, "## Plan", "## Plan\n" + taskEndMarker(model.TaskSectionPlan)},
		{"empty clears", model.TaskSectionPlan, "", "## Plan\n" + taskEndMarker(model.TaskSectionPlan)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := spliceTaskSection(skel, tc.section, tc.content)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, taskStartMarker(tc.section)+"\n"+tc.want) {
				t.Errorf("got:\n%s", got)
			}
			if _, err := parseTaskDoc(got); err != nil {
				t.Errorf("result must parse: %v", err)
			}
		})
	}
	if _, err := spliceTaskSection(skel, "bogus", "x"); err == nil {
		t.Error("unknown section must fail")
	}
}

// TestSpliceTaskSection_EscapesMarkers covers criterion "escape".
func TestSpliceTaskSection_EscapesMarkers(t *testing.T) {
	skel := renderTaskSkeleton("SPEC-001", "T")
	evil := "antes\n" + taskEndMarker(model.TaskSectionDelivery) + "\n" + tareaVersionLine + "\ndespues"
	doc, err := spliceTaskSection(skel, model.TaskSectionDelivery, evil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "&lt;!-- mneme:tarea:entrega:end -->") {
		t.Errorf("marker not escaped:\n%s", doc)
	}
	if _, err := parseTaskDoc(doc); err != nil {
		t.Fatalf("doc must still parse: %v", err)
	}
	doc2, err := spliceTaskSection(doc, model.TaskSectionReview, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc2, "&lt;!-- mneme:tarea:entrega:end -->") || !strings.Contains(doc2, "rev") {
		t.Errorf("later write lost content:\n%s", doc2)
	}
}

// TestParseTaskDoc_Malformed covers the structural failures.
func TestParseTaskDoc_Malformed(t *testing.T) {
	good := renderTaskSkeleton("SPEC-001", "T")
	s, e := taskStartMarker(model.TaskSectionPlan), taskEndMarker(model.TaskSectionPlan)
	tests := []struct {
		name string
		doc  string
	}{
		{"empty", ""},
		{"missing start", strings.Replace(good, s+"\n", "", 1)},
		{"missing end", strings.Replace(good, e+"\n", "", 1)},
		{"duplicate start", good + s + "\n"},
		{"duplicate end", good + e + "\n"},
		{"end before start", swapLines(good, s, e)},
		{"out of order", swapLines(good, taskStartMarker(model.TaskSectionWhat), taskStartMarker(model.TaskSectionPlan))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseTaskDoc(tc.doc); !errors.Is(err, model.ErrTaskDocMalformed) {
				t.Errorf("err = %v, want ErrTaskDocMalformed", err)
			}
		})
	}
	if _, err := parseTaskDoc(good); err != nil {
		t.Errorf("skeleton must parse: %v", err)
	}
}

// TestWriteTaskSection_IOErrors covers the filesystem failure paths: a path
// that is a directory cannot be read, and a missing parent cannot hold the
// temp file. Neither may panic or leave debris.
func TestWriteTaskSection_IOErrors(t *testing.T) {
	spec := &model.Spec{ID: "SPEC-001", Title: "T"}
	dir := t.TempDir()

	if _, err := writeTaskSection(dir, spec, model.TaskSectionPlan, "x"); err == nil {
		t.Error("reading a directory must fail")
	}
	missing := filepath.Join(dir, "nope", "tarea.md")
	if _, err := writeTaskSection(missing, spec, model.TaskSectionPlan, "x"); err == nil {
		t.Error("missing parent directory must fail")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("debris left behind: %v", entries)
	}
}

// errTestSeam is the sentinel the filesystem seams return in
// TestWriteTaskSection_SeamErrors.
var errTestSeam = errors.New("test seam failure")

// swapSeam replaces *target with fn and restores it when the test ends.
func swapSeam[T any](t *testing.T, target *T, fn T) {
	t.Helper()
	old := *target
	*target = fn
	t.Cleanup(func() { *target = old })
}

// TestWriteTaskSection_SeamErrors reaches the write, chmod and rename failure
// branches of the atomic write, with tarea.md absent and present. Each case
// must report the right error, report created=false, leave no .tarea-* debris
// and leave tarea.md untouched.
//
// It swaps package-level seams, so it must NOT call t.Parallel: a parallel
// sibling would race on those globals.
func TestWriteTaskSection_SeamErrors(t *testing.T) {
	spec := &model.Spec{ID: "SPEC-001", Title: "T"}

	failures := map[string]func(t *testing.T){
		"write": func(t *testing.T) {
			swapSeam(t, &taskCreateTemp, func(dir, pattern string) (*os.File, error) {
				f, err := os.CreateTemp(dir, pattern)
				if err != nil {
					return nil, err
				}
				name := f.Name()
				if err := f.Close(); err != nil {
					return nil, err
				}
				// A read-only handle makes WriteString fail for real.
				return os.Open(name)
			})
		},
		"chmod": func(t *testing.T) {
			swapSeam(t, &taskChmod, func(string, os.FileMode) error { return errTestSeam })
		},
		"rename": func(t *testing.T) {
			swapSeam(t, &taskRename, func(string, string) error { return errTestSeam })
		},
	}

	for _, name := range []string{"write", "chmod", "rename"} {
		for _, present := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/present=%t", name, present), func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "tarea.md")
				if present {
					doc := renderTaskSkeleton(spec.ID, spec.Title)
					if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				before, _ := os.ReadFile(path)
				failures[name](t)

				created, err := writeTaskSection(path, spec, model.TaskSectionPlan, "x")
				if err == nil {
					t.Fatal("expected an error")
				}
				if name == "write" {
					if !strings.Contains(err.Error(), "write temp") {
						t.Errorf("error %q does not come from the write branch", err)
					}
				} else if !errors.Is(err, errTestSeam) {
					t.Errorf("error %v does not wrap errTestSeam", err)
				}
				if created {
					t.Error("created must be false on failure")
				}
				if debris, _ := filepath.Glob(filepath.Join(dir, ".tarea-*")); len(debris) != 0 {
					t.Errorf("debris left behind: %v", debris)
				}
				after, afterErr := os.ReadFile(path)
				if present {
					if afterErr != nil || string(after) != string(before) {
						t.Error("existing tarea.md was modified")
					}
				} else if !errors.Is(afterErr, os.ErrNotExist) {
					t.Errorf("tarea.md must stay absent, got err=%v", afterErr)
				}
			})
		}
	}
}

// swapLines exchanges two distinct marker lines of doc.
func swapLines(doc, a, b string) string {
	const tmp = "\x00swap\x00"
	return strings.Replace(strings.Replace(strings.Replace(doc, a, tmp, 1), b, a, 1), tmp, b, 1)
}
