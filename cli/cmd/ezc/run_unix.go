//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// runChild replaces ezc with the command, so the command gets the signals sent
// to the process that was started and its exit status is the process's own. It
// returns only when the command could not be started.
func runChild(name string, args, env []string) (int, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return 0, startFailure(name, err)
	}
	return 0, startFailure(name, syscall.Exec(path, append([]string{name}, args...), env))
}
