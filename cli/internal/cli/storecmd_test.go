package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/proc"
)

const (
	storeCmdManifest = m1 + "optional:\n  API_KEY: {type: secret, desc: a}\n  DB_PASS: {type: secret, desc: d}\n"

	storeCmdMine   = "mine-marker-value"
	storeCmdTheirs = "theirs-marker-value"
)

// storeCmdRun runs `ezc store` with args and returns what it printed on stdout and stderr, and its error.
func storeCmdRun(f *fake, args ...string) (stdout, stderr string, err error) {
	f.out.Reset()
	f.errOut.Reset()
	f.gits = nil
	err = f.run(append([]string{"store"}, args...)...)
	return f.out.String(), f.errOut.String(), err
}

// storeCmdWant fails unless the command printed exactly stdout, nothing on stderr, and succeeded.
func storeCmdWant(t *testing.T, f *fake, stdout string, args ...string) {
	t.Helper()
	out, errOut, err := storeCmdRun(f, args...)
	if err != nil || out != stdout || errOut != "" {
		t.Fatalf("store %v: %v\nstdout %q\nwant   %q\nstderr %q", args, err, out, stdout, errOut)
	}
}

// storeCmdRefused fails unless the command is refused with want, exit 1, having printed nothing.
func storeCmdRefused(t *testing.T, f *fake, want string, args ...string) {
	t.Helper()
	out, errOut, err := storeCmdRun(f, args...)
	if err == nil || err.Error() != want {
		t.Fatalf("store %v: got %v\nwant %s", args, err, want)
	}
	storeWriteExitsOne(t, err)
	if out != "" || errOut != "" {
		t.Fatalf("store %v: stdout %q, stderr %q", args, out, errOut)
	}
}

// storeCmdStore is storeWriteStore whose manifest declares two secrets, API_KEY and DB_PASS.
func storeCmdStore(t *testing.T, f *fake) storeWriteFix {
	t.Helper()
	x := storeWriteStore(t, f)
	x.commit(x.store, "manifest.yaml", storeCmdManifest)
	return x.published()
}

// storeCmdClone is a second machine: a fake whose store is a new clone of x's remote.
func storeCmdClone(t *testing.T, x storeWriteFix) *fake {
	t.Helper()
	f := newFake(t)
	if err := x.g.Clone(x.bare, filepath.Join(f.env["EZC_HOME"], "store")); err != nil {
		t.Fatal(err)
	}
	return f
}

// storeCmdSet runs `ezc set -g` on f, with value on stdin when it is not "".
func storeCmdSet(t *testing.T, f *fake, value string, args ...string) {
	t.Helper()
	f.stdin = ""
	if value != "" {
		f.stdin = value + "\n"
	}
	if err := f.run(append([]string{"set", "-g"}, args...)...); err != nil {
		t.Fatalf("set -g %v: %v", args, err)
	}
}

// storeCmdDiverge sets API_KEY here and DB_PASS on a second machine, each in the common and the prod encrypted file, and pushes the second.
func storeCmdDiverge(t *testing.T, f *fake, x storeWriteFix) {
	t.Helper()
	their := storeCmdClone(t, x)
	storeCmdSet(t, f, storeCmdMine, "API_KEY", "--env", "prod")
	storeCmdSet(t, f, storeCmdMine, "API_KEY")
	storeCmdSet(t, their, storeCmdTheirs, "DB_PASS", "--env", "prod")
	storeCmdSet(t, their, storeCmdTheirs, "DB_PASS")
	storeCmdWant(t, their, "pushed 2 commits to origin\n", "push")
}

func storeCmdStatus(store, remote, state, fetched, requests string) string {
	return "store     " + store + "\nremote    " + remote + "\nstate     " + state + "\nfetched   " + fetched + "\nrequests  " + requests + "\n"
}

