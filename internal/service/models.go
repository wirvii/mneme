package service

import (
	"context"
	"fmt"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/install"
	"github.com/juanftp/mneme/internal/model"
)

// ModelsService manages per-agent model configuration. It provides
// list/set/reset operations that read from and write to the [models] section
// of ~/.mneme/config.toml. Known agents are derived from the bundled agent
// assets (install.BundledAgentNames) — the canonical source.
type ModelsService struct {
	configPath string
}

// NewModelsService constructs a ModelsService that operates on the config file
// at configPath (typically config.DefaultPath()).
func NewModelsService(configPath string) *ModelsService {
	return &ModelsService{configPath: configPath}
}

// ModelInfo describes the resolved model for a single agent.
type ModelInfo struct {
	// Agent is the agent name (e.g. "architect").
	Agent string `json:"agent"`

	// Model is the effective model alias (e.g. "opus" or "sonnet").
	Model string `json:"model"`

	// Origin is "override" when the value came from config, or "default"
	// when the built-in default is in use.
	Origin string `json:"origin"`
}

// ModelListResponse is the response for List.
type ModelListResponse struct {
	// Agents holds one entry per bundled agent in alphabetical order.
	Agents []ModelInfo `json:"agents"`
}

// ModelSetRequest is the request for Set.
type ModelSetRequest struct {
	// Agent is the agent name to configure.
	Agent string `json:"agent"`

	// Model is the model alias to assign (non-empty).
	Model string `json:"model"`
}

// ModelSetResponse is the response for Set.
type ModelSetResponse struct {
	// Agent that was updated.
	Agent string `json:"agent"`

	// Model that was set.
	Model string `json:"model"`

	// Warning is non-empty when the alias is not in knownAliases.
	Warning string `json:"warning,omitempty"`

	// Hint instructs the caller to reinstall to apply the change.
	Hint string `json:"hint,omitempty"`
}

// ModelResetRequest is the request for Reset.
type ModelResetRequest struct {
	// Agent is the agent to reset. When empty, all overrides are cleared.
	Agent string `json:"agent,omitempty"`
}

// ModelResetResponse is the response for Reset.
type ModelResetResponse struct {
	// Reset lists the agent names whose overrides were removed.
	Reset []string `json:"reset"`

	// Hint instructs the caller to reinstall to apply the change.
	Hint string `json:"hint,omitempty"`
}

const modelHint = "run `mneme install claude-code` to apply"

// List returns the effective model for every bundled agent, including whether
// each value comes from a config override or the built-in default.
func (s *ModelsService) List(ctx context.Context) (ModelListResponse, error) {
	_ = ctx

	bundled, err := install.BundledAgentNames()
	if err != nil {
		return ModelListResponse{}, fmt.Errorf("service: models list: bundled agents: %w", err)
	}

	cfg, err := config.Load(s.configPath)
	if err != nil {
		return ModelListResponse{}, fmt.Errorf("service: models list: load config: %w", err)
	}

	overrides := cfg.Models.Overrides
	effective := install.ResolveEffectiveModels(overrides)

	agents := make([]ModelInfo, 0, len(bundled))
	for _, name := range bundled {
		origin := "default"
		if v, ok := overrides[name]; ok && v != "" {
			origin = "override"
		}
		agents = append(agents, ModelInfo{
			Agent:  name,
			Model:  effective[name],
			Origin: origin,
		})
	}

	return ModelListResponse{Agents: agents}, nil
}

// Set writes a model override for a single agent to the config file.
// Returns ErrUnknownAgent when agent is not a bundled agent.
// Returns ErrInvalidModel when model is empty.
// Returns a warning (not an error) when the alias is not in knownAliases.
func (s *ModelsService) Set(ctx context.Context, req ModelSetRequest) (ModelSetResponse, error) {
	_ = ctx

	// Validate agent.
	bundled, err := install.BundledAgentNames()
	if err != nil {
		return ModelSetResponse{}, fmt.Errorf("service: models set: bundled agents: %w", err)
	}
	known := make(map[string]bool, len(bundled))
	for _, n := range bundled {
		known[n] = true
	}
	if !known[req.Agent] {
		return ModelSetResponse{}, fmt.Errorf("service: models set: %w: %q", model.ErrUnknownAgent, req.Agent)
	}

	// Validate model (must be non-empty).
	if req.Model == "" {
		return ModelSetResponse{}, fmt.Errorf("service: models set: %w", model.ErrInvalidModel)
	}

	// Load current overrides.
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return ModelSetResponse{}, fmt.Errorf("service: models set: load config: %w", err)
	}
	overrides := make(map[string]string)
	for k, v := range cfg.Models.Overrides {
		overrides[k] = v
	}
	overrides[req.Agent] = req.Model

	if err := config.SetModelsOverrides(s.configPath, overrides); err != nil {
		return ModelSetResponse{}, fmt.Errorf("service: models set: write config: %w", err)
	}

	resp := ModelSetResponse{
		Agent: req.Agent,
		Model: req.Model,
		Hint:  modelHint,
	}
	if !install.IsKnownAlias(req.Model) {
		resp.Warning = fmt.Sprintf("unknown model alias %q; known aliases: opus, sonnet, haiku, inherit — verify the alias is valid for your agent", req.Model)
	}
	return resp, nil
}

// Reset removes the model override for agent (or all overrides when agent is empty).
func (s *ModelsService) Reset(ctx context.Context, req ModelResetRequest) (ModelResetResponse, error) {
	_ = ctx

	// Load current overrides.
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return ModelResetResponse{}, fmt.Errorf("service: models reset: load config: %w", err)
	}
	overrides := make(map[string]string)
	for k, v := range cfg.Models.Overrides {
		overrides[k] = v
	}

	var reset []string

	if req.Agent != "" {
		// Single-agent reset — validate first.
		bundled, bundledErr := install.BundledAgentNames()
		if bundledErr != nil {
			return ModelResetResponse{}, fmt.Errorf("service: models reset: bundled agents: %w", bundledErr)
		}
		known := make(map[string]bool, len(bundled))
		for _, n := range bundled {
			known[n] = true
		}
		if !known[req.Agent] {
			return ModelResetResponse{}, fmt.Errorf("service: models reset: %w: %q", model.ErrUnknownAgent, req.Agent)
		}
		if _, ok := overrides[req.Agent]; ok {
			delete(overrides, req.Agent)
			reset = append(reset, req.Agent)
		}
	} else {
		// Reset all.
		for k := range overrides {
			reset = append(reset, k)
		}
		overrides = map[string]string{}
	}

	if err := config.SetModelsOverrides(s.configPath, overrides); err != nil {
		return ModelResetResponse{}, fmt.Errorf("service: models reset: write config: %w", err)
	}

	hint := ""
	if len(reset) > 0 {
		hint = modelHint
	}
	return ModelResetResponse{Reset: reset, Hint: hint}, nil
}

