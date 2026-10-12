package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const agreeSamPrivate = "FAKE-KEY-bb02\n"

func agreeNotFor(file string) string {
	return file + " is not encrypted for the people recipients.yaml names. Run ezc store rewrap."
}

// agreeBoth is a registry of gene and sam.
func agreeBoth() map[string]recipients.Recipient {
	return map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
}

// agreeClone is sam's machine: a fake with sam's key whose store is a new clone of x's remote.
func agreeClone(t *testing.T, x storeWriteFix) (*fake, storeWriteFix) {
	t.Helper()
	f := storeCmdClone(t, x)
	write(t, setupKeyFile(f), agreeSamPrivate)
	if err := config.Save(setupConfig(f), config.Config{User: "sam"}); err != nil {
		t.Fatal(err)
	}
	y := x
	y.store = filepath.Join(f.env["EZC_HOME"], "store")
	return f, y
}

// agreeStale is a store whose registry names gene alone while its policy and its files still name the stray key.
func agreeStale(t *testing.T, f *fake) storeWriteFix {
	t.Helper()
	return accessStoreWith(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene, accessStray)
}

// A write to a file that somebody the registry does not name can read would hand them the new value.
func TestStoreWriteRefusedWhenTheFileIsNotForTheRegistry(t *testing.T) {
	f := newFake(t)
	x := agreeStale(t, f)
	f.stdin = "n3w-value\n"
	accessUntouched(t, f, x, agreeNotFor("common.enc.yaml"), "set", "-g", "TOKEN")
	accessUntouched(t, f, x, agreeNotFor("env.prod.enc.yaml"), "set", "-g", "TOKEN", "--env", "prod")
	accessUntouched(t, f, x, agreeNotFor("common.enc.yaml"), "unset", "-g", "TOKEN")

	f.tty = true
	f.env["EDITOR"] = "vi"
	f.paths["vi"] = "/usr/bin/vi"
	accessUntouched(t, f, x, agreeNotFor("env.prod.enc.yaml"), "edit", "-g", "--secrets", "--env", "prod")
	if f.backend.LastEditor != "" {
		t.Fatal("the editor was opened")
	}
	f.tty = false

	// The fix it names works, and then the write does.
	if _, _, err := accessRun(f, "store", "rewrap"); err != nil {
		t.Fatal(err)
	}
	f.stdin = "n3w-value\n"
	if _, _, err := accessRun(f, "set", "-g", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene)
}

