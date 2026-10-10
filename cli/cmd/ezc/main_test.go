package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ezc-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "ezc")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		args   []string
		code   int
		stderr string
	}{
		{[]string{"version"}, 0, ""},
		{[]string{"nope"}, 2, `ezc: unknown command "nope"`},
		{[]string{"version", "extra"}, 2, "unexpected argument"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			cmd := exec.Command(binary, c.args...)
			cmd.Env = []string{"HOME=" + t.TempDir(), "EZC_HOME=" + t.TempDir()}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			code := 0
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != c.code {
				t.Errorf("exit %d, want %d", code, c.code)
			}
			if !strings.Contains(stderr.String(), c.stderr) {
				t.Errorf("stderr %q lacks %q", stderr.String(), c.stderr)
			}
		})
	}
}
