package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

// fake is a System over temp directories. home is $HOME, ezcHome is $EZC_HOME, wd is the working directory.
type fake struct {
	out, errOut bytes.Buffer
	env         map[string]string
	wd          string
	children    [][]string // each RunChild call: name, args..., then "--env--", then the env
	childExit   int
}

func newFake(t *testing.T) *fake {
	t.Helper()
	return &fake{
		env: map[string]string{"HOME": t.TempDir(), "EZC_HOME": t.TempDir()},
		wd:  t.TempDir(),
	}
}

func (f *fake) sys() System {
	return System{
		Stdout: &f.out,
		Stderr: &f.errOut,
		Stdin:  strings.NewReader(""),
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
		Version: "0.1.0",
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

func TestVersion(t *testing.T) {
	f := newFake(t)
	if err := f.run("version"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "ezc 0.1.0\n" {
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
	for _, want := range []string{"NAME — who", "not set", "SIZE — how many", "12 is above the maximum of 9"} {
		if !strings.Contains(f.errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, f.errOut.String())
		}
	}
	if f.out.Len() != 0 {
		t.Errorf("stdout %q", f.out.String())
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
	if !strings.Contains(f.errOut.String(), "12 is above the maximum of 9") {
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
	if err := f.run("env", "export", "--format", "dotenv"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "NAME=\"n\"\nSIZE=\"3\"\n" {
		t.Fatalf("dotenv %q", f.out.String())
	}
	f.out.Reset()
	if err := f.run("env", "export", "--format", "json"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "{\n  \"NAME\": \"n\",\n  \"SIZE\": \"3\"\n}\n" {
		t.Fatalf("json %q", f.out.String())
	}
	f.out.Reset()
	wantUsage(t, f.run("env", "export", "--format", "xml"), `unknown format "xml"`)
	if f.out.Len() != 0 {
		t.Fatalf("stdout %q", f.out.String())
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

func TestExportPrintsNothingOnFailure(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "B: ok\nA: $NOPE\n"})
	if err := f.run("env", "export"); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("want an error naming NOPE, got %v", err)
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

func TestExportHelpWarnsAboutDotenv(t *testing.T) {
	f := newFake(t)
	if err := f.run("help", "env", "export"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "must not be sourced") {
		t.Fatalf("got %q", f.out.String())
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
