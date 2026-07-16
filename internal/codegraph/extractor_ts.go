package codegraph

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed js/extract.js
var extractJS []byte

// ErrExtractorIncompatible indicates the extractor's toolchain is present but
// unusable, so NO file of that language can be extracted. Callers must abort
// rather than counting per-file failures. Distinct from an absent toolchain,
// which degrades gracefully (TS/JS extraction is optional). See SPEC-088 D4.
var ErrExtractorIncompatible = errors.New("codegraph: extractor toolchain incompatible")

// tsIncompatibleExitCode is the exit code js/extract.js uses (SPEC-088 D3)
// when the resolved typescript package lacks the Compiler API symbols the
// script depends on. Deliberately not 1-12: Node.js reserves that range for
// its own fatal failures (3 = Internal JavaScript Parse Error), and colliding
// with it would misreport a Node-internal crash as a toolchain incompatibility.
const tsIncompatibleExitCode = 20

// stderrBufferLimit bounds how much subprocess stderr TSExtractor retains for
// error messages (SPEC-088 D6). Large enough for the D2 guard's structured
// JSON message with room to spare; small enough to never matter for memory.
const stderrBufferLimit = 4 * 1024

// boundedBuffer is a mutex-protected, capacity-limited byte sink used as
// (part of) cmd.Stderr. os/exec copies into Stderr from a goroutine it owns
// whenever Stderr is not an *os.File — io.MultiWriter never is — so Write
// must be safe for concurrent use independent of TSExtractor's own mutex.
type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

// Write appends p up to the remaining capacity and always reports the full
// length written, per the io.Writer contract — truncation is silent by
// design, this buffer only ever backs a diagnostic message.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := stderrBufferLimit - len(b.buf); remaining > 0 {
		if len(p) > remaining {
			b.buf = append(b.buf, p[:remaining]...)
		} else {
			b.buf = append(b.buf, p...)
		}
	}
	return len(p), nil
}

// String returns the captured bytes collected so far.
func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// TSExtractor extracts code symbols from TypeScript/JavaScript files using a
// Node.js subprocess that parses source with the official TypeScript compiler.
// It implements the Extractor interface and manages the Node.js process lifecycle.
//
// The subprocess communicates via JSONL over stdin/stdout: each file is sent as
// a JSON line with path and content, and the result is read back as a JSON line.
type TSExtractor struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Scanner
	stderrBuf *boundedBuffer
	tmpDir    string
	ready     bool

	// waitOnce/waitErr guard against calling cmd.Wait() twice — once from the
	// death-detection path inside Extract, once from Close — which os/exec
	// turns into a panic ("exec: Wait was already called"). See SPEC-088 R2.
	waitOnce sync.Once
	waitErr  error

	// fatal is sticky: once a call classifies the subprocess's death as
	// ErrExtractorIncompatible, every subsequent call returns the same error
	// immediately instead of writing to a pipe whose reader is gone.
	fatal error
}

// NewTSExtractor creates a TS extractor. The underlying Node.js subprocess is
// started lazily on the first call to Extract. Call Close() when done to
// terminate the subprocess and remove temporary files.
func NewTSExtractor() *TSExtractor {
	return &TSExtractor{}
}

// Language returns "typescript", the language identifier for this extractor.
// Despite the name, the same extractor handles both TypeScript and JavaScript
// files — the Node.js script auto-detects based on file extension.
func (e *TSExtractor) Language() string { return "typescript" }

