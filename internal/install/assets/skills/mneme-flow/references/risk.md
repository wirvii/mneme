# The six risk items

A closed list. Every delivery closes with exactly one line, `Risk: none` or `Risk: item N (reason)` (several: `Risk: item 1, 5 (reason)`). The numbers are the contract: the delivery block, the lane auditor and the role denials all read them.

1. **Irreversible data or migrations.** Schema changes, migrations, persisted formats, anything a rollback cannot undo.
2. **Security and permissions.** Authentication, permissions, secrets, guards, sandboxing.
3. **Contracts others consume.** Public API, exported symbols, CLI flags, MCP tools, configuration keys, file formats other tools read. Code another feature shares counts too.
4. **Concurrency.** Locks, goroutines, shared state, ordering, retries.
5. **Delivery or environment.** Installers, release, CI, deploy, dependencies.
6. **No test would catch the regression.** The change is unverifiable by the existing suite.

## What the lane auditor detects by path

Only items 1, 2 and 5 are visible from a path, and part of item 3 from exported symbols. Items 4 and 6 are always declared, never detected.

| Item | Default path patterns |
|---|---|
| 1 | `**/*.sql`, `**/migrations/**`, `**/schema.*` |
| 2 | `**/.env`, `**/.env.*`, `**/*secret*`, `**/*secret*/**`, `**/*credential*`, `**/*credential*/**`, `**/*.pem`, `**/*.key`, `**/*.p12` |
| 5 | `cmd/**`, `internal/install/assets/**`, `.github/workflows/**`, `**/deploy/**`, `**/Dockerfile*`, `**/release*`, `**/.goreleaser*`, `**/go.mod`, `**/go.sum`, `**/package.json`, `**/package-lock.json`, `**/pnpm-lock.yaml`, `**/yarn.lock`, `**/bun.lockb`, `**/Cargo.lock`, `**/poetry.lock`, `**/uv.lock`, `**/Gemfile.lock`, `**/composer.lock` |

The defaults name mneme's own layout (`cmd/**`, `internal/install/assets/**`) plus formats every repository shares. Some are deliberately wide (`**/release*`, `**/*secret*/**`, `**/deploy/**`): a false match costs one reclassification, a miss costs a silent risk. A project adds its own paths, never removes any: `[lane.risk_globs]` in `~/.mneme/config.toml`, keys `"1"`, `"2"` or `"5"`.
