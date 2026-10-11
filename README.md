# ezc

`ezc` ("easy config") is a Go CLI that gives a person or a small team one place for secrets and variables. A global store holds values common to every environment and values per environment; a repo keeps the same kind of values in its own `.ezc/`, layered over the store. A manifest says which values a repo requires, and `ezc` validates the composed environment against it, then exports it or runs a command with it.

## Install

```sh
go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest
```

## Status

Part 1 of 7 is built; encrypted files arrive in part 2.

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
| `ezc get NAME [ENV] [--reveal]` | Print one value on stdout. A `secret` needs `--reveal`; a name is treated as a secret when the repo's or the store's manifest declares it one. |
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
| `env.sh` | The shim mise or direnv sources. Written by `ezc repo init`; do not edit. |

Encrypted `common.enc.yaml` and `env.<name>.enc.yaml` files, for secrets, arrive in part 2; today an existing one is an error.

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

## Resolution

Later wins:

```
store  common.yaml → env.<name>.yaml
repo   common.yaml → env.<name>.yaml
```

`<name>` is the command's argument, else `$EZC_ENV`, else `$APP_ENV`, else `local`. With no name given and no `env.local.yaml`, only the `common.yaml` files load, and `ezc` says so on stderr. A missing store is not an error: the repo's files compose alone. A store that is there but is not a folder, or cannot be read, is an error, and so is an `EZC_STORE` that names a folder which does not exist.

- Within a file, order is semantic.
- A value may reference `$NAME` or `${NAME}` for a name already resolved, earlier in the same file or in an earlier file. `$$` is a literal `$`.
- A leading `~`, `$HOME` or `${HOME}` is expanded at load.
- There are no defaults: a missing value fails the check. The check says what is wrong with a value (`not an integer`, `above the maximum of 9`, `not a URL (must start with http:// or https://)`) and never prints the value.
- Shell export single-quotes every value and never emits `EZC_ENV` or `APP_ENV`.
