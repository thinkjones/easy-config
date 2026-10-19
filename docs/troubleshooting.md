---
title: When something is wrong
nav_order: 10
---

# When something is wrong

Start with `ezc doctor`. It checks the machine, the store and the repo and says what to do about each thing it finds. Each line is a check (`✓`), a warning (`!`) or a problem (`✗`). It prints names, counts and paths, never a value; it exits 1 when anything is broken, and warnings alone exit 0. It works without a key and outside a repo.

```sh
ezc doctor                 # everything
ezc doctor prod            # check the environment prod
ezc doctor --only store    # one part: machine, store or repo
```

For the detail of a single failure, `ezc env` lists what is wrong with the environment and `ezc store audit` what is wrong with the store.

## The machine

| What doctor finds | What it means and what to do |
| --- | --- |
| A missing key | A warning: an environment with no encrypted file still loads, and one that has any fails as a whole until the key is there. |
| A missing `sops` or `git` | Install it. `git` also fails when its version is older than 2.23. |
| A missing or invalid `config.toml` | `ezc setup` writes it. Doctor does not suggest setup while `EZC_STORE` is set, because setup does not run then. |

## The store

| Item | What it reports |
| --- | --- |
| `manifest` | A store manifest that does not parse. |
| `sync` | For a store that is a git repo, where it stands against its remote: in sync, local only, ahead or behind with the command that fixes it, or uncommitted changes. Diverged fails, as does a store whose state cannot be read, such as one that is not on a branch. It fetches by the same 10 minute rule as other commands and says when origin could not be reached. |
| `access` | Whether your key is one of the store's recipients. A key that is not fails: when this machine has asked with `ezc join`, doctor says that its request is waiting for approval and who to ask to run `ezc user approve`, and otherwise to run `ezc join <remote>`, or `ezc join --print` for a store with no remote. With the key in the environment as text, as in CI, the machine's public key is not known and the item is left out. A store with an encryption policy and no `recipients.yaml` is a warning that says to write one. |
| `recipients` | Whether every encrypted file is encrypted for the registry's recipients. A file that is not fails and names the files. Run `ezc user list` first and check that it names only the people who should read the store, because either side can be the wrong one: then `ezc store rewrap` makes the files match the registry, and asks before it lets a name in. |
| `requests` | The access requests waiting in the store: up to 5 names, then `and <n> more`. `ezc user requests` shows them. |
| `rotation` | The secrets that were readable by a removed key: a warning with the count. `ezc store status --due` lists them and `ezc set -g NAME` gives each a new value (with `--env` for an environment's file); `ezc unset -g NAME` clears the entry of a secret that is no longer in its file. A `rotation-due.yaml` that cannot be read fails. |
| `policy` | Encrypted files in a folder with no encryption policy. |
| `files` | Files in a `.ezc/` folder that `ezc` never reads, such as `common.yml` or `comon.yaml`, with the name it probably should have. |

## The repo

| What doctor finds | What to do |
| --- | --- |
| A repo manifest that does not parse | Fix the file; `ezc manifest validate` checks it. |
| An environment that does not resolve or does not pass the manifest | `ezc env` lists what is wrong. |
| `recipients`: the repo's encrypted files are not encrypted for the store's recipients | Run `ezc repo rewrap`, then commit the result in the repo. It is refused until the store itself agrees with `recipients.yaml` (`ezc store rewrap`). |

## A file this machine's key does not open

`ezc store rewrap` (and any other change to who can read the store) opens each encrypted file with this machine's key before it re-wraps it. When it stops with `cannot decrypt … no key on this machine is one of its recipients`, the file is encrypted for a list that does not hold the key `config.toml` names. Nothing was changed.

- **Somebody else can read the store.** Ask them to run `ezc store pull` and `ezc store rewrap`, then pull. This is the usual way out.
- **You rotated this machine's key and the file is still encrypted for the old one.** `ezc user rotate` takes itself back when its push fails, so this needs an older `ezc` or a file put back by hand. The old key file is still beside the new one (`ezc user rotate` never deletes it). `ezc` reads with one key only, so use `sops` once, in the store's `.ezc/`, with the old file: `SOPS_AGE_KEY_FILE=<old key file> sops rotate -i <file>`, then `SOPS_AGE_KEY_FILE=<old key file> sops updatekeys --yes <file>`, which re-encrypts the file for the keys the policy names, this machine's new one among them. Commit the file with git, then run `ezc user list` to check that every file agrees with `recipients.yaml`.
- **Neither.** The file cannot be opened by anyone you can ask: restore it from a commit this machine's key opens, or set its secrets again.
