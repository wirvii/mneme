package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/codegraph"
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

	// SPEC-117 P10: a second, lightweight SDDService sharing the SAME
	// store and repoDir, constructed ONLY to hand its SpecDocWrite to
	// QualityService as the docWriter seam (WithDocWriter) — never a
	// dependency on *SDDService itself, and never a construction cycle
	// between the two services (D12's own design note).
	sddSvcForDocs := service.NewSDDService(sddStore, cfg, slug, nil)
	sddSvcForDocs.WithRepoDir(root)

	opts := []service.QualityOption{
		service.WithMnemeVersion(Version),
		service.WithWorkflowDir(cfg.WorkflowDir()),
		service.WithDocWriter(sddSvcForDocs.SpecDocWrite),
	}

	// SPEC-118 P14: the SAME project's code graph, opened via the exact
	// codegraph.DBPath route initCodeGraphService already uses for every
	// codegraph_* command — never a new location. Absent a resolvable
	// project slug there is no graph to open; runBudgetChecks' own
	// nil-graphFacts posture (D5/G21) already covers that case correctly
	// (a firmable finding, never a silent pass).
	if slug != "" {
		projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
		cdb, graphErr := codegraph.OpenDB(codegraph.DBPath(projectsDir, slug))
		if graphErr == nil {
			graphStore := codegraph.NewStore(cdb)
			opts = append(opts, service.WithGraphFacts(service.NewGraphFacts(graphStore)))
			prevCleanup := cleanup
			cleanup = func() { _ = cdb.Close(); prevCleanup() }
		}
	}

	qualitySvc := service.NewQualityService(sddStore, slug, root, &quality.ExecRunner{}, opts...)

	return qualitySvc, cleanup, nil
}

// newQualityCmd returns the "mneme quality" subcommand group (SPEC-115 D17,
// extended by SPEC-116 D15 with the "baseline" group, and by SPEC-117 S3
// with "sign"/"report"): verify runs the declared gates and emits a
// certificate; status reports the constitution's and latest certificate's
// state without executing anything; ack records a human's justified
// approval of a finding; sign records a qa-tester's attestation of a
// criterion; report generates the QA report from the latest certificate;
// baseline manages the ratchet's registered coverage mark. Every
// subcommand hangs off this SAME AddCommand call, so the top-level command
// count stays 42 (V15 of the SPEC-117 design) — only `mneme quality --help`
// gains entries.
func newQualityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quality",
		Short: "Quality constitution, certificates, findings, and the coverage ratchet",
		Long: `Manage mneme's quality mechanism: a repository declares its build/test
gates in .mneme/quality.toml; mneme runs them and emits a certificate bound
to the exact commit. spec_advance then only COMPARES an already-emitted
certificate — it never executes anything itself.

Subcommands:
  verify    Run the declared gates and emit (or deny) a certificate.
  status    Report the constitution's and latest certificate's state.
  ack       Record a human's justified approval of a finding.
  sign      Record a qa-tester's attestation that a criterion holds (SPEC-117).
  report    Generate the QA report from the spec's latest certificate (SPEC-117).
  baseline  Manage the ratchet's registered coverage baseline (SPEC-116).`,
	}

	cmd.AddCommand(
		newQualityVerifyCmd(),
		newQualityStatusCmd(),
		newQualityAckCmd(),
		newQualitySignCmd(),
		newQualityReportCmd(),
		newQualityBaselineCmd(),
	)

	return cmd
}

// newQualitySignCmd returns "mneme quality sign <cert-id>" (SPEC-117 S3
// D11): a qa-tester's attestation that a criterion row genuinely holds —
// a verb distinct from ack (an approval), restricted to the qa-tester role
// for a subagent caller by internal/cli/hook.go's roleScopedTools.
func newQualitySignCmd() *cobra.Command {
	var (
		flagCheck    int
		flagBy       string
		flagEvidence string
	)

	cmd := &cobra.Command{
		Use:   "sign <cert-id>",
		Short: "Record a qa-tester's attestation that a criterion holds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initQualityService()
			if err != nil {
				return err
			}
			defer cleanup()

			err = svc.Sign(cmd.Context(), model.QualitySignRequest{
				CertificateID: args[0],
				Seq:           flagCheck,
				By:            flagBy,
				Evidence:      flagEvidence,
			})
			if err != nil {
				if errors.Is(err, model.ErrReasonRequired) {
					return fmt.Errorf("--by and --evidence are both required")
				}
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: check %d signed by %s\n", args[0], flagCheck, flagBy)
			return nil
		},
	}

	cmd.Flags().IntVar(&flagCheck, "check", 0, "Seq of the criterion row to sign")
	cmd.Flags().StringVar(&flagBy, "by", "", "Who is signing (the qa-tester)")
	cmd.Flags().StringVar(&flagEvidence, "evidence", "", "What was verified and how (required, non-empty)")

	return cmd
}

