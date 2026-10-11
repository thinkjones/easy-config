package gitstore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

var author = Author{Name: "ezc on test-machine"}

func newGit(t *testing.T) Git {
	t.Helper()
	return Git{Run: gittest.Runner(t)}
}

// raw runs git directly, for what the tests assert about a repo.
func raw(t *testing.T, g Git, dir string, args ...string) string {
	t.Helper()
	stdout, stderr, err := g.Run(proc.Command{Dir: dir, Name: "git", Args: args})
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(stdout))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commit writes name and commits it.
func commit(t *testing.T, g Git, dir, name, body string) {
	t.Helper()
	write(t, dir, name, body)
	done, err := g.Commit(dir, "set "+name, author, name)
	if err != nil || !done {
		t.Fatalf("Commit %s: done=%v err=%v", name, done, err)
	}
}

// newStore is a store with one commit and no remote.
func newStore(t *testing.T, g Git) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	if err := g.Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	commit(t, g, dir, "common.yaml", "A: 1\n")
	return dir
}

// published is a bare remote holding one commit on main.
func published(t *testing.T, g Git) string {
	t.Helper()
	bare := gittest.Bare(t)
	seed := newStore(t, g)
	if err := g.SetRemote(seed, bare); err != nil {
		t.Fatalf("SetRemote: %v", err)
	}
	if _, err := g.Push(seed); err != nil {
		t.Fatalf("Push: %v", err)
	}
	return bare
}

func clone(t *testing.T, g Git, source string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	if err := g.Clone(source, dir); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	return dir
}

func status(t *testing.T, g Git, dir string) Status {
	t.Helper()
	st, err := g.Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return st
}

func wantCounts(t *testing.T, g Git, dir string, state State, ahead, behind int) {
	t.Helper()
	st := status(t, g, dir)
	if st.State() != state || st.Ahead != ahead || st.Behind != behind {
		t.Fatalf("state %d ahead %d behind %d, want state %d ahead %d behind %d", st.State(), st.Ahead, st.Behind, state, ahead, behind)
	}
}

func TestInitCommitStatus(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	st := status(t, g, dir)
	if st.State() != InSync || st.Branch != "main" || st.Remote != "" || st.Dirty || st.Ahead != 0 || st.Behind != 0 {
		t.Fatalf("unexpected status: %+v", st)
	}
	if url, err := g.Remote(dir); err != nil || url != "" {
		t.Fatalf("Remote: %q, %v", url, err)
	}
}

func TestIsRepo(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	if !g.IsRepo(dir) {
		t.Fatal("a store made by Init is not a repo")
	}
	inside := filepath.Join(dir, "plain")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if g.IsRepo(inside) {
		t.Fatal("a folder inside a repo is not the top of a work tree")
	}
	if g.IsRepo(filepath.Join(dir, ".git")) {
		t.Fatal("a .git folder is not a work tree")
	}
	if g.IsRepo(t.TempDir()) {
		t.Fatal("a plain folder is not a repo")
	}
	if g.IsRepo(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("a folder that does not exist is not a repo")
	}
	if g.IsRepo(gittest.Bare(t)) {
		t.Fatal("a bare repo is not a work tree")
	}
}

func TestInitEmptyStore(t *testing.T) {
	g := newGit(t)
	dir := filepath.Join(t.TempDir(), "a", "store")
	if err := g.Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	st := status(t, g, dir)
	if st.State() != InSync || st.Branch != "main" {
		t.Fatalf("unexpected status of an empty store: %+v", st)
	}
	if err := g.SetRemote(dir, gittest.Bare(t)); err != nil {
		t.Fatal(err)
	}
	if n, err := g.Push(dir); err != nil || n != 0 {
		t.Fatalf("Push with nothing to send: %d, %v", n, err)
	}
}

