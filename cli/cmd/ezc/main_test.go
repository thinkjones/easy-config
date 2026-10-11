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

func TestEnvAndRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	ezc := filepath.Join(dir, ".ezc")
	if err := os.MkdirAll(ezc, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"manifest.yaml":  "schemaVersion: v1\nrequired:\n  NAME: {type: str, desc: who}\n  SIZE: {type: int, min: 1, max: 9, desc: how many}\n",
		"env.local.yaml": "SIZE: 12\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(ezc, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (stdout, stderr string, code int) {
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		cmd.Env = []string{"HOME=" + t.TempDir(), "EZC_HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		var exit *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), errOut.String(), code
	}

	out, errOut, code := run("env")
	if code != 1 || out != "" || !strings.Contains(errOut, "12 is above the maximum of 9") {
		t.Fatalf("env: exit %d stdout %q stderr %q", code, out, errOut)
	}
	if _, _, code := run("run", "--", "sh", "-c", "exit 3"); code != 3 {
		t.Fatalf("run exit: %d", code)
	}
	out, errOut, code = run("run", "--", "sh", "-c", `printf %s "$SIZE"`)
	if code != 0 || out != "12" {
		t.Fatalf("run printf: exit %d stdout %q stderr %q", code, out, errOut)
	}
}
