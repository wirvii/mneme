package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/managedblock"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/skill"
	"github.com/wirvii/mneme/internal/subagents"
)

// profileBlockMarker/profileBlockVersion identify the single managed block a
// profile's blocks/*.md content is upserted into a project's CLAUDE.md as
// (SPEC-092 §3.4 step 3). Every blocks/*.md file is concatenated into this
// one block — a profile does not get one managed block per file.
const (
	profileBlockMarker  = "profile"
	profileBlockVersion = 1
)

// ActivationInput carries what Activate needs to materialize one profile
// into one project. Source/Ref/Commit are recorded on the lock purely for
// provenance/staleness — Activate never fetches or resolves them itself
// (that is the caller's job, typically after `profile add`/`update`
// already did the git work).
type ActivationInput struct {
	// RepoRoot is the absolute path of the project's repository root.
	RepoRoot string

	// Name is the profile to activate — already resolved by the caller
	// (§2 never decides which profile to activate, only how). Ignored (and
	// forced to profile.DefaultProfileName) when Default is true or Name
	// already equals profile.DefaultProfileName.
	Name string

	// Default requests activation of the embedded OSS default profile
	// (SPEC-096 §6) instead of a checkout under the host-level store: contents
	// are read from svc.defaultFS via profile.LoadContentsFS rather than
	// profile.LoadContents(store.ProfilePath(Name)). Source/Ref are typically
	// left empty for a default activation (mirroring Pin.IsDefault()); Commit
	// carries the caller-built synthetic "bundled:<mneme-version>+<manifest-
	// version>" marker (SPEC-096 §6 AC9) used by StalenessAgainst to detect a
	// `mneme upgrade`.
	Default bool

	// Source is the git remote the profile was cloned from, recorded on the
	// lock. Empty for mneme's internal default profile.
	Source string

	// Ref is the tag/branch/commit checked out, recorded on the lock.
	Ref string

	// Commit is the resolved SHA of Ref, recorded on the lock and used by
	// StalenessAgainst.
	Commit string
}

// isDefaultActivation reports whether in requests the embedded OSS default
// profile — either explicitly (Default) or by naming it directly
// (profile.DefaultProfileName), so a caller that already resolved the name
// (e.g. from a Pin) does not also need to set Default.
func (in ActivationInput) isDefaultActivation() bool {
	return in.Default || in.Name == profile.DefaultProfileName
}

// ActivateResult reports what Activate materialized and inserted, for the
// caller to relay to the user/agent.
type ActivateResult struct {
	// Profile is the activated profile's name.
	Profile string

	// Commit is the resolved SHA recorded on the lock.
	Commit string

	// Agents lists the absolute paths of every .claude/agents/<role>.md file
	// written.
	Agents []string

	// Skills lists the names of every skill directory installed.
	Skills []string

	// Blocks lists the absolute paths of every CLAUDE.md upserted (in
	// practice always exactly one, or none when the profile has no blocks/).
	Blocks []string

	// RulesInserted lists the ids of every memory SaveProfileRule inserted.
	RulesInserted []string

	// RulesPurged lists the ids of every memory PurgeProfileRules removed
	// before inserting RulesInserted — SPEC-105 DD4's before-insert sweep,
	// which is what makes "activating N times is idempotent in rules" true
	// by construction rather than by relying on Reconcile's guard alone.
	// Includes both this project's rows and any orphaned rows the same
	// sweep found in the global store (DD10).
	RulesPurged []string

	// Backups lists the absolute paths every displaced dev file/directory
	// was copied to before Activate overwrote it (SPEC-105 DD5/DD12).
	// Empty when nothing was displaced.
	Backups []string

	// Degradations lists human-readable notices for anything Activate chose
	// to skip rather than fail on — today, only "no project slug resolved,
	// rules were not written" (SPEC-105 DD8 layer 4). Empty means nothing
	// was degraded; a non-empty Activate is still considered successful.
	Degradations []string
}

