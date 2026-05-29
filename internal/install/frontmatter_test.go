package install

import (
	"strings"
	"testing"
)

// TestSetModelInFrontmatter_Replace verifies that an existing model: line is
// replaced and nothing else changes (I1 guard: byte-for-byte preservation).
func TestSetModelInFrontmatter_Replace(t *testing.T) {
	original := `---
name: backend
description: "Invocar UNICAMENTE cuando se requiera implementacion."
model: claude-sonnet-4-6
color: green
permissionMode: bypassPermissions
# bypassPermissions comment
tools: Read, Edit, Write
---

# Backend Agent

Body content here.
`
	got, err := SetModelInFrontmatter([]byte(original), "opus")
	if err != nil {
		t.Fatalf("SetModelInFrontmatter error: %v", err)
	}
	result := string(got)

	// model line changed
	if !strings.Contains(result, "model: opus") {
		t.Error("expected model: opus in result")
	}
	// old model gone
	if strings.Contains(result, "claude-sonnet-4-6") {
		t.Error("old model ID should not remain in result")
	}
	// description preserved verbatim
	if !strings.Contains(result, `description: "Invocar UNICAMENTE cuando se requiera implementacion."`) {
		t.Error("description line must be preserved verbatim")
	}
	// comment preserved
	if !strings.Contains(result, "# bypassPermissions comment") {
		t.Error("YAML comment must be preserved")
	}
	// permissionMode preserved
	if !strings.Contains(result, "permissionMode: bypassPermissions") {
		t.Error("permissionMode line must be preserved")
	}
	// body preserved
	if !strings.Contains(result, "# Backend Agent") {
		t.Error("body must be preserved")
	}
}

// TestSetModelInFrontmatter_RoundTrip3Cycles verifies that applying
// SetModelInFrontmatter 3 times produces a stable result (no cumulative
// corruption — the I1 regression test).
func TestSetModelInFrontmatter_RoundTrip3Cycles(t *testing.T) {
	original := `---
name: architect
description: "Invocar SIEMPRE que se deba analizar un nuevo requerimiento, definir una especificacion tecnica, o cuando necesites orientacion arquitectonica. El arquitecto analiza requerimientos y genera specs detalladas que guian a los agentes de backend y frontend."
model: claude-opus-4-6
color: blue
tools: Read, Grep, Glob, NotebookRead, BashOutput, mcp__mneme__*
---

# Architect Agent

Body here.
`
	data := []byte(original)
	for i := 0; i < 3; i++ {
		var err error
		data, err = SetModelInFrontmatter(data, "opus")
		if err != nil {
			t.Fatalf("cycle %d: SetModelInFrontmatter error: %v", i+1, err)
		}
	}

	result := string(data)

	// Model must be exactly what we set.
	if !strings.Contains(result, "model: opus") {
		t.Error("after 3 cycles: expected model: opus")
	}
	// Description must be verbatim — the classic I1 corruption was here.
	if !strings.Contains(result, `description: "Invocar SIEMPRE que se deba analizar un nuevo requerimiento, definir una especificacion tecnica, o cuando necesites orientacion arquitectonica. El arquitecto analiza requerimientos y genera specs detalladas que guian a los agentes de backend y frontend."`) {
		t.Error("after 3 cycles: description is corrupted (I1 regression)")
	}
	// Exactly one model: line.
	count := strings.Count(result, "model:")
	if count != 1 {
		t.Errorf("after 3 cycles: expected 1 model: line, got %d", count)
	}
}

// TestSetModelInFrontmatter_DescriptionSpecialChars tests that descriptions
// with colons, quotes, and unicode survive multiple cycles without corruption.
func TestSetModelInFrontmatter_DescriptionSpecialChars(t *testing.T) {
	original := "---\nname: qa-tester\ndescription: \"Invocar SIEMPRE: validar codigo. Unicode: 你好世界. Colons: a:b:c.\"\nmodel: claude-opus-4-6\ncolor: purple\ntools: Read\n---\n\n# Body\n"
	data := []byte(original)
	for i := 0; i < 3; i++ {
		var err error
		data, err = SetModelInFrontmatter(data, "sonnet")
		if err != nil {
			t.Fatalf("cycle %d error: %v", i+1, err)
		}
	}
	result := string(data)

	want := `description: "Invocar SIEMPRE: validar codigo. Unicode: 你好世界. Colons: a:b:c."`
	if !strings.Contains(result, want) {
		t.Errorf("description with special chars corrupted; want %q in result\ngot:\n%s", want, result)
	}
	if !strings.Contains(result, "model: sonnet") {
		t.Error("model not set to sonnet")
	}
}

