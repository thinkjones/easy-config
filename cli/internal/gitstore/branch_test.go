package gitstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

const requestPath = ".ezc/requests/laptop.yaml"

func writeRequest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ezc", "requests"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, requestPath, body)
}

// twoClones is a published remote with a clone that has an identity of its own.
func twoClones(t *testing.T) (g Git, bare, a, b string) {
	t.Helper()
	g = Git{Run: gittest.WithIdentity(t)}
	bare = published(t, g)
	return g, bare, clone(t, g, bare), clone(t, g, bare)
}

func TestPushBranchThenSeeItFromAnotherClone(t *testing.T) {
	g, _, a, b := twoClones(t)
	headBefore := raw(t, g, a, "rev-parse", "HEAD")
	writeRequest(t, a, "key: one\n")
	if err := g.PushBranch(a, "ezc/join-laptop", "request", author, requestPath); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}
	if st := status(t, g, a); st.Branch != "main" || st.Dirty || st.Ahead != 0 {
		t.Fatalf("A is not as before: %+v", st)
	}
	if raw(t, g, a, "rev-parse", "HEAD") != headBefore {
		t.Fatal("A's main moved")
	}
	if _, err := os.Stat(filepath.Join(a, requestPath)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the request is still in A's work tree: %v", err)
	}
	if got := raw(t, g, a, "branch", "--list", "ezc/join-laptop"); got != "" {
		t.Fatalf("local branch left: %q", got)
	}

	if err := g.Fetch(b, time.Minute); err != nil {
		t.Fatal(err)
	}
	names, err := g.RemoteBranches(b, "ezc/join-")
	if err != nil || !slices.Equal(names, []string{"ezc/join-laptop"}) {
		t.Fatalf("RemoteBranches = %v, %v", names, err)
	}
	body, err := g.Show(b, "ezc/join-laptop", requestPath)
	if err != nil || string(body) != "key: one\n" {
		t.Fatalf("Show = %q, %v", body, err)
	}
	who, err := g.LastAuthor(b, "ezc/join-laptop", requestPath)
	if err != nil || who != gittest.Name+" <"+gittest.Email+">" {
		t.Fatalf("LastAuthor = %q, %v", who, err)
	}
	if _, err := g.Show(b, "ezc/join-laptop", "nothing.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Show of a missing path: %v", err)
	}
}

func TestPushBranchTwice(t *testing.T) {
	g, bare, a, b := twoClones(t)
	writeRequest(t, a, "key: one\n")
	if err := g.PushBranch(a, "ezc/join-laptop", "request", author, requestPath); err != nil {
		t.Fatal(err)
	}
	tip := raw(t, g, bare, "rev-parse", "ezc/join-laptop")

	writeRequest(t, a, "key: one\n")
	if err := g.PushBranch(a, "ezc/join-laptop", "request", author, requestPath); err != nil {
		t.Fatalf("same bytes again: %v", err)
	}
	writeRequest(t, b, "key: two\n")
	err := g.PushBranch(b, "ezc/join-laptop", "request", author, requestPath)
	if !errors.Is(err, ErrBranchTaken) {
		t.Fatalf("other bytes: %v, want ErrBranchTaken", err)
	}
	if raw(t, g, bare, "rev-parse", "ezc/join-laptop") != tip {
		t.Fatal("the remote branch moved")
	}
	if st := status(t, g, b); st.Branch != "main" {
		t.Fatalf("B is on %q", st.Branch)
	}
}

