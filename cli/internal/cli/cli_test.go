package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
