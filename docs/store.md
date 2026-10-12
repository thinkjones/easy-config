[← README](../README.md)

# The store

The store is a git repo. This page covers what a write to it does and how it is kept level with its remote.

## Writing to the store

The store is a git repo, and every write to it is one commit: `ezc set -g`, `ezc unset -g` and `ezc edit -g`, or the same commands without `-g` run inside the store. The commit holds only the files the command changed and its message names them, never a value: `set NAME (common.enc.yaml)`, `set NAME (manifest.yaml, common.yaml)` for a name declared in the same step, `unset NAME (common.yaml, common.enc.yaml)`, `edit env.prod.yaml`. A command that changes nothing, or that fails and puts the files back, makes no commit; an `ezc unset -g` that removed the value from one file before failing on the other commits what it removed. The author is git's own identity, else the `user` of `config.toml`, else `ezc`.

Anything else you changed in the store by hand is left alone: it is never swept into a commit named after something else, and commands say `store: uncommitted changes in <store>` until you commit or discard it with git. An `ezc set -g` or `ezc unset -g` that would write a file that itself has uncommitted changes is refused before anything is written, because the commit would take them with it: `common.yaml has uncommitted changes in <store>. Commit or discard them with git, then run this again.` The files they may change are the values file targeted (both files of the base for `ezc unset`) and the manifest when a name is declared or undeclared. `ezc edit -g` is not refused: it opens such a file, and its commit, `edit <file>`, holds what was changed there by hand as well. An `ezc edit -g` whose result is not valid is saved but not committed; the edit that fixes it is.

A write is refused while the store is behind its remote or has diverged from it, before anything is written:

```
the store is 2 commits behind origin. Run `ezc store pull` first: a change made now could not be merged.
```

A store that is ahead or has no remote takes the write; when the remote cannot be reached, what was last known of it decides. When the write worked and the commit did not, the command says so and where to commit it with git.

A write is also refused when ezc cannot tell how the store stands, because then it cannot tell whether the change could be merged or even committed. A store that is not on a branch is the usual case, and a rebase left open is the usual cause: `the store in <store> is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again.` A store that is a git repo on a machine without git is refused the same way: `git is not installed, so a change to the store could not be committed. Install git, then run this again.` Commands that only read (`ezc env`, `ezc run`, `ezc list`, `ezc get`) still work in both cases; they say `store: cannot tell how the store stands in <store> (the store is not on a branch)` on stderr and carry on.

A secret is written only to a file that is encrypted for exactly the people `recipients.yaml` names. A file that somebody else can still open, because they were removed on another machine and this one's copy was made or kept under the old list, would hand them the new value, so `ezc set -g`, `ezc unset -g` and `ezc edit -g --secrets` refuse it before anything is written or asked:

```
env.prod.enc.yaml is not encrypted for the people recipients.yaml names. Run ezc store rewrap.
```

