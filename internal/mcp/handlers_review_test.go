package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validReviewSpecTOML and validReviewPlanTOML are minimal valid fresh-review
// reports of each moment (SPEC-181). They are byte-identical to the copies in
// the service and cli test packages, which cannot see these constants.
const validReviewSpecTOML = `schema_version = 1

[[revisor]]
papel = "comprension"
estado = "completo"
parafrasis_propuesta = "Propone revisar la spec sin contexto."
parafrasis_hoy = "Hoy nadie la revisa."

[[revisor]]
papel = "diseno"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"
`

const validReviewPlanTOML = `schema_version = 1

[[revisor]]
papel = "preparacion"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"
`

// writeReviewReportMCP writes a valid report of kind ("review-spec" or
// "review-plan") through the real spec_doc_write tool and then stamps the
// returned file at max(now, mtime(tarea.md), mtime(criteria.toml)) + 2s, so
// the SPEC-181 guard always finds it current (plan decision 9). It returns the
// path the server reported.
func writeReviewReportMCP(t *testing.T, srv *Server, specID, kind string) string {
	t.Helper()
	content := validReviewSpecTOML
	if kind == "review-plan" {
		content = validReviewPlanTOML
	}
	resp := process(t, srv, "tools/call", 2000, ToolCallParams{
		Name:      "spec_doc_write",
		Arguments: mustMarshal(t, map[string]any{"id": specID, "kind": kind, "content": content}),
	})
	if resp.Error != nil {
		t.Fatalf("spec_doc_write (%s fixture): %v", kind, resp.Error.Message)
	}
	var written struct {
		Path string `json:"path"`
	}
	unmarshalToolText(t, resp, &written)
	if written.Path == "" {
		t.Fatalf("spec_doc_write (%s fixture): empty path in the response", kind)
	}

	stamp := time.Now()
	for _, doc := range []string{"tarea.md", "criteria.toml"} {
		if info, err := os.Stat(filepath.Join(filepath.Dir(written.Path), doc)); err == nil && info.ModTime().After(stamp) {
			stamp = info.ModTime()
		}
	}
	stamp = stamp.Add(2 * time.Second)
	if err := os.Chtimes(written.Path, stamp, stamp); err != nil {
		t.Fatalf("chtimes %s: %v", written.Path, err)
	}
	return written.Path
}

// advanceToSpeccing creates a standard spec and moves it to speccing with its
// criteria written, ready to probe the guarded transitions over the wire.
func advanceToSpeccing(t *testing.T, srv *Server) string {
	t.Helper()
	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "spec_new",
		Arguments: mustMarshal(t, map[string]any{"title": "review guard", "lane": "standard"}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, newResp, &spec)
	if r := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "spec_doc_write", Arguments: mustMarshal(t, map[string]any{"id": spec.ID, "kind": "criteria", "content": fixtureCriteriaTOML}),
	}); r.Error != nil {
		t.Fatalf("spec_doc_write criteria: %v", r.Error.Message)
	}
	if r := process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "spec_advance", Arguments: mustMarshal(t, map[string]any{"id": spec.ID, "by": "orchestrator"}),
	}); r.Error != nil {
		t.Fatalf("spec_advance to speccing: %v", r.Error.Message)
	}
	return spec.ID
}

func advanceOverMCP(t *testing.T, srv *Server, specID string) *JSONRPCError {
	t.Helper()
	return process(t, srv, "tools/call", 4, ToolCallParams{
		Name: "spec_advance", Arguments: mustMarshal(t, map[string]any{"id": specID, "by": "architect"}),
	}).Error
}

// TestReviewErrors_InvalidParams covers SPEC-181 C14: a missing, stale or
// invalid fresh-review report makes spec_advance answer -32602, and
// spec_status never exposes the review_required mark.
func TestReviewErrors_InvalidParams(t *testing.T) {
	t.Run("missing report", func(t *testing.T) {
		srv, _ := newTestServerWithSDDAndMemSvc(t)
		id := advanceToSpeccing(t, srv)
		rpcErr := advanceOverMCP(t, srv, id)
		if rpcErr == nil || rpcErr.Code != CodeInvalidParams || !strings.Contains(rpcErr.Message, "revision-spec.toml") {
			t.Fatalf("error = %+v, want -32602 naming revision-spec.toml", rpcErr)
		}
	})

	t.Run("stale report", func(t *testing.T) {
		srv, _ := newTestServerWithSDDAndMemSvc(t)
		id := advanceToSpeccing(t, srv)
		path := writeReviewReportMCP(t, srv, id, "review-spec")
		past := time.Now().Add(-10 * time.Second)
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
		rpcErr := advanceOverMCP(t, srv, id)
		if rpcErr == nil || rpcErr.Code != CodeInvalidParams || !strings.Contains(rpcErr.Message, "caducado") {
			t.Fatalf("error = %+v, want -32602 stale", rpcErr)
		}
	})

	t.Run("invalid report written straight to disk", func(t *testing.T) {
		srv, _ := newTestServerWithSDDAndMemSvc(t)
		id := advanceToSpeccing(t, srv)
		path := writeReviewReportMCP(t, srv, id, "review-spec")
		if err := os.WriteFile(path, []byte("schema_version = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rpcErr := advanceOverMCP(t, srv, id)
		if rpcErr == nil || rpcErr.Code != CodeInvalidParams || !strings.Contains(rpcErr.Message, "no es un informe de revisión válido") {
			t.Fatalf("error = %+v, want -32602 invalid", rpcErr)
		}
	})

	t.Run("spec_doc_write rejects an invalid report with -32602", func(t *testing.T) {
		srv, _ := newTestServerWithSDDAndMemSvc(t)
		id := advanceToSpeccing(t, srv)
		resp := process(t, srv, "tools/call", 5, ToolCallParams{
			Name:      "spec_doc_write",
			Arguments: mustMarshal(t, map[string]any{"id": id, "kind": "review-spec", "content": "nope"}),
		})
		if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
			t.Fatalf("error = %+v, want -32602", resp.Error)
		}
	})

	t.Run("spec_status never exposes review_required", func(t *testing.T) {
		srv, _ := newTestServerWithSDDAndMemSvc(t)
		id := advanceToSpeccing(t, srv)
		resp := process(t, srv, "tools/call", 6, ToolCallParams{
			Name: "spec_status", Arguments: mustMarshal(t, map[string]any{"id": id}),
		})
		if resp.Error != nil {
			t.Fatalf("spec_status: %v", resp.Error.Message)
		}
		var result ToolCallResult
		unmarshalResult(t, resp, &result)
		if len(result.Content) == 0 {
			t.Fatal("spec_status returned no content")
		}
		if strings.Contains(result.Content[0].Text, "review_required") {
			t.Errorf("spec_status leaks review_required: %s", result.Content[0].Text)
		}
	})
}
