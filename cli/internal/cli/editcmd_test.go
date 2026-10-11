package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// editFake is a fake on a terminal, in a repo whose editor writes content to the file it is given.
func editFake(t *testing.T, editor, content string) *fake {
	t.Helper()
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.tty = true
	f.env["EDITOR"] = editor
	f.execFn = func(c proc.Command) ([]byte, []byte, error) {
		if content == "" {
			return nil, nil, nil
		}
		return nil, nil, os.WriteFile(c.Args[len(c.Args)-1], []byte(content), 0o644)
	}
	return f
}

func TestEditNeedsAnEditor(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	f.tty = true
	err := f.run("edit")
	if err == nil || !strings.Contains(err.Error(), "set EDITOR") {
		t.Fatalf("got %v", err)
	}
}

func TestEditRefusesNonBlockingEditor(t *testing.T) {
	for editor, ok := range map[string]bool{
		"code": false, "cursor": false, "/usr/bin/subl -n": false,
		"code --wait": true, "code -w": true, "/usr/local/bin/code --wait": true, "nano": true, "vim": true,
	} {
		f := editFake(t, editor, "NAME: x\n")
		err := f.run("edit")
		if ok && err != nil {
			t.Errorf("%q: %v", editor, err)
		}
		if !ok && (err == nil || !strings.Contains(err.Error(), "returns before you finish")) {
			t.Errorf("%q: got %v", editor, err)
		}
		if !ok && len(f.execs) != 0 {
			t.Errorf("%q: the editor ran", editor)
		}
	}
	f := editFake(t, "code", "NAME: x\n")
	err := f.run("edit")
	if err == nil || !strings.Contains(err.Error(), "Use `code --wait`.") {
		t.Fatalf("got %v", err)
	}
}

func TestEditVisualWinsOverEditor(t *testing.T) {
	f := editFake(t, "nano", "NAME: x\n")
	f.env["VISUAL"] = "vim"
	if err := f.run("edit"); err != nil {
		t.Fatal(err)
	}
	if f.execs[0].Name != "vim" {
		t.Fatalf("ran %q", f.execs[0].Name)
	}
}

func TestEditPlaintext(t *testing.T) {
	f := editFake(t, "nano", "NAME: edited\n")
	if err := f.run("edit"); err != nil {
		t.Fatal(err)
	}
	if len(f.execs) != 1 {
		t.Fatalf("execs %v", f.execs)
	}
	c := f.execs[0]
	want := repoFile(f, "common.yaml")
	if c.Name != "nano" || c.Args[len(c.Args)-1] != want || !filepath.IsAbs(want) || !c.Interactive {
		t.Fatalf("got %+v, want path %s", c, want)
	}
	if f.out.String() != "edited repo/common.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	if readFile(t, want) != "NAME: edited\n" {
		t.Fatal("file not written")
	}
}

func TestEditWithArgsInEditor(t *testing.T) {
	f := editFake(t, "vim -u NONE", "NAME: x\n")
	if err := f.run("edit"); err != nil {
		t.Fatal(err)
	}
	c := f.execs[0]
	if c.Name != "vim" || len(c.Args) != 3 || c.Args[0] != "-u" || c.Args[1] != "NONE" || c.Args[2] != repoFile(f, "common.yaml") {
		t.Fatalf("got %+v", c)
	}
}

