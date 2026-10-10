# ezc Part 1: Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A working `ezc` that composes a repo's environment from plaintext files in a global store and the repo, validates it against the repo's manifest, and exports it or runs a command with it.

**Architecture:** A Go module in `cli/` shaped like ipm's: `cmd/ezc/main.go` wires the process into `cli.Run`; every command is a `new<Name>Cmd(sys System)` in `internal/cli`; the commands call small packages (`paths`, `manifest`, `resolve`, `check`, `gen`). `manifest`, `resolve`, `check` and `gen` are ported from fs-config's `cli/internal/` and generalised: the resolver takes its inputs as an `Options` value and reads no process environment.

**Tech Stack:** Go 1.26, `github.com/spf13/cobra` v1.10.2, `gopkg.in/yaml.v3` v3.0.1. No other dependencies in this part.

**Spec:** `docs/superpowers/specs/2026-10-10-ezc-design.md` (sections Files, Resolution, Commands, Code structure, Errors, Testing; "Parts", item 1).

**Sources to port** (read-only; never modify them):

- fs-config: `/Users/thinkjones/dev/ao/fs-config/cli/internal/{manifest,resolve,check,gen}`
- ipm, the pattern reference: `/Users/thinkjones/dev/repos/__labs/ai-harness-skills/` (`cli/internal/cli/cli.go`, `cli/cmd/ipm/main.go`, `cli/internal/paths/paths.go`, `bin/ipm`, `.envrc`, `mise.toml`, `AGENTS.md`, `CLAUDE.md`)

## Global Constraints

- Module path is `github.com/thinkjones/easy-config/cli`; `go 1.26.0`; binary `ezc`; version constant `0.1.0`.
- Directory name is `.ezc`; environment prefix is `EZC_`. No string `fsconfig`, `fs-config`, `FS_CONFIG`, `Four Signals`, `thinkjones/fs-` or `ipm` may appear in any file under `cli/` (check with `grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/` returning nothing).
- Commands reach the outside world only through `System`. Packages under `internal/` other than `cli` never call `os.Getenv`, `os.UserHomeDir` or `os.Exit`.
- A malformed command line returns a `*UsageError` and exits 2; any other failure exits 1. Use `noArgs`, `exactArgs`, `minArgs`, `maxArgs` and `verbs`, never Cobra's own argument checks.
- No command prints a value except `ezc env export`, `ezc run` (to the child) and `ezc get`. Error messages and `--verbose` output carry names and paths only.
- No defaults for values, anywhere.
- Tests set `EZC_HOME` to an absolute temporary path and `HOME` to a temporary directory through the fake `System`; no test reads the real home or the real environment.
- Markdown prose is not hard-wrapped: one line per paragraph or list item.
- Comments follow the ported code's style where code is ported, and ipm's (one or two lines saying what a thing is) in new code. Drop comments that describe fs-config's history.
- The gate is `mise run cli:test` (gofmt check, `go vet ./...`, `go test ./...`). Every task ends with it passing.
- Commit messages are conventional (`feat(resolve): …`, `docs: …`) and end with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Encrypted files are out of scope: an existing `*.enc.yaml` is an error in this part (Task 4 fixes the message). Part 2 supplies decryption through `resolve.Options.Decrypt`.

## Decisions this plan makes beyond the spec

The spec leaves these open; they are fixed here so every task agrees.

1. **A missing store is not an error.** If the store's `.ezc` directory does not exist, the repo's files compose alone and `Result.StoreMissing` is true. `ezc env` says so on stderr. The spec lists using `.ezc/` "locally within a repo" as a use on its own.
2. **An environment exists when any of its four files exists**: `env.<name>.yaml` or `env.<name>.enc.yaml`, in the store or the repo. A named environment with none of them is an error listing the names that do exist. (The spec says "in the repo"; a repo that takes its per-environment values entirely from the store would otherwise be unable to name an environment.)
3. **With no environment named** (no argument, `EZC_ENV` and `APP_ENV` unset): `local` applies when it exists by rule 2; otherwise only the `common.*` files load and `Result.CommonOnly` is true.
4. **The selector is recorded as both `EZC_ENV` and `APP_ENV`** in the resolved environment (origin `shell`), unless `CommonOnly`. `ezc env export` omits both in every format; `ezc run` passes both to the child.
5. **An empty `required` map is a valid manifest**, so `ezc repo init` can write one. fs-config rejected it.
6. **`EZC_STORE` is the store's root** (the folder that contains `.ezc/`), default `$EZC_HOME/store`. `paths` is introduced here, not in part 3, because the resolver needs the default.
7. **`backend.Pair` is created now** (one type, one file) so `resolve.Options.Decrypt` has its final signature and part 2 changes no caller.
8. **The schema's published URL** is `https://raw.githubusercontent.com/thinkjones/easy-config/main/cli/schema/v1/manifest.schema.json`.
9. **User docs in this part are `README.md`, `AGENTS.md` and `CLAUDE.md`.** The Just the Docs site is part 7.

## File structure

```
.envrc                         PATH_add bin
.gitignore
LICENSE                        MIT, "Copyright (c) 2026 Gene Conroy-Jones"
README.md                      what ezc is, install, the part-1 commands
AGENTS.md                      router: where things are, conventions
CLAUDE.md                      commands and sharp edges
mise.toml                      cli:build, cli:test, schema:build, schema:check
bin/ezc                        shim: rebuild cli/bin/ezc when a Go source is newer, then exec it
cli/go.mod, cli/go.sum
cli/cmd/ezc/main.go            builds System, calls cli.Run, maps errors to exit codes
cli/cmd/ezc/main_test.go       builds the binary once; exit codes end to end
cli/internal/cli/cli.go        System, ExitError, UsageError, Run, newRootCmd, arg helpers
cli/internal/cli/scope.go      where the repo's .ezc and the store are; which environment
cli/internal/cli/env.go        ezc env, ezc env export, ezc run
cli/internal/cli/manifestcmd.go  ezc manifest validate|schema, ezc codegen python
cli/internal/cli/values.go     ezc list, ezc get
cli/internal/cli/repo.go       ezc repo init
cli/internal/cli/cli_test.go   every command, in-process, with a fake System
cli/internal/paths/paths.go    EZC_HOME and what is in it
cli/internal/backend/backend.go  type Pair (the rest arrives in part 2)
cli/internal/manifest/         manifest.go, load.go, validate.go, sort.go, jsonschema.go (ported)
cli/internal/resolve/file.go   one file: raw YAML scalars, home expansion, references
cli/internal/resolve/resolve.go  Options, Result, Load: which files, in what order
cli/internal/resolve/emit.go   Shell, Dotenv, JSON, ShellQuote
cli/internal/check/check.go    an environment against a manifest (ported)
cli/internal/gen/python.go     pydantic model from a manifest (ported)
cli/internal/gen/testdata/config.py.golden
cli/schema/v1/manifest.schema.json   generated
cli/share/share.go             //go:embed env.sh
cli/share/env.sh               the shim ezc repo init writes into a repo
```

## Review Focus

1. **A value containing shell metacharacters or a newline** (`'`, `$(...)`, backticks, `\n`) must come out of `ezc env export` in a form that `eval` (shell) and a dotenv reader take as inert data. Tests: Task 5.
2. **A typo in the environment name** (`ezc env prd`) must fail and list the valid names, never fall back to `local` or to common-only. Tests: Task 4.
3. **A repo with no store on the machine** must still resolve, and say the store is absent rather than failing or silently passing. Tests: Task 4 and Task 7.
4. **An `*.enc.yaml` present before part 2 exists** must be a clear error naming the file, not skipped; skipping would report a passing environment with its secrets missing. Tests: Task 4.
5. **Running a command from a subdirectory of the repo** must find the repo's `.ezc` in an ancestor, and running outside any repo must say so with the fix (`ezc repo init`). Tests: Task 7.

---

### Task 1: Scaffold, System and the root command

**Files:**
- Create: `.envrc`, `.gitignore`, `LICENSE`, `mise.toml`, `bin/ezc`, `README.md`, `AGENTS.md`, `CLAUDE.md`
- Create: `cli/go.mod`, `cli/cmd/ezc/main.go`, `cli/cmd/ezc/main_test.go`, `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`

