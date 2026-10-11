# AGENTS.md

Router for agents working in this repo: the source code of `ezc` ("easy config"), a Go CLI that composes a repo's environment from a global store of secrets and variables and the repo's own `.ezc/`, validates it against a manifest, and exports it or runs a command with it. Read the linked file for detail; this page is only where things are.

The design and its parts are in [the ezc design](docs/superpowers/specs/2026-10-10-ezc-design.md); parts 1 (core), 2 (secrets) and 3 (`ezc setup`, the store as a git repo, and its sync) are built. Adding a second person (`ezc join`), bots and CI setup are part 4.

## Where things are

| Path | What it is |
| --- | --- |
| `cli/` | `ezc`, the Go CLI. Every command is in `cli/internal/cli`; `cli/cmd/ezc` only wires the process into `cli.Run`. |
| `cli/internal/backend` | The interface to an encryption tool (`Name`, `Check`, `HasPolicy`, `Inspect`, `Decrypt`, `Set`, `Unset`, `Edit`, and for setup `NewKey`, `PublicKey`, `WritePolicy`, `ToolHint`, `BrewFormula`), a fake for tests that needs no tool (it keeps files in its own format on disk), and `backendtest`, the contract every backend passes. |
| `cli/internal/backend/sops` | The one place that knows `sops` exists: runs the program, picks the age key, makes a new key and reads a key file's public half, writes a folder's policy from a list of public keys, and turns its failures into fixed messages. |
| `cli/internal/paths` | Where ezc keeps things on a machine: `EZC_HOME`, and in it `config.toml`, `state.json` and the store. |
| `cli/internal/config` | Loads and saves the machine's `config.toml`: `user`, `backend`, `key_file` and the store's `remote`. |
| `cli/internal/recipients` | The registry of who can decrypt a store, its `.ezc/recipients.yaml`: load, save, the list of keys the policy is written from, and the rule for a recipient's name (`^[a-z0-9][a-z0-9-]*$`). |
| `cli/internal/proc` | The description of a child process (`Command`) and the runner type the backend is given, so tests can fake it. |
| `cli/internal/valuefile` | Edits plaintext values files and manifests in place (`ezc set` and `unset`): comments kept, written through a temp file and rename, read back before it replaces the old. |
| `cli/internal/storeaudit` | Audits a store's values files against its manifest (`ezc store audit`): undeclared, missing, secret-boundary, leak, placeholder and shape findings, never a value. |
| `cli/internal/gitstore` | The one place that builds `git` command lines: a store's status against its remote, fetch, commit, push, and `Pull`, which rebases local commits and keeps the remote's version of a conflicted encrypted file. `gittest` runs the real git for tests, cut off from the user's config. |
| `cli/internal/cli/sync.go` | What a command learns about the store before it uses it: the fetch every 10 minutes recorded in `$EZC_HOME/state.json`, the `store:` line on stderr, the refusal to write while behind or while the store's state cannot be read, and the commit after a write. |
| `cli/internal/cli/storecmd.go` | `ezc store status`, `pull`, `push` and `audit`. Pull turns the files `gitstore` replaced into the `ezc set -g` lines to run again, from `valuefile.ChangedNames`: names only. |
| `cli/internal/setup` | What `ezc setup` does to a machine: creates the key file, creates the store as a git repo (manifest, `recipients.yaml`, policy, one commit) or clones it, and publishes it to GitHub through `gh`. Flags, prompts and printing are in `cli/internal/cli/setupcmd.go`. It leaves alone whatever is already there. |
| `cli/internal/doctor` | Reports the health of the machine, the store and the repo (`ezc doctor`): checks, warnings and problems with their fixes, and the files in a `.ezc/` that ezc does not read. Never a value. |
| `bin/ezc` | Shim that builds `cli/bin/ezc` when a Go source changes and runs it; `direnv allow` puts `bin/` on PATH. |
| `docs/superpowers/` | Design specs and implementation plans. Historical: never rewrite them to match the code. |
| `LICENSE` | MIT. |

