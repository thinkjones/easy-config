---
title: Repos
nav_order: 5
---

# Repos

```sh
ezc repo add                    # inside a repo: register it in the store and record where it is checked out
ezc repo list                   # the registered repos, and where each is checked out here
ezc repo check --all            # check every registered repo's environment and its secrets' recipients
ezc repo uses API_URL           # which repos declare a name
ezc repo rewrap                 # encrypt this repo's secrets for the store's recipients
ezc repo remove owner/app       # take a repo out of the registry
```

The store keeps a list of the repos that use it, `repos.yaml` in its `.ezc/`. With it `ezc` can check every repo at once, refuse to remove a store value a repo still requires, and keep a repo's own secrets encrypted for the same people as the store's.

## The registry

A repo's ID is the `owner/repo` of its `origin` when that is on github.com, else the name given with `--name`, else its folder's name. A plain name is letters, digits, `.`, `_` and `-`, starting with a letter or a digit, up to 100 characters.

`ezc repo add`, run inside a repo (the folder holding the nearest `.ezc/`; run `ezc repo init` first), adds its ID to `repos.yaml` with the date and your name, and records its folder in `config.toml`:

```
✓ added owner/app to repos.yaml
✓ checkout /Users/sam/code/app
```

Given an ID, `ezc repo add owner/other` registers that repo and records no checkout, unless it is the repo you are in. Running it again for a registered repo prints `✓ owner/app is registered` and changes nothing. The store's own folder is never a repo of its registry.

