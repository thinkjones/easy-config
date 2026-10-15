# ezc Part 5: Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record the repos that depend on the store and act on them: `ezc repo add|remove|list|check|uses|rewrap`, the guard on `ezc unset -g`, the re-wrap of each repo's secrets after an access change, and the check of `ezc bot add --repo` against the registry.

**Architecture:** A new package `cli/internal/registry` owns `repos.yaml` in the store's `.ezc/` (load, validate, save, add, remove, who uses a name). Checkout paths are per machine in `config.toml`'s existing `[checkouts]` table; `cli/internal/cli/repos.go` finds a repo's checkout and the working repo's identity. A repo's secrets are re-wrapped by `recipients.Rewrap`, the policy-and-re-wrap half of `recipients.Apply` split out so that the store and a repo run the same code; `access.apply` calls it for every checked-out repo after a store change.

**Tech Stack:** Go 1.26, Cobra v1.10.2, yaml.v3, BurntSushi/toml; the `sops` binary, `git`; `gh` always faked.

**Spec:** `docs/superpowers/specs/2026-10-10-ezc-design.md` — sections "Store-only files" (`repos.yaml`), "On a machine" (`[checkouts]`), "Commands › Values" (`unset` guard), "Commands › Repos", "Access › The registry" (re-wrap repos after the store), "Access › Adding a bot" (repos named must be in the registry), "The repo registry".

## Global Constraints