func TestCommitOnlyNamedPaths(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	commit(t, g, dir, "other.yaml", "B: 1\n")
	commit(t, g, dir, "staged.yaml", "C: 1\n")

	write(t, dir, "common.yaml", "A: 2\n")
	write(t, dir, "other.yaml", "B: 2\n")
	write(t, dir, "staged.yaml", "C: 2\n")
	raw(t, g, dir, "add", "staged.yaml")

	done, err := g.Commit(dir, "set A", author, "common.yaml")
	if err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}
	if got := raw(t, g, dir, "show", "--name-only", "--format=", "HEAD"); got != "common.yaml" {
		t.Fatalf("the commit holds %q, want common.yaml only", got)
	}
	if got := raw(t, g, dir, "log", "-1", "--format=%s"); got != "set A" {
		t.Fatalf("message %q", got)
	}
	if !status(t, g, dir).Dirty {
		t.Fatal("the files left alone should still be uncommitted")
	}
	if got := raw(t, g, dir, "status", "--porcelain"); got != "M other.yaml\nM  staged.yaml" && got != " M other.yaml\nM  staged.yaml" {
		t.Fatalf("left behind:\n%s", got)
	}
}

func TestCommitNothingChanged(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	for _, paths := range [][]string{{"common.yaml"}, {"never-there.yaml"}, {"common.yaml", "never-there.yaml"}, nil} {
		done, err := g.Commit(dir, "nothing", author, paths...)
		if err != nil || done {
			t.Fatalf("Commit %v: done=%v err=%v", paths, done, err)
		}
	}
	if got := raw(t, g, dir, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("%s commits, want 1", got)
	}
}

func TestCommitDeletion(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	commit(t, g, dir, "env.dev.yaml", "B: 1\n")
	if err := os.Remove(filepath.Join(dir, "env.dev.yaml")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "new.yaml", "C: 1\n")
	done, err := g.Commit(dir, "remove B", author, "env.dev.yaml", "new.yaml", "never-there.yaml")
	if err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}
	if got := raw(t, g, dir, "show", "--name-status", "--format=", "HEAD"); got != "D\tenv.dev.yaml\nA\tnew.yaml" {
		t.Fatalf("the commit holds:\n%s", got)
	}
	if status(t, g, dir).Dirty {
		t.Fatal("nothing should be left uncommitted")
	}
}

func TestCommitPathIsLiteral(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	write(t, dir, "a.yaml", "A: 1\n")
	write(t, dir, "*.yaml", "B: 1\n")
	done, err := g.Commit(dir, "star", author, "*.yaml")
	if err != nil || !done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}
	if got := raw(t, g, dir, "show", "--name-only", "--format=", "HEAD"); got != "*.yaml" {
		t.Fatalf("the commit holds %q", got)
	}
}

func TestCommitWithoutIdentity(t *testing.T) {
	for _, run := range []proc.Runner{gittest.Runner(t), gittest.NoIdentity(t)} {
		g := Git{Run: run}
		dir := newStore(t, g)
		if got := raw(t, g, dir, "log", "-1", "--format=%an|%cn"); got != author.Name+"|"+author.Name {
			t.Fatalf("author|committer %q, want %q for both", got, author.Name)
		}
	}

	g := Git{Run: gittest.WithIdentity(t)}
	dir := newStore(t, g)
	if got := raw(t, g, dir, "log", "-1", "--format=%an|%ae|%cn"); got != gittest.Name+"|"+gittest.Email+"|"+gittest.Name {
		t.Fatalf("identity %q, want git's own", got)
	}
}

func TestAheadBehindDiverged(t *testing.T) {
	g := newGit(t)
	bare := published(t, g)
	a, b := clone(t, g, bare), clone(t, g, bare)
	wantCounts(t, g, a, InSync, 0, 0)
	if st := status(t, g, a); st.Remote != bare || st.Branch != "main" {
		t.Fatalf("unexpected status: %+v", st)
	}

	commit(t, g, a, "one.yaml", "A: 1\n")
	commit(t, g, a, "two.yaml", "A: 1\n")
	wantCounts(t, g, a, Ahead, 2, 0)
	if n, err := g.Push(a); err != nil || n != 2 {
		t.Fatalf("Push: %d, %v", n, err)
	}
	wantCounts(t, g, a, InSync, 0, 0)

	if err := g.Fetch(b, time.Minute); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	wantCounts(t, g, b, Behind, 0, 2)

	commit(t, g, b, "three.yaml", "A: 1\n")
	wantCounts(t, g, b, Diverged, 1, 2)
	if _, err := g.Push(b); err == nil {
		t.Fatal("a push that would lose the remote's commits succeeded")
	}

	write(t, b, "loose.yaml", "A: 1\n")
	wantCounts(t, g, b, Uncommitted, 1, 2)
}

