package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrNoValidation is returned by Validate when the skill directory does not
// contain a validation/run.sh file. This is an informational sentinel — the
// absence of a validator is not treated as a validation failure by the caller.
// Distinct from model.ErrSkillNoValidation (which lives in the model layer);
// this sentinel stays in the leaf package to avoid importing internal/model.
var ErrNoValidation = errors.New("skill has no validation/run.sh")

// ErrNoShell is returned by Validate when no "sh" executable can be found on
// PATH. validation/run.sh scripts are POSIX shell scripts; "sh" is always
// present on Unix, but on Windows it is only available when Git-for-Windows
// (or an equivalent) has put its "sh" on PATH. Like ErrNoValidation, this is
// an informational sentinel — a missing shell is not treated as a validation
// failure by the caller.
var ErrNoShell = errors.New("no \"sh\" executable found on PATH")

// ValidateResult describes the outcome of running a skill's validation script.
type ValidateResult struct {
	// Passed is true when the script exited with code 0.
	Passed bool

	// Output is the combined stdout+stderr of the script run.
	Output string

	// ExitCode is the script's exit code. 0 means success.
	ExitCode int
}

// Validate runs the validation/run.sh script inside skillDir with skillDir as
// the working directory. A context-derived timeout of 120 seconds is applied.
//
// Returns ErrNoValidation (from this package) when validation/run.sh does not
// exist. Returns ErrNoShell when no "sh" executable is found on PATH (expected
// on Windows without Git-for-Windows). Returns a non-nil error for other
// OS/exec failures. ValidateResult is non-nil when the script ran (even if it
// exited non-zero).
func Validate(ctx context.Context, skillDir string) (*ValidateResult, error) {
	scriptPath := filepath.Join(skillDir, "validation", "run.sh")

	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil, ErrNoValidation
	}

	shPath, err := exec.LookPath("sh")
	if err != nil {
		return nil, ErrNoShell
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	//nolint:gosec // shPath resolved via LookPath; script path is derived from a controlled skills directory
	cmd := exec.CommandContext(ctx, shPath, "validation/run.sh")
	cmd.Dir = skillDir

	out, err := cmd.CombinedOutput()
	output := string(out)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ValidateResult{
				Passed:   false,
				Output:   output,
				ExitCode: exitErr.ExitCode(),
			}, nil
		}
		return nil, fmt.Errorf("skill: validate: exec: %w", err)
	}

	return &ValidateResult{
		Passed:   true,
		Output:   output,
		ExitCode: 0,
	}, nil
}
