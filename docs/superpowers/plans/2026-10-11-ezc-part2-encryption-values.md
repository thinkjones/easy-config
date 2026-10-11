# ezc Part 2: Encryption and Values Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ezc` reads and writes encrypted `*.enc.yaml` files through a pluggable backend (SOPS with age), manages values with `ezc set`, `ezc unset` and `ezc edit`, audits a store against its manifest, and reports machine, store and repo health with `ezc doctor`.

**Architecture:** A `backend.Backend` interface with two implementations: an on-disk fake that most tests use, and `backend/sops`, which runs the `sops` binary through `System.Exec`. One contract test runs against both. The command layer picks the backend once (`System.Backend`, else sops) and hands its `Decrypt` to the resolver, so nothing outside `backend/sops` names sops. `storeaudit` and `doctor` are plain packages that return values and take their dependencies as arguments.

**Tech Stack:** Go 1.26, Cobra v1.10.2, yaml.v3 v3.0.1, plus `filippo.io/age` v1.2.1 (test-only in this part: generating a throwaway key for the real-sops tests), `golang.org/x/term` v0.47.0 (hidden input). External tool: `sops` with `sops set --value-stdin` (verified on 3.13.3).

**Spec:** `docs/superpowers/specs/2026-10-10-ezc-design.md` (sections Files, Commands "Values" and "Store", Code structure "The backend", Errors, Testing; "Parts", item 2).

**Builds on:** part 1, merged at `2290eb9`. Read `AGENTS.md` and `CLAUDE.md` first.

**Source to port** (read-only, never modify): `/Users/thinkjones/dev/ao/fs-config/cli/internal/store/store.go`, `/Users/thinkjones/dev/ao/fs-config/cli/internal/status/status.go`, and the `decrypt`/`flattenDecrypted` functions in `/Users/thinkjones/dev/ao/fs-config/cli/internal/resolve/resolve.go`.

## Global Constraints

