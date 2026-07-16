// SPEC-086 D11: "mneme subagents doctor" diagnoses the current project's
// subagent manifest without ever inventing certainty it does not have.
//
// Two things it deliberately does NOT do, both load-bearing decisions from
// the design (D11):
//   - It NEVER backfills areas_complete. That flag exists to represent an
//     explicit human answer to the mneme-init grill's completeness
//     question; fabricating it here would fabricate the confidence the
//     flag is supposed to certify.
//   - It NEVER rewrites a bare-directory area (e.g. "apps/web-ui" instead
//     of "apps/web-ui/**"). areaMatches (SPEC-084 D1/D2) already resolves
//     bare directories correctly — rewriting them would be pure churn with
//     zero behavior change, would dirty every existing manifest, and would
//     create the illusion that the compatibility net can be retired. Doctor
//     reports them as healthy.
//
// --fix is narrowly scoped to the one truly mechanical backfill: an entry
// whose Role is one of the six built-in archetypes and whose Archetype
// field is empty gets Archetype = Role (the compat assumption every
// pre-SPEC-086 manifest already relies on implicitly).
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/subagents"
)

// doctorFindingKind classifies a single diagnostic finding.
type doctorFindingKind string

const (
	doctorKindUnknownRole      doctorFindingKind = "unknown_role"
	doctorKindDegenerateAreas  doctorFindingKind = "degenerate_areas"
	doctorKindArchetypeMissing doctorFindingKind = "archetype_missing"
	doctorKindNotVerified      doctorFindingKind = "not_verified"
	doctorKindOrphanPath       doctorFindingKind = "orphan_path"
	doctorKindDrift            doctorFindingKind = "drift"
	doctorKindBareDirOK        doctorFindingKind = "bare_dir_ok"
)

// doctorFinding is one diagnostic observation about a single manifest entry.
type doctorFinding struct {
	Role   string            `json:"role"`
	Kind   doctorFindingKind `json:"kind"`
	Detail string            `json:"detail"`
}

// actionable reports whether kind represents something a human should act
// on, as opposed to purely informational output (bare-dir areas).
func (k doctorFindingKind) actionable() bool {
	return k != doctorKindBareDirOK
}

// diagnoseManifestEntry runs every SPEC-086 D11 check against a single
// manifest entry. fileExists/actualChecksum are injected so the function is
// testable without touching the real filesystem.
func diagnoseManifestEntry(e service.ManifestEntry, fileExists func(string) bool, actualChecksum func(string) (string, bool)) []doctorFinding {
	var findings []doctorFinding
	role := string(e.Role)
	archetype := e.EffectiveArchetype()

	if _, known := subagents.PermissionTable[archetype]; !known {
		findings = append(findings, doctorFinding{
			Role: role, Kind: doctorKindUnknownRole,
			Detail: fmt.Sprintf("archetype/role %q no está en PermissionTable — no es implementador para el hook, su área está desprotegida", archetype),
		})
	} else if subagents.IsImplementer(archetype) && len(e.Areas) == 0 {
		findings = append(findings, doctorFinding{
			Role: role, Kind: doctorKindDegenerateAreas,
			Detail: "rol implementador sin áreas declaradas (degenerado)",
		})
	}

	if e.Archetype == "" {
		findings = append(findings, doctorFinding{
			Role: role, Kind: doctorKindArchetypeMissing,
			Detail: "archetype ausente — backfill mecánico disponible (--fix)",
		})
	}
	if !e.AreasComplete {
		findings = append(findings, doctorFinding{
			Role: role, Kind: doctorKindNotVerified,
			Detail: "areas_complete ausente o false — no verificado (re-grillar para certificar)",
		})
	}

	if e.Path != "" {
		if !fileExists(e.Path) {
			findings = append(findings, doctorFinding{
				Role: role, Kind: doctorKindOrphanPath,
				Detail: fmt.Sprintf("path %q no existe en disco (huérfano)", e.Path),
			})
		} else if e.Checksum != "" {
			if actual, ok := actualChecksum(e.Path); ok && actual != e.Checksum {
				findings = append(findings, doctorFinding{
					Role: role, Kind: doctorKindDrift,
					Detail: "checksum en disco no coincide con el manifest (drift)",
				})
			}
		}
	}

	for _, area := range e.Areas {
		cleaned, ignore := cleanArea(area)
		if ignore {
			continue
		}
		if cleaned == area && !isGlobLike(area) {
			findings = append(findings, doctorFinding{
				Role: role, Kind: doctorKindBareDirOK,
				Detail: fmt.Sprintf("area %q es un directorio desnudo — areaMatches ya lo resuelve, sano", area),
			})
		}
	}

	return findings
}

