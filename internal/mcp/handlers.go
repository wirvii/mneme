package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/querylog"
	"github.com/wirvii/mneme/internal/service"
)

// handlers dispatches tools/call requests to the appropriate MemoryService method.
// Each handler is responsible for deserializing arguments, calling the service,
// and packaging the result into a ToolCallResult with a JSON text content block.
type handlers struct {
	svc         *service.MemoryService
	sdd         *service.SDDService
	cgSvc       *service.CodeGraphService  // lazy-initialized on first codegraph tool call
	skillsSvc   *service.SkillsService     // optional; nil disables skills tools
	modelsSvc   *service.ModelsService     // optional; nil disables model tools
	subagentSvc *service.SubagentService   // wraps svc; always available (SPEC-057/SS-4)
	profileSvc  *service.ProfileService    // wraps cfg.ProfilesDir(); always available (SPEC-091 §1)
	logger      *slog.Logger
}

// newHandlers constructs a handlers bound to the given services and logger.
func newHandlers(svc *service.MemoryService, sdd *service.SDDService, skillsSvc *service.SkillsService, modelsSvc *service.ModelsService, logger *slog.Logger) *handlers {
	cfg := svc.Config()
	subagentSvc := service.NewSubagentService(svc)

	skillsDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		skillsDir = filepath.Join(home, ".claude", "skills")
	}

	return &handlers{
		svc:         svc,
		sdd:         sdd,
		skillsSvc:   skillsSvc,
		modelsSvc:   modelsSvc,
		subagentSvc: subagentSvc,
		// noPrompt=true: MCP is an unattended agent session — a git credential
		// prompt no human will see must fail fast instead of hanging the
		// process (R1). The CLI frontend passes false instead (see
		// internal/cli/profile.go's newProfileSvc).
		// mem/sub/skillsDir/configPath wired (SPEC-093 §3) so profile_use can
		// materialize (Activate) and profile_default can read/write
		// [profiles].default — same wiring as the CLI's newActivatingProfileSvc.
		profileSvc: service.NewProfileService(cfg.ProfilesDir(), true,
			service.WithProfileMemoryService(svc),
			service.WithProfileSubagentService(subagentSvc),
			service.WithProfileSkillsDir(skillsDir),
			service.WithProfileConfigPath(config.DefaultPath()),
		),
		logger: logger,
	}
}

// handleToolCall dispatches the tool call to the correct handler method.
// It returns a JSONRPCError when the tool name is unknown, arguments are
// malformed, or the service returns an error that maps to a protocol error code.
func (h *handlers) handleToolCall(ctx context.Context, params ToolCallParams) (*ToolCallResult, *JSONRPCError) {
	// Record code graph adoption telemetry (SPEC-083 W1): every codegraph_* tool
	// call is an authoritative "use" event. This is the single, authoritative
	// hook point — the MCP dispatch always runs when the tool runs, it excludes
	// human CLI use (which never goes through this handler), and it avoids any
	// dependence on PreToolUse firing for MCP tools. Fail-open, best-effort.
	if strings.HasPrefix(params.Name, "codegraph_") {
		h.logCodegraphUse(params.Name)
	}

	switch params.Name {
	case "mem_save":
		return h.handleMemSave(ctx, params.Arguments)
	case "mem_search":
		return h.handleMemSearch(ctx, params.Arguments)
	case "mem_get":
		return h.handleMemGet(ctx, params.Arguments)
	case "mem_context":
		return h.handleMemContext(ctx, params.Arguments)
	case "mem_update":
		return h.handleMemUpdate(ctx, params.Arguments)
	case "mem_session_end":
		return h.handleMemSessionEnd(ctx, params.Arguments)
	case "mem_suggest_topic_key":
		return h.handleMemSuggestTopicKey(ctx, params.Arguments)
	case "mem_relate":
		return h.handleMemRelate(ctx, params.Arguments)
	case "mem_timeline":
		return h.handleMemTimeline(ctx, params.Arguments)
	case "mem_stats":
		return h.handleMemStats(ctx, params.Arguments)
	case "mem_forget":
		return h.handleMemForget(ctx, params.Arguments)
	case "mem_promote":
		return h.handleMemPromote(ctx, params.Arguments)
	case "mem_checkpoint":
		return h.handleMemCheckpoint(ctx, params.Arguments)
	case "mem_explore":
		return h.handleMemExplore(ctx, params.Arguments)
	case "mem_gaps":
		return h.handleMemGaps(ctx, params.Arguments)
	case "backlog_add":
		return h.handleBacklogAdd(ctx, params.Arguments)
	case "backlog_list":
		return h.handleBacklogList(ctx, params.Arguments)
	case "backlog_refine":
		return h.handleBacklogRefine(ctx, params.Arguments)
	case "backlog_promote":
		return h.handleBacklogPromote(ctx, params.Arguments)
	case "spec_new":
		return h.handleSpecNew(ctx, params.Arguments)
	case "spec_status":
		return h.handleSpecStatus(ctx, params.Arguments)
	case "spec_advance":
		return h.handleSpecAdvance(ctx, params.Arguments)
	case "spec_pushback":
		return h.handleSpecPushback(ctx, params.Arguments)
	case "spec_resolve":
		return h.handleSpecResolve(ctx, params.Arguments)
	case "spec_doc_write":
		return h.handleSpecDocWrite(ctx, params.Arguments)
	case "spec_list":
		return h.handleSpecList(ctx, params.Arguments)
	case "spec_quick":
		return h.handleSpecQuick(ctx, params.Arguments)
	case "lane_audit":
		return h.handleLaneAudit(ctx, params.Arguments)
	case "lane_reclassify":
		return h.handleLaneReclassify(ctx, params.Arguments)
	case "lane_override":
		return h.handleLaneOverride(ctx, params.Arguments)
	case "lane_status":
		return h.handleLaneStatus(ctx, params.Arguments)
	case "spec_reject":
		return h.handleSpecReject(ctx, params.Arguments)
	case "lane_stats":
		return h.handleLaneStats(ctx, params.Arguments)

	// --- SKILLS TOOLS ---
	case "skills_list":
		return h.handleSkillsList(ctx, params.Arguments)
	case "skills_install":
		return h.handleSkillsInstall(ctx, params.Arguments)
	case "skills_pin":
		return h.handleSkillsPin(ctx, params.Arguments)
	case "skills_unpin":
		return h.handleSkillsUnpin(ctx, params.Arguments)
	case "skills_remove":
		return h.handleSkillsRemove(ctx, params.Arguments)
	case "skills_lint":
		return h.handleSkillsLint(ctx, params.Arguments)
	case "skills_validate":
		return h.handleSkillsValidate(ctx, params.Arguments)

	// --- MODEL TOOLS (SPEC-038) ---
	case "model_list":
		return h.handleModelList(ctx, params.Arguments)
	case "model_set":
		return h.handleModelSet(ctx, params.Arguments)
	case "model_reset":
		return h.handleModelReset(ctx, params.Arguments)

	// --- CONFLICTS TOOLS (SPEC-039) ---
	case "conflicts_candidates":
		return h.handleConflictsCandidates(ctx, params.Arguments)
	case "conflicts_scan":
		return h.handleConflictsScan(ctx, params.Arguments)
	case "conflicts_link":
		return h.handleConflictsLink(ctx, params.Arguments)
	case "conflicts_unlink":
		return h.handleConflictsUnlink(ctx, params.Arguments)
	case "conflicts_list":
		return h.handleConflictsList(ctx, params.Arguments)

	// --- PROFILE TOOLS (SPEC-091 §1) ---
	case "profile_add":
		return h.handleProfileAdd(ctx, params.Arguments)
	case "profile_update":
		return h.handleProfileUpdate(ctx, params.Arguments)
	case "profile_list":
		return h.handleProfileList(ctx, params.Arguments)
	case "profile_status":
		return h.handleProfileStatus(ctx, params.Arguments)
	case "profile_use":
		return h.handleProfileUse(ctx, params.Arguments)
	case "profile_default":
		return h.handleProfileDefault(ctx, params.Arguments)

	// --- CODEGRAPH TOOLS ---
	case "codegraph_search":
		return h.handleCodegraphSearch(ctx, params.Arguments)
	case "codegraph_context":
		return h.handleCodegraphContext(ctx, params.Arguments)
	case "codegraph_callers":
		return h.handleCodegraphCallers(ctx, params.Arguments)
	case "codegraph_callees":
		return h.handleCodegraphCallees(ctx, params.Arguments)
	case "codegraph_impact":
		return h.handleCodegraphImpact(ctx, params.Arguments)
	case "codegraph_node":
		return h.handleCodegraphNode(ctx, params.Arguments)
	case "codegraph_explore":
		return h.handleCodegraphExplore(ctx, params.Arguments)
	case "codegraph_trace":
		return h.handleCodegraphTrace(ctx, params.Arguments)
	case "codegraph_status":
		return h.handleCodegraphStatus(ctx, params.Arguments)
	case "codegraph_files":
		return h.handleCodegraphFiles(ctx, params.Arguments)

	case "init":
		return h.handleInit(ctx, params.Arguments)

	// --- SUBAGENT TOOLS (SPEC-057 / EPIC agnostic-agents SS-4) ---
	case "subagent_fingerprint":
		return h.handleSubagentFingerprint(ctx, params.Arguments)
	case "subagent_profile_get":
		return h.handleSubagentProfileGet(ctx, params.Arguments)
	case "subagent_profile_save":
		return h.handleSubagentProfileSave(ctx, params.Arguments)
	case "subagent_compose":
		return h.handleSubagentCompose(ctx, params.Arguments)
	case "subagent_write":
		return h.handleSubagentWrite(ctx, params.Arguments)
	case "subagent_manifest_list":
		return h.handleSubagentManifestList(ctx, params.Arguments)

	default:
		return nil, &JSONRPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("unknown tool: %s", params.Name),
		}
	}
}

