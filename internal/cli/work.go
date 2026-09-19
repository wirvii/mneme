package cli

import (
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
		newWorkActionCmd("review"),
		newWorkActionCmd("verify"),
		newWorkActionCmd("complete"),
	)
	return cmd
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

func newWorkActionCmd(operation string) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   operation + " <id>",
		Short: "Report availability of work " + operation,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := model.WorkActionRequest{ID: args[0]}
			result, err := callWork(cmd, func(svc workService) (any, error) {
				switch operation {
				case "review":
					return svc.WorkReview(cmd.Context(), req)
				case "verify":
					return svc.WorkVerify(cmd.Context(), req)
				default:
					return svc.WorkComplete(cmd.Context(), req)
				}
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

type workService interface {
	WorkBegin(context.Context, model.WorkBeginRequest) (model.WorkGetResponse, error)
	WorkGet(context.Context, model.WorkGetRequest) (model.WorkGetResponse, error)
	WorkLock(context.Context, model.WorkLockRequest) (model.WorkGetResponse, error)
	WorkAmend(context.Context, model.WorkAmendRequest) (model.WorkGetResponse, error)
	WorkReview(context.Context, model.WorkActionRequest) (model.WorkCapabilityResult, error)
	WorkVerify(context.Context, model.WorkActionRequest) (model.WorkCapabilityResult, error)
	WorkComplete(context.Context, model.WorkActionRequest) (model.WorkCapabilityResult, error)
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
		defer closeFile()
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
	_, err := fmt.Fprintf(w, "%s %s [%s] revision:%d\n", label, work.Contract.ID, work.Contract.Status, work.Contract.ContractRevision)
	return err
}

func writeWorkCapability(w io.Writer, result model.WorkCapabilityResult) error {
	_, err := fmt.Fprintf(w, "NO DISPONIBLE %s: %s (%s)\n", result.Operation, result.Reason, result.ReasonCode)
	return err
}
