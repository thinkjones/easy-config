package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/registry"
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
		{"replace-me", "SIZE: placeholder, not a value"},
		{"", "SIZE: empty"},
	}
	for _, c := range cases {
		err := f.run("set", "SIZE", c.value)
		if err == nil || err.Error() != c.want {
			t.Errorf("%q: got %v", c.value, err)
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

// snapshot is the bytes of every file in the repo's .ezc folder, by name.
func snapshot(t *testing.T, f *fake) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.wd, ezcDir))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		out[e.Name()] = readFile(t, repoFile(f, e.Name()))
	}
	return out
}

func sameSnapshot(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("files changed: before %d, after %d", len(before), len(after))
	}
	for name, content := range before {
		if after[name] != content {
			t.Errorf("%s changed:\n%s\n---\n%s", name, content, after[name])
		}
	}
}

func TestSetValueFailureRestoresTheManifest(t *testing.T) {
	f := newFake(t)
	// The values file is not one ezc may edit, which fails the write after the manifest was declared.
	setRepo(t, f, map[string]string{"common.yaml": "A: &x 1\n"})
	before := snapshot(t, f)
	err := f.run("set", "NEW", "v", "--type", "str", "--desc", "d")
	if err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))
	if f.out.Len() != 0 {
		t.Fatalf("stdout %q", f.out.String())
	}
}

