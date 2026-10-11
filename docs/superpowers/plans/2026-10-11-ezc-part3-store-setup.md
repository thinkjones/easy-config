# ezc Part 3: Store and Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ezc setup` takes a machine from nothing to a working encrypted store that is a git repo, every write to the store is one commit, and `ezc store status|pull|push` plus a one-line indicator keep it in step with its remote.

**Architecture:** Three new leaf packages (`config`, `recipients`, `gitstore`) and one orchestrating package (`setup`), all reached from `internal/cli` and all running programs only through `proc.Runner`. The `Backend` interface grows the three methods setup needs (`NewKey`, `PublicKey`, `WritePolicy`); the rest of the spec's access methods wait for part 4. The sync indicator and the commit-after-write live in `internal/cli/sync.go` and are called by the commands that touch the store.

**Tech Stack:** Go 1.26, Cobra v1.10.2, yaml.v3, `github.com/BurntSushi/toml` v1.6.0 (the version ipm uses), `filippo.io/age` v1.2.1 (now a runtime dependency), real `git` and `sops` 3.13.3 in tests.

**Spec:** `docs/superpowers/specs/2026-10-10-ezc-design.md` (sections "On a machine", "Commands › Machine", "Commands › Store", "Store sync", "Errors", "Testing"; part 3 of "Parts").

## Global Constraints

- Module `github.com/thinkjones/easy-config/cli`; every command is `new<Name>Cmd(sys System)` in `cli/internal/cli`, registered in `newRootCmd`; argument checks use `noArgs`, `exactArgs`, `minArgs`, `maxArgs`, `verbs`. Exit codes: 0 success, 1 failure, 2 a malformed command line (`UsageError`).
- Commands reach the outside world only through `System`. `git`, `gh`, `brew` and `sops` run only through `proc.Runner`.
- **No value in output.** No command prints a value except `ezc env export`, `ezc run` (to the child) and `ezc get`. This covers stdout, stderr, error text, commit messages and anything passed through from `git`, `gh` or `sops`. A commit message holds names and file names only.
- **The private key is never printed**, never in a process's arguments or environment that ezc sets, and is written to exactly one place: the key file, mode `0600`, in a folder created `0700`. An existing key file is never rewritten, truncated or appended to.
- Only `cli/internal/backend/sops/` and `cli/internal/cli/scope.go` may contain `sops`, `.sops.yaml`, `SOPS_` or `ENC[` outside tests and docs. `cli/internal/setup` names the tool to install through a value the backend supplies.
- Only `cli/internal/gitstore/` builds `git` command lines. Git always runs with `GIT_TERMINAL_PROMPT=0` added to its environment, so nothing waits for a password.
- **Tests never read the real home, the real environment, a real key or the user's git config.** Git in tests runs with `HOME` set to a temporary folder, `GIT_CONFIG_GLOBAL` set to an empty temporary file and `GIT_CONFIG_NOSYSTEM=1`. Keys are generated in the test. Nothing touches `~/.config/sops`. No test uses the network; a remote is a bare repo in a temporary folder. `gh` and `brew` are always faked.
- `ezc env export` never fetches, never prints the indicator and never prints to stderr on success.
- Nothing in this part deletes or overwrites a store, a key or a config that already exists; setup re-run changes only what is missing.
- A store that is not a git repo (an `EZC_STORE` pointing at a plain folder) keeps working as in part 2: no fetch, no indicator, no commit, no error.
- `grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/` prints nothing.
- Markdown prose is one line per paragraph. User docs (`README.md`, `AGENTS.md`, `CLAUDE.md`) change in the same commit as the behaviour. `docs/superpowers/` is never edited to match the code.
- The gate is `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops`, then `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run schema:check`. Do not run `mise trust`, push, or switch branches.

## Decisions this plan makes beyond the spec

- **`recipients.yaml` starts here, with one entry.** Setup must write a policy, and the policy comes from the registry, so part 3 creates the `recipients` package with load, save and the key list. Requests, the change sequence, rotation-due and every `user`/`bot` command stay in part 4.
- **`ezc setup --store` clones and stops at the door.** `ezc join` is part 4. When the machine's key is not a recipient of the cloned store, setup says so and prints the public key to hand to someone with access. It exits 0: the machine is set up.
- **`ezc setup --ci` and installing the skills are not in this part** (parts 4 and 7).
- **Setup is a plain command here.** On a terminal it asks for what no flag supplied, one line at a time on stdin; the TUI wizard is part 6.
- **A write commits only the files it touched.** Hand edits left in the store are never swept into a commit named after something else; they show as `uncommitted changes` until the user commits or discards them with git.
- **`ezc setup` refuses to run with `EZC_STORE` set.** Setup manages `$EZC_HOME/store`.

## Review Focus

1. **A store that is not a git repo** (`EZC_STORE=/some/folder`): `set -g`, `env`, `list` and `doctor` work exactly as in part 2, with no indicator and no commit. Pinned in Tasks 7 and 8.
2. **The remote cannot be reached**: no command waits longer than 3 seconds for it or fails because of it; the cached state is used and its age is said once. Pinned in Tasks 5 and 7.
3. **Setup run again** on a machine with a key, a store and a config: exits 0, changes no file's bytes, never rewrites the key. Pinned in Task 10.
4. **A machine with no git identity** (`user.name` and `user.email` unset): setup and `set -g` still commit. Pinned in Tasks 5 and 10.
5. **A remote with no branch yet, or a store whose branch is not `main`**: status says ahead by every local commit, push creates the branch, and a store cloned on `master` syncs against `origin/master`. Pinned in Task 5.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `cli/internal/proc/proc.go` | adds `Command.Timeout` and `ErrTimeout` |
| `cli/internal/paths/paths.go` | adds `State()` |
| `cli/internal/config/config.go` | `config.toml`: load, save |
| `cli/internal/backend/backend.go`, `fake.go`, `backendtest/contract.go`, `sops/sops.go`, `sops/key.go` | `NewKey`, `PublicKey`, `WritePolicy` |
| `cli/internal/recipients/recipients.go` | `recipients.yaml`: load, save, keys |
| `cli/internal/gitstore/gitstore.go` | init, clone, remote, commit, fetch, status, push |
| `cli/internal/gitstore/pull.go` | pull: fast-forward, rebase, the encrypted-file rule |
| `cli/internal/valuefile/changed.go` | `ChangedNames` |
| `cli/internal/cli/sync.go` | `state.json`, the fetch rule, the indicator, the write guard, the commit |
| `cli/internal/cli/storecmd.go` | adds `status`, `pull`, `push` |
| `cli/internal/setup/setup.go` | the steps as plain functions |
| `cli/internal/cli/setupcmd.go` | flags, prompts, output |
| `cli/internal/doctor/doctor.go`, `cli/internal/cli/doctorcmd.go` | config, recipient and sync items |
| `cli/cmd/ezc/main.go`, `cli/cmd/ezc/store_test.go` | wiring; end-to-end tests |

