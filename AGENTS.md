# AGENTS.md

Router for agents working in this repo: the source code of `ezc` ("easy config"), a Go CLI that composes a repo's environment from a global store of secrets and variables and the repo's own `.ezc/`, validates it against a manifest, and exports it or runs a command with it. Read the linked file for detail; this page is only where things are.

The design and its parts are in [the ezc design](docs/superpowers/specs/2026-10-10-ezc-design.md); part 1 (core) is built.

## Where things are

| Path | What it is |
| --- | --- |
| `cli/` | `ezc`, the Go CLI. Every command is in `cli/internal/cli`; `cli/cmd/ezc` only wires the process into `cli.Run`. |
| `cli/internal/valuefile` | Edits plaintext values files and manifests in place (`ezc set` and `unset`): comments kept, written through a temp file and rename, read back before it replaces the old. |
| `bin/ezc` | Shim that builds `cli/bin/ezc` when a Go source changes and runs it; `direnv allow` puts `bin/` on PATH. |
| `docs/superpowers/` | Design specs and implementation plans. Historical: never rewrite them to match the code. |
| `LICENSE` | MIT. |

`ezc`'s data is outside this repo, per user, in `EZC_HOME` (default `~/Library/ezc` on macOS and `~/.local/share/ezc` on Linux; `$XDG_DATA_HOME/ezc` applies on every OS when set). The global store is `$EZC_HOME/store`, or the folder `EZC_STORE` names.

In a repo, `.ezc/` holds:

| Path | What it is |
| --- | --- |
| `.ezc/manifest.yaml` | What the repo needs from its environment. |
| `.ezc/common.yaml` | Plaintext values for every environment. |
| `.ezc/env.<name>.yaml` | Plaintext values for one environment; `env.local.yaml` is the default. |
| `.ezc/env.sh` | The shim mise or direnv sources, written by `ezc repo init` from `cli/share/env.sh`. |

On a machine, `EZC_HOME` is ezc's folder and `EZC_STORE` the store's root when it is not `$EZC_HOME/store`.

## Conventions

- **Commands reach the outside world only through `System`.** Packages under `cli/internal/` other than `cli` never call `os.Getenv`, `os.UserHomeDir` or `os.Exit`.
- **A malformed command line returns a `*UsageError` and exits 2**; any other failure exits 1. Use `noArgs`, `exactArgs`, `minArgs`, `maxArgs` and `verbs` from `cli.go`, never Cobra's own argument checks.
- **No command prints a value** except `ezc env export`, `ezc run` (to the command) and `ezc get`. Error messages and `--verbose` output carry names and paths only: a check's reason never includes the value that failed it, and a YAML parser's error text, which can quote the file, is never passed on (`invalidYAML` in `cli/internal/resolve/file.go`).
- **A variable name matches `manifest.NamePattern`** (`^[A-Za-z_][A-Za-z0-9_]*$`), tested with `manifest.ValidName`: one rule for the manifest, the values files and the JSON Schema.
- **A values file is one flat mapping of `NAME: value`.** The resolver rejects what would silently change or drop a value: an anchor, an alias, a tag, a key defined twice, a second YAML document, a NUL byte.
- **`ezc env export` has two formats**, `shell` and `json`.
- **No defaults for values, anywhere.**
- **All CLI commands are Go**, built with [Cobra](https://cobra.dev), in `cli/internal/cli`. Flags are POSIX style: long flags take two dashes.
- **Tests set `EZC_HOME` to an absolute temporary path and `HOME` to a temporary directory** through the fake `System`; no test reads the real home or the real environment.
- **Prose in Markdown is not hard-wrapped**: one line per paragraph or list item.
- **Comments are short**: one or two lines saying what a thing is.
- **Commit messages are conventional** (`feat(resolve): …`, `docs: …`).

[`CLAUDE.md`](CLAUDE.md) has the commands and the sharp edges.
