# ezc Part 6: TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ezc tui [-g]`: four views (values, access, repos, doctor) over the store and the working repo, and the setup wizard that `ezc tui` opens when there is no store and `ezc setup` opens on a terminal.

**Architecture:** A new package `cli/internal/tui` holds the Bubble Tea model, built in ipm's shape (`fit`, `panel`, palette, `?` help, golden files). It imports no ezc package: it depends on the interfaces `Store`, `Values`, `Access`, `Repos`, `Doctor` and `Commands` in `tui/deps.go`. `cli/internal/cli/tui.go` implements them. Reads call the functions the commands call; **every write is an ezc command line run in-process** (`Commands.Run`), shown on a confirm screen before it runs, so the TUI adds no behaviour and every guard, refusal and question of the CLI still applies.

**Tech Stack:** Go 1.26, Cobra v1.10.2; `charm.land/bubbletea/v2 v2.1.0`, `charm.land/lipgloss/v2 v2.0.6`, `charm.land/bubbles/v2 v2.2.1`, `github.com/charmbracelet/x/ansi v0.11.9` (the versions ipm uses, `~/dev/repos/intelligent-package-manager/cli/go.mod`).

**Spec:** `docs/superpowers/specs/2026-10-10-ezc-design.md` — sections "TUI", "Commands › Other" (`ezc tui [-g]`), "Commands › Machine" (`ezc setup` is the wizard), "Code structure" (`internal/tui`, `internal/cli/tui.go`), "Testing" (golden files).

**Reference implementation:** ipm's TUI, `~/dev/repos/intelligent-package-manager/cli/internal/tui/` (read-only). Copy its `fit`, `panel`, `selectedRow`, `plainRow`, `spaces` and palette from `style.go:11-99`, its golden helper from `view_test.go:17-35` and its key-driving helpers (`key`, `drain`, `press`, `typeText`) from `model_test.go`. ipm's confirm screen is `install_form.go` (`formConfirm`); its async runs are `install.go`.

## Global Constraints

