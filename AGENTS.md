# AGENTS.md

Router for agents working in this repo: the source code of `ezc` ("easy config"), a Go CLI that composes a repo's environment from a global store of secrets and variables and the repo's own `.ezc/`, validates it against a manifest, and exports it or runs a command with it. Read the linked file for detail; this page is only where things are.

The design and its parts are in [the ezc design](docs/superpowers/specs/2026-10-10-ezc-design.md); parts 1 (core) and 2 (secrets) are built, and `ezc setup` and store sync from part 3.

## Where things are

| Path | What it is |
| --- | --- |
| `cli/` | `ezc`, the Go CLI. Every command is in `cli/internal/cli`; `cli/cmd/ezc` only wires the process into `cli.Run`. |
| `cli/internal/backend` | The interface to an encryption tool (`Name`, `Check`, `HasPolicy`, `Inspect`, `Decrypt`, `Set`, `Unset`, `Edit`), a fake for tests that needs no tool (it keeps files in its own format on disk), and `backendtest`, the contract every backend passes. |
| `cli/internal/backend/sops` | The one place that knows `sops` exists: runs the program, picks the age key, and turns its failures into fixed messages. |
| `cli/internal/proc` | The description of a child process (`Command`) and the runner type the backend is given, so tests can fake it. |
| `cli/internal/valuefile` | Edits plaintext values files and manifests in place (`ezc set` and `unset`): comments kept, written through a temp file and rename, read back before it replaces the old. |
| `cli/internal/storeaudit` | Audits a store's values files against its manifest (`ezc store audit`): undeclared, missing, secret-boundary, leak, placeholder and shape findings, never a value. |
| `cli/internal/gitstore` | The one place that builds `git` command lines: a store's status against its remote, fetch, commit, push, and `Pull`, which rebases local commits and keeps the remote's version of a conflicted encrypted file. `gittest` runs the real git for tests, cut off from the user's config. |
| `cli/internal/cli/sync.go` | What a command learns about the store before it uses it: the fetch every 10 minutes recorded in `$EZC_HOME/state.json`, the `store:` line on stderr, the refusal to write while behind, and the commit after a write. |
| `cli/internal/cli/storecmd.go` | `ezc store status`, `pull`, `push` and `audit`. Pull turns the files `gitstore` replaced into the `ezc set -g` lines to run again, from `valuefile.ChangedNames`: names only. |
| `cli/internal/setup` | What `ezc setup` does to a machine: creates the key file, creates the store as a git repo (manifest, `recipients.yaml`, policy, one commit) or clones it, and publishes it to GitHub through `gh`. Flags, prompts and printing are in `cli/internal/cli/setupcmd.go`. It leaves alone whatever is already there. |
| `cli/internal/doctor` | Reports the health of the machine, the store and the repo (`ezc doctor`): checks, warnings and problems with their fixes, and the files in a `.ezc/` that ezc does not read. Never a value. |
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
| `.ezc/common.enc.yaml`, `.ezc/env.<name>.enc.yaml` | Encrypted values, for every environment and for one. |
| `.ezc/.sops.yaml` | The encryption policy, without which nothing can be encrypted in the folder. |
| `.ezc/env.sh` | The shim mise or direnv sources, written by `ezc repo init` from `cli/share/env.sh`. |

On a machine, `EZC_HOME` is ezc's folder and `EZC_STORE` the store's root when it is not `$EZC_HOME/store`.

## Conventions

- **Commands reach the outside world only through `System`.** Packages under `cli/internal/` other than `cli` never call `os.Getenv`, `os.UserHomeDir` or `os.Exit`.
- **A malformed command line returns a `*UsageError` and exits 2**; any other failure exits 1. Use `noArgs`, `exactArgs`, `minArgs`, `maxArgs` and `verbs` from `cli.go`, never Cobra's own argument checks.
- **No command prints a value** except `ezc env export`, `ezc run` (to the command) and `ezc get`. Error messages and `--verbose` output carry names and paths only: a check's reason never includes the value that failed it, and a YAML parser's error text, which can quote the file, is never passed on (`invalidYAML` in `cli/internal/resolve/file.go`).
- **Only `backend/sops` names sops.** No other package contains `sops`, `.sops.yaml`, `SOPS_` or `ENC[`, apart from tests and `cli/internal/cli/scope.go`, which builds the default backend. Text about the tool or the key comes from the backend's own messages.
- **No value in output.** That covers stdout, stderr, error text and anything passed through from a tool: the sops backend never passes sops's output on.
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
