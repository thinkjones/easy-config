package gitstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A push the remote refuses because its branch moved is ErrRemoteMoved; any other failure is not.
func TestPushSaysWhenTheRemoteMoved(t *testing.T) {
	g := newGit(t)
	bare := published(t, g)
	mine, theirs := clone(t, g, bare), clone(t, g, bare)
	commit(t, g, theirs, "theirs.yaml", "A: 1\n")
	if _, err := g.Push(theirs); err != nil {
		t.Fatal(err)
	}
	// No fetch: the store does not know the remote moved.
	commit(t, g, mine, "mine.yaml", "A: 1\n")
	if _, err := g.Push(mine); !errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("Push = %v, want ErrRemoteMoved", err)
	}

	// A remote that refuses the push for a reason of its own has not moved.
	if err := g.Fetch(mine, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Pull(mine, 0, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Push(mine); err == nil || errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("Push to a remote that refuses = %v", err)
	}
}

// Uncommit moves the branch back and nothing else: Restore then puts the commit's files back, and a file
// changed by hand, staged or not, is as it was.
func TestUncommitTakesBackOneCommitAndTouchesNothingElse(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	commit(t, g, dir, "other.yaml", "A: 1\n")
	commit(t, g, dir, "staged.yaml", "A: 1\n")
	parent, err := g.Head(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "other.yaml", "A: by hand\n")
	write(t, dir, "staged.yaml", "A: staged by hand\n")
	raw(t, g, dir, "add", "staged.yaml")
	write(t, dir, "untracked.yaml", "A: 1\n")

	write(t, dir, "common.yaml", "A: 2\n")
	write(t, dir, "new.yaml", "B: 1\n")
	if done, err := g.Commit(dir, "the change", author, "common.yaml", "new.yaml"); err != nil || !done {
		t.Fatalf("Commit: %v %v", done, err)
	}
	made, err := g.Head(dir)
	if err != nil || made == parent {
		t.Fatalf("Head = %q, %v", made, err)
	}
	before := raw(t, g, dir, "status", "--porcelain", "--untracked-files=all")

	if err := g.Uncommit(dir, parent, made); err != nil {
		t.Fatalf("Uncommit: %v", err)
	}
	if got := raw(t, g, dir, "rev-parse", "HEAD"); got != parent {
		t.Fatalf("HEAD is %s, want %s", got, parent)
	}
	if got := raw(t, g, dir, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Fatalf("HEAD is on %s", got)
	}
	if err := g.Restore(dir, "common.yaml", "new.yaml"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "common.yaml")); string(got) != "A: 1\n" {
		t.Errorf("common.yaml is %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.yaml")); err == nil {
		t.Error("new.yaml is still there")
	}
	if got := raw(t, g, dir, "status", "--porcelain", "--untracked-files=all"); got != before {
		t.Errorf("status is now\n%s\nwas\n%s", got, before)
	}
	for name, want := range map[string]string{"other.yaml": "A: by hand\n", "staged.yaml": "A: staged by hand\n", "untracked.yaml": "A: 1\n"} {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
}

// A branch that is no longer at the commit to take back is left where it is.
func TestUncommitLeavesABranchThatMoved(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	parent, _ := g.Head(dir)
	commit(t, g, dir, "a.yaml", "A: 1\n")
	made, _ := g.Head(dir)
	commit(t, g, dir, "b.yaml", "A: 1\n")
	now, _ := g.Head(dir)
	if err := g.Uncommit(dir, parent, made); err == nil {
		t.Fatal("Uncommit moved a branch that was elsewhere")
	}
	if got := raw(t, g, dir, "rev-parse", "HEAD"); got != now {
		t.Fatalf("HEAD is %s, want %s", got, now)
	}
}
