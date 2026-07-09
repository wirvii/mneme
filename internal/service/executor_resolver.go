package service

import (
	"fmt"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/subagents"
)

// Executor values identify who should execute a stage: a delegated subagent
// or the orchestrator itself as a conscious fallback (SPEC-068 D2).
const (
	executorSubagent     = "subagent"
	executorOrchestrator = "orchestrator"
)

// Responsible-role labels used as the ExecutorResolution.ResponsibleRole
// value and as the key space of stageResponsibleRole (SPEC-068 D3). These are
// intentionally plain strings, not subagents.Role: "implementer" is a class
// (backend or frontend), not a single role, and "orchestrator" is not a
// subagent role at all.
const (
	responsibleArchitect    = "architect"
	responsibleImplementer  = "implementer"
	responsibleQATester     = "qa-tester"
	responsibleOrchestrator = "orchestrator"
)

// ExecutorSubagent identifies one subagent, drawn from the project's
// manifest, capable of executing a resolved stage.
type ExecutorSubagent struct {
	// Role is the subagent role (e.g. "backend", "architect").
	Role string `json:"role"`

	// Path is the absolute filesystem path of the generated profile, copied
	// verbatim from the matching ManifestEntry.
	Path string `json:"path"`
}

// ExecutorResolution is the advisory output of ResolveStageExecutor. It never
// mutates SDD state and never triggers anything by itself — it is attached to
// the spec_advance response (see internal/mcp handleSpecAdvance) so the
// orchestrator can decide whether to delegate the stage it just entered to a
// subagent, or supply it directly as a conscious fallback (SPEC-068 D2/D12).
type ExecutorResolution struct {
	// Stage is the spec status this resolution was computed for (the
	// to-status of the transition that just completed).
	Stage model.SpecStatus `json:"stage"`

	// ResponsibleRole is who is responsible for this stage under the
	// canonical SDD flow: "architect", "implementer", "qa-tester", or
	// "orchestrator" (SPEC-068 D3).
	ResponsibleRole string `json:"responsible_role"`

	// Executor is "subagent" when Delegate is true and at least one matching
	// subagent exists in the manifest, or "orchestrator" otherwise.
	Executor string `json:"executor"`

	// Delegate is true when the orchestrator should launch one of Subagents
	// to execute this stage.
	Delegate bool `json:"delegate"`

	// Subagents lists the manifest entries capable of executing this stage.
	// Empty when Delegate is false.
	Subagents []ExecutorSubagent `json:"subagents,omitempty"`

	// Degraded is true only when ResponsibleRole is architect, implementer,
	// or qa-tester AND no matching subagent exists in the manifest — i.e. the
	// orchestrator is supplying a stage that a specialised subagent would
	// normally own (SPEC-068 D3/D12, a conscious fallback, not the ideal).
	// Gates and orchestrator-owned stages are never Degraded: they are the
	// orchestrator's normal job, not a fallback.
	Degraded bool `json:"degraded"`

	// Hint is a short human-readable note for the orchestrator: who to
	// delegate to, or (when Degraded) that this is a degraded fallback and
	// how to remove it (materialize the subagent via the mneme-init grill).
	Hint string `json:"hint"`
}

// stageResponsibleRole maps a spec stage (to-status) to the role responsible
// for executing it (SPEC-068 D3), anchored to the canonical SDD flow
// documented in CLAUDE.local.md: architect owns speccing/planning, the
// implementer class owns implementing, qa-tester owns qa. Gates (specced,
// planned) and deterministic/terminal stages (rationale, audit, done, draft,
// needs_grill) are genuinely the orchestrator's own responsibility — they are
// not degraded fallback, they are its normal job (D3). Any status absent from
// this map (there should be none among the valid model.SpecStatus values)
// defaults to orchestrator via the zero value lookup in ResolveStageExecutor.
var stageResponsibleRole = map[model.SpecStatus]string{
	model.SpecStatusSpeccing:     responsibleArchitect,
	model.SpecStatusPlanning:     responsibleArchitect,
	model.SpecStatusImplementing: responsibleImplementer,
	model.SpecStatusQA:           responsibleQATester,
	model.SpecStatusSpecced:      responsibleOrchestrator,
	model.SpecStatusPlanned:      responsibleOrchestrator,
	model.SpecStatusRationale:    responsibleOrchestrator,
	model.SpecStatusAudit:        responsibleOrchestrator,
	model.SpecStatusDone:         responsibleOrchestrator,
	model.SpecStatusDraft:        responsibleOrchestrator,
	model.SpecStatusNeedsGrill:   responsibleOrchestrator,
}

