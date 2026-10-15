package migrate

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// before composes the environment as the old tool does: `codegen shell [env]`, its stdout parsed and its stderr
// dropped. For the store it runs in an empty folder with FS_CONFIG_HOME at the old store, so that it composes the
// store alone; for a repo it runs in the repo. APP_ENV never reaches it: the environment is the argument.
func (o Options) before(env string, store bool) (map[string]string, error) {
	if _, err := o.LookPath(OldTool); err != nil {
		return nil, fmt.Errorf("%s is not on PATH; the proof runs it to compose the environment as it stands.", OldTool)
	}
	var vars []string
	for _, kv := range o.Environ {
		if strings.HasPrefix(kv, "APP_ENV=") || strings.HasPrefix(kv, "FS_CONFIG_HOME=") {
			continue
		}
		vars = append(vars, kv)
	}
	dir := o.Dir
	if store {
		empty, err := os.MkdirTemp("", "ezc-migrate-empty-")
		if err != nil {
			return nil, fmt.Errorf("could not make a temporary folder (%s).", reason(err))
		}
		defer os.RemoveAll(empty)
		dir = empty
		vars = append(vars, "FS_CONFIG_HOME="+o.Dir+string(os.PathSeparator)+OldDir)
	}
	args := []string{"codegen", "shell"}
	if env != "" {
		args = append(args, env)
	}
	out, _, err := o.Exec(proc.Command{Dir: dir, Env: vars, Name: OldTool, Args: args})
	if err != nil {
		return nil, fmt.Errorf("%s %s failed in %s (%s). Run %s env%s there to see why.",
			OldTool, strings.Join(args, " "), o.Dir, exitReason(err), OldTool, envArg(env))
	}
	return parseExports(out)
}

func envArg(env string) string {
	if env == "" {
		return ""
	}
	return " " + env
}

// exitReason says how a program failed without its error's text, which can carry what it printed.
func exitReason(err error) string {
	var code interface{ ExitCode() int }
	switch {
	case errors.As(err, &code):
		return fmt.Sprintf("exit status %d", code.ExitCode())
	case errors.Is(err, proc.ErrTimeout):
		return "timed out"
	default:
		return "it could not be started"
	}
}