---

### Task 1: Timeouts, the clock and the state path

**Files:**
- Modify: `cli/internal/proc/proc.go`, `cli/internal/paths/paths.go`, `cli/internal/paths/paths_test.go`, `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go` (the fake `System`), `cli/cmd/ezc/main.go`, `cli/cmd/ezc/exec_test.go`

**Interfaces:**
- Produces: `proc.Command.Timeout time.Duration` (0 means none); `var proc.ErrTimeout = errors.New("timed out")`; `paths.Paths.State() string` (`<Home>/state.json`); `System.Now func() time.Time`; `System.StderrIsTerminal func() bool`.

- [ ] **Step 1: Write the failing tests.** In `exec_test.go`: `TestExecCommandTimeout` runs `sleep 5` with `Timeout: 200 * time.Millisecond`, asserts `errors.Is(err, proc.ErrTimeout)` and that it returned in under 2 seconds; `TestExecCommandNoTimeout` runs `true` with `Timeout: 0` and asserts no error. In `paths_test.go`: `State()` is `filepath.Join(home, "state.json")`.
- [ ] **Step 2: Run them; they fail to compile.** `cd cli && go test ./cmd/ezc ./internal/paths`
- [ ] **Step 3: Implement.** `execCommand` uses `exec.CommandContext` when `Timeout > 0` and returns an error wrapping `proc.ErrTimeout` when the context's deadline passed. `main.go` sets `Now: time.Now` and `StderrIsTerminal` with `term.IsTerminal(int(os.Stderr.Fd()))`. The fake `System` in `cli_test.go` gets a fixed `Now` (2026-10-11T12:00:00Z, settable per test) and `StderrIsTerminal` returning false.
- [ ] **Step 4: Run the gate.** Expected: pass.
- [ ] **Step 5: Commit.** `feat(proc): a command can time out; System has a clock`

### Task 2: `config.toml`

**Files:**
- Create: `cli/internal/config/config.go`, `cli/internal/config/config_test.go`
- Modify: `cli/go.mod`, `cli/go.sum` (`github.com/BurntSushi/toml v1.6.0`)

**Interfaces:**
- Produces:

```go
type Config struct {
	User      string            `toml:"user"`
	Backend   string            `toml:"backend"`
	KeyFile   string            `toml:"key_file"`
	Store     Store             `toml:"store"`
	Checkouts map[string]string `toml:"checkouts,omitempty"`
}
type Store struct {
	Remote string `toml:"remote,omitempty"`
}
// Load reads path. found is false, with no error, when the file does not exist.
func Load(path string) (c Config, found bool, err error)
// Save writes c to path atomically, creating the folder.
func Save(path string, c Config) error
```

- [ ] **Step 1: Write the failing tests.** `TestRoundTrip`: save the spec's example (`user = "gene"`, `backend = "sops"`, a `key_file`, `[store] remote`, one `[checkouts]` entry whose key contains `/`), load it, deep-equal. `TestLoadAbsent`: `found == false`, `err == nil`. `TestLoadInvalid`: a syntax error is an error that names the path. `TestLoadRelativeKeyFile`: a `key_file` that is not absolute is an error naming the path and the key. `TestSaveLocalOnly`: with an empty `Store.Remote` the file has no `remote` line. `TestSaveCreatesFolder`. `TestSaveLeavesNoTempFile` after a successful save.
- [ ] **Step 2: Run; fail to compile.** `go test ./internal/config`
- [ ] **Step 3: Implement.** A key the struct does not know is ignored on load.
- [ ] **Step 4: Run; pass.**
- [ ] **Step 5: Commit.** `feat(config): load and save config.toml`

### Task 3: The backend makes keys and writes its policy

**Files:**
- Modify: `cli/internal/backend/backend.go`, `cli/internal/backend/fake.go`, `cli/internal/backend/fake_test.go`, `cli/internal/backend/backendtest/contract.go`, `cli/internal/backend/sops/sops.go`, `cli/internal/backend/sops/sops_test.go`
- Create: `cli/internal/backend/sops/key.go`

**Interfaces:**
- Consumes: `backend.Backend` as built in part 2.
- Produces, added to `Backend`:

```go
// NewKey creates a keypair: the public half, and the private text to write to a key file.
NewKey() (public string, private []byte, err error)
// PublicKey reads the public half of the first key in keyFile. A file that is absent or holds no key wraps ErrNoKey.
PublicKey(keyFile string) (string, error)
// WritePolicy writes the backend's policy file in dir for these recipients, replacing one that is there.
// No recipients is an error.
WritePolicy(dir string, recipients []string) error
// ToolHint is one line saying how to install the backend's tool; "" when it needs none.
ToolHint(goos string) string
// BrewFormula is the Homebrew formula that installs the tool; "" when there is none.
BrewFormula() string
```

- The sops policy file is exactly this, with the keys joined by `,` and no spaces, in the order given:

```yaml
# Generated by ezc from recipients.yaml. Do not edit.
creation_rules:
  - path_regex: \.enc\.yaml$
    age: <key1>,<key2>
```

- The private text `NewKey` returns for sops is `# public key: <public>\n<AGE-SECRET-KEY-1…>\n`.
- `ToolHint` for sops: on `darwin` and `linux`, `Install it with `brew install sops`, or download it from https://github.com/getsops/sops/releases.`; on `windows`, `Download it from https://github.com/getsops/sops/releases.` `BrewFormula` for sops is `sops`.

