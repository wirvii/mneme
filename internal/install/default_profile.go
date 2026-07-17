package install

import (
	"embed"
	"fmt"
	"io/fs"
)

// defaultProfileFS embeds mneme's OSS "default profile" (SPEC-096 §6): the
// entirety of internal/install/assets/{agents,skills,templates}/ migrated
// into profile format (mneme-profile.toml + agents/ + skills/ + models.toml
// + templates/ + blocks/ + policy.toml), so a project's sourceless
// PinDefault pin resolves to real, version-locked assets instead of a dead
// state. This is a SEPARATE embed from builtinAgents/builtinSkills/
// builtinTemplates (assets.go) — the global installer keeps reading its own
// assets exactly as before; the default profile tree is a parallel,
// byte-parity-guarded copy (see TestDefaultProfile_DriftAgainstAssets),
// never a replacement.
//
// blocks/ carries only a non-.md keep file (README) — go:embed cannot embed
// an empty directory, and the OSS default deliberately ships no blocks/*.md
// (the operating manual stays host-global infrastructure, never a profile
// block; see R3/AC4 in SPEC-096 §6). rules.jsonl is absent entirely: LoadContentsFS
// treats a missing rules file identically to an empty one (0 rules).
//
//go:embed assets/profiles/default
var defaultProfileFS embed.FS

// defaultProfileFSRoot is the subdirectory defaultProfileFS is rooted under
// — the embed directive above always preserves the full
// "assets/profiles/default/..." prefix, so DefaultProfileFS re-roots it via
// fs.Sub to a filesystem whose own root is the profile's mneme-profile.toml,
// exactly like os.DirFS(profileDir) would for a store-backed profile.
const defaultProfileFSRoot = "assets/profiles/default"

// DefaultProfileFS returns the embedded OSS "default profile" as a read-only
// filesystem rooted at the profile's mneme-profile.toml. It is the source
// PinDefault materialises from (SPEC-096 §6) — never written to
// ~/.mneme/profiles. The returned fs.FS is injected into ProfileService by
// the frontend (cli.initService / mcp's handler construction) via
// service.WithDefaultProfileFS, so the service layer and the internal/profile
// leaf never import internal/install — preserving the SPEC-092 layering
// rule (cli/mcp -> service -> leaf).
//
// A non-nil error from fs.Sub would mean the go:embed directive above and
// defaultProfileFSRoot have drifted apart — a compile-time constant
// mismatch, not a runtime condition — so DefaultProfileFS panics rather than
// returning an error a caller could plausibly recover from.
func DefaultProfileFS() fs.FS {
	sub, err := fs.Sub(defaultProfileFS, defaultProfileFSRoot)
	if err != nil {
		panic(fmt.Sprintf("install: default profile fs.Sub(%s): %v", defaultProfileFSRoot, err))
	}
	return sub
}
