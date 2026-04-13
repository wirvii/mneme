// Package http implements a REST API server for mneme. It exposes the same
// memory operations available through MCP and CLI as HTTP endpoints, enabling
// integration with web dashboards, CI/CD pipelines, and external tools that
// cannot use stdio-based MCP.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// Server is the mneme HTTP API server. It owns the routing, middleware, and
// lifecycle management for all REST endpoints. The embedded MemoryService
// handles all business logic; Server is a pure transport adapter.
type Server struct {
	svc    *service.MemoryService
	logger *slog.Logger
	addr   string
}

// NewServer constructs a Server. svc must be fully initialised; addr must be
// a valid listen address such as ":7437" or "127.0.0.1:7437".
func NewServer(svc *service.MemoryService, logger *slog.Logger, addr string) *Server {
	return &Server{
		svc:    svc,
		logger: logger,
		addr:   addr,
	}
}

// NewHandler returns an http.Handler with all mneme API routes registered.
// It is the same handler used by Start. Callers that need to compose mneme
// routes into a larger mux (e.g. in tests) can use this directly.
func NewHandler(svc *service.MemoryService, logger *slog.Logger) http.Handler {
	s := NewServer(svc, logger, "")
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

// Start registers routes, starts the HTTP server, and blocks until ctx is
// cancelled. It performs a graceful shutdown after receiving the cancellation
// signal, allowing in-flight requests up to 10 seconds to complete.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	srv := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	// Run the server in a goroutine so we can select on ctx and the server error.
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		// ListenAndServe always returns a non-nil error; ignore ErrServerClosed
		// which is expected after Shutdown.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}

// registerRoutes wires all HTTP routes onto mux. More-specific patterns are
// registered before catch-all ones so that /v1/memories/search and
// /v1/memories/context are matched before the /v1/memories/ prefix handler.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Health
	mux.HandleFunc("/v1/health", s.withLogging(s.withJSON(s.handleHealth)))

	// Memory collection
	mux.HandleFunc("/v1/memories", s.withLogging(s.withJSON(s.handleMemories)))

	// Named sub-resources — must be registered before the catch-all.
	mux.HandleFunc("/v1/memories/search", s.withLogging(s.withJSON(s.handleSearch)))
	mux.HandleFunc("/v1/memories/context", s.withLogging(s.withJSON(s.handleContext)))

	// Catch-all for /v1/memories/{id}
	mux.HandleFunc("/v1/memories/", s.withLogging(s.withJSON(s.handleMemoryByID)))

	// Sessions
	mux.HandleFunc("/v1/sessions/end", s.withLogging(s.withJSON(s.handleSessionEnd)))

	// Entities
	mux.HandleFunc("/v1/entities/relate", s.withLogging(s.withJSON(s.handleRelate)))

	// Stats
	mux.HandleFunc("/v1/stats", s.withLogging(s.withJSON(s.handleStats)))

	// Consolidation
	mux.HandleFunc("/v1/consolidate", s.withLogging(s.withJSON(s.handleConsolidate)))
}

// --------------------------------------------------------------------------
// Middleware
// --------------------------------------------------------------------------

// withJSON sets the Content-Type header to application/json before delegating
// to next. It does not validate or transform the response body.
func (s *Server) withJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next(w, r)
	}
}

// withLogging logs method, path, and elapsed time after the response is sent.
func (s *Server) withLogging(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next(w, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start),
		)
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// extractID strips prefix from path and returns the remainder.
// e.g. extractID("/v1/memories/019530a1-...", "/v1/memories/") → "019530a1-..."
func extractID(path, prefix string) string {
	return strings.TrimPrefix(path, prefix)
}

// writeJSON encodes v as JSON into w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are already written; nothing useful to do here.
		return
	}
}

// apiError is the canonical error envelope returned to clients.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes a JSON error response with the appropriate HTTP status.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{
		Error: apiErrorBody{Code: code, Message: message},
	})
}

// errorStatus maps well-known domain errors to HTTP status codes.
func errorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, model.ErrNotFound), errors.Is(err, model.ErrEntityNotFound), errors.Is(err, model.ErrRelationNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, model.ErrTitleRequired), errors.Is(err, model.ErrContentRequired),
		errors.Is(err, model.ErrSummaryRequired), errors.Is(err, model.ErrQueryRequired),
		errors.Is(err, model.ErrInvalidType), errors.Is(err, model.ErrInvalidScope),
		errors.Is(err, model.ErrInvalidEntityKind), errors.Is(err, model.ErrInvalidRelationType):
		return http.StatusBadRequest, "invalid_request"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

