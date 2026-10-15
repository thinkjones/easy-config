---
title: Home
nav_order: 1
description: ezc gives a person or a small team one place for secrets and variables, layered into every repo and checked against a manifest.
permalink: /
---

# ezc

`ezc` ("easy config") is a Go CLI that gives a person or a small team one place for secrets and variables. A global store, which is a git repo, holds values common to every environment and values per environment; a repo keeps the same kind of values in its own `.ezc/`, layered over the store. A manifest says which values a repo requires, and `ezc` validates the composed environment against it, then exports it or runs a command with it. Secrets are encrypted with [SOPS](https://github.com/getsops/sops) and [age](https://github.com/FiloSottile/age), and `ezc` manages who can read them.
{: .fs-6 .fw-300 }

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
```

When something does not work, run `ezc doctor`: it checks the machine, the store and the repo, and says what to do about each thing it finds.

## How it works

- **One layout, two places.** The store and a repo's `.ezc/` hold the same files: `common.yaml` for every environment, `env.<name>.yaml` for one, and an `.enc.yaml` beside each for secrets. The store's files load first, then the repo's, and a later file wins.
- **The manifest is the contract.** `manifest.yaml` declares every name with its type and description, required or optional. There are no defaults: a missing required value, or one of the wrong shape, fails `ezc env`, which says what is wrong and never prints the value.
- **Secrets stay encrypted at rest.** A value declared `secret` goes to the encrypted file, typed at a hidden prompt or piped on stdin, never given as an argument.
- **The store is a git repo.** Every write to it is one commit that names the files it changed and never a value.
- **Who can read it is one file**, `recipients.yaml`, and every encrypted file is encrypted for exactly the keys it names.

A repo loads its environment through mise (`_.source = ".ezc/env.sh"` in `[env]`), direnv (`source_env .ezc/env.sh`) or `ezc run -- <command>`.

## The pages

| Page | What is in it |
| --- | --- |
| [Set up](setup.md) | What `ezc setup` makes, what it never overwrites, and how it adopts a store made by hand. |
| [The store](store.md) | Writes as commits, the refusals that protect them, and `ezc store status`, `pull` and `push`. |
| [Access](access.md) | `recipients.yaml`, joining, approving a request, removing someone, rotating a key. |
| [Repos](repos.md) | `repos.yaml`, where each repo is checked out, checking every repo, and how a repo's own secrets follow the store's recipients. |
| [Bots and CI](ci.md) | A key for a bot, the two secrets a job needs, and a GitHub Actions workflow. |
| [The TUI](tui.md) | `ezc tui`: the four views, their keys, what a change runs, and the setup wizard. |
| [Migrating from fsconfig](migrate.md) | `ezc migrate`: the order, the proof, what it changes and what it reports. |
| [Reference](reference.md) | Every command, the files, encryption and the resolution rules. |
| [When something is wrong](troubleshooting.md) | What `ezc doctor` reports and what to do about it. |
