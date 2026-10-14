package gitstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// remoteRef is the commit the ref of exactly this full name is at on origin now, asked of origin; "" when origin
// has none. ls-remote matches its pattern against the end of a name, so a/refs/heads/x answers for refs/heads/x:
// only the line whose name is the whole of ref counts.
func (g Git) remoteRef(dir, ref string, timeout time.Duration) (string, error) {
	out, err := g.run(dir, timeout, g.remote("ls-remote", "origin", ref)...)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if commit, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return commit, nil
		}
	}
	return "", nil
}

// onRemote is the commit origin's branch is at now; "" when origin has no such branch.
func (g Git) onRemote(dir, branch string, timeout time.Duration) (string, error) {
	return g.remoteRef(dir, head(branch), timeout)
}

// TagError is a push that origin refused because it has a tag whose own name is refs/heads/<Branch>: git then
// finds two refs for the branch's full name. Nothing is read from such a tag; it only blocks the push.
type TagError struct{ Branch string }

func (e *TagError) Error() string {
	return "git: the remote has a tag named " + head(e.Branch) + ", which blocks pushes to that branch. Delete the tag on the remote."
}

// push runs a push that names branch on origin as head(branch). When it fails and origin has the tag that makes
// that name ambiguous, the error is a *TagError; else it is git's.
func (g Git) push(dir, branch string, timeout time.Duration, args ...string) error {
	_, err := g.run(dir, timeout, g.remote(append([]string{"push", "--quiet"}, args...)...)...)
	if err == nil {
		return nil
	}
	if tag, terr := g.remoteRef(dir, "refs/tags/"+head(branch), timeout); terr == nil && tag != "" {
		return &TagError{Branch: branch}
	}
	return err
}

// PushBranch commits paths (relative to dir) on a new branch cut from the current commit, pushes it to origin
// and puts the work tree back on the branch it was on, with paths gone from it and the local branch deleted;
// a failure does the same. When origin already has the branch, nothing is pushed: the result is nil if it
// holds the same bytes at each of paths and ErrBranchTaken if not. It never forces a push.
func (g Git) PushBranch(dir, branch, message string, a Author, paths ...string) (err error) {
	if remote, err := g.Remote(dir); err != nil {
		return err
	} else if remote == "" {
		return ErrNoRemote
	}
	// The full name: the short one is not the branch's name when a tag is called the same.
	out, err := g.run(dir, 0, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return fmt.Errorf("git: %w", ErrNoBranch)
	}
	original := strings.TrimPrefix(strings.TrimSpace(string(out)), head(""))

	created := false
	defer func() {
		var back, drop error
		if created {
			// switch takes a branch only: checkout would take a tag of the same name, and leave HEAD detached on it.
			_, back = g.run(dir, 0, "switch", "--quiet", original)
			_, drop = g.run(dir, 0, "branch", "-D", "--quiet", branch)
		}
		restored := g.Restore(dir, paths...)
		if err == nil {
			err = errors.Join(back, drop, restored)
		}
	}()
	taken, err := g.onRemote(dir, branch, 0)
	if err != nil {
		return err
	}
	if taken != "" {
		return g.sameOnRemote(dir, branch, paths)
	}
	if _, err := g.run(dir, 0, "checkout", "--quiet", "-b", branch); err != nil {
		return err
	}
	created = true
	if _, err := g.Commit(dir, message, a, paths...); err != nil {
		return err
	}
	return g.push(dir, branch, 0, "origin", head(branch)+":"+head(branch))
}

