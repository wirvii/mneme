// Package service implements the business logic layer for mneme.
// This file provides InitService which orchestrates the mneme init command:
// detecting legacy workflow artifacts, classifying them via a weighted heuristic,
// migrating viable content to the SDD engine, cleaning up filesystem debris, and
// rewriting CLAUDE.local.md so the orchestrator speaks SDD from day one.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/model"
)

// ArtifactKind classifies the type of a legacy workflow artifact.
type ArtifactKind string

const (
	// KindBacklogItem is a single line from a legacy plans/backlog.md.
	KindBacklogItem ArtifactKind = "backlog_item"
	// KindIssue is a standalone issues/*.md file.
	KindIssue ArtifactKind = "issue"
	// KindSpecDir is a legacy specs/<ID>/ directory.
	KindSpecDir ArtifactKind = "spec_dir"
	// KindBugDir is a legacy bugs/<ID>/ directory.
	KindBugDir ArtifactKind = "bug_dir"
	// KindQAReport is a qa-report.md file annexed to a spec directory.
	KindQAReport ArtifactKind = "qa_report"
	// KindAgentDup is a .claude/agents/<name>.md that is byte-identical to the global.
	KindAgentDup ArtifactKind = "agent_dup"
	// KindCommandDup is a .claude/commands/<name>.md that is byte-identical to the global.
	KindCommandDup ArtifactKind = "command_dup"
)

// Classification is the result of the weighted heuristic applied to a LegacyArtifact.
type Classification string

const (
	// ClassActive means the artifact is still vigente and should be migrated to backlog/spec.
	ClassActive Classification = "active"
	// ClassHistorical means the artifact is closed/completed and should go to memory.
	ClassHistorical Classification = "historical"
	// ClassAmbiguous means the heuristic had no clear winner; treated as historical
	// but flagged in the report for manual review.
	ClassAmbiguous Classification = "ambiguous"
)

// Signal is a single heuristic observation that contributes to classification.
// Positive weights push toward ClassActive; negative toward ClassHistorical.
type Signal struct {
	// Name is a human-readable label, e.g. "status:done", "checkboxes:mostly_checked".
	Name string
	// Weight is the signed contribution: +N for active, -N for historical.
	Weight int
}

// LegacyArtifact represents any file or directory from an old workflow.
type LegacyArtifact struct {
	Kind        ArtifactKind      // what kind of artifact this is
	SourcePath  string            // absolute path in the repo or home
	RawTitle    string            // first H1 heading or basename without extension
	RawBody     string            // markdown body, frontmatter stripped
	Frontmatter map[string]string // YAML key→value pairs parsed best-effort
	LegacyID    string            // e.g. "P009", "BUG-001" from path or frontmatter
	Signals     []Signal          // observations collected by the heuristic
}

// LegacyInventory is the output of DetectLegacy.
type LegacyInventory struct {
	RepoRoot        string
	WorkflowLegacy  string   // ~/.workflows/<slug> if it exists
	WorkflowNew     string   // ~/.mneme/workflows/<slug>
	Artifacts       []LegacyArtifact
	FilesToDelete   []string // abs paths that CleanFilesystem will remove
	FilesToPreserve []string // abs paths that will be kept
	CLAUDELocal     string   // path to CLAUDE.local.md if it exists
}

// CleanupPlan holds the concrete list of paths that CleanFilesystem will remove.
type CleanupPlan struct {
	// Paths contains absolute paths to remove, already classified as safe to delete.
	Paths []string
}

// CleanupResult records what actually happened when CleanFilesystem ran.
type CleanupResult struct {
	Deleted []string // paths removed successfully
	Kept    []string // paths that existed but were decided to preserve
	Errors  []string // non-fatal per-path errors
}

// ClaudeLocalResult records what happened to CLAUDE.local.md.
type ClaudeLocalResult struct {
	ExistedBefore bool
	BackupPath    string // empty when there was no previous file
	WrittenPath   string
}

// PlannedArtifact describes what will happen to a single artifact during Apply.
type PlannedArtifact struct {
	Source         string
	Kind           ArtifactKind
	Classification Classification
	// Destination is a human-readable label: "backlog:BL-new", "spec:SPEC-new", "memory:topic_key=...".
	Destination string
	// Reason is the human-readable justification produced by classificationReason.
	Reason string
	// ArtifactKey uniquely identifies this artifact within the plan. For backlog items
	// it is "<SourcePath>#<RawTitle>" because multiple items share the same SourcePath.
	// For all other kinds it equals SourcePath.
	ArtifactKey string
}

// InitPlan is the dry-run view of what Apply would do.
type InitPlan struct {
	Artifacts []PlannedArtifact
	Deletions []string
	Rewrites  []string // e.g. "CLAUDE.local.md (backup at CLAUDE.local.md.bak)"
}

// MigratedEntry records a single artifact migration with its source path and new ID.
// Storing source alongside ID ensures the report table remains aligned regardless of
// how other artifacts are ordered in the plan.
type MigratedEntry struct {
	Source string // original SourcePath
	ID     string // new BL-XXX / SPEC-XXX / topic_key
}

// InitReport aggregates the results of a Plan or Apply run for rendering.
type InitReport struct {
	Slug              string
	Plan              InitPlan
	Applied           bool
	MigratedBacklog   []MigratedEntry  // new BL-XXX IDs with their sources
	MigratedSpecs     []MigratedEntry  // new SPEC-XXX IDs with their sources
	MigratedMemories  []MigratedEntry  // memory IDs or topic_keys with their sources
	Ambiguous         []LegacyArtifact // artifacts flagged for manual review
	Cleanup           CleanupResult
	CLAUDELocal       ClaudeLocalResult
	GeneratedAt       time.Time
	criticalErrors    []string         // internal; not exported to callers
}

// HasCriticalErrors reports whether any migration step failed in a way that
// should block filesystem cleanup. A critical error is one where the artifact
// source data could be lost if we deleted the legacy dirs now.
func (r *InitReport) HasCriticalErrors() bool {
	return len(r.criticalErrors) > 0
}

func (r *InitReport) addError(source string, err error) {
	r.criticalErrors = append(r.criticalErrors, fmt.Sprintf("%s: %v", source, err))
}