- [ ] **Step 1: Add the contract tests** to `backendtest/contract.go`, run for the fake and for sops: `NewKey` twice gives different public halves; the private text written to a file gives the same public half back from `PublicKey`; `PublicKey` of a missing file and of an empty file is `errors.Is(err, backend.ErrNoKey)` and the error names the file; `WritePolicy(dir, nil)` is an error; after `WritePolicy(dir, []string{pub})` `HasPolicy(dir)` is true; writing the same recipients twice leaves identical bytes; writing a second list replaces the first.
- [ ] **Step 2: Add the sops tests** in `sops_test.go`: the policy bytes equal the block above for two keys; `PublicKey` of a file holding two identities returns the first; of a file holding comments and blank lines only, `ErrNoKey`; real sops (skipped without it unless `EZC_REQUIRE_SOPS=1`): `NewKey`, write the private text to a temporary key file, `WritePolicy`, then `Set` and `Decrypt` round-trip with a backend whose `KeyFile` is that file.
- [ ] **Step 3: Run; fail to compile.** `go test ./internal/backend/...`
- [ ] **Step 4: Implement.** sops: `age.GenerateX25519Identity` and `age.ParseIdentities` in `key.go`; the policy is written atomically with `writeFileAtomic`. Fake: `NewKey` returns `fake1<16 random hex>` and the private text `FAKE-KEY-<same hex>\n`; `PublicKey` reads that back; `WritePolicy` writes `.fakepolicy` holding one recipient per line; `ToolHint` returns `Install the fake tool.` and `BrewFormula` returns `fake-tool`, so setup's tests can see both used.
- [ ] **Step 5: Run the gate; pass.**
- [ ] **Step 6: Commit.** `feat(backend): create a key, read its public half, write the policy`

### Task 4: The recipients registry

**Files:**
- Create: `cli/internal/recipients/recipients.go`, `cli/internal/recipients/recipients_test.go`

**Interfaces:**
- Produces:

```go
const File = "recipients.yaml"

type Recipient struct {
	Key     string   `yaml:"key"`
	Kind    string   `yaml:"kind"` // user | machine | bot
	Owner   string   `yaml:"owner,omitempty"`
	Added   string   `yaml:"added"` // YYYY-MM-DD
	AddedBy string   `yaml:"addedBy"`
	Repos   []string `yaml:"repos,omitempty"`
}
type Registry struct {
	Recipients map[string]Recipient `yaml:"recipients"`
}
// ValidName reports whether s can name a recipient: ^[a-z0-9][a-z0-9-]*$
func ValidName(s string) bool
// Load reads dir/recipients.yaml. found is false, with no error, when it does not exist.
func Load(dir string) (r Registry, found bool, err error)
// Save writes it atomically, names in sorted order.
func (r Registry) Save(dir string) error
// Keys is every recipient's key, in order of name.
func (r Registry) Keys() []string
// NameOf is the name of the recipient with this key; "" when there is none.
func (r Registry) NameOf(key string) string
```

- [ ] **Step 1: Write the failing tests.** Round-trip of the spec's three-entry example (user, machine with owner, bot with repos); saved names are in sorted order whatever the map's order; `Keys()` follows name order; `NameOf` hit and miss; `Load` of a missing file is `found == false`; load errors, each naming the file and the entry: a kind other than the three, an empty key, a name that fails `ValidName`, two entries with the same key; `ValidName` table: `gene`, `gene-mini`, `ci2` pass; `Gene`, `-x`, `a b`, `a_b`, `""` fail.
- [ ] **Step 2: Run; fail to compile.** `go test ./internal/recipients`
- [ ] **Step 3: Implement.** The package imports no other ezc package.
- [ ] **Step 4: Run; pass.**
- [ ] **Step 5: Commit.** `feat(recipients): the registry file`

### Task 5: `gitstore`: a store is a git repo

**Files:**
- Create: `cli/internal/gitstore/gitstore.go`, `cli/internal/gitstore/gitstore_test.go`, `cli/internal/gitstore/gittest/gittest.go`

**Interfaces:**
- Consumes: `proc.Runner`, `proc.Command.Timeout`, `proc.ErrTimeout`.
- Produces:

```go
type Git struct {
	Run     proc.Runner
	Environ []string // the process environment; GIT_TERMINAL_PROMPT=0 is added
}
type Author struct{ Name string } // used only when git has no identity of its own

type State int
const (InSync State = iota; Ahead; Behind; Diverged; Uncommitted)

type Status struct {
	Branch        string
	Remote        string // origin's URL; "" for a local-only store
	Ahead, Behind int    // against origin/<Branch>; with no such branch on the remote, Ahead is every local commit
	Dirty         bool
}
// State is Uncommitted when Dirty, else Diverged, Behind, Ahead or InSync from the counts.
func (s Status) State() State

var ErrNotInstalled = errors.New("git is not installed")
var ErrUnreachable  = errors.New("the remote could not be reached")

func (g Git) IsRepo(dir string) bool                 // dir is the top of a work tree
func (g Git) Init(dir string) error                  // branch main; creates dir
func (g Git) Clone(source, dir string) error
func (g Git) Remote(dir string) (string, error)      // "" when there is no origin
func (g Git) SetRemote(dir, url string) error        // adds or replaces origin
// Commit commits exactly these paths (relative to dir; additions, changes and deletions). done is false when none of them changed.
func (g Git) Commit(dir, message string, a Author, paths ...string) (done bool, err error)
// Fetch updates what is known of origin. A timeout or a network failure wraps ErrUnreachable.
func (g Git) Fetch(dir string, timeout time.Duration) error
// Status reads the local state only; it never uses the network.
func (g Git) Status(dir string) (Status, error)
// Push publishes the branch, creating it on the remote and setting the upstream when needed. pushed is the number of commits sent.
func (g Git) Push(dir string) (pushed int, err error)
```

- `gittest` (test support): `func Runner(t *testing.T) proc.Runner` runs the real `git` with an isolated environment as the Global Constraints require and fails the test for any other program; `func Bare(t *testing.T) string` makes a bare repo; `func NoIdentity(t)` and `func WithIdentity(t)` variants of the environment. Tests skip when `git` is not on PATH unless `EZC_REQUIRE_GIT=1`; add that variable to the `cli:test:sops` task in `mise.toml`.

