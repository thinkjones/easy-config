# ezc design

Date: 2026-10-10. Status: approved in conversation, awaiting review of this file.

## Purpose

`ezc` ("easy config") is a public Go CLI and TUI that gives a person or a small team one place for secrets and variables. It carries the functionality of the private `fs-config` repo (`fsconfig`), generalised so that it works for anyone, and is built with the structure, tools and conventions of `ipm` (`thinkjones/intelligent-package-manager`).

A user of `ezc` can:

1. Define a global store of secrets and variables, kept in a local folder or a GitHub repo, with values common to every environment and values per environment.
2. Keep the same kind of values inside a repo, layered over the global store.
3. Have the tool set up encryption, the store location and the user-scope install, and manage the values.
4. Add a person, a machine or a bot as someone who can decrypt, and remove one, each in one step.
5. Record the repos that depend on the store, and build and validate the environment each one requires from its manifest.

Success is: a new user goes from nothing to a working encrypted store with `ezc setup`; a second person gets access with `ezc join` and one approval; a repo's environment is validated by `ezc env` and loaded by mise, direnv or `ezc run`; and no step needs hand-editing of an encryption policy or a hand-run `sops` command.

## Decisions

| Question | Decision |
| --- | --- |
| Relation to fs-config | New names and paths throughout. Migration is shipped as a command and a skill but is not run as part of this work. |
| Encryption | A `Backend` interface. One implementation in this version: SOPS with age keys. |
| How encryption is done | The `sops` binary, called through `System`; age keys are generated and read in-process with `filippo.io/age`. Secret files are standard sops files. |
| Global store | Always a git repo the tool manages in `$EZC_HOME/store`. Local-only or with a GitHub remote. Writes commit locally; push and pull are explicit. A strong indicator shows when local and remote differ. |
| Onboarding | The newcomer pushes a request file to the store repo; an existing user approves it. A local-only store uses a pasted request string. |
| Dependent repos | A shared list committed in the store, with checks across repos. |
| Frameworks | Cobra for the CLI, Bubble Tea v2 with Lip Gloss v2 and Bubbles v2 for the TUI, at the versions ipm uses. |
| Name | Repo `easy-config`, binary `ezc`, module `github.com/thinkjones/easy-config/cli`, directory `.ezc/`, environment prefix `EZC_`. |

## Files

### One layout

The global store and a repo use the same directory and the same file names:

```
.ezc/
  manifest.yaml          every value declared: type, desc, required or optional
  common.yaml            plaintext, all environments
  common.enc.yaml        secrets, all environments
  env.<name>.yaml        plaintext, one environment
  env.<name>.enc.yaml    secrets, one environment
  .sops.yaml             generated; never edited
```

Only `manifest.yaml` is required. Two rules are enforced by file name:

1. **The `.enc.` infix is the plaintext/secret boundary.** A plaintext `*.yaml` is safe in a diff. In a `*.enc.yaml` every value is encrypted. There is no per-field selection.
2. **Everything is YAML, read as raw text.** The loader walks the `yaml.Node` tree and takes each scalar's source text, so no type is inferred: `08540` stays `08540`. sops supports YAML and does not support TOML (it encrypts a `.toml` as one binary blob), which is why secrets are YAML and plaintext follows.

The manifest keeps fs-config's schema, version `v1`: `required` and `optional` maps of name to `{type, desc, values, min, max, requiredWhen}`, with types `str`, `int`, `bool`, `secret`, `url`, `path`, `enum`. Descriptions, never defaults. In a repo the manifest is a contract: what the repo needs to start. In the store it is a catalogue: what the store publishes. The Go types are the schema; `cli/schema/v1/manifest.schema.json` is generated from them and published at a public URL, so editors can fetch it.

### Store-only files

The store's `.ezc/` also holds:

```
recipients.yaml      who can decrypt
repos.yaml           the repos that depend on this store
requests/<name>.yaml pending access requests
rotation-due.yaml    secrets a removed key could read, not yet given a new value
```

