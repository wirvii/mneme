package install

import (
	"io/fs"
	"strings"
)

// defaultAgentModels holds the built-in model alias per bundled agent.
// These defaults represent the cost/quality trade-offs described in docs/models.md:
// the architect uses opus because its output (the spec) propagates to all other
// agents; implementers use sonnet for cost efficiency.
var defaultAgentModels = map[string]string{
	"architect":     "opus",
	"backend":       "sonnet",
	"frontend":      "sonnet",
	"qa-tester":     "sonnet",
	"bug-hunter":    "sonnet",
	"diagnostician": "sonnet",
}

// knownAliases is the set of model alias strings that Claude Code recognises.
// Any non-empty string is accepted by the model system (open-ended), but
// aliases not in this set trigger a warning rather than an error.
var knownAliases = map[string]bool{
	"opus":    true,
	"sonnet":  true,
	"haiku":   true,
	"inherit": true,
}

// BundledAgentNames returns the names of all agents embedded under
// assets/agents. Each name is the filename without the .md extension
// (e.g. "architect", "backend"). This is the canonical source of agent
// names used by defaults, service validation, and CLI commands.
func BundledAgentNames() ([]string, error) {
	entries, err := builtinAgents.ReadDir("assets/agents")
	if err != nil {
		return nil, fs.ErrNotExist
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		name = strings.TrimSuffix(name, ".md")
		names = append(names, name)
	}
	return names, nil
}

// DefaultModelFor returns the default model alias for the named agent, or an
// empty string when the agent is not in the built-in defaults map.
func DefaultModelFor(agent string) string {
	return defaultAgentModels[agent]
}

// IsKnownAlias reports whether alias is one of the recognised model alias
// strings. Unknown aliases are accepted but trigger a warning in Set.
func IsKnownAlias(alias string) bool {
	return knownAliases[alias]
}

// ResolveEffectiveModels builds a map of agent → effective model for every
// bundled agent. For each agent, the override wins if the override map
// contains a non-empty value for that agent; otherwise the default is used.
// BundledAgentNames determines the full set of agents in the result.
func ResolveEffectiveModels(overrides map[string]string) map[string]string {
	names, _ := BundledAgentNames()
	result := make(map[string]string, len(names))
	for _, name := range names {
		if ov, ok := overrides[name]; ok && ov != "" {
			result[name] = ov
		} else {
			result[name] = defaultAgentModels[name]
		}
	}
	return result
}
