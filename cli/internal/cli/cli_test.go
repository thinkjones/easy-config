package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/share"
)

// fake is a System over temp directories. home is $HOME, ezcHome is $EZC_HOME, wd is the working directory.
type fake struct {
	out, errOut bytes.Buffer
	env         map[string]string
	wd          string
	children    [][]string // each RunChild call: name, args..., then "--env--", then the env
	childExit   int

	execs     []proc.Command                             // every Exec call
	execFn    func(proc.Command) ([]byte, []byte, error) // the Exec response; nil is an error
	secret    string                                     // what ReadSecret returns
	secretErr error                                      // what ReadSecret fails with
	prompts   []string                                   // every prompt ReadSecret was given
	stdin     string                                     // what Stdin holds
	tty       bool                                       // StdinIsTerminal
	paths     map[string]string                          // what LookPath finds, by name
	backend   *backend.Fake                              // the encryption backend; System.Backend
	now       time.Time                                  // System.Now; the zero value means 2026-10-11T12:00:00Z
	errTTY    bool                                       // StderrIsTerminal
	noGit     bool                                       // git cannot be found
	gitFail   string                                     // a git command with this argument fails

	t    *testing.T
	gits []proc.Command // every git command Exec ran; they are not in execs
	git  proc.Runner    // the real git, isolated; made on first use
}

func newFake(t *testing.T) *fake {
	t.Helper()
	return &fake{
		env: map[string]string{"HOME": t.TempDir(), "EZC_HOME": t.TempDir()},
		wd:  t.TempDir(),

		paths:   map[string]string{"git": "/usr/bin/git"},
		backend: &backend.Fake{},
		t:       t,
	}
}

// runGit runs the real git, cut off from the user's config. Without git it answers as a machine that has none.
func (f *fake) runGit(c proc.Command) ([]byte, []byte, error) {
	f.gits = append(f.gits, c)
	if f.noGit {
		return nil, nil, exec.ErrNotFound
	}
	if f.gitFail != "" {
		for _, arg := range c.Args {
			if arg == f.gitFail {
				return nil, []byte("fatal: made to fail\n"), errors.New("exit status 128")
			}
		}
	}
	if f.git == nil {
		if _, err := exec.LookPath("git"); err != nil && os.Getenv("EZC_REQUIRE_GIT") != "1" {
			return nil, nil, exec.ErrNotFound
		}
		f.git = gittest.Runner(f.t)
	}
	return f.git(c)
}

func (f *fake) sys() System {
	return System{
		Stdout: &f.out,
		Stderr: &f.errOut,
		Stdin:  strings.NewReader(f.stdin),
		Getenv: func(k string) string { return f.env[k] },
		Environ: func() []string {
			var env []string
			for k, v := range f.env {
				env = append(env, k+"="+v)
			}
			return env
		},
		Getwd: func() (string, error) { return f.wd, nil },
		GOOS:  "linux",
		RunChild: func(name string, args, env []string) (int, error) {
			call := append([]string{name}, args...)
			call = append(call, "--env--")
			f.children = append(f.children, append(call, env...))
			return f.childExit, nil
		},
		Version: "0.3.0",
		Exec: func(c proc.Command) ([]byte, []byte, error) {
			if c.Name == "git" {
				return f.runGit(c)
			}
			f.execs = append(f.execs, c)
			if f.execFn == nil {
				return nil, nil, errors.New("no exec configured")
			}
			return f.execFn(c)
		},
		LookPath: func(name string) (string, error) {
			if p, ok := f.paths[name]; ok {
				return p, nil
			}
			return "", fmt.Errorf("%s: not found", name)
		},
		ReadSecret: func(prompt string) (string, error) {
			f.prompts = append(f.prompts, prompt)
			return f.secret, f.secretErr
		},
		StdinIsTerminal:  func() bool { return f.tty },
		StderrIsTerminal: func() bool { return f.errTTY },
		Now: func() time.Time {
			if f.now.IsZero() {
				return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
			}
			return f.now
		},
		Backend: f.backend,
	}
}

func (f *fake) run(args ...string) error { return Run(args, f.sys()) }

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// secretIn writes name=value to file ("common.enc.yaml") in the layer's .ezc folder ("repo" or "store"),
// through the fake backend, creating its policy first.
func secretIn(t *testing.T, f *fake, layer, file, name, value string) {
	t.Helper()
	dir := filepath.Join(f.wd, ezcDir)
	if layer == "store" {
		p, err := storePaths(f.sys())
		if err != nil {
			t.Fatal(err)
		}
		dir = p.StoreEzc()
	}
	policy := filepath.Join(dir, backend.FakePolicy)
	if _, err := os.Stat(policy); err != nil {
		write(t, policy, "")
	}
	if err := f.backend.Set(filepath.Join(dir, file), name, value); err != nil {
		t.Fatal(err)
	}
}

