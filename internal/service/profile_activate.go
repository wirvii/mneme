package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
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
	// (§2 never decides which profile to activate, only how).
	Name string

	// Source is the git remote the profile was cloned from, recorded on the
	// lock. Empty for mneme's internal default profile.
	Source string

	// Ref is the tag/branch/commit checked out, recorded on the lock.
	Ref string

	// Commit is the resolved SHA of Ref, recorded on the lock and used by
	// StalenessAgainst.
	Commit string
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
	if in.Name == "" {
		return nil, fmt.Errorf("service: profile: activate: profile name is required")
	}

	dir, err := s.store.ProfilePath(in.Name)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("service: profile: activate: %q: %w", in.Name, profile.ErrProfileNotFound)
		}
		return nil, fmt.Errorf("service: profile: activate: stat %s: %w", dir, statErr)
	}

	contents, err := profile.LoadContents(dir)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}

	// project="" resolves to s.sub's own MemoryService project slug — the
	// same default ReadProfile/SaveProfile already use elsewhere.
	projectProfile, err := s.sub.ReadProfile(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: read project profile: %w", err)
	}

	var artifacts []profile.LockArtifact

	agentArtifacts, agentPaths, err := s.materializeAgents(contents.Agents, in.RepoRoot, projectProfile)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}
	artifacts = append(artifacts, agentArtifacts...)

	skillArtifacts, skillNames, err := s.materializeSkills(contents)
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

	lockRules, ruleIDs, err := s.materializeRules(ctx, contents.Rules, in.Name)
	if err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}

	lock := profile.Lock{
		SchemaVersion: profile.LockSchemaVersion,
		Profile:       in.Name,
		Source:        in.Source,
		Ref:           in.Ref,
		Commit:        in.Commit,
		ActivatedAt:   time.Now().UTC(),
		Artifacts:     artifacts,
		Rules:         lockRules,
	}
	if err := writeLock(in.RepoRoot, lock); err != nil {
		return nil, fmt.Errorf("service: profile: activate: %w", err)
	}

	return &ActivateResult{
		Profile:       in.Name,
		Commit:        in.Commit,
		Agents:        agentPaths,
		Skills:        skillNames,
		Blocks:        blockPaths,
		RulesInserted: ruleIDs,
	}, nil
}

// materializeAgents fuses and writes every agent asset via
// SubagentService.WriteAgentProfiles, reusing its existing atomic
// write-with-rollback machinery instead of reimplementing it.
func (s *ProfileService) materializeAgents(agents []profile.AgentAsset, repoRoot string, pp *ProjectProfile) ([]profile.LockArtifact, []string, error) {
	if len(agents) == 0 {
		return nil, nil, nil
	}

	files := make([]WriteAgentFile, 0, len(agents))
	paths := make([]string, 0, len(agents))
	for _, a := range agents {
		fused, err := s.fuseAgent(a.Content, a.Role, pp)
		if err != nil {
			return nil, nil, fmt.Errorf("fuse agent %s: %w", a.Role, err)
		}
		path := filepath.Join(repoRoot, ".claude", "agents", a.Role+".md")
		files = append(files, WriteAgentFile{Role: subagents.Role(a.Role), Path: path, Content: fused})
		paths = append(paths, path)
	}

	if _, err := s.sub.WriteAgentProfiles(files); err != nil {
		return nil, nil, fmt.Errorf("write agent profiles: %w", err)
	}

	artifacts := make([]profile.LockArtifact, 0, len(paths))
	for _, p := range paths {
		artifacts = append(artifacts, profile.LockArtifact{Kind: "agent", Path: p})
	}
	return artifacts, paths, nil
}

