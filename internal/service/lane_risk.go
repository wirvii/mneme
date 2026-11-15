// Package service — this file reads the "Risk:" declaration the trivial-lane
// auditor contrasts with the diff (SPEC-184) and builds the remedy text a
// failed audit returns. It only reads from the store; it never writes.
package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/wirvii/mneme/internal/laneaudit"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/risk"
)

// Risk declaration sources, as LaneAuditResult.RiskSource reports them.
const (
	riskSourceDelivery  = "entrega"
	riskSourceRationale = "rationale"
)

// riskSource is one place a spec's risk declaration may come from. read
// reports found=false when the source has no declaration, so the next one
// is tried; found=true with a non-nil error means the declaration exists
// but is malformed.
type riskSource struct {
	name string
	read func(ctx context.Context, spec *model.Spec) (*risk.Declaration, bool, error)
}

// riskSources lists the declaration sources in precedence order: the
// delivery's prose first (the most recent claim about the change, the one
// worth testing), then the spec_quick rationale (which travels with the
// spec to other machines). SPEC-182's delivery contract block adds its own
// source at the FRONT of this list when it lands; nothing else changes.
func (svc *SDDService) riskSources() []riskSource {
	return []riskSource{
		{name: riskSourceDelivery, read: svc.deliveryProseRisk},
		{name: riskSourceRationale, read: svc.rationaleRisk},
	}
}

// declaredRisk returns the first declaration any source holds, its source
// name and, when it is malformed, the parse error (which the auditor turns
// into a breach). It never grants anything: the declaration is only
// contrasted with the diff. No declaration anywhere returns nil, "", nil.
func (svc *SDDService) declaredRisk(ctx context.Context, spec *model.Spec) (*risk.Declaration, string, error) {
	for _, src := range svc.riskSources() {
		decl, found, err := src.read(ctx, spec)
		if found {
			return decl, src.name, err
		}
	}
	return nil, "", nil
}

// deliveryProseRisk reads the last unfenced "Risk:" line of the entrega
// section of the spec's tarea.md. A missing or malformed tarea.md is not
// an audit error: this source simply has nothing and the next one is
// tried.
func (svc *SDDService) deliveryProseRisk(_ context.Context, spec *model.Spec) (*risk.Declaration, bool, error) {
	if svc.config == nil {
		return nil, false, nil
	}
	path, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, model.SpecDocKindTask)
	if err != nil {
		return nil, false, nil
	}
	text, ok, err := readTaskSectionPresent(path, model.TaskSectionDelivery)
	if err != nil {
		log.Printf("service: lane audit: read tarea.md for %s: %v", spec.ID, err)
		return nil, false, nil
	}
	if !ok {
		return nil, false, nil
	}
	return findRisk(text)
}

// rationaleRisk reads the "Risk:" line of the most recent history entry
// that moved the spec into rationale (spec_quick). A history read failure
// is logged and treated as "no declaration here", which fails the audit
// closed with "risk not declared" rather than passing it.
func (svc *SDDService) rationaleRisk(ctx context.Context, spec *model.Spec) (*risk.Declaration, bool, error) {
	history, err := svc.store.GetSpecHistory(ctx, spec.ID)
	if err != nil {
		log.Printf("service: lane audit: read history for %s: %v", spec.ID, err)
		return nil, false, nil
	}
	reason, ok := "", false
	for _, h := range history {
		if h.ToStatus == model.SpecStatusRationale {
			reason, ok = h.Reason, true
		}
	}
	if !ok {
		return nil, false, nil
	}
	return findRisk(reason)
}

// findRisk adapts risk.FindDeclaration to a riskSource result.
func findRisk(text string) (*risk.Declaration, bool, error) {
	decl, found, err := risk.FindDeclaration(text)
	if !found {
		return nil, false, nil
	}
	return &decl, true, err
}

// formatDeclared renders a declaration as LaneAuditResult.RiskDeclared
// does: "none", "item 1" or "item 1, 5"; "" for no declaration or a
// malformed one.
func formatDeclared(decl *risk.Declaration, declErr error) string {
	switch {
	case decl == nil || declErr != nil:
		return ""
	case len(decl.Items) > 0:
		return "item " + joinRiskItems(decl.Items)
	case decl.None:
		return "none"
	}
	return ""
}

