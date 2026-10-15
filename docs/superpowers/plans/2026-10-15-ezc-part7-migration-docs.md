# ezc Part 7: Migration and Docs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ezc migrate [--dry-run]` converts a store or a repo from the old tool's layout to ezc's and proves the resolved environment unchanged; the `ezc-convert` and `ezc-migrate` skills ship embedded and `ezc setup` installs them; `docs/` becomes a Just the Docs site.

**Architecture:** A new package `cli/internal/migrate` holds everything that knows the old tool: it builds the new `.ezc` in a temporary staging folder, computes the environment before (by running the old tool's own `codegen shell`, the authority on what it composes) and after (with ezc's one resolver over the staging folder), compares names and values in memory, and only when they are identical copies the staging folder into place. `cli/internal/cli/migratecmd.go` wires it to `System` and never names the old tool. Skills are embedded in `cli/share/skills/` and written by setup.

**Tech Stack:** Go (existing module), Cobra, `embed`, Jekyll with the Just the Docs remote theme (as the reference repo uses, `just-the-docs/just-the-docs@v0.10.1`).

**Spec:** `docs/superpowers/specs/2026-10-10-ezc-design.md`, sections "Migration from fs-config", "Commands → Other" and "Parts" (part 7), and the Machine table's `ezc setup` row ("install the skills").

## Global Constraints

- Everything in the earlier parts' constraints still binds: module `github.com/thinkjones/easy-config/cli`; commands reach the outside world only through `System`; `UsageError` exits 2; Markdown prose is not hard-wrapped; user docs change in the same commit as the code; `docs/superpowers/` is history (only this plan may be edited).
- The gate: `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops` and `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run schema:check` pass at the end of every task. Never run `mise trust`.
- **Name rule, as changed by the user on 2026-10-15:** the old tool's names may appear only in `cli/internal/migrate/` and `cli/share/skills/ezc-migrate/`. The check becomes `grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/ | grep -v -e '^cli/internal/migrate/' -e '^cli/share/skills/ezc-migrate/'`, which must print nothing. `Four Signals` and `ipm` stay banned everywhere under `cli/`, including the two exempt paths: `grep -rniE 'four signals|\bipm\b' cli/` prints nothing.
- The sops-name rule is unchanged: `grep -rnE 'sops|SOPS_|ENC\[' cli --include='*.go' --exclude='*_test.go' | grep -v -e 'cli/internal/backend/sops/' -e 'cli/internal/cli/scope.go'` prints nothing. `migrate` learns the key variable a CI workflow used from `Options`, filled in `scope.go`; it copies the policy file without naming it.
- No migrate output holds a value: names, counts, paths and line numbers only. Old-tool stderr is never passed through.
- Migration is built, not run: no task migrates the real store or a real repo in place. Task 7 runs it on copies in the scratchpad only.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Decisions