`recipients.yaml`:

```yaml
recipients:
  gene:
    key: age1...
    kind: user            # user | machine | bot
    added: 2026-10-10
    addedBy: gene
  gene-mini:
    key: age1...
    kind: machine
    owner: gene
    added: 2026-10-10
    addedBy: gene
  ci:
    key: age1...
    kind: bot
    added: 2026-10-10
    addedBy: gene
    repos: [thinkjones/fs-news-digest, thinkjones/fs-website]
```

`requests/<name>.yaml` has `name`, `key`, `kind`, `owner` (for a machine), `host` and `requested`.

`repos.yaml`:

```yaml
repos:
  thinkjones/fs-news-digest: {added: 2026-10-10, addedBy: gene}
  scratch-tool: {added: 2026-10-10, addedBy: gene}   # no remote: a plain name
```

`rotation-due.yaml` maps a secret's file and name to the removed recipient and the date: `common.enc.yaml/LLM_API_KEY: {removed: sam, on: 2026-10-10}`.

`.sops.yaml` is generated from `recipients.yaml` on every change to it. It has one creation rule, `path_regex: \.enc\.yaml$`, with every recipient key on one comma-separated line (sops splits on the comma and does not trim).

### On a machine

| Path | What |
| --- | --- |
| `$EZC_HOME/config.toml` | This machine's settings |
| `$EZC_HOME/store/` | The global store: a git repo the tool manages. Its values are in `store/.ezc/` |
| `$EZC_HOME/state.json` | The cached result of the last fetch of the store |
| `~/.config/sops/age/keys.txt` | The age key. An existing key is reused. `ezc` always passes this path to sops as `SOPS_AGE_KEY_FILE`, so the macOS default-location problem can't occur |
| `~/.claude/skills/ezc-convert/`, `~/.claude/skills/ezc-migrate/` | Skills installed by `ezc setup` when `~/.claude` exists |

`EZC_HOME` defaults follow ipm's `paths` package: `~/Library/ezc` on macOS, `$XDG_DATA_HOME/ezc` or `~/.local/share/ezc` on Linux, `%LOCALAPPDATA%\ezc` on Windows. An override must be absolute.

`config.toml`:

```toml
user = "gene"
backend = "sops"
key_file = "/Users/gene/.config/sops/age/keys.txt"

[store]
remote = "git@github.com:thinkjones/config.git"   # absent for a local-only store

[checkouts]
"thinkjones/fs-news-digest" = "/Users/gene/dev/ao/fs-news-digest"
```

`EZC_STORE` overrides the store path for one command, as `FS_CONFIG_HOME` does today; the tool works against any store.

### In a repo

`ezc repo init` writes `.ezc/manifest.yaml`, `.ezc/env.local.yaml` and `.ezc/env.sh`. `env.sh` is committed, one line of behaviour (`eval "$(ezc env export 2>/dev/null || true)"`), and carries a version marker on its first line that `ezc doctor` checks. It lives in the repo, not at a home path, because `EZC_HOME` differs per operating system and a committed `mise.toml` could not point at it.

Three ways to load the environment, all through the one resolver:

- mise: `_.source = ".ezc/env.sh"`
- direnv: `eval "$(ezc env export)"`
- neither: `ezc run -- <command>`

## Resolution

Order, later wins:

```
store  common.yaml → common.enc.yaml → env.<name>.yaml → env.<name>.enc.yaml
repo   common.yaml → common.enc.yaml → env.<name>.yaml → env.<name>.enc.yaml
```

`<name>` is the command's argument, else `$EZC_ENV`, else `$APP_ENV`, else `local`.

Rules carried over from fs-config, all in the resolver and nowhere else:

- Within a file, order is semantic. Loaders keep document order.
- A plaintext value may reference `$NAME` or `${NAME}` for a name already resolved (an earlier file or an earlier line). A forward or unknown reference is an error naming what is in scope. `$$` is a literal `$`. Decrypted values are never substituted into. `HOME` resolves without being declared.
- A leading `~`, `$HOME` or `${HOME}` is expanded at load, in plaintext and encrypted files.
- A bare `~` (YAML null) and an unquoted non-string scalar in an `*.enc.yaml` are rejected.
- No defaults. A missing value fails the check.
- A named environment with no `env.<name>.yaml` in the repo is an error listing the valid names. With no name given and no `env.local.yaml`, the store composes alone and the command says so on stderr.
- Shell export single-quotes every value and never emits the environment variable that selects the environment (`EZC_ENV`, `APP_ENV`), because mise unwinds variables it owns.

One resolver serves `env`, `env export`, `run`, `get`, `list`, `codegen` and `doctor`, so what is validated is what is loaded.

## Commands

Commands are grouped by the object they act on. `-g` means the global store; without it a command acts on the repo whose `.ezc/` is the nearest ancestor of the working directory. Exit codes: 0 success, 1 failure, 2 a malformed command line.

### Machine

| Command | Does |
| --- | --- |
| `ezc setup` | The wizard: check or install sops; find or create the age key and ask the user to back it up; then create a local store, create a GitHub store (`gh repo create --private`), or join an existing one; install the skills. Every prompt has a flag (`--store`, `--new-local`, `--new-github owner/repo`, `--name`, `--yes`), so it runs unattended. Safe to re-run. |
| `ezc setup --ci --store owner/repo` | For CI: clones the store read-only with the token in `EZC_STORE_TOKEN` (or `GH_TOKEN`) and uses the key in `EZC_AGE_KEY`, passed to sops as `SOPS_AGE_KEY`. No key file is written. |
| `ezc join <owner/repo>` | The newcomer path; see Access. |
| `ezc doctor [ENV] [--only machine\|store\|repo]` | Health of each; exits non-zero when something is broken. Prints names, counts and paths, never a value. |

### Values

| Command | Does |
| --- | --- |
| `ezc set NAME [VALUE] [--env E] [-g]` | The manifest's type decides the file: `secret` goes to the `.enc.yaml`, anything else to plaintext. A secret is read from a hidden prompt or stdin, never from argv. An undeclared name needs `--type` and `--desc` and is declared in the same step. Setting a secret listed in `rotation-due.yaml` clears its entry. |
| `ezc unset NAME [--env E] [-g]` | Removes the value, and the declaration when no file still holds one. In the store it refuses when a registered repo requires the name, naming the repos; `--force` overrides. |
| `ezc list [ENV] [-g]` | Every name: type, the file that supplies it, what it overrides. No values. |
| `ezc get NAME [ENV]` | One resolved value. A secret needs `--reveal`. |
| `ezc edit [--env E] [--secrets] [-g]` | Opens the file in `$EDITOR`, through sops for secrets. Refuses an editor known not to block (`code` without `--wait`). |

### Environment

| Command | Does |
| --- | --- |
| `ezc env [ENV] [-m FILE] [--verbose]` | Validates the composed environment against the repo's manifest: presence, placeholders (`replace-me`), ints and bounds, enum membership, URL shape, `requiredWhen`. |
| `ezc env export [ENV] [--format shell\|dotenv\|json]` | Emits it. Never fetches, never prints to stderr on success. |
| `ezc run [ENV] -- <command>` | Runs a command with it. |
| `ezc manifest validate [-m FILE]`, `ezc manifest schema [-o FILE]` | Check a manifest; emit the JSON Schema. |
| `ezc codegen python [-m FILE] [-o FILE]` | A pydantic v2 model from the manifest. |

### Store

| Command | Does |
| --- | --- |
| `ezc store status` | Path, remote, sync state, last fetch, pending requests. |
| `ezc store pull`, `ezc store push` | Sync with the remote. |
| `ezc store audit` | The store against its manifest, complete in both directions (an undeclared value fails, a declaration with no value fails), plus the secret boundary read from the declared type. Without a key it still checks names, completeness and the boundary, and says value checks were skipped. |