// logCodegraphUse appends a code graph "use" telemetry event (SPEC-083 W1) for
// the named codegraph_* tool. It is gated by [codegraph] querylog_enabled and
// is strictly best-effort: any failure (no project slug, append error) is
// ignored so telemetry never affects a tool call. No session id is recorded —
// the MCP server has none, and the adoption ratio is aggregate, not per-session.
func (h *handlers) logCodegraphUse(name string) {
	cfg := h.svc.Config()
	if cfg == nil || !cfg.Codegraph.QuerylogEnabled {
		return
	}
	slug := h.svc.ProjectSlug()
	if slug == "" {
		return
	}
	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
	path := codegraph.QuerylogPath(projectsDir, slug)
	ev := querylog.Event{
		TS:      time.Now().UTC(),
		Project: slug,
		Kind:    querylog.KindUse,
		Tool:    name,
		Source:  "mcp",
	}
	//nolint:errcheck // telemetry is best-effort; failures must not affect the call
	_ = querylog.Append(path, ev, querylog.DefaultMaxBytes)
}

// handleMemSave processes a mem_save tool call.
func (h *handlers) handleMemSave(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.SaveRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_save: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Save(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_save", err)
	}

	return resultFromAny(resp)
}

// handleMemSearch processes a mem_search tool call.
func (h *handlers) handleMemSearch(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.SearchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_search: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Search(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_search", err)
	}

	return resultFromAny(resp)
}

// handleMemGet processes a mem_get tool call. The arguments object must contain
// an "id" string field.
func (h *handlers) handleMemGet(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_get: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_get: id is required",
		}
	}

	mem, err := h.svc.Get(ctx, args.ID)
	if err != nil {
		return nil, h.mapServiceError("mem_get", err)
	}

	return resultFromAny(mem)
}

// handleMemContext processes a mem_context tool call.
func (h *handlers) handleMemContext(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.ContextRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_context: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Context(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_context", err)
	}

	// Inject codegraph availability hint so agents discover it automatically.
	resp.CodeGraphHint = h.buildCodeGraphContextSection()

	return resultFromAny(resp)
}

// buildCodeGraphContextSection returns a text hint describing codegraph tool
// availability and current indexing status. The hint is included in every
// mem_context response so agents automatically discover codegraph without
// requiring manual CLAUDE.md configuration.
func (h *handlers) buildCodeGraphContextSection() string {
	// If cgSvc is already initialized, use it to get live stats.
	if h.cgSvc != nil {
		return h.buildCodeGraphHintFromService(h.cgSvc)
	}

	// Try to determine whether a codegraph DB exists for this project.
	slug := h.svc.ProjectSlug()
	if slug == "" {
		return codeGraphGenericHint
	}

	cfg := h.svc.Config()
	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")

	if !service.CodeGraphDBExists(projectsDir, slug) {
		return codeGraphNotIndexedHint
	}

	// DB file exists — open it to check stats.
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		// Cannot open DB; fall back to generic hint.
		return codeGraphNotIndexedHint
	}

	return h.buildCodeGraphHintFromService(cgSvc)
}

// buildCodeGraphHintFromService generates the codegraph hint text using a live
// service instance to query stats.
func (h *handlers) buildCodeGraphHintFromService(cgSvc *service.CodeGraphService) string {
	stats, err := cgSvc.Status()
	if err != nil || stats.NodeCount == 0 {
		return codeGraphNotIndexedHint
	}

	return fmt.Sprintf(`Code Graph (indexed): %d symbols across %d files. `+
		`Use codegraph_search, codegraph_context, codegraph_callers, codegraph_callees, `+
		`codegraph_impact, codegraph_node, codegraph_explore, codegraph_trace instead of reading files. `+
		`Re-index with codegraph_index if code changed significantly.`,
		stats.NodeCount, stats.FileCount)
}

const codeGraphGenericHint = `Code graph tools available: codegraph_search, codegraph_context, ` +
	`codegraph_callers, codegraph_callees, codegraph_impact, codegraph_node, codegraph_explore, ` +
	`codegraph_trace, codegraph_status, codegraph_files. ` +
	`Call codegraph_index with the project root path to index a codebase. ` +
	`Prefer codegraph queries over reading entire files when exploring code structure.`

const codeGraphNotIndexedHint = `Code graph available but not yet indexed for this project. ` +
	`Run codegraph_index with the project root path to index the codebase. ` +
	`Once indexed, use codegraph_search, codegraph_callers, codegraph_impact etc. ` +
	`to understand code without reading files.`

