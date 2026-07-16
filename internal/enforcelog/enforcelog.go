// Package enforcelog records local, privacy-scoped telemetry for the
// delegation-enforcement hook's subagent containment decisions (SPEC-086 D3):
// the "warn -> block" rollout (D6/D7) needs evidence of what WOULD have been
// blocked, per role and per reason, before a project can safely flip its
// containment mode to "block".
//
// This package is deliberately NOT internal/querylog, despite the identical
// JSONL/rotation/leaf shape: querylog's own godoc is a published privacy
// promise ("NEVER records a file path, a shell command, or a search query")
// that this telemetry must violate on purpose — a containment decision is
// meaningless without the target path, the caller's role, and (for a block)
// the reason and the owning role. enforcelog therefore carries its own,
// explicit privacy contract instead of stretching querylog's:
//
//   - Local only: the file lives under the mneme data directory (a caller-
//     resolved path, this package never touches config) with 0o600
//     permissions, exactly like querylog.
//   - Never transmitted: no network call exists in this package or its
//     callers.
//   - Never shared to the team vault: enforcement events are operational
//     noise, not durable knowledge — nothing in this package creates a
//     memory, so SPEC-071's auto-share (which only applies to project-scoped
//     memory types) never sees these events. The caller is additionally
//     responsible for keeping the log path out of a git-tracked directory
//     (.gitignore'd), the same posture querylog's on-disk file already has.
//
// Like internal/querylog, this is a leaf: standard library only, no
// knowledge of model/store/config/service. The on-disk path is resolved by
// the caller and passed in.
package enforcelog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultMaxBytes is the rotation threshold applied by callers that do not
// pick their own cap (mirrors querylog.DefaultMaxBytes): once an append
// pushes the log past this size it is rotated to "<path>.1".
const DefaultMaxBytes int64 = 5 << 20

// rotatedSuffix names the single retained backup produced by rotation.
const rotatedSuffix = ".1"

// Decision classifies the outcome of a single containment evaluation.
type Decision string

const (
	// DecisionAllow means the invocation was allowed (whitelisted path, no
	// manifest, role absent from the manifest, unresolved role, incomplete
	// areas data, or a path inside the role's declared areas).
	DecisionAllow Decision = "allow"

	// DecisionWouldBlock means the path fell outside the role's certified
	// (areas_complete=true) areas, but containment mode was "warn" (or the
	// data was not yet certified complete): the event is recorded as
	// evidence for the D7 promotion gate, but the tool call was allowed.
	DecisionWouldBlock Decision = "would_block"

	// DecisionBlock means containment mode was "block" and the invocation
	// was actually rejected (exit 2).
	DecisionBlock Decision = "block"
)

// Event is a single containment-evaluation record, serialised as one JSON
// object per line.
type Event struct {
	// TS is the event time in UTC, RFC3339.
	TS time.Time `json:"ts"`
	// Session is Claude Code's opaque session identifier, when available.
	Session string `json:"session,omitempty"`
	// Project is the project slug the event belongs to.
	Project string `json:"project"`
	// Caller is "subagent" or "orchestrator".
	Caller string `json:"caller"`
	// AgentID is the opaque agent_id from the PreToolUse payload, when present.
	AgentID string `json:"agent_id,omitempty"`
	// Role is the resolved role (agent_type), when known.
	Role string `json:"role,omitempty"`
	// RoleSource is "payload", "unresolved", or "n/a" (CallerIdentity.RoleSource).
	RoleSource string `json:"role_source,omitempty"`
	// Tool is the tool name (Edit/Write/MultiEdit/NotebookEdit/Bash).
	Tool string `json:"tool"`
	// Target is the project-relative path the invocation touched.
	Target string `json:"target,omitempty"`
	// Decision is the containment outcome (allow/would_block/block).
	Decision Decision `json:"decision"`
	// Mode is the containment mode in effect for this project (off/warn/block).
	Mode string `json:"mode,omitempty"`
	// Reason is a short machine label explaining the decision.
	Reason string `json:"reason,omitempty"`
	// Owner is the role whose declared areas actually cover Target, when
	// known — named in the rendered message so the caller sees who to
	// delegate to, independent of which role is being contained.
	Owner string `json:"owner,omitempty"`
}

