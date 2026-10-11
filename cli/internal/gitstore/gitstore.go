// Package gitstore treats a store as a git repo: init, clone, commit, fetch,
// status and push. It is the only place that builds git command lines.
package gitstore

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// Git runs git through Run.
type Git struct {
	Run     proc.Runner
	Environ []string // the process environment; GIT_TERMINAL_PROMPT=0 is added
}

// Author names the author of a commit; used only when git has no identity of its own.
type Author struct{ Name string }

// authorEmail goes with Author.Name: a commit needs an address, and ezc knows none.
const authorEmail = "ezc@localhost"

// State is how a store stands against its remote.
type State int

const (
	InSync State = iota
	Ahead
	Behind
	Diverged
	Uncommitted
)

// Status is the local state of a store.
type Status struct {
	Branch        string
	Remote        string // origin's URL as configured, credentials and all; "" for a local-only store
	Ahead, Behind int    // against origin/<Branch>; with no such branch on the remote, Ahead is every local commit
	Dirty         bool
}

// State is Uncommitted when Dirty, else Diverged, Behind, Ahead or InSync from the counts.
func (s Status) State() State {
	switch {
	case s.Dirty:
		return Uncommitted
	case s.Ahead > 0 && s.Behind > 0:
		return Diverged
	case s.Behind > 0:
		return Behind
	case s.Ahead > 0:
		return Ahead
	}
	return InSync
}

var (
	ErrNotInstalled = errors.New("git is not installed")
	ErrUnreachable  = errors.New("the remote could not be reached")
)

// fixed is what every git call has in its environment: no password prompt,
// messages in one language, and a path that is never a pattern.
var fixed = []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_LITERAL_PATHSPECS=1"}

// elsewhere are the variables that point git at a repo other than the folder
// it runs in (git rev-parse --local-env-vars, and two more that move what it
// finds). Git sets them for a hook, so an ezc started from one inherits them.
var elsewhere = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_PREFIX",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_GRAFT_FILE", "GIT_SHALLOW_FILE",
	"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_INTERNAL_SUPER_PREFIX",
	"GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES",
}

// run runs git in dir and returns its stdout. An error carries one line of
// git's stderr, never its stdout.
func (g Git) run(dir string, timeout time.Duration, args ...string) ([]byte, error) {
	env := make([]string, 0, len(g.Environ)+len(fixed))
	for _, kv := range g.Environ {
		key, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(elsewhere, key) && !slices.ContainsFunc(fixed, func(f string) bool { return strings.HasPrefix(f, key+"=") }) {
			env = append(env, kv)
		}
	}
	env = append(env, fixed...)
	stdout, stderr, err := g.Run(proc.Command{Dir: dir, Env: env, Name: "git", Args: args, Timeout: timeout})
	switch {
	case err == nil:
		return stdout, nil
	case errors.Is(err, exec.ErrNotFound):
		return nil, ErrNotInstalled
	case errors.Is(err, proc.ErrTimeout):
		return nil, fmt.Errorf("git: %w", proc.ErrTimeout)
	}
	if line := reason(stderr); line != "" {
		return nil, errors.New("git: " + line)
	}
	return nil, fmt.Errorf("git: %v", err)
}

// reason is the line of git's stderr that says what went wrong: its first
// "fatal:" or "error:" line, else its first line.
func reason(stderr []byte) string {
	first := ""
	for _, line := range strings.Split(string(stderr), "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"fatal: ", "error: "} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				return rest
			}
		}
		if first == "" {
			first = line
		}
	}
	return first
}

// IsRepo reports whether dir is the top of a work tree.
func (g Git) IsRepo(dir string) bool {
	out, err := g.run(dir, 0, "rev-parse", "--is-inside-work-tree", "--show-cdup")
	// The path up to the top is empty only at the top.
	return err == nil && string(out) == "true\n\n"
}

// Init makes dir, and the folders above it, a repo on branch main.
func (g Git) Init(dir string) error {
	_, err := g.run("", 0, "init", "--initial-branch=main", "--", dir)
	return err
}

// Clone clones source into dir.
func (g Git) Clone(source, dir string) error {
	_, err := g.run("", 0, "clone", "--", source, dir)
	return err
}

// Remote is origin's URL, "" when there is no origin.
func (g Git) Remote(dir string) (string, error) {
	out, err := g.run(dir, 0, "config", "--default", "", "--get", "remote.origin.url")
	return strings.TrimSpace(string(out)), err
}

// SetRemote adds origin, or replaces its URL.
func (g Git) SetRemote(dir, url string) error {
	current, err := g.Remote(dir)
	if err != nil {
		return err
	}
	verb := "add"
	if current != "" {
		verb = "set-url"
	}
	_, err = g.run(dir, 0, "remote", verb, "--", "origin", url)
	return err
}

