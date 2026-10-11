# ezc

`ezc` ("easy config") is a Go CLI that gives a person or a small team one place for secrets and variables. A global store holds values common to every environment and values per environment; a repo keeps the same kind of values in its own `.ezc/`, layered over the store. A manifest says which values a repo requires, and `ezc` validates the composed environment against it, then exports it or runs a command with it.

## Install

```sh
go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest
```

`ezc` needs `git` 2.23 or later, and [`sops`](https://github.com/getsops/sops) for secrets. `ezc setup` checks for both.

## Quick start

```sh
# once per machine: your key, the store and ezc's config
ezc setup --new-local

# put values in the store, for every repo
ezc set -g REGION us-east-1 --type str --desc "where we run"
ezc set -g API_TOKEN --type secret --desc "the API's token"   # asks for the value at a hidden prompt

# use them in a repo
cd my-repo
ezc repo init                      # creates .ezc/ in this folder
ezc set PORT 8080 --type int --desc "the port to listen on"   # a value of the repo's own
ezc env                            # check the environment against the manifest
ezc run -- <command>               # run a command with it

# share the store through a private GitHub repo (needs gh), and keep it level
ezc setup --new-github owner/repo  # publishes the store you have
ezc store push                     # after you change the store
ezc store pull                     # to bring in changes made elsewhere
```

Each step is described below: [Set up](#set-up), [Writing to the store](#writing-to-the-store), [How a repo loads its environment](#how-a-repo-loads-its-environment), [Syncing the store](#syncing-the-store) and [Access](#access). When something does not work, run `ezc doctor`.

## Not there yet

This is `ezc` 0.3. A second person, or a second machine with a key of its own, asks for access with `ezc join`, is let in with `ezc user approve` (or with `ezc user add` once you have their public key), and removed with `ezc user remove` (see [Access](#access)). A bot gets a key of its own with `ezc bot add`, and a CI job reads the store with it after `ezc setup --ci` (see [Bots and CI](#bots-and-ci)). A second machine can also share the first one's key file: copy `~/.config/sops/age/keys.txt` from the first machine to the same path on the second, then run `ezc setup --store owner/repo` there. Setup uses a key file that is already there and never replaces it.

Two things are done by hand for now: a repo that keeps secrets of its own needs the store's encryption policy copied into its `.ezc/` (see [Encryption](#encryption)), and two machines that declare a new name at the same time must resolve the conflict with git (see [Syncing the store](#syncing-the-store)). The registry and the TUI come later.

## Set up

```sh
ezc setup --new-local                 # a new store on this machine
ezc setup --new-github owner/repo     # a new store, and a private GitHub repo for it (needs gh)
ezc setup --store owner/repo          # an existing store: owner/repo (needs gh), a URL or a path
ezc setup --ci --store owner/repo     # in a CI job: see "Bots and CI"
```

`ezc setup` takes a machine from nothing to a working store. It checks that `git` and `sops` are installed (and offers to install `sops` with Homebrew when `brew` is there; otherwise it says how to install it and stops, with nothing made), creates your key, creates or clones the store, and writes `config.toml` in ezc's home (`$EZC_HOME` when set, else `~/Library/ezc` on macOS and `~/.local/share/ezc` on Linux). The store is a git repo at `store` in that folder. On a terminal, setup asks for what no flag says: your name, where the store comes from, and whether to install with brew; `--name` and `--yes` answer in advance. Without a terminal it needs one of the three flags above.

Each line it prints is something that was already there (`✓`) or something it made (`+`). It ends with the command to run next when the store can take a secret, that is when it has an encryption policy. A new key is written to `~/.config/sops/age/keys.txt` (or the `key_file` of `config.toml`, or the file `SOPS_AGE_KEY_FILE` names), readable only by you. Back that file up when setup says so: without it your secrets cannot be read, and it cannot be recovered. Setup never prints the private key.

A new store starts with an empty manifest, `recipients.yaml` naming you and your public key, the encryption policy written from it, and one commit. Your name is `--name`, else the `user` of `config.toml`, else your login name (`$USER`) in lower case; it is lower case letters, digits and `-`, starting with a letter or a digit. When none of these gives one, setup stops and asks for `--name`.

`--store a/b` always means the GitHub repo `a/b`; for a folder of that shape write `./a/b`. After `--store`, when your key is not one the store's `recipients.yaml` names, setup says so and prints your public key: give it to someone who has access, who adds it with `ezc user add` (see [Access](#access)). The machine is set up either way, and `ezc doctor` reports the missing access as a problem until it is given. To use the key of another machine of yours, put its key file in place before you run setup.

Run again, setup changes only what is missing. A key file, a store or a value in `config.toml` that is already there is never overwritten, and a store with no remote is published by `--new-github`. Asking for a different store than the one in place is refused: move the store away first. Setup manages the store in ezc's home, so it refuses to run while `EZC_STORE` is set.

A store made by hand, a folder with a `.ezc/` that is already at the store's path, is adopted: setup adds what it lacks and rewrites nothing that is there, so the manifest, the values files and a policy you wrote keep their bytes. A folder that is not a git repo becomes one, with its `.ezc/` in one commit (`+ made the store a git repo: <store>`, commit `setup: adopt the store`); after that every write is committed and `--new-github` can publish it. A `.ezc/` with no `manifest.yaml` gets an empty one (`+ wrote an empty manifest`). A store with neither `recipients.yaml` nor an encryption policy gets both, naming you (`+ wrote the registry and the encryption policy`), but only when it holds no encrypted values file: one that does was set up by someone, under a policy that is not in its `.ezc/` (at the top of the repo, say), so setup writes neither and says `! This store holds encrypted values but has no recipients.yaml and no encryption policy in .ezc, so ezc does not manage who can read it.` A store with `recipients.yaml` and no policy gets the policy written from it. A store with a policy and no `recipients.yaml` is left as it is, because the policy was written by hand and setup will not replace it: setup says `! This store's encryption policy was written by hand and it has no recipients.yaml, so ezc does not manage who can read it. Keep editing the policy by hand for now.`, and `ezc doctor` keeps its `access` warning. A store cloned with `--store` is treated the same way. When the store is a git repo that lacks something and git cannot say how it stands (it is not on a branch, as while a rebase is open), setup writes nothing and stops with what the write commands say: `the store in <store> is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again.` A store that `EZC_STORE` names is not adopted: setup does not run for it, and it keeps working as a plain folder.

## Commands

| Command | What it does |
| --- | --- |
| `ezc version` | Print ezc's version. |
| `ezc help [command]` | Help for ezc, or for one command. |
| `ezc setup [--new-local \| --new-github owner/repo \| --store REPO] [--name NAME] [--yes]` | Set the machine up: check the tools, create your key, create or clone the store, write the config. Changes only what is missing; see [Set up](#set-up). |
| `ezc setup --ci --store owner/repo` | Set a CI job up to read a store on github.com: clone its latest commit with the token in `EZC_STORE_TOKEN` (or `GH_TOKEN`), check that the key in `EZC_AGE_KEY` reads it and write the config. No key file is made and nothing is asked. See [Bots and CI](#bots-and-ci). |
| `ezc join <owner/repo \| URL \| path> [--name NAME] [--kind user\|machine] [--owner USER]` | Ask for access to an existing store from this machine: clone it, create your key when there is none, write the config, and push a request holding your public key on the branch `ezc/join-NAME`, with a pull request when `gh` is installed and the store is on GitHub. Changes nothing on the store's main branch. See [Joining a store](#joining-a-store). |
| `ezc join --print [--name NAME] [--kind user\|machine] [--owner USER]` | Print the `ezc user add` line for this machine's key, creating the key when there is none. Takes no repo, needs no store and pushes nothing. |
| `ezc env [ENV] [-m FILE] [--verbose]` | Check an environment against the manifest. Reports on stderr, prints no values, exits 1 on problems. With `-m FILE` the values come from that manifest's folder, not this repo. |
| `ezc env export [ENV] [--format shell\|json]` | Print the environment's variables on stdout. `shell` is for `eval`; `json` is one object of names to values, for any other tool. Outside a repo it exports the store alone. |
| `ezc run [ENV] -- <command> [args…]` | Run a command with the environment added to its own. On Unix `ezc` replaces itself with the command, so signals sent to it reach the command and the exit status is the command's. Exits 127 when the command is not found, 126 when it cannot be run. |
| `ezc list [ENV] [-g]` | List the names an environment holds, with each one's type (from the repo's manifest, else the store's) and the file that supplies it, and the manifest's names that have no value. `EZC_ENV` and `APP_ENV` are listed last with the source `shell`: they name the environment and come from the command line or the shell, not from a file. Never prints a value. `-g` lists the store alone. |
| `ezc get NAME [ENV] [--reveal]` | Print one value on stdout. A `secret` needs `--reveal`; a name is treated as a secret when the repo's or the store's manifest declares it one, and so is any value read from an encrypted file or built by referencing a secret (`URL: https://u:$TOKEN@host`). |
| `ezc set NAME [VALUE] [--env E] [-g] [--expand] [--type T] [--desc D] [--optional] [--values a,b] [--min N] [--max N]` | Set a value in `common.yaml` (or `env.E.yaml`), in the repo or with `-g` in the store. A name the manifest does not declare is declared in the same step, as required unless `--optional`: give `--type` and `--desc`. The declaration is written first, then the value; if the value cannot be written the manifest is put back as it was. The value is checked against the declared type and bounds, and stored literally: what you give is what loads, so a `$` is written to the file as `$$` (a literal value cannot start with `~` or `~/`, which is always expanded). With `--expand` the value is written as given, so `$NAME`, `${NAME}`, `$$` and a leading `~` keep their meaning at load; the type check is then skipped for a value holding a `$`, the environment is resolved once, and a reference to a name that is not defined yet puts the file back as it was (when the environment cannot be resolved for another reason, such as a missing key, the value is kept and the reference is not checked). `--expand` does not apply to a secret, which is always stored exactly as given. A secret goes to the encrypted file and is typed at a hidden prompt or piped on stdin, never given as an argument; leave VALUE out of any variable to do the same. A name already in the other file of the pair (plaintext against encrypted) is refused. Prints names and files, never the value. |
| `ezc unset NAME [--env E] [-g] [--undeclare]` | Remove a name's value from the plaintext and the encrypted file of the base (`common` or `env.E`), whichever hold it. The declaration stays; `--undeclare` removes it too, and is refused while any other values file in the scope still holds the name. |
| `ezc edit [--env E] [--secrets] [-g]` | Open `common.yaml` (or `env.E.yaml`), or with `--secrets` the encrypted file of the same name, in `$VISUAL` or `$EDITOR`, in the repo or with `-g` in the store. The editor must wait until you finish editing, so a graphical one needs a flag, for example `export EDITOR="code --wait"`; ezc refuses one that returns at once. The command is split on spaces, including in a path (so an editor under a folder with a space in its name needs a wrapper script on a path without one), and quoting inside `EDITOR` is not supported. A plaintext file that is not valid afterwards is reported, and a file this command created and you left empty is removed. With `--secrets` the file must already exist (create it with `ezc set`), and the result is decrypted again to check it; closing the editor without a change is fine. Needs a terminal. |
| `ezc store status` | Fetch from the store's remote, then print where the store is, its remote, how it stands against the remote (in sync, ahead, behind, diverged, uncommitted changes), when the remote was last reached and the pending access requests. Exits 0 in every state it can read. |
| `ezc store pull` | Bring the remote's commits into the store, replaying the commits made here on top of them. An encrypted file changed on both sides keeps the remote's version: the names to set again are listed and the command exits 1. Prints names and files, never a value. |
| `ezc store push` | Send the commits made here to the store's remote. Refused while the store is behind the remote or has diverged from it, holds uncommitted changes, or has no remote. |
| `ezc store rewrap` | Write the store's encryption policy from `recipients.yaml` and re-wrap every encrypted file for exactly the keys it names, in one commit, then push. The fix when `ezc user list` names a file. See [Access](#access). |
| `ezc user list` | List who can read the store, from `recipients.yaml`: name, kind, owner, when added and by whom, with this machine's row marked. Then name every encrypted file whose recipients are not the registry's, and exit 1 when there is one. Needs no key. |
| `ezc user requests` | Fetch, then list the requests for access that `ezc join` filed: name, kind, owner, the whole public key, the host, the date and the Git author. One that breaks the rules is listed as not valid. See [Approving a request](#approving-a-request). |
| `ezc user approve <name> [--yes]` | Fetch, print the request filed under `name` with its whole public key, ask `Approve <name>? [y/N]` (`--yes` skips the question, and is needed without a terminal), then add that key to `recipients.yaml` and re-wrap every encrypted file, in one commit named `approve <name>`, push, and delete the request's branch. The branch is never merged. |
| `ezc user deny <name>` | Fetch, then remove a request file that is in the store itself in a commit named `deny <name>`, and delete the request's branch from the remote. Nobody's access changes. |
| `ezc user remove <name>` | Remove a person or one machine from `recipients.yaml` and re-wrap every encrypted file without their key, in one commit named `remove <name>`, then push; then list the secrets they could read, which need new values. See [Access](#access). |
| `ezc user rotate [--compromised]` | Give this machine a new key in a new file beside the old one (`keys-20261011.txt`, then `-2`, `-3`… when taken), replace its key in `recipients.yaml` and re-wrap every encrypted file and point `key_file` in `config.toml` at the new file, in one commit named `rotate <name>`, then push. The old file is never touched; delete it when you are sure. Takes no name, and refuses when the environment names the key file. `--compromised` also records every secret the old key could read in `rotation-due.yaml` and lists them. |
| `ezc user add <name> <key> [--kind user\|machine] [--owner USER]` | Add a public key to `recipients.yaml`, write the policy from it and re-wrap every encrypted file of the store, in one commit, then push. `--kind machine --owner USER` adds a second key of a user who is already a recipient. See [Access](#access). |
| `ezc bot add <name> --repo owner/repo… [--print-private]` | Create a key for a bot, add its public half to `recipients.yaml` and re-wrap every encrypted file, in one commit named `add bot <name>`, then push. Before the commit the private half is saved as the `EZC_AGE_KEY` Actions secret of each `--repo`, through `gh`; `--print-private` prints it on stdout instead, or as well. See [Bots and CI](#bots-and-ci). |
| `ezc bot remove <name>` | Remove a bot from `recipients.yaml` and re-wrap every encrypted file without its key, in one commit named `remove bot <name>`, then push; delete `EZC_AGE_KEY` from its repos; then list the secrets it could read, which need new values. |
| `ezc bot rotate <name> [--print-private] [--compromised]` | Replace a bot's key in one commit named `rotate bot <name>` and deliver the new one to its repos, as `ezc bot add` does. `--compromised` also records every secret the old key could read as due for a new value. |
| `ezc store audit` | Check the store's values files against its manifest: every value declared, every `required` name set in at least one file, secrets in encrypted files and nothing else there, no value still a placeholder or the wrong shape, and no value in an encrypted file left unencrypted. Prints names, files and the fix for each, never a value; exits 1 when there is anything to fix. |
| `ezc doctor [ENV] [--only machine\|store\|repo]` | Report the health of the machine (ezc, the encryption tool, the key), the store (location, manifest, audit, policy, unrecognised files) and the repo (manifest, `env.sh` shim version, environments, whether the environment resolves and passes the manifest). Each line is a check (`✓`), a warning (`!`) or a problem (`✗`), with what to do about it. Prints names, counts and paths, never a value; exits 1 when anything is broken, and warnings alone exit 0. Works without a key and outside a repo. |
| `ezc repo init` | Create `.ezc/` in the working directory with `manifest.yaml`, `env.local.yaml` and the `env.sh` shim. Existing files are kept; an `env.sh` from an older shim is updated, and one written by a newer `ezc` is kept as it is. Prints how to load the environment. |
| `ezc manifest validate [-m FILE]` | Check that the manifest is well formed; prints the path and counts of required and optional variables. |
| `ezc manifest schema [-o FILE]` | Print the manifest's JSON Schema, or write it to FILE. |
| `ezc codegen python [-m FILE] [-o FILE]` | Validate the manifest, then generate a pydantic config module. An invalid manifest generates nothing, and neither does one with a variable named after a Python keyword such as `class` or one whose name starts with `_`. |

## Writing to the store

The store is a git repo, and every write to it is one commit: `ezc set -g`, `ezc unset -g` and `ezc edit -g`, or the same commands without `-g` run inside the store. The commit holds only the files the command changed and its message names them, never a value: `set NAME (common.enc.yaml)`, `set NAME (manifest.yaml, common.yaml)` for a name declared in the same step, `unset NAME (common.yaml, common.enc.yaml)`, `edit env.prod.yaml`. A command that changes nothing, or that fails and puts the files back, makes no commit; an `ezc unset -g` that removed the value from one file before failing on the other commits what it removed. The author is git's own identity, else the `user` of `config.toml`, else `ezc`.

Anything else you changed in the store by hand is left alone: it is never swept into a commit named after something else, and commands say `store: uncommitted changes in <store>` until you commit or discard it with git. An `ezc set -g` or `ezc unset -g` that would write a file that itself has uncommitted changes is refused before anything is written, because the commit would take them with it: `common.yaml has uncommitted changes in <store>. Commit or discard them with git, then run this again.` The files they may change are the values file targeted (both files of the base for `ezc unset`) and the manifest when a name is declared or undeclared. `ezc edit -g` is not refused: it opens such a file, and its commit, `edit <file>`, holds what was changed there by hand as well. An `ezc edit -g` whose result is not valid is saved but not committed; the edit that fixes it is.

A write is refused while the store is behind its remote or has diverged from it, before anything is written:

```
the store is 2 commits behind origin. Run `ezc store pull` first: a change made now could not be merged.
```

A store that is ahead or has no remote takes the write; when the remote cannot be reached, what was last known of it decides. When the write worked and the commit did not, the command says so and where to commit it with git.

A write is also refused when ezc cannot tell how the store stands, because then it cannot tell whether the change could be merged or even committed. A store that is not on a branch is the usual case, and a rebase left open is the usual cause: `the store in <store> is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again.` A store that is a git repo on a machine without git is refused the same way: `git is not installed, so a change to the store could not be committed. Install git, then run this again.` Commands that only read (`ezc env`, `ezc run`, `ezc list`, `ezc get`) still work in both cases; they say `store: cannot tell how the store stands in <store> (the store is not on a branch)` on stderr and carry on.

A store that is not a git repo (an `EZC_STORE` pointing at a plain folder) is written with no commit, and is neither fetched nor reported on. A write to any other repo never touches git.

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

`state` is one of `in sync`, `N commits ahead of origin`, `N commits behind origin`, `diverged from origin (A ahead, B behind)` and `uncommitted changes`; a store with uncommitted changes that is also ahead, behind or diverged says both, as in `uncommitted changes; 1 commit behind origin`. `fetched` is how long ago the remote was last reached, followed by `; origin could not be reached` when it was not reached this time. `requests` is `none` or the names of the pending access requests (the files of the store's `.ezc/requests/`; one whose name could not be a person's name in the store is not shown). A remote that is a URL is shown as its scheme, host and path only, here and in every message: a user name, password, query or fragment in it is never printed, and setup does not save them in `config.toml` either. A store with no remote prints `remote    none (local only)` and `fetched   n/a`. A store that is not a git repo prints only where it is and `state     not a git repo, so it is not synced`. Status exits 0 in every state it can read, and 1 when git cannot say how the store stands: for a store that is not on a branch it says that a rebase may be in progress and to finish or abort it with git, and `ezc store pull` and `ezc store push` say the same.

`ezc store pull` prints `pulled 3 commits from origin` or `already up to date`. Commits you made here are replayed on top of the remote's. An encrypted file cannot be merged, so when one was changed both here and on the remote the remote's version is kept and the command tells you what to do again, on stderr, and exits 1:

```
! env.prod.enc.yaml was changed here and on the remote. The remote's version was kept; set these again:
  ezc set -g API_KEY --env prod
```

The list holds the names you changed in that file, never a value; a name you removed is listed as an `ezc unset -g` command, under `run these again:`. When the names cannot be worked out, the file is named and its local changes must be set again. Everything else in the pull went through.

A plaintext file changed in the same place on both sides stops the pull, and the store is left exactly as it was:

```
.ezc/common.yaml, .ezc/manifest.yaml were changed here and on the remote in the same place, so nothing was pulled. Resolve them in /Users/gene/Library/ezc/store with git (git pull --rebase).
```

This is a limit to know about: two machines that each declare a new name at the same time both add it at the end of `manifest.yaml` (and its value at the end of the same values file), so the second to pull gets this conflict and must resolve it with git: run `git pull --rebase` in the store, edit each file named so that it holds both sides' entries in full (when two declarations differ only in their names, git marks only the name lines, and the lines under them must be written out for each), `git add` the files and `git rebase --continue`. Until you run `git pull --rebase` the store has diverged: `ezc store status` says `diverged from origin`. While the rebase is open the store is not on a branch: `ezc store status`, `pull` and `push` exit 1 and say `the store in <store> is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again.`, and `ezc doctor` reports the same as a problem. Writes to the store are refused throughout, and reading still works. Pull before you declare a name and push soon after to make it rare. Setting a value for a name that is already declared does not touch the manifest.

A pull is also refused when the store holds uncommitted changes (commit or discard them with git first), when it has no remote (`ezc setup --new-github owner/repo` gives it one), and when the remote cannot be reached.

One more limit: a file that git ignores does not count as an uncommitted change, so a pull replaces it without a word when the remote tracks a file at that path. It takes a `.gitignore` written by hand in the store and a file added past it on another machine; keep anything you want to keep out of the store's folder.

`ezc store push` fetches, then prints `pushed 2 commits to origin` or `nothing to push`. It is refused while the store is behind the remote or has diverged from it (``the store is behind origin. Run `ezc store pull` first.``), while it holds uncommitted changes, and when it has no remote.

You do not have to ask. `ezc env`, `ezc run`, `ezc list`, `ezc get`, `ezc store audit` and the commands that write to the store (`ezc set -g`, `ezc unset -g`, `ezc edit -g`) look at the store before they use it, and print one line on stderr when there is something to say:

```
store: 2 commits behind origin — run ezc store pull
store: 1 commit ahead of origin — run ezc store push
store: diverged from origin (1 ahead, 2 behind) — run ezc store pull
store: uncommitted changes in /Users/gene/Library/ezc/store
store: 1 access request pending: sam
```

A store with uncommitted changes that is also ahead, behind or diverged says both: `store: uncommitted changes in <store>; 1 commit behind origin`. Pending access requests are the ones still on their `ezc/join-<name>` branch of the remote, as of the last fetch, and the ones in the store's `.ezc/requests/`; they are added to the end of the line when it already says something else, and `ezc user requests` shows them. The line names the first five and counts the rest (`sam, kim, … and 3 more`). A store that is in sync prints nothing. When git cannot say how the store stands, the line is `store: cannot tell how the store stands in <store> (<why>)` and nothing else. To keep these commands fast they contact the remote at most once every 10 minutes and wait at most 3 seconds for it; in between, and when the remote cannot be reached, the line is what was last known, and a fetch that just failed adds `store: origin could not be reached; last checked 2 hours ago`. The command then carries on. `ezc env export` never contacts the remote and never prints the line, so loading a shell's environment is not slowed down or made noisy. `ezc store status`, `pull` and `push` always contact the remote and do not print the line: their own output is the answer.

## Access

Who can read the store's secrets is written in one place, `recipients.yaml` in the store's `.ezc/`. Each entry is a name and a public key, with a kind (`user`, `machine` for a second key of a user, or `bot`), the machine's owner, the day it was added and who added it. The encryption policy is written from that file, and every encrypted file of the store is encrypted for exactly the keys it names. `ezc setup` writes the first entry: you.

```sh
ezc user list                                             # who can read the store
ezc user add sam age1…                                    # let sam read it
ezc user add sam-laptop age1… --kind machine --owner sam  # a second key of sam's
ezc user requests                                         # who is asking to read it
ezc user approve sam                                      # let in whoever filed the request named sam
ezc user deny sam                                         # refuse that request
ezc user remove sam                                       # sam can no longer read it
ezc user rotate                                           # this machine gets a new key; the old one stops opening the store
ezc bot add ci --repo owner/app                           # a key for CI, saved in owner/app's Actions secrets
ezc store status --due                                    # the secrets that need new values
ezc store rewrap                                          # make the files match recipients.yaml again
```

`ezc user list` prints the registry as a table and marks the row whose key is this machine's. It then reads, from each encrypted file, the keys the file is really encrypted for, which needs no key, and compares them with the registry. A file that differs is named, with the recipients it lacks and the keys it has that the registry does not, and the command exits 1:

```
✗ env.prod.enc.yaml: not encrypted for sam; encrypted for age1… who are not in the registry
Run ezc store rewrap.
```

`ezc user add <name> <key>` takes a public key, which is safe to pass around: `ezc setup --store owner/repo` prints it on a machine whose key cannot read the store yet. A private key is never given to a command. The name is lower case letters, digits and `-`. The command adds the entry, writes the policy, re-wraps every encrypted file of the store for the new list, checks that each file now lists exactly the registry's keys, commits those files as `add <name>` and pushes. The new person then runs `ezc setup --store owner/repo` (or `ezc store pull`) and can read the secrets. `ezc join --print` on the new machine writes this command out, key included; see [Joining a store](#joining-a-store).

### Joining a store

```sh
ezc join owner/repo                                              # ask to read the store (owner/repo needs gh; a URL or a path works too)
ezc join owner/repo --name sam-laptop --kind machine --owner sam # ask for a second key of sam's
ezc join --print                                                 # only print the line that adds this machine's key
```

`ezc join` is how a new person, or a new machine with a key of its own, asks for access. It takes the machine from nothing, as `ezc setup --store` does, and then files a request:

- It clones the store into ezc's home. A store that is already there must be a clone of the same repo, level with its remote: neither behind it nor holding commits it lacks.
- It creates your key when the machine has none, with the same notice to back the file up, and never replaces one that is there.
- It writes `config.toml`, filling in only the fields that are empty. Your name is saved as its `user` last, once the request is pushed or found waiting, so a name that was refused is never saved; a key that can already read the store gets the name `recipients.yaml` gives it (a machine: its owner). A `user` that is already there is never changed: when it differs, a `!` line says so.
- It writes `.ezc/requests/<name>.yaml` (your name, your public key, `user` or `machine` and the owner, this machine's host name and the date), commits it on a new branch `ezc/join-<name>`, pushes that branch and puts the store back on its own branch with the file gone. The store's main branch is not changed, here or on the remote.
- When `gh` is installed and the store is on GitHub, it opens a pull request for the branch and prints its URL. The pull request is only a way to see the request: it is not meant to be merged. When it cannot be opened the command says so on a `!` line and still succeeds, because the request is on its branch.

It ends with what happens next:

```
Ask someone who can read the store to run: ezc user approve sam
Then run ezc store pull.
```

Your name is `--name`, else the `user` of `config.toml`, else it is asked for on a terminal; with none of these the command stops and asks for `--name`. A machine (`--kind machine --owner <user>`) needs a name of its own and an owner who is already a user in `recipients.yaml`; its `config.toml` gets the owner as `user`.

Only the public key leaves the machine: it is in the request and may be printed. The private key stays in the key file and is in no output, no argument and no commit. Like `ezc setup`, `ezc join` refuses to run while `EZC_STORE` is set or while the environment holds the key itself (`EZC_AGE_KEY` or `SOPS_AGE_KEY`); `SOPS_AGE_KEY_FILE` names the key file to use.

Run again, `ezc join` changes only what is missing and pushes nothing twice: a request of yours that is already on the remote is reported as `✓ your request is waiting: ezc/join-<name>`. When your key is already one `recipients.yaml` names, it says `✓ <name> can already read this store.` and files nothing. When the name belongs to a recipient with another key, or somebody else's request under that name is waiting, it stops with `The store already has a recipient named <name>. Pick another name with --name.`

`ezc join --print` is for a store with no remote to push a request to, or for sending the request another way. It takes no repo and needs no store: it finds or creates the key and prints one line on stdout, `ezc user add <name> <key>` (with `--kind machine --owner <user>` for a machine), which someone who can read the store runs as it is.

### Approving a request

```sh
ezc user requests      # fetch, and show every request
ezc user approve sam   # let sam in
ezc user deny sam      # or refuse
```

`ezc user requests` fetches from the remote and prints each request that is waiting:

```
sam
  kind       user
  owner      -
  key        age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p
  host       sams-laptop
  requested  2026-10-11
  author     sam <ezc@localhost>
```

Approving is a decision to trust a key, made on text that somebody else wrote. Anyone who can push to the store's repo can file a request under any name, and the request says only what its writer typed. So the command shows you what you are about to trust, and no more than it knows:

- **The key** is printed whole, never shortened, because it is the one thing that matters: whoever holds its private half reads every secret once you approve. Ask the person for their public key over a channel you already trust (they see it with `ezc join --print`) and compare it with this line. A name you recognise proves nothing.
- **The host** and **the date** are what the requesting machine wrote into the request. They help you tell two requests apart; they are claims.
- **The author** is the Git author of the commit that filed the request, which is whatever the requester's git was set to say. It is a claim too. On GitHub, the account that pushed the branch or opened the pull request is better evidence than this line.

The name, kind and owner must hold to the same rules as an entry of `recipients.yaml`, and the key must be a valid public key; the host, the date and the author are printed with every character outside printable ASCII shown as `?` and cut at 80 characters, so a request cannot write to your terminal. A request that breaks a rule is listed as `<name>: not a valid request (<why>)` and cannot be approved. A request whose branch is not named `ezc/join-<a valid name>` is not shown anywhere.

`ezc user approve <name>` fetches first and reads the request as the remote holds it at that moment, so a branch that was changed after you looked is approved as it is now, not as you saw it: the command prints the request again, key and all, before it changes anything, and then asks:

```
Approve sam? [y/N]
```

Anything but `y` or `yes` changes nothing, prints `Not approved.` and exits 1. The key above the question is the one that is added: the request is not read again after you answer, so nothing pushed while you were reading can take its place. Without a terminal there is nobody to ask, and the command stops unless it is given `--yes`; a script that passes `--yes` approves a key nobody looked at, so use it only where the key was checked some other way. When the remote cannot be reached, nothing is approved. It then adds the entry (kind and owner from the request, today's date, you as `addedBy`), writes the policy, re-wraps every encrypted file, commits as `approve <name>` and pushes, exactly as `ezc user add` does and with the same refusals: a name that is taken, a key that is already registered, a machine whose owner is not a user.

**The request's branch is never merged.** Approve reads one file from it, `.ezc/requests/<name>.yaml`, and ignores everything else it holds, so a branch that also changes `recipients.yaml` or a values file changes nothing in the store. After the push the branch is deleted from the remote, which closes its pull request, but only while it is still at the commit the request was read from: when somebody pushed another request under the same name in the meantime, the branch is left and a `!` line says that a new request arrived. Do not merge that pull request on GitHub: a merge brings in whatever the branch holds. When somebody did merge it, the request file is in the store itself; it is listed and approved the same way and removed in the approve commit. When the branch and the store hold different requests under one name, neither is approved.

A request is read from the branch itself and never from a tag. Git looks a short name up among the tags first, so a tag called `origin/ezc/join-sam` would otherwise stand in for the branch; ezc names every ref in full and fetches and clones without tags, so no tag changes what is read. One tag can still get in the way of a push: a tag on the remote whose own name is `refs/heads/<branch>` makes git refuse every push to that branch, `ezc store push` included when the branch is the store's. Nothing is read from it, and ezc says what it is: `the remote has a tag named refs/heads/<branch>, which blocks pushes to that branch. Delete the tag on the remote.`

When the push fails the commit stays, the branch is left and the command says so: run `ezc store push`, then `ezc user approve <name>` again, which finds the key already registered and only deletes the branch. The same second run finishes the job when only the branch could not be deleted. It ends with `Tell <name> to run ezc store pull.`: on their machine that brings the re-wrapped files, which their key now opens.

`ezc user deny <name>` removes the request file, when it is in the store, in a commit named `deny <name>` and pushes, and deletes the request's branch, held to the commit it read in the same way. It asks nothing. It changes nobody's access, and it is how a request that is not valid is cleared. Like every change to who can read the store, approve and deny need this machine's key file and a key that `recipients.yaml` names.

### Removing someone

`ezc user remove <name>` takes the entry out, re-wraps every encrypted file without that key and commits as `remove <name>`, the same way `user add` does, and pushes. Re-wrapping does not un-leak anything: the removed key opened every value in the store while it was a recipient, and it still opens every older commit, which is still in the store's history and in every clone and backup of it. Only a new value protects a secret, so the command writes each secret the key could read to `rotation-due.yaml` (the file and the name, with who was removed and the date) and prints them:

```
2 secrets were readable by sam and need new values:
  common.enc.yaml/TOKEN
  env.prod.enc.yaml/TOKEN
Give each a new value with ezc set -g NAME.
```

Change the secret itself, at the service that issued it, then give ezc the new value with `ezc set -g NAME` (add `--env` for an environment's file). Setting the secret, or removing it with `ezc unset -g NAME`, clears its entry in the same commit; the file goes with its last entry. `ezc store status` shows `rotation due: <n> secrets`, and `ezc store status --due` lists them, one `<file>/<NAME>  readable by removed <who> since <date>` line each. Nothing here prints a value. Whoever is removed is not told, and nothing here takes the values back from them.

The command refuses a bot (`ezc bot remove`), the last recipient, a user who still owns machines (remove the machines first), and this machine's own key when no other entry of the same person could read the store: `<name> is this machine's key and your only one. Removing it would lock you out.`

`ezc store rewrap` runs the same steps with the registry as it is, in a commit named `rewrap`. When every file already matches it says `✓ every file is encrypted for the registry` and commits nothing.

Every change to access follows the same rules:

- It contacts the remote first, and is refused while the store is behind it (`The store is behind its remote. Run ezc store pull, then run this again.`), while git cannot say how the store stands, and while `recipients.yaml`, the policy file, `rotation-due.yaml` or an encrypted file has uncommitted changes.
- It needs this machine's key in a key file, and that key must be one `recipients.yaml` names: only someone who can read the store can re-wrap it.
- If any step fails, every file is put back as it was and nothing is committed; the error ends with `Nothing was changed.`
- It pushes, because a change to access that stays on one machine has changed nothing for anyone else. The push sends every commit the store is ahead by. When the push fails the commit stays, the command exits 1 with `Committed, but the push failed (<why>). Run ezc store push.`, and `ezc store push` sends it later. A store with no remote is committed and not pushed; a store that is not a git repo is changed with no commit.

Re-wrapping changes who can open the files from now on. It does not change the values, and it reads and prints none.

`ezc user rotate` replaces the key of the machine you run it on. It creates the new key in a new file beside the old one, re-wraps every encrypted file for it and saves the new file as `key_file` in `config.toml` once every file is proved readable with it, then commits as `rotate <name>` and pushes; if the change fails and the store is put back, the new file is removed and `config.toml` is as it was. If the store cannot be put back, both key files are kept and the message names them. If the push fails the rotation stands here: run `ezc store push`. The old key file is never rewritten or deleted; it no longer opens the store, but it still opens every older commit, so delete it when you are sure and use `--compromised` when it may have leaked. Nobody can rotate another person's key, because there is no safe way to hand them the private half.

### Bots and CI

```sh
ezc bot add ci --repo owner/app --repo owner/api   # a key for the bot, saved in both repos
ezc bot rotate ci                                  # replace it
ezc bot rotate ci --compromised                    # replace it after a leak
ezc bot remove ci                                  # the bot can no longer read the store
ezc bot add deploy --print-private | your-ci secret set EZC_AGE_KEY   # for a CI system that is not GitHub Actions
```

A bot is a recipient with a key of its own, so that a CI job reads the store without anybody's personal key, and so that it can be removed alone. A job needs two secrets:

| Secret | What it is | Who makes it |
| --- | --- | --- |
| `EZC_AGE_KEY` | The bot's private key, which decrypts the store's secrets. | `ezc bot add` |
| `EZC_STORE_TOKEN` | A token that can read the store's repo, to clone it over HTTPS. | You: `ezc` cannot create it. |

`ezc bot add <name> --repo owner/repo` creates the key in memory, adds its public half to `recipients.yaml` with kind `bot` and the repos it was given, writes the policy and re-wraps every encrypted file. Then, before anything is committed, it checks that the new private key opens every encrypted file and saves it as the `EZC_AGE_KEY` Actions secret of each repo with `gh secret set`, which reads it on stdin. Only then does it commit, as `add bot <name>`, and push:

```
✓ add bot ci
✓ pushed to https://github.com/owner/store
✓ EZC_AGE_KEY set in owner/app
✓ EZC_AGE_KEY set in owner/api
The bot also needs to read the store repo. Create a fine-grained token that can read that repo's contents and save it as the EZC_STORE_TOKEN secret in each repo. ezc cannot create that credential.
```

The private key is never written to disk, never passed as an argument or in the environment, and never printed unless you ask. It needs `gh`, signed in as someone who can set the secrets of each repo; without `gh` the command stops before it writes anything. When a repo cannot be given the key, the store is put back as it was and nothing is committed: `EZC_AGE_KEY could not be set in owner/api (<why>). Nothing was changed.` A secret that was set cannot be taken back. So when anything fails after a repo was given the key, a later repo that refuses it or the commit itself, that repo holds a key that was never registered and opens nothing, and the message names it and what to run: `EZC_AGE_KEY was set in owner/app to a key the store does not name, which reads nothing. The store was not changed. Run the same ezc bot add again.` The second run makes a new key and sets it in every repo. `--repo` is not yet checked against anything but its shape, `owner/repo`. A repo has one `EZC_AGE_KEY`, so give a repo to one bot only: a second bot added with the same repo replaces the first one's key there.

`--print-private` prints the private key on stdout as one line, with nothing else there: every other line of the command goes to stderr, so the output can be piped straight into another CI system's secret store. The same one line is what `gh` is given. Do not run it with stdout on a terminal you share or record. It can be given with `--repo` or alone; one of the two is needed. The key is printed after the commit and the push, and with no `--repo` that line is its only copy: when it cannot be written the command exits 1 and says `The bot <name> is registered, but its key could not be written to stdout and is lost.` A program that stops reading the pipe ends `ezc` before it can say anything. Either way run `ezc bot rotate <name> --print-private` for a new key.

The store credential is the part you do by hand, once for each repo whose jobs read the store:

1. On GitHub open Settings, Developer settings, Personal access tokens, Fine-grained tokens, and generate a token.
2. Under Repository access choose "Only select repositories" and pick the store's repo alone.
3. Under Repository permissions set Contents to "Read-only" and leave everything else as it is.
4. Save it as the secret: run `gh secret set EZC_STORE_TOKEN --repo owner/app` and paste the token at the prompt, which does not show it. Do not put it on the command line.

`EZC_STORE_TOKEN` holds a token, and nothing else works there. A deploy key is an SSH key, not a token: `ezc setup --ci` does not support one, and saving one as `EZC_STORE_TOKEN` makes the clone fail.

A job needs `git`, `sops` and `ezc` on its `PATH`. Give the two secrets to the steps that run `ezc` and to no other: a variable set for the whole job is also in the environment of every action and script the job runs. `ezc setup --ci` needs both; a later `ezc` command needs the key, and the token too when it looks at the remote (see [Syncing the store](#syncing-the-store)). The command `ezc run` starts inherits them along with the environment it was asked for.

```yaml
jobs:
  deploy:
    runs-on: ubuntu-latest   # has git
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: Install sops and ezc
        run: |
          curl -fsSL -o sops https://github.com/getsops/sops/releases/download/v3.13.3/sops-v3.13.3.linux.amd64
          sudo install -m 0755 sops /usr/local/bin/sops
          go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest
      - name: Deploy with the store's values
        env:
          EZC_AGE_KEY: ${{ secrets.EZC_AGE_KEY }}
          EZC_STORE_TOKEN: ${{ secrets.EZC_STORE_TOKEN }}
        run: |
          ezc setup --ci --store owner/store
          ezc run prod -- ./deploy.sh
```

`ezc setup --ci --store owner/repo` clones `https://github.com/owner/repo.git` into ezc's home with git itself, not `gh`, as a shallow clone of the latest commit, checks the key, and writes `config.toml` with the backend and the store's remote and with no `user` and no `key_file`. It prints `✓ git`, `✓ sops`, `+ cloned the store: <path>`, `✓ the key: EZC_AGE_KEY` and `+ wrote <config>`, and no `Next:` line; run again where the store is in place, every line is a `✓`. It never asks a question and never installs anything. `--store` is required and takes `owner/repo`, or the absolute path of a local repo; `--new-local`, `--new-github` and `--name` cannot be used with `--ci`.

It stops before it makes anything when a variable is missing or the key is not one: `ezc setup --ci: EZC_AGE_KEY is not set. Save the bot's key as that secret; ezc bot add does it.`, `ezc setup --ci: EZC_AGE_KEY does not hold a key. Save the bot's key as that secret; ezc bot add does it.` or `ezc setup --ci: set EZC_STORE_TOKEN (or GH_TOKEN) to a token that can read owner/repo.` After the clone it checks that the key opens one of the store's encrypted files, when the store has any, and stops with `✗ The key in EZC_AGE_KEY cannot read this store.` when it does not: the bot was removed or rotated, or the secret holds another key.

The store's repo must be on github.com. The token is offered to `https://github.com` and to no other host, so a store on a GitHub Enterprise host, or on any other, is not supported by `--ci`.

Neither secret is written anywhere. The token is never in an argument, a URL or the clone's `.git/config`: git is given a credential helper, for `https://github.com` only, whose text names the variable, and reads the token from its own environment. The key is never written to a file: every command that decrypts passes the text of `EZC_AGE_KEY` to `sops` as `SOPS_AGE_KEY`, in the environment `ezc` was given it in. `EZC_AGE_KEY` holds the one secret line `ezc bot add` saves; the whole text of a key file works too. It wins over `SOPS_AGE_KEY`, `SOPS_AGE_KEY_FILE` and every key file.

Which token is used follows one rule, for every command and not only `ezc setup --ci`: `EZC_STORE_TOKEN` when it is set; else `GH_TOKEN`, but only while `EZC_AGE_KEY` is also set, which is a CI run; else none. With a token, every clone, fetch and push of a store on github.com is made with it, in place of git's own credential helpers for that host. So a `GH_TOKEN` you have exported for other tools changes nothing on your own machine: git keeps the credentials it has. While `EZC_AGE_KEY` is set every command reads with that key, so unset it on a machine that has a key file. A machine that has the key only as text cannot change who reads the store: `ezc user add`, `ezc user remove`, `ezc bot add` and the rest stop with `An access change needs a key file, and this machine uses none. Run this on a machine that keeps its key in a file.`, and `ezc setup` without `--ci`, `ezc join` and `ezc user rotate` refuse to run.

`ezc bot rotate <name>` creates a new key and puts it in the bot's entry in place of the old one, in one commit named `rotate bot <name>`; from that commit on the old key opens no file. The new key is delivered as `ezc bot add` delivers one, to the repos the entry names, before the commit and with the same failure, which here matters more: a repo that took the new key before the failure has lost the old one, so its jobs cannot read the store until `ezc bot rotate <name>` is run again, as the message says. When the commit is made and only the push fails, the repos hold the new key while the remote still names the old one: run `ezc store push`, which is what makes their jobs work again. A bot that was added with `--print-private` alone has no repos, and needs `--print-private` here too. A routine rotation records nothing: the old key never left its repos' secrets. When it may have leaked, pass `--compromised`: every secret the old key could read is written to `rotation-due.yaml` as readable by `<name> (old key)` and listed, exactly as a removal does, because the old key still opens every older commit of the store.

`ezc bot remove <name>` takes the bot's entry out, re-wraps every encrypted file without its key, commits as `remove bot <name>` and pushes, then deletes `EZC_AGE_KEY` from each repo the entry names and lists the secrets that need new values, as [`ezc user remove`](#removing-someone) does. A repo the secret could not be deleted from is named on a `!` line, as are all of them when `gh` is not installed; the bot is removed all the same, and its key opens nothing committed from then on. `EZC_STORE_TOKEN` is yours, and is left where it is: delete it when no job of that repo reads the store any more. `ezc bot remove` and `ezc bot rotate` refuse an entry that is not a bot.

### A store made by hand

A store whose encryption policy you wrote yourself has no `recipients.yaml`, so `ezc` does not know who can read it, and `ezc user list`, `ezc user add`, `ezc user remove` and `ezc store rewrap` say so. To hand it over, write `.ezc/recipients.yaml` with one entry for each key your policy lists, this machine's among them:

```yaml
recipients:
  gene:
    key: age1…
    kind: user
    added: "2026-10-11"
    addedBy: gene
```

A name is lower case letters, digits and `-`; `kind` is `user`, `machine` (then add `owner: <a user's name>`) or `bot`; no two entries share a key. Commit the file with git, then run `ezc store rewrap`: it replaces your policy with one written from the file and re-wraps the encrypted files. From then on, change access with `ezc user add`, never by editing the policy.

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
| `recipients.yaml` | In the store only: who can decrypt it, by name and public key. `ezc setup` writes the first entry, and `ezc user add`, `ezc user approve` and `ezc bot add` the next ones; see [Access](#access). |
| `env.sh` | The shim mise or direnv sources. Written by `ezc repo init`; do not edit. |

On a machine, ezc's home holds the store (`store/`, a git repo whose values are in its `.ezc/`), `config.toml` (your name in the store, the key file, and the store's remote) and `state.json` (when the store's remote was last reached). `ezc setup` writes the first two.

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

The key file is the `key_file` of `$EZC_HOME/config.toml` when that file names one, else `~/.config/sops/age/keys.txt`; `ezc setup` creates it when it is not there. It is passed to `sops` as `SOPS_AGE_KEY_FILE`; when `SOPS_AGE_KEY` or `SOPS_AGE_KEY_FILE` is already set in the environment, that wins. `EZC_AGE_KEY`, the key's own text as a CI job has it, wins over all of them (see [Bots and CI](#bots-and-ci)). `ezc setup` follows the same rule: with `SOPS_AGE_KEY_FILE` set it uses that file as your key file (creating it when it is not there) and records it as `key_file`; the path must be absolute. With `SOPS_AGE_KEY` or `EZC_AGE_KEY` set it stops and asks you to unset it, because that key would be used instead of the one setup registers; `ezc setup --ci` is the setup that takes the key from `EZC_AGE_KEY`.

A secret is typed at a hidden prompt or piped on stdin, never passed as an argument, and reaches `sops` on its stdin only. A value cannot move across the secret boundary: `ezc set` refuses a name that is already in the other file of the pair, so remove it with `ezc unset`, which clears both files of the pair, first.

A store's commits should pass `ezc store audit`. Without a key it still checks names and the boundary between plaintext and encrypted files, and says which files' values it could not check. A line added by hand to an encrypted file makes the whole file undecryptable: the audit names the unencrypted value and skips that file's value checks (`! damaged`), and the fix is to restore the file from git and set the value again with `ezc set`, treating the old value as leaked.

## When something is wrong

Start with `ezc doctor`. It checks the machine, the store and the repo and says what to do about each thing it finds: a missing key (a warning: an environment with no encrypted file still loads, and one that has any fails as a whole until the key is there), a missing `sops` or `git`, a missing or invalid `config.toml` (`ezc setup` writes it; doctor does not suggest setup while `EZC_STORE` is set, because setup does not run then), a store or repo manifest that does not parse, an `env.sh` written by an older `ezc` (run `ezc repo init`) or by a newer one (a warning: upgrade `ezc`; `ezc repo init` leaves that shim alone), encrypted files in a folder with no encryption policy, and files in a `.ezc/` folder that `ezc` never reads, such as `common.yml` or `comon.yaml`, with the name it probably should have. For a store that is a git repo it also reports where the store stands against its remote (`sync`: in sync, local only, ahead or behind with the command that fixes it, uncommitted changes, or diverged, which fails, as does a store whose state cannot be read, such as one that is not on a branch; it fetches by the same 10 minute rule as other commands and says when origin could not be reached), whether your key is one of the store's recipients (`access`; a key that is not fails: when this machine has asked with `ezc join`, doctor says that its request is waiting for approval and who to ask to run `ezc user approve`, and otherwise to run `ezc join <remote>`, or `ezc join --print` for a store with no remote; with the key in the environment as text, as in CI, the machine's public key is not known and the item is left out; a store with an encryption policy and no `recipients.yaml` is a warning that says to write one), whether every encrypted file is encrypted for the registry's recipients (`recipients`; a file that is not fails and names the files, and `ezc store rewrap` fixes it), the access requests waiting in the store (`requests`: up to 5 names, then `and <n> more`; `ezc user requests` shows them), and the secrets that were readable by a removed key (`rotation`: a warning with the count, `ezc store status --due` lists them and `ezc set -g NAME` gives each a new value; a `rotation-due.yaml` that cannot be read fails). On the machine, `git` fails when its version is older than 2.23. `ezc doctor prod` checks that environment, and `--only` limits the report to one part. For the detail of a single failure, `ezc env` lists what is wrong with the environment and `ezc store audit` what is wrong with the store.

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