func TestDeleteRemoteBranch(t *testing.T) {
	g, _, a, _ := twoClones(t)
	writeRequest(t, a, "key: one\n")
	if err := g.PushBranch(a, "ezc/join-laptop", "request", author, requestPath); err != nil {
		t.Fatal(err)
	}
	if err := g.Fetch(a, time.Minute); err != nil {
		t.Fatal(err)
	}
	if names, _ := g.RemoteBranches(a, "ezc/"); len(names) != 1 {
		t.Fatalf("branches before: %v", names)
	}
	if err := g.DeleteRemoteBranch(a, "ezc/join-laptop", "", time.Minute); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := g.Fetch(a, time.Minute); err != nil {
		t.Fatal(err)
	}
	if names, _ := g.RemoteBranches(a, "ezc/"); len(names) != 0 {
		t.Fatalf("branches after: %v", names)
	}
	if err := g.DeleteRemoteBranch(a, "ezc/join-laptop", "", time.Minute); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestFetchPrunesAndSeesEveryBranchOfAShallowClone(t *testing.T) {
	g, _, a, _ := twoClones(t)
	shallow := filepath.Join(t.TempDir(), "shallow")
	bare, err := g.Remote(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.CloneShallow("file://"+bare, shallow); err != nil {
		t.Fatalf("CloneShallow: %v", err)
	}
	if raw(t, g, shallow, "rev-parse", "--is-shallow-repository") != "true" {
		t.Fatal("not a shallow clone")
	}
	writeRequest(t, a, "key: one\n")
	if err := g.PushBranch(a, "ezc/join-laptop", "request", author, requestPath); err != nil {
		t.Fatal(err)
	}
	if err := g.Fetch(shallow, time.Minute); err != nil {
		t.Fatal(err)
	}
	if names, _ := g.RemoteBranches(shallow, "ezc/join-"); !slices.Equal(names, []string{"ezc/join-laptop"}) {
		t.Fatalf("the shallow clone sees %v", names)
	}
}

func TestRestore(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	write(t, dir, "common.yaml", "A: 2\n")
	write(t, dir, "new.yaml", "B: 1\n")
	if err := g.Restore(dir, "common.yaml", "new.yaml"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if changed, err := g.Changed(dir, "common.yaml", "new.yaml"); err != nil || len(changed) != 0 {
		t.Fatalf("Changed = %v, %v", changed, err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "common.yaml")); string(body) != "A: 1\n" {
		t.Fatalf("common.yaml = %q", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("new.yaml remains: %v", err)
	}
}

func TestPushBranchNoRemote(t *testing.T) {
	g := newGit(t)
	dir := newStore(t, g)
	write(t, dir, "req.yaml", "k: v\n")
	if err := g.PushBranch(dir, "ezc/join-x", "request", author, "req.yaml"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v, want ErrNoRemote", err)
	}
	if got := raw(t, g, dir, "branch", "--list"); strings.Contains(got, "ezc/") {
		t.Fatalf("a branch was left: %q", got)
	}
}

func TestPushBranchFailureCleansUp(t *testing.T) {
	g, bare, a, _ := twoClones(t)
	if err := g.SetRemote(a, filepath.Join(t.TempDir(), "gone.git")); err != nil {
		t.Fatal(err)
	}
	_ = bare
	writeRequest(t, a, "key: one\n")
	if err := g.PushBranch(a, "ezc/join-laptop", "request", author, requestPath); err == nil {
		t.Fatal("pushing to a missing remote succeeded")
	}
	if st := status(t, g, a); st.Branch != "main" || st.Dirty {
		t.Fatalf("status after failure: %+v", st)
	}
	if got := raw(t, g, a, "branch", "--list", "ezc/*"); got != "" {
		t.Fatalf("a branch was left: %q", got)
	}
}

// recording is a runner that records the arguments and answers with an empty success.
func recording(calls *[][]string) proc.Runner {
	return func(c proc.Command) ([]byte, []byte, error) {
		*calls = append(*calls, c.Args)
		if slices.Contains(c.Args, "config") {
			return []byte("https://example.invalid/o/r.git\n"), nil, nil
		}
		return nil, nil, nil
	}
}

func TestTokenIsNamedNeverGiven(t *testing.T) {
	var calls [][]string
	g := Git{Run: recording(&calls), Environ: []string{"EZC_STORE_TOKEN=tok-123"}, TokenVar: "EZC_STORE_TOKEN"}
	dir := t.TempDir()
	if err := g.Fetch(dir, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := g.CloneShallow("https://example.invalid/o/r.git", filepath.Join(dir, "c")); err != nil {
		t.Fatal(err)
	}
	if err := g.Clone("https://example.invalid/o/r.git", filepath.Join(dir, "c2")); err != nil {
		t.Fatal(err)
	}
	// The fake answers "status" with nothing, so Push stops before pushing; ask for the push through the branch commands.
	if err := g.DeleteRemoteBranch(dir, "ezc/join-x", "", time.Second); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, args := range calls {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "tok-123") {
			t.Fatalf("the token is in arguments: %v", args)
		}
		if slices.Contains(args, "credential.helper=!f() { echo username=x-access-token; echo \"password=$EZC_STORE_TOKEN\"; }; f") {
			for _, verb := range []string{"fetch", "clone", "ls-remote"} {
				if slices.Contains(args, verb) {
					seen[verb] = true
				}
			}
			if !slices.Contains(args, "credential.helper=") {
				t.Fatalf("inherited helpers are not dropped: %v", args)
			}
		}
	}
	for _, verb := range []string{"fetch", "clone", "ls-remote"} {
		if !seen[verb] {
			t.Errorf("no %s carried the helper; calls: %v", verb, calls)
		}
	}
}

func TestTokenOnPush(t *testing.T) {
	g, _, a, _ := twoClones(t)
	var calls [][]string
	inner := g.Run
	g.Run = func(c proc.Command) ([]byte, []byte, error) {
		calls = append(calls, c.Args)
		return inner(c)
	}
	g.Environ = []string{"EZC_STORE_TOKEN=tok-123"}
	g.TokenVar = "EZC_STORE_TOKEN"
	commit(t, g, a, "more.yaml", "X: 1\n")
	if _, err := g.Push(a); err != nil {
		t.Fatalf("Push: %v", err)
	}
	pushed := false
	for _, args := range calls {
		if strings.Contains(strings.Join(args, " "), "tok-123") {
			t.Fatalf("the token is in arguments: %v", args)
		}
		if slices.Contains(args, "push") {
			pushed = true
			if !slices.Contains(args, "credential.helper=!f() { echo username=x-access-token; echo \"password=$EZC_STORE_TOKEN\"; }; f") {
				t.Fatalf("push has no helper: %v", args)
			}
		}
	}
	if !pushed {
		t.Fatal("no push ran")
	}
}

func TestNoTokenVarNoHelper(t *testing.T) {
	var calls [][]string
	g := Git{Run: recording(&calls)}
	dir := t.TempDir()
	_ = g.Fetch(dir, time.Second)
	_ = g.CloneShallow("https://example.invalid/o/r.git", filepath.Join(dir, "c"))
	_ = g.DeleteRemoteBranch(dir, "b", "", time.Second)
	for _, args := range calls {
		for _, a := range args {
			if strings.Contains(a, "credential.helper") {
				t.Fatalf("a helper without TokenVar: %v", args)
			}
		}
	}
}

// hostileTags pushes, from a clone of bare, a commit that changes the request and adds a file, with tags on it that a
// short name finds before the branch of the same name: origin/<branch>, <branch>, origin/main and main.
func hostileTags(t *testing.T, g Git, bare, branch string) {
	t.Helper()
	c := clone(t, g, bare)
	raw(t, g, c, "checkout", "--quiet", "-b", "hostile")
	writeRequest(t, c, "key: hostile\n")
	write(t, c, "hostile.txt", "theirs\n")
	raw(t, g, c, "add", "--all")
	raw(t, g, c, "-c", "user.name=Mallory", "commit", "--quiet", "-m", "hostile")
	for _, tag := range []string{"origin/" + branch, branch, "origin/main", "main"} {
		raw(t, g, c, "tag", tag)
		raw(t, g, c, "push", "--quiet", "origin", "refs/tags/"+tag)
	}
}

// A tag is found before a branch of the same short name, and anybody who can push can make one. Nothing gitstore
// reads, compares against, rebases onto, pushes or deletes may be chosen by a tag.
func TestTagsNeverStandInForABranch(t *testing.T) {
	const branch = "ezc/join-laptop"
	for _, held := range []bool{false, true} {
		g, bare, a, b := twoClones(t)
		writeRequest(t, a, "key: one\n")
		if err := g.PushBranch(a, branch, "request", author, requestPath); err != nil {
			t.Fatalf("PushBranch: %v", err)
		}
		hostileTags(t, g, bare, branch)
		if held {
			// A store that took the tags in some other way, or before ezc stopped fetching them.
			raw(t, g, b, "fetch", "--quiet", "--tags", "origin")
		}
		if err := g.Fetch(b, time.Minute); err != nil {
			t.Fatal(err)
		}
		if got := raw(t, g, b, "tag", "--list"); (got != "") != held {
			t.Fatalf("held %v: b's tags %q", held, got)
		}

		body, err := g.Show(b, branch, requestPath)
		if err != nil || string(body) != "key: one\n" {
			t.Fatalf("held %v: Show = %q, %v", held, body, err)
		}
		if who, err := g.LastAuthor(b, branch, requestPath); err != nil || strings.Contains(who, "Mallory") {
			t.Fatalf("held %v: LastAuthor = %q, %v", held, who, err)
		}
		commit, err := g.RemoteCommit(b, branch)
		if err != nil || commit != raw(t, g, bare, "rev-parse", "refs/heads/"+branch) {
			t.Fatalf("held %v: RemoteCommit = %q, %v", held, commit, err)
		}
		if names, err := g.RemoteBranches(b, "ezc/join-"); err != nil || !slices.Equal(names, []string{branch}) {
			t.Fatalf("held %v: RemoteBranches = %v, %v", held, names, err)
		}

		// Status and Pull count against, and bring in, the remote's main: one commit from a.
		wantCounts(t, g, b, InSync, 0, 0)
		commit1 := func(dir, name string) {
			write(t, dir, name, "x\n")
			if done, err := g.Commit(dir, "change "+name, author, name); err != nil || !done {
				t.Fatalf("commit: %v, %v", done, err)
			}
		}
		commit1(a, "theirs.txt")
		if n, err := g.Push(a); err != nil || n != 1 {
			t.Fatalf("held %v: Push = %d, %v", held, n, err)
		}
		commit1(b, "mine.txt")
		if err := g.Fetch(b, time.Minute); err != nil {
			t.Fatal(err)
		}
		wantCounts(t, g, b, Diverged, 1, 1)
		res, err := g.Pull(b, time.Minute, func(string) bool { return false })
		if err != nil || res.Commits != 1 {
			t.Fatalf("held %v: Pull = %+v, %v", held, res, err)
		}
		if _, err := os.Stat(filepath.Join(b, "hostile.txt")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("held %v: the tag's commit was pulled: %v", held, err)
		}
		if n, err := g.Push(b); err != nil || n != 1 {
			t.Fatalf("held %v: Push = %d, %v", held, n, err)
		}
		if raw(t, g, bare, "rev-parse", "refs/heads/main") != raw(t, g, b, "rev-parse", "HEAD") {
			t.Fatalf("held %v: the remote's main is not b's", held)
		}

		// A second request is pushed from the store that holds the tags, and the first is deleted.
		if err := os.MkdirAll(filepath.Join(b, ".ezc", "requests"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, b, ".ezc/requests/desk.yaml", "key: two\n")
		if err := g.PushBranch(b, "ezc/join-desk", "request", author, ".ezc/requests/desk.yaml"); err != nil {
			t.Fatalf("held %v: PushBranch: %v", held, err)
		}
		if st := status(t, g, b); st.Branch != "main" || st.Dirty || st.Ahead != 0 {
			t.Fatalf("held %v: b is not as before: %+v", held, st)
		}
		if err := g.DeleteRemoteBranch(b, branch, commit, time.Minute); err != nil {
			t.Fatalf("held %v: DeleteRemoteBranch: %v", held, err)
		}
		if got := raw(t, g, bare, "for-each-ref", "--format=%(refname)", "refs/heads/ezc/"); got != "refs/heads/ezc/join-desk" {
			t.Fatalf("held %v: the remote's request branches: %q", held, got)
		}
		if got := raw(t, g, bare, "for-each-ref", "--format=%(refname)", "refs/tags/"); strings.Count(got, "\n") != 3 {
			t.Fatalf("held %v: the remote's tags: %q", held, got)
		}
	}
}

func TestCloneTakesNoTags(t *testing.T) {
	g, bare, a, _ := twoClones(t)
	raw(t, g, a, "tag", "origin/main")
	raw(t, g, a, "push", "--quiet", "origin", "refs/tags/origin/main")
	for _, cloneIt := range []func(source, dir string) error{g.Clone, g.CloneShallow} {
		dir := filepath.Join(t.TempDir(), "c")
		if err := cloneIt(bare, dir); err != nil {
			t.Fatal(err)
		}
		if err := g.Fetch(dir, time.Minute); err != nil {
			t.Fatal(err)
		}
		if got := raw(t, g, dir, "tag", "--list"); got != "" {
			t.Fatalf("tags %q", got)
		}
	}
}

// A branch is deleted only while it is at the commit it was read at.
func TestDeleteRemoteBranchHoldsToTheCommitRead(t *testing.T) {
	g, bare, a, b := twoClones(t)
	writeRequest(t, a, "key: one\n")
	if err := g.PushBranch(a, "ezc/join-laptop", "request", author, requestPath); err != nil {
		t.Fatal(err)
	}
	if err := g.Fetch(b, time.Minute); err != nil {
		t.Fatal(err)
	}
	read, err := g.RemoteCommit(b, "ezc/join-laptop")
	if err != nil {
		t.Fatal(err)
	}
	body, err := g.ShowAt(b, read, requestPath)
	if err != nil || string(body) != "key: one\n" {
		t.Fatalf("ShowAt = %q, %v", body, err)
	}
	if who, err := g.LastAuthorAt(b, read, requestPath); err != nil || who == "" {
		t.Fatalf("LastAuthorAt = %q, %v", who, err)
	}

	// Somebody else moves the branch.
	c := clone(t, g, bare)
	raw(t, g, c, "checkout", "--quiet", "-b", "ezc/join-laptop", "origin/ezc/join-laptop")
	writeRequest(t, c, "key: two\n")
	raw(t, g, c, "commit", "--quiet", "-am", "another")
	raw(t, g, c, "push", "--quiet", "origin", "refs/heads/ezc/join-laptop")

	if err := g.DeleteRemoteBranch(b, "ezc/join-laptop", read, time.Minute); !errors.Is(err, ErrBranchMoved) {
		t.Fatalf("got %v, want ErrBranchMoved", err)
	}
	moved := raw(t, g, bare, "rev-parse", "refs/heads/ezc/join-laptop")
	if moved == read {
		t.Fatal("the branch did not move")
	}
	// What was read stays readable, whatever is fetched afterwards.
	if err := g.Fetch(b, time.Minute); err != nil {
		t.Fatal(err)
	}
	if body, err := g.ShowAt(b, read, requestPath); err != nil || string(body) != "key: one\n" {
		t.Fatalf("ShowAt after the fetch = %q, %v", body, err)
	}
	if err := g.DeleteRemoteBranch(b, "ezc/join-laptop", moved, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := raw(t, g, bare, "for-each-ref", "refs/heads/ezc/"); got != "" {
		t.Fatalf("the branch is left: %q", got)
	}
}