// decode decodes the JSON body of r into dst and returns false on error,
// writing the appropriate error response to w.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
		return false
	}
	return true
}

// --------------------------------------------------------------------------
// Handlers
// --------------------------------------------------------------------------

// handleHealth handles GET /v1/health.
// It returns 200 {"status":"ok"} unconditionally so load balancers and
// readiness probes have a fast, dependency-free check.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMemories handles POST /v1/memories (Save).
func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}

	var req model.SaveRequest
	if !decode(w, r, &req) {
		return
	}

	resp, err := s.svc.Save(r.Context(), req)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// handleMemoryByID dispatches GET, PATCH, and DELETE on /v1/memories/{id}.
func (s *Server) handleMemoryByID(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/v1/memories/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "memory ID is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGetMemory(w, r, id)
	case http.MethodPatch:
		s.handleUpdateMemory(w, r, id)
	case http.MethodDelete:
		s.handleForgetMemory(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET, PATCH, or DELETE")
	}
}

// handleGetMemory handles GET /v1/memories/{id}.
func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request, id string) {
	m, err := s.svc.Get(r.Context(), id)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleUpdateMemory handles PATCH /v1/memories/{id}.
func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request, id string) {
	var req model.UpdateRequest
	if !decode(w, r, &req) {
		return
	}

	resp, err := s.svc.Update(r.Context(), id, req)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleForgetMemory handles DELETE /v1/memories/{id}.
// The reason is accepted via an optional JSON body {"reason":"..."}; it is
// not persisted in the current implementation but included for forward
// compatibility.
func (s *Server) handleForgetMemory(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Reason string `json:"reason"`
	}
	// Body is optional for DELETE; ignore parse errors.
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := s.svc.Forget(r.Context(), id, body.Reason); err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "forgotten", "id": id})
}

// handleSearch handles GET /v1/memories/search.
//
// Query parameters:
//
//	q       — search query (required)
//	project — project slug filter
//	scope   — scope filter: global, org, project
//	type    — memory type filter
//	limit   — max results (default: service default)
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query parameter 'q' is required")
		return
	}

	req := model.SearchRequest{
		Query:   q,
		Project: r.URL.Query().Get("project"),
	}

	if scopeStr := r.URL.Query().Get("scope"); scopeStr != "" {
		scope := model.Scope(scopeStr)
		req.Scope = &scope
	}
	if typeStr := r.URL.Query().Get("type"); typeStr != "" {
		memType := model.MemoryType(typeStr)
		req.Type = &memType
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "'limit' must be a non-negative integer")
			return
		}
		req.Limit = n
	}

	resp, err := s.svc.Search(r.Context(), req)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleContext handles GET /v1/memories/context.
//
// Query parameters:
//
//	project — project slug (defaults to service project)
//	budget  — token budget (default: service default)
//	focus   — optional focus phrase
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}

	req := model.ContextRequest{
		Project: r.URL.Query().Get("project"),
		Focus:   r.URL.Query().Get("focus"),
	}

	if budgetStr := r.URL.Query().Get("budget"); budgetStr != "" {
		n, err := strconv.Atoi(budgetStr)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "'budget' must be a non-negative integer")
			return
		}
		req.Budget = n
	}

	resp, err := s.svc.Context(r.Context(), req)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSessionEnd handles POST /v1/sessions/end.
// Body: model.SessionEndRequest JSON.
func (s *Server) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}

	var req model.SessionEndRequest
	if !decode(w, r, &req) {
		return
	}

	resp, err := s.svc.SessionEnd(r.Context(), req)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRelate handles POST /v1/entities/relate.
// Body: model.RelateRequest JSON.
func (s *Server) handleRelate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}

	var req model.RelateRequest
	if !decode(w, r, &req) {
		return
	}

	resp, err := s.svc.Relate(r.Context(), req)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}

	status := http.StatusOK
	if resp.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, resp)
}

// handleStats handles GET /v1/stats.
//
// Query parameters:
//
//	project — project slug; empty means global store stats
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}

	project := r.URL.Query().Get("project")

	resp, err := s.svc.Stats(r.Context(), project)
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleConsolidate handles POST /v1/consolidate.
// It runs the consolidation pipeline synchronously and returns a summary of
// what was swept, hard-deleted, deduplicated, and evicted. Callers should be
// prepared for this endpoint to take several seconds on large stores.
func (s *Server) handleConsolidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}

	result, err := s.svc.RunConsolidation(r.Context())
	if err != nil {
		status, code := errorStatus(err)
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