// Activate materializes the profile named in.Name into the project rooted
// at in.RepoRoot (SPEC-092 §3.4): agents are fused (capa-1 from the profile +
// capa-2/3 from the repo, when present) and written to .claude/agents/,
// skills are copied to the configured skills directory (respecting
// pinned:true), blocks/*.md is concatenated into a single "profile" managed
// block in CLAUDE.md, and rules.jsonl entries are inserted as
// provenance-marked rule memories. Every materialized/inserted thing is
// recorded in a freshly written .mneme/profile.lock — the "receipt" a later
// Switch/Deactivate uses to undo exactly this and nothing else.
//
// Activate requires both the memory-service and subagent-service seams
// (WithProfileMemoryService/WithProfileSubagentService) to be wired —
// returns model.ErrProfileServiceNotConfigured otherwise.
func (s *ProfileService) Activate(ctx context.Context, in ActivationInput) (*ActivateResult, error) {
	if s.mem == nil || s.sub == nil {
		return nil, fmt.Errorf("service: profile: activate: %w", model.ErrProfileServiceNotConfigured)
	}
	if in.RepoRoot == "" {
		return nil, fmt.Errorf("service: profile: activate: repo root is required")
	}

	isDefault := in.isDefaultActivation()
	if !isDefault && in.Name == "" {
		return nil, fmt.Errorf("service: profile: activate: profile name is required")
	}

	// A single fixed instant for this activation run — used both as the
	// lock's ActivatedAt and, for any artifact this activation displaces, as
	// the backup run directory's timestamp (SPEC-105 DD12). Computing it
	// once here, rather than letting BackupDir and the lock construction
	// each call time.Now() independently, is what guarantees every backup
	// this single Activate call produces lands in the same run directory.
	at := time.Now().UTC()

	// The lock a PRIOR activation (if any) left behind determines which
	// on-disk paths this activation is allowed to overwrite without a
	// backup (SPEC-105 DD5): anything that activation owned is fair game;
	// anything else — including a workspace that has never activated a
	// profile at all, in which case prevLock is nil and ownedPaths is empty
	// — belongs to the dev and must be preserved.
	prevLock, _, err := s.ActiveLock(in.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}
	prevOwned := ownedPaths(prevLock)

	profileName := in.Name
	var contents *profile.Contents

	if isDefault {
		// SPEC-096 §6: the embedded OSS default profile is never a checkout
		// under the host-level store — its source is the fs.FS the frontend
		// injected via WithDefaultProfileFS, read through the exact same
		// LoadContentsFS parse path a disk checkout uses (AC2). The pin's own
		// Name field (if any) is purely informational for a sourceless pin —
		// the lock/rules/result always use the reserved DefaultProfileName.
		profileName = profile.DefaultProfileName
		if s.defaultFS == nil {
			return nil, fmt.Errorf("service: profile: activate: %w", model.ErrDefaultProfileUnavailable)
		}
		contents, err = profile.LoadContentsFS(s.defaultFS)
		if err != nil {
			return nil, fmt.Errorf("service: profile: activate: %w", err)
		}
	} else {
		dir, pathErr := s.store.ProfilePath(in.Name)
		if pathErr != nil {
			return nil, fmt.Errorf("service: profile: activate: %w", pathErr)
		}
		if _, statErr := os.Stat(dir); statErr != nil {
			if os.IsNotExist(statErr) {
				return nil, fmt.Errorf("service: profile: activate: %q: %w", in.Name, profile.ErrProfileNotFound)
			}
			return nil, fmt.Errorf("service: profile: activate: stat %s: %w", dir, statErr)
		}

		contents, err = profile.LoadContents(dir)
		if err != nil {
			return nil, fmt.Errorf("service: profile: activate: %w", err)
		}
	}

	// project="" resolves to s.sub's own MemoryService project slug — the
	// same default ReadProfile/SaveProfile already use elsewhere.
	projectProfile, err := s.sub.ReadProfile(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: read project profile: %w", err)
	}

	if failures := preflightActivate(in.RepoRoot, contents, s.skillsDir, at); len(failures) > 0 {
		return nil, fmt.Errorf("service: profile: activate: preflight failed: %s", strings.Join(failures, "; "))
	}

	var artifacts []profile.LockArtifact

	agentArtifacts, agentPaths, err := s.materializeAgents(contents.Agents, in.RepoRoot, projectProfile, prevOwned, at)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}
	artifacts = append(artifacts, agentArtifacts...)

	skillArtifacts, skillNames, err := s.materializeSkills(contents, in.RepoRoot, prevOwned, at)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}
	artifacts = append(artifacts, skillArtifacts...)

	var blockPaths []string
	blockArtifact, err := s.materializeBlocks(contents.Blocks, in.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}
	if blockArtifact != nil {
		artifacts = append(artifacts, *blockArtifact)
		blockPaths = append(blockPaths, blockArtifact.Path)
	}

	rulesResult, err := s.materializeRules(ctx, contents.Rules, profileName)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}

	lock := profile.Lock{
		SchemaVersion: profile.LockSchemaVersion,
		Profile:       profileName,
		Source:        in.Source,
		Ref:           in.Ref,
		Commit:        in.Commit,
		ActivatedAt:   at,
		Artifacts:     artifacts,
		Rules:         rulesResult.lockRules,
	}
	if err := writeLock(in.RepoRoot, lock); err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}

	var backups []string
	for _, a := range artifacts {
		if a.Backup != "" {
			backups = append(backups, a.Backup)
		}
	}

	return &ActivateResult{
		Profile:       profileName,
		Commit:        in.Commit,
		Agents:        agentPaths,
		Skills:        skillNames,
		Blocks:        blockPaths,
		RulesInserted: rulesResult.ids,
		RulesPurged:   rulesResult.purged,
		Backups:       backups,
		Degradations:  rulesResult.degradations,
	}, nil
}

// DefaultManifest reads and parses the embedded OSS default profile's
// mneme-profile.toml (SPEC-096 §6), letting a caller build the
// version-locked synthetic commit ("bundled:<mneme-version>+<manifest-
// version>", AC9) that ActivationInput.Commit expects for a default
// activation — Activate itself never resolves a version string, mirroring
// how it never resolves Source/Ref/Commit for any other profile either.
// Returns model.ErrDefaultProfileUnavailable when no fs.FS was injected via
// WithDefaultProfileFS.
func (s *ProfileService) DefaultManifest() (*profile.Manifest, error) {
	if s.defaultFS == nil {
		return nil, fmt.Errorf("service: profile: default manifest: %w", model.ErrDefaultProfileUnavailable)
	}
	m, err := profile.ParseManifestFS(s.defaultFS)
	if err != nil {
		return nil, fmt.Errorf("service: profile: default manifest: %w", err)
	}
	return m, nil
}