// InitService orchestrates the mneme init pipeline. It detects legacy workflow
// artifacts, classifies them via a weighted heuristic, migrates viable content
// to the SDD engine, cleans up filesystem debris, and rewrites CLAUDE.local.md.
//
// All filesystem operations are injected so the service is fully testable without
// touching real paths. Use NewInitService for production; inject mocks in tests.
type InitService struct {
	cfg       *config.Config
	sdd       *SDDService
	memory    *MemoryService
	slug      string
	now       func() time.Time
	readFile  func(string) ([]byte, error)
	statDir   func(string) (bool, error) // returns (exists, error)
	writeFile func(string, []byte, os.FileMode) error
	removeAll func(string) error
	mkdirAll  func(string, os.FileMode) error
	// inventory is populated by DetectLegacy and reused by Apply without re-reading disk.
	inventory *LegacyInventory
}

// NewInitService constructs an InitService with production filesystem operations.
// cfg provides workflow directory paths, sdd and mem provide the SDD/memory backends,
// and slug is the project identifier used for topic_key generation.
func NewInitService(cfg *config.Config, sdd *SDDService, mem *MemoryService, slug string) *InitService {
	return &InitService{
		cfg:    cfg,
		sdd:    sdd,
		memory: mem,
		slug:   slug,
		now:    time.Now,
		readFile: func(p string) ([]byte, error) {
			return os.ReadFile(p)
		},
		statDir: func(p string) (bool, error) {
			_, err := os.Stat(p)
			if os.IsNotExist(err) {
				return false, nil
			}
			return err == nil, err
		},
		writeFile: os.WriteFile,
		removeAll: os.RemoveAll,
		mkdirAll:  os.MkdirAll,
	}
}

// claudeLocalTemplate is the canonical CLAUDE.local.md written by RewriteClaudeLocal.
// {{SLUG}} is replaced with the actual project slug.
const claudeLocalTemplate = `# CLAUDE.local.md — Configuración local del orquestador (SDD engine)

> Este archivo se generó automáticamente con ` + "`mneme init`" + `. Si necesitas
> personalizarlo, edítalo — ` + "`mneme init`" + ` no sobrescribe sin avisar.

## Workflow

WORKFLOW_DIR: ~/.mneme/workflows/{{SLUG}}/

El estado del proyecto (backlog, specs, historial, pushbacks) vive en la base de datos de mneme, no en el filesystem. Los archivos de spec en ` + "`$WORKFLOW_DIR/specs/<ID>/spec.md`" + ` son solo documentos de apoyo.

## Rol del Orquestador

Eres un facilitador de conversación y redactor de documentos.
NO eres evaluador técnico ni analista de código.
Eres el puente entre el usuario y los agentes especializados.

### Lo que HACES
1. Conversar con el usuario — discutir ideas, proponer enfoques.
2. Crear/gestionar ítems de backlog vía ` + "`mneme backlog add|refine|promote|archive`" + `.
3. Crear/avanzar specs vía ` + "`mneme spec new|advance|pushback|resolve`" + `.
4. Consultar el dashboard al arrancar sesión: ` + "`mneme status`" + `.
5. Lanzar agentes — delegar implementación, review, diagnóstico.
6. Redactar documentos en $WORKFLOW_DIR (specs, qa-reports) cuando el agente ya los abrió (p.ej. architect ya te genera el esqueleto de spec.md).

### Lo que NUNCA haces
- Editar archivos de código fuente. Existe un hook global que lo bloquea.
- "Arreglar rápido" algo — TODO se delega.
- Crear markdown manual en ` + "`.workflow/`" + ` o ` + "`.claude/specs/`" + ` — esas rutas están deprecadas.
- Diseñar arquitectura (lo hace @architect).
- Clasificar bugs (lo hace @bug-hunter).

## Comandos CLI de mneme (12)

| Comando | Uso |
|---|---|
| ` + "`mneme status`" + ` | Dashboard al arrancar sesión |
| ` + "`mneme backlog add <title>`" + ` | Nueva idea (status raw) |
| ` + "`mneme backlog list [--status]`" + ` | Listar backlog |
| ` + "`mneme backlog refine <id> --refinement \"...\"`" + ` | Detallar item raw |
| ` + "`mneme backlog promote <id>`" + ` | Convertir a spec |
| ` + "`mneme backlog archive <id> --reason \"...\"`" + ` | Descartar |
| ` + "`mneme spec new --title \"...\"`" + ` | Crear spec (estado draft) |
| ` + "`mneme spec list [--status]`" + ` | Listar specs |
| ` + "`mneme spec status <id>`" + ` | Detalle completo de una spec |
| ` + "`mneme spec advance <id> --by <agent>`" + ` | Avanzar al siguiente estado |
| ` + "`mneme spec pushback <id> --from <agent> --question \"...\"`" + ` | Pushback |
| ` + "`mneme spec resolve <id> --resolution \"...\"`" + ` | Resolver pushback |

## MCP tools de mneme (10 SDD + 11 memory)

SDD: ` + "`backlog_add`" + `, ` + "`backlog_list`" + `, ` + "`backlog_refine`" + `, ` + "`backlog_promote`" + `, ` + "`spec_new`" + `, ` + "`spec_list`" + `, ` + "`spec_status`" + `, ` + "`spec_advance`" + `, ` + "`spec_pushback`" + `, ` + "`spec_resolve`" + `.

Memoria: ` + "`mem_save`" + `, ` + "`mem_search`" + `, ` + "`mem_get`" + `, ` + "`mem_update`" + `, ` + "`mem_forget`" + `, ` + "`mem_context`" + `, ` + "`mem_relate`" + `, ` + "`mem_stats`" + `, ` + "`mem_suggest_topic_key`" + `, ` + "`mem_timeline`" + `, ` + "`mem_checkpoint`" + `, ` + "`mem_session_end`" + `.

## Cómo decidir qué flujo usar

**BUG**: "bug", "error", "falla" — algo que antes funcionaba.
→ ` + "`mneme backlog add \"...\"`" + ` → refinar con reporte → ` + "`mneme backlog promote`" + ` → lanzar @bug-hunter.

**FEATURE**: "agregar", "implementar" — algo nuevo.
→ ` + "`mneme backlog add \"...\"`" + ` → grill con usuario → ` + "`mneme backlog refine`" + ` → ` + "`mneme backlog promote`" + ` → ` + "`mneme spec advance`" + ` (draft→speccing) → lanzar @architect.

## Flujo SDD canónico

1. ` + "`mneme backlog add`" + ` — idea raw.
2. Grill con usuario — ` + "`mneme backlog refine`" + ` al terminar.
3. ` + "`mneme backlog promote`" + ` — crea SPEC en draft.
4. ` + "`mneme spec advance`" + ` (draft→speccing) — arquitecto escribe ` + "`spec.md`" + ` en ` + "`$WORKFLOW_DIR/specs/<ID>/`" + `.
5. Gate usuario → ` + "`mneme spec advance`" + ` (speccing→specced).
6. ` + "`mneme spec advance`" + ` (specced→planning→planned) — arquitecto arma pasos.
7. ` + "`mneme spec advance`" + ` (planned→implementing) — lanza @backend / @frontend.
8. ` + "`mneme spec advance`" + ` (implementing→qa) — lanza @qa-tester.
9. ` + "`mneme spec advance`" + ` (qa→done) — guarda memoria de completitud.

## Pushback

Cualquier agente puede llamar ` + "`spec_pushback`" + ` si encuentra ambigüedad:
- La spec pasa a ` + "`needs_grill`" + `.
- El orquestador hace grill con el usuario.
- ` + "`mneme spec resolve <id> --resolution \"...\"`" + ` → vuelve a ` + "`speccing`" + `.

## Manejo de contexto al lanzar subagentes

**SI incluir:** fragmento de spec relevante, rutas de archivos, lineamientos específicos.
**NO incluir:** historial de conversación, specs de otros features, contexto de otros subagentes.

## Si se agota el contexto

Para y di: "Recomiendo iniciar nueva sesión. Estado actual: SPEC-{{ID}} en {{status}}. Ver ` + "`mneme spec status SPEC-{{ID}}`" + `."
`

