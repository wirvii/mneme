package subagents

import (
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveManifestPath maps a manifest entry's stored Path to an absolute
// path confined within root (SPEC-089 D1). It accepts both the root-relative
// form regen/write are migrating to and the legacy absolute form still
// present in every manifest written before this helper existed.
//
// ok is false — abs and relSlash are both "" — for any stored value that:
//   - is empty, or contains a NUL byte;
//   - looks like it was authored on a *different* OS family than the
//     current process (see isForeignManifestPath) — the case that actually
//     bit production (SPEC-089): a Windows path like `c:\Users\...` landing
//     in a manifest read on darwin/linux via the git-shared vault;
//   - resolves (after joining with root, for a relative stored value) to a
//     location outside root — whether via a literal ".." escape or a
//     foreign/unrelated absolute path (SPEC-089's novo -> chateaprov3 case:
//     an absolute path from a sibling repo on the SAME machine).
//
// Callers (regen, doctor) MUST treat ok=false as "never touch this path" —
// no os.ReadFile, no os.WriteFile, no os.Stat against the rejected stored
// value. This is the confinement primitive the SPEC-086 R5 risk (materialized
// as SPEC-089) exists to close.
//
// A pure function — no filesystem I/O, no existence check — so it is
// testable without touching a real filesystem and safe to call before
// deciding whether to touch one at all.
func ResolveManifestPath(stored, root string) (abs, relSlash string, ok bool) {
	if stored == "" || strings.ContainsRune(stored, 0) {
		return "", "", false
	}
	if isForeignManifestPath(stored) {
		return "", "", false
	}

	if filepath.IsAbs(stored) {
		rel, err := filepath.Rel(root, stored)
		if err != nil || escapesManifestRoot(rel) {
			return "", "", false
		}
		return filepath.Clean(stored), filepath.ToSlash(rel), true
	}

	candidate := filepath.Join(root, filepath.FromSlash(stored))
	rel, err := filepath.Rel(root, candidate)
	if err != nil || escapesManifestRoot(rel) {
		return "", "", false
	}
	return candidate, filepath.ToSlash(rel), true
}

// escapesManifestRoot reports whether rel (the result of filepath.Rel(root,
// candidate)) points outside root: either exactly ".." or beginning with
// ".."+separator (e.g. "../../etc/passwd"). This is the defense against a
// relative stored value carrying an embedded "../" that would otherwise
// escape root once joined.
func escapesManifestRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isForeignManifestPath reports whether stored looks like an absolute path
// authored on a different OS family than the current process — a Windows
// path (backslash-separated, a UNC "\\server\share\..." prefix, or a
// drive-letter like "c:") observed while runtime.GOOS != "windows".
//
// This check exists because filepath.IsAbs is itself GOOS-aware and answers
// the wrong question here: on darwin/linux, filepath.IsAbs("c:\\Users\\...")
// returns false — it is not a valid *Unix* absolute path either — so without
// an explicit foreign-path check, ResolveManifestPath would fall through to
// the relative branch and happily filepath.Join a literal-backslash string
// underneath root, never detecting that the value came from a different
// machine's Windows filesystem. That silent fallthrough is exactly the
// SPEC-089 ventasWpDropi case: four manifest entries with
// "c:\\Users\\Usuario\\Desktop\\..." paths, materialized on a Mac.
//
// The inverse (a Unix-style absolute path landing in a manifest read on
// Windows) is intentionally NOT specifically detected here — R1 in the
// SPEC-089 design accepts this asymmetry, since it is not the case that
// materialized in production; filepath.Rel's own escape check
// (escapesManifestRoot) remains the backstop for that direction.
func isForeignManifestPath(stored string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	if strings.Contains(stored, `\`) || strings.HasPrefix(stored, `\\`) {
		return true
	}
	return hasWindowsDriveLetter(stored)
}

// hasWindowsDriveLetter reports whether stored begins with a Windows drive
// letter designator (e.g. "c:", "C:") — len >= 2, first byte an ASCII
// letter, second byte a colon.
func hasWindowsDriveLetter(stored string) bool {
	if len(stored) < 2 || stored[1] != ':' {
		return false
	}
	c := stored[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
