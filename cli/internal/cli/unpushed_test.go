package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const (
	accessOffline = "The store's remote could not be reached, so nobody else would get this change. Run this again when it can be."
	accessMoved   = "The store's remote changed while this ran. Nothing was changed. Run ezc store pull, then run this again."
	// pushFails is a fake's gitFail that fails the push of the store's branch and nothing else.
	pushFails = "--set-upstream"
)

// movesOnPush makes the remote move just before the fake's first push: a commit arrives there from the other clone.
func movesOnPush(f *fake, x storeWriteFix) {
	done := false
	f.execHook = func(c proc.Command) {
		if !done && slices.Contains(c.Args, "push") {
			done = true
			x.theirs(1)
		}
	}
}

// An access change made while nobody else can get it is the start of two registries: it is refused.
func TestAccessChangeRefusedWhenTheRemoteCannotBeReached(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	botFakeGh(f)
	if err := os.Rename(x.bare, x.bare+".away"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"user", "add", "sam", accessSam}, {"store", "rewrap"}, {"user", "rotate"}, {"bot", "add", "ci", "--repo", "o/a"},
	} {
		accessUntouched(t, f, x, accessOffline, args...)
	}
	if _, err := os.Stat(rotateNew(f, "")); err == nil {
		t.Fatal("a key file was made")
	}
	// What only reads still does.
	if _, _, err := accessRun(f, "user", "list"); err != nil {
		t.Fatal(err)
	}
}

// The remote moved between the fetch and the push: the commit is taken back, the files are as they were, and what
// was changed by hand elsewhere in the store is still there.
func TestAccessChangeIsTakenBackWhenTheRemoteMoved(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	write(t, x.file("common.yaml"), "NAME: by hand\n")
	write(t, x.file("env.mine.yaml"), "A: untracked\n")
	movesOnPush(f, x)
	accessUntouched(t, f, x, accessMoved, "user", "add", "sam", accessSam)
	if got := readFile(t, x.file("common.yaml")); got != "NAME: by hand\n" {
		t.Fatalf("the hand edit is now %q", got)
	}
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene)

	// What it says to do works.
	f.execHook = nil
	if err := os.Remove(x.file("env.mine.yaml")); err != nil {
		t.Fatal(err)
	}
	x.git("checkout", "--quiet", "--", ".")
	storeCmdWant(t, f, "pulled 1 commit from origin\n", "pull")
	accessWant(t, f, "✓ add sam\n✓ pushed to "+x.bare+"\n", "user", "add", "sam", accessSam)
	accessLevel(t, x)
}

// A commit that cannot be taken back stays, and is said to be unpushed as before.
func TestAccessChangeStaysWhenItCannotBeTakenBack(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	movesOnPush(f, x)
	f.gitFail = "update-ref"
	out, _, err := accessRun(f, "user", "add", "sam", accessSam)
	var unpushed *unpushedError
	if !errors.As(err, &unpushed) || out != "✓ add sam\n" {
		t.Fatalf("got %v, stdout %q", err, out)
	}
	if got := x.subjects(); !slices.Equal(got, []string{"add sam"}) {
		t.Fatalf("commits %q", got)
	}
}

// A bot's key was delivered before the commit that the moved remote undid: the repos are named, and the pull.
func TestBotAddWhenTheRemoteMoved(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	registerRepos(t, x, "o/a")
	botFakeGh(f)
	movesOnPush(f, x)
	head := x.git("rev-parse", "HEAD")
	out, _, err := accessRun(f, "bot", "add", "ci", "--repo", "o/a")
	want := "The store's remote changed while this ran. EZC_AGE_KEY was set in o/a to a key the store does not name, which reads nothing. The store was not changed. Run ezc store pull, then run the same ezc bot add again."
	if err == nil || err.Error() != want || out != "" {
		t.Fatalf("got %v\nwant %s\nstdout %q", err, want, out)
	}
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("the commit was left")
	}
	x.wantClean()
}

// Two changes to who can read the store that did not see each other cannot be merged: the pull says how to get out.
func TestPullConflictInTheRegistry(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, agreeBoth(), accessGene, accessSam)
	sam, _ := agreeClone(t, x)
	f.gitFail = pushFails
	if _, _, err := accessRun(f, "user", "add", "zed", accessStray); err == nil {
		t.Fatal("the push was made to fail")
	}
	f.gitFail = ""
	if _, _, err := accessRun(sam, "user", "add", "tom", "fake1dd04"); err != nil {
		t.Fatal(err)
	}
	head := x.git("rev-parse", "HEAD")
	want := ".ezc/.fakepolicy, .ezc/recipients.yaml were changed here and on the remote, so nothing was pulled. Who can read the store was changed on two machines at once, and the files ezc writes for that cannot be merged. In " + x.store + ", `git log refs/remotes/origin/main..HEAD` lists the commits made here that the remote lacks; drop them with `git reset --hard refs/remotes/origin/main`, then make your change again."
	storeCmdRefused(t, f, want, "pull")
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("the store was changed")
	}
	x.wantClean()

	// What it says works.
	x.git("reset", "--quiet", "--hard", "refs/remotes/origin/main")
	accessWant(t, f, "✓ add zed\n✓ pushed to "+x.bare+"\n", "user", "add", "zed", accessStray)
	if reg := accessRegistry(t, filepath.Join(x.store, ezcDir)); len(reg.Recipients) != 4 {
		t.Fatalf("the registry names %d", len(reg.Recipients))
	}
}

// A file that this machine's key does not open may be one an older key file of the machine does.
func TestRewrapWithoutAKeySaysAnOlderKeyFileMayOpenIt(t *testing.T) {
	f := newFake(t)
	x := agreeStale(t, f)
	f.backend.Locked = true
	_, _, err := accessRun(f, "store", "rewrap")
	if err == nil || !strings.Contains(err.Error(), "Nothing was changed.") || !strings.HasSuffix(err.Error(), "A key file this machine used before may still open it: see \"A file this machine's key does not open\" in docs/troubleshooting.md.") {
		t.Fatalf("got %v", err)
	}
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene, accessStray)
	if _, err := os.Stat(x.file(recipients.DueFile)); err == nil {
		t.Fatal("rotation-due.yaml was written")
	}
}