func TestVersion(t *testing.T) {
	f := newFake(t)
	if err := f.run("version"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "ezc 0.3.0\n" {
		t.Fatalf("got %q", got)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	f := newFake(t)
	err := f.run("exprot")
	var u *UsageError
	if !errors.As(err, &u) {
		t.Fatalf("want UsageError, got %v", err)
	}
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
	if err := f.run("help"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"EZC_HOME", "EZC_STORE", "EZC_ENV"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("help lacks %s", want)
		}
	}
}

const m1 = `schemaVersion: v1
required:
  NAME: {type: str, desc: who}
  SIZE: {type: int, min: 1, max: 9, desc: how many}
`

const empty = "schemaVersion: v1\nrequired: {}\n"

// repo writes files under the working directory's .ezc; store writes them under the store's.
func repo(t *testing.T, f *fake, files map[string]string) {
	t.Helper()
	for name, content := range files {
		write(t, filepath.Join(f.wd, ".ezc", name), content)
	}
}

func store(t *testing.T, f *fake, files map[string]string) {
	t.Helper()
	for name, content := range files {
		write(t, filepath.Join(f.env["EZC_HOME"], "store", ".ezc", name), content)
	}
}

// good is a repo and store that pass m1.
func good(t *testing.T, f *fake) {
	t.Helper()
	store(t, f, map[string]string{"common.yaml": "NAME: n\n"})
	repo(t, f, map[string]string{"manifest.yaml": m1, "env.local.yaml": "SIZE: 3\n", "env.prod.yaml": "SIZE: 4\n"})
}

func wantUsage(t *testing.T, err error, sub string) {
	t.Helper()
	var u *UsageError
	if !errors.As(err, &u) || !strings.Contains(err.Error(), sub) {
		t.Fatalf("want UsageError containing %q, got %v", sub, err)
	}
}

func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	var e *ExitError
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want ExitError{%d}, got %v", code, err)
	}
}

func TestEnvPasses(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("env"); err != nil {
		t.Fatal(err)
	}
	if f.out.Len() != 0 || f.errOut.String() != "environment local ✓ 2 vars from 2 files\n" {
		t.Fatalf("stdout %q stderr %q", f.out.String(), f.errOut.String())
	}
}

func TestEnvFailsListsEveryProblem(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "OTHER: x\n"})
	repo(t, f, map[string]string{"manifest.yaml": m1, "env.local.yaml": "SIZE: 12\n"})
	wantExit(t, f.run("env"), 1)
	for _, want := range []string{"NAME — who", "not set", "SIZE — how many", "above the maximum of 9"} {
		if !strings.Contains(f.errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, f.errOut.String())
		}
	}
	if f.out.Len() != 0 {
		t.Errorf("stdout %q", f.out.String())
	}
}

func TestEnvNeverPrintsAFailingValue(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{
		"manifest.yaml":  "schemaVersion: v1\nrequired:\n  DB_URL: {type: url, desc: the database}\n",
		"env.local.yaml": "DB_URL: postgres://admin:hunter2@db/x\n",
	})
	wantExit(t, f.run("env"), 1)
	if !strings.Contains(f.errOut.String(), "not a URL (must start with http:// or https://)") {
		t.Errorf("stderr %q", f.errOut.String())
	}
	if strings.Contains(f.out.String(), "hunter2") || strings.Contains(f.errOut.String(), "hunter2") {
		t.Errorf("value leaked: stdout %q stderr %q", f.out.String(), f.errOut.String())
	}
}

func TestEnvNamedEnvironmentAndTypo(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("env", "prod"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.errOut.String(), "environment prod ✓") {
		t.Fatalf("got %q", f.errOut.String())
	}
	err := f.run("env", "prd")
	if err == nil || !strings.Contains(err.Error(), `no environment "prd"`) || !strings.Contains(err.Error(), "Valid: local, prod") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvFromEZC_ENVThenAPP_ENV(t *testing.T) {
	f := newFake(t)
	good(t, f)
	f.env["APP_ENV"] = "prod"
	if err := f.run("env"); err != nil || !strings.HasPrefix(f.errOut.String(), "environment prod") {
		t.Fatalf("APP_ENV: %v %q", err, f.errOut.String())
	}
	f.errOut.Reset()
	f.env["EZC_ENV"] = "local"
	if err := f.run("env"); err != nil || !strings.HasPrefix(f.errOut.String(), "environment local") {
		t.Fatalf("EZC_ENV: %v %q", err, f.errOut.String())
	}
	f.errOut.Reset()
	if err := f.run("env", "prod"); err != nil || !strings.HasPrefix(f.errOut.String(), "environment prod") {
		t.Fatalf("arg: %v %q", err, f.errOut.String())
	}
}

func TestEnvFromSubdirectory(t *testing.T) {
	f := newFake(t)
	good(t, f)
	f.wd = filepath.Join(f.wd, "a", "b")
	if err := os.MkdirAll(f.wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.run("env"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvOutsideARepo(t *testing.T) {
	f := newFake(t)
	err := f.run("env")
	if err == nil || !strings.Contains(err.Error(), "not in an ezc repo: no .ezc folder here or in any parent folder") || !strings.Contains(err.Error(), "ezc repo init") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvWarnsWhenNoStore(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
	if err := f.run("env"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.errOut.String(), "! no store at ") || !strings.Contains(f.errOut.String(), "ezc setup") {
		t.Fatalf("got %q", f.errOut.String())
	}
}

func TestNoStoreWarningSaysWhatWasLoaded(t *testing.T) {
	// In a repo the repo's values were loaded.
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
	if err := f.run("run", "--", "true"); err != nil {
		t.Fatal(err)
	}
	where := filepath.Join(f.env["EZC_HOME"], "store")
	if got, want := f.errOut.String(), "! no store at "+where+" — only this repo's values were loaded. Run `ezc setup` to create one.\n"; got != want {
		t.Errorf("in a repo: got %q, want %q", got, want)
	}
	// With no repo in scope nothing was.
	for _, args := range [][]string{{"run", "--", "true"}, {"list", "-g"}} {
		f := newFake(t)
		if args[0] == "list" {
			repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
		}
		if err := f.run(args...); err != nil {
			t.Fatal(err)
		}
		where := filepath.Join(f.env["EZC_HOME"], "store")
		if got, want := f.errOut.String(), "! no store at "+where+", so nothing was loaded. Run `ezc setup` to create one.\n"; got != want {
			t.Errorf("%v: got %q, want %q", args, got, want)
		}
	}
}

func TestEnvWarnsWhenCommonOnly(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "A: b\n"})
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.prod.yaml": "A: c\n"})
	if err := f.run("env"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.errOut.String(), "! no environment named and no env.local.yaml") {
		t.Fatalf("got %q", f.errOut.String())
	}
}

func TestEnvVerboseShowsNoValues(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "NAME: topsecretvalue\n"})
	repo(t, f, map[string]string{"manifest.yaml": m1, "env.local.yaml": "SIZE: 3\n"})
	if err := f.run("env", "--verbose"); err != nil {
		t.Fatal(err)
	}
	got := f.errOut.String()
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "Where each value came from") || strings.Contains(got, "topsecretvalue") {
		t.Fatalf("got %q", got)
	}
}

