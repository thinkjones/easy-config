package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

// setRepo is a repo with manifest (else m1 and a TOKEN secret) and an encryption policy.
func setRepo(t *testing.T, f *fake, files map[string]string) {
	t.Helper()
	if _, ok := files["manifest.yaml"]; !ok {
		files["manifest.yaml"] = mToken
	}
	repo(t, f, files)
	write(t, filepath.Join(f.wd, ezcDir, backend.FakePolicy), "")
}

func repoFile(f *fake, name string) string { return filepath.Join(f.wd, ezcDir, name) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func missing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s should not exist: %v", path, err)
	}
}

// stored is the fake backend's decrypted value of name in a repo file.
func stored(t *testing.T, f *fake, file, name string) (string, bool) {
	t.Helper()
	pairs, err := f.backend.Decrypt(repoFile(f, file))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		if p.Name == name {
			return p.Value, true
		}
	}
	return "", false
}

func TestSetPlaintext(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"env.local.yaml": ""})
	if err := f.run("set", "NAME", "alice"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "set NAME in repo/common.yaml\n" {
		t.Fatalf("got %q", got)
	}
	if got := readFile(t, repoFile(f, "common.yaml")); got != "NAME: alice\n" {
		t.Fatalf("got %q", got)
	}
	f.out.Reset()
	if err := f.run("get", "NAME"); err != nil || f.out.String() != "alice\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
}