// handleMemUpdate processes a mem_update tool call. The arguments object must
// contain an "id" field; all other update fields are optional.
func (h *handlers) handleMemUpdate(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	// We decode id separately so UpdateRequest can be cleanly separated.
	var args struct {
		ID string `json:"id"`
		model.UpdateRequest
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_update: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_update: id is required",
		}
	}

	resp, err := h.svc.Update(ctx, args.ID, args.UpdateRequest)
	if err != nil {
		return nil, h.mapServiceError("mem_update", err)
	}

	return resultFromAny(resp)
}

// handleMemSessionEnd processes a mem_session_end tool call.
func (h *handlers) handleMemSessionEnd(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.SessionEndRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_session_end: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.SessionEnd(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_session_end", err)
	}

	return resultFromAny(resp)
}

// handleMemSuggestTopicKey processes a mem_suggest_topic_key tool call.
// Arguments must contain a "title" field; "project" is optional.
func (h *handlers) handleMemSuggestTopicKey(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		Title   string `json:"title"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_suggest_topic_key: invalid arguments: %s", err),
		}
	}
	if args.Title == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_suggest_topic_key: title is required",
		}
	}

	resp, err := h.svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title:   args.Title,
		Project: args.Project,
	})
	if err != nil {
		return nil, h.mapServiceError("mem_suggest_topic_key", err)
	}

	return resultFromAny(resp)
}

// handleMemRelate processes a mem_relate tool call.
func (h *handlers) handleMemRelate(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.RelateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_relate: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Relate(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_relate", err)
	}

	return resultFromAny(resp)
}

// handleMemTimeline processes a mem_timeline tool call.
func (h *handlers) handleMemTimeline(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.TimelineRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_timeline: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Timeline(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_timeline", err)
	}

	return resultFromAny(resp)
}

// handleMemStats processes a mem_stats tool call. The arguments object may
// contain an optional "project" string field; when omitted the service's
// detected project is used.
func (h *handlers) handleMemStats(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		Project string `json:"project"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle mem_stats: invalid arguments: %s", err),
			}
		}
	}

	// When the caller does not supply a project, use the service's detected slug.
	project := args.Project
	if project == "" {
		project = h.svc.ProjectSlug()
	}

	resp, err := h.svc.Stats(ctx, project)
	if err != nil {
		return nil, h.mapServiceError("mem_stats", err)
	}

	return resultFromAny(resp)
}

// handleMemForget processes a mem_forget tool call. The arguments object must
// contain an "id" field; "reason" is optional.
func (h *handlers) handleMemForget(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_forget: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_forget: id is required",
		}
	}

	if err := h.svc.Forget(ctx, args.ID, args.Reason); err != nil {
		return nil, h.mapServiceError("mem_forget", err)
	}

	return resultFromAny(map[string]string{
		"id":     args.ID,
		"status": "marked_for_decay",
	})
}

// handleMemPromote processes a mem_promote tool call. The arguments object
// must contain an "id" field. Unlike other not-found mappings routed through
// mapServiceError (CodeMemoryNotFound), an unknown id here is reported as
// CodeInvalidParams — the caller supplied a bad argument, not a query that
// legitimately found nothing (SPEC-063 SS-C contract).
func (h *handlers) handleMemPromote(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_promote: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_promote: id is required",
		}
	}

	m, err := h.svc.Promote(ctx, args.ID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle mem_promote: %s", err),
			}
		}
		h.logger.Error("mcp: internal error", "method", "mem_promote", "error", err)
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle mem_promote: %v", err),
		}
	}

	return resultFromAny(map[string]any{
		"id":     m.ID,
		"shared": m.Shared,
		"author": m.Author,
		"status": "promoted",
	})
}

// handleMemCheckpoint processes a mem_checkpoint tool call.
func (h *handlers) handleMemCheckpoint(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.CheckpointRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_checkpoint: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Checkpoint(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_checkpoint", err)
	}

	return resultFromAny(resp)
}

// mapServiceError converts a service-layer error into a JSONRPCError with an
// appropriate error code. ErrNotFound maps to CodeMemoryNotFound; validation
// errors map to CodeInvalidParams; all others become CodeInternalError.
//
// For non-sentinel errors the full error message is included in Message so that
// the calling agent receives an actionable description instead of a generic
// "internal error" string. This is safe because mneme is a local, single-user
// tool and error messages never contain sensitive credentials.
func (h *handlers) mapServiceError(method string, err error) *JSONRPCError {
	if errors.Is(err, model.ErrNotFound) ||
		errors.Is(err, model.ErrEntityNotFound) ||
		errors.Is(err, model.ErrRelationNotFound) ||
		errors.Is(err, model.ErrBacklogNotFound) ||
		errors.Is(err, model.ErrSpecNotFound) ||
		errors.Is(err, model.ErrPushbackNotFound) ||
		errors.Is(err, model.ErrSkillNotFound) ||
		errors.Is(err, model.ErrProfileNotFound) {
		return &JSONRPCError{
			Code:    CodeMemoryNotFound,
			Message: fmt.Sprintf("mcp: handle %s: %s", method, err),
		}
	}

	if errors.Is(err, model.ErrTitleRequired) ||
		errors.Is(err, model.ErrContentRequired) ||
		errors.Is(err, model.ErrQueryRequired) ||
		errors.Is(err, model.ErrSummaryRequired) ||
		errors.Is(err, model.ErrInvalidType) ||
		errors.Is(err, model.ErrInvalidScope) ||
		errors.Is(err, model.ErrInvalidEntityKind) ||
		errors.Is(err, model.ErrInvalidRelationType) ||
		errors.Is(err, model.ErrInvalidTransition) ||
		errors.Is(err, model.ErrBacklogNotRefined) ||
		errors.Is(err, model.ErrQualityGateFailed) ||
		errors.Is(err, model.ErrInvalidBacklogStatus) ||
		errors.Is(err, model.ErrInvalidPriority) ||
		errors.Is(err, model.ErrInvalidSpecStatus) ||
		errors.Is(err, model.ErrAppliesToRequired) ||
		errors.Is(err, model.ErrAppliesToForbidden) ||
		errors.Is(err, model.ErrInvalidSeverity) ||
		errors.Is(err, model.ErrEmptyPattern) ||
		errors.Is(err, model.ErrInvalidWeight) ||
		errors.Is(err, model.ErrAmbiguousSeed) ||
		errors.Is(err, model.ErrLaneRequired) ||
		errors.Is(err, model.ErrInvalidLane) ||
		errors.Is(err, model.ErrScopeRequired) ||
		errors.Is(err, model.ErrLaneImmutable) ||
		errors.Is(err, model.ErrLaneMismatch) ||
		errors.Is(err, model.ErrAuditFailed) ||
		errors.Is(err, model.ErrReasonRequired) ||
		errors.Is(err, model.ErrSkillMalformed) ||
		errors.Is(err, model.ErrSkillPinned) ||
		errors.Is(err, model.ErrSkillNoValidation) ||
		errors.Is(err, model.ErrUnknownAgent) ||
		errors.Is(err, model.ErrInvalidModel) ||
		errors.Is(err, model.ErrInvalidRelation) ||
		errors.Is(err, model.ErrUnknownSpecDocKind) ||
		errors.Is(err, model.ErrProfileExists) ||
		errors.Is(err, model.ErrProfileNameMismatch) ||
		errors.Is(err, model.ErrInvalidProfile) {
		return &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle %s: %s", method, err),
		}
	}

	h.logger.Error("mcp: internal error", "method", method, "error", err)
	return &JSONRPCError{
		Code:    CodeInternalError,
		Message: fmt.Sprintf("mcp: handle %s: %v", method, err),
	}
}

