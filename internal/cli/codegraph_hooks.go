package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/service"
)

// hooksMarkerBegin and hooksMarkerEnd are the sentinel lines that delimit the
// mneme-managed block inside a git hook script. The remove subcommand only
// strips the region between these two lines (inclusive), so any other content
// in the hook is preserved.
const (
	hooksMarkerBegin = "# >>> mneme codegraph (SPEC-044) >>>"
	hooksMarkerEnd   = "# <<< mneme codegraph (SPEC-044) <<<"
)

// hooksManagedBlock is the exact content injected between the markers. The
// shebang is prepended separately when the file is created from scratch; this
// block is appended to existing hooks that already carry a shebang.
//
// The command is run detached in background (trailing &) so the git operation
// that triggered the hook is never blocked or slowed by the re-index. Using
// "$(command -v mneme || echo mneme)" makes the script degrade gracefully when
// mneme is not on PATH rather than causing a syntax error.
const hooksManagedBlock = hooksMarkerBegin + `
# Auto-reindex the code graph after this git event. Managed by ` + "`mneme codegraph hooks`" + `.
# Skipped during rebase/merge/cherry-pick to avoid storms.
"$(command -v mneme || echo mneme)" codegraph hooks run-reindex >/dev/null 2>&1 &
` + hooksMarkerEnd

// hooksTargetHooks is the list of git hook file names that the install command
// manages. post-commit fires after every commit; post-checkout fires after
// branch switches and file-level checkouts.
var hooksTargetHooks = []string{"post-commit", "post-checkout"}

// newCodegraphHooksCmd returns the "mneme codegraph hooks" parent command that
// groups the install, remove, and (hidden) run-reindex subcommands.
func newCodegraphHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage git hooks for automatic code-graph re-indexing",
		Long: `Install or remove git hooks that keep the code graph fresh automatically.

After installation, every git commit and branch checkout triggers an incremental
re-index of the code graph in the background. The git operation itself is never
blocked; the re-index is detached with &.

The hooks are appended with markers so any pre-existing hook content is
preserved. Running install twice is safe (idempotent).`,
	}
	cmd.AddCommand(
		newCodegraphHooksInstallCmd(),
		newCodegraphHooksRemoveCmd(),
		newCodegraphHooksRunReindexCmd(),
	)
	return cmd
}

// newCodegraphHooksInstallCmd returns the "mneme codegraph hooks install"
// subcommand. It adds the mneme-managed re-index block to the post-commit and
// post-checkout hooks of the current repository, creating the hook files and
// the #!/bin/sh shebang if they do not already exist. The operation is
// idempotent: if the block is already present the command succeeds without
// duplicating it.
func newCodegraphHooksInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install auto-reindex git hooks in the current repository",
		Long: `Append the mneme re-index block to post-commit and post-checkout hooks.

The current working directory must be inside a git repository. Hooks are
located via "git rev-parse --git-path hooks" so custom core.hooksPath settings
and git worktrees are respected.

If a hook file already exists its content is preserved; the mneme block is
appended. Running install a second time is a no-op (the block is not
duplicated). New hook files are created with a #!/bin/sh shebang and 0755
permissions.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("codegraph hooks install: cannot determine cwd: %w", err)
			}

			hooksDir, err := gitHooksDir(cwd)
			if err != nil {
				return fmt.Errorf("codegraph hooks install: %w", err)
			}

			for _, hookName := range hooksTargetHooks {
				hookPath := filepath.Join(hooksDir, hookName)
				if appendErr := appendMarkedBlock(hookPath); appendErr != nil {
					return fmt.Errorf("codegraph hooks install: %s: %w", hookName, appendErr)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Installed hook: %s\n", hookPath)
			}
			return nil
		},
	}
}

// newCodegraphHooksRemoveCmd returns the "mneme codegraph hooks remove"
// subcommand. It removes only the mneme-managed block (delimited by the
// hooksMarkerBegin/End sentinels) from each hook file. The rest of the hook
// is untouched. If a hook has no mneme block the command prints a no-op
// message and exits successfully.
func newCodegraphHooksRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the auto-reindex block from git hooks",
		Long: `Strip the mneme-managed block from post-commit and post-checkout hooks.

