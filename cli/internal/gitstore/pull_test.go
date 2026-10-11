package gitstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
)

// encrypted says which files take the remote's version.
func encrypted(path string) bool { return strings.HasSuffix(path, ".enc.yaml") }

// pair is two clones of one remote: the store under test and a teammate's.
func pair(t *testing.T, g Git) (mine, theirs string) {
	t.Helper()
	bare := published(t, g)
	return clone(t, g, bare), clone(t, g, bare)
}

// publish commits name in dir and pushes it.
func publish(t *testing.T, g Git, dir, name, body string) {
	t.Helper()
	commit(t, g, dir, name, body)
	if _, err := g.Push(dir); err != nil {
		t.Fatalf("Push: %v", err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func pull(t *testing.T, g Git, dir string) PullResult {
	t.Helper()
	res, err := g.Pull(dir, time.Minute, encrypted)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	wantNoRebase(t, g, dir)
	return res
}

func wantNoRebase(t *testing.T, g Git, dir string) {
	t.Helper()
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", name)); err == nil {
			t.Fatalf("a rebase is left in progress (.git/%s)", name)
		}
	}
	if st := status(t, g, dir); st.Branch != "main" {
		t.Fatalf("the store is on %q, want main", st.Branch)
	}
}

// wantUnchanged checks a failed pull left the store at head with a clean tree.
func wantUnchanged(t *testing.T, g Git, dir, head string) {
	t.Helper()
	wantNoRebase(t, g, dir)
	if got := raw(t, g, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD is %s, want %s", got, head)
	}
	if out := raw(t, g, dir, "status", "--porcelain"); out != "" {
		t.Fatalf("the tree is not clean:\n%s", out)
	}
}

func TestPullFastForward(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "common.yaml", "A: 2\n")
	publish(t, g, theirs, "env.local.yaml", "B: 1\n")

	res := pull(t, g, mine)
	if res.Commits != 2 || len(res.Replaced) != 0 {
		t.Fatalf("result %+v, want 2 commits and nothing replaced", res)
	}
	wantCounts(t, g, mine, InSync, 0, 0)
	if got := read(t, mine, "common.yaml"); got != "A: 2\n" {
		t.Fatalf("common.yaml = %q", got)
	}
}

func TestPullIntoEmptyStore(t *testing.T) {
	g := newGit(t)
	mine := filepath.Join(t.TempDir(), "store")
	if err := g.Init(mine); err != nil {
		t.Fatal(err)
	}
	if err := g.SetRemote(mine, published(t, g)); err != nil {
		t.Fatal(err)
	}
	if res := pull(t, g, mine); res.Commits != 1 {
		t.Fatalf("result %+v, want 1 commit", res)
	}
	wantCounts(t, g, mine, InSync, 0, 0)
}

func TestPullRebasesPlaintext(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "common.yaml", "A: 2\n")
	commit(t, g, mine, "env.local.yaml", "B: 1\n")

	res := pull(t, g, mine)
	if res.Commits != 1 || len(res.Replaced) != 0 {
		t.Fatalf("result %+v, want 1 commit and nothing replaced", res)
	}
	if a, b := read(t, mine, "common.yaml"), read(t, mine, "env.local.yaml"); a != "A: 2\n" || b != "B: 1\n" {
		t.Fatalf("common.yaml = %q, env.local.yaml = %q", a, b)
	}
	wantCounts(t, g, mine, Ahead, 1, 0)
}

func TestPullKeepsRemoteForEncrypted(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "base\n")
	pull(t, g, mine)

	publish(t, g, theirs, "x.enc.yaml", "remote\n")
	write(t, mine, "x.enc.yaml", "local\n")
	write(t, mine, "env.local.yaml", "B: 1\n")
	if done, err := g.Commit(mine, "set two", author, "x.enc.yaml", "env.local.yaml"); err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}

	res := pull(t, g, mine)
	if res.Commits != 1 || len(res.Replaced) != 1 {
		t.Fatalf("result %+v, want 1 commit and 1 replaced", res)
	}
	r := res.Replaced[0]
	if r.Path != "x.enc.yaml" || string(r.Base) != "base\n" || string(r.Local) != "local\n" {
		t.Fatalf("replaced %q base %q local %q", r.Path, r.Base, r.Local)
	}
	if got := read(t, mine, "x.enc.yaml"); got != "remote\n" {
		t.Fatalf("x.enc.yaml = %q, want the remote's", got)
	}
	if got := read(t, mine, "env.local.yaml"); got != "B: 1\n" {
		t.Fatalf("env.local.yaml = %q, want the local change", got)
	}
	wantCounts(t, g, mine, Ahead, 1, 0)
}

