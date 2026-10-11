package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/share"
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
Load the environment with one of:
  mise     [env]  _.source = ".ezc/env.sh"
  direnv   source_env .ezc/env.sh
  neither  ezc run -- <command>

Check it with: ezc env
`

func newRepoCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "repo",
		Short: "set up a repo's .ezc folder",
	}, "init")
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "create .ezc/ with a manifest, a local values file and the env.sh shim",
		Long:  "Create .ezc/ in the working directory (never in a parent) with manifest.yaml, env.local.yaml and env.sh. Files that exist are kept, except an env.sh from an older shim, which is updated.",
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return repoInit(sys) },
	})
	return cmd
}

// repoInit writes the three files into the working directory's .ezc, one status line each.
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
		{"env.sh", share.EnvSh},
	}
	for _, f := range files {
		status, err := ensureFile(filepath.Join(dir, f.name), f.content, f.name == "env.sh")
		if err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "%s %s/%s\n", status, ezcDir, f.name)
	}
	_, err = fmt.Fprint(sys.Stdout, nextSteps)
	return err
}

// ensureFile writes content to path unless it exists, and says "created" or "kept". For a shim, an existing file whose first line differs from content's is rewritten: "updated".
func ensureFile(path, content string, shim bool) (string, error) {
	old, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "created", os.WriteFile(path, []byte(content), 0o644)
	}
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(content, "\n")
	oldFirst, _, _ := strings.Cut(string(old), "\n")
	if shim && oldFirst != first {
		return "updated", os.WriteFile(path, []byte(content), 0o644)
	}
	return "kept", nil
}
