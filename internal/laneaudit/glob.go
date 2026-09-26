package laneaudit

import "github.com/bmatcuk/doublestar/v4"

// MatchGlobs reports whether path matches at least one of globs (doublestar
// syntax, `**` included).
func MatchGlobs(path string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}
