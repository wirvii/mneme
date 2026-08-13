// Package service — this file implements graphFactsAdapter (SPEC-118
// EPIC-calidad S4 P8): the production implementation of
// quality.GraphFacts, backed by a *codegraph.Store (query methods) and a
// *codegraph.QueryEngine (BFS traversal). internal/quality never imports
// internal/codegraph directly (it is a leaf) — this file is the ONE
// translation point, mirroring how S1/S2/S3 already translate model.*
// against quality's own leaf types.
package service

import (
	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/quality"
)

// graphFactsAdapter implements quality.GraphFacts over an ALREADY-OPEN code
// graph store — it never opens or closes the underlying database itself;
// the caller (initQualityService, P14) owns that lifecycle, exactly like
// CodeGraphService already does for the codegraph_* MCP tools.
type graphFactsAdapter struct {
	store *codegraph.Store
	query *codegraph.QueryEngine
}

// newGraphFactsAdapter constructs a graphFactsAdapter over store.
func newGraphFactsAdapter(store *codegraph.Store) *graphFactsAdapter {
	return &graphFactsAdapter{store: store, query: codegraph.NewQueryEngine(store)}
}

// resolveNodeID finds the ONE graph node ref identifies. When ref.File is
// non-empty it looks up that file's own symbols and matches by qualified
// name (the exact case for a symbol quality.CollectSymbols itself
// produced); when ref.File is empty it resolves by NAME ACROSS THE WHOLE
// GRAPH via Store.FindNodesByName — the "dead" detection's own case (D8
// #5), where only a removed reference's bare name is known, never its
// defining file. An ambiguous (>=2 candidates) or absent name-based lookup
// resolves to "not found" rather than guessing — the caller then reports
// "no incoming edges" for a symbol mneme cannot pin down, which is the
// conservative (never-a-false-negative-on-a-real-orphan) direction to err
// in.
func (a *graphFactsAdapter) resolveNodeID(ref quality.SymbolRef) (string, bool, error) {
	if ref.File != "" {
		nodes, err := a.store.GetNodesByFilePath(ref.File)
		if err != nil {
			return "", false, err
		}
		for _, n := range nodes {
			if n.QualifiedName == ref.QualifiedName {
				return n.ID, true, nil
			}
		}
		return "", false, nil
	}

	nodes, err := a.store.FindNodesByName(ref.QualifiedName)
	if err != nil {
		return "", false, err
	}
	if len(nodes) != 1 {
		return "", false, nil
	}
	return nodes[0].ID, true, nil
}

// edgesToRefs translates a slice of codegraph.Edge (Source -> the caller's
// own node) into quality.SymbolRef, resolving each Source's own Node record
// for its QualifiedName/FilePath. An edge whose source node is gone (should
// not happen against a consistent graph, but the store's own contract
// allows GetNode to return nil) is skipped rather than erroring.
func (a *graphFactsAdapter) edgesToRefs(edges []codegraph.Edge) ([]quality.SymbolRef, error) {
	refs := make([]quality.SymbolRef, 0, len(edges))
	for _, e := range edges {
		n, err := a.store.GetNode(e.Source)
		if err != nil {
			return nil, err
		}
		if n == nil {
			continue
		}
		refs = append(refs, quality.SymbolRef{QualifiedName: n.QualifiedName, File: n.FilePath})
	}
	return refs, nil
}

// IncomingEdges implements quality.GraphFacts: every edge of any kind
// targeting ref.
func (a *graphFactsAdapter) IncomingEdges(ref quality.SymbolRef) ([]quality.SymbolRef, error) {
	id, ok, err := a.resolveNodeID(ref)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	edges, err := a.store.GetEdgesTo(id, "")
	if err != nil {
		return nil, err
	}
	return a.edgesToRefs(edges)
}

// IncomingCalls implements quality.GraphFacts: only "calls"-kind edges
// targeting ref.
func (a *graphFactsAdapter) IncomingCalls(ref quality.SymbolRef) ([]quality.SymbolRef, error) {
	id, ok, err := a.resolveNodeID(ref)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	edges, err := a.store.GetEdgesTo(id, string(codegraph.EdgeKindCalls))
	if err != nil {
		return nil, err
	}
	return a.edgesToRefs(edges)
}

// TestReachable implements quality.GraphFacts via QueryEngine.Callers'
// transitive BFS: ref is reachable from a test file within depth hops when
// at least one caller (at any hop up to depth) lives in a file matching
// testGlobs.
func (a *graphFactsAdapter) TestReachable(ref quality.SymbolRef, depth int, testGlobs []string) (bool, error) {
	id, ok, err := a.resolveNodeID(ref)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	callers, err := a.query.Callers(id, depth, 0)
	if err != nil {
		return false, err
	}
	for _, c := range callers {
		if quality.MatchGlobs(c.FilePath, testGlobs) {
			return true, nil
		}
	}
	return false, nil
}

// SameNameAndSignature implements quality.GraphFacts: every OTHER node
// sharing s's Name and normalised Signature, found via
// Store.FindNodesByName — the "reinvention" detection's own raw material
// (D8 #7); the directory/method filtering is DetectGraph's own job
// (internal/quality), not this adapter's.
func (a *graphFactsAdapter) SameNameAndSignature(s quality.Symbol) ([]quality.SymbolRef, error) {
	nodes, err := a.store.FindNodesByName(s.Name)
	if err != nil {
		return nil, err
	}
	var out []quality.SymbolRef
	for _, n := range nodes {
		if n.FilePath == s.File && n.QualifiedName == s.QualifiedName {
			continue
		}
		if n.Signature != s.Signature {
			continue
		}
		out = append(out, quality.SymbolRef{QualifiedName: n.QualifiedName, File: n.FilePath})
	}
	return out, nil
}

// IndexedContentHash implements quality.GraphFacts via Store.GetFile — the
// exact ContentHash comparison D5 requires (never a "last indexed" stamp,
// V9).
func (a *graphFactsAdapter) IndexedContentHash(path string) (string, bool, error) {
	rec, err := a.store.GetFile(path)
	if err != nil {
		return "", false, err
	}
	if rec == nil {
		return "", false, nil
	}
	return rec.ContentHash, true, nil
}