- The gate is `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops` and `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run schema:check`; both pass at the end of every task. Never run `mise trust`, never push, never switch branches.
- No command prints a value except `ezc env export`, `ezc run` (to the child), `ezc get` and `ezc bot add|rotate --print-private`. This covers stdout, stderr, error text, commit messages and text from git, gh and sops. Every new command prints names, counts and paths only.
- A private key is never in arguments, never in an environment variable ezc sets (except `EZC_AGE_KEY` passed to sops as `SOPS_AGE_KEY`), never written by this part. A token is never in arguments, a URL or `.git/config`.
- Only `cli/internal/backend/sops/` and `cli/internal/cli/scope.go` may contain `sops`, `.sops.yaml`, `SOPS_` or `ENC[` outside tests and docs; a policy file's name comes from `policyFiles(sys)`. Only `cli/internal/gitstore/` builds git command lines. `gh` command lines only in `cli/internal/setup/` and `cli/internal/ghcli/`.
- **A write to a repo other than the store runs no git at all.** Reading a repo's `origin` (`gitstore.Git.Remote`) is the one git call this part makes in a repo, and only to name it.
- Tests never read the real home, environment, key or git config; never touch `~/.config/sops`; no network; `gh` and `brew` faked; keys generated in the test.
- Nothing deletes or overwrites an existing store, key or config field, except as listed in the decisions below (a `[checkouts]` entry whose folder no longer holds `.ezc/`; a repo's policy file and encrypted files when it is re-wrapped).
- Everything in `repos.yaml` is untrusted (whoever can push to the store wrote it): an ID must pass `registry.ValidID` before it reaches a path, an argument or the terminal, and `added`/`addedBy` are printed through `recipients.Printable`. A name read from a repo's encrypted file is printed through `recipients.Printable`. Never echo an invalid argument.
- Name rule: `grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/` prints nothing.
- Commands follow the existing shape: `new<Name>Cmd(sys System)`, `UsageError` (exit 2), everything external through `System`. CLAUDE.md and AGENTS.md change in the commit that changes the behaviour they describe. Markdown prose is one line per paragraph. `docs/superpowers/` is history, except this plan.
- Commit trailer: `Co-Authored-By: Claude <model> <noreply@anthropic.com>`, naming the model that wrote the commit.

## Decisions this plan makes beyond the spec

1. **`ezc repo add` and `ezc repo remove` commit and do not push**, like a value change (spec, "Store sync": value changes do not push on their own). They run `guardStoreWrite` and `guardStoreFiles(…, "repos.yaml")` first, and commit `repos.yaml` alone as `repo add <id>` / `repo remove <id>`.
2. **An ID** is `owner/repo` (`setup.IsSlug`) or a plain name matching `^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$` that is not `.` or `..`. `registry.Load` refuses a file holding any other ID, an unknown field, or a date that is not `YYYY-MM-DD`: the whole file is an error, never a partial list.
3. **The working repo** is the folder holding the nearest `.ezc/` (`repoDir`'s parent; `ezc repo init` first). Its ID is the `owner/repo` of its `origin` when that is on github.com (`onGitHub`, parsed as `sameRemote` parses it, `.git` dropped), else `--name`, else the folder's base name. A non-GitHub `origin` gets a plain name. The store itself is never a repo of its registry (`sameDir` with the store's `.ezc` is refused).
4. **A repo's checkout** on this machine is `config.toml`'s `[checkouts]` path when that folder holds `.ezc/`, else the working repo when its ID is that ID. Lookup never writes the config. `repo add` records the checkout only for the working repo: with an argument that is not the working repo's ID it registers the ID and records nothing. An existing `[checkouts]` entry for the ID whose folder still holds `.ezc/` is kept, with a `!` line naming it; one whose folder does not is replaced.
5. **`ezc repo remove` is refused while a bot's entry names the repo** (decision 11 depends on it), naming the bot; it also removes this machine's `[checkouts]` entry for the ID.
6. **`ezc repo rewrap`** re-wraps a checkout's `.ezc/` for the store's registry with `recipients.Rewrap`: policy written from `registry.Keys()`, each encrypted file re-wrapped and verified, everything put back on a failure. It is refused before anything is written when the store has no registry or when `storeDiffers` is not empty (`Run ezc store rewrap first.`): a name somebody pushed into `recipients.yaml` must have been let in by a store access change, which asks, before any repo follows it. A checkout with neither an encrypted file nor a policy file is skipped. The result is left in the repo's working tree for its owner to commit; no git runs there.
7. **A repo secret that a key leaving its file could read is listed, not recorded.** `rotation-due.yaml` keys name the store's files, and `common.enc.yaml/NAME` of a repo would collide with the store's. `repo rewrap` and the access-change re-wrap print one `!` line per such secret.
8. **After an access change, every registered repo with a checkout here is re-wrapped**, with the backend the change itself used (`a.b`, built by `openAccess` before `user rotate` moved `key_file`, so a rotation re-wraps its repos with the old key). It runs after `access.run` returns `changed` with a nil error or an `*unpushedError` (the commit exists here), never after an `*unchangedError`. A repo that fails is a `!` line naming `ezc repo rewrap <id>`, a repo with no checkout here is a `!` line saying its secrets still have the old recipients, and neither changes the command's exit status. The store re-wrap's own confirm question already covered who gains access.
9. **The `unset -g` guard** reads the manifest of every registered repo with a checkout here. A name in `required`, or in `optional` with `requiredWhen`, refuses with exit 1, naming the repos; a name in plain `optional` is a `!` line; repos with no checkout here or whose manifest cannot be read are a `!` line saying they were not checked. `--force` skips the refusal and keeps the lines. A store with no `repos.yaml` has no guard.
10. **`ezc repo check`** with no argument checks the working repo (registered or not); with an ID, that repo; with `--all`, every registered repo. A check is `ezc env`'s (`load`, `check.Run`) in the checkout, plus `recipients.Differences` of the checkout's encrypted files against the store's registry. It exits 1 when a checked repo fails either; a repo with no checkout here is listed and is not a failure. `ezc repo rewrap` takes the same arguments.
11. **`ezc bot add --repo` takes only a registered `owner/repo`.** `bot rotate` and `bot remove` keep acting on the entry's repos as they are.
12. **`ezc doctor`'s repo group gains `recipients`**: Broken when the working repo's encrypted files differ from the store's registry, fix `Run ezc repo rewrap.`; skipped (no item) when the store has no registry or the repo has no encrypted file.

## Review Focus

1. A `repos.yaml` pushed by somebody else holding an ID such as `../../etc`, `--upload-pack=x`, `a/b/c` or one with a terminal escape: the file is refused as a whole and the ID reaches no path, `gh` argument or terminal (Task 1 and Task 2 tests).
2. A recorded checkout that was moved, deleted or lost its `.ezc/`: listed as having no checkout here by `list`, `check`, `uses`, the guard and the access re-wrap, and the others still run (Tasks 2, 3, 4, 6 tests).
3. An access change that succeeds on the store and then fails on one of two repos (this machine's key cannot open that repo's file): the store change stands, the exit status is the one the store change gave, the failed repo's files are byte-identical, the other repo is re-wrapped (Task 6 test).
4. `ezc user rotate` and `ezc user remove` with a checked-out repo: afterwards the repo's file opens with the new key, and the key that left unwraps no data key the file now has, from any of the repo's commits (Task 7 end to end).
5. `ezc repo rewrap` while `recipients.yaml` names a key no store file is encrypted for: refused, no repo file touched (Task 5 test).

---

### Task 1: The registry package

**Files:**
- Create: `cli/internal/registry/registry.go`, `cli/internal/registry/registry_test.go`

**Interfaces:**
- Produces:
  - `const File = "repos.yaml"`
  - `type Repo struct { Added string \`yaml:"added"\`; AddedBy string \`yaml:"addedBy"\` }`
  - `type Registry struct { Repos map[string]Repo \`yaml:"repos"\` }`
  - `func ValidID(s string) bool` — decision 2.
  - `func Load(dir string) (r Registry, found bool, err error)` — `found` false, no error, when the file is missing; strict decoding (`KnownFields`); errors name the file and never quote an ID that failed.
  - `func (r Registry) Save(dir string) error` — sorted IDs, flow-style entries as in the spec (`thinkjones/app: {added: 2026-10-13, addedBy: gene}`), atomic write.
  - `func (r Registry) With(id string, rep Repo) (Registry, error)` — a copy; errors: invalid ID, already registered.
  - `func (r Registry) Without(id string) (Registry, error)` — a copy; error: not registered.
  - `func (r Registry) IDs() []string` — sorted.
  - `type Use int` with `NotUsed`, `Optional`, `Required` (`Required` covers `optional` with `requiredWhen`); `func Uses(m manifest.Manifest, name string) Use`.

- [ ] **Step 1: Write the tests** — `ValidID` true for `thinkjones/app`, `scratch-tool`, `my.tool_2`; false for `""`, `.`, `..`, `../x`, `a/b/c`, `-x`, `--upload-pack=x`, `a b`, `a\x1b[31m`, 101 characters. `Load` of a missing file: found false. A round trip of the spec's example through `Save` and `Load`, and `Save`'s bytes for two entries equal `repos:\n  scratch-tool: {added: 2026-10-13, addedBy: gene}\n  thinkjones/app: {added: 2026-10-13, addedBy: gene}\n`. `Load` refuses an invalid ID (the error does not contain it), an unknown field, `added: yesterday`. `With` of a registered ID and `Without` of an unknown one fail and leave the receiver unchanged. `Uses` returns `Required` for a required name and for an optional one with `requiredWhen`, `Optional` for a plain optional one, `NotUsed` otherwise.
- [ ] **Step 2:** Run `cd cli && go test ./internal/registry/`: fails to compile.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the gate.
- [ ] **Step 5:** Add the `cli/internal/registry` row to AGENTS.md; commit `feat(registry): repos.yaml, the store's list of the repos that use it`.

### Task 2: Checkouts, `repo add`, `repo remove`, `repo list`

**Files:**
- Create: `cli/internal/cli/repos.go` (lookup), `cli/internal/cli/repocmd.go` (the new subcommands), `cli/internal/cli/repocmd_test.go`
- Modify: `cli/internal/cli/repo.go` (`newRepoCmd` registers the new subcommands; `verbs` list becomes `init, add, remove, list, check, uses or rewrap` as tasks add them)

**Interfaces:**
- Consumes: Task 1; `gitstore.Git.Remote`, `onGitHub`, `storeGit`, `guardStoreWrite`, `guardStoreFiles`, `commitStore`, `loadConfig`, `config.Save`, `paths.Resolve(...).Config()`, `repoDir`, `sameDir`, `recipients.Load`, `recipients.Printable`.
- Produces (in `repos.go`):
  - `func githubSlug(remote string) (string, bool)` — `owner/repo` of a github.com remote; false otherwise.
  - `type workRepo struct { ID, Top, Ezc string }`; `func workingRepo(sys System, name string) (workRepo, error)` — decision 3; `name` is `--name` ("" for none) and must pass `ValidID` (a `UsageError` that states the rule and does not echo it); an error when there is no `.ezc` or it is the store's.
  - `func checkoutOf(sys System, c config.Config, id string) (ezc string, ok bool)` — decision 4; `ezc` is the checkout's `.ezc` folder.
  - `func loadRepos(sys System) (registry.Registry, bool, string, error)` — the store's registry, found, and the store's `.ezc`.

- [ ] **Step 1: Write the tests** (fake `System`, real git through `gittest` for the remotes): `githubSlug` for `https://github.com/o/r.git`, `git@github.com:o/r`, `https://user:pw@github.com/o/r` → `o/r`; false for `https://gitlab.com/o/r`, `https://github.com/a/o/r`, a local path. `ezc repo add` inside a repo whose origin is `https://github.com/o/app.git` prints `✓ added o/app to repos.yaml` and `✓ checkout <top>`, writes `repos.yaml` with today's date and `config.toml`'s `user` as `addedBy`, records `[checkouts] "o/app" = "<top>"`, and makes one store commit `repo add o/app` holding `.ezc/repos.yaml` only. A second run prints `✓ o/app is registered` and makes no commit. A repo with no origin gets its folder's name; `--name x` wins; `--name '../x'` is a usage error whose text does not contain `../x`. `ezc repo add o/other` there registers `o/other` and records no checkout. A `[checkouts]` entry for `o/app` naming a folder with `.ezc/` is kept with a `!` line; one naming a missing folder is replaced. Refused, with nothing written: in the store's own folder; while the store is behind (the `behind` sentence); while `repos.yaml` has uncommitted changes; outside a repo with no argument. `ezc repo remove o/app` removes the entry and the checkout line, commits `repo remove o/app`; an unregistered valid ID is `o/nope is not in repos.yaml.`; an invalid one is a usage error that does not echo it; a bot entry with `repos: [o/app]` refuses, naming the bot. `ezc repo list` prints, per ID sorted, `<id>  added <date> by <who>  <checkout or "no checkout here">` and `  bot <name>` for each bot whose entry names it; with no registry `No repos are registered. Run ezc repo add inside a repo.` A `repos.yaml` holding `a\x1b[31m` makes `list` fail with an error containing no escape byte.
- [ ] **Step 2:** Run `go test ./internal/cli/ -run 'Repo(Add|Remove|List)|GithubSlug'`: fails.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the gate.
- [ ] **Step 5:** CLAUDE.md sharp edges: the working repo's ID, checkout lookup, `repo add|remove` commit without pushing, and that a repo is never a store of its own; commit `feat(repo): register the repos that use the store, and where they are checked out`.

### Task 3: `repo check` and `repo uses`

**Files:**
- Modify: `cli/internal/cli/repocmd.go`, `repocmd_test.go`

**Interfaces:**
- Consumes: Task 2; `load`, `loadManifest`, `check.Run`, `check.Report`, `check.ReportOK`, `warn`, `encryptedFiles`, `recipients.Differences`, `registry.Uses`, `touchStore` (once, before reading the store).
- Produces:
  - `type repoTarget struct { ID, Ezc string; Here bool }` and `func repoTargets(sys System, args []string, all bool) ([]repoTarget, error)` — decision 10, shared with Task 5. With `--all`, at most one positional (ENV); otherwise the first positional is an ID (validated, not echoed when invalid, an error when unregistered) and the second ENV. `Here` is false for a repo with no checkout.
  - `func repoDiffers(sys System, ezc string, reg recipients.Registry) (map[string]recipients.Diff, error)` — `recipients.Differences` on the checkout's encrypted files.

- [ ] **Step 1: Write the tests:** two registered repos with checkouts, one valid and one missing a required value, and a third with no checkout. `ezc repo check --all` prints, on stdout, for each checked repo a line `<id> (<checkout>)` followed by `ezc env`'s report indented by two spaces; for the third `<id>: no checkout on this machine. Run ezc repo add inside it.`; a last line `2 checked, 1 failed, 1 with no checkout here`; exit 1. `ezc repo check <valid id> prod` checks only that repo in `prod`. With no argument it checks the working repo. A checkout whose encrypted file lacks a registry key adds `  ✗ common.enc.yaml is not encrypted for exactly the store's recipients. Run ezc repo rewrap <id>.` and fails. Neither stdout nor stderr holds a value (the tests set distinctive values and assert their absence). A recorded checkout whose folder is gone counts as no checkout. `ezc repo uses API_URL` prints `required by: a, b`, `optional in: c` and `not checked (no checkout here): d` lines (each only when non-empty), or `No registered repo with a checkout here uses API_URL.`; an invalid NAME is a usage error that does not echo it.
- [ ] **Step 2:** Run the new tests: fail.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the gate.
- [ ] **Step 5:** Commit `feat(repo): check every registered repo's environment, and find who uses a name`.

### Task 4: The `unset -g` guard

**Files:**
- Modify: `cli/internal/cli/setcmd.go` (`newUnsetCmd`), `cli/internal/cli/setcmd_test.go`

**Interfaces:**
- Consumes: Task 2's `loadRepos`, `checkoutOf`; Task 1's `Uses`.
- Produces: `func repoUsers(sys System, name string) (required, optional, unchecked []string, err error)`, in `repos.go`.

- [ ] **Step 1: Write the tests:** with a registered checkout requiring `API_URL`, `ezc unset -g API_URL` exits 1 with `API_URL is required by o/app. Pass --force to unset it anyway, or take it out of their manifests first.`, and the store's files and commits are unchanged; `--force` removes it, prints the same repos on a `!` line and commits as before. A name required only through `requiredWhen` refuses the same way. A plain optional name prints `! API_URL is optional in o/app.` and is removed. A registered repo with no checkout here prints `! not checked, no checkout here: o/other` and does not refuse. A checkout with an invalid manifest is listed as not checked. `ezc unset API_URL` in a repo (no `-g`) runs no guard. A store with no `repos.yaml` behaves exactly as before (the existing unset tests pass unchanged). The guard runs before `scope.guard`, so a refusal writes and commits nothing.
- [ ] **Step 2:** Run `go test ./internal/cli/ -run Unset`: the new tests fail.
- [ ] **Step 3:** Implement; add `--force` (`unset it even when a registered repo requires it`).
- [ ] **Step 4:** Run the gate.
- [ ] **Step 5:** CLAUDE.md: the `ezc unset` sharp edge gains the guard; commit `feat(unset): refuse to remove a store value a registered repo requires`.

### Task 5: `recipients.Rewrap`, `repo rewrap`, doctor's repo recipients

**Files:**
- Modify: `cli/internal/recipients/apply.go`, `apply_test.go`
- Modify: `cli/internal/cli/repocmd.go`, `repocmd_test.go`, `cli/internal/cli/doctorcmd.go`, `cli/internal/doctor/doctor.go`, `doctor_test.go`, `doctorcmd_test.go`

**Interfaces:**
- Consumes: Task 3's `repoTargets`, `repoDiffers`; `storeDiffers`, `policyFiles`, `backendFor`, `Backend.Inspect`, `Backend.Recipients`.
- Produces:
  - `func Rewrap(dir string, keys []string, b Backend, encFiles []string) error` in `recipients` — `Apply` without saving a registry: policy, re-wrap, verify, and on failure the encrypted files and every dot file put back. `Apply` becomes `next.Save` plus `Rewrap`, with `recipients.yaml` in the same snapshot; its tests pass unchanged.
  - `type repoRewrap struct { Changed []string; Leaked []string; Skipped bool }` (`Leaked` holds `<file>/<NAME>` through `Printable`) and `func rewrapRepo(sys System, b backend.Backend, ezc string, reg recipients.Registry) (repoRewrap, error)` in `repos.go` — decision 6 and 7; `Leaked` is every name of a file whose recipients before the re-wrap hold a key `reg.Keys()` does not.
  - `func sayRewrap(w io.Writer, id string, r repoRewrap, err error)` — the lines below; used by `repo rewrap` and Task 6.

- [ ] **Step 1: Write the `recipients` tests:** `Rewrap` on a folder with two fake files succeeds and leaves no `recipients.yaml`; with `RewrapFail` on the second file both files and the policy are byte-identical afterwards and a policy file that did not exist is gone.
- [ ] **Step 2: Write the command tests:** `ezc repo rewrap o/app` on a checkout whose file lacks a registry key prints `✓ o/app: re-wrapped common.enc.yaml in <checkout>/.ezc. Commit it there.` and the file now lists exactly the registry's keys; run again, `✓ o/app: already encrypted for the store's recipients`; a checkout with no encrypted file and no policy prints `- o/app: no secrets to re-wrap` and writes nothing. A file that held a key the registry no longer names adds `! o/app: common.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN.` (and ` --env prod` for `env.prod.enc.yaml`). The store's `recipients.yaml` naming a key no store file is encrypted for: refused with `Run ezc store rewrap first.` and every repo file byte-identical. A repo whose file this machine's key cannot open: `✗ o/app could not be re-wrapped (<backend's fixed message>). Nothing was changed there.`, exit 1, files byte-identical. No git command is run in a checkout (assert on `fake.gits`' directories). `--all` covers every registered repo and lists those with no checkout here. `ezc doctor` in a repo whose file lacks a registry key has a Broken `recipients` item with fix `Run ezc repo rewrap.`; in one that matches, a `✓` item; with no store registry, no item.
- [ ] **Step 3:** Run the new tests: fail.
- [ ] **Step 4:** Implement.
- [ ] **Step 5:** Run the gate.
- [ ] **Step 6:** CLAUDE.md: replace "nothing writes a repo's `.ezc/.sops.yaml`" with how `repo rewrap` writes it, and the doctor item; AGENTS.md's `.ezc/.sops.yaml` row; commit `feat(repo): re-wrap a repo's secrets for the store's recipients`.

### Task 6: Re-wrap the repos after an access change; `bot add --repo` from the registry

**Files:**
- Modify: `cli/internal/cli/access.go` (`apply`), `cli/internal/cli/botcmd.go`, their tests (a new `cli/internal/cli/accessrepos_test.go`)

**Interfaces:**
- Consumes: Task 5's `rewrapRepo`, `sayRewrap`; Task 2's `loadRepos`, `checkoutOf`.
- Produces: `func (a *access) rewrapRepos(next recipients.Registry)` — decision 8; prints on `a.sys.Stdout`, after the store's lines.

- [ ] **Step 1: Write the tests:** with two registered checkouts holding fake encrypted files, `ezc user add sam <key> --yes` re-wraps both (each prints its `✓` line after `✓ pushed to …`) and the store's commit holds no repo file; `ezc user remove sam` lists each repo secret sam could read; `ezc store rewrap` re-wraps them too. A repo whose file `RewrapFail` names: `! o/app could not be re-wrapped (…). Run ezc repo rewrap o/app.`, the other repo re-wrapped, exit status 0, o/app's files byte-identical. A registered repo with no checkout here: `! o/other has no checkout on this machine, so its secrets are still encrypted for the old recipients. Run ezc repo rewrap o/other where it has one.` An access change that is refused or answered `n` touches no repo. After an `*unpushedError` the repos are re-wrapped and the exit status is still 1. `ezc user rotate` re-wraps the repos with the backend `openAccess` built (with the fake: assert the re-wrap happens before the command returns and the files list the new key). `ezc bot add ci --repo o/unregistered` is refused before `openAccess` writes anything: `o/unregistered is not in repos.yaml. Run ezc repo add o/unregistered first.`; a registered one works as before.
- [ ] **Step 2:** Run the new tests: fail.
- [ ] **Step 3:** Implement; `access.run` calls `rewrapRepos` as its last step when it changed a file and its error is nil or an `*unpushedError`, so `apply` and `ezc store rewrap` (which calls `run`) both get it and no command calls it itself.
- [ ] **Step 4:** Run the gate.
- [ ] **Step 5:** CLAUDE.md: the `access.apply` sharp edge gains the repo re-wrap, and the bot `--repo` one loses "Decision 5"; commit `feat(access): an access change re-wraps the repos checked out here`.

### Task 7: End to end, docs, version 0.5.0

**Files:**
- Modify: `cli/cmd/ezc/access_test.go` (or a new `cli/cmd/ezc/repo_test.go` using `newMachine`, `watch`, `wantShutOut`)
- Modify: `README.md`, `docs/reference.md`, `docs/access.md`, `docs/ci.md`, `docs/troubleshooting.md`; Create: `docs/repos.md`
- Modify: `cli/cmd/ezc/main.go`, the version in `cli/cmd/ezc/store_test.go` and `cli/internal/cli/cli_test.go`; `AGENTS.md` (parts built)

- [ ] **Step 1: Write `TestRepoEndToEnd`** with real git and sops: machine A sets up a store, `ezc repo init` and `ezc set API_TOKEN --type secret` in a repo that is a git repo with a commit of its `.ezc/`, `ezc repo add`; B joins and is approved (the repo is re-wrapped for B; A commits it in the repo); `ezc user remove b` re-wraps the repo, lists `common.enc.yaml/API_TOKEN`, and `wantShutOut` holds for B's key on the repo's file across the repo's commits; `ezc user rotate` on A leaves the repo's file readable with the new key (`ezc get API_TOKEN --reveal` in the repo) and `wantShutOut` holds for the old key; `ezc unset -g` of a name the repo requires is refused; `ezc repo check --all` exits 0. `watch.done` finds no value or private key in any output or commit.
- [ ] **Step 2:** Run `EZC_REQUIRE_SOPS=1 EZC_REQUIRE_GIT=1 go test ./cmd/ezc/ -run RepoEndToEnd`; fix what it finds in the owning package, with a unit test there.
- [ ] **Step 3:** Docs: `docs/repos.md` (the registry, checkouts, check, uses, the unset guard, re-wrap after access changes, decision 7's list); README's command table gains the `repo` row's subcommands, the Documentation table gains Repos, and "Status and known limits" loses the two part-5 limits and says 0.5; `docs/reference.md` gains every new command, flag and exit status and `repos.yaml`; `docs/access.md` and `docs/ci.md` say repos are re-wrapped and `--repo` must be registered. Check every command and flag named exists in `ezc help`, and every relative link resolves.
- [ ] **Step 4:** Version `0.5.0`.
- [ ] **Step 5:** Run the gate and both greps; commit `feat: ezc 0.5.0 — the repo registry`.
