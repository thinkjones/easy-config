# ezc

`ezc` ("easy config") is a Go CLI that gives a person or a small team one place for secrets and variables. A global store holds values common to every environment and values per environment; a repo keeps the same kind of values in its own `.ezc/`, layered over the store. A manifest says which values a repo requires, and `ezc` validates the composed environment against it, then exports it or runs a command with it.

## Install

```sh
go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest
```

## Status

Parts 1 and 2 of 7 are built: the environment commands, and secrets: encrypted values files read and written through `sops`, `ezc set`, `unset` and `edit`, `ezc store audit` and `ezc doctor`. Still to come: `ezc setup`, store sync, users and bots, the registry and the TUI.

## Quick start

```sh
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
| `ezc env [ENV] [-m FILE] [--verbose]` | Check an environment against the manifest. Reports on stderr, prints no values, exits 1 on problems. With `-m FILE` the values come from that manifest's folder, not this repo. |
| `ezc env export [ENV] [--format shell\|json]` | Print the environment's variables on stdout. `shell` is for `eval`; `json` is one object of names to values, for any other tool. Outside a repo it exports the store alone. |
| `ezc run [ENV] -- <command> [args…]` | Run a command with the environment added to its own. On Unix `ezc` replaces itself with the command, so signals sent to it reach the command and the exit status is the command's. Exits 127 when the command is not found, 126 when it cannot be run. |
| `ezc list [ENV] [-g]` | List the names an environment holds, with each one's type (from the repo's manifest, else the store's) and the file that supplies it, and the manifest's names that have no value. Never prints a value. `-g` lists the store alone. |
| `ezc get NAME [ENV] [--reveal]` | Print one value on stdout. A `secret` needs `--reveal`; a name is treated as a secret when the repo's or the store's manifest declares it one, and so is any value read from an encrypted file or built by referencing a secret (`URL: https://u:$TOKEN@host`). |
| `ezc set NAME [VALUE] [--env E] [-g] [--expand] [--type T] [--desc D] [--optional] [--values a,b] [--min N] [--max N]` | Set a value in `common.yaml` (or `env.E.yaml`), in the repo or with `-g` in the store. A name the manifest does not declare is declared in the same step, as required unless `--optional`: give `--type` and `--desc`. The declaration is written first, then the value; if the value cannot be written the manifest is put back as it was. The value is checked against the declared type and bounds, and stored literally: what you give is what loads, so a `$` is written to the file as `$$` (a literal value cannot start with `~` or `~/`, which is always expanded). With `--expand` the value is written as given, so `$NAME`, `${NAME}`, `$$` and a leading `~` keep their meaning at load; the type check is then skipped for a value holding a `$`, the environment is resolved once, and a reference to a name that is not defined yet puts the file back as it was. `--expand` does not apply to a secret, which is always stored exactly as given. A secret goes to the encrypted file and is typed at a hidden prompt or piped on stdin, never given as an argument; leave VALUE out of any variable to do the same. A name already in the other file of the pair (plaintext against encrypted) is refused. Prints names and files, never the value. |
| `ezc unset NAME [--env E] [-g] [--undeclare]` | Remove a name's value from the plaintext and the encrypted file of the base (`common` or `env.E`), whichever hold it. The declaration stays; `--undeclare` removes it too, and is refused while any other values file in the scope still holds the name. |
| `ezc edit [--env E] [--secrets] [-g]` | Open `common.yaml` (or `env.E.yaml`), or with `--secrets` the encrypted file of the same name, in `$VISUAL` or `$EDITOR`, in the repo or with `-g` in the store. The editor must wait until you finish editing, so a graphical one needs a flag, for example `export EDITOR="code --wait"`; ezc refuses one that returns at once. The command is split on spaces, including in a path (so an editor under a folder with a space in its name needs a wrapper script on a path without one), and quoting inside `EDITOR` is not supported. A plaintext file that is not valid afterwards is reported, and a file this command created and you left empty is removed. With `--secrets` the file must already exist (create it with `ezc set`), and the result is decrypted again to check it; closing the editor without a change is fine. Needs a terminal. |
| `ezc store audit` | Check the store's values files against its manifest: every value declared, every `required` name set in at least one file, secrets in encrypted files and nothing else there, no value still a placeholder or the wrong shape, and no value in an encrypted file left unencrypted. Prints names, files and the fix for each, never a value; exits 1 when there is anything to fix. |
| `ezc doctor [ENV] [--only machine\|store\|repo]` | Report the health of the machine (ezc, the encryption tool, the key), the store (location, manifest, audit, policy, unrecognised files) and the repo (manifest, `env.sh` shim version, environments, whether the environment resolves and passes the manifest). Each line is a check (`✓`), a warning (`!`) or a problem (`✗`), with what to do about it. Prints names, counts and paths, never a value; exits 1 when anything is broken, and warnings alone exit 0. Works without a key and outside a repo. |
| `ezc repo init` | Create `.ezc/` in the working directory with `manifest.yaml`, `env.local.yaml` and the `env.sh` shim. Existing files are kept; an `env.sh` from an older shim is updated. Prints how to load the environment. |
| `ezc manifest validate [-m FILE]` | Check that the manifest is well formed; prints the path and counts of required and optional variables. |
| `ezc manifest schema [-o FILE]` | Print the manifest's JSON Schema, or write it to FILE. |
| `ezc codegen python [-m FILE] [-o FILE]` | Validate the manifest, then generate a pydantic config module. An invalid manifest generates nothing, and neither does one with a variable named after a Python keyword such as `class` or one whose name starts with `_`. |

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