Only the region between the mneme markers is removed; all other content in the
hook file is preserved. The hook file itself is not deleted. If no mneme block
is present the command exits successfully without modifying anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("codegraph hooks remove: cannot determine cwd: %w", err)
			}

			hooksDir, err := gitHooksDir(cwd)
			if err != nil {
				return fmt.Errorf("codegraph hooks remove: %w", err)
			}

			for _, hookName := range hooksTargetHooks {
				hookPath := filepath.Join(hooksDir, hookName)
				removed, removeErr := removeMarkedBlock(hookPath)
				if removeErr != nil {
					return fmt.Errorf("codegraph hooks remove: %s: %w", hookName, removeErr)
				}
				if removed {
					fmt.Fprintf(cmd.OutOrStdout(), "Removed mneme block from: %s\n", hookPath)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "No mneme block found in: %s (no-op)\n", hookPath)
				}
			}
			return nil
		},
	}
}

// newCodegraphHooksRunReindexCmd returns the hidden "mneme codegraph hooks
// run-reindex" subcommand. It is invoked by the installed git hooks and must
// never propagate errors (exit 0 always), so git commits and checkouts are
// never affected by index failures. Index failures are appended to
// ~/.mneme/codegraph-hooks.log for post-hoc inspection.
//
// The command skips silently when any of the following in-progress git
// operations are detected (checked via <git-dir>/rebase-merge,
// rebase-apply, MERGE_HEAD, CHERRY_PICK_HEAD):
//   - interactive or non-interactive rebase
//   - merge in progress
//   - cherry-pick in progress
//
// This prevents storms of redundant re-index runs during a multi-commit rebase.
func newCodegraphHooksRunReindexCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run-reindex",
		Short:  "Run incremental code-graph re-index (invoked by git hooks)",
		Hidden: true, // internal sub-command; not shown in --help
		Args:   cobra.NoArgs,
		// RunE must never return an error — callers rely on exit-0-always semantics.
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runCodegraphHooksReindex(); err != nil {
				// Log but do not propagate — the git operation must not be affected.
				_ = logHookFailure(err)
			}
			return nil
		},
	}
}

// Coalesce-lock file names and staleness TTL for the run-reindex hook (SPEC-101).
const (
	// reindexLockName is the pidfile created (atomically, O_EXCL) inside the git
	// directory while a re-index is running. Its existence IS the lock.
	reindexLockName = "mneme-reindex.lock"

	// reindexDirtyName is the marker a coalesced (locked-out) invocation touches
	// so the current holder knows more commits arrived and runs one catch-up pass.
	reindexDirtyName = "mneme-reindex.dirty"

	// reindexLockTTL bounds how long a lockfile is trusted before it is treated
	// as stale (holder crashed without releasing) and stolen. A scoped re-index
	// is a matter of seconds, so ten minutes is a very generous ceiling. Staleness
	// is judged by mtime — the portable, syscall-free alternative to probing the
	// recorded PID for liveness (which would need build-tagged platform code).
	reindexLockTTL = 10 * time.Minute
)

