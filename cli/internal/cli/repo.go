package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const manifestTemplate = `# What this repo needs from its environment. ` + "`ezc env`" + ` checks it.
# yaml-language-server: $schema=` + schemaID + `
schemaVersion: v1

required: {}

# Example:
#   API_URL:
#     type: url          # str, int, bool, secret, url, path or enum
#     desc: Base URL of the API this service calls
`

const envLocalTemplate = `# Values for the local environment. Plain NAME: value lines.
# Secrets do not belong in this file.
`

const nextSteps = `
Load the environment however you like, for example:
  ezc run -- <command>
  eval "$(ezc env export)"
  ezc codegen shell, ezc codegen mise or ezc codegen direnv: a script that loads it, with how to use it at the top

Check it with: ezc env
`

func newRepoCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "repo",
		Short: "set up a repo's .ezc folder, and the store's list of the repos that use it",
	}, "init, add, remove, list, check, uses or rewrap")
	cmd.AddCommand(newRepoAddCmd(sys), newRepoRemoveCmd(sys), newRepoListCmd(sys), newRepoCheckCmd(sys), newRepoUsesCmd(sys), newRepoRewrapCmd(sys))
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "create .ezc/ with a manifest and a local values file",
		Long:  "Create .ezc/ in the working directory (never in a parent) with manifest.yaml and env.local.yaml. Files that exist are kept. How the environment is loaded is yours to choose: `ezc run`, `ezc env export`, or a script from `ezc codegen shell|mise|direnv`.",
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return repoInit(sys) },
	})
	return cmd
}

// repoInit writes the two files into the working directory's .ezc, one status line each.
func repoInit(sys System) error {
	wd, err := sys.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(wd, ezcDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []struct{ name, content string }{
		{manifestName, manifestTemplate},
		{"env.local.yaml", envLocalTemplate},
	}
	for _, f := range files {
		status, err := ensureFile(filepath.Join(dir, f.name), f.content)
		if err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "%s %s/%s\n", status, ezcDir, f.name)
	}
	_, err = fmt.Fprint(sys.Stdout, nextSteps)
	return err
}

// ensureFile writes content to path unless it exists, and says "created" or "kept".
func ensureFile(path, content string) (string, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "created", os.WriteFile(path, []byte(content), 0o644)
	}
	if err != nil {
		return "", err
	}
	return "kept", nil
}