func TestStoreVerbs(t *testing.T) {
	f := newFake(t)
	wantUsage(t, f.run("store"), "say status, pull, push or audit")
	wantUsage(t, f.run("store", "sync"), "use status, pull, push or audit")
	for _, verb := range []string{"status", "pull", "push"} {
		wantUsage(t, f.run("store", verb, "extra"), "")
	}
}

func TestStatusStates(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "in sync", "just now", "none"), "status")

	x.commit(x.store, "env.a.yaml", "A: b\n")
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "1 commit ahead of origin", "just now", "none"), "status")
	x.commit(x.store, "env.b.yaml", "A: b\n")
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "2 commits ahead of origin", "just now", "none"), "status")

	x.theirs(1)
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "diverged from origin (2 ahead, 1 behind)", "just now", "none"), "status")

	write(t, filepath.Join(x.store, ezcDir, "env.new.yaml"), "A: b\n")
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "uncommitted changes", "just now", "none"), "status")

	f = newFake(t)
	x = remoteStore(t, f)
	x.theirs(1)
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "1 commit behind origin", "just now", "none"), "status")
	x.commit(x.other, "env.more.yaml", "A: b\n")
	x.push(x.other)
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "2 commits behind origin", "just now", "none"), "status")
}

func TestStatusRequests(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	for _, name := range []string{"sam", "kim", "bad name"} {
		x.commit(x.store, "requests/"+name+".yaml", "name: x\n")
	}
	x.push(x.store)
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "in sync", "just now", "kim, sam"), "status")
}

func TestStatusLocalOnly(t *testing.T) {
	f := newFake(t)
	x := localStore(t, f)
	storeCmdWant(t, f, storeCmdStatus(x.store, "none (local only)", "in sync", "n/a", "none"), "status")
	if len(fetches(f)) != 0 {
		t.Fatalf("fetches %v", fetches(f))
	}
}

func TestStatusForcesFetch(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(2)
	attemptedAt(t, f, noon)
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "2 commits behind origin", "just now", "none"), "status")
	if got := fetches(f); len(got) != 1 || got[0].Timeout != 10*time.Second {
		t.Fatalf("fetches %+v", got)
	}
}

func TestStatusOffline(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	if err := os.RemoveAll(x.bare); err != nil {
		t.Fatal(err)
	}
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "in sync", "never; origin could not be reached", "none"), "status")
	attemptedAt(t, f, noon)
	f.now = noon.Add(2 * time.Hour)
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "in sync", "2 hours ago; origin could not be reached", "none"), "status")
}

func TestStatusPlainFolder(t *testing.T) {
	f := newFake(t)
	plain := t.TempDir()
	f.env["EZC_STORE"] = plain
	write(t, filepath.Join(plain, ezcDir, "common.yaml"), "NAME: n\n")
	storeCmdWant(t, f, "store     "+plain+"\nstate     not a git repo, so it is not synced\n", "status")
	if len(fetches(f)) != 0 {
		t.Fatalf("fetches %v", fetches(f))
	}
	if _, err := os.Stat(statePath(f)); err == nil {
		t.Error("state.json was written")
	}
}

func TestStoreCommandsWithoutAStore(t *testing.T) {
	for _, verb := range []string{"status", "pull", "push"} {
		f := newFake(t)
		storeCmdRefused(t, f, "no store at "+filepath.Join(f.env["EZC_HOME"], "store")+". `ezc setup` creates one.", verb)
		f.env["EZC_STORE"] = filepath.Join(t.TempDir(), "nowhere")
		storeCmdRefused(t, f, "EZC_STORE is set to "+f.env["EZC_STORE"]+", which does not exist.", verb)
	}
}

// A store whose status cannot be read is not reported as in sync.
func TestStatusUnreadableIsAnError(t *testing.T) {
	f := newFake(t)
	x := storeWriteFix{remoteStore(t, f)}
	x.git("checkout", "--quiet", "--detach")
	out, _, err := storeCmdRun(f, "status")
	if err == nil || !strings.Contains(err.Error(), "not on a branch") || out != "" {
		t.Fatalf("got %v, stdout %q", err, out)
	}
}

