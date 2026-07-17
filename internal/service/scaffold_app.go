package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// AppAddInput configures ProfileService.AddApp (SPEC-099 §7b): drop a
// composable app (a blueprint of the monorepo's originating scaffold) into an
// existing monorepo and auto-wire it into the root files.
type AppAddInput struct {
	// Blueprint is the _blueprints/<name> archetype to instantiate. Required;
	// must be one the monorepo's scaffold declares.
	Blueprint string

	// Name is the directory name the new app takes under the scaffold's apps
	// directory. Required; must be a safe-slug.
	Name string

	// Dir is the monorepo root the app is added to. Empty means the current
	// working directory.
	Dir string

	// Vars are the caller-supplied substitution values, merged over the
	// scaffold's declared [vars] defaults.
	Vars map[string]string

	// Scaffold optionally overrides which scaffold archetype supplies the
	// blueprint catalog. Empty reads it from the monorepo's pin (pin.Scaffold).
	Scaffold string
}

// AppAddResult reports the outcome of ProfileService.AddApp.
type AppAddResult struct {
	// Blueprint is the archetype that was instantiated.
	Blueprint string `json:"blueprint"`

	// App is the name the app took in the monorepo.
	App string `json:"app"`

	// Scaffold is the archetype (pin.Scaffold) whose catalog was read.
	Scaffold string `json:"scaffold"`

	// Profile is the active profile that provided the blueprint.
	Profile string `json:"profile"`

	// AppPath is the absolute directory the blueprint was copied into.
	AppPath string `json:"app_path"`

	// Wired lists the monorepo-root-relative files the wiring touched (or would
	// have touched — a no-op workspace edit still lists the file).
	Wired []string `json:"wired"`
}

// AddApp adds one composable app to an existing monorepo (SPEC-099 §7b AC10):
// it reads the monorepo's pin to learn which scaffold generated it, resolves
// the active profile to obtain the blueprint catalog, plans the copy + wiring
// (leaf, pure), then executes it — copying _blueprints/<blueprint> into the
// scaffold's apps directory under Name and applying the root-file wiring edits
// (Turborepo built-in adapter, or the scaffold's declared [wiring]). It never
// runs git init (the monorepo already has its .git) and never commits.
//
// A single-layout scaffold yields ErrAppAddNotApplicable; a blueprint the
// scaffold does not offer yields ErrScaffoldNotFound; a non-empty target app
// directory yields ErrProfileExists.
func (s *ProfileService) AddApp(ctx context.Context, in AppAddInput) (*AppAddResult, error) {
	if in.Blueprint == "" {
		return nil, fmt.Errorf("service: profile: add app: blueprint is required")
	}
	if in.Name == "" {
		return nil, fmt.Errorf("service: profile: add app: app name is required")
	}

	root := in.Dir
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("service: profile: add app: %w", err)
		}
		root = cwd
	}
	monorepoRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("service: profile: add app: %w", err)
	}

	scaffoldName, err := s.resolveScaffoldName(monorepoRoot, in.Scaffold)
	if err != nil {
		return nil, err
	}

	profileFS, activePin, err := s.resolveActiveProfile(monorepoRoot)
	if err != nil {
		return nil, err
	}

	scaffold, err := profile.FindScaffold(profileFS, scaffoldName)
	if err != nil {
		return nil, translateProfileError("service: profile: add app", err)
	}

	vars := scaffold.ResolveVars(in.Vars)
	plan, err := profile.PlanAddApp(scaffold, profile.AddAppChoices{
		Blueprint:    in.Blueprint,
		AppName:      in.Name,
		MonorepoRoot: monorepoRoot,
		Vars:         vars,
	})
	if err != nil {
		return nil, translateProfileError("service: profile: add app", err)
	}

	appPath := monorepoRoot
	if len(plan.Blueprints) > 0 {
		appPath = plan.Blueprints[0].Dest
		if err := requireEmptyOrAbsent(appPath); err != nil {
			return nil, err
		}
	}

	if err := s.executePlan(ctx, plan, profileFS, monorepoRoot); err != nil {
		return nil, err
	}

	return &AppAddResult{
		Blueprint: in.Blueprint,
		App:       in.Name,
		Scaffold:  scaffoldName,
		Profile:   activePin.Name,
		AppPath:   appPath,
		Wired:     wiredFiles(plan.Wiring),
	}, nil
}

// resolveScaffoldName determines which scaffold archetype's blueprint catalog
// to read: the explicit override when given, else the monorepo's own pin
// (.mneme-profile's scaffold field, recorded by /new-project). A monorepo whose
// pin records no scaffold (and no override) is a hard error — app add needs to
// know the archetype to offer blueprints from.
func (s *ProfileService) resolveScaffoldName(monorepoRoot, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	res, err := s.ResolvePin(monorepoRoot)
	if err != nil {
		return "", err
	}
	if res.Pin == nil || res.Pin.Scaffold == "" {
		return "", fmt.Errorf("service: profile: add app: this repository's pin records no scaffold — pass --scaffold or run `mneme project new` to birth a monorepo: %w", model.ErrScaffoldNotFound)
	}
	return res.Pin.Scaffold, nil
}

