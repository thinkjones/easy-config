// ezc is easy config: one store for secrets and variables, layered into every
// repo. Every command is Go code in internal/cli:
//
//	ezc env [export] | run | version | help [command]
//
// Data lives in EZC_HOME. bin/ezc in the source checkout builds this binary
// on demand. main only wires the process into cli.Run.
package main

import (
	"errors"
	"fmt"
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
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Stdin:   os.Stdin,
		Getenv:  os.Getenv,
		Environ: os.Environ,
		Getwd:   os.Getwd,
		GOOS:    runtime.GOOS,
		RunChild: func(name string, args, env []string) (int, error) {
			cmd := exec.Command(name, args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			cmd.Env = env
			err := cmd.Run()
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exit.ExitCode(), nil
			}
			return 0, err
		},
		Version: version,
	})
	var exit *cli.ExitError
	var usage *cli.UsageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code
	case errors.As(err, &usage):
		fmt.Fprintf(os.Stderr, "ezc: %v\n", err)
		return 2
	}
	fmt.Fprintf(os.Stderr, "ezc: %v\n", err)
	return 1
}
