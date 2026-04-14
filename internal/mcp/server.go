package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/juanftp/mneme/internal/service"
)

// Server is a Model Context Protocol server that speaks JSON-RPC 2.0 over stdio.
// It wraps a MemoryService and an SDDService, exposing their capabilities as MCP tools.
type Server struct {
	svc      *service.MemoryService
	sdd      *service.SDDService
	tools    []ToolDefinition
	handlers *handlers
	logger   *slog.Logger
	version  string
}

// NewServer constructs a Server. toolsMode selects which tool set to expose:
// "agent" exposes the agent-facing subset; any other value (including "all")
// exposes the full set. sddSvc may be nil — when nil, all SDD tools return an
// error indicating the service is unavailable.
func NewServer(svc *service.MemoryService, sddSvc *service.SDDService, logger *slog.Logger, toolsMode string, version string) *Server {
	var tools []ToolDefinition
	if toolsMode == "agent" {
		tools = agentTools()
	} else {
		tools = allTools()
	}

	return &Server{
		svc:      svc,
		sdd:      sddSvc,
		tools:    tools,
		handlers: newHandlers(svc, sddSvc, logger),
		logger:   logger,
		version:  version,
	}
}

// Run starts the JSON-RPC 2.0 message loop, reading line-delimited JSON from
// reader and writing responses to writer. It returns when reader reaches EOF,
// when ctx is cancelled, or when a fatal I/O error occurs.
//
// Each incoming line must be a complete JSON object. Responses are written as
// single-line JSON followed by a newline. Notifications (requests without an id)
// are processed silently with no response emitted.
//
// Background tasks (e.g. consolidation) are started via svc.Start before the
// message loop begins. They are stopped automatically when ctx is cancelled.
func (s *Server) Run(ctx context.Context, reader io.Reader, writer io.Writer) error {
	// Start background tasks. This is a no-op when consolidation is disabled.
	s.svc.Start(ctx)

	scanner := bufio.NewScanner(reader)
	bw := bufio.NewWriter(writer)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("mcp: run: read: %w", err)
			}
			// EOF — client closed the connection.
			return nil
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		resp, hasResp := s.handleMessage(line)
		if !hasResp {
			// Notification — no response required.
			continue
		}

		b, err := json.Marshal(resp)
		if err != nil {
			s.logger.Error("mcp: marshal response", "error", err)
			continue
		}

		if _, err := bw.Write(b); err != nil {
			return fmt.Errorf("mcp: run: write: %w", err)
		}
		if err := bw.WriteByte('\n'); err != nil {
			return fmt.Errorf("mcp: run: write newline: %w", err)
		}
		if err := bw.Flush(); err != nil {
			return fmt.Errorf("mcp: run: flush: %w", err)
		}
	}
}

// handleMessage parses a single JSON-RPC message and returns the response to
// send and whether a response should be sent. Notifications return
// (nil, false). Parse errors return an error response with a null id.
//
// This method is also used directly by tests to process individual messages
// without running the full I/O loop.
func (s *Server) handleMessage(msg []byte) (JSONRPCResponse, bool) {
	var req JSONRPCRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		resp := s.errorResponse(json.RawMessage("null"), CodeInvalidRequest,
			fmt.Sprintf("mcp: parse request: %s", err))
		return resp, true
	}

	// Notifications have no ID. Per JSON-RPC 2.0, they must not receive a response.
	isNotification := req.ID == nil || string(req.ID) == "null"
	if isNotification {
		s.dispatchNotification(req)
		return JSONRPCResponse{}, false
	}

	result, rpcErr := s.dispatchMethod(context.Background(), req)
	if rpcErr != nil {
		return s.errorResponse(req.ID, rpcErr.Code, rpcErr.Message), true
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}, true
}

// dispatchMethod routes a request to the appropriate handler and returns
// either a result or a JSONRPCError.
func (s *Server) dispatchMethod(ctx context.Context, req JSONRPCRequest) (any, *JSONRPCError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params)

	case "tools/list":
		return ToolsListResult{Tools: s.tools}, nil

	case "tools/call":
		var params ToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: tools/call: invalid params: %s", err),
			}
		}
		result, rpcErr := s.handlers.handleToolCall(ctx, params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return result, nil

	default:
		return nil, &JSONRPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("mcp: method not found: %s", req.Method),
		}
	}
}

// handleInitialize processes the initialize handshake and returns server
// capabilities and identity.
func (s *Server) handleInitialize(_ json.RawMessage) (InitializeResult, *JSONRPCError) {
	return InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: Capabilities{
			Tools: &ToolsCapability{ListChanged: false},
		},
		ServerInfo: ServerInfo{
			Name:    "mneme",
			Version: s.version,
		},
	}, nil
}

// dispatchNotification handles incoming notifications (requests without an id).
// Currently the only handled notification is notifications/initialized; all
// others are silently ignored per the JSON-RPC 2.0 specification.
func (s *Server) dispatchNotification(req JSONRPCRequest) {
	switch req.Method {
	case "notifications/initialized":
		// No-op: client has confirmed initialization.
	default:
		s.logger.Debug("mcp: ignoring notification", "method", req.Method)
	}
}

// errorResponse builds a JSON-RPC 2.0 error response with the given id, code,
// and message.
func (s *Server) errorResponse(id json.RawMessage, code int, message string) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
}
