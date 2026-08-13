// Package service — this file implements the budget mechanism's I/O
// adapters (SPEC-118 EPIC-calidad S4 P8): symbolExtractorAdapter wraps
// codegraph's per-language Extractor registry into quality.SymbolExtractor
// — a PURE function of content bytes (V3 of the design), so it never opens
// any database and needs no injection of its own. runBudgetChecks itself
// (the row assembly, P9) and the QualityOption wiring (WithGraphFacts, P9)
// land in a later commit — this step is exclusively the two adapters plus
// the guardian proving the whole chain works against a REAL git repository
// and the REAL Go extractor (D20 point 1).
package service

import (
	"errors"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/quality"
)

// symbolExtractorAdapter implements quality.SymbolExtractor over
// codegraph.DetectLanguage + codegraph.GetExtractor(lang).Extract — never
// touching disk (content is handed in by the caller, quality.CollectSymbols,
// which read it via Git.FileAtRef). A path whose language is not detected,
// or for which no extractor is registered, produces an EMPTY result rather
// than an error — the SAME "no extractor for this language" silence
// Indexer.indexFile already treats as normal, never a systemic failure.
type symbolExtractorAdapter struct{}

// Symbols implements quality.SymbolExtractor.
func (symbolExtractorAdapter) Symbols(path string, content []byte) ([]quality.Symbol, []quality.SymbolRef, error) {
	lang := codegraph.DetectLanguage(path)
	if lang == "" {
		return nil, nil, nil
	}
	extractor := codegraph.GetExtractor(lang)
	if extractor == nil {
		return nil, nil, nil
	}

	result, err := extractor.Extract(path, content)
	// R7: ErrExtractorIncompatible is the ONE error this adapter
	// propagates — a toolchain present but unusable means the delta for
	// this file cannot be trusted at all, and a silently-empty result
	// would read as "nothing was created here", i.e. as budget respected.
	// Every OTHER extraction error is non-fatal per codegraph.Extractor's
	// own contract (it "must not return nil even when errors occur") —
	// this adapter still uses whatever partial result came back.
	if err != nil && errors.Is(err, codegraph.ErrExtractorIncompatible) {
		return nil, nil, err
	}
	if result == nil {
		return nil, nil, nil
	}

	syms := make([]quality.Symbol, 0, len(result.Nodes))
	for _, n := range result.Nodes {
		syms = append(syms, quality.Symbol{
			Name: n.Name, QualifiedName: n.QualifiedName, Kind: string(n.Kind),
			Exported: n.IsExported, Signature: n.Signature,
			StartLine: n.StartLine, EndLine: n.EndLine,
		})
	}

	refs := make([]quality.SymbolRef, 0, len(result.UnresolvedRefs))
	for _, r := range result.UnresolvedRefs {
		refs = append(refs, quality.SymbolRef{QualifiedName: r.ReferenceName})
	}

	return syms, refs, nil
}
