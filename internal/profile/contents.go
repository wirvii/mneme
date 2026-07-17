package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// agentsSubdir, skillsSubdir, blocksSubdir, and rulesFileName are the
// well-known layout of a profile's store directory (SPEC-092 §3.4). A
// profile that has none of them is still valid — LoadContentsFS treats every
// piece as optional so a minimal profile (manifest only) never fails to
// load.
const (
	agentsSubdir    = "agents"
	skillsSubdir    = "skills"
	blocksSubdir    = "blocks"
	rulesFileName   = "rules.jsonl"
	modelsFileName  = "models.toml"
	policyFileName  = "policy.toml"
	templatesSubdir = "templates"
)

// DefaultProfileName is the reserved name of the embedded OSS default
// profile (SPEC-096 §6). A sourceless pin (profile.Pin.IsDefault(), i.e.
// PinDefault) always resolves to this profile, regardless of whatever the
// pin's own Name field happens to say — that field is purely informational
// for a sourceless pin, since there is nothing in the host-level store to
// cross-reference it against.
const DefaultProfileName = "mneme-default"

// AgentAsset is one capa-1 agent file from a profile's agents/ directory,
// keyed by role (the filename without its ".md" extension).
type AgentAsset struct {
	// Role is the subagent role this file materializes (e.g. "backend"),
	// derived from its filename.
	Role string

	// Content is the raw file content — a fully composed profile (frontmatter
	// + agent-fixed layer-1), exactly as the profile author committed it.
	Content []byte
}

// BlockAsset is one managed-block fragment from a profile's blocks/
// directory, keyed by name (the filename without its ".md" extension).
type BlockAsset struct {
	// Name is the block's identifier, derived from its filename.
	Name string

	// Content is the raw markdown that gets upserted into CLAUDE.md's
	// "profile" managed block.
	Content []byte
}

// RuleSpec is one line of a profile's rules.jsonl: the portable shape of a
// rule a profile injects into a project's memory database on activation.
// It deliberately mirrors only the fields a rule needs — RuleSpec never
// imports internal/model (leaf perimeter); ProfileService is the single
// place that maps a RuleSpec to a model.SaveRequest (SPEC-092 §3.4).
type RuleSpec struct {
	// Title is the rule's short summary. Required.
	Title string `json:"title"`

	// Content is the rule's full body. Required.
	Content string `json:"content"`

	// AppliesTo is the list of patterns the rule applies to. Required —
	// non-empty, matching the same invariant model.SaveRequest enforces for
	// TypeRule.
	AppliesTo []string `json:"applies_to"`

	// Severity is one of "info", "warn", "block". Empty is allowed here (the
	// service-layer default-to-"warn" behaviour applies downstream,
	// mirroring model.SaveRequest); any other non-empty value is rejected at
	// parse time so a malformed profile fails fast instead of silently
	// producing an un-enforceable rule.
	Severity string `json:"severity,omitempty"`

	// TopicKey enables a deterministic upsert for the materialized rule,
	// e.g. so re-activating the same profile updates rather than duplicates.
	TopicKey string `json:"topic_key,omitempty"`
}

// validRuleSeverities is the closed set of severities a RuleSpec accepts.
// Mirrors model.Severity's values without importing internal/model.
var validRuleSeverities = map[string]struct{}{
	"":      {}, // deferred to the service-layer default
	"info":  {},
	"warn":  {},
	"block": {},
}

// Validate checks that r carries every field a materialized rule requires.
func (r RuleSpec) Validate() error {
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("profile: rule spec: title is required: %w", ErrInvalidRuleSpec)
	}
	if strings.TrimSpace(r.Content) == "" {
		return fmt.Errorf("profile: rule spec: content is required: %w", ErrInvalidRuleSpec)
	}
	if len(r.AppliesTo) == 0 {
		return fmt.Errorf("profile: rule spec %q: applies_to must not be empty: %w", r.Title, ErrInvalidRuleSpec)
	}
	if _, ok := validRuleSeverities[r.Severity]; !ok {
		return fmt.Errorf("profile: rule spec %q: invalid severity %q: %w", r.Title, r.Severity, ErrInvalidRuleSpec)
	}
	return nil
}

// ErrInvalidRuleSpec is the sentinel returned by RuleSpec.Validate and
// parseRulesJSONL when a rules.jsonl line fails validation.
var ErrInvalidRuleSpec = fmt.Errorf("profile: invalid rule spec")

// Contents is everything LoadContentsFS parses out of a profile's
// filesystem: the file-based pieces to materialize (agents, skills, blocks)
// and the rules to inject, plus the resolved paths of the pieces §2 leaves
// resoluble-but-unconsumed (models/policy/templates — their runtime wiring
// is a later spec).
type Contents struct {
	// Agents is every agents/<role>.md file found, sorted by Role.
	Agents []AgentAsset

	// Skills is the list of skill directory names found directly under
	// skills/ (e.g. "new-project"), sorted. The directories themselves are
	// copied by ProfileService — LoadContentsFS only reports their names and
	// leaves filesystem I/O for the copy step to the service layer.
	Skills []string

	// SkillsDir is the FS-relative path to the profile's skills/ directory
	// (e.g. "skills"), or "" when the profile has none. Combined with FS by
	// the caller to actually read each skill directory's files.
	SkillsDir string

	// Blocks is every blocks/*.md file found, sorted by Name.
	Blocks []BlockAsset

	// Rules is every valid line of rules.jsonl, in file order. Nil when the
	// profile has no rules.jsonl.
	Rules []RuleSpec

	// ModelsPath is the FS-relative path to models.toml (e.g. "models.toml"),
	// or "" when absent.
	ModelsPath string

	// PolicyPath is the FS-relative path to policy.toml, or "" when absent.
	PolicyPath string

	// TemplatesDir is the FS-relative path to the templates/ directory, or ""
	// when absent.
	TemplatesDir string

	// FS is the filesystem every path above is relative to — a disk checkout
	// (os.DirFS) when Contents came from LoadContents, or the embedded OSS
	// default profile (install.DefaultProfileFS) when it came from
	// LoadContentsFS directly (SPEC-096 §6). Never nil once LoadContentsFS
	// returns successfully. A caller needing to read bytes at SkillsDir/
	// ModelsPath/PolicyPath/TemplatesDir reopens them through FS — never
	// through the os package directly — so a disk checkout and the embedded
	// default share one read path end to end.
	FS fs.FS
}

