// Package ghcli is the one place, beside setup, that builds gh command lines: the pull request of a join
// request and the secrets a bot's key is delivered as.
package ghcli

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// ErrNotInstalled is gh missing from PATH.
var ErrNotInstalled = errors.New("gh is not installed. https://cli.github.com")

// GH runs gh through Run.
type GH struct {
	Run      proc.Runner
	LookPath func(string) (string, error)
	Environ  []string // the process environment
}

// Installed reports whether gh is on PATH.
func (g GH) Installed() bool {
	_, err := g.LookPath("gh")
	return err == nil
}

// PRCreate opens a pull request for the branch head of the repo in dir and returns its URL.
func (g GH) PRCreate(dir, head, title, body string) (url string, err error) {
	stdout, err := g.run(dir, nil, "pr", "create", "--head", head, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	// gh prints the pull request's URL last.
	lines := strings.Fields(string(stdout))
	if len(lines) == 0 {
		return "", nil
	}
	return lines[len(lines)-1], nil
}

// SecretSet sets the Actions secret name of repo (owner/repo) to value, which gh reads from stdin.
func (g GH) SecretSet(repo, name string, value []byte) error {
	_, err := g.run("", value, "secret", "set", name, "--repo", repo)
	return err
}

// SecretDelete removes the Actions secret name of repo.
func (g GH) SecretDelete(repo, name string) error {
	_, err := g.run("", nil, "secret", "delete", name, "--repo", repo)
	return err
}

// run runs gh in dir with stdin and returns its stdout. An error carries one line of what gh said on stderr,
// never its stdout and never what stdin held.
func (g GH) run(dir string, stdin []byte, args ...string) ([]byte, error) {
	stdout, stderr, err := g.Run(proc.Command{Dir: dir, Env: g.Environ, Stdin: stdin, Name: "gh", Args: args})
	switch {
	case err == nil:
		return stdout, nil
	case errors.Is(err, exec.ErrNotFound):
		return nil, ErrNotInstalled
	}
	// A gh that repeats its input, or one line of it, anywhere in what it says must not put a secret in an error.
	for _, held := range bytes.Split(stdin, []byte("\n")) {
		if held = bytes.TrimSpace(held); len(held) > 0 && bytes.Contains(stderr, held) {
			return nil, errors.New("gh: it failed")
		}
	}
	if line := reason(stderr); line != "" {
		return nil, errors.New("gh: " + line)
	}
	return nil, fmt.Errorf("gh: %v", err)
}

// urlPattern is a URL in a line of gh's stderr, up to the space that ends it.
var urlPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S+`)

// reason is the first line gh said, with each URL as gitstore.ShowURL gives it and each control character as ?.
func reason(stderr []byte) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		line = urlPattern.ReplaceAllStringFunc(line, gitstore.ShowURL)
		return strings.Map(func(c rune) rune {
			if c < 0x20 || c == 0x7f {
				return '?'
			}
			return c
		}, line)
	}
	return ""
}
