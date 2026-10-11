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
- **`cli/share/env.sh` and `share.ShimVersion` change together.** `ezc repo init` rewrites a repo's shim when its first line differs from the embedded one, unless the shim's version is newer: that one is kept, so a teammate on a newer `ezc` is not downgraded.
- **`ezc env export` never prints to stderr on success and never emits `EZC_ENV` or `APP_ENV`**: the shim discards stderr and mise unwinds variables it owns.
- **The resolver is the only place `~`, `$HOME` and `$NAME` are expanded.**
- **On Unix `ezc run` replaces itself with the command** (`syscall.Exec` in `cli/cmd/ezc/run_unix.go`), so nothing after `RunChild` runs there; `run_other.go` keeps a child process on other systems. The in-process tests use the fake `RunChild`; the real one is tested end to end in `cli/cmd/ezc`.
- **A missing store is fine only at the default location.** A store that is not a folder or cannot be read is an error from `resolve.Load`; an `EZC_STORE` that does not exist is an error from `storePaths` in `cli/internal/cli/scope.go`.
- **`codegen python` refuses a variable named after a Python keyword**; the manifest itself allows it.
- **`doctor` and `storeaudit` print names, counts and paths only, never a value**, and `doctor` never names the encryption tool: the policy file names, the key's location and the backend's messages come in through `Options` from `scope.go` and the backend.
- **`doctor` enforces `share.ShimVersion` against the repo's `.ezc/env.sh`.** Change the shim, its first line and `ShimVersion` together, or `ezc doctor` reports a stale shim as healthy or a good one as broken. A shim newer than the binary is a warning there, never a fix to apply.
- **`docs/superpowers/` is history.** Specs and plans describe what was intended at the time; don't edit them to match the code.