// handleMemExplore processes a mem_explore tool call.
func (h *handlers) handleMemExplore(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.ExploreRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_explore: invalid arguments: %s", err),
		}
	}
	if req.Seed == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_explore: seed is required",
		}
	}

	resp, err := h.svc.Explore(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_explore", err)
	}
	return resultFromAny(resp)
}

// handleMemGaps processes a mem_gaps tool call.
func (h *handlers) handleMemGaps(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.GapsRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle mem_gaps: invalid arguments: %s", err),
			}
		}
	}

	resp, err := h.svc.Gaps(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_gaps", err)
	}
	return resultFromAny(resp)
}

// --- SDD HANDLERS ---

// sddUnavailable returns a JSONRPCError indicating the SDD service is not
// initialised. This happens when the MCP server starts but the SDD store
// could not be opened (e.g. during tests that only need memory tools).
func (h *handlers) sddUnavailable(method string) *JSONRPCError {
	return &JSONRPCError{
		Code:    CodeInternalError,
		Message: fmt.Sprintf("mcp: handle %s: SDD service not available", method),
	}
}

// handleBacklogAdd processes a backlog_add tool call.
func (h *handlers) handleBacklogAdd(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("backlog_add")
	}
	var req model.BacklogAddRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle backlog_add: invalid arguments: %s", err),
		}
	}

	item, err := h.sdd.BacklogAdd(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("backlog_add", err)
	}

	return resultFromAny(item)
}

// handleBacklogList processes a backlog_list tool call.
func (h *handlers) handleBacklogList(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("backlog_list")
	}
	var req model.BacklogListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle backlog_list: invalid arguments: %s", err),
			}
		}
	}

	items, err := h.sdd.BacklogList(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("backlog_list", err)
	}

	return resultFromAny(items)
}

// handleBacklogRefine processes a backlog_refine tool call.
func (h *handlers) handleBacklogRefine(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("backlog_refine")
	}
	var req model.BacklogRefineRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle backlog_refine: invalid arguments: %s", err),
		}
	}

	item, err := h.sdd.BacklogRefine(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("backlog_refine", err)
	}

	return resultFromAny(item)
}

// handleBacklogPromote processes a backlog_promote tool call.
func (h *handlers) handleBacklogPromote(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("backlog_promote")
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle backlog_promote: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle backlog_promote: id is required",
		}
	}

	spec, err := h.sdd.BacklogPromote(ctx, args.ID)
	if err != nil {
		return nil, h.mapServiceError("backlog_promote", err)
	}

	return resultFromAny(spec)
}

// handleSpecNew processes a spec_new tool call.
func (h *handlers) handleSpecNew(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_new")
	}
	var req model.SpecNewRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_new: invalid arguments: %s", err),
		}
	}

	spec, err := h.sdd.SpecNew(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_new", err)
	}

	return resultFromAny(spec)
}

// handleSpecStatus processes a spec_status tool call.
func (h *handlers) handleSpecStatus(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_status")
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_status: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle spec_status: id is required",
		}
	}

	resp, err := h.sdd.SpecStatus(ctx, args.ID)
	if err != nil {
		return nil, h.mapServiceError("spec_status", err)
	}

	return resultFromAny(resp)
}

// specAdvanceResponse is the envelope handleSpecAdvance returns (SPEC-068
// D5): the advanced spec plus an advisory ExecutorResolution for the stage it
// just entered. The envelope is additive — Spec carries exactly what the bare
// *model.Spec response used to carry, as its own subfield — so existing
// callers that only read the spec fields keep working unchanged.
type specAdvanceResponse struct {
	Spec     *model.Spec                `json:"spec"`
	Executor service.ExecutorResolution `json:"executor"`
}

// handleSpecAdvance processes a spec_advance tool call. After the transition
// succeeds, it resolves which executor (a delegated subagent, or the
// orchestrator as a conscious fallback) should carry out the stage the spec
// just entered (SPEC-068 D2/D5) and returns both in one envelope.
//
// Manifest lookup is best-effort: SubagentService.ReadManifest returns
// (nil, nil) for a project that never ran the grill, and any read error is
// treated the same way (empty manifest) rather than failing the whole
// request — spec_advance must never fail because the manifest is
// unreadable. Either way ResolveStageExecutor still runs and produces a
// well-formed resolution (typically Degraded for delegable stages, per D3).
func (h *handlers) handleSpecAdvance(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_advance")
	}
	var req model.SpecAdvanceRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_advance: invalid arguments: %s", err),
		}
	}

	spec, err := h.sdd.SpecAdvance(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_advance", err)
	}

	manifest, manifestErr := h.subagentSvc.ReadManifest(ctx, spec.Project)
	if manifestErr != nil {
		// Best-effort (D5): an unreadable manifest must not fail an already
		// -successful advance. Fall back to an empty manifest.
		manifest = nil
	}

	executor := service.ResolveStageExecutor(spec.Status, spec.Lane, manifest)

	return resultFromAny(specAdvanceResponse{Spec: spec, Executor: executor})
}

// handleSpecPushback processes a spec_pushback tool call.
func (h *handlers) handleSpecPushback(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_pushback")
	}
	var req model.SpecPushbackRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_pushback: invalid arguments: %s", err),
		}
	}

	spec, err := h.sdd.SpecPushback(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_pushback", err)
	}

	return resultFromAny(spec)
}

// handleSpecResolve processes a spec_resolve tool call.
func (h *handlers) handleSpecResolve(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_resolve")
	}
	var req model.SpecResolveRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_resolve: invalid arguments: %s", err),
		}
	}

	spec, err := h.sdd.SpecResolve(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_resolve", err)
	}

	return resultFromAny(spec)
}

// handleSpecDocWrite processes a spec_doc_write tool call (SPEC-087 D3): the
// entregable path a subagent uses instead of copying a report into the
// workflow directory by hand. kind is validated by the service against a
// closed Go-authored enum (model.SpecDocKind) — this handler never builds a
// path itself.
func (h *handlers) handleSpecDocWrite(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_doc_write")
	}
	var req model.SpecDocWriteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_doc_write: invalid arguments: %s", err),
		}
	}

	resp, err := h.sdd.SpecDocWrite(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_doc_write", err)
	}

	return resultFromAny(resp)
}

// handleSpecList processes a spec_list tool call.
func (h *handlers) handleSpecList(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_list")
	}
	var req model.SpecListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle spec_list: invalid arguments: %s", err),
			}
		}
	}

	specs, err := h.sdd.SpecList(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_list", err)
	}

	return resultFromAny(specs)
}

