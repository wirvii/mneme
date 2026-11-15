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

## Role denials (SPEC-185)

The hook `mneme hook enforce-delegation` turns items 1, 2, 5 and 6 into denials while a spec is being implemented. It reads the same pattern table as the lane auditor, so what the hook denies, what the auditor detects and what the trivial inline permission excludes are one list.

- An implementer (backend, frontend, bug-hunter, or a custom role built on one) cannot write, with a file tool or with Bash, a path that matches items 1, 2 or 5.
- Every subagent with Bash (implementers, qa-tester, diagnostician) cannot write such a path from the terminal, cannot run a command that adds, removes or updates a named dependency, and cannot delete or empty a test file.
- The test watch (item 6) is a text heuristic: an edit that adds a skip or focus marker to a test file (`t.Skip(`, `.skip(`, `.only(`, `xit(`, `@pytest.mark.skip`...), or leaves it without any test function, is denied; so is `rm`, `git rm` (not `--cached`), moving the file out of the tree or to a non-test name, and `> file`.
- The orchestrator is judged too on every write that is not on its whitelist: on a path no implementer owns, and on a path the trivial inline permission let through, where any match blocks.

The only way out is a declaration written by the architect or the orchestrator, never by the implementer: the architect closes the `plan` section of `tarea.md` (or `diseno`) with a line of the form `Risk: item N (reason)`. It counts while that standard spec is in `implementing` and, when the spec has a scope, only for paths inside that scope. A dependency command is never bounded by the scope: a declared item 5 in any open standard spec authorizes it project-wide. A standard spec's scope also declares items 1, 2 and 5 for the paths it covers, but never item 6. A trivial spec declares nothing: it only admits `Risk: none`. The delivery section never declares, because the implementer writes it.

Dependency commands count as item 5 only when they add, remove or update a named dependency: `go get <module>`, `go mod edit -require|-droprequire|-replace`, `npm|pnpm|yarn|bun add|remove|uninstall|update|upgrade`, `npm|pnpm|bun install <package>`, `pip install|uninstall <package>`, `cargo add|remove|update`, `poetry add|remove|update`. Installing what the lockfile already pins is routine test preparation and is never denied: `npm ci`, `npm install` with no package, `pnpm install`, `pip install -r <file>`, `pip install -e .`, `go mod download|verify|tidy`, `cargo install`, `poetry install`, and any global install (`-g`). Limit: a flag with a value the hook does not know may have its value read as a package; the remedy is declaring item 5 in the spec's plan, or widening the flag list in a separate change.

Subagents never write mneme's own data by hand either: the data directory, the workflow directory with every `tarea.md`, and the folder of `config.toml` are closed to them through file tools, Bash (reads included) and `apply_patch`. They change that state only through mneme's MCP tools.
