package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/model"
)

const workInputLimit = 10 << 20

func newWorkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "work",
		Short: "Manage delivery work contracts",
		Long: `Manage delivery-v2 WORK contracts.

Mutating operations require workflow.engine = "delivery_v2". Diagnose the
effective setting with "mneme config show workflow". The usual sequence is
begin -> lock -> implement -> review -> verify/complete. Review records the
independent broad or targeted assessment; verify evaluates factual checks
without changing state. metrics is read-only.

Pass larger JSON requests with --input <file>|-. See docs/api/cli.md for the
request fields, authority rules, and full response contract.`,
	}
	cmd.AddCommand(
		newWorkBeginCmd(),
		newWorkGetCmd(),
		newWorkLockCmd(),
		newWorkAmendCmd(),
		newWorkReviewCmd(),
		newWorkVerifyCmd(),
		newWorkCompleteCmd(),
		newWorkResumeCmd(),
		newWorkMetricsCmd(),
	)
	return cmd
}

func newWorkMetricsCmd() *cobra.Command {
	var limit int
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "metrics [WORK-ID...]",
		Short:   "Read local delivery metrics without changing work",
		Example: "  mneme work metrics WORK-001 WORK-002 --limit 20 --json",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var ids []string
			if len(args) > 0 {
				ids = append([]string(nil), args...)
			}
			result, err := callWorkForCommand(cmd, func(svc workService) (any, error) {
				return svc.WorkMetrics(cmd.Context(), model.WorkMetricsRequest{IDs: ids, Limit: limit})
			})
			if err != nil {
				return err
			}
			metrics := result.(model.WorkMetricsResponse)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), metrics)
			}
			return writeWorkMetrics(cmd.OutOrStdout(), metrics)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum detail rows for a project report")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newWorkReviewCmd() *cobra.Command {
	var input string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "review <id>",
		Short:   "Record one commit-bound initial work review",
		Example: "  mneme work review WORK-001 --input review.json --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := decodeWorkReviewRequest(cmd, input, args[0])
			if err != nil {
				return err
			}
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkReview(cmd.Context(), req)
			})
			if err != nil {
				return err
			}
			capability := result.(model.WorkCapabilityResult)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), capability)
			}
			return writeWorkCapability(cmd.OutOrStdout(), capability)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "JSON review file, or - for standard input")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func decodeWorkReviewRequest(cmd *cobra.Command, source, id string) (model.WorkReviewRequest, error) {
	var req model.WorkReviewRequest
	if err := decodeWorkInput(cmd, source, &req); err != nil {
		return model.WorkReviewRequest{}, err
	}
	req.ID = id
	return req, nil
}

func newWorkBeginCmd() *cobra.Command {
	var input string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "begin",
		Short:   "Create a draft work contract",
		Example: "  mneme work begin --input contract.json --json\n  cat contract.json | mneme work begin --input -",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var req model.WorkBeginRequest
			if err := decodeWorkInput(cmd, input, &req); err != nil {
				return err
			}
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkBegin(cmd.Context(), req)
			})
			if err != nil {
				return err
			}
			work := result.(model.WorkGetResponse)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), work)
			}
			return writeWorkSummary(cmd.OutOrStdout(), "CREADO", work)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "JSON request file, or - for standard input")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func newWorkGetCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "get <id>",
		Short:   "Read a complete work aggregate",
		Example: "  mneme work get WORK-001 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkGet(cmd.Context(), model.WorkGetRequest{ID: args[0]})
			})
			if err != nil {
				return err
			}
			work := result.(model.WorkGetResponse)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), work)
			}
			return writeWorkSummary(cmd.OutOrStdout(), "TRABAJO", work)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newWorkLockCmd() *cobra.Command {
	var by string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "lock <id>",
		Short:   "Lock a draft work contract",
		Example: "  mneme work lock WORK-001 --by orchestrator --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkLock(cmd.Context(), model.WorkLockRequest{ID: args[0], By: by})
			})
			if err != nil {
				return err
			}
			work := result.(model.WorkGetResponse)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), work)
			}
			return writeWorkSummary(cmd.OutOrStdout(), "BLOQUEADO", work)
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "Coordinator identity")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newWorkAmendCmd() *cobra.Command {
	var input string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "amend",
		Short:   "Amend a locked work contract",
		Example: "  mneme work amend --input amendment.json --json\n  cat amendment.json | mneme work amend --input -",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var req model.WorkAmendRequest
			if err := decodeWorkInput(cmd, input, &req); err != nil {
				return err
			}
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkAmend(cmd.Context(), req)
			})
			if err != nil {
				return err
			}
			work := result.(model.WorkGetResponse)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), work)
			}
			return writeWorkSummary(cmd.OutOrStdout(), "ENMENDADO", work)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "JSON request file, or - for standard input")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func newWorkVerifyCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "verify <id>",
		Short:   "Verify work against its delivery contract",
		Example: "  mneme work verify WORK-001 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := model.WorkActionRequest{ID: args[0]}
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkVerify(cmd.Context(), req)
			})
			if err != nil {
				return err
			}
			capability := result.(model.WorkCapabilityResult)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), capability)
			}
			return writeWorkCapability(cmd.OutOrStdout(), capability)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newWorkCompleteCmd() *cobra.Command {
	var by string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "complete <id>",
		Short:   "Close work using its latest persisted evidence",
		Example: "  mneme work complete WORK-001 --by orchestrator --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkComplete(cmd.Context(), model.WorkCompleteRequest{ID: args[0], By: by})
			})
			if err != nil {
				return err
			}
			capability := result.(model.WorkCapabilityResult)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), capability)
			}
			return writeWorkCapability(cmd.OutOrStdout(), capability)
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "Coordinator identity")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.MarkFlagRequired("by")
	return cmd
}

