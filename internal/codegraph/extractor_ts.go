package codegraph

import (
	"bufio"
	_ "embed"
	"encoding/json"
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

// TSExtractor extracts code symbols from TypeScript/JavaScript files using a
// Node.js subprocess that parses source with the official TypeScript compiler.
// It implements the Extractor interface and manages the Node.js process lifecycle.
//
// The subprocess communicates via JSONL over stdin/stdout: each file is sent as
// a JSON line with path and content, and the result is read back as a JSON line.
type TSExtractor struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	tmpDir string
	ready  bool
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
	// so we prepend it to NODE_PATH if not already present.
	e.cmd.Env = os.Environ()
	if globalRoot, gErr := exec.Command("npm", "root", "-g").Output(); gErr == nil {
		rootPath := strings.TrimSpace(string(globalRoot))
		if rootPath != "" {
			existing := os.Getenv("NODE_PATH")
			if existing != "" {
				e.cmd.Env = append(e.cmd.Env, "NODE_PATH="+rootPath+string(os.PathListSeparator)+existing)
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

	// Capture stderr but do not block on it
	e.cmd.Stderr = os.Stderr

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
func (e *TSExtractor) Extract(filePath string, content []byte) (*ExtractionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

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
		return nil, fmt.Errorf("codegraph: ts extractor: write stdin: %w", err)
	}

	// Read result
	if !e.stdout.Scan() {
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
// It is safe to call Close multiple times.
func (e *TSExtractor) Close() error {
	if !e.ready {
		return nil
	}
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Wait()
	}
	if e.tmpDir != "" {
		os.RemoveAll(e.tmpDir)
	}
	e.ready = false
	return nil
}