1. **The "before" environment is the old tool's own output.** `migrate` runs `fsconfig codegen shell [ENV]` through `System.Exec` and parses its `export NAME='…'` lines. Porting the old resolver would put a second resolver in ezc (the resolver is the only place `~`, `$HOME` and `$NAME` are expanded) and would prove ezc against ezc's idea of the old tool. Cost if wrong: the proof needs the old tool on PATH, so a machine without it cannot migrate; that is the machine that has nothing to migrate.
2. **Store or repo is decided from `.fs-config/`:** it is a store when it holds `global.yaml`, `global.enc.yaml` or `recipients.yaml`; otherwise a repo. No `.fs-config/` is `Nothing to migrate here: no .fs-config/ folder.` (exit 1). A `.ezc/` already there is `<dir> already has .ezc/. Nothing was changed.` (exit 1), before anything runs.
3. **The store keeps `.fs-config/`; a repo loses it.** The old tool reads repos through the old store until every repo has moved, so the store migration writes `.ezc/` beside `.fs-config/` and says to remove it once every repo has migrated. A repo's `.fs-config/` is removed after `.ezc/` is in place, since the repo's `mise.toml` is rewritten to ezc in the same run.
4. **Nothing is written until the proof passes.** The new `.ezc` is built in `os.MkdirTemp("", "ezc-migrate-")`; `--dry-run` stops after the proof and removes it. Applying copies the staging files into `<dir>/.ezc` (created with `os.Mkdir`, so a race with another `.ezc` fails), writes `mise.toml` through a temporary file and rename, then removes `.fs-config/` (repo only). A failure before the removal removes `.ezc/` and puts `mise.toml`'s bytes back. No git runs: the result stays in the working tree for its owner to commit, as `ezc repo rewrap` leaves it.
5. **Environments proved.** A store: the old tool with `FS_CONFIG_HOME=<dir>/.fs-config`, run in an empty temporary folder with no `APP_ENV` (it composes the store alone), against `resolve.Load` with `StoreDir` = staging and no repo. A repo: each `ENV` named by a `.fs-config/env.ENV.yaml`, the old tool run in `<dir>` with `ENV` as its argument and `APP_ENV` dropped from its environment, against `resolve.Load` with `StoreDir` = the ezc store's `.ezc` and `RepoDir` = staging. A repo with no `env.*.yaml` is proved as the store alone (the old tool loads only the store when no environment file exists) against `resolve.Load` with no repo, plus the line `! with no environment named, ezc also loads this repo's common files; the old tool did not.` A repo whose ezc store is missing is refused: `Set up the ezc store first: run ezc migrate in the old store's folder, commit and push it, then ezc setup --store <it>.`
6. **The comparison** is of `Result.Exported()` against the parsed exports: identical name sets and byte-identical values. Each difference is one line naming the environment and the name: `✗ <env>: <NAME> differs`, `✗ <env>: <NAME> only before`, `✗ <env>: <NAME> only after`, then `The environment would change. Nothing was changed.` and exit 1. Success per environment is `✓ <env>: <n> names, identical` (`(store)` for the store-alone case).
7. **Recipients conversion:** `purpose: human` → `kind: user`, `purpose: machine` → `kind: bot` with its `repos`; any other purpose, a name `recipients.ValidName` refuses, or a key `Backend.ValidRecipient` refuses is an error naming the entry by position (`recipient 2`), never by its text. Every entry gets `added` = today (`YYYY-MM-DD` of `Options.Now`) and `addedBy: migrate`. Each bot repo that passes `registry.ValidID` seeds `repos.yaml` with the same `added`/`addedBy`; one that does not is an error. The written `recipients.yaml` must load with `recipients.Load`.
8. **The store's policy** is written with `Backend.WritePolicy(staging, registry.Keys())`, replacing the copied one. A repo's policy file is copied as it is; `ezc repo rewrap` regenerates it later.
9. **Rewrites:** in each `manifest.yaml`, a top-level `$schema:` line is replaced with `$schema: <Options.SchemaURL>`; nothing else in a manifest changes. In the store, `global.yaml` and `global.enc.yaml` are renamed `common.yaml` and `common.enc.yaml`, bytes unchanged. A repo gets `.ezc/env.sh` (`share.EnvSh`). In the repo's `mise.toml` (if there is one), these replacements, in this order: the string `{{ env.HOME }}/.local/share/fsconfig/env.sh` → `.ezc/env.sh`; `fsconfig codegen shell` → `ezc env export`; `fsconfig codegen python` → `ezc codegen python`; `fsconfig manifest` → `ezc manifest`; `fsconfig doctor` → `ezc doctor`; `fsconfig env` → `ezc env`; `.fs-config/` → `.ezc/`. Comments included. Every line still naming the old tool afterwards is reported, not changed.
10. **What is reported, never changed** (`!` lines after the proof, on the real run and the dry run): in a repo, every text file under `<dir>` (skipping `.git`, `.ezc`, `.fs-config`, `node_modules`, `.venv`, `venv`, `vendor`, `dist` and `build` folders, files over 1 MiB and files with a NUL in their first 8 KiB) with a line naming the old tool (`fsconfig`, `fs-config`, case-insensitive): `! <path>:<line>[,<line>…] names the old tool`, at most 3 line numbers per file and 20 files, then `! and <n> more files`. Every file under `.github/` that names `Options.KeyTextVariable`: `! <path> passes <var> to CI; ezc reads EZC_AGE_KEY. Run ezc bot rotate <name>` when a bot of the ezc store's registry lists this repo's ID (`githubSlug` of `origin` is not available here, so the ID is matched against the repo folder's base name and every bot repo's part after `/`), else `Run ezc bot add <name> --repo <owner/repo>`, then see docs/ci.md.
11. **Next steps** printed after a real run. Store: `Next: commit .ezc/ and push, then on each machine ezc setup --store <this repo>. Remove .fs-config/ once every repo has migrated.` and, when the encrypted files' recipients (`Backend.Recipients`) differ from `registry.Keys()`, `! common.enc.yaml is not encrypted for exactly recipients.yaml's keys; run ezc store rewrap once the store is set up.` Repo: `Next: ezc repo add, commit .ezc/ and mise.toml, and run ezc doctor.`
12. **Skills** live at `cli/share/skills/<name>/SKILL.md`, embedded as `share.Skills` (`embed.FS`). `ezc setup` (not `--ci`) writes each to `<claude>/skills/<name>/SKILL.md`, where `<claude>` is `CLAUDE_CONFIG_DIR` when set, else `$HOME/.claude`: `+ installed the skill <name>` when missing, `✓ the skill <name>` when identical, `+ updated the skill <name>` when different (the skills are ezc's own files under its names). A write failure is `! could not install the skill <name> (<reason>)` and never fails setup. They are written after the config and before `Next:`.
13. **The docs site** is `docs/` served by GitHub Pages from `main` `/docs`, configured like the reference repo (`remote_theme: just-the-docs/just-the-docs@v0.10.1`, `jekyll-relative-links`, `exclude: [superpowers/]`). `docs/index.md` is the home (`permalink: /`). Each page gets front matter (`title`, `nav_order`); the `[← README](../README.md)` back-links are removed because they point outside the site. Turning Pages on is a repository setting and is the user's step, named in the PR.

## Review Focus

1. A value holding `'`, a newline, `$`, `\` or a trailing space survives `parseExports` exactly: the old tool quotes with `'…'\''…'`, and a wrong parse is a false "differs" or, worse, a false "identical". (Task 1, `TestParseExportsRoundTripsEveryByte`.)
2. A failure while applying leaves the folder as it was: no `.ezc/`, `.fs-config/` intact, `mise.toml` byte-identical. (Task 3, `TestRepoApplyFailurePutsBack`.)
3. `--dry-run` changes no byte under `<dir>`, store or repo. (Tasks 2 and 3, `TestDryRunWritesNothing*`.)
4. The old tool missing, failing, or printing something that is not exports refuses the migration before any write, and its stderr is never echoed. (Task 2, `TestOldToolFailureRefuses`.)
5. No secret value appears in any output, on success, on a difference or on a failure. (Tasks 2 and 3 assert the output lacks every value they set.)