- [ ] **Step 1: Write the failing tests** (real git, bare remote in a temporary folder):
  - `TestInitCommitStatus`: after `Init` and one `Commit`, status is `InSync`, branch `main`, remote `""`, not dirty.
  - `TestCommitOnlyNamedPaths`: two files changed, one named: `done == true`, the other stays modified and `Status().Dirty` is true; `git show --stat HEAD` lists one file.
  - `TestCommitNothingChanged`: `done == false`, no new commit.
  - `TestCommitDeletion`: a named path that was removed is committed as a deletion.
  - `TestCommitWithoutIdentity` (Review Focus 4): with no `user.name`/`user.email`, the commit succeeds and its author name is `Author.Name`; with an identity configured, git's own is used.
  - `TestAheadBehindDiverged`: two clones of one bare remote; each state reached and reported with exact counts after `Fetch`.
  - `TestStatusNeverFetches`: the other clone pushes; `Status` here still says in sync until `Fetch` runs.
  - `TestEmptyRemote` (Review Focus 5): a remote with no branch: `Ahead` equals the local commit count; `Push` returns that count and afterwards the state is `InSync`.
  - `TestBranchNotMain` (Review Focus 5): a store cloned from a remote whose branch is `master` reports against `origin/master`.
  - `TestFetchUnreachable` (Review Focus 2): the remote's folder is removed; `Fetch` is `errors.Is(err, ErrUnreachable)`. With a runner that sleeps past the timeout, the same.
  - `TestFetchNoRemote`: returns nil without running git.
  - `TestNotInstalled`: a runner whose error is `exec.ErrNotFound` gives `errors.Is(err, ErrNotInstalled)`.
  - `TestErrorsHoldNoFileContent`: a failing commit's error does not contain a marker string placed in the file's content.
- [ ] **Step 2: Run; fail to compile.** `go test ./internal/gitstore/...`
- [ ] **Step 3: Implement.** Every git call goes through one private `run(dir string, timeout time.Duration, args ...string)` that sets `Dir`, the environment and `LC_ALL=C`. Errors carry git's first line of stderr, never its stdout. Status uses `git status --porcelain=v2 --branch` and `git rev-list --left-right --count`.
- [ ] **Step 4: Run the gate; pass.**
- [ ] **Step 5: Commit.** `feat(gitstore): init, clone, commit, fetch, status, push`

### Task 6: Pull, and which names a replaced file lost

**Files:**
- Create: `cli/internal/gitstore/pull.go`, `cli/internal/gitstore/pull_test.go`, `cli/internal/valuefile/changed.go`, `cli/internal/valuefile/changed_test.go`
- Modify: `cli/internal/backend/sops/sops_test.go` (one real-sops test)

**Interfaces:**
- Consumes: `Git`, `Status` from Task 5.
- Produces:

```go
// Replaced is a file changed here and on the remote, for which the remote's version was kept.
type Replaced struct {
	Path        string // relative to the store's root
	Base, Local []byte // its content at the common ancestor (nil when it did not exist) and here before the pull
}
type PullResult struct {
	Commits  int        // commits received
	Replaced []Replaced
}
var ErrConflict = errors.New("changed here and on the remote")
var ErrDirty    = errors.New("uncommitted changes")
var ErrNoRemote = errors.New("the store has no remote")

// Pull fetches, then fast-forwards, or rebases the local commits onto the remote's. During the rebase a
// conflicted file for which keepRemote(path) is true takes the remote's version; any other conflict
// aborts the rebase, leaves the store as it was, and returns an error wrapping ErrConflict that names the file.
func (g Git) Pull(dir string, timeout time.Duration, keepRemote func(path string) bool) (PullResult, error)
```

```go
// ChangedNames is the top-level names whose scalar text differs between old and new, or that only one of
// them holds, in new's order and then old's. Entries that are not scalars are ignored. old may be nil.
func ChangedNames(old, new []byte) ([]string, error)   // package valuefile
```

- [ ] **Step 1: Write the failing tests.**
  - `TestPullFastForward`: behind by 2: `Commits == 2`, no `Replaced`, state `InSync`.
  - `TestPullRebasesPlaintext`: different plaintext files changed on each side: both changes present, state `Ahead` by 1.
  - `TestPullKeepsRemoteForEncrypted`: `x.enc.yaml` changed on both sides with `keepRemote` true for it: the file equals the remote's version, `Replaced` has one entry whose `Local` is this side's old content and `Base` the ancestor's, no rebase is left in progress, and a plaintext change made in the same local commit survives.
  - `TestPullLocalCommitBecomesEmpty`: the local commit touched only the replaced file: the pull finishes (the commit is dropped), state `InSync`.
  - `TestPullConflictAborts`: the same line of a plaintext file changed on both sides: `errors.Is(err, ErrConflict)`, the error names the file, `HEAD` is the commit it was before, the tree is clean, no rebase is in progress.
  - `TestPullDirty`: `errors.Is(err, ErrDirty)`, nothing fetched into the work tree.
  - `TestPullNoRemote`: `errors.Is(err, ErrNoRemote)`.
  - `TestPullUnreachable`: `errors.Is(err, ErrUnreachable)`, store unchanged.
  - `TestPullNothingToDo`: `Commits == 0`.
  - `ChangedNames` table: a changed value, an added name, a removed name, an unchanged file (empty), `old == nil` (every name), a mapping entry named `sops` that differs (ignored), invalid YAML (error).
  - Real sops, in `sops_test.go`: set `A` and `B`, keep the bytes; set `B` to a new value; `valuefile.ChangedNames(before, after)` is exactly `["B"]`. This pins that the tool leaves an unchanged value's stored text alone.
