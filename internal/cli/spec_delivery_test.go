package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/delivery"
	"github.com/wirvii/mneme/internal/model"
)

// tareaWithEntrega renders a complete tarea.md (version line, title, the five
// sections in order) whose entrega section holds body. There is no CLI write
// path for tarea.md (SPEC-182 Dp4), so the test writes the file the way
// spec_doc_write would leave it.
func tareaWithEntrega(id, title, body string) string {
	var b strings.Builder
	b.WriteString("<!-- mneme:tarea v=1 -->\n")
	fmt.Fprintf(&b, "# %s — %s\n", id, title)
	for _, s := range model.TaskSections {
		heading, _ := s.Heading()
		fmt.Fprintf(&b, "\n<!-- mneme:tarea:%s:start -->\n%s\n", s, heading)
		if s == model.TaskSectionDelivery {
			b.WriteString("\n" + body + "\n")
		}
		fmt.Fprintf(&b, "<!-- mneme:tarea:%s:end -->\n", s)
	}
	return b.String()
}

// TestSpecStatus_DeliveryState_CLI covers C8: `mneme spec status` of a spec
// in implementing prints the delivery state line, carries delivery_state in
// --json, reports a hand-broken block, and prints nothing without tarea.md.
func TestSpecStatus_DeliveryState_CLI(t *testing.T) {
	const (
		project = "delivery-cli/project"
		title   = "entrega por CLI"
	)
	dataDir := t.TempDir()
	workflowDir := t.TempDir()
	t.Setenv("MNEME_WORKFLOW_DIR", workflowDir)

	run := func(args ...string) string {
		t.Helper()
		out, _, err := runBacklogCmd(t, dataDir, project, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}

	id := specIDFromCreateOutput(t, run("spec", "new", title, "--lane", "standard"))
	run("spec", "advance", id, "--by", "orch")
	writeFrontierCriteriaCLI(t, workflowDir, project, id)
	writeReviewReportCLI(t, workflowDir, project, id, "review-spec")
	for range 3 {
		run("spec", "advance", id, "--by", "orch")
	}
	writeReviewReportCLI(t, workflowDir, project, id, "review-plan")
	run("spec", "advance", id, "--by", "orch")

	tarea := filepath.Join(workflowDir, strings.ReplaceAll(project, "/", "-"), "specs", id, "tarea.md")
	writeTarea := func(estado string) {
		t.Helper()
		body := "Bloqueado.\n\n" + delivery.StartMarker + "\nschema_version = 1\nestado = \"" + estado +
			"\"\nriesgo = \"none\"\n" + delivery.EndMarker
		if err := os.WriteFile(tarea, []byte(tareaWithEntrega(id, title, body)), 0o644); err != nil {
			t.Fatalf("write tarea.md: %v", err)
		}
	}

	if out := run("spec", "status", id); !strings.Contains(out, "Status: implementing") || strings.Contains(out, "Entrega:") {
		t.Fatalf("without tarea.md: output = %q, want implementing and no Entrega line", out)
	}

	writeTarea("bloqueado")
	if out := run("spec", "status", id); !strings.Contains(out, "Entrega: bloqueado") {
		t.Errorf("valid block: output = %q, want %q", out, "Entrega: bloqueado")
	}
	if out := run("spec", "status", id, "--json"); !strings.Contains(out, `"delivery_state": "bloqueado"`) {
		t.Errorf("valid block --json: output = %q, want delivery_state bloqueado", out)
	}

	writeTarea("roto")
	if out := run("spec", "status", id); !strings.Contains(out, "Entrega: contrato invalido") {
		t.Errorf("broken block: output = %q, want %q", out, "Entrega: contrato invalido")
	}

	if err := os.WriteFile(tarea, []byte(tareaWithEntrega(id, title, "Sin bloque.")), 0o644); err != nil {
		t.Fatalf("write tarea.md: %v", err)
	}
	if out := run("spec", "status", id); !strings.Contains(out, "Entrega: sin contrato") {
		t.Errorf("no block: output = %q, want %q", out, "Entrega: sin contrato")
	}

	if err := os.Remove(tarea); err != nil {
		t.Fatalf("remove tarea.md: %v", err)
	}
	if out := run("spec", "status", id); strings.Contains(out, "Entrega:") {
		t.Errorf("tarea.md removed: output = %q, want no Entrega line", out)
	}
}
