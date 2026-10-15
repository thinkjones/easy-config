---
title: The TUI
nav_order: 7
---

# The TUI

```sh
ezc tui        # the repo you are in, over the store
ezc tui -g     # the store's values alone
```

`ezc tui` opens a terminal UI over the store and the repo you are in. It needs a terminal; everything it shows and does is also a command, so a script uses the commands instead.

```
ezc  store github.com/owner/store · env local                                    ▲ 2 behind  ● 1 request
╭─ values ── repo owner/app ─────────────────────────────────────────────────────────────────────────╮
│ ▶ LLM_API_KEY    secret   repo/common.enc.yaml  ••••••••            used by 3 repos                 │
│   CF_ACCOUNT_ID  str      store/common.yaml     a1b2c3d4e5f6        used by 5 repos                 │
│   BATCH_SIZE     int      store/env.local.yaml  20                  overrides common.yaml           │
╰────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ detail ── LLM_API_KEY ────────────────────────────────────────────────────────────────────────────╮
│ API key for whichever provider LLM_PROVIDER names                                                  │
│ required by  owner/digest · owner/website · owner/gascity                                          │
│ ! rotation due: readable by removed sam (2026-10-01)                                               │
╰────────────────────────────────────────────────────────────────────────────────────────────────────╯
1 values  2 access  3 repos  4 doctor  ? help     s/S set  u unset  r reveal  e env  g scope  p/P pull/push
```

The top line says where the store is, the environment the views use, how the store stands against its remote and how many access requests are waiting.

## Keys

| Key | What it does |
| --- | --- |
| `1` `2` `3` `4` | The values, access, repos and doctor views. |
| `↑` `↓`, `j` `k` | Move. |
| `tab` | The next panel. |
| `e` | Change the environment every view uses. |
| `ctrl+r` | Load the view again. |
| `?` | The keys of the view you are in. |
| `q` | Quit. `ctrl+c` quits too; while a command runs, press it twice. |

## The views

**Values** lists every name of the scope: its type, the file that supplies it, its value and how it is used. A secret's value is hidden. `g` switches between the repo (its values over the store's) and the store alone; outside a repo there is only the store. On the selected name:

| Key | What it does |
| --- | --- |
| `s` | Set it for every environment: `ezc set NAME`, in the store with `-g`. |
| `S` | Set it for the environment in the top line: `ezc set NAME --env E`. |
| `u` | Unset it from the file that supplies it: `ezc unset NAME`, with `--env` and `-g` from that file. |
| `r` | Show a secret's value. |
| `p`, `P` | `ezc store pull`, `ezc store push`. |

The value you type is hidden for a secret, and goes to the command on its standard input, never on its command line. Only a declared name can be set here: declaring a new one needs its type and description, so use `ezc set NAME --type T --desc D`.

**Access** lists who can read the store, the pending requests and the secrets that need a new value (see [Access](access.md)). On a request, `a` approves it after showing everything `ezc user approve` shows, the whole key included, and `d` denies it. On a recipient, `x` removes it (`ezc user remove`, or `ezc bot remove` for a bot), and `R` rotates this machine's key (`ezc user rotate`) or a bot's (`ezc bot rotate`). `b` adds a bot: its name, then its repos.

**Repos** lists the registered repos, each checked as `ezc repo check` checks it in its checkout here, for the environment in the top line. `enter` shows what the check found.

**Doctor** shows what `ezc doctor` reports.

## What a change does

Every change is an `ezc` command. Before it runs, a screen shows the command line, and `y` runs it; `esc` cancels. What the command printed is shown after it, and the views load again.

The command runs as it does without a terminal: it asks nothing. So a change the command would ask about first is refused, and the screen shows the command's own sentence. An access change that would also let in someone `recipients.yaml` names besides the person it is about is one: run it on the command line, after `ezc user list`. Approving a request runs `ezc user approve NAME --key KEY` with the key the confirm screen showed: the command approves only that key, and still refuses when approving would also let someone else in.

A program that needs the terminal, such as an editor or brew, is not run: the output says so, and the command is run on the command line. What the TUI does not do: `ezc edit`, `ezc repo rewrap` (it asks before it lets anyone read a repo's secrets), and declaring a new name.

## A revealed secret

`r` shows the selected secret's value until anything moves: another row, another view, the scope, the environment, or a command. The TUI then drops the value; it is not kept hidden in memory.

## The setup wizard

With no store set up, `ezc tui` opens the setup wizard, and so does `ezc setup` on a terminal when it is given no flag. It asks what to do:

1. a new store on this machine (`ezc setup --new-local`),
2. a new store on GitHub (`ezc setup --new-github owner/repo`),
3. join a store as a new person or machine (`ezc join owner/repo --name NAME`),
4. an existing store, with this machine's key (`ezc setup --store REPO`),

then the store's repo and your name, and shows the command before it runs it. It does not install missing tools: brew would need the terminal, which the TUI is using, so setup says what to install and you run the wizard again afterwards. Once setup has worked, `ezc tui` opens its views; see [Set up](setup.md) for what setup makes.