### Repos

| Command | Does |
| --- | --- |
| `ezc repo init` | Scaffolds `.ezc/` and prints the mise or direnv line. |
| `ezc repo add [owner/repo]`, `ezc repo remove <repo>`, `ezc repo list` | The shared registry. `add` with no argument registers the working repo and records its checkout path. |
| `ezc repo check [repo\|--all] [ENV]` | Validates registered repos from their local checkouts; lists the ones with no checkout on this machine. Also reports a repo whose secret files' recipients differ from the registry. |
| `ezc repo uses NAME` | Which registered repos require a store value. |
| `ezc repo rewrap [repo\|--all]` | Regenerates a repo's `.ezc/.sops.yaml` from the store's registry and re-wraps its `*.enc.yaml`. Leaves the change in the repo's working tree for its owner to commit. |

### Access

`ezc user list|add|remove|rotate`, `ezc user requests|approve|deny`, `ezc bot add|remove|rotate`. See Access.

### Other

`ezc tui [-g]`, `ezc migrate [--dry-run]`, `ezc version`, `ezc help [command]`.

There is no `ezc install`: the binary arrives by `go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest` or a release download, and `ezc setup` does the rest. There are no generators beyond Python.

## Access

### The registry

`recipients.yaml` is the single registry. `.sops.yaml` is generated from it. The ciphertext is the reality: recipient lines are plaintext inside an encrypted file, so `ezc user list` and `ezc doctor` compare each file's actual recipients with the registry without needing a key, and fail on a difference.

Every change to recipients runs the same sequence: update `recipients.yaml`, regenerate `.sops.yaml`, re-wrap every `*.enc.yaml` in the store, verify each file lists exactly the registry's keys, commit, push. If any step fails the working tree is restored and nothing is committed. After the store, it re-wraps each registered repo that has a checkout on this machine (as `ezc repo rewrap` does) and lists the registered repos it could not reach.

Repo secret files are encrypted for the store's recipients. A repo has no recipient list of its own.

### Adding a person or a machine

1. The newcomer runs `ezc join <owner/repo>`. It clones the store, finds or creates their age key, writes `requests/<name>.yaml`, and pushes it on the branch `ezc/join-<name>`, opening a pull request when `gh` is available. `ezc doctor` then reports that they cannot decrypt yet and shows the request's status.
2. An existing user sees it in `ezc user requests`, in the sync line of any store command, and in the TUI header. The listing shows the full public key, the host and the Git author of the request, because approving is a trust decision.
3. `ezc user approve <name>` merges the request branch, moves the entry into `recipients.yaml`, deletes the request and runs the sequence above. `ezc user deny <name>` deletes the request and its branch.
4. The newcomer runs `ezc store pull` and can decrypt.

For a local-only store `ezc join --print` prints one request string, and `ezc user add <name> <key> [--kind machine --owner <user>]` takes it. `user add` also works on a GitHub store.

One key per machine: a person's second machine is a recipient of kind `machine` with an `owner`, so a lost laptop is one entry removed.

### Adding a bot

`ezc bot add <name> --repo owner/repo…` generates a keypair in memory, adds the public half as a recipient of kind `bot`, runs the sequence, proves the new key decrypts every file on its own, then writes the private half to each repo's `EZC_AGE_KEY` Actions secret with `gh secret set`, through stdin. The private half is never written to disk or printed. `--print-private` is the alternative for CI that is not GitHub Actions. Repos named must be in the registry.

A bot also needs to read the store repo. `ezc` cannot create that credential; `bot add` prints what is needed (a read-only deploy key or fine-grained token for the store repo, as the `EZC_STORE_TOKEN` secret), and the docs give the steps.

