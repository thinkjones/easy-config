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
	if err := g.DeleteRemoteBranch(a, "ezc/join-laptop", time.Minute); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := g.Fetch(a, time.Minute); err != nil {
		t.Fatal(err)
	}
	if names, _ := g.RemoteBranches(a, "ezc/"); len(names) != 0 {
		t.Fatalf("branches after: %v", names)
	}
	if err := g.DeleteRemoteBranch(a, "ezc/join-laptop", time.Minute); err != nil {
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
	if err := g.DeleteRemoteBranch(dir, "ezc/join-x", time.Second); err != nil {
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
	_ = g.DeleteRemoteBranch(dir, "b", time.Second)
	for _, args := range calls {
		for _, a := range args {
			if strings.Contains(a, "credential.helper") {
				t.Fatalf("a helper without TokenVar: %v", args)
			}
		}
	}
}
