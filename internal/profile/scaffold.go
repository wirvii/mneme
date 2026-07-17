package profile

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScaffoldInput configures Scaffold — the deterministic half of a profile's
// creation (SPEC-095 §5, docs/profiles-design.md decision #3): a scaffolder
// producing the profile's own repo structure, paired with the
// mneme-profile-author skill that curates its content afterwards.
type ScaffoldInput struct {
	// Name is the new profile's canonical identifier: must be a safe-slug
	// (^[a-z0-9][a-z0-9-]*$ — the same pattern Manifest.Name/Pin.Name
	// require), since it becomes the rendered manifest's Name and, later, a
	// path component under any host-level store that installs this profile
	// via Store.Add.
	Name string
}

// scaffoldSubdirs is the set of directories Scaffold creates, each seeded
// with a ".gitkeep" so an otherwise-empty directory is still tracked by git
// (SPEC-095 §3.2). agentsSubdir/skillsSubdir/blocksSubdir/templatesSubdir are
// the same well-known names contents.go already defines for LoadContents —
// reused here rather than duplicated so the scaffolded tree and the reader
// can never silently drift apart.
var scaffoldSubdirs = []string{
	agentsSubdir,
	skillsSubdir,
	blocksSubdir,
	templatesSubdir,
	// scaffolds/_blueprints/ is deliberately left EMPTY — populating it with
	// project blueprints/layouts is entirely out of scope for §5 (project
	// scaffolding is §7); this directory only needs to exist so a profile
	// author knows where §7's tooling will eventually write.
	"scaffolds/_blueprints",
}

// scaffoldModelsStub and scaffoldPolicyStub are the commented-out starter
// contents for models.toml/policy.toml (§3.2) — a profile author fills these
// in during the mneme-profile-author grill; an empty/commented file is valid
// input to a later `profile use`/Activate (both paths treat a missing or
// all-comments file as "nothing to consume").
const (
	scaffoldModelsStub = `# models.toml — per-agent model assignment.
# Curated by the mneme-profile-author skill. Example:
#
# [models]
# architect = "opus"
# backend   = "sonnet"
`

	scaffoldPolicyStub = `# policy.toml — enforcement + lanes policy.
# Curated by the mneme-profile-author skill. Example:
#
# [enforcement]
# subagent_containment = "warn"
#
# [lanes]
# trivial_max_files = 3
# trivial_max_lines = 20
`

	scaffoldReadmeTemplate = `# %[1]s

This is a **mneme profile** repository: a team's methodology (agents, skills,
rules, blocks, models, templates) packaged as a portable git repo, activated
with nvm-like semantics.

## Next step

Run the ` + "`mneme-profile-author`" + ` skill (or curate by hand) to fill in:

- ` + "`agents/<role>.md`" + `   — capa-1 (Go-authored envelope + team doctrine) for each role
- ` + "`skills/<name>/`" + `     — team skills
- ` + "`blocks/*.md`" + `         — managed CLAUDE.md blocks
- ` + "`rules.jsonl`" + `        — team rules, one JSON RuleSpec per line
- ` + "`models.toml`" + `        — per-agent model assignment
- ` + "`policy.toml`" + `        — enforcement/lanes policy
- ` + "`templates/*.md`" + `     — spec/plan/qa-report templates

` + "`scaffolds/_blueprints/`" + ` is left EMPTY on purpose: project scaffolding
(` + "`/new-project`" + `/` + "`/new-app`" + `) is authored by a separate mneme spec, not here.

## Consuming this profile

Once curated, committed, and pushed:

    mneme profile add <this-repo-url> --ref v0.1.0
    mneme profile use %[1]s
`
)

// Scaffold creates the on-disk skeleton of a brand-new profile repository at
// dest: the standard directory tree, a stub mneme-profile.toml (rendered
// from ScaffoldInput.Name at version "0.1.0"), stub rules.jsonl/models.toml/
// policy.toml/README.md, and a plain `git init` (SPEC-095 §3.2) — no commit,
// no remote; the profile author supplies both once they have curated content
// worth committing.
//
// Scaffold is a free function, not a Store method, precisely because it
// never touches a host-level store (~/.mneme/profiles/): it produces a
// SOURCE repository a profile author fills in, commits, and pushes; only
// then does a consumer install it via Store.Add ("profile add"). This is the
// clean frontier between "authoring a profile" (§5) and "installing one"
// (§1).
//
// Validation happens before any filesystem write: an unsafe-slug Name fails
// Manifest.Validate (wrapping ErrInvalidManifest) with nothing touched, and a
// non-empty dest fails with ErrProfileExists — the same sentinel Store.Add
// uses for an already-installed destination — again before any write. A
// git-init failure is reported as an error but the already-written tree is
// left in place (recoverable: the author can run `git init` by hand) — no
// filesystem rollback is attempted, since (unlike Store.Add's temp+rename)
// dest here is a location the caller explicitly chose and confirmed empty.
func Scaffold(dest string, in ScaffoldInput) error {
	manifest := Manifest{Name: in.Name, Version: "0.1.0"}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("profile: scaffold: %w", err)
	}

	empty, err := dirIsEmptyOrAbsent(dest)
	if err != nil {
		return fmt.Errorf("profile: scaffold: %w", err)
	}
	if !empty {
		return fmt.Errorf("profile: scaffold: destination %s is not empty: %w", dest, ErrProfileExists)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("profile: scaffold: mkdir %s: %w", dest, err)
	}

	for _, sub := range scaffoldSubdirs {
		full := filepath.Join(dest, filepath.FromSlash(sub))
		if err := os.MkdirAll(full, 0o755); err != nil {
			return fmt.Errorf("profile: scaffold: mkdir %s: %w", full, err)
		}
		if err := os.WriteFile(filepath.Join(full, ".gitkeep"), nil, 0o644); err != nil {
			return fmt.Errorf("profile: scaffold: write .gitkeep in %s: %w", full, err)
		}
	}

	manifestData, err := RenderManifest(manifest)
	if err != nil {
		return fmt.Errorf("profile: scaffold: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dest, ManifestFileName), manifestData, 0o644); err != nil {
		return fmt.Errorf("profile: scaffold: write manifest: %w", err)
	}

	readme := fmt.Sprintf(scaffoldReadmeTemplate, in.Name)
	if err := os.WriteFile(filepath.Join(dest, "README.md"), []byte(readme), 0o644); err != nil {
		return fmt.Errorf("profile: scaffold: write README.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dest, rulesFileName), nil, 0o644); err != nil {
		return fmt.Errorf("profile: scaffold: write %s: %w", rulesFileName, err)
	}
	if err := os.WriteFile(filepath.Join(dest, modelsFileName), []byte(scaffoldModelsStub), 0o644); err != nil {
		return fmt.Errorf("profile: scaffold: write %s: %w", modelsFileName, err)
	}
	if err := os.WriteFile(filepath.Join(dest, policyFileName), []byte(scaffoldPolicyStub), 0o644); err != nil {
		return fmt.Errorf("profile: scaffold: write %s: %w", policyFileName, err)
	}

	if _, err := runGit(dest, nil, "init"); err != nil {
		return fmt.Errorf("profile: scaffold: git init: %w", err)
	}

	return nil
}

// dirIsEmptyOrAbsent reports whether dir does not exist yet, or exists as an
// empty directory. Any other stat/read error (permission denied, dir is
// actually a regular file, etc.) is returned as-is for the caller to wrap.
func dirIsEmptyOrAbsent(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("read dir %s: %w", dir, err)
	}
	return len(entries) == 0, nil
}
