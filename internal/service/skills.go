package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/skill"
)

// SkillsService manages the lifecycle of mneme skills: listing, installing,
// pinning, removing, linting, and validating. All operations target the
// filesystem directory skillsDir (typically ~/.claude/skills/) and the embedded
// bundle accessed through the install package. No database access is performed.
type SkillsService struct {
	skillsDir string
	mirrors   []string
}

// NewSkillsService constructs a SkillsService targeting skillsDir as the
// installation root. The directory is created lazily — the constructor itself
// does not write to the filesystem.
func NewSkillsService(skillsDir string) *SkillsService {
	return &SkillsService{skillsDir: skillsDir}
}

// NewMirroredSkillsService constructs a service whose mutations are applied
// to the primary directory and every runtime mirror. Reads and validation use
// the primary copy; successful installs keep all runtime discovery paths in
// sync from the same embedded bytes.
func NewMirroredSkillsService(primary string, mirrors ...string) *SkillsService {
	return &SkillsService{skillsDir: primary, mirrors: mirrors}
}

// SkillInfo summarises the combined state of a skill as seen from both the
// embedded bundle and the installation directory.
type SkillInfo struct {
	// Name is the kebab-case skill identifier.
	Name string

	// Version is the version string from the installed (or bundled) SKILL.md.
	Version string

	// Installed is true when the skill directory exists under skillsDir.
	Installed bool

	// Pinned is true when the installed SKILL.md has pinned:true.
	Pinned bool

	// Bundled is true when the skill appears in the embedded bundle.
	Bundled bool

	// LintOK is true when the installed SKILL.md passes lint with no errors.
	// It is false when the skill is not installed.
	LintOK bool
}

// List returns a merged view of all bundled skills and all installed skills.
// Skills that appear in both are represented with a single entry reflecting
// the installed state where applicable.
func (s *SkillsService) List() ([]SkillInfo, error) {
	bundledNames, err := install.BundledSkillNames()
	if err != nil {
		return nil, fmt.Errorf("service: skills: list: bundled names: %w", err)
	}

	bundledSet := make(map[string]bool, len(bundledNames))
	for _, n := range bundledNames {
		bundledSet[n] = true
	}

	// Collect installed skills.
	installedNames, err := s.installedNames()
	if err != nil {
		return nil, fmt.Errorf("service: skills: list: installed names: %w", err)
	}

	// Build a merged ordered list: bundled first, then installed-only.
	seen := make(map[string]bool)
	var names []string
	for _, n := range bundledNames {
		names = append(names, n)
		seen[n] = true
	}
	for _, n := range installedNames {
		if !seen[n] {
			names = append(names, n)
			seen[n] = true
		}
	}

	var infos []SkillInfo
	for _, n := range names {
		info := SkillInfo{
			Name:    n,
			Bundled: bundledSet[n],
		}

		skillDir := filepath.Join(s.skillsDir, n)
		skillMD := filepath.Join(skillDir, "SKILL.md")
		if _, err := os.Stat(skillMD); err == nil {
			info.Installed = true
			if parsed, parseErr := skill.ParseFile(skillMD); parseErr == nil {
				info.Version = parsed.Version
				info.Pinned = parsed.Pinned
				lr := skill.Lint(parsed, n)
				info.LintOK = lr.Passed
			}
		}

		infos = append(infos, info)
	}

	return infos, nil
}

// Install copies a single bundled skill into the skills directory. If the skill
// is already installed and pinned:true, it returns model.ErrSkillPinned unless
// force is true.
func (s *SkillsService) Install(name string, force bool) error {
	if err := s.installOne(name, force); err != nil {
		return err
	}
	for _, mirror := range s.mirrors {
		if err := (&SkillsService{skillsDir: mirror}).installOne(name, force); err != nil {
			return fmt.Errorf("service: skills: mirror %s: %w", mirror, err)
		}
	}
	return nil
}

func (s *SkillsService) installOne(name string, force bool) error {
	if !s.isBundled(name) {
		return fmt.Errorf("service: skills: install %q: %w", name, model.ErrSkillNotFound)
	}

	// Build a temporary single-skill agent to reuse WriteSkills.
	agent := &install.Agent{
		Skills: func() ([]install.SkillEntry, error) {
			all, err := install.BundledSkillEntries()
			if err != nil {
				return nil, err
			}
			var filtered []install.SkillEntry
			prefix := name + string(filepath.Separator)
			fwdPrefix := name + "/"
			for _, e := range all {
				fwd := filepath.ToSlash(e.RelPath)
				if strings.HasPrefix(fwd, fwdPrefix) || e.RelPath == name {
					filtered = append(filtered, e)
				}
				// Also accept OS-native prefix.
				if filepath.ToSlash(e.RelPath) != fwd {
					if strings.HasPrefix(e.RelPath, prefix) {
						filtered = append(filtered, e)
					}
				}
			}
			return filtered, nil
		},
	}

	if err := os.MkdirAll(s.skillsDir, 0o755); err != nil {
		return fmt.Errorf("service: skills: install %q: mkdir: %w", name, err)
	}

	res, err := install.WriteSkills(agent, s.skillsDir, force)
	if err != nil {
		return fmt.Errorf("service: skills: install %q: %w", name, err)
	}

	if len(res.Skipped) > 0 {
		return fmt.Errorf("service: skills: install %q: %w", name, model.ErrSkillPinned)
	}

	return nil
}