func TestRemoteShownHoldsNoPassword(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:thinkjones/config.git":            "git@github.com:thinkjones/config.git",
		"https://github.com/thinkjones/config.git":        "https://github.com/thinkjones/config.git",
		"https://sam:t0ken@github.com/thinkjones/c.git":   "https://github.com/thinkjones/c.git",
		"https://t0ken@github.com/thinkjones/c.git":       "https://github.com/thinkjones/c.git",
		"ssh://git@github.com/thinkjones/c.git":           "ssh://git@github.com/thinkjones/c.git",
		"ssh://git:t0ken@github.com:22/thinkjones/c.git":  "ssh://git@github.com:22/thinkjones/c.git",
		"/srv/git/config.git":                             "/srv/git/config.git",
		"https://sam:t0ken@bad host/%zz":                  "a URL that cannot be shown",
		"":                                                "none (local only)",
		"https://sam:t0ken@github.com/c.git?token=t0ken2": "https://github.com/c.git",
	} {
		if got := remoteShown(remote); got != want {
			t.Errorf("%s: got %q, want %q", remote, got, want)
		}
	}
}

func TestPull(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	storeCmdWant(t, f, "already up to date\n", "pull")
	x.theirs(1)
	storeCmdWant(t, f, "pulled 1 commit from origin\n", "pull")
	for _, name := range []string{"env.x.yaml", "env.y.yaml", "env.z.yaml"} {
		x.commit(x.other, name, "A: b\n")
	}
	x.push(x.other)
	storeCmdWant(t, f, "pulled 3 commits from origin\n", "pull")
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "in sync", "just now", "none"), "status")
}

// Local commits that touch other files are replayed on the remote's.
func TestPullRebasesLocalCommits(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.commit(x.store, "env.mine.yaml", "A: b\n")
	x.theirs(2)
	storeCmdWant(t, f, "pulled 2 commits from origin\n", "pull")
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "1 commit ahead of origin", "just now", "none"), "status")
}

func TestPullWritesState(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	write(t, statePath(f), `{"other": {"n": 1}, "attempted": "2026-10-11T08:00:00Z"}`)
	f.now = noon.Add(time.Hour)
	storeCmdWant(t, f, "pulled 1 commit from origin\n", "pull")
	if got := readFile(t, statePath(f)); !strings.Contains(got, `"other"`) {
		t.Fatalf("state %s", got)
	}
	write(t, statePath(f), "{}")
	storeCmdWant(t, f, "already up to date\n", "pull")
	if state, want := readStateFile(t, f), f.now.Format(time.RFC3339); state["attempted"] != want || state["fetched"] != want {
		t.Fatalf("state %v, want both %s", state, want)
	}
}

func TestPullRefused(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	write(t, filepath.Join(x.store, ezcDir, "env.new.yaml"), "A: b\n")
	storeCmdRefused(t, f, "the store has uncommitted changes in "+x.store+". Commit or discard them with git, then pull.", "pull")

	f = newFake(t)
	x = remoteStore(t, f)
	if err := os.RemoveAll(x.bare); err != nil {
		t.Fatal(err)
	}
	storeCmdRefused(t, f, "origin could not be reached.", "pull")
	if _, err := os.Stat(statePath(f)); err == nil {
		t.Error("state.json was written by a pull that reached nothing")
	}

	f = newFake(t)
	localStore(t, f)
	storeCmdRefused(t, f, "the store has no remote, so there is nothing to pull. `ezc setup --new-github owner/repo` gives it one.", "pull")

	f = newFake(t)
	plain := t.TempDir()
	f.env["EZC_STORE"] = plain
	storeCmdRefused(t, f, "the store in "+plain+" is not a git repo, so there is nothing to pull.", "pull")
}