// handleSpecQuick processes a spec_quick tool call.
func (h *handlers) handleSpecQuick(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_quick")
	}
	var req model.SpecQuickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_quick: invalid arguments: %s", err),
		}
	}
	spec, err := h.sdd.SpecQuick(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_quick", err)
	}
	return resultFromAny(spec)
}

// handleLaneAudit processes a lane_audit tool call.
//
// When the auditor detects threshold violations it returns both a non-nil
// AuditResult (containing the breach list) and ErrAuditFailed. In that case we
// must not discard the result and route it through mapServiceError — the agent
// needs the breach details to decide whether to reclassify or override. We
// return a ToolCallResult with IsError=true so the agent is signalled that the
// audit failed while still receiving the full AuditResult payload.
func (h *handlers) handleLaneAudit(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("lane_audit")
	}
	var req model.LaneAuditRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle lane_audit: invalid arguments: %s", err),
		}
	}
	result, err := h.sdd.LaneAudit(ctx, req)
	if err != nil {
		if errors.Is(err, model.ErrAuditFailed) && result != nil {
			// Audit ran successfully but breaches were found. Return the full
			// AuditResult to the agent so it can inspect the breach list.
			// IsError=true signals failure at the tool level without losing data.
			b, serErr := json.Marshal(result)
			if serErr != nil {
				return nil, &JSONRPCError{
					Code:    CodeInternalError,
					Message: fmt.Sprintf("mcp: handle lane_audit: serialize result: %s", serErr),
				}
			}
			return &ToolCallResult{
				Content: []ContentBlock{{Type: "text", Text: string(b)}},
				IsError: true,
			}, nil
		}
		return nil, h.mapServiceError("lane_audit", err)
	}
	return resultFromAny(result)
}

// handleLaneReclassify processes a lane_reclassify tool call.
func (h *handlers) handleLaneReclassify(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("lane_reclassify")
	}
	var req model.LaneReclassifyRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle lane_reclassify: invalid arguments: %s", err),
		}
	}
	spec, err := h.sdd.LaneReclassify(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("lane_reclassify", err)
	}
	return resultFromAny(spec)
}

// handleLaneOverride processes a lane_override tool call.
func (h *handlers) handleLaneOverride(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("lane_override")
	}
	var req model.LaneOverrideRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle lane_override: invalid arguments: %s", err),
		}
	}
	spec, err := h.sdd.LaneOverride(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("lane_override", err)
	}
	return resultFromAny(spec)
}

// handleLaneStatus processes a lane_status tool call.
func (h *handlers) handleLaneStatus(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("lane_status")
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle lane_status: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle lane_status: id is required",
		}
	}
	resp, err := h.sdd.LaneStatus(ctx, args.ID)
	if err != nil {
		return nil, h.mapServiceError("lane_status", err)
	}
	return resultFromAny(resp)
}

// handleSpecReject processes a spec_reject tool call. It rejects a spec from
// qa (standard), audit (trivial), or done (either lane, SPEC-087 D6) back to
// implementing. Uses the standard mapServiceError path — no special
// IsError-with-payload pattern.
func (h *handlers) handleSpecReject(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("spec_reject")
	}
	var req model.SpecRejectRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle spec_reject: invalid arguments: %s", err),
		}
	}
	spec, err := h.sdd.SpecReject(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("spec_reject", err)
	}
	return resultFromAny(spec)
}

// handleLaneStats processes a lane_stats tool call.
func (h *handlers) handleLaneStats(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("lane_stats")
	}
	var args struct {
		Project string `json:"project"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle lane_stats: invalid arguments: %s", err),
			}
		}
	}
	resp, err := h.sdd.LaneStats(ctx, args.Project)
	if err != nil {
		return nil, h.mapServiceError("lane_stats", err)
	}
	return resultFromAny(resp)
}

// --- CONFLICTS HANDLERS (SPEC-039) ---

// handleConflictsCandidates processes a conflicts_candidates tool call.
// Returns candidate memory IDs that share FTS5 terms with the given memory.
// Purely deterministic — no LLM involved.
func (h *handlers) handleConflictsCandidates(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		ID    string `json:"id"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle conflicts_candidates: invalid arguments: %s", err)}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle conflicts_candidates: id is required"}
	}

	ids, err := h.svc.ConflictCandidates(ctx, args.ID, args.Limit)
	if err != nil {
		return nil, h.mapServiceError("conflicts_candidates", err)
	}

	return resultFromAny(map[string]any{"id": args.ID, "candidates": ids, "count": len(ids)})
}

// handleConflictsScan processes a conflicts_scan tool call.
// When the Claude CLI is unavailable, returns IsError:true with a structured
// payload rather than a protocol error, consistent with the SPEC-035 pattern.
func (h *handlers) handleConflictsScan(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req service.ConflictScanRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle conflicts_scan: invalid arguments: %s", err)}
		}
	}

	resp, err := h.svc.ConflictScan(ctx, req)
	if err != nil {
		if errors.Is(err, model.ErrCLIUnavailable) {
			payload := map[string]any{
				"error":      err.Error(),
				"suggestion": "Install the Claude CLI (claude) and ensure it is on PATH, then retry. Run 'mneme conflicts scan' from the terminal for interactive use.",
			}
			b, _ := json.Marshal(payload)
			return &ToolCallResult{
				Content: []ContentBlock{{Type: "text", Text: string(b)}},
				IsError: true,
			}, nil
		}
		return nil, h.mapServiceError("conflicts_scan", err)
	}

	return resultFromAny(resp)
}

// handleConflictsLink processes a conflicts_link tool call.
func (h *handlers) handleConflictsLink(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		FromID    string `json:"from_id"`
		ToID      string `json:"to_id"`
		Relation  string `json:"relation"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle conflicts_link: invalid arguments: %s", err)}
	}
	if args.FromID == "" || args.ToID == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle conflicts_link: from_id and to_id are required"}
	}
	if args.Relation == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle conflicts_link: relation is required"}
	}

	if err := h.svc.ConflictLink(ctx, args.FromID, args.ToID, args.Relation, args.Rationale); err != nil {
		return nil, h.mapServiceError("conflicts_link", err)
	}

	return resultFromAny(map[string]string{
		"from_id":  args.FromID,
		"to_id":    args.ToID,
		"relation": args.Relation,
		"status":   "linked",
	})
}

// handleConflictsUnlink processes a conflicts_unlink tool call.
func (h *handlers) handleConflictsUnlink(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		FromID string `json:"from_id"`
		ToID   string `json:"to_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle conflicts_unlink: invalid arguments: %s", err)}
	}
	if args.FromID == "" || args.ToID == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle conflicts_unlink: from_id and to_id are required"}
	}

	if err := h.svc.ConflictUnlink(ctx, args.FromID, args.ToID); err != nil {
		return nil, h.mapServiceError("conflicts_unlink", err)
	}

	return resultFromAny(map[string]string{
		"from_id": args.FromID,
		"to_id":   args.ToID,
		"status":  "unlinked",
	})
}

