package profile

import "regexp"

// safeSlugPattern is the safe-slug format required for a profile's Name —
// lowercase letters, digits, and hyphens, never starting with a hyphen.
// Every filesystem path this package builds from a name
// (<profilesDir>/<name>) is confined by this pattern: a name containing "/",
// "..", or other path separators can never reach filepath.Join in the first
// place (R2 — precedent SPEC-057 C1 / SPEC-089 ResolveManifestPath).
var safeSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// isSafeSlug reports whether name matches safeSlugPattern.
func isSafeSlug(name string) bool {
	return safeSlugPattern.MatchString(name)
}
