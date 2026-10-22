# Contributing to easy-config

`easy-config` is the source of `ezc`, a Go CLI that gives a person or a small team one place for secrets and variables. This page is how to work on it: what to run, what has to pass, and what stops a change landing.

The code itself is described in [AGENTS.md](AGENTS.md) (where things are, and the conventions) and [CLAUDE.md](CLAUDE.md) (the commands, and the sharp edges). Read those before a change that touches `cli/`.

## Set up

`ezc` needs Go 1.26 or later, `git` 2.23 or later and [`sops`](https://github.com/getsops/sops) for secrets. The task runner is [mise](https://mise.jdx.dev), which pins the versions this repo uses; `lefthook` is pinned in `mise.toml` too.

```sh
git clone https://github.com/thinkjones/easy-config.git
cd easy-config
direnv allow        # once per clone: installs the local gate
```

`direnv allow` is the whole setup. It runs `mise install`, which installs the pinned tools and writes the local gate into the hooks git runs (see [the gate](#the-gate) below). If you use mise's shell activation instead of direnv, `cd` into the project does the same thing; mise asks once whether to trust the project's config.

Everything else is optional:

```sh
mise run cli:build          # build ezc into cli/bin/
mise run cli:test           # gofmt, go vet, go test — what a change to cli/ must pass
mise run cli:test:sops      # the same gate with the real-sops and real-git tests required
mise run schema:build       # regenerate the committed manifest JSON Schema
mise run schema:check       # fail when that schema is stale
```

## The gate

There is one gate and one definition of it: `mise run cli:test` runs `gofmt -l`, `go vet ./...` and `go test ./...` over the whole module. CI runs the same task, so a green laptop and a green CI mean the same thing.

| Where | What runs | What it can be bypassed by |
| --- | --- | --- |
| Before a commit | `gofmt`, `go vet` and the tests of the packages the staged files belong to | `git commit --no-verify` |
| Before a push | the full `mise run cli:test`, or `cli:test:sops` when `sops` and `git` are on `PATH` | `git push --no-verify` |
| On a pull request | `mise run cli:test`, `mise run cli:test:sops` and `mise run schema:check`, then the **CI Gate: all required checks passed** | nothing |
| Every day, on `main` | `mise run cli:test:sops` and `mise run schema:check` | nothing |

The CI Gate is the check that has to pass before a pull request merges. It waits for both test jobs and fails when either of them did not succeed, so a change that breaks the real-sops end-to-end suite cannot go green.

The push hook refuses a push to `main`: `main` is merged through a pull request and the CI Gate. Run the gate yourself first (`mise run cli:test:sops`); `git push --no-verify` is the deliberate way past it, and the hook says so when it refuses.

**`main` is not protected yet.** Nothing on GitHub requires the CI Gate today, so the local hooks and the daily job are what stand behind it. Branch protection (the required check named `CI Gate: all required checks passed`) is on its way; when it lands, the CI Gate becomes the server-side enforcement and the push hook mirrors it.

## Making a change

1. Work on a branch. `main` is not pushed to directly.
2. Commit in the conventional form the repo uses: `feat(resolve): …`, `fix(cli): …`, `docs: …`.
3. Push the branch and open a pull request. The CI Gate and the daily job's checks are named above; `gh pr checks --watch --required` waits for them.
4. A command, a flag or a message that changes is changed in `docs/` in the same commit. `docs/superpowers/` is history and is not rewritten to match the code.

The rules the code itself has to keep are in [AGENTS.md](AGENTS.md#conventions) and [CLAUDE.md](CLAUDE.md#sharp-edges): no value in any output, no secret outside the key file, `git` command lines built in one place, tests that never touch the real home.

## The manifest schema

`cli/schema/v1/manifest.schema.json` is generated from the Go types in `cli/internal/manifest`. After changing them, run `mise run schema:build` and commit the result. `mise run schema:check` fails when the committed file is stale; it runs on the pull request and every day on `main`.

## Reporting

A bug or a gap in the docs is an issue on [GitHub](https://github.com/thinkjones/easy-config/issues). For a security problem, do not open a public issue: ask the maintainers first.

## Licence

MIT — see [LICENSE](LICENSE).
