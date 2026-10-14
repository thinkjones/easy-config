package cli

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/registry"
)

// registerRepos writes repos.yaml with ids registered, and commits and pushes it.
func registerRepos(t *testing.T, x storeWriteFix, ids ...string) {
	t.Helper()
	repos := registry.Registry{Repos: map[string]registry.Repo{}}
	for _, id := range ids {
		repos.Repos[id] = registry.Repo{Added: "2026-10-01", AddedBy: "gene"}
	}
	if err := repos.Save(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	if done, err := x.g.Commit(x.store, "repos", gitstore.Author{}, ezcDir+"/"+registry.File); err != nil || !done {
		t.Fatalf("commit: %v, %v", done, err)
	}
	x.push(x.store)
	// The other clone follows, so that a commit made there later is not behind.
	if x.other != "" {
		if _, stderr, err := x.g.Run(proc.Command{Dir: x.other, Name: "git", Args: []string{"pull", "--ff-only", "--quiet"}}); err != nil {
			t.Fatalf("pull: %v\n%s", err, stderr)
		}
	}
}

// noCheckoutHere is what an access change says of each registered repo with no checkout on this machine.
func noCheckoutHere(ids ...string) string {
	var b strings.Builder
	for _, id := range ids {
		b.WriteString("! " + id + " has no checkout on this machine, so its secrets are still encrypted for the old recipients. Run ezc repo rewrap " + id + " where it has one.\n")
	}
	return b.String()
}

// reposCheckout is a checkout of id whose .ezc has a policy for keys and API_TOKEN in each of files, encrypted for
// them, recorded in config.toml's [checkouts]. It returns the checkout's .ezc.
func reposCheckout(t *testing.T, f *fake, id string, keys []string, files ...string) string {
	t.Helper()
	top := t.TempDir()
	dir := filepath.Join(top, ezcDir)
	write(t, filepath.Join(dir, manifestName), mToken)
	if err := f.backend.WritePolicy(dir, keys); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := f.backend.Set(filepath.Join(dir, file), "API_TOKEN", rewrapValue); err != nil {
			t.Fatal(err)
		}
	}
	c, _, err := config.Load(setupConfig(f))
	if err != nil {
		t.Fatal(err)
	}
	if c.Checkouts == nil {
		c.Checkouts = map[string]string{}
	}
	c.Checkouts[id] = top
	if err := config.Save(setupConfig(f), c); err != nil {
		t.Fatal(err)
	}
	return dir
}

// reposFor fails unless each of files in dir is encrypted for exactly keys.
func reposFor(t *testing.T, f *fake, dir string, files []string, keys ...string) {
	t.Helper()
	slices.Sort(keys)
	for _, file := range files {
		got, err := f.backend.Recipients(filepath.Join(dir, file))
		if err != nil || !slices.Equal(got, keys) {
			t.Fatalf("%s is for %q (%v), want %q", filepath.Join(dir, file), got, err, keys)
		}
	}
}

// reposFix is a store whose registry is reg and whose files are for storeKeys, with o/app and o/web registered and
// checked out here, each holding common.enc.yaml encrypted for repoKeys.
func reposFix(t *testing.T, f *fake, reg map[string]recipients.Recipient, storeKeys, repoKeys []string) (x storeWriteFix, app, web string) {
	t.Helper()
	x = accessStoreWith(t, f, reg, storeKeys...)
	registerRepos(t, x, "o/app", "o/web")
	app = reposCheckout(t, f, "o/app", repoKeys, "common.enc.yaml")
	web = reposCheckout(t, f, "o/web", repoKeys, "common.enc.yaml")
	return x, app, web
}

func reposRewrapped(id, ezc string) string {
	return "✓ " + id + ": re-wrapped common.enc.yaml in " + ezc + ". Commit it there.\n"
}