`ezc bot rotate <name>` does the same with a new key and removes the old one in the same commit. `ezc user rotate` replaces the caller's own key and rewrites the key file; it refuses to rotate another person's key, since there is no safe channel for the private half.

### Removing

`ezc user remove <name>` or `ezc bot remove <name>` deletes the entry and runs the sequence. For a bot it also deletes the `EZC_AGE_KEY` secret from each of its repos.

Re-wrapping does not un-leak: the removed key could read every secret that existed. So removal lists those secrets and writes them to `rotation-due.yaml`. `ezc doctor`, `ezc store status` and the TUI show the list until each one has been given a new value with `ezc set`.

Refused: removing the last recipient, and removing your own only key.

## Store sync

The store is a git repo. Every command that writes to it makes one commit with a message that names the change (`set LLM_API_KEY (common.enc.yaml)`, `approve sam`). `ezc store push` publishes; `ezc store pull` fetches and rebases.

State is one of: in sync, ahead, behind, diverged, uncommitted changes. A local-only store is only ever in sync or uncommitted.

- **Fetching.** Knowing "behind" needs a fetch. A command that touches the store fetches first when `state.json` is older than 10 minutes, with a 3 second timeout. Offline, it uses the cached state and says how old it is.
- **The indicator.** Any state other than in sync prints one line on stderr, in amber for ahead and red for the rest, on every command that touches the store: `store: 2 commits behind origin — run ezc store pull`. Pending requests are named in the same line. The TUI shows it as a permanent badge in the header. `NO_COLOR` is honoured.
- **The fast path.** `ezc env export` never fetches and never prints the line, so a shell stays fast and quiet on `cd`. `ezc env` does both.
- **Writes while behind.** A write to the store is refused while it is behind or diverged, because conflicting ciphertext cannot be merged by hand. `ezc store pull` rebases plaintext changes; for an encrypted file changed on both sides it takes the remote's version, stops, and names the keys whose local change must be re-applied with `ezc set`.
- **Access changes push.** `approve`, `remove`, `bot add` and `rotate` push as their last step, since an access change that stays local has changed nothing for anyone else. Value changes do not push on their own.

## The repo registry

`repos.yaml` lists repos by `owner/repo`, or by a plain name for a repo with no remote. Checkout paths are per machine and live in `config.toml`: recorded when `ezc repo add` runs inside a checkout, and otherwise found by matching the git remote of the working directory.

The registry is what makes these work: `ezc repo check --all`; `ezc repo uses NAME`; the guard on `ezc unset -g`; the list of repos a bot's key is delivered to; and re-wrapping repo secret files after an access change.

## TUI

`ezc tui [-g]` uses ipm's `panel`, `fit`, palette and key conventions: `?` for a help overlay, `tab` to change panel, `q` to quit, arrow and vim keys.

```
ezc  store thinkjones/config · env local                              ▲ 2 behind  ● 1 request
╭─ values ── global ───────────────────────────────────────────────────────────────────────────╮
│ › LLM_API_KEY          secret  common.enc.yaml        ••••••••          used by 3 repos       │
│   CF_ACCOUNT_ID        str     common.yaml            a1b2…             used by 5 repos       │
│   BATCH_SIZE           int     env.local.yaml         20                overrides common      │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ detail ── LLM_API_KEY ──────────────────────────────────────────────────────────────────────╮
│ API key for whichever provider LLM_PROVIDER names                                            │
│ required by  fs-news-digest · fs-website · fs-gascity                                        │
│ ! rotation due: readable by removed user "sam"                                               │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
1 values  2 access  3 repos  4 doctor     s set  u unset  r reveal  e env  g scope  P push  ? help
```

Four views, switched with `1` to `4`:

- **values**: every name, its type, the file supplying it and its use. Set, unset, reveal a secret (hidden again when the selection moves), switch environment, toggle store or repo scope.
- **access**: recipients by kind, pending requests with approve and deny, add a bot, remove, rotate, and the rotation-due list.
- **repos**: the registry with each repo's check result for the selected environment; enter shows what is missing or invalid.
- **doctor**: the checks `ezc doctor` runs.

