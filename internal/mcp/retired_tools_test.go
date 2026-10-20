package mcp

import "testing"

// TestToolsCall_RetiredAdminToolsAreUnknown is SPEC-163 C2: calling any of the
// 23 retired tools answers exactly like any other unknown tool — method not
// found, "unknown tool: <name>" — with no special message and no alias that
// would keep an old client half-working.
func TestToolsCall_RetiredAdminToolsAreUnknown(t *testing.T) {
	for _, name := range retiredAdminTools {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(t)

			resp := process(t, srv, "tools/call", 1, ToolCallParams{
				Name:      name,
				Arguments: mustMarshal(t, map[string]any{}),
			})
			if resp.Error == nil {
				t.Fatalf("%s: expected an error, got a result", name)
			}
			if resp.Error.Code != CodeMethodNotFound {
				t.Errorf("%s: code = %d, want CodeMethodNotFound (%d)", name, resp.Error.Code, CodeMethodNotFound)
			}
			if want := "unknown tool: " + name; resp.Error.Message != want {
				t.Errorf("%s: message = %q, want %q", name, resp.Error.Message, want)
			}
		})
	}
}