---

## File structure

```
cli/internal/migrate/
  migrate.go        Options, Run, ErrNotProved, OldTool, Short/Long help text; detection (decision 2); apply (decision 4)
  exports.go        parseExports: the old tool's shell output → map
  old.go            before(): run the old tool for one environment
  recipients.go     convert(): old recipients.yaml → recipients.Registry + registry.Registry
  stage.go          stageStore, stageRepo: the staging folder
  proof.go          prove(): compare, and the ✓/✗ lines
  mise.go           rewriteMise
  scan.go           oldNames, ciFindings (decision 10)
  *_test.go
cli/internal/cli/migratecmd.go    newMigrateCmd(sys)
cli/internal/cli/scope.go         keyTextVariable const (the CI key variable's name)
cli/share/share.go                Skills embed.FS
cli/share/skills/ezc-convert/SKILL.md
cli/share/skills/ezc-migrate/SKILL.md
cli/internal/setup/skills.go      InstallSkills
docs/_config.yml, docs/index.md, docs/migrate.md, front matter on every page
```

---

### Task 1: The old tool's output, the recipients and mise.toml

**Files:**
- Create: `cli/internal/migrate/exports.go`, `recipients.go`, `mise.go`, `migrate.go` (package doc and `OldTool` only)
- Test: `cli/internal/migrate/exports_test.go`, `recipients_test.go`, `mise_test.go`

**Interfaces:**
- Produces: `const OldTool = "fsconfig"`; `func parseExports(out []byte) (map[string]string, error)`; `func convert(old []byte, today string, valid func(key string) error) (recipients.Registry, registry.Registry, error)`; `func rewriteMise(text string) string`.

- [ ] **Step 1: Write the failing tests**

`TestParseExportsRoundTripsEveryByte`: for values `""`, `"plain"`, `"it's"`, `"a\nb"`, `"$HOME and $$"`, `"back\\slash"`, `"trail "`, `"'''"`, build the old tool's output with `"export " + name + "=" + "'" + strings.ReplaceAll(v, "'", `'\''`) + "'\n"` per name (names `A0`…`A7`); `parseExports` returns exactly those values. `TestParseExportsRefusesOtherText`: `echo hi\n`, `export A=unquoted\n`, `export A='open\n`, `export 1A='x'\n` each return an error whose text is `the old tool printed something that is not an export line` and holds none of the input.

`TestConvertRecipients`: the old file (a `recipients:` map with `gene: {key: <valid>, purpose: human, repos: []}` and `ci: {key: <valid2>, purpose: machine, repos: [owner/a, owner/b]}`, with the comments the real file has) gives `gene` kind `user` with no repos, `ci` kind `bot` with repos `[owner/a owner/b]`, both `added` = today and `addedBy` = `migrate`; the repos registry holds `owner/a` and `owner/b` with the same. Errors: purpose `robot` → `recipient 1: purpose must be human or machine`; a name `Bad Name` → `recipient 1: not a valid name …` (the error does not contain `Bad Name`); a key `valid` refuses → `recipient 2: the key is not valid`; a repo `../x` → `recipient 2: a repo is not a valid repo ID`. Entries are numbered in the file's order (parse with `yaml.Node` to keep it).