`repos.yaml` is a file of the store, so a change to it is a commit, `repo add <id>` or `repo remove <id>`, made under the same rules as `ezc set -g` (see [Writing to the store](store.md#writing-to-the-store)): it is refused while the store is behind its remote or `repos.yaml` has uncommitted changes, and it is not pushed. Run `ezc store push` to share it.

```yaml
repos:
  owner/app: {added: 2026-10-13, addedBy: sam}
  scratch-tool: {added: 2026-10-13, addedBy: sam}
```

Whoever can push to the store can write this file. `ezc` refuses the whole file when an ID breaks the rule above, an entry has a field it does not know or a date is not `YYYY-MM-DD`, and prints the dates and names through the same filter as `ezc user list`.

`ezc repo remove <id>` takes the entry out and forgets this machine's checkout of it. It is refused while a bot's entry in `recipients.yaml` names the repo: run `ezc bot remove <bot>` first.

## Checkouts

Where a repo is checked out is different on every machine, so it is kept in `config.toml`, never in the store:

```toml
[checkouts]
"owner/app" = "/Users/sam/code/app"
```

A repo's checkout here is that folder while it holds `.ezc/`, else the repo you are in when its ID is the one asked about. A folder that was moved or deleted counts as no checkout: every command lists that repo as having none and goes on with the others. `ezc repo add` in the new place records it again; an entry whose folder still holds `.ezc/` is kept, with a line saying so (`! owner/app's checkout stays <folder>, which config.toml names.`), so edit `[checkouts]` by hand to move it.

`ezc repo list` prints each registered repo, when and by whom it was added, its checkout here (or `no checkout here`) and the bots whose entry names it.

## Checking every repo

`ezc repo check` runs what `ezc env` checks, in a repo's checkout, and also checks that each encrypted file there is encrypted for exactly the people the store's `recipients.yaml` names. With no argument it checks the repo you are in, registered or not, and `.` names it too, so that an environment can follow (`ezc repo check . prod`); with an ID, that repo; with `--all`, every registered repo, and the only argument is then the environment:

```
owner/app (/Users/sam/code/app)
  environment local ✓ 12 vars from 3 files
owner/api (/Users/sam/code/api)
  environment local: validation failed
  …
owner/web: no checkout on this machine. Run ezc repo add inside it.
2 checked, 1 failed, 1 with no checkout here
```

It exits 1 when a checked repo fails; a repo with no checkout here is listed and is not a failure. It prints names and paths, never a value.

`ezc repo uses NAME` lists the registered repos whose manifest declares a name, looked up in each checkout here: `required by: …` (a name in `optional` with `requiredWhen` counts as required), `optional in: …`, and the repos that could not be looked at, because they have no checkout here or their manifest cannot be read.

## Removing a store value a repo requires

`ezc unset -g NAME` reads the manifest of every registered repo with a checkout here before it changes anything. A name a repo requires is refused, with exit 1:

```
API_URL is required by owner/app. Pass --force to unset it anyway, or take it out of their manifests first.
```

With `--force` the value is removed and the same repos are named on a `!` line. A name a repo lists as optional, and repos that could not be checked, are `!` lines and do not stop it. A `repos.yaml` that cannot be read checks no repo: the unset is refused, and with `--force` that is a `!` line. A store with no `repos.yaml` has no such check, and `ezc unset` without `-g` never runs it.

## A repo's own secrets

A repo can keep secrets of its own in its `.ezc/`, encrypted under its own encryption policy. They should be readable by the same people as the store's, so `ezc` keeps them that way.

`ezc repo rewrap` writes a checkout's policy from the store's `recipients.yaml` and re-wraps each of its encrypted files for exactly the keys it names; a key that leaves a file also gets the file a new data key, as in the store. It takes the same arguments as `ezc repo check`, without the environment. The result is left in the checkout for you to commit there: `ezc` runs no git in a repo.

```
✓ owner/app: re-wrapped common.enc.yaml in /Users/sam/code/app/.ezc. Commit it there.
! owner/app: common.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN.
```

A secret of a repo that a key leaving its file could read is listed like that, with `--env` for an environment's file. It is not recorded in the store's `rotation-due.yaml`, whose entries name the store's files: give it a new value in the repo, at the service that issued it first. A checkout with neither an encrypted file nor a policy gets its first policy when you name it or run the command in it, after the same question for everyone `recipients.yaml` names; with `--all`, and in the re-wrap that follows a change to who can read the store, it prints `- owner/app: no secrets to re-wrap` and is left alone, so no repo gets a policy it did not ask for. One whose files are already for the store's recipients is not written to.

It is refused before any repo is written when the store has no `recipients.yaml`, when an encrypted file of the store is not encrypted for the people it names, or when the store's policy does not name exactly them, each with `Run ezc store rewrap first.`: a name somebody pushed into `recipients.yaml` is let in by the store's own re-wrap, which asks, before any repo follows it (see [Who can change the registry](access.md#who-can-change-the-registry)). It also asks for itself: each name that would gain access to a repo's secrets is printed, and the command asks `Let <names> read <id>'s secrets? [y/N]` on a terminal; anything but `y` prints `Not re-wrapped.` for that repo and the command exits 1 at the end. Without a terminal it needs `--yes`. A repo it cannot re-wrap (`✗ owner/app could not be re-wrapped (<why>). Nothing was changed there.`) is left as it was and the command exits 1.

`ezc doctor` in a repo has a `recipients` item: broken, with `Run ezc repo rewrap.`, when the repo's encrypted files are not encrypted for the store's recipients. It is left out when the store has no `recipients.yaml` or the repo has no encrypted file.

## After a change to who can read the store

Every change to who can read the store, `ezc user add`, `approve`, `remove` and `rotate`, `ezc bot add`, `remove` and `rotate` and `ezc store rewrap`, re-wraps the registered repos checked out on this machine once the store's commit is made, after the store's own lines:

```
✓ remove sam
✓ pushed to https://github.com/owner/store
✓ owner/app: re-wrapped common.enc.yaml in /Users/gene/code/app/.ezc. Commit it there.
! owner/app: common.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN.
! owner/web has no checkout on this machine, so its secrets are still encrypted for the old recipients. Run ezc repo rewrap owner/web where it has one.
```

A repo that fails is left as it was, with a `!` line naming `ezc repo rewrap <id>`, and does not change the command's exit status: the store's change is made. So is a repo whose re-wrap would let in a name the change was not about and that you were not asked about; `ezc repo rewrap` asks. `ezc user rotate` re-wraps the repos with the old key, before it moves to the new one. When it leaves a repo checked out here as it was, it says to keep the old key and gives the command that re-wraps with it, because that repo is still encrypted for the old key and `ezc repo rewrap` reads with the new one. A routine rotation lists no repo secret, because the old key was never exposed; with `--compromised` each one the old key could read is listed. When the push of the store's commit fails, the commit stays here and the repos are re-wrapped all the same.

Commit what was re-wrapped in each repo. On another machine, run `ezc repo rewrap --all` for the repos that are checked out only there.
