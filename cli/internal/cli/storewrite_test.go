package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

const (
	storeWriteBehind   = "the store is 2 commits behind origin. Run `ezc store pull` first: a change made now could not be merged."
	storeWriteDiverged = "the store has diverged from origin. Run `ezc store pull` first: a change made now could not be merged."
)

// storeWriteFix is a gitFix with what these tests ask of the store's history.
type storeWriteFix struct{ gitFix }

// storeWriteStore is a store that is a git repo with a remote and a second clone, holding mToken, a policy and NAME.
func storeWriteStore(t *testing.T, f *fake) storeWriteFix {
	t.Helper()
	x := storeWriteFix{localStore(t, f)}
	x.commit(x.store, "manifest.yaml", mToken)
	x.commit(x.store, backend.FakePolicy, "")
	x.bare = gittest.Bare(t)
	if err := x.g.SetRemote(x.store, x.bare); err != nil {
		t.Fatal(err)
	}
	return x.published()
}

// published pushes the store and makes the other clone a new one, level with it.
func (x storeWriteFix) published() storeWriteFix {
	x.t.Helper()
	x.push(x.store)
	x.other = filepath.Join(x.t.TempDir(), "other")
	if err := x.g.Clone(x.bare, x.other); err != nil {
		x.t.Fatal(err)
	}
	return x
}

// git runs git in the store and returns what it printed.
func (x storeWriteFix) git(args ...string) string {
	x.t.Helper()
	stdout, stderr, err := x.g.Run(proc.Command{Dir: x.store, Name: "git", Args: args})
	if err != nil {
		x.t.Fatalf("git %v: %v\n%s", args, err, stderr)
	}
	return strings.TrimRight(string(stdout), "\n")
}

func (x storeWriteFix) commits() string { return x.git("rev-list", "--count", "HEAD") }