func TestStatusNeverFetches(t *testing.T) {
	g := newGit(t)
	bare := published(t, g)
	a, b := clone(t, g, bare), clone(t, g, bare)
	commit(t, g, a, "one.yaml", "A: 1\n")
	if _, err := g.Push(a); err != nil {
		t.Fatal(err)
	}
	wantCounts(t, g, b, InSync, 0, 0)
	if err := g.Fetch(b, time.Minute); err != nil {
		t.Fatal(err)
	}
	wantCounts(t, g, b, Behind, 0, 1)
}

func TestEmptyRemote(t *testing.T) {
	g := newGit(t)
	bare := gittest.Bare(t)
	dir := newStore(t, g)
	commit(t, g, dir, "one.yaml", "A: 1\n")
	if err := g.SetRemote(dir, bare); err != nil {
		t.Fatal(err)
	}
	if err := g.Fetch(dir, time.Minute); err != nil {
		t.Fatalf("Fetch from an empty remote: %v", err)
	}
	wantCounts(t, g, dir, Ahead, 2, 0)
	if n, err := g.Push(dir); err != nil || n != 2 {
		t.Fatalf("Push: %d, %v", n, err)
	}
	wantCounts(t, g, dir, InSync, 0, 0)
	if got := raw(t, g, bare, "rev-list", "--count", "main"); got != "2" {
		t.Fatalf("the remote holds %s commits, want 2", got)
	}
	if got := raw(t, g, dir, "rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
		t.Fatalf("upstream %q", got)
	}
}

func TestBranchNotMain(t *testing.T) {
	g := newGit(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	raw(t, g, "", "init", "--bare", "--initial-branch=master", bare)
	seed := filepath.Join(t.TempDir(), "seed")
	raw(t, g, "", "init", "--initial-branch=master", seed)
	commit(t, g, seed, "common.yaml", "A: 1\n")
	if err := g.SetRemote(seed, bare); err != nil {
		t.Fatal(err)
	}
	if n, err := g.Push(seed); err != nil || n != 1 {
		t.Fatalf("Push: %d, %v", n, err)
	}

	dir := clone(t, g, bare)
	if st := status(t, g, dir); st.Branch != "master" || st.State() != InSync {
		t.Fatalf("unexpected status: %+v", st)
	}
	commit(t, g, seed, "one.yaml", "A: 1\n")
	if _, err := g.Push(seed); err != nil {
		t.Fatal(err)
	}
	if err := g.Fetch(dir, time.Minute); err != nil {
		t.Fatal(err)
	}
	wantCounts(t, g, dir, Behind, 0, 1)
	commit(t, g, dir, "two.yaml", "A: 1\n")
	wantCounts(t, g, dir, Diverged, 1, 1)
}

func TestSetRemoteReplaces(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	for _, url := range []string{"/tmp/first.git", "/tmp/second.git"} {
		if err := g.SetRemote(dir, url); err != nil {
			t.Fatalf("SetRemote %s: %v", url, err)
		}
		if got, err := g.Remote(dir); err != nil || got != url {
			t.Fatalf("Remote: %q, %v", got, err)
		}
	}
}

func TestFetchUnreachable(t *testing.T) {
	g := newGit(t)
	gone := published(t, g)
	dir := clone(t, g, gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := g.Fetch(dir, time.Minute); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Fetch from a remote that is gone: %v", err)
	}
	wantCounts(t, g, dir, InSync, 0, 0)

	// The remote's side of the fetch hangs.
	slow := clone(t, g, published(t, g))
	raw(t, g, slow, "config", "remote.origin.uploadpack", "sleep 20;")
	start := time.Now()
	err := g.Fetch(slow, 300*time.Millisecond)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Fetch that timed out: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("Fetch waited %s for a 300ms timeout", took)
	}
}