A file that does not exist yet is made from the store's encryption policy, so a policy that names other keys than `recipients.yaml` is refused the same way: `The store's encryption policy does not name the people recipients.yaml names, so env.prod.enc.yaml would not be encrypted for them. Run ezc store rewrap.` [`ezc store rewrap`](access.md#removing-someone) makes the policy and the files match the registry again. The check reads who each file is encrypted for from the file itself and needs no key. A store with no `recipients.yaml` is not checked: ezc does not know who should read it.

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

When the commits a pull brought changed who `recipients.yaml` names, it says so on stderr, whether or not you expected it: `! recipients.yaml changed on the remote: + sam - kim. ezc user list shows who can read the store.` (`~ name (new key)` is a key that was replaced). See [Who can change the registry](access.md#who-can-change-the-registry).

After every pull, also one that brought nothing, ezc compares who each encrypted file is encrypted for with `recipients.yaml` as the store now has it. A file that differs is named on stderr and the command exits 1:

```
! env.staging.enc.yaml is not encrypted for the registry. Run ezc store rewrap before you set anything.
```

It happens when a change to who can read the store and a write to a file crossed: somebody was removed and the removal pushed while this machine, which had not fetched yet, wrote a secret to a file under the list it still had. Until `ezc store rewrap` has run, writes to that file and `ezc store push` are refused, so the file does not reach the remote while a removed key opens it. When the commit that was made here and lost its file was itself a change to who can read the store (it was committed and its push failed), the pull does not list values to set again, because the file it kept is still encrypted for the old list:

```
! common.enc.yaml was re-wrapped here and changed on the remote. The remote's version was kept, and it is not encrypted for the people recipients.yaml names. Run ezc store rewrap, then set again any value you changed in it since your last push.
```

A plaintext file changed in the same place on both sides stops the pull, and the store is left exactly as it was:

```
.ezc/common.yaml, .ezc/manifest.yaml were changed here and on the remote in the same place, so nothing was pulled. Resolve them in /Users/gene/Library/ezc/store with git (git pull --rebase).
```

This is a limit to know about: two machines that each declare a new name at the same time both add it at the end of `manifest.yaml` (and its value at the end of the same values file), so the second to pull gets this conflict and must resolve it with git: run `git pull --rebase` in the store, edit each file named so that it holds both sides' entries in full (when two declarations differ only in their names, git marks only the name lines, and the lines under them must be written out for each), `git add` the files and `git rebase --continue`. Until you run `git pull --rebase` the store has diverged: `ezc store status` says `diverged from origin`. While the rebase is open the store is not on a branch: `ezc store status`, `pull` and `push` exit 1 and say `the store in <store> is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again.`, and `ezc doctor` reports the same as a problem. Writes to the store are refused throughout, and reading still works. Pull before you declare a name and push soon after to make it rare. Setting a value for a name that is already declared does not touch the manifest.

When the files in the conflict are `recipients.yaml` or the encryption policy, the message is a different one and git cannot resolve it by hand: see [Removing someone](access.md#removing-someone) for the way out.

A pull is also refused when the store holds uncommitted changes (commit or discard them with git first), when it has no remote (`ezc setup --new-github owner/repo` gives it one), and when the remote cannot be reached.

One more limit: a file that git ignores does not count as an uncommitted change, so a pull replaces it without a word when the remote tracks a file at that path. It takes a `.gitignore` written by hand in the store and a file added past it on another machine; keep anything you want to keep out of the store's folder.

`ezc store push` fetches, then prints `pushed 2 commits to origin` or `nothing to push`. It is refused while the store is behind the remote or has diverged from it (``the store is behind origin. Run `ezc store pull` first.``), while it holds uncommitted changes, and when it has no remote. It is also refused while a commit made here waits to be sent and an encrypted file is not encrypted for the people `recipients.yaml` names: `env.staging.enc.yaml is not encrypted for the people recipients.yaml names, so nothing was pushed. Run ezc store rewrap.`

You do not have to ask. `ezc env`, `ezc run`, `ezc list`, `ezc get`, `ezc store audit` and the commands that write to the store (`ezc set -g`, `ezc unset -g`, `ezc edit -g`) look at the store before they use it, and print one line on stderr when there is something to say:

```
store: 2 commits behind origin — run ezc store pull
store: 1 commit ahead of origin — run ezc store push
store: diverged from origin (1 ahead, 2 behind) — run ezc store pull
store: uncommitted changes in /Users/gene/Library/ezc/store
store: 1 access request pending: sam
```

A store with uncommitted changes that is also ahead, behind or diverged says both: `store: uncommitted changes in <store>; 1 commit behind origin`. Pending access requests are the ones still on their `ezc/join-<name>` branch of the remote, as of the last fetch, and the ones in the store's `.ezc/requests/`; they are added to the end of the line when it already says something else, and `ezc user requests` shows them. The line names the first five and counts the rest (`sam, kim, … and 3 more`). A store that is in sync prints nothing. When git cannot say how the store stands, the line is `store: cannot tell how the store stands in <store> (<why>)` and nothing else. To keep these commands fast they contact the remote at most once every 10 minutes and wait at most 3 seconds for it; in between, and when the remote cannot be reached, the line is what was last known, and a fetch that just failed adds `store: origin could not be reached; last checked 2 hours ago`. The command then carries on. `ezc env export` never contacts the remote and never prints the line, so loading a shell's environment is not slowed down or made noisy. `ezc store status`, `pull` and `push` always contact the remote and do not print the line: their own output is the answer.
