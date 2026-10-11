# ezc

`ezc` ("easy config") is a Go CLI that gives a person or a small team one place for secrets and variables. A global store holds values common to every environment and values per environment; a repo keeps the same kind of values in its own `.ezc/`, layered over the store. A manifest says which values a repo requires, and `ezc` validates the composed environment against it, then exports it or runs a command with it.

## Install

```sh
go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest
```

## Status

Parts 1 and 2 of 7 are built: the environment commands, and reading encrypted files.

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
| `ezc env [ENV] [-m FILE] [--verbose]` | Check an environment against the manifest. Reports on stderr, prints no values, exits 1 on problems. |
| `ezc env export [ENV] [--format shell\|json]` | Print the environment's variables on stdout. `shell` is for `eval`; `json` is one object of names to values, for any other tool. Outside a repo it exports the store alone. |
| `ezc run [ENV] -- <command> [args…]` | Run a command with the environment added to its own. On Unix `ezc` replaces itself with the command, so signals sent to it reach the command and the exit status is the command's. Exits 127 when the command is not found, 126 when it cannot be run. |
| `ezc list [ENV] [-g]` | List the names an environment holds, with each one's type (from the repo's manifest, else the store's) and the file that supplies it, and the manifest's names that have no value. Never prints a value. `-g` lists the store alone. |
| `ezc get NAME [ENV] [--reveal]` | Print one value on stdout. A `secret` needs `--reveal`; a name is treated as a secret when the repo's or the store's manifest declares it one, and so is any value read from an encrypted file. |
| `ezc set NAME [VALUE] [--env E] [-g] [--type T] [--desc D] [--optional] [--values a,b] [--min N] [--max N]` | Set a value in `common.yaml` (or `env.E.yaml`), in the repo or with `-g` in the store. A name the manifest does not declare is declared in the same step, as required unless `--optional`: give `--type` and `--desc`. The value is checked against the declared type and bounds. A secret goes to the encrypted file and is typed at a hidden prompt or piped on stdin, never given as an argument; leave VALUE out of any variable to do the same. A name already in the other file of the pair (plaintext against encrypted) is refused. Prints names and files, never the value. |
| `ezc unset NAME [--env E] [-g]` | Remove a value, and its declaration once no values file in the scope holds the name. |
| `ezc repo init` | Create `.ezc/` in the working directory with `manifest.yaml`, `env.local.yaml` and the `env.sh` shim. Existing files are kept; an `env.sh` from an older shim is updated. Prints how to load the environment. |
| `ezc manifest validate [-m FILE]` | Check that the manifest is well formed; prints the path and counts of required and optional variables. |
| `ezc manifest schema [-o FILE]` | Print the manifest's JSON Schema, or write it to FILE. |
| `ezc codegen python [-m FILE] [-o FILE]` | Validate the manifest, then generate a pydantic config module. An invalid manifest generates nothing, and neither does one with a variable named after a Python keyword such as `class`. |

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

The key file is `~/.config/sops/age/keys.txt`, unless `SOPS_AGE_KEY` or `SOPS_AGE_KEY_FILE` is set. Make a key with `age-keygen -o ~/.config/sops/age/keys.txt`; its public key is printed, and goes after `age:` above.

A secret is typed at a hidden prompt or piped on stdin, never passed as an argument. A value cannot move across the secret boundary: `ezc set` refuses a name that is already in the other file of the pair, so remove it with `ezc unset` first.

## Resolution

Later wins:

```
store  common.yaml → common.enc.yaml → env.<name>.yaml → env.<name>.enc.yaml
repo   common.yaml → common.enc.yaml → env.<name>.yaml → env.<name>.enc.yaml
```

`<name>` is the command's argument, else `$EZC_ENV`, else `$APP_ENV`, else `local`. With no name given and no `env.local.yaml`, only the `common` files load, and `ezc` says so on stderr. A missing store is not an error: the repo's files compose alone. A store that is there but is not a folder, or cannot be read, is an error, and so is an `EZC_STORE` that names a folder which does not exist.

- Within a file, order is semantic.
- A value may reference `$NAME` or `${NAME}` for a name already resolved, earlier in the same file or in an earlier file. `$$` is a literal `$`.
- Values from an encrypted file are literal: no `~`, `$HOME` or `$NAME` is expanded in them. A later plaintext file can still reference them by name. A command that needs an encrypted file's key stops with an error, and prints nothing, when it has none.
- A leading `~`, `$HOME` or `${HOME}` is expanded at load.
- There are no defaults: a missing value fails the check. The check says what is wrong with a value (`not an integer`, `above the maximum of 9`, `not a URL (must start with http:// or https://)`) and never prints the value.
- Shell export single-quotes every value and never emits `EZC_ENV` or `APP_ENV`.