- Everything in part 1's constraints still binds: module `github.com/thinkjones/easy-config/cli`; the name rule (`grep -rniE 'fsconfig|fs-config|fs_config|four signals|\bipm\b' cli/` prints nothing); commands reach the outside world only through `System`; `UsageError` exits 2; Markdown prose is not hard-wrapped; the gate is `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test` and it passes at the end of every task; user docs change in the same commit as the code.
- **No value in output.** No command prints a value except `ezc env export`, `ezc run` (to the child) and `ezc get`. That covers stdout, stderr, error text, and text passed through from `sops`. A value never appears in a process's arguments or environment that ezc sets, and is never written to a temporary file in plaintext by ezc.
- **A secret is never accepted on the command line.** It comes from a hidden prompt or from stdin.
- **Only `backend/sops` knows sops exists.** No other package contains the strings `sops`, `.sops.yaml`, `SOPS_` or `ENC[`, except test files, docs, and the one file that constructs the default backend, `cli/internal/cli/scope.go` (`backendFor` and `keySource`). Text shown to the user about the tool or the key comes from the backend's own messages. Check: `grep -rn --include='*.go' -iE 'sops|ENC\[' cli/internal cli/cmd | grep -v _test.go | grep -v 'internal/backend/sops/' | grep -v 'internal/cli/scope.go'` prints nothing.
- A write that fails leaves the file as it was. Plaintext files are written to a temporary file in the same folder and renamed into place.
- The real-sops tests skip when `sops` is not on PATH, and fail instead of skipping when `EZC_REQUIRE_SOPS=1`.
- Tests never read the real home, the real environment or a real key. A key used in a test is generated in the test.
- Commit messages are conventional and end with a `Co-Authored-By: Claude <model> <noreply@anthropic.com>` line naming the model that wrote the commit.
- Version constant becomes `0.2.0` in the last task.

## Decisions this plan makes beyond the spec

1. **The backend interface in this part is the subset part 2 uses**: `Name`, `Check`, `HasPolicy`, `Inspect`, `Decrypt`, `Set`, `Unset`, `Edit`. `NewKey`, `PublicKey`, `Recipients`, `WritePolicy`, `Rewrap` and `CanDecrypt` arrive with part 4, which is the first to call them. `Inspect` (names in an encrypted file, read without a key) is new relative to the spec; the keyless audit needs it.
2. **The encryption policy is not created in this part.** A folder's `.sops.yaml` must already exist for `ezc set` to write a secret there; without it the error says so. Part 3 (`ezc setup`) and part 4 (`recipients.yaml`) generate it. The README shows the three-line file to write by hand until then.
3. **The age key file** is `~/.config/sops/age/keys.txt`, passed to sops as `SOPS_AGE_KEY_FILE`, unless `SOPS_AGE_KEY` or `SOPS_AGE_KEY_FILE` is already set in the environment, in which case the environment wins. Part 3 moves the path into `config.toml`.
4. **`ezc set` adds a new name to `required`** unless `--optional` is given. The manifest is rewritten through a YAML node tree, which keeps comments and order but may normalise indentation.
5. **Removing the last name from an encrypted file deletes the file.**
6. **`ezc list` needs a key** when the environment includes encrypted files, like every other command that resolves. `ezc doctor` and `ezc store audit` work without one and say what they skipped.
7. **`ezc store audit` requires a declared `required` name to have a value in at least one file of the store, and every value in any file to be declared.** An `optional` name may have no value.
8. **A value read from an encrypted file needs `--reveal` in `ezc get`** whether or not a manifest declares it a secret.
9. **`enc` is a reserved environment name**, and `env.enc.yaml` is not an environment file. **Codegen refuses a name starting with `_`.** **`ezc env -m FILE` keeps its meaning** (the values come from the manifest's folder) and its help says so. **Unrecognised files in `.ezc/` are reported by `ezc doctor`**, not by other commands. (User decisions after part 1.)
10. **Environment names** match `^[A-Za-z0-9][A-Za-z0-9_-]*$`.

## File structure

```
cli/internal/proc/proc.go            Command: what System.Exec runs (Dir, Env, Stdin, Name, Args, Interactive)
cli/internal/backend/backend.go      Pair, Backend, the error values                     (extended)
cli/internal/backend/fake.go         Fake: an on-disk stand-in that needs no tool
cli/internal/backend/backendtest/contract.go   Run(t, factory): the behaviour every backend must have
cli/internal/backend/sops/sops.go    the sops backend
cli/internal/backend/sops/sops_test.go   contract test against real sops; unit tests with a recorded runner
cli/internal/resolve/file.go         Plain(path): one plaintext file, raw                 (extended)
cli/internal/resolve/resolve.go      reserved env name, env-name rule                     (extended)
cli/internal/valuefile/valuefile.go  write one name in a plaintext values file, or a declaration in a manifest
cli/internal/storeaudit/storeaudit.go  a store against its manifest                       (ported)
cli/internal/doctor/doctor.go        Report, Section, Item, Collect
cli/internal/doctor/files.go         which file names belong in a .ezc folder
cli/internal/cli/cli.go              System: Exec, ReadSecret, StdinIsTerminal, LookPath, Backend   (extended)
cli/internal/cli/scope.go            backendFor, the store-is-the-repo case               (extended)
cli/internal/cli/setcmd.go           ezc set, ezc unset
cli/internal/cli/editcmd.go          ezc edit
cli/internal/cli/storecmd.go         ezc store audit
cli/internal/cli/doctorcmd.go        ezc doctor
cli/cmd/ezc/main.go                  wires Exec, ReadSecret, StdinIsTerminal, LookPath    (extended)
cli/cmd/ezc/sops_test.go             end to end with real sops
```

## Review Focus

1. **A secret reaching a place another process or person can see**: argv, a child's environment, a temp file, stderr passed through from sops. Tests: Task 4 (recorded runner asserts no call carries the value), Task 6 (same at the command level), Task 10 (real sops).
2. **Setting a secret with no policy, no key or no tool** must fail with a message that says which, and must not leave the value in a plaintext file or a half-written encrypted one. Tests: Task 3 contract, Task 6.
3. **A plaintext value YAML would reinterpret** (`08540`, `yes`, `!x`, `a: b`, ` leading space`, `# hash`, multi-line, empty string) must read back exactly after `ezc set`. Tests: Task 5.
4. **A machine with no key** (CI before part 4, a newcomer): `ezc env` explains, `ezc env export` prints nothing and the shim stays quiet, `ezc doctor` and `ezc store audit` complete and say what they skipped. Tests: Task 3, Task 7, Task 8, Task 9.
5. **A write interrupted or refused partway** (read-only folder, sops failing after the manifest was updated) must not leave a declaration with no value or a value with no declaration. Tests: Task 6.

---

### Task 1: Carry-over fixes from part 1

**Files:**
- Modify: `cli/internal/resolve/resolve.go`, `cli/internal/resolve/file.go`, `cli/internal/resolve/resolve_test.go`, `cli/internal/resolve/file_test.go`, `cli/internal/gen/python.go`, `cli/internal/gen/python_test.go`, `cli/internal/cli/scope.go`, `cli/internal/cli/env.go`, `cli/internal/cli/cli_test.go`, `AGENTS.md`, `README.md`

**Interfaces:**
- Produces: `resolve.ValidEnvName(s string) bool` (`^[A-Za-z0-9][A-Za-z0-9_-]*$` and not `enc`); `resolve.ReservedEnv = "enc"`.

- [ ] **Step 1: Write the failing tests**

| Test | Setup | Expect |
| --- | --- | --- |
| `resolve.TestReservedEnvName` | `Load(Env: "enc")` | error containing `enc is a reserved name` |
| `resolve.TestInvalidEnvName` | `Load(Env: "../x")`, `Load(Env: "a b")` | error containing `is not a valid environment name` (the name printed with `%q`) |
| `resolve.TestEnvEncFileIsNotAnEnvironment` | repo holds `env.enc.yaml` and `env.local.yaml` | `Envs` returns `["local"]` |
| `resolve.TestUnknownReferenceHidesTheFragment` | `PW: P@ss$word123` | error contains `PW references a name that is not defined` and `defined so far`; does not contain `word123` |
| `resolve.TestBareTagBeforeQuotedValue` | `PW: ! "hunter2"`, `PW: ! 'hunter2'`, and `PW: ! \|` with a block | each: error containing `"PW" has a YAML tag` |
| `resolve.TestBOMDoesNotHideATag` | file starts with a UTF-8 BOM, then `PW: ! hunter2` | error containing `has a YAML tag` |
| `resolve.TestDocumentEndMarker` | `A: 1` then a line `...` | loads `A=1` (a document end with nothing after it is one document); `A: 1`, `...`, `B: 2` is the more-than-one-document error |
| `gen.TestPythonRefusesPrivateNames` | manifest with `_TOKEN` | error `cannot generate Python: _TOKEN starts with an underscore, which pydantic treats as private` |
| `cli.TestStoreIsNotItsOwnRepo` | working directory is `$EZC_HOME/store`; store `common.yaml` has `A: 1` | `ezc list -g` and `ezc env export` each show `A` once, with no `overrides` note, and `env export` prints `export A='1'` |
| `cli.TestEnvManifestFlagHelp` | `ezc help env` | contains `the values come from that manifest's folder` |

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/resolve ./internal/gen ./internal/cli`. Expected: the ten new tests FAIL.

- [ ] **Step 3: Implement.**
  - `resolve.Load`: validate `o.Env` with `ValidEnvName` before anything else; messages `enc is a reserved name; it marks encrypted files (env.<name>.enc.yaml)` and `%q is not a valid environment name; use letters, digits, - and _`. `Envs` skips `env.enc.yaml`.
  - `unknownRefError`: `<path>: <NAME> references a name that is not defined.` then the existing scope line, then `Write $$ if you meant a literal dollar sign`. The reference text is not printed.
  - `hasTag` in `file.go`: strip a leading BOM before computing columns, and treat a bare `!` followed by any value (quoted, block or plain) as a tag.
  - `gen.Python`: the underscore rule beside the keyword rule.
  - `scope.go` `load`: when the repo's `.ezc` and the store's `.ezc` are the same directory (compare after `filepath.EvalSymlinks`), pass `RepoDir: ""`.
  - `env.go`: the `-m` flag's usage text becomes `the manifest to check against; the values come from that manifest's folder, not this repo`.
  - `AGENTS.md`: the `EZC_HOME` default line says `XDG_DATA_HOME` applies on every OS when set. `README.md`: the sentence "anything that would change or drop a value is an error" becomes a list of what is rejected (anchors, tags, duplicate keys, more than one document).

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
git add -A && git commit -m "fix: reserved env name, value-free reference error, and part 1 carry-overs"
```

---

### Task 2: `proc.Command` and the System additions

**Files:**
- Create: `cli/internal/proc/proc.go`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `cli/cmd/ezc/main.go`, `cli/go.mod`

**Interfaces:**
- Produces:

```go
package proc
// Command is a program ezc runs and waits for.
type Command struct {
    Dir   string   // working directory; "" is the process's
    Env   []string // the whole environment as K=V; nil is the process's
    Stdin []byte   // fed to the program; ignored when Interactive
    Name  string
    Args  []string
    // Interactive wires the program to the terminal (an editor); Stdout and Stderr are then not captured.
    Interactive bool
}
// Runner runs a Command. err is non-nil when it could not start or exited non-zero.
type Runner func(Command) (stdout, stderr []byte, err error)
```

```go
// added to cli.System
Exec            proc.Runner
LookPath        func(string) (string, error)
// ReadSecret prints prompt to the terminal and reads a line without echo.
ReadSecret      func(prompt string) (string, error)
StdinIsTerminal func() bool
// Backend overrides the encryption backend; nil means the default (sops). Tests set it.
Backend         backend.Backend
```

- Produces, in `cli_test.go`, on the `fake`: `execs []proc.Command` (every `Exec` call, recorded), `execFn func(proc.Command) ([]byte, []byte, error)` (the response; default: error `no exec configured`), `secret string` and `secretErr error` (what `ReadSecret` returns), `stdin string`, `tty bool`, `paths map[string]string` (what `LookPath` finds).

- [ ] **Step 1: Write the failing test** in `cli/cmd/ezc/main_test.go` (Unix only), exercising the real `Exec` through a hidden test hook is not possible from outside, so test the function directly: move the implementation to `func execCommand(c proc.Command) ([]byte, []byte, error)` in `main.go` and add `cli/cmd/ezc/exec_test.go`:

```go
func TestExecCapturesStreamsSeparately(t *testing.T)   // sh -c 'printf out; printf err >&2' → "out", "err", nil
func TestExecFeedsStdin(t *testing.T)                  // cat with Stdin "abc" → stdout "abc"
func TestExecUsesDirAndEnv(t *testing.T)               // sh -c 'pwd; printf %s "$X"' with Dir=tmp, Env=["X=1","PATH=…"] → tmp path and "1"
func TestExecNonZeroIsError(t *testing.T)              // sh -c 'printf oops >&2; exit 3' → err != nil, stderr "oops"
func TestExecNilEnvInheritsProcessEnv(t *testing.T)    // t.Setenv("EZC_T","v"); Env nil → child sees it
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./cmd/ezc`. Expected: FAIL, `undefined: execCommand`.

- [ ] **Step 3: Implement.** `proc.go` as above. `execCommand` with `os/exec`: `Interactive` connects the three streams to the process's and returns nil output. `main.go` wires `Exec: execCommand`, `LookPath: exec.LookPath`, `StdinIsTerminal` with `term.IsTerminal(int(os.Stdin.Fd()))`, and `ReadSecret` with `term.ReadPassword` on stdin, printing the prompt and a final newline to stderr. Add the fields to `System` and the recording hooks to the test `fake`.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
git add -A && git commit -m "feat(cli): System can run programs with stdin and read a hidden value"
```

---

### Task 3: The backend interface, the fake and the contract test

**Files:**
- Modify: `cli/internal/backend/backend.go`
- Create: `cli/internal/backend/fake.go`, `cli/internal/backend/fake_test.go`, `cli/internal/backend/backendtest/contract.go`

**Interfaces:**
- Produces:

```go
package backend

type Pair struct{ Name, Value string }

// Backend stores secrets in *.enc.yaml files.
type Backend interface {
    // Name is the value of backend in config.toml.
    Name() string
    // Check reports whether the backend can work on this machine at all (its tool is installed and able).
    Check() error
    // HasPolicy reports whether dir holds the backend's encryption policy, without which nothing can be encrypted there.
    HasPolicy(dir string) (bool, error)
    // Inspect reads an encrypted file without a key: the names it holds, in file order, and
    // those among them whose value is not encrypted.
    Inspect(path string) (names, unencrypted []string, err error)
    // Decrypt returns the file's values in file order.
    Decrypt(path string) ([]Pair, error)
    // Set writes one value, creating the file when it does not exist.
    Set(path, name, value string) error
    // Unset removes one name. Removing the last one deletes the file.
    Unset(path, name string) error
    // Edit opens the decrypted file in the user's editor and re-encrypts it on save.
    Edit(path string) error
}

var (
    ErrNoTool   = errors.New("the encryption tool is not installed")
    ErrNoPolicy = errors.New("no encryption policy")
    ErrNoKey    = errors.New("cannot decrypt")
    ErrNotSet   = errors.New("not set")
)
```

Every error a backend returns names the file, wraps one of these where it applies (`errors.Is` works), and never contains a value.

```go
// Fake is a Backend that needs no tool. It keeps each file on disk in a form that is not plaintext.
type Fake struct {
    Locked   bool  // Decrypt, Set, Unset and Edit fail with ErrNoKey
    Missing  bool  // Check fails with ErrNoTool
    EditFunc func(pairs []Pair) []Pair // what Edit does; nil leaves the file unchanged
}
func (f *Fake) Name() string // "fake"
const FakePolicy = ".fakepolicy" // HasPolicy is true when dir holds this file
```

The fake's file format is YAML so the real file-name rules apply: one line per name, `NAME: FAKE[<base64 of the value>]`, in insertion order; a line `NAME: plain` (no `FAKE[`) is what `Inspect` reports as unencrypted.

```go
package backendtest
// Env is one backend under test in a fresh folder that has a policy, and a way to make it keyless.
type Env struct {
    Backend backend.Backend
    Dir     string             // a folder with the backend's policy in place
    Lock    func()             // after this, the backend has no usable key
    NoPolicyDir string         // a folder with no policy
}
// Run exercises the behaviour every Backend must have.
func Run(t *testing.T, newEnv func(t *testing.T) Env)
```

- [ ] **Step 1: Write the contract** in `backendtest/contract.go`, as subtests of `Run`:

| Subtest | Steps | Expect |
| --- | --- | --- |
| `SetCreatesTheFile` | `Set(dir/common.enc.yaml, "TOKEN", "t0k")` | file exists; `Decrypt` returns `[{TOKEN t0k}]` |
| `FileIsNotPlaintext` | after the set above | the file's bytes do not contain `t0k`; they do contain `TOKEN` |
| `SetKeepsOrderAndOthers` | set `A`, `B`, `C`, then set `B` again | `Decrypt` order `A, B, C`; `A` and `C` unchanged; `B` new |
| `ExactText` | set each of `08540`, `1.20`, `yes`, `null`, `!x`, `&a b`, ` lead`, `trail `, `a: b`, `# h`, `it's "q"`, `$(id)`, `a\nb\nc`, `` (empty), a 4,000-character string, `héllo ✓` | `Decrypt` returns each byte for byte |
| `InspectNeedsNoKey` | set `A`, `B`; `Lock()` | `Inspect` returns names `[A B]`, unencrypted `[]`, no error |
| `DecryptWithoutKey` | set `A`; `Lock()` | `Decrypt` error is `ErrNoKey`, names the file, and does not contain the value |
| `SetWithoutPolicy` | `Set(NoPolicyDir/common.enc.yaml, "A", "v")` | error is `ErrNoPolicy`; no file was created in `NoPolicyDir` |
| `HasPolicy` | `Dir`, `NoPolicyDir` | true, false |
| `UnsetRemovesOne` | set `A`, `B`; `Unset(A)` | `Decrypt` returns `[{B …}]` |
| `UnsetLastDeletesFile` | set `A`; `Unset(A)` | the file does not exist |
| `UnsetMissingName` | set `A`; `Unset("NOPE")` | error is `ErrNotSet`; file unchanged |
| `UnsetMissingFile` | `Unset` on a path that does not exist | error is `ErrNotSet` |
| `SetFailureLeavesFile` | set `A`; `Lock()`; `Set(A, "new")` | error; unlock is not available, so compare the file's bytes before and after: identical |
| `Check` | fresh env | nil |

- [ ] **Step 2: Write `fake_test.go`**: `backendtest.Run` against the fake, plus `TestFakeMissing` (`Missing: true` → `Check` is `ErrNoTool`), `TestFakeInspectReportsUnencrypted` (a hand-written `A: plain` line), `TestFakeEdit` (`EditFunc` appending a pair changes what `Decrypt` returns).

- [ ] **Step 3: Run and see them fail.** Run: `cd cli && go test ./internal/backend/...`. Expected: FAIL, `undefined: Fake`.

- [ ] **Step 4: Implement `backend.go` and `fake.go`.**

- [ ] **Step 5: Run and see them pass.** Run: `cd cli && go test ./internal/backend/...`. Expected: PASS.

- [ ] **Step 6: Gate and commit**

```bash
git add -A && git commit -m "feat(backend): the Backend interface, a fake and a contract test"
```

---

### Task 4: The sops backend

**Files:**
- Create: `cli/internal/backend/sops/sops.go`, `cli/internal/backend/sops/sops_test.go`
- Modify: `cli/go.mod` (adds `filippo.io/age` v1.2.1)

**Interfaces:**
- Consumes: `backend.Backend`, the error values, `backendtest.Run`, `proc.Command`, `proc.Runner`.
- Produces:

```go
package sops
const PolicyFile = ".sops.yaml"
// DefaultKeyFile is the key file under home.
func DefaultKeyFile(home string) string // <home>/.config/sops/age/keys.txt
type Options struct {
    Run      proc.Runner
    LookPath func(string) (string, error)
    Environ  []string // the process environment, K=V
    KeyFile  string   // used unless Environ already sets SOPS_AGE_KEY or SOPS_AGE_KEY_FILE
}
func New(o Options) backend.Backend   // Name() is "sops"
```

- How it runs sops. Every call has `Dir` set to the folder holding the file (where the policy is), and `Env` set to `Environ` plus `SOPS_AGE_KEY_FILE=<KeyFile>` unless `Environ` already has `SOPS_AGE_KEY` or `SOPS_AGE_KEY_FILE`. The file is named by its base name.

| Operation | Command | Stdin |
| --- | --- | --- |
| `Check` | `LookPath("sops")`; then `sops set --help` must mention `--value-stdin` | |
| `Decrypt` | `sops decrypt --output-type json <base>` | |
| `Set`, file exists | `sops set --value-stdin <base> ["NAME"]` | the value as a JSON string |
| `Set`, file absent | `sops encrypt --filename-override <base> --input-type json --output-type yaml /dev/stdin`; stdout is written to a temp file in the folder and renamed to `<base>` | `{"NAME": "value"}` |
| `Unset` | `sops unset <base> ["NAME"]`, after `Inspect` shows the name is there; when it is the only name, remove the file instead | |
| `Edit` | `sops edit <base>`, `Interactive: true` | |
| `Inspect` | no command: parse the file as YAML; names are the top-level keys other than `sops`; a name whose scalar does not start with `ENC[` is unencrypted | |
| `HasPolicy` | no command: `PolicyFile` exists in dir | |

The index argument is the JSON form `["NAME"]`, built with `json.Marshal` on the name.

- Reading the decrypted JSON: decode with a `json.Decoder` token stream so the object's key order is kept. Every value must be a JSON string; a number or boolean is the error `<path>: <NAME> is not a string; it was written unquoted and has lost its exact text. Set it again with `ezc set <NAME>`.` and a nested object or array is `<path>: <NAME> is not a single value`. Neither prints the value.

- Errors. Missing `sops`: `ErrNoTool`, message `sops is not installed. Install it (brew install sops, or https://github.com/getsops/sops/releases) and run `ezc doctor`.` Too old: `this sops is too old for ezc (it lacks `sops set --value-stdin`); upgrade it`. No policy: `ErrNoPolicy`, `<dir> has no encryption policy (.sops.yaml), so nothing can be encrypted there.` A decrypt failure whose stderr contains `Failed to get the data key`: `ErrNoKey`, `cannot decrypt <path>: no key on this machine is one of its recipients. Your key file is <KeyFile>.` (or `the key comes from SOPS_AGE_KEY` / `SOPS_AGE_KEY_FILE` when the environment supplied it). Any other failure: `<path>: sops failed:` followed by at most the first 5 lines of sops's stderr.

- [ ] **Step 1: Write the failing tests** in `sops_test.go`.

With real sops (helper `realEnv(t)`: skip or fail per the `EZC_REQUIRE_SOPS` rule; generate an age identity with `age.GenerateX25519Identity()`, write it to `t.TempDir()/keys.txt` mode 0600, write `Dir/.sops.yaml` with one creation rule for that recipient, use `os/exec` as the `Runner`; `Lock` swaps the backend's key file for a path holding a different freshly generated key):

```go
func TestContract(t *testing.T) { backendtest.Run(t, realEnv) }
func TestKeyFileFromEnvironmentWins(t *testing.T) {
    // Environ has SOPS_AGE_KEY_FILE=<the right key>, Options.KeyFile is a wrong one → Decrypt succeeds
}
func TestUnquotedNumberIsRejected(t *testing.T) {
    // write a file with real sops from JSON {"ZIP": 8540}; Decrypt → error containing "ZIP is not a string"; not containing "8540"
}
func TestInspectReportsUnencrypted(t *testing.T) {
    // take a real encrypted file, append a line "LEAK: oops" above the sops: block → Inspect unencrypted == ["LEAK"]
}
```

With a recording runner (no sops needed):

```go
func TestSetSendsTheValueOnlyOnStdin(t *testing.T) {
    // file exists (any content). Set(path, "TOKEN", "s3cr3t-value").
    // exactly one Command; Name "sops"; Args == ["set", "--value-stdin", "common.enc.yaml", `["TOKEN"]`];
    // Stdin == `"s3cr3t-value"`; Dir == the folder; no element of Args or Env contains "s3cr3t-value"
}
func TestCreateSendsTheValueOnlyOnStdin(t *testing.T) {
    // file absent, policy present; runner returns stdout "CIPHERTEXT". Set → Args start ["encrypt", "--filename-override", "common.enc.yaml"];
    // Stdin == `{"TOKEN":"s3cr3t-value"}`; the file now holds "CIPHERTEXT"; no temp file is left in the folder
}
func TestCreateFailureLeavesNoFile(t *testing.T)   // runner errors → no common.enc.yaml and no temp file
func TestEnvCarriesTheKeyFile(t *testing.T)        // Env contains "SOPS_AGE_KEY_FILE=<KeyFile>"; with SOPS_AGE_KEY in Environ it does not add it
func TestCheckWithoutSops(t *testing.T)            // LookPath fails → ErrNoTool, message contains "sops is not installed"
func TestCheckOldSops(t *testing.T)                // runner's `set --help` output lacks --value-stdin → error contains "too old"
func TestNoKeyError(t *testing.T)                  // runner fails with stderr "Failed to get the data key required to decrypt the SOPS file." → ErrNoKey; message contains the key file path
func TestOtherFailurePassesFiveLines(t *testing.T) // stderr of 9 lines → message contains lines 1-5 and not line 6
func TestNameIsJSONEncodedInTheIndex(t *testing.T) // name `A"B` cannot occur (callers validate), but the index for "A_B" is exactly `["A_B"]`
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go get filippo.io/age@v1.2.1 && go test ./internal/backend/sops`. Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Implement `sops.go`.** Port the reasoning comments from fs-config's `decrypt` and `flattenDecrypted` where they still apply (why a non-string is rejected, not guessed), reworded.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./internal/backend/...`. Expected: PASS, with `TestContract` running (not skipped) on this machine.

- [ ] **Step 5: Gate, the sops-name check from Global Constraints, and commit**

```bash
git add -A && git commit -m "feat(backend): the sops backend"
```

---

### Task 5: Encrypted files in the resolver and the read commands

**Files:**
- Modify: `cli/internal/cli/scope.go`, `cli/internal/cli/values.go`, `cli/internal/cli/env.go`, `cli/internal/cli/cli_test.go`, `cli/internal/resolve/file.go`, `cli/internal/resolve/file_test.go`, `README.md`

**Interfaces:**
- Consumes: `backend.Backend`, `backend.Fake`, `backend.ErrNoKey`, `sops.New`, `sops.DefaultKeyFile`.
- Produces:

```go
// in scope.go
// backendFor is sys.Backend, else the sops backend for this machine.
func backendFor(sys System) backend.Backend
```

```go
// in resolve/file.go
// Plain reads one plaintext values file as written: names and raw values in file order,
// with no home or reference expansion. A file that does not exist is (nil, false, nil).
func Plain(path string) (pairs []backend.Pair, present bool, err error)
```

- `load` passes `Decrypt: backendFor(sys).Decrypt`.
- `ezc get`: a value whose `Origin` is an `*.enc.yaml` file needs `--reveal`, whatever the manifests say. The message is unchanged.
- `ezc env`: when resolution fails with `backend.ErrNoKey`, the error is printed as is and the exit code is 1.
- The test `fake` gets `backend: &backend.Fake{}` by default, wired into `System.Backend`, and helpers `secretIn(t, f, layer, file, name, value)` (layer `repo` or `store`; writes through the fake's `Set`, creating the fake policy first).

- [ ] **Step 1: Write the failing tests**

```go
func TestEnvReadsEncryptedFiles(t *testing.T) {
    // repo m1 with NAME, SIZE and "TOKEN: {type: secret, desc: t}"; env.local.yaml "NAME: n\nSIZE: 3"; secretIn repo common.enc.yaml TOKEN=t0k
    // run("env") → nil; stderr "environment local ✓ 3 vars from 2 files\n"
    // run("env", "export") → stdout contains "export TOKEN='t0k'\n"
}
func TestPrecedenceAcrossEncryptedFiles(t *testing.T) {
    // store common.enc.yaml X=1; store env.local.enc.yaml X=2; repo common.enc.yaml X=3; repo env.local.yaml "X: 4"  → get X --reveal? X's origin is plaintext: "4"
    // drop the repo env.local.yaml line → "3" needs --reveal
}
func TestEncryptedValuesAreNotExpanded(t *testing.T) {
    // secret "a$HOME$$b" → export shows 'a$HOME$$b' exactly
}
func TestGetFromEncryptedFileNeedsReveal(t *testing.T) {
    // no manifest declares STRAY; secretIn repo common.enc.yaml STRAY=v → run("get","STRAY") errors "STRAY is a secret"; stdout empty; with --reveal "v\n"
}
func TestNoKey(t *testing.T) {
    // f.backend.Locked = true, an encrypted file present
    // run("env") → error is backend.ErrNoKey (errors.Is), message names the file; stdout empty
    // run("env","export") → error; stdout empty
    // run("list") → error is ErrNoKey
}
func TestListShowsEncryptedOrigin(t *testing.T) {
    // list row for TOKEN: "TOKEN  secret  repo/common.enc.yaml"; the value appears nowhere
}
func TestDefaultBackendIsSops(t *testing.T) {
    // System.Backend nil → backendFor(sys).Name() == "sops"
}
func TestPlain(t *testing.T) // resolve: raw values, no expansion ("A: ~/x" stays "~/x"), absent file → (nil,false,nil)
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/cli ./internal/resolve`. Expected: FAIL.

- [ ] **Step 3: Implement.** Remove the part-1 sentence "encrypted files arrive in part 2" from the README and describe the eight-file resolution order from the spec.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
git add -A && git commit -m "feat(cli): resolve encrypted files through the backend"
```

---

### Task 6: `valuefile`, `ezc set` and `ezc unset`

**Files:**
- Create: `cli/internal/valuefile/valuefile.go`, `cli/internal/valuefile/valuefile_test.go`, `cli/internal/cli/setcmd.go`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `README.md`, `AGENTS.md`

**Interfaces:**
- Consumes: `resolve.Plain`, `resolve.ValidEnvName`, `resolve.SelectorVars`, `manifest.ValidName`, `manifest.Load`, `Manifest.Validate`, `check.ValidateValue`, `check.IsPlaceholder`, `backendFor`, `backend.Err*`.
- Produces:

```go
package valuefile
// Set writes name: value in a plaintext values file, replacing the name's value or appending it,
// creating the file when absent. Comments and order are kept. The value read back by
// resolve.Plain is exactly value, or Set fails and the file is as it was.
func Set(path, name, value string) error
// Unset removes name. It reports whether the name was there. A file left with no names is deleted.
func Unset(path, name string) (removed bool, err error)
// Declare adds name to the manifest's required (or optional) map. It fails if the name is declared.
func Declare(manifestPath, name string, v manifest.Var, optional bool) error
// Undeclare removes name from the manifest. It reports whether it was there.
func Undeclare(manifestPath, name string) (removed bool, err error)
```

All four write through a temp file in the same folder and a rename. `Set` writes the value as a YAML scalar node tagged `!!str` and lets the encoder choose the quoting.

- `ezc set NAME [VALUE] [--env E] [-g] [--type T] [--desc D] [--optional] [--values a,b] [--min N] [--max N]`

| Step | Rule | Failure |
| --- | --- | --- |
| scope | without `-g`: the repo (`requireRepoDir`); with `-g`: the store's `.ezc`, which must exist | `no store at <path>. `ezc setup` creates one.` |
| name | `manifest.ValidName`, and not a selector variable | `UsageError`: `ezc set: %q is not a valid variable name…` / `ezc set: <NAME> names the environment and cannot be stored.` |
| env | `--env` given → `resolve.ValidEnvName` | `UsageError` with the resolver's message |
| declaration | declared in the scope's manifest → its type; `--type` given and different → error. Undeclared → `--type` and `--desc` are required | `<NAME> is declared <type> in <manifest>; --type <other> contradicts it.` / `UsageError`: `ezc set: <NAME> is not declared. Add --type and --desc to declare it.` |
| value source | type `secret`: never from argv. Otherwise `VALUE` if given. With no `VALUE`: `ReadSecret("Value for <NAME>: ")` when stdin is a terminal, else all of stdin with one trailing newline removed | `UsageError`: `ezc set: a secret is never given on the command line. Run `ezc set <NAME>` and type it, or pipe it on stdin.` |
| value checks | no NUL; not empty; not a placeholder; `check.ValidateValue` passes | `<NAME>: <why>` (never the value) |
| file | `env.<E>` or `common`, plus `.enc.yaml` for a secret, `.yaml` otherwise | |
| boundary | the same name already in the file on the other side of the boundary (same base, other suffix) | `<NAME> is already in <other file>. Moving a value across the secret boundary means it was (or will be) stored in plaintext: remove it with `ezc unset`, and give a secret a new value.` |
| secret, no policy | `backend.HasPolicy(dir)` false | the backend's `ErrNoPolicy` message plus `See "Encryption" in the README.` |
| write order | new declaration: write the value first, then `Declare`; if `Declare` fails, undo the value (unset it) and report the `Declare` error | |
| output | stdout: `set <NAME> in <layer>/<file>` and, when declared in this call, a second line `declared <NAME> (<type>) in <layer>/manifest.yaml` | |

`<layer>` is `repo` or `store`.

- `ezc unset NAME [--env E] [-g]`: the file is chosen as in `set` when the name is declared; when it is not, both the plaintext and the encrypted file of that base are tried. Not found: `<NAME> is not set in <layer>/<file>.` After removing, if no values file in the scope still holds the name (checked with `resolve.Plain` and `backend.Inspect` across `common.*` and every `env.*`), the declaration is removed too. Output: `unset <NAME> from <layer>/<file>` and, when removed, `removed <NAME> from <layer>/manifest.yaml`.

- [ ] **Step 1: Write the failing `valuefile` tests**

```go
func TestSetRoundTripsExactText(t *testing.T) {
    // for each of: "08540", "1.20", "yes", "null", "~", "!x", "&a b", "*a", " lead", "trail ", "a: b", "# h", "it's \"q\"",
    //   "$(id)", "a\nb\nc", "a\n", "héllo ✓", "- item", "{a: 1}", "[1]", "@x", "`x`", "%x", "key: |", a 4,000-char string
    // Set(path, "A", v); resolve.Plain(path) → [{A v}]
}
func TestSetKeepsCommentsAndOrder(t *testing.T) {
    // file "# head\nA: 1 # tail\n\n# about B\nB: 2\n"; Set(B, "9") → A before B; "# head", "# tail", "# about B" all still present; B is 9
}
func TestSetAppends(t *testing.T)            // new name goes last
func TestSetCreatesFile(t *testing.T)        // absent file → created with the one line; mode 0644
func TestSetLeavesNoTempFile(t *testing.T)   // folder holds only the values file afterwards
func TestSetOnReadOnlyFolderFails(t *testing.T) // chmod 0555 → error; file bytes unchanged (skip as root)
func TestUnset(t *testing.T)                 // removes one; (false, nil) for an absent name; deleting the last name deletes the file
func TestDeclareAndUndeclare(t *testing.T) {
    // manifest from `ezc repo init`'s template → Declare("API_URL", {url, "Base URL"}, false):
    // manifest.Load + Validate pass; Required has API_URL; the template's comments are still in the file
    // Declare again → error "API_URL is already declared"; Declare(..., optional true) puts a name under optional, creating that map
    // Undeclare removes it; Undeclare of an absent name → (false, nil)
}
```

- [ ] **Step 2: Write the failing command tests** (the test `fake`'s backend is the `Fake`, with a policy in the repo unless the test says otherwise)

```go
func TestSetPlaintext(t *testing.T) {
    // m1 repo. run("set","NAME","alice") → stdout "set NAME in repo/common.yaml\n"; common.yaml has it; run("get","NAME") → "alice\n"
}
func TestSetEnvFile(t *testing.T)            // --env prod → repo/env.prod.yaml
func TestSetDeclaresANewName(t *testing.T) {
    // run("set","API_URL","https://x","--type","url","--desc","Base URL") → two stdout lines; manifest validates and has API_URL required
    // --optional puts it under optional
}
func TestSetUndeclaredNeedsTypeAndDesc(t *testing.T) // UsageError containing "is not declared"
func TestSetTypeContradiction(t *testing.T)   // NAME is str; --type int → error containing "contradicts"
func TestSetValidatesTheValue(t *testing.T)   // SIZE (int 1..9): "12" → error "SIZE: above the maximum of 9"; "replace-me" → placeholder error; "" → "SIZE: empty"; file unchanged; the value 12 is not in the error
func TestSetSecretFromPrompt(t *testing.T) {
    // f.tty = true; f.secret = "s3cr3t"; manifest declares TOKEN secret. run("set","TOKEN") →
    // stdout "set TOKEN in repo/common.enc.yaml\n"; fake Decrypt shows it; the prompt given to ReadSecret was "Value for TOKEN: "
    // "s3cr3t" is in neither stdout nor stderr
}
func TestSetSecretFromStdin(t *testing.T)     // f.tty = false; f.stdin = "s3cr3t\n" → stored "s3cr3t" (one newline trimmed); "a\n\n" stores "a\n"
func TestSetSecretOnCommandLineRefused(t *testing.T) // run("set","TOKEN","s3cr3t") → UsageError "never given on the command line"; nothing written
func TestSetNewSecret(t *testing.T)           // run("set","KEY","--type","secret","--desc","d") with stdin → common.enc.yaml and a declaration
func TestSetSecretWithoutPolicy(t *testing.T) // no fake policy in the repo → error is ErrNoPolicy; no common.enc.yaml; no common.yaml; the manifest is unchanged
func TestSetSecretWithoutKey(t *testing.T)    // f.backend.Locked → error is ErrNoKey; nothing written; manifest unchanged
func TestSetAcrossTheBoundaryRefused(t *testing.T) // TOKEN (secret) already present in common.yaml → error containing "across the secret boundary"
func TestSetGlobal(t *testing.T)              // -g with a store → store/common.yaml; -g with no store → "no store at"
func TestSetSelectorRefused(t *testing.T)     // run("set","APP_ENV","x") → UsageError "names the environment"
func TestSetBadEnvName(t *testing.T)          // --env enc → UsageError "reserved"
func TestSetDeclareFailureUndoesTheValue(t *testing.T) {
    // make the manifest read-only (chmod 0444 on the file and 0555 on nothing else; skip as root), set a new name with --type/--desc →
    // error; common.yaml does not hold the name
}
func TestUnsetPlaintext(t *testing.T)         // unset NAME → "unset NAME from repo/common.yaml\n"; second line when the declaration went
func TestUnsetKeepsDeclarationWhenStillSet(t *testing.T) // NAME in common.yaml and env.prod.yaml; unset (common) → declaration kept
func TestUnsetSecret(t *testing.T)            // removes from common.enc.yaml through the backend
func TestUnsetUndeclaredFindsEitherFile(t *testing.T) // STRAY only in common.enc.yaml, undeclared → removed
func TestUnsetNotSet(t *testing.T)            // "NOPE is not set in repo/common.yaml."
```

- [ ] **Step 3: Run and see them fail.** Run: `cd cli && go test ./internal/valuefile ./internal/cli`. Expected: FAIL.

- [ ] **Step 4: Implement `valuefile.go`.** Read the file into a `yaml.Node`, change the mapping's content, encode with a 2-space indent, write to `<path>.tmp-<random>`, re-read that temp file with `resolve.Plain` (for `Set`) or `manifest.Load` + `Validate` (for `Declare`) and compare, then rename. On any failure remove the temp file.

- [ ] **Step 5: Implement `setcmd.go`, register `newSetCmd` and `newUnsetCmd`.**

- [ ] **Step 6: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 7: Document.** README commands table rows for `set` and `unset`; a short "Encryption" section: what a `.sops.yaml` must contain today (the three-line example with one `age:` recipient), that `ezc setup` will write it in a later version, where the key file is, and that a secret is typed or piped, never passed as an argument.

- [ ] **Step 8: Gate and commit**

```bash
git add -A && git commit -m "feat(cli): set and unset"
```

---

### Task 7: `ezc edit`

**Files:**
- Create: `cli/internal/cli/editcmd.go`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `README.md`

**Interfaces:**
- Consumes: `proc.Command`, `System.Exec`, `backendFor`, `resolve.Plain`, `resolve.ValidEnvName`.

- `ezc edit [--env E] [--secrets] [-g]`: opens `common.yaml` (or `env.<E>.yaml`), or with `--secrets` the `.enc.yaml` of the same base, in the repo or with `-g` the store.

| Rule | Detail |
| --- | --- |
| editor | `$VISUAL`, else `$EDITOR`; neither set: `set EDITOR to an editor that waits, for example: export EDITOR=nano` |
| blocking | the editor command is split on spaces; if its first word's base name is one of `code`, `code-insiders`, `cursor`, `windsurf`, `subl`, `zed`, `atom`, `mate` and no later word is `--wait`, `-w` or `-W`: `<editor> returns before you finish editing, so changes would be lost. Use `<first word> --wait`.` |
| plaintext | create the file (empty) if absent, then `Exec` the editor with the path appended, `Interactive: true`. Afterwards read it with `resolve.Plain`; if that fails, print the error and `<file> was saved but is not valid; fix it with `ezc edit`.` and exit 1. A file left empty that this command created is removed |
| secrets | `backend.HasPolicy` else the no-policy error; then `backend.Edit(path)`. The backend is given the editor through the environment it already passes on |
| no terminal | `StdinIsTerminal()` false: `ezc edit needs a terminal` |
| output | stdout: `edited <layer>/<file>` |

- [ ] **Step 1: Write the failing tests**

```go
func TestEditNeedsAnEditor(t *testing.T)        // neither variable → error containing "set EDITOR"
func TestEditRefusesNonBlockingEditor(t *testing.T) {
    // EDITOR="code" → error containing "returns before you finish"; EDITOR="code --wait", "code -w", "/usr/local/bin/code --wait", "nano", "vim" are accepted
    // VISUAL wins over EDITOR
}
func TestEditPlaintext(t *testing.T) {
    // f.tty = true; EDITOR="nano"; f.execFn writes "NAME: edited\n" to the path it is given (last arg) →
    // one Exec: Name "nano", last Arg the absolute path of repo common.yaml, Interactive true; stdout "edited repo/common.yaml\n"
}
func TestEditWithArgsInEditor(t *testing.T)     // EDITOR="vim -u NONE" → Name "vim", Args ["-u","NONE",<path>]
func TestEditEnvFile(t *testing.T)              // --env prod → env.prod.yaml
func TestEditInvalidResult(t *testing.T)        // execFn writes "A: [1]" → error containing "was saved but is not valid"; ExitError or plain error, exit 1
func TestEditCreatedButLeftEmptyIsRemoved(t *testing.T) // absent file, execFn writes nothing → file does not exist afterwards
func TestEditSecrets(t *testing.T)              // --secrets → the fake backend's EditFunc was called for repo common.enc.yaml; stdout "edited repo/common.enc.yaml\n"
func TestEditSecretsWithoutPolicy(t *testing.T) // ErrNoPolicy
func TestEditNeedsATerminal(t *testing.T)       // f.tty = false → "ezc edit needs a terminal"
func TestEditGlobal(t *testing.T)               // -g → store/common.yaml
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/cli -run Edit`. Expected: FAIL.

- [ ] **Step 3: Implement `editcmd.go` and register `newEditCmd`.**

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 5: Document** the command in the README table, with the note that the editor must wait.

- [ ] **Step 6: Gate and commit**

```bash
git add -A && git commit -m "feat(cli): edit"
```

---

### Task 8: `storeaudit` and `ezc store audit`

**Files:**
- Create: `cli/internal/storeaudit/storeaudit.go`, `cli/internal/storeaudit/storeaudit_test.go`, `cli/internal/cli/storecmd.go`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `README.md`, `mise.toml` is NOT changed (this repo is not a store)

**Interfaces:**
- Consumes: `manifest`, `resolve.Plain`, `check.ValidateValue`, `check.IsPlaceholder`, `backend.Backend`, `backend.ErrNoKey`.
- Produces:

```go
package storeaudit
// Finding is one problem: a variable, what is wrong, and what to do. Never a value.
type Finding struct{ Name, File, Why, Fix string }
func (f Finding) String() string
type Result struct {
    Dir       string
    Files     []string  // values files examined, base names, in precedence order
    Declared  int
    Findings  []Finding // sorted by Name, then File
    // KeylessSkipped is the encrypted files whose values could not be checked for want of a key.
    KeylessSkipped []string
}
func (r Result) OK() bool
func (r Result) Report() string
// Audit checks the store in dir (a .ezc folder) against its manifest.
func Audit(dir string, b backend.Backend) (Result, error)
```

`Audit` returns an error only when the audit could not run: no `manifest.yaml`, or an invalid one (listing its problems). Everything else is a `Finding`.

| Finding | Condition | Why | Fix |
| --- | --- | --- | --- |
| undeclared | a name in any values file with no declaration | `not declared in manifest.yaml` | `Declare it with a type and a desc, or remove it with `ezc unset -g`.` |
| missing | a `required` name with a value in no file | `declared but no file sets it` | `Set it with `ezc set -g <NAME>`, or remove the declaration.` |
| secret in plaintext | type `secret`, found in a `*.yaml` | `is a secret but is in plaintext` | `It is in git history in plaintext: give it a new value with `ezc set -g <NAME>` after `ezc unset -g <NAME>`.` |
| plaintext in secrets | another type, found in a `*.enc.yaml` | `is not a secret but is in an encrypted file` | `Declare it secret, or move it: `ezc unset -g`, then `ezc set -g`.` |
| unencrypted | `Inspect` reports the name unencrypted | `is in an encrypted file but its value is not encrypted` | `Set it again with `ezc set -g <NAME>` and treat the old value as leaked.` |
| placeholder | `check.IsPlaceholder(value)` | `is still a placeholder` | |
| shape | `check.ValidateValue` returns text | that text | |

Files examined: `common.yaml`, `common.enc.yaml`, then for each environment (sorted) `env.<e>.yaml`, `env.<e>.enc.yaml`. When `Decrypt` fails with `ErrNoKey`, the file's names come from `Inspect`, placeholder and shape checks are skipped for it, and it is added to `KeylessSkipped`. Any other decrypt error is returned.

`Report()`: `store <dir>: <n> declared, <m> files` then either `✓ nothing to fix` or one block per finding (`  <NAME> (<file>) — <why>` and the fix indented on the next line), then, when `KeylessSkipped` is not empty, `! no key: value checks skipped for <files>`.

- `ezc store audit`: `store` is a `verbs` parent (`audit`). Audits the store's `.ezc`; prints the report to stdout; `&ExitError{1}` when there are findings. No store: `no store at <path>. `ezc setup` creates one.`

- [ ] **Step 1: Write the failing tests** in `storeaudit_test.go`, using `backend.Fake` and a helper that builds a store folder from a manifest string and file contents:

```go
func TestCleanStore(t *testing.T)               // declared == set, types in the right files → OK(); Report contains "✓ nothing to fix"
func TestUndeclared(t *testing.T)               // STRAY in common.yaml → one Finding{Name STRAY, File common.yaml, Why "not declared in manifest.yaml"}
func TestUndeclaredInEncryptedFile(t *testing.T)
func TestMissingRequired(t *testing.T)          // required A with no value → "declared but no file sets it"
func TestOptionalMayBeAbsent(t *testing.T)      // optional B with no value → no finding
func TestSetOnlyInOneEnvironmentCounts(t *testing.T) // required A only in env.prod.yaml → no finding
func TestSecretInPlaintext(t *testing.T)
func TestPlaintextInSecrets(t *testing.T)
func TestUnencryptedValueInEncryptedFile(t *testing.T) // a hand-written "LEAK: plain" line in the fake's file
func TestPlaceholderAndShape(t *testing.T)      // URL "ftp://x" → Why "not a URL (must start with http:// or https://)"; "replace-me" → placeholder
func TestFindingsNeverContainValues(t *testing.T) // distinctive values everywhere; Report() contains none of them
func TestKeyless(t *testing.T) {
    // Fake.Locked: undeclared, missing and boundary findings still appear; a placeholder in the encrypted file does not;
    // KeylessSkipped == ["common.enc.yaml"]; Report contains "! no key: value checks skipped for common.enc.yaml"
}
func TestNoManifestIsAnError(t *testing.T)      // error containing "has no manifest.yaml"
func TestInvalidManifestIsAnError(t *testing.T)
func TestFindingsAreSorted(t *testing.T)
```

and in `cli_test.go`:

```go
func TestStoreAuditClean(t *testing.T)          // exit nil; stdout ends "✓ nothing to fix\n"
func TestStoreAuditFindings(t *testing.T)       // ExitError{1}; stdout names each finding
func TestStoreAuditNoStore(t *testing.T)        // "no store at"
func TestStoreNeedsAVerb(t *testing.T)          // UsageError "say audit"
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/storeaudit ./internal/cli`. Expected: FAIL.

- [ ] **Step 3: Port and implement.** Start from `/Users/thinkjones/dev/ao/fs-config/cli/internal/store/store.go`: keep the structure (`undeclared`, `missing`, `boundary`, `shapes`) and the comments that explain why the audit is complete in both directions; replace its file list with the one above, its direct sops and recipients calls with the `backend.Backend` argument, and `global.*` with the eight-file layout.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 5: Document** `ezc store audit` in the README, as the check a store's commits should pass.

- [ ] **Step 6: Gate and commit**

```bash
git add -A && git commit -m "feat(storeaudit): audit a store against its manifest"
```

---

### Task 9: `doctor`

**Files:**
- Create: `cli/internal/doctor/doctor.go`, `cli/internal/doctor/files.go`, `cli/internal/doctor/doctor_test.go`, `cli/internal/cli/doctorcmd.go`
- Modify: `cli/internal/cli/cli.go`, `cli/internal/cli/cli_test.go`, `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: `storeaudit.Audit`, `resolve.Load`, `resolve.Envs`, `check.Run`, `manifest`, `backend.Backend`, `share.ShimVersion`, `paths.Paths`.
- Produces:

```go
package doctor
type State int
const ( OK State = iota; Warn; Broken )
type Item struct{ State State; Label, Detail, Fix string }
type Section struct{ Name string; Items []Item }   // Name is "machine", "store" or "repo"
type Report struct{ Sections []Section }
func (r Report) Broken() bool
func (r Report) String() string
var Sections = []string{"machine", "store", "repo"}
type Options struct {
    Only     string          // "" for all
    Version  string
    Home     string
    StoreDir string          // the store's .ezc
    StoreSet bool            // EZC_STORE was set explicitly
    RepoDir  string          // "" when not in a repo
    Env      string          // "" when none named
    Backend  backend.Backend
    KeyNote  string          // where the key comes from, for display (a path, or "SOPS_AGE_KEY")
    KeyFound bool
}
func Collect(o Options) Report
// Unrecognised lists files in a .ezc folder that ezc does not read, each with a suggestion ("" when there is none).
func Unrecognised(dir string, store bool) ([][2]string, error)
```

`String()`: per section a line with its name, then one line per item: `  ✓ label  detail`, `  ! label  detail`, `  ✗ label  detail`, labels padded to the section's widest, and for a `Warn` or `Broken` item with a `Fix`, a following line `      <fix>`. Never a value.

| Section | Item | OK | Otherwise |
| --- | --- | --- | --- |
| machine | `ezc` | version | |
| machine | `backend` | `<name>` when `Backend.Check()` is nil | Broken, the check's message |
| machine | `key` | `KeyNote` when `KeyFound` | Warn: `no key at <KeyNote>`; fix: `You can read plaintext values. Encrypted ones need a key; `ezc setup` will create one.` |
| store | `location` | the folder | not there: Warn (`no store`) when the path is the default, Broken when `StoreSet` |
| store | `manifest` | `<n> declared` | Broken when invalid; Warn `no manifest.yaml` when absent (then `audit` is skipped) |
| store | `audit` | `nothing to fix` | Broken: `<n> to fix — run `ezc store audit``; Warn appended when values were skipped for want of a key |
| store | `policy` | present, or `not needed` when the store has no encrypted file | Broken: encrypted files but no policy |
| store | `files` | `all recognised` | Warn per unrecognised file: `<name>` with `did you mean <suggestion>?` |
| repo | `folder` | the `.ezc` path | when `RepoDir` is "": one Warn item `not in an ezc repo`, and nothing else in the section |
| repo | `manifest` | `<r> required, <o> optional` | Broken when absent or invalid |
| repo | `shim` | `version <n>` | Warn `no .ezc/env.sh` when absent; Broken when its marker is missing or not `share.ShimVersion`; fix `Run `ezc repo init`.` |
| repo | `environments` | the names, comma-separated | Warn `none` |
| repo | `environment <label>` | `<n> vars from <m> files` | Broken: `<n> problems — run `ezc env``; or the resolve error's first line; Warn `cannot decrypt: no key` for `ErrNoKey` |
| repo | `policy`, `files` | as for the store | |

`files.go`: recognised in any `.ezc`: `manifest.yaml`, `common.yaml`, `common.enc.yaml`, `env.<name>.yaml` and `env.<name>.enc.yaml` for a valid environment name, `.gitignore`, and the backend's policy file (the list of extra names is a parameter, so `doctor` does not name sops); in a repo also `env.sh`; in a store also `recipients.yaml`, `repos.yaml`, `rotation-due.yaml` and the folder `requests`. Suggestions: a `.yml` for the same name with `.yaml`; `env.enc.yaml` → `env.<name>.enc.yaml`; otherwise the recognised name within edit distance 2, if any.

- `ezc doctor [ENV] [--only machine|store|repo]`: prints the report to stdout; `&ExitError{1}` when `Broken()`. An unknown `--only` is a `UsageError` listing the three. The command builds `Options` from `System` (the key note comes from the environment variables and `sops.DefaultKeyFile`, resolved in `scope.go` beside `backendFor` as `keySource(sys) (note string, found bool)`).

- [ ] **Step 1: Write the failing tests** in `doctor_test.go` (pure: temp folders, `backend.Fake`):

```go
func TestAllHealthy(t *testing.T)               // no Warn or Broken; Broken() false; String() has the three section names in order
func TestBackendMissing(t *testing.T)           // Fake.Missing → machine/backend Broken; Broken() true
func TestNoKeyIsAWarning(t *testing.T)          // KeyFound false → Warn, Broken() false
func TestNoStoreDefaultVersusExplicit(t *testing.T) // StoreSet false → Warn; true → Broken
func TestStoreAuditFindings(t *testing.T)       // an undeclared value → store/audit Broken "1 to fix"
func TestEncryptedFilesWithoutPolicy(t *testing.T) // Broken in store and in repo
func TestUnrecognisedFiles(t *testing.T) {
    // .ezc holds common.yml, comon.yaml, env.enc.yaml, notes.txt, env.prod.yaml →
    // [common.yml → common.yaml], [comon.yaml → common.yaml], [env.enc.yaml → env.<name>.enc.yaml], [notes.txt → ""]; env.prod.yaml not listed
    // a store's recipients.yaml and requests/ are recognised in a store and not in a repo
}
func TestNotInARepo(t *testing.T)               // one Warn item; Broken() false
func TestShimStates(t *testing.T)               // absent → Warn; "# ezc-shim: 0" → Broken; current → OK
func TestEnvironmentProblems(t *testing.T)      // a required name unset → Broken "1 problem — run `ezc env`"
func TestEnvironmentWithoutKey(t *testing.T)    // Fake.Locked + an encrypted file → Warn "cannot decrypt: no key"
func TestOnly(t *testing.T)                     // Only "store" → exactly one section
func TestNoValuesInReport(t *testing.T)         // distinctive values in every file → String() contains none
func TestStringLayout(t *testing.T)             // exact output for a small fixed Report with one of each state and a Fix
```

and in `cli_test.go`:

```go
func TestDoctor(t *testing.T)                   // healthy fake machine → nil; stdout has "machine", "store", "repo"
func TestDoctorExitsOneWhenBroken(t *testing.T) // ExitError{1}
func TestDoctorOnly(t *testing.T)               // --only repo → no "machine" line; --only nope → UsageError naming machine, store, repo
func TestDoctorNamedEnvironment(t *testing.T)   // doctor prod → item label "environment prod"
```

- [ ] **Step 2: Run and see them fail.** Run: `cd cli && go test ./internal/doctor ./internal/cli`. Expected: FAIL.

- [ ] **Step 3: Implement.** Use `/Users/thinkjones/dev/ao/fs-config/cli/internal/status/status.go` for the shape (`State`, `Item`, `Section`, `Report`, the marker and padding in `String`) and for the reasoning behind checking the shim's version; none of its install-path or symlink checks apply.

- [ ] **Step 4: Run and see them pass.** Run: `cd cli && go test ./...`. Expected: PASS.

- [ ] **Step 5: Document.** README: `ezc doctor` row and a "When something is wrong" paragraph (start with `ezc doctor`). `CLAUDE.md` sharp edges: `doctor` and `storeaudit` print names, counts and paths only; `share.ShimVersion` and `env.sh` change together and `doctor` enforces it.

- [ ] **Step 6: Gate and commit**

```bash
git add -A && git commit -m "feat(doctor): machine, store and repo health"
```

---

### Task 10: End to end with real sops, docs and version

**Files:**
- Create: `cli/cmd/ezc/sops_test.go`
- Modify: `cli/cmd/ezc/main.go` (version `0.2.0`), `README.md`, `AGENTS.md`, `CLAUDE.md`, `mise.toml`

- [ ] **Step 1: Write the end-to-end test** (skips or fails per the `EZC_REQUIRE_SOPS` rule; Unix only). It drives the built binary with `HOME` and `EZC_HOME` in temp folders, a generated age key at `$HOME/.config/sops/age/keys.txt`, and a repo whose `.ezc/.sops.yaml` names that key:

```go
func TestSecretsEndToEnd(t *testing.T) {
    // ezc repo init
    // printf 's3cr3t-e2e' | ezc set API_TOKEN --type secret --desc "token"   → exit 0; stdout has "set API_TOKEN in repo/common.enc.yaml"
    // the file .ezc/common.enc.yaml exists and does not contain "s3cr3t-e2e"
    // ezc env                              → exit 0
    // ezc env export                       → contains "export API_TOKEN='s3cr3t-e2e'"
    // ezc run -- sh -c 'printf %s "$API_TOKEN"'  → "s3cr3t-e2e"
    // ezc get API_TOKEN                    → exit 1, stdout empty;  --reveal → "s3cr3t-e2e\n"
    // ezc list                             → has "API_TOKEN  secret  repo/common.enc.yaml"; no "s3cr3t-e2e"
    // ezc doctor                           → exit 0; no "s3cr3t-e2e"
    // ezc unset API_TOKEN                  → the file is gone and the declaration removed
}
func TestNoKeyEndToEnd(t *testing.T) {
    // same repo with a secret set, then replace the key file with a different generated key
    // ezc env          → exit 1; stderr contains "cannot decrypt" and the key file path; no "s3cr3t-e2e"
    // ezc env export   → exit 1; stdout empty
    // sh -c '. .ezc/env.sh; echo ok'   → "ok", exit 0
    // ezc doctor       → exit 0 (warnings only); contains "cannot decrypt: no key"
}
func TestNoValueInAnyProcessListing(t *testing.T) {
    // a wrapper script named `sops` first on PATH appends "$@" and its environment to a log file, then execs the real sops
    // printf 'ps-probe-value' | ezc set TOK --type secret --desc d
    // the log does not contain "ps-probe-value"
}
```

- [ ] **Step 2: Run and see it fail or pass for the right reason.** Run: `cd cli && go test ./cmd/ezc -run 'EndToEnd|ProcessListing' -v`. Expected: the tests run (not skipped) and PASS; if any fails, the defect is in an earlier task's code and is fixed here, with the fix described in the report.

- [ ] **Step 3: Version and mise.** `version = "0.2.0"`; update the version assertion in the part-1 tests. Add a mise task `cli:test:sops` that runs the gate with `EZC_REQUIRE_SOPS=1`.

- [ ] **Step 4: Finish the docs.** Each must be true of the code, checked against `ezc help <command>` from the built binary:
  - `README.md`: status line (parts 1 and 2 of 7); the full commands table; "Encryption" (policy file by hand for now, key file location and the two environment variables that override it, secrets typed or piped); the eight-file resolution order; what is still to come (setup, store sync, users and bots, the registry, the TUI).
  - `AGENTS.md`: the new packages in "Where things are" (`backend`, `backend/sops`, `valuefile`, `storeaudit`, `doctor`, `proc`); the conventions "only `backend/sops` names sops" and "no value in output".
  - `CLAUDE.md`: commands (`cli:test:sops`); sharp edges: sops runs with its working directory set to the file's folder; a secret reaches sops on stdin only; `ezc set` writes the value before the declaration and undoes it if declaring fails; the fake backend's file format is not sops's.

- [ ] **Step 5: Final checks**

Run: `MISE_TRUSTED_CONFIG_PATHS=$PWD mise run cli:test:sops && MISE_TRUSTED_CONFIG_PATHS=$PWD mise run schema:check`, the name-rule grep, and the sops-name grep from Global Constraints.
Expected: both tasks pass; both greps print nothing.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: end-to-end secrets with sops; ezc 0.2.0"
```

---

## Done when

- `mise run cli:test:sops` and `mise run schema:check` pass on a machine with sops.
- In a fresh repo with a hand-written `.sops.yaml` and a generated key: `ezc set TOKEN --type secret --desc x` (typed at the prompt), `ezc env`, `ezc run -- printenv TOKEN`, `ezc get TOKEN --reveal`, `ezc edit --secrets`, `ezc unset TOKEN` all work, and the value appears in no file in plaintext.
- `ezc store audit` on a store with an undeclared value exits 1 and names it; `ezc doctor` on a machine with no key exits 0 and says what it could not check.
