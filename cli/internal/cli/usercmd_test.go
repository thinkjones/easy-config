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
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

// The fake keys of these tests: this machine's (gene), the one to add (sam) and one nobody registered.
const (
	accessGenePrivate = "FAKE-KEY-aa01\n"
	accessGene        = "fake1aa01"
	accessSam         = "fake1bb02"
	accessStray       = "fake1cc03"

	accessNoKeyFile    = "An access change needs a key file, and this machine uses none. Run this on a machine that keeps its key in a file."
	accessBehind       = "The store is behind its remote. Run ezc store pull, then run this again."
	accessNotRecipient = "This machine's key is not a recipient of the store, so it cannot re-wrap it. Run ezc join."
)

// accessEncFiles are the encrypted files of the store these tests build.
var accessEncFiles = []string{"common.enc.yaml", "env.prod.enc.yaml"}

func accessNoRegistry(store string) string {
	return store + ` has no recipients.yaml, so ezc does not know who can read it. Write one (README, "Access"), then run ezc store rewrap.`
}

// accessEntry is a user's entry as setup writes it.
func accessEntry(key string) recipients.Recipient {
	return recipients.Recipient{Key: key, Kind: recipients.KindUser, Added: "2026-10-01", AddedBy: "gene"}
}

// accessMachine gives the fake gene's key file and a config that names gene.
func accessMachine(t *testing.T, f *fake) {
	t.Helper()
	write(t, setupKeyFile(f), accessGenePrivate)
	if err := config.Save(setupConfig(f), config.Config{User: "gene"}); err != nil {
		t.Fatal(err)
	}
}

