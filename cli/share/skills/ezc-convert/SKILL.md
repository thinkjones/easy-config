---
name: ezc-convert
description: Convert a repo to load its environment with ezc. Use when a repo still reads .env files, hardcodes secrets, wires env vars directly in mise.toml, or has no .ezc/ folder, and when asked to "adopt ezc", "use the shared config", "get rid of .env", or "add a manifest" for environment variables. Also covers promoting a value into the store and debugging a converted repo that loads nothing.
---

# Convert a repo to ezc

Take a repo from ad-hoc environment loading (`.env`, hardcoded values, `mise.toml [env]`) to ezc: values in the store plus the repo's own `.ezc/`, validated against a manifest the repo owns.

## The model in one paragraph

The store holds values more than one repo needs. Each repo declares in its own `.ezc/manifest.yaml` what **it** requires, and holds its own values in `.ezc/`. `ezc env export` composes both; `ezc env` validates that same composition against the manifest. Both go through one resolver, so what is checked is by construction what is loaded. What a repo requires lives in the repo, never in the store.

Resolution order, later wins (every file is optional):

```
<store>/.ezc/common.yaml           shared plaintext
<store>/.ezc/common.enc.yaml       shared secrets
<store>/.ezc/env.<name>.yaml       shared, per environment
<store>/.ezc/env.<name>.enc.yaml
.ezc/common.yaml                   repo, every environment
.ezc/common.enc.yaml               repo secrets
.ezc/env.<name>.yaml               repo, per environment
.ezc/env.<name>.enc.yaml
```

The environment is the command's argument, else `$EZC_ENV`, else `$APP_ENV`, else `local`.

## Step 0: Preflight

```bash
ezc doctor
```

Every line must be `✓` or `!`. A `✗` means the machine is not set up and no conversion will work. Fix that first with what doctor says, usually `ezc setup` (or `ezc join owner/store` for a store someone else runs). If `ezc` is not on PATH at all, stop and say so. Do not hand-roll a substitute.

## Step 1: Inventory what the repo actually uses

Do not guess from a `.env.example`. Find real usages:

```bash
rg -n 'os\.environ|getenv|process\.env|System\.getenv|ENV\[' --no-heading
cat .env .env.* 2>/dev/null
sed -n '/\[env\]/,/^\[/p' mise.toml 2>/dev/null
rg -n '\$\{?[A-Z][A-Z0-9_]*' .github/workflows docker-compose*.yml Dockerfile 2>/dev/null
```

Produce one list of variable names. For each, note where it is read and whether its value differs between environments. Then check which are already in the store, by name, without printing a value:

```bash
ezc list -g
```

## Step 2: Classify every variable

| Question | The answer decides |
|---|---|
| Already in the store? | Declare it in the repo's manifest; store **no value** in the repo |
| Is it a credential? | `type: secret` (ezc puts it in an encrypted file) or another type (plaintext) |
| Does it vary by environment? | `--env <name>` or every environment |
| Needed to start, or does its absence turn a feature off? | `required:` or `optional:` |

Rules that are not negotiable:

- **Never copy a store value into the repo.** If a value is in the store, the repo declares it in `required:` and stores nothing. A copy is a second source of truth that drifts.
- **The declared type is the secret boundary.** `ezc set` writes a `secret` to an encrypted file and anything else to plaintext; never put a credential under another type.
- **No defaults.** A default turns a missing production secret into a silent fallback. If a value is required, declare it required and let the check fail.
- **`local` must be inert.** It is the default environment, so it must not reach a live resource: a filesystem path, a scratch bucket, a fake endpoint. Anything that costs money or changes real data belongs only in a named environment.

If a variable is used by **two or more** repos, put it in the store instead: see "Promoting a value" below.

## Step 3: Create `.ezc/` and the manifest

```bash
ezc repo init
```

This writes `.ezc/manifest.yaml`, `.ezc/env.local.yaml` and the `.ezc/env.sh` shim, and keeps any that exist. Declare what the repo requires from the store in the manifest by hand:

```yaml
schemaVersion: v1

required:
  LLM_API_KEY:
    type: secret
    desc: API key for whichever provider LLM_PROVIDER names. In the store.
  BATCH_SIZE:
    type: int
    min: 1
    max: 100
    desc: Articles per scoring call.

optional:
  DIGEST_OUTPUT_PATH:
    type: path
    desc: Where the digest is written when DIGEST_OUTPUT=file.
    requiredWhen: {var: DIGEST_OUTPUT, equals: file}
```

Types: `str`, `int`, `bool`, `secret`, `url`, `path`, `enum`. `enum` needs `values:`; `min` and `max` apply to `int`. Write a real `desc` for every entry: it is what someone sees when the check fails, and the generator emits it as the docstring.

