package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
)

// bootstrapTimeout caps how long a scaffold's official-generator subprocess
// may run before it is killed — a network bootstrap can be slow, but it must
// never hang an unattended MCP session indefinitely (SPEC-098 §7a §4.4).
const bootstrapTimeout = 300 * time.Second

// Bootstrapper runs a scaffold's pinned official generator (docs/profiles-
// design.md §15.4 layer 1). It is a Strategy injected into ProfileService
// (WithProfileBootstrapper), mirroring how WithDefaultProfileFS/WithTeamMemory
// inject their own collaborators: production wires NewExecBootstrapper (a real
// `pnpm dlx`/`npx` subprocess); tests wire a fake that materializes a fixture
// tree with ZERO network, so `go test ./...` never touches the network
// (SPEC-098 §7a §4.4, the double network guard).
type Bootstrapper interface {
	// Run executes step's pinned generator, writing into step.Dest. It must be
	// deterministic given a pinned version and never prompt (CI/--yes).
	Run(ctx context.Context, step profile.BootstrapStep) error
}

// execBootstrapper is the production Bootstrapper: it shells out to a package
// runner (pnpm dlx / npx) to run the official generator pinned in the
// scaffold. It is the one genuinely new network subprocess the profiles EPIC
// introduces — hence isolated behind the Bootstrapper seam and never
// constructed in a test.
type execBootstrapper struct {
	// runner is the package runner: "pnpm" (default, the repo-map standard) or
	// "npx". A scaffold can select it in a later spec; §7a hard-defaults pnpm.
	runner string
}

// NewExecBootstrapper returns the production Bootstrapper (pnpm dlx). The
// frontends that expose `project new`/`project_new` wire it via
// WithProfileBootstrapper; every other ProfileService construction leaves the
// seam nil, since only a bootstrap-bearing (monorepo, §7b) plan needs it.
func NewExecBootstrapper() Bootstrapper {
	return &execBootstrapper{runner: "pnpm"}
}

