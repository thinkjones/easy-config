// Package cli is ezc's command line, built with Cobra. Every command is Go
// code in this package; newRootCmd assembles them and Run executes argv.
//
// Commands reach the outside world only through System, so tests drive them
// in-process with a fake: no real environment, home or child process.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// System is everything a command needs from outside the process.
type System struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Getenv         func(string) string
	// Environ is the process environment as "K=V" strings, for ezc run.
	Environ func() []string
	Getwd   func() (string, error)
	// GOOS is runtime.GOOS; tests fix it.
	GOOS string
	// RunChild runs name with args and env, wired to the terminal, and returns its exit code.
	// On Unix it replaces ezc with the command, and returns only when that failed.
	RunChild func(name string, args, env []string) (int, error)
	Version  string

	// Exec runs a program with stdin and waits for it.
	Exec     proc.Runner
	LookPath func(string) (string, error)
	// ReadSecret prints prompt to the terminal and reads a line without echo.
	ReadSecret      func(prompt string) (string, error)
	StdinIsTerminal func() bool
	// Backend overrides the encryption backend; nil means the default backend for this machine. Tests set it.
	Backend backend.Backend
}

// ExitError ends ezc with Code and no further message: the output already
// said why.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// UsageError is a malformed command line; ezc exits 2.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &UsageError{fmt.Sprintf(format, a...)} }

// Run executes the command line args (without the program name).
func Run(args []string, sys System) error {
	root := newRootCmd(sys)
	root.SetArgs(args)
	return root.Execute()
}

// newRootCmd is ezc's command tree. Every command writes to sys and returns
// a UsageError for a malformed command line, so ezc exits 2 for those.
func newRootCmd(sys System) *cobra.Command {
	root := &cobra.Command{
		Use:   "ezc",
		Short: "ezc — easy config: one store for secrets and variables, layered into every repo",
		Long: `ezc — easy config: one store for secrets and variables, layered into every repo

Compose a repo's environment from a global store and the repo's own .ezc/, validate it against a manifest, and export it or run a command with it.

Config
  EZC_HOME    ezc's folder: its config and the global store (default ~/Library/ezc, ~/.local/share/ezc)
  EZC_STORE   the global store's folder, when it is not $EZC_HOME/store
  EZC_ENV     the environment to compose (then APP_ENV, then local)`,
		Version:       sys.Version,
		Args:          unknownCommand,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetOut(sys.Stdout)
	root.SetErr(sys.Stderr)
	root.SetVersionTemplate("ezc {{.Version}}\n")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		msg := err.Error()
		// pflag reads -verbose as -v -e -r …; say what was probably meant.
		if _, word, found := strings.Cut(msg, " in -"); found && strings.HasPrefix(msg, "unknown shorthand flag") && len(word) > 1 {
			msg += " (long flags take two dashes: --" + word + ")"
		}
		return usagef("%s. Try `%s`.", msg, helpFor(cmd))
	})
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [command]",
		Short: "help for ezc, or for one command",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 {
				return usagef("no command %q. Try `ezc help`.", strings.Join(args, " "))
			}
			return target.Help()
		},
	})
	root.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "print ezc's version",
			Args:  noArgs,
			RunE: func(*cobra.Command, []string) error {
				fmt.Fprintf(sys.Stdout, "ezc %s\n", sys.Version)
				return nil
			},
		},
		newEnvCmd(sys),
		newRunCmd(sys),
		newManifestCmd(sys),
		newCodegenCmd(sys),
		newListCmd(sys),
		newGetCmd(sys),
		newSetCmd(sys),
		newUnsetCmd(sys),
		newRepoCmd(sys),
	)
	return root
}

// helpFor is the help command for cmd: `ezc help env`, or `ezc help`.
func helpFor(cmd *cobra.Command) string {
	return strings.TrimSpace("ezc help " + strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), "ezc")))
}

// unknownCommand rejects words that name no command, suggesting near ones.
func unknownCommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return usagef("unknown command %q%s. Try `ezc help`.", args[0], didYouMean(cmd, args[0]))
}

// didYouMean is " — did you mean x?" for the subcommands of cmd near word.
func didYouMean(cmd *cobra.Command, word string) string {
	cmd.SuggestionsMinimumDistance = 2
	if s := cmd.SuggestionsFor(word); len(s) > 0 {
		return fmt.Sprintf(" — did you mean %s?", strings.Join(s, " or "))
	}
	return ""
}

// noArgs, exactArgs, minArgs and maxArgs check positional arguments, failing
// with a UsageError that says what the command wants.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usagef("%s: unexpected argument %q. Try `%s`.", cmd.CommandPath(), args[0], helpFor(cmd))
	}
	return nil
}

func exactArgs(n int, want string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usagef("%s: give %s. Try `%s`.", cmd.CommandPath(), want, helpFor(cmd))
		}
		return nil
	}
}

func minArgs(n int, want string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return usagef("%s: give %s. Try `%s`.", cmd.CommandPath(), want, helpFor(cmd))
		}
		return nil
	}
}

func maxArgs(n int, want string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usagef("%s: give %s. Try `%s`.", cmd.CommandPath(), want, helpFor(cmd))
		}
		return nil
	}
}

// verbs is a parent command such as `ezc env`: run alone, or with a word
// that names none of its subcommands, it is a usage error listing them.
func verbs(cmd *cobra.Command, list string) *cobra.Command {
	cmd.Args = func(c *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usagef("%s: unknown action %q%s — use %s. Try `%s`.", c.CommandPath(), args[0], didYouMean(c, args[0]), list, helpFor(c))
		}
		return nil
	}
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return usagef("%s: say %s. Try `%s`.", c.CommandPath(), list, helpFor(c))
	}
	return cmd
}