// wiredFiles collects the distinct monorepo-root-relative files a wiring plan
// touches, for the result's human-facing report.
func wiredFiles(edits []profile.WiringEdit) []string {
	seen := make(map[string]struct{})
	var files []string
	for _, e := range edits {
		f := e.File
		if e.Kind == profile.WiringEditCopy {
			f = e.Dest
		}
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		files = append(files, f)
	}
	return files
}

// applyWiringEdit performs one WiringEdit against the real monorepo (SPEC-099
// §7b §4.3, the service-executes half): a workspace-yaml package insert, a JSON
// node member merge, or a template-fragment copy. profileFS is the active
// profile's contents FS (copy sources resolve through it); monorepoRoot is the
// absolute repo root every File is relative to.
func applyWiringEdit(profileFS fs.FS, monorepoRoot string, edit profile.WiringEdit) error {
	switch edit.Kind {
	case profile.WiringEditWorkspace:
		return ensureWorkspacePackage(filepath.Join(monorepoRoot, filepath.FromSlash(edit.File)), edit.Entry)
	case profile.WiringEditJSONMerge:
		return mergeJSONMember(filepath.Join(monorepoRoot, filepath.FromSlash(edit.File)), edit.JSONPath, edit.Entry)
	case profile.WiringEditCopy:
		return copyFSDirSubst(profileFS, edit.Src, edit.Dest, edit.Vars)
	default:
		return fmt.Errorf("service: profile: wiring: unknown edit kind %q", edit.Kind)
	}
}

// ensureWorkspacePackage adds entry (e.g. "apps/myapp") to the packages: list
// of a pnpm-style workspace yaml, a no-op when an existing glob already covers
// it (SPEC-099 §7b AC8). It is deliberately a line-oriented edit — mneme
// carries no YAML dependency, and pnpm-workspace.yaml's packages block is a
// flat sequence — so it preserves the rest of the file verbatim. An absent file
// is created with a fresh packages block.
func ensureWorkspacePackage(yamlPath, entry string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(yamlPath, []byte("packages:\n  - \""+entry+"\"\n"), 0o644)
		}
		return fmt.Errorf("service: profile: wiring: read %s: %w", yamlPath, err)
	}

	lines := strings.Split(string(data), "\n")
	pkgLine := -1
	lastItem := -1
	itemIndent := "  "
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if pkgLine == -1 {
			if trimmed == "packages:" || strings.HasPrefix(trimmed, "packages:") && strings.HasSuffix(trimmed, ":") {
				pkgLine = i
			}
			continue
		}
		// Inside the packages block: sequence items begin with "-".
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			lastItem = i
			if idx := strings.IndexByte(ln, '-'); idx > 0 {
				itemIndent = ln[:idx]
			}
			if covered(unquoteYAML(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))), entry) {
				return nil // an existing glob (or exact match) already covers it
			}
			continue
		}
		if trimmed == "" {
			continue // blank lines may separate items
		}
		// A non-item, non-blank line ends the packages block.
		break
	}

	if pkgLine == -1 {
		// No packages: key — append a fresh block.
		out := strings.TrimRight(string(data), "\n") + "\npackages:\n  - \"" + entry + "\"\n"
		return os.WriteFile(yamlPath, []byte(out), 0o644)
	}

	newItem := itemIndent + "- \"" + entry + "\""
	insertAt := lastItem + 1
	if lastItem == -1 {
		insertAt = pkgLine + 1
	}
	updated := make([]string, 0, len(lines)+1)
	updated = append(updated, lines[:insertAt]...)
	updated = append(updated, newItem)
	updated = append(updated, lines[insertAt:]...)
	return os.WriteFile(yamlPath, []byte(strings.Join(updated, "\n")), 0o644)
}

// covered reports whether an existing packages entry already accounts for the
// entry being added: an exact match, or a glob (via path.Match) that matches it
// — the "apps/* already covers apps/foo" no-op case.
func covered(existing, entry string) bool {
	if existing == entry {
		return true
	}
	if ok, err := path.Match(existing, entry); err == nil && ok {
		return true
	}
	return false
}

// unquoteYAML strips a single pair of matching single or double quotes from a
// scalar, leaving unquoted scalars untouched.
func unquoteYAML(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// mergeJSONMember inserts key (mapping to an empty object) under the top-level
// node addressed by jsonPath in a JSON file (SPEC-099 §7b §4.3, the json-merge
// action). An empty jsonPath inserts key at the document root. The member is a
// no-op when already present. Output is re-marshalled with 2-space indent;
// encoding/json sorts object keys, so the result is deterministic. An absent
// file is created with the single member.
func mergeJSONMember(jsonPath, node, key string) error {
	doc := map[string]any{}
	data, err := os.ReadFile(jsonPath)
	switch {
	case err == nil:
		if uerr := json.Unmarshal(data, &doc); uerr != nil {
			return fmt.Errorf("service: profile: wiring: parse %s: %w", jsonPath, uerr)
		}
	case errors.Is(err, os.ErrNotExist):
		// create below
	default:
		return fmt.Errorf("service: profile: wiring: read %s: %w", jsonPath, err)
	}

	target := doc
	if node != "" {
		child, _ := doc[node].(map[string]any)
		if child == nil {
			child = map[string]any{}
			doc[node] = child
		}
		target = child
	}
	if _, exists := target[key]; !exists {
		target[key] = map[string]any{}
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("service: profile: wiring: marshal %s: %w", jsonPath, err)
	}
	return os.WriteFile(jsonPath, append(out, '\n'), 0o644)
}
