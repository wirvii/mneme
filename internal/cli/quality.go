package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// initQualityService creates a QualityService sharing the same
// config-loading, project-detection, and repoDir-fixing logic as
// initSDDService (SPEC-115 P6/P9) — repoDir is NEVER left to fall back to
// os.Getwd() in the quality path (D13), and the injected runner is always a
// real &quality.ExecRunner{} (a pointer, so it satisfies
// quality.TailBytesSetter — see runner.go).
//
// The caller MUST invoke the returned cleanup function when done.
func initQualityService() (*service.QualityService, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cfgPath := filepath.Join(home, ".mneme", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot determine working directory: %w", err)
	}

	slug := flagProject
	if slug == "" {
		det := project.NewDetector(cwd)
		detected, _ := det.DetectProject()
		slug = detected
	}

	var dbPath string
	if slug != "" {
		dbPath = cfg.ProjectDBPath(slug)
	} else {
		dbPath = cfg.GlobalDBPath()
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	cleanup := func() { _ = database.Close() }

	root, rootErr := repoRoot(cwd)
	if rootErr != nil {
		root = cwd
	}

	sddStore := store.NewSDDStore(database)
	qualitySvc := service.NewQualityService(sddStore, slug, root, &quality.ExecRunner{},
		service.WithMnemeVersion(Version))

	return qualitySvc, cleanup, nil
}

// newQualityCmd returns the "mneme quality" subcommand group (SPEC-115 D17):
// verify runs the declared gates and emits a certificate; status reports the
// constitution's and latest certificate's state without executing anything;
// ack records a human's justified approval of a finding.
func newQualityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quality",
		Short: "Quality constitution, certificates, and findings (SPEC-115)",
		Long: `Manage mneme's quality mechanism: a repository declares its build/test
gates in .mneme/quality.toml; mneme runs them and emits a certificate bound
to the exact commit. spec_advance then only COMPARES an already-emitted
certificate — it never executes anything itself.

Subcommands:
  verify  Run the declared gates and emit (or deny) a certificate.
  status  Report the constitution's and latest certificate's state.
  ack     Record a human's justified approval of a finding.`,
	}

	cmd.AddCommand(
		newQualityVerifyCmd(),
		newQualityStatusCmd(),
		newQualityAckCmd(),
	)

	return cmd
}

// newQualityVerifyCmd returns "mneme quality verify <SPEC-ID>".
func newQualityVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <spec-id>",
		Short: "Run the declared gates and emit a certificate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initQualityService()
			if err != nil {
				return err
			}
			defer cleanup()

			cert, err := svc.Verify(cmd.Context(), model.QualityVerifyRequest{ID: args[0]})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: certificate %s verdict=%s head_sha=%s dirty=%v\n",
				args[0], cert.ID, cert.Verdict, cert.HeadSHA, cert.Dirty)
			if cert.Verdict != model.QualityVerdictPass {
				return fmt.Errorf("quality verify: verdict is %s, not pass", cert.Verdict)
			}
			return nil
		},
	}
	return cmd
}

// newQualityStatusCmd returns "mneme quality status [SPEC-ID]".
func newQualityStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [spec-id]",
		Short: "Report the constitution's and latest certificate's state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initQualityService()
			if err != nil {
				return err
			}
			defer cleanup()

			var specID string
			if len(args) > 0 {
				specID = args[0]
			}

			resp, err := svc.Status(cmd.Context(), model.QualityStatusRequest{ID: specID})
			if err != nil {
				return err
			}

			fmt.Fprintln(os.Stdout, resp.Note)
			if resp.Exists {
				fmt.Fprintf(os.Stdout, "constitution: %s hash=%s enabled=%v gates=%v\n",
					resp.Path, resp.ConstitutionHash, resp.Enabled, resp.GateNames)
			}
			if resp.LatestCertificate != nil {
				cert := resp.LatestCertificate
				fmt.Fprintf(os.Stdout, "latest certificate: %s verdict=%s head_sha=%s created_at=%s\n",
					cert.ID, cert.Verdict, cert.HeadSHA, cert.CreatedAt.Format("2006-01-02T15:04:05Z"))
				for _, chk := range resp.Checks {
					fmt.Fprintf(os.Stdout, "  [%d] %s/%s: %s\n", chk.Seq, chk.Kind, chk.Name, chk.Status)
				}
			}
			// AC24: status is a read command — it always exits 0, even when
			// the mechanism is off or no certificate exists yet. Reporting
			// the truth is its entire job.
			return nil
		},
	}
	return cmd
}

// newQualityAckCmd returns "mneme quality ack <cert-id>".
func newQualityAckCmd() *cobra.Command {
	var (
		flagCheck         int
		flagBy            string
		flagJustification string
	)

	cmd := &cobra.Command{
		Use:   "ack <cert-id>",
		Short: "Record a human's justified approval of a finding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initQualityService()
			if err != nil {
				return err
			}
			defer cleanup()

			err = svc.Ack(cmd.Context(), model.QualityAckRequest{
				CertificateID: args[0],
				Seq:           flagCheck,
				By:            flagBy,
				Justification: flagJustification,
			})
			if err != nil {
				if errors.Is(err, model.ErrReasonRequired) {
					return fmt.Errorf("--by and --justification are both required")
				}
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: check %d acked by %s\n", args[0], flagCheck, flagBy)
			return nil
		},
	}

	cmd.Flags().IntVar(&flagCheck, "check", 0, "Seq of the finding to acknowledge")
	cmd.Flags().StringVar(&flagBy, "by", "", "Who is acknowledging the finding")
	cmd.Flags().StringVar(&flagJustification, "justification", "", "Why the finding is acceptable (required, non-empty)")

	return cmd
}