// runCodegraphHooksReindex performs the actual incremental re-index. It is a
// standalone function (not a method) so it can be called and its return value
// inspected in tests without needing a full Cobra invocation.
//
// Flow (SPEC-101): skip during rebase/merge/cherry-pick; then take a coalesce
// lock so concurrent git events never stack multiple indexing processes. An
// invocation that cannot take the lock leaves a dirty marker and exits; the
// holder consumes that marker with a single catch-up pass on the way out. The
// last_sha invariant guarantees any commit dropped by the lock is picked up by
// the next diff, so coalescing can discard work without ever losing a file.
func runCodegraphHooksReindex() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("run-reindex: cannot determine cwd: %w", err)
	}

	// 1. Detect git-dir and skip (before taking any lock) if a rebase/merge/
	//    cherry-pick is in progress.
	gd, err := gitDir(cwd)
	if err != nil {
		return fmt.Errorf("run-reindex: git dir: %w", err)
	}
	if reindexInProgress(gd) {
		return nil // skip silently during rebase/merge/cherry-pick
	}

	// 2. Resolve repo root for indexing.
	root, err := repoRoot(cwd)
	if err != nil {
		return fmt.Errorf("run-reindex: repo root: %w", err)
	}

	// 3. Coalesce lock: only one indexing pass per git-dir at a time.
	lock, err := acquireReindexLock(gd)
	if err != nil {
		return fmt.Errorf("run-reindex: acquire lock: %w", err)
	}
	if !lock.acquired {
		// Another run holds the lock — record that work is pending and exit 0.
		touchReindexDirty(gd)
		return nil
	}
	defer lock.release()

	// 4. Init service with project detection from cwd.
	svc, err := initCodeGraphServiceForCWD(cwd)
	if err != nil {
		return fmt.Errorf("run-reindex: init service: %w", err)
	}
	defer func() { _ = svc.Close() }()

	// 5. One indexing pass (scoped from last_sha, or full-scan fallback).
	if err := reindexOnce(svc, root); err != nil {
		return err
	}

	// 6. Catch-up: if commits arrived while we were indexing, a coalesced
	//    invocation left the dirty marker. Consume it and run exactly one more
	//    pass, which recomputes the range from the (now advanced) last_sha.
	dirtyPath := filepath.Join(gd, reindexDirtyName)
	if fileExists(dirtyPath) {
		_ = os.Remove(dirtyPath)
		if err := reindexOnce(svc, root); err != nil {
			return err
		}
	}
	return nil
}

// gitEligibleFiles returns the repo paths (relative to root) that git
// considers part of the working tree and NOT gitignored — tracked files plus
// untracked-but-not-ignored files (SPEC-102 decision B) — via a single `git
// ls-files` call. ok is false when root is not inside a git worktree or git
// is unavailable, signalling the caller to fall back to the legacy filesystem
// walk (Include=nil).
//
// `--cached` selects tracked files, `--others` adds untracked ones, and
// `--exclude-standard` applies .gitignore, .git/info/exclude, and the global
// excludes file to the untracked set. git prunes ignored directories
// internally while resolving this, so a large ignored directory (e.g.
// tmp/testhome) is never walked — this is what makes ls-files both simpler
// and faster than enumerating candidates first and running them through
// `git check-ignore`. -z (NUL-separated output) avoids git's path quoting,
// mirroring the robustness of parseDiff's own parsing.
func gitEligibleFiles(root string) (paths []string, ok bool) {
	out, err := runGitCmd(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false
	}
	for _, p := range strings.Split(out, "\x00") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	return paths, true
}

// fullScanOptions builds the codegraph.IndexOptions for a full scan rooted at
// root, honouring .gitignore when root is inside a git repository and falling
// back to the indexer's legacy filesystem walk (ignoredDirs + hidden-dir skip)
// otherwise (SPEC-102). Callers overlay any additional fields (Language,
// DryRun, Force) on the returned value.
func fullScanOptions(root string, force bool) codegraph.IndexOptions {
	opts := codegraph.IndexOptions{RootDir: root, Force: force}
	if files, ok := gitEligibleFiles(root); ok {
		opts.Include = files
	}
	return opts
}

// reindexOnce runs a single indexing pass for the repository rooted at root.
// It reads the last indexed SHA, diffs it against HEAD, and indexes only the
// delta (scoped mode). It falls back to a full scan when there is no recorded
// SHA, when the recorded SHA no longer exists (gc/squash/rebase dropped it), or
// when the diff itself fails. last_sha is advanced to HEAD only after the index
// succeeds — never before — so a failed or discarded pass leaves the anchor put.
func reindexOnce(svc *service.CodeGraphService, root string) error {
	last, err := svc.LastIndexedSHA()
	if err != nil {
		return fmt.Errorf("run-reindex: read last sha: %w", err)
	}

	headOut, err := runGitCmd(root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("run-reindex: rev-parse HEAD: %w", err)
	}
	head := strings.TrimSpace(headOut)
	if head == "" {
		return fmt.Errorf("run-reindex: empty HEAD")
	}

	switch {
	case last == "" || !gitCommitExists(root, last):
		// First run, or the anchor was garbage-collected: index the whole tree.
		if _, err := svc.Index(fullScanOptions(root, false)); err != nil {
			return err
		}

	case last == head:
		// Nothing changed since the last successful index — do not re-stamp.
		return nil

	default:
		// Scoped: index exactly the files in the last_sha..HEAD delta. The tree
		// diff form (last..head) is symmetric, so it works across branch switches
		// and rewritten history, not just fast-forwards.
		diffOut, diffErr := runGitCmd(root, "diff", "--name-status", "-M", last+".."+head)
		if diffErr != nil {
			// The anchor existed but the diff failed unexpectedly — degrade to a
			// full scan rather than lose the update.
			if _, err := svc.Index(fullScanOptions(root, false)); err != nil {
				return err
			}
		} else {
			changes := parseDiff(diffOut)
			if _, err := svc.Index(codegraph.IndexOptions{RootDir: root, Changes: changes}); err != nil {
				return err
			}
		}
	}

	if err := svc.SetLastIndexedSHA(head); err != nil {
		return fmt.Errorf("run-reindex: stamp last sha: %w", err)
	}
	return nil
}