// accessFiles writes a store's .ezc in dir: the registry, a policy for policyKeys and two encrypted files wrapped for them.
func accessFiles(t *testing.T, f *fake, dir string, reg map[string]recipients.Recipient, policyKeys ...string) {
	t.Helper()
	write(t, filepath.Join(dir, manifestName), mToken)
	if reg != nil {
		if err := (recipients.Registry{Recipients: reg}).Save(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.backend.WritePolicy(dir, policyKeys); err != nil {
		t.Fatal(err)
	}
	for _, file := range accessEncFiles {
		if err := f.backend.Set(filepath.Join(dir, file), "TOKEN", "s3cr3t-marker-value"); err != nil {
			t.Fatal(err)
		}
	}
}

// accessStoreWith is gene's machine and a store that is a git repo with a remote and a second clone, holding reg (none when nil).
func accessStoreWith(t *testing.T, f *fake, reg map[string]recipients.Recipient, policyKeys ...string) storeWriteFix {
	t.Helper()
	accessMachine(t, f)
	x := storeWriteFix{localStore(t, f)}
	accessFiles(t, f, filepath.Join(x.store, ezcDir), reg, policyKeys...)
	if done, err := x.g.Commit(x.store, "the store", gitstore.Author{}, ezcDir); err != nil || !done {
		t.Fatalf("commit: done %v, %v", done, err)
	}
	x.bare = gittest.Bare(t)
	if err := x.g.SetRemote(x.store, x.bare); err != nil {
		t.Fatal(err)
	}
	return x.published()
}

// accessStore is accessStoreWith for a store only gene reads.
func accessStore(t *testing.T, f *fake) storeWriteFix {
	t.Helper()
	return accessStoreWith(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
}

// accessRun runs a command and returns what it printed and its error.
func accessRun(f *fake, args ...string) (stdout, stderr string, err error) {
	f.out.Reset()
	f.errOut.Reset()
	f.gits = nil
	err = f.run(args...)
	return f.out.String(), f.errOut.String(), err
}

// accessWant fails unless the command succeeded, printing exactly stdout and nothing on stderr.
func accessWant(t *testing.T, f *fake, stdout string, args ...string) {
	t.Helper()
	out, errOut, err := accessRun(f, args...)
	if err != nil || out != stdout || errOut != "" {
		t.Fatalf("%v: %v\nstdout %q\nwant   %q\nstderr %q", args, err, out, stdout, errOut)
	}
}

// accessReaders fails unless each encrypted file in dir is wrapped for exactly keys.
func accessReaders(t *testing.T, f *fake, dir string, keys ...string) {
	t.Helper()
	slices.Sort(keys)
	for _, file := range accessEncFiles {
		got, err := f.backend.Recipients(filepath.Join(dir, file))
		if err != nil || !slices.Equal(got, keys) {
			t.Fatalf("%s is for %q (%v), want %q", file, got, err, keys)
		}
	}
}

func accessRegistry(t *testing.T, dir string) recipients.Registry {
	t.Helper()
	reg, found, err := recipients.Load(dir)
	if err != nil || !found {
		t.Fatalf("registry: found %v, %v", found, err)
	}
	return reg
}

// accessLevel fails unless the remote's branch is at the store's last commit.
func accessLevel(t *testing.T, x storeWriteFix) {
	t.Helper()
	stdout, stderr, err := x.g.Run(proc.Command{Dir: x.bare, Name: "git", Args: []string{"rev-parse", "refs/heads/main"}})
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if got, want := strings.TrimSpace(string(stdout)), x.git("rev-parse", "HEAD"); got != want {
		t.Fatalf("the remote is at %s, the store at %s", got, want)
	}
}

// accessUntouched runs a command that must be refused with want, exit 1, having printed nothing and changed nothing in the store.
func accessUntouched(t *testing.T, f *fake, x storeWriteFix, want string, args ...string) {
	t.Helper()
	head, status := x.git("rev-parse", "HEAD"), x.git("status", "--porcelain", "--untracked-files=all")
	before := setupSnapshot(t, filepath.Join(x.store, ezcDir))
	out, _, err := accessRun(f, args...)
	if err == nil || err.Error() != want {
		t.Fatalf("%v: got %v\nwant %s", args, err, want)
	}
	storeWriteExitsOne(t, err)
	if out != "" {
		t.Errorf("%v: stdout %q", args, out)
	}
	if got := x.git("rev-parse", "HEAD"); got != head {
		t.Errorf("%v: a commit was made", args)
	}
	if got := x.git("status", "--porcelain", "--untracked-files=all"); got != status {
		t.Errorf("%v: git status is now %q, was %q", args, got, status)
	}
	if after := setupSnapshot(t, filepath.Join(x.store, ezcDir)); !mapsEqual(before, after) {
		t.Errorf("%v: a file of the store changed", args)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestUserAdd(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	accessWant(t, f, "✓ add sam\n✓ pushed to "+x.bare+"\n", "user", "add", "sam", accessSam)

	if got := x.git("log", "-1", "--format=%s"); got != "add sam" {
		t.Fatalf("the last commit is %q", got)
	}
	want := ".ezc/.fakepolicy\n.ezc/common.enc.yaml\n.ezc/env.prod.enc.yaml\n.ezc/recipients.yaml"
	if got := x.git("show", "--name-only", "--format=", "HEAD"); got != want {
		t.Fatalf("the commit holds %q", got)
	}
	x.wantClean()
	accessLevel(t, x)
	if got := x.git("rev-list", "--count", "origin/main..HEAD"); got != "0" {
		t.Fatalf("%s commits not pushed", got)
	}

	dir := filepath.Join(x.store, ezcDir)
	accessReaders(t, f, dir, accessGene, accessSam)
	reg := accessRegistry(t, dir)
	if got, want := reg.Recipients["sam"], (recipients.Recipient{Key: accessSam, Kind: "user", Added: "2026-10-11", AddedBy: "gene"}); got.Key != want.Key || got.Kind != want.Kind || got.Owner != "" || got.Added != want.Added || got.AddedBy != want.AddedBy {
		t.Fatalf("sam is %+v", got)
	}
	if len(reg.Recipients) != 2 || reg.Recipients["gene"].Key != accessGene {
		t.Fatalf("the registry is %+v", reg)
	}
	if strings.Contains(x.git("log", "-p"), "s3cr3t-marker-value") {
		t.Fatal("the history holds a value")
	}
}

func TestUserAddMachine(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	accessUntouched(t, f, x, "the owner of a machine must be a user among the recipients.", "user", "add", "laptop", accessSam, "--kind", "machine", "--owner", "nobody")
	accessWant(t, f, "✓ add laptop\n✓ pushed to "+x.bare+"\n", "user", "add", "laptop", accessSam, "--kind", "machine", "--owner", "gene")
	got := accessRegistry(t, filepath.Join(x.store, ezcDir)).Recipients["laptop"]
	if got.Kind != "machine" || got.Owner != "gene" || got.Key != accessSam {
		t.Fatalf("laptop is %+v", got)
	}
}

func TestUserAddUsage(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	head := x.git("rev-parse", "HEAD")
	for _, c := range []struct {
		want string
		args []string
	}{
		{"ezc user add: give a name and a public key.", []string{"sam"}},
		{"ezc user add: give a name and a public key.", []string{"sam", accessSam, "more"}},
		{"ezc user add: --kind machine needs --owner, the user whose machine it is.", []string{"sam", accessSam, "--kind", "machine"}},
		{"ezc user add: --owner goes with --kind machine only.", []string{"sam", accessSam, "--owner", "gene"}},
		{"ezc user add: add a bot with ezc bot add.", []string{"sam", accessSam, "--kind", "bot"}},
		{"ezc user add: --kind is user or machine.", []string{"sam", accessSam, "--kind", "robot"}},
		{"ezc user add: the name is lower case letters, digits and -, starting with a letter or a digit.", []string{"Sam", accessSam}},
	} {
		out, _, err := accessRun(f, append([]string{"user", "add"}, c.args...)...)
		wantUsage(t, err, c.want)
		if out != "" || len(f.gits) != 0 {
			t.Errorf("%v: stdout %q, git ran %d times", c.args, out, len(f.gits))
		}
	}
	wantUsage(t, f.run("user"), "say list, add, remove, requests, approve or deny")
	wantUsage(t, f.run("user", "ad"), "use list, add, remove, requests, approve or deny")
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("a commit was made")
	}
	x.wantClean()
}

func TestUserAddRefused(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	for _, c := range []struct {
		want string
		args []string
	}{
		{"gene is already a recipient.", []string{"gene", accessSam}},
		{"that key is already registered to gene.", []string{"sam", accessGene}},
		{"the second argument is not a valid public key.", []string{"sam", "not-a-key"}},
		{"the second argument is not a valid public key.", []string{"sam", strings.TrimSpace(accessGenePrivate)}},
	} {
		accessUntouched(t, f, x, c.want, append([]string{"user", "add"}, c.args...)...)
	}
}

// Every refusal of openAccess comes before a byte is written.
func TestAccessRefusals(t *testing.T) {
	writes := [][]string{{"user", "add", "sam", accessSam}, {"store", "rewrap"}}
	for _, c := range []struct {
		name  string
		setup func(f *fake, x storeWriteFix) string // arranges the store and returns the refusal
	}{
		{"behind", func(f *fake, x storeWriteFix) string {
			x.theirs(1)
			// A fetch a minute ago: an access change fetches again all the same.
			attemptedAt(t, f, noon.Add(-time.Minute))
			return accessBehind
		}},
		{"diverged", func(f *fake, x storeWriteFix) string {
			x.commit(x.store, "common.yaml", "NAME: mine\n")
			x.theirs(1)
			return accessBehind
		}},
		{"dirty registry", func(f *fake, x storeWriteFix) string {
			write(t, x.file(recipients.File), readFile(t, x.file(recipients.File))+"# by hand\n")
			return storeWriteDirty(recipients.File, x)
		}},
		{"dirty policy", func(f *fake, x storeWriteFix) string {
			write(t, x.file(".fakepolicy"), accessGene+"\n"+accessStray+"\n")
			return storeWriteDirty(".fakepolicy", x)
		}},
		{"dirty rotation-due", func(f *fake, x storeWriteFix) string {
			write(t, x.file(recipients.DueFile), "common.enc.yaml/TOKEN: {removed: zed, on: 2026-10-01}\n")
			return storeWriteDirty(recipients.DueFile, x)
		}},
		{"dirty encrypted file", func(f *fake, x storeWriteFix) string {
			if err := f.backend.Set(x.file("env.prod.enc.yaml"), "OTHER", "v"); err != nil {
				t.Fatal(err)
			}
			return storeWriteDirty("env.prod.enc.yaml", x)
		}},
		{"deleted encrypted file", func(f *fake, x storeWriteFix) string {
			if err := os.Remove(x.file("env.prod.enc.yaml")); err != nil {
				t.Fatal(err)
			}
			return storeWriteDirty("env.prod.enc.yaml", x)
		}},
		{"new encrypted file", func(f *fake, x storeWriteFix) string {
			if err := f.backend.Set(x.file("env.dev.enc.yaml"), "TOKEN", "v"); err != nil {
				t.Fatal(err)
			}
			return storeWriteDirty("env.dev.enc.yaml", x)
		}},
		{"detached HEAD", func(f *fake, x storeWriteFix) string {
			x.git("checkout", "--quiet", "--detach")
			return "the store in " + x.store + " is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again."
		}},
		{"no key file", func(f *fake, x storeWriteFix) string {
			f.env["HOME"] = ""
			return accessNoKeyFile
		}},
		{"a key file that is not there", func(f *fake, x storeWriteFix) string {
			if err := os.Remove(setupKeyFile(f)); err != nil {
				t.Fatal(err)
			}
			return "no key was found in " + setupKeyFile(f) + ", so this machine cannot re-wrap the store. Run ezc doctor."
		}},
		{"not a recipient", func(f *fake, x storeWriteFix) string {
			write(t, setupKeyFile(f), "FAKE-KEY-dd04\n")
			return accessNotRecipient
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			x := accessStore(t, f)
			want := c.setup(f, x)
			for _, args := range writes {
				accessUntouched(t, f, x, want, args...)
			}
		})
	}
}

func TestAccessNeedsARegistry(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, nil, accessGene)
	for _, args := range [][]string{{"user", "add", "sam", accessSam}, {"store", "rewrap"}, {"user", "list"}} {
		accessUntouched(t, f, x, accessNoRegistry(x.store), args...)
	}
	if _, err := os.Stat(x.file(recipients.File)); err == nil {
		t.Fatal("a registry was written")
	}
}

func TestAccessNeedsAStore(t *testing.T) {
	f := newFake(t)
	accessMachine(t, f)
	want := "no store at " + setupStore(f) + ". `ezc setup` creates one."
	for _, args := range [][]string{{"user", "add", "sam", accessSam}, {"store", "rewrap"}, {"user", "list"}} {
		if _, _, err := accessRun(f, args...); err == nil || err.Error() != want {
			t.Errorf("%v: got %v", args, err)
		}
	}
}

// A file that cannot be re-wrapped: the files before it, the registry and the policy go back to the last commit.
func TestUserAddRestoresWhenAFileFails(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	f.backend.RewrapFail = "env.prod.enc.yaml"
	head := x.git("rev-parse", "HEAD")
	out, _, err := accessRun(f, "user", "add", "sam", accessSam)
	if err == nil || !strings.Contains(err.Error(), "env.prod.enc.yaml") || !strings.HasSuffix(err.Error(), "Nothing was changed.") {
		t.Fatalf("got %v", err)
	}
	storeWriteExitsOne(t, err)
	if out != "" {
		t.Fatalf("stdout %q", out)
	}
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("a commit was made")
	}
	x.wantClean()
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene)
	accessLevel(t, x)
}

// Decision 9: the commit stays when the push fails.
func TestUserAddPushFails(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	if err := os.RemoveAll(x.bare); err != nil {
		t.Fatal(err)
	}
	out, _, err := accessRun(f, "user", "add", "sam", accessSam)
	if err == nil || !strings.HasPrefix(err.Error(), "Committed, but the push failed (") || !strings.HasSuffix(err.Error(), "). Run ezc store push.") {
		t.Fatalf("got %v", err)
	}
	var unpushed *unpushedError
	var unchanged *unchangedError
	if !errors.As(err, &unpushed) || unpushed.Unwrap() == nil || errors.As(err, &unchanged) {
		t.Fatalf("want an *unpushedError and no *unchangedError, got %T", err)
	}
	storeWriteExitsOne(t, err)
	if out != "✓ add sam\n" {
		t.Fatalf("stdout %q", out)
	}
	if got := x.subjects(); !slices.Equal(got, []string{"add sam"}) {
		t.Fatalf("commits %q", got)
	}
	x.wantClean()
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene, accessSam)
}

