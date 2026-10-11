//go:build unix

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ezcRun starts `ezc run -- args...` outside any repo and returns the finished command with its output.
func ezcRun(t *testing.T, args ...string) (cmd *exec.Cmd, stdout, stderr string, code int) {
	t.Helper()
	cmd = exec.Command(binary, append([]string{"run", "--"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"HOME=" + t.TempDir(), "EZC_HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	var exit *exec.ExitError
	if err := cmd.Run(); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return cmd, out.String(), errOut.String(), code
}

// The command replaces ezc, so a signal sent to the process that was started reaches the command.
func TestRunReplacesItselfWithTheCommand(t *testing.T) {
	cmd, out, errOut, code := ezcRun(t, "sh", "-c", "echo $$")
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	if got, want := strings.TrimSpace(out), strconv.Itoa(cmd.Process.Pid); got != want {
		t.Errorf("the command ran as pid %s; ezc was started as pid %s", got, want)
	}
	if _, _, _, code := ezcRun(t, "sh", "-c", "exit 7"); code != 7 {
		t.Errorf("exit %d, want 7", code)
	}
}

func TestRunCommandNotFound(t *testing.T) {
	for _, name := range []string{"ezc-no-such-command", "./no/such/file"} {
		_, out, errOut, code := ezcRun(t, name)
		if code != 127 || out != "" || !strings.HasSuffix(errOut, "ezc: "+name+": command not found\n") {
			t.Errorf("%s: exit %d stdout %q stderr %q", name, code, out, errOut)
		}
	}
}

func TestRunCommandNotExecutable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{file, t.TempDir()} {
		_, out, errOut, code := ezcRun(t, name)
		if code != 126 || out != "" || !strings.Contains(errOut, "ezc: "+name+": ") {
			t.Errorf("%s: exit %d stdout %q stderr %q", name, code, out, errOut)
		}
	}
}
