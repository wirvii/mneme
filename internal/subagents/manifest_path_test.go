package subagents

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolveManifestPath_AcceptsRootRelative is AC3's happy path for the
// new (Part 2) representation: a root-relative slash path resolves inside
// root and reports the same relative form back.
func TestResolveManifestPath_AcceptsRootRelative(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	abs, relSlash, ok := ResolveManifestPath(".claude/agents/backend.md", root)
	if !ok {
		t.Fatal("ok = false, want true for a root-relative path")
	}
	wantAbs := filepath.Join(root, ".claude", "agents", "backend.md")
	if abs != wantAbs {
		t.Errorf("abs = %q, want %q", abs, wantAbs)
	}
	if relSlash != ".claude/agents/backend.md" {
		t.Errorf("relSlash = %q, want %q", relSlash, ".claude/agents/backend.md")
	}
}

// TestResolveManifestPath_AcceptsAbsoluteInRepo is AC3/AC6's legacy path: an
// absolute path that is actually inside root (the correct, pre-Part-2 shape
// on the owner's own machine) still resolves, transparently.
func TestResolveManifestPath_AcceptsAbsoluteInRepo(t *testing.T) {
	root := t.TempDir()
	stored := filepath.Join(root, ".claude", "agents", "backend.md")

	abs, relSlash, ok := ResolveManifestPath(stored, root)
	if !ok {
		t.Fatal("ok = false, want true for an absolute-in-repo path")
	}
	if abs != filepath.Clean(stored) {
		t.Errorf("abs = %q, want %q", abs, filepath.Clean(stored))
	}
	wantRel := filepath.ToSlash(filepath.Join(".claude", "agents", "backend.md"))
	if relSlash != wantRel {
		t.Errorf("relSlash = %q, want %q", relSlash, wantRel)
	}
}

// TestResolveManifestPath_RejectsParentEscape is AC3: a relative stored
// value carrying an embedded "../" must never resolve outside root.
func TestResolveManifestPath_RejectsParentEscape(t *testing.T) {
	root := t.TempDir()
	_, _, ok := ResolveManifestPath("../x", root)
	if ok {
		t.Error("ok = true, want false for a \"../x\" escape")
	}
}

// TestResolveManifestPath_RejectsAbsoluteOutsideRoot is AC1's mechanism
// directly: an absolute path from an unrelated location (the novo ->
// chateaprov3 shape — a sibling repo on the SAME machine) must be rejected.
func TestResolveManifestPath_RejectsAbsoluteOutsideRoot(t *testing.T) {
	_, _, ok := ResolveManifestPath("/etc/passwd", t.TempDir())
	if ok {
		t.Error("ok = true, want false for an absolute path outside root")
	}
}

// TestResolveManifestPath_RejectsUNCPath is AC2/AC3: a UNC-style path must
// be rejected on non-Windows regardless of root.
func TestResolveManifestPath_RejectsUNCPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UNC rejection is scoped to non-Windows GOOS (SPEC-089 D1 step 2)")
	}
	_, _, ok := ResolveManifestPath(`\\server\share\x`, t.TempDir())
	if ok {
		t.Error("ok = true, want false for a UNC path on non-Windows")
	}
}

// TestResolveManifestPath_RejectsWindowsDriveLetter is AC2's core guard: the
// exact ventasWpDropi shape — "c:\Users\Usuario\Desktop\..." — read on a
// non-Windows machine must be rejected, not silently treated as a relative
// path containing literal backslashes.
func TestResolveManifestPath_RejectsWindowsDriveLetter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("drive-letter rejection is scoped to non-Windows GOOS (SPEC-089 D1 step 2)")
	}
	_, _, ok := ResolveManifestPath(`c:\Users\Usuario\Desktop\chateaprov3\.claude\agents\bug-hunter.md`, t.TempDir())
	if ok {
		t.Error("ok = true, want false for a Windows drive-letter path on non-Windows")
	}
}

// TestResolveManifestPath_RejectsEmpty covers the D1 step 1 guard.
func TestResolveManifestPath_RejectsEmpty(t *testing.T) {
	_, _, ok := ResolveManifestPath("", t.TempDir())
	if ok {
		t.Error("ok = true, want false for an empty stored value")
	}
}

// TestResolveManifestPath_RejectsNUL covers the D1 step 1 guard against a
// NUL byte embedded in stored (would otherwise reach a raw syscall).
func TestResolveManifestPath_RejectsNUL(t *testing.T) {
	_, _, ok := ResolveManifestPath("backend.md\x00.md", t.TempDir())
	if ok {
		t.Error("ok = true, want false for a stored value containing a NUL byte")
	}
}

// TestResolveManifestPath_MutationGuard_ForeignDetection is the mutation
// guardian for AC2/AC3 (guardian 2 in the SPEC-089 design): removing the
// Windows-absolute branch and falling back to filepath.IsAbs alone must turn
// this red — filepath.IsAbs("c:\\Users\\...") is false on darwin/linux, so a
// naive implementation would treat the fixture below as a harmless relative
// path and accept it.
func TestResolveManifestPath_MutationGuard_ForeignDetection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scoped to non-Windows GOOS")
	}
	stored := `c:\Users\Usuario\Desktop\chateaprov3\.claude\agents\bug-hunter.md`
	if filepath.IsAbs(stored) {
		t.Fatal("test fixture assumption broken: filepath.IsAbs must be false for this Windows path on this GOOS")
	}
	_, _, ok := ResolveManifestPath(stored, t.TempDir())
	if ok {
		t.Error("ok = true, want false — a naive filepath.IsAbs-only check would wrongly accept this foreign path")
	}
}