// legacyInitCommandSHA256 is the SHA-256 hex digest of the init.md slash command
// asset that was removed in this release. DetectLegacy uses this to identify copies
// of the old init.md in project .claude/commands/ directories, marking them safe
// to delete even though the source asset no longer ships in the binary.
//
// Computed from the last published version of assets/commands/init.md.
// If the value is empty the check is skipped (safe default).
const legacyInitCommandSHA256 = ""

// --- PHASE 1: DetectLegacy ---

// DetectLegacy walks the known legacy filesystem paths under repoRoot and the
// legacy workflow directory, building a complete inventory of artifacts that
// mneme init should handle. It never writes anything — pure read-only discovery.
func (s *InitService) DetectLegacy(ctx context.Context, repoRoot string) (LegacyInventory, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return LegacyInventory{}, fmt.Errorf("service: init: detect legacy: home dir: %w", err)
	}

	inv := LegacyInventory{
		RepoRoot:   repoRoot,
		WorkflowNew: filepath.Join(home, ".mneme", "workflows", s.slug),
	}

	// Detect legacy ~/.workflows/<slug>/ if it exists.
	legacyWorkflow := filepath.Join(home, ".workflows", s.slug)
	if exists, _ := s.statDir(legacyWorkflow); exists {
		inv.WorkflowLegacy = legacyWorkflow
	}

	// CLAUDE.local.md in repo root.
	claudeLocal := filepath.Join(repoRoot, "CLAUDE.local.md")
	if _, err := os.Stat(claudeLocal); err == nil {
		inv.CLAUDELocal = claudeLocal
	}

	// Directories to scan for legacy artifacts (under repoRoot).
	repoDirs := []struct {
		base    string
		subDirs []string
	}{
		{filepath.Join(repoRoot, ".workflow"), []string{"specs", "bugs", "issues", "plans", "docs", "decisions", "migrations", "templates"}},
		{filepath.Join(repoRoot, ".claude"), []string{"specs", "bugs", "issues", "plans", "docs", "migrations"}},
	}

	if inv.WorkflowLegacy != "" {
		repoDirs = append(repoDirs, struct {
			base    string
			subDirs []string
		}{inv.WorkflowLegacy, []string{"specs", "bugs", "issues", "plans", "docs", "templates"}})
	}

	for _, rd := range repoDirs {
		for _, sub := range rd.subDirs {
			dir := filepath.Join(rd.base, sub)
			if exists, _ := s.statDir(dir); !exists {
				continue
			}
			artifacts, deletions, err := s.scanSubdir(ctx, dir, sub, rd.base)
			if err != nil {
				return LegacyInventory{}, fmt.Errorf("service: init: detect legacy: scan %s: %w", dir, err)
			}
			inv.Artifacts = append(inv.Artifacts, artifacts...)
			inv.FilesToDelete = append(inv.FilesToDelete, deletions...)
		}
	}

	// Scan .claude/agents/ and .claude/commands/ for duplicates.
	agentDups, agentPreserve, agentDels := s.scanDuplicates(
		filepath.Join(repoRoot, ".claude", "agents"),
		"agents/",
	)
	inv.Artifacts = append(inv.Artifacts, agentDups...)
	inv.FilesToPreserve = append(inv.FilesToPreserve, agentPreserve...)
	inv.FilesToDelete = append(inv.FilesToDelete, agentDels...)

	cmdDups, cmdPreserve, cmdDels := s.scanDuplicates(
		filepath.Join(repoRoot, ".claude", "commands"),
		"commands/",
	)
	inv.Artifacts = append(inv.Artifacts, cmdDups...)
	inv.FilesToPreserve = append(inv.FilesToPreserve, cmdPreserve...)
	inv.FilesToDelete = append(inv.FilesToDelete, cmdDels...)

	// Add the top-level dirs to FilesToDelete (cleanup phase handles recursion).
	topLevelDels := []string{
		filepath.Join(repoRoot, ".workflow"),
		filepath.Join(repoRoot, ".claude", "specs"),
		filepath.Join(repoRoot, ".claude", "bugs"),
		filepath.Join(repoRoot, ".claude", "issues"),
		filepath.Join(repoRoot, ".claude", "plans"),
		filepath.Join(repoRoot, ".claude", "docs"),
		filepath.Join(repoRoot, ".claude", "migrations"),
	}
	for _, d := range topLevelDels {
		if exists, _ := s.statDir(d); exists {
			inv.FilesToDelete = appendIfMissing(inv.FilesToDelete, d)
		}
	}

	// ~/.workflows/<slug>/ is appended only if we read from it and can verify no errors.
	// The actual decision to delete it is deferred to CleanFilesystem.
	if inv.WorkflowLegacy != "" {
		inv.FilesToDelete = appendIfMissing(inv.FilesToDelete, inv.WorkflowLegacy)
	}

	return inv, nil
}