// subjects are the messages of the commits made since the store was pushed, oldest first.
func (x storeWriteFix) subjects() []string {
	out := x.git("log", "--reverse", "--format=%s", "origin/main..HEAD")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// wantCommit fails unless exactly one commit was made since the push, with this message and these files of .ezc, and nothing is left uncommitted.
func (x storeWriteFix) wantCommit(message string, files ...string) {
	x.t.Helper()
	if got := x.subjects(); !slices.Equal(got, []string{message}) {
		x.t.Fatalf("commits %q, want %q", got, message)
	}
	var want []string
	for _, file := range files {
		want = append(want, ezcDir+"/"+file)
	}
	slices.Sort(want)
	if got := x.git("show", "--name-only", "--format=", "HEAD"); got != strings.Join(want, "\n") {
		x.t.Fatalf("the commit holds %q, want %q", got, want)
	}
	x.wantClean()
}

func (x storeWriteFix) wantClean() {
	x.t.Helper()
	if got := x.git("status", "--porcelain", "--untracked-files=all"); got != "" {
		x.t.Fatalf("left uncommitted:\n%s", got)
	}
}

func (x storeWriteFix) file(name string) string { return filepath.Join(x.store, ezcDir, name) }

// secret sets name in an encrypted file of the store and commits the file.
func (x storeWriteFix) secret(f *fake, file, name, value string) {
	x.t.Helper()
	secretIn(x.t, f, "store", file, name, value)
	x.commit(x.store, file, readFile(x.t, x.file(file)))
}

// storeWriteExitsOne fails when err is one ezc exits 2 for: any other error exits 1.
func storeWriteExitsOne(t *testing.T, err error) {
	t.Helper()
	var usage *UsageError
	if errors.As(err, &usage) {
		t.Fatalf("a usage error: %v", err)
	}
}

// storeWriteCommitsRun are the git commands the fake ran that commit.
func storeWriteCommitsRun(f *fake) []proc.Command {
	var out []proc.Command
	for _, c := range f.gits {
		if slices.Contains(c.Args, "commit") {
			out = append(out, c)
		}
	}
	return out
}

func TestSetGlobalCommits(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	if got := stderrOf(t, f, "set", "-g", "NAME", "v2"); got != "" {
		t.Fatalf("stderr %q", got)
	}
	if f.out.String() != "set NAME in store/common.yaml\n" {
		t.Fatalf("stdout %q", f.out.String())
	}
	x.wantCommit("set NAME (common.yaml)", "common.yaml")
	if got := x.git("log", "-1", "--format=%an"); got != "ezc" {
		t.Fatalf("author %q", got)
	}

	// A secret: the commit names it and its file, and the history never holds the value.
	const value = "s3cr3t-marker-value"
	f = newFake(t)
	x = storeWriteStore(t, f)
	f.stdin = value + "\n"
	stderrOf(t, f, "set", "-g", "TOKEN")
	x.wantCommit("set TOKEN (common.enc.yaml)", "common.enc.yaml")
	if got := x.git("show", "--stat", "HEAD"); !strings.Contains(got, "set TOKEN (common.enc.yaml)") || !strings.Contains(got, ".ezc/common.enc.yaml") {
		t.Fatalf("git show --stat:\n%s", got)
	}
	if got := x.git("log", "-p"); strings.Contains(got, value) || !strings.Contains(got, "+TOKEN: ") {
		t.Fatalf("git log -p holds the value, or not the name:\n%s", got)
	}
}

func TestSetGlobalSameValueNoCommit(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	stderrOf(t, f, "set", "-g", "NAME", "n")
	if got := x.subjects(); got != nil {
		t.Fatalf("commits %q", got)
	}
	x.wantClean()
}

func TestSetGlobalNewNameOneCommit(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	stderrOf(t, f, "set", "-g", "NEW", "v", "--type", "str", "--desc", "d")
	x.wantCommit("set NEW (manifest.yaml, common.yaml)", "manifest.yaml", "common.yaml")

	// A name whose value was already in the file: only the declaration changed.
	f = newFake(t)
	x = storeWriteStore(t, f)
	x.commit(x.store, "env.prod.yaml", "LOOSE: v\n")
	x.push(x.store)
	stderrOf(t, f, "set", "-g", "LOOSE", "v", "--env", "prod", "--type", "str", "--desc", "d")
	x.wantCommit("set LOOSE (manifest.yaml)", "manifest.yaml")
}

func TestSetGlobalAuthorFromConfig(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	if err := config.Save(filepath.Join(f.env["EZC_HOME"], "config.toml"), config.Config{User: "sam"}); err != nil {
		t.Fatal(err)
	}
	stderrOf(t, f, "set", "-g", "NAME", "v2")
	if got := x.git("log", "-1", "--format=%an|%cn"); got != "sam|sam" {
		t.Fatalf("author|committer %q", got)
	}
}

func TestUnsetGlobalCommits(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	stderrOf(t, f, "unset", "-g", "NAME")
	x.wantCommit("unset NAME (common.yaml)", "common.yaml")

	// In both files of the base: one commit names both.
	f = newFake(t)
	x = storeWriteStore(t, f)
	x.secret(f, "common.enc.yaml", "TOKEN", "t1")
	x.secret(f, "common.enc.yaml", "OTHER", "o1")
	x.commit(x.store, "common.yaml", "NAME: n\nTOKEN: leaked\n")
	x.push(x.store)
	stderrOf(t, f, "unset", "-g", "TOKEN")
	x.wantCommit("unset TOKEN (common.yaml, common.enc.yaml)", "common.yaml", "common.enc.yaml")

	// Only the encrypted file held it, and it goes with its last name.
	f = newFake(t)
	x = storeWriteStore(t, f)
	x.secret(f, "env.prod.enc.yaml", "TOKEN", "t1")
	x.push(x.store)
	stderrOf(t, f, "unset", "-g", "TOKEN", "--env", "prod")
	x.wantCommit("unset TOKEN (env.prod.enc.yaml)", "env.prod.enc.yaml")
	missing(t, x.file("env.prod.enc.yaml"))
}

func TestUnsetUndeclareCommits(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	stderrOf(t, f, "unset", "-g", "NAME", "--undeclare")
	x.wantCommit("unset NAME (manifest.yaml, common.yaml)", "manifest.yaml", "common.yaml")

	// Declared with no value anywhere: the declaration alone.
	f = newFake(t)
	x = storeWriteStore(t, f)
	stderrOf(t, f, "unset", "-g", "SIZE", "--undeclare")
	x.wantCommit("unset SIZE (manifest.yaml)", "manifest.yaml")
}

// The plaintext copy is gone and said to be before the encrypted one fails: that much is committed, and the failure reported.
func TestUnsetGlobalPartFailureCommitsWhatWasRemoved(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	x.secret(f, "common.enc.yaml", "TOKEN", "t1")
	x.secret(f, "common.enc.yaml", "OTHER", "o1")
	x.commit(x.store, "common.yaml", "NAME: n\nTOKEN: leaked\n")
	x.push(x.store)
	f.backend.Locked = true
	if err := f.run("unset", "-g", "TOKEN"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	if f.out.String() != "unset TOKEN from store/common.yaml\n" {
		t.Fatalf("stdout %q", f.out.String())
	}
	x.wantCommit("unset TOKEN (common.yaml)", "common.yaml")
}

func TestUnsetGlobalNotSetCommitsNothing(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	if err := f.run("unset", "-g", "SIZE"); err == nil || !strings.Contains(err.Error(), "SIZE is not set in") {
		t.Fatalf("got %v", err)
	}
	if got := x.subjects(); got != nil {
		t.Fatalf("commits %q", got)
	}
	x.wantClean()
}

// storeWriteEditor puts the fake on a terminal with an editor that writes content, or nothing when content is "".
func storeWriteEditor(f *fake, content string) {
	f.tty = true
	f.env["EDITOR"] = "nano"
	f.paths = map[string]string{"nano": "/bin/nano"}
	f.execFn = func(c proc.Command) ([]byte, []byte, error) {
		if content == "" {
			return nil, nil, nil
		}
		return nil, nil, os.WriteFile(c.Args[len(c.Args)-1], []byte(content), 0o644)
	}
}

func TestEditGlobalCommits(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	storeWriteEditor(f, "SIZE: 4\n")
	if got := stderrOf(t, f, "edit", "-g", "--env", "prod"); got != "" {
		t.Fatalf("stderr %q", got)
	}
	if f.out.String() != "edited store/env.prod.yaml\n" {
		t.Fatalf("stdout %q", f.out.String())
	}
	x.wantCommit("edit env.prod.yaml", "env.prod.yaml")

	// The encrypted file, through the backend.
	f = newFake(t)
	x = storeWriteStore(t, f)
	x.secret(f, "common.enc.yaml", "TOKEN", "t1")
	x.push(x.store)
	storeWriteEditor(f, "")
	f.backend.EditFunc = func(p []backend.Pair) []backend.Pair { return append(p, backend.Pair{Name: "B", Value: "b1-marker"}) }
	stderrOf(t, f, "edit", "-g", "--secrets")
	x.wantCommit("edit common.enc.yaml", "common.enc.yaml")
	if got := x.git("log", "-p"); strings.Contains(got, "b1-marker") {
		t.Fatalf("git log -p holds the value:\n%s", got)
	}
}

func TestEditGlobalNoChangeNoCommit(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	x.secret(f, "common.enc.yaml", "TOKEN", "t1")
	x.push(x.store)
	storeWriteEditor(f, "")
	for _, args := range [][]string{{"edit", "-g"}, {"edit", "-g", "--secrets"}, {"edit", "-g", "--env", "new"}} {
		stderrOf(t, f, args...)
		if got := x.subjects(); got != nil || len(storeWriteCommitsRun(f)) != 0 {
			t.Fatalf("%v: commits %q, git commit ran %d times", args, got, len(storeWriteCommitsRun(f)))
		}
		x.wantClean()
	}
}

// A file saved but not valid is left for `ezc edit` to fix, uncommitted; the edit that fixes it is the commit.
func TestEditGlobalInvalidResultIsNotCommitted(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	storeWriteEditor(f, "NAME: [\n")
	wantExit(t, f.run("edit", "-g"), 1)
	if got := x.subjects(); got != nil {
		t.Fatalf("commits %q", got)
	}
	storeWriteEditor(f, "NAME: fixed\n")
	f.errOut.Reset()
	if err := f.run("edit", "-g"); err != nil {
		t.Fatal(err)
	}
	x.wantCommit("edit common.yaml", "common.yaml")
}

func TestSetFailureCommitsNothing(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	f.backend.Locked = true
	f.stdin = "v\n"
	if err := f.run("set", "-g", "KEY", "--type", "secret", "--desc", "d"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("got %v", err)
	}
	if got := x.subjects(); got != nil || len(storeWriteCommitsRun(f)) != 0 {
		t.Fatalf("commits %q, git commit ran %d times", got, len(storeWriteCommitsRun(f)))
	}
	x.wantClean()

	// A plaintext write that fails after the declaration, and an --expand that is put back.
	f = newFake(t)
	x = storeWriteStore(t, f)
	x.commit(x.store, "env.prod.yaml", "A: &x 1\n")
	x.push(x.store)
	if err := f.run("set", "-g", "NEW", "v", "--env", "prod", "--type", "str", "--desc", "d"); err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("got %v", err)
	}
	if err := f.run("set", "-g", "NEW", "$NOWHERE", "--expand", "--type", "str", "--desc", "d"); err == nil || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("got %v", err)
	}
	if got := x.subjects(); got != nil || len(storeWriteCommitsRun(f)) != 0 {
		t.Fatalf("commits %q, git commit ran %d times", got, len(storeWriteCommitsRun(f)))
	}
	x.wantClean()
}

