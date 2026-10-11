# ezc Part 4: Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make it easy and safe to add and remove a person, a machine or a bot as a reader of the store's secrets: `ezc join`, `ezc user list|add|remove|rotate|requests|approve|deny`, `ezc bot add|remove|rotate`, `ezc store rewrap`, the rotation-due list, and `ezc setup --ci`.

**Architecture:** `recipients.yaml` stays the single registry. Every access change is one function, `recipients.Apply`, which writes the registry, regenerates the policy, re-wraps every encrypted file through the `Backend`, verifies each file's actual recipients and restores every byte when a step fails; the command layer wraps it in one helper (`accessChange` in `cli/internal/cli/access.go`) that guards, commits and pushes. A join request travels as a file on its own branch of the store's remote, and approval reads that one file from the branch: it never merges a branch somebody else wrote.

**Tech Stack:** Go 1.26, Cobra v1.10.2, yaml.v3, `filippo.io/age` v1.2.1, BurntSushi/toml; the `sops` binary (3.13.3 on this machine), `git`, and `gh` (always faked in tests).

**Spec:** `docs/superpowers/specs/2026-10-10-ezc-design.md` — sections "Store-only files", "Commands › Machine" (`setup --ci`, `join`), "Commands › Access", "Access", "Store sync" (access changes push; requests in the indicator), "Errors".

## Global Constraints

- The gate is `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops` and `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run schema:check`; both pass at the end of every task. Never run `mise trust`, never push, never switch branches.
- No command prints a value except `ezc env export`, `ezc run` (to the child), `ezc get`, and, new in this part, `ezc bot add|rotate --print-private`, which prints the bot's private key to stdout and nothing else there. This covers stdout, stderr, error text, commit messages and text passed through from git, gh and sops.
- A private key is never in a process's arguments. It is never in an environment variable ezc sets, with one exception the spec fixes: when `EZC_AGE_KEY` is set, the backend passes its text to sops as `SOPS_AGE_KEY` (the text is already in the environment ezc was given). A private key reaches `gh secret set` on stdin only. A bot's private key is never written to disk.
- A key file that exists is never rewritten, truncated or appended to. `ezc user rotate` writes the new key to a new file created with `O_EXCL`, mode `0600`, and points `config.toml` at it.
- A secret or a private key is never accepted on the command line. A public key is.
- A token (`EZC_STORE_TOKEN`, `GH_TOKEN`) is never in arguments, never in a URL and never in `.git/config`: git reads it from the environment through a credential helper whose text names the variable only.
- Only `cli/internal/backend/sops/` and `cli/internal/cli/scope.go` may contain `sops`, `.sops.yaml`, `SOPS_` or `ENC[` outside tests and docs. Only `cli/internal/gitstore/` builds git command lines. `gh` command lines are built in `cli/internal/setup/` and, new, `cli/internal/ghcli/`.
- Tests never read the real home, the real environment, a real key or the user's git config; never touch `~/.config/sops`; no network; `gh` and `brew` are always faked. Keys are generated in the test.
- Nothing deletes or overwrites an existing store, key or config field, except: an access change rewrites `recipients.yaml`, the policy file and re-wraps the encrypted files; `user rotate` and `join` set `key_file` or `user` in `config.toml` when they say so.
- Everything a remote can name is untrusted before it reaches the terminal: a request's name must pass `recipients.ValidName`, its key `Backend.ValidRecipient`, and its host and Git author are printed with every character outside printable ASCII replaced by `?` and cut at 80 characters.
- Name rule: `grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/` prints nothing.
- Commands follow the existing shape: `new<Name>Cmd(sys System)`, `UsageError` for a malformed command line (exit 2), everything external through `System`. User docs (README, CLAUDE.md, AGENTS.md) change in the commit that changes the behaviour. Markdown prose is one line per paragraph. `docs/superpowers/` is not edited.
- Commit trailer: `Co-Authored-By: Claude <model> <noreply@anthropic.com>`, naming the model that wrote the commit.

## Decisions this plan makes beyond the spec

1. **Approve does not merge the request branch.** It reads `.ezc/requests/<name>.yaml` from `origin/ezc/join-<name>` and ignores everything else on that branch, so a request cannot carry a change to `recipients.yaml` or a values file. The branch is deleted after the push, which closes its pull request. A request file that is in the store's working tree (somebody merged the pull request on GitHub) is treated the same and removed in the approve commit.
2. **`ezc join --print` prints a command, not an opaque string:** `ezc user add <name> <key> [--kind machine --owner <user>]`, which the approver pastes.
3. **`ezc store rewrap`** is new: it runs the sequence with the registry unchanged. It is the fix `ezc doctor` and `ezc user list` name when a file's recipients differ from the registry.
4. **A bot's secrets are delivered before the commit.** Order for `bot add`: sequence in the working tree, prove the new key decrypts every file, `gh secret set` for each repo, commit, push. A delivery that fails restores the working tree; nothing is committed.
5. **`--repo` is not checked against the repo registry yet.** `repos.yaml` arrives in part 5, which adds the check.
6. **`user rotate` and `bot rotate` write rotation-due entries only with `--compromised`.** A routine rotation of a key that never left its machine leaks nothing; removal always writes them.
7. **Removing a user who owns machines is refused**, naming them; remove the machines first.
8. **An access change needs a key file.** With the key only in the environment as text (`EZC_AGE_KEY`, CI) the access commands refuse.
9. **A push that fails after an access change's commit leaves the commit** and exits 1 with `Committed, but the push failed (<reason>). Run ezc store push.`
10. **A store with a policy and no `recipients.yaml`** (made by hand): access commands refuse with `<store> has no recipients.yaml, so ezc does not know who can read it. Write one (README, "Access"), then run ezc store rewrap.` `ezc store rewrap` is the only access command that runs there once the file exists.
11. **`ezc setup --ci` makes a shallow clone** and saves `backend` and `store.remote` in `config.toml`; no `user`, no `key_file`.