- The gate is `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops` and `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run schema:check`; both pass at the end of every task. Never run `mise trust`, never push, never switch branches.
- `internal/tui` imports no package of this module and never `System`. `go list -deps ./internal/tui | grep easy-config` prints only `…/internal/tui`.
- **The TUI adds no behaviour.** Every change it makes is one `ezc` command line run through `Commands.Run`, and the confirm screen shows that exact line (with `ezc ` in front) before `y` runs it. A secret's value goes to the command on stdin, never in the line. The TUI never passes `--yes` except to `ezc user approve`, whose question the confirm screen asks itself (decision 4), and to `ezc setup` when the wizard's install toggle is on (decision 8).
- No secret value is shown, except the one the user reveals with `r`, which is dropped (not merely hidden) when the selection, the view, the scope or the environment changes, or an action runs. A value built from a secret (`resolve.Result.Refs`) counts as a secret, as in `ezc get`. Golden files hold no secret value.
- Everything shown that came from the store, a repo, a request or a command's output is untrusted: `fit` replaces every control character (including ESC) with `?` before it measures or truncates. The adapter also passes names, owners, hosts and dates through `recipients.Printable` as the commands do.
- Only `cli/internal/backend/sops/` and `cli/internal/cli/scope.go` may contain `sops`, `.sops.yaml`, `SOPS_` or `ENC[` outside tests and docs. Only `cli/internal/gitstore/` builds git command lines.
- Tests never read the real home, environment, key or git config; no network; `gh` and `brew` faked.
- Name rule: `grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/` prints nothing. (Comments in `cli/` say "the reference TUI", never the other tool's name.)
- Commands follow the existing shape: `new<Name>Cmd(sys System)`, `UsageError` (exit 2), everything external through `System`. CLAUDE.md and AGENTS.md change in the commit that changes the behaviour they describe. Markdown prose is one line per paragraph. `docs/superpowers/` is history, except this plan.
- Golden files are plain text (`ansi.Strip`) at 120×30 unless a case sets another size; update with `go test ./internal/tui -run Golden -update` and read the diff before committing.
- Commit trailer: `Co-Authored-By: Claude <model> <noreply@anthropic.com>`, naming the model that wrote the commit.

## Decisions this plan makes beyond the spec

1. **Writes are command lines.** `Commands.Run(args []string, stdin string) Outcome` runs `cli.Run(args, quiet)` where `quiet` is the TUI's `System` with `Stdout` and `Stderr` both pointed at one buffer, `Stdin` the given text, `StdinIsTerminal`, `StderrIsTerminal` and `StdoutIsTerminal` false, and `ReadSecret` an error. `Outcome.Lines` is the buffer split into lines, then `✗ <err>` when the error is not an `*ExitError`; `OK` is `err == nil`. Nothing the TUI reads goes through `Run`.
2. **The confirm screen** lists `ezc <args>` for each command, then the action's notes, then `y run · esc cancel`. After the run an output panel shows `Outcome.Lines` (teal title `done`, red `failed`); any key closes it and every view reloads. Only one run at a time: a key that would start a second while one is running sets the status `wait for the running command to finish`.
3. **A question the command itself would ask is not answered for it.** An access change that also lets in a name it is not about needs `--yes` without a terminal, so from the TUI it fails with the command's own sentence, which the output panel shows, and nothing changes: such a change is made from the CLI, after `ezc user list`. Likewise `ezc repo rewrap` is not offered.
4. **Approve** shows on its confirm screen what `ezc user approve` shows before its question (name, kind, owner, host, requested, Git author and the whole key, from `Access.Load`) and runs `user approve <name> --yes`.
5. **Values actions** (current scope, current environment `E`): `s` sets the selected name for every environment (`set NAME [-g]`), `S` for `E` only (`set NAME --env E [-g]`); the value is typed into a bubbles `textinput`, with `EchoPassword` for a secret, and passed on stdin. `u` unsets it from the layer and base that supply it, read from `Value.File` (`store/…` → `-g`; `env.X.yaml`/`env.X.enc.yaml` → `--env X`). Only declared names can be set from the TUI (a new name needs `--type` and `--desc`: the CLI). `P` pushes (`store push`), `p` pulls (`store pull`). No `edit`.
6. **Access actions** on the selected row: requests `a` approve, `d` deny (`user deny NAME`); recipients `x` remove (`user remove NAME` or `bot remove NAME` by kind), `R` rotate (`user rotate` on this machine's row, `bot rotate NAME` on a bot's; refused with a status on anyone else's row); `b` add a bot (name, then repos separated by spaces → `bot add NAME --repo R…`).
7. **Scope.** `g` toggles store and repo scope; outside a repo the TUI is store-only and `g` sets the status `not in a repo: the store only`. `-g` starts in store scope. The environment starts as `Header.Env` (the commands' default) and `e` changes it through a text input (validated by the command that next uses it; an invalid name shows that command's error).
8. **The wizard** asks: what to do (1 a new store on this machine, 2 a new store on GitHub, 3 join a store as a new person or machine, 4 an existing store, with this machine's key), then `owner/repo` (2, 3) or the store's repo (4), then the name (blank keeps setup's default; required for 3), then confirm with an `i` toggle `also install missing tools with brew` (adds `--yes` for setup; not offered for join). It runs `setup --new-local|--new-github R|--store R [--name N] [--yes]` or `join R --name N`. `ezc tui` opens it when `setup.HasStore` is false for the default store (and `EZC_STORE` is unset); on success `enter` reloads into the values view. `ezc setup` opens it when no store exists, no store flag was given, stdin and stdout are terminals and `System.RunTUI` is set; on success any key quits. Without `RunTUI` (tests) setup keeps its line prompts.
9. **`ezc tui`** refuses (`UsageError`: `ezc tui needs a terminal. Use the commands instead; ezc help lists them.`) unless stdin and stdout are terminals. `System` gains `StdoutIsTerminal func() bool` and `RunTUI func(tea.Model) error`; `main.go` sets `RunTUI` to `tea.NewProgram(m).Run()`.
10. **Loading.** Each view loads through its interface in a `tea.Cmd` when first shown and after every run; `ctrl+r` reloads the current view. The header (`Store.Status`) loads at start and after every run. A load error is shown in the view's panel as `✗ <error>`.

## Review Focus

1. **Hostile text** (a recipient name, owner, host, request date, a plaintext value or a command's output line holding `\x1b[31m`, `\r` or a newline) must render as one row with `?` in place of the control characters and never move the cursor or colour the screen. Tested in Task 1 (`fit`) and Task 4 (golden with a hostile request).
2. **A revealed secret** must be gone from `View()` and from the model after moving the selection, switching view, toggling scope, changing environment or running an action. Tested in Task 3.
3. **A tiny or resized terminal and empty lists** (no values, no requests, no repos; 40×10; a resize while the confirm screen is open) must neither panic nor index out of range: under 60×15 only `terminal too small` and `q` work. Tested in Task 2.
4. **A second `y`, or another action key, while a command runs** must not start a second command. Tested in Task 2.
5. **A store with no `recipients.yaml`, no `repos.yaml`, no remote, or that is not a git repo, and running outside a repo** must show the reason in the view and keep the other views working. Tested in Task 6.

---

### Task 1: The tui package's foundations

**Files:**
- Modify: `cli/go.mod`, `cli/go.sum`
- Create: `cli/internal/tui/deps.go`, `cli/internal/tui/style.go`, `cli/internal/tui/style_test.go`

**Interfaces:**
- Produces (`deps.go`), the whole contract between the TUI and ezc:

```go
type Scope int // StoreScope, RepoScope
const (StoreScope Scope = iota; RepoScope)

type Header struct {
	Store            string // ShowURL of the remote, else the store's folder
	Repo             string // the working repo's ID; "" outside a repo
	Env              string // the commands' default environment
	Ahead, Behind    int
	Diverged, Offline, Unknown bool // Unknown: the store's state cannot be told
	Requests         int
}
type Value struct {
	Name, Type, Desc string
	File             string   // "store/common.yaml", "repo/env.local.enc.yaml"; "" when the name has no value
	Shown            string   // the plaintext value; "" for a secret
	Secret           bool
	Overrides        []string // base names of the files it overrides
	RequiredBy, OptionalIn []string // registered repos, from their checkouts here
	Due              string   // "readable by removed sam (2026-10-01)"; "" when not due
}
type Recipient struct{ Name, Kind, Owner, Added, AddedBy string; Me bool; Repos []string }
type Request struct{ Name, Kind, Owner, Host, Requested, Author, Key, Problem string } // Problem "" when valid
type DueEntry struct{ Secret, Removed, On string } // Secret is "<file>/<NAME>"
type AccessData struct{ Recipients []Recipient; Requests []Request; Due []DueEntry; Differ []string }
type Repo struct{ ID, Checkout string; Here, OK bool; Report []string }
type State int // OK, Warn, Broken
type Item struct{ State State; Label, Detail, Fix string }
type Section struct{ Name string; Items []Item }
type Outcome struct{ Lines []string; OK bool }

type Store interface{ Status() (Header, error) }
type Values interface {
	List(s Scope, env string) ([]Value, error)
	Reveal(s Scope, name, env string) (string, error)
}
type Access interface{ Load() (AccessData, error) }
type Repos interface{ Load(env string) ([]Repo, error) }
type Doctor interface{ Check(env string) ([]Section, error) }
type Commands interface{ Run(args []string, stdin string) Outcome }
type Deps struct{ Store Store; Values Values; Access Access; Repos Repos; Doctor Doctor; Commands Commands }
```

- Produces (`style.go`): `fit(s string, w int) string`, `panel(title string, lines []string, w int, focused bool) []string`, `selectedRow(row string, w int, active bool) string`, `plainRow(row string, w int) string`, `spaces(n int) string`, `clean(s string) string`, and the palette styles `stTitle stText stMuted stAmber stTeal stError stSelected stRule`.

- [ ] **Step 1: Add the dependencies**

Run in `cli/`: `go get charm.land/bubbletea/v2@v2.1.0 charm.land/lipgloss/v2@v2.0.6 charm.land/bubbles/v2@v2.2.1 github.com/charmbracelet/x/ansi@v0.11.9`
Expected: `go.mod` lists the four at those versions.

- [ ] **Step 2: Write the failing tests** in `style_test.go`

```go
func TestFitCleansControlCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"a\x1b[31mb": "a?[31mb  ", "x\ry": "x?y      ", "two\nlines": "two?lines", "\x07": "?        ",
	} {
		if got := fit(in, 9); got != want { t.Errorf("fit(%q) = %q, want %q", in, got, want) }
	}
}
func TestFitTruncatesAndPads(t *testing.T) // fit("abcdef", 4) == "abc…"; fit("ab", 4) == "ab  "; fit("x", 0) == ""
func TestPanelShape(t *testing.T)          // panel("values ── global", []string{"a"}, 30, false): 3 rows, each lipgloss.Width == 30 after ansi.Strip; row 0 starts "╭─ values ── global " and ends "╮"; last row is "╰" + 28×"─" + "╯"
```

- [ ] **Step 3: Run them to see them fail**

Run: `cd cli && go test ./internal/tui/`
Expected: FAIL (undefined: fit, panel).

- [ ] **Step 4: Write `deps.go` and `style.go`**

`style.go` is the reference's `style.go:11-99` with one change: `fit` calls `clean` first. `clean` maps every rune `r` with `unicode.IsControl(r)` to `?`. `panel` cleans its title. Palette values as in the reference (`#F3EFE6 #C9D1D9 #8B95A3 #2C3747 #F0A847 #5CC8A8 #F77E7E`, selection background `#263142`).

- [ ] **Step 5: Run the tests**

Run: `cd cli && go test ./internal/tui/ && go list -deps ./internal/tui | grep easy-config`
Expected: PASS; the grep prints only `github.com/thinkjones/easy-config/cli/internal/tui`.

- [ ] **Step 6: Commit**

```bash
git add cli/go.mod cli/go.sum cli/internal/tui
git commit -m "feat(tui): the package's contract, palette, fit and panel"
```

### Task 2: The model shell: header, views, help, confirm and runs

**Files:**
- Create: `cli/internal/tui/model.go`, `cli/internal/tui/keys.go`, `cli/internal/tui/run.go`, `cli/internal/tui/model_test.go`, `cli/internal/tui/view_test.go`, `cli/internal/tui/testdata/*.golden`

**Interfaces:**
- Consumes: Task 1's `Deps` and style helpers.
- Produces: the test helpers in `view_test.go` — `golden(t, name string, m *Model)`, `view(m *Model) string`, `key(s string) tea.KeyPressMsg`, `drain(t, m *Model, cmd tea.Cmd)`, `press(t, m *Model, keys ...string)`, `typeText(t, m *Model, s string)`, the fakes `fakeStore`, `fakeValues`, `fakeAccess`, `fakeRepos`, `fakeDoctor`, `fakeCommands` (records each `Run`'s args and stdin, answers a set `Outcome`) and `newTest(t) (*Model, *fakes)`; `New(d Deps, s Scope) *Model` (120×30 default; `plain` when `NO_COLOR` is set); `(*Model) Init() tea.Cmd` (loads the header and the first view); `Update`, `View() tea.View` (`AltScreen = true`); `type view int` with `valuesView, accessView, reposView, doctorView`; `(*Model) confirm(title string, cmds [][]string, stdin string, notes []string)` which opens the confirm screen; `(*Model) setStatus(text string, ok bool)`; the messages `headerLoaded{h Header; err error}`, `ranMsg{out Outcome}`. Each later view implements `body(w, h int) []string`, `keys(k string) tea.Cmd`, `hints() string`, `help() [][2]string`, `load() tea.Cmd`, `loaded(msg tea.Msg) bool`.

Layout, top to bottom: the header line `ezc  store <Store> · env <Env>` with, right-aligned, `▲ N behind`, `▼ N ahead`, `◆ diverged`, `offline` or `store state unknown` (amber; Unknown and Diverged red), then `● N request(s)` (amber) when `Requests > 0`; the view's body (panels); the status line; the footer `1 values  2 access  3 repos  4 doctor` then the view's hints then `? help`. Under 60×15: the one line `terminal too small: make it at least 60×15, or press q`.

Keys, in order: a running command takes `ctrl+c` only (first arms with a status, second quits); `ctrl+c` quits; help open: `esc`/`?` close, `q` quits; too small: `q` only; confirm screen: `y` runs, `esc` cancels, nothing else; output panel: any key closes and reloads; a focused text input gets every key but `esc` (cancel) and `enter` (submit); else `1`-`4` switch view, `?` opens help, `q` quits, `ctrl+r` reloads, `tab` moves focus between the view's list and detail panel, then the view's keys. `j`/`k`/arrows move the selection, which is clamped to the list (an empty list has no selection and the view's action keys set the status `nothing selected`).

- [ ] **Step 1: Move the reference's `key`, `drain`, `press`, `typeText` and `golden` into `view_test.go`, and add the fakes and `newTest`** (`newTest` returns `New(Deps{…fakes…}, RepoScope)` with a header `{Store: "github.com/o/store", Repo: "o/app", Env: "local"}`).

- [ ] **Step 2: Write the failing tests** in `model_test.go`

```go
func TestGoldenShell(t *testing.T) // cases: "shell-values-empty" (no values), "shell-header-behind" (Behind 2, Requests 1: the header ends "▲ 2 behind  ● 1 request"), "shell-help" (after "?"), "shell-too-small" (WindowSizeMsg 40×10), "shell-confirm" (m.confirm("Remove sam", [][]string{{"user","remove","sam"}}, "", []string{"Every secret sam could read is listed as due for a new value."})), "shell-output-failed" (ranMsg{Outcome{Lines: []string{"✗ the store is 2 commits behind origin."}, OK: false}})
func TestConfirmRunsOnceAndShowsTheCommand(t *testing.T) // confirm(...), press "y", then "y" again before ranMsg arrives: fakeCommands saw exactly one Run with args {"user","remove","sam"}; status "wait for the running command to finish"
func TestConfirmEscRunsNothing(t *testing.T)              // confirm, press "esc": no Run
func TestRunReloadsTheHeaderAndTheView(t *testing.T)      // after ranMsg and a key: fakeStore.calls and the values fake's calls each grew by one
func TestResizeWhileConfirmOpen(t *testing.T)             // confirm open, WindowSizeMsg 40×10 then 120×30: no panic, the confirm screen is back
func TestEveryLineIsTheTerminalsWidth(t *testing.T)       // for each golden case at 120×30: every line of view(m) has lipgloss.Width 120 and the view has 30 lines
```

- [ ] **Step 3: Run them to see them fail**

Run: `cd cli && go test ./internal/tui/`
Expected: FAIL (undefined: New).

- [ ] **Step 4: Implement `model.go`, `keys.go`, `run.go`**

`run.go` holds the confirm screen, the output panel and the busy flag: `y` sets `busy` and returns a `tea.Cmd` that calls `Commands.Run` for each command in order, stopping at the first that is not `OK`, and returns one `ranMsg` with all the lines. The values view's body in this task is only its empty panel (`No values.`); Tasks 3-5 fill each view.

- [ ] **Step 5: Write the goldens and run the tests**

Run: `cd cli && go test ./internal/tui -run Golden -update && go test ./internal/tui/`
Expected: PASS; read each `testdata/shell-*.golden`.

- [ ] **Step 6: Commit**

```bash
git add cli/internal/tui
git commit -m "feat(tui): the model's shell, the confirm screen and one run at a time"
```

### Task 3: The values view

**Files:**
- Create: `cli/internal/tui/values.go`, `cli/internal/tui/values_test.go`, goldens
- Modify: `cli/internal/tui/model.go` (scope and environment state)

**Interfaces:**
- Consumes: `Values.List`, `Values.Reveal`, `Commands.Run` through `confirm`.
- Produces: the model's `scope Scope` and `env string`, which the repos and doctor views read.

The list panel's title is `values ── global` (store scope) or `values ── repo <Repo>`; columns `name`, `type`, `file`, `value` (`Shown`; `••••••••` for a secret; the revealed value for the selected secret; empty for a name with no value, whose file column is `(not set)`), `use` (`used by N repos` when `RequiredBy`+`OptionalIn` is not empty, else `overrides <first>` when `Overrides` is not empty). The detail panel `detail ── <Name>` shows `Desc`, `required by  <repos joined by " · ">`, `optional in …`, and `! rotation due: <Due>` (amber). Hints: `s set  S set for <env>  u unset  r reveal  e env  g scope  p pull  P push`.

- [ ] **Step 1: Write the failing tests**

```go
func TestGoldenValues(t *testing.T) // "values-repo": three values as in the spec's sketch (a secret, a str used by 5 repos, an int "overrides common.yaml") with the secret selected and its detail (required by three repos, Due "readable by removed sam (2026-10-01)"); "values-global" after "g"; "values-narrow" at 80×30
func TestRevealIsDroppedWhenAnythingMoves(t *testing.T) // for each of "j", "2" then "1", "g", the env change (e, type "prod", enter), and a run: press "r" on the secret (fakeValues.Reveal answers "s3cr3t-reveal"); view(m) holds it; after the move neither view(m) nor fmt.Sprintf("%+v", *m) holds it
func TestSetSecretTakesTheValueOnStdin(t *testing.T)     // on the secret: "s", type "n3w-s3cr3t", enter: the confirm screen shows "ezc set LLM_API_KEY" and not the value; the input shows no character of it; "y": Run args {"set","LLM_API_KEY"}, stdin "n3w-s3cr3t\n"
func TestSetForTheEnvironmentAndScope(t *testing.T)      // "S" in repo scope, env local: args {"set","BATCH_SIZE","--env","local"}; after "g", "s": args end "-g"
func TestUnsetFromTheFileThatSuppliesIt(t *testing.T)    // File "store/env.local.yaml" → {"unset","BATCH_SIZE","--env","local","-g"}; "repo/common.enc.yaml" → {"unset","LLM_API_KEY"}; a name with no value: status "NAME has no value here", no confirm
func TestScopeOutsideARepo(t *testing.T)                 // header Repo "": starts in store scope; "g" sets status "not in a repo: the store only" and List is called with StoreScope only
func TestPushAndPull(t *testing.T)                       // "P" confirms {"store","push"}; "p" confirms {"store","pull"}
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd cli && go test ./internal/tui/ -run 'Values|Reveal|Set|Unset|Scope|Push'`
Expected: FAIL.

- [ ] **Step 3: Implement `values.go`**

The revealed value lives in one field, `revealed struct{ name, value string }`, set by the `revealed` message only when its name is still selected, and zeroed by a single `forget()` that every selection, view, scope, environment and run change calls. The set input is a bubbles `textinput.Model` with `EchoMode = textinput.EchoPassword` for a secret; its value is copied into the confirm's stdin and the input is reset before the confirm screen opens.

- [ ] **Step 4: Run the tests, write the goldens**

Run: `cd cli && go test ./internal/tui -run Golden -update && go test ./internal/tui/`
Expected: PASS; no golden holds `s3cr3t`.

- [ ] **Step 5: Commit**

```bash
git add cli/internal/tui
git commit -m "feat(tui): the values view: set, unset, reveal, scope and environment"
```

### Task 4: The access view

**Files:**
- Create: `cli/internal/tui/access.go`, `cli/internal/tui/access_test.go`, goldens

**Interfaces:**
- Consumes: `Access.Load`, `confirm`.

Three panels: `recipients` (rows `name  kind  owner  added  by`, this machine's row ends `(this machine)`; a bot's repos in the detail), `requests` (`name  kind  host  requested`, an invalid one `not valid: <Problem>`), `rotation due` (`<Secret>  removed <Removed> on <On>`), and under them, when `Differ` is not empty, `✗ <files> not encrypted for exactly recipients.yaml. Run ezc store rewrap.` `tab` moves between recipients and requests. Hints: requests `a approve  d deny`; recipients `x remove  R rotate  b add bot`.

- [ ] **Step 1: Write the failing tests**

```go
func TestGoldenAccess(t *testing.T) // "access": gene (user, Me), gene-laptop (machine, owner gene), ci (bot, repos o/app), one valid request sam and one Problem "the key is not a valid key", two due entries; "access-hostile": a request whose Host is "h\x1b[2Jx" and Owner "a\nb": each stays on its row, rendered with "?"
func TestApproveShowsTheRequestAndPassesYes(t *testing.T) // on sam's request, "a": the confirm screen holds sam's whole key, host, requested and author; "y": args {"user","approve","sam","--yes"}
func TestApproveRefusesAnInvalidRequest(t *testing.T)    // on the Problem row, "a": status "this request is not valid: the key is not a valid key", no confirm
func TestRemoveByKind(t *testing.T)                      // gene-laptop "x" → {"user","remove","gene-laptop"}; ci "x" → {"bot","remove","ci"}
func TestRotateOnlyOwnOrBot(t *testing.T)                // gene (Me) "R" → {"user","rotate"}; ci "R" → {"bot","rotate","ci"}; gene-laptop "R": status "only this machine's key or a bot's can be rotated here", no confirm
func TestAddBot(t *testing.T)                            // "b", type "deploy", enter, type "o/app o/web", enter: {"bot","add","deploy","--repo","o/app","--repo","o/web"}; an empty repos answer: status "a bot needs at least one repo", no confirm
func TestApproveNeverAddsOtherYes(t *testing.T)          // across every action above, "--yes" appears only in the approve args
```

- [ ] **Step 2: Run them to see them fail** — `cd cli && go test ./internal/tui/ -run 'Access|Approve|Remove|Rotate|AddBot'`, FAIL.

- [ ] **Step 3: Implement `access.go`.**

- [ ] **Step 4: Run the tests, write the goldens** — `go test ./internal/tui -run Golden -update && go test ./internal/tui/`, PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(tui): the access view: requests, recipients, bots and rotation due"`

### Task 5: The repos and doctor views

**Files:**
- Create: `cli/internal/tui/repos.go`, `cli/internal/tui/doctor.go`, `cli/internal/tui/views_test.go`, goldens

**Interfaces:**
- Consumes: `Repos.Load(env)`, `Doctor.Check(env)` with the model's `env`.

Repos: one panel `repos ── env <env>` with a row per repo, `✓`/`✗` and `ID  checkout` (a repo with no checkout here: `-  ID  no checkout on this machine`); `enter` opens a panel `<ID>` with its `Report` lines, `esc` closes it. No repos: `No repos are registered. Run ezc repo add inside a repo.` Doctor: one panel per section, a line per item as `doctor.Report.String` lays it out (`✓`/`!`/`✗`, label, detail, and the fix under a Warn or Broken item). Neither view writes.

- [ ] **Step 1: Write the failing tests**

```go
func TestGoldenRepos(t *testing.T)  // "repos": o/app OK, o/bad failed with Report {"environment local: validation failed", "  ✗ API_URL is missing"}, o/web not here; "repos-detail" after "j", "enter"; "repos-empty"
func TestGoldenDoctor(t *testing.T) // "doctor": sections machine (OK), store (a Warn with fix "Run ezc store pull."), repo (a Broken recipients item, fix "Run ezc repo rewrap.")
func TestReposAndDoctorFollowTheEnvironment(t *testing.T) // after "e", "prod", enter: the next Repos.Load and Doctor.Check get "prod"
```

- [ ] **Step 2: Run them to see them fail**, **Step 3: implement**, **Step 4: goldens and PASS**, **Step 5: commit** `feat(tui): the repos and doctor views`.

### Task 6: The adapter and `ezc tui`

**Files:**
- Create: `cli/internal/cli/tui.go`, `cli/internal/cli/tui_test.go`
- Modify: `cli/internal/cli/cli.go` (`System.StdoutIsTerminal`, `System.RunTUI`, register `newTUICmd`), `cli/internal/cli/doctorcmd.go` (extract `doctorReport`), `cli/internal/cli/cli_test.go` (the fake's new fields), `cli/cmd/ezc/main.go`

**Interfaces:**
- Consumes: every `tui` type; `touchStore`, `load`, `manifestIfPresent`, `storeManifest`, `loadRepos`, `checkoutOf`, `loadManifest`, `registry.Uses`, `recipients.LoadDue`, `openAccess(sys, false)`, `pendingRequests`, `storeDiffers`, `checkOne`, `workingRepo`, `envName`, `gitstore.ShowURL`, `recipients.Printable`.
- Produces: `tuiDeps(sys System) tui.Deps`; `doctorReport(sys System, env, only string) (doctor.Report, error)` (the body of `newDoctorCmd`'s `RunE` up to `doctor.Collect`, which the command then prints); `newTUICmd(sys System) *cobra.Command`; `System.StdoutIsTerminal func() bool`, `System.RunTUI func(m tea.Model) error`.

Adapter types, each holding `sys` with `Stderr` discarded for reads: `tuiStore.Status` (from `touchStore`'s `storeSync`, `workingRepo(sys, "")` for `Repo`, `envName(sys, "")` for `Env`), `tuiValues.List` (the rows `ezc list` builds — `load`, the scope's manifest then the store's, `typeOf`, `fileOf`, `res.Overrides` — plus `Shown`, `Secret` as `ezc get` decides it, `Desc`, the repos from one pass over the registered checkouts' manifests with `registry.Uses`, and `Due` from `rotation-due.yaml` for a store layer's encrypted file), `tuiValues.Reveal` (`load` for the scope, then `res.Env[name]`; an unset name is an error), `tuiAccess.Load`, `tuiRepos.Load` (`checkOne` for each repo here, its report split into lines), `tuiDoctor.Check` (`doctorReport`), `tuiCommands.Run` (decision 1).

- [ ] **Step 1: Write the failing tests** in `tui_test.go` (fake `System`, real git as the other store tests use it)

```go
func TestTUICommandsRunInProcessWithoutATerminal(t *testing.T) // Run({"set","-g","TOKEN"}, "v4lue\n") on a store whose manifest declares TOKEN secret: OK, the store's file holds TOKEN, no Line holds "v4lue"; Run({"user","add","zed","fake1dd04"}, "") where it would also let in another name: OK false, a Line holds the command's --yes sentence, recipients.yaml unchanged
func TestTUIValuesList(t *testing.T)        // store + repo: rows match what `ezc list` prints for names, types and files; a secret has Shown "" and Secret true; a str built from a secret ("$TOKEN") is Secret; RequiredBy names a registered repo whose checkout's manifest requires the name; Due set for a store secret with an entry
func TestTUIValuesReveal(t *testing.T)      // Reveal(StoreScope, "TOKEN", "local") is the value; an unset name is an error naming it
func TestTUIStatus(t *testing.T)            // Behind 1 after a commit on the remote; Requests counts a pending request; outside a repo Repo ""; a store that is no git repo: zero counts, no error
func TestTUIAccessLoad(t *testing.T)        // recipients with Me on this machine's key; a request with a hostile host comes back through Printable; a store with no recipients.yaml: the error openAccess gives
func TestTUIReposLoad(t *testing.T)         // a registered repo here (OK), one failing, one with no checkout; no repos.yaml: empty, no error
func TestTUIDoctorCheckIsTheDoctorReport(t *testing.T) // the sections and items equal doctorReport's
func TestTUICommandNeedsATerminal(t *testing.T)        // `ezc tui` with StdinIsTerminal or StdoutIsTerminal false: UsageError "ezc tui needs a terminal. Use the commands instead; ezc help lists them."; with both true it calls RunTUI once with a *tui.Model; with -g the model starts in StoreScope
```

- [ ] **Step 2: Run them to see them fail** — `cd cli && go test ./internal/cli/ -run TUI`, FAIL.

- [ ] **Step 3: Implement**, extracting `doctorReport` first (`TestDoctor*` stay green).

- [ ] **Step 4: Run the gate** — `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops`, PASS; the sops grep and `go list -deps ./internal/tui` still clean.

- [ ] **Step 5: Commit** — `feat: ezc tui, and the adapter that runs the commands for it`

### Task 7: The setup wizard

**Files:**
- Create: `cli/internal/tui/wizard.go`, `cli/internal/tui/wizard_test.go`, goldens
- Modify: `cli/internal/cli/tui.go`, `cli/internal/cli/setupcmd.go`, `cli/internal/cli/tui_test.go`

**Interfaces:**
- Consumes: `Commands.Run`; Task 2's model.
- Produces: `NewWizard(d Deps, then bool) *Model` — the same `Model` in wizard mode; `then` true (from `ezc tui`) turns a successful run's `enter` into loading the values view, false (from `ezc setup`) quits on any key.

Steps and copy (decision 8): `Set up ezc on this machine` with `1 a new store on this machine`, `2 a new store on GitHub`, `3 join a store as a new person or machine`, `4 an existing store, with this machine's key`; inputs `The store's GitHub repo (owner/repo)` (2, 3) or `The store's repo (owner/repo, a URL or a path)` (4) and `Your name in the store (blank for the default)` (`Your name in the store` for 3); confirm with the command line and `i also install missing tools with brew` shown `[ ]`/`[x]` (not for 3). `esc` steps back; on the first step it quits.

- [ ] **Step 1: Write the failing tests**

```go
func TestGoldenWizard(t *testing.T)   // "wizard-choose", "wizard-confirm-github" (2, "o/store", "gene", "i"), "wizard-done"
func TestWizardCommandLines(t *testing.T) // 1 + blank name → {"setup","--new-local"}; 2 + "o/store" + "gene" + i → {"setup","--new-github","o/store","--name","gene","--yes"}; 3 + "o/store" + "sam" → {"join","o/store","--name","sam"}; 4 + "/srv/store" → {"setup","--store","/srv/store"}; 3 with a blank name: status "join needs your name in the store", no confirm
func TestWizardThen(t *testing.T)     // then=true: after OK and "enter" Values.List is called; then=false: any key returns tea.Quit
```
and in `cli/internal/cli/tui_test.go`:
```go
func TestTUIOpensTheWizardWithoutAStore(t *testing.T) // no store at the default location: RunTUI gets a model in wizard mode
func TestSetupOnATerminalOpensTheWizard(t *testing.T) // StdinIsTerminal and StdoutIsTerminal true, RunTUI set, no store, no flag: RunTUI called, nothing written; with --new-local, or with RunTUI nil, the existing path runs (TestSetup* stay green)
```

- [ ] **Step 2: Run them to see them fail**, **Step 3: implement**, **Step 4: goldens and the gate PASS**, **Step 5: commit** `feat(tui): the setup wizard, for ezc tui and ezc setup on a terminal`.

### Task 8: Docs and version 0.6.0

**Files:**
- Create: `docs/tui.md`
- Modify: `README.md`, `docs/reference.md`, `docs/setup.md`, `AGENTS.md`, `CLAUDE.md`, `cli/cmd/ezc/main.go`, `cli/cmd/ezc/store_test.go`, `cli/internal/cli/cli_test.go` (version strings)

- [ ] **Step 1:** `docs/tui.md` (`[← README](../README.md)` first): opening it, the four views and their keys, the confirm screen and that every change is a command shown first, what the TUI leaves to the CLI (decision 3: a change that also lets in another name, `repo rewrap`, `edit`, a new name), reveal and when it is dropped, the wizard. README: the command row `ezc tui`, the Documentation row `[The TUI](docs/tui.md)`, `This is ezc 0.6.` with the TUI sentence removed from the limits. `docs/reference.md`: `ezc tui [-g]`, and `ezc setup` on a terminal opens the wizard. `docs/setup.md`: the wizard. AGENTS.md: part 6 is built, `internal/tui` in the layout, `tui.md` in the docs list. CLAUDE.md sharp edges: `internal/tui` imports no ezc package; every TUI write is a command line through `tuiCommands.Run` with no terminal, and `--yes` only for `user approve` and the wizard's setup; `fit` cleans control characters; reveal is dropped by `forget()`; goldens are updated with `-update` and read before committing.
- [ ] **Step 2:** version `0.6.0` in the three files.
- [ ] **Step 3:** every relative link and anchor in the changed docs resolves; the gate, `schema:check` and both greps pass.
- [ ] **Step 4: Commit** — `feat: ezc 0.6.0 — the TUI`
