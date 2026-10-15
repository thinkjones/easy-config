---
name: ezc-migrate
description: Move a store and the repos that use it from fsconfig (.fs-config/) to ezc (.ezc/) with ezc migrate, in the right order, proving each environment unchanged. Use when a repo or a store still has a .fs-config/ folder, mise.toml sources fsconfig's env.sh, or someone asks to "migrate from fsconfig", "move to ezc" or "retire fs-config".
---

# Migrate from fsconfig to ezc

`ezc migrate` converts one folder at a time and proves the environment is identical before it writes anything. This skill is the order and the follow-up for a whole set of repos. Never edit `.fs-config/` or `.ezc/` by hand to make a proof pass.

## Rules

- **Store first, then repos.** A repo's proof composes its environment over the ezc store, so the store must be migrated and set up on this machine first.
- **Dry run first, every time.** `ezc migrate --dry-run` proves and reports and writes nothing.
- **A failed proof is a finding, not an obstacle.** `✗ <env>: <NAME> differs` means ezc would load something else. Find out why (usually a value fsconfig expanded differently, or a value the store gained since), fix the source, and run it again. Never print a value to compare: `ezc get NAME --reveal` and `fsconfig codegen shell` print secrets.
- **migrate runs no git.** Review `git status` and the diff, and commit yourself.
- **fsconfig must stay on PATH** until every repo is migrated: the proof runs `fsconfig codegen shell`.

## 1. Preflight

```bash
command -v fsconfig ezc sops git
ezc version
fsconfig doctor          # the old setup must be healthy, or the proof cannot run
```

List the repos: the old store's `recipients.yaml` names the CI repos, and each checkout with a `.fs-config/` folder is one (`ls ~/dev/repos/*/.fs-config`).

## 2. The store

In the old store's checkout (the repo whose `.fs-config/` holds `global.yaml`):

```bash
ezc migrate --dry-run
ezc migrate
git status && git diff --stat
```

It writes `.ezc/` beside `.fs-config/` and keeps `.fs-config/`, which fsconfig still reads for the repos not yet moved. Check `.ezc/recipients.yaml` (people are `kind: user`, CI is `kind: bot` with its repos) and `.ezc/repos.yaml`. Commit `.ezc/` and push.

On this machine and every other one:

```bash
ezc setup --store <owner/store>     # the repo you just pushed
ezc doctor
```

If migrate printed `! common.enc.yaml is not encrypted for exactly recipients.yaml's keys`, run `ezc store rewrap` once, on one machine.

## 3. Each repo

For each repo, in its checkout:

```bash
ezc migrate --dry-run
ezc migrate
```

Then act on every `!` line it printed:

- **A file that still names fsconfig.** A generated `env.py` or similar: run the repo's codegen again (`ezc codegen python -m .ezc/manifest.yaml -o <the same path>`). `.envrc`: replace any fsconfig line with `source_env .ezc/env.sh` if the repo uses direnv, and keep `export APP_ENV=…` (ezc reads it). A README or docs: update the commands. Comments in `mise.toml`: reword them.
- **A workflow that passes `SOPS_AGE_KEY`.** ezc's CI reads `EZC_AGE_KEY` and clones the store with `EZC_STORE_TOKEN`. Run the command the line names (`ezc bot rotate <name>` when the store's registry already has the bot, else `ezc bot add <name> --repo <owner/repo>`), save a token that can read the store as `EZC_STORE_TOKEN`, replace the old setup action with `ezc setup --ci --store <owner/store>`, and delete the old `SOPS_AGE_KEY` and store-token secrets once the job is green.

Then:

```bash
ezc repo add            # registers the repo in the store's repos.yaml (a commit in the store)
ezc doctor              # if it reports the repo's recipients, run ezc repo rewrap
ezc env && ezc env prod # each environment
mise run <a-real-task>
git add -A .ezc .fs-config mise.toml && git commit
```

Push the store too: `ezc store push` sends the `repo add` commits.

## 4. Finish

When `ls ~/dev/repos/*/.fs-config` finds nothing:

```bash
cd <the store's checkout> && git rm -r .fs-config && git commit -m "Remove .fs-config: every repo uses ezc" && git push
```

Then retire fsconfig: remove its binary and shim (`~/.local/bin/fsconfig`, `~/.local/share/fsconfig/`) and the `~/.config/fs-config` link on each machine, and its skill.

## Putting a repo back

Before it is committed: `git restore --staged --worktree .fs-config mise.toml && rm -rf .ezc`.
