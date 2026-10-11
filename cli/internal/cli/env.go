package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/check"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

func newEnvCmd(sys System) *cobra.Command {
	var manifestFlag string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "env [ENV]",
		Short: "check that an environment is complete and valid; `env export` prints it",
		Long: `Compose ENV (else $EZC_ENV, $APP_ENV, then local) from the store and this repo's .ezc/, and check it against the manifest. Reports on stderr and prints no values.

Exits 1 when a required variable is missing or invalid. Names and paths only; --verbose adds which file set each name.

An environment named "export" cannot be passed to ` + "`ezc env`" + ` (it is the subcommand); set EZC_ENV instead.`,
		Args: maxArgs(1, "at most one environment name"),
		RunE: func(_ *cobra.Command, args []string) error {
			return envCheck(sys, firstArg(args), manifestFlag, verbose)
		},
	}
	cmd.Flags().StringVarP(&manifestFlag, "manifest", "m", "", "the manifest to check against; the values come from that manifest's folder, not this repo")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show which file set each name")
	cmd.AddCommand(newExportCmd(sys))
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// envCheck is `ezc env`: resolve, then validate against the manifest.
func envCheck(sys System, arg, manifestPath string, verbose bool) error {
	tgt, err := manifestTarget(sys, manifestPath)
	if err != nil {
		return err
	}
	repo := tgt.Repo
	m, err := loadManifest(tgt.Path, tgt.Explicit)
	if err != nil {
		return err
	}
	touchStore(sys)
	res, err := load(sys, repo, envName(sys, arg))
	if err != nil {
		return err
	}
	warn(sys, res, repo)
	if problems := check.Run(m, res); len(problems) > 0 {
		io.WriteString(sys.Stderr, check.Report(problems, res))
		return &ExitError{1}
	}
	if verbose {
		io.WriteString(sys.Stderr, check.ReportVerbose(res, homeDir(sys)))
	} else {
		io.WriteString(sys.Stderr, check.ReportOK(res))
	}
	return nil
}

func newExportCmd(sys System) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "export [ENV]",
		Short: "print an environment's variables on stdout",
		Long: `Print ENV's variables (else $EZC_ENV, $APP_ENV, then local) on stdout, and nothing else. Outside a repo it exports the store alone. Nothing is printed unless the whole resolution succeeded. The selector variables EZC_ENV and APP_ENV are not exported.

Formats:
  shell  export NAME='value' lines, for eval "$(ezc env export)"
  json   one object of names to values, for any other tool`,
		Args: maxArgs(1, "at most one environment name"),
		RunE: func(_ *cobra.Command, args []string) error {
			if !slices.Contains([]string{"shell", "json"}, format) {
				return usagef("ezc env export: unknown format %q — use shell or json.", format)
			}
			repo, err := repoDir(sys)
			if err != nil {
				return err
			}
			res, err := load(sys, repo, envName(sys, firstArg(args)))
			if err != nil {
				return err
			}
			out, err := render(res, format)
			if err != nil {
				return err
			}
			_, err = io.WriteString(sys.Stdout, out)
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "shell", "shell (for eval) or json")
	return cmd
}

func render(res resolve.Result, format string) (string, error) {
	if format == "json" {
		b, err := res.JSON()
		return string(b), err
	}
	return res.Shell(), nil
}

func newRunCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "run [ENV] -- <command> [args...]",
		Short: "run a command with an environment loaded",
		Long: `Run a command with ENV's variables (else $EZC_ENV, $APP_ENV, then local) added to its environment. They replace same-named variables already set, and EZC_ENV and APP_ENV are passed too. The command's exit code becomes ezc's.

On Unix ezc replaces itself with the command, so there is no process in between: a signal sent to ezc reaches the command. A command that is not found exits 127; one that cannot be run exits 126.`,
		Args: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || len(args) == dash {
				return usagef("ezc run: give a command after --. Try `ezc help run`.")
			}
			if dash > 1 {
				return usagef("ezc run: give at most one environment name before --. Try `ezc help run`.")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			repo, err := repoDir(sys)
			if err != nil {
				return err
			}
			// On Unix RunChild does not return, so the store is looked at first.
			touchStore(sys)
			res, err := load(sys, repo, envName(sys, firstArg(args[:dash])))
			if err != nil {
				return err
			}
			warn(sys, res, repo)
			child := args[dash:]
			code, err := sys.RunChild(child[0], child[1:], childEnv(sys.Environ(), res))
			if err != nil {
				return err
			}
			if code != 0 {
				return &ExitError{code}
			}
			return nil
		},
	}
}

// childEnv is environ with every resolved name replaced or appended.
func childEnv(environ []string, res resolve.Result) []string {
	env := make([]string, 0, len(environ)+len(res.Env))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := res.Env[name]; !ok {
			env = append(env, kv)
		}
	}
	for _, name := range res.Names() {
		env = append(env, fmt.Sprintf("%s=%s", name, res.Env[name]))
	}
	return env
}