// parseDiff turns the output of `git diff --name-status -M` into a git-agnostic
// list of ChangedFile the scoped indexer consumes. Each line is tab-separated:
//
//	A|M|D|T<TAB>path
//	R<score><TAB>old<TAB>new
//	C<score><TAB>src<TAB>dst
//
// The first character of the status field selects the ChangeStatus. Renames map
// to ChangeRenamed (old→new); copies map to a ChangeAdded of the destination;
// type-changes (T) are treated as a modification. Unknown/unmerged statuses and
// malformed lines are skipped. The result is always non-nil (possibly empty) so
// callers stay on the scoped path even when the delta contains no files.
func parseDiff(nameStatus string) []codegraph.ChangedFile {
	changes := []codegraph.ChangedFile{}
	for _, raw := range strings.Split(nameStatus, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		switch fields[0][0] {
		case 'A':
			changes = append(changes, codegraph.ChangedFile{Path: fields[1], Status: codegraph.ChangeAdded})
		case 'M':
			changes = append(changes, codegraph.ChangedFile{Path: fields[1], Status: codegraph.ChangeModified})
		case 'T':
			// Type change (e.g. file ↔ symlink): re-extract as a modification.
			changes = append(changes, codegraph.ChangedFile{Path: fields[1], Status: codegraph.ChangeModified})
		case 'D':
			changes = append(changes, codegraph.ChangedFile{Path: fields[1], Status: codegraph.ChangeDeleted})
		case 'R':
			if len(fields) >= 3 {
				changes = append(changes, codegraph.ChangedFile{
					OldPath: fields[1], Path: fields[2], Status: codegraph.ChangeRenamed,
				})
			}
		case 'C':
			// Copy: the source is untouched; only the destination is new.
			if len(fields) >= 3 {
				changes = append(changes, codegraph.ChangedFile{Path: fields[2], Status: codegraph.ChangeAdded})
			}
		}
	}
	return changes
}

// reindexLock is the handle returned by acquireReindexLock. When acquired is
// true the caller owns the lock and must call release() (typically via defer);
// when false another process holds it and the caller must not index.
type reindexLock struct {
	path     string
	acquired bool
}

// acquireReindexLock attempts to take the coalesce lock for gitDir. The lock is
// a pidfile created with O_CREATE|O_EXCL — the atomic exclusive create IS the
// lock, which is portable to every OS mneme runs on without flock/syscall.Kill
// or build-tagged platform files. If the file already exists it is treated as a
// live lock unless its mtime is older than reindexLockTTL, in which case the
// holder is presumed dead, the stale file is removed, and the lock is retried
// once.
func acquireReindexLock(gitDir string) (*reindexLock, error) {
	lockPath := filepath.Join(gitDir, reindexLockName)

	lock, err := tryCreateReindexLock(lockPath)
	if err != nil {
		return nil, err
	}
	if lock.acquired {
		return lock, nil
	}

	// The lock exists. Decide whether it is stale (crashed holder) by mtime.
	info, statErr := os.Stat(lockPath)
	if statErr != nil {
		// Vanished between the failed create and the stat — the holder released
		// it. Retry once; whatever this returns is authoritative.
		return tryCreateReindexLock(lockPath)
	}
	if time.Since(info.ModTime()) > reindexLockTTL {
		_ = os.Remove(lockPath)
		return tryCreateReindexLock(lockPath)
	}
	return lock, nil // held by a live process
}