// handleConflictsList processes a conflicts_list tool call.
func (h *handlers) handleConflictsList(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		Project string `json:"project"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle conflicts_list: invalid arguments: %s", err)}
		}
	}

	rels, err := h.svc.ConflictList(ctx, args.Project)
	if err != nil {
		return nil, h.mapServiceError("conflicts_list", err)
	}

	return resultFromAny(map[string]any{"relations": rels, "count": len(rels)})
}

// resultFromAny serializes v to a compact JSON string and wraps it in a single
// text ContentBlock inside a ToolCallResult. Returns CodeInternalError if
// serialization fails (should never happen for well-formed domain types).
func resultFromAny(v any) (*ToolCallResult, *JSONRPCError) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: serialize result: %s", err),
		}
	}
	return &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: string(b)}},
	}, nil
}

// --- SKILLS HANDLERS ---

// skillsUnavailable returns a JSONRPCError indicating the SkillsService is not
// initialised. This is the guard used by all skills handlers when skillsSvc is nil.
func (h *handlers) skillsUnavailable(method string) *JSONRPCError {
	return &JSONRPCError{
		Code:    CodeInternalError,
		Message: fmt.Sprintf("mcp: handle %s: skills service not available", method),
	}
}

// handleSkillsList processes a skills_list tool call.
func (h *handlers) handleSkillsList(_ context.Context, _ json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.skillsSvc == nil {
		return nil, h.skillsUnavailable("skills_list")
	}
	infos, err := h.skillsSvc.List()
	if err != nil {
		return nil, h.mapServiceError("skills_list", err)
	}
	return resultFromAny(infos)
}

// handleSkillsInstall processes a skills_install tool call.
func (h *handlers) handleSkillsInstall(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.skillsSvc == nil {
		return nil, h.skillsUnavailable("skills_install")
	}
	var args struct {
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle skills_install: invalid arguments: %s", err)}
	}
	if args.Name == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle skills_install: name is required"}
	}
	if err := h.skillsSvc.Install(args.Name, args.Force); err != nil {
		return nil, h.mapServiceError("skills_install", err)
	}
	return resultFromAny(map[string]string{"skill": args.Name, "status": "installed"})
}

// handleSkillsPin processes a skills_pin tool call.
func (h *handlers) handleSkillsPin(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.skillsSvc == nil {
		return nil, h.skillsUnavailable("skills_pin")
	}
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle skills_pin: invalid arguments: %s", err)}
	}
	if args.Name == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle skills_pin: name is required"}
	}
	if err := h.skillsSvc.Pin(args.Name); err != nil {
		return nil, h.mapServiceError("skills_pin", err)
	}
	return resultFromAny(map[string]string{"skill": args.Name, "status": "pinned"})
}

// handleSkillsUnpin processes a skills_unpin tool call.
func (h *handlers) handleSkillsUnpin(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.skillsSvc == nil {
		return nil, h.skillsUnavailable("skills_unpin")
	}
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle skills_unpin: invalid arguments: %s", err)}
	}
	if args.Name == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle skills_unpin: name is required"}
	}
	if err := h.skillsSvc.Unpin(args.Name); err != nil {
		return nil, h.mapServiceError("skills_unpin", err)
	}
	return resultFromAny(map[string]string{"skill": args.Name, "status": "unpinned"})
}

// handleSkillsRemove processes a skills_remove tool call.
func (h *handlers) handleSkillsRemove(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.skillsSvc == nil {
		return nil, h.skillsUnavailable("skills_remove")
	}
	var args struct {
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle skills_remove: invalid arguments: %s", err)}
	}
	if args.Name == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle skills_remove: name is required"}
	}
	if err := h.skillsSvc.Remove(args.Name, args.Force); err != nil {
		return nil, h.mapServiceError("skills_remove", err)
	}
	return resultFromAny(map[string]string{"skill": args.Name, "status": "removed"})
}

// handleSkillsLint processes a skills_lint tool call.
// When lint errors are found, the result is returned with IsError:true so the
// caller receives the full LintResult payload instead of a protocol error.
func (h *handlers) handleSkillsLint(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.skillsSvc == nil {
		return nil, h.skillsUnavailable("skills_lint")
	}
	var args struct {
		Name string `json:"name"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle skills_lint: invalid arguments: %s", err)}
		}
	}

	results, err := h.skillsSvc.Lint(args.Name)
	if err != nil {
		return nil, h.mapServiceError("skills_lint", err)
	}

	// Determine whether any skill has lint errors.
	hasErrors := false
	for _, r := range results {
		if !r.Passed {
			hasErrors = true
			break
		}
	}

	b, serErr := json.Marshal(results)
	if serErr != nil {
		return nil, &JSONRPCError{Code: CodeInternalError, Message: fmt.Sprintf("mcp: handle skills_lint: serialize: %s", serErr)}
	}

	if hasErrors {
		return &ToolCallResult{
			Content: []ContentBlock{{Type: "text", Text: string(b)}},
			IsError: true,
		}, nil
	}
	return resultFromAny(results)
}

// handleSkillsValidate processes a skills_validate tool call.
// When the script fails or the no-validation sentinel is returned, the result
// is returned with IsError:true so the caller receives the full output.
func (h *handlers) handleSkillsValidate(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.skillsSvc == nil {
		return nil, h.skillsUnavailable("skills_validate")
	}
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle skills_validate: invalid arguments: %s", err)}
	}
	if args.Name == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle skills_validate: name is required"}
	}

	result, err := h.skillsSvc.Validate(ctx, args.Name)
	if err != nil {
		// ErrSkillNotFound → normal not-found protocol error.
		// ErrSkillNoValidation → IsError:true with structured payload.
		if errors.Is(err, model.ErrSkillNoValidation) {
			payload := map[string]any{
				"skill":  args.Name,
				"passed": false,
				"error":  err.Error(),
			}
			b, _ := json.Marshal(payload)
			return &ToolCallResult{
				Content: []ContentBlock{{Type: "text", Text: string(b)}},
				IsError: true,
			}, nil
		}
		return nil, h.mapServiceError("skills_validate", err)
	}

	if !result.Passed {
		b, serErr := json.Marshal(result)
		if serErr != nil {
			return nil, &JSONRPCError{Code: CodeInternalError, Message: fmt.Sprintf("mcp: handle skills_validate: serialize: %s", serErr)}
		}
		return &ToolCallResult{
			Content: []ContentBlock{{Type: "text", Text: string(b)}},
			IsError: true,
		}, nil
	}

	return resultFromAny(result)
}

// --- CODEGRAPH HANDLERS ---

// textResult wraps a plain text string in a ToolCallResult.
func textResult(text string) *ToolCallResult {
	return &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

// getCodeGraphService returns the lazily initialized CodeGraphService. If not
// already set (e.g. injected in tests), it creates one using the MemoryService's
// project config. Returns a JSONRPCError if initialization fails.
func (h *handlers) getCodeGraphService() (*service.CodeGraphService, *JSONRPCError) {
	if h.cgSvc != nil {
		return h.cgSvc, nil
	}

	cfg := h.svc.Config()
	slug := h.svc.ProjectSlug()
	if slug == "" {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: "mcp: codegraph: no project context available",
		}
	}

	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
	cgSvc, err := service.NewCodeGraphService(projectsDir, slug)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: codegraph: init: %v", err),
		}
	}
	h.cgSvc = cgSvc
	return h.cgSvc, nil
}