// ensureStarted lazily starts the Node.js subprocess on first use. It writes
// the embedded extract.js to a temporary directory and launches node with
// stdin/stdout pipes for the JSONL protocol.
func (e *TSExtractor) ensureStarted() error {
	if e.ready {
		return nil
	}

	// Write extract.js to temp dir
	tmpDir, err := os.MkdirTemp("", "mneme-codegraph-*")
	if err != nil {
		return fmt.Errorf("codegraph: ts extractor: create temp dir: %w", err)
	}
	e.tmpDir = tmpDir

	scriptPath := filepath.Join(tmpDir, "extract.js")
	if err := os.WriteFile(scriptPath, extractJS, 0644); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("codegraph: ts extractor: write script: %w", err)
	}

	// Start node process
	e.cmd = exec.Command("node", scriptPath)

	// Ensure globally installed npm packages are resolvable by Node.js.
	// Modern Node.js versions do not automatically search the global prefix
	// so we append it to NODE_PATH if not already present.
	//
	// SPEC-088 D5: an explicit, caller-set NODE_PATH is listed FIRST so it
	// wins over the global npm root — Node's module resolution searches
	// NODE_PATH entries in order and uses the first match. This is both the
	// correct semantics for an explicitly-set env var and the escape hatch
	// ErrExtractorIncompatible's error message points users at (D4): a global
	// typescript@7 no longer overrides a compatible install pinned via
	// NODE_PATH.
	e.cmd.Env = os.Environ()
	if globalRoot, gErr := exec.Command("npm", "root", "-g").Output(); gErr == nil {
		rootPath := strings.TrimSpace(string(globalRoot))
		if rootPath != "" {
			existing := os.Getenv("NODE_PATH")
			if existing != "" {
				e.cmd.Env = append(e.cmd.Env, "NODE_PATH="+existing+string(os.PathListSeparator)+rootPath)
			} else {
				e.cmd.Env = append(e.cmd.Env, "NODE_PATH="+rootPath)
			}
		}
	}

	e.stdin, err = e.cmd.StdinPipe()
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("codegraph: ts extractor: stdin pipe: %w", err)
	}
	stdout, err := e.cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("codegraph: ts extractor: stdout pipe: %w", err)
	}
	e.stdout = bufio.NewScanner(stdout)
	// 10MB buffer for large files
	e.stdout.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	// Capture stderr for humans watching the process AND for Go-side error
	// messages (SPEC-088 D6): a MultiWriter still passes bytes through to
	// os.Stderr, but also retains a bounded copy so that a systemic death
	// (e.g. the D2 API guard exiting 20) can report what the subprocess
	// actually said even in contexts where stderr passthrough is invisible
	// (the codegraph auto-reindex git hook, MCP tool invocations).
	e.stderrBuf = &boundedBuffer{}
	e.cmd.Stderr = io.MultiWriter(os.Stderr, e.stderrBuf)

	if err := e.cmd.Start(); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("codegraph: ts extractor: start node: %w (is Node.js installed?)", err)
	}

	e.ready = true
	return nil
}

// tsInput is the JSONL message sent to the Node.js subprocess for each file.
type tsInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// tsExtractionResult mirrors the JSON output from the Node.js extraction script.
// Fields use snake_case to match the script's JSON output format.
type tsExtractionResult struct {
	Nodes          []tsNode          `json:"nodes"`
	Edges          []tsEdge          `json:"edges"`
	UnresolvedRefs []tsUnresolvedRef `json:"unresolved_refs"`
	Errors         []tsError         `json:"errors"`
	DurationMs     int64             `json:"duration_ms"`
}

// tsNode is the JSON representation of a node from the JS extractor.
type tsNode struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	QualifiedName  string `json:"qualified_name"`
	FilePath       string `json:"file_path"`
	Language       string `json:"language"`
	StartLine      int    `json:"start_line"`
	EndLine        int    `json:"end_line"`
	StartColumn    int    `json:"start_column"`
	EndColumn      int    `json:"end_column"`
	Docstring      string `json:"docstring,omitempty"`
	Signature      string `json:"signature,omitempty"`
	IsExported     bool   `json:"is_exported,omitempty"`
	IsAsync        bool   `json:"is_async,omitempty"`
	IsStatic       bool   `json:"is_static,omitempty"`
	UpdatedAt      int64  `json:"updated_at"`
}

// tsEdge is the JSON representation of an edge from the JS extractor.
type tsEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Col    int    `json:"col,omitempty"`
}

// tsUnresolvedRef is the JSON representation of an unresolved reference.
type tsUnresolvedRef struct {
	FromNodeID    string `json:"from_node_id"`
	ReferenceName string `json:"reference_name"`
	ReferenceKind string `json:"reference_kind"`
	Line          int    `json:"line,omitempty"`
	Col           int    `json:"col,omitempty"`
	FilePath      string `json:"file_path"`
	Language      string `json:"language"`
}