// tryCreateReindexLock performs one atomic O_EXCL create attempt, writing
// "PID\ntimestamp" for diagnostics. It returns acquired=true on success and
// acquired=false (no error) when the file already exists; any other error is
// returned.
func tryCreateReindexLock(lockPath string) (*reindexLock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return &reindexLock{path: lockPath, acquired: false}, nil
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "%d\n%d\n", os.Getpid(), time.Now().Unix())
	_ = f.Close()
	return &reindexLock{path: lockPath, acquired: true}, nil
}

// release removes the lockfile. It is safe to call on a non-acquired lock (no-op)
// and safe to call multiple times.
func (l *reindexLock) release() {
	if l != nil && l.acquired {
		_ = os.Remove(l.path)
		l.acquired = false
	}
}

// touchReindexDirty creates (or refreshes the mtime of) the dirty marker inside
// gitDir, signalling the current lock holder that more commits arrived and a
// catch-up pass is warranted. All failures are ignored — the marker is a
// best-effort latency optimisation, not a correctness requirement.
func touchReindexDirty(gitDir string) {
	dirtyPath := filepath.Join(gitDir, reindexDirtyName)
	if f, err := os.OpenFile(dirtyPath, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()
	}
	now := time.Now()
	_ = os.Chtimes(dirtyPath, now, now)
}