// scanSubdir reads a single subdirectory and returns artifacts, deletions, and preservations.
func (s *InitService) scanSubdir(_ context.Context, dir, subKind, base string) ([]LegacyArtifact, []string, error) {
	var artifacts []LegacyArtifact
	var deletions []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}

	switch subKind {
	case "plans":
		// Parse backlog.md
		for _, e := range entries {
			if e.IsDir() || e.Name() != "backlog.md" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			content, err := s.readFile(path)
			if err != nil {
				continue
			}
			arts := s.parseBacklogFile(path, content)
			artifacts = append(artifacts, arts...)
			deletions = append(deletions, path)
		}
	case "issues":
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			content, err := s.readFile(path)
			if err != nil {
				continue
			}
			fm, body := parseFrontmatter(content)
			title := extractTitle(body, e.Name())
			legacyID := legacyIDFromBasename(e.Name())
			a := LegacyArtifact{
				Kind:        KindIssue,
				SourcePath:  path,
				RawTitle:    title,
				RawBody:     body,
				Frontmatter: fm,
				LegacyID:    legacyID,
			}
			a.Signals = collectBodySignals(body, base, time.Now())
			artifacts = append(artifacts, a)
		}
	case "specs":
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			specDir := filepath.Join(dir, e.Name())
			a, qaArt := s.readSpecDir(specDir, e.Name())
			artifacts = append(artifacts, a)
			if qaArt != nil {
				artifacts = append(artifacts, *qaArt)
			}
		}
	case "bugs":
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			bugDir := filepath.Join(dir, e.Name())
			a := s.readBugDir(bugDir, e.Name())
			artifacts = append(artifacts, a)
		}
	}

	return artifacts, deletions, nil
}

// readSpecDir reads a spec directory and returns its artifact (and optionally a QA sub-artifact).
func (s *InitService) readSpecDir(specDir, dirName string) (LegacyArtifact, *LegacyArtifact) {
	specMDPath := filepath.Join(specDir, "spec.md")
	var fm map[string]string
	var body string
	if content, err := s.readFile(specMDPath); err == nil {
		fm, body = parseFrontmatter(content)
	} else {
		fm = map[string]string{}
	}

	signals := collectBodySignals(body, specDir, time.Now())

	// Check for qa-report.md presence — short-circuit to Historical.
	qaReportPath := filepath.Join(specDir, "qa-report.md")
	var qaArt *LegacyArtifact
	if _, err := os.Stat(qaReportPath); err == nil {
		signals = append(signals, Signal{Name: "qa_report_present", Weight: -10})
		// Create a separate QA artifact.
		qaContent, _ := s.readFile(qaReportPath)
		qaFM, qaBody := parseFrontmatter(qaContent)
		qa := LegacyArtifact{
			Kind:        KindQAReport,
			SourcePath:  qaReportPath,
			RawTitle:    extractTitle(qaBody, "qa-report.md"),
			RawBody:     qaBody,
			Frontmatter: qaFM,
			LegacyID:    dirName,
		}
		qaArt = &qa
	}

	a := LegacyArtifact{
		Kind:        KindSpecDir,
		SourcePath:  specDir,
		RawTitle:    extractTitle(body, dirName),
		RawBody:     body,
		Frontmatter: fm,
		LegacyID:    dirName,
		Signals:     signals,
	}
	return a, qaArt
}

// readBugDir reads a bug directory and returns its artifact.
func (s *InitService) readBugDir(bugDir, dirName string) LegacyArtifact {
	var parts []string
	for _, name := range []string{"bug-report.md", "diagnosis.md"} {
		if content, err := s.readFile(filepath.Join(bugDir, name)); err == nil {
			_, body := parseFrontmatter(content)
			parts = append(parts, body)
		}
	}
	body := strings.Join(parts, "\n\n")

	reportContent, _ := s.readFile(filepath.Join(bugDir, "bug-report.md"))
	fm, _ := parseFrontmatter(reportContent)
	title := extractTitle(body, dirName)
	signals := collectBodySignals(body, bugDir, time.Now())

	return LegacyArtifact{
		Kind:        KindBugDir,
		SourcePath:  bugDir,
		RawTitle:    title,
		RawBody:     body,
		Frontmatter: fm,
		LegacyID:    dirName,
		Signals:     signals,
	}
}

// scanDuplicates checks .claude/agents/ or .claude/commands/ for files that are
// byte-identical to the global installed versions. Returns (dups, preserves, deletions).
func (s *InitService) scanDuplicates(dir, assetSubdir string) ([]LegacyArtifact, []string, []string) {
	var dups []LegacyArtifact
	var preserve, deletions []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, nil
	}

	home, _ := os.UserHomeDir()

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		localPath := filepath.Join(dir, e.Name())
		localContent, err := s.readFile(localPath)
		if err != nil {
			continue
		}

		// Compare with global installed file.
		globalPath := filepath.Join(home, ".claude", assetSubdir, e.Name())
		globalContent, err := os.ReadFile(globalPath)

		// Special case: init.md was removed from the binary in this release.
		// Identify surviving copies by SHA-256 if the global no longer exists.
		if err != nil && strings.Contains(assetSubdir, "commands") &&
			e.Name() == "init.md" && legacyInitCommandSHA256 != "" {
			if sha256hex(localContent) == legacyInitCommandSHA256 {
				dups = append(dups, LegacyArtifact{
					Kind:       KindCommandDup,
					SourcePath: localPath,
					RawTitle:   e.Name(),
					LegacyID:   e.Name(),
				})
				deletions = append(deletions, localPath)
			} else {
				preserve = append(preserve, localPath)
			}
			continue
		}

		if err != nil {
			// Global doesn't exist — preserve the local one.
			preserve = append(preserve, localPath)
			continue
		}

		if string(localContent) == string(globalContent) {
			kind := KindAgentDup
			if strings.Contains(assetSubdir, "commands") {
				kind = KindCommandDup
			}
			dups = append(dups, LegacyArtifact{
				Kind:       kind,
				SourcePath: localPath,
				RawTitle:   e.Name(),
				LegacyID:   e.Name(),
			})
			deletions = append(deletions, localPath)
		} else {
			preserve = append(preserve, localPath)
		}
	}

	return dups, preserve, deletions
}

// --- PHASE 2: Classification ---

// ClassifyArtifact applies the weighted heuristic to a LegacyArtifact and returns
// its classification. Kind-specific short-circuits are evaluated first.
func (s *InitService) ClassifyArtifact(a LegacyArtifact) Classification {
	// Kind-specific short-circuits.
	switch a.Kind {
	case KindQAReport:
		return ClassHistorical
	case KindAgentDup, KindCommandDup:
		// Not classified — handled by cleanup only.
		return ClassHistorical
	}

	// Strong signals from frontmatter (devuelve directo).
	status := strings.ToLower(a.Frontmatter["status"])
	done := strings.ToLower(a.Frontmatter["done"])
	completed := strings.ToLower(a.Frontmatter["completed"])

	switch {
	case status == "done" || done == "true" || completed == "true":
		return ClassHistorical
	case status == "open" || status == "todo" || status == "in_progress":
		return ClassActive
	}

	// KindSpecDir: qa-report presence is captured in Signals with weight -10.
	// Fall through to weighted sum.

	total := 0
	for _, sig := range a.Signals {
		total += sig.Weight
	}

	switch {
	case total >= 2:
		return ClassActive
	case total <= -2:
		return ClassHistorical
	default:
		return ClassAmbiguous
	}
}