// materializeSkills copies every profile-declared skill directory into
// s.skillsDir, skipping any whose already-installed SKILL.md has
// pinned:true — the same pin-respecting semantics install.WriteSkills
// already establishes for the global installer.
func (s *ProfileService) materializeSkills(c *profile.Contents) ([]profile.LockArtifact, []string, error) {
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

		if err := copyDir(filepath.Join(c.SkillsDir, name), dest); err != nil {
			return nil, nil, fmt.Errorf("copy skill %s: %w", name, err)
		}
		artifacts = append(artifacts, profile.LockArtifact{Kind: "skill", Path: dest})
		installed = append(installed, name)
	}
	return artifacts, installed, nil
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
	if err := managedblock.Upsert(claudeMD, profileBlockMarker, profileBlockVersion, joined); err != nil {
		return nil, fmt.Errorf("upsert profile block: %w", err)
	}
	return &profile.LockArtifact{Kind: "block", Path: claudeMD, Marker: profileBlockMarker}, nil
}

// materializeRules inserts every RuleSpec as a provenance-marked rule memory
// via MemoryService.SaveProfileRule.
func (s *ProfileService) materializeRules(ctx context.Context, rules []profile.RuleSpec, profileName string) ([]profile.LockRule, []string, error) {
	if len(rules) == 0 {
		return nil, nil, nil
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
			return nil, nil, fmt.Errorf("save profile rule %q: %w", r.Title, err)
		}
		lockRules = append(lockRules, profile.LockRule{ID: resp.ID, Source: source})
		ids = append(ids, resp.ID)
	}
	return lockRules, ids, nil
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

// removeArtifact removes a single materialized artifact according to its
// Kind. A missing agent file is not an error (already gone is the desired
// end state).
func removeArtifact(a profile.LockArtifact) error {
	switch a.Kind {
	case "agent":
		if err := os.Remove(a.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	case "skill":
		if err := os.RemoveAll(a.Path); err != nil {
			return err
		}
	case "block":
		if err := managedblock.Remove(a.Path, a.Marker); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown artifact kind %q", a.Kind)
	}
	return nil
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

// writeLock serialises lock and writes it atomically (temp file + rename,
// same-directory so the rename is same-filesystem) to
// profile.LockPath(repoRoot), creating the parent .mneme/ directory as
// needed. It also guarantees (AC13) that the freshly written profile.lock is
// gitignored in the destination project — see ensureLockGitignore.
func writeLock(repoRoot string, lock profile.Lock) error {
	data, err := profile.RenderLock(lock)
	if err != nil {
		return fmt.Errorf("render lock: %w", err)
	}

	path := profile.LockPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	if err := ensureLockGitignore(repoRoot); err != nil {
		return fmt.Errorf("ensure lock gitignore: %w", err)
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

// lockGitignoreEntry is the single line ensureLockGitignore guarantees
// inside <repoRoot>/.mneme/.gitignore — scoped to profile.lock alone, never
// a blanket ignore of the .mneme/ directory (AC13 fix, QA observation 1):
// .mneme/shared/ (the team-memory vault, SPEC-053) must stay trackable when
// a repo opts into team-memory, and the pin .mneme-profile lives at the repo
// root, outside .mneme/ entirely — neither is affected by this entry.
const lockGitignoreEntry = "profile.lock"

// ensureLockGitignore makes sure profile.lock can never be accidentally
// committed by whatever repo Activate materializes into (AC13): it
// writes/updates a small, self-contained <repoRoot>/.mneme/.gitignore
// containing exactly lockGitignoreEntry — a pattern scoped to that single
// file inside .mneme/, not a blanket ".mneme/" ignore, which would silently
// break team-memory's committed .mneme/shared/ vault. Idempotent: a
// .gitignore that already contains the entry (on any line, exact match after
// trimming) is left untouched; any other pre-existing content in the file is
// preserved.
func ensureLockGitignore(repoRoot string) error {
	path := filepath.Join(repoRoot, ".mneme", ".gitignore")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == lockGitignoreEntry {
			return nil
		}
	}

	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString(lockGitignoreEntry)
	b.WriteString("\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// copyDir recursively copies every file under src to the same relative
// location under dst, creating directories as needed. Used to materialize a
// profile's skills/<name>/ directory into the host-level skills directory.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return fmt.Errorf("copy dir: relative path of %s: %w", path, relErr)
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("copy dir: read %s: %w", path, readErr)
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