// An access change re-wraps every registered repo checked out here, after the store's own lines, and the store's
// commit holds none of a repo's files.
func TestAccessChangeRewrapsTheRepos(t *testing.T) {
	f := newFake(t)
	x, app, web := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, []string{accessGene}, []string{accessGene})
	accessWant(t, f, "✓ add sam\n✓ pushed to "+x.bare+"\n"+reposRewrapped("o/app", app)+reposRewrapped("o/web", web), "user", "add", "sam", accessSam, "--yes")
	reposFor(t, f, app, []string{"common.enc.yaml"}, accessGene, accessSam)
	reposFor(t, f, web, []string{"common.enc.yaml"}, accessGene, accessSam)
	noGitIn(t, f, filepath.Dir(app), filepath.Dir(web))
	want := ".ezc/.fakepolicy\n.ezc/common.enc.yaml\n.ezc/env.prod.enc.yaml\n.ezc/recipients.yaml"
	if got := x.git("show", "--name-only", "--format=", "HEAD"); got != want {
		t.Fatalf("the commit holds %q", got)
	}
	x.wantClean()
	accessLevel(t, x)
}

// A removal re-wraps the repos and names each of their secrets the removed key could read.
func TestUserRemoveRewrapsTheReposAndListsTheirSecrets(t *testing.T) {
	f := newFake(t)
	both := []string{accessGene, accessSam}
	x, app, web := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}, both, both)
	leaked := func(id string) string {
		return "! " + id + ": common.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN.\n"
	}
	out, errOut, err := accessRun(f, "user", "remove", "sam")
	want := "✓ remove sam\n✓ pushed to " + x.bare + "\n" + reposRewrapped("o/app", app) + leaked("o/app") + reposRewrapped("o/web", web) + leaked("o/web")
	if err != nil || errOut != "" || !strings.HasPrefix(out, want) {
		t.Fatalf("%v\nstdout %q\nwant   %q…\nstderr %q", err, out, want, errOut)
	}
	reposFor(t, f, app, []string{"common.enc.yaml"}, accessGene)
	reposFor(t, f, web, []string{"common.enc.yaml"}, accessGene)
	noGitIn(t, f, filepath.Dir(app), filepath.Dir(web))
}

// The store's re-wrap re-wraps the repos too, for the names its question let in.
func TestStoreRewrapRewrapsTheRepos(t *testing.T) {
	f := newFake(t)
	x, app, web := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}, []string{accessGene}, []string{accessGene})
	accessWant(t, f, "! sam gains access to common.enc.yaml, env.prod.enc.yaml.\n✓ rewrap\n✓ pushed to "+x.bare+"\n"+reposRewrapped("o/app", app)+reposRewrapped("o/web", web), "store", "rewrap", "--yes")
	reposFor(t, f, app, []string{"common.enc.yaml"}, accessGene, accessSam)
	reposFor(t, f, web, []string{"common.enc.yaml"}, accessGene, accessSam)
	noGitIn(t, f, filepath.Dir(app), filepath.Dir(web))
}

// A repo that fails is a line naming the command that re-wraps it, and is left as it was; the others are re-wrapped
// and the change stands.
func TestAccessChangeRepoThatFails(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	registerRepos(t, x, "o/app", "o/web")
	app := reposCheckout(t, f, "o/app", []string{accessGene}, "env.staging.enc.yaml")
	web := reposCheckout(t, f, "o/web", []string{accessGene}, "common.enc.yaml")
	before := folderBytes(t, app)
	f.backend.RewrapFail = "env.staging.enc.yaml"
	out, errOut, err := accessRun(f, "user", "add", "sam", accessSam)
	if err != nil || errOut != "" {
		t.Fatalf("%v, stderr %q", err, errOut)
	}
	lines := strings.SplitAfter(out, "\n")
	if len(lines) != 5 || lines[0]+lines[1] != "✓ add sam\n✓ pushed to "+x.bare+"\n" || lines[3] != reposRewrapped("o/web", web) {
		t.Fatalf("stdout %q", out)
	}
	if !strings.HasPrefix(lines[2], "! o/app could not be re-wrapped (") || !strings.HasSuffix(lines[2], "). Run ezc repo rewrap o/app.\n") {
		t.Fatalf("the failure is %q", lines[2])
	}
	if after := folderBytes(t, app); !mapsEqual(before, after) {
		t.Fatal("o/app's files changed")
	}
	reposFor(t, f, web, []string{"common.enc.yaml"}, accessGene, accessSam)
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene, accessSam)
}