// classificationReason returns a human-readable explanation of the classification decision.
func (s *InitService) classificationReason(a LegacyArtifact) string {
	if len(a.Signals) == 0 {
		return "no signals detected"
	}
	var parts []string
	total := 0
	for _, sig := range a.Signals {
		parts = append(parts, fmt.Sprintf("%s(%+d)", sig.Name, sig.Weight))
		total += sig.Weight
	}
	return fmt.Sprintf("%s = %+d", strings.Join(parts, ", "), total)
}

// plannedDestination returns a human-readable destination label for dry-run output.
func (s *InitService) plannedDestination(a LegacyArtifact, cls Classification) string {
	switch {
	case cls == ClassActive && (a.Kind == KindBacklogItem || a.Kind == KindIssue || a.Kind == KindBugDir):
		return "backlog:BL-new"
	case cls == ClassActive && a.Kind == KindSpecDir:
		return "spec:SPEC-new"
	case a.Kind == KindAgentDup || a.Kind == KindCommandDup:
		return "delete (global duplicate)"
	default:
		topicKey := s.topicKeyFor(a)
		return fmt.Sprintf("memory:topic_key=%s", topicKey)
	}
}

// topicKeyFor generates the canonical topic_key for a LegacyArtifact.
func (s *InitService) topicKeyFor(a LegacyArtifact) string {
	switch a.Kind {
	case KindBacklogItem:
		return "backlog/" + s.slug + "/" + slugify(a.RawTitle)
	case KindIssue:
		return "issue/" + a.LegacyID
	case KindSpecDir:
		return "spec/" + a.LegacyID
	case KindBugDir:
		return "bug/" + a.LegacyID
	case KindQAReport:
		return "qa/" + a.LegacyID
	default:
		return "legacy/" + fmt.Sprintf("%08x", fnv32(a.SourcePath))
	}
}

// artifactKey returns the unique key for a LegacyArtifact within a plan.
// BacklogItem artifacts share SourcePath (all come from the same backlog.md),
// so we disambiguate by appending the title. All other kinds use SourcePath alone.
func artifactKey(a LegacyArtifact) string {
	if a.Kind == KindBacklogItem {
		return a.SourcePath + "#" + a.RawTitle
	}
	return a.SourcePath
}

// findArtifact searches the cached inventory for an artifact by its unique key.
// Using the key (rather than SourcePath alone) prevents multiple backlog items from
// the same file from aliasing to the first item in the list.
func (s *InitService) findArtifact(key string) LegacyArtifact {
	if s.inventory == nil {
		return LegacyArtifact{SourcePath: key}
	}
	for _, a := range s.inventory.Artifacts {
		if artifactKey(a) == key {
			return a
		}
	}
	return LegacyArtifact{SourcePath: key}
}

// --- PHASE 2: Migration ---

// MigrateToBacklog creates a BacklogItem from a legacy artifact classified as active.
// Used for KindBacklogItem, KindIssue, and KindBugDir.
func (s *InitService) MigrateToBacklog(ctx context.Context, a LegacyArtifact) (*model.BacklogItem, error) {
	desc := a.RawBody
	priority := model.PriorityMedium
	if a.Kind == KindBugDir {
		// Bug dirs get high priority — pending bugs are urgent.
		priority = model.PriorityHigh
		rel := a.SourcePath
		desc = fmt.Sprintf("# Bug migrado: %s\n\n## Reporte original\n%s\n\n## Archivo original\n%s",
			a.RawTitle, a.RawBody, rel)
	}

	req := model.BacklogAddRequest{
		Title:       a.RawTitle,
		Description: desc,
		Priority:    priority,
	}
	item, err := s.sdd.BacklogAdd(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("service: init: migrate to backlog: %w", err)
	}
	return item, nil
}

// MigrateToSpec creates a Spec from a legacy spec directory classified as active.
// It infers the target state from the directory contents and advances through the
// state machine, then copies the legacy spec.md body over the generated template.
func (s *InitService) MigrateToSpec(ctx context.Context, a LegacyArtifact) (*model.Spec, error) {
	spec, err := s.sdd.SpecNew(ctx, model.SpecNewRequest{
		Title: a.RawTitle,
	})
	if err != nil {
		return nil, fmt.Errorf("service: init: migrate to spec: new: %w", err)
	}

	// Infer the target state from directory contents.
	targetStatus := s.inferSpecStatus(a.SourcePath)

	// Advance through the state machine from draft to the inferred status.
	// draft → speccing (creates the spec dir + copies template).
	advanceReq := model.SpecAdvanceRequest{
		ID:     spec.ID,
		By:     "mneme-init",
		Reason: "migrated from legacy workflow",
	}

	stateOrder := []model.SpecStatus{
		model.SpecStatusSpeccing,
		model.SpecStatusSpecced,
		model.SpecStatusPlanning,
		model.SpecStatusPlanned,
		model.SpecStatusImplementing,
		model.SpecStatusQA,
	}

	for _, st := range stateOrder {
		updated, advErr := s.sdd.SpecAdvance(ctx, advanceReq)
		if advErr != nil {
			return nil, fmt.Errorf("service: init: migrate to spec: advance: %w", advErr)
		}
		spec = updated
		if updated.Status == targetStatus {
			break
		}
		_ = st
	}

	// Copy the legacy spec.md over the generated template (if we have body content).
	if a.RawBody != "" {
		newSpecPath := filepath.Join(
			s.cfg.ProjectWorkflowDir(s.sdd.project),
			"specs", spec.ID, "spec.md",
		)
		if writeErr := s.writeFile(newSpecPath, []byte(a.RawBody), 0o644); writeErr != nil {
			// Non-fatal: the spec was created, body copy failed.
			_ = writeErr
		}
	}

	return spec, nil
}

