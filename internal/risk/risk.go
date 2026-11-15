package risk

import (
	"errors"
	"fmt"
	"strings"
)

// Item is one entry of the closed list of six risk items. The numbers are
// the contract: deliveries, the lane auditor and the role denials all
// exchange them, so an Item is never renumbered.
type Item int

// The six risk items, in the order and with the numbers of
// mneme-flow/references/risk.md.
const (
	// ItemIrreversibleData covers schema changes, migrations and persisted
	// formats: anything a rollback cannot undo.
	ItemIrreversibleData Item = 1
	// ItemSecurity covers authentication, permissions, secrets, guards and
	// sandboxing.
	ItemSecurity Item = 2
	// ItemContracts covers public API, exported symbols, CLI flags, MCP
	// tools, configuration keys and file formats other tools read.
	ItemContracts Item = 3
	// ItemConcurrency covers locks, goroutines, shared state, ordering and
	// retries.
	ItemConcurrency Item = 4
	// ItemDelivery covers installers, release, CI, deploy and dependencies.
	ItemDelivery Item = 5
	// ItemUntestable marks a change no existing test would catch.
	ItemUntestable Item = 6
)

// ErrItemNotPathDetectable is returned by Table.With when extra patterns
// name an item a path cannot reveal (3, 4, 6) or a number outside 1..6:
// only items 1, 2 and 5 carry path patterns.
var ErrItemNotPathDetectable = errors.New("risk: item is not detectable by path; accepted: 1, 2, 5")

// ErrEmptyGlob is returned by Table.With for an empty or blank pattern,
// which would otherwise match nothing and silently hide a configuration
// mistake.
var ErrEmptyGlob = errors.New("risk: empty glob")

// itemTitles are the exact bold headings of mneme-flow/references/risk.md;
// internal/install's drift test compares them byte for byte.
var itemTitles = map[Item]string{
	ItemIrreversibleData: "Irreversible data or migrations",
	ItemSecurity:         "Security and permissions",
	ItemContracts:        "Contracts others consume",
	ItemConcurrency:      "Concurrency",
	ItemDelivery:         "Delivery or environment",
	ItemUntestable:       "No test would catch the regression",
}

// pathDetectableItems is the order Globs and RiskItemsForPath report in.
var pathDetectableItems = []Item{ItemIrreversibleData, ItemSecurity, ItemDelivery}

// Items returns the six items in order. It returns a new slice on every
// call so a caller can never mutate the shared list.
func Items() []Item {
	return []Item{
		ItemIrreversibleData, ItemSecurity, ItemContracts,
		ItemConcurrency, ItemDelivery, ItemUntestable,
	}
}

// Valid reports whether i is one of the six items. Declarations and the
// SPEC-182 delivery block reject any other number.
func (i Item) Valid() bool {
	_, ok := itemTitles[i]
	return ok
}

// Title returns the item's heading exactly as risk.md writes it, so
// messages and the drift test quote the same words agents read. An invalid
// item returns "".
func (i Item) Title() string {
	return itemTitles[i]
}

// PathDetectable reports whether a changed path alone can reveal the item.
// Only items 1, 2 and 5 can; 3 is partly visible from exported symbols,
// and 4 and 6 are always declared, never detected.
func (i Item) PathDetectable() bool {
	for _, p := range pathDetectableItems {
		if p == i {
			return true
		}
	}
	return false
}

// Table maps each path-detectable item to its glob patterns.
type Table map[Item][]string

// DefaultTable returns a fresh copy of the default path patterns of items
// 1, 2 and 5. Go has no constant slices, so a copy per call is what keeps a
// caller from mutating the defaults every other caller relies on.
func DefaultTable() Table {
	return Table{
		ItemIrreversibleData: {"**/*.sql", "**/migrations/**", "**/schema.*"},
		ItemSecurity: {
			"**/.env", "**/.env.*", "**/*secret*", "**/*secret*/**",
			"**/*credential*", "**/*credential*/**", "**/*.pem", "**/*.key", "**/*.p12",
		},
		ItemDelivery: {
			"cmd/**", "internal/install/assets/**", ".github/workflows/**", "**/deploy/**",
			"**/Dockerfile*", "**/release*", "**/.goreleaser*", "**/go.mod", "**/go.sum",
			"**/package.json", "**/package-lock.json", "**/pnpm-lock.yaml", "**/yarn.lock",
			"**/bun.lockb", "**/Cargo.lock", "**/poetry.lock", "**/uv.lock",
			"**/Gemfile.lock", "**/composer.lock",
		},
	}
}

// With returns the union of t and extra: every pattern of t first, then
// extra's new patterns in the order given, without duplicates. It never
// removes a pattern — a project may widen what counts as risky, never
// narrow it. It is the only place that decides which items accept
// patterns, so configuration validation calls it instead of keeping a
// second list.
func (t Table) With(extra Table) (Table, error) {
	for item, globs := range extra {
		if !item.PathDetectable() {
			return nil, fmt.Errorf("%w: item %d", ErrItemNotPathDetectable, int(item))
		}
		for _, g := range globs {
			if strings.TrimSpace(g) == "" {
				return nil, fmt.Errorf("%w: item %d", ErrEmptyGlob, int(item))
			}
		}
	}

	out := make(Table, len(pathDetectableItems))
	for _, item := range pathDetectableItems {
		var merged []string
		seen := make(map[string]bool)
		for _, src := range [][]string{t[item], extra[item]} {
			for _, g := range src {
				if seen[g] {
					continue
				}
				seen[g] = true
				merged = append(merged, g)
			}
		}
		if len(merged) > 0 {
			out[item] = merged
		}
	}
	return out, nil
}

// Globs returns every pattern of the table in a stable order (items 1, 2,
// 5, each in its own order), for callers that only need "is this path
// risky at all", such as the orchestrator's inline-edit permission.
func (t Table) Globs() []string {
	var out []string
	for _, item := range pathDetectableItems {
		out = append(out, t[item]...)
	}
	return out
}