// LoadContents parses every optional piece of a profile's store directory
// dir by wrapping it as an fs.FS (os.DirFS) and delegating to
// LoadContentsFS — the single parse path shared with the embedded OSS
// default profile (SPEC-096 §6 AC2). Every piece is optional: a minimal
// profile (bare mneme-profile.toml, nothing else) parses to a near-zero-value
// Contents and no error.
func LoadContents(dir string) (*Contents, error) {
	return LoadContentsFS(os.DirFS(dir))
}

// LoadContentsFS parses every optional piece of a profile's contents from an
// arbitrary filesystem: a disk checkout (via os.DirFS, through LoadContents)
// or the embedded OSS default profile (install.DefaultProfileFS, SPEC-096
// §6). Every piece is optional: a minimal profile (bare mneme-profile.toml,
// nothing else) parses to a zero-value Contents (FS still set) and no error.
func LoadContentsFS(fsys fs.FS) (*Contents, error) {
	c := Contents{FS: fsys}

	agents, err := loadAgents(fsys, agentsSubdir)
	if err != nil {
		return nil, err
	}
	c.Agents = agents

	skills, err := loadSkillNames(fsys, skillsSubdir)
	if err != nil {
		return nil, err
	}
	if len(skills) > 0 {
		c.Skills = skills
		c.SkillsDir = skillsSubdir
	}

	blocks, err := loadBlocks(fsys, blocksSubdir)
	if err != nil {
		return nil, err
	}
	c.Blocks = blocks

	rules, err := loadRules(fsys, rulesFileName)
	if err != nil {
		return nil, err
	}
	c.Rules = rules

	if fsFileExists(fsys, modelsFileName) {
		c.ModelsPath = modelsFileName
	}
	if fsFileExists(fsys, policyFileName) {
		c.PolicyPath = policyFileName
	}
	if fsDirExists(fsys, templatesSubdir) {
		c.TemplatesDir = templatesSubdir
	}

	return &c, nil
}

// loadAgents reads every "*.md" file directly under dir (an fsys-relative
// path), keying each AgentAsset's Role by its filename without extension.
// Returns (nil, nil) when dir does not exist.
func loadAgents(fsys fs.FS, dir string) ([]AgentAsset, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: load contents: read agents dir %s: %w", dir, err)
	}

	var assets []AgentAsset
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".md" {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("profile: load contents: read agent %s: %w", e.Name(), err)
		}
		assets = append(assets, AgentAsset{
			Role:    strings.TrimSuffix(e.Name(), ".md"),
			Content: data,
		})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Role < assets[j].Role })
	return assets, nil
}

// loadBlocks reads every "*.md" file directly under dir (an fsys-relative
// path), keying each BlockAsset's Name by its filename without extension.
// Returns (nil, nil) when dir does not exist.
func loadBlocks(fsys fs.FS, dir string) ([]BlockAsset, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: load contents: read blocks dir %s: %w", dir, err)
	}

	var assets []BlockAsset
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".md" {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("profile: load contents: read block %s: %w", e.Name(), err)
		}
		assets = append(assets, BlockAsset{
			Name:    strings.TrimSuffix(e.Name(), ".md"),
			Content: data,
		})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	return assets, nil
}

// loadSkillNames lists the directory names directly under dir (an
// fsys-relative path). Returns (nil, nil) when dir does not exist.
func loadSkillNames(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: load contents: read skills dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// loadRules parses rulesPath (an fsys-relative path) as a rules.jsonl file:
// one JSON-encoded RuleSpec per non-blank line. Returns (nil, nil) when
// rulesPath does not exist. A malformed line (broken JSON or a RuleSpec that
// fails Validate) produces an error naming the 1-indexed line number, so a
// profile author can find the exact offending line without re-parsing the
// whole file by hand.
func loadRules(fsys fs.FS, rulesPath string) ([]RuleSpec, error) {
	data, err := fs.ReadFile(fsys, rulesPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: load contents: read rules %s: %w", rulesPath, err)
	}

	var rules []RuleSpec
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		var r RuleSpec
		if err := json.Unmarshal([]byte(trimmed), &r); err != nil {
			return nil, fmt.Errorf("profile: load contents: rules.jsonl line %d: parse: %w", i+1, err)
		}
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("profile: load contents: rules.jsonl line %d: %w", i+1, err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// fsFileExists reports whether p exists in fsys and is a regular file.
func fsFileExists(fsys fs.FS, p string) bool {
	info, err := fs.Stat(fsys, p)
	return err == nil && !info.IsDir()
}

// fsDirExists reports whether p exists in fsys and is a directory.
func fsDirExists(fsys fs.FS, p string) bool {
	info, err := fs.Stat(fsys, p)
	return err == nil && info.IsDir()
}