// A file both sides created has no ancestor.
func TestPullReplacedWithoutBase(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "remote\n")
	commit(t, g, mine, "x.enc.yaml", "local\n")

	res := pull(t, g, mine)
	if len(res.Replaced) != 1 || res.Replaced[0].Base != nil || string(res.Replaced[0].Local) != "local\n" {
		t.Fatalf("result %+v, want one replaced file with no base", res)
	}
	if got := read(t, mine, "x.enc.yaml"); got != "remote\n" {
		t.Fatalf("x.enc.yaml = %q, want the remote's", got)
	}
}

// The remote's version of a file it deleted is no file.
func TestPullKeepsRemoteDeletion(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "base\n")
	pull(t, g, mine)

	if err := os.Remove(filepath.Join(theirs, "x.enc.yaml")); err != nil {
		t.Fatal(err)
	}
	if done, err := g.Commit(theirs, "remove x.enc.yaml", author, "x.enc.yaml"); err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}
	if _, err := g.Push(theirs); err != nil {
		t.Fatal(err)
	}
	commit(t, g, mine, "x.enc.yaml", "local\n")

	res := pull(t, g, mine)
	if len(res.Replaced) != 1 || string(res.Replaced[0].Local) != "local\n" || string(res.Replaced[0].Base) != "base\n" {
		t.Fatalf("result %+v, want one replaced file", res)
	}
	if _, err := os.Stat(filepath.Join(mine, "x.enc.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("x.enc.yaml is still there: %v", err)
	}
	wantCounts(t, g, mine, InSync, 0, 0)
}

func TestPullLocalCommitBecomesEmpty(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "base\n")
	pull(t, g, mine)

	publish(t, g, theirs, "x.enc.yaml", "remote\n")
	commit(t, g, mine, "x.enc.yaml", "local\n")

	res := pull(t, g, mine)
	if res.Commits != 1 || len(res.Replaced) != 1 {
		t.Fatalf("result %+v, want 1 commit and 1 replaced", res)
	}
	if got := read(t, mine, "x.enc.yaml"); got != "remote\n" {
		t.Fatalf("x.enc.yaml = %q, want the remote's", got)
	}
	wantCounts(t, g, mine, InSync, 0, 0)
}

// A file replaced in two local commits is reported once, with what it held before the pull.
func TestPullReplacedOnce(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "base\n")
	pull(t, g, mine)

	publish(t, g, theirs, "x.enc.yaml", "remote\n")
	commit(t, g, mine, "x.enc.yaml", "local 1\n")
	commit(t, g, mine, "env.local.yaml", "B: 1\n")
	commit(t, g, mine, "x.enc.yaml", "local 2\n")

	res := pull(t, g, mine)
	if len(res.Replaced) != 1 || string(res.Replaced[0].Local) != "local 2\n" || string(res.Replaced[0].Base) != "base\n" {
		t.Fatalf("result %+v, want one replaced file holding the last local content", res)
	}
	if got := read(t, mine, "x.enc.yaml"); got != "remote\n" {
		t.Fatalf("x.enc.yaml = %q, want the remote's", got)
	}
	wantCounts(t, g, mine, Ahead, 1, 0)
}

func TestPullConflictAborts(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "common.yaml", "A: remote-value\n")
	commit(t, g, mine, "env.local.yaml", "B: 1\n")
	commit(t, g, mine, "common.yaml", "A: local-value\n")
	head := raw(t, g, mine, "rev-parse", "HEAD")

	_, err := g.Pull(mine, time.Minute, encrypted)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Pull: %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "common.yaml") {
		t.Fatalf("the error does not name the file: %v", err)
	}
	if strings.Contains(err.Error(), "value") {
		t.Fatalf("the error carries content: %v", err)
	}
	wantUnchanged(t, g, mine, head)
	if got := read(t, mine, "common.yaml"); got != "A: local-value\n" {
		t.Fatalf("common.yaml = %q", got)
	}
}

// One conflict that may not be replaced stops the pull, even beside one that may.
func TestPullConflictBesideEncrypted(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "remote\n")
	publish(t, g, theirs, "common.yaml", "A: 2\n")
	write(t, mine, "x.enc.yaml", "local\n")
	write(t, mine, "common.yaml", "A: 3\n")
	if done, err := g.Commit(mine, "set two", author, "x.enc.yaml", "common.yaml"); err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}
	head := raw(t, g, mine, "rev-parse", "HEAD")

	res, err := g.Pull(mine, time.Minute, encrypted)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "common.yaml") || strings.Contains(err.Error(), "x.enc.yaml") {
		t.Fatalf("Pull: %v, want ErrConflict naming common.yaml only", err)
	}
	if len(res.Replaced) != 0 {
		t.Fatalf("a failed pull replaced %+v", res.Replaced)
	}
	wantUnchanged(t, g, mine, head)
	if got := read(t, mine, "x.enc.yaml"); got != "local\n" {
		t.Fatalf("x.enc.yaml = %q", got)
	}
}