func TestFetchTimeout(t *testing.T) {
	var fetch proc.Command
	g := Git{Run: func(c proc.Command) ([]byte, []byte, error) {
		if slices.Contains(c.Args, "fetch") {
			fetch = c
			return nil, nil, proc.ErrTimeout
		}
		return []byte("/somewhere/remote.git\n"), nil, nil
	}}
	if err := g.Fetch("/store", 3*time.Second); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Fetch: %v", err)
	}
	if fetch.Timeout != 3*time.Second {
		t.Fatalf("the fetch ran with timeout %s", fetch.Timeout)
	}
}

func TestFetchNoRemote(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	real := g.Run
	g.Run = func(c proc.Command) ([]byte, []byte, error) {
		if slices.Contains(c.Args, "fetch") {
			t.Errorf("ran git %v", c.Args)
		}
		return real(c)
	}
	if err := g.Fetch(dir, time.Minute); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestNotInstalled(t *testing.T) {
	g := Git{Run: func(proc.Command) ([]byte, []byte, error) {
		return nil, nil, &exec.Error{Name: "git", Err: exec.ErrNotFound}
	}}
	check := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrNotInstalled) {
			t.Errorf("%s: %v", what, err)
		}
	}
	check("Init", g.Init("/store"))
	check("Clone", g.Clone("/remote", "/store"))
	_, err := g.Remote("/store")
	check("Remote", err)
	check("SetRemote", g.SetRemote("/store", "/remote"))
	_, err = g.Commit("/store", "m", author, "a")
	check("Commit", err)
	check("Fetch", g.Fetch("/store", time.Second))
	_, err = g.Status("/store")
	check("Status", err)
	_, err = g.Push("/store")
	check("Push", err)
	if g.IsRepo("/store") {
		t.Error("IsRepo without git")
	}
}

func TestEnvironment(t *testing.T) {
	var got proc.Command
	g := Git{
		Environ: []string{"PATH=/bin", "LC_ALL=fr_FR.UTF-8", "GIT_TERMINAL_PROMPT=1", "KEEP=me"},
		Run: func(c proc.Command) ([]byte, []byte, error) {
			got = c
			return nil, nil, nil
		},
	}
	if _, err := g.Remote("/store"); err != nil {
		t.Fatal(err)
	}
	if got.Name != "git" || got.Dir != "/store" || got.Interactive {
		t.Fatalf("unexpected command: %+v", got)
	}
	for _, want := range []string{"PATH=/bin", "KEEP=me", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"} {
		if !slices.Contains(got.Env, want) {
			t.Errorf("the environment lacks %s: %v", want, got.Env)
		}
	}
	for _, not := range []string{"LC_ALL=fr_FR.UTF-8", "GIT_TERMINAL_PROMPT=1"} {
		if slices.Contains(got.Env, not) {
			t.Errorf("the environment still holds %s", not)
		}
	}
}

func TestErrorsHoldNoFileContent(t *testing.T) {
	const marker = "s3cr3t-marker-value"
	g := newGit(t)
	dir := newStore(t, g)
	hook := "#!/bin/sh\necho refused by the hook\ncat common.yaml\ncat common.yaml >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "common.yaml", "A: "+marker+"\n")
	done, err := g.Commit(dir, "set A", author, "common.yaml")
	if err == nil || done {
		t.Fatalf("Commit: done=%v err=%v", done, err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("the error holds the file's content: %v", err)
	}
	if !strings.Contains(err.Error(), "refused by the hook") {
		t.Fatalf("the error does not say why: %v", err)
	}

	// A push that is refused says so, not where it went.
	bare := published(t, g)
	a, b := clone(t, g, bare), clone(t, g, bare)
	commit(t, g, a, "one.yaml", "A: 1\n")
	if _, err := g.Push(a); err != nil {
		t.Fatal(err)
	}
	commit(t, g, b, "two.yaml", "A: "+marker+"\n")
	_, err = g.Push(b)
	if err == nil || strings.Contains(err.Error(), marker) || !strings.Contains(err.Error(), "failed to push") {
		t.Fatalf("Push: %v", err)
	}
}