## Review Focus

1. A join branch that also changes `recipients.yaml`, the policy or a values file: approve must take nothing from it but the request file (Task 7 test).
2. A request whose name, host or author holds terminal escapes, or whose `name` field differs from its file or branch name: never printed, never approved (Tasks 2 and 7 tests).
3. An access change that fails on the third of five files: every file, the registry and the policy are byte-identical to before, and there is no commit (Task 2 and Task 4 tests).
4. An access change run while the store is behind, diverged, dirty or unreadable, or on a machine whose key is not a recipient: refused before a byte is written (Task 4 tests).
5. `bot add` when `gh` is missing, or fails on the second repo: no commit, no registry entry, and the private key in no output, argument or file (Task 8 tests).

---

### Task 1: Backend — recipients, re-wrap, proof

**Files:**
- Modify: `cli/internal/backend/backend.go`, `cli/internal/backend/fake.go`, `cli/internal/backend/fake_test.go`, `cli/internal/backend/backendtest/*.go`
- Create: `cli/internal/backend/sops/access.go`; Modify: `cli/internal/backend/sops/sops_test.go`

**Interfaces:**
- Produces, added to `backend.Backend`:
  - `Recipients(path string) ([]string, error)` — the public keys the file is encrypted for, sorted; needs no key and no tool.
  - `Rewrap(path string) error` — re-encrypts the file's data key for the policy in its folder; needs the key and the tool. `ErrNoKey`, `ErrNoTool`, `ErrNoPolicy` as for `Set`.
  - `CanDecrypt(path string, private []byte) error` — nil when that private key alone opens the file; needs no tool. The error names the file, never the key.
  - `ValidRecipient(key string) error` — nil for a well-formed public key.
- Fake: a fake file gains a first line `# recipients: k1,k2` (sorted), written by `Set` and `Rewrap` from the folder's `FakePolicy` file, which `WritePolicy` already writes; a file with no such line has no recipients. The fake's key pair: private is `FAKE-SECRET-<hex>`, public is `fake1<same hex>` (keep `NewKey` and `PublicKey` consistent with that; adjust if the existing forms differ, keeping them derivable). `CanDecrypt` succeeds when the public half of `private` is in the file's recipients. `ValidRecipient` accepts `fake1` followed by hex. `Rewrap` fails with `ErrNoKey` when `Locked`. New field `RewrapFail string`: `Rewrap` of a file with that base name fails, for rollback tests.
- sops: `Recipients` reads `sops.age[].recipient` from the file's YAML. `Rewrap` runs `sops updatekeys --yes <base>` in the file's folder, with the same environment and error mapping as `Set`. `CanDecrypt` parses the private text with `age.ParseIdentities`, takes the `enc` block of the matching entry in `sops.age` and decrypts it in-process with `age.Decrypt` through `armor.NewReader`: the data key unwraps or it does not; sops is not run. `ValidRecipient` is `age.ParseX25519Recipient`.

- [ ] **Step 1:** Add contract tests in `backendtest` (run against both backends): `Recipients` of a file created under a policy of two keys returns both, sorted; after `WritePolicy` with a third key and `Rewrap`, returns three and the values still decrypt; after `WritePolicy` without the second and `Rewrap`, `CanDecrypt` with the second private key fails and with the first succeeds; `CanDecrypt` with a key that was never a recipient fails and its error text contains neither key; `ValidRecipient("")`, `("not a key")` fail and a `NewKey` public half passes; `Recipients` of a missing file is an error naming the path.
- [ ] **Step 2:** Run `go test ./cli/internal/backend/...` (with `EZC_REQUIRE_SOPS=1`): fails to compile.
- [ ] **Step 3:** Implement in the fake and in `sops/access.go`.
- [ ] **Step 4:** Add sops-only tests: `Rewrap` with no tool is `ErrNoTool`; with a key that cannot open the file is `ErrNoKey`; no test output contains a value; a recording runner shows `updatekeys` got the base name, the folder as `Dir`, and no key text in `Args` or `Env`.
- [ ] **Step 5:** Run the gate; commit `feat(backend): read a file's recipients, re-wrap it and prove a key opens it`.

### Task 2: recipients — changes, requests, rotation-due, the sequence

**Files:**
- Modify: `cli/internal/recipients/recipients.go`, `recipients_test.go`
- Create: `cli/internal/recipients/request.go`, `due.go`, `apply.go` and their `_test.go`