`TestRewriteMise`: the input holding `_.source = "{{ env.HOME }}/.local/share/fsconfig/env.sh"`, `run = "fsconfig env"`, `run = "fsconfig codegen python -m .fs-config/manifest.yaml -o src/x/env.py"`, `eval "$(fsconfig codegen shell)"`, `fsconfig doctor || status=$?`, `# fsconfig composes the store` gives `_.source = ".ezc/env.sh"`, `run = "ezc env"`, `run = "ezc codegen python -m .ezc/manifest.yaml -o src/x/env.py"`, `eval "$(ezc env export)"`, `ezc doctor || status=$?`, and the comment unchanged; `fsconfig envelope` is not rewritten (the replacement of `fsconfig env` needs a word boundary after it).

- [ ] **Step 2: Run them to see them fail**

Run: `cd cli && go test ./internal/migrate/`
Expected: FAIL to build: `undefined: parseExports` (and the others).

- [ ] **Step 3: Implement** `parseExports` as a small scanner over exactly `export NAME=` followed by one or more `'…'` segments joined by `\'`, ending at a newline (names match `manifest.ValidName`); `convert` with `yaml.v3`, building `recipients.Recipient{Key, Kind, Added, AddedBy, Repos}` and `registry.Repo{Added, AddedBy}`, then saving to a temporary folder and loading back with `recipients.Load` (its error, made from the file path, is returned as `recipients.yaml would not load: <reason>` with the temporary path removed); `rewriteMise` with the decision 9 replacements in order, using a regexp with `\b` for the subcommand forms.

- [ ] **Step 4: Run them to see them pass**

Run: `cd cli && go test ./internal/migrate/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/internal/migrate
git commit -m "feat(migrate): read the old tool's exports, convert its recipients, rewrite mise.toml"
```

### Task 2: Migrating a store

**Files:**
- Modify: `cli/internal/migrate/migrate.go`
- Create: `cli/internal/migrate/old.go`, `stage.go`, `proof.go`
- Test: `cli/internal/migrate/store_test.go`

**Interfaces:**
- Consumes: Task 1's functions; `resolve.Load`, `resolve.Options`, `Result.Exported`; `backend.Backend` (`WritePolicy`, `Recipients`, `Decrypt`, `ValidRecipient`); `recipients.Registry.Keys`, `Save`; `registry.Registry.Save`; `proc.Runner`, `proc.Command`.
- Produces:

```go
type Options struct {
	Dir             string    // the folder holding .fs-config/
	Home            string
	Now             time.Time
	Environ         []string  // the old tool's environment is built from it
	Exec            proc.Runner
	LookPath        func(string) (string, error)
	Backend         backend.Backend
	StoreEzc        string    // the ezc store's .ezc, for a repo's proof
	SchemaURL       string
	KeyTextVariable string    // the variable a CI workflow passed the key in
	Stdout          io.Writer
	DryRun          bool
}
var ErrNotProved = errors.New("the environment would change")
func Run(o Options) error
```

`Run` returns `ErrNotProved` after printing the ✗ lines and `Nothing was changed.`; any other error has said nothing yet and is a sentence for the caller to print.

- [ ] **Step 1: Write the failing tests** (backend `*backend.Fake`, `Exec` a fake that answers `OldTool` with canned exports and records each `proc.Command`)

