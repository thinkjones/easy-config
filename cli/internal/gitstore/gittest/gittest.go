// Package gittest runs the real git for tests, cut off from the user's git
// config and home.
package gittest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// Name and Email are the identity WithIdentity configures.
const (
	Name  = "Store Tester"
	Email = "store.tester@example.invalid"
)

// Runner runs git with HOME a temporary folder, GIT_CONFIG_GLOBAL an empty
// temporary file and GIT_CONFIG_NOSYSTEM=1, whatever the command's own
// environment says. Any other program fails the test.
func Runner(t *testing.T) proc.Runner {
	t.Helper()
	return runner(t, "")
}

// NoIdentity is Runner for a machine where git has no identity and refuses to guess one.
func NoIdentity(t *testing.T) proc.Runner {
	t.Helper()
	return runner(t, "[user]\n\tuseConfigOnly = true\n")
}

// WithIdentity is Runner for a machine whose git config names Name and Email.
func WithIdentity(t *testing.T) proc.Runner {
	t.Helper()
	return runner(t, fmt.Sprintf("[user]\n\tname = %s\n\temail = %s\n", Name, Email))
}

// Bare makes an empty bare repo whose branch is main and returns its folder.
func Bare(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	_, stderr, err := Runner(t)(proc.Command{Name: "git", Args: []string{"init", "--bare", "--initial-branch=main", "--", dir}})
	if err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, stderr)
	}
	return dir
}

// requireGit skips when git is not installed, and fails instead when EZC_REQUIRE_GIT=1.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		if os.Getenv("EZC_REQUIRE_GIT") == "1" {
			t.Fatal("git is required (EZC_REQUIRE_GIT=1) but is not on PATH")
		}
		t.Skip("git is not on PATH")
	}
}

// isolated are the variables the runner sets itself; XDG_CONFIG_HOME is only removed.
var isolated = []string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_SYSTEM"}

func runner(t *testing.T, globalConfig string) proc.Runner {
	t.Helper()
	requireGit(t)
	home := t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte(globalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return func(c proc.Command) ([]byte, []byte, error) {
		if c.Name != "git" {
			t.Errorf("gittest: ran %q; only git may run", c.Name)
			return nil, nil, fmt.Errorf("gittest: %q is not git", c.Name)
		}
		var env []string
		hasPath := false
		for _, kv := range c.Env {
			key, _, _ := strings.Cut(kv, "=")
			if slices.Contains(isolated, key) {
				continue
			}
			hasPath = hasPath || key == "PATH"
			env = append(env, kv)
		}
		if !hasPath {
			env = append(env, "PATH="+os.Getenv("PATH"))
		}
		env = append(env, "HOME="+home, "GIT_CONFIG_GLOBAL="+config, "GIT_CONFIG_NOSYSTEM=1")

		ctx := context.Background()
		if c.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.Timeout)
			defer cancel()
		}
		cmd := exec.CommandContext(ctx, c.Name, c.Args...)
		cmd.Dir = c.Dir
		cmd.Env = env
		// A helper git started can outlive it holding the pipes; do not wait for it.
		cmd.WaitDelay = time.Second
		var stdout, stderr bytes.Buffer
		cmd.Stdin = bytes.NewReader(c.Stdin)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		switch {
		case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
			err = proc.ErrTimeout
		case errors.Is(err, exec.ErrWaitDelay):
			err = nil
		}
		return stdout.Bytes(), stderr.Bytes(), err
	}
}
