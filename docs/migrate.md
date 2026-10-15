---
title: Migrating from fsconfig
nav_order: 8
---

# Migrating from fsconfig

`ezc migrate` moves a store or a repo from fsconfig's `.fs-config/` folder to ezc's `.ezc/`, and proves that the environment it composes is the same before and after. The `ezc-migrate` skill, which `ezc setup` installs, walks an agent through every repo in order.

```sh
ezc migrate --dry-run   # prove and report; write nothing
ezc migrate             # prove, then write .ezc/
```

It acts on the working directory, which must hold `.fs-config/` and no `.ezc/`. It runs no git: the result is left in the working tree for you to review and commit.

## The order

1. **The store first.** In the old store's checkout, run `ezc migrate`. Commit `.ezc/` and push.
2. **Each machine** runs `ezc setup --store <that repo>`, so that its ezc store is the migrated one. If migrate said the encrypted file is not encrypted for exactly `recipients.yaml`'s keys, run `ezc store rewrap` once.
3. **Each repo**: `ezc migrate --dry-run`, then `ezc migrate`, act on what it reports, `ezc repo add`, and commit.
4. **When every repo has moved**, delete `.fs-config/` from the store and retire fsconfig.

## The proof

Nothing is written until the proof passes. migrate builds the new `.ezc/` in a temporary folder, then composes the environment twice:

- **before**, with `fsconfig codegen shell`, which must be on PATH: the old tool is the authority on what it composes;
- **after**, with ezc's resolver over the new folder.

For the store it composes the store alone. For a repo it composes every environment the repo has an `env.<name>.yaml` for, over your ezc store, so the store must be migrated and set up first. Every name and every value must be identical. A difference is listed by name, never by value:

```
✓ local: 23 names, identical
✗ prod: API_URL differs
The environment would change. Nothing was changed.
```

and migrate exits 1. A repo with no `env.<name>.yaml` is proved for the store alone, and migrate says that ezc, with no environment named, also loads the repo's common files, which fsconfig did not.

## What it changes

In the **store**, `.ezc/` is written beside `.fs-config/`, which fsconfig still reads for every repo that has not moved yet:

| From `.fs-config/` | In `.ezc/` |
| --- | --- |
| `global.yaml`, `global.enc.yaml` | `common.yaml`, `common.enc.yaml`, byte for byte |
| `recipients.yaml` | `recipients.yaml` in ezc's form: `purpose: human` becomes `kind: user`, `machine` becomes `kind: bot` with its `repos`, each dated today and added by `migrate` |
| the bots' `repos` | `repos.yaml` |
| the encryption policy | written again for `recipients.yaml`'s keys |
| `manifest.yaml` | the same, with `$schema` pointing at ezc's schema |
| any other file | copied |

In a **repo**, `.ezc/` takes the place of `.fs-config/`: the same files, `manifest.yaml`'s `$schema` pointed at ezc's schema, and the `env.sh` shim. `mise.toml` is rewritten: the shim's path becomes `.ezc/env.sh`, `fsconfig env`, `codegen python`, `manifest` and `doctor` become their ezc commands, `fsconfig codegen shell` becomes `ezc env export`, and `.fs-config/` becomes `.ezc/`. The repo's own encryption policy is copied as it is; `ezc repo rewrap` writes it from the store's registry when you need it to.

A failure while writing puts the folder back: no `.ezc/`, `.fs-config/` and `mise.toml` as they were.

## What it reports

migrate changes nothing else. After the proof it lists, as `!` lines:

- every file that still names fsconfig, with the first line numbers: a generated `env.py` (run `ezc codegen python` again), `.envrc`, a README, a CI workflow;
- every file under `.github/` that passes `SOPS_AGE_KEY` to a job. ezc's CI reads `EZC_AGE_KEY`: run `ezc bot rotate <name>` when the store's registry already has a bot for the repo, else `ezc bot add`, and follow [Bots and CI](ci.md#bots-and-ci).

## Putting it back

The store keeps `.fs-config/` until you delete it, and a repo's old folder is in its git history: `git restore --staged --worktree .fs-config mise.toml && rm -rf .ezc` undoes a repo's migration before you commit it.