- [ ] **Step 2: Run; fail to compile.**
- [ ] **Step 3: Implement.** `git rebase origin/<branch>` with `GIT_EDITOR=true`; on a stop, list unmerged paths (`git diff --name-only --diff-filter=U`); when every one satisfies `keepRemote`, `git checkout --ours -- <path>` (during a rebase "ours" is the remote's side), `git add`, then `git rebase --continue`, or `git rebase --skip` when the commit has become empty; otherwise `git rebase --abort`. `Base` and `Local` are read with `git show` before the rebase starts.
- [ ] **Step 4: Run the gate; pass.**
- [ ] **Step 5: Commit.** `feat(gitstore): pull rebases, and keeps the remote's version of an encrypted file`

### Task 7: The sync indicator, and the key file from the config

**Files:**
- Create: `cli/internal/cli/sync.go`, `cli/internal/cli/sync_test.go`
- Modify: `cli/internal/cli/scope.go`, `cli/internal/cli/env.go`, `cli/internal/cli/values.go`, `cli/internal/cli/cli_test.go` (the fake `Exec` routes `git` to `gittest.Runner`)

**Interfaces:**
- Consumes: `gitstore.Git`, `Status`, `State`; `config.Load`; `paths.State()`; `System.Now`, `System.StderrIsTerminal`.
- Produces, in `sync.go`:

```go
// storeSync is what a command learns about the store before it uses it.
type storeSync struct {
	Git      bool            // the store is a git repo
	Status   gitstore.Status
	Fetched  time.Time       // the last fetch that worked; zero when never
	Offline  bool            // a fetch was tried just now and failed
	Requests []string        // names in the store's requests folder, sorted
}
// syncStore fetches when the store has a remote and the last attempt is older than maxAge, then reads the status.
// It never fails a command: any error leaves Git false or the status as last known.
func syncStore(sys System, maxAge time.Duration) storeSync
// indicate prints the one line to stderr when there is something to say.
func indicate(sys System, s storeSync)
// touchStore is syncStore with the 10 minute rule followed by indicate. Commands that read or write the store call it once.
func touchStore(sys System) storeSync
// loadConfig is the machine's config; the zero Config when there is none.
func loadConfig(sys System) (config.Config, error)
```

- `state.json` holds `{"attempted": "<RFC 3339>", "fetched": "<RFC 3339>"}`; a missing or unreadable file means never.
- The fetch timeout is 3 seconds; the age rule is 10 minutes since the last attempt.
- The lines, exactly (with `1 commit` in the singular):
  - `store: 2 commits ahead of origin — run ezc store push`
  - `store: 2 commits behind origin — run ezc store pull`
  - `store: diverged from origin (1 ahead, 2 behind) — run ezc store pull`
  - `store: uncommitted changes in <store path>`
  - with requests, ` · 1 access request pending: sam` or ` · 2 access requests pending: kim, sam` is appended; in sync with requests the line is `store: 1 access request pending: sam`
  - when `Offline`: a second line `store: origin could not be reached; last checked 2 hours ago` (or `never checked`). Ages are `just now`, `N minutes ago`, `N hours ago`, `N days ago`.
- Colour: only when `sys.StderrIsTerminal()` and `NO_COLOR` is unset. Amber (`\x1b[33m`) for ahead, for requests alone and for the offline line; red (`\x1b[31m`) for the rest; reset `\x1b[0m`.
- `backendFor` and `keySource` in `scope.go` use the config's `key_file` in place of the default key file when the config has one; the environment's `SOPS_AGE_KEY` and `SOPS_AGE_KEY_FILE` still win.
- `touchStore` is called by `env` (not `env export`), `run`, `list` and `get`; the writers get it in Task 8, the `store` commands use `syncStore` in Task 9 and `doctor` in Task 11.

- [ ] **Step 1: Write the failing tests** in `sync_test.go` (real git through the fake `System`):
  - one test per line above, asserting stderr's exact text, including singular and plural, requests appended, and requests alone.
  - `TestInSyncPrintsNothing`.
  - `TestFetchOnlyWhenStale`: `state.json` 9 minutes old: no fetch (the other clone's push is not seen); 11 minutes old: fetch, the line says behind, and `attempted` is rewritten to `Now`.
  - `TestOfflineUsesCachedState` (Review Focus 2): remote removed: the command exits 0, stderr has the last known state line and the offline line with the age, and a second run inside 10 minutes prints no offline line and runs no fetch.
  - `TestFetchTimeoutIsThreeSeconds`: the recorded `proc.Command` for `fetch` has `Timeout == 3*time.Second`.
  - `TestExportNeverFetchesOrPrints`: behind a remote with a stale `state.json`, `ezc env export` leaves stderr empty and runs no `git`.
  - `TestPlainFolderStore` (Review Focus 1): `EZC_STORE` at a folder that is not a git repo: `env`, `list`, `get` print no store line and run no `git fetch`.
  - `TestLocalOnlyStore`: a git store with no remote: no fetch, no line when clean.
  - `TestColour`: with a terminal, the behind line is wrapped in red and the ahead line in amber; with `NO_COLOR=1`, no escape codes; without a terminal, none.
  - `TestGetStdoutUntouched`: `ezc get NAME` while behind: stdout is the value and a newline only.
  - `TestKeyFileFromConfig`: with `key_file` in the config, the sops backend's options carry it; with `SOPS_AGE_KEY_FILE` set, that one.
  - `TestBrokenConfigIsAnError`: an invalid `config.toml` fails `ezc env` with the path in the message.
- [ ] **Step 2: Run; fail.** `go test ./internal/cli -run 'Sync|Fetch|Offline|Export|Plain|Local|Colour|KeyFile|Config'`
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the gate; pass.**
- [ ] **Step 5: Commit.** `feat(cli): say when the store and its remote differ`

### Task 8: A write to the store is one commit

**Files:**
- Modify: `cli/internal/cli/sync.go`, `cli/internal/cli/setcmd.go`, `cli/internal/cli/editcmd.go`, `cli/internal/cli/setcmd_test.go`, `cli/internal/cli/editcmd_test.go`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: `touchStore`, `storeSync`, `gitstore.Git.Commit`, `loadConfig`.
- Produces, in `sync.go`:

```go
// guardStoreWrite runs touchStore and refuses the write when the store is behind or diverged.
func guardStoreWrite(sys System) (storeSync, error)
// commitStore commits the named files of the store's .ezc (base names) with message. It does nothing for a store that is not a git repo.
func commitStore(sys System, s storeSync, message string, files ...string) error
```

- The refusal: `the store is 2 commits behind origin. Run `ezc store pull` first: a change made now could not be merged.` (for diverged: `the store has diverged from origin. Run …`). Exit 1; nothing is written.
- Commit messages: `set NAME (common.enc.yaml)`; for a name declared in the same step `set NAME (manifest.yaml, common.yaml)`; `unset NAME (common.yaml, common.enc.yaml)` listing only the files that changed, with `manifest.yaml` first under `--undeclare`; `edit env.prod.yaml`. The author fallback is the config's `user`, else `ezc`.
- When the write succeeded and the commit failed: `the change was written but could not be committed: <git's first line>. Commit it in <store path> with git.` Exit 1.
- These apply only with `-g`, or when the working repo is the store itself. A write to any other repo never touches git.

- [ ] **Step 1: Write the failing tests.**
  - `TestSetGlobalCommits`: one new commit with the exact message; the tree is clean afterwards; for a secret, the message and `git show --stat` hold the name and file and `git log -p` does not hold the value.
  - `TestSetGlobalNewNameOneCommit`: declaration and value in one commit, message as above.
  - `TestUnsetGlobalCommits`, `TestUnsetUndeclareCommits`: exact messages.
  - `TestEditGlobalCommits`; `TestEditGlobalNoChangeNoCommit`.
  - `TestSetFailureCommitsNothing`: a value write that fails (locked fake backend) leaves no commit and a clean tree.
  - `TestHandEditNotSwept`: a hand-edited `common.yaml` stays uncommitted after `ezc set -g OTHER --env prod …`; stderr carries the uncommitted line.
  - `TestWriteRefusedWhileBehind`, `TestWriteRefusedWhileDiverged`: exact message, exit 1, file bytes unchanged, no commit. `TestWriteAllowedWhileAhead`.
  - `TestRepoWriteNeverCommits`: `ezc set NAME v` in a repo that is itself a git repo makes no commit there.
  - `TestPlainFolderStoreWrites` (Review Focus 1): `EZC_STORE` at a plain folder: `set -g` writes, exits 0, runs no `git commit`.
  - `TestCommitMessageNeverHoldsAValue`: a plaintext `set -g NAME value` commit message does not contain the value.
- [ ] **Step 2: Run; fail.**
- [ ] **Step 3: Implement.** `set`, `unset` and `edit` call `guardStoreWrite` before their first write and `commitStore` after their last, with the files they changed.
- [ ] **Step 4: Update the docs.** README: writes to the store are commits; the refusal while behind. CLAUDE.md sharp edge: `commitStore` commits only named files; a hand edit is never swept in.
- [ ] **Step 5: Run the gate; pass.**
- [ ] **Step 6: Commit.** `feat(store): every write to the store is one commit`

### Task 9: `ezc store status`, `pull`, `push`

**Files:**
- Modify: `cli/internal/cli/storecmd.go`, `README.md`, `AGENTS.md`
- Create: `cli/internal/cli/storecmd_test.go`

**Interfaces:**
- Consumes: `syncStore`, `indicate`, `gitstore.Git.Pull`, `Push`, `valuefile.ChangedNames`, `resolve.IsEncrypted`.

- `ezc store status` fetches whatever the age of `state.json` (10 second timeout) and prints, aligned:

```
store     /Users/gene/Library/ezc/store
remote    git@github.com:thinkjones/config.git
state     2 commits behind origin
fetched   just now
requests  none
```

  `remote` is `none (local only)` without one, and then `fetched` is `n/a`. `state` is one of `in sync`, `N commit(s) ahead of origin`, `N commit(s) behind origin`, `diverged from origin (A ahead, B behind)`, `uncommitted changes`. A store that is not a git repo prints `store` and `state     not a git repo, so it is not synced`. Exit 0 in every state; no indicator line is printed by this command.
- `ezc store pull`: prints `pulled 3 commits from origin` or `already up to date`. For each replaced file, on stderr: `! env.prod.enc.yaml was changed here and on the remote. The remote's version was kept; set these again:` followed by one line per name, `  ezc set -g API_KEY --env prod` (no `--env` for a common file), and exits 1. `keepRemote` is true for a path whose base name `resolve.IsEncrypted`. `ErrConflict`: `<file> was changed here and on the remote in the same place, so nothing was pulled. Resolve it in <store path> with git (git pull --rebase).` `ErrDirty`: `the store has uncommitted changes in <store path>. Commit or discard them with git, then pull.` `ErrNoRemote`: `the store has no remote, so there is nothing to pull. `ezc setup --new-github owner/repo` gives it one.` `ErrUnreachable`: `origin could not be reached.` Pull writes `state.json`.
- `ezc store push`: prints `pushed 2 commits to origin` or `nothing to push`. Refused, exit 1: behind or diverged (`the store is behind origin. Run `ezc store pull` first.`), dirty (the `ErrDirty` sentence ending `then push.`), no remote.
- `verbs` lists `status, pull, push or audit`.

- [ ] **Step 1: Write the failing tests** for every output and refusal above, with exact text; `TestPullReplacedNamesTheKeys` uses the fake backend's file format with two clones that each `ezc set -g` a different name in the same encrypted file and asserts the `ezc set -g` line names only the local one, with `--env` when the file is an environment's; `TestPullOutputHoldsNoValue`; `TestStatusForcesFetch` (a fresh `state.json` does not stop it); `TestStatusPlainFolder`.
- [ ] **Step 2: Run; fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Update README (a "Syncing the store" section) and the AGENTS.md map.**
- [ ] **Step 5: Run the gate; pass.**
- [ ] **Step 6: Commit.** `feat(store): status, pull and push`

### Task 10: `ezc setup`

**Files:**
- Create: `cli/internal/setup/setup.go`, `cli/internal/setup/setup_test.go`, `cli/internal/cli/setupcmd.go`, `cli/internal/cli/setupcmd_test.go`
- Modify: `cli/internal/cli/cli.go` (register), `README.md`, `AGENTS.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: Tasks 2–5; `backend.Backend.Check`, `NewKey`, `PublicKey`, `WritePolicy`, `ToolHint`.
- Produces, in `setup`:

```go
type Env struct {
	Git      gitstore.Git
	Backend  backend.Backend
	Run      proc.Runner
	LookPath func(string) (string, error)
	Now      func() time.Time
	Paths    paths.Paths
	GOOS     string
}
// Key returns the public half of the key in keyFile, creating the file (0600, its folder 0700) when it does not exist.
func Key(e Env, keyFile string) (public string, created bool, err error)
// NewStore creates the store: git init, .ezc/manifest.yaml, recipients.yaml with name as its one user, the policy, one commit.
func NewStore(e Env, name, public string) error
// Publish creates the private GitHub repo slug from the store and pushes to it; it returns the remote's URL.
func Publish(e Env, slug string) (remote string, err error)
// CloneStore clones source into the store's path; it returns the remote's URL.
func CloneStore(e Env, source string) (remote string, err error)
// Recipient is the name the store's registry gives this key; "" when it is not a recipient.
func Recipient(e Env, public string) (string, error)
```

- Flags: `--new-local`, `--new-github owner/repo`, `--store <owner/repo | URL | path>`, `--name <name>`, `--yes`. At most one of the first three (`UsageError` otherwise). `ezc setup` takes no arguments.
- Order of work, stopping at the first failure with nothing later attempted: tools → name → key → store → config.
- **Tools.** `git` must be on PATH (`git is not installed. Install it and run ezc setup again.`). `Backend.Check()`; on `ErrNoTool`: with `brew` on PATH and either `--yes` or a `y` at the prompt `Install it now with brew? [y/N]`, run `brew install <Backend.BrewFormula()>` and check again; otherwise print the backend's error and `Backend.ToolHint(GOOS)` and exit 1. Setup itself never names the tool: the `✓` line shows `Backend.Name()`. `gh` is required only for `--new-github` and for a `--store owner/repo` (`gh is not installed; it is needed to create a GitHub repo. https://cli.github.com`).
- **Name.** `--name`, else the config's `user`, else on a terminal the prompt `Your name in this store [<$USER lowercased>]: `, else `$USER` lowercased. It must pass `recipients.ValidName` (`UsageError` naming the rule).
- **Key.** The file is the config's `key_file`, else `keyFile(sys)` from `scope.go`; with no home folder, an error saying to set `HOME`. Existing: reused, bytes untouched. Created: stdout gets the backup notice below; on a terminal without `--yes` it waits for Enter.
- **Store.** Exists already (a `.ezc` folder in the store path): kept; with `--new-github` and no remote it is published; with `--store` naming a different remote, error `a store is already set up at <path> (remote <url>). Move it away to set up another.` Absent: `--new-local` → `NewStore`; `--new-github` → `NewStore` then `Publish`; `--store` → `CloneStore`; none of the three on a terminal → the prompt `Store: [1] new, on this machine  [2] new, on GitHub  [3] an existing one: ` and a follow-up for the repo; none without a terminal → `UsageError`: `ezc setup: say --new-local, --new-github owner/repo or --store <repo>.`
- `Publish` runs `gh repo create <slug> --private --source <store> --remote origin --push`. `CloneStore` runs `gh repo clone <slug> <path>` for an `owner/repo` and `git clone` for anything else (a URL or a path). A cloned folder with no `.ezc/manifest.yaml` is removed again and reported: `<source> is not an ezc store (it has no .ezc/manifest.yaml).`
- `NewStore` writes `manifest.yaml` as `required: {}\noptional: {}\n`, the registry entry `{key, kind: user, added: <today>, addedBy: <name>}`, the policy through `WritePolicy`, and commits `setup: create the store` with every file it wrote.
- **Config.** Saved with `user`, `backend` (`Backend.Name()`), `key_file` and `store.remote`; existing `checkouts` are kept.
- With `EZC_STORE` set: `UsageError`: `ezc setup: unset EZC_STORE. Setup manages the store in <EZC_HOME>/store.`
- Output, one line per step, `✓` for what was already there and `+` for what was made (matching doctor's markers), then what to do next:

```
✓ git
✓ sops
+ created your key: ~/.config/sops/age/keys.txt
  Back this file up now. Without it your secrets cannot be read, and it cannot be recovered.
+ created the store: /Users/gene/Library/ezc/store
+ wrote /Users/gene/Library/ezc/config.toml

Next: ezc set -g NAME --type secret --desc "what it is"
```

  After a clone where the key is not a recipient, in place of the `Next` line: `! Your key cannot read this store's secrets yet. Give this public key to someone who has access:` and the public key on its own line.

- [ ] **Step 1: Write the failing tests for `setup`** (fake backend, real git): `Key` creates the file with mode `0600` in a `0700` folder and returns its public half; `Key` on an existing file returns `created == false` and leaves bytes and modification time alone; `Key` on an existing file that holds no key is an error naming it and leaves it alone; `NewStore` leaves a clean git repo with one commit `setup: create the store`, a valid manifest, a registry whose one entry is the name and key, and `HasPolicy` true; `NewStore` with no git identity succeeds (Review Focus 4); `CloneStore` of a bare repo that holds a store; of one that does not (folder removed, the error above); `Publish` with a fake `gh` records exactly the argument list above; `Recipient` hit and miss.
- [ ] **Step 2: Write the failing tests for the command** (fake `System`, fake backend, real git, fake `gh`/`brew`):
  - `TestSetupNewLocal`: exact stdout for a fresh machine with `--new-local --name gene --yes`; then `ezc set -g TOKEN --type secret --desc d` from stdin and `ezc get TOKEN --reveal` round-trip, and `ezc doctor` exits 0.
  - `TestSetupAgainChangesNothing` (Review Focus 3): run twice; the second prints only `✓` lines, exits 0, and every file under `EZC_HOME` and the key file have the same bytes; no new commit.
  - `TestSetupReusesKey`: a key file present before setup keeps its bytes and becomes the recipient.
  - `TestSetupNewGithub`: the fake `gh` is called once with the argument list; the config's `store.remote` is what `git remote` reports.
  - `TestSetupPublishesExistingLocalStore`: `--new-local`, then `--new-github o/r`: one `gh` call, no second store.
  - `TestSetupClone`: `--store <path to a bare remote>` whose registry holds this key: the `Next` line; whose registry does not: the `!` lines with the public key, exit 0.
  - `TestSetupCloneNotAStore`; `TestSetupDifferentStoreRefused`.
  - `TestSetupNoChoiceNoTerminal`: exit 2 with the message above. `TestSetupTwoChoices`: exit 2. `TestSetupWithEzcStore`: exit 2. `TestSetupBadName`: exit 2.
  - `TestSetupPrompts`: on a terminal, stdin `\n1\n\n` (accept the name, choose 1, confirm the backup) gives the same result as `--new-local --yes`.
  - `TestSetupToolMissing`: the backend's `Check` fails with `ErrNoTool` and no `brew`: exit 1, the hint printed, no key, no store, no config created. With a fake `brew` and `--yes`: `brew` runs once, then setup continues.
  - `TestSetupNoGit`: exit 1, nothing created.
  - `TestSetupNeverPrintsThePrivateKey`: stdout and stderr of every test above do not contain the key file's secret line.
- [ ] **Step 3: Run; fail to compile.**
- [ ] **Step 4: Implement.** The command file holds flags, prompts and printing; every step that changes the machine is a function in `setup`.
- [ ] **Step 5: Update the docs.** README: a "Set up" section replacing the hand-written policy instructions; CLAUDE.md: remove "The encryption policy is still written by hand", add "setup never overwrites a key, a store or a config" and "`ezc setup` refuses `EZC_STORE`"; AGENTS.md map.
- [ ] **Step 6: Run the gate; pass.**
- [ ] **Step 7: Commit.** `feat(setup): from nothing to an encrypted store`

### Task 11: Doctor knows the config, the recipient and the sync state

**Files:**
- Modify: `cli/internal/doctor/doctor.go`, `cli/internal/doctor/doctor_test.go`, `cli/internal/cli/doctorcmd.go`, `cli/internal/cli/cli_test.go` or a new `doctorcmd_test.go`, `README.md`

**Interfaces:**
- Consumes: `loadConfig`, `syncStore`, `recipients.Load`, `Backend.PublicKey`.
- Produces, added to `doctor.Options` (doctor still imports no tool-specific package and names no tool):

```go
ConfigPath  string // where config.toml is
ConfigFound bool
ConfigErr   string // "" unless the file could not be read
User        string
GitFound    bool
Sync        string // the store's state as one phrase, "" when the store is not a git repo
SyncState   State  // OK, Warn or Broken for that phrase
Recipient   string // the registry's name for this machine's key; "" when it is not a recipient
HasRegistry bool   // the store has a recipients.yaml
Requests    []string
```

- Items, machine section: `config` (OK with the path and user; Warn `no config at <path>. Run `ezc setup`.`; Broken with `ConfigErr`); `git` (OK; Broken `git is not installed.`). Store section: `sync` (OK `in sync` or `local only`; Warn for ahead, behind and uncommitted with the command that fixes each; Broken for diverged); `access` (OK `your key is the recipient <name>`; Broken `your key is not one of this store's recipients. Give your public key to someone who has access.` when a registry exists and the key is not in it; Warn `the store has no recipients.yaml. Run `ezc setup`.` when the store has a policy but no registry; omitted when the store has neither); `requests` (Warn listing names, only when there are any).
- Doctor fetches by the same 10 minute rule as other commands and prints no separate indicator line: the `sync` item carries it.

- [ ] **Step 1: Write the failing tests**: one per item and state above in `doctor_test.go` with exact text; in the command test, after `ezc setup --new-local` doctor shows `config`, `git`, `sync` and `access` as OK and exits 0; with a key that is not a recipient it exits 1; with a diverged store it exits 1; with `EZC_STORE` at a plain folder none of `sync`, `access`, `requests` appear and the result is what part 2 gave; `--only machine` shows `config` and `git` and no store item.
- [ ] **Step 2: Run; fail.**
- [ ] **Step 3: Implement.** Review every fix text in `doctor` and `storeaudit` that names `ezc setup` and make sure it is now true.
- [ ] **Step 4: Update README's "When something is wrong".**
- [ ] **Step 5: Run the gate; pass.**
- [ ] **Step 6: Commit.** `feat(doctor): the config, the store's sync state and whether this key has access`

### Task 12: End to end, with real git and real sops

**Files:**
- Create: `cli/cmd/ezc/store_test.go`
- Modify: `cli/cmd/ezc/main.go` (version `0.3.0`), `README.md`, `AGENTS.md`, `CLAUDE.md`, `mise.toml`

**Interfaces:**
- Consumes: the built binary, as `cli/cmd/ezc/sops_test.go` does (reuse its helpers).

Each test builds a machine as a temporary `HOME` and `EZC_HOME`, with the isolated git environment of the Global Constraints and `PATH` holding the binary, `git` and `sops`. Tests skip without `sops` or `git` unless `EZC_REQUIRE_SOPS=1` / `EZC_REQUIRE_GIT=1`.

- [ ] **Step 1: Write the tests.**
  - `TestSetupEndToEnd`: `ezc setup --new-local --name gene --yes` on an empty machine; the key file is `0600`; `ezc set -g API_TOKEN --type secret --desc d` with the value on stdin; `ezc get API_TOKEN --reveal` prints it; `ezc doctor` exits 0; `git -C <store> log --oneline` has `setup: create the store` and `set API_TOKEN (manifest.yaml, common.enc.yaml)`; no file in the store and no commit holds the value in clear; `ezc setup --new-local --yes` again exits 0 and adds no commit.
  - `TestTwoMachinesEndToEnd`: machine A sets up a local store, a bare repo is added as its `origin` with git, and `ezc store push` publishes it. Machine B, given a copy of A's key file (access is part 4), runs `ezc setup --store <bare>`; `ezc get API_TOKEN --reveal` works there. A sets a plaintext value and pushes. On B, with `state.json` made stale: `ezc list -g` prints `store: 1 commit behind origin — run ezc store pull` on stderr; `ezc set -g X y --type str --desc d` is refused; `ezc store pull` prints `pulled 1 commit from origin`; the value resolves.
  - `TestDivergedSecretEndToEnd`: A and B each set a different secret in `common.enc.yaml`; A pushes; B's `ezc store pull` exits 1, names B's secret in an `ezc set -g` line, prints no value, and afterwards A's secret decrypts on B and `ezc store status` says `in sync`.
  - `TestOfflineEndToEnd` (Review Focus 2): B's remote folder is removed and `state.json` made stale: `ezc env` in a repo still exits 0 inside 5 seconds and stderr carries the offline line.
  - `TestExportStaysQuietEndToEnd`: behind, with a stale `state.json`: `ezc env export` has empty stderr.
- [ ] **Step 2: Run them.** `EZC_REQUIRE_SOPS=1 EZC_REQUIRE_GIT=1 go test ./cmd/ezc -run 'EndToEnd' -count=1`. Expected: pass; a failure here is a defect in an earlier task: fix it there, with a test at that level.
- [ ] **Step 3: Docs.** README reads top to bottom as a new user's path: install, `ezc setup`, set a value, use it in a repo, sync. Every command and message quoted in README, AGENTS.md and CLAUDE.md is checked against the built binary. State plainly what is not there yet: adding a second person (`ezc join`), bots and CI setup arrive in the next part; until then a second machine needs the same key file.
- [ ] **Step 4: Run the gate and both greps** (the name rule, and `grep -rnE 'sops|SOPS_|ENC\[' cli --include='*.go' --exclude='*_test.go' | grep -v -e 'cli/internal/backend/sops/' -e 'cli/internal/cli/scope.go'`). Expected: gate passes, both greps print nothing.
- [ ] **Step 5: Commit.** `test(e2e): setup, two machines and a diverged secret; docs for part 3`