func TestHandEditNotSwept(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	write(t, x.file("common.yaml"), "NAME: by hand\n")
	write(t, x.file("env.loose.yaml"), "A: b\n")
	got := stderrOf(t, f, "set", "-g", "OTHER", "v", "--env", "prod", "--type", "str", "--desc", "d")
	if want := "store: uncommitted changes in " + x.store + "\n"; got != want {
		t.Fatalf("stderr %q, want %q", got, want)
	}
	if got := x.subjects(); !slices.Equal(got, []string{"set OTHER (manifest.yaml, env.prod.yaml)"}) {
		t.Fatalf("commits %q", got)
	}
	if got := x.git("show", "--name-only", "--format=", "HEAD"); got != ".ezc/env.prod.yaml\n.ezc/manifest.yaml" {
		t.Fatalf("the commit holds %q", got)
	}
	if got := x.git("status", "--porcelain", "--untracked-files=all"); got != " M .ezc/common.yaml\n?? .ezc/env.loose.yaml" {
		t.Fatalf("left behind:\n%s", got)
	}
	if readFile(t, x.file("common.yaml")) != "NAME: by hand\n" {
		t.Fatal("the hand edit was changed")
	}
}

// storeWriteRefused runs each write and fails unless it is refused with want, exit 1, having changed and committed nothing.
func storeWriteRefused(t *testing.T, f *fake, x storeWriteFix, want string) {
	t.Helper()
	storeWriteEditor(f, "NAME: edited\n")
	f.backend.EditFunc = func(p []backend.Pair) []backend.Pair { return nil }
	f.stdin = "v\n"
	head, status := x.git("rev-parse", "HEAD"), x.git("status", "--porcelain", "--untracked-files=all")
	before := map[string]string{}
	for _, name := range []string{"manifest.yaml", "common.yaml", "common.enc.yaml"} {
		before[name] = readFile(t, x.file(name))
	}
	for _, args := range [][]string{
		{"set", "-g", "NAME", "v2"},
		{"set", "-g", "TOKEN"},
		{"set", "-g", "NEW", "v", "--type", "str", "--desc", "d"},
		{"unset", "-g", "NAME"},
		{"unset", "-g", "NAME", "--undeclare"},
		{"unset", "-g", "TOKEN"},
		{"edit", "-g"},
		{"edit", "-g", "--secrets"},
		{"edit", "-g", "--env", "new"},
	} {
		f.out.Reset()
		f.gits, f.execs, f.prompts = nil, nil, nil
		err := f.run(args...)
		if err == nil || err.Error() != want {
			t.Errorf("%v: got %v", args, err)
			continue
		}
		storeWriteExitsOne(t, err)
		if f.out.Len() != 0 || len(f.execs) != 0 || len(storeWriteCommitsRun(f)) != 0 {
			t.Errorf("%v: stdout %q, editor ran %d times, git commit ran %d times", args, f.out.String(), len(f.execs), len(storeWriteCommitsRun(f)))
		}
		for name, content := range before {
			if readFile(t, x.file(name)) != content {
				t.Errorf("%v: %s changed", args, name)
			}
		}
		missing(t, x.file("env.new.yaml"))
		if got := x.git("rev-parse", "HEAD"); got != head {
			t.Errorf("%v: a commit was made", args)
		}
		if got := x.git("status", "--porcelain", "--untracked-files=all"); got != status {
			t.Errorf("%v: the tree changed:\n%s", args, got)
		}
	}
}

