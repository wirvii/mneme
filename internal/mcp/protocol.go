// Package mcp implements a Model Context Protocol server over stdio.
// MCP enables AI coding agents to interact with mneme's memory system
// through a standardized JSON-RPC 2.0 interface, allowing them to save,
// search, and retrieve persistent memories across sessions.
package mcp

import "encoding/json"

// JSONRPCRequest represents an incoming JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // can be number, string, or null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodeMemoryNotFound is a server-defined error code indicating the requested
	// memory ID does not exist.
	CodeMemoryNotFound = -32000
)

// InitializeParams holds the parameters sent by the client in the initialize request.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    any        `json:"capabilities"`
	ClientInfo      ClientInfo `json:"clientInfo"`
}

// ClientInfo describes the MCP client that initiated the connection.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is returned in response to the initialize request.
// It advertises the server's protocol version, capabilities, and identity.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
}

// Capabilities lists the optional protocol features the server supports.
type Capabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability signals that the server supports the tools sub-protocol and
// indicates whether the tool list can change after initialization.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ServerInfo provides the human-readable identity of the server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ToolsListResult is returned in response to tools/list.
type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolDefinition describes a single tool the server exposes to the client.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// ToolCallParams carries the tool name and arguments for a tools/call request.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolCallResult is the envelope returned after executing a tool. IsError is true
// when the tool encountered a logical error that does not warrant a JSON-RPC error
// response (e.g. a validation failure surfaced as text to the agent).
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is a single piece of content within a ToolCallResult.
// Type is always "text" for mneme responses.
type ContentBlock struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}