// ResolveStageExecutor is a pure function (SPEC-068 D2): given the stage a
// spec just transitioned into, its lane, and the project's subagent
// manifest, it recommends whether the orchestrator should delegate the stage
// to a subagent or supply it directly as a conscious fallback. It performs no
// I/O and does not touch the SDD state machine — SDDService.SpecAdvance is
// completely unaffected by this function; it is advisory output consumed by
// the MCP frontend (handleSpecAdvance).
//
// lane is accepted for signature completeness and because callers (the MCP
// handler) always have it available from the just-advanced spec, but
// stageResponsibleRole already covers every status reachable by either lane
// (trivial-lane specs only ever reach draft, rationale, implementing, audit,
// needs_grill, done — all mapped) — so no branch on lane is currently needed.
// Keying by stage (the to-status of the transition) rather than the
// free-text `--by <role>` argument SpecAdvance records is deliberate
// (SPEC-068 R6): the resolver answers "who is responsible for the stage just
// entered", independent of who reported having done the previous stage's
// work.
func ResolveStageExecutor(status model.SpecStatus, lane model.Lane, manifest []ManifestEntry) ExecutorResolution {
	_ = lane // see godoc: reserved for signature completeness, not currently branched on.

	role, ok := stageResponsibleRole[status]
	if !ok {
		role = responsibleOrchestrator
	}

	res := ExecutorResolution{
		Stage:           status,
		ResponsibleRole: role,
	}

	if role == responsibleOrchestrator {
		res.Executor = executorOrchestrator
		res.Delegate = false
		res.Degraded = false
		res.Hint = "esta etapa es responsabilidad del orquestador (gate o etapa determinista/terminal), no un fallback degradado."
		return res
	}

	candidates := manifestCandidatesForRole(role, manifest)
	if len(candidates) == 0 {
		res.Executor = executorOrchestrator
		res.Delegate = false
		res.Degraded = true
		res.Hint = fmt.Sprintf(
			"no hay subagente %s en el manifest de este proyecto; el orquestador suple esta etapa en MODO DEGRADADO. "+
				"Materializa el subagente con el grill de mneme-init para restaurar aislamiento y calidad especializada.",
			role,
		)
		return res
	}

	res.Executor = executorSubagent
	res.Delegate = true
	res.Degraded = false
	res.Subagents = candidates
	res.Hint = fmt.Sprintf("delega esta etapa a %d subagente(s) %s disponible(s) en el manifest.", len(candidates), role)
	return res
}

// manifestCandidatesForRole returns the manifest entries able to execute the
// stage owned by responsibleRole, applying SPEC-068 D4's implementer-class
// rule when responsibleRole is "implementer".
func manifestCandidatesForRole(responsibleRole string, manifest []ManifestEntry) []ExecutorSubagent {
	switch responsibleRole {
	case responsibleArchitect:
		return matchManifestRole(manifest, subagents.RoleArchitect)
	case responsibleQATester:
		return matchManifestRole(manifest, subagents.RoleQATester)
	case responsibleImplementer:
		return matchImplementerClass(manifest)
	default:
		return nil
	}
}

// matchManifestRole returns every manifest entry whose Role equals want, in
// manifest order.
func matchManifestRole(manifest []ManifestEntry, want subagents.Role) []ExecutorSubagent {
	var out []ExecutorSubagent
	for _, entry := range manifest {
		if entry.Role == want {
			out = append(out, ExecutorSubagent{Role: string(entry.Role), Path: entry.Path})
		}
	}
	return out
}

// matchImplementerClass returns every manifest entry belonging to the
// "implementer" class for the implementing stage: role is backend or
// frontend AND subagents.IsImplementer reports true for it (SPEC-068 D4).
// bug-hunter is deliberately excluded even though IsImplementer(bug-hunter)
// is also true — it belongs to the bug-fix flow, not the implementing stage
// of a feature spec, and including it would mislead the orchestrator into
// picking the wrong subagent.
func matchImplementerClass(manifest []ManifestEntry) []ExecutorSubagent {
	var out []ExecutorSubagent
	for _, entry := range manifest {
		if entry.Role != subagents.RoleBackend && entry.Role != subagents.RoleFrontend {
			continue
		}
		if !subagents.IsImplementer(entry.Role) {
			continue
		}
		out = append(out, ExecutorSubagent{Role: string(entry.Role), Path: entry.Path})
	}
	return out
}
