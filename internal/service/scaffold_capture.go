package service

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// captureExcludeDirs are the directory names never copied into a captured
// scaffold (SPEC-100 §7c): VCS metadata and installed dependencies are noise a
// generated project reproduces itself, and copying them would bloat the profile
// repo. The author curates the rest of the "legacy vs template" question in the
// mneme-profile-author grill — capture only strips the unambiguous noise.
var captureExcludeDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// captureExcludeFiles are the root files skipped when detecting a monorepo's
// shell (OS/editor cruft that is never part of a team's template).
var captureExcludeFiles = map[string]bool{
	".DS_Store": true,
}

// ScaffoldCaptureInput configures ProfileService.CaptureScaffold (SPEC-100
// §7c): turn an exemplar repository into a draft scaffold in a profile repo the
// author is curating.
type ScaffoldCaptureInput struct {
	// Repo is the exemplar repository to capture from. Required.
	Repo string

	// Name is the scaffold's catalog name (a safe-slug). Empty derives it from
	// the exemplar's directory basename.
	Name string

	// Into is the profile repository the scaffold is written into (its
	// scaffolds/<name>/ and _blueprints/). Empty means the current working
	// directory.
	Into string
}

// ScaffoldCaptureResult reports the outcome of ProfileService.CaptureScaffold.
type ScaffoldCaptureResult struct {
	// Scaffold is the captured scaffold's name.
	Scaffold string `json:"scaffold"`

	// Layout is the inferred layout (single | monorepo).
	Layout string `json:"layout"`

	// Toolchain is the inferred toolchain (turborepo | custom); empty for a
	// single layout.
	Toolchain string `json:"toolchain,omitempty"`

	// ScaffoldTOMLPath is the absolute path of the generated scaffold.toml.
	ScaffoldTOMLPath string `json:"scaffold_toml_path"`

	// Blueprints are the composable apps captured into _blueprints/ (monorepo).
	Blueprints []string `json:"blueprints,omitempty"`

	// Vars are the [vars] names the draft declares for the author to curate.
	Vars []string `json:"vars"`

	// ProfileDir is the absolute profile repository the scaffold was written
	// into.
	ProfileDir string `json:"profile_dir"`
}

// CaptureScaffold captures an exemplar repository into a draft scaffold within
// a profile repo (SPEC-100 §7c, the deterministic half of the §15.6 authoring
// grill): it detects the exemplar's structure (apps/, packages/, turbo.json,
// go.mod/package.json identity), plans the capture (leaf, pure — infers
// layout/toolchain, drafts the scaffold.toml, enumerates the copies and the
// identity parametrization), then executes it — copying the shell/overlay/
// skeleton and each app blueprint with the exemplar's project name and module
// path rewritten to {{PROJECT_NAME}}/{{MODULE_PATH}} placeholders, and writing
// the validated scaffold.toml. It never bootstraps, never runs git, and never
// activates: the author curates the draft and commits it themselves.
func (s *ProfileService) CaptureScaffold(in ScaffoldCaptureInput) (*ScaffoldCaptureResult, error) {
	if in.Repo == "" {
		return nil, fmt.Errorf("service: profile: capture: exemplar repo is required")
	}

	repo, err := filepath.Abs(in.Repo)
	if err != nil {
		return nil, fmt.Errorf("service: profile: capture: %w", err)
	}
	info, err := os.Stat(repo)
	if err != nil {
		return nil, fmt.Errorf("service: profile: capture: exemplar repo %s: %w", in.Repo, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("service: profile: capture: exemplar repo %s is not a directory", in.Repo)
	}

	profileDir := in.Into
	if profileDir == "" {
		cwd, wderr := os.Getwd()
		if wderr != nil {
			return nil, fmt.Errorf("service: profile: capture: %w", wderr)
		}
		profileDir = cwd
	}
	profileDir, err = filepath.Abs(profileDir)
	if err != nil {
		return nil, fmt.Errorf("service: profile: capture: %w", err)
	}
	if pinfo, perr := os.Stat(profileDir); perr != nil || !pinfo.IsDir() {
		return nil, fmt.Errorf("service: profile: capture: profile dir %s must be an existing directory: %w", profileDir, model.ErrProfileNotFound)
	}

	name := in.Name
	if name == "" {
		name = deriveScaffoldName(repo)
	}

	rs, err := detectRepoStructure(repo)
	if err != nil {
		return nil, err
	}

	plan, err := profile.PlanCapture(name, rs)
	if err != nil {
		return nil, translateProfileError("service: profile: capture", err)
	}

	scaffoldDir := filepath.Join(profileDir, "scaffolds", name)
	if rerr := requireCaptureDestFree(scaffoldDir); rerr != nil {
		return nil, rerr
	}

	if err := executeCapture(repo, profileDir, plan); err != nil {
		return nil, err
	}

	return &ScaffoldCaptureResult{
		Scaffold:         name,
		Layout:           string(plan.Def.Layout),
		Toolchain:        string(plan.Def.Toolchain),
		ScaffoldTOMLPath: filepath.Join(profileDir, filepath.FromSlash(plan.ScaffoldTOMLDest)),
		Blueprints:       plan.Def.Blueprints,
		Vars:             sortedVarNames(plan.Def.Vars),
		ProfileDir:       profileDir,
	}, nil
}

// detectRepoStructure scans an exemplar repository (a real directory) into the
// pure profile.RepoStructure the leaf plans from (SPEC-100 §7c): it reads the
// root entries (regular files -> shell candidates; apps/ and packages/ ->
// monorepo signals), notes turbo.json / pnpm-workspace.yaml, and resolves the
// exemplar's identity (Go module path from go.mod, project name from
// package.json "name" or the directory basename).
func detectRepoStructure(repo string) (profile.RepoStructure, error) {
	entries, err := os.ReadDir(repo)
	if err != nil {
		return profile.RepoStructure{}, fmt.Errorf("service: profile: capture: read exemplar %s: %w", repo, err)
	}

	rs := profile.RepoStructure{ProjectName: filepath.Base(repo)}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			switch name {
			case "apps":
				rs.Apps = subdirNames(filepath.Join(repo, "apps"))
			case "packages":
				rs.HasPackages = true
			}
			continue
		}
		if captureExcludeFiles[name] {
			continue
		}
		switch name {
		case "turbo.json":
			rs.HasTurboJSON = true
		case "pnpm-workspace.yaml", "pnpm-workspace.yml":
			rs.HasPnpmWorkspace = true
		}
		rs.RootFiles = append(rs.RootFiles, name)
	}

	rs.ModulePath = readGoModulePath(filepath.Join(repo, "go.mod"))
	if pkgName := readPackageJSONName(filepath.Join(repo, "package.json")); pkgName != "" {
		rs.ProjectName = pkgName
	}
	return rs, nil
}