```bash
ezc manifest validate
```

## Step 4: Write the values

Use `ezc set`, never a hand-edited encrypted file. A name the manifest does not declare yet needs `--type` and `--desc`, and is declared in the same step:

```bash
ezc set BATCH_SIZE 25 --type int --desc "Articles per scoring call."
ezc set LLM_PROVIDER openrouter --env prod --type str --desc "Which LLM provider to call."
ezc set SOME_REPO_TOKEN --type secret --desc "Token for the one service only this repo calls."
```

A secret is read from a hidden prompt or from stdin, never from the command line. `ezc set` writes literally (`$` stays `$`); `--expand` keeps `$NAME` references to earlier values. A repo with secrets of its own needs them encrypted for the store's recipients: run `ezc repo add`, then `ezc repo rewrap` if `ezc doctor` says the repo's recipients differ.

> **Moving a value from plaintext to a secret requires rotating it.** It is in git history in plaintext. Encrypting it now protects the future, not the past.

## Step 5: Wire mise or direnv

```toml
[env]
_.source = ".ezc/env.sh"

[tasks."env:check"]
hide = true
run = "ezc env"
```

Then `depends = ["env:check"]` on **every** task that touches real infrastructure. With direnv instead: `source_env .ezc/env.sh` in `.envrc`. With neither: `ezc run -- <command>`.

> **`_.source` takes a FILE PATH.** There is no command syntax in mise's `[env]`, and an unresolvable `_.source` loads nothing and reports no error. That silence is why `env:check` is not optional.

> **The environment's name comes from the shell, never from a file.** `ezc env export` never emits `EZC_ENV` or `APP_ENV`: mise unwinds the variables it owns, so a mise-owned name would keep its old value. Use `EZC_ENV=prod mise run <task>`, never a task-level `env = { EZC_ENV = "prod" }`.

## Step 6: Verify

```bash
ezc env                     # the default environment
ezc env prod                # every other environment, one at a time
ezc env --verbose           # which file set each value, and what it beat
ezc list                    # names and where each comes from, never values
mise run <a-real-task>      # proves env:check gates it
```

Then prove the gate fails: add a bogus `required:` entry, confirm `mise run <task>` refuses to run, and remove it. A gate nobody has seen fail is not known to work.

## Step 7: Remove the old mechanism

Only after Step 6 passes:

- Delete `.env`, `.env.*` and any committed example that now lies.
- Delete the `[env]` entries ezc now supplies, and bespoke config-loading scripts.
- Add `.env`, `.env.*` and `keys.txt` to `.gitignore`.
- Run `ezc repo add` to register the repo in the store, then commit `.ezc/`, including its encrypted files: they are meant to be in the repo.

## Step 8: CI, if the repo has any

A job needs two secrets, `EZC_AGE_KEY` (a bot's key, which `ezc bot add <name> --repo owner/repo` creates and saves) and `EZC_STORE_TOKEN` (a token that can read the store's repo, which you create). The job runs `ezc setup --ci --store owner/store`, then `ezc run <env> -- <command>` or `mise run <task>`. Follow the ezc docs' "Bots and CI" page for the workflow, and:

- **Wiring the workflow does not create the secrets.** `gh secret list -R <repo>` must show both.
- **A named environment needs its files**, such as `.ezc/env.ci.yaml`, committed.
- **Never set a store value in the workflow's `env:`**: pass per-run values as task flags.

## Promoting a value into the store

A value earns a place in the store when a **second** repo needs the identical value:

```bash
ezc set -g NAME VALUE --type str --desc "What it is and who needs it."
ezc set -g NAME --type secret --desc "..."      # a secret, from the prompt or stdin
ezc store push
```

Then remove the value from each repo with `ezc unset NAME` and keep the declaration in the repo's manifest. `ezc store audit` checks the store is complete in both directions: every value declared, every declaration with a value.

## Generating typed config

```bash
ezc codegen python -o src/<pkg>/config.py
```

The output is derived: regenerate it, never hand-edit it, and add a CI drift check.

## When a converted repo loads nothing

1. `ezc doctor`: the shim's version, the store, the key, the repo's environments.
2. `ezc env`: the actual check, with the file to open for each problem. No output at all means the command never ran, not that it passed.
3. `env -u __MISE_DIFF -u __MISE_SESSION mise env | grep <VAR>`: mise keeps unwinding a variable it once owned until the shell is replaced.
4. `mise run env:check` directly, to confirm the task exists and is wired.

Never fix it by hardcoding a value or bringing back a `.env`: that turns a loud, findable configuration error into a silent one.
