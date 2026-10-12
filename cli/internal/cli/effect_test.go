package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

	out, _, err = accessRun(f, "user", "add", "sam", accessSam, "--yes")
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

const (
	effectIntruderKey = "fake1ee05"
	effectAlso        = "! mallory gains access to common.enc.yaml, env.prod.enc.yaml.\n"
	effectAsk         = "Let them in too? [y/N] "
)

// effectIntruder is a store gene, sam and the bot ci (repo o/a) read, into whose registry the name mallory was
// written with git alone: no file is encrypted for its key.
func effectIntruder(t *testing.T, f *fake) storeWriteFix {
	t.Helper()
	x := accessStoreWith(t, f, map[string]recipients.Recipient{
		"gene": accessEntry(accessGene),
		"sam":  accessEntry(accessSam),
		"ci":   {Key: accessStray, Kind: recipients.KindBot, Added: "2026-10-01", AddedBy: "gene", Repos: []string{"o/a"}},
	}, accessGene, accessSam, accessStray)
	dir := filepath.Join(x.store, ezcDir)
	reg := accessRegistry(t, dir)
	reg.Recipients["mallory"] = recipients.Recipient{Key: effectIntruderKey, Kind: recipients.KindUser, Added: "2026-10-11", AddedBy: "mallory"}
	if err := reg.Save(dir); err != nil {
		t.Fatal(err)
	}
	if done, err := x.g.Commit(x.store, "tidy", gitstore.Author{}, ezcDir); err != nil || !done {
		t.Fatalf("commit: %v %v", done, err)
	}
	x.push(x.store)
	return x
}

// effectChanges are the access changes that have a subject, each of which would let mallory in besides.
var effectChanges = [][]string{
	{"user", "add", "zed", "fake1dd04"},
	{"user", "remove", "sam"},
	{"user", "rotate"},
	{"bot", "add", "deploy", "--repo", "o/b"},
	{"bot", "remove", "ci"},
	{"bot", "rotate", "ci"},
}

// effectUntouched runs a command that must change nothing in the store, deliver no key and make no key file, and
// returns what it printed and its error.
func effectUntouched(t *testing.T, f *fake, x storeWriteFix, g *botFake, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	dir := filepath.Join(x.store, ezcDir)
	head, before, config := x.git("rev-parse", "HEAD"), setupSnapshot(t, dir), readFile(t, setupConfig(f))
	stdout, stderr, err = accessRun(f, args...)
	if x.git("rev-parse", "HEAD") != head || !mapsEqual(before, setupSnapshot(t, dir)) {
		t.Fatalf("%v changed the store", args)
	}
	x.wantClean()
	if len(g.calls) != 0 {
		t.Fatalf("%v ran gh: %+v", args, g.calls)
	}
	if _, serr := os.Stat(rotateNew(f, "")); serr == nil || readFile(t, setupConfig(f)) != config {
		t.Fatalf("%v left a key file or changed the config", args)
	}
	return stdout, stderr, err
}

// Without a terminal, a change that would let in somebody it was not asked about is refused unless --yes says so.
func TestAccessChangeNeedsYesToLetSomebodyElseIn(t *testing.T) {
	f := newFake(t)
	x := effectIntruder(t, f)
	g := botFakeGh(f)
	for _, args := range effectChanges {
		out, errOut, err := effectUntouched(t, f, x, g, args...)
		wantUsage(t, err, "this would also let mallory read the store. Check ezc user list: pass --yes when that is right, or take the name out with ezc user remove first.")
		if out != effectAlso || errOut != "" {
			t.Fatalf("%v: stdout %q, stderr %q", args, out, errOut)
		}
	}
}

// On a terminal it asks, on stderr, and anything but yes changes nothing.
func TestAccessChangeAsksBeforeItLetsSomebodyElseIn(t *testing.T) {
	f := newFake(t)
	x := effectIntruder(t, f)
	g := botFakeGh(f)
	f.tty = true
	for _, args := range effectChanges {
		for _, answer := range []string{"n\n", ""} {
			f.stdin = answer
			out, errOut, err := effectUntouched(t, f, x, g, args...)
			var usage *UsageError
			if err == nil || err.Error() != "Nothing was changed." || errors.As(err, &usage) {
				t.Fatalf("%v, answer %q: got %v", args, answer, err)
			}
			if out != effectAlso || errOut != effectAsk {
				t.Fatalf("%v: stdout %q, stderr %q", args, out, errOut)
			}
		}
	}

	f.stdin = "y\n"
	out, errOut, err := accessRun(f, "user", "add", "zed", "fake1dd04")
	if err != nil || out != effectAlso+"✓ add zed\n✓ pushed to "+x.bare+"\n" || errOut != effectAsk {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
}

// --yes goes on without a question, with or without a terminal, and a bot's key is then delivered.
func TestAccessChangeWithYesLetsSomebodyElseIn(t *testing.T) {
	for _, args := range effectChanges {
		f := newFake(t)
		x := effectIntruder(t, f)
		botFakeGh(f)
		out, errOut, err := accessRun(f, append(args, "--yes")...)
		if err != nil || !strings.HasPrefix(out, effectAlso+"✓ ") || errOut != "" {
			t.Fatalf("%v: %v\nstdout %q\nstderr %q", args, err, out, errOut)
		}
		got, rerr := f.backend.Recipients(x.file("common.enc.yaml"))
		if rerr != nil || !slices.Contains(got, effectIntruderKey) {
			t.Fatalf("%v: common.enc.yaml is for %q (%v)", args, got, rerr)
		}
	}
}

// A change that only takes a key nobody names off the files lets nobody in, and asks nothing.
func TestAccessChangeThatOnlyShutsOutDoesNotAsk(t *testing.T) {
	f := newFake(t)
	x := agreeStale(t, f)
	accessWant(t, f, effectLoses+"✓ add sam\n✓ pushed to "+x.bare+"\n", "user", "add", "sam", accessSam)
}

// approve asks its own question first; its --yes answers both.
func TestUserApproveAsksAboutSomebodyElseToo(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	dir := filepath.Join(j.x.store, ezcDir)
	reg := accessRegistry(t, dir)
	reg.Recipients["mallory"] = recipients.Recipient{Key: effectIntruderKey, Kind: recipients.KindUser, Added: "2026-10-11", AddedBy: "mallory"}
	if err := reg.Save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := j.x.g.Commit(j.x.store, "tidy", gitstore.Author{}, ezcDir); err != nil {
		t.Fatal(err)
	}
	j.x.push(j.x.store)
	head := j.x.git("rev-parse", "HEAD")

	j.a.tty, j.a.stdin = true, "y\nn\n"
	out, errOut, err := accessRun(j.a, "user", "approve", "sam")
	if err == nil || err.Error() != "Nothing was changed." || errOut != "Approve sam? [y/N] "+effectAsk || !strings.HasSuffix(out, effectAlso) {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
	if j.x.git("rev-parse", "HEAD") != head || !slices.Equal(j.branches(), []string{joinBranch, "main"}) {
		t.Fatal("the store or the branch changed")
	}
	j.x.wantClean()

	j.a.tty, j.a.stdin = false, ""
	out, errOut, err = accessRun(j.a, "user", "approve", "sam", "--yes")
	if err != nil || !strings.Contains(out, effectAlso+"✓ approve sam\n") || errOut != "" {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
}