**Interfaces:**
- Consumes: Task 1's `Backend` methods; `Registry`, `Recipient`, `ValidName`, `Load`, `Save`, `Keys`, `NameOf` as they are.
- Produces:
  - `const KindUser, KindMachine, KindBot = "user", "machine", "bot"`
  - `func (r Registry) With(name string, rec Recipient) (Registry, error)` — a copy with the entry added. Errors: invalid name; name taken; key already registered (names its holder); a machine whose `Owner` is not a recipient of kind user; a kind that is none of the three.
  - `func (r Registry) Without(name string) (Registry, error)` — a copy without it. Errors: no such recipient; `ErrLast` when it is the only one; `*OwnsError{Machines []string}` when it is a user that machines name as owner.
  - `func (r Registry) WithKey(name, key string) (Registry, error)` — a copy with that entry's key replaced; errors as `With` for the key.
  - `var ErrLast = errors.New("the last recipient cannot be removed")`
  - `type Request struct { Name, Key, Kind, Owner, Host, Requested string }` (yaml: `name`, `key`, `kind`, `owner,omitempty`, `host`, `requested`); `const RequestsDir = "requests"`; `func ParseRequest(name string, data []byte, valid func(key string) error) (Request, error)` — rejects: `name` field differing from the `name` argument, a name `ValidName` refuses, a key `valid` refuses, kind other than user or machine, a machine with no owner, unknown fields; `func (q Request) Marshal() []byte`; `func RequestPath(name string) string` returning `requests/<name>.yaml`.
  - `func Printable(s string) string` — every rune outside `0x20..0x7e` becomes `?`, cut at 80 characters.
  - `const DueFile = "rotation-due.yaml"`; `type Due map[string]DueEntry` keyed `<file>/<NAME>`; `type DueEntry struct { Removed, On string }` (yaml `removed`, `on`); `func LoadDue(dir string) (Due, error)` (missing file is an empty map); `func (d Due) Save(dir string) error` (sorted keys, removes the file when empty); `func (d Due) Add(file, name, removed, on string)` (keeps an existing entry); `func (d Due) Clear(file, name string) bool`.
  - `func Apply(dir string, next Registry, b Backend, encFiles []string) error` where `Backend` is the local interface `{ WritePolicy(dir string, recipients []string) error; Rewrap(path string) error; Recipients(path string) ([]string, error) }` and `encFiles` are base names in `dir`. It saves `next`, writes the policy from `next.Keys()`, re-wraps each file, and checks each file's `Recipients` equals `next.Keys()` exactly. On any failure it puts back the bytes of `recipients.yaml`, of every file in `encFiles` and of every file in `dir` whose name starts with `.` (the policy file; recipients does not know its name), removes any it created, and returns the first error wrapped with the file it happened on.
  - `func Differences(dir string, r Registry, b interface{ Recipients(string) ([]string, error) }, encFiles []string) (map[string]Diff, error)` with `type Diff struct { Missing, Extra []string }` holding registry names for `Missing` and names or, for an unregistered key, the key itself for `Extra`; only files that differ are in the map.

- [ ] **Step 1:** Write the tests: each error of `With`, `Without`, `WithKey`, each proving the receiver is unchanged; `ParseRequest` for each rejection, plus a round trip through `Marshal`; `Printable("a\x1b[31mb")` is `a?[31mb` and an 81-character string is cut to 80; `Due` add-keeps-first, clear, save-removes-empty, load-missing, and a saved file reading `common.enc.yaml/LLM_API_KEY: {removed: sam, on: 2026-10-11}`.
- [ ] **Step 2:** Write the `Apply` tests with `backend.Fake` in a temp folder holding two fake encrypted files: success leaves both files with exactly the new keys and the registry saved; with `RewrapFail` set to the second file, `recipients.yaml`, the policy file and both encrypted files are byte-identical to before; a `Recipients` mismatch after re-wrap (a fake wrapper that lies) is an error naming the file and restores; `Apply` on a folder with no encrypted files writes registry and policy. `Differences`: a file missing one registry key and holding one unregistered key reports both.
- [ ] **Step 3:** Run `go test ./cli/internal/recipients/`: fails to compile.
- [ ] **Step 4:** Implement.
- [ ] **Step 5:** Run the gate; commit `feat(recipients): registry changes, requests, rotation-due and the change sequence`.

### Task 3: gitstore — request branches and a token

**Files:**
- Modify: `cli/internal/gitstore/gitstore.go`, `gitstore_test.go`, `gittest/gittest.go`
- Create: `cli/internal/gitstore/branch.go`, `branch_test.go`

**Interfaces:**
- Produces:
  - `Git.TokenVar string` — when set, every git command that can reach a remote gets `-c credential.helper=` (to drop inherited helpers) and `-c credential.helper=!f() { echo username=x-access-token; echo "password=$<TokenVar>"; }; f`. The variable's value appears nowhere in `Args`.
  - `func (g Git) CloneShallow(source, dir string) error` — `--depth 1`, otherwise as `Clone`.
  - `Fetch` also prunes remote-tracking branches that are gone and fetches every branch (check what it fetches today; a shallow clone must still see `ezc/join-*` branches: set the fetch refspec to all branches when it is single-branch).
  - `func (g Git) PushBranch(dir, branch, message string, a Author, paths ...string) error` — commits `paths` on a new branch `branch` cut from the current commit and pushes it to `origin`, then returns the working tree to the branch it was on, with `paths` no longer present there and the local `branch` deleted. On failure the same cleanup runs. `ErrNoRemote` without a remote. When the remote already has that branch: if it holds the same bytes at each of `paths`, nothing is pushed and the result is nil; otherwise the error is `ErrBranchTaken` and the remote branch is left alone. It never force-pushes.
  - `func (g Git) RemoteBranches(dir, prefix string) ([]string, error)` — names under `origin/<prefix>` as of the last fetch, without `origin/`, sorted.
  - `func (g Git) Show(dir, ref, path string) ([]byte, error)` — the file at `origin/<ref>`; `fs.ErrNotExist` when the branch has no such file.
  - `func (g Git) LastAuthor(dir, ref, path string) (string, error)` — `Name <email>` of the last commit on `origin/<ref>` that touched `path`.
  - `func (g Git) DeleteRemoteBranch(dir, branch string, timeout time.Duration) error` — nil when it is already gone.
  - `func (g Git) Restore(dir string, paths ...string) error` — puts tracked `paths` back to the last commit and removes untracked ones among them.

