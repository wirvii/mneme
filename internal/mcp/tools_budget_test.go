package mcp

import (
	"encoding/json"
	"testing"
)

// toolsListMaxBytes pins the size of the tools/list payload every MCP
// session pays before its first call (SPEC-187, D4b). It is the exact value
// measured after mem_context gained include_synthesis, with no margin: the
// test fails on any growth. Raising it is a visible act that the commit
// raising it must justify; this spec only records the ceiling, it does not
// lower it.
const toolsListMaxBytes = 26576

// TestToolsList_BytesCeiling keeps the MCP catalogue from growing silently:
// 31 tools and no more than toolsListMaxBytes of JSON.
func TestToolsList_BytesCeiling(t *testing.T) {
	tools := allTools()
	if len(tools) != 31 {
		t.Fatalf("allTools() returned %d tools, want 31", len(tools))
	}
	b, err := json.Marshal(ToolsListResult{Tools: tools})
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	t.Logf("tools/list: %d bytes (ceiling %d)", len(b), toolsListMaxBytes)
	if len(b) > toolsListMaxBytes {
		t.Errorf("tools/list is %d bytes, ceiling toolsListMaxBytes = %d", len(b), toolsListMaxBytes)
	}
}