- `TestStoreMigrates`: `.fs-config/` with `global.yaml` (`A: one`, `B: ~/x`), `global.enc.yaml` (Fake-encrypted `S: s3cret-value`), `manifest.yaml` with `$schema: https://example.com/old.json`, `recipients.yaml` from Task 1, the policy file and an extra `notes.txt`. The fake old tool prints `A`, `B` (with `Home` expanded) and `S`. After `Run`: `.ezc/common.yaml` and `.ezc/common.enc.yaml` hold the old bytes, `manifest.yaml`'s `$schema` is `SchemaURL` and every other line is unchanged, `recipients.yaml` loads with two recipients, `repos.yaml` loads with the bot's repos, `notes.txt` is copied, the policy is for `registry.Keys()` (`Backend.PolicyRecipients`), `.fs-config/` is untouched, and the output holds `✓ (store): 3 names, identical` and `Next:` and does not hold `s3cret-value`. The recorded command is `OldTool codegen shell` with `FS_CONFIG_HOME=<dir>/.fs-config` in its `Env`, no `APP_ENV`, and a `Dir` outside `<dir>`.
- `TestStoreProofDiffers`: the old tool prints `S` with another value and an extra `Z`: `Run` returns `ErrNotProved`; the output holds `✗ (store): S differs`, `✗ (store): Z only before`, `Nothing was changed.`, and neither value; no `.ezc/` exists.
- `TestDryRunWritesNothingInAStore`: snapshot every path and its bytes under `<dir>`; `Run` with `DryRun`; the snapshot is identical, the output ends `Dry run: nothing was written.`, and the staging folder is gone (the fake records it as the `StoreDir` it saw; it does not exist afterwards).
- `TestOldToolFailureRefuses`: (a) `LookPath` does not find `OldTool`: the error is `fsconfig is not on PATH; the proof runs it to compose the environment as it stands.`; (b) it exits 1 with stderr `boom s3cret-value`: the error is `fsconfig codegen shell failed in <dir> (exit status 1). Run fsconfig env there to see why.` and holds neither `boom` nor the value; (c) it prints `oops`: the parse error from Task 1. No `.ezc/` in any case.
- `TestRefusals`: no `.fs-config/` → `Nothing to migrate here: no .fs-config/ folder.`; `.ezc/` present → `<dir> already has .ezc/. Nothing was changed.`; the old tool is never run for either.
- `TestStoreRecipientsDiffer`: `global.enc.yaml` encrypted for a key not in `recipients.yaml`: success, plus the `! common.enc.yaml is not encrypted for exactly recipients.yaml's keys; …` line.

- [ ] **Step 2: Run them to see them fail**

Run: `cd cli && go test ./internal/migrate/ -run 'Store|DryRun|OldTool|Refusals'`
Expected: FAIL: `undefined: Run` / `Options`.

- [ ] **Step 3: Implement** detection and refusals (decision 2), `before(o, env string, storeOnly bool) (map[string]string, error)` in `old.go` (environment: `o.Environ` without `APP_ENV` and `FS_CONFIG_HOME`, plus `FS_CONFIG_HOME` for a store; stdout parsed, stderr dropped), `stageStore` in `stage.go` (copy every regular file of `.fs-config/` with the renames and decisions 7–9; a folder or a symlink in `.fs-config/` is an error naming it through `recipients.Printable`), `prove` in `proof.go` (decision 6), and apply (decision 4). Print `Migrating the store in <dir>` first, then a `+` line per file written to `.ezc/` (or `would write` in a dry run).

- [ ] **Step 4: Run them to see them pass**

Run: `cd cli && go test ./internal/migrate/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/internal/migrate
git commit -m "feat(migrate): migrate a store, proved against the old tool's own output"
```

### Task 3: Migrating a repo

**Files:**
- Modify: `cli/internal/migrate/migrate.go`, `stage.go`
- Create: `cli/internal/migrate/scan.go`
- Test: `cli/internal/migrate/repo_test.go`, `scan_test.go`

**Interfaces:**
- Consumes: Tasks 1–2; `share.EnvSh`; `recipients.Load` (for the bot lookup in decision 10).
- Produces: `func stageRepo(o Options) (dir string, err error)`; `func oldNames(root string) []string` (the `!` lines); `func ciFindings(root, keyVar string, bots map[string][]string) []string`.

- [ ] **Step 1: Write the failing tests**

