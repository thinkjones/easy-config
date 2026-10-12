package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const (
	effectGains = "! sam gains access to common.enc.yaml, env.prod.enc.yaml.\n"
	effectLoses = "! " + accessStray + ", a key recipients.yaml does not name, loses access to common.enc.yaml, env.prod.enc.yaml. The secrets it could read are recorded as needing new values: ezc store status --due lists them.\n"
)

// effectPushed commits what the other clone holds in its .ezc as message and pushes it: a change made with git alone.
func effectPushed(t *testing.T, x storeWriteFix, message string) {
	t.Helper()
	if done, err := x.g.Commit(x.other, message, gitstore.Author{}, ezcDir); err != nil || !done {
		t.Fatalf("commit: %v %v", done, err)
	}
	x.push(x.other)
}

// A re-wrap that lets somebody in asks first: the registry may hold a name nobody approved.
func TestRewrapAsksBeforeItLetsSomebodyIn(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, agreeBoth(), accessGene, accessStray)
	dir := filepath.Join(x.store, ezcDir)

	// Without a terminal there is nobody to ask.
	head, before := x.git("rev-parse", "HEAD"), setupSnapshot(t, dir)
	out, _, err := accessRun(f, "store", "rewrap")
	wantUsage(t, err, "ezc store rewrap: this would let sam read the store. Say --yes to do that without being asked: there is no terminal to ask on.")
	if out != effectGains+effectLoses {
		t.Fatalf("stdout %q", out)
	}
	if x.git("rev-parse", "HEAD") != head || !mapsEqual(before, setupSnapshot(t, dir)) {
		t.Fatal("the store was changed")
	}

	// On a terminal it asks on stderr, and anything but yes changes nothing.
	f.tty, f.stdin = true, "n\n"
	out, errOut, err := accessRun(f, "store", "rewrap")
	wantExit(t, err, 1)
	if out != effectGains+effectLoses+"Not re-wrapped.\n" || errOut != "Let sam read the store? [y/N] " {
		t.Fatalf("stdout %q\nstderr %q", out, errOut)
	}
	if x.git("rev-parse", "HEAD") != head || !mapsEqual(before, setupSnapshot(t, dir)) {
		t.Fatal("the store was changed")
	}

	f.stdin = "y\n"
	out, errOut, err = accessRun(f, "store", "rewrap")
	if err != nil || out != effectGains+effectLoses+"✓ rewrap\n✓ pushed to "+x.bare+"\n" || errOut != "Let sam read the store? [y/N] " {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
	accessReaders(t, f, dir, accessGene, accessSam)
	// What the key that left could read is due for new values, under that key.
	due, err := recipients.LoadDue(dir)
	if err != nil || len(due) != 2 || due["common.enc.yaml/TOKEN"].Removed != accessStray || due["env.prod.enc.yaml/TOKEN"].On != "2026-10-11" {
		t.Fatalf("rotation-due: %v, %v", due, err)
	}
	x.wantClean()
	accessLevel(t, x)
}

// A re-wrap that only takes a key off the files asks nothing.
func TestRewrapThatLetsNobodyInDoesNotAsk(t *testing.T) {
	f := newFake(t)
	x := agreeStale(t, f)
	accessWant(t, f, effectLoses+"✓ rewrap\n✓ pushed to "+x.bare+"\n", "store", "rewrap")
	if due, err := recipients.LoadDue(filepath.Join(x.store, ezcDir)); err != nil || len(due) != 2 {
		t.Fatalf("rotation-due: %v, %v", due, err)
	}
}

// The registry is a file in a repo: whoever can push can add a name to it without ezc. The pull names who was
// added, and the next access change says who else it lets in besides the one it was asked for.
func TestAccessChangeSaysWhoElseItLetsIn(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	reg := accessRegistry(t, filepath.Join(x.other, ezcDir))
	reg.Recipients["mallory"] = recipients.Recipient{Key: accessStray, Kind: recipients.KindUser, Added: "2026-10-11", AddedBy: "mallory"}
	if err := reg.Save(filepath.Join(x.other, ezcDir)); err != nil {
		t.Fatal(err)
	}
	effectPushed(t, x, "tidy")

	out, errOut, err := storeCmdRun(f, "pull")
	wantExit(t, err, 1)
	want := "! recipients.yaml changed on the remote: + mallory. ezc user list shows who can read the store.\n" +
		"! common.enc.yaml is not encrypted for the registry. Run ezc store rewrap before you set anything.\n" +
		"! env.prod.enc.yaml is not encrypted for the registry. Run ezc store rewrap before you set anything.\n"
	if out != "pulled 1 commit from origin\n" || errOut != want {
		t.Fatalf("stdout %q\nstderr\n%s\nwant\n%s", out, errOut, want)
	}

	out, _, err = accessRun(f, "user", "add", "sam", accessSam)
	if err != nil || out != "! mallory gains access to common.enc.yaml, env.prod.enc.yaml.\n✓ add sam\n✓ pushed to "+x.bare+"\n" {
		t.Fatalf("%v\nstdout %q", err, out)
	}
}

// A change that does what it was asked and no more says nothing more.
func TestAccessChangeIsQuietAboutItsOwnSubject(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, agreeBoth(), accessGene, accessSam)
	accessWant(t, f, "✓ add zed\n✓ pushed to "+x.bare+"\n", "user", "add", "zed", accessStray)
	out, _, err := accessRun(f, "user", "remove", "zed")
	if err != nil || strings.Contains(out, "!") || !strings.HasPrefix(out, "✓ remove zed\n") {
		t.Fatalf("%v\nstdout %q", err, out)
	}
	out, _, err = accessRun(f, "user", "rotate")
	if err != nil || strings.Contains(out, "!") {
		t.Fatalf("%v\nstdout %q", err, out)
	}
}

// What a pull brought is said when it changed who the registry names: added, removed, or given another key.
func TestPullSaysWhenTheRegistryChanged(t *testing.T) {
	f := newFake(t)
	reg := agreeBoth()
	reg["zed"] = accessEntry(accessStray)
	x := accessStoreWith(t, f, reg, accessGene, accessSam, accessStray)
	sam, _ := agreeClone(t, x)
	for _, args := range [][]string{{"user", "remove", "zed"}, {"user", "add", "kim", "fake1dd04"}, {"user", "rotate"}} {
		if _, _, err := accessRun(sam, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	out, errOut, err := storeCmdRun(f, "pull")
	if err != nil || out != "pulled 3 commits from origin\n" || errOut != "! recipients.yaml changed on the remote: + kim - zed ~ sam (new key). ezc user list shows who can read the store.\n" {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
	// A pull that changes nobody says nothing.
	if _, _, err := accessRun(sam, "set", "-g", "NAME", "n2"); err != nil {
		t.Fatal(err)
	}
	storeCmdWant(t, sam, "pushed 1 commit to origin\n", "push")
	storeCmdWant(t, f, "pulled 1 commit from origin\n", "pull")
}
