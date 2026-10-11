# ezc

`ezc` ("easy config") is a Go CLI that gives a person or a small team one place for secrets and variables. A global store holds values common to every environment and values per environment; a repo keeps the same kind of values in its own `.ezc/`, layered over the store. A manifest says which values a repo requires, and `ezc` validates the composed environment against it, then exports it or runs a command with it.

## Install

```sh
go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest
```

## Set up

```sh
ezc setup --new-local                 # a new store on this machine
ezc setup --new-github owner/repo     # a new store, and a private GitHub repo for it (needs gh)
ezc setup --store owner/repo          # an existing store: owner/repo (needs gh), a URL or a path
```

`ezc setup` takes a machine from nothing to a working store. It checks that `git` and `sops` are installed (and offers to install `sops` with Homebrew when `brew` is there), creates your key, creates or clones the store, and writes `config.toml` in ezc's home (`$EZC_HOME` when set, else `~/Library/ezc` on macOS and `~/.local/share/ezc` on Linux). The store is a git repo at `store` in that folder. On a terminal, setup asks for what no flag says: your name, where the store comes from, and whether to install with brew; `--name` and `--yes` answer in advance. Without a terminal it needs one of the three flags above.

Each line it prints is something that was already there (`✓`) or something it made (`+`). A new key is written to `~/.config/sops/age/keys.txt` (or the `key_file` of `config.toml`), readable only by you. Back that file up when setup says so: without it your secrets cannot be read, and it cannot be recovered. Setup never prints the private key.

A new store starts with an empty manifest, `recipients.yaml` naming you and your public key, the encryption policy written from it, and one commit. Your name is `--name`, else the `user` of `config.toml`, else your login name in lower case; it is lower case letters, digits and `-`.

After `--store`, when your key is not one the store's `recipients.yaml` names, setup says so and prints your public key: give it to someone who has access. The machine is set up either way.

Run again, setup changes only what is missing. A key file, a store or a value in `config.toml` that is already there is never overwritten, and a store with no remote is published by `--new-github`. Asking for a different store than the one in place is refused: move the store away first. Setup manages the store in ezc's home, so it refuses to run while `EZC_STORE` is set.

## Status

Parts 1 and 2 of 7 are built: the environment commands, and secrets: encrypted values files read and written through `sops`, `ezc set`, `unset` and `edit`, `ezc store audit` and `ezc doctor`. From part 3, `ezc setup` and store sync (`ezc store status`, `pull` and `push`) are built. Still to come: users and bots, the registry and the TUI.

## Quick start

```sh
ezc setup --new-local              # once per machine: your key and the store
ezc repo init                      # creates .ezc/ in this folder
$EDITOR .ezc/manifest.yaml         # declare what the repo needs under `required`
$EDITOR .ezc/env.local.yaml        # give it values: NAME: value
ezc env                            # check the environment against the manifest
ezc run -- <command>               # run a command with it
```

## Commands