func TestWriteRefusedWhileBehind(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	x.secret(f, "common.enc.yaml", "TOKEN", "t1")
	x = x.published()
	x.theirs(2)
	storeWriteRefused(t, f, x, storeWriteBehind)
	if !strings.Contains(f.errOut.String(), behindTwo) {
		t.Fatalf("stderr %q", f.errOut.String())
	}

	f = newFake(t)
	x = storeWriteStore(t, f)
	x.theirs(1)
	err := f.run("set", "-g", "NAME", "v2")
	if want := strings.Replace(storeWriteBehind, "2 commits", "1 commit", 1); err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
}

func TestWriteRefusedWhileDiverged(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	x.secret(f, "common.enc.yaml", "TOKEN", "t1")
	x = x.published()
	x.commit(x.store, "env.mine.yaml", "A: b\n")
	x.theirs(2)
	storeWriteRefused(t, f, x, storeWriteDiverged)
}

func TestWriteAllowedWhileAhead(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	x.commit(x.store, "env.mine.yaml", "A: b\n")
	got := stderrOf(t, f, "set", "-g", "NAME", "v2")
	if want := "store: 1 commit ahead of origin — run ezc store push\n"; got != want {
		t.Fatalf("stderr %q, want %q", got, want)
	}
	if got := x.subjects(); !slices.Equal(got, []string{"change env.mine.yaml", "set NAME (common.yaml)"}) {
		t.Fatalf("commits %q", got)
	}
	x.wantClean()
}

