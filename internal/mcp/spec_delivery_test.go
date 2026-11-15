package mcp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/delivery"
)

// deliveryBlockMCP is a valid hecho_con_dudas entrega with prose around the
// block, the content an implementer passes to spec_doc_write.
const deliveryBlockMCP = "Implementado.\n\n" + delivery.StartMarker + `
schema_version = 1
estado = "hecho_con_dudas"
arbol = "2ba81d8c4e1f"
riesgo = { item = 3, razon = "spec_status gana campos" }

[[evidencia]]
comando = "go test ./internal/delivery/"
exit_code = 0
salida = "ok"
criterio = "AC1"
` + delivery.EndMarker + "\n\nNotas.\n"

// toolText returns the raw JSON text of a successful tool call, so a test can
// assert on exact keys and their absence.
func toolText(t *testing.T, resp JSONRPCResponse) string {
	t.Helper()
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if len(result.Content) == 0 {
		t.Fatal("tool result has no content blocks")
	}
	return result.Content[0].Text
}

// implementingSpecMCP walks a standard spec to implementing through the real
// tools and returns its id and the path its tarea.md resolves to.
func implementingSpecMCP(t *testing.T, srv *Server, workflowDir string) (id, tareaPath string) {
	t.Helper()
	id = advanceSpecToPlanned(t, srv, "entrega MCP")
	resp := process(t, srv, "tools/call", 50, ToolCallParams{
		Name:      "spec_advance",
		Arguments: mustMarshal(t, map[string]any{"id": id, "by": "orchestrator"}),
	})
	if resp.Error != nil {
		t.Fatalf("spec_advance to implementing: %v", resp.Error.Message)
	}
	return id, filepath.Join(workflowDir, "test-project", "specs", id, "tarea.md")
}

// writeEntregaMCP calls spec_doc_write for the entrega section.
func writeEntregaMCP(t *testing.T, srv *Server, id, content string) JSONRPCResponse {
	t.Helper()
	return process(t, srv, "tools/call", 60, ToolCallParams{
		Name: "spec_doc_write",
		Arguments: mustMarshal(t, map[string]any{
			"id": id, "kind": "task", "section": "entrega", "content": content,
		}),
	})
}

// statusMCP calls spec_status and returns its raw JSON text.
func statusMCP(t *testing.T, srv *Server, id string) string {
	t.Helper()
	return toolText(t, process(t, srv, "tools/call", 70, ToolCallParams{
		Name:      "spec_status",
		Arguments: mustMarshal(t, map[string]any{"id": id}),
	}))
}

// TestSpecDocWrite_Delivery_MCP covers C7's write half: an invalid block is
// -32602 naming the field with tarea.md untouched; a valid one reports its
// contract and state.
func TestSpecDocWrite_Delivery_MCP(t *testing.T) {
	srv, workflowDir := newTestServerWithSDDAndWorkflowDir(t)
	id, path := implementingSpecMCP(t, srv, workflowDir)

	text := toolText(t, writeEntregaMCP(t, srv, id, deliveryBlockMCP))
	for _, want := range []string{`"contract":"valid"`, `"delivery_state":"hecho_con_dudas"`} {
		if !strings.Contains(text, want) {
			t.Errorf("spec_doc_write response = %s, want %s", text, want)
		}
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tarea.md: %v", err)
	}
	resp := writeEntregaMCP(t, srv, id, strings.Replace(deliveryBlockMCP, "item = 3", "item = 7", 1))
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("invalid block: error = %+v, want CodeInvalidParams", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "riesgo.item") {
		t.Errorf("message = %q, want it to name riesgo.item", resp.Error.Message)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tarea.md: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("tarea.md changed by a refused write")
	}

	resp = writeEntregaMCP(t, srv, id, "Sin bloque.")
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams || !strings.Contains(resp.Error.Message, "references/entrega.md") {
		t.Fatalf("missing block: error = %+v, want CodeInvalidParams pointing to references/entrega.md", resp.Error)
	}
}

// TestSpecStatus_DeliveryState_MCP covers C7's read half and the presence
// condition of the three keys.
func TestSpecStatus_DeliveryState_MCP(t *testing.T) {
	srv, workflowDir := newTestServerWithSDDAndWorkflowDir(t)
	id, path := implementingSpecMCP(t, srv, workflowDir)
	deliveryKeys := []string{`"delivery_contract"`, `"delivery_state"`, `"delivery_detail"`}

	if text := statusMCP(t, srv, id); containsAny(text, deliveryKeys) {
		t.Errorf("no tarea.md: spec_status = %s, want no delivery keys", text)
	}

	if resp := writeEntregaMCP(t, srv, id, deliveryBlockMCP); resp.Error != nil {
		t.Fatalf("spec_doc_write: %v", resp.Error.Message)
	}
	text := statusMCP(t, srv, id)
	for _, want := range []string{`"delivery_contract":"valid"`, `"delivery_state":"hecho_con_dudas"`} {
		if !strings.Contains(text, want) {
			t.Errorf("valid block: spec_status = %s, want %s", text, want)
		}
	}
	if strings.Contains(text, `"delivery_detail"`) {
		t.Errorf("valid block: spec_status = %s, want no delivery_detail", text)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tarea.md: %v", err)
	}
	broken := strings.Replace(string(raw), `estado = "hecho_con_dudas"`, `estado = "terminado"`, 1)
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("write tarea.md: %v", err)
	}
	text = statusMCP(t, srv, id)
	for _, want := range []string{`"delivery_contract":"invalid"`, `"delivery_detail"`} {
		if !strings.Contains(text, want) {
			t.Errorf("broken block: spec_status = %s, want %s", text, want)
		}
	}
	if strings.Contains(text, `"delivery_state"`) {
		t.Errorf("broken block: spec_status = %s, want no delivery_state", text)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove tarea.md: %v", err)
	}
	if text := statusMCP(t, srv, id); containsAny(text, deliveryKeys) {
		t.Errorf("tarea.md removed: spec_status = %s, want no delivery keys", text)
	}

	t.Run("speccing", func(t *testing.T) {
		srv, _ := newTestServerWithSDDAndWorkflowDir(t)
		newResp := process(t, srv, "tools/call", 1, ToolCallParams{
			Name:      "spec_new",
			Arguments: mustMarshal(t, map[string]any{"title": "en diseno", "lane": "standard"}),
		})
		var spec struct {
			ID string `json:"id"`
		}
		unmarshalToolText(t, newResp, &spec)
		if resp := process(t, srv, "tools/call", 2, ToolCallParams{
			Name:      "spec_advance",
			Arguments: mustMarshal(t, map[string]any{"id": spec.ID, "by": "orchestrator"}),
		}); resp.Error != nil {
			t.Fatalf("spec_advance: %v", resp.Error.Message)
		}
		if resp := process(t, srv, "tools/call", 3, ToolCallParams{
			Name: "spec_doc_write",
			Arguments: mustMarshal(t, map[string]any{
				"id": spec.ID, "kind": "task", "section": "que", "content": "Que.",
			}),
		}); resp.Error != nil {
			t.Fatalf("spec_doc_write(que): %v", resp.Error.Message)
		}
		if text := statusMCP(t, srv, spec.ID); containsAny(text, deliveryKeys) {
			t.Errorf("speccing: spec_status = %s, want no delivery keys", text)
		}
	})
}

// containsAny reports whether s contains any of subs.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
