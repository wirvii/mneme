// Package gitident resolves the local human git identity ("Name <email>")
// used to attribute memories materialized into the team-memory git vault
// (SPEC-053 D7). It is a leaf package: only the standard library (os/exec) is
// imported, no other internal package. Author is distinct from a memory's
// CreatedBy field, which records the saving agent (e.g. "claude-code").
package gitident

import (
	"os/exec"
	"strings"
	"sync"
)

// once and cached hold the process-wide memoized result of Author. git
// identity does not change within a single mneme invocation, so shelling out
// to git on every save (Save/Update can be called many times per session)
// would be wasteful.
var (
	once   sync.Once
	cached string
)

// Author returns the local git identity formatted as "Name <email>", read
// from `git config user.name` and `git config user.email` in the current
// process working directory.
//
// Returns "" when git is not installed, the current directory is not inside a
// git repository, or neither user.name nor user.email is configured. This is
// intentional best-effort behaviour (SPEC-053 D7/design): a missing identity
// must never fail the caller's save — it only means the materialized memory
// is attributed to no one.
//
// The result is cached for the lifetime of the process — call Reset in tests
// that need to simulate a change in git identity.
func Author() string {
	once.Do(func() {
		name := gitConfig("user.name")
		email := gitConfig("user.email")
		cached = format(name, email)
	})
	return cached
}

// Reset clears the memoized Author result. Production code never needs this;
// it exists so tests can exercise Author() under different git configurations
// within the same test binary.
func Reset() {
	once = sync.Once{}
	cached = ""
}

// format combines name and email into the "Name <email>" convention. Either
// part may be absent; format degrades gracefully rather than producing a
// malformed string.
func format(name, email string) string {
	switch {
	case name != "" && email != "":
		return name + " <" + email + ">"
	case name != "":
		return name
	case email != "":
		return "<" + email + ">"
	default:
		return ""
	}
}

// gitConfig runs `git config <key>` in the process working directory and
// returns the trimmed value, or "" on any error (git absent, key unset, not a
// repository). Errors are intentionally swallowed — see Author's godoc.
func gitConfig(key string) string {
	//nolint:gosec // key is always one of the two constant strings passed by Author.
	out, err := exec.Command("git", "config", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