An action that changes access or pushes shows a confirm screen listing exactly what it will do, as ipm's deploy does. With no store configured the TUI opens the setup wizard. `ezc setup` in a terminal uses the same wizard; with flags or without a terminal it runs as a plain command.

The TUI adds no behaviour. `internal/tui` imports no ezc package and depends only on the interfaces `Values`, `Access`, `Repos`, `Store` and `Doctor`, which `internal/cli/tui.go` implements by calling the functions the commands call.

## Code structure

```
cli/cmd/ezc/main.go        wires the process into cli.Run
cli/internal/
  cli/           one file per command group; System; UsageError; ExitError
  tui/           model, views, style, keys, testdata/*.golden
  paths/         EZC_HOME and the files in it
  config/        config.toml
  manifest/      the types that are the schema; load, validate, JSON Schema   (ported)
  resolve/       the single resolver                                          (ported, generalised)
  check/         an environment against a manifest                            (ported)
  storeaudit/    a store against its manifest                                 (ported)
  gen/           python                                                       (ported)
  backend/       the Backend interface and an in-memory fake
  backend/sops/  sops through System, age in-process
  recipients/    the registry, requests, rotation-due; calls Backend only
  gitstore/      clone, fetch, status, commit, pull, push through System
  registry/      repos.yaml and checkout lookup
  setup/         the wizard's steps as plain functions
cli/schema/v1/manifest.schema.json     generated
cli/share/       the env.sh template, skills/ezc-convert, skills/ezc-migrate (embedded with go:embed)
bin/ezc          shim that rebuilds cli/bin/ezc when a Go source is newer, then runs it
.envrc, mise.toml, AGENTS.md, CLAUDE.md, README.md, LICENSE (MIT), docs/ (Just the Docs)
```

Conventions taken from ipm: every command is a `new<Name>Cmd(sys System)` in `internal/cli`, registered in `newRootCmd`; argument checks use `noArgs`, `exactArgs`, `minArgs` and `verbs`, which return a `UsageError`; commands reach the outside world only through `System`; Markdown prose is not hard-wrapped; user docs change in the same commit as the code; `docs/superpowers/` is history and is not edited to match the code.

`System` has ipm's fields (`Stdout`, `Stderr`, `Getenv`, `TermWidth`, `LookPath`, `Getwd`, `Now`, `Version`) and three changes: `Stdin`; `ReadSecret`, a hidden-input reader; and `Exec(Command)` in place of `Output`, where `Command` has `Dir`, `Env`, `Stdin`, `Name` and `Args`. sops needs `SOPS_AGE_KEY_FILE` in its environment and a working directory, and `gh secret set` needs the private key on stdin.

The backend:

```go
// Pair is one name and its value. backend imports no other ezc package;
// resolve imports backend.
type Pair struct{ Name, Value string }

type Backend interface {
    // Name is the value of backend in config.toml.
    Name() string
    // NewKey creates a keypair, returning the public half and the private text.
    NewKey() (public string, private []byte, err error)
    // PublicKey reads the public half of the key in keyFile.
    PublicKey(keyFile string) (string, error)
    // Decrypt returns the file's values in document order.
    Decrypt(path string) ([]Pair, error)
    // Set and Unset change one value in an encrypted file, creating it if needed.
    Set(path, name, value string) error
    Unset(path, name string) error
    // Edit opens the decrypted file in the user's editor.
    Edit(path string) error
    // Recipients reads who the file is encrypted for; it needs no key.
    Recipients(path string) ([]string, error)
    // WritePolicy writes the backend's policy file in dir for these recipients.
    WritePolicy(dir string, recipients []string) error
    // Rewrap re-encrypts the file's data key for the policy in its directory.
    Rewrap(path string) error
    // CanDecrypt proves a private key decrypts the file on its own.
    CanDecrypt(path string, private []byte) error
}
```

