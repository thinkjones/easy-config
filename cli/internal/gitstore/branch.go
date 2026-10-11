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

// existsOnRemote reports whether origin has the branch.
func (g Git) existsOnRemote(dir, branch string, timeout time.Duration) (bool, error) {
	out, err := g.run(dir, timeout, g.remote("ls-remote", "--heads", "origin", "refs/heads/"+branch)...)
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
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
	out, err := g.run(dir, 0, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return fmt.Errorf("git: %w", ErrNoBranch)
	}
	original := strings.TrimSpace(string(out))

	created := false
	defer func() {
		var back, drop error
		if created {
			_, back = g.run(dir, 0, "checkout", "--quiet", original)
			_, drop = g.run(dir, 0, "branch", "-D", "--quiet", branch)
		}
		restored := g.Restore(dir, paths...)
		if err == nil {
			err = errors.Join(back, drop, restored)
		}
	}()
	taken, err := g.existsOnRemote(dir, branch, 0)
	if err != nil {
		return err
	}
	if taken {
		return g.sameOnRemote(dir, branch, paths)
	}
	if _, err := g.run(dir, 0, "checkout", "--quiet", "-b", branch); err != nil {
		return err
	}
	created = true
	if _, err := g.Commit(dir, message, a, paths...); err != nil {
		return err
	}
	_, err = g.run(dir, 0, g.remote("push", "--quiet", "origin", branch)...)
	return err
}

// sameOnRemote is nil when origin's branch holds, at each path, the bytes the work tree has.
func (g Git) sameOnRemote(dir, branch string, paths []string) error {
	if _, err := g.run(dir, 0, g.remote("fetch", "--quiet", "origin", "+refs/heads/"+branch+":refs/remotes/origin/"+branch)...); err != nil {
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
	out, err := g.run(dir, 0, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ref := range strings.Fields(string(out)) {
		name := strings.TrimPrefix(ref, "refs/remotes/origin/")
		if name != "HEAD" && strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Show is the file at path on origin/ref as of the last fetch; fs.ErrNotExist when the branch has no such file.
func (g Git) Show(dir, ref, path string) ([]byte, error) {
	out, err := g.run(dir, 0, "ls-tree", "-z", "--name-only", "origin/"+ref, "--", path)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fs.ErrNotExist
	}
	return g.run(dir, 0, "cat-file", "blob", "origin/"+ref+":"+path)
}

// LastAuthor is "Name <email>" of the last commit on origin/ref that touched path.
func (g Git) LastAuthor(dir, ref, path string) (string, error) {
	out, err := g.run(dir, 0, "log", "-1", "--format=%an <%ae>", "origin/"+ref, "--", path)
	if err != nil {
		return "", err
	}
	author := strings.TrimSpace(string(out))
	if author == "" {
		return "", fs.ErrNotExist
	}
	return author, nil
}

// DeleteRemoteBranch removes the branch from origin; nil when it is already gone.
func (g Git) DeleteRemoteBranch(dir, branch string, timeout time.Duration) error {
	if remote, err := g.Remote(dir); err != nil {
		return err
	} else if remote == "" {
		return ErrNoRemote
	}
	exists, err := g.existsOnRemote(dir, branch, timeout)
	if err != nil || !exists {
		return err
	}
	_, err = g.run(dir, timeout, g.remote("push", "--quiet", "origin", "--delete", branch)...)
	return err
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