// tsError is the JSON representation of an extraction error.
type tsError struct {
	Message  string `json:"message"`
	FilePath string `json:"file_path,omitempty"`
	Severity string `json:"severity"`
}

// Extract sends a file to the Node.js subprocess for parsing and returns the
// extracted nodes, edges, and unresolved references. The subprocess is started
// lazily on the first call.
//
// Extract never returns (result, nil) when the toolchain is incompatible
// (SPEC-088 AC3): once the subprocess is classified as having died from the
// D2 API guard (exit 20), that classification is sticky — this and every
// later call return ErrExtractorIncompatible immediately without touching the
// dead stdin/stdout pipes.
func (e *TSExtractor) Extract(filePath string, content []byte) (*ExtractionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.fatal != nil {
		return nil, e.fatal
	}

	if err := e.ensureStarted(); err != nil {
		return nil, err
	}

	// Send file to Node.js
	input := tsInput{Path: filePath, Content: string(content)}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("codegraph: ts extractor: marshal input: %w", err)
	}
	data = append(data, '\n')
	if _, err := e.stdin.Write(data); err != nil {
		if dErr := e.checkDeath(); dErr != nil {
			return nil, dErr
		}
		return nil, fmt.Errorf("codegraph: ts extractor: write stdin: %w", err)
	}

	// Read result
	if !e.stdout.Scan() {
		if dErr := e.checkDeath(); dErr != nil {
			return nil, dErr
		}
		if scanErr := e.stdout.Err(); scanErr != nil {
			return nil, fmt.Errorf("codegraph: ts extractor: read stdout: %w", scanErr)
		}
		return nil, fmt.Errorf("codegraph: ts extractor: no response from node (process may have exited)")
	}

	var raw tsExtractionResult
	if err := json.Unmarshal(e.stdout.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("codegraph: ts extractor: decode response: %w", err)
	}

	return convertTSResult(&raw), nil
}

// checkDeath waits for the subprocess and classifies its exit. When the exit
// code matches tsIncompatibleExitCode (the D2 guard in js/extract.js), it
// wraps ErrExtractorIncompatible with the captured stderr (D6) and latches it
// into e.fatal so every subsequent call short-circuits (R2) instead of
// writing to a pipe whose reader is gone. Any other death (crash, killed
// process, ordinary EOF) is left to the caller's existing error path — only
// the D2 guard's specific signal is systemic; a same-file JS exception or an
// unrelated process death must not be misclassified as toolchain
// incompatibility.
func (e *TSExtractor) checkDeath() error {
	waitErr := e.waitProcess()

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == tsIncompatibleExitCode {
		stderr := strings.TrimSpace(e.stderrBuf.String())
		e.fatal = fmt.Errorf("%w: %s", ErrExtractorIncompatible, stderr)
		return e.fatal
	}
	return nil
}

// waitProcess calls cmd.Wait() exactly once no matter how many call sites
// (Extract's death-detection path, Close) request it, caching the result for
// everyone else. A second real call to cmd.Wait() panics with "exec: Wait
// was already called" — see SPEC-088 R2.
func (e *TSExtractor) waitProcess() error {
	e.waitOnce.Do(func() {
		if e.cmd != nil {
			e.waitErr = e.cmd.Wait()
		}
	})
	return e.waitErr
}

