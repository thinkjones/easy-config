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
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/term"

	"github.com/thinkjones/easy-config/cli/internal/cli"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

const version = "0.2.0"

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

		Exec:            execCommand,
		LookPath:        exec.LookPath,
		ReadSecret:      readSecret,
		StdinIsTerminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
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

// execCommand runs c and waits. An interactive command shares the terminal and
// returns no output; otherwise stdout and stderr are captured separately and
// the program's stdin is exactly c.Stdin.
func execCommand(c proc.Command) ([]byte, []byte, error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	if c.Interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return nil, nil, cmd.Run()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(c.Stdin)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// readSecret prints prompt to stderr and reads a line from the terminal on
// stdin without echoing it.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