// inferSpecStatus determines the target SpecStatus for a legacy spec directory
// based on its file contents.
func (s *InitService) inferSpecStatus(specDir string) model.SpecStatus {
	qaPath := filepath.Join(specDir, "qa-report.md")
	if _, err := os.Stat(qaPath); err == nil {
		// qa-report exists: the spec reached QA.
		return model.SpecStatusQA
	}
	changesPath := filepath.Join(specDir, "changes.md")
	if _, err := os.Stat(changesPath); err == nil {
		return model.SpecStatusImplementing
	}
	planPath := filepath.Join(specDir, "plan.md")
	implPath := filepath.Join(specDir, "implementation-plan.md")
	if _, err := os.Stat(planPath); err == nil {
		return model.SpecStatusPlanned
	}
	if _, err := os.Stat(implPath); err == nil {
		return model.SpecStatusPlanned
	}
	specMDPath := filepath.Join(specDir, "spec.md")
	if _, err := os.Stat(specMDPath); err == nil {
		return model.SpecStatusSpecced
	}
	return model.SpecStatusSpeccing
}

// MigrateToMemory saves a legacy artifact as a memory entry.
// Used for historical/ambiguous artifacts across all kinds.
// Returns a synthetic Memory containing the ID from SaveResponse so callers
// can record which IDs were generated.
func (s *InitService) MigrateToMemory(ctx context.Context, a LegacyArtifact) (*model.Memory, error) {
	memType := s.memoryTypeFor(a)
	topicKey := s.topicKeyFor(a)

	title := a.RawTitle
	if title == "" {
		title = filepath.Base(a.SourcePath)
	}

	content := fmt.Sprintf("# %s\n\n%s", title, a.RawBody)
	if a.RawBody == "" {
		content = fmt.Sprintf("Legacy artifact migrated from: %s", a.SourcePath)
	}

	resp, err := s.memory.Save(ctx, model.SaveRequest{
		Title:    title,
		Type:     memType,
		Scope:    model.ScopeProject,
		TopicKey: topicKey,
		Content:  content,
		Project:  s.slug,
	})
	if err != nil {
		return nil, fmt.Errorf("service: init: migrate to memory: %w", err)
	}
	// SaveResponse does not embed the full Memory struct; return a synthetic one
	// with the ID so callers can track which IDs were created.
	return &model.Memory{ID: resp.ID, TopicKey: topicKey, Title: title}, nil
}

// memoryTypeFor returns the appropriate memory type for a legacy artifact kind.
func (s *InitService) memoryTypeFor(a LegacyArtifact) model.MemoryType {
	switch a.Kind {
	case KindSpecDir:
		return model.TypeArchitecture
	case KindBugDir:
		return model.TypeBugfix
	case KindQAReport:
		return model.TypeDiscovery
	default:
		return model.TypeDecision
	}
}

// --- PHASE 3: CleanFilesystem ---

// CleanFilesystem removes the paths listed in plan.Paths. Errors are collected
// as non-fatal strings in the result rather than aborting the operation.
func (s *InitService) CleanFilesystem(_ context.Context, plan CleanupPlan) (CleanupResult, error) {
	var result CleanupResult
	for _, p := range plan.Paths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			// Already gone — count as deleted idempotently.
			result.Deleted = append(result.Deleted, p)
			continue
		}
		if err := s.removeAll(p); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", p, err))
		} else {
			result.Deleted = append(result.Deleted, p)
		}
	}

	// Clean up empty .claude/agents/ and .claude/commands/ dirs if applicable.
	// (Best-effort; errors ignored.)
	return result, nil
}

// --- PHASE 4: RewriteClaudeLocal ---

// RewriteClaudeLocal backs up the existing CLAUDE.local.md (if any) and writes
// the canonical SDD-engine template. The backup is placed at CLAUDE.local.md.bak.
func (s *InitService) RewriteClaudeLocal(_ context.Context, repoRoot, slug string) (ClaudeLocalResult, error) {
	target := filepath.Join(repoRoot, "CLAUDE.local.md")
	backup := target + ".bak"

	var result ClaudeLocalResult
	result.WrittenPath = target

	// Check if file exists.
	existing, err := s.readFile(target)
	if err == nil {
		result.ExistedBefore = true
		result.BackupPath = backup
		// Write backup.
		if writeErr := s.writeFile(backup, existing, 0o644); writeErr != nil {
			return result, fmt.Errorf("service: init: rewrite claude local: backup: %w", writeErr)
		}
	}

	content := strings.ReplaceAll(claudeLocalTemplate, "{{SLUG}}", slug)
	if err := s.writeFile(target, []byte(content), 0o644); err != nil {
		return result, fmt.Errorf("service: init: rewrite claude local: write: %w", err)
	}

	return result, nil
}

// --- PHASE 5: EmitReport ---

// EmitReport writes the init-report.md to the workflow directory and returns
// its absolute path. The file is overwritten on every run — it represents the
// last execution, not a cumulative log.
func (s *InitService) EmitReport(_ context.Context, report InitReport) (string, error) {
	dir := s.cfg.ProjectWorkflowDir(s.slug)
	if err := s.mkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("service: init: emit report: mkdir: %w", err)
	}

	reportPath := filepath.Join(dir, "init-report.md")
	content := renderInitReport(report)

	if err := s.writeFile(reportPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("service: init: emit report: write: %w", err)
	}
	return reportPath, nil
}