**Interfaces:**
- Produces, in package `cli`:

```go
type System struct {
    Stdout, Stderr io.Writer
    Stdin          io.Reader
    Getenv         func(string) string
    // Environ is the process environment as "K=V" strings, for ezc run.
    Environ func() []string
    Getwd   func() (string, error)
    // GOOS is runtime.GOOS; tests fix it.
    GOOS string
    // RunChild runs name with args and env, wired to the terminal, and returns its exit code.
    RunChild func(name string, args, env []string) (int, error)
    Version  string
}
type ExitError struct{ Code int }   // Error() "exit status N"; ezc exits Code and prints nothing more
type UsageError struct{ msg string }
func usagef(format string, a ...any) error
func Run(args []string, sys System) error
func newRootCmd(sys System) *cobra.Command
func noArgs(cmd *cobra.Command, args []string) error
func exactArgs(n int, want string) cobra.PositionalArgs
func minArgs(n int, want string) cobra.PositionalArgs
func maxArgs(n int, want string) cobra.PositionalArgs
func verbs(cmd *cobra.Command, list string) *cobra.Command
func helpFor(cmd *cobra.Command) string
```

- Produces, in `cli_test.go`, the helper every later task's tests use:

```go
// fake is a System over temp directories. home is $HOME, ezcHome is $EZC_HOME, wd is the working directory.
type fake struct {
    out, errOut bytes.Buffer
    env         map[string]string
    wd          string
    children    [][]string // each RunChild call: name, args..., then "--env--", then the env
    childExit   int
}
func newFake(t *testing.T) *fake                  // HOME, EZC_HOME and wd are fresh t.TempDir()s; env has HOME and EZC_HOME
func (f *fake) sys() System
func (f *fake) run(args ...string) error          // Run(args, f.sys())
func write(t *testing.T, path, content string)    // MkdirAll + WriteFile
```

- [ ] **Step 1: Write the failing tests** in `cli/internal/cli/cli_test.go`:

```go
func TestVersion(t *testing.T) {
    f := newFake(t)
    if err := f.run("version"); err != nil { t.Fatal(err) }
    if got := f.out.String(); got != "ezc 0.1.0\n" { t.Fatalf("got %q", got) }
}

func TestUnknownCommandIsUsageError(t *testing.T) {
    f := newFake(t)
    err := f.run("exprot")
    var u *UsageError
    if !errors.As(err, &u) { t.Fatalf("want UsageError, got %v", err) }
    if !strings.Contains(err.Error(), `unknown command "exprot"`) || !strings.Contains(err.Error(), "ezc help") {
        t.Fatalf("got %q", err)
    }
}

func TestSingleDashLongFlagHint(t *testing.T) {
    f := newFake(t)
    err := f.run("version", "-verbose")
    var u *UsageError
    if !errors.As(err, &u) || !strings.Contains(err.Error(), "long flags take two dashes") {
        t.Fatalf("got %v", err)
    }
}

func TestHelpListsConfig(t *testing.T) {
    f := newFake(t)
    if err := f.run("help"); err != nil { t.Fatal(err) }
    for _, want := range []string{"EZC_HOME", "EZC_STORE", "EZC_ENV"} {
        if !strings.Contains(f.out.String(), want) { t.Errorf("help lacks %s", want) }
    }
}
```

The fake's `Version` is `"0.1.0"`.

- [ ] **Step 2: Run them and see them fail**

Run: `cd cli && go mod init github.com/thinkjones/easy-config/cli && go get github.com/spf13/cobra@v1.10.2 && go test ./internal/cli`
Expected: FAIL, `undefined: Run` (and the other names).

- [ ] **Step 3: Write `cli/internal/cli/cli.go`**

Copy ipm's `cli/internal/cli/cli.go` and change: the `System` struct to the one above; `ipm` to `ezc` in every string; the root `Short` to `ezc — easy config: one store for secrets and variables, layered into every repo`; the `Long` to that line, a blank line, `Compose a repo's environment from a global store and the repo's own .ezc/, validate it against a manifest, and export it or run a command with it.`, a blank line, and this block:

```
Config
  EZC_HOME    ezc's folder: its config and the global store (default ~/Library/ezc, ~/.local/share/ezc)
  EZC_STORE   the global store's folder, when it is not $EZC_HOME/store
  EZC_ENV     the environment to compose (then APP_ENV, then local)
```

Register only the `version` command for now. Add `maxArgs(n, want)` beside `minArgs`, with the same message shape (`"%s: give %s. Try `%s`."`).

- [ ] **Step 4: Write `cli/cmd/ezc/main.go`**

Follow ipm's `main.go`: `const version = "0.1.0"`, `run() int` builds `cli.System` and maps `nil` to 0, `*cli.ExitError` to its code with no message, `*cli.UsageError` to 2 after printing `ezc: <err>` to stderr, anything else to 1 after the same print. `RunChild` uses `exec.Command` with `Stdin`, `Stdout`, `Stderr` set to the process's and `Env` set to the given env; a `*exec.ExitError` returns its `ExitCode()` and a nil error; a start failure returns the error.

- [ ] **Step 5: Run the tests and see them pass**

Run: `cd cli && go test ./internal/cli`
Expected: PASS.

- [ ] **Step 6: Write the repo files**