// sameOnRemote is nil when origin's branch holds, at each path, the bytes the work tree has.
func (g Git) sameOnRemote(dir, branch string, paths []string) error {
	if _, err := g.run(dir, 0, g.remote("fetch", "--quiet", "--no-tags", "origin", "+"+head(branch)+":"+tracking(branch))...); err != nil {
		return err
	}
	for _, p := range paths {
		want, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			return err
		}
		have, err := g.Show(dir, branch, p)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !bytes.Equal(have, want)) {
			return ErrBranchTaken
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RemoteBranches are the branches of origin whose names start with prefix, as of the last fetch, without
// "origin/", sorted.
func (g Git) RemoteBranches(dir, prefix string) ([]string, error) {
	out, err := g.run(dir, 0, "for-each-ref", "--format=%(refname)", tracking(""))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ref := range strings.Fields(string(out)) {
		name := strings.TrimPrefix(ref, tracking(""))
		if name != "HEAD" && strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// RemoteCommit is the commit origin's branch was at as of the last fetch. What is read of the branch afterwards is
// read at that commit (ShowAt, LastAuthorAt), so that a later fetch cannot change it under the reader.
func (g Git) RemoteCommit(dir, branch string) (string, error) {
	return g.revParse(dir, tracking(branch))
}

// Show is the file at path on origin's branch ref as of the last fetch; fs.ErrNotExist when the branch has no such file.
func (g Git) Show(dir, ref, path string) ([]byte, error) {
	return g.ShowAt(dir, tracking(ref), path)
}

// ShowAt is the file at path in a commit, given as RemoteCommit gives it; fs.ErrNotExist when it has no such file.
func (g Git) ShowAt(dir, commit, path string) ([]byte, error) {
	out, err := g.run(dir, 0, "ls-tree", "-z", "--name-only", commit, "--", path)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fs.ErrNotExist
	}
	return g.run(dir, 0, "cat-file", "blob", commit+":"+path)
}

// LastAuthor is "Name <email>" of the last commit on origin's branch ref, as of the last fetch, that touched path.
func (g Git) LastAuthor(dir, ref, path string) (string, error) {
	return g.LastAuthorAt(dir, tracking(ref), path)
}

// LastAuthorAt is "Name <email>" of the last commit that touched path, among commit and those before it.
func (g Git) LastAuthorAt(dir, commit, path string) (string, error) {
	out, err := g.run(dir, 0, "log", "-1", "--format=%an <%ae>", commit, "--", path)
	if err != nil {
		return "", err
	}
	author := strings.TrimSpace(string(out))
	if author == "" {
		return "", fs.ErrNotExist
	}
	return author, nil
}

// DeleteRemoteBranch removes the branch from origin; nil when it is already gone. With commit not "", the branch is
// deleted only while origin still has it at that commit: one that has moved since it was read is left, and the
// error is ErrBranchMoved.
func (g Git) DeleteRemoteBranch(dir, branch, commit string, timeout time.Duration) error {
	if remote, err := g.Remote(dir); err != nil {
		return err
	} else if remote == "" {
		return ErrNoRemote
	}
	now, err := g.onRemote(dir, branch, timeout)
	if err != nil || now == "" {
		return err
	}
	var args []string
	if commit != "" {
		if now != commit {
			return fmt.Errorf("git: %w", ErrBranchMoved)
		}
		// The lease covers a push that lands between the question above and this one.
		args = append(args, "--force-with-lease="+head(branch)+":"+commit)
	}
	return g.push(dir, branch, timeout, append(args, "origin", ":"+head(branch))...)
}

// Restore puts the tracked paths back to the last commit and removes the untracked ones among them.
func (g Git) Restore(dir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	if _, err := g.run(dir, 0, append([]string{"reset", "--quiet", "--"}, paths...)...); err != nil {
		return err
	}
	out, err := g.run(dir, 0, append([]string{"ls-files", "-z", "--"}, paths...)...)
	if err != nil {
		return err
	}
	if tracked := slices.DeleteFunc(strings.Split(string(out), "\x00"), func(s string) bool { return s == "" }); len(tracked) > 0 {
		if _, err := g.run(dir, 0, append([]string{"checkout", "--quiet", "--"}, tracked...)...); err != nil {
			return err
		}
	}
	_, err = g.run(dir, 0, append([]string{"clean", "--quiet", "--force", "-d", "--"}, paths...)...)
	return err
}