func TestPullReplacedNamesTheKeys(t *testing.T) {
	f := newFake(t)
	x := storeCmdStore(t, f)
	storeCmdDiverge(t, f, x)

	out, errOut, err := storeCmdRun(f, "pull")
	wantExit(t, err, 1)
	if out != "pulled 2 commits from origin\n" {
		t.Fatalf("stdout %q", out)
	}
	want := "! env.prod.enc.yaml was changed here and on the remote. The remote's version was kept; set these again:\n" +
		"  ezc set -g API_KEY --env prod\n" +
		"! common.enc.yaml was changed here and on the remote. The remote's version was kept; set these again:\n" +
		"  ezc set -g API_KEY\n"
	if errOut != want {
		t.Fatalf("stderr\n%s\nwant\n%s", errOut, want)
	}

	// The store holds the remote's files and is level with it.
	for _, file := range []string{"env.prod.enc.yaml", "common.enc.yaml"} {
		pairs, err := f.backend.Decrypt(x.file(file))
		if err != nil || len(pairs) != 1 || pairs[0].Name != "DB_PASS" || pairs[0].Value != storeCmdTheirs {
			t.Fatalf("%s holds %d names (%v)", file, len(pairs), err)
		}
	}
	x.wantClean()
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "in sync", "just now", "none"), "status")

	// What it says to run works.
	storeCmdSet(t, f, storeCmdMine, "API_KEY", "--env", "prod")
	storeCmdWant(t, f, "pushed 1 commit to origin\n", "push")
}

// A name removed here from a file the remote changed is to be removed again, not set.
func TestPullReplacedNamesWhatWasUnset(t *testing.T) {
	f := newFake(t)
	x := storeCmdStore(t, f)
	x.secret(f, "env.prod.enc.yaml", "API_KEY", "a1")
	x.secret(f, "env.prod.enc.yaml", "DB_PASS", "d1")
	x = x.published()
	their := storeCmdClone(t, x)
	if err := f.run("unset", "-g", "DB_PASS", "--env", "prod"); err != nil {
		t.Fatal(err)
	}
	storeCmdSet(t, their, storeCmdTheirs, "DB_PASS", "--env", "prod")
	storeCmdWant(t, their, "pushed 1 commit to origin\n", "push")

	_, errOut, err := storeCmdRun(f, "pull")
	wantExit(t, err, 1)
	want := "! env.prod.enc.yaml was changed here and on the remote. The remote's version was kept; run these again:\n" +
		"  ezc unset -g DB_PASS --env prod\n"
	if errOut != want {
		t.Fatalf("stderr\n%s\nwant\n%s", errOut, want)
	}
}

func TestPullOutputHoldsNoValue(t *testing.T) {
	f := newFake(t)
	x := storeCmdStore(t, f)
	storeCmdDiverge(t, f, x)
	out, errOut, err := storeCmdRun(f, "pull")
	wantExit(t, err, 1)
	all := out + errOut + err.Error()
	for _, c := range f.gits {
		all += strings.Join(c.Args, " ") + strings.Join(c.Env, " ")
	}
	for _, value := range []string{storeCmdMine, storeCmdTheirs, "FAKE["} {
		if strings.Contains(all, value) {
			t.Fatalf("the output or a git command holds %q:\n%s%s", value, out, errOut)
		}
	}
	if !strings.Contains(errOut, "API_KEY") || strings.Contains(errOut, "DB_PASS") {
		t.Fatalf("stderr names the wrong keys:\n%s", errOut)
	}
}