func TestUserAddLocalOnlyStore(t *testing.T) {
	f := newFake(t)
	accessMachine(t, f)
	x := storeWriteFix{localStore(t, f)}
	accessFiles(t, f, filepath.Join(x.store, ezcDir), map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
	if _, err := x.g.Commit(x.store, "the store", gitstore.Author{}, ezcDir); err != nil {
		t.Fatal(err)
	}
	accessWant(t, f, "✓ add sam\n", "user", "add", "sam", accessSam)
	if got := x.git("log", "-1", "--format=%s"); got != "add sam" {
		t.Fatalf("the last commit is %q", got)
	}
	x.wantClean()
}

// A store that is a plain folder: no commit, no push, and a failure still puts every file back.
func TestUserAddPlainFolder(t *testing.T) {
	f := newFake(t)
	accessMachine(t, f)
	dir := filepath.Join(setupStore(f), ezcDir)
	accessFiles(t, f, dir, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
	before := setupSnapshot(t, dir)

	f.backend.RewrapFail = "env.prod.enc.yaml"
	if _, _, err := accessRun(f, "user", "add", "sam", accessSam); err == nil {
		t.Fatal("want an error")
	}
	if !mapsEqual(before, setupSnapshot(t, dir)) {
		t.Fatal("the failure left a file changed")
	}

	f.backend.RewrapFail = ""
	accessWant(t, f, "✓ add sam\n", "user", "add", "sam", accessSam)
	accessReaders(t, f, dir, accessGene, accessSam)
	if n := len(storeWriteCommitsRun(f)); n != 0 {
		t.Fatalf("git commit ran %d times", n)
	}
}

func TestUserAddInEzcStore(t *testing.T) {
	f := newFake(t)
	accessMachine(t, f)
	elsewhere := t.TempDir()
	f.env["EZC_STORE"] = elsewhere
	dir := filepath.Join(elsewhere, ezcDir)
	accessFiles(t, f, dir, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
	accessWant(t, f, "✓ add sam\n", "user", "add", "sam", accessSam)
	accessReaders(t, f, dir, accessGene, accessSam)
	if _, err := os.Stat(setupStore(f)); err == nil {
		t.Fatal("the default store was made")
	}
}

func TestUserList(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	accessWant(t, f, "NAME  KIND  OWNER  ADDED       ADDED BY\ngene  user  -      2026-10-01  gene      (this machine)\n", "user", "list")

	accessWant(t, f, "✓ add sam\n✓ pushed to "+x.bare+"\n", "user", "add", "sam", accessSam)
	accessWant(t, f, "✓ add laptop\n✓ pushed to "+x.bare+"\n", "user", "add", "laptop", accessStray, "--kind", "machine", "--owner", "sam")
	want := "NAME    KIND     OWNER  ADDED       ADDED BY\n" +
		"gene    user     -      2026-10-01  gene      (this machine)\n" +
		"laptop  machine  sam    2026-10-11  gene\n" +
		"sam     user     -      2026-10-11  gene\n"
	accessWant(t, f, want, "user", "list")

	// It needs no key: not this machine's, and not the tool's.
	if err := os.Remove(setupKeyFile(f)); err != nil {
		t.Fatal(err)
	}
	f.backend.Locked = true
	accessWant(t, f, strings.Replace(want, "      (this machine)", "", 1), "user", "list")
	wantUsage(t, f.run("user", "list", "sam"), `ezc user list: unexpected argument "sam"`)
}

// The ciphertext is the reality: a file whose recipients are not the registry's is named, and store rewrap fixes it.
func TestUserListDifferencesAndRewrap(t *testing.T) {
	f := newFake(t)
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	x := accessStoreWith(t, f, reg, accessGene, accessStray)
	head := x.git("rev-parse", "HEAD")

	table := "NAME  KIND  OWNER  ADDED       ADDED BY\ngene  user  -      2026-10-01  gene      (this machine)\nsam   user  -      2026-10-01  gene\n"
	out, errOut, err := accessRun(f, "user", "list")
	wantExit(t, err, 1)
	want := table +
		"✗ common.enc.yaml: not encrypted for sam; encrypted for " + accessStray + " who are not in the registry\n" +
		"✗ env.prod.enc.yaml: not encrypted for sam; encrypted for " + accessStray + " who are not in the registry\n" +
		"Run ezc store rewrap.\n"
	if out != want || errOut != "" {
		t.Fatalf("stdout %q\nwant   %q\nstderr %q", out, want, errOut)
	}
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("user list made a commit")
	}

	accessWant(t, f, "✓ rewrap\n✓ pushed to "+x.bare+"\n", "store", "rewrap")
	if got := x.git("log", "-1", "--format=%s"); got != "rewrap" {
		t.Fatalf("the last commit is %q", got)
	}
	if got, want := x.git("show", "--name-only", "--format=", "HEAD"), ".ezc/.fakepolicy\n.ezc/common.enc.yaml\n.ezc/env.prod.enc.yaml"; got != want {
		t.Fatalf("the commit holds %q", got)
	}
	x.wantClean()
	accessLevel(t, x)
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene, accessSam)
	accessWant(t, f, table, "user", "list")

	head = x.git("rev-parse", "HEAD")
	accessWant(t, f, "✓ every file is encrypted for the registry\n", "store", "rewrap")
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("the second rewrap made a commit")
	}
	x.wantClean()
	wantUsage(t, f.run("store", "rewrap", "now"), `ezc store rewrap: unexpected argument "now"`)
}

func TestUserListOneSidedDifferences(t *testing.T) {
	f := newFake(t)
	accessStoreWith(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam), "zed": accessEntry("fake1ee05")}, accessGene)
	out, _, err := accessRun(f, "user", "list")
	wantExit(t, err, 1)
	if !strings.Contains(out, "✗ common.enc.yaml: not encrypted for sam, zed\n") {
		t.Fatalf("stdout %q", out)
	}

	f = newFake(t)
	accessStoreWith(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene, accessStray, accessSam)
	out, _, err = accessRun(f, "user", "list")
	wantExit(t, err, 1)
	if !strings.Contains(out, "✗ common.enc.yaml: encrypted for "+accessSam+", "+accessStray+" who are not in the registry\n") {
		t.Fatalf("stdout %q", out)
	}
}

