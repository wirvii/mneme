package subagents

import (
	"fmt"
	"strings"
)

// DoctorEntry is the runtime-neutral manifest data needed by the shared
// doctor. Keeping it here prevents the CLI and MCP adapters from maintaining
// separate copies of the same diagnostic policy.
type DoctorEntry struct {
	Role          string
	Archetype     string
	Areas         []string
	AreasComplete bool
	Path          string
	Checksum      string
	Version       int
}

// DoctorFinding is one shared manifest diagnosis. Adapters may add transport
// fields such as the role, but must not reimplement the decision itself.
type DoctorFinding struct {
	Kind   string
	Detail string
}

// DiagnoseManifestEntry evaluates every manifest invariant shared by the CLI
// doctor and MCP manifest listing.
func DiagnoseManifestEntry(e DoctorEntry, root string, fileExists func(string) bool, actualChecksum func(string) (string, bool), readContent func(string) (string, bool)) []DoctorFinding {
	var findings []DoctorFinding
	archetype := e.Archetype
	if archetype == "" {
		archetype = e.Role
	}
	add := func(kind, detail string) { findings = append(findings, DoctorFinding{Kind: kind, Detail: detail}) }

	roleArchetype := Role(archetype)
	if _, known := PermissionTable[roleArchetype]; !known {
		add("unknown_role", fmt.Sprintf("archetype/role %q no está en PermissionTable — no es implementador para el hook, su área está desprotegida", archetype))
	} else if IsImplementer(roleArchetype) && len(e.Areas) == 0 {
		add("degenerate_areas", "rol implementador sin áreas declaradas (degenerado)")
	}
	if e.Archetype == "" {
		add("archetype_missing", "archetype ausente — backfill mecánico disponible (mneme subagents doctor --fix)")
	}
	if !e.AreasComplete {
		add("not_verified", "areas_complete ausente o false — no verificado (re-grillar para certificar)")
	}
	if e.Version < AgentFixedVersion {
		add("stale_agent_fixed", fmt.Sprintf("agent-fixed block en v%d, la versión actual es v%d — regenerar con `mneme subagents regen --role %s`", e.Version, AgentFixedVersion, e.Role))
	}

	if e.Path != "" {
		if _, _, ok := ResolveManifestPath(e.Path, root); !ok {
			add("foreign_path", fmt.Sprintf("path %q está fuera de la raíz del proyecto o es foráneo-de-otro-SO — `regen` la omite, nunca la toca (SPEC-089)", e.Path))
		} else if !fileExists(e.Path) {
			add("orphan_path", fmt.Sprintf("path %q no existe en disco (huérfano)", e.Path))
		} else {
			if e.Checksum != "" {
				if actual, ok := actualChecksum(e.Path); ok && actual != e.Checksum {
					add("drift", "checksum en disco no coincide con el manifest (drift)")
				}
			}
			for _, leak := range DetectLayer23Leaks(e.Path, readContent) {
				add("lifecycle_in_layer23", fmt.Sprintf("fuga de capa 1 (%s) en la región de capa 2/3: %q, línea %d — re-grillar el rol para limpiarlo", leak.Kind, leak.Token, leak.Line))
			}
		}
	}

	for _, area := range e.Areas {
		trimmed := strings.Trim(strings.TrimSpace(area), "/")
		trimmed = strings.TrimPrefix(trimmed, "./")
		if trimmed == "" || trimmed == "." || isDoctorGlob(trimmed) {
			continue
		}
		add("bare_dir_ok", fmt.Sprintf("area %q es un directorio desnudo — areaMatches ya lo resuelve, sano", area))
	}
	return findings
}

func isDoctorGlob(area string) bool {
	return strings.ContainsAny(area, "*?[")
}
