// Package gitstore treats a store as a git repo: init, clone, commit, fetch,
// status and push. It is the only place that builds git command lines.
package gitstore

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// TokenVar names an environment variable that holds a token for a remote on GitHub. Git reads it
	// through a credential helper whose text names the variable only; the value is never an argument,
	// and no other host is offered it.
	TokenVar string
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
	// ErrNoBranch is a store whose HEAD is on no branch, as while a rebase is open.
	ErrNoBranch    = errors.New("the store is not on a branch")
	ErrUnreachable = errors.New("the remote could not be reached")
	// ErrBranchTaken is a branch the remote already has, holding other bytes than the ones to push.
	ErrBranchTaken = errors.New("the remote already has that branch with other content")
	// ErrBranchMoved is a branch of the remote that is no longer at the commit it was read at.
	ErrBranchMoved = errors.New("the remote's branch has changed since it was read")
	// ErrRemoteMoved is a push the remote refused because its branch holds commits the store does not.
	ErrRemoteMoved = errors.New("the remote has commits the store does not have")
)

// allBranches is the refspec that maps every branch of origin to its remote-tracking branch.
const allBranches = "+refs/heads/*:refs/remotes/origin/*"

// A ref is always named in full. Git looks a short name up among the tags first, so a tag called origin/main
// or ezc/join-sam, which anybody who can push can make, would stand in for the branch of that name. That makes
// every read safe from a tag. A push is not: a tag on the remote whose own name is refs/heads/<branch> makes the
// branch's full name match two refs there, and git refuses the push (a *TagError says so).

// tracking is the full name of the remote-tracking branch for a branch of origin.
func tracking(branch string) string { return "refs/remotes/origin/" + branch }

// head is the full name of a branch, here or on origin.
func head(branch string) string { return "refs/heads/" + branch }

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
// "fatal:" or "error:" line, else its first line, with each URL as ShowURL gives it.
func reason(stderr []byte) string {
	first := ""
	for _, line := range strings.Split(string(stderr), "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"fatal: ", "error: "} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				return urlPattern.ReplaceAllStringFunc(rest, ShowURL)
			}
		}
		if first == "" {
			first = line
		}
	}
	return urlPattern.ReplaceAllStringFunc(first, ShowURL)
}

// urlPattern is a URL as git quotes one in a message, up to the space or quote that ends it.
var urlPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s'"]+`)

// schemePattern is the start of a remote that is a URL.
var schemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// ShowURL is a remote as it may be printed or saved: a URL keeps only its scheme, host and path, because
// its user, password, query and fragment can each carry a secret. An scp-style remote (git@host:o/r.git)
// and a local path are returned as they are. It is the only rule for showing a remote.
func ShowURL(remote string) string {
	if !schemePattern.MatchString(remote) {
		return remote
	}
	scheme, rest, _ := strings.Cut(remote, "://")
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	host, path, slash := strings.Cut(rest, "/")
	host = host[strings.LastIndex(host, "@")+1:]
	if slash {
		host += "/" + path
	}
	return scheme + "://" + host
}

// IsRepo reports whether dir is the top of a work tree. Without git, a folder that holds a .git entry
// counts as one, and the error is ErrNotInstalled: nothing more can be learnt about it.
func (g Git) IsRepo(dir string) (bool, error) {
	out, err := g.run(dir, 0, "rev-parse", "--is-inside-work-tree", "--show-cdup")
	if errors.Is(err, ErrNotInstalled) {
		if _, serr := os.Lstat(filepath.Join(dir, ".git")); serr == nil {
			return true, ErrNotInstalled
		}
		return false, nil
	}
	// The path up to the top is empty only at the top.
	return err == nil && string(out) == "true\n\n", nil
}

// MinVersion is the oldest git ezc works with.
const MinVersion = "2.23"

// Version is the version git reports, such as "2.43.0": the number in `git version 2.43.0 (Apple Git-146)`.
func (g Git) Version() (string, error) {
	out, err := g.run("", 0, "version")
	if err != nil {
		return "", err
	}
	v, ok := ParseVersion(string(out))
	if !ok {
		return "", errors.New("git: its version could not be read")
	}
	return v, nil
}

// ParseVersion is the version number in the output of `git version`: the numeric part of 2.43.0.windows.1 is 2.43.0.
func ParseVersion(out string) (string, bool) {
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return "", false
	}
	v := versionPattern.FindString(fields[2])
	return v, v != ""
}