func TestEnvInvalidManifest(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v2\nrequired: {}\n"})
	err := f.run("env")
	if err == nil || !strings.Contains(err.Error(), "manifest.yaml is invalid:") || !strings.Contains(err.Error(), "this ezc understands") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvManifestFlag(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "NAME: n\n"})
	repo(t, f, map[string]string{"manifest.yaml": m1, "env.local.yaml": "SIZE: 3\n"})
	other := t.TempDir()
	write(t, filepath.Join(other, ".ezc", "manifest.yaml"), m1)
	write(t, filepath.Join(other, ".ezc", "env.local.yaml"), "SIZE: 12\n")
	wantExit(t, f.run("env", "-m", filepath.Join(other, ".ezc", "manifest.yaml")), 1)
	if !strings.Contains(f.errOut.String(), "above the maximum of 9") {
		t.Fatalf("got %q", f.errOut.String())
	}
}

func TestExportShell(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("env", "export"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "export NAME='n'\nexport SIZE='3'\n" || f.errOut.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", f.out.String(), f.errOut.String())
	}
}

func TestExportFormats(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("env", "export", "--format", "json"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "{\n  \"NAME\": \"n\",\n  \"SIZE\": \"3\"\n}\n" {
		t.Fatalf("json %q", f.out.String())
	}
	f.out.Reset()
	if err := f.run("env", "export", "--format", "shell"); err != nil || f.out.String() != "export NAME='n'\nexport SIZE='3'\n" {
		t.Fatalf("shell %q, %v", f.out.String(), err)
	}
	f.out.Reset()
	// The third name is a format ezc had and dropped: no encoding of it reads back the same in every loader.
	for _, format := range []string{"xml", "dot" + "env", ""} {
		err := f.run("env", "export", "--format", format)
		wantUsage(t, err, fmt.Sprintf("ezc env export: unknown format %q — use shell or json.", format))
		if f.out.Len() != 0 {
			t.Fatalf("stdout %q", f.out.String())
		}
	}
}

func TestExportIsSilentWithoutAStore(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
	if err := f.run("env", "export"); err != nil {
		t.Fatal(err)
	}
	if f.errOut.Len() != 0 || f.out.String() != "export A='b'\n" {
		t.Fatalf("stdout %q stderr %q", f.out.String(), f.errOut.String())
	}
}

func TestStoreThatIsAFileIsAnError(t *testing.T) {
	for _, args := range [][]string{{"env", "export"}, {"env"}, {"run", "--", "true"}, {"list"}, {"get", "A"}} {
		f := newFake(t)
		repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
		write(t, filepath.Join(f.env["EZC_HOME"], "store"), "not a folder")
		err := f.run(args...)
		if err == nil || !strings.Contains(err.Error(), ": the store is not a folder") {
			t.Errorf("%v: got %v", args, err)
		}
		if f.out.Len() != 0 || len(f.children) != 0 {
			t.Errorf("%v: stdout %q, children %v", args, f.out.String(), f.children)
		}
	}
}

func TestEZC_STOREThatDoesNotExistIsAnError(t *testing.T) {
	for _, args := range [][]string{{"env", "export"}, {"env"}, {"run", "--", "true"}, {"list"}, {"list", "-g"}, {"get", "A"}} {
		f := newFake(t)
		repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
		nowhere := filepath.Join(t.TempDir(), "typo")
		f.env["EZC_STORE"] = nowhere
		err := f.run(args...)
		if err == nil || err.Error() != "EZC_STORE is set to "+nowhere+", which does not exist." {
			t.Errorf("%v: got %v", args, err)
		}
		if f.out.Len() != 0 || len(f.children) != 0 {
			t.Errorf("%v: stdout %q, children %v", args, f.out.String(), f.children)
		}
	}
}

// EZC_STORE naming a folder that exists, with no .ezc in it yet, is a store with nothing in it.
func TestEZC_STOREThatExistsButIsEmpty(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
	f.env["EZC_STORE"] = t.TempDir()
	if err := f.run("env", "export"); err != nil || f.out.String() != "export A='b'\n" || f.errOut.Len() != 0 {
		t.Fatalf("got %v, stdout %q stderr %q", err, f.out.String(), f.errOut.String())
	}
}

func TestExportPrintsNothingOnFailure(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "B: ok\nA: $NOPE\n"})
	if err := f.run("env", "export"); err == nil || !strings.Contains(err.Error(), "A references a name that is not defined") || strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("want an error naming A and not NOPE, got %v", err)
	}
	if f.out.Len() != 0 {
		t.Fatalf("stdout %q", f.out.String())
	}
}

