package model

// DefaultImportance maps each MemoryType to its initial importance score (0.0–1.0).
// These values encode domain knowledge about how critical each type of memory
// typically is. Architecture and decisions are expensive to rediscover; session
// summaries are transient and lose value quickly. The service layer uses these
// when the caller does not specify an explicit importance in SaveRequest.
var DefaultImportance = map[MemoryType]float64{
	TypeArchitecture:   0.9,
	TypeDecision:       0.85,
	TypeConvention:     0.8,
	TypePattern:        0.75,
	TypeBugfix:         0.7,
	TypeDiscovery:      0.6,
	TypeConfig:         0.5,
	TypePreference:     0.5,
	TypeSessionSummary: 0.4,
}

// DefaultDecayRate maps each MemoryType to its per-day decay multiplier.
// A lower rate means the memory retains its importance longer without access.
// Structural knowledge (architecture, decisions, conventions) decays slowly
// because it remains valid for months; session summaries decay fast because
// they become irrelevant after a few days.
var DefaultDecayRate = map[MemoryType]float64{
	TypeArchitecture:   0.005,
	TypeDecision:       0.005,
	TypeConvention:     0.005,
	TypePattern:        0.01,
	TypePreference:     0.01,
	TypeBugfix:         0.02,
	TypeDiscovery:      0.02,
	TypeConfig:         0.02,
	TypeSessionSummary: 0.05,
}

// DefaultConfidence is the confidence score assigned to a memory when the
// saving agent does not specify one. 0.8 reflects "pretty sure but not
// verified" — a reasonable baseline for agent-generated knowledge.
const DefaultConfidence = 0.8