func TestSetEnvFile(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	if err := f.run("set", "NAME", "bob", "--env", "prod"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "set NAME in repo/env.prod.yaml\n" || readFile(t, repoFile(f, "env.prod.yaml")) != "NAME: bob\n" {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestSetDeclaresANewName(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	if err := f.run("set", "API_URL", "https://x", "--type", "url", "--desc", "Base URL"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "set API_URL in repo/common.yaml\ndeclared API_URL (url) in repo/manifest.yaml\n" {
		t.Fatalf("got %q", got)
	}
	m, err := manifest.Load(repoFile(f, "manifest.yaml"))
	if err != nil || len(m.Validate()) > 0 {
		t.Fatalf("got %v %v", err, m.Validate())
	}
	if v, ok := m.Required["API_URL"]; !ok || v.Type != manifest.TypeURL || v.Desc != "Base URL" {
		t.Fatalf("got %v", m.Required)
	}
	if err := f.run("set", "DEBUG", "yes", "--type", "bool", "--desc", "d", "--optional"); err != nil {
		t.Fatal(err)
	}
	m, _ = manifest.Load(repoFile(f, "manifest.yaml"))
	if _, ok := m.Optional["DEBUG"]; !ok {
		t.Fatalf("got %v", m.Optional)
	}
}

func TestSetDeclaresAnIntWithBoundsAndAnEnum(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	if err := f.run("set", "N", "5", "--type", "int", "--desc", "d", "--min", "1", "--max", "9"); err != nil {
		t.Fatal(err)
	}
	if err := f.run("set", "MODE", "fast", "--type", "enum", "--desc", "d", "--values", "fast,slow"); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Load(repoFile(f, "manifest.yaml"))
	if n := m.Required["N"]; n.Min == nil || *n.Min != 1 || *n.Max != 9 {
		t.Fatalf("got %+v", n)
	}
	if got := m.Required["MODE"].Values; len(got) != 2 || got[1] != "slow" {
		t.Fatalf("got %v", got)
	}
	err := f.run("set", "MODE", "medium")
	if err == nil || !strings.Contains(err.Error(), "MODE: not one of") {
		t.Fatalf("got %v", err)
	}
}

func TestSetInvalidDeclarationWritesNothing(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	before := readFile(t, repoFile(f, "manifest.yaml"))
	err := f.run("set", "N", "5", "--type", "str", "--desc", "d", "--min", "1")
	wantUsage(t, err, "min/max")
	if readFile(t, repoFile(f, "manifest.yaml")) != before {
		t.Fatal("the manifest changed")
	}
	missing(t, repoFile(f, "common.yaml"))
}

func TestSetUndeclaredNeedsTypeAndDesc(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	wantUsage(t, f.run("set", "NEW", "v"), "is not declared")
	wantUsage(t, f.run("set", "NEW", "v", "--type", "str"), "is not declared")
	missing(t, repoFile(f, "common.yaml"))
}

func TestSetTypeContradiction(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	err := f.run("set", "NAME", "v", "--type", "int")
	if err == nil || !strings.Contains(err.Error(), "contradicts") {
		t.Fatalf("got %v", err)
	}
	if err := f.run("set", "NAME", "v", "--type", "str"); err != nil {
		t.Fatalf("the declared type is accepted: %v", err)
	}
}

func TestSetDeclarationFlagsOnADeclaredNameRefused(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	for _, flags := range [][]string{{"--desc", "x"}, {"--optional"}, {"--values", "a,b"}, {"--min", "1"}, {"--max", "2"}} {
		err := f.run(append([]string{"set", "NAME", "v"}, flags...)...)
		wantUsage(t, err, "NAME is already declared; edit ")
	}
	missing(t, repoFile(f, "common.yaml"))
}

func TestSetValidatesTheValue(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	cases := []struct{ value, want string }{
		{"12", "SIZE: above the maximum of 9"},
		{"replace-me", "SIZE: placeholder"},
		{"", "SIZE: empty"},
	}
	for _, c := range cases {
		err := f.run("set", "SIZE", c.value)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v", c.value, err)
		} else if c.value != "" && strings.Contains(err.Error(), c.value+" ") {
			t.Errorf("the error carries the value: %v", err)
		}
	}
	err := f.run("set", "NAME", "a\x00b")
	if err == nil || !strings.Contains(err.Error(), "NAME: ") || strings.Contains(err.Error(), "a\x00b") {
		t.Fatalf("got %v", err)
	}
	missing(t, repoFile(f, "common.yaml"))
}

func TestSetSecretFromPrompt(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.tty, f.secret = true, "s3cr3t"
	if err := f.run("set", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "set TOKEN in repo/common.enc.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	if v, ok := stored(t, f, "common.enc.yaml", "TOKEN"); !ok || v != "s3cr3t" {
		t.Fatalf("got %q, %v", v, ok)
	}
	if len(f.prompts) != 1 || f.prompts[0] != "Value for TOKEN: " {
		t.Fatalf("prompts %q", f.prompts)
	}
	if strings.Contains(f.out.String()+f.errOut.String(), "s3cr3t") {
		t.Fatal("the secret was printed")
	}
}

func TestSetSecretFromStdin(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.stdin = "s3cr3t\n"
	if err := f.run("set", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if v, _ := stored(t, f, "common.enc.yaml", "TOKEN"); v != "s3cr3t" {
		t.Fatalf("got %q", v)
	}
	f.stdin = "a\n\n"
	if err := f.run("set", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if v, _ := stored(t, f, "common.enc.yaml", "TOKEN"); v != "a\n" {
		t.Fatalf("got %q", v)
	}
	f.stdin = "crlf\r\n"
	if err := f.run("set", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if v, _ := stored(t, f, "common.enc.yaml", "TOKEN"); v != "crlf" {
		t.Fatalf("got %q", v)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("prompted: %q", f.prompts)
	}
}

func TestSetPlaintextFromStdin(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.stdin = "alice\n"
	if err := f.run("set", "NAME"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, repoFile(f, "common.yaml")) != "NAME: alice\n" {
		t.Fatalf("got %q", readFile(t, repoFile(f, "common.yaml")))
	}
	f.tty, f.secret = true, "bob"
	if err := f.run("set", "NAME"); err != nil || len(f.prompts) != 1 || f.prompts[0] != "Value for NAME: " {
		t.Fatalf("got %v, %q", err, f.prompts)
	}
}

func TestSetSecretOnCommandLineRefused(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	err := f.run("set", "TOKEN", "s3cr3t")
	wantUsage(t, err, "never given on the command line")
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatal("the error carries the secret")
	}
	missing(t, repoFile(f, "common.enc.yaml"))
}

func TestSetNewSecret(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.stdin = "k3y\n"
	if err := f.run("set", "KEY", "--type", "secret", "--desc", "d"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "set KEY in repo/common.enc.yaml\ndeclared KEY (secret) in repo/manifest.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	if v, _ := stored(t, f, "common.enc.yaml", "KEY"); v != "k3y" {
		t.Fatalf("got %q", v)
	}
	m, _ := manifest.Load(repoFile(f, "manifest.yaml"))
	if !m.Required["KEY"].IsSecret() {
		t.Fatalf("got %v", m.Required)
	}
	wantUsage(t, f.run("set", "KEY2", "v", "--type", "secret", "--desc", "d"), "never given on the command line")
}

func TestSetSecretWithoutPolicy(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": mToken})
	f.stdin = "v\n"
	err := f.run("set", "TOKEN")
	if !errors.Is(err, backend.ErrNoPolicy) || !strings.Contains(err.Error(), `See "Encryption" in the README.`) {
		t.Fatalf("got %v", err)
	}
	err = f.run("set", "KEY", "--type", "secret", "--desc", "d")
	if !errors.Is(err, backend.ErrNoPolicy) {
		t.Fatalf("got %v", err)
	}
	missing(t, repoFile(f, "common.enc.yaml"))
	missing(t, repoFile(f, "common.yaml"))
	if readFile(t, repoFile(f, "manifest.yaml")) != mToken {
		t.Fatal("the manifest changed")
	}
}

func TestSetSecretWithoutKey(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.backend.Locked = true
	f.stdin = "v\n"
	err := f.run("set", "KEY", "--type", "secret", "--desc", "d")
	if !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	missing(t, repoFile(f, "common.enc.yaml"))
	if readFile(t, repoFile(f, "manifest.yaml")) != mToken {
		t.Fatal("the manifest changed")
	}
}

func TestSetAcrossTheBoundaryRefused(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "TOKEN: leaked\n", "env.prod.yaml": ""})
	f.stdin = "v\n"
	err := f.run("set", "TOKEN")
	if err == nil || !strings.Contains(err.Error(), "TOKEN is already in repo/common.yaml") || !strings.Contains(err.Error(), "across the secret boundary") {
		t.Fatalf("got %v", err)
	}
	missing(t, repoFile(f, "common.enc.yaml"))

	secretIn(t, f, "repo", "common.enc.yaml", "NAME", "x")
	err = f.run("set", "NAME", "plain")
	if err == nil || !strings.Contains(err.Error(), "NAME is already in repo/common.enc.yaml") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(readFile(t, repoFile(f, "common.yaml")), "NAME") {
		t.Fatal("the name was written in plaintext")
	}
}

func TestSetGlobal(t *testing.T) {
	f := newFake(t)
	err := f.run("set", "NAME", "v", "-g")
	if err == nil || !strings.Contains(err.Error(), "no store at ") {
		t.Fatalf("got %v", err)
	}
	store(t, f, map[string]string{"manifest.yaml": m1})
	if err := f.run("set", "NAME", "v", "-g"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "set NAME in store/common.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	p, _ := storePaths(f.sys())
	if readFile(t, filepath.Join(p.StoreEzc(), "common.yaml")) != "NAME: v\n" {
		t.Fatal("not written to the store")
	}
}

func TestSetGlobalWithoutManifest(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": ""})
	err := f.run("set", "NEW", "v", "-g", "--type", "str", "--desc", "d")
	if err == nil || !strings.Contains(err.Error(), "has no manifest.yaml.") {
		t.Fatalf("got %v", err)
	}
	p, _ := storePaths(f.sys())
	if readFile(t, filepath.Join(p.StoreEzc(), "common.yaml")) != "" {
		t.Fatal("a value was written")
	}
}

func TestSetSelectorRefused(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	wantUsage(t, f.run("set", "APP_ENV", "x"), "names the environment")
	wantUsage(t, f.run("set", "bad-name", "x"), "not a valid variable name")
}

func TestSetBadEnvName(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	wantUsage(t, f.run("set", "NAME", "x", "--env", "enc"), "reserved")
	wantUsage(t, f.run("set", "NAME", "x", "--env", "a/b"), "not a valid environment name")
}

// mAliased is a valid manifest whose required and optional tables are one node, so declaring a name in
// either reads back as declared in both: Declare fails after the value is written.
const mAliased = "schemaVersion: v1\nrequired: &r {}\noptional: *r\n"

func TestSetDeclareFailureUndoesTheValue(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"manifest.yaml": mAliased, "common.yaml": "OTHER: o\n"})
	err := f.run("set", "NEW", "v", "--type", "str", "--desc", "d")
	if err == nil {
		t.Fatal("want an error")
	}
	if got := readFile(t, repoFile(f, "common.yaml")); got != "OTHER: o\n" {
		t.Fatalf("got %q", got)
	}
	if readFile(t, repoFile(f, "manifest.yaml")) != mAliased {
		t.Fatal("the manifest changed")
	}
	if f.out.Len() != 0 {
		t.Fatalf("stdout %q", f.out.String())
	}
}

func TestSetDeclareFailureUndoesTheSecret(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"manifest.yaml": mAliased})
	f.stdin = "v\n"
	if err := f.run("set", "KEY", "--type", "secret", "--desc", "d"); err == nil {
		t.Fatal("want an error")
	}
	missing(t, repoFile(f, "common.enc.yaml"))
}

