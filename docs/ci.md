[← README](../README.md)

# Bots and CI

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

`ezc bot add <name> --repo owner/repo` creates the key in memory, adds its public half to `recipients.yaml` with kind `bot` and the repos it was given, writes the policy and re-wraps every encrypted file. Then, before anything is committed, it checks that the new private key opens every encrypted file and saves it as the `EZC_AGE_KEY` Actions secret of each repo with `gh secret set`, which reads it on stdin. Only then does it commit, as `add bot <name>`, and push. A repo holds one secret named `EZC_AGE_KEY`, so a `--repo` that another bot's entry already names is refused before anything is made (`owner/app already gets its key from the bot ci, and a repo holds one EZC_AGE_KEY. Give that bot's key to the workflows that need it, or remove ci first.`): a second bot there would replace the first one's key, and removing either would delete what the other reads with.

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

A job needs `git`, `sops` and `ezc` on its `PATH`. Give the two secrets to the steps that run `ezc` and to no other: a variable set for the whole job is also in the environment of every action and script the job runs. `ezc setup --ci` needs both; a later `ezc` command needs the key, and the token too when it looks at the remote (see [Syncing the store](store.md#syncing-the-store)). The command `ezc run` starts inherits them along with the environment it was asked for.

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

`ezc bot remove <name>` takes the bot's entry out, re-wraps every encrypted file without its key, commits as `remove bot <name>` and pushes, then deletes `EZC_AGE_KEY` from each repo the entry names and lists the secrets that need new values, as [`ezc user remove`](access.md#removing-someone) does. A repo the secret could not be deleted from is named on a `!` line, as are all of them when `gh` is not installed; the bot is removed all the same, and its key opens nothing committed from then on. `EZC_STORE_TOKEN` is yours, and is left where it is: delete it when no job of that repo reads the store any more. `ezc bot remove` and `ezc bot rotate` refuse an entry that is not a bot.
