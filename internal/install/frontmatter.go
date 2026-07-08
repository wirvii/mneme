package install

import "github.com/juanftp/mneme/internal/frontmatter"

// SetModelInFrontmatter replaces (or inserts) the `model: <x>` line in the
// YAML frontmatter of a Claude agent markdown file with `model: <newModel>`.
// The replacement is purely surgical: only the model line changes; every
// other byte is preserved verbatim. This avoids YAML re-serialization bugs
// (bug I1: description round-trip corruption introduced by full-frontmatter
// rewrites).
//
// This is a thin wrapper around the shared frontmatter.SetFrontmatter editor
// (internal/frontmatter), requesting only the Model field. See that package
// for the exact insertion/preservation rules.
func SetModelInFrontmatter(content []byte, newModel string) ([]byte, error) {
	return frontmatter.SetFrontmatter(content, frontmatter.Fields{Model: &newModel})
}
