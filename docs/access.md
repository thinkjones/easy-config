[← README](../README.md)

# Access

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

## Joining a store

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

## Approving a request

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

The name, kind and owner must hold to the same rules as an entry of `recipients.yaml`, and the key must be a valid public key; the host, the date and the author are printed with every character outside printable ASCII shown as `?` and cut at 80 characters, so a request cannot write to your terminal. A request that breaks a rule is listed as `<name>: not a valid request (<why>)` and cannot be approved. A request whose branch or file is not named after a valid name is not shown anywhere, because its name could write to your terminal. `ezc user requests` counts them on stderr, `! 1 request has a name that cannot be shown, so it is not listed and ezc user deny cannot remove it. Delete its branch or its file with git.`, since no ezc command takes such a name.

`ezc user approve <name>` fetches first and reads the request as the remote holds it at that moment, so a branch that was changed after you looked is approved as it is now, not as you saw it: the command prints the request again, key and all, before it changes anything, and then asks:

```
Approve sam? [y/N]
```

Anything but `y` or `yes` changes nothing, prints `Not approved.` and exits 1. The key above the question is the one that is added: the request is not read again after you answer, so nothing pushed while you were reading can take its place. Without a terminal there is nobody to ask, and the command stops unless it is given `--yes`; a script that passes `--yes` approves a key nobody looked at, so use it only where the key was checked some other way. When the remote cannot be reached, nothing is approved. It then adds the entry (kind and owner from the request, today's date, you as `addedBy`), writes the policy, re-wraps every encrypted file, commits as `approve <name>` and pushes, exactly as `ezc user add` does and with the same refusals: a name that is taken, a key that is already registered, a machine whose owner is not a user.

**The request's branch is never merged.** Approve reads one file from it, `.ezc/requests/<name>.yaml`, and ignores everything else it holds, so a branch that also changes `recipients.yaml` or a values file changes nothing in the store. After the push the branch is deleted from the remote, which closes its pull request, but only while it is still at the commit the request was read from: when somebody pushed another request under the same name in the meantime, the branch is left and a `!` line says that a new request arrived. Do not merge that pull request on GitHub: a merge brings in whatever the branch holds. When somebody did merge it, the request file is in the store itself; it is listed and approved the same way and removed in the approve commit. When the branch and the store hold different requests under one name, neither is approved.

A request is read from the branch itself and never from a tag. Git looks a short name up among the tags first, so a tag called `origin/ezc/join-sam` would otherwise stand in for the branch; ezc names every ref in full and fetches and clones without tags, so no tag changes what is read. One tag can still get in the way of a push: a tag on the remote whose own name is `refs/heads/<branch>` makes git refuse every push to that branch, `ezc store push` included when the branch is the store's. Nothing is read from it, and ezc says what it is: `the remote has a tag named refs/heads/<branch>, which blocks pushes to that branch. Delete the tag on the remote.`

When the remote moved while the approval ran, nothing is changed and the command says to pull and run it again. When the push fails for another reason the commit stays, the branch is left and the command says so: run `ezc store push`, then `ezc user approve <name>` again, which finds the key already registered and only deletes the branch. The same second run finishes the job when only the branch could not be deleted. It ends with `Tell <name> to run ezc store pull.`: on their machine that brings the re-wrapped files, which their key now opens.

`ezc user deny <name>` removes the request file, when it is in the store, in a commit named `deny <name>` and pushes, and deletes the request's branch, held to the commit it read in the same way. It asks nothing. It changes nobody's access, and it is how a request that is not valid is cleared. Like every change to who can read the store, approve and deny need this machine's key file and a key that `recipients.yaml` names.

## Removing someone

`ezc user remove <name>` takes the entry out, re-wraps every encrypted file without that key and commits as `remove <name>`, the same way `user add` does, and pushes. Re-wrapping does not un-leak anything: the removed key opened every value in the store while it was a recipient, and it still opens every older commit, which is still in the store's history and in every clone and backup of it. Each file a key leaves is also encrypted again from scratch, with a new data key (`sops rotate`), so the removed key opens nothing that is written from that commit on, with or without `ezc`; every line of the file changes in that commit. Only a new value protects a secret, so the command writes each secret the key could read to `rotation-due.yaml` (the file and the name, with who was removed and the date) and prints them:

```
2 secrets were readable by sam and need new values. Give each one with:
  ezc set -g TOKEN
  ezc set -g TOKEN --env prod
```

Change the secret itself, at the service that issued it, then give ezc the new value with the command listed for it, which names the file the secret is in (`--env prod` for `env.prod.enc.yaml`); a secret whose file or name could not be an argument of `ezc set` is listed as `<file>/<NAME>`. Setting the secret, or removing it with `ezc unset -g NAME`, clears its entry in the same commit; the file goes with its last entry. `ezc edit -g --secrets` does not clear an entry, whatever was changed in the editor: set the value with `ezc set -g`. When the secret is no longer in its file, because it was deleted in an editor or the file is gone, `ezc unset -g NAME` (with the file's `--env`) finds nothing to remove and clears the entry: `NAME is not set in store/env.prod.yaml or store/env.prod.enc.yaml; it no longer needs a new value there`. `ezc store status` shows `rotation due: <n> secrets`, and `ezc store status --due` lists them, one `<file>/<NAME>  readable by removed <who> since <date>` line each. Nothing here prints a value. Whoever is removed is not told, and nothing here takes the values back from them.

The command refuses a bot (`ezc bot remove`), the last recipient, a user who still owns machines (remove the machines first), and this machine's own key when no other entry of the same person could read the store: `<name> is this machine's key and your only one. Removing it would lock you out.`

`ezc store rewrap` runs the same steps with the registry as it is, in a commit named `rewrap`. When every file already matches it says `✓ every file is encrypted for the registry` and commits nothing. It takes `recipients.yaml` at its word, so read `ezc user list` before you run it; see [Who can change the registry](#who-can-change-the-registry).

Every change to access follows the same rules:

- It contacts the remote first, and is refused when the remote cannot be reached (`The store's remote could not be reached, so nobody else would get this change. Run this again when it can be.`), while the store is behind it or has diverged from it (``the store is 1 commit behind origin. Run `ezc store pull`, then run this again.``), while git cannot say how the store stands, and while `recipients.yaml`, the policy file, `rotation-due.yaml` or an encrypted file has uncommitted changes. A store with no remote has nobody to tell and is changed as it is.
- It needs this machine's key in a key file, and that key must be one `recipients.yaml` names: only someone who can read the store can re-wrap it.
- If any step fails, every file is put back as it was and nothing is committed; the error ends with `Nothing was changed.`
- It pushes, because a change to access that stays on one machine has changed nothing for anyone else. The push sends every commit the store is ahead by. A store with no remote is committed and not pushed; a store that is not a git repo is changed with no commit.
- When the remote took somebody else's commits between the fetch and the push, the push is refused, and the change is taken back: the commit is undone, the files it changed are put back (nothing else in the store is touched), and the command exits 1 with `The store's remote changed while this ran. Nothing was changed. Run ezc store pull, then run this again.` Two changes to who can read the store cannot be merged, so the second one is made again on top of the first.
- When the push fails for any other reason (the remote refuses it, or the network drops after the fetch), the commit stays and the command exits 1 with `Committed, but the push failed (<why>). Run ezc store push.` Run `ezc store push` soon. Until it is pushed nobody else has the change, and if the remote moves in the meantime `ezc store push` asks for `ezc store pull` first, which replays your commit on the remote's: an encrypted file changed on both sides keeps the remote's version, the pull says that the file is no longer encrypted for the people `recipients.yaml` names, and writes to it and `ezc store push` are refused until `ezc store rewrap` has run (see [Syncing the store](store.md#syncing-the-store)). `ezc user rotate` never leaves a commit like that: see below.

When two machines did change access without seeing each other, and one of the commits was left unpushed, `ezc store pull` on that machine stops, because `recipients.yaml` and the encryption policy are written by ezc and cannot be merged:

```
.ezc/.sops.yaml, .ezc/recipients.yaml were changed here and on the remote, so nothing was pulled. Who can read the store was changed on two machines at once, and the files ezc writes for that cannot be merged. In <store>, `git log refs/remotes/origin/main..HEAD` lists the commits made here that the remote lacks; drop them with `git reset --hard refs/remotes/origin/main`, then make your change again.
```

Do what it says: look at the commits you are about to drop, since a value you set since your last push is among them and has to be set again, run the `git reset --hard`, then run your `ezc user add`, `remove` or other change again. Do not try to merge the two files by hand.

Re-wrapping changes who can open the files from now on. It does not change the values, and it reads and prints none.

## Who can change the registry

`recipients.yaml` is a file in a git repo, so whoever can push to the store's branch can write a name and a key into it with git alone, without `ezc user approve` and without anybody seeing the key. That does not let them read anything yet: the files are still encrypted for the old list. The next re-wrap would, because every change to who can read the store re-wraps for everybody `recipients.yaml` names. `ezc join` pushes its request to the store's remote, so everybody who can ask for access can also push.

**Protect the store's default branch.** On GitHub, add a branch protection rule or a ruleset for the store's branch that lets only the people who are recipients push to it, and no force pushes. The `ezc/join-*` branches need no protection: a request on one is read as one file, shown whole and asked about, and never merged. With the branch protected, somebody who is not a recipient can file a request and nothing else.

ezc also says what it sees, so that a name written in by hand does not pass unnoticed:

- `ezc store pull` prints a line when the commits it brought changed the names or keys of the registry: `! recipients.yaml changed on the remote: + mallory - sam ~ kim (new key). ezc user list shows who can read the store.` After a change made with `ezc` by somebody you know, that line is what you expect to see. After a commit called `tidy`, it is not.
- A registry changed by hand leaves the files encrypted for the old list, so the same pull names them and exits 1, `ezc user list` and `ezc doctor` report them, and writes to those files and `ezc store push` are refused until somebody re-wraps.
- Every change to who can read the store prints, before it changes anything, each name that would gain access to a file and each key that would lose it, apart from the one the command is about: `! mallory gains access to common.enc.yaml, env.prod.enc.yaml.` The command was asked for its own change and goes on to make it, so the line is a warning, not a question: when it names somebody who should not read the store, take them out with `ezc user remove <name>` straight away and give the secrets it lists new values. Reading `ezc user list` before a change to access avoids the surprise, and `ezc user remove <name>` of a name that was written in lets nobody in.
- `ezc store rewrap` does nothing but trust the registry, so when a name would gain access it asks `Let mallory read the store? [y/N]` on a terminal, and without one it stops unless it is given `--yes`. Anything but `y` or `yes` prints `Not re-wrapped.` and changes nothing.
- A key that `recipients.yaml` does not name and that a file is still encrypted for loses access in the re-wrap: `! age1…, a key recipients.yaml does not name, loses access to env.staging.enc.yaml. The secrets it could read are recorded as needing new values: ezc store status --due lists them.` Those secrets get entries in `rotation-due.yaml` like the ones a removal writes, under the key itself, because a value written while that key could read the file is as exposed as one a removed person read.

`ezc user rotate` replaces the key of the machine you run it on. It creates the new key in a new file beside the old one, re-wraps every encrypted file for it and saves the new file as `key_file` in `config.toml` once every file is proved readable with it, then commits as `rotate <name>` and pushes; if the change fails and the store is put back, the new file is removed and `config.toml` is as it was. A rotation is not finished until the remote has it, because until then this machine would read with a key the remote's files are not encrypted for. So when the push fails, for any reason, the commit is taken back, the new key file is removed, `config.toml` is as it was and the command says `The push failed (<why>). Nothing was changed.`: you still have the key you had. If the store or the commit cannot be put back, both key files are kept and the message names them. The old key file is never rewritten or deleted. Once the command has printed `✓ new key in <file>` the old key no longer opens the store, but it still opens every older commit, so delete it when you are sure and use `--compromised` when it may have leaked. Nobody can rotate another person's key, because there is no safe way to hand them the private half.

## A store made by hand

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
