# ezc

`ezc` ("easy config") is a Go CLI that gives a person or a small team one place for secrets and variables. A global store holds values common to every environment and values per environment; a repo keeps the same kind of values in its own `.ezc/`, layered over the store. A manifest says which values a repo requires, and `ezc` validates the composed environment against it, then exports it or runs a command with it.

## Install

```sh
go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest
```

## Status

Part 1 of 7 is built; encrypted files arrive in part 2.

## Commands

| Command | What it does |
| --- | --- |
| `ezc version` | Print ezc's version. |
| `ezc help [command]` | Help for ezc, or for one command. |
| `ezc env [ENV] [-m FILE] [--verbose]` | Check an environment against the manifest. Reports on stderr, prints no values, exits 1 on problems. |
| `ezc env export [ENV] [--format shell\|dotenv\|json]` | Print the environment's variables on stdout. `shell` is for `eval`; `dotenv` is for dotenv loaders and must not be sourced by a shell. Outside a repo it exports the store alone. |
| `ezc run [ENV] -- <command> [args…]` | Run a command with the environment added to its own. |
| `ezc list [ENV] [-g]` | List the names an environment holds, with each one's type and the file that supplies it, and the manifest's names that have no value. Never prints a value. `-g` lists the store alone. |
| `ezc get NAME [ENV] [--reveal]` | Print one value on stdout. A `secret` needs `--reveal`; a name is treated as a secret when the repo's or the store's manifest declares it one. |
| `ezc manifest validate [-m FILE]` | Check that the manifest is well formed; prints the path and counts of required and optional variables. |
| `ezc manifest schema [-o FILE]` | Print the manifest's JSON Schema, or write it to FILE. |
| `ezc codegen python [-m FILE] [-o FILE]` | Validate the manifest, then generate a pydantic config module. An invalid manifest generates nothing. |

## How a repo loads its environment

The environment is named by the argument, else `EZC_ENV`, else `APP_ENV`; with none of them, `local` applies when it exists. There are three ways in:

- mise: in `mise.toml`, `[env]` then `_.source = ".ezc/env.sh"`. `ezc repo init` writes that file.
- direnv: `eval "$(ezc env export)"` in `.envrc`.
- neither: `ezc run -- <command>` runs one command with the variables and leaves the shell alone.
