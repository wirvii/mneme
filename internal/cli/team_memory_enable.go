package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newTeamMemoryEnableCmd returns the "mneme team-memory enable" subcommand
// (SPEC-053 SS-F / SPEC-065). It is the single opt-in entry point that turns
// on git-native team memory sharing for the current repository: it creates
// .mneme/shared/ with its marker, exports existing durable knowledge into it,
// and installs the same import hooks "mneme team-memory hooks install"
// would — all in one idempotent step.
func newTeamMemoryEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Turn on git-native team memory sharing for this repository",
		Long: `Activate the shared team-memory vault (SPEC-053) for the current repository.

This single command:
  1. Creates <repo>/.mneme/shared/ with its .mneme-vault marker, if not
     already present.
  2. Bakes shared=1 onto every pre-existing decision/convention/architecture/
     pattern/bugfix/rule memory that is still local-only, and exports every
     shared memory to notes/<uuid>.md.
  3. Installs the post-merge/post-checkout git hooks that import teammates'
     shared knowledge automatically (same as "mneme team-memory hooks
     install").

Idempotent: running it again re-bakes/re-exports anything not yet shared and
leaves already-installed hooks untouched. It never overwrites a vault marker
belonging to a different project.

Committing .mneme/shared/ makes its contents visible to anyone who can read
this repository, including its full commit history once pushed. Review the
privacy notice this command prints before pushing, especially on a public
remote.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("team-memory enable: cannot determine cwd: %w", err)
			}

			root, err := repoRoot(cwd)
			if err != nil {
				return fmt.Errorf("team-memory enable: %w", err)
			}

			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			result, err := svc.EnableTeamMemory(cmd.Context(), root)
			if err != nil {
				return fmt.Errorf("team-memory enable: %w", err)
			}

			hooksDir, err := gitHooksDir(cwd)
			if err != nil {
				return fmt.Errorf("team-memory enable: %w", err)
			}
			for _, hookName := range teamMemoryHooksTargetHooks {
				hookPath := filepath.Join(hooksDir, hookName)
				if appendErr := appendTeamMemoryMarkedBlock(hookPath); appendErr != nil {
					return fmt.Errorf("team-memory enable: install hook %s: %w", hookName, appendErr)
				}
			}

			out := cmd.OutOrStdout()
			if result.AlreadyEnabled {
				fmt.Fprintf(out, "Team memory already enabled at %s\n", result.VaultRoot)
			} else {
				fmt.Fprintf(out, "Team memory enabled at %s\n", result.VaultRoot)
			}
			fmt.Fprintf(out, "Baked %d pre-existing memories to shared, %d exported to the vault.\n", result.Baked, result.Exported)
			fmt.Fprintf(out, "Installed import hooks: %s\n", strings.Join(teamMemoryHooksTargetHooks, ", "))
			for _, f := range result.GitattrsFindings {
				fmt.Fprintf(out, "[gitattributes] %s\n", f)
			}
			fmt.Fprintln(out)
			printTeamMemoryPrivacyNotice(out, root)

			return nil
		},
	}
}

// printTeamMemoryPrivacyNotice prints an explicit privacy warning about
// .mneme/shared/ being committed to the repository.
//
// Team-memory is deliberately git-native and offline (SPEC-053 D9 "cero
// red") — mneme never makes a network call to ask GitHub/GitLab/etc. whether
// the remote is public, so visibility can never be reliably determined from
// here. Per SPEC-065 ("aviso de privacidad ... cuando el remote es público, o
// SIEMPRE si no puede determinarlo"), the notice is therefore always printed,
// listing whatever remote URL git reports locally so the user can judge for
// themselves without mneme needing to phone home.
func printTeamMemoryPrivacyNotice(out io.Writer, repoRoot string) {
	fmt.Fprintln(out, "PRIVACY NOTICE: .mneme/shared/ is committed to this repository.")
	fmt.Fprintln(out, "Every memory materialized there (decisions, conventions, architecture,")
	fmt.Fprintln(out, "patterns, bugfixes, rules) becomes visible to anyone who can read this repo,")
	fmt.Fprintln(out, "including its full commit history once pushed.")

	if remote := gitRemoteURL(repoRoot); remote != "" {
		fmt.Fprintf(out, "Remote: %s\n", remote)
	}
	fmt.Fprintln(out, "mneme cannot determine offline whether this remote is public — if it is")
	fmt.Fprintln(out, "(or might become public), review .mneme/shared/ before pushing.")
}

// gitRemoteURL returns the "origin" remote URL for repoRoot, or "" when there
// is none or it cannot be determined. Best-effort, local-only (runs "git
// remote get-url", never a network call) — used purely for display in the
// privacy notice.
func gitRemoteURL(repoRoot string) string {
	out, err := runGitCmd(repoRoot, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