func TestExportOutsideARepoExportsTheStore(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "NAME: n\n"})
	if err := f.run("env", "export"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "export NAME='n'\n" {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestExportHelpListsTheFormats(t *testing.T) {
	f := newFake(t)
	if err := f.run("help", "env", "export"); err != nil {
		t.Fatal(err)
	}
	got := f.out.String()
	if !strings.Contains(got, "  shell ") || !strings.Contains(got, "  json ") || !strings.Contains(got, "shell (for eval) or json") || strings.Contains(got, "dot"+"env") {
		t.Fatalf("got %q", got)
	}
}

func TestRunPassesEnvironment(t *testing.T) {
	f := newFake(t)
	good(t, f)
	f.env["PATH"] = "/bin"
	f.env["NAME"] = "old"
	if err := f.run("run", "prod", "--", "printenv", "NAME"); err != nil {
		t.Fatal(err)
	}
	if len(f.children) != 1 {
		t.Fatalf("children %v", f.children)
	}
	call := f.children[0]
	if call[0] != "printenv" || call[1] != "NAME" || call[2] != "--env--" {
		t.Fatalf("call %v", call)
	}
	env := call[3:]
	count := map[string]int{}
	for _, e := range env {
		count[e]++
	}
	for _, want := range []string{"NAME=n", "SIZE=4", "EZC_ENV=prod", "APP_ENV=prod", "PATH=/bin"} {
		if count[want] != 1 {
			t.Errorf("env lacks %q: %v", want, env)
		}
	}
	if count["NAME=old"] != 0 {
		t.Errorf("stale NAME in %v", env)
	}
}

func TestRunReturnsChildExitCode(t *testing.T) {
	f := newFake(t)
	good(t, f)
	f.childExit = 7
	wantExit(t, f.run("run", "--", "false"), 7)
}

func TestRunNeedsACommand(t *testing.T) {
	f := newFake(t)
	good(t, f)
	wantUsage(t, f.run("run"), "give a command after --")
	wantUsage(t, f.run("run", "--"), "give a command after --")
	wantUsage(t, f.run("run", "a", "b", "--", "x"), "at most one environment name")
	if len(f.children) != 0 {
		t.Fatal("ran a child")
	}
}

func TestExportFromARepoWithoutAManifest(t *testing.T) {
	f := newFake(t)
	// a parent repo whose values must not appear
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "PARENT: p\n"})
	f.wd = filepath.Join(f.wd, "child")
	repo(t, f, map[string]string{"env.local.yaml": "CHILD: c\n"})
	if err := f.run("env", "export"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "export CHILD='c'\n" {
		t.Fatalf("got %q", f.out.String())
	}
	err := f.run("env")
	if err == nil || !strings.Contains(err.Error(), ".ezc has no manifest.yaml. Run `ezc repo init`") {
		t.Fatalf("got %v", err)
	}
}

func TestManifestValidate(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("manifest", "validate"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); !strings.HasSuffix(got, "manifest.yaml ok — 2 required, 0 optional (schema v1)\n") {
		t.Fatalf("got %q", got)
	}
}

func TestManifestValidateInvalid(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  A: {type: str}\n  B: {type: int, min: 5, max: 1, desc: x}\n"})
	err := f.run("manifest", "validate")
	if err == nil || !strings.Contains(err.Error(), "is invalid:") || !strings.Contains(err.Error(), "A: empty desc") || !strings.Contains(err.Error(), "B: min 5 exceeds max 1") {
		t.Fatalf("got %v", err)
	}
	if f.out.Len() != 0 {
		t.Fatalf("stdout %q", f.out.String())
	}
}

func TestManifestValidateExplicitMissing(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": m1})
	err := f.run("manifest", "validate", "-m", "other.yaml")
	want := filepath.Join(f.wd, "other.yaml") + ": no such file."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestManifestValidateDefaultMissing(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"env.local.yaml": "A: a\n"})
	err := f.run("manifest", "validate")
	if err == nil || !strings.Contains(err.Error(), ".ezc has no manifest.yaml. Run `ezc repo init`") {
		t.Fatalf("got %v", err)
	}
}