// Append serialises ev as one JSON line and appends it to the file at path
// (created with 0o600 if absent), rotating to "<path>.1" when the resulting
// file exceeds maxBytes. A non-positive maxBytes disables rotation.
//
// Append is best-effort telemetry: callers are expected to ignore the
// returned error silently (fail-open), matching querylog.Append.
func Append(path string, ev Event, maxBytes int64) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("enforcelog: marshal event: %w", err)
	}
	line = append(line, '\n')

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("enforcelog: create dir %s: %w", dir, err)
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("enforcelog: open %s: %w", path, err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("enforcelog: append to %s: %w", path, err)
	}
	info, statErr := f.Stat()
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("enforcelog: close %s: %w", path, closeErr)
	}

	if statErr == nil && maxBytes > 0 && info.Size() > maxBytes {
		if err := os.Rename(path, path+rotatedSuffix); err != nil {
			return fmt.Errorf("enforcelog: rotate %s: %w", path, err)
		}
	}
	return nil
}

// Read parses every event from the rotated backup ("<path>.1", if present)
// followed by the live file at path, returning them in chronological order.
// Corrupt or blank lines are skipped. A missing file is not an error.
func Read(path string) ([]Event, error) {
	var events []Event
	for _, p := range []string{path + rotatedSuffix, path} {
		evs, err := readFile(p)
		if err != nil {
			return events, err
		}
		events = append(events, evs...)
	}
	return events, nil
}

func readFile(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("enforcelog: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue // skip corrupt line
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return events, fmt.Errorf("enforcelog: scan %s: %w", path, err)
	}
	return events, nil
}

// RoleReport aggregates would_block/block counts and areas_complete/mode
// evidence for a single role over a report window (SPEC-086 D7's promotion
// gate reads exactly this).
type RoleReport struct {
	Role        string   `json:"role"`
	WouldBlock  int      `json:"would_block"`
	Blocked     int      `json:"blocked"`
	Allowed     int      `json:"allowed"`
	SamplePaths []string `json:"sample_paths,omitempty"`
}

// Report is the aggregate containment-adoption metric over a time window.
type Report struct {
	// Since is the lower bound of the window events were aggregated over.
	Since time.Time `json:"since"`
	// Total is the total number of events in the window.
	Total int `json:"total"`
	// Unresolved is the count of events with RoleSource=="unresolved" (D2's
	// health counter — a rising count means agent_type stopped arriving).
	Unresolved int `json:"unresolved"`
	// ByRole aggregates per-role counts, sorted by role name ascending.
	ByRole []RoleReport `json:"by_role"`
}

// maxSamplePaths caps the number of example paths retained per role in a
// Report, so a busy project's report stays small.
const maxSamplePaths = 5

// Aggregate computes the containment Report over events at or after since.
func Aggregate(events []Event, since time.Time) Report {
	report := Report{Since: since}
	roles := make(map[string]*RoleReport)
	roleOrder := make([]string, 0)

	roleFor := func(name string) *RoleReport {
		rr, ok := roles[name]
		if !ok {
			rr = &RoleReport{Role: name}
			roles[name] = rr
			roleOrder = append(roleOrder, name)
		}
		return rr
	}

	for _, ev := range events {
		if ev.TS.Before(since) {
			continue
		}
		report.Total++
		if ev.RoleSource == "unresolved" {
			report.Unresolved++
		}
		if ev.Role == "" {
			continue
		}
		rr := roleFor(ev.Role)
		switch ev.Decision {
		case DecisionWouldBlock:
			rr.WouldBlock++
			if len(rr.SamplePaths) < maxSamplePaths && ev.Target != "" {
				rr.SamplePaths = append(rr.SamplePaths, ev.Target)
			}
		case DecisionBlock:
			rr.Blocked++
		case DecisionAllow:
			rr.Allowed++
		}
	}

	for _, name := range roleOrder {
		report.ByRole = append(report.ByRole, *roles[name])
	}
	return report
}