// convertTSResult transforms the JSON-native types from the Node.js script into
// the canonical codegraph types used by the rest of the system.
func convertTSResult(raw *tsExtractionResult) *ExtractionResult {
	result := &ExtractionResult{
		DurationMs: raw.DurationMs,
	}

	for _, n := range raw.Nodes {
		result.Nodes = append(result.Nodes, Node{
			ID:            n.ID,
			Kind:          NodeKind(n.Kind),
			Name:          n.Name,
			QualifiedName: n.QualifiedName,
			FilePath:      n.FilePath,
			Language:      n.Language,
			StartLine:     n.StartLine,
			EndLine:       n.EndLine,
			StartColumn:   n.StartColumn,
			EndColumn:     n.EndColumn,
			Docstring:     n.Docstring,
			Signature:     n.Signature,
			IsExported:    n.IsExported,
			IsAsync:       n.IsAsync,
			IsStatic:      n.IsStatic,
			UpdatedAt:     n.UpdatedAt,
		})
	}

	for _, e := range raw.Edges {
		result.Edges = append(result.Edges, Edge{
			Source: e.Source,
			Target: e.Target,
			Kind:   EdgeKind(e.Kind),
			Line:   e.Line,
			Col:    e.Col,
		})
	}

	for _, ref := range raw.UnresolvedRefs {
		result.UnresolvedRefs = append(result.UnresolvedRefs, UnresolvedRef{
			FromNodeID:    ref.FromNodeID,
			ReferenceName: ref.ReferenceName,
			ReferenceKind: EdgeKind(ref.ReferenceKind),
			Line:          ref.Line,
			Col:           ref.Col,
			FilePath:      ref.FilePath,
			Language:      ref.Language,
		})
	}

	for _, e := range raw.Errors {
		result.Errors = append(result.Errors, ExtractionError{
			Message:  e.Message,
			FilePath: e.FilePath,
			Severity: e.Severity,
		})
	}

	return result
}

// tsconfigOp is the control message sent to the Node.js subprocess when
// requesting tsconfig parsing.
type tsconfigOp struct {
	Op   string `json:"op"`
	Root string `json:"root"`
}

// tsconfigEntry is one tsconfig descriptor returned by the subprocess.
type tsconfigEntry struct {
	// Dir is the absolute path of the directory containing the tsconfig.json.
	Dir string `json:"dir"`
	// BaseURL is the resolved absolute base URL, or empty string if absent.
	BaseURL string `json:"baseUrl"`
	// Paths is the compilerOptions.paths map. Keys are patterns (e.g. "@/*"),
	// values are arrays of replacement patterns (e.g. ["./src/*"]).
	Paths map[string][]string `json:"paths"`
}

// tsconfigOpResult is the full response from the subprocess for the tsconfig op.
type tsconfigOpResult struct {
	Tsconfigs []tsconfigEntry `json:"tsconfigs"`
}

// LoadTSConfigAliases sends the tsconfig op to the Node.js subprocess and
// returns all tsconfig descriptors found under rootDir. On any error (Node.js
// unavailable, no tsconfig, parse failure) it returns an empty slice so callers
// degrade gracefully.
func (e *TSExtractor) LoadTSConfigAliases(rootDir string) ([]tsconfigEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureStarted(); err != nil {
		return nil, fmt.Errorf("codegraph: ts extractor: load tsconfig: start: %w", err)
	}

	msg := tsconfigOp{Op: "tsconfig", Root: rootDir}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("codegraph: ts extractor: load tsconfig: marshal: %w", err)
	}
	data = append(data, '\n')
	if _, err := e.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("codegraph: ts extractor: load tsconfig: write: %w", err)
	}

	if !e.stdout.Scan() {
		if scanErr := e.stdout.Err(); scanErr != nil {
			return nil, fmt.Errorf("codegraph: ts extractor: load tsconfig: read stdout: %w", scanErr)
		}
		return nil, fmt.Errorf("codegraph: ts extractor: load tsconfig: no response")
	}

	var result tsconfigOpResult
	if err := json.Unmarshal(e.stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("codegraph: ts extractor: load tsconfig: decode: %w", err)
	}
	return result.Tsconfigs, nil
}

// Close terminates the Node.js subprocess and cleans up temporary files.
// It is safe to call Close multiple times. Waiting on the subprocess goes
// through waitProcess so that a prior death detected inside Extract (R2)
// never triggers a second, panicking call to cmd.Wait().
func (e *TSExtractor) Close() error {
	if !e.ready {
		return nil
	}
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.waitProcess()
	}
	if e.tmpDir != "" {
		os.RemoveAll(e.tmpDir)
	}
	e.ready = false
	return nil
}