func TestLocalOnlyStoreWriteCommits(t *testing.T) {
	f := newFake(t)
	x := storeWriteFix{localStore(t, f)}
	x.commit(x.store, "manifest.yaml", mToken)
	stderrOf(t, f, "set", "-g", "NAME", "v2")
	if got := x.git("log", "-1", "--format=%s"); got != "set NAME (common.yaml)" || x.commits() != "3" {
		t.Fatalf("last commit %q of %s", got, x.commits())
	}
	x.wantClean()
}

func TestRepoWriteNeverCommits(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	g := x.g
	if err := g.Init(f.wd); err != nil {
		t.Fatal(err)
	}
	setRepo(t, f, map[string]string{})
	if done, err := g.Commit(f.wd, "start", gitstore.Author{}, ezcDir+"/manifest.yaml", ezcDir+"/"+backend.FakePolicy); err != nil || !done {
		t.Fatalf("commit: %v, %v", done, err)
	}
	storeWriteEditor(f, "NAME: edited\n")
	for _, args := range [][]string{{"set", "NAME", "v"}, {"set", "NEW", "v", "--type", "str", "--desc", "d"}, {"unset", "NAME"}, {"edit"}} {
		if got := stderrOf(t, f, args...); got != "" || len(f.gits) != 0 {
			t.Errorf("%v: stderr %q, git ran %v", args, got, f.gits)
		}
	}
	stdout, _, err := g.Run(proc.Command{Dir: f.wd, Name: "git", Args: []string{"rev-list", "--count", "HEAD"}})
	if err != nil || strings.TrimSpace(string(stdout)) != "1" {
		t.Fatalf("the repo holds %q commits (%v), want 1", stdout, err)
	}
	if got := x.subjects(); got != nil {
		t.Fatalf("the store got commits %q", got)
	}
}

