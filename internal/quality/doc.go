// Package quality implements the deterministic core of mneme's quality
// mechanism (SPEC-115, EPIC-calidad S1): parsing a repository's
// .mneme/quality.toml constitution, running the gates it declares without a
// shell, and deriving a verdict from the resulting checks.
//
// It is a leaf package, same posture as internal/profile and internal/skill:
// stdlib plus github.com/pelletier/go-toml/v2 only — never internal/model,
// internal/store, internal/service, or any other internal/* package (see
// leaf_test.go). The service layer (internal/service/quality.go) translates
// between this package's own sentinels (ErrInvalid, ErrUnsupportedSchema) and
// model.* sentinels, exactly as internal/conflicts already does for
// ErrCLIUnavailable and internal/lane's git.go does for its own types.
package quality