// newQualityReportCmd returns "mneme quality report <spec-id>" (SPEC-117
// S3 D12): render the QA report from the spec's latest certificate and
// write it via spec_doc_write's kind qa-report.
func newQualityReportCmd() *cobra.Command {
	var flagForce bool

	cmd := &cobra.Command{
		Use:   "report <spec-id>",
		Short: "Generate the QA report from the spec's latest certificate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initQualityService()
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := svc.Report(cmd.Context(), model.QualityReportRequest{ID: args[0], Force: flagForce})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "%s: qa-report written to %s (%d bytes, certificate %s)\n",
				args[0], resp.Path, resp.Bytes, resp.CertificateID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Overwrite an existing qa-report.md even if it lacks mneme's generation marker")

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
			// SPEC-119 D14: the declared mutation config plus the latest
			// certificate's own equivalent-signature count against the
			// declared cupo — read-only, never re-parses a report.
			if m := resp.Mutation; m != nil {
				fmt.Fprintf(os.Stdout, "mutation: format=%s report_path=%s equivalent=%d/%d survivors=%d\n",
					m.Format, m.ReportPath, m.SignedEquivalent, m.MaxEquivalent, m.SurvivorCount)
			}
			// SPEC-120 D14: the declared visual config plus the latest
			// certificate's own figures — read-only, never re-parses a report.
			if v := resp.Visual; v != nil {
				fmt.Fprintf(os.Stdout, "visual: format=%s declared_targets=%d compare_enabled=%v verified=%d failed=%d missing_references=%d\n",
					v.Format, v.DeclaredTargets, v.CompareEnabled, v.VerifiedTargets, v.FailedTargets, v.MissingReferences)
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

// newQualityBaselineCmd returns "mneme quality baseline" (SPEC-116 D15):
// update writes the registered ratchet baseline from a spec's latest PASS
// certificate; show reports it read-only. Neither is exposed over MCP
// (D15) — writing the baseline is a governance act over a versioned file,
// the same class as hand-editing the constitution.
func newQualityBaselineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "baseline",
		Short: "Manage the ratchet's registered coverage baseline (SPEC-116)",
		Long: `The ratchet compares the repository's current global coverage against a
REGISTERED baseline — a measurement mneme itself took, never a number
anyone typed. This baseline is a versioned file
(.mneme/quality-baseline.toml); these subcommands are the ONLY way it is
written, and only from an already-green certificate.`,
	}

	cmd.AddCommand(newQualityBaselineUpdateCmd(), newQualityBaselineShowCmd())

	return cmd
}

// newQualityBaselineUpdateCmd returns "mneme quality baseline update <spec-id>".
func newQualityBaselineUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <spec-id>",
		Short: "Write the baseline from the spec's latest PASS certificate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initQualityService()
			if err != nil {
				return err
			}
			defer cleanup()

			baseline, err := svc.BaselineUpdate(cmd.Context(), model.QualityBaselineUpdateRequest{ID: args[0]})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stdout, "linea base actualizada: %.2f%% (sha=%s, certificado=%s)\n",
				baseline.GlobalLinePct, baseline.MeasuredAtSHA, baseline.CertificateID)
			return nil
		},
	}
	return cmd
}

// newQualityBaselineShowCmd returns "mneme quality baseline show".
func newQualityBaselineShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the registered baseline",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initQualityService()
			if err != nil {
				return err
			}
			defer cleanup()

			path := filepath.Join(svc.RepoDir(), quality.BaselineRelPath)
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				if os.IsNotExist(readErr) {
					fmt.Fprintln(os.Stdout, "sin linea base registrada — `mneme quality baseline update <spec-id>`")
					return nil
				}
				return readErr
			}

			baseline, err := quality.ParseBaseline(raw)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "linea base: %.2f%% (sha=%s, medido=%s, certificado=%s)\n",
				baseline.GlobalLinePct, baseline.MeasuredAtSHA, baseline.MeasuredAt.Format("2006-01-02"), baseline.CertificateID)
			return nil
		},
	}
	return cmd
}
