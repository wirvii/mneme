package laneaudit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/wirvii/mneme/internal/gitrepo"
	"github.com/wirvii/mneme/internal/risk"
)

// Breach is a single human-readable trivial-lane audit failure — a named
// string type, not a bare string, so a caller cannot accidentally pass an
// unrelated string where a Breach is expected. Breach texts are persisted
// verbatim in lane_audits.breaches and read back by lane_status and
// lane_stats, so their prefixes are part of the contract.
type Breach string

// Breach prefixes. IsRiskBreach and lane_stats classify persisted rows by
// these, so they never change once written.
const (
	prefixOutOfScope     = "out of scope: "
	prefixRiskItem       = "risk item "
	prefixRiskDeclared   = "risk declared: "
	prefixContradicts    = "risk declaration contradicts"
	prefixNotDeclared    = "risk not declared"
	prefixMalformed      = "risk declaration malformed: "
	legacyForbiddenPath  = "forbidden path modified: "
	legacyPublicSymbol   = "public symbol changed: "
	notDeclaredBreachMsg = prefixNotDeclared + `: the implementer or the orchestrator adds "Risk: none" to tarea.md section entrega (spec_doc_write kind task section entrega)`
)

// Input is everything the trivial-lane auditor looks at: the facts git
// already collected, the declared scope, the risk path table and the
// declaration the spec carries.
type Input struct {
	// Files are the changed files with their added/removed line counts.
	// The counts are recorded by callers but never decide a breach.
	Files []gitrepo.FileStat
	// Symbols is the exported-symbol delta between base and HEAD.
	Symbols SymbolDelta
	// Scope is the declared scope glob; empty skips the scope check.
	Scope string
	// Table holds the path patterns of risk items 1, 2 and 5; nil falls
	// back to risk.DefaultTable() so a caller can never audit with fewer
	// patterns than the defaults.
	Table risk.Table
	// Declaration is the spec's "Risk:" declaration; nil means none was
	// found.
	Declaration *risk.Declaration
	// DeclErr, when not nil, means the declaration was malformed; it wins
	// over Declaration.
	DeclErr error
}

// Evaluate is the trivial lane's auditor (SPEC-184): it checks only what
// the diff shows, never how big it is. Breaches come in a fixed order —
// scope, risk paths, exported symbols, declaration — so the same diff and
// declaration always yield the same strings.
//
// In the trivial lane any detected risk item already fails the audit,
// whatever was declared; the declaration block adds the separate signal of
// an inconsistent "Risk: none", which is what lane_stats needs to measure
// false declarations. The number of files and lines appears in no breach.
func Evaluate(in Input) []Breach {
	table := in.Table
	if table == nil {
		table = risk.DefaultTable()
	}

	var breaches []Breach

	if in.Scope != "" {
		for _, f := range in.Files {
			if !MatchGlobs(f.Path, []string{in.Scope}) {
				breaches = append(breaches, Breach(prefixOutOfScope+f.Path))
			}
		}
	}

	detected := make(map[risk.Item]bool)
	for _, f := range in.Files {
		for _, item := range RiskItemsForPath(f.Path, table) {
			detected[item] = true
			breaches = append(breaches, Breach(fmt.Sprintf("%s%d: path %s", prefixRiskItem, int(item), f.Path)))
		}
	}

	for _, group := range [][]Symbol{in.Symbols.Created, in.Symbols.Deleted} {
		for _, s := range group {
			if !s.Exported {
				continue
			}
			detected[risk.ItemContracts] = true
			breaches = append(breaches, Breach(fmt.Sprintf("%s%d: %s%s in %s",
				prefixRiskItem, int(risk.ItemContracts), legacyPublicSymbol, s.QualifiedName, s.File)))
		}
	}

	return append(breaches, declarationBreaches(in, detected)...)
}

// declarationBreaches is Evaluate's fourth block: the declaration must
// exist, parse, say none, and agree with what the diff showed.
func declarationBreaches(in Input, detected map[risk.Item]bool) []Breach {
	switch {
	case in.DeclErr != nil:
		line := in.DeclErr.Error()
		if in.Declaration != nil && in.Declaration.Line != "" {
			line = strings.TrimSpace(in.Declaration.Line)
		}
		return []Breach{Breach(prefixMalformed + line)}
	case in.Declaration == nil:
		return []Breach{Breach(notDeclaredBreachMsg)}
	case len(in.Declaration.Items) > 0:
		out := make([]Breach, 0, len(in.Declaration.Items))
		for _, item := range in.Declaration.Items {
			out = append(out, Breach(fmt.Sprintf("%sitem %d; the trivial lane admits only Risk: none", prefixRiskDeclared, int(item))))
		}
		return out
	case in.Declaration.None && len(detected) > 0:
		items := make([]risk.Item, 0, len(detected))
		for item := range detected {
			items = append(items, item)
		}
		return []Breach{Breach(fmt.Sprintf("%s diff: Risk: none declared, items %s detected", prefixContradicts, joinItems(items)))}
	}
	return nil
}

// joinItems renders items sorted and comma-separated ("1, 5"), the one
// format breaches, RiskDeclared and the remedy text share.
func joinItems(items []risk.Item) string {
	sorted := append([]risk.Item(nil), items...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	parts := make([]string, len(sorted))
	for i, item := range sorted {
		parts[i] = strconv.Itoa(int(item))
	}
	return strings.Join(parts, ", ")
}

// RiskItemsForPath returns the path-detectable items (in order 1, 2, 5)
// whose patterns in t match path. A path may match several items.
func RiskItemsForPath(path string, t risk.Table) []risk.Item {
	var out []risk.Item
	for _, item := range risk.Items() {
		if !item.PathDetectable() {
			continue
		}
		if MatchGlobs(path, t[item]) {
			out = append(out, item)
		}
	}
	return out
}

// IsRiskBreach reports whether a persisted breach text names a risk item
// — detected, declared, or contradicted. It is defined by extension over
// prefixes so lane_stats can classify rows written before SPEC-184 too:
// the legacy "forbidden path modified: " and "public symbol changed: "
// were in fact items 1/5 and 3. A missing or malformed declaration fails
// an audit but is not a risk breach.
func IsRiskBreach(b string) bool {
	for _, p := range []string{prefixRiskItem, prefixRiskDeclared, prefixContradicts, legacyForbiddenPath, legacyPublicSymbol} {
		if strings.HasPrefix(b, p) {
			return true
		}
	}
	return false
}

// IsCatchAllScope reports whether glob cannot serve as a trivial scope:
// it is empty, uses braces (which alternate paths and hide which segment
// is literal), or has no path segment free of the wildcards *, ? and [.
// With the orchestrator's inline permission on by default, such a scope
// would unlock the whole tree, so creation, spec_quick and the inline hook
// all refuse it.
func IsCatchAllScope(glob string) bool {
	if strings.TrimSpace(glob) == "" || strings.ContainsAny(glob, "{}") {
		return true
	}
	for _, seg := range strings.Split(glob, "/") {
		if seg != "" && !strings.ContainsAny(seg, "*?[") {
			return false
		}
	}
	return true
}