- `bin/ezc`: ipm's `bin/ipm` with `ipm` replaced by `ezc` and the data comment changed to `ezc keeps its data in EZC_HOME, not in this checkout.` Make it executable.
- `.envrc`: ipm's, with `ezc`.
- `mise.toml`: tasks `cli:build` (`go build -o bin/ezc ./cmd/ezc`, `dir = "cli"`) and `cli:test` (ipm's three commands, unchanged).
- `.gitignore`: `/bin/` is NOT ignored (the shim is committed); ignore `.DS_Store`, `cli/bin/`, `.superpowers/`, `.claude/`, `.worktrees/`, `CLAUDE.local.md`, `.env`, `.env.*`, `docs/_site/`, `docs/.jekyll-cache/`, `docs/Gemfile.lock`.
- `LICENSE`: MIT, `Copyright (c) 2026 Gene Conroy-Jones`.
- `README.md`: one paragraph on what ezc is (from the spec's Purpose), the install line `go install github.com/thinkjones/easy-config/cli/cmd/ezc@latest`, a "Status" line saying part 1 of 7 is built and encrypted files arrive in part 2, and a commands table that later tasks extend (start it with `ezc version` and `ezc help [command]`).
- `AGENTS.md`: ipm's structure. "Where things are" table for `cli/`, `bin/ezc`, `docs/superpowers/` (historical; never rewritten to match the code); a "Conventions" list with the lines from Global Constraints that concern how to write code and docs.
- `CLAUDE.md`: a commands table (`direnv allow`, `mise run cli:build`, `mise run cli:test`) and a "Sharp edges" list with: commands reach the outside world only through `System`; tests never touch the real home; `docs/superpowers/` is history.

- [ ] **Step 7: Write the end-to-end test** in `cli/cmd/ezc/main_test.go`

`TestMain` builds the binary once into a temp dir with `go build -o`. Tests run it with `exec.Command` and a clean env (`HOME` and `EZC_HOME` set to temp dirs):

```go
func TestExitCodes(t *testing.T) {
    // args, want exit code, want stderr substring
    cases := []struct{ args []string; code int; stderr string }{
        {[]string{"version"}, 0, ""},
        {[]string{"nope"}, 2, `ezc: unknown command "nope"`},
        {[]string{"version", "extra"}, 2, "unexpected argument"},
    }
    ...
}
```

- [ ] **Step 8: Run the gate**

Run: `mise run cli:test`
Expected: gofmt prints nothing, vet passes, all tests PASS.

- [ ] **Step 9: Commit**

```bash
git add -A && git commit -m "feat: scaffold ezc with the root command"
```

---

### Task 2: paths

**Files:**
- Create: `cli/internal/paths/paths.go`, `cli/internal/paths/paths_test.go`

**Interfaces:**
- Produces:

```go
package paths
// Paths is where ezc keeps things on this machine.
type Paths struct {
    Home  string // EZC_HOME
    store string // EZC_STORE, "" for the default
}
func Resolve(getenv func(string) string, goos string) (Paths, error)
func (p Paths) Config() string    // <Home>/config.toml
func (p Paths) Store() string     // EZC_STORE, else <Home>/store
func (p Paths) StoreEzc() string  // <Store()>/.ezc
```

- [ ] **Step 1: Write the failing tests**

```go
func TestDefaults(t *testing.T) {
    cases := []struct{ goos string; env map[string]string; home string }{
        {"darwin", map[string]string{"HOME": "/u"}, "/u/Library/ezc"},
        {"linux", map[string]string{"HOME": "/u"}, "/u/.local/share/ezc"},
        {"linux", map[string]string{"HOME": "/u", "XDG_DATA_HOME": "/x"}, "/x/ezc"},
        {"darwin", map[string]string{"HOME": "/u", "XDG_DATA_HOME": "/x"}, "/x/ezc"},
        {"windows", map[string]string{"USERPROFILE": `C:\u`, "LOCALAPPDATA": `C:\l`}, filepath.Join(`C:\l`, "ezc")},
        {"linux", map[string]string{"HOME": "/u", "EZC_HOME": "/e"}, "/e"},
    }
    // each: Resolve(...).Home == home
}
func TestStore(t *testing.T) {
    // EZC_HOME=/e                → Store() "/e/store", StoreEzc() "/e/store/.ezc", Config() "/e/config.toml"
    // EZC_HOME=/e EZC_STORE=/s   → Store() "/s", StoreEzc() "/s/.ezc"
}
func TestRelativeOverridesRefused(t *testing.T) {
    // EZC_HOME=rel   → error containing "EZC_HOME must be an absolute path"
    // EZC_STORE=rel  → error containing "EZC_STORE must be an absolute path"
}
func TestNoHome(t *testing.T) {
    // no HOME, no EZC_HOME → error containing "set HOME (or EZC_HOME)"
    // no HOME, EZC_HOME=/e → no error
}
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/paths`. Expected: FAIL, `undefined: Resolve`.

- [ ] **Step 3: Implement `paths.go`.** Follow ipm's `paths.go` for the home rules (override must be absolute; `XDG_DATA_HOME` wins on every OS when set; darwin `~/Library/ezc`; windows `%LOCALAPPDATA%\ezc`, falling back to `%USERPROFILE%\AppData\Local\ezc`; otherwise `~/.local/share/ezc`). There is no cache directory.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./internal/paths`. Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(paths): EZC_HOME and the store's location"
```

---

### Task 3: manifest

**Files:**
- Create: `cli/internal/manifest/{manifest.go,load.go,validate.go,sort.go,jsonschema.go}`, `cli/internal/manifest/manifest_test.go`

**Interfaces:**
- Produces (unchanged from fs-config unless noted):

```go
package manifest
const SchemaVersion = "v1"
type VarType string  // TypeStr, TypeInt, TypeBool, TypeSecret, TypeURL, TypePath, TypeEnum
func AllTypes() []VarType
type Condition struct{ Var, Equals string }
type Var struct{ Type VarType; Desc string; Values []string; Min, Max *int; RequiredWhen *Condition }
type Manifest struct{ Schema, SchemaVersion string; Required, Optional map[string]Var }
type Entry struct{ Name string; Var Var }
func (m Manifest) All() []Entry
func (m Manifest) Lookup(name string) (Var, bool)   // NEW: required, then optional
func (v Var) IsSecret() bool
func Load(path string) (Manifest, error)
func (m Manifest) Validate() []error
func SortedNames(m map[string]Var) []string
func JSONSchema(schemaID string) ([]byte, error)
```

- [ ] **Step 1: Write the failing tests** in `manifest_test.go`. Each validation case is a manifest written as YAML in the test, loaded with `Load` from a temp file, and the joined `Validate()` errors checked for a substring:

| Test | Manifest | Expect |
| --- | --- | --- |
| `TestValidManifest` | the example in fs-config's `README.md` ("The manifest") | no errors; 2 required, 1 optional |
| `TestEmptyRequiredIsValid` | `schemaVersion: v1` and `required: {}` | no errors |
| `TestWrongSchemaVersion` | `schemaVersion: v2` | `schemaVersion "v2" — this ezc understands "v1"` |
| `TestBothRequiredAndOptional` | `A` in both | `A: declared both required and optional` |
| `TestEmptyDesc` | `desc: ""` | `A: empty desc` |
| `TestEnumNeedsValues` | `type: enum`, no values | `type enum requires a non-empty values list` |
| `TestValuesOnlyForEnum` | `type: str` with `values` | `values is only meaningful for type enum` |
| `TestBoundsOnlyForInt` | `type: str` with `min` | `min/max are only meaningful for type int` |
| `TestInvertedBounds` | `min: 5`, `max: 1` | `min 5 exceeds max 1` |
| `TestRequiredWhenOnRequired` | a required var with `requiredWhen` | `requiredWhen on an already-required variable` |
| `TestRequiredWhenUnknownVar` | `requiredWhen: {var: NOPE, equals: x}` | `requiredWhen names unknown variable "NOPE"` |
| `TestRequiredWhenNotInEnum` | target enum `[file, s3]`, `equals: flie` | `only permits [file s3]` |
| `TestUnknownFieldRejected` | a var with `default: x` | `Load` returns an error containing `default` |
| `TestSchemaKeyAccepted` | top-level `$schema: https://example/x.json` | `Load` succeeds |

Plus:

```go
func TestLookup(t *testing.T)      // finds a required name, an optional name; reports false for an absent one
func TestAllIsSorted(t *testing.T) // required names sorted, then optional names sorted
func TestJSONSchema(t *testing.T) {
    b, err := JSONSchema("https://example/s.json")
    // valid JSON; "$id" == "https://example/s.json"; "additionalProperties" false at the top level;
    // the type enum lists exactly AllTypes(); two calls return identical bytes
}
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/manifest`. Expected: FAIL (package has no files).

- [ ] **Step 3: Port the package.** Copy the five files from `/Users/thinkjones/dev/ao/fs-config/cli/internal/manifest/`. Then:
  - `validate.go`: delete the `len(m.Required) == 0` rule; change `this fsconfig understands` to `this ezc understands`.
  - `manifest.go`: add `Lookup`; rewrite the package comment and the `Var.Desc` comment so neither mentions Four Signals or `check-env.sh` (`Desc` is "prose for humans; the generators emit it as the docstring on the generated field").
  - `jsonschema.go`: if the schema marks `required` as needing at least one property (`minProperties`), remove that; keep the `required` key itself mandatory.
  - Apply the name rule from Global Constraints to every comment and string.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./internal/manifest`. Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(manifest): the manifest types, loader, validator and JSON Schema"
```

---

### Task 4: resolve — files and composition

**Files:**
- Create: `cli/internal/backend/backend.go`, `cli/internal/resolve/file.go`, `cli/internal/resolve/resolve.go`, `cli/internal/resolve/file_test.go`, `cli/internal/resolve/resolve_test.go`

**Interfaces:**
- Produces:

```go
package backend
// Pair is one name and its value, in the order its file declares it.
type Pair struct{ Name, Value string }
```

```go
package resolve
const DefaultEnv = "local"
// SelectorVars are the variables that name the environment. They are recorded
// in a Result and never exported.
var SelectorVars = []string{"EZC_ENV", "APP_ENV"}
const ShellOrigin = "shell"

type Options struct {
    StoreDir string // the store's .ezc directory; "" or a directory that does not exist means no store
    RepoDir  string // the repo's .ezc directory; "" means none (the store alone)
    Env      string // the environment named; "" when none was
    Home     string // the user's home, for ~ and $HOME; "" when there is none
    // Decrypt reads an *.enc.yaml. nil means this build cannot.
    Decrypt func(path string) ([]backend.Pair, error)
}
type Source struct {
    Path    string
    Layer   string   // "store" or "repo"
    Present bool     // the file exists
    Vars    []string // names it supplied, in file order
}
type Result struct {
    Env          map[string]string
    Origin       map[string]string // the last file to set each name, or ShellOrigin
    Sources      []Source          // every candidate file, in precedence order
    EnvName      string            // "" when CommonOnly
    CommonOnly   bool
    StoreMissing bool
}
func Load(o Options) (Result, error)
func (r Result) Names() []string                 // sorted
func (r Result) EnvLabel() string                // EnvName, or "(none)" when CommonOnly
func (r Result) Overrides(name string) []string  // paths of earlier Sources that also set name, in order
func Envs(dirs ...string) []string               // sorted, de-duplicated environment names found in dirs
```

- Candidate files, in order, for environment `E`: store `common.yaml`, `common.enc.yaml`, `env.E.yaml`, `env.E.enc.yaml`; then the same four in the repo. When `CommonOnly`, only the four `common.*` files are candidates. Every candidate is optional.

- [ ] **Step 1: Write the failing tests for one file** in `file_test.go` (package `resolve`, testing the unexported loader `loadFile(path, home string, decrypt func(string) ([]backend.Pair, error)) ([]backend.Pair, error)`):

| Test | File content | Expect |
| --- | --- | --- |
| `TestRawScalars` | `ZIP: 08540`, `VER: 1.20`, `BIG: 1e5`, `PORT: "8080"`, `ON: yes` | values exactly `08540`, `1.20`, `1e5`, `8080`, `yes`, in that order |
| `TestEmptyFile` | empty, and comments only | no pairs, no error |
| `TestNestedRejected` | `A: {b: 1}` | error containing `"A" is a nested block` |
| `TestListRejected` | `A: [1, 2]` | error containing `"A" is a list` |
| `TestAliasRejected` | `A: &x v` / `B: *x` | error containing `"B" is an anchor reference` |
| `TestNullRejected` | `A:` and `A: ~` | error containing `"A" has no value` |
| `TestNotAMapping` | `- a` | error containing `expected a flat mapping of NAME: value` |
| `TestHomeExpansion` | `A: ~/x`, `B: $HOME/y`, `C: ${HOME}/z`, `D: "~"`, `E: ~other/x`, `F: a/~/b` with home `/h` | `/h/x`, `/h/y`, `/h/z`, `/h`, `~other/x`, `a/~/b` |
| `TestHomeExpansionWithoutHome` | `A: ~/x`, home `""` | error containing `A starts with a home reference` |
| `TestEncryptedWithoutDecrypter` | path `common.enc.yaml`, decrypt nil | error containing the path and `is encrypted, and this version of ezc cannot decrypt` |
| `TestEncryptedUsesDecrypter` | path `common.enc.yaml`, decrypt returns `{K, ~/k}` with home `/h` | `K` = `/h/k` |

- [ ] **Step 2: Write the failing tests for composition** in `resolve_test.go`. A helper `tree(t, map[string]string) (storeDir, repoDir string)` writes files under two temp `.ezc` directories from keys like `store/common.yaml` and `repo/env.local.yaml`.

```go
func TestPrecedence(t *testing.T) {
    // store/common.yaml  A: s-common   B: s-common   C: s-common   D: s-common
    // store/env.local.yaml             B: s-env      C: s-env      D: s-env
    // repo/common.yaml                               C: r-common   D: r-common
    // repo/env.local.yaml                                          D: r-env
    // Load(Env "local") → A=s-common B=s-env C=r-common D=r-env
    // Origin[D] is repo/env.local.yaml; Overrides("D") is the other three paths in load order
    // len(Sources) == 8; the four *.enc.yaml sources have Present false
}
func TestSelectorRecorded(t *testing.T) {
    // repo/env.prod.yaml exists; Load(Env "prod") → Env["EZC_ENV"] == "prod", Env["APP_ENV"] == "prod",
    // Origin of both == ShellOrigin, EnvName "prod", EnvLabel() "prod"
}
func TestUnknownEnvironmentListsValid(t *testing.T) {
    // repo/env.local.yaml, repo/env.prod.yaml, store/env.staging.yaml; Load(Env "prd") →
    // error containing `no environment "prd"` and `Valid: local, prod, staging`
}
func TestEnvironmentOnlyInStore(t *testing.T) {
    // store/env.prod.yaml only, repo/common.yaml; Load(Env "prod") succeeds
}
func TestNoEnvNamedDefaultsToLocal(t *testing.T) {
    // repo/env.local.yaml; Load(Env "") → EnvName "local", CommonOnly false
}
func TestNoEnvNamedAndNoLocalIsCommonOnly(t *testing.T) {
    // store/common.yaml A: 1, repo/env.prod.yaml B: 2; Load(Env "") →
    // CommonOnly true, EnvName "", EnvLabel() "(none)", Env == {A: 1} (no B, no EZC_ENV, no APP_ENV), len(Sources) == 4
}
func TestStoreMissing(t *testing.T) {
    // StoreDir is a path that does not exist, repo/env.local.yaml A: 1 → no error, StoreMissing true, A=1
    // StoreDir "" behaves the same
}
func TestNoRepoDir(t *testing.T) {
    // RepoDir "", store/common.yaml A: 1 → CommonOnly true, A=1, every Source has Layer "store"
}
func TestReferences(t *testing.T) {
    // store/common.yaml  ROOT: /r
    // repo/common.yaml   A: $ROOT/a   B: ${A}/b   C: cost $$5   D: trailing $   E: $HOME/e
    // Home "/h" → A=/r/a B=/r/a/b C="cost $5" D="trailing $" E=/h/e
}
func TestForwardReferenceIsError(t *testing.T) {
    // repo/env.local.yaml  A: $B/x  then  B: y  → error containing "A references $B, which is not defined"
    // and "Write $$B if you meant a literal dollar sign"
}
func TestUnknownReferenceNamesScope(t *testing.T) {
    // store/common.yaml ROOT: /r ; repo/env.local.yaml A: $ROOTT → error containing "defined so far: ROOT"
}
func TestEncryptedValuesAreNotSubstituted(t *testing.T) {
    // repo/common.enc.yaml via Decrypt returning {TOKEN, "a$ROOT$$b"}; store/common.yaml ROOT: /r → TOKEN == "a$ROOT$$b"
}
func TestEncryptedFilePresentWithoutDecrypter(t *testing.T) {
    // repo/common.enc.yaml exists, Decrypt nil → Load errors, naming the file
}
func TestEnvs(t *testing.T) {
    // dirs holding env.local.yaml, env.prod.enc.yaml, env.prod.yaml, common.yaml → ["local", "prod"]
}
```

- [ ] **Step 3: Run and see them fail.** Run: `cd cli && go test ./internal/resolve`. Expected: FAIL (no package files).

- [ ] **Step 4: Write `backend/backend.go`** with the `Pair` type and a package comment: `Package backend is the encryption backend ezc stores secrets with. Part 1 holds only Pair.`

- [ ] **Step 5: Write `resolve/file.go`.** Port from fs-config's `resolve.go`: `loadYAML`, `expandHome`, `expandRefs`, `parseRef`, `isRefChar`, `lookupRef`, `unknownRefError`, `sortedKeys`, and a `loadFile` that stats, dispatches on the `.enc.yaml` suffix to `decrypt` (the option) or `loadYAML`, then applies `expandHome`. Changes: `kv` becomes `backend.Pair`; `home` is a parameter everywhere `os.UserHomeDir` was called (`lookupRef` takes it too); `expandRefs` loses its `strict` parameter and is always strict; a missing file returns `(nil, nil)` plus a way for the caller to know it was absent (return a third `present bool`); the nil-decrypter error is ``fmt.Errorf("%s is encrypted, and this version of ezc cannot decrypt it yet", path)``. Do not port `decrypt`, `flattenDecrypted`, `coercedError`, `Values`, `TryValues`, `Probe`, `StorePath` or `missingFileError`.

- [ ] **Step 6: Write `resolve/resolve.go`** with the types above and `Load`:

```
storeDir present?  (o.StoreDir != "" and it is a directory)  → else StoreMissing
envs := Envs(storeDir if present, o.RepoDir if set)
name := o.Env
if name == "":
    if DefaultEnv in envs: name = DefaultEnv
    else: CommonOnly = true
else if name not in envs:
    error: no environment %q: no env.%s.yaml or env.%s.enc.yaml in <the dirs searched, joined with " or ">.
           \n       Valid: a, b, c        (or "No environment files found." when envs is empty)
build the candidate list (store's only when present, repo's only when RepoDir set)
for each candidate, in order: loadFile; record Source; for each pair:
    plaintext file → expandRefs against Env so far;  encrypted file → value as is
    Env[name] = value; Origin[name] = path; append to Source.Vars
unless CommonOnly: for each of SelectorVars: Env[v] = name; Origin[v] = ShellOrigin
```

Keep the comments from fs-config's `Load` that explain why composition is one-directional and why the selector is recorded but not exported, reworded for the new names.

- [ ] **Step 7: Run and see them pass.** Run: `cd cli && go test ./internal/resolve ./internal/backend`. Expected: PASS.

- [ ] **Step 8: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(resolve): compose the store and a repo, plaintext files"
```

---

### Task 5: resolve — emitting

**Files:**
- Create: `cli/internal/resolve/emit.go`, `cli/internal/resolve/emit_test.go`

**Interfaces:**
- Consumes: `Result`, `SelectorVars` from Task 4.
- Produces:

```go
func ShellQuote(s string) string         // 'single quoted', with ' as '\''
func (r Result) Shell() string           // one "export NAME='value'\n" per name, sorted, SelectorVars omitted
func (r Result) Dotenv() string          // one NAME="value"\n per name, sorted, SelectorVars omitted
func (r Result) JSON() ([]byte, error)   // one object, keys sorted, two-space indent, trailing newline, SelectorVars omitted
func (r Result) Exported() []string      // Names() without SelectorVars
```

Dotenv quoting: the value in double quotes, with `\` as `\\`, `"` as `\"`, `$` as `\$`, a newline as `\n` and a carriage return as `\r`.

- [ ] **Step 1: Write the failing tests**

```go
var nasty = map[string]string{
    "PLAIN":   "v",
    "QUOTE":   `it's`,
    "CMD":     "$(rm -rf /)`id`",
    "MULTI":   "a\nb",
    "DQ":      `say "hi" \ $X`,
    "EZC_ENV": "local",
    "APP_ENV": "local",
}

func TestShell(t *testing.T) {
    // Result{Env: nasty}.Shell() ==
    // "export CMD='$(rm -rf /)`id`'\nexport DQ='say \"hi\" \\ $X'\nexport MULTI='a\nb'\nexport PLAIN='v'\nexport QUOTE='it'\\''s'\n"
}
func TestShellRoundTripsThroughSh(t *testing.T) {
    // skip when /bin/sh is absent. For each exported name: run
    //   sh -c 'eval "$1"; printf %s "$'NAME'"' sh "<Shell() output>"
    // and compare stdout with the value byte for byte. A file named by the CMD value must not be touched.
}
func TestDotenv(t *testing.T) {
    // lines, sorted: CMD="\$(rm -rf /)`id`"  DQ="say \"hi\" \\ \$X"  MULTI="a\nb"  PLAIN="v"  QUOTE="it's"
    // no EZC_ENV or APP_ENV line
}
func TestJSON(t *testing.T) {
    // unmarshals to the five non-selector pairs; bytes end in "}\n"; two calls are identical
}
func TestEmptyResult(t *testing.T) {
    // Shell() == "", Dotenv() == "", JSON() == "{}\n"
}
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/resolve -run 'Shell|Dotenv|JSON|EmptyResult'`. Expected: FAIL, `undefined`.

- [ ] **Step 3: Implement `emit.go`.**

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./internal/resolve`. Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(resolve): shell, dotenv and JSON output"
```

---

### Task 6: check and gen

**Files:**
- Create: `cli/internal/check/check.go`, `cli/internal/check/check_test.go`, `cli/internal/gen/python.go`, `cli/internal/gen/python_test.go`, `cli/internal/gen/testdata/config.py.golden`

**Interfaces:**
- Consumes: `manifest.Manifest`, `manifest.Var`, `resolve.Result` (with `EnvLabel`, `Exported`, `Overrides`, `Sources`, `Origin`).
- Produces:

```go
package check
type Problem struct{ Name, Desc, Why string }
func (p Problem) String() string
func Run(m manifest.Manifest, res resolve.Result) []Problem
func ValidateValue(v manifest.Var, value string) string   // "" when valid
func IsPlaceholder(value string) bool
func PlaceholderWords() []string                           // sorted
func Report(problems []Problem, res resolve.Result) string
func ReportOK(res resolve.Result) string
func ReportVerbose(res resolve.Result, home string) string
```

```go
package gen
func Python(m manifest.Manifest, manifestPath string) ([]byte, error)
```

- Report formats (exact):
  - `Report`: first line `environment <label>: validation failed`, then one line per problem indented two spaces, a blank line, `Files, in precedence order:`, then per Source `  <marker> <path> (<n> vars)` where the marker is `-` when the file is absent or supplied nothing and a space otherwise.
  - `ReportOK`: `environment <label> ✓ <n> var(s) from <m> file(s)\n`, counting `Exported()` names and Sources with at least one var, with fs-config's `plural`.
  - `ReportVerbose`: fs-config's layout, paths shortened with `~` using the `home` argument, last line `Names and paths only — no values. Use `ezc env export` to emit them.`

- [ ] **Step 1: Write the failing check tests.** A helper builds a `resolve.Result` from a `map[string]string` and a manifest from YAML.

| Test | Manifest var | Env value | Expect |
| --- | --- | --- | --- |
| `TestRequiredMissing` | required `A` str | absent | one Problem, Why `not set` |
| `TestRequiredEmpty` | required `A` str | `"  "` | Why `set but empty` |
| `TestPlaceholder` | required `A` str | `Replace-Me` | Why `still the placeholder "Replace-Me"` |
| `TestOptionalMissingIsFine` | optional `A` | absent | no problems |
| `TestRequiredWhenMet` | optional `P` with `requiredWhen {var: OUT, equals: file}`, required enum `OUT` | `OUT=file` | Why `required because OUT=file` |
| `TestRequiredWhenNotMet` | same | `OUT=s3` | no problems |
| `TestIntNotInteger` | required int | `x` | `not an integer: "x"` |
| `TestIntBelowMin` | int `min: 1` | `0` | `0 is below the minimum of 1` |
| `TestIntAboveMax` | int `max: 100` | `101` | `101 is above the maximum of 100` |
| `TestBool` | bool | each of `1 0 true false yes no TRUE` passes; `maybe` gives `not a boolean: "maybe"` | |
| `TestEnum` | enum `[file, s3]` | `slack` | `"slack" is not one of: file, s3` |
| `TestURL` | url | `ftp://x` | `not a URL: "ftp://x"`; `https://x` passes |
| `TestProblemsSortedAndComplete` | three failing vars `C`, `A`, `B` | | three problems in order `A`, `B`, `C` |
| `TestExtraValuesIgnored` | manifest declares `A` only | `A=1`, `Z=2` | no problems |

```go
func TestReportOK(t *testing.T) {
    // Env {A:1, B:2, EZC_ENV:local, APP_ENV:local}, EnvName local, two Sources with vars →
    // "environment local ✓ 2 vars from 2 files\n"
}
func TestReportNamesEveryProblemAndFile(t *testing.T) // starts "environment local: validation failed\n"; contains each Name and each Source path
func TestReportVerboseHasNoValues(t *testing.T) {
    // Env {SECRET: "hunter2-value"} → output contains "SECRET" and its file, shortened with ~, and does not contain "hunter2-value";
    // a name set in two files shows "(overrides common.yaml)"
}
```

- [ ] **Step 2: Write the failing gen test**

```go
var update = flag.Bool("update", false, "rewrite golden files")

func TestPythonGolden(t *testing.T) {
    // manifest: required LLM_API_KEY secret, BATCH_SIZE int min 1 max 100, DIGEST_OUTPUT enum [file, s3], SITE url;
    //           optional DIGEST_OUTPUT_PATH path requiredWhen DIGEST_OUTPUT=file, DEBUG bool
    // Python(m, ".ezc/manifest.yaml") equals testdata/config.py.golden (written when -update)
}
func TestPythonHeader(t *testing.T) {
    // first line "# GENERATED by ezc from .ezc/manifest.yaml — DO NOT EDIT."
    // contains "# Regenerate:  ezc codegen python -o <this file>"
    // contains no "fsconfig", "mise"
}
func TestPythonIsDeterministic(t *testing.T) // two calls, identical bytes
```

- [ ] **Step 3: Run and see them fail.** Run: `cd cli && go test ./internal/check ./internal/gen`. Expected: FAIL (no package files).

- [ ] **Step 4: Port `check.go`** from `/Users/thinkjones/dev/ao/fs-config/cli/internal/check/check.go`. Changes: the three report functions take the signatures and formats above; `ReportOK` counts `res.Exported()`; `ReportVerbose` uses `res.Overrides(name)` and `filepath.Base` for the overridden files, and treats `resolve.ShellOrigin` as an origin with no path; `tilde` takes `home`; `Run`, `ValidateValue`, `IsPlaceholder`, `PlaceholderWords`, `plural` are unchanged apart from the name rule.

- [ ] **Step 5: Port `python.go`** from `/Users/thinkjones/dev/ao/fs-config/cli/internal/gen/python.go`. Changes: the header's first line and `Regenerate` line as in `TestPythonHeader`; `Mirrors fsconfig's own validator` becomes `Mirrors ezc's own validator`; the name rule everywhere else. Then run `go test ./internal/gen -run Golden -update` and read the golden file through once: it must be valid Python with one field per manifest entry.

- [ ] **Step 6: Run and see them pass.** Run: `cd cli && go test ./internal/check ./internal/gen`. Expected: PASS.

- [ ] **Step 7: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(check,gen): validate an environment and generate Python config"
```

---

### Task 7: `ezc env`, `ezc env export`, `ezc run`

**Files:**
- Create: `cli/internal/cli/scope.go`, `cli/internal/cli/env.go`
- Modify: `cli/internal/cli/cli.go` (register `newEnvCmd`, `newRunCmd`), `cli/internal/cli/cli_test.go`, `README.md`

**Interfaces:**
- Consumes: `paths.Resolve`, `manifest.Load`, `Manifest.Validate`, `resolve.Load`, `resolve.Options`, `Result.Shell|Dotenv|JSON|Exported`, `check.Run|Report|ReportOK|ReportVerbose`.
- Produces, in `scope.go`:

```go
const ezcDir = ".ezc"
const manifestName = "manifest.yaml"

// repoDir is the .ezc directory of the nearest ancestor of the working directory that has
// one with a manifest.yaml; "" when there is none.
func repoDir(sys System) (string, error)
// requireRepoDir is repoDir, or an error:
//   not in an ezc repo: no .ezc/manifest.yaml here or in any parent folder. Run `ezc repo init` to create one.
func requireRepoDir(sys System) (string, error)
// envName is the first of: arg, $EZC_ENV, $APP_ENV. "" when none is set.
func envName(sys System, arg string) string
// load resolves the environment for repo (a .ezc directory, or "" for the store alone).
func load(sys System, repo, env string) (resolve.Result, error)
// loadManifest reads and validates a manifest; an invalid one is an error listing every problem,
// one per line, under "<path> is invalid:".
func loadManifest(path string) (manifest.Manifest, error)
// warn prints the stderr notes a Result carries (see below).
func warn(sys System, res resolve.Result, repo string)
```

`load` passes `Home: sys.Getenv("HOME")` (`USERPROFILE` when `sys.GOOS` is `windows`), `StoreDir: paths.StoreEzc()`, `Decrypt: nil`.

`warn` writes, when they apply:
- `! no store at <paths.Store()> — only this repo's values were loaded. Run `ezc setup` to create one.` (`StoreMissing`)
- `! no environment named and no env.local.yaml — only the common files were loaded. Set EZC_ENV or pass a name.` (`CommonOnly` and a repo is in scope)

- Command behaviour:

| Command | Behaviour |
| --- | --- |
| `ezc env [ENV] [-m FILE] [--verbose]` | `-m` defaults to the repo's manifest; with `-m`, the repo directory is the manifest's directory. Loads and validates the manifest, resolves, `warn`s, runs `check.Run`. Problems: `check.Report` to stderr and `&ExitError{1}`. Otherwise `ReportOK` (or `ReportVerbose` with `--verbose`) to stderr. Nothing on stdout, ever. |
| `ezc env export [ENV] [--format shell\|dotenv\|json]` | Resolves (a repo is not required: outside one it exports the store alone) and writes the chosen format to stdout. No `warn`, nothing on stderr on success. On a resolve error: nothing on stdout, the error returned. An unknown format is a `UsageError`: `ezc env export: unknown format "x" — use shell, dotenv or json.` |
| `ezc run [ENV] -- <command> [args…]` | Requires `--` and at least one word after it, else `UsageError` `ezc run: give a command after --. Try `ezc help run`.` At most one argument before `--`. Resolves, `warn`s, then `sys.RunChild(command, args, env)` where env is `sys.Environ()` with every resolved name (selector variables included) replaced or appended. A non-zero child exit returns `&ExitError{code}`. |

`env` is a parent with one child (`export`) that also runs on its own with an optional positional; build it with Cobra directly (`Args: maxArgs(1, "at most one environment name")`), not with `verbs`. Because `export` is a subcommand, an environment literally named `export` cannot be passed positionally to `ezc env`; say so in the command's help.

- [ ] **Step 1: Write the failing tests** in `cli_test.go`. A helper `repo(t, f, files map[string]string)` writes files under `f.wd/.ezc/`, and `store(t, f, files)` under `$EZC_HOME/store/.ezc/`. `m1` is this manifest:

```yaml
schemaVersion: v1
required:
  NAME: {type: str, desc: who}
  SIZE: {type: int, min: 1, max: 9, desc: how many}
```

```go
func TestEnvPasses(t *testing.T) {
    // store common.yaml "NAME: n"; repo manifest m1, env.local.yaml "SIZE: 3"
    // run("env") → nil; stdout empty; stderr == "environment local ✓ 2 vars from 2 files\n"
}
func TestEnvFailsListsEveryProblem(t *testing.T) {
    // repo manifest m1, env.local.yaml "SIZE: 12" (no NAME); store present but empty
    // run("env") → ExitError{1}; stderr contains "NAME — who", "not set", "SIZE — how many", "12 is above the maximum of 9"; stdout empty
}
func TestEnvNamedEnvironmentAndTypo(t *testing.T) {
    // env.local.yaml, env.prod.yaml → run("env", "prod") passes and stderr starts "environment prod ✓"
    // run("env", "prd") → error containing `no environment "prd"` and "Valid: local, prod"
}
func TestEnvFromEZC_ENVThenAPP_ENV(t *testing.T) {
    // f.env["APP_ENV"]="prod" → "environment prod"; adding f.env["EZC_ENV"]="local" → "environment local";
    // an argument beats both
}
func TestEnvFromSubdirectory(t *testing.T) {
    // f.wd = <repo>/a/b (created) → run("env") passes
}
func TestEnvOutsideARepo(t *testing.T) {
    // no .ezc anywhere → error containing "not in an ezc repo" and "ezc repo init"
}
func TestEnvWarnsWhenNoStore(t *testing.T) {
    // no store directory → run("env") passes; stderr contains "! no store at " and "ezc setup"
}
func TestEnvVerboseShowsNoValues(t *testing.T) {
    // NAME: topsecretvalue → run("env", "--verbose"); stderr contains "NAME" and "Where each value came from"; not "topsecretvalue"
}
func TestEnvInvalidManifest(t *testing.T) {
    // manifest with schemaVersion v2 → error containing "manifest.yaml is invalid:" and "this ezc understands"
}
func TestEnvManifestFlag(t *testing.T) {
    // a second repo at <tmp>/other/.ezc; from f.wd, run("env", "-m", "<tmp>/other/.ezc/manifest.yaml") validates that repo's values
}
func TestExportShell(t *testing.T) {
    // run("env", "export") → stdout == "export NAME='n'\nexport SIZE='3'\n"; stderr empty
}
func TestExportFormats(t *testing.T) {
    // --format dotenv → "NAME=\"n\"\nSIZE=\"3\"\n";  --format json → "{\n  \"NAME\": \"n\",\n  \"SIZE\": \"3\"\n}\n"
    // --format xml → UsageError containing `unknown format "xml"`
}
func TestExportIsSilentWithoutAStore(t *testing.T) {
    // no store → run("env", "export") succeeds; stderr empty
}
func TestExportPrintsNothingOnFailure(t *testing.T) {
    // env.local.yaml "A: $NOPE" → error; stdout empty
}
func TestExportOutsideARepoExportsTheStore(t *testing.T) {
    // no repo; store common.yaml "NAME: n" → stdout == "export NAME='n'\n"
}
func TestRunPassesEnvironment(t *testing.T) {
    // f.env has PATH=/bin; run("run", "prod", "--", "printenv", "NAME") →
    // f.children[0] begins ["printenv", "NAME"]; its env contains "NAME=n", "SIZE=3", "EZC_ENV=prod", "APP_ENV=prod", "PATH=/bin"
    // a resolved name already in Environ appears once, with the resolved value
}
func TestRunReturnsChildExitCode(t *testing.T) {
    // f.childExit = 7 → ExitError{7}
}
func TestRunNeedsACommand(t *testing.T) {
    // run("run") and run("run", "--") → UsageError containing "give a command after --"
}
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/cli`. Expected: FAIL, unknown command `env`.

- [ ] **Step 3: Implement `scope.go` and `env.go`, and register the commands.** For `ezc run`, read the position of `--` from `cmd.ArgsLenAtDash()`.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./internal/cli`. Expected: PASS.

- [ ] **Step 5: Add an end-to-end case** to `cmd/ezc/main_test.go`: a temp repo with `m1` and a failing `env.local.yaml`; `ezc env` exits 1 with empty stdout; `ezc run -- sh -c 'exit 3'` exits 3; `ezc run -- sh -c 'printf %s "$SIZE"'` prints the resolved value.

- [ ] **Step 6: Document.** Add `ezc env`, `ezc env export` and `ezc run` to the README's commands table, and a "How a repo loads its environment" section with the three ways in from the spec (the `.ezc/env.sh` line is added in Task 10).

- [ ] **Step 7: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(cli): env, env export and run"
```

---

### Task 8: `ezc manifest`, `ezc codegen python` and the published schema

**Files:**
- Create: `cli/internal/cli/manifestcmd.go`, `cli/schema/v1/manifest.schema.json`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `mise.toml`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: `loadManifest`, `requireRepoDir` (Task 7); `manifest.JSONSchema`; `gen.Python`.
- Produces:

```go
const schemaID = "https://raw.githubusercontent.com/thinkjones/easy-config/main/cli/schema/" + manifest.SchemaVersion + "/manifest.schema.json"
// writeOut writes b to path, creating its folder, and says "wrote <path>" on stderr; "" writes to stdout.
func writeOut(sys System, path string, b []byte) error
```

| Command | Behaviour |
| --- | --- |
| `ezc manifest validate [-m FILE]` | Prints `<path> ok — <r> required, <o> optional (schema v1)` to stdout, or the invalid-manifest error. |
| `ezc manifest schema [-o FILE]` | Writes `manifest.JSONSchema(schemaID)`. |
| `ezc codegen python [-m FILE] [-o FILE]` | Validates the manifest, then writes `gen.Python`. An invalid manifest generates nothing. |

`manifest` and `codegen` are `verbs` parents (`validate or schema`; `python`).

- [ ] **Step 1: Write the failing tests**

```go
func TestManifestValidate(t *testing.T)        // m1 → stdout ends "manifest.yaml ok — 2 required, 0 optional (schema v1)\n"
func TestManifestValidateInvalid(t *testing.T) // error lists every problem; stdout empty
func TestManifestSchemaStdout(t *testing.T)    // stdout is JSON whose "$id" is schemaID
func TestManifestSchemaToFile(t *testing.T)    // -o <tmp>/a/b/s.json creates the folders; stderr "wrote <path>\n"; stdout empty
func TestManifestNeedsAVerb(t *testing.T)      // run("manifest") → UsageError containing "say validate or schema"
func TestCodegenPython(t *testing.T)           // stdout starts "# GENERATED by ezc"
func TestCodegenRefusesInvalidManifest(t *testing.T) // error; stdout empty; -o file not created
func TestCodegenUnknownLanguage(t *testing.T)  // run("codegen", "rust") → UsageError containing `unknown action "rust"`
func TestCommittedSchemaIsCurrent(t *testing.T) {
    // reads ../../schema/v1/manifest.schema.json and compares it with manifest.JSONSchema(schemaID);
    // on a difference: t.Fatal("cli/schema/v1/manifest.schema.json is stale — run: mise run schema:build")
}
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/cli`. Expected: FAIL.

- [ ] **Step 3: Implement `manifestcmd.go` and register the commands.**

- [ ] **Step 4: Add the mise tasks and generate the schema.** In `mise.toml`: `schema:build` (`depends = ["cli:build"]`, `run = "./cli/bin/ezc manifest schema -o cli/schema/v1/manifest.schema.json"`) and `schema:check` (builds, generates to a temp file, `diff -u` against the committed one, exits 1 with `cli/schema/v1/manifest.schema.json is stale — run: mise run schema:build` on a difference). Run `mise run schema:build`.

- [ ] **Step 5: Run and see them pass.** Run: `cd cli && go test ./internal/cli && cd .. && mise run schema:check`. Expected: PASS, and `schema:check` exits 0.

- [ ] **Step 6: Document.** README commands table; `CLAUDE.md` commands (`schema:build`, `schema:check`) and a sharp edge: `cli/schema/v1/manifest.schema.json` is generated from the Go types in `cli/internal/manifest`, and `schemaID` must change with it if the file moves.

- [ ] **Step 7: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(cli): manifest validate and schema, codegen python"
```

---

### Task 9: `ezc list` and `ezc get`

**Files:**
- Create: `cli/internal/cli/values.go`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `README.md`

**Interfaces:**
- Consumes: `repoDir`, `requireRepoDir`, `envName`, `load`, `loadManifest`, `warn` (Task 7); `Manifest.Lookup`; `Result.Names|Origin|Overrides`; `paths`.

| Command | Behaviour |
| --- | --- |
| `ezc list [ENV] [-g]` | Without `-g`: requires a repo; resolves it. With `-g`: resolves the store alone (`repo` is `""`). Prints one row per resolved name, sorted, selector variables last: `NAME  TYPE  FILE  NOTE`, columns padded to the widest cell, two spaces apart, no header. TYPE is from the scope's manifest (the repo's, or the store's with `-g`), `-` when the name is undeclared. FILE is `store/<base>` or `repo/<base>` from the Source's layer, or `shell`. NOTE is `overrides <base>, <base>` when `Overrides` is non-empty. Then a blank line and one line per manifest name that has no value: `NAME  TYPE  (not set)`. Never a value. `warn`s. With `-g` and no store manifest, TYPE is `-` for every name. |
| `ezc get NAME [ENV] [--reveal]` | Resolves the repo when in one, else the store. Prints the value and a newline to stdout. The name's type is looked up in the repo's manifest, then the store's; a `secret` without `--reveal` is an error: `NAME is a secret — add --reveal to print it.` An unset name is an error: `NAME is not set in environment <label>.` |

- [ ] **Step 1: Write the failing tests**

```go
func TestList(t *testing.T) {
    // store common.yaml "NAME: n\nEXTRA: e"; repo m1 + optional "NOTE: {type: str, desc: d}", common.yaml "NAME: r", env.local.yaml "SIZE: 3"
    // run("list") stdout ==
    // "EXTRA    -    store/common.yaml\n" +
    // "NAME     str  repo/common.yaml     overrides common.yaml\n" +
    // "SIZE     int  repo/env.local.yaml\n" +
    // "APP_ENV  -    shell\n" +
    // "EZC_ENV  -    shell\n" +
    // "\n" +
    // "NOTE  str  (not set)\n"
    // (trailing spaces trimmed from every line)
}
func TestListShowsNoValues(t *testing.T)   // a value "topsecretvalue" never appears in stdout or stderr
func TestListGlobal(t *testing.T)          // -g from outside any repo lists the store's names with FILE store/…; types from the store's manifest
func TestListGlobalWithoutManifest(t *testing.T) // store has common.yaml only → every TYPE is "-", no error
func TestListOutsideARepo(t *testing.T)    // without -g → "not in an ezc repo"
func TestGet(t *testing.T)                 // run("get", "SIZE") → stdout "3\n"
func TestGetNamedEnvironment(t *testing.T) // run("get", "SIZE", "prod") reads env.prod.yaml
func TestGetSecretNeedsReveal(t *testing.T) {
    // manifest declares TOKEN secret; common.yaml "TOKEN: t0k" →
    // run("get", "TOKEN") errors with "TOKEN is a secret — add --reveal to print it."; stdout empty
    // run("get", "TOKEN", "--reveal") → "t0k\n"
}
func TestGetSecretDeclaredOnlyInStore(t *testing.T) // store manifest declares TOKEN secret, repo's does not → still needs --reveal
func TestGetUnset(t *testing.T)            // "NOPE is not set in environment local."
func TestGetNeedsAName(t *testing.T)       // run("get") → UsageError
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/cli -run 'List|Get'`. Expected: FAIL.

- [ ] **Step 3: Implement `values.go` and register the commands.** A missing store manifest is not an error for either command; an invalid one is.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./internal/cli`. Expected: PASS.

- [ ] **Step 5: Document** both commands in the README's table.

- [ ] **Step 6: Gate and commit**

```bash
mise run cli:test && git add -A && git commit -m "feat(cli): list and get"
```

---

### Task 10: `ezc repo init` and the shim

**Files:**
- Create: `cli/share/share.go`, `cli/share/env.sh`, `cli/internal/cli/repo.go`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `cli/cmd/ezc/main_test.go`, `README.md`, `AGENTS.md`, `CLAUDE.md`

**Interfaces:**
- Produces:

```go
package share
// EnvSh is the shim ezc repo init writes to a repo's .ezc/env.sh.
//go:embed env.sh
var EnvSh string
// ShimVersion is the number on the first line of EnvSh ("# ezc-shim: 1").
const ShimVersion = 1
```

`cli/share/env.sh` (exact):

```sh
# ezc-shim: 1
#
# Loads this repo's environment from ezc. Written by `ezc repo init`; do not edit.
#
#   mise:    [env]  _.source = ".ezc/env.sh"
#   direnv:  source_env .ezc/env.sh
#
# It never fails the shell: a broken config must not make `cd` unusable.
# `ezc env` is the check that fails, so make tasks depend on it.
if command -v ezc >/dev/null 2>&1; then
  eval "$(ezc env export 2>/dev/null || true)"
fi
```

| Command | Behaviour |
| --- | --- |
| `ezc repo init` | In the working directory (not an ancestor): creates `.ezc/manifest.yaml`, `.ezc/env.local.yaml` and `.ezc/env.sh` (mode 0644), each only when absent, and prints one line per file to stdout: `created .ezc/manifest.yaml` or `kept .ezc/manifest.yaml`. An existing `env.sh` whose first line is not `# ezc-shim: 1` is rewritten and reported as `updated .ezc/env.sh`. Then prints the next steps (below). `repo` is a `verbs` parent (`init`). |

`manifest.yaml` as written:

```yaml
# What this repo needs from its environment. `ezc env` checks it.
# yaml-language-server: $schema=https://raw.githubusercontent.com/thinkjones/easy-config/main/cli/schema/v1/manifest.schema.json
schemaVersion: v1

required: {}

# Example:
#   API_URL:
#     type: url          # str, int, bool, secret, url, path or enum
#     desc: Base URL of the API this service calls
```

`env.local.yaml` as written:

```yaml
# Values for the local environment. Plain NAME: value lines.
# Secrets do not belong in this file.
```

Next steps as printed (after a blank line):

```
Load the environment with one of:
  mise     [env]  _.source = ".ezc/env.sh"
  direnv   source_env .ezc/env.sh
  neither  ezc run -- <command>

Check it with: ezc env
```

- [ ] **Step 1: Write the failing tests**

```go
func TestRepoInit(t *testing.T) {
    // run("repo", "init") → the three files exist; stdout starts
    // "created .ezc/manifest.yaml\ncreated .ezc/env.local.yaml\ncreated .ezc/env.sh\n" and contains `_.source = ".ezc/env.sh"`
    // then run("manifest", "validate") and run("env") both succeed
}
func TestRepoInitKeepsExistingFiles(t *testing.T) {
    // a manifest with content already there → "kept .ezc/manifest.yaml"; content unchanged; a second run reports three "kept"
}
func TestRepoInitUpdatesAnOldShim(t *testing.T) {
    // .ezc/env.sh is "# ezc-shim: 0\n…" → "updated .ezc/env.sh"; its content now equals share.EnvSh
}
func TestRepoInitInsideAnotherRepo(t *testing.T) {
    // f.wd = <repo>/sub, where <repo>/.ezc exists → creates <repo>/sub/.ezc (the working directory, not the ancestor)
}
func TestShimVersionMatchesFile(t *testing.T) {
    // strings.HasPrefix(share.EnvSh, fmt.Sprintf("# ezc-shim: %d\n", share.ShimVersion))
}
func TestRepoNeedsAVerb(t *testing.T) // run("repo") → UsageError containing "say init"
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/cli -run Repo`. Expected: FAIL.

- [ ] **Step 3: Implement `share/share.go`, `share/env.sh` and `repo.go`; register `newRepoCmd`.**

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 5: Add the end-to-end shim test** to `cmd/ezc/main_test.go` (skipped when `/bin/sh` is absent):

```go
func TestShimLoadsTheEnvironment(t *testing.T) {
    // temp repo: `ezc repo init`, then append "GREETING: hello" to .ezc/env.local.yaml
    // with the built binary's folder first on PATH, run:  sh -c '. .ezc/env.sh; printf %s "$GREETING"'  → "hello"
    // then break the file ("GREETING: $NOPE") and run:   sh -c '. .ezc/env.sh; echo ok'  → "ok", exit 0
}
```

- [ ] **Step 6: Finish the docs for this part.**
  - `README.md`: `ezc repo init` in the table; a "Quick start" of five lines (`ezc repo init`, add a declaration to the manifest, add a value to `env.local.yaml`, `ezc env`, `ezc run -- <command>`); the files table and resolution order from the spec's "One layout" and "Resolution" sections.
  - `AGENTS.md`: the "In a repo" table (`.ezc/manifest.yaml`, `common.yaml`, `env.<name>.yaml`, `env.sh`) and "On a machine" (`EZC_HOME`, `EZC_STORE`).
  - `CLAUDE.md` sharp edges: `cli/share/env.sh` and `share.ShimVersion` change together; `ezc env export` must never print to stderr on success or emit `EZC_ENV` or `APP_ENV`; the resolver is the only place `~`, `$HOME` and `$NAME` are expanded.

- [ ] **Step 7: Check the name rule and the gate**

Run: `grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/ ; mise run cli:test && mise run schema:check`
Expected: grep prints nothing; the gate and the schema check pass.

- [ ] **Step 8: Commit**

```bash
git add -A && git commit -m "feat(cli): repo init and the env.sh shim"
```

---

## Done when

- `mise run cli:test` and `mise run schema:check` pass.
- From a fresh temp directory with `EZC_HOME` set to another: `ezc repo init`, declare one required `str`, set it in `.ezc/env.local.yaml`, then `ezc env` prints `environment local ✓ 1 var from 1 file` (with the no-store note), `ezc env export` prints one `export` line, and `ezc run -- printenv <NAME>` prints the value.
- The same with a store: a `common.yaml` under `$EZC_HOME/store/.ezc/` supplies a second value, `ezc list` shows it as `store/common.yaml`, and a repo value of the same name shows `overrides common.yaml`.