func TestPullDirty(t *testing.T) {
	g := newGit(t)
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "common.yaml", "A: 2\n")
	head := raw(t, g, mine, "rev-parse", "HEAD")
	write(t, mine, "common.yaml", "A: by hand\n")

	if _, err := g.Pull(mine, time.Minute, encrypted); !errors.Is(err, ErrDirty) {
		t.Fatalf("Pull: %v, want ErrDirty", err)
	}
	wantNoRebase(t, g, mine)
	if got := raw(t, g, mine, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
	if got := read(t, mine, "common.yaml"); got != "A: by hand\n" {
		t.Fatalf("common.yaml = %q", got)
	}
}

func TestPullNoRemote(t *testing.T) {
	g := newGit(t)
	if _, err := g.Pull(newStore(t, g), time.Minute, encrypted); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("Pull: %v, want ErrNoRemote", err)
	}
}

func TestPullUnreachable(t *testing.T) {
	g := newGit(t)
	gone := published(t, g)
	mine := clone(t, g, gone)
	commit(t, g, mine, "env.local.yaml", "B: 1\n")
	head := raw(t, g, mine, "rev-parse", "HEAD")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	if _, err := g.Pull(mine, time.Minute, encrypted); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Pull: %v, want ErrUnreachable", err)
	}
	wantUnchanged(t, g, mine, head)
}

func TestPullNothingToDo(t *testing.T) {
	g := newGit(t)
	mine, _ := pair(t, g)
	if res := pull(t, g, mine); res.Commits != 0 || len(res.Replaced) != 0 {
		t.Fatalf("result %+v, want nothing", res)
	}
	wantCounts(t, g, mine, InSync, 0, 0)

	// Ahead only: nothing to receive, and the local commit stays.
	commit(t, g, mine, "env.local.yaml", "B: 1\n")
	if res := pull(t, g, mine); res.Commits != 0 {
		t.Fatalf("result %+v, want nothing", res)
	}
	wantCounts(t, g, mine, Ahead, 1, 0)
}

// A rebase commits; with no identity of git's own it must still finish.
func TestPullRebasesWithoutIdentity(t *testing.T) {
	g := Git{Run: gittest.NoIdentity(t)}
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "remote\n")
	write(t, mine, "x.enc.yaml", "local\n")
	write(t, mine, "env.local.yaml", "B: 1\n")
	if done, err := g.Commit(mine, "set two", author, "x.enc.yaml", "env.local.yaml"); err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}

	if res := pull(t, g, mine); res.Commits != 1 || len(res.Replaced) != 1 {
		t.Fatalf("result %+v, want 1 commit and 1 replaced", res)
	}
	wantCounts(t, g, mine, Ahead, 1, 0)
	if got := raw(t, g, mine, "log", "-1", "--format=%an <%ae> / %cn <%ce>"); got != author.Name+" <ezc@localhost> / "+author.Name+" <ezc@localhost>" {
		t.Fatalf("the rebased commit is by %q", got)
	}
}

// An editor in the environment is never opened for a rebased commit's message.
func TestPullNeverOpensAnEditor(t *testing.T) {
	g := newGit(t)
	g.Environ = []string{"GIT_EDITOR=false", "PATH=" + os.Getenv("PATH")}
	mine, theirs := pair(t, g)
	publish(t, g, theirs, "x.enc.yaml", "remote\n")
	write(t, mine, "x.enc.yaml", "local\n")
	write(t, mine, "env.local.yaml", "B: 1\n")
	if done, err := g.Commit(mine, "set two", author, "x.enc.yaml", "env.local.yaml"); err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}
	pull(t, g, mine)
	wantCounts(t, g, mine, Ahead, 1, 0)
}

// A store made here and then pointed at a remote shares no history with it.
func TestPullUnrelatedHistories(t *testing.T) {
	g := newGit(t)
	bare := gittest.Bare(t)
	theirs := filepath.Join(t.TempDir(), "theirs")
	if err := g.Init(theirs); err != nil {
		t.Fatal(err)
	}
	if err := g.SetRemote(theirs, bare); err != nil {
		t.Fatal(err)
	}
	publish(t, g, theirs, "x.enc.yaml", "remote\n")

	mine := filepath.Join(t.TempDir(), "mine")
	if err := g.Init(mine); err != nil {
		t.Fatal(err)
	}
	commit(t, g, mine, "x.enc.yaml", "local\n")
	commit(t, g, mine, "env.local.yaml", "B: 1\n")
	if err := g.SetRemote(mine, bare); err != nil {
		t.Fatal(err)
	}

	res := pull(t, g, mine)
	if len(res.Replaced) != 1 || res.Replaced[0].Base != nil || string(res.Replaced[0].Local) != "local\n" {
		t.Fatalf("result %+v, want one replaced file with no base", res)
	}
	if got := read(t, mine, "x.enc.yaml"); got != "remote\n" {
		t.Fatalf("x.enc.yaml = %q, want the remote's", got)
	}
	wantCounts(t, g, mine, Ahead, 1, 0)
}