// TestSetModelInFrontmatter_NoModelLine verifies insertion after description:
// when no model: line exists in the frontmatter.
func TestSetModelInFrontmatter_NoModelLine(t *testing.T) {
	original := `---
name: frontend
description: "Frontend agent"
color: red
tools: Read
---

Body.
`
	got, err := SetModelInFrontmatter([]byte(original), "haiku")
	if err != nil {
		t.Fatalf("SetModelInFrontmatter error: %v", err)
	}
	result := string(got)

	if !strings.Contains(result, "model: haiku") {
		t.Error("expected model: haiku to be inserted")
	}
	// description must still be there
	if !strings.Contains(result, `description: "Frontend agent"`) {
		t.Error("description must be preserved after insertion")
	}
}

// TestSetModelInFrontmatter_MissingOpenDelimiter returns an error when the
// opening --- delimiter is absent.
func TestSetModelInFrontmatter_MissingOpenDelimiter(t *testing.T) {
	content := "name: foo\nmodel: x\n"
	_, err := SetModelInFrontmatter([]byte(content), "opus")
	if err == nil {
		t.Error("expected error when opening --- is missing")
	}
}

// TestSetModelInFrontmatter_MissingCloseDelimiter returns an error when the
// closing --- delimiter is absent.
func TestSetModelInFrontmatter_MissingCloseDelimiter(t *testing.T) {
	content := "---\nname: foo\nmodel: x\n"
	_, err := SetModelInFrontmatter([]byte(content), "opus")
	if err == nil {
		t.Error("expected error when closing --- is missing")
	}
}

// TestSetModelInFrontmatter_CommentYAMLPreserved verifies that YAML comments
// inside the frontmatter block are kept verbatim.
func TestSetModelInFrontmatter_CommentYAMLPreserved(t *testing.T) {
	original := `---
name: backend
description: "desc"
model: sonnet
# this is a comment
permissionMode: bypassPermissions
tools: Edit
---
`
	got, err := SetModelInFrontmatter([]byte(original), "opus")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(string(got), "# this is a comment") {
		t.Error("YAML comment must survive SetModelInFrontmatter")
	}
	if !strings.Contains(string(got), "permissionMode: bypassPermissions") {
		t.Error("permissionMode must survive SetModelInFrontmatter")
	}
}

// TestSetModelInFrontmatter_PermissionModePreserved verifies the permissionMode
// field is preserved across multiple set cycles (regression for backend.md).
func TestSetModelInFrontmatter_PermissionModePreserved(t *testing.T) {
	// Mirrors backend.md structure exactly.
	original := `---
name: backend
description: "Invocar UNICAMENTE cuando se requiera: 1. Disenar o modificar logica de negocio (Casos de Uso/Dominio). 2. Alterar esquemas de base de datos o queries SQL. 3. Implementar adaptadores de infraestructura (HTTP, gRPC, PubSub). 4. Configurar inyeccion de dependencias o wiring de modulos."
model: claude-sonnet-4-6
color: green
permissionMode: bypassPermissions
# bypassPermissions: implementer en autonomous runs (sin prompts de permiso); la barrera de rol es el allowlist tools: de abajo
tools: Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*
---

# Backend Agent
`
	data := []byte(original)
	for i := 0; i < 3; i++ {
		var err error
		data, err = SetModelInFrontmatter(data, "sonnet")
		if err != nil {
			t.Fatalf("cycle %d error: %v", i+1, err)
		}
	}
	result := string(data)
	if !strings.Contains(result, "permissionMode: bypassPermissions") {
		t.Error("permissionMode must survive 3 cycles of SetModelInFrontmatter")
	}
	if !strings.Contains(result, "model: sonnet") {
		t.Error("model must be sonnet after 3 cycles")
	}
}
