package config

// DefaultReviewDays maps a memory type to the number of days after which it is
// due for review (SPEC-167 D4). A value of 0 means the type never expires.
// The keys are plain strings so this package keeps not importing model.
var DefaultReviewDays = map[string]int{
	"discovery":       60,
	"config":          60,
	"decision":        120,
	"architecture":    120,
	"pattern":         120,
	"bugfix":          180,
	"rule":            180,
	"preference":      0,
	"convention":      0,
	"session_summary": 0,
	"synthesis":       0,
}

// MemoryConfig tunes memory behaviour that is not retrieval or decay: today,
// how long each memory type stays trustworthy before an agent is asked to
// re-check it.
type MemoryConfig struct {
	// ReviewDays overrides DefaultReviewDays per memory type
	// ([memory.review_days] in config.toml). Only the types listed here are
	// overridden; the rest keep their defaults, so a partial table never erases
	// the others. A value <= 0 disables review for that type. Keys that are not
	// a known type are ignored.
	ReviewDays map[string]int `toml:"review_days"`
}

// ReviewDaysFor returns the review deadline, in days, for memType: the user's
// value when the type is a known one and configured, otherwise the default.
// An unknown type, or any value <= 0, means "never due" and yields 0.
func (c MemoryConfig) ReviewDaysFor(memType string) int {
	def, known := DefaultReviewDays[memType]
	if !known {
		return 0
	}
	if days, ok := c.ReviewDays[memType]; ok {
		return max(days, 0)
	}
	return def
}
