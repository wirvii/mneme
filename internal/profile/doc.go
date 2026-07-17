// Package profile implements the foundation of mneme's "profile" feature
// (EPIC profiles, SPEC-091 §1): a team's working methodology packaged as a
// portable git repository, activated with nvm-like semantics — a host-level
// store installed once, plus a per-project pointer (the "pin") with
// precedence.
//
// This package is a leaf: it imports only the standard library plus
// github.com/pelletier/go-toml/v2 for TOML parsing. It never imports
// internal/model, internal/store, internal/service, or any other
// internal/* package — the same perimeter as internal/skill,
// internal/conflicts, internal/subagents, and internal/enforcement. This
// keeps the package trivially reusable and testable without a database or a
// resolved HOME: every path (profilesDir, projectRoot, git source) is
// injected by the caller.
//
// Three file formats/mechanisms are defined here:
//
//   - Manifest (mneme-profile.toml): the identity of a profile — name,
//     version, description, mneme compatibility constraint. Lives at the
//     root of the profile's own git repository.
//   - Pin (.mneme-profile): a committed, auto-describing pointer at the
//     root of a project's repository — name, optional source/ref/scaffold.
//     Analogous to .nvmrc/package.json's "engines" field.
//   - Store (~/.mneme/profiles/<name>/): each profile is cloned exactly
//     once, shared by every project on the host. Add/Update/List operate on
//     it; ResolvePin cross-references a project's pin against it.
//
// §1 (this package) only reads and reports pin state — it never writes
// .mneme-profile (that is the "use"/"default" verbs, §3) and never
// materializes anything to a project's disk (that is §2's activation).
package profile
