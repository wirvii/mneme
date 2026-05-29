package lane

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// AuditInput carries the parameters for a single audit run.
type AuditInput struct {
	// Scope is the glob pattern declared in the spec (e.g. "internal/store/*.go").
	// When empty the scope check is skipped.
	Scope string

	// BaseRef is the git ref to diff against. When empty the auditor calls
	// GitDiffer.DefaultBaseRef() to resolve it.
	BaseRef string

	// RepoDir is the absolute path to the git repository root.
	RepoDir string
}

// AuditResult holds the outcome of a single audit run.
type AuditResult struct {
	// FileCount is the number of files changed in the diff.
	FileCount int

	// LinesChanged is the total added+removed lines across all changed files.
	LinesChanged int

	// OutOfScopeFiles lists files that fall outside the declared scope glob.
	OutOfScopeFiles []string

	// ForbiddenPaths lists files that match the forbidden-path patterns.
	ForbiddenPaths []string

	// PublicSymbolChanges lists exported Go symbols (or TS exports) whose
	// name changed between base and HEAD.
	PublicSymbolChanges []string

	// Breaches is the union of all threshold violations as human-readable strings.
	// An audit passes when len(Breaches)==0.
	Breaches []string

	// Passed is true when len(Breaches)==0.
	Passed bool
}

// forbiddenGlobs are file path patterns that trivial-lane specs must not modify.
// These cover schema changes, command-level entry points, and embedded assets.
var forbiddenGlobs = []string{
	"**/*.sql",
	"**/migrations/**",
	"**/schema.*",
	"cmd/**",
	"internal/install/assets/**",
}

// reExportAdded matches lines added in a diff that export a TypeScript/JS symbol.
var reExportAdded = regexp.MustCompile(`^\+export `)

// reExportRemoved matches lines removed in a diff that exported a TS/JS symbol.
var reExportRemoved = regexp.MustCompile(`^-export `)

// Audit runs the deterministic trivial-lane auditor against the repository at
// input.RepoDir. It computes the actual diff (via git-exec), then checks every
// threshold defined in §5.3.1 of the spec. No LLM or external service is used;
// the result depends solely on the diff content and declared scope.
func Audit(input AuditInput) (*AuditResult, error) {
	differ := &GitDiffer{RepoDir: input.RepoDir}

	baseRef := input.BaseRef
	if baseRef == "" {
		var err error
		baseRef, err = differ.DefaultBaseRef()
		if err != nil {
			return nil, fmt.Errorf("lane: audit: resolve base ref: %w", err)
		}
	}

	stats, err := differ.NumStat(baseRef)
	if err != nil {
		return nil, fmt.Errorf("lane: audit: numstat: %w", err)
	}

	return auditFromStats(stats, input, differ, baseRef)
}

// auditFromStats is the pure-logic core of the auditor. It is separated from
// Audit() so tests can inject a pre-built DiffStats without a real repository.
func auditFromStats(stats *DiffStats, input AuditInput, differ *GitDiffer, baseRef string) (*AuditResult, error) {
	result := &AuditResult{
		FileCount:    stats.TotalFiles(),
		LinesChanged: stats.TotalLines(),
	}

	// Check 1: file count limit.
	if result.FileCount > 3 {
		result.Breaches = append(result.Breaches,
			fmt.Sprintf("file count %d exceeds trivial limit of 3", result.FileCount))
	}

	// Check 2: line count limit.
	if result.LinesChanged > 20 {
		result.Breaches = append(result.Breaches,
			fmt.Sprintf("line count %d exceeds trivial limit of 20", result.LinesChanged))
	}

	// Checks 3, 4: forbidden paths and scope.
	for _, f := range stats.Files {
		// Check 3: forbidden paths.
		for _, glob := range forbiddenGlobs {
			if matchGlobStar(glob, f.Path) {
				result.ForbiddenPaths = append(result.ForbiddenPaths, f.Path)
				result.Breaches = append(result.Breaches,
					fmt.Sprintf("forbidden path modified: %s", f.Path))
				break
			}
		}

		// Check 4: out-of-scope files.
		if input.Scope != "" && !matchGlobStar(input.Scope, f.Path) {
			result.OutOfScopeFiles = append(result.OutOfScopeFiles, f.Path)
			result.Breaches = append(result.Breaches,
				fmt.Sprintf("out of scope: %s", f.Path))
		}
	}

	// Checks 5, 6: public-symbol changes.
	if differ != nil {
		symBreaches, err := checkPublicSymbols(stats, differ, baseRef, input.RepoDir)
		if err != nil {
			return nil, fmt.Errorf("lane: audit: check public symbols: %w", err)
		}
		result.PublicSymbolChanges = symBreaches
		result.Breaches = append(result.Breaches, symBreaches...)
	}

	result.Passed = len(result.Breaches) == 0
	return result, nil
}