// The store is the scope without -g when the working directory is inside it.
func TestWriteInsideTheStoreCommits(t *testing.T) {
	f := newFake(t)
	x := storeWriteStore(t, f)
	f.wd = x.store
	stderrOf(t, f, "set", "NAME", "v2")
	if f.out.String() != "set NAME in repo/common.yaml\n" {
		t.Fatalf("stdout %q", f.out.String())
	}
	x.wantCommit("set NAME (common.yaml)", "common.yaml")

	x.theirs(1)
	f.now = noon.Add(11 * time.Minute)
	err := f.run("unset", "NAME")
	if err == nil || !strings.Contains(err.Error(), "the store has diverged from origin.") {
		t.Fatalf("got %v", err)
	}
}

func TestPlainFolderStoreWrites(t *testing.T) {
	f := newFake(t)
	plain := t.TempDir()
	f.env["EZC_STORE"] = plain
	write(t, filepath.Join(plain, ezcDir, "manifest.yaml"), m1)
	storeWriteEditor(f, "NAME: edited\n")
	for _, args := range [][]string{{"edit", "-g"}, {"set", "-g", "NAME", "v"}, {"set", "-g", "NEW", "v", "--type", "str", "--desc", "d"}, {"unset", "-g", "NAME"}} {
		if got := stderrOf(t, f, args...); got != "" {
			t.Errorf("%v: stderr %q", args, got)
		}
		// One question, whether the folder is a repo, and nothing after it.
		if len(f.gits) != 1 || f.gits[0].Dir != plain || len(storeWriteCommitsRun(f)) != 0 {
			t.Errorf("%v: git ran %v", args, f.gits)
		}
	}
	if got := readFile(t, filepath.Join(plain, ezcDir, "common.yaml")); got != "NEW: v\n" {
		t.Fatalf("common.yaml %q", got)
	}
	if _, err := os.Stat(filepath.Join(plain, ".git")); err == nil {
		t.Fatal("the folder was made a git repo")
	}
}

func TestCommitMessageNeverHoldsAValue(t *testing.T) {
	const value = "plain-marker-value"
	f := newFake(t)
	x := storeWriteStore(t, f)
	stderrOf(t, f, "set", "-g", "NAME", value)
	stderrOf(t, f, "set", "-g", "NEW", value, "--type", "str", "--desc", "d")
	stderrOf(t, f, "unset", "-g", "NEW", "--undeclare")
	if got := x.subjects(); len(got) != 3 {
		t.Fatalf("commits %q", got)
	}
	if got := x.git("log", "--format=%B%an%ae%cn%ce"); strings.Contains(got, value) {
		t.Fatalf("a commit message holds the value:\n%s", got)
	}
	for _, c := range storeWriteCommitsRun(f) {
		if strings.Contains(strings.Join(c.Args, " "), value) {
			t.Fatalf("git was given the value: %v", c.Args)
		}
	}
}

func TestCommitFailureSaysWhereTheChangeIs(t *testing.T) {
	const value = "plain-marker-value"
	f := newFake(t)
	x := storeWriteStore(t, f)
	hook := "#!/bin/sh\ncat .ezc/common.yaml\necho 'error: refused by the hook' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(x.store, ".git", "hooks", "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	err := f.run("set", "-g", "NAME", value)
	want := "the change was written but could not be committed: refused by the hook. Commit it in " + x.store + " with git."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	storeWriteExitsOne(t, err)
	if got := readFile(t, x.file("common.yaml")); got != "NAME: "+value+"\n" {
		t.Fatalf("common.yaml %q", got)
	}
	if got := x.subjects(); got != nil {
		t.Fatalf("commits %q", got)
	}
}