// renderInitReport produces the markdown content for init-report.md.
func renderInitReport(r InitReport) string {
	mode := "dry-run"
	if r.Applied {
		mode = "applied"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# mneme init — Reporte\n\n")
	fmt.Fprintf(&b, "Proyecto: %s\n", r.Slug)
	fmt.Fprintf(&b, "Ejecutado: %s\n", r.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Modo: %s\n\n", mode)

	fmt.Fprintf(&b, "## Resumen\n\n")
	fmt.Fprintf(&b, "- Artefactos detectados: %d\n", len(r.Plan.Artifacts))
	fmt.Fprintf(&b, "- Migrados a backlog: %d\n", len(r.MigratedBacklog))
	fmt.Fprintf(&b, "- Migrados a specs: %d\n", len(r.MigratedSpecs))
	fmt.Fprintf(&b, "- Migrados a memoria: %d\n", len(r.MigratedMemories))
	fmt.Fprintf(&b, "- Clasificados como dudosos: %d\n", len(r.Ambiguous))
	fmt.Fprintf(&b, "- Archivos/directorios borrados: %d\n\n", len(r.Cleanup.Deleted))

	if len(r.MigratedBacklog) > 0 {
		fmt.Fprintf(&b, "## Migrado a backlog (vigente)\n\n")
		fmt.Fprintf(&b, "| Source | Nuevo ID |\n|---|---|\n")
		for _, entry := range r.MigratedBacklog {
			fmt.Fprintf(&b, "| %s | %s |\n", entry.Source, entry.ID)
		}
		fmt.Fprintln(&b)
	}

	if len(r.MigratedSpecs) > 0 {
		fmt.Fprintf(&b, "## Migrado a specs (vigente)\n\n")
		fmt.Fprintf(&b, "| Source | Nuevo ID |\n|---|---|\n")
		for _, entry := range r.MigratedSpecs {
			fmt.Fprintf(&b, "| %s | %s |\n", entry.Source, entry.ID)
		}
		fmt.Fprintln(&b)
	}

	if len(r.MigratedMemories) > 0 {
		fmt.Fprintf(&b, "## Migrado a memoria (histórico)\n\n")
		fmt.Fprintf(&b, "| Source | topic_key |\n|---|---|\n")
		for _, entry := range r.MigratedMemories {
			fmt.Fprintf(&b, "| %s | %s |\n", entry.Source, entry.ID)
		}
		fmt.Fprintln(&b)
	}

	if len(r.Ambiguous) > 0 {
		fmt.Fprintf(&b, "## Dudosos (revisar)\n\n")
		fmt.Fprintf(&b, "| Source | Señales detectadas | Destino aplicado |\n|---|---|---|\n")
		for _, a := range r.Ambiguous {
			sigs := make([]string, len(a.Signals))
			for i, sig := range a.Signals {
				sigs[i] = fmt.Sprintf("%s(%+d)", sig.Name, sig.Weight)
			}
			fmt.Fprintf(&b, "| %s | %s | memoria |\n", a.SourcePath, strings.Join(sigs, ", "))
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "## Limpieza de filesystem\n\n")
	if len(r.Cleanup.Deleted) > 0 {
		fmt.Fprintf(&b, "- Borrados: %s\n", strings.Join(r.Cleanup.Deleted, ", "))
	}
	if len(r.Cleanup.Kept) > 0 {
		fmt.Fprintf(&b, "- Preservados: %s\n", strings.Join(r.Cleanup.Kept, ", "))
	}
	if len(r.Cleanup.Errors) > 0 {
		fmt.Fprintf(&b, "- Errores: %s\n", strings.Join(r.Cleanup.Errors, ", "))
	} else {
		fmt.Fprintf(&b, "- Errores: ninguno\n")
	}

	fmt.Fprintf(&b, "\n## CLAUDE.local.md\n\n")
	if r.CLAUDELocal.BackupPath != "" {
		fmt.Fprintf(&b, "- Backup: `%s`\n", r.CLAUDELocal.BackupPath)
	}
	fmt.Fprintf(&b, "- Nuevo: `%s`\n", r.CLAUDELocal.WrittenPath)

	fmt.Fprintf(&b, `
## Próximos pasos sugeridos

1. Revisa la sección "Dudosos" y decide qué recuperar.
2. Corre `+"`mneme status`"+` para ver el estado unificado.
3. `+"`git add CLAUDE.local.md && git commit -m \"chore: migrate to mneme SDD\"`"+` (opcional).
`)

	return b.String()
}

// --- Pipeline orchestration ---

// Plan performs a dry-run: detects legacy artifacts and computes the InitReport
// without writing anything to filesystem or DB.
func (s *InitService) Plan(ctx context.Context, repoRoot string) (InitReport, error) {
	inv, err := s.DetectLegacy(ctx, repoRoot)
	if err != nil {
		return InitReport{}, err
	}
	s.inventory = &inv

	var plan InitPlan
	for _, a := range inv.Artifacts {
		cls := s.ClassifyArtifact(a)
		dest := s.plannedDestination(a, cls)
		reason := s.classificationReason(a)
		plan.Artifacts = append(plan.Artifacts, PlannedArtifact{
			Source:         a.SourcePath,
			Kind:           a.Kind,
			Classification: cls,
			Destination:    dest,
			Reason:         reason,
			ArtifactKey:    artifactKey(a),
		})
	}
	plan.Deletions = inv.FilesToDelete
	plan.Rewrites = []string{filepath.Join(repoRoot, "CLAUDE.local.md")}

	return InitReport{
		Slug:        s.slug,
		Plan:        plan,
		GeneratedAt: s.now(),
	}, nil
}

// Apply runs Plan, then executes all migrations, filesystem cleanup, and
// CLAUDE.local.md rewrite. Returns the populated InitReport.
func (s *InitService) Apply(ctx context.Context, repoRoot string) (InitReport, error) {
	report, err := s.Plan(ctx, repoRoot)
	if err != nil {
		return report, err
	}
	report.Applied = true

	// Phase 2: migrate each artifact.
	for _, p := range report.Plan.Artifacts {
		a := s.findArtifact(p.ArtifactKey)
		switch {
		case (p.Classification == ClassActive) &&
			(p.Kind == KindBacklogItem || p.Kind == KindIssue || p.Kind == KindBugDir):
			item, migrErr := s.MigrateToBacklog(ctx, a)
			if migrErr != nil {
				report.addError(p.Source, migrErr)
				continue
			}
			report.MigratedBacklog = append(report.MigratedBacklog, MigratedEntry{Source: p.Source, ID: item.ID})

		case p.Classification == ClassActive && p.Kind == KindSpecDir:
			spec, migrErr := s.MigrateToSpec(ctx, a)
			if migrErr != nil {
				report.addError(p.Source, migrErr)
				continue
			}
			report.MigratedSpecs = append(report.MigratedSpecs, MigratedEntry{Source: p.Source, ID: spec.ID})

		case p.Kind == KindAgentDup || p.Kind == KindCommandDup:
			// No migration needed — handled by cleanup.

		default:
			mem, migrErr := s.MigrateToMemory(ctx, a)
			if migrErr != nil {
				report.addError(p.Source, migrErr)
				continue
			}
			report.MigratedMemories = append(report.MigratedMemories, MigratedEntry{Source: p.Source, ID: mem.ID})
			if p.Classification == ClassAmbiguous {
				report.Ambiguous = append(report.Ambiguous, a)
			}
		}
	}

	// Phase 3: cleanup — only when no critical migration errors.
	if !report.HasCriticalErrors() {
		cleanup, cleanErr := s.CleanFilesystem(ctx, CleanupPlan{Paths: report.Plan.Deletions})
		if cleanErr != nil {
			// Non-fatal: log and continue.
			_ = cleanErr
		}
		report.Cleanup = cleanup
	}

	// Phase 4: rewrite CLAUDE.local.md.
	claudeRes, rewriteErr := s.RewriteClaudeLocal(ctx, repoRoot, s.slug)
	if rewriteErr != nil {
		_ = rewriteErr
	}
	report.CLAUDELocal = claudeRes

	return report, nil
}

// --- Parsing helpers ---

// reBacklogItem matches both checked and unchecked markdown task list items.
var reBacklogItem = regexp.MustCompile(`^- \[([ xX])\] (.+)$`)

// parseBacklogFile returns LegacyArtifact entries for every task list item in
// a backlog.md file. Checked items get Signal{Name:"checked", Weight:-5};
// unchecked items get Signal{Name:"unchecked", Weight:+5}.
func (s *InitService) parseBacklogFile(path string, content []byte) []LegacyArtifact {
	var arts []LegacyArtifact
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimRight(line, "\r")
		m := reBacklogItem.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		checked := m[1] != " "
		title := strings.TrimSpace(m[2])
		weight := 5
		sigName := "unchecked"
		if checked {
			weight = -5
			sigName = "checked"
		}
		legacyID := fmt.Sprintf("legacy-%08x", fnv32(path+title))
		arts = append(arts, LegacyArtifact{
			Kind:        KindBacklogItem,
			SourcePath:  path,
			RawTitle:    title,
			RawBody:     line,
			Frontmatter: map[string]string{},
			LegacyID:    legacyID,
			Signals:     []Signal{{Name: sigName, Weight: weight}},
		})
	}
	return arts
}

// reFrontmatterDelim matches the --- delimiter lines.
var reFrontmatterDelim = regexp.MustCompile(`^---\s*$`)

// reFrontmatterKV matches a simple key: value line.
var reFrontmatterKV = regexp.MustCompile(`^([A-Za-z_][\w-]*):\s*(.+)$`)

// parseFrontmatter splits a markdown document into YAML frontmatter (if any)
// and the remaining body. Only accepts the --- delimited format at the start.
// Returns an empty map and the full content as body when frontmatter is absent
// or malformed. Keys with quoted values have their outer quotes stripped.
func parseFrontmatter(raw []byte) (fm map[string]string, body string) {
	fm = map[string]string{}
	text := string(raw)
	lines := strings.Split(text, "\n")

	if len(lines) < 2 || !reFrontmatterDelim.MatchString(lines[0]) {
		return fm, text
	}

	// Find closing ---.
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if reFrontmatterDelim.MatchString(lines[i]) {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return fm, text
	}

	for _, l := range lines[1:closeIdx] {
		m := reFrontmatterKV.FindStringSubmatch(l)
		if len(m) != 3 {
			continue
		}
		key := m[1]
		val := strings.TrimSpace(m[2])
		// Strip outer quotes.
		if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
			(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
			val = val[1 : len(val)-1]
		}
		fm[key] = val
	}

	body = strings.Join(lines[closeIdx+1:], "\n")
	return fm, body
}

// reH1 matches a markdown H1 heading.
var reH1 = regexp.MustCompile(`(?m)^# (.+)$`)

// extractTitle returns the first H1 heading from body, or the basename of filename
// without its extension if no heading is found.
func extractTitle(body, filename string) string {
	if m := reH1.FindStringSubmatch(body); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	base := filepath.Base(filename)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// slugify converts a string to a lowercase, hyphen-separated slug for use in
// topic_key values. Only [a-z0-9-] characters are kept; runs of non-alnum chars
// become a single hyphen. Max 40 characters.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 40 {
		result = result[:40]
	}
	return result
}

// legacyIDFromBasename extracts a legacy ID from a file basename.
// e.g. "ISSUE-007.md" → "ISSUE-007", "P009" → "P009".
func legacyIDFromBasename(name string) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext)
}