- `TestRepoMigrates`: an ezc store `.ezc` (`StoreEzc`) holding `common.yaml` `A: one`; a repo `.fs-config/` with `manifest.yaml`, `common.yaml` (`R: two`), `env.local.yaml` (`L: three`), `env.prod.yaml` (`P: four`), Fake-encrypted `common.enc.yaml` (`RS: repo-secret`), and a `mise.toml` as in Task 1. The fake old tool answers `codegen shell local` and `codegen shell prod` with the matching composition. After `Run`: `.ezc/` holds the same file names plus `env.sh` (`share.EnvSh`), `.fs-config/` is gone, `mise.toml` is rewritten, the output holds `✓ local: 4 names, identical`, `✓ prod: 4 names, identical`, the `Next:` line, and not `repo-secret`. The two recorded commands have `Dir` = `<dir>`, args `codegen shell local` / `codegen shell prod`, no `APP_ENV` in `Env`.
- `TestRepoNeedsTheEzcStore`: `StoreEzc` does not exist: the decision 5 refusal, before the old tool runs.
- `TestRepoWithNoEnvironmentFile`: only `common.yaml` in `.fs-config/`: one proof `(store)` against the store alone, and the `! with no environment named …` line.
- `TestRepoApplyFailurePutsBack`: make `<dir>` read-only after staging is built (a hook in `Options`, `afterProof func()`, test-only, nil in production) so creating `.ezc` fails: the error says `could not write .ezc/ (<reason>). Nothing was changed.`, `.fs-config/` and `mise.toml` are byte-identical, no `.ezc/`. A second case fails the `mise.toml` rename (make `mise.toml` a folder after the proof): `.ezc/` is removed and `.fs-config/` is intact.
- `TestDryRunWritesNothingInARepo`: as for the store; the output still lists the `!` findings.
- `TestOldNames`: a tree with `README.md` naming the old tool on lines 2 and 9, `src/env.py` naming it on 4 lines, `node_modules/x.js` and `.git/config` naming it, a 2 MiB file and a binary file naming it: the lines are `! README.md:2,9 names the old tool` and `! src/env.py:1,2,3 names the old tool` (first three), nothing else; 25 such files give 20 lines and `! and 5 more files`.
- `TestCIFindings`: `.github/workflows/a.yml` holding `SOPS_AGE_KEY` (the test passes it as `keyVar`) in a repo folder named `b`, with `bots` = `{"ci": ["owner/a", "owner/b"]}`: `! .github/workflows/a.yml passes SOPS_AGE_KEY to CI; ezc reads EZC_AGE_KEY. Run ezc bot rotate ci, then see docs/ci.md.`; with `bots` empty: `… Run ezc bot add <name> --repo <owner/repo>, then see docs/ci.md.`

- [ ] **Step 2: Run them to see them fail**

Run: `cd cli && go test ./internal/migrate/ -run 'Repo|OldNames|CIFindings'`
Expected: FAIL (`undefined: oldNames`, and repo runs report the store path or refuse wrongly).

- [ ] **Step 3: Implement** `stageRepo` (copy, `$schema`, `env.sh`), the repo branch of `Run` (decisions 3–6, 9–11), apply with the put-back order of decision 4, `oldNames` and `ciFindings` (decision 10). `Run` prints `Migrating the repo in <dir>`.

- [ ] **Step 4: Run them to see them pass**

Run: `cd cli && go test ./internal/migrate/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/internal/migrate
git commit -m "feat(migrate): migrate a repo: .ezc, the shim, mise.toml, and what still names the old tool"
```

### Task 4: `ezc migrate`, with real sops

**Files:**
- Create: `cli/internal/cli/migratecmd.go`, `cli/internal/cli/migratecmd_test.go`, `cli/internal/migrate/sops_test.go`, `docs/migrate.md`
- Modify: `cli/internal/cli/cli.go` (register), `cli/internal/cli/scope.go` (`const keyTextVariable = "SOPS_AGE_KEY"`, used by the three places that spell it today), `cli/internal/migrate/migrate.go` (`Short`, `Long`), `docs/reference.md`, `README.md`, `CLAUDE.md`, `AGENTS.md`

**Interfaces:**
- Consumes: `migrate.Run`, `migrate.Options`, `migrate.ErrNotProved`, `migrate.Short`, `migrate.Long`, `migrate.OldTool` (tests only); `storePaths`, `homeDir`, `backendFor`, `schemaID`, `keyTextVariable`.
- Produces: `newMigrateCmd(sys System) *cobra.Command`: `ezc migrate [--dry-run]`, `noArgs`; `Dir` is `sys.Getwd()`; `ErrNotProved` becomes `&ExitError{Code: 1}`; another error is printed as `ezc: <it>` like every command.

- [ ] **Step 1: Write the failing tests**

`TestMigrateCommandWiresTheStore` (in `cli`): a fake `System` whose working directory holds an old store, `execFn` answering `migrate.OldTool`, `paths[migrate.OldTool]` set; `ezc migrate --dry-run` exits 0 and prints `✓ (store)`; `ezc migrate` writes `.ezc/` and its `manifest.yaml`'s `$schema` is `schemaID`. `TestMigrateCommandNotProved`: a differing old tool → `*ExitError` code 1. `TestMigrateRejectsArguments`: `ezc migrate x` is a `UsageError`.

`TestRealSopsStoreMigrates` (in `migrate`, skipped without sops unless `EZC_REQUIRE_SOPS=1`, as the other real-sops tests are): a real age key, `global.enc.yaml` written with the real backend's `Set` under a policy for that key, the fake old tool printing the matching exports; `Run` succeeds, `common.enc.yaml` decrypts with the real backend to the same pairs, and the policy it wrote is for exactly the registry's keys.

- [ ] **Step 2: Run them to see them fail**

