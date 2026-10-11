package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const removeValue = "s3cr3t-marker-value"

// removeStore is gene's machine and a store read by gene and sam, each encrypted file holding TOKEN.
func removeStore(t *testing.T, f *fake) storeWriteFix {
	t.Helper()
	return accessStoreWith(t, f, map[string]recipients.Recipient{
		"gene": accessEntry(accessGene),
		"sam":  accessEntry(accessSam),
	}, accessGene, accessSam)
}

const removeSamList = "2 secrets were readable by sam and need new values:\n  common.enc.yaml/TOKEN\n  env.prod.enc.yaml/TOKEN\nGive each a new value with ezc set -g NAME.\n"

func TestUserRemove(t *testing.T) {
	f := newFake(t)
	x := removeStore(t, f)
	out, errOut, err := accessRun(f, "user", "remove", "sam")
	if err != nil || errOut != "" {
		t.Fatalf("%v, stderr %q", err, errOut)
	}
	if want := "✓ remove sam\n✓ pushed to " + x.bare + "\n" + removeSamList; out != want {
		t.Fatalf("stdout %q\nwant   %q", out, want)
	}
	accessLevel(t, x)
	x.git("update-ref", "refs/remotes/origin/main", "HEAD~1")
	x.wantCommit("remove sam", ".fakepolicy", "common.enc.yaml", "env.prod.enc.yaml", recipients.File, recipients.DueFile)

	dir := filepath.Join(x.store, ezcDir)
	accessReaders(t, f, dir, accessGene)
	if reg := accessRegistry(t, dir); len(reg.Recipients) != 1 || reg.Recipients["gene"].Key != accessGene {
		t.Fatalf("the registry is %+v", reg)
	}
	due, err := recipients.LoadDue(dir)
	if err != nil || len(due) != 2 {
		t.Fatalf("due %v, %v", due, err)
	}
	for _, k := range []string{"common.enc.yaml/TOKEN", "env.prod.enc.yaml/TOKEN"} {
		if e := due[k]; e.Removed != "sam" || e.On != "2026-10-11" {
			t.Errorf("%s: %+v", k, e)
		}
	}
	if strings.Contains(x.git("log", "-p")+out, removeValue) {
		t.Fatal("a value was printed or committed")
	}
}

func TestUserRemoveWithoutSecrets(t *testing.T) {
	f := newFake(t)
	x := removeStore(t, f)
	for _, file := range accessEncFiles {
		if err := os.Remove(x.file(file)); err != nil {
			t.Fatal(err)
		}
	}
	x.git("add", "-A")
	x.git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "-m", "no secrets")
	x.push(x.store)
	accessWant(t, f, "✓ remove sam\n✓ pushed to "+x.bare+"\n", "user", "remove", "sam")
	x.git("update-ref", "refs/remotes/origin/main", "HEAD~1")
	x.wantCommit("remove sam", ".fakepolicy", recipients.File)
}

func TestUserRemoveKeepsTheFirstEntry(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, map[string]recipients.Recipient{
		"gene": accessEntry(accessGene),
		"sam":  accessEntry(accessSam),
		"zed":  accessEntry(accessStray),
	}, accessGene, accessSam, accessStray)
	accessRun(f, "user", "remove", "sam")
	_, _, err := accessRun(f, "user", "remove", "zed")
	if err != nil {
		t.Fatal(err)
	}
	due, _ := recipients.LoadDue(filepath.Join(x.store, ezcDir))
	if e := due["common.enc.yaml/TOKEN"]; e.Removed != "sam" {
		t.Fatalf("entry %+v", e)
	}
}

func TestUserRemoveUsage(t *testing.T) {
	f := newFake(t)
	x := removeStore(t, f)
	head := x.git("rev-parse", "HEAD")
	for _, c := range []struct {
		want string
		args []string
	}{
		{"ezc user remove: give a name.", nil},
		{"ezc user remove: the name is lower case letters, digits and -, starting with a letter or a digit.", []string{"Sam"}},
		{"ezc user remove: the name is lower case letters, digits and -, starting with a letter or a digit.", []string{strings.TrimSpace(accessGenePrivate)}},
	} {
		out, errOut, err := accessRun(f, append([]string{"user", "remove"}, c.args...)...)
		wantUsage(t, err, c.want)
		if out != "" || len(f.gits) != 0 || strings.Contains(err.Error()+errOut, "FAKE-KEY") {
			t.Errorf("%v: stdout %q, git ran %d times, error %v", c.args, out, len(f.gits), err)
		}
	}
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("a commit was made")
	}
}