// A replaced file whose names cannot be read is named, without a list.
func TestPullReplacedUnreadableFile(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.commit(x.store, "env.prod.enc.yaml", "A: [\n")
	x.commit(x.other, "env.prod.enc.yaml", "B: FAKE[eA==]\n")
	// A file ezc has no command for: not an environment's, or not in the store's .ezc.
	x.commit(x.store, "notes.enc.yaml", "A: FAKE[eA==]\n")
	x.commit(x.other, "notes.enc.yaml", "B: FAKE[eA==]\n")
	x.commit(x.store, "../common.enc.yaml", "A: FAKE[eA==]\n")
	x.commit(x.other, "../common.enc.yaml", "B: FAKE[eA==]\n")
	x.push(x.other)

	out, errOut, err := storeCmdRun(f, "pull")
	wantExit(t, err, 1)
	want := "! env.prod.enc.yaml was changed here and on the remote. The remote's version was kept; its local changes must be set again.\n" +
		"! notes.enc.yaml was changed here and on the remote. The remote's version was kept; its local changes must be set again.\n" +
		"! common.enc.yaml was changed here and on the remote. The remote's version was kept; its local changes must be set again.\n"
	if out != "pulled 3 commits from origin\n" || errOut != want {
		t.Fatalf("stdout %q, stderr\n%s\nwant\n%s", out, errOut, want)
	}
}

// Two machines that each declare a new name both add to the end of manifest.yaml: a conflict in a plaintext file stops the pull.
func TestPullConflictInThePlainManifest(t *testing.T) {
	f := newFake(t)
	x := storeCmdStore(t, f)
	their := storeCmdClone(t, x)
	storeCmdSet(t, f, "", "MINE", "v", "--env", "a", "--type", "str", "--desc", "d")
	storeCmdSet(t, their, "", "THEIRS", "v", "--env", "b", "--type", "str", "--desc", "d")
	storeCmdWant(t, their, "pushed 1 commit to origin\n", "push")

	head, manifest := x.git("rev-parse", "HEAD"), readFile(t, x.file("manifest.yaml"))
	storeCmdRefused(t, f, ".ezc/manifest.yaml was changed here and on the remote in the same place, so nothing was pulled. Resolve it in "+x.store+" with git (git pull --rebase).", "pull")
	if x.git("rev-parse", "HEAD") != head || readFile(t, x.file("manifest.yaml")) != manifest {
		t.Fatal("the store was changed")
	}
	x.wantClean()
	if _, err := os.Stat(filepath.Join(x.store, ".git", "rebase-merge")); err == nil {
		t.Fatal("a rebase was left part way")
	}
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "diverged from origin (1 ahead, 1 behind)", "just now", "none"), "status")
}

func TestPush(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	storeCmdWant(t, f, "nothing to push\n", "push")
	x.commit(x.store, "env.a.yaml", "A: b\n")
	storeCmdWant(t, f, "pushed 1 commit to origin\n", "push")
	x.commit(x.store, "env.b.yaml", "A: b\n")
	x.commit(x.store, "env.c.yaml", "A: b\n")
	f.now = noon.Add(time.Hour)
	storeCmdWant(t, f, "pushed 2 commits to origin\n", "push")
	if state, want := readStateFile(t, f), f.now.Format(time.RFC3339); state["attempted"] != want || state["fetched"] != want {
		t.Fatalf("state %v, want both %s", state, want)
	}
	storeCmdWant(t, f, storeCmdStatus(x.store, x.bare, "in sync", "just now", "none"), "status")

	// The other clone receives them.
	if res, err := x.g.Pull(x.other, 0, func(string) bool { return false }); err != nil || res.Commits != 3 {
		t.Fatalf("the other clone pulled %d (%v)", res.Commits, err)
	}
}

// The first push of a store whose remote is empty.
func TestPushToAnEmptyRemote(t *testing.T) {
	f := newFake(t)
	x := localStore(t, f)
	x.bare = filepath.Join(t.TempDir(), "remote.git")
	if _, stderr, err := x.g.Run(proc.Command{Name: "git", Args: []string{"init", "--bare", "--initial-branch=main", "--", x.bare}}); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if err := x.g.SetRemote(x.store, x.bare); err != nil {
		t.Fatal(err)
	}
	storeCmdWant(t, f, "pushed 1 commit to origin\n", "push")
	storeCmdWant(t, f, "nothing to push\n", "push")
}