// collectBodySignals scans the artifact body for weighted signals.
func collectBodySignals(body, path string, now time.Time) []Signal {
	var signals []Signal

	lower := strings.ToLower(body)

	// Negative signals (historical).
	for _, word := range []string{"completed", "closed", "resolved"} {
		if containsWholeWord(lower, word) {
			signals = append(signals, Signal{Name: word + ":in_body", Weight: -2})
		}
	}

	lowerPath := strings.ToLower(path)
	for _, word := range []string{"done", "archived", "archive"} {
		if strings.Contains(lowerPath, word) {
			signals = append(signals, Signal{Name: word + ":in_path", Weight: -3})
		}
	}

	if containsSection(lower, "date closed") || containsSection(lower, "resolved on") {
		signals = append(signals, Signal{Name: "date_closed_section", Weight: -3})
	}

	// Checkbox ratio (>70% checked → historical).
	total, checked := countCheckboxes(body)
	if total > 0 && float64(checked)/float64(total) > 0.7 {
		signals = append(signals, Signal{Name: "checkboxes:mostly_checked", Weight: -1})
	}

	// Positive signals (active).
	for _, word := range []string{"todo", "pendiente", "wip", "in progress"} {
		if strings.Contains(lower, word) {
			signals = append(signals, Signal{Name: word + ":in_body", Weight: 1})
		}
	}

	// Time-based signals based on filesystem mtime.
	// Threshold: < 30 days → active (+1); > 180 days → historical (-1).
	// Skipped when path is empty (e.g. unit tests with synthetic artifacts).
	if path != "" {
		if info, statErr := os.Stat(path); statErr == nil {
			age := now.Sub(info.ModTime())
			const day = 24 * time.Hour
			switch {
			case age < 30*day:
				signals = append(signals, Signal{Name: "mtime:recent_30d", Weight: 1})
			case age > 180*day:
				signals = append(signals, Signal{Name: "mtime:old_180d", Weight: -1})
			}
		}
	}

	return signals
}

// containsWholeWord reports whether word appears as a whole word in s.
func containsWholeWord(s, word string) bool {
	idx := strings.Index(s, word)
	if idx < 0 {
		return false
	}
	before := idx == 0 || !unicode.IsLetter(rune(s[idx-1]))
	after := idx+len(word) >= len(s) || !unicode.IsLetter(rune(s[idx+len(word)]))
	return before && after
}

// containsSection checks for a markdown section heading (case-insensitive).
func containsSection(lower, section string) bool {
	return strings.Contains(lower, "## "+strings.ToLower(section))
}

// countCheckboxes counts total and checked markdown task list items in body.
func countCheckboxes(body string) (total, checked int) {
	re := regexp.MustCompile(`- \[([ xX])\]`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		total++
		if m[1] != " " {
			checked++
		}
	}
	return
}

// appendIfMissing appends s to slice only if it is not already present.
func appendIfMissing(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// fnv32 returns a 32-bit FNV hash of s, used for fallback ID generation.
func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// sha256hex returns the lowercase hex SHA-256 digest of data.
// Used to identify surviving copies of removed slash command assets.
func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