// intFromArgs extracts an integer from args, returning defaultVal when the key
// is missing or not a number.
func intFromArgs(args map[string]any, key string, defaultVal int) int {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return defaultVal
		}
		return int(i)
	default:
		return defaultVal
	}
}

// stringSliceFromArgs extracts a []string from args at the given key.
func stringSliceFromArgs(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// handleCodegraphSearch processes a codegraph_search tool call.
func (h *handlers) handleCodegraphSearch(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_search: invalid arguments: %s", err),
		}
	}

	query, _ := args["query"].(string)
	if query == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_search: query is required",
		}
	}

	limit := intFromArgs(args, "limit", 20)
	kindStrs := stringSliceFromArgs(args, "kind")
	languages := stringSliceFromArgs(args, "language")

	var kinds []codegraph.NodeKind
	for _, k := range kindStrs {
		kinds = append(kinds, codegraph.NodeKind(k))
	}

	nodes, err := cgSvc.Search(query, kinds, languages, limit)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_search: %v", err),
		}
	}

	return textResult(formatSearchResults(nodes)), nil
}

// handleCodegraphContext processes a codegraph_context tool call.
func (h *handlers) handleCodegraphContext(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_context: invalid arguments: %s", err),
		}
	}

	symbol, _ := args["symbol"].(string)
	if symbol == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_context: symbol is required",
		}
	}

	depth := intFromArgs(args, "depth", 1)

	// Get the node detail (without source since we don't have rootDir in context).
	node, source, err := cgSvc.NodeDetail(symbol, "")
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_context: %v", err),
		}
	}

	// Get callers and callees at the requested depth.
	callers, _ := cgSvc.Callers(symbol, depth, 20)
	callees, _ := cgSvc.Callees(symbol, depth, 20)

	var sb strings.Builder
	sb.WriteString(formatNodeDetail(node, source))
	if len(callers) > 0 {
		sb.WriteString("\n")
		sb.WriteString(formatNodeList(callers, "Callers"))
	}
	if len(callees) > 0 {
		sb.WriteString("\n")
		sb.WriteString(formatNodeList(callees, "Callees"))
	}

	return textResult(sb.String()), nil
}

// handleCodegraphCallers processes a codegraph_callers tool call.
func (h *handlers) handleCodegraphCallers(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_callers: invalid arguments: %s", err),
		}
	}

	symbol, _ := args["symbol"].(string)
	if symbol == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_callers: symbol is required",
		}
	}

	depth := intFromArgs(args, "depth", 1)
	limit := intFromArgs(args, "limit", 20)

	nodes, err := cgSvc.Callers(symbol, depth, limit)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_callers: %v", err),
		}
	}

	return textResult(formatNodeList(nodes, "Callers of "+symbol)), nil
}

// handleCodegraphCallees processes a codegraph_callees tool call.
func (h *handlers) handleCodegraphCallees(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_callees: invalid arguments: %s", err),
		}
	}

	symbol, _ := args["symbol"].(string)
	if symbol == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_callees: symbol is required",
		}
	}

	depth := intFromArgs(args, "depth", 1)
	limit := intFromArgs(args, "limit", 20)

	nodes, err := cgSvc.Callees(symbol, depth, limit)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_callees: %v", err),
		}
	}

	return textResult(formatNodeList(nodes, "Callees of "+symbol)), nil
}

// handleCodegraphImpact processes a codegraph_impact tool call.
func (h *handlers) handleCodegraphImpact(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_impact: invalid arguments: %s", err),
		}
	}

	symbol, _ := args["symbol"].(string)
	if symbol == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_impact: symbol is required",
		}
	}

	depth := intFromArgs(args, "depth", 3)
	limit := intFromArgs(args, "limit", 50)

	nodes, err := cgSvc.Impact(symbol, depth, limit)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_impact: %v", err),
		}
	}

	return textResult(formatNodeList(nodes, "Impact of "+symbol)), nil
}

// handleCodegraphNode processes a codegraph_node tool call.
func (h *handlers) handleCodegraphNode(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_node: invalid arguments: %s", err),
		}
	}

	symbol, _ := args["symbol"].(string)
	if symbol == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_node: symbol is required",
		}
	}

	node, source, err := cgSvc.NodeDetail(symbol, "")
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_node: %v", err),
		}
	}

	return textResult(formatNodeDetail(node, source)), nil
}

// handleCodegraphExplore processes a codegraph_explore tool call.
func (h *handlers) handleCodegraphExplore(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_explore: invalid arguments: %s", err),
		}
	}

	symbols := stringSliceFromArgs(args, "symbols")
	if len(symbols) == 0 {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_explore: symbols is required",
		}
	}
	if len(symbols) > 10 {
		symbols = symbols[:10]
	}

	budget := intFromArgs(args, "budget", 30000)

	var sb strings.Builder
	for _, sym := range symbols {
		if sb.Len() >= budget {
			break
		}
		node, source, err := cgSvc.NodeDetail(sym, "")
		if err != nil {
			fmt.Fprintf(&sb, "## %s\nError: %v\n\n", sym, err)
			continue
		}
		sb.WriteString(formatNodeDetail(node, source))
		sb.WriteString("\n")
	}

	output := sb.String()
	if len(output) > budget {
		output = output[:budget]
	}

	return textResult(output), nil
}

// handleCodegraphTrace processes a codegraph_trace tool call.
func (h *handlers) handleCodegraphTrace(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle codegraph_trace: invalid arguments: %s", err),
		}
	}

	from, _ := args["from"].(string)
	to, _ := args["to"].(string)
	if from == "" || to == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle codegraph_trace: from and to are required",
		}
	}

	maxDepth := intFromArgs(args, "max_depth", 5)

	nodes, edges, err := cgSvc.Trace(from, to, maxDepth)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_trace: %v", err),
		}
	}

	return textResult(formatTrace(nodes, edges)), nil
}

// handleCodegraphStatus processes a codegraph_status tool call.
func (h *handlers) handleCodegraphStatus(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	stats, err := cgSvc.Status()
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_status: %v", err),
		}
	}

	return textResult(formatStats(stats)), nil
}

// handleCodegraphFiles processes a codegraph_files tool call.
func (h *handlers) handleCodegraphFiles(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	cgSvc, rpcErr := h.getCodeGraphService()
	if rpcErr != nil {
		return nil, rpcErr
	}

	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle codegraph_files: invalid arguments: %s", err),
			}
		}
	}

	pattern, _ := args["pattern"].(string)
	language, _ := args["language"].(string)

	files, err := cgSvc.Files(pattern, language)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle codegraph_files: %v", err),
		}
	}

	return textResult(formatFiles(files)), nil
}

// --- CODEGRAPH OUTPUT FORMATTERS ---

// formatSearchResults formats a list of nodes as a readable text list.
func formatSearchResults(nodes []codegraph.Node) string {
	if len(nodes) == 0 {
		return "No results found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d symbol(s):\n\n", len(nodes))
	for _, n := range nodes {
		fmt.Fprintf(&sb, "  %s %s  (%s:%d)\n", n.Kind, n.QualifiedName, n.FilePath, n.StartLine)
	}
	return sb.String()
}

