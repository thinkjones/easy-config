// ezc is easy config: one store for secrets and variables, layered into every
// repo. Every command is Go code in internal/cli:
//
//	ezc env [export] | run | version | help [command]
//
// Data lives in EZC_HOME. bin/ezc in the source checkout builds this binary
// on demand. main only wires the process into cli.Run; run_unix.go and
// run_other.go are how `ezc run` starts its command.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"

	"github.com/thinkjones/easy-config/cli/internal/cli"
)

const version = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	err := cli.Run(os.Args[1:], cli.System{
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Stdin:    os.Stdin,
		Getenv:   os.Getenv,
		Environ:  os.Environ,
		Getwd:    os.Getwd,
		GOOS:     runtime.GOOS,
		RunChild: runChild,
		Version:  version,
	})
	var exit *cli.ExitError
	var usage *cli.UsageError
	var start *startError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code
	case errors.As(err, &start):
		fmt.Fprintf(os.Stderr, "ezc: %v\n", err)
		return start.code
	case errors.As(err, &usage):
		fmt.Fprintf(os.Stderr, "ezc: %v\n", err)
		return 2
	}
	fmt.Fprintf(os.Stderr, "ezc: %v\n", err)
	return 1
}

// startError is a command `ezc run` could not start. ezc exits as a shell
// would: 127 when there is no such command, 126 when it cannot be run.
type startError struct {
	code int
	msg  string
}

func (e *startError) Error() string { return e.msg }

// startFailure is the startError for name, from the error that stopped it.
func startFailure(name string, err error) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return &startError{127, name + ": command not found"}
	}
	var lookup *exec.Error
	var path *fs.PathError
	switch {
	case errors.As(err, &lookup):
		err = lookup.Err
	case errors.As(err, &path):
		err = path.Err
	}
	return &startError{126, fmt.Sprintf("%s: %v", name, err)}
}
