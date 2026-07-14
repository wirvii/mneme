// Package querylog records privacy-preserving code graph adoption telemetry as
// an append-only JSONL file. It exists to answer a single question — "when an
// agent could have used the code graph, did it?" — so the nudge (SPEC-044) and
// the codegraph-first prompt policy (SPEC-045) can be measured and iterated on
// (SPEC-083, workstream W1).
//
// Privacy is a hard requirement (SPEC-083 D-owner-2): an event carries ONLY the
// name of the tool involved (and, for Bash, the normalised executable head as
// "bash:<cmd>"). It NEVER records a file path, a shell command, or a search
// query. The file lives under the mneme data directory with 0o600 permissions
// and is never transmitted anywhere.
//
// This is a leaf package: it imports the standard library only and knows nothing
// about model, store, config, or service. The on-disk path is resolved by the
// caller (an adapter) and passed in — the leaf never touches configuration.
package querylog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DefaultMaxBytes is the rotation threshold applied by callers that do not want
// to pick their own cap: once an append pushes the log past this size it is
// rotated to "<path>.1" (SPEC-083 D7). 5 MiB keeps a meaningful window of
// events while bounding disk growth.
const DefaultMaxBytes int64 = 5 << 20

// rotatedSuffix is appended to the log path to name the single retained backup
// produced by rotation. Read consumes "<path>.1" before "<path>" so events are
// returned in chronological order.
const rotatedSuffix = ".1"

// Kind classifies a telemetry event.
type Kind string

const (
	// KindOpportunity records that an agent explored code with a generic
	// read/search tool (Read/Grep/Glob or a Bash code-search command) on a
	// project that HAS an indexed code graph — i.e. it could have queried the
	// graph but chose not to. Logged by the pre-tool-use hook.
	KindOpportunity Kind = "opportunity"

	// KindUse records that an agent called a codegraph_* MCP tool. Logged by the
	// MCP dispatch, which is authoritative (the tool actually ran) and excludes
	// human CLI use of the code graph.
	KindUse Kind = "use"
)

// Event is a single telemetry record. It is serialised as one JSON object per
// line. Only tool names are ever stored — no paths, commands, or queries.
type Event struct {
	// TS is the event time in UTC, serialised as RFC3339.
	TS time.Time `json:"ts"`
	// Session is Claude Code's opaque session identifier, when available. It
	// identifies neither the machine nor the user and is omitted for MCP-side
	// events (the server has no session id).
	Session string `json:"session,omitempty"`
	// Project is the project slug the event belongs to.
	Project string `json:"project"`
	// Kind is the event class (opportunity or use).
	Kind Kind `json:"kind"`
	// Tool is the tool name: "Read"/"Grep"/"Glob"/"bash:<cmd>" for opportunities,
	// "codegraph_search"/... for uses.
	Tool string `json:"tool"`
	// Source is the writer: "hook" (opportunity) or "mcp" (use).
	Source string `json:"source"`
}

// Append serialises ev as one JSON line and appends it to the file at path
// (created with 0o600 if absent). When the resulting file exceeds maxBytes it is
// rotated to "<path>.1" (overwriting any previous backup); the next append
// re-creates a fresh file lazily. A non-positive maxBytes disables rotation.
//
// Append is best-effort telemetry: callers are expected to ignore the returned
// error silently (fail-open). No locking is used — a single POSIX O_APPEND write
// of a sub-4 KiB line is atomic, which is sufficient for the two low-frequency
// writers (the ephemeral hook process and the MCP server).
func Append(path string, ev Event, maxBytes int64) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("querylog: marshal event: %w", err)
	}
	line = append(line, '\n')

	// Ensure the parent directory exists — the slug may contain a "/" that maps
	// to a sub-directory (e.g. projects/wirvii/) which is created lazily.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("querylog: create dir %s: %w", dir, err)
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("querylog: open %s: %w", path, err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("querylog: append to %s: %w", path, err)
	}
	info, statErr := f.Stat()
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("querylog: close %s: %w", path, closeErr)
	}

	if statErr == nil && maxBytes > 0 && info.Size() > maxBytes {
		if err := os.Rename(path, path+rotatedSuffix); err != nil {
			return fmt.Errorf("querylog: rotate %s: %w", path, err)
		}
	}
	return nil
}

// Read parses every event from the rotated backup ("<path>.1", if present)
// followed by the live file at path, returning them in chronological order.
// Corrupt or blank lines are skipped rather than aborting the read — a partial
// telemetry file must never break the adoption report. A missing file is not an
// error (returns an empty slice).
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

// readFile parses one JSONL file, skipping corrupt lines. A non-existent file
// yields (nil, nil) so a first-ever read (or an absent backup) is not an error.
func readFile(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("querylog: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var events []Event
	sc := bufio.NewScanner(f)
	// Allow long lines (defensive) while keeping a bounded buffer.
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
		return events, fmt.Errorf("querylog: scan %s: %w", path, err)
	}
	return events, nil
}

// ToolCount pairs a tool name with how many times it appeared in a report
// window. Used for the "top tools" breakdowns.
type ToolCount struct {
	Tool  string `json:"tool"`
	Count int    `json:"count"`
}

// Report is the aggregate adoption metric over a time window (SPEC-083 D8).
type Report struct {
	// Since is the lower bound of the window events were aggregated over.
	Since time.Time `json:"since"`
	// Uses is the number of code graph tool calls (kind=use) in the window.
	Uses int `json:"uses"`
	// Opportunities is the number of generic read/search calls (kind=opportunity)
	// on an indexed project in the window.
	Opportunities int `json:"opportunities"`
	// AdoptionRatio is Uses / (Uses + Opportunities); 0 when the denominator is 0.
	// It rises as agents choose the graph over generic exploration.
	AdoptionRatio float64 `json:"adoption_ratio"`
	// TopUseTools are the most frequently used codegraph_* tools, descending.
	TopUseTools []ToolCount `json:"top_use_tools"`
	// TopMissedTools are the most frequent opportunity tools (Read/Grep/Glob/
	// bash:*), descending — the patterns to target next.
	TopMissedTools []ToolCount `json:"top_missed_tools"`
}

// Aggregate computes the adoption Report over the events whose timestamp is at
// or after since. Events before since are ignored so a stale on-disk backlog
// does not distort a recent window.
func Aggregate(events []Event, since time.Time) Report {
	report := Report{Since: since}
	useCounts := make(map[string]int)
	missedCounts := make(map[string]int)

	for _, ev := range events {
		if ev.TS.Before(since) {
			continue
		}
		switch ev.Kind {
		case KindUse:
			report.Uses++
			useCounts[ev.Tool]++
		case KindOpportunity:
			report.Opportunities++
			missedCounts[ev.Tool]++
		}
	}

	if den := report.Uses + report.Opportunities; den > 0 {
		report.AdoptionRatio = float64(report.Uses) / float64(den)
	}
	report.TopUseTools = topCounts(useCounts)
	report.TopMissedTools = topCounts(missedCounts)
	return report
}

// topCounts flattens a tool→count map into a slice sorted by count descending,
// breaking ties by tool name ascending so the output is deterministic.
func topCounts(counts map[string]int) []ToolCount {
	if len(counts) == 0 {
		return nil
	}
	out := make([]ToolCount, 0, len(counts))
	for tool, count := range counts {
		out = append(out, ToolCount{Tool: tool, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}