- [ ] **Step 1:** Write tests with `gittest` (a bare remote and two clones): `PushBranch` from clone A, then `Fetch` in B: `RemoteBranches(dir, "ezc/join-")` lists it, `Show` returns the file's bytes, `LastAuthor` returns A's author, and A's working tree, branch and status are as before; `PushBranch` twice with the same bytes succeeds, with different bytes from B is `ErrBranchTaken` and the remote branch still holds A's bytes; `DeleteRemoteBranch` then `Fetch` in A no longer lists it, and a second delete is nil; `Show` of a path the branch lacks is `fs.ErrNotExist`; `Restore` after editing a tracked file and creating an untracked one leaves `Changed` empty; `PushBranch` with no remote is `ErrNoRemote` and leaves no branch.
- [ ] **Step 2:** Write the token tests with a recording runner: with `TokenVar: "EZC_STORE_TOKEN"` and that variable set to `tok-123` in `Environ`, the arguments of `Fetch`, `CloneShallow` and `Push` contain `$EZC_STORE_TOKEN` and never `tok-123`; without `TokenVar` no `credential.helper` argument is present.
- [ ] **Step 3:** Run `go test ./cli/internal/gitstore/`: fails.
- [ ] **Step 4:** Implement. `Restore` and the branch commands go through `Git.run`, so the environment rules already there apply.
- [ ] **Step 5:** Run the gate; commit `feat(gitstore): request branches, restore and a token read from the environment`.

### Task 4: the access change, `ezc user list|add`, `ezc store rewrap`

**Files:**
- Create: `cli/internal/cli/access.go`, `usercmd.go`, `usercmd_test.go`
- Modify: `cli/internal/cli/cli.go` (register `newUserCmd`), `storecmd.go` (`rewrap`), `scope.go` (encrypted file names of the store), `README.md`, `CLAUDE.md`, `AGENTS.md`

**Interfaces:**
- Consumes: Tasks 1–3; `syncStore`, `storeGit`, `loadConfig`, `commitStore`, `cannotTell`, `keyFileInUse`, `backendFor`, `storePaths`, `resolve.IsEncrypted`.
- Produces, in `access.go`:
  - `type access struct { sys System; sync storeSync; dir string /* the store's .ezc */; reg recipients.Registry; me string /* config user */; myKey string; myName string /* registry name of myKey, "" when none */; b backend.Backend; enc []string }`
  - `func openAccess(sys System, write bool) (*access, error)` — loads the store (the one `storePaths` gives, so `EZC_STORE` works), the registry and this machine's public key. With `write`: fetches now (`syncStore(sys, 0, 30*time.Second)`), and refuses, before anything is written, when: the state cannot be told (`cannotTell`); the store is behind or diverged (`The store is behind its remote. Run ezc store pull, then run this again.`); `recipients.yaml`, the policy file, `rotation-due.yaml` or any encrypted file has uncommitted changes; there is no key file in use (decision 8); the registry is missing (decision 10); this machine's key is not a recipient (`This machine's key is not a recipient of the store, so it cannot re-wrap it. Run ezc join.`).
  - `type change struct { next recipients.Registry; message string; due func(d recipients.Due); extra func() error /* runs after the sequence, before the commit; its failure restores */; remove []string /* files in .ezc to delete in the commit */ }`
  - `func (a *access) apply(c change) error` — `recipients.Apply`; then `c.due` on the loaded `Due` and save; then `c.extra`; then one commit of every file that changed with `c.message`; then push when the store has a remote (30 second timeout). Any failure before the commit restores with `Git.Restore` when the store is a git repo and with `Apply`'s own rollback otherwise, and returns the error. A push failure is decision 9. Prints `✓ <message>` and, after a push, `✓ pushed to <ShowURL(remote)>`; for a store with no remote, nothing more.
- Commands:
  - `ezc user list` — a table: name, kind, owner, added, added by, and `(this machine)` on the row whose key is this machine's. Then, per encrypted file whose recipients differ: `✗ <file>: not encrypted for <names>; encrypted for <names or keys> who are not in the registry`. Exit 1 when any differs, with `Run ezc store rewrap.` Needs no key. No registry: decision 10's sentence, exit 1.
  - `ezc user add <name> <key> [--kind user|machine] [--owner <user>]` — `exactArgs(2, "a name and a public key")`; the key through `Backend.ValidRecipient`; `--owner` required with `--kind machine` and refused otherwise (`UsageError`); kind `bot` refused: `ezc user add: add a bot with ezc bot add.` Message `add <name>`. `added` is today (`sys.Now`), `addedBy` is `a.me`.
  - `ezc store rewrap` — `apply` with the registry unchanged, message `rewrap`; when nothing differs prints `✓ every file is encrypted for the registry` and makes no commit.

- [ ] **Step 1:** Write `usercmd_test.go` with the fake `System`, `backend.Fake` and real git through `gittest` (follow `storewrite_test.go`): `user add sam <key>` leaves a commit `add sam`, the store pushed, both fake files listing both keys, and stdout `✓ add sam`; `user list` shows both and marks this machine; each refusal of `openAccess` (behind, diverged, dirty registry, detached HEAD, no key file, no registry, not a recipient) writes nothing: assert `git status` clean and the head commit unchanged; `user add` with a taken name, a registered key, an invalid key, `--kind machine` without `--owner`, `--kind bot`; with `Fake.RewrapFail` on the second file: no commit, working tree clean, exit 1; a remote that refuses the push (remove the bare repo): the commit exists, exit 1, decision 9's sentence; `user list` on a store where one file lacks a registry key exits 1 and names the file and `ezc store rewrap`; `store rewrap` then fixes it in one commit `rewrap`, and a second run commits nothing.
- [ ] **Step 2:** Run `go test ./cli/internal/cli/ -run 'User|Rewrap'`: fails.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** README: an "Access" section (the registry, `user list`, `user add`, `store rewrap`, how to write a `recipients.yaml` by hand for a store made by hand). CLAUDE.md sharp edges: every access change goes through `openAccess` and `access.apply`; what `apply` restores; the push rule. AGENTS.md: rows for the new files.
- [ ] **Step 5:** Run the gate; commit `feat(access): ezc user list and add, ezc store rewrap, and the one path every access change takes`.

