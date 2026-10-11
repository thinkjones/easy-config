package gitstore

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Replaced is a file changed here and on the remote, for which the remote's version was kept
// when a local commit conflicted in it. A later local commit that merges into it cleanly still applies.
type Replaced struct {
	Path        string // relative to the store's root
	Base, Local []byte // its content at the common ancestor and here before the pull; nil where it did not exist
}

// PullResult is what a pull brought in.
type PullResult struct {
	Commits  int // commits received
	Replaced []Replaced
}

var (
	ErrConflict = errors.New("changed here and on the remote")
	ErrDirty    = errors.New("uncommitted changes")
	ErrNoRemote = errors.New("the store has no remote")
)

// Pull fetches, then fast-forwards, or rebases the local commits onto the remote's. During the rebase a
// conflicted file for which keepRemote(path) is true takes the remote's version; any other conflict
// aborts the rebase, leaves the store as it was, and returns an error wrapping ErrConflict that names the file.
func (g Git) Pull(dir string, timeout time.Duration, keepRemote func(path string) bool) (PullResult, error) {
	st, err := g.Status(dir)
	switch {
	case err != nil:
		return PullResult{}, err
	case st.Remote == "":
		return PullResult{}, ErrNoRemote
	case st.Dirty:
		return PullResult{}, ErrDirty
	}
	if err := g.Fetch(dir, timeout); err != nil {
		return PullResult{}, err
	}
	if st, err = g.Status(dir); err != nil || st.Behind == 0 {
		return PullResult{}, err
	}
	// The commit is pinned, so every step below sees the same remote.
	remote, err := g.revParse(dir, "refs/remotes/origin/"+st.Branch)
	if err != nil {
		return PullResult{}, err
	}
	res := PullResult{Commits: st.Behind}
	if st.Ahead == 0 {
		_, err := g.run(dir, 0, "merge", "--quiet", "--ff-only", remote)
		return res, err
	}
	if res.Replaced, err = g.rebase(dir, remote, keepRemote); err != nil {
		return PullResult{}, err
	}
	return res, nil
}

// rebase replays the local commits onto remote and returns the files that took the remote's version.
func (g Git) rebase(dir, remote string, keepRemote func(path string) bool) ([]Replaced, error) {
	local, err := g.revParse(dir, "HEAD")
	if err != nil {
		return nil, err
	}
	// Histories with no common ancestor still rebase; no file has a base then.
	base := ""
	if out, err := g.run(dir, 0, "merge-base", local, remote); err == nil {
		base = strings.TrimSpace(string(out))
	}
	pre, err := g.rebaseArgs(dir)
	if err != nil {
		return nil, err
	}
	// The message of a replayed commit is kept; no editor is ever opened for it.
	g.Environ = append(slices.DeleteFunc(slices.Clone(g.Environ), func(kv string) bool {
		return strings.HasPrefix(kv, "GIT_EDITOR=")
	}), "GIT_EDITOR=true")
	step := func(args ...string) error {
		_, err := g.run(dir, 0, append(slices.Clone(pre), args...)...)
		return err
	}
	fail := func(err error) ([]Replaced, error) {
		if abort := step("rebase", "--abort"); abort != nil && g.rebasing(dir) {
			return nil, fmt.Errorf("%w; the rebase could not be undone: %v", err, abort)
		}
		return nil, err
	}

	var replaced []Replaced
	// Renames are not followed: a file renamed here and changed on the remote is then a file
	// deleted here, which keeps the remote's version, beside a new one.
	err = step("rebase", "--quiet", "--no-autostash", "-Xno-renames", remote)
	for err != nil {
		paths, lerr := g.names(dir, "diff", "--name-only", "--diff-filter=U", "-z")
		if lerr != nil || len(paths) == 0 {
			// Not a conflict: git's own reason stands.
			return fail(err)
		}
		if other := slices.DeleteFunc(slices.Clone(paths), keepRemote); len(other) > 0 {
			for i, path := range other {
				// A name that would not read as one name is quoted.
				if q := strconv.Quote(path); q != `"`+path+`"` || strings.Contains(path, ", ") {
					other[i] = q
				}
			}
			return fail(fmt.Errorf("%s: %w", strings.Join(other, ", "), ErrConflict))
		}
		for _, path := range paths {
			if !slices.ContainsFunc(replaced, func(r Replaced) bool { return r.Path == path }) {
				r := Replaced{Path: path}
				if r.Local, err = g.content(dir, local, path); err != nil {
					return fail(err)
				}
				if base != "" {
					if r.Base, err = g.content(dir, base, path); err != nil {
						return fail(err)
					}
				}
				replaced = append(replaced, r)
			}
			if err := g.takeFrom(dir, remote, path); err != nil {
				return fail(err)
			}
		}
		// A commit that changed only replaced files has nothing left in it.
		staged, serr := g.names(dir, "diff", "--cached", "--name-only", "-z")
		switch {
		case serr != nil:
			return fail(serr)
		case len(staged) == 0:
			err = step("rebase", "--skip")
		default:
			err = step("rebase", "--continue")
		}
	}
	return replaced, nil
}

// rebaseArgs is what goes before a rebase command so that it can commit: nothing when git has an
// identity, else the author of the last local commit, which Commit made with the Author it was given.
func (g Git) rebaseArgs(dir string) ([]string, error) {
	if g.hasIdentity(dir) {
		return nil, nil
	}
	out, err := g.run(dir, 0, "log", "-1", "--format=%an%x00%ae", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	name, email, _ := strings.Cut(strings.TrimSuffix(string(out), "\n"), "\x00")
	return []string{"-c", "user.name=" + name, "-c", "user.email=" + email}, nil
}

// rebasing reports whether a rebase is stopped part way.
func (g Git) rebasing(dir string) bool {
	_, err := g.run(dir, 0, "rev-parse", "--verify", "--quiet", "REBASE_HEAD")
	return err == nil
}

// takeFrom makes path, in the work tree and the index, what rev holds: its content, or no file.
func (g Git) takeFrom(dir, rev, path string) error {
	held, err := g.holds(dir, rev, path)
	if err != nil {
		return err
	}
	if held {
		_, err = g.run(dir, 0, "checkout", "--quiet", rev, "--", path)
	} else {
		_, err = g.run(dir, 0, "rm", "--quiet", "--force", "--", path)
	}
	return err
}

// holds reports whether rev has a file at path.
func (g Git) holds(dir, rev, path string) (bool, error) {
	names, err := g.names(dir, "ls-tree", "-z", "--name-only", rev, "--", path)
	return len(names) > 0, err
}

// content is path's bytes at rev, nil when rev has no such file.
func (g Git) content(dir, rev, path string) ([]byte, error) {
	held, err := g.holds(dir, rev, path)
	if err != nil || !held {
		return nil, err
	}
	out, err := g.run(dir, 0, "show", rev+":"+path)
	if err != nil {
		return nil, err
	}
	// An empty file is not a missing one.
	return append([]byte{}, out...), nil
}

// names runs a git command that prints NUL-terminated paths.
func (g Git) names(dir string, args ...string) ([]string, error) {
	out, err := g.run(dir, 0, args...)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// revParse is the commit rev names; a file of the same name does not make it ambiguous.
func (g Git) revParse(dir, rev string) (string, error) {
	out, err := g.run(dir, 0, "rev-parse", "--verify", rev+"^{commit}")
	return strings.TrimSpace(string(out)), err
}