// checkPublicSymbols detects changes to exported Go symbols and TS/JS exports.
// For Go files it uses go/parser to compare the exported names before and after.
// For TS/JS it uses a regex heuristic on the diff lines.
func checkPublicSymbols(stats *DiffStats, differ *GitDiffer, baseRef, repoDir string) ([]string, error) {
	var breaches []string

	for _, f := range stats.Files {
		switch {
		case strings.HasSuffix(f.Path, ".go"):
			bs, err := checkGoPublicSymbols(f.Path, differ, baseRef, repoDir)
			if err != nil {
				// Non-fatal: new files won't have a "before" version.
				continue
			}
			breaches = append(breaches, bs...)

		case strings.HasSuffix(f.Path, ".ts") ||
			strings.HasSuffix(f.Path, ".tsx") ||
			strings.HasSuffix(f.Path, ".js") ||
			strings.HasSuffix(f.Path, ".jsx"):
			bs, err := checkTSExports(f.Path, differ, baseRef)
			if err != nil {
				continue
			}
			breaches = append(breaches, bs...)
		}
	}
	return breaches, nil
}

// checkGoPublicSymbols compares the set of exported names in a Go file between
// base and HEAD. A change is a removal or addition of an exported identifier.
func checkGoPublicSymbols(path string, differ *GitDiffer, baseRef, repoDir string) ([]string, error) {
	beforeSrc, err := differ.ShowFile(baseRef, path)
	if err != nil {
		return nil, err
	}

	afterPath := filepath.Join(repoDir, path)
	afterBytes, err := os.ReadFile(afterPath)
	if err != nil {
		// File deleted; any symbols that existed before are removed.
		afterBytes = nil
	}

	before := exportedGoNames(beforeSrc)
	after := exportedGoNames(string(afterBytes))

	var breaches []string
	// Names added or removed constitute a public API change.
	for name := range before {
		if _, ok := after[name]; !ok {
			breaches = append(breaches, fmt.Sprintf("public symbol changed: %s in %s", name, path))
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			breaches = append(breaches, fmt.Sprintf("public symbol changed: %s in %s", name, path))
		}
	}
	return breaches, nil
}

// exportedGoNames parses Go source and returns a set of exported top-level names.
// Returns an empty set for empty or unparseable source.
func exportedGoNames(src string) map[string]struct{} {
	names := make(map[string]struct{})
	if strings.TrimSpace(src) == "" {
		return names
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return names
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name != nil && ast.IsExported(d.Name.Name) {
				names[d.Name.Name] = struct{}{}
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(s.Name.Name) {
						names[s.Name.Name] = struct{}{}
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if ast.IsExported(name.Name) {
							names[name.Name] = struct{}{}
						}
					}
				}
			}
		}
	}
	return names
}

// checkTSExports detects additions or removals of `export ` in TypeScript/JS
// files using a heuristic on the diff lines.
func checkTSExports(path string, differ *GitDiffer, baseRef string) ([]string, error) {
	diffContent, err := differ.DiffContent(baseRef, []string{path})
	if err != nil {
		return nil, err
	}

	for _, line := range strings.Split(diffContent, "\n") {
		if reExportAdded.MatchString(line) || reExportRemoved.MatchString(line) {
			return []string{fmt.Sprintf("public export changed in %s", path)}, nil
		}
	}
	return nil, nil
}

// matchGlobStar matches a path against a glob pattern that supports "**" for
// any number of path segments. filepath.Match does not support "**", so we
// implement a simple recursive matcher.
func matchGlobStar(pattern, path string) bool {
	// Normalise separators.
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	return matchGlobStarRecursive(pattern, path)
}

// matchGlobStarRecursive is the recursive engine for matchGlobStar.
func matchGlobStarRecursive(pattern, path string) bool {
	if pattern == "" {
		return path == ""
	}
	if path == "" {
		// Pattern only matches empty path if it is all "**" or "**/"
		return pattern == "**" || pattern == "**/"
	}

	// Locate the first "**" segment.
	starIdx := strings.Index(pattern, "**")
	if starIdx < 0 {
		// No "**" — fall back to filepath.Match.
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Everything before "**" must match the beginning of path literally.
	prefix := pattern[:starIdx]
	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false
	}

	// The "**" component can match zero or more path segments.
	suffix := pattern[starIdx+2:]
	// Strip leading separator from suffix.
	suffix = strings.TrimPrefix(suffix, "/")

	// Try matching suffix against every possible suffix of path.
	// "**" can consume zero path components.
	pathAfterPrefix := strings.TrimPrefix(path, prefix)
	// Remove leading slash introduced by prefix strip.
	pathAfterPrefix = strings.TrimPrefix(pathAfterPrefix, "/")

	if suffix == "" {
		// "**" at the end matches everything.
		return true
	}

	// Try each suffix of pathAfterPrefix.
	segments := strings.Split(pathAfterPrefix, "/")
	for i := 0; i <= len(segments); i++ {
		remaining := strings.Join(segments[i:], "/")
		matched, _ := filepath.Match(suffix, remaining)
		if matched {
			return true
		}
		// Also recurse if suffix itself contains another "**".
		if strings.Contains(suffix, "**") {
			if matchGlobStarRecursive(suffix, remaining) {
				return true
			}
		}
	}
	return false
}