func TestUserRemoveRefused(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, map[string]recipients.Recipient{
		"gene":   {Key: accessStray, Kind: recipients.KindUser, Added: "2026-10-01", AddedBy: "gene"},
		"laptop": {Key: accessGene, Kind: recipients.KindMachine, Owner: "gene", Added: "2026-10-01", AddedBy: "gene"},
		"ci":     {Key: accessSam, Kind: recipients.KindBot, Added: "2026-10-01", AddedBy: "gene"},
	}, accessGene, accessStray, accessSam)
	accessUntouched(t, f, x, "remove a bot with ezc bot remove.", "user", "remove", "ci")
	accessUntouched(t, f, x, "this user owns machines: laptop.", "user", "remove", "gene")
	accessUntouched(t, f, x, "nobody is not a recipient.", "user", "remove", "nobody")
}

func TestUserRemoveOneselfRefused(t *testing.T) {
	f := newFake(t)
	x := removeStore(t, f)
	accessUntouched(t, f, x, "gene is this machine's key and your only one. Removing it would lock you out.", "user", "remove", "gene")

	// A machine of a person with another entry is not the only way in.
	f = newFake(t)
	x = accessStoreWith(t, f, map[string]recipients.Recipient{
		"gene":   {Key: accessStray, Kind: recipients.KindUser, Added: "2026-10-01", AddedBy: "gene"},
		"laptop": {Key: accessGene, Kind: recipients.KindMachine, Owner: "gene", Added: "2026-10-01", AddedBy: "gene"},
	}, accessGene, accessStray)
	// But a bot is not a person.
	accessUntouched(t, f, x, "this user owns machines: laptop.", "user", "remove", "gene")
	if _, _, err := accessRun(f, "user", "remove", "laptop"); err != nil {
		t.Fatal(err)
	}
}

func TestUserRemoveLastRefused(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	accessUntouched(t, f, x, "gene is the last recipient and cannot be removed.", "user", "remove", "gene")
}

// removeDue is removeStore after sam was removed, with a clean tree level with the remote.
func removeDue(t *testing.T, f *fake) storeWriteFix {
	t.Helper()
	x := removeStore(t, f)
	if _, _, err := accessRun(f, "user", "remove", "sam"); err != nil {
		t.Fatal(err)
	}
	x.wantClean()
	x.git("update-ref", "refs/remotes/origin/main", "HEAD")
	return x
}