// joinRiskItems renders items as "1, 5", in the order given.
func joinRiskItems(items []risk.Item) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = fmt.Sprintf("%d", int(item))
	}
	return strings.Join(parts, ", ")
}

// laneRemedy builds the one plain-English sentence a failed trivial audit
// returns as its way out (SPEC-184 D5). It always names the MCP tool and
// the CLI command, and it never proposes lane_override first: a risk item
// means the change is standard, and override is only for a false positive.
// A passing audit has no remedy.
func laneRemedy(result *model.LaneAuditResult, breaches []string, specID string) string {
	if result == nil || result.Passed {
		return ""
	}
	reclassify := fmt.Sprintf("lane_reclassify %s standard (mneme lane reclassify %s standard --by <you>)", specID, specID)

	if items := remedyItems(result); len(items) > 0 {
		noun, verb := "Risk item", "makes"
		if len(items) > 1 {
			noun, verb = "Risk items", "make"
		}
		return fmt.Sprintf("%s %s (see mneme-flow/references/risk.md) %s this change standard. Orchestrator: %s. "+
			"Only if this is a false positive, such as a generated file, the orchestrator may ask the person for lane_override.",
			noun, joinRiskItems(items), verb, reclassify)
	}

	var undeclared, outOfScope bool
	for _, b := range breaches {
		switch {
		case strings.HasPrefix(b, "risk not declared"), strings.HasPrefix(b, "risk declaration malformed"):
			undeclared = true
		case strings.HasPrefix(b, "out of scope: "):
			outOfScope = true
		}
	}
	var parts []string
	if undeclared {
		parts = append(parts, "Declare the risk: the implementer or the orchestrator writes \"Risk: none\" in tarea.md section entrega "+
			"(spec_doc_write kind task section entrega), then run lane_audit again; if the change does touch a risk item, "+
			"the orchestrator runs "+reclassify+".")
	}
	if outOfScope {
		parts = append(parts, "Files outside the declared scope changed. Revert them (spec_reject back to implementing) "+
			"or the orchestrator runs "+reclassify+".")
	}
	return strings.Join(parts, " ")
}

// remedyItems is the union of the items the diff revealed and the items
// the declaration named, sorted and without duplicates.
func remedyItems(result *model.LaneAuditResult) []risk.Item {
	seen := make(map[risk.Item]bool)
	for _, n := range result.RiskItems {
		seen[risk.Item(n)] = true
	}
	if result.RiskDeclared != "" && result.RiskDeclared != "none" {
		if decl, err := risk.ParseLine("Risk: " + result.RiskDeclared); err == nil {
			for _, item := range decl.Items {
				seen[item] = true
			}
		}
	}
	var out []risk.Item
	for _, item := range risk.Items() {
		if seen[item] {
			out = append(out, item)
		}
	}
	return out
}

// laneRiskTable is the table the auditor applies: the configured one, or
// risk.DefaultTable() when there is no configuration or it fails — never
// fewer patterns than the defaults.
func (svc *SDDService) laneRiskTable() risk.Table {
	if svc.config == nil {
		return risk.DefaultTable()
	}
	table, err := svc.config.LaneRiskTable()
	if err != nil {
		return risk.DefaultTable()
	}
	return table
}

// laneDetectedItems returns the items the diff revealed — by path and by
// exported symbol — sorted and without duplicates, as LaneAuditResult.
// RiskItems reports them.
func laneDetectedItems(paths []string, table risk.Table, publicSymbols int) []int {
	seen := make(map[risk.Item]bool)
	for _, p := range paths {
		for _, item := range laneaudit.RiskItemsForPath(p, table) {
			seen[item] = true
		}
	}
	if publicSymbols > 0 {
		seen[risk.ItemContracts] = true
	}
	var out []int
	for _, item := range risk.Items() {
		if seen[item] {
			out = append(out, int(item))
		}
	}
	return out
}
