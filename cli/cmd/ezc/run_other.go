//go:build !unix

package main

import (
	"errors"
	"os"
	"os/exec"
)

// runChild runs the command as a child wired to the terminal and returns its exit code.
func runChild(name string, args, env []string) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return 0, startFailure(name, err)
}
