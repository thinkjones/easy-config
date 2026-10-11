# CLAUDE.md

Guidance for Claude Code in this repository. [`AGENTS.md`](AGENTS.md) is the router (where things are, conventions); this file adds commands and the sharp edges.

## Commands

| Task | Command |
| --- | --- |
| Put `ezc` on PATH (once) | `direnv allow` |
| Build `ezc` into `cli/bin/` | `mise run cli:build` |
| Test changes to `cli/` (the gate: gofmt, vet, tests) | `mise run cli:test` |
| Regenerate the committed manifest JSON Schema | `mise run schema:build` |
| Fail when the committed schema is stale | `mise run schema:check` |

## Sharp edges

- **Commands reach the outside world only through `System`** (environment, working directory, child processes, streams). Tests drive commands in-process with the fake in `cli_test.go`.
- **Tests never touch the real home**: set `HOME` and `EZC_HOME` to temporary directories through the fake `System`.
- **`cli/schema/v1/manifest.schema.json` is generated** from the Go types in `cli/internal/manifest`; run `mise run schema:build` after changing them. `schemaID` (in `cli/internal/cli/manifestcmd.go`) must change with the file if it moves.
- **`cli/share/env.sh` and `share.ShimVersion` change together.** `ezc repo init` rewrites a repo's shim when its first line differs from the embedded one.
- **`ezc env export` never prints to stderr on success and never emits `EZC_ENV` or `APP_ENV`**: the shim discards stderr and mise unwinds variables it owns.
- **The resolver is the only place `~`, `$HOME` and `$NAME` are expanded.**
- **`docs/superpowers/` is history.** Specs and plans describe what was intended at the time; don't edit them to match the code.
