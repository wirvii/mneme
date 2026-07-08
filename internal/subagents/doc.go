// Package subagents provides the building blocks for generating and
// validating per-project Claude Code subagent profiles (SPEC-052, the
// agnostic-agents EPIC). It ports the project-root/stack fingerprinting
// pattern from gentle-ai's internal/components/sdd package and combines it
// with mneme's own managed-block and frontmatter leaves to compose the
// three-layer subagent profile described in SPEC-052 §3.1/§4:
//
//   - Layer 1 ("agent-fixed"): mneme-authored, Go-generated content — the
//     codegraph-search-order policy and the mneme SDD/memory integration
//     protocol. Never written by an LLM. Lives inside a managed block
//     (marker "agent-fixed") so regeneration is idempotent.
//   - Layer 2/3 (role/area body): repo- and role-specific content, either
//     hand-authored or LLM-drafted via a GenerationEngine. Always preserved
//     verbatim across regenerations of layer 1.
//
// This package is a leaf: it imports only the standard library plus, where
// useful, internal/model, internal/managedblock, and internal/frontmatter —
// never internal/store, internal/db, or internal/service. Orchestration (the
// SubagentService, MCP tools, persistence) is built on top of this package
// in later sub-specs (SS-3/SS-4) and must never be imported back into it.
package subagents
