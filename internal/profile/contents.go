package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// agentsSubdir, skillsSubdir, blocksSubdir, and rulesFileName are the
// well-known layout of a profile's store directory (SPEC-092 §3.4). A
// profile that has none of them is still valid — LoadContents treats every
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

// Contents is everything LoadContents parses out of a profile's store
// directory: the file-based pieces to materialize (agents, skills, blocks)
// and the rules to inject, plus the resolved paths of the pieces §2 leaves
// resoluble-but-unconsumed (models/policy/templates — their runtime wiring
// is a later spec).
type Contents struct {
	// Agents is every agents/<role>.md file found, sorted by Role.
	Agents []AgentAsset

	// Skills is the list of skill directory names found directly under
	// skills/ (e.g. "new-project"), sorted. The directories themselves are
	// copied by ProfileService — LoadContents only reports their names and
	// leaves filesystem I/O for the copy step to the service layer.
	Skills []string

	// SkillsDir is the absolute path to the profile's skills/ directory, or
	// "" when the profile has none. Recorded so ProfileService can copy each
	// entry in Skills without re-deriving the path.
	SkillsDir string

	// Blocks is every blocks/*.md file found, sorted by Name.
	Blocks []BlockAsset

	// Rules is every valid line of rules.jsonl, in file order. Nil when the
	// profile has no rules.jsonl.
	Rules []RuleSpec

	// ModelsPath is the absolute path to models.toml, or "" when absent.
	ModelsPath string

	// PolicyPath is the absolute path to policy.toml, or "" when absent.
	PolicyPath string

	// TemplatesDir is the absolute path to the templates/ directory, or ""
	// when absent.
	TemplatesDir string
}

// LoadContents parses every optional piece of a profile's store directory
// dir. Every piece is optional: a minimal profile (bare mneme-profile.toml,
// nothing else) parses to a zero-value Contents and no error.
func LoadContents(dir string) (*Contents, error) {
	var c Contents

	agents, err := loadAgents(filepath.Join(dir, agentsSubdir))
	if err != nil {
		return nil, err
	}
	c.Agents = agents

	skillsDir := filepath.Join(dir, skillsSubdir)
	skills, err := loadSkillNames(skillsDir)
	if err != nil {
		return nil, err
	}
	if len(skills) > 0 {
		c.Skills = skills
		c.SkillsDir = skillsDir
	}

	blocks, err := loadBlocks(filepath.Join(dir, blocksSubdir))
	if err != nil {
		return nil, err
	}
	c.Blocks = blocks

	rules, err := loadRules(filepath.Join(dir, rulesFileName))
	if err != nil {
		return nil, err
	}
	c.Rules = rules

	if p := filepath.Join(dir, modelsFileName); fileExists(p) {
		c.ModelsPath = p
	}
	if p := filepath.Join(dir, policyFileName); fileExists(p) {
		c.PolicyPath = p
	}
	if p := filepath.Join(dir, templatesSubdir); dirExists(p) {
		c.TemplatesDir = p
	}

	return &c, nil
}

// loadAgents reads every "*.md" file directly under dir, keying each
// AgentAsset's Role by its filename without extension. Returns (nil, nil)
// when dir does not exist.
func loadAgents(dir string) ([]AgentAsset, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: load contents: read agents dir %s: %w", dir, err)
	}

	var assets []AgentAsset
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
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

// loadBlocks reads every "*.md" file directly under dir, keying each
// BlockAsset's Name by its filename without extension. Returns (nil, nil)
// when dir does not exist.
func loadBlocks(dir string) ([]BlockAsset, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: load contents: read blocks dir %s: %w", dir, err)
	}

	var assets []BlockAsset
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
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

// loadSkillNames lists the directory names directly under dir. Returns
// (nil, nil) when dir does not exist.
func loadSkillNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
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

// loadRules parses path as a rules.jsonl file: one JSON-encoded RuleSpec per
// non-blank line. Returns (nil, nil) when path does not exist. A malformed
// line (broken JSON or a RuleSpec that fails Validate) produces an error
// naming the 1-indexed line number, so a profile author can find the exact
// offending line without re-parsing the whole file by hand.
func loadRules(path string) ([]RuleSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: load contents: read rules %s: %w", path, err)
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

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