func TestPushRefused(t *testing.T) {
	const behind = "the store is behind origin. Run `ezc store pull` first."

	// Behind, though the state file says the remote was just checked: push looks for itself.
	f := newFake(t)
	x := storeWriteFix{remoteStore(t, f)}
	x.theirs(1)
	attemptedAt(t, f, noon)
	storeCmdRefused(t, f, behind, "push")

	x.commit(x.store, "env.mine.yaml", "A: b\n")
	head := x.git("rev-parse", "origin/main")
	storeCmdRefused(t, f, behind, "push")
	if x.git("rev-parse", "origin/main") != head {
		t.Fatal("something was pushed")
	}

	f = newFake(t)
	x = storeWriteFix{remoteStore(t, f)}
	x.commit(x.store, "env.mine.yaml", "A: b\n")
	write(t, x.file("env.new.yaml"), "A: b\n")
	storeCmdRefused(t, f, "the store has uncommitted changes in "+x.store+". Commit or discard them with git, then push.", "push")
	if got := x.git("rev-list", "--count", "origin/main..HEAD"); got != "1" {
		t.Fatalf("%s commits unpushed, want 1", got)
	}

	f = newFake(t)
	localStore(t, f)
	storeCmdRefused(t, f, "the store has no remote, so there is nothing to push to. `ezc setup --new-github owner/repo` gives it one.", "push")

	f = newFake(t)
	x = storeWriteFix{remoteStore(t, f)}
	x.commit(x.store, "env.mine.yaml", "A: b\n")
	if err := os.RemoveAll(x.bare); err != nil {
		t.Fatal(err)
	}
	storeCmdRefused(t, f, "origin could not be reached.", "push")

	f = newFake(t)
	plain := t.TempDir()
	f.env["EZC_STORE"] = plain
	storeCmdRefused(t, f, "the store in "+plain+" is not a git repo, so there is nothing to push.", "push")
}

// None of the three prints the indicator line: what it would say is the command's own answer.
func TestStoreCommandsPrintNoIndicator(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.commit(x.store, "requests/sam.yaml", "name: sam\n")
	x.commit(x.store, "env.mine.yaml", "A: b\n")
	x.theirs(1)
	for _, verb := range []string{"status", "pull", "push"} {
		_, errOut, err := storeCmdRun(f, verb)
		if errOut != "" || err != nil {
			t.Errorf("%s: stderr %q, %v", verb, errOut, err)
		}
	}
}

// A push the remote refuses after a good fetch says why in git's words, without what the remote's URL carries after its path.
func TestPushFailureHoldsNoToken(t *testing.T) {
	const token = "QSECRET-marker"
	f := newFake(t)
	x := storeWriteFix{localStore(t, f)}
	// A folder whose name ends like a query, so that the URL with a token in it is one git can reach.
	x.bare = filepath.Join(t.TempDir(), "remote.git")
	dir := x.bare + "?token=" + token
	if _, stderr, err := x.g.Run(proc.Command{Name: "git", Args: []string{"init", "--bare", "--initial-branch=main", "--", dir}}); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := x.g.SetRemote(x.store, "file://"+dir); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := storeCmdRun(f, "push")
	want := "the push to origin failed: failed to push some refs to 'file://" + x.bare + "'."
	if err == nil || err.Error() != want || out != "" || errOut != "" {
		t.Fatalf("got %v\nwant %s\nstdout %q, stderr %q", err, want, out, errOut)
	}
	storeWriteExitsOne(t, err)
	if len(fetches(f)) != 1 {
		t.Fatalf("fetches %v", fetches(f))
	}
	out, _, err = storeCmdRun(f, "status")
	if err != nil || strings.Contains(out, token) || !strings.Contains(out, "remote    file://"+x.bare+"\n") {
		t.Fatalf("status: %v\n%s", err, out)
	}
}