// Commit commits exactly these paths (relative to dir; additions, changes and
// deletions). done is false when none of them changed, which includes a path
// git ignores. A commit that fails leaves the paths staged.
func (g Git) Commit(dir, message string, a Author, paths ...string) (bool, error) {
	if len(paths) == 0 {
		return false, nil
	}
	args := append([]string{"status", "--porcelain", "-z", "--no-renames", "--untracked-files=all", "--"}, paths...)
	out, err := g.run(dir, 0, args...)
	if err != nil {
		return false, err
	}
	var changed []string
	for _, entry := range strings.Split(string(out), "\x00") {
		// An entry is two status letters, a space and the path.
		if len(entry) > 3 {
			changed = append(changed, entry[3:])
		}
	}
	if len(changed) == 0 {
		return false, nil
	}
	if _, err := g.run(dir, 0, append([]string{"add", "--all", "--"}, changed...)...); err != nil {
		return false, err
	}
	args = nil
	if !g.hasIdentity(dir) {
		args = []string{"-c", "user.name=" + a.Name, "-c", "user.email=" + authorEmail}
	}
	// --only leaves alone whatever else is staged.
	args = append(args, "commit", "--quiet", "--only", "-m", message, "--")
	if _, err := g.run(dir, 0, append(args, changed...)...); err != nil {
		return false, err
	}
	return true, nil
}

// hasIdentity reports whether git knows who commits, from its config or the
// environment, without guessing from the machine's account.
func (g Git) hasIdentity(dir string) bool {
	for _, ident := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, err := g.run(dir, 0, "-c", "user.useConfigOnly=true", "var", ident); err != nil {
			return false
		}
	}
	return true
}

// Fetch updates what is known of origin; a store with no origin has nothing
// to fetch. A timeout or a network failure wraps ErrUnreachable.
func (g Git) Fetch(dir string, timeout time.Duration) error {
	remote, err := g.Remote(dir)
	if err != nil || remote == "" {
		return err
	}
	_, err = g.run(dir, timeout, "fetch", "--quiet", "origin")
	if err != nil && !errors.Is(err, ErrNotInstalled) {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return err
}

// Status reads the local state only; it never uses the network.
func (g Git) Status(dir string) (Status, error) {
	out, err := g.run(dir, 0, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return Status{}, err
	}
	var st Status
	born := true
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case line == "# branch.oid (initial)":
			born = false
		case strings.HasPrefix(line, "# branch.head "):
			st.Branch = strings.TrimPrefix(line, "# branch.head ")
		case line != "" && !strings.HasPrefix(line, "#"):
			st.Dirty = true
		}
	}
	if st.Branch == "" || st.Branch == "(detached)" {
		return Status{}, errors.New("git: the store is not on a branch")
	}
	if st.Remote, err = g.Remote(dir); err != nil || st.Remote == "" {
		return st, err
	}

	ref := "refs/remotes/origin/" + st.Branch
	out, err = g.run(dir, 0, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return Status{}, err
	}
	onRemote := strings.TrimSpace(string(out)) == ref
	switch {
	case born && onRemote:
		counts, err := g.count(dir, 2, "--left-right", "HEAD..."+ref)
		if err != nil {
			return Status{}, err
		}
		st.Ahead, st.Behind = counts[0], counts[1]
	case born:
		counts, err := g.count(dir, 1, "HEAD")
		if err != nil {
			return Status{}, err
		}
		st.Ahead = counts[0]
	case onRemote:
		counts, err := g.count(dir, 1, ref)
		if err != nil {
			return Status{}, err
		}
		st.Behind = counts[0]
	}
	return st, nil
}

// count runs git rev-list --count and returns the n numbers it prints. The
// closing "--" keeps a file named like a revision from making it ambiguous.
func (g Git) count(dir string, n int, args ...string) ([]int, error) {
	out, err := g.run(dir, 0, append(append([]string{"rev-list", "--count"}, args...), "--")...)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(out))
	if len(fields) != n {
		return nil, errors.New("git: unexpected output from rev-list")
	}
	counts := make([]int, n)
	for i, f := range fields {
		if counts[i], err = strconv.Atoi(f); err != nil {
			return nil, errors.New("git: unexpected output from rev-list")
		}
	}
	return counts, nil
}

// Push publishes the branch, creating it on the remote and setting the
// upstream when needed. pushed is the number of commits sent.
func (g Git) Push(dir string) (int, error) {
	st, err := g.Status(dir)
	if err != nil {
		return 0, err
	}
	if st.Remote == "" {
		return 0, errors.New("git: the store has no remote")
	}
	if st.Ahead == 0 {
		return 0, nil
	}
	if _, err := g.run(dir, 0, "push", "--quiet", "--set-upstream", "origin", st.Branch); err != nil {
		return 0, err
	}
	return st.Ahead, nil
}
