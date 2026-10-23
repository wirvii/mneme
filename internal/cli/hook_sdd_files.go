package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wirvii/mneme/internal/enforcement"
)

// sddFilesBlockMessage is the SPEC-179 rejection, the same for every caller:
// the line addressed to a subagent is harmless to the orchestrator. The two
// %s are the rejected path and the way it was written. It names how to proceed
// (the mneme verbs that legitimately change SDD state, `mneme sdd export` to
// regenerate stale files) and who resolves a git conflict in those files.
const sddFilesBlockMessage = "⛔ mneme: nadie escribe a mano en .mneme/sdd/ (ruta: %s, con: %s).\n" +
	"Esos archivos los escribe solo mneme: el siguiente git pull o checkout de cualquier persona del equipo los importa tal cual, sin pasar por la aprobación ni por las reglas de quién mueve una spec.\n" +
	"Cómo proceder: haz el cambio con mneme (backlog_refine, spec_doc_write, spec_pushback; avanzar una spec es del orquestador) y, si los archivos quedaron desfasados, regenéralos con `mneme sdd export`. Si eres un subagente, reporta y devuélvelo al orquestador. Si hay un conflicto de git en esos archivos, lo resuelve una persona en su editor o con `mneme sdd import`.\n"

// sddFilesDeniedReason is the enforcelog reason of a SPEC-179 rejection.
const sddFilesDeniedReason = "sdd_files_write_denied"

// Caller labels recorded in the enforcelog.
const (
	callerOrchestrator = "orchestrator"
	callerSubagent     = "subagent"
)

// Tool names the .mneme/sdd guard understands: Claude Code's file tools, Bash
// (both runtimes) and Codex's apply_patch.
const (
	toolBash       = "Bash"
	toolApplyPatch = "apply_patch"
)

// sddFileTools are the file tools whose path is the one to judge.
var sddFileTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// checkSDDFilesGuard is the SPEC-179 guard of mneme hook enforce-delegation:
// it reports true, after printing the rejection and logging the event, when the
// call writes inside .mneme/sdd. The files there are applied as written by the
// next git pull or checkout of any teammate, with no approval gate, so only the
// mneme binary (which never passes through these hooks) may write them.
//
// It takes no decision from identity: it applies to the orchestrator, to a
// subagent with a role and to one without. It reads no config, manifest or
// database, so no setting can switch it off. The event goes to the enforcelog
// only, never to a discovery memory: a repeated rejection must not fill the
// database.
func checkSDDFilesGuard(input hookPreToolInput, identity CallerIdentity, errW io.Writer) bool {
	if !sddGuardedTool(input.ToolName) {
		return false
	}
	d := evaluateSDDFilesCall(input, newSDDContext(input))
	if !d.Block {
		return false
	}
	fmt.Fprintf(errW, sddFilesBlockMessage, d.Target, d.Form)

	caller := callerOrchestrator
	if identity.IsSubagent {
		caller = callerSubagent
	}
	appendGuardDenial(input, identity, caller, d.Target, sddFilesDeniedReason)
	return true
}

// sddGuardedTool reports whether tool can write files, so the context (which
// touches the file system) is built only for those.
func sddGuardedTool(tool string) bool {
	return sddFileTools[tool] || tool == toolBash || tool == toolApplyPatch
}

// evaluateSDDFilesCall dispatches the call to the matching pure evaluator.
// apply_patch reads the patch from tool_input.command and, failing that, from
// tool_input.input: the real payload of Codex has not been captured.
func evaluateSDDFilesCall(input hookPreToolInput, c enforcement.SDDContext) enforcement.SDDWriteDecision {
	switch {
	case sddFileTools[input.ToolName]:
		return enforcement.EvaluateSDDFileTool(input.ToolName, input.filePath(), c)
	case input.ToolName == toolBash:
		return enforcement.EvaluateSDDBash(input.ToolInput.Command, c)
	case input.ToolName == toolApplyPatch:
		if d := enforcement.EvaluateSDDApplyPatch(input.ToolInput.Command, c); d.Block {
			return d
		}
		return enforcement.EvaluateSDDApplyPatch(input.ToolInput.Input, c)
	}
	return enforcement.SDDWriteDecision{}
}

// newSDDContext builds the per-call context from the real host. A missing home
// only disables ~ expansion: the guard still judges the text of the path. The
// CWD is canonicalised so that a symlinked temporary directory (macOS /var ->
// /private/var) yields the same form as Canonical gives for the operands.
func newSDDContext(input hookPreToolInput) enforcement.SDDContext {
	pc, _ := hostPathContext()
	cwd, _ := os.Getwd() //nolint:errcheck // an unknown cwd only leaves relative paths unanchored
	cwd = resolveHookCWD(input, cwd)
	return enforcement.SDDContext{
		PathContext: pc,
		CWD:         canonicalPath(cwd),
		Canonical:   canonicalPath,
		HasSDDDir:   hasSDDDir,
	}
}

// canonicalPath resolves the symlinks of p, a slash-separated absolute path
// that may not exist yet (a file about to be created): it resolves the deepest
// ancestor that exists and appends the rest. Any error returns p unchanged, so
// the guard falls back to judging the text and never switches itself off.
func canonicalPath(p string) string {
	native := filepath.FromSlash(p)
	if p == "" || !filepath.IsAbs(native) {
		return p
	}
	existing, rest := native, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return p
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return p
	}
	return filepath.ToSlash(filepath.Join(resolved, rest))
}

// hasSDDDir reports whether abs/.mneme/sdd is a directory: what makes
// `rm -rf <repo>` a deletion of the SDD tree.
func hasSDDDir(abs string) bool {
	fi, err := os.Stat(filepath.Join(filepath.FromSlash(abs), ".mneme", "sdd"))
	return err == nil && fi.IsDir()
}