// materializeAgents fuses and writes every agent asset via
// SubagentService.WriteAgentProfiles, reusing its existing atomic
// write-with-rollback machinery instead of reimplementing it.
//
// Before writing, each target path is classified against prevOwned (SPEC-105
// DD5): a path the previous activation already owned is safely overwritten
// with no backup; a path that exists but was NOT in the previous lock
// belongs to the dev and is backed up first (backupDisplaced) so Deactivate
// can restore it byte-for-byte; a path that does not exist yet is marked
// Created so its provenance is explicit on the lock, even though — for the
// agent kind — Deactivate's removal behaviour does not otherwise depend on
// it (only the "block" kind's cleanup does, DD7).
func (s *ProfileService) materializeAgents(agents []profile.AgentAsset, repoRoot string, pp *ProjectProfile, prevOwned map[string]bool, at time.Time) ([]profile.LockArtifact, []string, error) {
	if len(agents) == 0 {
		return nil, nil, nil
	}

	files := make([]WriteAgentFile, 0, len(agents))
	paths := make([]string, 0, len(agents))
	artifacts := make([]profile.LockArtifact, 0, len(agents))
	for _, a := range agents {
		fused, err := s.fuseAgent(a.Content, a.Role, pp)
		if err != nil {
			return nil, nil, fmt.Errorf("fuse agent %s: %w", a.Role, err)
		}
		path := filepath.Join(repoRoot, ".claude", "agents", a.Role+".md")

		artifact, err := displaceAndBuildArtifact(profile.LockArtifactKindAgent, path, repoRoot, at, prevOwned)
		if err != nil {
			return nil, nil, fmt.Errorf("agent %s: %w", a.Role, err)
		}
		artifacts = append(artifacts, artifact)

		files = append(files, WriteAgentFile{Role: subagents.Role(a.Role), Path: path, Content: fused})
		paths = append(paths, path)
	}

	if _, err := s.sub.WriteAgentProfiles(files); err != nil {
		return nil, nil, fmt.Errorf("write agent profiles: %w", err)
	}

	return artifacts, paths, nil
}

// materializeSkills copies every profile-declared skill directory into
// s.skillsDir, skipping any whose already-installed SKILL.md has
// pinned:true — the same pin-respecting semantics install.WriteSkills
// already establishes for the global installer. Skill bytes are read through
// c.FS (fs.WalkDir/fs.ReadFile) rather than the os package directly, so a
// disk checkout (c.FS == os.DirFS(profileDir)) and the embedded OSS default
// profile (c.FS == install.DefaultProfileFS(), SPEC-096 §6) share one copy
// path — the destination on disk is always a real directory either way.
//
// Displacement (SPEC-105 DD5/DD6) is handled identically to
// materializeAgents: a pre-existing, non-pinned skill directory not owned by
// the previous activation is backed up before being overwritten. pinned:true
// keeps skipping entirely, unaffected by this — the backup only protects the
// skills a dev did NOT think to pin.
func (s *ProfileService) materializeSkills(c *profile.Contents, repoRoot string, prevOwned map[string]bool, at time.Time) ([]profile.LockArtifact, []string, error) {
	if len(c.Skills) == 0 {
		return nil, nil, nil
	}
	if s.skillsDir == "" {
		return nil, nil, fmt.Errorf(
			"profile declares skills but no skills directory is configured (WithProfileSkillsDir)")
	}

	var artifacts []profile.LockArtifact
	var installed []string
	for _, name := range c.Skills {
		dest := filepath.Join(s.skillsDir, name)

		if existing, readErr := os.ReadFile(filepath.Join(dest, "SKILL.md")); readErr == nil {
			if parsed, parseErr := skill.Parse(existing); parseErr == nil && parsed.Pinned {
				continue
			}
		}

		artifact, err := displaceAndBuildArtifact(profile.LockArtifactKindSkill, dest, repoRoot, at, prevOwned)
		if err != nil {
			return nil, nil, fmt.Errorf("skill %s: %w", name, err)
		}

		src := path.Join(c.SkillsDir, name)
		if err := copyFSDir(c.FS, src, dest); err != nil {
			return nil, nil, fmt.Errorf("copy skill %s: %w", name, err)
		}
		artifacts = append(artifacts, artifact)
		installed = append(installed, name)
	}
	return artifacts, installed, nil
}

// ownedPaths returns the set of artifact paths prev (the previous
// activation's lock) recorded — the "this activation may overwrite these
// without asking" set of SPEC-105 DD5. A nil prev (no prior activation)
// yields an empty set, meaning every existing path at that location belongs
// to the dev.
func ownedPaths(prev *profile.Lock) map[string]bool {
	owned := make(map[string]bool)
	if prev == nil {
		return owned
	}
	for _, a := range prev.Artifacts {
		owned[a.Path] = true
	}
	return owned
}

// displaceAndBuildArtifact classifies path against prevOwned (SPEC-105 DD5)
// and returns the LockArtifact this activation should record for it: backing
// path up first via backupDisplaced when it exists and is NOT owned by the
// previous activation, or marking Created when it does not exist yet. Shared
// by materializeAgents and materializeSkills so the two never drift on the
// backup-vs-overwrite decision.
func displaceAndBuildArtifact(kind, path, repoRoot string, at time.Time, prevOwned map[string]bool) (profile.LockArtifact, error) {
	artifact := profile.LockArtifact{Kind: kind, Path: path}

	_, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		if !prevOwned[path] {
			backupPath, err := backupDisplaced(repoRoot, at, path)
			if err != nil {
				return profile.LockArtifact{}, fmt.Errorf("back up displaced %s: %w", path, err)
			}
			artifact.Backup = backupPath
		}
	case os.IsNotExist(statErr):
		artifact.Created = true
	default:
		return profile.LockArtifact{}, fmt.Errorf("stat %s: %w", path, statErr)
	}

	return artifact, nil
}