func TestSetClearsRotationDue(t *testing.T) {
	f := newFake(t)
	x := removeDue(t, f)
	f.stdin = "fresh-value-1\n"
	if got := stderrOf(t, f, "set", "-g", "TOKEN", "--env", "prod"); got != "" {
		t.Fatalf("stderr %q", got)
	}
	x.wantCommit("set TOKEN (env.prod.enc.yaml, rotation-due.yaml)", "env.prod.enc.yaml", recipients.DueFile)
	due, _ := recipients.LoadDue(filepath.Join(x.store, ezcDir))
	if len(due) != 1 || due["common.enc.yaml/TOKEN"].Removed != "sam" {
		t.Fatalf("due %v", due)
	}

	// The last entry takes the file with it, in the same commit.
	x.git("update-ref", "refs/remotes/origin/main", "HEAD")
	f.stdin = "fresh-value-2\n"
	stderrOf(t, f, "set", "-g", "TOKEN")
	x.wantCommit("set TOKEN (common.enc.yaml, rotation-due.yaml)", "common.enc.yaml", recipients.DueFile)
	if _, err := recipients.LoadDue(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(x.file(recipients.DueFile)); err == nil {
		t.Fatal("rotation-due.yaml is still there")
	}
	if strings.Contains(x.git("log", "-p"), "fresh-value") {
		t.Fatal("the history holds a value")
	}
}

func TestUnsetClearsRotationDue(t *testing.T) {
	f := newFake(t)
	x := removeDue(t, f)
	stderrOf(t, f, "unset", "-g", "TOKEN", "--env", "prod")
	x.wantCommit("unset TOKEN (env.prod.enc.yaml, rotation-due.yaml)", "env.prod.enc.yaml", recipients.DueFile)
	due, _ := recipients.LoadDue(filepath.Join(x.store, ezcDir))
	if len(due) != 1 || due["common.enc.yaml/TOKEN"].Removed != "sam" {
		t.Fatalf("due %v", due)
	}
}

func TestSetWithoutAnEntryLeavesRotationDueAlone(t *testing.T) {
	f := newFake(t)
	x := removeStore(t, f)
	// An uncommitted rotation-due.yaml would be refused if the command watched it.
	write(t, x.file(recipients.DueFile), "common.enc.yaml/OTHER: {removed: zed, on: 2026-10-01}\n")
	f.stdin = "fresh\n"
	stderrOf(t, f, "set", "-g", "TOKEN", "--env", "prod")
	if got := x.git("show", "--name-only", "--format=", "HEAD"); got != ".ezc/env.prod.enc.yaml" {
		t.Fatalf("the commit holds %q", got)
	}
	if got := readFile(t, x.file(recipients.DueFile)); !strings.Contains(got, "OTHER") {
		t.Fatalf("rotation-due.yaml is now %q", got)
	}
}

func TestSetRefusesWhenTheEntryIsEditedByHand(t *testing.T) {
	f := newFake(t)
	x := removeDue(t, f)
	write(t, x.file(recipients.DueFile), readFile(t, x.file(recipients.DueFile))+"# by hand\n")
	f.stdin = "fresh\n"
	err := f.run("set", "-g", "TOKEN")
	if err == nil || err.Error() != storeWriteDirty(recipients.DueFile, x) {
		t.Fatalf("got %v", err)
	}
}

func TestStoreStatusRotationDue(t *testing.T) {
	f := newFake(t)
	removeDue(t, f)
	out, _, err := accessRun(f, "store", "status")
	if err != nil || !strings.Contains(out, "rotation due: 2 secrets (ezc store status --due lists them)\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, _, err = accessRun(f, "store", "status", "--due")
	want := "common.enc.yaml/TOKEN  readable by removed sam since 2026-10-11\nenv.prod.enc.yaml/TOKEN  readable by removed sam since 2026-10-11\n"
	if err != nil || out != want {
		t.Fatalf("%v\n%q\nwant %q", err, out, want)
	}

	// Nothing due: no line.
	f = newFake(t)
	removeStore(t, f)
	if out, _, _ = accessRun(f, "store", "status"); strings.Contains(out, "rotation") {
		t.Fatalf("status says %q", out)
	}
	if out, _, err = accessRun(f, "store", "status", "--due"); err != nil || out != "" {
		t.Fatalf("--due: %v %q", err, out)
	}
}

func TestSetLeavesNothingWhenTheEntryCannotBeCleared(t *testing.T) {
	f := newFake(t)
	x := removeDue(t, f)
	due := x.file(recipients.DueFile)
	if err := os.Chmod(due, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(due, 0o644) })
	if f2, err := os.OpenFile(due, os.O_WRONLY, 0); err == nil {
		f2.Close()
		t.Skip("a read-only file is writable here")
	}
	head, before := x.git("rev-parse", "HEAD"), setupSnapshot(t, filepath.Join(x.store, ezcDir))
	f.stdin = "fresh-value-1\n"
	if err := f.run("set", "-g", "TOKEN", "--env", "prod"); err == nil {
		t.Fatal("set succeeded")
	}
	x.wantClean()
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("a commit was made")
	}
	if after := setupSnapshot(t, filepath.Join(x.store, ezcDir)); !mapsEqual(before, after) {
		t.Fatal("a file of the store changed")
	}
}

func TestStoreStatusSaysWhenRotationDueCannotBeRead(t *testing.T) {
	f := newFake(t)
	x := removeStore(t, f)
	write(t, x.file(recipients.DueFile), "{{ not yaml")
	x.git("add", "-A")
	x.git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "-m", "broken")
	x.push(x.store)
	out, _, err := accessRun(f, "store", "status")
	if err != nil || !strings.Contains(out, "rotation due: cannot read rotation-due.yaml\n") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUserRemoveOneSecret(t *testing.T) {
	f := newFake(t)
	x := removeStore(t, f)
	for _, file := range accessEncFiles[1:] {
		if err := os.Remove(x.file(file)); err != nil {
			t.Fatal(err)
		}
	}
	x.git("add", "-A")
	x.git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "-m", "one file")
	x.push(x.store)
	accessWant(t, f, "✓ remove sam\n✓ pushed to "+x.bare+"\n1 secret was readable by sam and needs a new value:\n  common.enc.yaml/TOKEN\nGive each a new value with ezc set -g NAME.\n", "user", "remove", "sam")
}