func newWorkResumeCmd() *cobra.Command {
	var by, reason string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "resume <id>",
		Short:   "Resume escalated work after a human decision",
		Example: `  mneme work resume WORK-001 --by orchestrator --reason "owner approved another round" --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := callWork(cmd, func(svc workService) (any, error) {
				return svc.WorkResume(cmd.Context(), model.WorkResumeRequest{ID: args[0], By: by, Reason: reason})
			})
			if err != nil {
				return err
			}
			work := result.(model.WorkGetResponse)
			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), work)
			}
			return writeWorkSummary(cmd.OutOrStdout(), "REANUDADO", work)
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "Coordinator identity")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for resuming work")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.MarkFlagRequired("by")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

type workService interface {
	WorkBegin(context.Context, model.WorkBeginRequest) (model.WorkGetResponse, error)
	WorkGet(context.Context, model.WorkGetRequest) (model.WorkGetResponse, error)
	WorkLock(context.Context, model.WorkLockRequest) (model.WorkGetResponse, error)
	WorkAmend(context.Context, model.WorkAmendRequest) (model.WorkGetResponse, error)
	WorkReview(context.Context, model.WorkReviewRequest) (model.WorkCapabilityResult, error)
	WorkVerify(context.Context, model.WorkActionRequest) (model.WorkCapabilityResult, error)
	WorkComplete(context.Context, model.WorkCompleteRequest) (model.WorkCapabilityResult, error)
	WorkResume(context.Context, model.WorkResumeRequest) (model.WorkGetResponse, error)
	WorkMetrics(context.Context, model.WorkMetricsRequest) (model.WorkMetricsResponse, error)
}

var callWorkForCommand = callWork

func callWork(cmd *cobra.Command, call func(workService) (any, error)) (any, error) {
	svc, cleanup, err := initSDDService()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return call(svc)
}

func decodeWorkInput(cmd *cobra.Command, source string, target any) error {
	data, err := readWorkInput(cmd, source)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode work input: %w", err)
	}
	return nil
}

func readWorkInput(cmd *cobra.Command, source string) ([]byte, error) {
	var reader io.Reader
	var closeFile func() error
	if source == "-" {
		reader = cmd.InOrStdin()
	} else {
		file, err := os.Open(source)
		if err != nil {
			return nil, fmt.Errorf("open work input: %w", err)
		}
		reader = file
		closeFile = file.Close
	}
	if closeFile != nil {
		defer func() { _ = closeFile() }()
	}
	data, err := io.ReadAll(io.LimitReader(reader, workInputLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read work input: %w", err)
	}
	if len(data) > workInputLimit {
		return nil, fmt.Errorf("work input exceeds 10 MiB")
	}
	return data, nil
}

func writeWorkSummary(w io.Writer, label string, work model.WorkGetResponse) error {
	source := string(work.Contract.SourceType)
	if work.Contract.SourceID != "" {
		source += ":" + work.Contract.SourceID
	}
	base := work.Contract.BaseSHA
	if base == "" {
		base = "-"
	}
	hash := work.Contract.ContractHash
	if hash == "" {
		hash = "-"
	} else if len(hash) > 12 {
		hash = hash[:12]
	}
	_, err := fmt.Fprintf(w, "%s %s source:%s status:%s revision:%d base:%s hash:%s criteria:%d constraints:%d findings:%d\n",
		label, work.Contract.ID, source, work.Contract.Status, work.Contract.ContractRevision, base, hash,
		len(work.Criteria), len(work.Constraints), len(work.Findings))
	return err
}

func writeWorkMetrics(w io.Writer, result model.WorkMetricsResponse) error {
	median, p95 := "-", "-"
	if result.Summary.CycleDuration.MedianMs != nil {
		median = fmt.Sprintf("%dms", *result.Summary.CycleDuration.MedianMs)
	}
	if result.Summary.CycleDuration.P95Ms != nil {
		p95 = fmt.Sprintf("%dms", *result.Summary.CycleDuration.P95Ms)
	}
	if _, err := fmt.Fprintf(w, "METRICAS %s total:%d legibles:%d ilegibles:%d draft:%d active:%d done:%d abandoned:%d duracion-terminal:%d/%dms mediana:%s p95:%s correcciones:%d escaladas:%d reanudaciones:%d evidencia-local:%d completa/%d parcial/%d no-iniciada\n",
		result.Project, result.Total, result.Included, result.UnreadableCount,
		result.Summary.Draft, result.Summary.Active, result.Summary.Done, result.Summary.Abandoned,
		result.Summary.CycleDuration.Count, result.Summary.CycleDuration.TotalMs, median, p95,
		result.Summary.AutomaticCorrections, result.Summary.Escalations, result.Summary.Resumptions,
		result.Summary.LocalEvidenceComplete, result.Summary.LocalEvidencePartial, result.Summary.LocalEvidenceNotStarted); err != nil {
		return err
	}
	for _, detail := range result.Details {
		duration := "no terminada"
		if detail.CycleDurationMs != nil {
			duration = fmt.Sprintf("%dms", *detail.CycleDurationMs)
		}
		evidence := string(detail.VerificationEvidence)
		if detail.VerificationEvidence == model.WorkMetricEvidencePartial {
			evidence = "certificados locales incompletos"
		}
		if _, err := fmt.Fprintf(w, "%s status:%s duracion:%s certificados-locales:%d tiempo-local:%dms evidencia:%s correcciones:%d escaladas:%d reanudaciones:%d\n",
			detail.ID, detail.Status, duration, detail.DeliveryCertificates, detail.LocalVerificationDurationMs,
			evidence, detail.AutomaticCorrections, detail.Escalations, detail.Resumptions); err != nil {
			return err
		}
	}
	return nil
}

func writeWorkCapability(w io.Writer, result model.WorkCapabilityResult) error {
	if result.Operation == "review" && result.Available && result.Performed && result.Certificate != nil {
		architecture := map[model.DeliveryCheckStatus]int{}
		for _, check := range result.Checks {
			if check.Kind == "architecture" {
				architecture[check.Status]++
			}
		}
		blocking, nonBlocking, resolved, newBlockers := 0, 0, 0, 0
		for _, finding := range result.Work.Findings {
			if finding.Category.Blocks() {
				blocking++
				if finding.Status != model.FindingOpen {
					resolved++
				}
				if finding.Status == model.FindingOpen && finding.ReviewPhase == model.ReviewPhaseTargeted {
					newBlockers++
				}
			} else {
				nonBlocking++
			}
		}
		mandateFindings, mandateChecks := 0, 0
		if result.CorrectionMandate != nil {
			mandateFindings = len(result.CorrectionMandate.BlockingFindings)
			mandateChecks = len(result.CorrectionMandate.BlockingChecks)
		}
		_, err := fmt.Fprintf(w, "REVISADO %s verdict:%s head:%s phase:%s next:%s round:%d resolved:%d new-blockers:%d mandate-findings:%d mandate-checks:%d architecture-pass:%d architecture-fail:%d architecture-not-reviewed:%d blocking-findings:%d non-blocking-findings:%d\n",
			result.Work.Contract.ID, result.Certificate.Verdict, result.Certificate.HeadSHA,
			result.ReviewPhase, result.NextStatus, result.Work.Contract.CorrectionRounds, resolved, newBlockers, mandateFindings, mandateChecks,
			architecture[model.DeliveryCheckPass], architecture[model.DeliveryCheckFail], architecture[model.DeliveryCheckNotReviewed],
			blocking, nonBlocking)
		return err
	}
	if result.Operation == "verify" && result.Available && result.Performed && result.Certificate != nil {
		counts := map[model.DeliveryCheckStatus]int{}
		for _, check := range result.Checks {
			counts[check.Status]++
		}
		hash := result.Work.Contract.ContractHash
		if len(hash) > 12 {
			hash = hash[:12]
		}
		_, err := fmt.Fprintf(w, "VERIFICADO %s verdict:%s head:%s revision:%d hash:%s pass:%d fail:%d not-reviewed:%d stopped:%d\n",
			result.Work.Contract.ID, result.Certificate.Verdict, result.Certificate.HeadSHA,
			result.Work.Contract.ContractRevision, hash,
			counts[model.DeliveryCheckPass], counts[model.DeliveryCheckFail],
			counts[model.DeliveryCheckNotReviewed], counts[model.DeliveryCheckSkipped])
		return err
	}
	if result.Operation == "complete" && result.Available && result.Performed && result.Certificate != nil {
		_, err := fmt.Fprintf(w, "CERRADO %s verdict:%s head:%s revision:%d checks:%d\n",
			result.Work.Contract.ID, result.Certificate.Verdict, result.Certificate.HeadSHA,
			result.Work.Contract.ContractRevision, len(result.Checks))
		return err
	}
	_, err := fmt.Fprintf(w, "NO DISPONIBLE %s: %s (%s)\n", result.Operation, result.Reason, result.ReasonCode)
	return err
}