// backupDisplaced copies target (a file or directory that Activate is about
// to overwrite and that does NOT belong to the previous activation, SPEC-105
// DD5) into this run's backup directory (profile.BackupDir(repoRoot, at)),
// preserving target's path relative to repoRoot when target lives inside it
// (e.g. ".claude/agents/backend.md"), or — for a skill directory, which
// lives under s.skillsDir outside the repo entirely — under "skills/<base
// name>" instead (DD12). Never overwrites an existing backup path: a
// colliding destination is suffixed "-1", "-2", … and the path actually used
// is returned so the caller can record it verbatim on the lock — restoration
// never needs to reconstruct the name.
func backupDisplaced(repoRoot string, at time.Time, target string) (string, error) {
	backupRoot := profile.BackupDir(repoRoot, at)

	rel, relErr := filepath.Rel(repoRoot, target)
	if relErr != nil || rel == "." || strings.HasPrefix(rel, "..") {
		rel = filepath.Join("skills", filepath.Base(target))
	}
	dest := uniqueBackupPath(filepath.Join(backupRoot, rel))

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("backup displaced: mkdir %s: %w", filepath.Dir(dest), err)
	}

	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("backup displaced: stat %s: %w", target, err)
	}

	if info.IsDir() {
		if err := copyFSDir(os.DirFS(target), ".", dest); err != nil {
			return "", fmt.Errorf("backup displaced: copy dir %s: %w", target, err)
		}
		return dest, nil
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("backup displaced: read %s: %w", target, err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("backup displaced: write %s: %w", dest, err)
	}
	return dest, nil
}

// uniqueBackupPath returns path unchanged when nothing already exists there,
// or the first "<stem>-N<ext>" (file) / "<name>-N" (directory) variant that
// does not yet exist (SPEC-105 DD12: a backup destination is never
// overwritten).
func uniqueBackupPath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// materializeBlocks concatenates every blocks/*.md asset (in the sorted
// order LoadContents already returns) into a single "profile" managed block
// upserted into repoRoot/CLAUDE.md. Returns (nil, nil) when the profile has
// no blocks.
func (s *ProfileService) materializeBlocks(blocks []profile.BlockAsset, repoRoot string) (*profile.LockArtifact, error) {
	if len(blocks) == 0 {
		return nil, nil
	}

	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, strings.TrimSpace(string(b.Content)))
	}
	joined := strings.Join(parts, "\n\n")

	claudeMD := filepath.Join(repoRoot, "CLAUDE.md")
	created := false
	if _, statErr := os.Stat(claudeMD); os.IsNotExist(statErr) {
		created = true
	}

	if err := managedblock.Upsert(claudeMD, profileBlockMarker, profileBlockVersion, joined); err != nil {
		return nil, fmt.Errorf("upsert profile block: %w", err)
	}
	return &profile.LockArtifact{
		Kind:    profile.LockArtifactKindBlock,
		Path:    claudeMD,
		Marker:  profileBlockMarker,
		Created: created,
		Digest:  profile.BlockDigest(joined),
	}, nil
}

// materializedRules is materializeRules' full result: the lock entries and
// ids to report on ActivateResult, the ids DD4's before-insert sweep
// removed, and any degradation notices (DD8 layer 4). Bundled into one
// struct rather than growing materializeRules' return list further.
type materializedRules struct {
	lockRules    []profile.LockRule
	ids          []string
	purged       []string
	degradations []string
}

// materializeRules inserts every RuleSpec as a provenance-marked rule memory
// via MemoryService.SaveProfileRule.
//
// SPEC-105 DD4: before inserting anything, it ALWAYS purges by provenance
// via PurgeProfileRules — regardless of the convergence guard's state,
// regardless of len(rules), and regardless of whether a project slug
// resolved. This is deliberate defense in depth: the guard depends on the
// lock; this purge does not depend on anything, so "activating N times is
// idempotent in rules" is true BY CONSTRUCTION, even with the lock deleted
// by hand, .mneme/ wiped, or a future bug in Converged.
//
// SPEC-105 DD8 layer 4: when the service has no resolved project slug
// (!svc.mem.HasProject()), rules are skipped rather than failing the whole
// activation — agents/skills/blocks still materialize. The omission is
// reported via materializedRules.degradations, never silently dropped.
func (s *ProfileService) materializeRules(ctx context.Context, rules []profile.RuleSpec, profileName string) (materializedRules, error) {
	purged, purgeErr := s.mem.PurgeProfileRules(ctx, s.mem.ProjectSlug(), profileName)
	if purgeErr != nil {
		return materializedRules{}, fmt.Errorf("purge profile rules before insert: %w", purgeErr)
	}

	if len(rules) == 0 {
		return materializedRules{purged: purged}, nil
	}

	if !s.mem.HasProject() {
		degradation := fmt.Sprintf(
			"omitiendo %d regla(s) del profile: este directorio no resuelve un slug de proyecto "+
				"(git remote ausente, o cwd fuera de un repo) — ejecuta desde un repo con remote, o repunta el pin",
			len(rules))
		return materializedRules{purged: purged, degradations: []string{degradation}}, nil
	}

	source := model.ProfileSourcePrefix + profileName
	lockRules := make([]profile.LockRule, 0, len(rules))
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		req := model.SaveRequest{
			Title:     r.Title,
			Content:   r.Content,
			AppliesTo: r.AppliesTo,
			Severity:  model.Severity(r.Severity),
			TopicKey:  r.TopicKey,
		}
		resp, err := s.mem.SaveProfileRule(ctx, req, profileName)
		if err != nil {
			return materializedRules{}, fmt.Errorf("save profile rule %q: %w", r.Title, err)
		}
		lockRules = append(lockRules, profile.LockRule{ID: resp.ID, Source: source})
		ids = append(ids, resp.ID)
	}
	return materializedRules{lockRules: lockRules, ids: ids, purged: purged}, nil
}