func TestUnsetPlaintext(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\nOTHER: o\n"})
	if err := f.run("unset", "NAME"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "unset NAME from repo/common.yaml\nremoved NAME from repo/manifest.yaml\n" {
		t.Fatalf("got %q", got)
	}
	m, err := manifest.Load(repoFile(f, "manifest.yaml"))
	if err != nil || len(m.Validate()) > 0 {
		t.Fatalf("got %v %v", err, m.Validate())
	}
	if _, ok := m.Lookup("NAME"); ok {
		t.Fatal("still declared")
	}
	if readFile(t, repoFile(f, "common.yaml")) != "OTHER: o\n" {
		t.Fatalf("got %q", readFile(t, repoFile(f, "common.yaml")))
	}
}

func TestUnsetKeepsDeclarationWhenStillSet(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\n", "env.prod.yaml": "NAME: p\n"})
	if err := f.run("unset", "NAME"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset NAME from repo/common.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	m, _ := manifest.Load(repoFile(f, "manifest.yaml"))
	if _, ok := m.Lookup("NAME"); !ok {
		t.Fatal("the declaration went")
	}
}

func TestUnsetKeepsDeclarationWhenAnEncryptedFileHoldsIt(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\n"})
	secretIn(t, f, "repo", "env.prod.enc.yaml", "NAME", "p")
	if err := f.run("unset", "NAME"); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Load(repoFile(f, "manifest.yaml"))
	if _, ok := m.Lookup("NAME"); !ok {
		t.Fatal("the declaration went")
	}
}

func TestUnsetSecret(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t")
	if err := f.run("unset", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset TOKEN from repo/common.enc.yaml\nremoved TOKEN from repo/manifest.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	missing(t, repoFile(f, "common.enc.yaml"))
}

func TestUnsetSecretNeedsTheKey(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t")
	f.backend.Locked = true
	if err := f.run("unset", "TOKEN"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	m, _ := manifest.Load(repoFile(f, "manifest.yaml"))
	if _, ok := m.Lookup("TOKEN"); !ok {
		t.Fatal("the declaration went")
	}
}

func TestUnsetUndeclaredFindsEitherFile(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	secretIn(t, f, "repo", "common.enc.yaml", "STRAY", "v")
	if err := f.run("unset", "STRAY"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset STRAY from repo/common.enc.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	f.out.Reset()
	repo(t, f, map[string]string{"common.yaml": "STRAY: v\n"})
	if err := f.run("unset", "STRAY"); err != nil || f.out.String() != "unset STRAY from repo/common.yaml\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
}

func TestUnsetNotSet(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	err := f.run("unset", "NOPE")
	if err == nil || !strings.Contains(err.Error(), "NOPE is not set in repo/common.yaml") {
		t.Fatalf("got %v", err)
	}
	err = f.run("unset", "NAME")
	if err == nil || !strings.Contains(err.Error(), "NAME is not set in repo/common.yaml.") {
		t.Fatalf("got %v", err)
	}
	err = f.run("unset", "TOKEN")
	if err == nil || !strings.Contains(err.Error(), "TOKEN is not set in repo/common.enc.yaml.") {
		t.Fatalf("got %v", err)
	}
}

func TestUnsetGlobalAndEnv(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": m1, "env.prod.yaml": "NAME: n\nSIZE: 3\n"})
	if err := f.run("unset", "NAME", "-g", "--env", "prod"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset NAME from store/env.prod.yaml\nremoved NAME from store/manifest.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestSetAndUnsetRejectArguments(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	wantUsage(t, f.run("set"), "give a variable name")
	wantUsage(t, f.run("set", "A", "b", "c"), "give")
	wantUsage(t, f.run("unset"), "give")
	wantUsage(t, f.run("unset", "A", "b"), "give")
	wantUsage(t, f.run("unset", "A", "--type", "str"), "unknown flag")
}