// formatNodeList formats a list of nodes under a title heading.
func formatNodeList(nodes []codegraph.Node, title string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "### %s (%d)\n", title, len(nodes))
	if len(nodes) == 0 {
		sb.WriteString("  (none)\n")
		return sb.String()
	}
	for _, n := range nodes {
		fmt.Fprintf(&sb, "  %s %s  %s:%d\n", n.Kind, n.QualifiedName, n.FilePath, n.StartLine)
	}
	return sb.String()
}

// formatNodeDetail formats a single node's full information.
func formatNodeDetail(node *codegraph.Node, source string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## %s (%s)\n", node.QualifiedName, node.Kind)
	fmt.Fprintf(&sb, "File: %s:%d-%d\n", node.FilePath, node.StartLine, node.EndLine)
	fmt.Fprintf(&sb, "Language: %s\n", node.Language)
	if node.Signature != "" {
		fmt.Fprintf(&sb, "Signature: %s\n", node.Signature)
	}
	if node.Docstring != "" {
		fmt.Fprintf(&sb, "Doc: %s\n", node.Docstring)
	}
	if node.IsExported {
		sb.WriteString("Exported: yes\n")
	}
	if source != "" {
		sb.WriteString("\n```\n")
		sb.WriteString(source)
		sb.WriteString("\n```\n")
	}
	return sb.String()
}

// formatTrace formats a call path between two symbols.
func formatTrace(nodes []codegraph.Node, edges []codegraph.Edge) string {
	if len(nodes) == 0 {
		return "No path found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Call path (%d hops):\n\n", len(edges))
	for i, n := range nodes {
		if i > 0 {
			sb.WriteString("  -> ")
		} else {
			sb.WriteString("  ")
		}
		fmt.Fprintf(&sb, "%s (%s:%d)\n", n.QualifiedName, n.FilePath, n.StartLine)
	}
	return sb.String()
}

// formatStats formats graph statistics as a readable table.
func formatStats(stats *codegraph.GraphStats) string {
	var sb strings.Builder
	sb.WriteString("Code Graph Status\n")
	sb.WriteString("=================\n\n")
	fmt.Fprintf(&sb, "Nodes: %d\n", stats.NodeCount)
	fmt.Fprintf(&sb, "Edges: %d\n", stats.EdgeCount)
	fmt.Fprintf(&sb, "Files: %d\n", stats.FileCount)
	fmt.Fprintf(&sb, "DB Size: %d bytes\n", stats.DBSizeBytes)

	if len(stats.NodesByKind) > 0 {
		sb.WriteString("\nNodes by kind:\n")
		for k, v := range stats.NodesByKind {
			fmt.Fprintf(&sb, "  %s: %d\n", k, v)
		}
	}
	if len(stats.EdgesByKind) > 0 {
		sb.WriteString("\nEdges by kind:\n")
		for k, v := range stats.EdgesByKind {
			fmt.Fprintf(&sb, "  %s: %d\n", k, v)
		}
	}
	if len(stats.FilesByLanguage) > 0 {
		sb.WriteString("\nFiles by language:\n")
		for k, v := range stats.FilesByLanguage {
			fmt.Fprintf(&sb, "  %s: %d\n", k, v)
		}
	}
	return sb.String()
}

// formatFiles formats a list of file records.
func formatFiles(files []codegraph.FileRecord) string {
	if len(files) == 0 {
		return "No files indexed."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Indexed files (%d):\n\n", len(files))
	for _, f := range files {
		fmt.Fprintf(&sb, "  %s  [%s] %d nodes\n", f.Path, f.Language, f.NodeCount)
	}
	return sb.String()
}

// --- MODEL HANDLERS (SPEC-038) ---

// handleModelList processes a model_list tool call.
func (h *handlers) handleModelList(ctx context.Context, _ json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.modelsSvc == nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: "mcp: model_list: models service unavailable",
		}
	}
	resp, err := h.modelsSvc.List(ctx)
	if err != nil {
		return nil, h.mapServiceError("model_list", err)
	}
	return resultFromAny(resp)
}

// handleModelSet processes a model_set tool call.
func (h *handlers) handleModelSet(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.modelsSvc == nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: "mcp: model_set: models service unavailable",
		}
	}
	var req service.ModelSetRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: model_set: invalid arguments: %s", err),
		}
	}
	resp, err := h.modelsSvc.Set(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("model_set", err)
	}
	return resultFromAny(resp)
}

// handleModelReset processes a model_reset tool call.
func (h *handlers) handleModelReset(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.modelsSvc == nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: "mcp: model_reset: models service unavailable",
		}
	}
	var req service.ModelResetRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: model_reset: invalid arguments: %s", err),
		}
	}
	resp, err := h.modelsSvc.Reset(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("model_reset", err)
	}
	return resultFromAny(resp)
}

// handleInit processes an init tool call. It applies managed blocks to CLAUDE.md
// files and runs drift detection. When check=true, it is report-only (no writes).
func (h *handlers) handleInit(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: "mcp: handle init: SDD service unavailable",
		}
	}

	var args struct {
		RepoRoot string `json:"repo_root"`
		Check    bool   `json:"check"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle init: invalid arguments: %s", err),
		}
	}

	repoRoot := args.RepoRoot
	if repoRoot == "" {
		var err error
		repoRoot, err = os.Getwd()
		if err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInternalError,
				Message: fmt.Sprintf("mcp: handle init: getwd: %s", err),
			}
		}
	}

	// Load config for workflow dir.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: handle init: home dir: %s", err),
		}
	}
	cfg, cfgErr := config.Load(filepath.Join(home, ".mneme", "config.toml"))
	if cfgErr != nil {
		cfg = config.Default()
	}

	opts := service.InitServiceOptions{}
	if !args.Check {
		opts.UpsertBlock = install.UpsertManagedBlock
		opts.ManualContent = install.OperatingManual
	}

	initSvc := service.NewInitService(cfg, h.sdd, h.svc, h.svc.ProjectSlug(), opts)

	type initResult struct {
		RepoRoot      string   `json:"repo_root"`
		CheckMode     bool     `json:"check_mode"`
		ManualApplied bool     `json:"manual_applied"`
		RepoBlock     bool     `json:"repo_block_applied"`
		DriftFindings []string `json:"drift_findings"`
	}

	result := initResult{
		RepoRoot:  repoRoot,
		CheckMode: args.Check,
	}

	// Greenfield scaffold.
	if !args.Check {
		_ = initSvc.EnsureGreenfieldScaffold(repoRoot)
	}

	// Global manual.
	if !args.Check {
		if err := initSvc.EnsureGlobalManual(); err != nil {
			h.logger.Warn("mcp: init: ensure global manual", "error", err)
		} else {
			result.ManualApplied = true
		}
	}

	// Repo block.
	if !args.Check {
		if err := initSvc.UpsertRepoBlock(repoRoot); err != nil {
			h.logger.Warn("mcp: init: upsert repo block", "error", err)
		} else {
			result.RepoBlock = true
		}
	}

	// Drift detection.
	findings, driftErr := initSvc.RunDrift(repoRoot)
	if driftErr != nil {
		h.logger.Warn("mcp: init: drift", "error", driftErr)
	}
	for _, f := range findings {
		result.DriftFindings = append(result.DriftFindings, f.String())
	}

	_ = ctx
	return resultFromAny(result)
}