func TestManifestSchemaStdout(t *testing.T) {
	f := newFake(t)
	if err := f.run("manifest", "schema"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(f.out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["$id"] != schemaID {
		t.Fatalf("$id = %v", doc["$id"])
	}
}

func TestManifestSchemaToFile(t *testing.T) {
	f := newFake(t)
	path := filepath.Join(t.TempDir(), "a", "b", "s.json")
	if err := f.run("manifest", "schema", "-o", path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if f.errOut.String() != "wrote "+path+"\n" || f.out.Len() != 0 {
		t.Fatalf("stderr %q stdout %q", f.errOut.String(), f.out.String())
	}
}

func TestManifestNeedsAVerb(t *testing.T) {
	f := newFake(t)
	wantUsage(t, f.run("manifest"), "say validate or schema")
}

func TestCodegenPython(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("codegen", "python"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.out.String(), "# GENERATED by ezc") {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestCodegenRefusesInvalidManifest(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  A: {type: str}\n"})
	out := filepath.Join(t.TempDir(), "cfg.py")
	if err := f.run("codegen", "python", "-o", out); err == nil {
		t.Fatal("want an error")
	}
	if f.out.Len() != 0 {
		t.Fatalf("stdout %q", f.out.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("output file was created")
	}
}

func TestInvalidNameRefusedByValidateAndCodegen(t *testing.T) {
	for _, args := range [][]string{{"manifest", "validate"}, {"codegen", "python"}, {"env"}} {
		f := newFake(t)
		repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  my-var: {type: str, desc: d}\n  \"X: int = 1\\n    Y\": {type: str, desc: d}\n"})
		err := f.run(args...)
		if err == nil || !strings.Contains(err.Error(), `"my-var": not a valid variable name`) || !strings.Contains(err.Error(), `"X: int = 1\n    Y": not a valid variable name`) {
			t.Errorf("%v: got %v", args, err)
		}
		if f.out.Len() != 0 {
			t.Errorf("%v: stdout %q", args, f.out.String())
		}
	}
}

func TestPythonKeywordPassesValidateAndFailsCodegen(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  class: {type: str, desc: d}\n"})
	if err := f.run("manifest", "validate"); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	out := filepath.Join(t.TempDir(), "cfg.py")
	err := f.run("codegen", "python", "-o", out)
	if err == nil || err.Error() != "cannot generate Python: class is a Python keyword" {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(out); err == nil || f.out.Len() != 0 {
		t.Fatalf("something was generated: stdout %q", f.out.String())
	}
}

func TestCodegenUnknownLanguage(t *testing.T) {
	f := newFake(t)
	wantUsage(t, f.run("codegen", "rust"), `unknown action "rust"`)
}

func TestCommittedSchemaIsCurrent(t *testing.T) {
	want, err := manifest.JSONSchema(schemaID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "schema", "v1", "manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("cli/schema/v1/manifest.schema.json is stale — run: mise run schema:build")
	}
}

func TestUnknownTypeRefusedByValidateAndCodegen(t *testing.T) {
	for _, args := range [][]string{{"manifest", "validate"}, {"codegen", "python"}} {
		f := newFake(t)
		repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  A: {type: nope, desc: x}\n"})
		err := f.run(args...)
		if err == nil || !strings.Contains(err.Error(), `A: unknown type "nope"`) {
			t.Errorf("%v: got %v", args, err)
		}
		if f.out.Len() != 0 {
			t.Errorf("%v: stdout %q", args, f.out.String())
		}
	}
}

func TestCodegenSourceIsStableAcrossCheckouts(t *testing.T) {
	var outs []string
	for range 2 {
		f := newFake(t)
		good(t, f)
		if err := f.run("codegen", "python"); err != nil {
			t.Fatal(err)
		}
		outs = append(outs, f.out.String())
	}
	if outs[0] != outs[1] || !strings.Contains(outs[0], "# Source:      .ezc/manifest.yaml\n") {
		t.Fatalf("outputs differ or lack the stable source:\n%s", outs[0][:200])
	}
}

func TestCodegenExplicitManifestSourceAsTyped(t *testing.T) {
	f := newFake(t)
	write(t, filepath.Join(f.wd, "sub", "other.yaml"), m1)
	if err := f.run("codegen", "python", "-m", "sub/other.yaml"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "# Source:      sub/other.yaml\n") {
		t.Fatalf("got %q", f.out.String()[:200])
	}
}

func TestRelativeOutputIsUnderTheWorkingDirectory(t *testing.T) {
	f := newFake(t)
	if err := f.run("manifest", "schema", "-o", "out/s.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.wd, "out", "s.json")); err != nil {
		t.Fatal(err)
	}
	if f.errOut.String() != "wrote out/s.json\n" {
		t.Fatalf("stderr %q", f.errOut.String())
	}
}

const m1Note = m1 + `optional:
  NOTE: {type: str, desc: d}
`

func TestList(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "NAME: n\nEXTRA: e\n"})
	repo(t, f, map[string]string{"manifest.yaml": m1Note, "common.yaml": "NAME: r\n", "env.local.yaml": "SIZE: 3\n"})
	if err := f.run("list"); err != nil {
		t.Fatal(err)
	}
	want := "EXTRA    -    store/common.yaml\n" +
		"NAME     str  repo/common.yaml     overrides common.yaml\n" +
		"SIZE     int  repo/env.local.yaml\n" +
		"APP_ENV  -    shell\n" +
		"EZC_ENV  -    shell\n" +
		"\n" +
		"NOTE  str  (not set)\n"
	if got := f.out.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A name the repo's manifest does not declare takes its type from the store's, as `ezc get` does.
func TestListTypeFallsBackToTheStoreManifest(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{
		"manifest.yaml": "schemaVersion: v1\nrequired:\n  TOKEN: {type: secret, desc: t}\n  NAME: {type: secret, desc: n}\n  STORE_ONLY: {type: str, desc: s}\n",
		"common.yaml":   "TOKEN: t\nNAME: n\nEXTRA: e\n",
	})
	repo(t, f, map[string]string{"manifest.yaml": m1, "env.local.yaml": "SIZE: 3\n"})
	if err := f.run("list"); err != nil {
		t.Fatal(err)
	}
	want := "EXTRA    -       store/common.yaml\n" +
		"NAME     str     store/common.yaml\n" +
		"SIZE     int     repo/env.local.yaml\n" +
		"TOKEN    secret  store/common.yaml\n" +
		"APP_ENV  -       shell\n" +
		"EZC_ENV  -       shell\n"
	if got := f.out.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestListShowsNoValues(t *testing.T) {
	f := newFake(t)
	// No store manifest and a store with no files beyond one value; the repo overrides NAME, TOKEN is a secret, and there is no env.local so warn fires.
	repo(t, f, map[string]string{
		"manifest.yaml": "schemaVersion: v1\nrequired:\n  NAME: {type: str, desc: n}\n  TOKEN: {type: secret, desc: t}\n",
		"common.yaml":   "NAME: losingvalue1\nTOKEN: secretvalue2\n",
		"env.dev.yaml":  "NAME: winningvalue3\n",
	})
	if err := f.run("list", "dev"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.errOut.String(), "no store") {
		t.Fatalf("warn did not fire: %q", f.errOut.String())
	}
	for _, v := range []string{"losingvalue1", "secretvalue2", "winningvalue3"} {
		if strings.Contains(f.out.String()+f.errOut.String(), v) {
			t.Fatalf("%s leaked: %q %q", v, f.out.String(), f.errOut.String())
		}
	}
}

func TestListInvalidManifest(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  A: {type: nope, desc: d}\n", "env.local.yaml": ""})
	if err := f.run("list"); err == nil {
		t.Fatal("want an error for an invalid repo manifest")
	}
	f = newFake(t)
	store(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  A: {type: nope, desc: d}\n", "common.yaml": "A: b\n"})
	if err := f.run("list", "-g"); err == nil {
		t.Fatal("want an error for an invalid store manifest")
	}
}

func TestListUnsetSorted(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{
		"manifest.yaml":  "schemaVersion: v1\nrequired:\n  ZED: {type: str, desc: z}\noptional:\n  ALPHA: {type: int, desc: a}\n",
		"env.local.yaml": "",
	})
	if err := f.run("list"); err != nil {
		t.Fatal(err)
	}
	want := "APP_ENV  -  shell\nEZC_ENV  -  shell\n\nALPHA  int  (not set)\nZED    str  (not set)\n"
	if got := f.out.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestListGlobal(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": m1, "common.yaml": "NAME: n\n", "env.local.yaml": "SIZE: 2\n"})
	if err := f.run("list", "-g"); err != nil {
		t.Fatal(err)
	}
	want := "NAME     str  store/common.yaml\n" +
		"SIZE     int  store/env.local.yaml\n" +
		"APP_ENV  -    shell\n" +
		"EZC_ENV  -    shell\n"
	if got := f.out.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestListGlobalWithoutManifest(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "NAME: n\n"})
	if err := f.run("list", "-g"); err != nil {
		t.Fatal(err)
	}
	if got, want := f.out.String(), "NAME  -  store/common.yaml\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestListOutsideARepo(t *testing.T) {
	f := newFake(t)
	err := f.run("list")
	if err == nil || !strings.Contains(err.Error(), "not in an ezc repo") {
		t.Fatalf("got %v", err)
	}
}

func TestGet(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("get", "SIZE"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "3\n" {
		t.Fatalf("got %q", got)
	}
}

func TestGetNamedEnvironment(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("get", "SIZE", "prod"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "4\n" {
		t.Fatalf("got %q", got)
	}
}

func TestGetSelector(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("get", "EZC_ENV", "prod"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "prod\n" {
		t.Fatalf("got %q", got)
	}
}

func TestGetSecretNeedsReveal(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{
		"manifest.yaml":  "schemaVersion: v1\nrequired:\n  TOKEN: {type: secret, desc: t}\n",
		"common.yaml":    "TOKEN: t0k\n",
		"env.local.yaml": "",
	})
	err := f.run("get", "TOKEN")
	if err == nil || err.Error() != "TOKEN is a secret — add --reveal to print it." {
		t.Fatalf("got %v", err)
	}
	if f.out.Len() != 0 || strings.Contains(f.errOut.String(), "t0k") {
		t.Fatalf("leak: %q %q", f.out.String(), f.errOut.String())
	}
	if err := f.run("get", "TOKEN", "--reveal"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "t0k\n" {
		t.Fatalf("got %q", got)
	}
}

func TestGetSecretDeclaredOnlyInStore(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  TOKEN: {type: secret, desc: t}\n"})
	repo(t, f, map[string]string{"manifest.yaml": empty, "common.yaml": "TOKEN: t0k\n", "env.local.yaml": ""})
	err := f.run("get", "TOKEN")
	if err == nil || !strings.Contains(err.Error(), "is a secret") {
		t.Fatalf("got %v", err)
	}
	if f.out.Len() != 0 || strings.Contains(err.Error()+f.errOut.String(), "t0k") {
		t.Fatalf("leak: %q %q %v", f.out.String(), f.errOut.String(), err)
	}
}

func TestGetSecretInStoreStrInRepo(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  TOKEN: {type: secret, desc: t}\n"})
	repo(t, f, map[string]string{"manifest.yaml": "schemaVersion: v1\nrequired:\n  TOKEN: {type: str, desc: t}\n", "common.yaml": "TOKEN: t0k\n", "env.local.yaml": ""})
	err := f.run("get", "TOKEN")
	if err == nil || !strings.Contains(err.Error(), "is a secret") || f.out.Len() != 0 {
		t.Fatalf("got %v, stdout %q", err, f.out.String())
	}
}

func TestGetMultiLineValue(t *testing.T) {
	f := newFake(t)
	pem := "-----BEGIN KEY-----\nabc\n-----END KEY-----"
	repo(t, f, map[string]string{"common.yaml": "KEY: |-\n  -----BEGIN KEY-----\n  abc\n  -----END KEY-----\n", "env.local.yaml": ""})
	if err := f.run("get", "KEY"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != pem+"\n" {
		t.Fatalf("got %q", got)
	}
}

func TestGetUnsetLeavesStdoutEmpty(t *testing.T) {
	f := newFake(t)
	good(t, f)
	err := f.run("get", "NOPE")
	if err == nil || f.out.Len() != 0 || strings.Contains(err.Error()+f.errOut.String(), "3") {
		t.Fatalf("got %v, stdout %q, stderr %q", err, f.out.String(), f.errOut.String())
	}
}

func TestGetWithoutAnyManifest(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"common.yaml": "A: b\n", "env.local.yaml": ""})
	if err := f.run("get", "A"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "b\n" {
		t.Fatalf("got %q", got)
	}
}

func TestGetUnset(t *testing.T) {
	f := newFake(t)
	good(t, f)
	err := f.run("get", "NOPE")
	if err == nil || err.Error() != "NOPE is not set in environment local." {
		t.Fatalf("got %v", err)
	}
}

func TestGetNeedsAName(t *testing.T) {
	f := newFake(t)
	good(t, f)
	err := f.run("get")
	var u *UsageError
	if !errors.As(err, &u) {
		t.Fatalf("want UsageError, got %v", err)
	}
}

func TestRepoInit(t *testing.T) {
	f := newFake(t)
	if err := f.run("repo", "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.yaml", "env.local.yaml", "env.sh"} {
		if _, err := os.Stat(filepath.Join(f.wd, ".ezc", name)); err != nil {
			t.Fatal(err)
		}
	}
	got := f.out.String()
	if !strings.HasPrefix(got, "created .ezc/manifest.yaml\ncreated .ezc/env.local.yaml\ncreated .ezc/env.sh\n") || !strings.Contains(got, `_.source = ".ezc/env.sh"`) {
		t.Fatalf("got %q", got)
	}
	if err := f.run("manifest", "validate"); err != nil {
		t.Fatal(err)
	}
	if err := f.run("env"); err != nil {
		t.Fatal(err)
	}
}

func TestRepoInitKeepsExistingFiles(t *testing.T) {
	f := newFake(t)
	manifestPath := filepath.Join(f.wd, ".ezc", "manifest.yaml")
	const content = "schemaVersion: v1\nrequired: {}\n# mine\n"
	write(t, manifestPath, content)
	if err := f.run("repo", "init"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "kept .ezc/manifest.yaml\n") {
		t.Fatalf("got %q", f.out.String())
	}
	if b, _ := os.ReadFile(manifestPath); string(b) != content {
		t.Fatalf("manifest changed: %q", b)
	}
	f.out.Reset()
	if err := f.run("repo", "init"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(f.out.String(), "kept .ezc/"); got != 3 {
		t.Fatalf("want three kept, got %q", f.out.String())
	}
}

func TestRepoInitUpdatesAnOldShim(t *testing.T) {
	f := newFake(t)
	shim := filepath.Join(f.wd, ".ezc", "env.sh")
	write(t, shim, "# ezc-shim: 0\necho old\n")
	if err := f.run("repo", "init"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "updated .ezc/env.sh\n") {
		t.Fatalf("got %q", f.out.String())
	}
	if b, _ := os.ReadFile(shim); string(b) != share.EnvSh {
		t.Fatalf("shim not rewritten: %q", b)
	}
}

func TestRepoInitKeepsANewerShim(t *testing.T) {
	f := newFake(t)
	shim := filepath.Join(f.wd, ".ezc", "env.sh")
	newer := fmt.Sprintf("# ezc-shim: %d\necho newer\n", share.ShimVersion+1)
	write(t, shim, newer)
	if err := f.run("repo", "init"); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("kept .ezc/env.sh (version %d, newer than this ezc)\n", share.ShimVersion+1)
	if !strings.Contains(f.out.String(), want) {
		t.Fatalf("got %q", f.out.String())
	}
	if b, _ := os.ReadFile(shim); string(b) != newer {
		t.Fatalf("shim changed: %q", b)
	}
}

func TestRepoInitInsideAnotherRepo(t *testing.T) {
	f := newFake(t)
	write(t, filepath.Join(f.wd, ".ezc", "manifest.yaml"), "schemaVersion: v1\nrequired: {}\n")
	f.wd = filepath.Join(f.wd, "sub")
	if err := os.MkdirAll(f.wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.run("repo", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.wd, ".ezc", "manifest.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestShimVersionMatchesFile(t *testing.T) {
	if !strings.HasPrefix(share.EnvSh, fmt.Sprintf("# ezc-shim: %d\n", share.ShimVersion)) {
		t.Fatalf("shim starts %q", share.EnvSh[:20])
	}
}

func TestRepoNeedsAVerb(t *testing.T) {
	f := newFake(t)
	err := f.run("repo")
	var u *UsageError
	if !errors.As(err, &u) || !strings.Contains(err.Error(), "say init") {
		t.Fatalf("got %v", err)
	}
}

func TestStoreIsNotItsOwnRepo(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": "A: 1\n"})
	f.wd = filepath.Join(f.env["EZC_HOME"], "store")
	for _, args := range [][]string{{"list", "-g"}, {"list"}, {"env", "export"}} {
		f.out.Reset()
		if err := f.run(args...); err != nil {
			t.Fatal(err)
		}
		got := f.out.String()
		if strings.Count(got, "A") != 1 || strings.Contains(got, "overrides") {
			t.Errorf("%v: got %q", args, got)
		}
		if args[0] == "env" && got != "export A='1'\n" {
			t.Errorf("got %q", got)
		}
	}
}

func TestEnvManifestFlagHelp(t *testing.T) {
	f := newFake(t)
	if err := f.run("help", "env"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "the values come from that manifest's folder") {
		t.Errorf("got %q", f.out.String())
	}
}

const auditManifest = "schemaVersion: v1\nrequired:\n  NAME: {type: str, desc: who}\n"

func TestStoreAuditClean(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": auditManifest, "common.yaml": "NAME: n\n"})
	if err := f.run("store", "audit"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); !strings.HasSuffix(got, "✓ nothing to fix\n") || !strings.Contains(got, "1 declared, 1 files") {
		t.Fatalf("got %q", got)
	}
}

func TestStoreAuditFindings(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": auditManifest, "common.yaml": "STRAY: secret-value\n"})
	err := f.run("store", "audit")
	var x *ExitError
	if !errors.As(err, &x) || x.Code != 1 {
		t.Fatalf("got %v", err)
	}
	out := f.out.String()
	if !strings.Contains(out, "STRAY (common.yaml)") || !strings.Contains(out, "NAME — declared but no file sets it") {
		t.Fatalf("got %q", out)
	}
	if strings.Contains(out, "secret-value") {
		t.Fatalf("a value leaked: %q", out)
	}
}

func TestStoreAuditNoStore(t *testing.T) {
	f := newFake(t)
	err := f.run("store", "audit")
	if err == nil || !strings.Contains(err.Error(), "no store at") {
		t.Fatalf("got %v", err)
	}
}

func TestStoreAuditBackendFailureIsAnError(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": auditManifest, "common.yaml": "NAME: n\n"})
	secretIn(t, f, "store", "common.enc.yaml", "NAME", "v")
	write(t, filepath.Join(f.env["EZC_HOME"], "store", ".ezc", "env.x.enc.yaml"), "garbage\n")
	err := f.run("store", "audit")
	var x *ExitError
	if err == nil || errors.As(err, &x) {
		t.Fatalf("got %v", err)
	}
}

func TestStoreNeedsAVerb(t *testing.T) {
	f := newFake(t)
	wantUsage(t, f.run("store"), "say status, pull, push, rewrap or audit")
}

func TestDoctor(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	for _, want := range []string{"machine\n", "store\n", "repo\n", "environment local"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("no %q in\n%s", want, f.out.String())
		}
	}
}

func TestDoctorExitsOneWhenBroken(t *testing.T) {
	f := newFake(t)
	good(t, f)
	f.backend.Missing = true
	wantExit(t, f.run("doctor"), 1)
	if !strings.Contains(f.out.String(), "✗ backend") {
		t.Fatalf("got\n%s", f.out.String())
	}
}

func TestDoctorOnly(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("doctor", "--only", "repo"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.out.String(), "machine") || !strings.Contains(f.out.String(), "repo\n") {
		t.Fatalf("got\n%s", f.out.String())
	}
	err := f.run("doctor", "--only", "nope")
	wantUsage(t, err, "machine")
	wantUsage(t, err, "store")
	wantUsage(t, err, "repo")
}

func TestDoctorNamedEnvironment(t *testing.T) {
	f := newFake(t)
	good(t, f)
	if err := f.run("doctor", "prod"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if !strings.Contains(f.out.String(), "environment prod") {
		t.Fatalf("got\n%s", f.out.String())
	}
}

func TestDoctorOutsideARepo(t *testing.T) {
	f := newFake(t)
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if !strings.Contains(f.out.String(), "not in an ezc repo") {
		t.Fatalf("got\n%s", f.out.String())
	}
}

func TestDoctorMissingExplicitStoreIsReported(t *testing.T) {
	f := newFake(t)
	good(t, f)
	f.env["EZC_STORE"] = filepath.Join(f.env["EZC_HOME"], "typo")
	wantExit(t, f.run("doctor"), 1)
	if !strings.Contains(f.out.String(), "✗ location") {
		t.Fatalf("got\n%s", f.out.String())
	}
}

func TestDoctorNoKey(t *testing.T) {
	f := newFake(t)
	good(t, f)
	f.backend.Locked = true
	if err := f.run("doctor"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "! key") {
		t.Fatalf("got\n%s", f.out.String())
	}
}

func TestKeySource(t *testing.T) {
	f := newFake(t)
	sys := f.sys()
	sys.Backend = nil // the default backend, whose key comes from the environment
	check := func(wantNote string, wantFound bool) {
		t.Helper()
		note, found := keySource(sys)
		if note != wantNote || found != wantFound {
			t.Fatalf("got (%q, %v), want (%q, %v)", note, found, wantNote, wantFound)
		}
	}
	check("~/.config/sops/age/keys.txt", false)
	write(t, filepath.Join(f.env["HOME"], ".config", "sops", "age", "keys.txt"), "k")
	check("~/.config/sops/age/keys.txt", true)
	other := filepath.Join(t.TempDir(), "other.txt")
	f.env["SOPS_AGE_KEY_FILE"] = other
	check(other, false)
	write(t, other, "k")
	check(other, true)
	f.env["SOPS_AGE_KEY"] = "AGE-SECRET-KEY-x"
	check("SOPS_AGE_KEY", true)
}