| Command | What it does |
| --- | --- |
| `ezc version` | Print ezc's version. |
| `ezc help [command]` | Help for ezc, or for one command. |
| `ezc setup [--new-local \| --new-github owner/repo \| --store REPO] [--name NAME] [--yes]` | Set the machine up: check the tools, create your key, create or clone the store, write the config. Changes only what is missing; see [Set up](#set-up). |
| `ezc env [ENV] [-m FILE] [--verbose]` | Check an environment against the manifest. Reports on stderr, prints no values, exits 1 on problems. With `-m FILE` the values come from that manifest's folder, not this repo. |
| `ezc env export [ENV] [--format shell\|json]` | Print the environment's variables on stdout. `shell` is for `eval`; `json` is one object of names to values, for any other tool. Outside a repo it exports the store alone. |
| `ezc run [ENV] -- <command> [args…]` | Run a command with the environment added to its own. On Unix `ezc` replaces itself with the command, so signals sent to it reach the command and the exit status is the command's. Exits 127 when the command is not found, 126 when it cannot be run. |
| `ezc list [ENV] [-g]` | List the names an environment holds, with each one's type (from the repo's manifest, else the store's) and the file that supplies it, and the manifest's names that have no value. `EZC_ENV` and `APP_ENV` are listed last with the source `shell`: they name the environment and come from the command line or the shell, not from a file. Never prints a value. `-g` lists the store alone. |
| `ezc get NAME [ENV] [--reveal]` | Print one value on stdout. A `secret` needs `--reveal`; a name is treated as a secret when the repo's or the store's manifest declares it one, and so is any value read from an encrypted file or built by referencing a secret (`URL: https://u:$TOKEN@host`). |
| `ezc set NAME [VALUE] [--env E] [-g] [--expand] [--type T] [--desc D] [--optional] [--values a,b] [--min N] [--max N]` | Set a value in `common.yaml` (or `env.E.yaml`), in the repo or with `-g` in the store. A name the manifest does not declare is declared in the same step, as required unless `--optional`: give `--type` and `--desc`. The declaration is written first, then the value; if the value cannot be written the manifest is put back as it was. The value is checked against the declared type and bounds, and stored literally: what you give is what loads, so a `$` is written to the file as `$$` (a literal value cannot start with `~` or `~/`, which is always expanded). With `--expand` the value is written as given, so `$NAME`, `${NAME}`, `$$` and a leading `~` keep their meaning at load; the type check is then skipped for a value holding a `$`, the environment is resolved once, and a reference to a name that is not defined yet puts the file back as it was (when the environment cannot be resolved for another reason, such as a missing key, the value is kept and the reference is not checked). `--expand` does not apply to a secret, which is always stored exactly as given. A secret goes to the encrypted file and is typed at a hidden prompt or piped on stdin, never given as an argument; leave VALUE out of any variable to do the same. A name already in the other file of the pair (plaintext against encrypted) is refused. Prints names and files, never the value. |
| `ezc unset NAME [--env E] [-g] [--undeclare]` | Remove a name's value from the plaintext and the encrypted file of the base (`common` or `env.E`), whichever hold it. The declaration stays; `--undeclare` removes it too, and is refused while any other values file in the scope still holds the name. |
| `ezc edit [--env E] [--secrets] [-g]` | Open `common.yaml` (or `env.E.yaml`), or with `--secrets` the encrypted file of the same name, in `$VISUAL` or `$EDITOR`, in the repo or with `-g` in the store. The editor must wait until you finish editing, so a graphical one needs a flag, for example `export EDITOR="code --wait"`; ezc refuses one that returns at once. The command is split on spaces, including in a path (so an editor under a folder with a space in its name needs a wrapper script on a path without one), and quoting inside `EDITOR` is not supported. A plaintext file that is not valid afterwards is reported, and a file this command created and you left empty is removed. With `--secrets` the file must already exist (create it with `ezc set`), and the result is decrypted again to check it; closing the editor without a change is fine. Needs a terminal. |
| `ezc store status` | Fetch from the store's remote, then print where the store is, its remote, how it stands against the remote (in sync, ahead, behind, diverged, uncommitted changes), when the remote was last reached and the pending access requests. Exits 0 in every state. |
| `ezc store pull` | Bring the remote's commits into the store, replaying the commits made here on top of them. An encrypted file changed on both sides keeps the remote's version: the names to set again are listed and the command exits 1. Prints names and files, never a value. |
| `ezc store push` | Send the commits made here to the store's remote. Refused while the store is behind the remote or has diverged from it, holds uncommitted changes, or has no remote. |
| `ezc store audit` | Check the store's values files against its manifest: every value declared, every `required` name set in at least one file, secrets in encrypted files and nothing else there, no value still a placeholder or the wrong shape, and no value in an encrypted file left unencrypted. Prints names, files and the fix for each, never a value; exits 1 when there is anything to fix. |
| `ezc doctor [ENV] [--only machine\|store\|repo]` | Report the health of the machine (ezc, the encryption tool, the key), the store (location, manifest, audit, policy, unrecognised files) and the repo (manifest, `env.sh` shim version, environments, whether the environment resolves and passes the manifest). Each line is a check (`✓`), a warning (`!`) or a problem (`✗`), with what to do about it. Prints names, counts and paths, never a value; exits 1 when anything is broken, and warnings alone exit 0. Works without a key and outside a repo. |
| `ezc repo init` | Create `.ezc/` in the working directory with `manifest.yaml`, `env.local.yaml` and the `env.sh` shim. Existing files are kept; an `env.sh` from an older shim is updated, and one written by a newer `ezc` is kept as it is. Prints how to load the environment. |
| `ezc manifest validate [-m FILE]` | Check that the manifest is well formed; prints the path and counts of required and optional variables. |
| `ezc manifest schema [-o FILE]` | Print the manifest's JSON Schema, or write it to FILE. |
| `ezc codegen python [-m FILE] [-o FILE]` | Validate the manifest, then generate a pydantic config module. An invalid manifest generates nothing, and neither does one with a variable named after a Python keyword such as `class` or one whose name starts with `_`. |

## Writing to the store

The store is a git repo, and every write to it is one commit: `ezc set -g`, `ezc unset -g` and `ezc edit -g`, or the same commands without `-g` run inside the store. The commit holds only the files the command changed and its message names them, never a value: `set NAME (common.enc.yaml)`, `set NAME (manifest.yaml, common.yaml)` for a name declared in the same step, `unset NAME (common.yaml, common.enc.yaml)`, `edit env.prod.yaml`. A command that changes nothing, or that fails and puts the files back, makes no commit; an `ezc unset -g` that removed the value from one file before failing on the other commits what it removed. The author is git's own identity, else the `user` of `config.toml`, else `ezc`.

Anything else you changed in the store by hand is left alone: it is never swept into a commit named after something else, and commands say `store: uncommitted changes in <store>` until you commit or discard it with git. A write to a file that itself has uncommitted changes is refused before anything is written, because the commit would take them with it: `common.yaml has uncommitted changes in <store>. Commit or discard them with git, then run this again.` The files a write may change are the values file it targets (both files of the base for `ezc unset`) and the manifest when a name is declared or undeclared. An `ezc edit -g` whose result is not valid is saved but not committed, and is then such a file: fix and commit it, or discard it, with git.

A write is refused while the store is behind its remote or has diverged from it, before anything is written:

```
the store is 2 commits behind origin. Run `ezc store pull` first: a change made now could not be merged.
```

A store that is ahead or has no remote takes the write; when the remote cannot be reached, what was last known of it decides. When the write worked and the commit did not, the command says so and where to commit it with git.

A store that is not a git repo (an `EZC_STORE` pointing at a plain folder) is written as before, with no commit. A write to any other repo never touches git.

## Syncing the store

A store with a remote is shared through git. Three commands keep it level with the remote, which they call `origin`:

```sh
ezc store status    # how the store stands against the remote
ezc store pull      # bring the remote's commits in
ezc store push      # send yours
```

`ezc store status` fetches, waiting up to 10 seconds, then prints:

```
store     /Users/gene/Library/ezc/store
remote    git@github.com:thinkjones/config.git
state     2 commits behind origin
fetched   just now
requests  none
```

`state` is one of `in sync`, `N commits ahead of origin`, `N commits behind origin`, `diverged from origin (A ahead, B behind)` and `uncommitted changes`. `fetched` is how long ago the remote was last reached, followed by `; origin could not be reached` when it was not reached this time. `requests` is `none` or the names of the pending access requests. A password or token in the remote's URL is not printed. A store with no remote prints `remote    none (local only)` and `fetched   n/a`. A store that is not a git repo prints only where it is and `state     not a git repo, so it is not synced`. Status exits 0 in every state.

`ezc store pull` prints `pulled 3 commits from origin` or `already up to date`. Commits you made here are replayed on top of the remote's. An encrypted file cannot be merged, so when one was changed both here and on the remote the remote's version is kept and the command tells you what to do again, on stderr, and exits 1:

```
! env.prod.enc.yaml was changed here and on the remote. The remote's version was kept; set these again:
  ezc set -g API_KEY --env prod
```

The list holds the names you changed in that file, never a value; a name you removed is listed as an `ezc unset -g` command, under `run these again:`. When the names cannot be worked out, the file is named and its local changes must be set again. Everything else in the pull went through.

A plaintext file changed in the same place on both sides stops the pull, and the store is left exactly as it was:

```
.ezc/manifest.yaml was changed here and on the remote in the same place, so nothing was pulled. Resolve it in /Users/gene/Library/ezc/store with git (git pull --rebase).
```

This is a limit to know about: two people who each declare a new name at the same time both add it at the end of `manifest.yaml`, so the second to pull gets this conflict and must resolve it with git: run `git pull --rebase` in the store, keep both declarations, `git add` the file and `git rebase --continue`. Pull before you declare a name and push soon after to make it rare.

A pull is also refused when the store holds uncommitted changes (commit or discard them with git first), when it has no remote (`ezc setup --new-github owner/repo` gives it one), and when the remote cannot be reached.

`ezc store push` fetches, then prints `pushed 2 commits to origin` or `nothing to push`. It is refused while the store is behind the remote or has diverged from it (``the store is behind origin. Run `ezc store pull` first.``), while it holds uncommitted changes, and when it has no remote.

You do not have to ask. `ezc env`, `ezc run`, `ezc list`, `ezc get` and the commands that write to the store (`ezc set -g`, `ezc unset -g`, `ezc edit -g`) look at the store before they use it, and print one line on stderr when there is something to say:

```
store: 2 commits behind origin — run ezc store pull
store: 1 commit ahead of origin — run ezc store push
store: diverged from origin (1 ahead, 2 behind) — run ezc store pull
store: uncommitted changes in /Users/gene/Library/ezc/store
store: 1 access request pending: sam
```

Pending access requests are added to the end of the line when it already says something else. A store that is in sync prints nothing. To keep these commands fast they contact the remote at most once every 10 minutes and wait at most 3 seconds for it; in between, and when the remote cannot be reached, the line is what was last known, and a fetch that just failed adds `store: origin could not be reached; last checked 2 hours ago`. The command then carries on. `ezc env export` never contacts the remote and never prints the line, so loading a shell's environment is not slowed down or made noisy. `ezc store status`, `pull` and `push` always contact the remote and do not print the line: their own output is the answer.

## How a repo loads its environment

The environment is named by the argument, else `EZC_ENV`, else `APP_ENV`; with none of them, `local` applies when it exists. There are three ways in:

- mise: in `mise.toml`, `[env]` then `_.source = ".ezc/env.sh"`. `ezc repo init` writes that file.
- direnv: `source_env .ezc/env.sh` in `.envrc`.
- neither: `ezc run -- <command>` runs one command with the variables and leaves the shell alone.

The shim never fails the shell, so a broken config does not make `cd` unusable. `ezc env` is the check that fails; make tasks depend on it.

## Files

A repo's `.ezc/` folder and the global store use the same layout. Every values file is optional; `ezc env` needs `manifest.yaml`.

| File | What it holds |
| --- | --- |
| `manifest.yaml` | Every value declared: type, description, required or optional. |
| `common.yaml` | Plaintext values for all environments. |
| `env.<name>.yaml` | Plaintext values for one environment. |
| `common.enc.yaml` | Encrypted values for all environments. |
| `env.<name>.enc.yaml` | Encrypted values for one environment. |
| `.sops.yaml` | The encryption policy: which files are encrypted, and for which keys. Needed before a secret can be stored in the folder. `ezc setup` writes the store's; see [Encryption](#encryption). |
| `recipients.yaml` | In the store only: who can decrypt it, by name and public key. `ezc setup` writes the first entry. |
| `env.sh` | The shim mise or direnv sources. Written by `ezc repo init`; do not edit. |

A values file is one flat YAML mapping of `NAME: value` lines. Values are read as raw text, so no type is inferred: `08540` stays `08540`.

These are rejected with an error naming the file and the key, because each would change or drop a value without saying so:

- a nested block or a list
- an anchor (`&x`) or an alias (`*x`)
- a tag (`!x`; quote a value that starts with `!`, `&` or `*`)
- a key defined twice
- more than one YAML document
- a value that contains a NUL byte

A file that is not valid YAML is reported by path and line only, never with the parser's own text.

A variable name, in the manifest and in a values file, is letters, digits and underscores and does not start with a digit (`^[A-Za-z_][A-Za-z0-9_]*$`). `ezc manifest validate` and the JSON Schema apply the same rule.

## Encryption

Secrets need [`sops`](https://github.com/getsops/sops) installed: `brew install sops`, or its release page; `ezc setup` offers to install it. `ezc doctor` says when `sops` is missing or too old, and every command that needs it names it. Storing a secret in a new file works on macOS and Linux only.

A variable the manifest declares `secret` is stored encrypted, in `common.enc.yaml` or `env.<name>.enc.yaml`, and `ezc set` writes it there. A folder needs its encryption policy, `.ezc/.sops.yaml`, before a secret can be stored in it. `ezc setup` writes the store's from `recipients.yaml`, so `ezc set -g` works as soon as setup has run; do not edit that file. A repo that keeps secrets of its own needs the same file in its `.ezc/`: copy the store's. A policy whose `path_regex` matches no file name stores nothing, and `ezc set` says so.

The key file is the `key_file` of `$EZC_HOME/config.toml` when that file names one, else `~/.config/sops/age/keys.txt`; `ezc setup` creates it when it is not there. It is passed to `sops` as `SOPS_AGE_KEY_FILE`; when `SOPS_AGE_KEY` or `SOPS_AGE_KEY_FILE` is already set in the environment, that wins, so unset them before `ezc setup` unless they name the same key.

A secret is typed at a hidden prompt or piped on stdin, never passed as an argument, and reaches `sops` on its stdin only. A value cannot move across the secret boundary: `ezc set` refuses a name that is already in the other file of the pair, so remove it with `ezc unset`, which clears both files of the pair, first.

A store's commits should pass `ezc store audit`. Without a key it still checks names and the boundary between plaintext and encrypted files, and says which files' values it could not check. A line added by hand to an encrypted file makes the whole file undecryptable: the audit names the unencrypted value and skips that file's value checks (`! damaged`), and the fix is to restore the file from git and set the value again with `ezc set`, treating the old value as leaked.

## When something is wrong

Start with `ezc doctor`. It checks the machine, the store and the repo and says what to do about each thing it finds: a missing key (a warning: an environment with no encrypted file still loads, and one that has any fails as a whole until the key is there), a missing `sops` or `git`, a missing or invalid `config.toml` (`ezc setup` writes it), a store or repo manifest that does not parse, an `env.sh` written by an older `ezc` (run `ezc repo init`) or by a newer one (a warning: upgrade `ezc`; `ezc repo init` leaves that shim alone), encrypted files in a folder with no encryption policy, and files in a `.ezc/` folder that `ezc` never reads, such as `common.yml` or `comon.yaml`, with the name it probably should have. For a store that is a git repo it also reports where the store stands against its remote (`sync`: in sync, local only, ahead or behind with the command that fixes it, uncommitted changes, or diverged, which fails; it fetches by the same 10 minute rule as other commands and says when origin could not be reached), whether your key is one of the store's recipients (`access`; a key that is not fails, and doctor then says to give your public key to someone who has access), and the access requests waiting in the store (`requests`). `ezc doctor prod` checks that environment, and `--only` limits the report to one part. For the detail of a single failure, `ezc env` lists what is wrong with the environment and `ezc store audit` what is wrong with the store.

## Resolution

Later wins:

```
store  common.yaml → common.enc.yaml → env.<name>.yaml → env.<name>.enc.yaml
repo   common.yaml → common.enc.yaml → env.<name>.yaml → env.<name>.enc.yaml
```

`<name>` matches `^[A-Za-z0-9][A-Za-z0-9_-]*$`, and `enc` is reserved (`env.enc.yaml` is not an environment file). It is the command's argument, else `$EZC_ENV`, else `$APP_ENV`, else `local`. With no name given and no `env.local.yaml`, only the `common` files load, and `ezc` says so on stderr. A missing store is not an error: the repo's files compose alone. A store that is there but is not a folder, or cannot be read, is an error, and so is an `EZC_STORE` that names a folder which does not exist.

- Within a file, order is semantic.
- A value may reference `$NAME` or `${NAME}` for a name already resolved, earlier in the same file or in an earlier file. `$$` is a literal `$`. `ezc set` writes a value literally, doubling each `$` for you; `ezc set --expand`, or `ezc edit`, writes a reference.
- Values from an encrypted file are literal: no `~`, `$HOME` or `$NAME` is expanded in them. A later plaintext file can still reference them by name. A command that needs an encrypted file's key stops with an error, and prints nothing, when it has none.
- A leading `~`, `$HOME` or `${HOME}` is expanded at load.
- There are no defaults: a missing value fails the check. The check says what is wrong with a value (`not an integer`, `above the maximum of 9`, `not a URL (must start with http:// or https://)`) and never prints the value.
- Shell export single-quotes every value and never emits `EZC_ENV` or `APP_ENV`.
