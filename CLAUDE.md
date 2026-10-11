# CLAUDE.md

Guidance for Claude Code in this repository. [`AGENTS.md`](AGENTS.md) is the router (where things are, conventions); this file adds commands and the sharp edges.

## Commands

| Task | Command |
| --- | --- |
| Put `ezc` on PATH (once) | `direnv allow` |
| Build `ezc` into `cli/bin/` | `mise run cli:build` |
| Test changes to `cli/` (the gate: gofmt, vet, tests) | `mise run cli:test` |
| Same gate with the real-sops tests required, not skipped | `mise run cli:test:sops` |
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
- **sops runs with its working directory set to the file's folder**, so it finds that folder's `.sops.yaml`; paths given to it are the file's base name.
- **A secret reaches sops on stdin only**, never in arguments, the environment or a temporary plaintext file. The backend never passes sops's output through: a failure is the no-key message, the not-installed message (`ErrNoTool`, from every operation that runs sops), the no-matching-rule message (`ErrNoPolicy`) or a fixed "could not read/write this file" message, and "no key" is said only for sops's own no-identity failures. The one exception is `Edit`: `sops edit` owns the terminal, so what it prints there is not filtered.
- **`ezc set` declares first**: for a new name it writes the declaration, then the value, and puts the manifest's bytes back if the value write fails. No `set` or `unset` error echoes an argument.
- **`ezc set` writes literally (`$` as `$$`); `--expand` keeps references.** `resolve.Escape` and `resolve.Unescape` are the resolver's own account of that, and `valuefile.SetLiteral` reads the result back through them. A literal leading `~` has no escape and is refused; `--expand` is refused for a secret, resolves the environment after the write and restores the file's bytes when the resolver's `*RefError` names this name in this file.
- **`ezc unset` removes only the value**, from the plaintext and encrypted file of the base; `--undeclare` also removes the declaration and is refused while another values file in the scope holds the name.
- **`Backend.Edit(path, editor)`** passes the editor `ezc edit` validated to sops as `SOPS_EDITOR`. `edit --secrets` needs the file to exist, opens one that fails validation, and checks the result afterwards. `sops edit` exits 200 when nothing was changed; the backend treats that as success.
- **A shim newer than the binary** is a doctor warning ("Upgrade ezc."), and `ezc repo init` keeps it.
- **`enc` is a reserved environment name**; names match `^[A-Za-z0-9][A-Za-z0-9_-]*$`. `codegen python` also refuses a name starting with `_`.
- **The fake backend refuses what sops refuses**: a file holding an unencrypted line cannot be decrypted, set, unset or edited, and the last name of a file is removed without a key. `storeaudit` therefore inspects a file before it decrypts it.
- **The fake backend's file format is not sops's.** Only the real-sops tests (`cli:test:sops`, and the end-to-end tests in `cli/cmd/ezc/sops_test.go`) prove the real format. They skip without `sops` unless `EZC_REQUIRE_SOPS=1`.
- **`ezc env`, `run`, `list` and `get` call `touchStore` once, before they resolve** (`cli/internal/cli/sync.go`): when the store is a git repo with a remote and the last attempt in `$EZC_HOME/state.json` is 10 minutes old, it fetches with a 3 second timeout, then prints at most one `store:` line on stderr (and a second when the fetch just failed). It never fails a command. A store that is not the top of a git repo gets one `git` call, to ask, and nothing else. `ezc env export` never calls it: no git, no line.
- **`sync.go` never names a git subcommand**: it calls `gitstore` methods. In tests the fake `System.Exec` sends `git` to `gittest.Runner` and records it in `fake.gits`, not `fake.execs`.
- **An invalid `config.toml` is an error from `load` and `scopeFor`**, naming the file. `backendFor` and `keySource` use the zero config when it cannot be read; its `key_file` replaces the default key file, and the environment's key still wins.
- **The encryption policy is still written by hand**: `.ezc/.sops.yaml`.
- **`docs/superpowers/` is history.** Specs and plans describe what was intended at the time; don't edit them to match the code.