func TestSetSecretWithoutKeyLeavesTheManifestAndFilesAlone(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\n"})
	f.backend.Locked = true
	f.stdin = "v\n"
	before := snapshot(t, f)
	if err := f.run("set", "KEY", "--type", "secret", "--desc", "d"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestSetSecretOverAnUndeclaredStoredOneKeepsItWhenTheKeyIsMissing(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	secretIn(t, f, "repo", "common.enc.yaml", "KEY", "old")
	f.backend.Locked = true
	f.stdin = "new\n"
	before := snapshot(t, f)
	if err := f.run("set", "KEY", "--type", "secret", "--desc", "d"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestSetKeepsAFileWhoseCommentsAreAllItHolds(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"env.local.yaml": "# header\n", "common.yaml": "A: &x 1\n"})
	before := snapshot(t, f)
	if err := f.run("set", "NEW", "v", "--type", "str", "--desc", "d"); err == nil {
		t.Fatal("want an error")
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestSetArgumentsAreNeverEchoed(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	cases := [][]string{
		{"set", "TOKEN=s3cr3t"},
		{"unset", "TOKEN=s3cr3t"},
		{"set", "TOKEN", "-s3cr3t"},
		{"unset", "TOKEN", "-s3cr3t"},
		{"set", "NAME", "s3cr3t", "x"},
		{"unset", "NAME", "s3cr3t"},
		{"set", "NAME", "v", "--env", "s3cr3t/x"},
		{"set", "NEW", "v", "--type", "s3cr3t", "--desc", "d"},
		{"set", "NAME", "v", "--min", "s3cr3t"},
	}
	for _, args := range cases {
		err := f.run(args...)
		var u *UsageError
		if !errors.As(err, &u) {
			t.Errorf("%v: want a UsageError, got %v", args, err)
			continue
		}
		if strings.Contains(err.Error()+f.out.String()+f.errOut.String(), "s3cr3t") {
			t.Errorf("%v: echoed: %v", args, err)
		}
	}
	wantUsage(t, f.run("set", "TOKEN=x"), "NAME and VALUE are separate")
	wantUsage(t, f.run("set", "NAME", "-x"), "invalid or unrecognised flag. A value that starts with - must be piped on stdin.")
}

func TestSetIsRefusedBeforeAskingForASecretWithoutPolicy(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": mToken})
	f.tty = true
	if err := f.run("set", "TOKEN"); !errors.Is(err, backend.ErrNoPolicy) {
		t.Fatalf("got %v", err)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("prompted: %q", f.prompts)
	}
}

func TestSetInvalidUTF8Refused(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	err := f.run("set", "NAME", "\xff")
	if err == nil || !strings.Contains(err.Error(), "NAME: not valid UTF-8") {
		t.Fatalf("got %v", err)
	}
}

func declared(t *testing.T, f *fake, name string) bool {
	t.Helper()
	m, err := manifest.Load(repoFile(f, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, ok := m.Lookup(name)
	return ok
}

func TestUnsetPlaintext(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\nOTHER: o\n"})
	if err := f.run("unset", "NAME"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "unset NAME from repo/common.yaml\n" {
		t.Fatalf("got %q", got)
	}
	if !declared(t, f, "NAME") {
		t.Fatal("the declaration went")
	}
	if readFile(t, repoFile(f, "common.yaml")) != "OTHER: o\n" {
		t.Fatalf("got %q", readFile(t, repoFile(f, "common.yaml")))
	}
}

func TestUnsetUndeclare(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\n"})
	if err := f.run("unset", "NAME", "--undeclare"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != "unset NAME from repo/common.yaml\nremoved NAME from repo/manifest.yaml\n" {
		t.Fatalf("got %q", got)
	}
	m, err := manifest.Load(repoFile(f, "manifest.yaml"))
	if err != nil || len(m.Validate()) > 0 || declared(t, f, "NAME") {
		t.Fatalf("got %v %v", err, m.Validate())
	}
}

func TestUnsetUndeclareRefusedWhileAnotherFileHoldsTheName(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\n", "env.prod.yaml": "NAME: p\n"})
	before := snapshot(t, f)
	err := f.run("unset", "NAME", "--undeclare")
	if err == nil || !strings.Contains(err.Error(), "NAME is still set in repo/env.prod.yaml; unset it there first, or leave the declaration.") {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))

	f2 := newFake(t)
	setRepo(t, f2, map[string]string{"common.yaml": "NAME: n\n"})
	secretIn(t, f2, "repo", "env.prod.enc.yaml", "NAME", "p")
	err = f2.run("unset", "NAME", "--undeclare")
	if err == nil || !strings.Contains(err.Error(), "still set in repo/env.prod.enc.yaml") {
		t.Fatalf("got %v", err)
	}
}

func TestUnsetUndeclareWithNoValueRemovesJustTheDeclaration(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	if err := f.run("unset", "NAME", "--undeclare"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "removed NAME from repo/manifest.yaml\n" || declared(t, f, "NAME") {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestUnsetKeepsDeclarationWhenStillSet(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\n", "env.prod.yaml": "NAME: p\n"})
	if err := f.run("unset", "NAME"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset NAME from repo/common.yaml\n" || !declared(t, f, "NAME") {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestUnsetSecret(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t")
	if err := f.run("unset", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset TOKEN from repo/common.enc.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	missing(t, repoFile(f, "common.enc.yaml"))
}

func TestUnsetRemovesTheLeakedPlaintextCopyOfASecret(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "TOKEN: leaked\n"})
	f.stdin = "v\n"
	err := f.run("set", "TOKEN")
	if err == nil || !strings.Contains(err.Error(), "remove it with `ezc unset`") {
		t.Fatalf("got %v", err)
	}
	if err := f.run("unset", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	missing(t, repoFile(f, "common.yaml"))
	if err := f.run("set", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if v, _ := stored(t, f, "common.enc.yaml", "TOKEN"); v != "v" {
		t.Fatalf("got %q", v)
	}
}

func TestUnsetRemovesFromBothFilesOfTheBase(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "STRAY: a\nKEEP: k\n"})
	secretIn(t, f, "repo", "common.enc.yaml", "STRAY", "b")
	if err := f.run("unset", "STRAY"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset STRAY from repo/common.yaml\nunset STRAY from repo/common.enc.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestUnsetSecretNeedsTheKey(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t")
	secretIn(t, f, "repo", "common.enc.yaml", "OTHER", "o")
	f.backend.Locked = true
	if err := f.run("unset", "TOKEN"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	if !declared(t, f, "TOKEN") {
		t.Fatal("the declaration went")
	}
}

// The last name's removal deletes the file, which needs no key.
func TestUnsetTheLastSecretNeedsNoKey(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t")
	f.backend.Locked = true
	if err := f.run("unset", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	missing(t, repoFile(f, "common.enc.yaml"))
}

func TestUnsetUndeclareIgnoresAFileThatIsNotAValuesFile(t *testing.T) {
	f := newFake(t)
	// env.a.b.yaml names no environment, so nothing reads it.
	setRepo(t, f, map[string]string{"common.yaml": "NAME: n\n", "env.a.b.yaml": "- not\n- a mapping\n"})
	if err := f.run("unset", "NAME", "--undeclare"); err != nil {
		t.Fatal(err)
	}
	if declared(t, f, "NAME") {
		t.Fatal("still declared")
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
	for _, name := range []string{"NOPE", "NAME", "TOKEN"} {
		err := f.run("unset", name)
		want := name + " is not set in repo/common.yaml or repo/common.enc.yaml."
		if err == nil || err.Error() != want {
			t.Errorf("got %v", err)
		}
	}
}

func TestUnsetGlobalAndEnv(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": m1, "env.prod.yaml": "NAME: n\nSIZE: 3\n"})
	if err := f.run("unset", "NAME", "-g", "--env", "prod"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "unset NAME from store/env.prod.yaml\n" {
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
	wantUsage(t, f.run("unset", "A", "--type", "str"), "invalid or unrecognised flag.")
}

func TestSetStoresWhatWasTypedLiterally(t *testing.T) {
	for _, value := range []string{"a$b", "a$$b", "$HOME/x", "${X}", "${HOME}", "100%", "$(id)", "$", "a$", "~user/x", "x~/y"} {
		f := newFake(t)
		setRepo(t, f, map[string]string{"common.yaml": "X: other\n", "env.local.yaml": ""})
		if err := f.run("set", "NAME", value); err != nil {
			t.Fatalf("%q: set: %v", value, err)
		}
		f.out.Reset()
		if err := f.run("get", "NAME"); err != nil || f.out.String() != value+"\n" {
			t.Errorf("%q: get: %v, %q", value, err, f.out.String())
		}
	}
}

func TestSetWritesADollarDoubled(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"env.local.yaml": ""})
	f.stdin = "p$w$$\n"
	if err := f.run("set", "NAME"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repoFile(f, "common.yaml")); got != "NAME: p$$w$$$$\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetRefusesALiteralLeadingTilde(t *testing.T) {
	for _, value := range []string{"~", "~/x"} {
		f := newFake(t)
		setRepo(t, f, map[string]string{"env.local.yaml": ""})
		before := snapshot(t, f)
		err := f.run("set", "NAME", value)
		want := "NAME: a value that starts with ~ is expanded to your home folder when loaded. Pass --expand to keep that meaning."
		if err == nil || err.Error() != want {
			t.Fatalf("%q: got %v", value, err)
		}
		sameSnapshot(t, before, snapshot(t, f))
	}
}

func TestSetExpandKeepsReferences(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "HOST: h\n", "env.local.yaml": ""})
	if err := f.run("set", "NAME", "https://$HOST/${HOST}/$$5", "--expand"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repoFile(f, "common.yaml")); got != "HOST: h\nNAME: https://$HOST/${HOST}/$$5\n" {
		t.Fatalf("got %q", got)
	}
	f.out.Reset()
	if err := f.run("get", "NAME"); err != nil || f.out.String() != "https://h/h/$5\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
	if err := f.run("set", "NAME", "~/x", "--expand", "--env", "local"); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	if err := f.run("get", "NAME"); err != nil || f.out.String() != f.env["HOME"]+"/x\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
}

func TestSetExpandSkipsTheShapeCheckOnlyForAReference(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "N: 3\n", "env.local.yaml": ""})
	if err := f.run("set", "SIZE", "$N", "--expand"); err != nil {
		t.Fatal(err)
	}
	if err := f.run("set", "SIZE", "abc", "--expand"); err == nil || !strings.Contains(err.Error(), "SIZE: ") {
		t.Fatalf("got %v", err)
	}
	// Without --expand the same text is a literal, and not an integer.
	if err := f.run("set", "SIZE", "$N"); err == nil || !strings.Contains(err.Error(), "SIZE: ") {
		t.Fatalf("got %v", err)
	}
}

func TestSetExpandWithAnUnknownReferencePutsTheFileBack(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		args  []string
	}{
		{"unknown", map[string]string{"common.yaml": "# kept\nNAME: old\n", "env.local.yaml": ""}, []string{"set", "NAME", "x$NOPE-canary", "--expand"}},
		{"forward", map[string]string{"common.yaml": "NAME: old\n", "env.local.yaml": "LATER: l\n"}, []string{"set", "NAME", "${LATER}-canary", "--expand"}},
		{"new file", map[string]string{"env.local.yaml": ""}, []string{"set", "NAME", "$NOPE-canary", "--expand"}},
		{"new name", map[string]string{"env.local.yaml": ""}, []string{"set", "NEW", "$NOPE-canary", "--expand", "--type", "str", "--desc", "d"}},
		{"named environment", map[string]string{"env.prod.yaml": "A: 1\n"}, []string{"set", "NAME", "$NOPE-canary", "--expand", "--env", "prod"}},
	}
	for _, c := range cases {
		f := newFake(t)
		setRepo(t, f, c.files)
		before := snapshot(t, f)
		err := f.run(c.args...)
		if err == nil || !strings.Contains(err.Error(), c.args[1]+" references a name that is not defined") || !strings.Contains(err.Error(), filepath.Join(f.wd, ezcDir)) {
			t.Fatalf("%s: got %v", c.name, err)
		}
		if all := err.Error() + f.out.String() + f.errOut.String(); strings.Contains(all, "canary") || f.out.Len() != 0 {
			t.Fatalf("%s: the value was printed, or the write reported: %q", c.name, all)
		}
		sameSnapshot(t, before, snapshot(t, f))
	}
}

func TestSetExpandKeepsTheWriteWhenSomethingElseIsBroken(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"common.yaml": "HOST: h\n", "env.local.yaml": "B: $UNDEFINED\n"})
	if err := f.run("set", "NAME", "$HOST", "--expand"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repoFile(f, "common.yaml")); got != "HOST: h\nNAME: $HOST\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetExpandGlobalResolvesTheStore(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": m1, "common.yaml": "HOST: h\n"})
	setRepo(t, f, map[string]string{"common.yaml": "REPO_ONLY: r\n", "env.local.yaml": ""})
	if err := f.run("set", "-g", "NAME", "$HOST", "--expand"); err != nil {
		t.Fatal(err)
	}
	err := f.run("set", "-g", "NAME", "$REPO_ONLY", "--expand")
	if err == nil || !strings.Contains(err.Error(), "NAME references a name that is not defined") {
		t.Fatalf("got %v", err)
	}
	if got := readFile(t, filepath.Join(f.env["EZC_HOME"], "store", ezcDir, "common.yaml")); got != "HOST: h\nNAME: $HOST\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetExpandRefusedForASecret(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.tty, f.secret = true, "s"
	before := snapshot(t, f)
	wantUsage(t, f.run("set", "TOKEN", "--expand"), "ezc set: --expand does not apply to a secret; secrets are stored exactly as given.")
	wantUsage(t, f.run("set", "KEY", "--expand", "--type", "secret", "--desc", "d"), "--expand does not apply to a secret")
	if len(f.prompts) != 0 {
		t.Fatalf("prompted: %v", f.prompts)
	}
	sameSnapshot(t, before, snapshot(t, f))
}

// unsetGuardFix is a store with NAME set in it and repos registered with checkouts holding the given manifests;
// a manifest "" is a repo with no checkout here.
func unsetGuardFix(t *testing.T, manifests map[string]string) repoFix {
	t.Helper()
	x := repoStore(t, "")
	dirs := map[string]string{}
	ids := make([]string, 0, len(manifests))
	for id := range manifests {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		x.out("repo", "add", id)
		if m := manifests[id]; m != "" {
			top := t.TempDir()
			write(t, filepath.Join(top, ezcDir, "manifest.yaml"), m)
			dirs[id] = top
		}
	}
	x.setConfig(config.Config{User: "gene", Checkouts: dirs})
	x.push(x.store)
	return x
}

const (
	guardReq  = "schemaVersion: v1\nrequired:\n  NAME: {type: str, desc: u}\n"
	guardOpt  = "schemaVersion: v1\nrequired: {}\noptional:\n  NAME: {type: str, desc: u}\n"
	guardWhen = "schemaVersion: v1\nrequired:\n  MODE: {type: str, desc: m}\noptional:\n  NAME: {type: str, desc: u, requiredWhen: {var: MODE, equals: x}}\n"
)

func TestUnsetGlobalRefusedWhileARepoRequiresIt(t *testing.T) {
	for _, m := range []string{guardReq, guardWhen} {
		x := unsetGuardFix(t, map[string]string{"o/app": m, "o/b": guardOpt})
		head, before := x.git("rev-parse", "HEAD"), readFile(t, x.file("common.yaml"))
		x.f.out.Reset()
		err := x.f.run("unset", "-g", "NAME")
		if err == nil || err.Error() != "NAME is required by o/app. Pass --force to unset it anyway, or take it out of their manifests first." {
			t.Fatalf("got %v", err)
		}
		storeWriteExitsOne(t, err)
		if x.git("rev-parse", "HEAD") != head || readFile(t, x.file("common.yaml")) != before {
			t.Fatal("the refusal changed the store")
		}
		x.wantClean()
	}
}

func TestUnsetGlobalForceKeepsTheLines(t *testing.T) {
	x := unsetGuardFix(t, map[string]string{"o/app": guardReq, "o/opt": guardOpt, "o/other": ""})
	out := x.out("unset", "-g", "NAME", "--force")
	for _, want := range []string{"! NAME is required by o/app.\n", "! NAME is optional in o/opt.\n", "! not checked, no checkout here: o/other\n", "unset NAME from"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	x.wantCommit("unset NAME (common.yaml)", "common.yaml")
}

func TestUnsetGlobalOptionalAndUnchecked(t *testing.T) {
	x := unsetGuardFix(t, map[string]string{"o/app": guardOpt, "o/other": "", "o/bad": "schemaVersion: v1\nrequired: [\n"})
	out := x.out("unset", "-g", "NAME")
	for _, want := range []string{"! NAME is optional in o/app.\n", "! not checked, no checkout here: o/other\n", "! not checked, manifest cannot be read: o/bad\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	x.wantCommit("unset NAME (common.yaml)", "common.yaml")
}

// A repos.yaml nobody can load checks no repo: unset -g refuses, and --force gets past it with a ! line.
func TestUnsetGlobalWhenReposYamlCannotBeRead(t *testing.T) {
	x := unsetGuardFix(t, map[string]string{"o/app": guardReq})
	write(t, x.file(registry.File), "repos:\n  \"\\e[31mo/app\": {added: 2026-10-01, addedBy: gene}\n")
	x.git("commit", "--quiet", "-am", "break repos.yaml")
	x.push(x.store)
	head := x.git("rev-parse", "HEAD")
	err := x.f.run("unset", "-g", "NAME")
	if err == nil || err.Error() != "repos.yaml cannot be read, so no repo was checked for NAME. Run ezc repo list to see why, or pass --force to unset it anyway." {
		t.Fatalf("got %v", err)
	}
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("the refusal changed the store")
	}
	x.f.out.Reset()
	out := x.out("unset", "-g", "NAME", "--force")
	if !strings.HasPrefix(out, "! repos.yaml cannot be read, so no repo was checked for NAME.\n") || strings.Contains(out, "\x1b") {
		t.Fatalf("got %q", out)
	}
	x.wantCommit("unset NAME (common.yaml)", "common.yaml")
}

func TestUnsetInARepoRunsNoGuard(t *testing.T) {
	x := unsetGuardFix(t, map[string]string{"o/app": guardReq})
	write(t, filepath.Join(x.f.wd, ezcDir, "common.yaml"), "NAME: n\n")
	write(t, filepath.Join(x.f.wd, ezcDir, backend.FakePolicy), "")
	write(t, filepath.Join(x.f.wd, ezcDir, "manifest.yaml"), mToken)
	if out := x.out("unset", "NAME"); strings.Contains(out, "!") {
		t.Fatalf("got %q", out)
	}
}