// fileExists reports whether path exists (of any type). Used for the dirty
// marker check.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// gitCommitExists reports whether sha resolves to an existing commit object in
// the repository at root. It uses `git rev-parse --verify --quiet <sha>^{commit}`
// which exits non-zero (captured as an error) when the object is absent — the
// signal to fall back to a full scan.
func gitCommitExists(root, sha string) bool {
	_, err := runGitCmd(root, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	return err == nil
}

// initCodeGraphServiceForCWD is a variant of initCodeGraphService that derives
// the project slug from an explicit cwd rather than os.Getwd(). This makes the
// run-reindex path explicit and testable.
func initCodeGraphServiceForCWD(cwd string) (*service.CodeGraphService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cfgPath := filepath.Join(home, ".mneme", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}

	slug := flagProject
	if slug == "" {
		det := project.NewDetector(cwd)
		detected, _ := det.DetectProject()
		slug = detected
	}

	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
	return service.NewCodeGraphService(projectsDir, slug)
}

// gitHooksDir resolves the hooks directory for the repository rooted at (or
// containing) cwd. It uses "git rev-parse --git-path hooks" so that custom
// core.hooksPath configurations and git worktrees are handled correctly. If
// cwd is not inside a git repository an error is returned.
func gitHooksDir(cwd string) (string, error) {
	out, err := runGitCmd(cwd, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", fmt.Errorf("not a git repository (or any of the parent directories): %w", err)
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		// git may return a path relative to cwd when inside a worktree.
		dir = filepath.Join(cwd, dir)
	}
	return dir, nil
}

// gitDir resolves the .git directory path for the repository containing cwd
// using "git rev-parse --git-dir". The result is needed to check for in-progress
// rebase/merge/cherry-pick state files.
func gitDir(cwd string) (string, error) {
	out, err := runGitCmd(cwd, "rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return dir, nil
}

// repoRoot returns the top-level directory of the repository containing cwd,
// using "git rev-parse --show-toplevel". This is the directory that is passed
// to the indexer as its root when the hooks fire.
func repoRoot(cwd string) (string, error) {
	out, err := runGitCmd(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// runGitCmd runs git with the provided arguments in the given working directory
// and returns the combined stdout output. A non-zero exit code is treated as an
// error with the stderr message included.
func runGitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// appendMarkedBlock appends the mneme-managed block to the hook file at
// hookPath. If the file does not exist it is created with a #!/bin/sh shebang.
// The function is idempotent: if the marker begin line is already present the
// file is not modified. The hook file is always set to 0755 (git requires hooks
// to be executable).
func appendMarkedBlock(hookPath string) error {
	// Read existing content (if any).
	existing, readErr := os.ReadFile(hookPath)
	var content string
	if readErr == nil {
		content = string(existing)
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read hook file: %w", readErr)
	}

	// Idempotence: do not append if the begin marker is already present.
	if strings.Contains(content, hooksMarkerBegin) {
		return nil
	}

	// Build the new content.
	var sb strings.Builder
	if content == "" {
		// New file: add shebang.
		sb.WriteString("#!/bin/sh\n")
	} else {
		sb.WriteString(content)
		// Ensure there is a newline before our block.
		if !strings.HasSuffix(content, "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(hooksManagedBlock)
	sb.WriteByte('\n')

	// Write atomically enough for our use case: write and chmod.
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks directory: %w", err)
	}
	if err := os.WriteFile(hookPath, []byte(sb.String()), 0o755); err != nil {
		return fmt.Errorf("write hook file: %w", err)
	}
	// Ensure executable bit is set even when the file already existed.
	return os.Chmod(hookPath, 0o755)
}

// removeMarkedBlock removes the mneme-managed block (from hooksMarkerBegin to
// hooksMarkerEnd inclusive) from the hook file at hookPath. All content outside
// the markers is preserved. Returns (true, nil) when the block was found and
// removed, or (false, nil) when no block was present (no-op). The file is not
// deleted even if it becomes empty after removal.
func removeMarkedBlock(hookPath string) (removed bool, err error) {
	data, readErr := os.ReadFile(hookPath)
	if os.IsNotExist(readErr) {
		return false, nil // file absent → nothing to remove
	}
	if readErr != nil {
		return false, fmt.Errorf("read hook file: %w", readErr)
	}

	content := string(data)
	beginIdx := strings.Index(content, hooksMarkerBegin)
	if beginIdx < 0 {
		return false, nil // no block present
	}

	endIdx := strings.Index(content, hooksMarkerEnd)
	if endIdx < 0 {
		// Malformed: begin marker present but no end marker. Remove from begin to EOF.
		endIdx = len(content) - len(hooksMarkerEnd)
	}
	// endIdx points to the start of the end marker; advance past the full line.
	afterEnd := endIdx + len(hooksMarkerEnd)
	if afterEnd < len(content) && content[afterEnd] == '\n' {
		afterEnd++ // consume the trailing newline of the end marker line
	}

	newContent := content[:beginIdx] + content[afterEnd:]

	// Trim a spurious leading blank line that may appear if the block was at the
	// very start of the file (just after the shebang newline).
	newContent = strings.TrimRight(newContent, "\n")
	if newContent != "" {
		newContent += "\n"
	}

	if writeErr := os.WriteFile(hookPath, []byte(newContent), 0o755); writeErr != nil {
		return false, fmt.Errorf("write hook file: %w", writeErr)
	}
	return true, nil
}

// reindexInProgress reports whether any in-progress git operation that spawns
// many post-commit/post-checkout events is currently active. It checks for the
// presence of sentinel files inside the git directory that git creates during
// rebase, merge, and cherry-pick operations.
//
// Returning true causes run-reindex to skip the index without error, preventing
// storms of redundant re-index runs during an interactive rebase that may fire
// dozens of post-checkout hooks.
func reindexInProgress(gitDir string) bool {
	sentinels := []string{
		"rebase-merge",     // git rebase -i (interactive) in progress
		"rebase-apply",     // git rebase (non-interactive) / git am in progress
		"MERGE_HEAD",       // git merge in progress
		"CHERRY_PICK_HEAD", // git cherry-pick in progress
	}
	for _, s := range sentinels {
		if _, err := os.Stat(filepath.Join(gitDir, s)); err == nil {
			return true
		}
	}
	return false
}

// logHookFailure appends a single timestamped line to ~/.mneme/codegraph-hooks.log.
// The caller is responsible for passing the error value; the function silently
// ignores any write failure so it never itself causes a non-zero exit.
func logHookFailure(cause error) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil // fail silently
	}
	logPath := filepath.Join(home, ".mneme", "codegraph-hooks.log")

	cwd, _ := os.Getwd() // best-effort

	line := fmt.Sprintf("[%s] repo=%s error=%v\n",
		time.Now().UTC().Format(time.RFC3339),
		cwd,
		cause,
	)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil // fail silently
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
	return nil
}
