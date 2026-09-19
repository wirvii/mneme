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
	)
	return cmd
}

func newWorkReviewCmd() *cobra.Command {
	var input string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "review <id>",
		Short: "Record one commit-bound initial work review",
		Args:  cobra.ExactArgs(1),
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
		Use:   "begin",
		Short: "Create a draft work contract",
		Args:  cobra.NoArgs,
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
		Use:   "get <id>",
		Short: "Read a complete work aggregate",
		Args:  cobra.ExactArgs(1),
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
		Use:   "lock <id>",
		Short: "Lock a draft work contract",
		Args:  cobra.ExactArgs(1),
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
		Use:   "amend",
		Short: "Amend a locked work contract",
		Args:  cobra.NoArgs,
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
		Use:   "verify <id>",
		Short: "Verify work against its delivery contract",
		Args:  cobra.ExactArgs(1),
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
		Use:   "complete <id>",
		Short: "Close work using its latest persisted evidence",
		Args:  cobra.ExactArgs(1),
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
		Use:   "resume <id>",
		Short: "Resume escalated work after a human decision",
		Args:  cobra.ExactArgs(1),
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
}

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