var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*`)

// TooOld reports whether version, as Version returns it, is older than MinVersion.
func TooOld(version string) bool {
	parse := func(v string) (major, minor int) {
		parts := strings.Split(v, ".")
		major, _ = strconv.Atoi(parts[0])
		if len(parts) > 1 {
			minor, _ = strconv.Atoi(parts[1])
		}
		return major, minor
	}
	wantMajor, wantMinor := parse(MinVersion)
	major, minor := parse(version)
	return major < wantMajor || (major == wantMajor && minor < wantMinor)
}

// Init makes dir, and the folders above it, a repo on branch main.
func (g Git) Init(dir string) error {
	_, err := g.run("", 0, "init", "--initial-branch=main", "--", dir)
	return err
}

// tokenHost is the one host the token is for. Both helper settings are scoped to it, so git asks the
// helper for no other host, and a remote that is a path or uses ssh never asks at all.
const tokenHost = "https://github.com"

// remote is args for a git command that can reach a remote: with TokenVar set, the first helper setting
// drops every inherited credential helper for tokenHost and the second reads the token from the variable.
// For any other host the inherited helpers stay as they are.
func (g Git) remote(args ...string) []string {
	if g.TokenVar == "" {
		return args
	}
	setting := "credential." + tokenHost + ".helper="
	helper := `!f() { echo username=x-access-token; echo "password=$` + g.TokenVar + `"; }; f`
	return append([]string{"-c", setting, "-c", setting + helper}, args...)
}

// Clone clones source into dir, without its tags: ezc uses none.
func (g Git) Clone(source, dir string) error {
	_, err := g.run("", 0, g.remote("clone", "--no-tags", "--", source, dir)...)
	return err
}

// CloneShallow clones source into dir with only its latest commit.
func (g Git) CloneShallow(source, dir string) error {
	_, err := g.run("", 0, g.remote("clone", "--no-tags", "--depth", "1", "--", source, dir)...)
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

// Changed are the files at or under these paths (relative to dir) that differ
// from the last commit or that git has never seen, staged or not; a path git
// ignores is never among them. Nothing is changed by asking.
func (g Git) Changed(dir string, paths ...string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := append([]string{"status", "--porcelain", "-z", "--no-renames", "--untracked-files=all", "--"}, paths...)
	out, err := g.run(dir, 0, args...)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, entry := range strings.Split(string(out), "\x00") {
		// An entry is two status letters, a space and the path.
		if len(entry) > 3 {
			changed = append(changed, entry[3:])
		}
	}
	return changed, nil
}

// Commit commits exactly these paths (relative to dir; additions, changes and
// deletions). done is false when none of them changed, which includes a path
// git ignores. A commit that fails leaves the paths staged.
func (g Git) Commit(dir, message string, a Author, paths ...string) (bool, error) {
	changed, err := g.Changed(dir, paths...)
	if err != nil {
		return false, err
	}
	if len(changed) == 0 {
		return false, nil
	}
	if _, err := g.run(dir, 0, append([]string{"add", "--all", "--"}, changed...)...); err != nil {
		return false, err
	}
	var args []string
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
// to fetch. It fetches every branch (a shallow clone tracks only one) and drops
// remote-tracking branches the remote no longer has. It fetches no tag. A timeout or a network failure wraps ErrUnreachable.
func (g Git) Fetch(dir string, timeout time.Duration) error {
	remote, err := g.Remote(dir)
	if err != nil || remote == "" {
		return err
	}
	_, err = g.run(dir, timeout, g.remote("fetch", "--quiet", "--prune", "--no-tags", "origin", allBranches)...)
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
		return Status{}, fmt.Errorf("git: %w", ErrNoBranch)
	}
	if st.Remote, err = g.Remote(dir); err != nil || st.Remote == "" {
		return st, err
	}

	ref := tracking(st.Branch)
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
// upstream when needed. pushed is the number of commits sent. A push the remote
// refuses because its branch has moved on wraps ErrRemoteMoved.
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
	if err := g.push(dir, st.Branch, 0, "--set-upstream", "origin", head(st.Branch)+":"+head(st.Branch)); err != nil {
		var tag *TagError
		if !errors.As(err, &tag) && g.moved(dir, st.Branch) {
			return 0, fmt.Errorf("git: %w", ErrRemoteMoved)
		}
		return 0, err
	}
	return st.Ahead, nil
}

// moved reports whether origin's branch is at a commit the store's last commit does not come after, asked of
// origin now. A remote that cannot be asked has not been seen to move.
func (g Git) moved(dir, branch string) bool {
	now, err := g.onRemote(dir, branch, 0)
	if err != nil || now == "" {
		return false
	}
	// A commit the store has never fetched is not an ancestor of anything here.
	_, err = g.run(dir, 0, "merge-base", "--is-ancestor", now, "HEAD")
	return err != nil
}

// Head is the commit the store is at.
func (g Git) Head(dir string) (string, error) {
	return g.revParse(dir, "HEAD")
}

// Uncommit takes back the commit made on top of parent while the store's branch is still at it: the branch
// returns to parent, and the index and the work tree are left as they are. The files the commit changed are
// then for Restore to put back, and no other file is touched. A branch that is elsewhere by now is left alone.
func (g Git) Uncommit(dir, parent, made string) error {
	out, err := g.run(dir, 0, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return fmt.Errorf("git: %w", ErrNoBranch)
	}
	// The branch by its full name, and the commit it must be at for git to move it.
	_, err = g.run(dir, 0, "update-ref", "-m", "ezc: take back a commit that was not pushed", strings.TrimSpace(string(out)), parent, made)
	return err
}