// A file that does not exist yet is made from the policy: one that names another key than the registry is refused.
func TestStoreWriteRefusedWhenThePolicyIsNotTheRegistrys(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	x.commit(x.store, ".fakepolicy", accessGene+"\n"+accessStray+"\n")
	x.push(x.store)
	f.stdin = "v\n"
	want := "The store's encryption policy does not name the people recipients.yaml names, so env.staging.enc.yaml would not be encrypted for them. Run ezc store rewrap."
	accessUntouched(t, f, x, want, "set", "-g", "TOKEN", "--env", "staging")
	if _, err := os.Stat(x.file("env.staging.enc.yaml")); err == nil {
		t.Fatal("the file was made")
	}
	// The files that are there are encrypted for the registry, and a write to one goes through.
	f.stdin = "v\n"
	if _, _, err := accessRun(f, "set", "-g", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	// A plaintext value has no recipients.
	if _, _, err := accessRun(f, "set", "-g", "NAME", "n2", "--env", "staging"); err != nil {
		t.Fatal(err)
	}
}

// A store with no registry was set up under a policy ezc cannot judge: its writes are as before.
func TestStoreWriteWithoutARegistryIsNotJudged(t *testing.T) {
	f := newFake(t)
	accessStoreWith(t, f, nil, accessGene, accessStray)
	f.stdin = "v\n"
	if _, _, err := accessRun(f, "set", "-g", "TOKEN"); err != nil {
		t.Fatal(err)
	}
}

// Route A of the review: a recipient is removed on the remote while this machine, which has not fetched, writes a
// secret to a new file under its old policy. The pull says so, and nothing is pushed or set until the re-wrap.
func TestPullAndPushSayWhenAFileIsNotForTheRegistry(t *testing.T) {
	f := newFake(t)
	reg := agreeBoth()
	reg["zed"] = accessEntry(accessStray)
	x := accessStoreWith(t, f, reg, accessGene, accessSam, accessStray)
	sam, y := agreeClone(t, x)
	if _, _, err := accessRun(sam, "user", "remove", "zed"); err != nil {
		t.Fatal(err)
	}
	accessLevel(t, y)

	// gene fetched a minute ago and does not know.
	attemptedAt(t, f, noon.Add(-time.Minute))
	f.stdin = "n3w-value\n"
	if _, _, err := accessRun(f, "set", "-g", "TOKEN", "--env", "staging"); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := storeCmdRun(f, "pull")
	wantExit(t, err, 1)
	if out != "pulled 1 commit from origin\n" {
		t.Fatalf("stdout %q", out)
	}
	want := "! env.staging.enc.yaml is not encrypted for the registry. Run ezc store rewrap before you set anything.\n"
	if !strings.Contains(errOut, want) || strings.Contains(errOut, "common.enc.yaml") {
		t.Fatalf("stderr\n%s\nwant\n%s", errOut, want)
	}
	// It is said again by a pull that brings nothing.
	if out, errOut, err := storeCmdRun(f, "pull"); out != "already up to date\n" || !strings.Contains(errOut, want) || err == nil {
		t.Fatalf("second pull: %v\nstdout %q\nstderr %q", err, out, errOut)
	}

	// Nothing leaves this machine while a file is for somebody the registry does not name.
	head := x.git("rev-parse", "refs/remotes/origin/main")
	storeCmdRefused(t, f, "env.staging.enc.yaml is not encrypted for the people recipients.yaml names, so nothing was pushed. Run ezc store rewrap.", "push")
	if _, _, err := storeCmdRun(f, "status"); err != nil || x.git("rev-parse", "refs/remotes/origin/main") != head {
		t.Fatalf("the remote moved (%v)", err)
	}
	f.stdin = "again\n"
	if _, _, err := accessRun(f, "set", "-g", "TOKEN", "--env", "staging"); err == nil || err.Error() != agreeNotFor("env.staging.enc.yaml") {
		t.Fatalf("set: %v", err)
	}

	if _, _, err := accessRun(f, "store", "rewrap"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.backend.Recipients(x.file("env.staging.enc.yaml")); err != nil || !slices.Equal(got, []string{accessGene, accessSam}) {
		t.Fatalf("env.staging.enc.yaml is for %q (%v)", got, err)
	}
	accessLevel(t, x)
}

// Route B: an access change that stayed here unpushed, and a value changed on the remote in the same file. The pull
// keeps the remote's file, which is not encrypted for the registry, and says that and not "set these again".
func TestPullSaysWhenAReplacedFileWasRewrappedHere(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, agreeBoth(), accessGene, accessSam)
	sam, _ := agreeClone(t, x)

	f.gitFail = "push"
	if _, _, err := accessRun(f, "user", "add", "zed", accessStray); err == nil || !strings.HasPrefix(err.Error(), "Committed, but the push failed") {
		t.Fatalf("user add: %v", err)
	}
	f.gitFail = ""
	sam.stdin = "their-value\n"
	if _, _, err := accessRun(sam, "set", "-g", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	storeCmdWant(t, sam, "pushed 1 commit to origin\n", "push")

	out, errOut, err := storeCmdRun(f, "pull")
	wantExit(t, err, 1)
	if out != "pulled 1 commit from origin\n" {
		t.Fatalf("stdout %q", out)
	}
	want := "! common.enc.yaml was re-wrapped here and changed on the remote. The remote's version was kept, and it is not encrypted for the people recipients.yaml names. Run ezc store rewrap, then set again any value you changed in it since your last push.\n"
	if errOut != want {
		t.Fatalf("stderr\n%s\nwant\n%s", errOut, want)
	}
	f.stdin = "mine\n"
	if _, _, err := accessRun(f, "set", "-g", "TOKEN"); err == nil || err.Error() != agreeNotFor("common.enc.yaml") {
		t.Fatalf("set: %v", err)
	}
	storeCmdRefused(t, f, "common.enc.yaml is not encrypted for the people recipients.yaml names, so nothing was pushed. Run ezc store rewrap.", "push")

	if _, _, err := accessRun(f, "store", "rewrap"); err != nil {
		t.Fatal(err)
	}
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene, accessSam, accessStray)
	accessLevel(t, x)
}

// A request is denied with a commit and a push: not while that push would carry a file the registry does not agree with.
func TestDenyRefusedWhileAFileIsNotForTheRegistry(t *testing.T) {
	f := newFake(t)
	x := agreeStale(t, f)
	req := recipients.Request{Name: "sam", Key: accessSam, Kind: recipients.KindUser, Host: "h", Requested: "2026-10-11"}
	write(t, x.file(recipients.RequestPath("sam")), string(req.Marshal()))
	if _, err := x.g.Commit(x.store, "a merged request", gitstore.Author{}, ezcDir); err != nil {
		t.Fatal(err)
	}
	x.push(x.store)
	accessUntouched(t, f, x, "common.enc.yaml, env.prod.enc.yaml are not encrypted for the people recipients.yaml names. Run ezc store rewrap.", "user", "deny", "sam")
}
