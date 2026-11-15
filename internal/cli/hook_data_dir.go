package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/enforcement"
)

// dataDirBlockMessage is the SPEC-185 layer A rejection, addressed to a
// subagent. The two %s are the rejected path and the way it was written. It
// names the MCP tools a subagent can call instead, because calling them is
// exactly how the change it wanted gets made; it names no terminal command
// (D6).
const dataDirBlockMessage = "⛔ mneme: un subagente no escribe a mano en los datos de mneme: ruta %s, con %s.\n" +
	"Ahí viven las specs, la memoria, las bases de datos, la configuración y el registro del hook; un subagente los cambia solo con las herramientas MCP (mem_save, spec_doc_write, spec_pushback...), que validan y registran quién escribe. " +
	"Si necesitas otro cambio ahí, repórtalo al orquestador.\n"

// dataDirDeniedReason is the enforcelog reason of a layer A rejection.
const dataDirDeniedReason = "data_dir_write_denied"

// Environment variables that move the data and workflow directories; read
// directly when config.toml does not load, so the guard never loses a root.
const (
	envMnemeDataDir     = "MNEME_DATA_DIR"
	envMnemeWorkflowDir = "MNEME_WORKFLOW_DIR"
)

// checkDataDirGuard is SPEC-185's layer A: it reports true, after printing the
// rejection and logging the event, when a subagent (with a resolved role or
// not) writes by hand under mneme's own data: the data directory, the workflow
// directory or the folder of config.toml. Specs, memory, the declarations the
// risk guard trusts and the hook's own log live there, and a subagent changes
// them only through mneme's MCP tools. The orchestrator is not restricted: it
// edits config.toml as the person's delegate and makes the sqlite3 .backup
// copies tests need.
//
// Like the .mneme/sdd guard it runs before the whitelist (which allows
// /.mneme/) and the unresolved-role short-circuit, and reads no manifest. A
// config.toml that does not load never switches it off: the roots then come
// from the defaults with MNEME_DATA_DIR and MNEME_WORKFLOW_DIR applied.
func checkDataDirGuard(input hookPreToolInput, identity CallerIdentity, errW io.Writer) bool {
	if !identity.IsSubagent || !sddGuardedTool(input.ToolName) {
		return false
	}
	cfg := loadGuardConfig()
	c := enforcement.NewDataDirContext(newSDDContext(input), dataDirRoots(cfg))
	d := evaluateDataDirCall(input, c)
	if !d.Block {
		return false
	}
	fmt.Fprintf(errW, dataDirBlockMessage, d.Target, d.Form)
	appendGuardDenial(input, identity, cfg, callerSubagent, d.Target, dataDirDeniedReason)
	return true
}

// loadGuardConfig loads config.toml once for the guard and its event; on any
// error it falls back to the defaults with the two directory variables applied,
// the ones Load itself would have applied.
func loadGuardConfig() *config.Config {
	if cfg, err := config.Load(config.DefaultPath()); err == nil {
		return cfg
	}
	cfg := config.Default()
	if v := os.Getenv(envMnemeDataDir); v != "" {
		cfg.Storage.DataDir = v
	}
	if v := os.Getenv(envMnemeWorkflowDir); v != "" {
		cfg.Workflow.Dir = v
	}
	return cfg
}

// dataDirRoots lists the three roots of mneme's data: the data directory, the
// workflow directory and the folder of config.toml (always ~/.mneme, even when
// the data directory moved). Only empty values are dropped here: a raw "~/x"
// is expanded and relative leftovers dropped by enforcement.NewDataDirContext.
// Absolute roots are resolved through their symlinks.
func dataDirRoots(cfg *config.Config) []string {
	raw := []string{cfg.Storage.DataDir, cfg.WorkflowDir(), filepath.Dir(config.DefaultPath())}
	var roots []string
	for _, r := range raw {
		if r == "" {
			continue
		}
		slash := filepath.ToSlash(r)
		if filepath.IsAbs(r) {
			slash = canonicalPath(slash)
		}
		roots = append(roots, slash)
	}
	return roots
}

// evaluateDataDirCall dispatches the call to the matching pure evaluator, the
// same way evaluateSDDFilesCall does for .mneme/sdd.
func evaluateDataDirCall(input hookPreToolInput, c enforcement.DataDirContext) enforcement.SDDWriteDecision {
	switch {
	case sddFileTools[input.ToolName]:
		return enforcement.EvaluateDataDirFileTool(input.ToolName, input.filePath(), c)
	case input.ToolName == toolBash:
		return enforcement.EvaluateDataDirBash(input.ToolInput.Command, c)
	case input.ToolName == toolApplyPatch:
		if d := enforcement.EvaluateDataDirApplyPatch(input.ToolInput.Command, c); d.Block {
			return d
		}
		return enforcement.EvaluateDataDirApplyPatch(input.ToolInput.Input, c)
	}
	return enforcement.SDDWriteDecision{}
}
