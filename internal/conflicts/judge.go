package conflicts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrCLIUnavailable is returned when the Claude CLI binary cannot be found on
// PATH. Callers should report the condition and skip judgment — never fall back
// to a metered API.
var ErrCLIUnavailable = errors.New("claude CLI not found on PATH")

// Verdict holds the judgment output for a memory pair.
type Verdict struct {
	// Relation is one of the four canonical values:
	//   "supersedes_a_over_b" — A supersedes B (A is current, B is obsolete)
	//   "supersedes_b_over_a" — B supersedes A (B is current, A is obsolete)
	//   "conflicts_with"      — A and B conflict without a clear winner
	//   "unrelated"           — A and B are on different topics (negative cache)
	Relation string

	// WinnerID is populated when Relation is supersedes_a_over_b or
	// supersedes_b_over_a. It holds the ID of the memory that supersedes the other.
	WinnerID string

	// LoserID is populated when Relation is supersedes_a_over_b or
	// supersedes_b_over_a. It holds the ID of the memory that is superseded.
	LoserID string

	// Rationale is a one-line explanation from the judge.
	Rationale string
}

// JudgeConfig holds runtime configuration for the judge. Construct it with
// NewJudgeConfig to ensure CLI availability is checked.
type JudgeConfig struct {
	// CLIPath is the absolute path to the claude binary.
	CLIPath string

	// Timeout is the per-judgment subprocess timeout.
	Timeout time.Duration
}

// NewJudgeConfig resolves the claude CLI path via exec.LookPath. Returns
// ErrCLIUnavailable when the binary is not on PATH. A timeout of 60 seconds
// is applied unless overridden by the caller after construction.
func NewJudgeConfig() (*JudgeConfig, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil, ErrCLIUnavailable
	}
	return &JudgeConfig{
		CLIPath: path,
		Timeout: 60 * time.Second,
	}, nil
}

// judgmentResponse is the internal JSON structure parsed from the claude CLI
// output. The CLI outputs {"type":"result","result":"<text>"} where <text>
// is itself the JSON judgment payload.
type judgmentResponse struct {
	Type   string `json:"type"`
	Result string `json:"result"`
}

// judgmentPayload is the JSON schema expected inside Result.
type judgmentPayload struct {
	Relation  string `json:"relation"`
	Rationale string `json:"rationale"`
}

// validRelations is the set of accepted relation values from the judge.
var validRelations = map[string]bool{
	"supersedes_a_over_b": true,
	"supersedes_b_over_a": true,
	"conflicts_with":      true,
	"unrelated":           true,
}

// JudgePair invokes the Claude CLI as a subprocess to judge whether two
// memories are related and how. It uses a tightly constrained prompt that
// asks for exactly one of four classifications.
//
// When Relation is "supersedes_a_over_b", WinnerID=aID and LoserID=bID.
// When Relation is "supersedes_b_over_a", WinnerID=bID and LoserID=aID.
//
// Errors are returned for:
//   - subprocess failure (timeout, non-zero exit code)
//   - malformed or missing JSON in the claude output
//   - unrecognised relation value
func JudgePair(
	ctx context.Context,
	cfg *JudgeConfig,
	aID, aTitle, aContent string,
	bID, bTitle, bContent string,
) (*Verdict, error) {
	prompt := buildJudgePrompt(aID, aTitle, aContent, bID, bTitle, bContent)

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	//nolint:gosec // CLIPath is resolved via exec.LookPath and controlled by NewJudgeConfig
	cmd := exec.CommandContext(ctx, cfg.CLIPath, "-p", prompt, "--output-format", "json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("conflicts: judge pair: timeout after %s", cfg.Timeout)
		}
		return nil, fmt.Errorf("conflicts: judge pair: exec: %w (output: %s)", err, truncate(string(out), 200))
	}

	return parseJudgeOutput(string(out), aID, bID)
}

// parseJudgeOutput parses the JSON output from the Claude CLI and builds a Verdict.
func parseJudgeOutput(output, aID, bID string) (*Verdict, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("conflicts: judge pair: empty output from claude CLI")
	}

	// The claude CLI --output-format json returns {"type":"result","result":"<text>"}.
	var outer judgmentResponse
	if err := json.Unmarshal([]byte(output), &outer); err != nil {
		return nil, fmt.Errorf("conflicts: judge pair: parse outer JSON: %w (output: %s)", err, truncate(output, 200))
	}

	if outer.Result == "" {
		return nil, fmt.Errorf("conflicts: judge pair: empty result field in claude output")
	}

	// The inner result is the JSON judgment payload.
	// Strip markdown code fences if the model wrapped the JSON.
	inner := strings.TrimSpace(outer.Result)
	inner = strings.TrimPrefix(inner, "```json")
	inner = strings.TrimPrefix(inner, "```")
	inner = strings.TrimSuffix(inner, "```")
	inner = strings.TrimSpace(inner)

	var payload judgmentPayload
	if err := json.Unmarshal([]byte(inner), &payload); err != nil {
		return nil, fmt.Errorf("conflicts: judge pair: parse judgment payload: %w (result: %s)", err, truncate(inner, 200))
	}

	if !validRelations[payload.Relation] {
		return nil, fmt.Errorf("conflicts: judge pair: unrecognised relation %q", payload.Relation)
	}

	verdict := &Verdict{
		Relation:  payload.Relation,
		Rationale: payload.Rationale,
	}

	switch payload.Relation {
	case "supersedes_a_over_b":
		verdict.WinnerID = aID
		verdict.LoserID = bID
	case "supersedes_b_over_a":
		verdict.WinnerID = bID
		verdict.LoserID = aID
	}

	return verdict, nil
}

// buildJudgePrompt constructs the tightly-scoped prompt sent to the Claude CLI.
// The prompt instructs the model to output exactly one JSON object with two
// fields: relation and rationale.
func buildJudgePrompt(aID, aTitle, aContent, bID, bTitle, bContent string) string {
	return fmt.Sprintf(`You are a memory conflict judge. Given two knowledge base entries, classify their relationship.

Memory A (id: %s):
Title: %s
Content: %s

Memory B (id: %s):
Title: %s
Content: %s

Classify the relationship between Memory A and Memory B. Respond with ONLY a JSON object — no prose, no markdown, no explanation outside the JSON:

{"relation": "<one of: supersedes_a_over_b, supersedes_b_over_a, conflicts_with, unrelated>", "rationale": "<one sentence explaining why>"}

Definitions:
- supersedes_a_over_b: A contains more current/accurate information and B should be considered obsolete.
- supersedes_b_over_a: B contains more current/accurate information and A should be considered obsolete.
- conflicts_with: A and B make contradictory claims about the same topic, with no clear winner.
- unrelated: A and B are on different topics and have no meaningful conflict.`,
		aID, aTitle, truncate(aContent, 500),
		bID, bTitle, truncate(bContent, 500),
	)
}

// truncate returns s truncated to maxLen characters with "..." appended if
// truncation occurred.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