Run: `cd cli && go test ./internal/cli -run Migrate && EZC_REQUIRE_SOPS=1 go test ./internal/migrate -run RealSops`
Expected: FAIL: `unknown command "migrate"`; the real-sops test fails only if the store path is wrong for real sops (it may pass at once, since Task 2 built the code: then read its output and confirm it ran, not skipped).

- [ ] **Step 3: Implement** `newMigrateCmd`, the constant, and the docs: `docs/migrate.md` (when to use it, store first then each repo, what it changes and reports, the proof, `--dry-run`, putting it back with git, removing `.fs-config/` at the end), `docs/reference.md`'s row, the README's command list, and the sharp edges in `CLAUDE.md` and `AGENTS.md`: the name-rule exemption and its new grep; migrate's proof runs the old tool; nothing is written before the proof; the store keeps `.fs-config/`.

- [ ] **Step 4: Run the gate and the three greps**

Run: `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops && MISE_TRUSTED_CONFIG_PATHS=$PWD mise run schema:check`, then the name-rule grep, the `four signals|ipm` grep and the sops-name grep from Global Constraints.
Expected: gate passes; all three greps print nothing.

- [ ] **Step 5: Commit**

```bash
git add -A cli docs README.md CLAUDE.md AGENTS.md
git commit -m "feat: ezc migrate"
```

### Task 5: The skills, installed by setup

**Files:**
- Create: `cli/share/skills/ezc-convert/SKILL.md`, `cli/share/skills/ezc-migrate/SKILL.md`, `cli/internal/setup/skills.go`, `cli/internal/setup/skills_test.go`
- Modify: `cli/share/share.go` (`//go:embed skills` → `var Skills embed.FS`), `cli/internal/cli/setupcmd.go`, `cli/internal/cli/setupcmd_test.go`, `docs/setup.md`, `README.md`

**Interfaces:**
- Produces: `func InstallSkills(claudeDir string, skills fs.FS) ([]SkillResult, error)` with `type SkillResult struct{ Name, Status string; Err error }` (`Status` one of `installed`, `kept`, `updated`, `failed`); the returned error is only for an `fs.FS` that cannot be read.

- [ ] **Step 1: Write the skills.** `ezc-convert`: the old convert skill (291 lines at `/Users/thinkjones/dev/ao/fs-config/cli/share/skills/fsconfig-convert/SKILL.md`) rewritten for ezc's commands and layout (`ezc repo init`, `ezc set`/`ezc set -g` in place of hand-edited files and `sops`, `.ezc/`, `EZC_ENV`, `ezc env`, `ezc codegen python`, `ezc repo add`, CI through `ezc bot add` and `ezc setup --ci` as `docs/ci.md` says), with no reference repo named and none of the old names (it is under the name rule). `ezc-migrate`: drives `ezc migrate` across the user's repos: the store first (dry run, run, review the diff, commit and push, `ezc setup --store` on each machine, `ezc store rewrap` if migrate said so), then each repo (dry run, run, act on every `!` line: regenerate codegen outputs, CI with `ezc bot rotate`/`add`, `.envrc`), `ezc repo add`, commit; finally remove `.fs-config/` from the store and retire the old tool. Front matter `name` and `description` like the old skill's.

- [ ] **Step 2: Write the failing tests.** `TestInstallSkills` (in `setup`, with `fstest.MapFS`): first run → both `installed`, files equal; second → both `kept`, no write (mtime unchanged); one file edited → `updated` and restored; a `claudeDir` that is a file → both `failed` with an error. `TestSetupInstallsSkills` (in `cli`): `ezc setup --new-local` prints `+ installed the skill ezc-convert` and `+ installed the skill ezc-migrate` before `Next:`, the files are under `$HOME/.claude/skills/`; with `CLAUDE_CONFIG_DIR` set they are under it; a second run prints `✓ the skill …` and setup's existing "second run prints only ✓ lines" test still passes; `ezc setup --ci` writes no skill.

- [ ] **Step 3: Run them to see them fail**

Run: `cd cli && go test ./internal/setup -run Skills && go test ./internal/cli -run 'Setup'`
Expected: FAIL: `undefined: InstallSkills`; the setup test finds no skill lines.

- [ ] **Step 4: Implement** `share.Skills`, `InstallSkills` (folders `0755`, files `0644`), and the call in `runSetup` (decision 12). Docs: `docs/setup.md` says what setup installs and where; the README mentions the two skills.

- [ ] **Step 5: Run the gate and the greps** as in Task 4. Expected: pass, nothing printed.

- [ ] **Step 6: Commit**