Nothing outside `backend/sops` names sops. The sops backend always runs sops with its working directory set to the `.ezc/` directory that holds the policy, because `sops updatekeys` resolves `.sops.yaml` from the working directory.

## Errors

- A message says what failed, on which file or name, and the command that fixes it.
- No command prints a secret value except `ezc get --reveal` and `ezc bot add --print-private`. Errors from sops are passed through with values redacted.
- A failure partway through a multi-step change (an access change, a pull) restores the store's working tree to the last commit.
- `ezc env export` exits non-zero on failure but the `env.sh` shim swallows it, so a broken config never makes `cd` unusable; `ezc env` is the gate.
- A missing tool (`sops`, `git`, `gh`) is named with how to install it. `gh` is needed only for creating a GitHub store, opening a join pull request and bot secrets; everything else works without it.

## Testing

`mise run cli:test` is the gate: gofmt check, `go vet ./...`, `go test ./...`.

- Commands are driven in-process with a fake `System`. `EZC_HOME` must be an absolute temporary path; tests never touch the real home.
- Git is real in tests: temporary repos with a bare repo as the remote. `gh` is faked through `System.Exec`.
- `Backend` has a contract test run against the in-memory fake, which most tests use, and against the real sops backend. The sops run is skipped when `sops` is not on PATH and is required in CI.
- The ported packages get the tests fs-config never had: resolver precedence, references and expansion; each check rule; each audit rule and the keyless path.
- TUI screens are golden files, updated with `go test ./internal/tui -run Golden -update`.

## Migration from fs-config

`ezc migrate [--dry-run]`, run in a store or in a consuming repo, and the `ezc-migrate` skill that drives it across repos. It is built but not run as part of this work.

- Store: `.fs-config/` becomes `.ezc/`; `global.yaml` and `global.enc.yaml` become `common.yaml` and `common.enc.yaml`; `recipients.yaml` is converted (`purpose: human` to `kind: user`, `machine` to `kind: bot`); `.sops.yaml` is regenerated; the bot's `repos` seed `repos.yaml`.
- Repo: `.fs-config/` becomes `.ezc/`; `.ezc/env.sh` is written; the `mise.toml` lines are rewritten (`_.source`, `fsconfig env` to `ezc env`); a CI workflow's `SOPS_AGE_KEY` is reported as needing `ezc bot add`.
- Proof: the resolved environment is computed before and after, for every environment, and must be identical in names and values.

`ezc-convert` is the fs-config conversion skill rewritten for ezc: take a repo that reads `.env` files or hard-codes configuration and move it to a manifest and `.ezc/`.

## Parts

Each part has its own implementation plan and leaves a working tool.

1. **Core.** The scaffold in ipm's shape; `manifest`, `resolve`, `check` and `gen` ported with tests; `env`, `env export`, `run`, `manifest`, `codegen`, `list`, `get`, `repo init`. Plaintext files only; the store path comes from `EZC_STORE`.
2. **Encryption and values.** The `Backend` interface, the fake and the sops backend; encrypted files in the resolver; `set`, `unset`, `edit`; `store audit`; `doctor`.
3. **Store and setup.** `paths`, `config`, `gitstore`; `setup`; `store status|pull|push`; the sync indicator.
4. **Access.** `recipients`; `join`; `user` and `bot` commands; rotation-due; `setup --ci`.
5. **Registry.** `repo add|remove|list|check|uses|rewrap`; the `unset` guard; repo re-wrap after access changes.
6. **TUI.** The four views and the wizard.
7. **Migration and docs.** `ezc migrate`, both skills, the docs site.

## Out of scope

- Backends other than SOPS with age.
- Store remotes other than a git remote; GitHub is the only host with create, pull request and secret delivery.
- Generators other than Python.
- Automatic push or pull of value changes.
- Delivering a rotated private key to another person.
- Running the migration of the existing fs-config store and its consuming repos.
