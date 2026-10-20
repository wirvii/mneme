package cli

import (
	"fmt"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/enforcement"
	"github.com/wirvii/mneme/internal/laneaudit"
)

// trivialInlineTools are the only tools whose block the trivial-inline
// permission may lift (SPEC-171 D2). Bash and NotebookEdit are deliberately
// absent: a shell redirection or `sed -i` cannot be matched to one file with
// the certainty a direct file-tool call has.
var trivialInlineTools = map[string]bool{
	"Edit":      true,
	"Write":     true,
	"MultiEdit": true,
}

// trivialScopeLookup returns the declared scope globs of every open trivial
// spec. An error means "unknown", which callers treat as "grant nothing".
type trivialScopeLookup func() ([]string, error)

// newTrivialScopeLookup builds the production lookup. It is a package
// variable only so a test can swap in a counting spy and prove the database
// is never consulted when the feature is off.
var newTrivialScopeLookup = trivialScopesFromDB

// trivialScopesQuery selects the scope of every trivial-lane spec that is
// open for implementation. Only `implementing` counts: after a failed
// lane_audit the spec returns there (and the permission with it), while in
// `audit` nobody edits. The backlog clause mirrors service.specFreeze on
// purpose — a spec whose originating item was archived or no longer exists
// is frozen and must not grant anything. specs has a composite key
// (project, id), so project is always filtered.
const trivialScopesQuery = `
SELECT s.scope FROM specs s
WHERE s.project = ? AND s.lane = 'trivial' AND s.status = 'implementing' AND s.scope <> ''
  AND (COALESCE(s.backlog_id, '') = ''
       OR EXISTS (SELECT 1 FROM backlog_items b
                  WHERE b.project = s.project AND b.id = s.backlog_id AND b.status <> 'archived'))`

// trivialScopesFromDB returns a lookup that opens the project database
// read-only (the hook never starts the service layer, same reason as
// queryManifestContent), reads the open trivial scopes and closes it. A
// missing database is an error, not an empty answer, so the caller cannot
// confuse "no permission" with "could not tell".
func trivialScopesFromDB(path, project string) trivialScopeLookup {
	return func() ([]string, error) {
		database, err := db.OpenReadOnly(path)
		if err != nil {
			return nil, fmt.Errorf("cli: trivial scopes: open %s: %w", path, err)
		}
		defer database.Close() //nolint:errcheck // cleanup path; error is not actionable

		rows, err := database.Query(trivialScopesQuery, project)
		if err != nil {
			return nil, fmt.Errorf("cli: trivial scopes: query: %w", err)
		}
		defer rows.Close() //nolint:errcheck // cleanup path; error is not actionable

		var scopes []string
		for rows.Next() {
			var scope string
			if err := rows.Scan(&scope); err != nil {
				return nil, fmt.Errorf("cli: trivial scopes: scan: %w", err)
			}
			scopes = append(scopes, scope)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("cli: trivial scopes: rows: %w", err)
		}
		return scopes, nil
	}
}

// withTrivialInline wraps the orchestrator's ownership check (SPEC-171 D2).
// It asks base first and only when base blocks does it consult lookup — once
// per call, memoised — and lift the block if the target sits inside an open
// trivial spec's scope and outside the trivial lane's forbidden globs. Every
// other outcome, including any lookup error, returns exactly what base
// returned, so the new permission can only ever fail closed. onGrant, when
// non-nil, receives the cwd-relative path of each lifted block.
//
// Paths are matched relative to cwd with laneaudit.MatchGlobs, the same
// function lane_audit uses to decide "out of scope", so what the hook allows
// and what the audit later accepts cannot disagree. A cwd below the
// repository root yields paths that do not match root-relative scopes, which
// blocks: the safe direction.
func withTrivialInline(base enforcement.OwnershipFunc, lookup trivialScopeLookup, cwd string, onGrant func(rel string)) enforcement.OwnershipFunc {
	var (
		loaded  bool
		scopes  []string
		lookErr error
	)
	return func(target string) (bool, string) {
		block, owner := base(target)
		if !block {
			return block, owner
		}
		rel, outOfTree := normalisePathForOwnership(target, cwd)
		if outOfTree || rel == "" {
			return block, owner
		}
		if laneaudit.MatchGlobs(rel, laneaudit.DefaultTrivialBudget.ForbiddenGlobs) {
			return block, owner
		}
		if !loaded {
			scopes, lookErr = lookup()
			loaded = true
		}
		if lookErr != nil || !laneaudit.MatchGlobs(rel, scopes) {
			return block, owner
		}
		if onGrant != nil {
			onGrant(rel)
		}
		return false, ""
	}
}

// withTrivialInlineIfEnabled is evaluateDelegation's single hook into this
// feature: it wraps own only for the orchestrator, only for the file tools in
// trivialInlineTools, and only when the project opted in. In every other case
// it returns own untouched and builds no lookup at all, so a project that
// never opts in pays nothing.
func withTrivialInlineIfEnabled(own enforcement.OwnershipFunc, input hookPreToolInput, identity CallerIdentity, cfg *config.Config, slug, cwd string) enforcement.OwnershipFunc {
	if identity.IsSubagent || !trivialInlineTools[input.ToolName] || !cfg.OrchestratorTrivialInlineEnabled(slug) {
		return own
	}
	lookup := newTrivialScopeLookup(cfg.ProjectDBPath(slug), slug)
	return withTrivialInline(own, lookup, cwd, func(rel string) {
		logTrivialInlineGrantEvent(cfg, slug, input, rel)
	})
}

// logTrivialInlineGrantEvent records, best-effort, that the orchestrator was
// let through by an open trivial spec's scope, so the use of the permission
// can be audited. A write failure is ignored: telemetry never affects the
// decision.
func logTrivialInlineGrantEvent(cfg *config.Config, slug string, input hookPreToolInput, rel string) {
	ev := enforcelog.Event{
		TS:       time.Now().UTC(),
		Session:  input.SessionID,
		Project:  slug,
		Caller:   "orchestrator",
		Tool:     input.ToolName,
		Target:   rel,
		Decision: enforcelog.DecisionAllow,
		Reason:   "trivial_inline_scope",
	}
	_ = enforcelog.Append(enforcelogPath(cfg.Storage.DataDir, slug), ev, enforcelog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
}