// fuseAgent merges layer1 — a profile's own agents/<role>.md file, already
// fully composed (frontmatter + agent-fixed layer-1) by the profile's
// author — with the repo's capa-2/3 project profile pp, when pp carries
// anything relevant to role (SPEC-092 §3.5). Degrades cleanly to layer1
// unchanged when pp is nil (no capa-2/3 yet — mneme-init has not run) or
// when pp has nothing for this specific role.
//
// This deliberately does NOT call into internal/subagents.Compose (which
// assumes a Go-authored, archetype-built-in layer-1 it generates itself) —
// layer1 here is already complete, sourced from the profile, not an
// archetype. The wrap/escape treatment of the fused section mirrors what
// subagent_compose does for grill-provided content today
// (internal/mcp/handlers_subagents.go's wrapUntrustedAreasContent), but is a
// new, self-contained assembly local to this package: it reuses only the
// already-exported subagents.GrillContentWrapStart/End delimiters (the
// single source of truth ExtractGrillRegion also reads) rather than
// exporting new surface from internal/subagents, so this fusion introduces
// no change to that package's public contract.
func (s *ProfileService) fuseAgent(layer1 []byte, role string, pp *ProjectProfile) (string, error) {
	if pp == nil {
		return string(layer1), nil
	}

	section := renderProfileFusionSection(*pp, role)
	if section == "" {
		return string(layer1), nil
	}

	text := strings.TrimRight(string(layer1), "\n")
	return text + "\n\n" + wrapProfileFusionSection(section) + "\n", nil
}