// A registered repo with no checkout here is named, with where to re-wrap it.
func TestAccessChangeRepoWithNoCheckoutHere(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	registerRepos(t, x, "o/app", "o/other")
	app := reposCheckout(t, f, "o/app", []string{accessGene}, "common.enc.yaml")
	accessWant(t, f, "✓ add sam\n✓ pushed to "+x.bare+"\n"+reposRewrapped("o/app", app)+noCheckoutHere("o/other"), "user", "add", "sam", accessSam)
}

// A store with no repos.yaml re-wraps no repo and says nothing; one whose repos.yaml cannot be read says so once and
// the change stands.
func TestAccessChangeWithoutARepoRegistry(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	accessWant(t, f, "✓ add sam\n✓ pushed to "+x.bare+"\n", "user", "add", "sam", accessSam)

	write(t, x.file(registry.File), "repos:\n  \"\\e[31mo/app\": {added: 2026-10-01, addedBy: gene}\n")
	if done, err := x.g.Commit(x.store, "repos", gitstore.Author{}, ezcDir+"/"+registry.File); err != nil || !done {
		t.Fatalf("commit: %v, %v", done, err)
	}
	x.push(x.store)
	accessWant(t, f, "✓ add zed\n✓ pushed to "+x.bare+"\n! repos.yaml cannot be read, so no repo was re-wrapped. Run ezc repo list to see why, then ezc repo rewrap --all.\n", "user", "add", "zed", "fake1dd04")
}

// A repo whose secrets would be opened to a name the change neither is about nor asked about is left as it was.
func TestAccessChangeNeverLetsAnUnaskedNameIntoARepo(t *testing.T) {
	f := newFake(t)
	both := []string{accessGene, accessSam}
	x, app, web := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}, both, []string{accessGene})
	before := folderBytes(t, app)
	skipped := func(id string) string {
		return "! " + id + ": sam would also gain access to its secrets. Run ezc repo rewrap " + id + ".\n"
	}
	accessWant(t, f, "✓ add zed\n✓ pushed to "+x.bare+"\n"+skipped("o/app")+skipped("o/web"), "user", "add", "zed", "fake1dd04")
	if after := folderBytes(t, app); !mapsEqual(before, after) {
		t.Fatal("o/app's files changed")
	}
	reposFor(t, f, web, []string{"common.enc.yaml"}, accessGene)
}

// A name the change's question let in is let into the repos too.
func TestAccessChangeLetsTheAnsweredNamesIntoTheRepos(t *testing.T) {
	f := newFake(t)
	x := effectIntruder(t, f)
	registerRepos(t, x, "o/app")
	keys := []string{accessGene, accessSam, accessStray, effectIntruderKey}
	app := reposCheckout(t, f, "o/app", []string{accessGene, accessSam, accessStray}, "common.enc.yaml")
	f.tty, f.stdin = true, "y\n"
	out, _, err := accessRun(f, "user", "add", "zed", "fake1dd04")
	if want := effectAlso + "✓ add zed\n✓ pushed to " + x.bare + "\n" + reposRewrapped("o/app", app); err != nil || out != want {
		t.Fatalf("%v\nstdout %q\nwant   %q", err, out, want)
	}
	reposFor(t, f, app, []string{"common.enc.yaml"}, append(keys, "fake1dd04")...)
}

// A change that is refused, or answered no, touches no repo.
func TestAccessChangeThatIsNotMadeTouchesNoRepo(t *testing.T) {
	f := newFake(t)
	x := effectIntruder(t, f)
	registerRepos(t, x, "o/app")
	app := reposCheckout(t, f, "o/app", []string{accessGene, accessSam, accessStray}, "common.enc.yaml")
	before := folderBytes(t, app)
	accessUntouched(t, f, x, "nobody is not a recipient.", "user", "remove", "nobody")
	f.tty, f.stdin = true, "n\n"
	if _, _, err := accessRun(f, "user", "add", "zed", "fake1dd04"); err == nil {
		t.Fatal("answered no, the change was made")
	}
	if after := folderBytes(t, app); !mapsEqual(before, after) {
		t.Fatal("o/app's files changed")
	}
}