### Task 5: `ezc user remove` and rotation-due

**Files:**
- Modify: `cli/internal/cli/usercmd.go`, `usercmd_test.go`, `setcmd.go`, `values.go` or wherever `unset` lives, `setcmd_test.go`, `storecmd.go`, `storecmd_test.go`, `sync.go`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: Task 4's `access`, `change`; `recipients.Due`; `Backend.Inspect` (names without a key).
- Produces: `func dueFor(a *access, removed string) (func(recipients.Due), []string, error)` — for every encrypted file in the store, every name in it (from `Inspect`), an entry `removed`, today; also returns the `<file>/<NAME>` list for printing. `storeSync.Due int` — how many entries `rotation-due.yaml` holds, read by `syncStore`.

- `ezc user remove <name>` — refuses: a bot (`remove a bot with ezc bot remove.`); `ErrLast`; `*OwnsError` (decision 7, naming the machines); this machine's own entry unless another recipient is the same person (a user or machine whose name or owner is the entry's person): `<name> is this machine's key and your only one. Removing it would lock you out.` Message `remove <name>`. Afterwards prints `<n> secrets were readable by <name> and need new values:`, the list, and `Give each a new value with ezc set -g NAME.`
- `ezc set -g` of a secret, and `ezc unset -g` of one, clear the name's entry for the file they wrote and include `rotation-due.yaml` in their commit and in the files given to `scope.guard`, only when an entry exists.
- `ezc store status` gains a line `rotation due: <n> secrets (ezc store status --due lists them)` and the flag `--due`, which prints one `<file>/<NAME>  readable by removed <who> since <date>` line each. The `store:` indicator line is unchanged.

- [ ] **Step 1:** Tests: remove with two recipients and one secret in each of two files writes two entries, one per file and name, in the `remove sam` commit, and the removed key is in no file's recipients; each refusal writes nothing; `set -g` of a due secret clears its entry in the same commit and leaves the others; `unset -g` likewise; a `set -g` of a secret with no entry does not touch or commit `rotation-due.yaml`; when the last entry clears the file is removed in that commit; `store status` shows the count and `--due` the lines; no output anywhere contains a value.
- [ ] **Step 2:** Run them: fail. **Step 3:** Implement. **Step 4:** README ("Removing someone", why re-wrapping does not un-leak) and CLAUDE.md.
- [ ] **Step 5:** Run the gate; commit `feat(access): ezc user remove, and the secrets that need new values afterwards`.

### Task 6: `ezc join`

