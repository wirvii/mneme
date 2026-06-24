package codegraph

import (
	"path/filepath"
	"sort"
	"strings"
)

// TSAliasMap maps path-alias prefixes to lists of candidate base directories
// (repo-relative). Each entry is anchored to the tsconfig directory that
// declared it so that monorepo apps with different baseUrls are handled
// correctly.
//
// Example (quantium — single tsconfig in apps/web-ui):
//
//	TSAliasMap["@/"] = TSAliasEntry{
//	    TsconfigDir: "/abs/apps/web-ui",            // absolute, used for finding the best match
//	    BaseDirs:    ["apps/web-ui"],                // repo-relative
//	    RootDir:     "/abs/root",                    // absolute repo root
//	}
type TSAliasMap map[string][]TSAliasEntry

// TSAliasEntry records the base directories for one path-alias prefix, anchored
// to the tsconfig that declared it.
type TSAliasEntry struct {
	// TsconfigDir is the absolute path of the directory containing the tsconfig.
	TsconfigDir string
	// BaseDirs holds the repo-relative candidate directories after stripping
	// trailing "/*" wildcards from the paths values and resolving against baseUrl.
	BaseDirs []string
	// RootDir is the absolute repo root used to make paths repo-relative.
	RootDir string
}

// LoadTSAliases uses the given TSExtractor to read all tsconfig.json files under
// rootDir (absolute) and builds a TSAliasMap. Failures are fail-open: if Node.js
// is unavailable, no tsconfig is found, or parsing fails, the returned map is
// empty so callers degrade gracefully without errors.
func LoadTSAliases(ex *TSExtractor, rootDir string) TSAliasMap {
	entries, err := ex.LoadTSConfigAliases(rootDir)
	if err != nil || len(entries) == 0 {
		return TSAliasMap{}
	}

	aliasMap := make(TSAliasMap)

	for _, tc := range entries {
		if tc.Paths == nil {
			continue
		}
		for pattern, replacements := range tc.Paths {
			// Normalize the alias prefix: strip the trailing "/*" wildcard.
			// "@/*" → "@/";  "@utils/*" → "@utils/".
			// Patterns without "/*" (e.g. "@utils") are kept verbatim + "/".
			prefix := strings.TrimSuffix(pattern, "*")

			var baseDirs []string
			for _, repl := range replacements {
				// Strip trailing "/*" wildcard from the replacement path.
				repl = strings.TrimSuffix(repl, "*")

				// Resolve the replacement path relative to baseUrl (if present)
				// or the tsconfig directory. The subprocess already resolved
				// baseUrl to an absolute path when it ran parseJsonConfigFileContent.
				var absBase string
				if tc.BaseURL != "" {
					absBase = filepath.Join(tc.BaseURL, repl)
				} else {
					absBase = filepath.Join(tc.Dir, repl)
				}
				// Convert to repo-relative.
				rel, relErr := filepath.Rel(rootDir, absBase)
				if relErr != nil {
					continue
				}
				// filepath.Rel may produce "../..." when the path escapes rootDir.
				if strings.HasPrefix(rel, "..") {
					continue
				}
				baseDirs = append(baseDirs, filepath.ToSlash(rel))
			}
			if len(baseDirs) == 0 {
				continue
			}

			entry := TSAliasEntry{
				TsconfigDir: tc.Dir,
				BaseDirs:    baseDirs,
				RootDir:     rootDir,
			}
			aliasMap[prefix] = append(aliasMap[prefix], entry)
		}
	}

	return aliasMap
}

// ResolveAlias expands a module specifier (e.g. "@/lib/utils") to candidate
// repo-relative file paths using the alias map. refFilePath is the importer's
// repo-relative path and is used to pick the closest tsconfig when multiple
// entries match the same alias prefix (monorepo case).
//
// Returns nil when no alias matches. The returned paths do NOT include
// extensions — callers must probe via tsCandidatePaths.
func (m TSAliasMap) ResolveAlias(moduleSource, refFilePath string) []string {
	if len(m) == 0 {
		return nil
	}

	// Convert the importer to an absolute path so we can compare with
	// TsconfigDir below. We only need string prefix comparison so we
	// reconstruct the abs path from the stored RootDir — use any entry.
	// Since we only need the importer's abs dir for prefix comparison,
	// derive it as: RootDir/refFilePath.

	// Find the matching alias prefix (longest match first for correctness).
	type match struct {
		prefix  string
		tail    string
		entries []TSAliasEntry
	}
	var best *match
	for prefix, entries := range m {
		if strings.HasPrefix(moduleSource, prefix) {
			tail := moduleSource[len(prefix):]
			if best == nil || len(prefix) > len(best.prefix) {
				b := match{prefix: prefix, tail: tail, entries: entries}
				best = &b
			}
		}
	}
	if best == nil {
		return nil
	}

	// Among all entries for this prefix, pick the one whose TsconfigDir is the
	// closest ancestor of the importer (longest-prefix-match on the abs path).
	var bestEntry *TSAliasEntry
	bestDepth := -1
	for i := range best.entries {
		e := &best.entries[i]
		// Absolute path of the importer file.
		absImporter := filepath.Join(e.RootDir, filepath.FromSlash(refFilePath))
		absImporterDir := filepath.Dir(absImporter)
		// Check if TsconfigDir is an ancestor of absImporterDir.
		rel, err := filepath.Rel(e.TsconfigDir, absImporterDir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		depth := len(e.TsconfigDir)
		if depth > bestDepth {
			bestDepth = depth
			bestEntry = e
		}
	}
	if bestEntry == nil {
		// No entry is an ancestor of the importer — fall back to the first.
		bestEntry = &best.entries[0]
	}

	// Build candidate repo-relative paths: baseDir + tail.
	var candidates []string
	seen := make(map[string]bool)
	for _, baseDir := range bestEntry.BaseDirs {
		candidate := filepath.ToSlash(filepath.Join(filepath.FromSlash(baseDir), filepath.FromSlash(best.tail)))
		if !seen[candidate] {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	sort.Strings(candidates)
	return candidates
}