func TestEditEnvFile(t *testing.T) {
	f := editFake(t, "nano", "NAME: x\n")
	if err := f.run("edit", "--env", "prod"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "edited repo/env.prod.yaml\n" || f.execs[0].Args[0] != repoFile(f, "env.prod.yaml") {
		t.Fatalf("got %q %+v", f.out.String(), f.execs[0])
	}
}

func TestEditBadEnvName(t *testing.T) {
	f := editFake(t, "nano", "")
	wantUsage(t, f.run("edit", "--env", "a b"), "not a valid environment name")
	wantUsage(t, f.run("edit", "--env", "enc"), "reserved")
	if len(f.execs) != 0 {
		t.Fatal("the editor ran")
	}
}

func TestEditInvalidResult(t *testing.T) {
	for _, content := range []string{"A: [1]\n", "EZC_ENV: x\n", "APP_ENV: x\n", "1BAD: x\n", "A: ok\nA: again\n"} {
		f := editFake(t, "nano", content)
		err := f.run("edit")
		var x *ExitError
		if !errors.As(err, &x) || x.Code != 1 {
			t.Fatalf("%q: got %v", content, err)
		}
		if !strings.Contains(f.errOut.String(), "repo/common.yaml was saved but is not valid; fix it with `ezc edit`.") {
			t.Fatalf("%q: stderr %q", content, f.errOut.String())
		}
		if f.out.String() != "" {
			t.Fatalf("stdout %q", f.out.String())
		}
	}
}

func TestEditInvalidResultNamesNoValue(t *testing.T) {
	f := editFake(t, "nano", "A: tok3n\n  B: tok3n\n")
	f.run("edit")
	if strings.Contains(f.out.String()+f.errOut.String(), "tok3n") {
		t.Fatalf("a value was printed: %q", f.errOut.String())
	}
}

func TestEditCreatedButLeftEmptyIsRemoved(t *testing.T) {
	f := editFake(t, "nano", "")
	if err := f.run("edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repoFile(f, "common.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat: %v", err)
	}
}

func TestEditExistingEmptyFileIsKept(t *testing.T) {
	f := editFake(t, "nano", "")
	write(t, repoFile(f, "common.yaml"), "")
	if err := f.run("edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repoFile(f, "common.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestEditorFailure(t *testing.T) {
	f := editFake(t, "nano -x", "")
	f.execFn = func(proc.Command) ([]byte, []byte, error) { return nil, nil, errors.New("exit status 1") }
	err := f.run("edit")
	var x *ExitError
	if !errors.As(err, &x) || x.Code != 1 {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(f.errOut.String(), "nano exited with an error; repo/common.yaml may not have been saved.") {
		t.Fatalf("stderr %q", f.errOut.String())
	}
	if _, err := os.Stat(repoFile(f, "common.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the created empty file was kept")
	}
}

func TestEditSecrets(t *testing.T) {
	f := editFake(t, "nano", "")
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t1")
	var path string
	f.backend.EditFunc = func(p []backend.Pair) []backend.Pair { return append(p, backend.Pair{Name: "B", Value: "b1"}) }
	if err := f.run("edit", "--secrets"); err != nil {
		t.Fatal(err)
	}
	path = repoFile(f, "common.enc.yaml")
	if f.out.String() != "edited repo/common.enc.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	if v, _ := stored(t, f, "common.enc.yaml", "B"); v != "b1" {
		t.Fatalf("EditFunc did not run on %s", path)
	}
	if len(f.execs) != 0 {
		t.Fatal("ezc ran the editor itself")
	}
}

func TestEditSecretsCreatesTheFile(t *testing.T) {
	f := editFake(t, "nano", "")
	setRepo(t, f, map[string]string{})
	f.backend.EditFunc = func(p []backend.Pair) []backend.Pair { return []backend.Pair{{Name: "A", Value: "a"}} }
	if err := f.run("edit", "--secrets", "--env", "prod"); err != nil {
		t.Fatal(err)
	}
	if v, _ := stored(t, f, "env.prod.enc.yaml", "A"); v != "a" {
		t.Fatal("not created")
	}
}

func TestEditSecretsWithoutPolicy(t *testing.T) {
	f := editFake(t, "nano", "")
	if err := os.Remove(repoFile(f, backend.FakePolicy)); err != nil {
		t.Fatal(err)
	}
	if err := f.run("edit", "--secrets"); !errors.Is(err, backend.ErrNoPolicy) {
		t.Fatalf("got %v", err)
	}
}

func TestEditSecretsWithoutKey(t *testing.T) {
	f := editFake(t, "nano", "")
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t1")
	f.backend.Locked = true
	called := false
	f.backend.EditFunc = func(p []backend.Pair) []backend.Pair { called = true; return p }
	if err := f.run("edit", "--secrets"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("the editor was opened")
	}
	if strings.Contains(f.out.String()+f.errOut.String(), "t1") {
		t.Fatal("a value was printed")
	}
}

// leakyBackend's Edit leaves a name in the file without encrypting it.
type leakyBackend struct{ *backend.Fake }

func (b leakyBackend) Edit(path string) error {
	return os.WriteFile(path, []byte("TOKEN: FAKE[dDE=]\nLEAK: plain\n"), 0o644)
}

func TestEditSecretsLeavingAnUnencryptedName(t *testing.T) {
	f := editFake(t, "nano", "")
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t1")
	sys := f.sys()
	sys.Backend = leakyBackend{f.backend}
	err := Run([]string{"edit", "--secrets"}, sys)
	if err == nil || !strings.Contains(err.Error(), "repo/common.enc.yaml: LEAK is not encrypted; set it again with `ezc set LEAK`.") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "plain") || f.out.String() != "" {
		t.Fatalf("out %q err %v", f.out.String(), err)
	}
}

func TestEditNeedsATerminal(t *testing.T) {
	f := editFake(t, "nano", "")
	f.tty = false
	if err := f.run("edit"); err == nil || !strings.Contains(err.Error(), "ezc edit needs a terminal") {
		t.Fatalf("got %v", err)
	}
	delete(f.env, "EDITOR")
	if err := f.run("edit"); err == nil || !strings.Contains(err.Error(), "ezc edit needs a terminal") {
		t.Fatalf("without an editor: got %v", err)
	}
}

func TestEditGlobal(t *testing.T) {
	f := editFake(t, "nano", "NAME: x\n")
	store(t, f, map[string]string{"manifest.yaml": empty})
	if err := f.run("edit", "-g"); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.env["EZC_HOME"], "store", ".ezc", "common.yaml")
	if f.execs[0].Args[0] != want || f.out.String() != "edited store/common.yaml\n" {
		t.Fatalf("got %+v %q", f.execs[0], f.out.String())
	}
}

func TestEditGlobalNeedsAStore(t *testing.T) {
	f := editFake(t, "nano", "")
	err := f.run("edit", "-g")
	if err == nil || !strings.Contains(err.Error(), "no store at") {
		t.Fatalf("got %v", err)
	}
}