// subdirNames returns the immediate subdirectory names of dir (sorted, excluded
// noise removed), used to enumerate apps/ into blueprints.
func subdirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !captureExcludeDirs[e.Name()] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// readGoModulePath returns the module path declared by a go.mod file, or empty
// when the file is absent or declares none.
func readGoModulePath(goModPath string) string {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// readPackageJSONName returns the "name" field of a package.json, or empty when
// the file is absent or unparseable — a best-effort identity hint that falls
// back to the directory basename.
func readPackageJSONName(pkgPath string) string {
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return ""
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Name
}

// executeCapture performs a CapturePlan against the real filesystem (SPEC-100
// §7c, the service-executes half): it writes the validated scaffold.toml, then
// copies each planned tree/file from the exemplar into the profile repo,
// rewriting the exemplar's identity to placeholders as it goes.
func executeCapture(repo, profileDir string, plan profile.CapturePlan) error {
	tomlDest := filepath.Join(profileDir, filepath.FromSlash(plan.ScaffoldTOMLDest))
	if err := os.MkdirAll(filepath.Dir(tomlDest), 0o755); err != nil {
		return fmt.Errorf("service: profile: capture: mkdir %s: %w", filepath.Dir(tomlDest), err)
	}
	if err := os.WriteFile(tomlDest, plan.TOML, 0o644); err != nil {
		return fmt.Errorf("service: profile: capture: write scaffold.toml: %w", err)
	}

	for _, c := range plan.Copies {
		src := filepath.Join(repo, filepath.FromSlash(c.Src))
		dst := filepath.Join(profileDir, filepath.FromSlash(c.Dest))
		if c.IsDir {
			if err := requireCaptureDestFree(dst); err != nil {
				return err
			}
			if err := copyExemplarTree(src, dst, plan.Params); err != nil {
				return fmt.Errorf("service: profile: capture: copy %s: %w", c.Src, err)
			}
			continue
		}
		if err := copyExemplarFile(src, dst, plan.Params); err != nil {
			return fmt.Errorf("service: profile: capture: copy %s: %w", c.Src, err)
		}
	}
	return nil
}

// requireCaptureDestFree returns model.ErrProfileExists (wrapped) when dest
// already exists and is a non-empty directory — capture never overwrites an
// existing scaffold or blueprint, so an author never loses curated content to a
// re-run.
func requireCaptureDestFree(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("service: profile: capture: read dest %s: %w", dest, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("service: profile: capture: destination %s already exists and is not empty: %w", dest, model.ErrProfileExists)
	}
	return nil
}

// copyExemplarTree recursively copies src into dst, skipping excluded
// directories (.git, node_modules) and applying the capture's identity
// parametrization to every copied file's contents.
func copyExemplarTree(src, dst string, params map[string]string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != src && captureExcludeDirs[d.Name()] {
			return filepath.SkipDir
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyExemplarFile(p, target, params)
	})
}

// copyExemplarFile copies one file from src to dst, creating parent
// directories and rewriting the exemplar's identity literals to their
// {{VAR}} placeholders in the file's contents.
func copyExemplarFile(src, dst string, params map[string]string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	data = profile.ParametrizeContent(data, params)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// deriveScaffoldName turns an exemplar's directory basename into a safe-slug
// candidate: lowercased, with every run of non-[a-z0-9] rewritten to a single
// hyphen and leading hyphens trimmed. A caller can always override it with an
// explicit name; PlanCapture still validates the result, so a pathological
// basename surfaces as ErrInvalidScaffold rather than silently misbehaving.
func deriveScaffoldName(repo string) string {
	base := strings.ToLower(filepath.Base(repo))
	var b strings.Builder
	prevHyphen := false
	for _, r := range base {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// sortedVarNames returns the [vars] keys in lexical order for a stable result.
func sortedVarNames(vars map[string]profile.VarSpec) []string {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
