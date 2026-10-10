# CLAUDE.md

Guidance for Claude Code in this repository. [`AGENTS.md`](AGENTS.md) is the router (where things are, conventions); this file adds commands and the sharp edges.

## Commands

| Task | Command |
| --- | --- |
| Put `ezc` on PATH (once) | `direnv allow` |
| Build `ezc` into `cli/bin/` | `mise run cli:build` |
| Test changes to `cli/` (the gate: gofmt, vet, tests) | `mise run cli:test` |

## Sharp edges

- **Commands reach the outside world only through `System`** (environment, working directory, child processes, streams). Tests drive commands in-process with the fake in `cli_test.go`.
- **Tests never touch the real home**: set `HOME` and `EZC_HOME` to temporary directories through the fake `System`.
- **`docs/superpowers/` is history.** Specs and plans describe what was intended at the time; don't edit them to match the code.