// Pin sets pinned:true in the installed SKILL.md for name.
// Returns model.ErrSkillNotFound when the skill is not installed.
func (s *SkillsService) Pin(name string) error {
	return s.rewritePinnedEverywhere(name, true)
}

// Unpin sets pinned:false in the installed SKILL.md for name.
// Returns model.ErrSkillNotFound when the skill is not installed.
func (s *SkillsService) Unpin(name string) error {
	return s.rewritePinnedEverywhere(name, false)
}

// Remove deletes the skill directory for name. Returns model.ErrSkillPinned
// when the skill is pinned and force is false.
func (s *SkillsService) Remove(name string, force bool) error {
	if err := s.removeOne(name, force); err != nil {
		return err
	}
	for _, mirror := range s.mirrors {
		if err := (&SkillsService{skillsDir: mirror}).removeOne(name, force); err != nil && !errors.Is(err, model.ErrSkillNotFound) {
			return fmt.Errorf("service: skills: mirror %s: %w", mirror, err)
		}
	}
	return nil
}

func (s *SkillsService) removeOne(name string, force bool) error {
	skillDir := filepath.Join(s.skillsDir, name)
	skillMD := filepath.Join(skillDir, "SKILL.md")

	if _, err := os.Stat(skillMD); os.IsNotExist(err) {
		return fmt.Errorf("service: skills: remove %q: %w", name, model.ErrSkillNotFound)
	}

	if !force {
		data, readErr := os.ReadFile(skillMD)
		if readErr == nil {
			parsed, parseErr := skill.Parse(data)
			if parseErr == nil && parsed.Pinned {
				return fmt.Errorf("service: skills: remove %q: %w", name, model.ErrSkillPinned)
			}
		}
	}

	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("service: skills: remove %q: %w", name, err)
	}
	return nil
}

// Lint returns lint results for the named skill(s). When name is empty, all
// installed skills are linted. Returns model.ErrSkillNotFound when name is
// non-empty and the skill is not installed.
func (s *SkillsService) Lint(name string) ([]skill.LintResult, error) {
	var names []string

	if name != "" {
		skillDir := filepath.Join(s.skillsDir, name)
		if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); os.IsNotExist(err) {
			return nil, fmt.Errorf("service: skills: lint %q: %w", name, model.ErrSkillNotFound)
		}
		names = []string{name}
	} else {
		installed, err := s.installedNames()
		if err != nil {
			return nil, fmt.Errorf("service: skills: lint: list installed: %w", err)
		}
		names = installed
	}

	var results []skill.LintResult
	for _, n := range names {
		mdPath := filepath.Join(s.skillsDir, n, "SKILL.md")
		lr := skill.LintFile(mdPath, n)
		results = append(results, lr)
	}
	return results, nil
}

// Validate runs the validation/run.sh script for the named skill.
// Returns model.ErrSkillNotFound when the skill is not installed.
// Returns model.ErrSkillNoValidation when no validation/run.sh exists.
func (s *SkillsService) Validate(ctx context.Context, name string) (*skill.ValidateResult, error) {
	skillDir := filepath.Join(s.skillsDir, name)
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); os.IsNotExist(err) {
		return nil, fmt.Errorf("service: skills: validate %q: %w", name, model.ErrSkillNotFound)
	}

	result, err := skill.Validate(ctx, skillDir)
	if err != nil {
		if errors.Is(err, skill.ErrNoValidation) {
			return nil, fmt.Errorf("service: skills: validate %q: %w", name, model.ErrSkillNoValidation)
		}
		return nil, fmt.Errorf("service: skills: validate %q: %w", name, err)
	}
	return result, nil
}

// --- helpers ---

// installedNames returns the names of all skill directories found in skillsDir.
func (s *SkillsService) installedNames() ([]string, error) {
	entries, err := os.ReadDir(s.skillsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("service: skills: read dir %s: %w", s.skillsDir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only treat directories with a SKILL.md as valid skills.
		md := filepath.Join(s.skillsDir, e.Name(), "SKILL.md")
		if _, err := os.Stat(md); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// isBundled reports whether name appears in the embedded skill bundle.
func (s *SkillsService) isBundled(name string) bool {
	names, err := install.BundledSkillNames()
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// rewritePinned reads the installed SKILL.md, toggles the pinned field, and
// writes it back. Returns model.ErrSkillNotFound when the skill is not installed.
func (s *SkillsService) rewritePinned(name string, pinned bool) error {
	skillMD := filepath.Join(s.skillsDir, name, "SKILL.md")
	data, err := os.ReadFile(skillMD)
	if os.IsNotExist(err) {
		return fmt.Errorf("service: skills: %q: %w", name, model.ErrSkillNotFound)
	}
	if err != nil {
		return fmt.Errorf("service: skills: read %s: %w", skillMD, err)
	}

	updated, err := skill.RewritePinned(data, pinned)
	if err != nil {
		return fmt.Errorf("service: skills: rewrite pinned %q: %w", name, err)
	}

	if err := os.WriteFile(skillMD, updated, 0o644); err != nil {
		return fmt.Errorf("service: skills: write %s: %w", skillMD, err)
	}
	return nil
}

func (s *SkillsService) rewritePinnedEverywhere(name string, pinned bool) error {
	if err := s.rewritePinned(name, pinned); err != nil {
		return err
	}
	for _, mirror := range s.mirrors {
		if err := (&SkillsService{skillsDir: mirror}).rewritePinned(name, pinned); err != nil {
			return fmt.Errorf("service: skills: mirror %s: %w", mirror, err)
		}
	}
	return nil
}