// Run executes the pinned generator subprocess. It fails fast with
// model.ErrBootstrapToolMissing when the runner is absent from PATH (an
// actionable precondition, never a panic), and wraps a generator failure with
// its combined output so the caller can build an actionable message. The
// version is guaranteed exact by ParseScaffold's ErrBootstrapNotPinned
// invariant, so two runs of the same scaffold produce the same base tree.
func (b *execBootstrapper) Run(ctx context.Context, step profile.BootstrapStep) error {
	runner := b.runner
	if runner == "" {
		runner = "pnpm"
	}
	if _, err := exec.LookPath(runner); err != nil {
		return fmt.Errorf("service: scaffold: bootstrap runner %q not found — install it and retry: %w", runner, model.ErrBootstrapToolMissing)
	}

	spec := step.Generator + "@" + step.Version
	var args []string
	switch runner {
	case "npx":
		args = []string{"--yes", spec, step.Dest, "--no-install"}
	default: // pnpm
		args = []string{"dlx", spec, step.Dest, "--no-install"}
	}

	tctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()

	// #nosec G204 -- runner is a fixed literal (pnpm/npx); spec is built from a
	// safe-slug generator + a ParseScaffold-validated exact version; dest is a
	// caller-chosen path. Never raw, unvalidated shell input.
	cmd := exec.CommandContext(tctx, runner, args...)
	cmd.Env = append(os.Environ(), "CI=1")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("service: scaffold: bootstrap %s: %w: %s", spec, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ProjectNewInput configures ProfileService.NewProject (SPEC-098 §7a): grow a
// brand-new project repository from a scaffold in the active profile's catalog.
type ProjectNewInput struct {
	// Scaffold is the catalog name to assemble (a scaffolds/<name>/ entry of
	// the active profile). Required.
	Scaffold string

	// Dir is the destination directory. Required; must be empty or absent.
	Dir string

	// Vars are the caller-supplied substitution values, merged over the
	// scaffold's declared [vars] defaults (ScaffoldDef.ResolveVars).
	Vars map[string]string

	// ProjectRoot is the directory from which the ACTIVE profile is resolved
	// (pin > host default > vanilla) — NOT the destination. Empty means the
	// process's current working directory.
	ProjectRoot string
}

// ProjectNewResult reports the outcome of ProfileService.NewProject.
type ProjectNewResult struct {
	// Scaffold is the assembled scaffold's name.
	Scaffold string `json:"scaffold"`

	// Profile is the active profile that provided the catalog (recorded in the
	// fresh repo's pin).
	Profile string `json:"profile"`

	// Layout is the assembled scaffold's layout (single, in §7a).
	Layout string `json:"layout"`

	// Path is the absolute destination directory the project was assembled
	// into.
	Path string `json:"path"`

	// PinPath is the absolute path of the .mneme-profile pin written into the
	// fresh repo, with scaffold=<name> recorded (§4.6).
	PinPath string `json:"pin_path"`
}

// NewProject assembles a brand-new project repository from a scaffold in the
// active profile's catalog (SPEC-098 §7a): it resolves the active profile
// (pin > host default > vanilla) to obtain both the scaffold catalog and the
// identity to pin, finds the named scaffold, plans the assembly (leaf, pure),
// executes it (bootstrap → copy skeleton with variable substitution → git
// init), and writes the fresh repo's .mneme-profile pin with scaffold=<name>
// plus the active profile's identity — so the repo is born pointing at the
// profile that generated it. NewProject never commits or sets a remote
// (precedent §1/§5) and never activates: the /new-project skill chains
// mneme-init over the fresh repo for that (§4.7).
//
// The destination must be empty or absent — assembling over existing content
// is rejected (model.ErrProfileExists) before anything is written.
func (s *ProfileService) NewProject(ctx context.Context, in ProjectNewInput) (*ProjectNewResult, error) {
	if in.Scaffold == "" {
		return nil, fmt.Errorf("service: profile: new project: scaffold name is required")
	}
	if in.Dir == "" {
		return nil, fmt.Errorf("service: profile: new project: destination dir is required")
	}

	dest, err := filepath.Abs(in.Dir)
	if err != nil {
		return nil, fmt.Errorf("service: profile: new project: %w", err)
	}
	if err := requireEmptyOrAbsent(dest); err != nil {
		return nil, err
	}

	projectRoot := in.ProjectRoot
	if projectRoot == "" {
		cwd, wderr := os.Getwd()
		if wderr != nil {
			return nil, fmt.Errorf("service: profile: new project: %w", wderr)
		}
		projectRoot = cwd
	}

	profileFS, pin, err := s.resolveActiveProfile(projectRoot)
	if err != nil {
		return nil, err
	}

	scaffold, err := profile.FindScaffold(profileFS, in.Scaffold)
	if err != nil {
		return nil, translateProfileError("service: profile: new project", err)
	}

	vars := scaffold.ResolveVars(in.Vars)
	plan, err := profile.PlanNewProject(scaffold, profile.ProjectChoices{Dest: dest, Vars: vars})
	if err != nil {
		return nil, translateProfileError("service: profile: new project", err)
	}

	if err := s.executePlan(ctx, plan, profileFS, dest); err != nil {
		return nil, err
	}

	pin.Scaffold = in.Scaffold
	if err := profile.WritePin(dest, pin); err != nil {
		return nil, translateProfileError("service: profile: new project", err)
	}

	return &ProjectNewResult{
		Scaffold: in.Scaffold,
		Profile:  pin.Name,
		Layout:   string(scaffold.Layout),
		Path:     dest,
		PinPath:  filepath.Join(dest, profile.PinFileName),
	}, nil
}

// resolveActiveProfile resolves the profile active for projectRoot (pin > host
// default > vanilla) into both the filesystem its scaffold catalog is read
// from and the pin identity a fresh repo should record. A store-backed profile
// (the repo's own pin or the host default, PinInstalled) yields os.DirFS of
// its checkout plus a reproducible pin reconstructed via PinFromStore; the
// embedded OSS default (PinDefault, or vanilla PinAbsent) yields s.defaultFS
// plus a sourceless pin. A pinned-but-uninstalled profile (PinMissing) is a
// hard, actionable error (`profile add` first).
func (s *ProfileService) resolveActiveProfile(projectRoot string) (fs.FS, *profile.Pin, error) {
	res, err := s.ResolveActive(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	r := res.Resolution

	switch r.State {
	case profile.PinInstalled:
		pinRes, perr := s.store.PinFromStore(r.Pin.Name)
		if perr != nil {
			return nil, nil, translateProfileError("service: profile: new project", perr)
		}
		return os.DirFS(r.Path), pinRes.Pin, nil

	case profile.PinDefault:
		if s.defaultFS == nil {
			return nil, nil, fmt.Errorf("service: profile: new project: %w", model.ErrDefaultProfileUnavailable)
		}
		name := r.Pin.Name
		if name == "" {
			name = profile.DefaultProfileName
		}
		return s.defaultFS, &profile.Pin{Name: name}, nil

	case profile.PinAbsent:
		if s.defaultFS == nil {
			return nil, nil, fmt.Errorf("service: profile: new project: %w", model.ErrDefaultProfileUnavailable)
		}
		return s.defaultFS, &profile.Pin{Name: profile.DefaultProfileName}, nil

	case profile.PinMissing:
		name := ""
		if r.Pin != nil {
			name = r.Pin.Name
		}
		return nil, nil, fmt.Errorf("service: profile: new project: active profile %q is not installed — run `mneme profile add` first: %w", name, model.ErrProfileNotFound)

	default:
		return nil, nil, fmt.Errorf("service: profile: new project: unresolvable active profile state")
	}
}

// executePlan performs an AssemblyPlan against the real filesystem (SPEC-098
// §7a §4.2, the service-executes half): the optional pinned bootstrap
// subprocess, then each skeleton/overlay copy with variable substitution, then
// a plain git init at dest. profileFS is the active profile's contents FS
// (os.DirFS of a checkout, or the embedded default) — every CopyStep.Src is
// resolved through it, so a store profile and the embedded default share one
// read path.
func (s *ProfileService) executePlan(ctx context.Context, plan profile.AssemblyPlan, profileFS fs.FS, dest string) error {
	if plan.Bootstrap != nil {
		if s.bootstrapper == nil {
			return fmt.Errorf("service: profile: new project: scaffold needs a bootstrap but no bootstrapper is wired: %w", model.ErrProfileServiceNotConfigured)
		}
		if err := s.bootstrapper.Run(ctx, *plan.Bootstrap); err != nil {
			return err
		}
	}

	for _, cp := range plan.Copies {
		if cp.Optional {
			if _, err := fs.Stat(profileFS, cp.Src); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue // an optional overlay/shell the scaffold does not ship
				}
				return fmt.Errorf("service: profile: new project: stat %s: %w", cp.Src, err)
			}
		}
		if err := copyFSDirSubst(profileFS, cp.Src, cp.Dest, cp.Vars); err != nil {
			return fmt.Errorf("service: profile: new project: %w", err)
		}
	}

	for _, bp := range plan.Blueprints {
		if err := copyFSDirSubst(profileFS, bp.Src, bp.Dest, bp.Vars); err != nil {
			return fmt.Errorf("service: profile: new project: blueprint %s: %w", bp.Src, err)
		}
	}

	for _, edit := range plan.Wiring {
		if err := applyWiringEdit(profileFS, dest, edit); err != nil {
			return fmt.Errorf("service: profile: new project: wiring: %w", err)
		}
	}

	if plan.GitInit {
		if err := profile.GitInit(dest); err != nil {
			return fmt.Errorf("service: profile: new project: %w", err)
		}
	}
	return nil
}

// requireEmptyOrAbsent returns model.ErrProfileExists (wrapped) when dest
// exists and is a non-empty directory — assembling a new project over existing
// content is never allowed. An absent dest, or an empty one, is fine.
func requireEmptyOrAbsent(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("service: profile: new project: read dest %s: %w", dest, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("service: profile: new project: destination %s is not empty: %w", dest, model.ErrProfileExists)
	}
	return nil
}

// copyFSDirSubst recursively copies every file under src (a path within fsys)
// to the same relative location under dst (a real directory), substituting
// every "{{<var>}}" occurrence in each file's contents (SPEC-098 §7a §4.2 step
// 2). It mirrors copyFSDir (the profile-activation copy path) but adds the
// substitution pass — kept separate so activation, which copies verbatim, is
// never accidentally coupled to scaffold variable substitution.
func copyFSDirSubst(fsys fs.FS, src, dst string, vars map[string]string) error {
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
			return fmt.Errorf("copy scaffold: read %s: %w", p, readErr)
		}
		data = substituteVars(data, vars)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("copy scaffold: mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("copy scaffold: write %s: %w", target, err)
		}
		return nil
	})
}

// substituteVars replaces every "{{<key>}}" in data with its value. Exact
// match only (no whitespace-tolerant `{{ key }}`) — deterministic and
// dependency-free, matching the plan's golden-testable contract.
func substituteVars(data []byte, vars map[string]string) []byte {
	if len(vars) == 0 {
		return data
	}
	s := string(data)
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return []byte(s)
}