// isGlobLike reports whether area already contains glob metacharacters
// (already the shape "internal/**" rather than a bare "internal" directory).
func isGlobLike(area string) bool {
	for _, r := range area {
		switch r {
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// realFileExists/realChecksum are the production fileExists/actualChecksum
// implementations diagnoseManifestEntry is wired with outside tests.
func realFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func realChecksum(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

// backfillArchetypes returns a copy of entries with Archetype set to Role
// for every entry whose Archetype is empty AND whose Role is a recognised
// built-in archetype (the only case this backfill can do mechanically,
// without guessing). changed reports whether anything was modified.
func backfillArchetypes(entries []service.ManifestEntry) (out []service.ManifestEntry, changed bool) {
	out = make([]service.ManifestEntry, len(entries))
	copy(out, entries)
	for i, e := range out {
		if e.Archetype != "" {
			continue
		}
		if _, known := subagents.PermissionTable[e.Role]; !known {
			continue
		}
		out[i].Archetype = e.Role
		changed = true
	}
	return out, changed
}

func newSubagentsDoctorCmd() *cobra.Command {
	var (
		flagFix  bool
		flagJSON bool
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the current project's subagent manifest (report-only by default)",
		Long: `Runs SPEC-086 D11's checks against every entry in the current project's
subagent manifest: an implementer role with no declared areas (degenerate),
areas_complete absent (not verified), archetype absent (backfill available
via --fix), a checksum that no longer matches the file on disk (drift), a
path that no longer exists (orphan), and a role/archetype not recognised by
subagents.PermissionTable (unknown — its area is unprotected by the hook).

A bare-directory area (e.g. "apps/web-ui" instead of "apps/web-ui/**") is
reported as healthy, not rewritten — areaMatches already resolves it.

--fix ONLY backfills the archetype field for entries where Role is one of
the six built-in archetypes. It NEVER touches areas_complete: that flag
represents an explicit human answer from the mneme-init grill, and doctor
must never fabricate the confidence it certifies.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initSubagentService()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx := cmd.Context()
			entries, err := svc.ReadManifest(ctx, flagProject)
			if err != nil {
				return fmt.Errorf("subagents doctor: %w", err)
			}

			if flagFix {
				fixed, changed := backfillArchetypes(entries)
				if changed {
					if _, err := svc.SaveManifest(ctx, flagProject, fixed); err != nil {
						return fmt.Errorf("subagents doctor: --fix: save manifest: %w", err)
					}
				}
				entries = fixed
			}

			var all []doctorFinding
			for _, e := range entries {
				all = append(all, diagnoseManifestEntry(e, realFileExists, realChecksum)...)
			}
			sort.SliceStable(all, func(i, j int) bool {
				if all[i].Role != all[j].Role {
					return all[i].Role < all[j].Role
				}
				return all[i].Kind < all[j].Kind
			})

			if flagJSON {
				return printJSON(cmd.OutOrStdout(), all)
			}

			out := cmd.OutOrStdout()
			if len(entries) == 0 {
				fmt.Fprintln(out, "No subagent manifest for this project yet — nothing to diagnose.")
				return nil
			}
			actionableCount := 0
			for _, f := range all {
				marker := "info"
				if f.Kind.actionable() {
					marker = "!!"
					actionableCount++
				}
				fmt.Fprintf(out, "[%s] %-14s %s: %s\n", marker, f.Kind, f.Role, f.Detail)
			}
			if actionableCount == 0 {
				fmt.Fprintln(out, "No actionable findings.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagFix, "fix", false, "Backfill missing archetype fields (mechanical only — never areas_complete)")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output findings as JSON")
	return cmd
}
