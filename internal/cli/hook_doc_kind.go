package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/wirvii/mneme/internal/model"
)

// specDocKindBlockMessage mirrors taskSectionBlockMessage's shape but names
// the KIND, not the tool: spec_doc_write itself is not restricted, only the
// kinds listed in model's specDocKindWriters.
const specDocKindBlockMessage = "⛔ mneme: spec_doc_write kind %q está restringido a %s. Este subagente no puede escribirlo. El orquestador (o el arquitecto) puede hacerlo en su lugar.\n"

// specDocKindUnresolvedRoleMessage documents SPEC-118 D10's deliberate
// posture: the rule fails OPEN when the role cannot be resolved. Closing it
// would leave the architect unable to write any restricted document the
// moment agent_type stopped arriving in the payload; the human approval gate
// is a sufficient downstream check on its own.
const specDocKindUnresolvedRoleMessage = "⚠ mneme: spec_doc_write kind %q está restringido a %s, pero el rol de este subagente no se pudo resolver — se permite (falla ABIERTO a propósito).\n"

// specDocKindUnresolvedArchetypeMessage is the fail-open message for a custom
// role whose archetype cannot be found in the project manifest.
const specDocKindUnresolvedArchetypeMessage = "⚠ mneme: spec_doc_write kind %q: no se pudo resolver el arquetipo del rol %q en el manifiesto; la restricción por kind no se aplica en esta llamada.\n"

// checkSpecDocKindScope reports whether a spec_doc_write call must be blocked
// because the calling subagent's archetype may not write the requested kind
// (SPEC-181 D4). It returns true to block (the caller owns the exit). A kind
// with no entry in model.SpecDocKindWriters is never restricted, and anything
// that cannot be decided — unresolved role, unresolvable archetype — is
// allowed with a warning. The orchestrator is never restricted.
func checkSpecDocKindScope(input hookPreToolInput, identity CallerIdentity, errW io.Writer, archetypeOf archetypeLookup) bool {
	if !identity.IsSubagent || input.ToolName != "mcp__mneme__spec_doc_write" {
		return false
	}
	kind := model.SpecDocKind(input.ToolInput.Kind)
	writers := model.SpecDocKindWriters(kind)
	if len(writers) == 0 {
		return false
	}
	allowed := strings.Join(writers, ", ")
	if identity.RoleSource == "unresolved" {
		fmt.Fprintf(errW, specDocKindUnresolvedRoleMessage, input.ToolInput.Kind, allowed)
		return false
	}

	archetype := identity.Role
	if !builtinArchetypes[archetype] {
		resolved, ok := archetypeOf(identity.Role)
		if !ok {
			fmt.Fprintf(errW, specDocKindUnresolvedArchetypeMessage, input.ToolInput.Kind, identity.Role)
			return false
		}
		archetype = resolved
	}

	if model.SpecDocKindAllows(kind, archetype) {
		return false
	}
	printRoleScopedDocKindBlock(errW, input.ToolInput.Kind, allowed)
	logRoleScopedDocKindDeniedEvent(input, identity, allowed)
	return true
}

// printRoleScopedDocKindBlock writes the role-scoped-by-kind denial message
// to w; allowed is the comma-joined list of archetypes that may write kind.
func printRoleScopedDocKindBlock(w io.Writer, kind, allowed string) {
	fmt.Fprintf(w, specDocKindBlockMessage, kind, allowed)
}