// renderProfileFusionSection renders pp's repo/org facts plus the subset of
// pp.Mapping assigned to role as a "## Contexto del proyecto" markdown
// section. Returns "" when pp carries nothing at all relevant to role, so an
// empty/irrelevant project profile never injects an empty section.
func renderProfileFusionSection(pp ProjectProfile, role string) string {
	var areas []string
	for _, m := range pp.Mapping {
		if string(m.Role) == role {
			areas = append(areas, m.App)
		}
	}

	hasRepoFacts := pp.Org != "" || pp.Repo.Commits != "" || pp.Repo.Lang != "" ||
		pp.Repo.Layout != "" || len(pp.Repo.CrossRules) > 0
	if !hasRepoFacts && len(areas) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Contexto del proyecto\n\n")
	if pp.Org != "" {
		fmt.Fprintf(&b, "- Organización: %s\n", pp.Org)
	}
	if pp.Repo.Commits != "" {
		fmt.Fprintf(&b, "- Convención de commits: %s\n", pp.Repo.Commits)
	}
	if pp.Repo.Lang != "" {
		fmt.Fprintf(&b, "- Stack: %s\n", pp.Repo.Lang)
	}
	if pp.Repo.Layout != "" {
		fmt.Fprintf(&b, "- Layout: %s\n", pp.Repo.Layout)
	}
	for _, rule := range pp.Repo.CrossRules {
		fmt.Fprintf(&b, "- Regla cross-cutting: %s\n", rule)
	}
	if len(areas) > 0 {
		sort.Strings(areas)
		fmt.Fprintf(&b, "- Áreas asignadas: %s\n", strings.Join(areas, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// wrapProfileFusionSection wraps section in the same untrusted-data envelope
// subagent_compose uses for grill-provided content
// (subagents.GrillContentWrapStart/End), escaping any literal occurrence of
// mneme's managed-block marker syntax or of the wrap delimiters themselves
// first, so a profile-injected string can never forge a managed-block
// boundary or smuggle text past its own wrap.
func wrapProfileFusionSection(section string) string {
	return subagents.GrillContentWrapStart + "\n\n" + escapeManagedBlockSyntax(section) + "\n\n" + subagents.GrillContentWrapEnd
}

// escapeManagedBlockSyntax neutralizes literal occurrences of mneme's
// managed-block marker prefix and of the grill-content wrap delimiters
// inside s, by prefixing them with a backslash so they render as inert
// text — mirrors internal/mcp/handlers_subagents.go's
// escapeManagedBlockMarkers, reimplemented locally (see fuseAgent's doc).
func escapeManagedBlockSyntax(s string) string {
	replacer := strings.NewReplacer(
		"<!-- mneme:", "\\<!-- mneme:",
		subagents.GrillContentWrapStart, "\\"+subagents.GrillContentWrapStart,
		subagents.GrillContentWrapEnd, "\\"+subagents.GrillContentWrapEnd,
	)
	return replacer.Replace(s)
}

// ActiveLock reads and parses the activation lock for the project rooted at
// repoRoot. present is false (with a nil lock and nil error) when no lock
// exists yet — a workspace that has never activated a profile.
//
// SPEC-105 DD7 wires Lock.Validate() here — previously never called by any
// production code path. A lock whose schema_version is newer than this
// binary understands surfaces model.ErrProfileLockUnsupported: this build
// cannot safely determine what such a lock's Backup/Created/Digest fields
// mean, so it must refuse to act on it (Reconcile/Deactivate) rather than
// silently misinterpreting them.
func (s *ProfileService) ActiveLock(repoRoot string) (lock *profile.Lock, present bool, err error) {
	path := profile.LockPath(repoRoot)
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("service: profile: active lock: read %s: %w", path, readErr)
	}

	parsed, parseErr := profile.ParseLock(data)
	if parseErr != nil {
		return nil, false, fmt.Errorf("service: profile: active lock: %w", parseErr)
	}
	if validateErr := parsed.Validate(); validateErr != nil {
		return nil, false, fmt.Errorf("service: profile: active lock: %w: %w", model.ErrProfileLockUnsupported, validateErr)
	}
	return parsed, true, nil
}

// Deactivate undoes exactly what lock says an earlier Activate materialized
// (SPEC-092 §3.6): every artifact it lists (agent files removed, skill
// directories removed, the "profile" managed block removed from CLAUDE.md —
// never the surrounding prose or any other marker's block) and every rule it
// injected — hard-deleted by provenance (MemoryService.PurgeProfileRules),
// not merely by the ids the lock happens to list, so drift between the lock
// and the database can never leave an orphaned rule behind. A nil lock is a
// no-op (nothing was ever activated).
func (s *ProfileService) Deactivate(ctx context.Context, lock *profile.Lock) error {
	if s.mem == nil {
		return fmt.Errorf("service: profile: deactivate: %w", model.ErrProfileServiceNotConfigured)
	}
	if lock == nil {
		return nil
	}

	for _, a := range lock.Artifacts {
		if err := removeArtifact(a); err != nil {
			return fmt.Errorf("service: profile: deactivate: remove %s %s: %w", a.Kind, a.Path, err)
		}
	}

	if _, err := s.mem.PurgeProfileRules(ctx, s.mem.ProjectSlug(), lock.Profile); err != nil {
		return fmt.Errorf("service: profile: deactivate: purge rules: %w", err)
	}

	return nil
}

// removeArtifact undoes a single materialized artifact according to its
// Kind. When a.Backup is set (SPEC-105 DD5: the path was displaced from a
// dev's own file/directory at activation time), it takes priority over the
// kind-specific removal below — restoring the backup IS the correct
// end state, not deleting it. A missing agent file with no backup is not an
// error (already gone is the desired end state).
func removeArtifact(a profile.LockArtifact) error {
	if a.Backup != "" {
		return restoreArtifactBackup(a)
	}

	switch a.Kind {
	case profile.LockArtifactKindAgent:
		if err := os.Remove(a.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	case profile.LockArtifactKindSkill:
		if err := os.RemoveAll(a.Path); err != nil {
			return err
		}
	case profile.LockArtifactKindBlock:
		return removeBlockArtifact(a)
	default:
		return fmt.Errorf("unknown artifact kind %q", a.Kind)
	}
	return nil
}

// removeBlockArtifact removes a's managed block from its containing file
// (CLAUDE.md) and, when a.Created is true — meaning that file did not exist
// before this activation created it (SPEC-105 DD7/DD14) — deletes the file
// entirely if removing the block left it empty or containing only
// whitespace (AC14). A CLAUDE.md that predates the activation (Created ==
// false) is NEVER deleted, no matter how empty the remaining prose is —
// deleting a file mneme did not create is not this function's call to make.
func removeBlockArtifact(a profile.LockArtifact) error {
	if err := managedblock.Remove(a.Path, a.Marker); err != nil {
		return err
	}
	if !a.Created {
		return nil
	}

	data, err := os.ReadFile(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(string(data)) != "" {
		return nil
	}
	if err := os.Remove(a.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// restoreArtifactBackup restores a.Backup (a file or directory copy
// backupDisplaced saved before Activate overwrote a.Path) back onto a.Path,
// then removes the backup copy and — when the run directory it lived in is
// now empty — the run directory itself (SPEC-105 DD12 retention: Deactivate
// cleans up after itself, but never touches a run directory belonging to a
// DIFFERENT activation, since removeBackupRunDirIfEmpty only ever removes
// directories it finds empty).
func restoreArtifactBackup(a profile.LockArtifact) error {
	info, err := os.Stat(a.Backup)
	if err != nil {
		return fmt.Errorf("stat backup %s: %w", a.Backup, err)
	}

	// Clear whatever the profile wrote at a.Path first, so restoring a
	// directory backup can never merge with stale profile-owned content.
	if err := os.RemoveAll(a.Path); err != nil {
		return fmt.Errorf("remove %s before restore: %w", a.Path, err)
	}

	if info.IsDir() {
		if err := os.MkdirAll(a.Path, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", a.Path, err)
		}
		if err := copyFSDir(os.DirFS(a.Backup), ".", a.Path); err != nil {
			return fmt.Errorf("restore dir %s: %w", a.Path, err)
		}
		if err := os.RemoveAll(a.Backup); err != nil {
			return fmt.Errorf("remove backup dir %s: %w", a.Backup, err)
		}
	} else {
		data, err := os.ReadFile(a.Backup)
		if err != nil {
			return fmt.Errorf("read backup %s: %w", a.Backup, err)
		}
		if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(a.Path), err)
		}
		if err := os.WriteFile(a.Path, data, 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", a.Path, err)
		}
		if err := os.Remove(a.Backup); err != nil {
			return fmt.Errorf("remove backup %s: %w", a.Backup, err)
		}
	}

	removeBackupRunDirIfEmpty(a.Backup)
	return nil
}

// backupRunDir walks up from backupPath to find the run directory a backup
// lives directly under or within — the directory whose OWN parent is named
// "backups" (i.e. "<repoRoot>/.mneme/backups/<UTC>"). Returns "" when no such
// ancestor is found (e.g. a hand-constructed LockArtifact in a test that
// does not follow the real BackupDir layout) — the caller treats that as
// "nothing to clean up", never as an error.
func backupRunDir(backupPath string) string {
	dir := filepath.Dir(backupPath)
	for {
		parent := filepath.Dir(dir)
		if filepath.Base(parent) == "backups" {
			return dir
		}
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// removeBackupRunDirIfEmpty removes backupPath's parent directory, and each
// empty ancestor above it up to and including the run directory
// (backupRunDir), stopping at the first non-empty directory it finds (SPEC-
// 105 DD12: "elimina el directorio de la corrida cuando queda vacío" — but
// never a directory that still holds another artifact's backup from the
// SAME run, and never a directory belonging to a DIFFERENT run). Best-effort:
// any error here is silently ignored — a residual empty backup directory is
// harmless clutter, never a correctness problem, and `profile deactivate`
// separately reports leftover run directories from OTHER activations
// (ResidualBackups, DD12) that this function correctly leaves alone.
func removeBackupRunDirIfEmpty(backupPath string) {
	runDir := backupRunDir(backupPath)
	if runDir == "" {
		return
	}

	dir := filepath.Dir(backupPath)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		if dir == runDir {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// Switch deactivates whatever profile is currently active for the project
// rooted at repoRoot (if any) and activates to in its place (SPEC-092 §3.6).
// When no lock is present yet, Switch is equivalent to a fresh Activate(to).
// Switch never touches anything outside the departing profile's own lock —
// hand-authored files, rules without a profile provenance stamp, and
// CLAUDE.md prose outside the "profile" block are all invisible to it.
func (s *ProfileService) Switch(ctx context.Context, repoRoot string, to ActivationInput) (*ActivateResult, error) {
	to.RepoRoot = repoRoot

	lockA, present, err := s.ActiveLock(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("service: profile: switch: %w", err)
	}
	if present {
		if err := s.Deactivate(ctx, lockA); err != nil {
			return nil, fmt.Errorf("service: profile: switch: deactivate %s: %w", lockA.Profile, err)
		}
	}

	result, err := s.Activate(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("service: profile: switch: %w", err)
	}
	return result, nil
}

// DetectStaleness reports whether the on-disk lock for repoRoot has diverged
// from cached — a Snapshot taken at some earlier Activate/Switch call in
// this process (SPEC-092 §3.7). stale is always false when no lock exists
// (nothing to compare against). DetectStaleness never re-materializes
// anything; it only reports.
func (s *ProfileService) DetectStaleness(repoRoot string, cached profile.Snapshot) (stale bool, msg string, err error) {
	lock, present, err := s.ActiveLock(repoRoot)
	if err != nil {
		return false, "", fmt.Errorf("service: profile: detect staleness: %w", err)
	}
	if !present {
		return false, "", nil
	}

	stale, msg = lock.StalenessAgainst(cached)
	return stale, msg, nil
}

// preflightActivate checks every filesystem precondition Activate's
// materialization phase will need — BEFORE any of it runs (SPEC-105 DD16):
// the parent directory of each agent file is creatable/writable, skillsDir
// is writable when the profile declares skills, CLAUDE.md is writable (or
// creatable) when the profile declares blocks, and this run's backup
// directory (profile.BackupDir) is creatable. Returns the list of unmet
// preconditions in human-readable form; an empty list means Activate may
// proceed. Each check is a real (transient, immediately-undone) write probe
// rather than a permission-bit inspection, so it catches ACL-based
// restrictions a bare os.Stat can't see — and so it behaves identically on
// Windows, where Unix permission bits do not apply (this repo's other
// OS-specific branches are all runtime.GOOS checks, never build tags).
func preflightActivate(repoRoot string, c *profile.Contents, skillsDir string, at time.Time) []string {
	var failures []string

	if len(c.Agents) > 0 {
		agentsDir := filepath.Join(repoRoot, ".claude", "agents")
		if msg := checkDirWritable(agentsDir); msg != "" {
			failures = append(failures, msg)
		}
	}

	if len(c.Skills) > 0 {
		if skillsDir == "" {
			failures = append(failures, "profile declares skills but no skills directory is configured")
		} else if msg := checkDirWritable(skillsDir); msg != "" {
			failures = append(failures, msg)
		}
	}

	if len(c.Blocks) > 0 {
		if msg := checkFileWritableOrCreatable(filepath.Join(repoRoot, "CLAUDE.md")); msg != "" {
			failures = append(failures, msg)
		}
	}

	if msg := checkDirWritable(profile.BackupDir(repoRoot, at)); msg != "" {
		failures = append(failures, msg)
	}

	return failures
}

// checkDirWritable reports "" when dir (or its nearest existing ancestor,
// when dir itself does not exist yet) accepts a transient probe file, or a
// human-readable failure message otherwise. Creating and immediately
// removing a real file — rather than inspecting permission bits — is what
// makes this reliable across ACL-based restrictions and across platforms.
func checkDirWritable(dir string) string {
	existing := nearestExistingAncestor(dir)

	probe := filepath.Join(existing, ".mneme-preflight-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Sprintf("%s is not writable: %v", existing, err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return ""
}

// checkFileWritableOrCreatable reports "" when path already exists and is
// writable, or — when it does not exist yet — when its parent directory
// passes checkDirWritable (i.e. path is creatable).
func checkFileWritableOrCreatable(path string) string {
	if _, err := os.Stat(path); err == nil {
		f, openErr := os.OpenFile(path, os.O_WRONLY, 0)
		if openErr != nil {
			return fmt.Sprintf("%s is not writable: %v", path, openErr)
		}
		_ = f.Close()
		return ""
	}
	return checkDirWritable(filepath.Dir(path))
}

// nearestExistingAncestor walks up from dir until it finds a directory that
// already exists, returning dir itself when it already exists. Used so a
// writability probe never needs to call MkdirAll first — that would create
// directories as a side effect of a check that might still fail for an
// entirely different reason.
func nearestExistingAncestor(dir string) string {
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// writeLock serialises lock and writes it atomically (temp file + rename,
// same-directory so the rename is same-filesystem) to
// profile.LockPath(repoRoot), creating the parent .mneme/ directory as
// needed. It also guarantees (AC13, and DD24's backups/ addition) that the
// freshly written profile.lock — and any pre-activation backup directory —
// are gitignored in the destination project — see ensureMnemeGitignore.
func writeLock(repoRoot string, lock profile.Lock) error {
	data, err := profile.RenderLock(lock)
	if err != nil {
		return fmt.Errorf("render lock: %w", err)
	}

	path := profile.LockPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	if err := ensureMnemeGitignore(repoRoot, lockGitignoreEntry, backupsGitignoreEntry); err != nil {
		return fmt.Errorf("ensure mneme gitignore: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp lock: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp lock: %w", err)
	}
	return nil
}

// lockGitignoreEntry is the line ensureMnemeGitignore guarantees inside
// <repoRoot>/.mneme/.gitignore for the activation lock — scoped to
// profile.lock alone, never a blanket ignore of the .mneme/ directory (AC13
// fix, QA observation 1): .mneme/shared/ (the team-memory vault, SPEC-053)
// must stay trackable when a repo opts into team-memory, and the pin
// .mneme-profile lives at the repo root, outside .mneme/ entirely — neither
// is affected by this entry.
const lockGitignoreEntry = "profile.lock"

// backupsGitignoreEntry is the second, equally scoped entry SPEC-105 DD24
// adds: the pre-activation backups directory holds copies of files that, in
// several repos, ARE themselves tracked (e.g. .claude/agents/*.md) — a
// backup copy under .mneme/backups/ is noise in the repo's history at best,
// and potentially a dev's personal content leaking into shared history at
// worst. Honors the exact same reasoning lockGitignoreEntry's godoc
// documents: a second SCOPED entry, not a wider ".mneme/" ignore.
const backupsGitignoreEntry = "backups/"

// ensureMnemeGitignore makes sure every one of entries is present, in order,
// inside <repoRoot>/.mneme/.gitignore (SPEC-105 DD24 generalises the AC13
// fix from a single hard-coded line to any number of entries — today
// lockGitignoreEntry and backupsGitignoreEntry). Idempotent per line: an
// entry already present anywhere in the file (exact match after trimming)
// is never duplicated; any other pre-existing content — hand-authored or
// from an earlier version of this function — is preserved untouched.
// Missing entries are appended in the order given, so a fresh file always
// lists profile.lock before backups/.
func ensureMnemeGitignore(repoRoot string, entries ...string) error {
	path := filepath.Join(repoRoot, ".mneme", ".gitignore")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	present := make(map[string]bool)
	for _, line := range strings.Split(string(existing), "\n") {
		present[strings.TrimSpace(line)] = true
	}

	var b strings.Builder
	b.Write(existing)
	needsNewline := len(existing) > 0 && !strings.HasSuffix(string(existing), "\n")

	wroteAny := false
	for _, entry := range entries {
		if present[entry] {
			continue
		}
		if needsNewline {
			b.WriteString("\n")
			needsNewline = false
		}
		b.WriteString(entry)
		b.WriteString("\n")
		wroteAny = true
	}

	if !wroteAny {
		return nil
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// copyFSDir recursively copies every file under src (a path within fsys) to
// the same relative location under dst (a real filesystem directory),
// creating directories as needed. fsys is either os.DirFS(profileDir) for a
// store-backed profile or the embedded OSS default profile
// (install.DefaultProfileFS, SPEC-096 §6) — this is the single copy path
// shared by both, so a profile's skills/<name>/ directory materializes
// identically to the host-level skills directory regardless of where its
// bytes actually live.
func copyFSDir(fsys fs.FS, src, dst string) error {
	return fs.WalkDir(fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		target := dst
		if rel != "" {
			target = filepath.Join(dst, filepath.FromSlash(rel))
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return fmt.Errorf("copy dir: read %s: %w", p, readErr)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("copy dir: mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("copy dir: write %s: %w", target, err)
		}
		return nil
	})
}