// accessOpen is openAccess for a write on the fake, which must succeed.
func accessOpen(t *testing.T, f *fake) *access {
	t.Helper()
	a, err := openAccess(f.sys(), true)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOpenAccess(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	a := accessOpen(t, f)
	if a.dir != filepath.Join(x.store, ezcDir) || a.me != "gene" || a.myKey != accessGene || a.myName != "gene" || !slices.Equal(a.enc, accessEncFiles) || !a.sync.Git || a.b == nil || len(a.reg.Recipients) != 1 {
		t.Fatalf("got %+v", a)
	}
	if n := len(fetches(f)); n != 1 {
		t.Fatalf("%d fetches", n)
	}
}

// The parts of a change the later commands use: rotation-due entries, a step before the commit, and files removed in it.
func TestAccessApplyChange(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	x.commit(x.store, "requests/sam.yaml", "name: sam\n")
	x.push(x.store)
	a := accessOpen(t, f)
	next, err := a.reg.With("sam", accessEntry(accessSam))
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	c := change{
		next:    next,
		message: "approve sam",
		due:     func(d recipients.Due) { d.Add("common.enc.yaml", "TOKEN", "zed", "2026-10-11") },
		extra: func() error {
			ran = true
			// The sequence is done when extra runs.
			accessReaders(t, f, a.dir, accessGene, accessSam)
			return nil
		},
		remove: []string{"requests/sam.yaml"},
	}
	if err := a.apply(c); err != nil || !ran {
		t.Fatalf("ran %v, %v", ran, err)
	}
	want := ".ezc/.fakepolicy\n.ezc/common.enc.yaml\n.ezc/env.prod.enc.yaml\n.ezc/recipients.yaml\n.ezc/requests/sam.yaml\n.ezc/rotation-due.yaml"
	if got := x.git("show", "--name-only", "--format=", "HEAD"); got != want {
		t.Fatalf("the commit holds %q", got)
	}
	if got := x.git("log", "-1", "--format=%s|%an"); got != "approve sam|gene" {
		t.Fatalf("the commit is %q", got)
	}
	x.wantClean()
	accessLevel(t, x)
	if _, err := os.Stat(x.file("requests/sam.yaml")); err == nil {
		t.Fatal("the request file is still there")
	}
	if got := readFile(t, x.file(recipients.DueFile)); got != "common.enc.yaml/TOKEN: {removed: zed, on: 2026-10-11}\n" {
		t.Fatalf("rotation-due.yaml is %q", got)
	}
	if got := f.out.String(); got != "✓ approve sam\n✓ pushed to "+x.bare+"\n" {
		t.Fatalf("stdout %q", got)
	}
}

// A step that fails before the commit, as a delivery does, leaves the store at its last commit.
func TestAccessApplyRestoresWhenExtraFails(t *testing.T) {
	for _, git := range []bool{true, false} {
		f := newFake(t)
		dir := filepath.Join(setupStore(f), ezcDir)
		var x storeWriteFix
		if git {
			x = accessStore(t, f)
			x.commit(x.store, "requests/sam.yaml", "name: sam\n")
			x.push(x.store)
		} else {
			accessMachine(t, f)
			accessFiles(t, f, dir, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
			write(t, filepath.Join(dir, "requests", "sam.yaml"), "name: sam\n")
		}
		before := setupSnapshot(t, dir)
		a := accessOpen(t, f)
		next, err := a.reg.With("sam", accessEntry(accessSam))
		if err != nil {
			t.Fatal(err)
		}
		err = a.apply(change{
			next:    next,
			message: "add bot sam",
			due:     func(d recipients.Due) { d.Add("common.enc.yaml", "TOKEN", "zed", "2026-10-11") },
			extra:   func() error { return errors.New("the delivery failed") },
			remove:  []string{"requests/sam.yaml"},
		})
		if err == nil || err.Error() != "the delivery failed. Nothing was changed." {
			t.Fatalf("git %v: got %v", git, err)
		}
		if !mapsEqual(before, setupSnapshot(t, dir)) {
			t.Fatalf("git %v: a file was left changed", git)
		}
		if f.out.Len() != 0 {
			t.Fatalf("git %v: stdout %q", git, f.out.String())
		}
		if git {
			x.wantClean()
			if n := len(storeWriteCommitsRun(f)); n != 0 {
				t.Fatalf("git commit ran %d times", n)
			}
		}
	}
}

// A commit that fails (a hook that refuses it) is a failure like any other: the store goes back.
func TestAccessApplyRestoresWhenTheCommitFails(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	write(t, filepath.Join(x.store, ".git", "hooks", "pre-commit"), "#!/bin/sh\necho no >&2\nexit 1\n")
	if err := os.Chmod(filepath.Join(x.store, ".git", "hooks", "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := x.git("rev-parse", "HEAD")
	out, _, err := accessRun(f, "user", "add", "sam", accessSam)
	if err == nil || !strings.HasPrefix(err.Error(), "the change could not be committed") || !strings.HasSuffix(err.Error(), "Nothing was changed.") {
		t.Fatalf("got %v", err)
	}
	if out != "" || x.git("rev-parse", "HEAD") != head {
		t.Fatalf("stdout %q, or a commit was made", out)
	}
	x.wantClean()
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene)
}

// With no user in config.toml the entry is signed with this machine's name in the registry.
func TestUserAddWithoutAConfigUser(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	if err := os.Remove(setupConfig(f)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := accessRun(f, "user", "add", "sam", accessSam); err != nil {
		t.Fatal(err)
	}
	if got := accessRegistry(t, filepath.Join(x.store, ezcDir)).Recipients["sam"].AddedBy; got != "gene" {
		t.Fatalf("addedBy %q", got)
	}
}

// A private key pasted where the name goes is printed nowhere, and nothing is fetched for it.
func TestUserAddNeverEchoesAnArgument(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	private := strings.TrimSpace(accessGenePrivate)
	for _, args := range [][]string{{private, accessSam}, {"sam", private}, {private, private}} {
		head := x.git("rev-parse", "HEAD")
		out, errOut, err := accessRun(f, append([]string{"user", "add"}, args...)...)
		if err == nil {
			t.Fatalf("%v: want an error", args)
		}
		if strings.Contains(out+errOut+err.Error(), private) {
			t.Errorf("the private text was printed: %q %q %v", out, errOut, err)
		}
		if len(f.gits) != 0 || x.git("rev-parse", "HEAD") != head {
			t.Errorf("git ran %d times", len(f.gits))
		}
	}
	wantUsage(t, f.run("user", "add", private, accessSam), "ezc user add: the name is")
}

// A commit that fails leaves its files staged; when git then cannot unstage them, the error does not say nothing changed.
func TestAccessApplySaysWhenGitCannotBePutBack(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	write(t, filepath.Join(x.store, ".git", "hooks", "pre-commit"), "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(x.store, ".git", "hooks", "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.gitFail = "reset"
	_, _, err := accessRun(f, "user", "add", "sam", accessSam)
	var unchanged *unchangedError
	if err == nil || errors.As(err, &unchanged) || strings.Contains(err.Error(), "Nothing was changed") || !strings.Contains(err.Error(), "Run git status in "+x.store) {
		t.Fatalf("got %v", err)
	}
	// The bytes went back all the same.
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene)

	// A restore that git fails at but that leaves nothing staged is a restore.
	f = newFake(t)
	x = accessStore(t, f)
	f.backend.RewrapFail = "env.prod.enc.yaml"
	f.gitFail = "reset"
	if _, _, err = accessRun(f, "user", "add", "sam", accessSam); err == nil || !errors.As(err, &unchanged) {
		t.Fatalf("got %v", err)
	}
	f.gitFail = ""
	x.wantClean()
}

// Git ignores the store's files, so no commit can be made: no line says one was, nothing is pushed and the files go back.
func TestAccessApplyWhenGitIgnoresTheFiles(t *testing.T) {
	f := newFake(t)
	accessMachine(t, f)
	x := storeWriteFix{localStore(t, f)}
	write(t, filepath.Join(x.store, ".gitignore"), ".ezc/\n")
	if _, err := x.g.Commit(x.store, "ignore", gitstore.Author{}, ".gitignore"); err != nil {
		t.Fatal(err)
	}
	x.bare = gittest.Bare(t)
	if err := x.g.SetRemote(x.store, x.bare); err != nil {
		t.Fatal(err)
	}
	x = x.published()
	dir := filepath.Join(x.store, ezcDir)
	accessFiles(t, f, dir, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
	before, head := setupSnapshot(t, dir), x.git("rev-parse", "HEAD")

	out, _, err := accessRun(f, "user", "add", "sam", accessSam)
	want := "the change could not be committed: git ignores the store's files in " + dir + ". Nothing was changed."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	if out != "" || x.git("rev-parse", "HEAD") != head || !mapsEqual(before, setupSnapshot(t, dir)) {
		t.Fatalf("stdout %q, or a commit, or a changed file", out)
	}
	for _, c := range f.gits {
		if slices.Contains(c.Args, "push") {
			t.Fatal("it pushed")
		}
	}
}

// An encrypted file's name comes from whoever can push: one that cannot be shown stops every access command, and is shown made safe.
func TestAccessRefusesAFileNameThatCannotBeShown(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	x.commit(x.store, "env.\x1b[31mx.enc.yaml", "garbage\n")
	x.push(x.store)
	want := "an encrypted file in " + filepath.Join(x.store, ezcDir) + " has a name that cannot be shown (env.?[31mx.enc.yaml). Rename it, then run this again."
	for _, args := range [][]string{{"user", "list"}, {"user", "add", "sam", accessSam}, {"store", "rewrap"}} {
		accessUntouched(t, f, x, want, args...)
		if strings.Contains(f.out.String()+f.errOut.String(), "\x1b") {
			t.Errorf("%v printed the raw name", args)
		}
	}
}

// The requests of these tests are filed by machine b of joinMachines, or pushed by hand from the third clone.
const (
	requestNone     = "No pending requests.\n"
	requestOffline  = "The store's remote could not be reached, so the request cannot be read as it stands. Run this again when it can be."
	requestNameRule = "the name is lower case letters, digits and -, starting with a letter or a digit."
	// requestHand is a key nobody holds, for a request written by hand.
	requestHand = "fake1dd04"
)

// requestShown is a request as `ezc user requests` and `ezc user approve` print it.
func requestShown(q recipients.Request, author string) string {
	owner := q.Owner
	if owner == "" {
		owner = "-"
	}
	return q.Name + "\n" +
		"  kind       " + q.Kind + "\n" +
		"  owner      " + owner + "\n" +
		"  key        " + q.Key + "\n" +
		"  host       " + q.Host + "\n" +
		"  requested  " + q.Requested + "\n" +
		"  author     " + author + "\n"
}

// other runs git in the third clone, which has an identity.
func (j joinFix) other(args ...string) string {
	j.t.Helper()
	stdout, stderr, err := j.x.g.Run(proc.Command{Dir: j.x.other, Name: "git", Args: args})
	if err != nil {
		j.t.Fatalf("git %v: %v\n%s", args, err, stderr)
	}
	return strings.TrimRight(string(stdout), "\n")
}

// byHand commits files (paths in .ezc) on branch, cut from the remote's from, and pushes it: what anybody who can
// push to the store can do without ezc.
func (j joinFix) byHand(branch, from string, files map[string]string) {
	j.t.Helper()
	j.other("fetch", "--quiet", "origin")
	j.other("checkout", "--quiet", "-B", branch, "origin/"+from)
	for name, content := range files {
		write(j.t, filepath.Join(j.x.other, ezcDir, name), content)
	}
	j.other("add", "--all")
	j.other("commit", "--quiet", "-m", "by hand")
	j.other("push", "--quiet", "origin", branch)
	j.other("checkout", "--quiet", "main")
}

// merged merges sam's request branch into the remote's main, as merging its pull request does, and pulls it on a.
func (j joinFix) merged() {
	j.t.Helper()
	j.other("fetch", "--quiet", "origin")
	j.other("merge", "--quiet", "--ff-only", "origin/main")
	j.other("merge", "--quiet", "--no-edit", "--no-ff", "origin/"+joinBranch)
	j.other("push", "--quiet", "origin", "main")
	if _, _, err := accessRun(j.a, "store", "pull"); err != nil {
		j.t.Fatal(err)
	}
}

// author is the Git author of the last commit of a branch of the remote.
func (j joinFix) author(branch string) string {
	return j.remote("log", "-1", "--format=%an <%ae>", branch)
}

// approved fails unless a's last commit is `approve <name>` holding exactly the registry, the policy, the encrypted
// files and extra, the store is clean and level with the remote, and the registry names exactly gene and name.
func (j joinFix) approved(head, name, key string, extra ...string) {
	j.t.Helper()
	if got := j.x.git("log", "--format=%s", head+"..HEAD"); got != "approve "+name {
		j.t.Fatalf("commits %q", got)
	}
	want := append([]string{recipients.File, backend.FakePolicy}, accessEncFiles...)
	want = inEzc(append(want, extra...))
	slices.Sort(want)
	if got := j.x.git("show", "--name-only", "--format=", "HEAD"); got != strings.Join(want, "\n") {
		j.t.Fatalf("the commit holds %q, want %q", got, want)
	}
	j.x.wantClean()
	accessLevel(j.t, j.x)
	dir := filepath.Join(j.x.store, ezcDir)
	reg := accessRegistry(j.t, dir)
	if len(reg.Recipients) != 2 || reg.Recipients["gene"].Key != accessGene || reg.Recipients[name].Key != key {
		j.t.Fatalf("registry %+v", reg.Recipients)
	}
	accessReaders(j.t, j.a, dir, accessGene, key)
}

func TestUserRequestsAndApprove(t *testing.T) {
	j := joinMachines(t)
	accessWant(t, j.a, requestNone, "user", "requests")
	j.join(j.b, "--name", "sam")
	public := setupPublicKey(t, j.b)
	shown := requestShown(recipients.Request{Name: "sam", Key: public, Kind: "user", Host: "sams-laptop", Requested: "2026-10-11"}, j.author(joinBranch))
	if !strings.Contains(shown, "sam <") {
		t.Fatalf("no author in %q", shown)
	}
	// The listing fetches, whenever the store was last fetched.
	attemptedAt(t, j.a, noon)
	accessWant(t, j.a, shown, "user", "requests")

	head := j.x.git("rev-parse", "HEAD")
	accessWant(t, j.a, shown+"✓ approve sam\n✓ pushed to "+j.x.bare+"\n✓ deleted the branch "+joinBranch+"\nTell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
	j.approved(head, "sam", public)
	want := recipients.Recipient{Key: public, Kind: recipients.KindUser, Added: "2026-10-11", AddedBy: "gene"}
	if got := accessRegistry(t, filepath.Join(j.x.store, ezcDir)).Recipients["sam"]; got.Key != want.Key || got.Kind != want.Kind || got.Owner != "" || got.Added != want.Added || got.AddedBy != want.AddedBy {
		t.Fatalf("entry %+v, want %+v", got, want)
	}
	if got := j.branches(); !slices.Equal(got, []string{"main"}) {
		t.Fatalf("the remote's branches %q", got)
	}
	if got := j.x.git("for-each-ref", "refs/remotes/origin/ezc/"); got != "" {
		t.Fatalf("a's copy of the branch is left: %q", got)
	}
	accessWant(t, j.a, requestNone, "user", "requests")

	// Machine b pulls, and its key now opens every file.
	if _, _, err := joinRun(t, j.b, "store", "pull"); err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(setupKeyFile(j.b))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range accessEncFiles {
		if err := j.b.backend.CanDecrypt(filepath.Join(setupStore(j.b), ezcDir, file), private); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
}

func TestUserApproveAMachine(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "gene-laptop", "--kind", "machine", "--owner", "gene")
	if _, _, err := accessRun(j.a, "user", "approve", "gene-laptop", "--yes"); err != nil {
		t.Fatal(err)
	}
	got := accessRegistry(t, filepath.Join(j.x.store, ezcDir)).Recipients["gene-laptop"]
	if got.Key != setupPublicKey(t, j.b) || got.Kind != recipients.KindMachine || got.Owner != "gene" {
		t.Fatalf("entry %+v", got)
	}
}

// Review focus 1: approving takes the request file from the branch and nothing else that the branch holds.
func TestUserApproveTakesOnlyTheRequest(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	public := setupPublicKey(t, j.b)
	registry := readFile(t, j.x.file(recipients.File))
	entry := "\n        key: " + public + "\n        kind: user\n        added: \"2026-10-11\"\n        addedBy: gene\n"
	j.byHand(joinBranch, joinBranch, map[string]string{
		recipients.File: registry + "    mallory:" + entry + "    mallory-2:" + entry,
		"common.yaml":   "NAME: theirs\n",
	})

	head := j.x.git("rev-parse", "HEAD")
	if _, _, err := accessRun(j.a, "user", "approve", "sam", "--yes"); err != nil {
		t.Fatal(err)
	}
	j.approved(head, "sam", public)
	for _, where := range []string{readFile(t, j.x.file("common.yaml")), j.remote("show", "main:.ezc/common.yaml") + "\n"} {
		if where != "NAME: n\n" {
			t.Fatalf("common.yaml is now %q", where)
		}
	}
	if got := j.remote("show", "main:.ezc/"+recipients.File); strings.Contains(got, "mallory") {
		t.Fatalf("the remote's registry:\n%s", got)
	}
}

// Review focus 2: what a request says, and what its branch is called, is text somebody else wrote.
func TestUserRequestsAreUntrusted(t *testing.T) {
	j := joinMachines(t)
	request := func(name string, q recipients.Request) map[string]string {
		return map[string]string{recipients.RequestPath(name): string(q.Marshal())}
	}
	esc := recipients.Request{Name: "esc", Key: requestHand, Kind: "user", Host: "\x1b]0;x\x07", Requested: "2026-10-\x1b[31m"}
	j.byHand(requestBranch+"bad", "main", request("bad", recipients.Request{Name: "other", Key: "fake1ee05", Kind: "user"}))
	j.byHand(requestBranch+"esc", "main", request("esc", esc))
	j.byHand(requestBranch+"UP_PER", "main", request("UP_PER", recipients.Request{Name: "UP_PER", Key: "fake1ff06", Kind: "user"}))
	j.byHand(requestBranch+"empty", "main", map[string]string{"notes.txt": "no request here\n"})

	esc.Host, esc.Requested = "?]0;x?", "2026-10-?[31m"
	listed := "bad: not a valid request (the name inside the request differs from its file name)\n" +
		"empty: not a valid request (its branch holds no request)\n" +
		requestShown(esc, j.author(requestBranch+"esc"))
	accessWant(t, j.a, listed, "user", "requests")
	status, errOut, err := accessRun(j.a, "store", "status")
	if err != nil || !strings.Contains(status, "requests  bad, empty, esc\n") {
		t.Fatalf("store status: %v\n%s", err, status)
	}
	line := stderrOf(t, j.a, "list", "-g")
	if line != "store: 3 access requests pending: bad, empty, esc\n" {
		t.Fatalf("the line: %q", line)
	}
	for _, text := range []string{status, errOut, line} {
		if strings.ContainsAny(text, "\x1b\x07") || strings.Contains(text, "UP_PER") {
			t.Fatalf("shown: %q", text)
		}
	}

	// A request that is not valid cannot be approved, and a name that could not be one is never repeated.
	accessUntouched(t, j.a, j.x, "The request of bad is not valid (the name inside the request differs from its file name). Refuse it with ezc user deny bad.", "user", "approve", "bad")
	accessUntouched(t, j.a, j.x, "The request of empty is not valid (its branch holds no request). Refuse it with ezc user deny empty.", "user", "approve", "empty")
	for _, verb := range []string{"approve", "deny"} {
		out, errOut, err := accessRun(j.a, "user", verb, "UP_PER")
		wantUsage(t, err, requestNameRule)
		if strings.Contains(out+errOut+err.Error(), "UP_PER") || len(j.a.gits) != 0 {
			t.Fatalf("%s: %v, %q, %q, git ran %d times", verb, err, out, errOut, len(j.a.gits))
		}
	}
	if got := j.branches(); !slices.Contains(got, requestBranch+"UP_PER") {
		t.Fatalf("branches %q", got)
	}

	// The one that is valid is approved as it was shown, and the one that is not is refused with deny.
	head := j.x.git("rev-parse", "HEAD")
	out, _, err := accessRun(j.a, "user", "approve", "esc", "--yes")
	if err != nil || !strings.HasPrefix(out, "esc\n") || strings.ContainsAny(out, "\x1b\x07") || !strings.Contains(out, "  host       ?]0;x?\n") {
		t.Fatalf("approve esc: %v\n%q", err, out)
	}
	j.approved(head, "esc", requestHand)
	accessWant(t, j.a, "✓ deleted the branch "+requestBranch+"bad\n", "user", "deny", "bad")
	if got := j.branches(); !slices.Equal(got, []string{requestBranch + "UP_PER", requestBranch + "empty", "main"}) {
		t.Fatalf("branches %q", got)
	}
}

// The Git author is as untrusted as the request.
func TestUserRequestsAuthorIsMadePrintable(t *testing.T) {
	j := joinMachines(t)
	q := recipients.Request{Name: "kim", Key: requestHand, Kind: "user", Host: "h", Requested: "2026-10-11"}
	j.other("config", "user.name", "ki\x1b[31mm é")
	j.byHand(requestBranch+"kim", "main", map[string]string{recipients.RequestPath("kim"): string(q.Marshal())})
	accessWant(t, j.a, requestShown(q, "ki?[31mm ? <"+gittest.Email+">"), "user", "requests")
}

// The request is read again when it is approved: what is printed is what is on the remote then, and what is registered.
func TestUserApproveReadsTheRequestAgain(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	first := setupPublicKey(t, j.b)
	if out, _, err := accessRun(j.a, "user", "requests"); err != nil || !strings.Contains(out, first) {
		t.Fatalf("%v\n%s", err, out)
	}
	q := recipients.Request{Name: "sam", Key: requestHand, Kind: "user", Host: "elsewhere", Requested: "2026-10-11"}
	j.byHand(joinBranch, joinBranch, map[string]string{recipients.RequestPath("sam"): string(q.Marshal())})

	head := j.x.git("rev-parse", "HEAD")
	out, _, err := accessRun(j.a, "user", "approve", "sam", "--yes")
	if err != nil || !strings.HasPrefix(out, "sam\n") || !strings.Contains(out, "  key        "+requestHand+"\n") || strings.Contains(out, first) {
		t.Fatalf("%v\n%s", err, out)
	}
	j.approved(head, "sam", requestHand)
}

// Somebody merged the request's pull request: the file is in the store, and approving removes it in its commit.
func TestUserApproveARequestMergedIntoTheStore(t *testing.T) {
	for _, branchLeft := range []bool{true, false} {
		j := joinMachines(t)
		j.join(j.b, "--name", "sam")
		public := setupPublicKey(t, j.b)
		author := j.author(joinBranch)
		j.merged()
		if !branchLeft {
			j.other("push", "--quiet", "origin", "--delete", joinBranch)
		}
		shown := requestShown(recipients.Request{Name: "sam", Key: public, Kind: "user", Host: "sams-laptop", Requested: "2026-10-11"}, author)
		accessWant(t, j.a, shown, "user", "requests")

		head := j.x.git("rev-parse", "HEAD")
		want := shown + "✓ approve sam\n✓ pushed to " + j.x.bare + "\n"
		if branchLeft {
			want += "✓ deleted the branch " + joinBranch + "\n"
		}
		accessWant(t, j.a, want+"Tell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
		j.approved(head, "sam", public, recipients.RequestPath("sam"))
		missing(t, j.x.file(recipients.RequestPath("sam")))
		if got := j.branches(); !slices.Equal(got, []string{"main"}) {
			t.Fatalf("branches %q", got)
		}
	}
}

// A branch that says one thing and a store that says another is not a request anybody can approve.
func TestUserApproveWhenTheBranchAndTheStoreDiffer(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	j.merged()
	q := recipients.Request{Name: "sam", Key: requestHand, Kind: "user", Host: "sams-laptop", Requested: "2026-10-11"}
	j.byHand(joinBranch, joinBranch, map[string]string{recipients.RequestPath("sam"): string(q.Marshal())})
	const reason = "its branch and the store hold different requests"
	accessWant(t, j.a, "sam: not a valid request ("+reason+")\n", "user", "requests")
	accessUntouched(t, j.a, j.x, "The request of sam is not valid ("+reason+"). Refuse it with ezc user deny sam.", "user", "approve", "sam")
}

func TestUserApproveRefused(t *testing.T) {
	j := joinMachines(t)
	request := func(name, key, kind, owner string) {
		q := recipients.Request{Name: name, Key: key, Kind: kind, Owner: owner, Host: "h", Requested: "2026-10-11"}
		j.byHand(requestBranch+name, "main", map[string]string{recipients.RequestPath(name): string(q.Marshal())})
	}
	request("gene", requestHand, "user", "")
	request("twin", accessGene, "user", "")
	request("laptop", requestHand, "machine", "nobody")
	request("robo", "fake1ee05", "bot", "")
	accessUntouched(t, j.a, j.x, "The request of robo is not valid (kind must be user or machine). Refuse it with ezc user deny robo.", "user", "approve", "robo")
	accessUntouched(t, j.a, j.x, "gene is already a recipient.", "user", "approve", "gene")
	accessUntouched(t, j.a, j.x, "that key is already registered to gene.", "user", "approve", "twin")
	accessUntouched(t, j.a, j.x, "the owner of a machine must be a user among the recipients.", "user", "approve", "laptop")
	accessUntouched(t, j.a, j.x, "There is no request from sam. ezc user requests lists them.", "user", "approve", "sam")
	accessUntouched(t, j.a, j.x, "There is no request from sam. ezc user requests lists them.", "user", "deny", "sam")
	if got := j.branches(); len(got) != 5 {
		t.Fatalf("branches %q", got)
	}
	for _, verb := range []string{"approve", "deny"} {
		wantUsage(t, j.a.run("user", verb), "a name")
	}
}

// A request is approved as the remote holds it now, so a remote that cannot be asked stops it.
func TestUserApproveNeedsTheRemote(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	accessWant(t, j.a, requestShown(j.request(j.b), j.author(joinBranch)), "user", "requests")
	if err := os.Rename(j.x.bare, j.x.bare+".away"); err != nil {
		t.Fatal(err)
	}
	accessUntouched(t, j.a, j.x, requestOffline, "user", "approve", "sam")
	accessUntouched(t, j.a, j.x, requestOffline, "user", "deny", "sam")

	// The listing still says what was known, and that it may be old.
	out, errOut, err := accessRun(j.a, "user", "requests")
	if err != nil || !strings.HasPrefix(out, "sam\n") || errOut != "! The store's remote could not be reached: these are the requests as they were when it last was.\n" {
		t.Fatalf("%v\n%q\n%q", err, out, errOut)
	}
}

// The commit is made and the push fails: the branch stays, and approving again after the push deletes it.
func TestUserApproveWhenThePushFails(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	public := setupPublicKey(t, j.b)
	shown := requestShown(j.request(j.b), j.author(joinBranch))

	j.a.gitFail = "--set-upstream"
	out, _, err := accessRun(j.a, "user", "approve", "sam", "--yes")
	var unpushed *unpushedError
	if !errors.As(err, &unpushed) {
		t.Fatalf("got %v", err)
	}
	if want := shown + "✓ approve sam\n! The request's branch " + joinBranch + " was left. Run ezc store push, then ezc user approve sam again to delete it.\n"; out != want {
		t.Fatalf("stdout %q\nwant   %q", out, want)
	}
	if got := j.x.subjects(); !slices.Equal(got, []string{"approve sam"}) {
		t.Fatalf("commits %q", got)
	}
	if got := j.branches(); !slices.Equal(got, []string{joinBranch, "main"}) {
		t.Fatalf("branches %q", got)
	}

	j.a.gitFail = ""
	accessUntouched(t, j.a, j.x, "The store has commits its remote does not. Run ezc store push, then run this again.", "user", "approve", "sam")
	if _, _, err := accessRun(j.a, "store", "push"); err != nil {
		t.Fatal(err)
	}
	head := j.x.git("rev-parse", "HEAD")
	accessWant(t, j.a, shown+"✓ sam can already read the store\n✓ deleted the branch "+joinBranch+"\nTell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
	if j.x.git("rev-parse", "HEAD") != head || !slices.Equal(j.branches(), []string{"main"}) {
		t.Fatalf("a commit was made, or the branch is left: %q", j.branches())
	}
	accessReaders(t, j.a, filepath.Join(j.x.store, ezcDir), accessGene, public)
}

func TestUserApproveWhenTheBranchCannotBeDeleted(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	public := setupPublicKey(t, j.b)
	shown := requestShown(j.request(j.b), j.author(joinBranch))
	head := j.x.git("rev-parse", "HEAD")
	j.a.gitFail = ":refs/heads/" + joinBranch
	accessWant(t, j.a, shown+"✓ approve sam\n✓ pushed to "+j.x.bare+"\n! The request's branch "+joinBranch+" could not be deleted (made to fail). Run ezc user approve sam again.\nTell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
	j.approved(head, "sam", public)

	accessUntouched(t, j.a, j.x, "The request's branch "+joinBranch+" could not be deleted (made to fail). Run ezc user deny sam again.", "user", "deny", "sam")
	j.a.gitFail = ""
	accessWant(t, j.a, shown+"✓ sam can already read the store\n✓ deleted the branch "+joinBranch+"\nTell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
}

func TestUserDeny(t *testing.T) {
	// A request on its branch: the branch goes, and the store is not written to.
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	head := j.x.git("rev-parse", "HEAD")
	accessWant(t, j.a, "✓ deleted the branch "+joinBranch+"\n", "user", "deny", "sam")
	if j.x.git("rev-parse", "HEAD") != head || !slices.Equal(j.branches(), []string{"main"}) {
		t.Fatalf("a commit was made, or the branch is left: %q", j.branches())
	}
	j.x.wantClean()
	accessWant(t, j.a, requestNone, "user", "requests")

	// A request that was merged: the file goes in a commit of its own, and so does the branch.
	j = joinMachines(t)
	j.join(j.b, "--name", "sam")
	j.merged()
	accessWant(t, j.a, "✓ deny sam\n✓ pushed to "+j.x.bare+"\n✓ deleted the branch "+joinBranch+"\n", "user", "deny", "sam")
	if got := j.x.git("show", "--name-status", "--format=%s", "HEAD"); got != "deny sam\n\nD\t.ezc/"+recipients.RequestPath("sam") {
		t.Fatalf("the commit: %q", got)
	}
	j.x.wantClean()
	accessLevel(t, j.x)
	if got := accessRegistry(t, filepath.Join(j.x.store, ezcDir)).Recipients; len(got) != 1 || !slices.Equal(j.branches(), []string{"main"}) {
		t.Fatalf("registry %+v, branches %q", got, j.branches())
	}
	accessWant(t, j.a, requestNone, "user", "requests")
}

// A commit that fails puts the request's file back, and the branch is still there.
func TestUserDenyRestoresWhenTheCommitFails(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	j.merged()
	j.a.gitFail = "commit"
	accessUntouched(t, j.a, j.x, "the change could not be committed: made to fail. Nothing was changed.", "user", "deny", "sam")
	if got := j.branches(); !slices.Equal(got, []string{joinBranch, "main"}) {
		t.Fatalf("branches %q", got)
	}
}

// A store with no remote, or that is no git repo at all, has requests only as files somebody put there.
func TestUserApproveAndDenyWithoutARemote(t *testing.T) {
	q := recipients.Request{Name: "sam", Key: accessSam, Kind: "user", Host: "h", Requested: "2026-10-11"}
	for _, repo := range []bool{true, false} {
		f := newFake(t)
		accessMachine(t, f)
		dir := filepath.Join(setupStore(f), ezcDir)
		files := func() {
			accessFiles(t, f, dir, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
			write(t, filepath.Join(dir, recipients.RequestPath("sam")), string(q.Marshal()))
		}
		if repo {
			x := localStore(t, f)
			files()
			if _, err := x.g.Commit(x.store, "the store", gitstore.Author{}, ezcDir); err != nil {
				t.Fatal(err)
			}
		} else {
			files()
		}
		accessWant(t, f, requestShown(q, "-"), "user", "requests")
		accessWant(t, f, "✓ deny sam\n", "user", "deny", "sam")
		missing(t, filepath.Join(dir, recipients.RequestPath("sam")))
		accessWant(t, f, requestNone, "user", "requests")

		write(t, filepath.Join(dir, recipients.RequestPath("sam")), string(q.Marshal()))
		accessWant(t, f, requestShown(q, "-")+"✓ approve sam\nTell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
		missing(t, filepath.Join(dir, recipients.RequestPath("sam")))
		accessReaders(t, f, dir, accessGene, accessSam)
		if repo {
			if got := setupGitOut(t, f, "status", "--porcelain", "--untracked-files=all"); got != "" {
				t.Fatalf("left uncommitted:\n%s", got)
			}
		}
	}
}

// The key was added another way and the request was merged all the same: approving only clears the request.
func TestUserApproveAKeyThatIsAlreadyRegistered(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	shown := requestShown(j.request(j.b), j.author(joinBranch))
	if _, _, err := accessRun(j.a, "user", "add", "sam", setupPublicKey(t, j.b)); err != nil {
		t.Fatal(err)
	}
	j.merged()
	want := shown + "✓ sam can already read the store\n✓ approve sam\n✓ pushed to " + j.x.bare + "\n✓ deleted the branch " + joinBranch + "\nTell sam to run ezc store pull.\n"
	accessWant(t, j.a, want, "user", "approve", "sam", "--yes")
	if got := j.x.git("show", "--name-status", "--format=%s", "HEAD"); got != "approve sam\n\nD\t.ezc/"+recipients.RequestPath("sam") {
		t.Fatalf("the commit: %q", got)
	}
	j.x.wantClean()
	accessLevel(t, j.x)
	accessWant(t, j.a, requestNone, "user", "requests")
}

// tagged pushes tags that a short name would find before sam's request branch, and before main: each points at a
// commit whose request for sam holds another key.
func (j joinFix) tagged() {
	j.t.Helper()
	q := recipients.Request{Name: "sam", Key: requestHand, Kind: "user", Host: "elsewhere", Requested: "2026-10-11"}
	j.byHand("hostile", "main", map[string]string{recipients.RequestPath("sam"): string(q.Marshal()), "common.yaml": "NAME: theirs\n"})
	for _, tag := range []string{"origin/" + joinBranch, joinBranch, "origin/main", "main"} {
		j.other("tag", tag, "hostile")
		j.other("push", "--quiet", "origin", "refs/tags/"+tag)
	}
}

// A tag is found before a branch of the same short name. Whoever can push one must not thereby choose the key.
func TestUserApproveReadsTheBranchAndNoTag(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	public := setupPublicKey(t, j.b)
	shown := requestShown(j.request(j.b), j.author("refs/heads/"+joinBranch))
	j.tagged()
	// A store that took the tags in before ezc stopped fetching them.
	j.x.git("fetch", "--quiet", "--tags", "origin")
	if got := j.x.git("tag", "--list"); !strings.Contains(got, "origin/"+joinBranch) {
		t.Fatalf("a's tags: %q", got)
	}

	accessWant(t, j.a, shown, "user", "requests")
	head := j.x.git("rev-parse", "HEAD")
	accessWant(t, j.a, shown+"✓ approve sam\n✓ pushed to "+j.x.bare+"\n✓ deleted the branch "+joinBranch+"\nTell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
	j.approved(head, "sam", public)
	if got := j.remote("for-each-ref", "--format=%(refname)", "refs/heads/"); got != "refs/heads/hostile\nrefs/heads/main" {
		t.Fatalf("branches %q", got)
	}
	if got := readFile(t, j.x.file("common.yaml")); got != "NAME: n\n" {
		t.Fatalf("common.yaml is now %q", got)
	}
}

// On a terminal approve asks, after showing the request and before changing anything.
func TestUserApproveAsks(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	public := setupPublicKey(t, j.b)
	shown := requestShown(j.request(j.b), j.author(joinBranch))

	// Without a terminal there is nobody to ask.
	out, errOut, err := accessRun(j.a, "user", "approve", "sam")
	wantUsage(t, err, "say --yes to approve without being asked")
	if out != "" || errOut != "" {
		t.Fatalf("stdout %q, stderr %q", out, errOut)
	}

	j.a.tty = true
	for _, answer := range []string{"n\n", "\n", "", "yes please\n"} {
		j.a.stdin = answer
		head, before := j.x.git("rev-parse", "HEAD"), setupSnapshot(t, filepath.Join(j.x.store, ezcDir))
		out, errOut, err := accessRun(j.a, "user", "approve", "sam")
		wantExit(t, err, 1)
		if out != shown+"Not approved.\n" || errOut != "Approve sam? [y/N] " {
			t.Fatalf("answer %q: stdout %q, stderr %q", answer, out, errOut)
		}
		if j.x.git("rev-parse", "HEAD") != head || !mapsEqual(before, setupSnapshot(t, filepath.Join(j.x.store, ezcDir))) || !slices.Equal(j.branches(), []string{joinBranch, "main"}) {
			t.Fatalf("answer %q changed the store or the branch", answer)
		}
		j.x.wantClean()
	}

	j.a.stdin = "Y\n"
	head := j.x.git("rev-parse", "HEAD")
	out, errOut, err = accessRun(j.a, "user", "approve", "sam")
	if want := shown + "✓ approve sam\n✓ pushed to " + j.x.bare + "\n✓ deleted the branch " + joinBranch + "\nTell sam to run ezc store pull.\n"; err != nil || out != want || errOut != "Approve sam? [y/N] " {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
	j.approved(head, "sam", public)
}

// What was shown is what is added: a request pushed under the name while the approver reads is neither registered
// nor deleted unread.
func TestUserApproveWhileAnotherRequestArrives(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	public := setupPublicKey(t, j.b)
	shown := requestShown(j.request(j.b), j.author(joinBranch))
	later := recipients.Request{Name: "sam", Key: requestHand, Kind: "user", Host: "elsewhere", Requested: "2026-10-11"}
	arrived := "A new request named sam arrived in the meantime, so its branch " + joinBranch + " was left. ezc user requests shows it."

	// The branch moves once a's fetch is done: when a commits.
	moved := false
	j.a.execHook = func(c proc.Command) {
		if !moved && c.Name == "git" && slices.Contains(c.Args, "commit") {
			moved = true
			j.byHand(joinBranch, joinBranch, map[string]string{recipients.RequestPath("sam"): string(later.Marshal())})
		}
	}
	head := j.x.git("rev-parse", "HEAD")
	accessWant(t, j.a, shown+"✓ approve sam\n✓ pushed to "+j.x.bare+"\n! "+arrived+"\nTell sam to run ezc store pull.\n", "user", "approve", "sam", "--yes")
	j.a.execHook = nil
	j.approved(head, "sam", public)
	if got := j.branches(); !slices.Equal(got, []string{joinBranch, "main"}) {
		t.Fatalf("branches %q", got)
	}
	// The one that arrived is listed, and is another's request under a name that is now taken.
	accessWant(t, j.a, requestShown(later, j.author(joinBranch)), "user", "requests")
	accessUntouched(t, j.a, j.x, "sam is already a recipient.", "user", "approve", "sam")
}

// Many requests do not make the line long.
func TestSomeNames(t *testing.T) {
	if got := someNames([]string{"a", "b", "c", "d", "e"}); got != "a, b, c, d, e" {
		t.Fatalf("got %q", got)
	}
	if got := someNames([]string{"a", "b", "c", "d", "e", "f", "g"}); got != "a, b, c, d, e and 2 more" {
		t.Fatalf("got %q", got)
	}
}
