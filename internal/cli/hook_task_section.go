package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/subagents"
)

// taskSectionBlockMessage is printed when a subagent tries to write a
// tarea.md section that belongs to another role (SPEC-170 D6).
const taskSectionBlockMessage = "⛔ mneme: spec_doc_write kind \"task\" sección %q está restringida a %s. Este subagente no puede escribirla. Escribí solo la sección de tu rol; el orquestador puede escribir cualquiera.\n"

// taskSectionUnresolvedRoleMessage documents the fail-open posture when the
// payload carries no agent_type: containment cannot tell who is writing, and
// closing the rule would leave every role unable to write its section.
const taskSectionUnresolvedRoleMessage = "⚠ mneme: spec_doc_write kind \"task\" sección %q: no se pudo resolver el rol del subagente (agent_type ausente); la restricción por sección no se aplica en esta llamada.\n"

// taskSectionUnresolvedArchetypeMessage is the fail-open message for a custom
// role whose archetype cannot be found in the project manifest.
const taskSectionUnresolvedArchetypeMessage = "⚠ mneme: spec_doc_write kind \"task\" sección %q: no se pudo resolver el arquetipo del rol %q en el manifiesto; la restricción por sección no se aplica en esta llamada.\n"

// archetypeLookup resolves a custom role name to its archetype, reporting
// false when the manifest cannot be read or does not list the role.
type archetypeLookup func(role string) (archetype string, ok bool)

// builtinArchetypes are the six archetype names a role may carry directly;
// for them no manifest I/O is needed.
var builtinArchetypes = map[string]bool{
	string(subagents.RoleArchitect):     true,
	string(subagents.RoleBackend):       true,
	string(subagents.RoleFrontend):      true,
	string(subagents.RoleQATester):      true,
	string(subagents.RoleBugHunter):     true,
	string(subagents.RoleDiagnostician): true,
}

// checkTaskSectionScope reports whether a spec_doc_write kind=task call must
// be blocked because the calling subagent's archetype may not write the
// requested section (SPEC-170 D6). It returns true to block (the caller owns
// the exit). Everything that cannot be decided — unknown or empty section,
// unresolved role, unresolvable archetype — is allowed with a warning: the
// service validates the section itself, and failing closed here would stop
// every role from writing its own section if the payload shape ever changed.
// The orchestrator is never restricted.
func checkTaskSectionScope(input hookPreToolInput, identity CallerIdentity, errW io.Writer, archetypeOf archetypeLookup) bool {
	if !identity.IsSubagent || input.ToolName != "mcp__mneme__spec_doc_write" || input.ToolInput.Kind != string(model.SpecDocKindTask) {
		return false
	}
	section := model.TaskSection(input.ToolInput.Section)
	if _, known := section.Heading(); !known {
		return false
	}
	if identity.RoleSource == "unresolved" {
		fmt.Fprintf(errW, taskSectionUnresolvedRoleMessage, input.ToolInput.Section)
		return false
	}

	archetype := identity.Role
	if !builtinArchetypes[archetype] {
		resolved, ok := archetypeOf(identity.Role)
		if !ok {
			fmt.Fprintf(errW, taskSectionUnresolvedArchetypeMessage, input.ToolInput.Section, identity.Role)
			return false
		}
		archetype = resolved
	}

	if model.TaskSectionAllows(section, archetype) {
		return false
	}
	fmt.Fprintf(errW, taskSectionBlockMessage, input.ToolInput.Section, strings.Join(model.TaskSectionWriters(section), ", "))
	logRoleScopedTaskSectionDeniedEvent(input, identity)
	return true
}

// manifestArchetypeLookup builds the production archetypeLookup: it reads the
// project manifest lazily (only when a custom role needs resolving) through
// the same read-only query path-owned uses.
func manifestArchetypeLookup(input hookPreToolInput) archetypeLookup {
	return func(role string) (string, bool) {
		cfg, err := config.Load(config.DefaultPath())
		if err != nil {
			return "", false
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		cwd = resolveHookCWD(input, cwd)
		slug, _ := project.NewDetector(cwd).DetectProject()
		return archetypeFromManifest(cfg.ProjectDBPath(slug), slug, role)
	}
}

// archetypeFromManifest returns the effective archetype of role in the
// project's subagent manifest, false on any read, parse or lookup failure.
func archetypeFromManifest(dbPath, slug, role string) (string, bool) {
	content, found, err := queryManifestContent(dbPath, slug)
	if err != nil || !found {
		return "", false
	}
	var entries []hookManifestEntry
	if err := json.Unmarshal([]byte(content), &entries); err != nil {
		return "", false
	}
	entry, ok := findManifestEntryByRole(entries, role)
	if !ok {
		return "", false
	}
	return string(entry.effectiveArchetype()), true
}

// logRoleScopedTaskSectionDeniedEvent appends a best-effort enforcelog event
// for a tarea.md section denial (Reason "role_scoped_task_section_denied"),
// shaped like logRoleScopedDocKindDeniedEvent. Failures are ignored.
func logRoleScopedTaskSectionDeniedEvent(input hookPreToolInput, identity CallerIdentity) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	cwd = resolveHookCWD(input, cwd)
	slug, _ := project.NewDetector(cwd).DetectProject()

	ev := enforcelog.Event{
		TS:         time.Now().UTC(),
		Session:    input.SessionID,
		Project:    slug,
		Caller:     "subagent",
		AgentID:    identity.AgentID,
		Role:       identity.Role,
		RoleSource: identity.RoleSource,
		Tool:       input.ToolName,
		Target:     "task:" + input.ToolInput.Section,
		Decision:   enforcelog.DecisionBlock,
		Reason:     "role_scoped_task_section_denied",
	}
	_ = enforcelog.Append(enforcelogPath(cfg.Storage.DataDir, slug), ev, enforcelog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
}