**Files:**
- Create: `cli/internal/cli/joincmd.go`, `joincmd_test.go`, `cli/internal/ghcli/ghcli.go`, `ghcli_test.go`
- Modify: `cli/internal/cli/cli.go` (`System.Hostname func() (string, error)`, register the command), `cli_test.go` (fake hostname), `cli/cmd/ezc/main.go` (`os.Hostname`), `cli/internal/setup/setup.go` only if a helper must be exported, `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: `setup.Key`, `setup.CloneStore`, `setup.InTheWay`, `setup.IsSlug`, `keyInEnvironment`, `Git.PushBranch`, `recipients.Request`, `config.Save`.
- Produces: package `ghcli`: `type GH struct { Run proc.Runner; LookPath func(string) (string, error); Environ []string }`; `func (g GH) Installed() bool`; `func (g GH) PRCreate(dir, head, title, body string) (url string, err error)`; `func (g GH) SecretSet(repo, name string, value []byte) error` (value on stdin; `gh secret set <name> --repo <repo>`); `func (g GH) SecretDelete(repo, name string) error`. Errors carry one line of gh's stderr put through `gitstore.ShowURL` per word, never stdin's content. `const requestBranch = "ezc/join-"` in `access.go`.

- `ezc join <owner/repo | remote> [--name N] [--kind user|machine] [--owner U]` — refuses `EZC_STORE` and key text in the environment, as `setup` does. Name: `--name`, else the config's `user`, else asked on a terminal, else a `UsageError`. Steps, each printing a `✓` line: clone the store when `$EZC_HOME/store` is absent (a store in place must have the same remote, by setup's rule); find or create the key; save `user`, `backend`, `key_file` and `store.remote` in `config.toml` where they are empty. Then: this key already a recipient → `✓ <name> can already read this store.` and stop, exit 0. A registry entry of that name with another key, or `ErrBranchTaken` from the push (somebody else's request of that name is waiting) → error: `The store already has a recipient named <name>. Pick another name with --name.` Otherwise write the request (`host` from `sys.Hostname`, `requested` today) and `PushBranch(storeDir, "ezc/join-<name>", "request access for <name>", author, ".ezc/requests/<name>.yaml")`; then `gh pr create` when `gh` is installed and the remote is a GitHub one, printing its URL; a failure to open it is a `!` line, not an error. Ends with: `Ask someone who can read the store to run: ezc user approve <name>` and `Then run ezc store pull.`
- `ezc join --print [--name N] [--kind …] [--owner …]` — takes no remote and needs no store: finds or creates the key and prints one line to stdout, `ezc user add <name> <key>` with `--kind machine --owner <user>` when that is the kind.

- [ ] **Step 1:** Tests (fake backend, real git, a bare remote holding a store made by a first machine; `gh` faked through `System.Exec`): join clones, creates the key file mode `0600`, pushes a branch `ezc/join-sam` whose only change is `.ezc/requests/sam.yaml` holding the public key and the fake hostname, leaves the local store on its branch and clean, and prints the two closing lines; with a fake `gh` it calls `pr create` with head `ezc/join-sam`; with `gh` failing it still exits 0; run twice it pushes nothing new and exits 0; a machine whose key is already a recipient gets the `can already read` line and no branch; a taken name; `--kind machine` without `--owner` (exit 2); `EZC_STORE` set (exit 2); `--print` output is exactly one line that `ezc user add` on the first machine then accepts; the private key appears in no output and no argument.
- [ ] **Step 2:** `ghcli` tests with a recording runner: `SecretSet` puts the value in `Stdin` and never in `Args` or `Env`; a failing gh's error holds no stdin content.
- [ ] **Step 3:** Run: fail. **Step 4:** Implement. **Step 5:** README ("Joining a store"), CLAUDE.md. **Step 6:** Run the gate; commit `feat(access): ezc join asks for access with a request on its own branch`.

### Task 7: `ezc user requests|approve|deny`, and requests in the sync line

**Files:**
- Modify: `cli/internal/cli/usercmd.go`, `usercmd_test.go`, `sync.go`, `sync_test.go`, `storecmd.go`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: `Git.RemoteBranches`, `Show`, `LastAuthor`, `DeleteRemoteBranch`; `recipients.ParseRequest`, `Printable`; Task 4's `access`.
- Produces: `type pending struct { Name string; Req recipients.Request; Author string; Branch bool /* on a request branch */; File bool /* in the working tree */; Err error }`; `func pendingRequests(sys System, storeDir string) []pending` — the union of `origin/ezc/join-*` branches and `.ezc/requests/*.yaml` in the working tree, by name, sorted; a name that fails `ValidName` is left out entirely; a request that fails `ParseRequest` is kept with `Err`. `requests()` in `sync.go` returns these names, so the `store:` line and `ezc store status` name requests that are still on a branch.

- `ezc user requests` — per request: name, kind, owner, the full public key, `Printable(host)`, requested, `Printable(author)`; an invalid one as `<name>: not a valid request (<reason>)`. None: `No pending requests.`
- `ezc user approve <name>` — `openAccess(write)`; the request must parse; then `With(name, …)` (so a taken name or a registered key is refused) and `apply` with message `approve <name>`, removing the working-tree request file when there is one; after the push, `DeleteRemoteBranch`. A branch that cannot be deleted is a `!` line. Ends with `Tell <name> to run ezc store pull.`
- `ezc user deny <name>` — removes the working-tree file when present (commit `deny <name>`, push) and deletes the branch. No such request: an error.

- [ ] **Step 1:** Tests, built on Task 6's fixture (machine B joins, machine A acts): `requests` on A after a fetch shows B's full key, host and author; `approve sam` leaves B's key in the registry and in every file's recipients, one commit `approve sam` that touches no file outside `.ezc`, the remote branch gone; B then runs `store pull` and decrypts (fake backend: `CanDecrypt` with B's key). **Review Focus 1:** B's branch is rewritten by hand to also add B's key twice under another name in `recipients.yaml` and to change `common.yaml`: after approve, the registry holds exactly A and `sam`, and `common.yaml` is unchanged. **Review Focus 2:** a branch `ezc/join-bad` whose file says `name: other`, one with host `\x1b]0;x\x07`, one named `ezc/join-UP_PER`: the first is listed as invalid and cannot be approved, the second prints `?` for each control character, the third is absent from every output. A request file merged into the working tree by hand is listed and approving it removes the file in the same commit. `deny` removes branch and file. The `store:` line on A names `sam` while the request is only on a branch.
- [ ] **Step 2:** Run: fail. **Step 3:** Implement. **Step 4:** README ("Approving a request": why the key, host and author are shown) and CLAUDE.md (approve never merges; what is untrusted).
- [ ] **Step 5:** Run the gate; commit `feat(access): list, approve and deny join requests`.

### Task 8: `ezc bot add|remove|rotate`

**Files:**
- Create: `cli/internal/cli/botcmd.go`, `botcmd_test.go`
- Modify: `cli/internal/cli/cli.go`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: Task 4's `access`, `change.extra`; `ghcli.GH`; `Backend.NewKey`, `CanDecrypt`; `setup.IsSlug`; Task 5's `dueFor`.
- Produces: `const botSecret = "EZC_AGE_KEY"`, `const storeTokenSecret = "EZC_STORE_TOKEN"`.

- `ezc bot add <name> --repo owner/repo… [--print-private]` — at least one of `--repo` and `--print-private` (`UsageError`: `ezc bot add: name the repos that get the key with --repo, or pass --print-private.`); each repo an `IsSlug`; `gh` must be installed when `--repo` is given, checked before anything is written. `change.extra`: `CanDecrypt` on every encrypted file with the new private key, then `SecretSet(repo, "EZC_AGE_KEY", private)` for each repo. Message `add bot <name>`. Afterwards: `✓ EZC_AGE_KEY set in <repo>` each; with `--print-private` the private text on stdout as the only thing on stdout (every other line of this command goes to stderr in that mode); and the note: `The bot also needs to read the store repo. Create a read-only deploy key or a fine-grained token for it and save it as the EZC_STORE_TOKEN secret in each repo. ezc cannot create that credential.`
- `ezc bot remove <name>` — only kind bot (`<name> is not a bot. Remove it with ezc user remove.`); sequence with Task 5's rotation-due; message `remove bot <name>`; after the push `SecretDelete` for each of its repos, a failure being a `!` line naming the repo.
- `ezc bot rotate <name> [--print-private] [--compromised]` — a new key replaces the old in one commit, `rotate bot <name>`; delivery as `add`, to the entry's repos; an entry with no repos needs `--print-private`; `--compromised` adds rotation-due entries with `removed: <name> (old key)`.

- [ ] **Step 1:** Tests (fake backend, real git, `gh` faked and recording stdin): `bot add ci --repo o/a --repo o/b` leaves a registry entry of kind bot with both repos, a commit `add bot ci`, a push, and the fake `gh` having received on stdin, twice, a private key whose public half is the registered key and that `CanDecrypt`s every file; that key text is in no `Args`, no `Env`, no stdout, no stderr and no file under the temp home (walk it). **Review Focus 5:** `gh` not installed → exit 1 before any write; `gh` failing on the second repo → no commit, working tree clean, registry unchanged. `--print-private` prints exactly the key and a newline on stdout. Neither flag → exit 2. `bot remove ci` removes the entry, writes rotation-due, calls `secret delete` for both repos; on a user → refused. `bot rotate ci` changes the key, the old one opens no file, the repos get the new one; `--compromised` writes the entries; without it none.
- [ ] **Step 2:** Run: fail. **Step 3:** Implement. **Step 4:** README ("Bots and CI": the two secrets, the steps to create the store credential) and CLAUDE.md (delivery before commit; where a private key may go).
- [ ] **Step 5:** Run the gate; commit `feat(access): ezc bot add, remove and rotate deliver the key to GitHub Actions`.

### Task 9: `ezc user rotate`

**Files:**
- Modify: `cli/internal/cli/usercmd.go`, `usercmd_test.go`, `cli/internal/setup/setup.go` (export what creates a key file, if needed), `README.md`, `CLAUDE.md`

- `ezc user rotate [--compromised]` — takes no name (`noArgs`; a name is the `UsageError` `ezc user rotate replaces this machine's own key and takes no name. Nobody can rotate another person's key: there is no safe way to hand them the private half.`). Refused when the environment names the key file (`The key file is set by <variable>; ezc cannot change it. Unset it, or rotate by hand.`, the variable's name from `keyInEnvironment`). Steps: a new key is created in a new file beside the old one, `<old name without extension>-<YYYYMMDD>.txt`, with `-2`, `-3`… when taken, through the same `O_EXCL`, `0600` path as `setup.Key`; `change.next` is `WithKey(myName, newPublic)`; `change.extra` proves the new private key opens every file (`CanDecrypt`) and then saves `key_file` in `config.toml`; message `rotate <name>`. Any failure removes the new file and puts `config.toml` back. Prints `✓ new key in <path>`, `Back it up. The old key in <old path> no longer opens the store; delete it when you are sure.` The old file is never touched.

- [ ] **Step 1:** Tests: after rotate the registry holds the new key, every file's recipients are the new set, `config.toml`'s `key_file` is the new path, the old file's bytes and mode are unchanged, the new file is `0600`, and `ezc get -g NAME --reveal` still works; with `RewrapFail` the new file is gone, the config unchanged, no commit; a name argument → exit 2; `SOPS_AGE_KEY_FILE`-style variable set (use the fake's path through `keyInEnvironment`; test with `Backend == nil` wiring as `setupcmd_test.go` does) → refused; `--compromised` writes rotation-due with `removed: <name> (old key)`; run twice on one day → the second file ends `-2.txt`.
- [ ] **Step 2:** Run: fail. **Step 3:** Implement. **Step 4:** Docs. **Step 5:** Run the gate; commit `feat(access): ezc user rotate gives this machine a new key in a new file`.

### Task 10: `ezc setup --ci` and the key in `EZC_AGE_KEY`

**Files:**
- Modify: `cli/internal/cli/setupcmd.go`, `setupcmd_test.go`, `scope.go`, `sync.go` (`storeGit` sets `TokenVar`), `cli/internal/backend/sops/sops.go`, `sops_test.go`, `cli/internal/setup/setup.go`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Produces: `sops.Options.Key string` — when set, sops gets `SOPS_AGE_KEY=<Key>` and no key file variable. `backendFor` passes `sys.Getenv("EZC_AGE_KEY")`. `keySource` reports `EZC_AGE_KEY` first; `keyInEnvironment` returns it as key text; `keyFileInUse` is false with it. `storeGit` sets `TokenVar` to the first of `EZC_STORE_TOKEN`, `GH_TOKEN` that is set, else leaves it empty. `func setup.CloneShallowStore(e Env, source string) (remote string, err error)`.

- `ezc setup --ci --store <owner/repo>` — `--store` required and a slug (`UsageError`); `--ci` with `--new-local`, `--new-github` or `--name` is a `UsageError`. Needs `EZC_AGE_KEY` (`ezc setup --ci: EZC_AGE_KEY is not set. Save the bot's key as that secret; ezc bot add does it.`) and one of the two token variables (`…: set EZC_STORE_TOKEN (or GH_TOKEN) to a token that can read <owner/repo>.`), both exit 1 before anything is made. Clones `https://github.com/<owner/repo>.git` shallow into `$EZC_HOME/store`; the folder must hold an ezc store; writes `config.toml` with `backend` and `store.remote`; writes no key file; never prompts; prints `✓` lines and no `Next:`. A second run with the store in place and the same remote is all `✓`.

- [ ] **Step 1:** Tests: sops backend with `Key` set passes it as `SOPS_AGE_KEY` in `Env`, never in `Args`, and sets no key-file variable (recording runner); a real-sops test decrypts with `Key` and no key file anywhere. `setup --ci` with a recording `System.Exec`: the clone's arguments name `$EZC_STORE_TOKEN` and not the token's value, `.git/config` (when cloning a local bare repo through a test hook for the URL, as `localSource` allows) holds no token, no key file exists under the temp home, `config.toml` has no `user` and no `key_file`; each missing variable; each refused flag pair; with only `GH_TOKEN` set the helper names `GH_TOKEN`; `ezc user add` with `EZC_AGE_KEY` set is refused (decision 8).
- [ ] **Step 2:** Run: fail. **Step 3:** Implement. **Step 4:** README ("Bots and CI": a workflow snippet using `ezc setup --ci --store owner/repo` then `ezc run -- …`), CLAUDE.md (the one environment exception; the credential helper).
- [ ] **Step 5:** Run the gate; commit `feat(setup): ezc setup --ci reads the store with a token and a key from the environment`.

### Task 11: doctor

**Files:**
- Modify: `cli/internal/doctor/doctor.go`, `doctor_test.go`, `cli/internal/cli/doctorcmd.go`, `doctorcmd_test.go`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Produces, added to `doctor.Options`: `Differ []string` (encrypted files whose recipients differ from the registry), `Due int`, `Request string` (this machine's request status: `""`, `pending`, `not found`), `RequestName string`.

- `access` item: not a recipient with `Request == "pending"` → Broken, `this machine's key cannot read the store yet; request <name> is waiting for approval`, fix `Ask someone who can read the store to run ezc user approve <name>.`; not a recipient and no request → Broken, fix `Run ezc join <remote>.` (or `ezc join --print` for a store with no remote); a recipient → OK as today. The part 3 warning for a store with a policy and no registry gets the fix `Write recipients.yaml (README, "Access"), then run ezc store rewrap.`
- New `recipients` item in the store section: Broken `<n> files are not encrypted for the registry: <names>`, fix `Run ezc store rewrap.`; OK `every file is encrypted for the <n> recipients`.
- New `rotation` item: Warn `<n> secrets were readable by a removed key`, fix `ezc store status --due lists them; give each a new value with ezc set -g NAME.`; absent when zero.
- The `requests` item's fix becomes `Run ezc user requests.`

- [ ] **Step 1:** Tests in `doctor_test.go` for each state and its exact fix text, and in `doctorcmd_test.go` end to end with the fake: a joined-but-unapproved machine's doctor exits 1 with the pending line; after approve and pull it exits 0; a differing file is Broken and `store rewrap` clears it; a due list is a Warn (exit 0). The tool's name appears in no doctor output.
- [ ] **Step 2:** Run: fail. **Step 3:** Implement. **Step 4:** Docs. **Step 5:** Run the gate; commit `feat(doctor): who can read the store, whether the files agree, and what needs a new value`.

### Task 12: end to end, version, docs

**Files:**
- Create: `cli/cmd/ezc/access_test.go`
- Modify: `cli/cmd/ezc/main.go` (`0.4.0`), every test asserting `0.3.0`, `README.md`, `AGENTS.md`, `CLAUDE.md`

- [ ] **Step 1:** Write end-to-end tests driving the built binary with real git and real sops, on machines built with `newMachine` from `store_test.go` (nothing inherited; a fake `gh` script on the child's `PATH` that writes its arguments and stdin to files in the test's temp folder):
  - `TestJoinApproveEndToEnd`: A sets up a store with a bare remote and sets a secret; B (its own key, no copy of A's) runs `ezc join <remote> --name sam`; B's `ezc get -g TOKEN --reveal` fails and B's `ezc doctor` exits 1 naming the request; A runs `ezc user requests` (B's public key is printed in full), `ezc user approve sam`; B runs `ezc store pull`, then `ezc get -g TOKEN --reveal` prints the value and `ezc doctor` exits 0.
  - `TestRemoveEndToEnd`: then A runs `ezc user remove sam`; A's `ezc store status` shows `rotation due: 1`; B pulls and `ezc get -g TOKEN --reveal` fails without printing a value; A's `ezc set -g TOKEN` (stdin) clears the entry.
  - `TestBotEndToEnd`: A runs `ezc bot add ci --repo o/a`; the key the fake `gh` received on stdin, given to a third machine as `EZC_AGE_KEY` together with `EZC_STORE_TOKEN=x`, lets `ezc setup --ci --store <remote>` (through the test's local-remote hook) and `ezc get -g TOKEN --reveal` succeed, with no key file under that machine's home; `ezc bot remove ci` then calls `secret delete`.
  - `TestRotateEndToEnd`: A runs `ezc user rotate`; A still reads the secret; the old key file is byte-identical; a machine holding only a copy of A's old key cannot read after a pull.
  - In each: no secret value and no private key in any stdout or stderr except where the test asks for it, and `git log -p` of the store holds neither.
- [ ] **Step 2:** Run them; fix what they find in the owning package, with a unit test there.
- [ ] **Step 3:** Version `0.4.0`. README: the command table gains every command of this part; "Known limits" loses the same-key-file limit and gains: repos are not re-wrapped after an access change until part 5; `--repo` is not checked against a registry until part 5. AGENTS.md map: `recipients` (requests, due, apply), `ghcli`, the new command files.
- [ ] **Step 4:** Run the gate, the schema check and both greps from the constraints; all pass, both greps print nothing.
- [ ] **Step 5:** Commit `feat: ezc 0.4.0 — access: join, users, bots and CI`.
