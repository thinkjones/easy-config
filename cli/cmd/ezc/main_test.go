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
	if code != 1 || out != "" || !strings.Contains(errOut, "above the maximum of 9") {
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

func TestShimLoadsTheEnvironment(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	env := []string{"HOME=" + t.TempDir(), "EZC_HOME=" + t.TempDir(), "PATH=" + filepath.Dir(binary) + string(os.PathListSeparator) + os.Getenv("PATH")}
	run := func(name string, args ...string) (string, int) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.Output()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	if _, code := run(binary, "repo", "init"); code != 0 {
		t.Fatalf("repo init: exit %d", code)
	}
	local := filepath.Join(dir, ".ezc", "env.local.yaml")
	f, err := os.OpenFile(local, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("GREETING: hello\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if out, code := run(sh, "-c", `. .ezc/env.sh; printf %s "$GREETING"`); out != "hello" || code != 0 {
		t.Fatalf("shim: exit %d stdout %q", code, out)
	}
	if err := os.WriteFile(local, []byte("GREETING: $NOPE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A broken config loads nothing and says nothing. The first half of this
	// test showed the shim finds ezc, so an empty GREETING is ezc's doing.
	if out, code := run(sh, "-c", `. .ezc/env.sh; echo "ok${GREETING}"`); out != "ok\n" || code != 0 {
		t.Fatalf("broken config: exit %d stdout %q", code, out)
	}
}

// An ezc that mise installed is a mise shim, and mise loads the repo's environment (the shim) before it runs the
// tool: the shim's own ezc would load the shim again, without end. The fake here does what that shim does, and gives
// up after a few rounds so that a failing test ends.
func TestShimIsNotLoadedAgainByTheEzcItRuns(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir, bin := t.TempDir(), t.TempDir()
	rounds := filepath.Join(t.TempDir(), "rounds")
	fake := "#!/bin/sh\n" +
		"echo x >> \"$ROUNDS\"\n" +
		"[ \"$(wc -l < \"$ROUNDS\")\" -gt 5 ] && exit 0\n" +
		". \"$REPO/.ezc/env.sh\"\n" +
		"exec \"$REAL\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ezc"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + t.TempDir(), "EZC_HOME=" + t.TempDir(), "ROUNDS=" + rounds, "REPO=" + dir, "REAL=" + binary,
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	init := exec.Command(binary, "repo", "init")
	init.Dir, init.Env = dir, env
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("repo init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ezc", "env.local.yaml"), []byte("GREETING: hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-c", `. .ezc/env.sh; printf %s "$GREETING"`)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(rounds)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), "x"); n != 1 || string(out) != "hello" {
		t.Fatalf("the shim ran ezc %d times (want 1), stdout %q", n, out)
	}
}