// The commit exists here when only its push failed: the repos follow it, and the command still fails.
func TestAccessChangeRewrapsTheReposWhenThePushFails(t *testing.T) {
	f := newFake(t)
	x, app, web := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, []string{accessGene}, []string{accessGene})
	f.gitFail = pushFails
	out, _, err := accessRun(f, "user", "add", "sam", accessSam)
	var unpushed *unpushedError
	if !errors.As(err, &unpushed) || out != "✓ add sam\n"+reposRewrapped("o/app", app)+reposRewrapped("o/web", web) {
		t.Fatalf("got %v, stdout %q", err, out)
	}
	if got := x.subjects(); !slices.Equal(got, []string{"add sam"}) {
		t.Fatalf("commits %q", got)
	}
	reposFor(t, f, app, []string{"common.enc.yaml"}, accessGene, accessSam)
}

// A rotation of this machine's key re-wraps the repos before it returns, for the new key.
func TestUserRotateRewrapsTheRepos(t *testing.T) {
	f := newFake(t)
	x, app, web := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, []string{accessGene}, []string{accessGene})
	fresh := rotateNew(f, "")
	accessWant(t, f, "✓ rotate gene\n✓ pushed to "+x.bare+"\n"+reposRewrapped("o/app", app)+reposRewrapped("o/web", web)+"✓ new key in "+fresh+"\nBack it up. The old key in "+setupKeyFile(f)+" no longer opens the store; delete it when you are sure.\n", "user", "rotate")
	public, err := f.backend.PublicKey(fresh)
	if err != nil {
		t.Fatal(err)
	}
	reposFor(t, f, app, []string{"common.enc.yaml"}, public)
	reposFor(t, f, web, []string{"common.enc.yaml"}, public)
	noGitIn(t, f, filepath.Dir(app), filepath.Dir(web))
}

// A routine rotation lists no repo secret; a compromised key may have leaked them, so they are listed.
func TestUserRotateCompromisedListsTheReposSecrets(t *testing.T) {
	f := newFake(t)
	_, app, _ := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, []string{accessGene}, []string{accessGene})
	out, _, err := accessRun(f, "user", "rotate", "--compromised")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"o/app", "o/web"} {
		if want := "! " + id + ": common.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN.\n"; !strings.Contains(out, want) {
			t.Fatalf("stdout %q\nlacks  %q", out, want)
		}
	}
	public, err := f.backend.PublicKey(rotateNew(f, ""))
	if err != nil {
		t.Fatal(err)
	}
	reposFor(t, f, app, []string{"common.enc.yaml"}, public)
}

// A bot's repo lines go where the command's other lines go: with --print-private, stderr.
func TestBotAddPrintPrivateRewrapsTheReposOnStderr(t *testing.T) {
	f := newFake(t)
	x, app, web := reposFix(t, f, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, []string{accessGene}, []string{accessGene})
	out, errOut, err := accessRun(f, "bot", "add", "ci", "--print-private")
	if err != nil {
		t.Fatal(err)
	}
	if want := "✓ add bot ci\n✓ pushed to " + x.bare + "\n" + reposRewrapped("o/app", app) + reposRewrapped("o/web", web) + botNote; errOut != want {
		t.Fatalf("stderr %q\nwant   %q", errOut, want)
	}
	public := botPublic(t, out)
	reposFor(t, f, app, []string{"common.enc.yaml"}, accessGene, public)
}

// A bot's key goes only to a registered repo.
func TestBotAddTakesOnlyARegisteredRepo(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	registerRepos(t, x, "o/a")
	g := botFakeGh(f)
	accessUntouched(t, f, x, "o/unregistered is not in repos.yaml. Run ezc repo add o/unregistered first.", "bot", "add", "ci", "--repo", "o/a", "--repo", "o/unregistered")
	if len(g.calls) != 0 {
		t.Fatalf("gh ran: %+v", g.calls)
	}
	out, _, err := accessRun(f, "bot", "add", "ci", "--repo", "o/a")
	if want := "✓ add bot ci\n✓ pushed to " + x.bare + "\n" + noCheckoutHere("o/a") + "✓ EZC_AGE_KEY set in o/a\n" + botNote; err != nil || out != want {
		t.Fatalf("%v\nstdout %q\nwant   %q", err, out, want)
	}
	g.sets(t, "o/a")
}
