//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/proc"
)

func TestExecCapturesStreamsSeparately(t *testing.T) {
	out, errOut, err := execCommand(proc.Command{Name: "sh", Args: []string{"-c", "printf out; printf err >&2"}})
	if err != nil || string(out) != "out" || string(errOut) != "err" {
		t.Fatalf("got %q, %q, %v", out, errOut, err)
	}
}

func TestExecFeedsStdin(t *testing.T) {
	out, _, err := execCommand(proc.Command{Name: "cat", Stdin: []byte("abc")})
	if err != nil || string(out) != "abc" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestExecUsesDirAndEnv(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := execCommand(proc.Command{
		Name: "sh", Args: []string{"-c", `pwd; printf %s "$X"`},
		Dir: dir, Env: []string{"X=1", "PATH=" + os.Getenv("PATH")},
	})
	if err != nil || string(out) != dir+"\n1" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestExecNonZeroIsError(t *testing.T) {
	_, errOut, err := execCommand(proc.Command{Name: "sh", Args: []string{"-c", "printf oops >&2; exit 3"}})
	if err == nil || string(errOut) != "oops" {
		t.Fatalf("got %q, %v", errOut, err)
	}
}

func TestExecNilEnvInheritsProcessEnv(t *testing.T) {
	t.Setenv("EZC_T", "v")
	out, _, err := execCommand(proc.Command{Name: "sh", Args: []string{"-c", `printf %s "$EZC_T"`}})
	if err != nil || string(out) != "v" {
		t.Fatalf("got %q, %v", out, err)
	}
}
