# CLAUDE.md

This file provides behavioral guidance to Claude Code when working in this repository.
Project knowledge (architecture, commands, tech stack) lives in mneme persistent memory.

## Quality Standards

This project adheres to the highest engineering standards:

- **Clean Code**: Every function has a single responsibility. Names are intention-revealing. No dead code, no commented-out code, no magic numbers.
- **Clean Architecture**: Strict dependency inversion. Domain types have zero external dependencies. Infrastructure adapters are pluggable and testable.
- **Documentation**: Every exported type, function, and package has a godoc comment explaining *why*, not just *what*. Internal packages document their design rationale.
- **Testing**: Every public API has unit tests. Integration tests cover the full pipeline. Table-driven tests are preferred. Test helpers are well-documented. Target: >85% coverage on core packages.
- **Error Handling**: Errors are wrapped with context (`fmt.Errorf("store: save memory: %w", err)`). Never swallow errors silently. Sentinel errors for expected conditions.
- **Design Patterns**: Repository pattern for storage. Strategy pattern for retrieval backends. Observer pattern for hooks. Command pattern for CLI. Builder pattern where constructors are complex.

## Conventions

- **Commits**: [Conventional Commits](https://www.conventionalcommits.org/) — `type(scope): description`
- **Branches**: `type/short-description` (lowercase, hyphens)
- **Go version**: 1.24+
- **Linting**: `golangci-lint run` must pass with zero warnings
- **Formatting**: `gofmt` and `goimports` enforced