```bash
git add -A cli docs README.md
git commit -m "feat: the ezc-convert and ezc-migrate skills, installed by ezc setup"
```

### Task 6: The docs site, and 0.7.0

**Files:**
- Create: `docs/_config.yml`, `docs/index.md`, `docs/Gemfile`
- Modify: every page in `docs/*.md` (front matter, back-links), `README.md`, the version (where part 6 set 0.6.0), `CLAUDE.md` (the site's command)

- [ ] **Step 1: Write `docs/_config.yml`** after the reference repo's: `title: ezc`, the description from the README's first line, `url: https://thinkjones.github.io`, `baseurl: /easy-config`, `remote_theme: just-the-docs/just-the-docs@v0.10.1`, plugins `jekyll-remote-theme`, `jekyll-relative-links`, `jekyll-seo-tag`, `relative_links: {enabled: true, collections: false}`, `search_enabled: true`, `heading_anchors: true`, `aux_links` to the GitHub repo, `gh_edit_*` for `docs` on `main`, `footer_content: "ezc is MIT licensed."`, `exclude: [superpowers/, Gemfile, Gemfile.lock]`. No colour scheme, logo or brand of the reference repo's. `docs/Gemfile`: `gem "github-pages", group: :jekyll_plugins` and `gem "webrick"`.

- [ ] **Step 2: Front matter and navigation.** `index.md` (`title: Home`, `nav_order: 1`, `permalink: /`): what ezc is, install, the first three commands, and links to every page (taken from the README's opening, not a copy of the whole README). Then `nav_order` in reading order: setup 2, store 3, access 4, repos 5, ci 6, tui 7, migrate 8, reference 9, troubleshooting 10. Remove each `[← README](../README.md)` line. Links between pages stay `page.md` (relative-links turns them into page URLs).

- [ ] **Step 3: Check every link.** Run a link check over `docs/*.md` and `README.md` (a short script in the scratchpad: every relative link's file exists, and every `#anchor` matches a heading's GitHub/kramdown slug). Expected: no broken link. Then try a local build: `cd docs && mise x ruby@3.3 -- bundle install && mise x ruby@3.3 -- bundle exec jekyll build -d "$SCRATCH/site"`; expected: it builds and `$SCRATCH/site/index.html` and `$SCRATCH/site/migrate.html` exist. If Ruby 3 cannot be installed here, ledger a ruling and rely on the link check (cost: a theme error is found only when Pages builds).

- [ ] **Step 4: README and version.** The README gets the site's URL (`https://thinkjones.github.io/easy-config/`) with a note that it is published from `docs/`; the version becomes 0.7.0; `CLAUDE.md`'s Commands table gains the local build line.

- [ ] **Step 5: Run the gate and the greps.** Expected: pass, nothing printed.

- [ ] **Step 6: Commit**

```bash
git add -A docs README.md CLAUDE.md cli
git commit -m "docs: the ezc site; ezc 0.7.0"
```

### Task 7: The proof on copies of the real store and a real repo

No new code unless something fails; a failure is fixed under TDD in the task that owns the code, with its own commit.

- [ ] **Step 1:** Copy `/Users/thinkjones/dev/ao/fs-config` and `/Users/thinkjones/dev/repos/fs-news-digest` into the scratchpad (`cp -R`). Never run migrate in the originals.
- [ ] **Step 2:** In the store copy, with a temporary `EZC_HOME` and the real `HOME` (so the real key is read, nothing written outside the copy): `ezc migrate --dry-run`, then `ezc migrate`. Expected: `✓ (store): <n> names, identical`; `.ezc/` written in the copy; output holds names and paths only.
- [ ] **Step 3:** In the repo copy, with `EZC_STORE=<store copy>`: `ezc migrate --dry-run`, then `ezc migrate`. Expected: `✓ <env>` for each of the repo's environments; the `!` lines name the CI workflow and the codegen output; `ezc env` passes in the copy.
- [ ] **Step 4:** Delete the copies. Record the result (counts only) in the PR body.

---

## Self-review

- Spec coverage: store moves (decisions 7–9, Task 2), repo moves and `env.sh` and `mise.toml` (Task 3), CI's key reported (decision 10), the proof for every environment (decisions 5–6), `--dry-run` (Tasks 2–3), both skills (Task 5), setup installs them (Task 5, the spec's Machine row), the docs site (Task 6), not run on the real store (Task 7 uses copies).
- Types: `Options`, `Run`, `ErrNotProved`, `OldTool`, `parseExports`, `convert`, `rewriteMise`, `stageRepo`, `oldNames`, `ciFindings`, `InstallSkills`, `SkillResult` are named once and used with the same signatures.