A variable the manifest declares `secret` is stored encrypted, in `common.enc.yaml` or `env.<name>.enc.yaml`, and `ezc set` writes it there. Until `ezc setup` does this for you in a later version, a folder needs its encryption policy written by hand before a secret can be stored in it: create `.ezc/.sops.yaml` with

```yaml
creation_rules:
  - path_regex: \.enc\.yaml$
    age: age1...your public key...
```

The key file is `~/.config/sops/age/keys.txt`, unless `SOPS_AGE_KEY` or `SOPS_AGE_KEY_FILE` is set. Make a key with `mkdir -p ~/.config/sops/age && age-keygen -o ~/.config/sops/age/keys.txt`; its public key is printed, and goes after `age:` above.

The key file is passed to `sops` as `SOPS_AGE_KEY_FILE`; when `SOPS_AGE_KEY` or `SOPS_AGE_KEY_FILE` is already set in the environment, that wins. A secret is typed at a hidden prompt or piped on stdin, never passed as an argument, and reaches `sops` on its stdin only. A value cannot move across the secret boundary: `ezc set` refuses a name that is already in the other file of the pair, so remove it with `ezc unset`, which clears both files of the pair, first.

A store's commits should pass `ezc store audit`. Without a key it still checks names and the boundary between plaintext and encrypted files, and says which files' values it could not check. A line added by hand to an encrypted file makes the whole file undecryptable: the audit names the unencrypted value and skips that file's value checks (`! damaged`), and the fix is to restore the file from git and set the value again with `ezc set`, treating the old value as leaked.

## When something is wrong

Start with `ezc doctor`. It checks the machine, the store and the repo and says what to do about each thing it finds: a missing key (a warning, because plaintext values still load), a store or repo manifest that does not parse, an `env.sh` written by an older `ezc` (run `ezc repo init`), encrypted files in a folder with no encryption policy, and files in a `.ezc/` folder that `ezc` never reads, such as `common.yml` or `comon.yaml`, with the name it probably should have. `ezc doctor prod` checks that environment, and `--only` limits the report to one part. For the detail of a single failure, `ezc env` lists what is wrong with the environment and `ezc store audit` what is wrong with the store.

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
