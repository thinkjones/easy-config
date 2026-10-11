package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

const mToken = m1 + "  TOKEN: {type: secret, desc: t}\n"

func TestEnvReadsEncryptedFiles(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"common.yaml": ""})
	repo(t, f, map[string]string{"manifest.yaml": mToken, "env.local.yaml": "NAME: n\nSIZE: 3\n"})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t0k")
	if err := f.run("env"); err != nil {
		t.Fatal(err)
	}
	if got := f.errOut.String(); got != "environment local ✓ 3 vars from 2 files\n" {
		t.Fatalf("got %q", got)
	}
	f.out.Reset()
	if err := f.run("env", "export"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "export TOKEN='t0k'\n") {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestPrecedenceAcrossEncryptedFiles(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"env.local.yaml": "X: 4\n"})
	secretIn(t, f, "store", "common.enc.yaml", "X", "1")
	secretIn(t, f, "store", "env.local.enc.yaml", "X", "2")
	secretIn(t, f, "repo", "common.enc.yaml", "X", "3")
	if err := f.run("get", "X"); err != nil || f.out.String() != "4\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
	f.out.Reset()
	write(t, filepath.Join(f.wd, ".ezc", "env.local.yaml"), "")
	if err := f.run("get", "X"); err == nil || !strings.Contains(err.Error(), "X is a secret") || f.out.Len() != 0 {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
	if err := f.run("get", "X", "--reveal"); err != nil || f.out.String() != "3\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
}

func TestEncryptedValuesAreNotExpanded(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"env.local.yaml": ""})
	secretIn(t, f, "repo", "common.enc.yaml", "PW", "a$HOME$$b")
	secretIn(t, f, "repo", "common.enc.yaml", "TILDE", "~/x")
	if err := f.run("env", "export"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"export PW='a$HOME$$b'\n", "export TILDE='~/x'\n"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("want %q in %q", want, f.out.String())
		}
	}
}

func TestEncryptedValueIsReferencedByLaterPlaintext(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"env.local.yaml": "URL: https://u:$PW@h\n"})
	secretIn(t, f, "repo", "common.enc.yaml", "PW", "p")
	if err := f.run("env", "export"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "export URL='https://u:p@h'\n") {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestGetFromEncryptedFileNeedsReveal(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"env.local.yaml": ""})
	secretIn(t, f, "repo", "common.enc.yaml", "STRAY", "v")
	if err := f.run("get", "STRAY"); err == nil || !strings.Contains(err.Error(), "STRAY is a secret") || f.out.Len() != 0 {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
	if err := f.run("get", "STRAY", "--reveal"); err != nil || f.out.String() != "v\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
}

func TestNoKey(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": mToken, "env.local.yaml": "NAME: n\nSIZE: 3\n"})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t0k")
	f.backend.Locked = true
	for _, args := range [][]string{{"env"}, {"env", "export"}, {"list"}, {"get", "NAME"}, {"run", "--", "true"}} {
		err := f.run(args...)
		if !errors.Is(err, backend.ErrNoKey) || !strings.Contains(err.Error(), "common.enc.yaml") {
			t.Errorf("%v: got %v", args, err)
		}
		if f.out.Len() != 0 {
			t.Errorf("%v: stdout %q", args, f.out.String())
		}
	}
	if len(f.children) != 0 {
		t.Errorf("the command ran: %v", f.children)
	}
}

func TestListShowsEncryptedOrigin(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": mToken, "env.local.yaml": "NAME: n\nSIZE: 3\n"})
	secretIn(t, f, "repo", "common.enc.yaml", "TOKEN", "t0k")
	if err := f.run("list"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "TOKEN    secret  repo/common.enc.yaml\n") || strings.Contains(f.out.String()+f.errOut.String(), "t0k") {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestDefaultBackendIsSops(t *testing.T) {
	sys := newFake(t).sys()
	sys.Backend = nil
	if got := backendFor(sys).Name(); got != "sops" {
		t.Fatalf("got %q", got)
	}
}