`ezc`'s data is outside this repo, per user, in `EZC_HOME` (default `~/Library/ezc` on macOS and `~/.local/share/ezc` on Linux; `$XDG_DATA_HOME/ezc` applies on every OS when set). `ezc setup` writes `config.toml` there and creates or clones the global store, a git repo at `$EZC_HOME/store` whose values are in its `.ezc/`; `state.json` beside them records when the store's remote was last reached. `EZC_STORE` names another folder as the store, which `ezc setup` does not manage.

In a repo, `.ezc/` holds:

| Path | What it is |
| --- | --- |
| `.ezc/manifest.yaml` | What the repo needs from its environment. |
| `.ezc/common.yaml` | Plaintext values for every environment. |
| `.ezc/env.<name>.yaml` | Plaintext values for one environment; `env.local.yaml` is the default. |
| `.ezc/common.enc.yaml`, `.ezc/env.<name>.enc.yaml` | Encrypted values, for every environment and for one. |
| `.ezc/.sops.yaml` | The encryption policy, without which nothing can be encrypted in the folder. In the store `ezc setup` writes it from `recipients.yaml`; in a repo it is a copy of the store's. |
| `.ezc/env.sh` | The shim mise or direnv sources, written by `ezc repo init` from `cli/share/env.sh`. |

The store's `.ezc/` holds the same files without `env.sh`, and `recipients.yaml`: who can decrypt the store, by name and public key.

## Conventions

- **Commands reach the outside world only through `System`.** Packages under `cli/internal/` other than `cli` never call `os.Getenv`, `os.UserHomeDir` or `os.Exit`.
- **A malformed command line returns a `*UsageError` and exits 2**; any other failure exits 1. Use `noArgs`, `exactArgs`, `minArgs`, `maxArgs` and `verbs` from `cli.go`, never Cobra's own argument checks.
- **No command prints a value** except `ezc env export`, `ezc run` (to the command) and `ezc get`. Error messages and `--verbose` output carry names and paths only: a check's reason never includes the value that failed it, and a YAML parser's error text, which can quote the file, is never passed on (`invalidYAML` in `cli/internal/resolve/file.go`).
- **Only `backend/sops` names sops.** No other package contains `sops`, `.sops.yaml`, `SOPS_` or `ENC[`, apart from tests and `cli/internal/cli/scope.go`, which builds the default backend. Text about the tool or the key comes from the backend's own messages.
- **No value in output.** That covers stdout, stderr, error text, commit messages and anything passed through from a tool: the sops backend never passes sops's output on, and a commit message holds names and file names only.
- **Only `gitstore` builds `git` command lines.** Every other package calls its methods; `gh` and `brew` command lines are built in `setup`. Git, `gh`, `brew` and `sops` run only through `proc.Runner`.
- **The private key is never printed** and is written to one place, the key file (`0600`, in a folder created `0700`). Nothing overwrites a key file, a store or a `config.toml` that is there.
- **A variable name matches `manifest.NamePattern`** (`^[A-Za-z_][A-Za-z0-9_]*$`), tested with `manifest.ValidName`: one rule for the manifest, the values files and the JSON Schema.
- **A values file is one flat mapping of `NAME: value`.** The resolver rejects what would silently change or drop a value: an anchor, an alias, a tag, a key defined twice, a second YAML document, a NUL byte.
- **`ezc env export` has two formats**, `shell` and `json`.
- **No defaults for values, anywhere.**
- **All CLI commands are Go**, built with [Cobra](https://cobra.dev), in `cli/internal/cli`. Flags are POSIX style: long flags take two dashes.
- **Tests set `EZC_HOME` to an absolute temporary path and `HOME` to a temporary directory** through the fake `System`; no test reads the real home, the real environment, a real key or the user's git config, and none uses the network. Git in tests runs with `GIT_CONFIG_GLOBAL` naming an empty temporary file and `GIT_CONFIG_NOSYSTEM=1` (`gitstore/gittest`); a remote is a bare repo in a temporary folder; keys are generated in the test; `gh` and `brew` are always faked.
- **Prose in Markdown is not hard-wrapped**: one line per paragraph or list item.
- **Comments are short**: one or two lines saying what a thing is.
- **Commit messages are conventional** (`feat(resolve): …`, `docs: …`).

[`CLAUDE.md`](CLAUDE.md) has the commands and the sharp edges.
