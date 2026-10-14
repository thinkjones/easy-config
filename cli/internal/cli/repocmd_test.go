package cli

import (
	"maps"
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
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/registry"
)

func TestGithubSlug(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/o/r.git":     "o/r",
		"https://github.com/o/r":         "o/r",
		"git@github.com:o/r":             "o/r",
		"git@github.com:o/r.git":         "o/r",
		"https://user:pw@github.com/o/r": "o/r",
		"ssh://git@github.com/o/r.git":   "o/r",
	} {
		if got, ok := githubSlug(remote); !ok || got != want {
			t.Errorf("%s: got %q, %v", remote, got, ok)
		}
	}
	for _, remote := range []string{"https://gitlab.com/o/r", "https://github.com/a/o/r", "/srv/git/o/r", "git@gitlab.com:o/r", "https://github.com/o", ""} {
		if got, ok := githubSlug(remote); ok {
			t.Errorf("%s: got %q", remote, got)
		}
	}
}

// repoFix is the store with a remote (storeWriteStore), and a working repo in the fake's working directory.
type repoFix struct {
	storeWriteFix
	f *fake
}

// repoStore is the store, gene's config, and a working repo whose origin is origin ("" for none).
func repoStore(t *testing.T, origin string) repoFix {
	t.Helper()
	f := newFake(t)
	x := repoFix{storeWriteStore(t, f), f}
	if err := config.Save(x.configPath(), config.Config{User: "gene"}); err != nil {
		t.Fatal(err)
	}
	repo(t, f, map[string]string{"manifest.yaml": empty})
	if origin != "" {
		g := gitstore.Git{Run: gittest.WithIdentity(t)}
		if err := g.Init(f.wd); err != nil {
			t.Fatal(err)
		}
		if err := g.SetRemote(f.wd, origin); err != nil {
			t.Fatal(err)
		}
	}
	return x
}

func (x repoFix) configPath() string { return filepath.Join(x.f.env["EZC_HOME"], "config.toml") }

func (x repoFix) config() config.Config {
	x.t.Helper()
	c, _, err := config.Load(x.configPath())
	if err != nil {
		x.t.Fatal(err)
	}
	return c
}

func (x repoFix) setConfig(c config.Config) {
	x.t.Helper()
	if err := config.Save(x.configPath(), c); err != nil {
		x.t.Fatal(err)
	}
}

func (x repoFix) repos() registry.Registry {
	x.t.Helper()
	r, _, err := registry.Load(filepath.Join(x.store, ezcDir))
	if err != nil {
		x.t.Fatal(err)
	}
	return r
}

// out runs a command that must succeed and returns its stdout.
func (x repoFix) out(args ...string) string {
	x.t.Helper()
	x.f.out.Reset()
	x.f.errOut.Reset()
	if err := x.f.run(args...); err != nil {
		x.t.Fatalf("%v: %v", args, err)
	}
	return x.f.out.String()
}

// unchanged fails unless the store's HEAD is head, the store has nothing uncommitted and the config is cfg.
func (x repoFix) unchanged(head, cfg string) {
	x.t.Helper()
	if got := x.git("rev-parse", "HEAD"); got != head {
		x.t.Errorf("a commit was made: %s", x.git("log", "-1", "--format=%s"))
	}
	if got := x.git("status", "--porcelain", "--untracked-files=all"); got != "" {
		x.t.Errorf("left in the store:\n%s", got)
	}
	if got := readFile(x.t, x.configPath()); got != cfg {
		x.t.Errorf("config.toml changed:\n%s", got)
	}
}

func TestRepoAddRegistersTheWorkingRepo(t *testing.T) {
	x := repoStore(t, "https://github.com/o/app.git")
	out := x.out("repo", "add")
	want := "✓ added o/app to repos.yaml\n✓ checkout " + x.f.wd + "\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if got := x.repos().Repos["o/app"]; got != (registry.Repo{Added: "2026-10-11", AddedBy: "gene"}) {
		t.Fatalf("entry %+v", got)
	}
	if got := x.config().Checkouts["o/app"]; got != x.f.wd {
		t.Fatalf("checkout %q", got)
	}
	x.wantCommit("repo add o/app", registry.File)

	// A second run registers nothing more.
	head := x.git("rev-parse", "HEAD")
	if out := x.out("repo", "add"); !strings.HasPrefix(out, "✓ o/app is registered\n") {
		t.Fatalf("second run %q", out)
	}
	if got := x.git("rev-parse", "HEAD"); got != head {
		t.Fatal("the second run committed")
	}
	x.wantClean()
}

func TestRepoAddAuthorIsEzcWithoutAUser(t *testing.T) {
	x := repoStore(t, "")
	x.setConfig(config.Config{})
	x.out("repo", "add")
	if got := x.repos().Repos[filepath.Base(x.f.wd)].AddedBy; got != "ezc" {
		t.Fatalf("addedBy %q", got)
	}
}

func TestRepoAddNames(t *testing.T) {
	// No origin: the folder's name.
	x := repoStore(t, "")
	id := filepath.Base(x.f.wd)
	if out := x.out("repo", "add"); !strings.Contains(out, "✓ added "+id+" to repos.yaml\n") {
		t.Fatalf("got %q", out)
	}
	x.wantCommit("repo add "+id, registry.File)

	// --name wins over the folder's name.
	x = repoStore(t, "")
	x.out("repo", "add", "--name", "x")
	if _, ok := x.repos().Repos["x"]; !ok || x.config().Checkouts["x"] != x.f.wd {
		t.Fatalf("registry %v, config %v", x.repos(), x.config())
	}

	// A non-GitHub origin gets a plain name.
	x = repoStore(t, "https://gitlab.com/o/app.git")
	x.out("repo", "add", "--name", "app")
	if _, ok := x.repos().Repos["app"]; !ok {
		t.Fatalf("registry %v", x.repos())
	}

	// An invalid --name is never echoed.
	x = repoStore(t, "")
	head, cfg := x.git("rev-parse", "HEAD"), readFile(t, x.configPath())
	err := x.f.run("repo", "add", "--name", "../x")
	wantUsage(t, err, "--name")
	if strings.Contains(err.Error(), "../x") {
		t.Fatalf("echoed: %v", err)
	}
	x.unchanged(head, cfg)
}

func TestRepoAddAnotherID(t *testing.T) {
	x := repoStore(t, "https://github.com/o/app.git")
	if out := x.out("repo", "add", "o/other"); out != "✓ added o/other to repos.yaml\n" {
		t.Fatalf("got %q", out)
	}
	if len(x.config().Checkouts) != 0 {
		t.Fatalf("checkouts %v", x.config().Checkouts)
	}
	x.wantCommit("repo add o/other", registry.File)

	// The working repo's own ID records its checkout.
	x = repoStore(t, "https://github.com/o/app.git")
	x.out("repo", "add", "o/app")
	if got := x.config().Checkouts["o/app"]; got != x.f.wd {
		t.Fatalf("checkout %q", got)
	}

	// An invalid ID is never echoed.
	x = repoStore(t, "")
	head, cfg := x.git("rev-parse", "HEAD"), readFile(t, x.configPath())
	err := x.f.run("repo", "add", "a/b/\x1b[31m")
	wantUsage(t, err, "repo ID")
	if strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("echoed: %q", err)
	}
	x.unchanged(head, cfg)
}

func TestRepoAddKeepsALiveCheckout(t *testing.T) {
	x := repoStore(t, "https://github.com/o/app.git")
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, ezcDir, "manifest.yaml"), empty)
	x.setConfig(config.Config{User: "gene", Checkouts: map[string]string{"o/app": elsewhere}})
	out := x.out("repo", "add")
	if !strings.Contains(out, "! ") || !strings.Contains(out, elsewhere) || strings.Contains(out, "✓ checkout") {
		t.Fatalf("got %q", out)
	}
	if got := x.config().Checkouts["o/app"]; got != elsewhere {
		t.Fatalf("checkout %q", got)
	}

	// One whose folder no longer holds .ezc is replaced.
	x = repoStore(t, "https://github.com/o/app.git")
	x.setConfig(config.Config{User: "gene", Checkouts: map[string]string{"o/app": filepath.Join(t.TempDir(), "gone")}})
	if out := x.out("repo", "add"); !strings.Contains(out, "✓ checkout "+x.f.wd+"\n") {
		t.Fatalf("got %q", out)
	}
	if got := x.config().Checkouts["o/app"]; got != x.f.wd {
		t.Fatalf("checkout %q", got)
	}
}

func TestRepoAddRefused(t *testing.T) {
	cases := []struct {
		name  string
		setup func(x repoFix)
		want  string
	}{
		{"in the store's own folder", func(x repoFix) { x.f.wd = x.store }, "the store's own"},
		{"behind", func(x repoFix) { x.theirs(2) }, storeWriteBehind},
		{"repos.yaml changed by hand", func(x repoFix) {
			x.commit(x.store, registry.File, "repos: {}\n")
			x.push(x.store)
			write(t, x.file(registry.File), "repos:\n  o/hand: {added: 2026-10-01, addedBy: gene}\n")
		}, "repos.yaml has uncommitted changes"},
		{"outside a repo", func(x repoFix) { x.f.wd = t.TempDir() }, "not in an ezc repo"},
	}
	for _, c := range cases {
		x := repoStore(t, "https://github.com/o/app.git")
		c.setup(x)
		head, cfg := x.git("rev-parse", "HEAD"), readFile(t, x.configPath())
		status := x.git("status", "--porcelain", "--untracked-files=all")
		x.f.out.Reset()
		err := x.f.run("repo", "add")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v", c.name, err)
			continue
		}
		if strings.Contains(x.f.out.String(), "✓") {
			t.Errorf("%s: printed %q", c.name, x.f.out.String())
		}
		if got := x.git("rev-parse", "HEAD"); got != head {
			t.Errorf("%s: a commit was made", c.name)
		}
		if got := x.git("status", "--porcelain", "--untracked-files=all"); got != status {
			t.Errorf("%s: the store changed:\n%s", c.name, got)
		}
		if got := readFile(t, x.configPath()); got != cfg {
			t.Errorf("%s: config.toml changed", c.name)
		}
	}
}

// registered is repoStore with o/app registered and checked out, and the registration pushed.
func registered(t *testing.T) repoFix {
	t.Helper()
	x := repoStore(t, "https://github.com/o/app.git")
	x.out("repo", "add")
	x.storeWriteFix = x.published()
	return x
}

func TestRepoRemove(t *testing.T) {
	x := registered(t)
	out := x.out("repo", "remove", "o/app")
	if !strings.HasPrefix(out, "✓ removed o/app from repos.yaml\n") {
		t.Fatalf("got %q", out)
	}
	if _, ok := x.repos().Repos["o/app"]; ok {
		t.Fatal("still registered")
	}
	if _, ok := x.config().Checkouts["o/app"]; ok {
		t.Fatal("the checkout is still recorded")
	}
	if got := x.config().User; got != "gene" {
		t.Fatalf("user %q", got)
	}
	x.wantCommit("repo remove o/app", registry.File)
}

func TestRepoRemoveRefused(t *testing.T) {
	x := registered(t)
	head, cfg := x.git("rev-parse", "HEAD"), readFile(t, x.configPath())
	err := x.f.run("repo", "remove", "o/nope")
	if err == nil || err.Error() != "o/nope is not in repos.yaml." {
		t.Fatalf("got %v", err)
	}
	storeWriteExitsOne(t, err)
	x.unchanged(head, cfg)

	err = x.f.run("repo", "remove", "../\x1b[31m")
	wantUsage(t, err, "repo ID")
	if strings.Contains(err.Error(), "\x1b") || strings.Contains(err.Error(), "../") {
		t.Fatalf("echoed: %q", err)
	}
	x.unchanged(head, cfg)

	// The last fetch was a moment ago; ten minutes on, the command fetches again and learns it is behind.
	x.theirs(1)
	x.f.now = time.Date(2026, 10, 11, 12, 30, 0, 0, time.UTC)
	head = x.git("rev-parse", "HEAD")
	err = x.f.run("repo", "remove", "o/app")
	if want := strings.Replace(storeWriteBehind, "2 commits", "1 commit", 1); err == nil || err.Error() != want {
		t.Fatalf("behind: got %v", err)
	}
	x.unchanged(head, cfg)
}

func TestRepoRemoveRefusedWhileABotNamesIt(t *testing.T) {
	x := registered(t)
	reg := recipients.Registry{Recipients: map[string]recipients.Recipient{
		"gene": {Key: accessGene, Kind: recipients.KindUser, Added: "2026-10-01", AddedBy: "gene"},
		"ci":   {Key: accessSam, Kind: recipients.KindBot, Added: "2026-10-01", AddedBy: "gene", Repos: []string{"o/app"}},
	}}
	if err := reg.Save(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	x.commit(x.store, recipients.File, readFile(t, x.file(recipients.File)))
	x.push(x.store)
	head, cfg := x.git("rev-parse", "HEAD"), readFile(t, x.configPath())
	err := x.f.run("repo", "remove", "o/app")
	if err == nil || !strings.Contains(err.Error(), "ci") || !strings.Contains(err.Error(), "ezc bot remove ci") {
		t.Fatalf("got %v", err)
	}
	storeWriteExitsOne(t, err)
	x.unchanged(head, cfg)
}

func TestRepoList(t *testing.T) {
	x := repoStore(t, "https://github.com/o/app.git")
	if out := x.out("repo", "list"); out != "No repos are registered. Run ezc repo add inside a repo.\n" {
		t.Fatalf("empty: %q", out)
	}
	x.out("repo", "add")
	x.out("repo", "add", "o/other")
	reg := recipients.Registry{Recipients: map[string]recipients.Recipient{
		"gene": {Key: accessGene, Kind: recipients.KindUser, Added: "2026-10-01", AddedBy: "gene"},
		"ci":   {Key: accessSam, Kind: recipients.KindBot, Added: "2026-10-01", AddedBy: "gene", Repos: []string{"o/app", "o/third"}},
	}}
	if err := reg.Save(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	want := "o/app  added 2026-10-11 by gene  " + x.f.wd + "\n  bot ci\n" +
		"o/other  added 2026-10-11 by gene  no checkout here\n"
	if out := x.out("repo", "list"); out != want {
		t.Fatalf("got %q, want %q", out, want)
	}

	// A checkout that lost its .ezc is no checkout, outside the repo.
	x.f.wd = t.TempDir()
	if err := os.RemoveAll(filepath.Join(x.config().Checkouts["o/app"], ezcDir)); err != nil {
		t.Fatal(err)
	}
	if out := x.out("repo", "list"); !strings.HasPrefix(out, "o/app  added 2026-10-11 by gene  no checkout here\n") {
		t.Fatalf("got %q", out)
	}
}

func TestRepoListNeverPrintsAnEscape(t *testing.T) {
	x := repoStore(t, "")
	write(t, x.file(registry.File), "repos:\n  \"a\\e[31m\": {added: 2026-10-01, addedBy: gene}\n")
	err := x.f.run("repo", "list")
	if err == nil {
		t.Fatal("listed")
	}
	if strings.Contains(err.Error(), "\x1b") || strings.Contains(x.f.out.String(), "\x1b") {
		t.Fatalf("echoed: %q", err)
	}

	// added and addedBy are printed through Printable.
	write(t, x.file(registry.File), "repos:\n  o/app: {added: 2026-10-01, addedBy: \"g\\e[31m\"}\n")
	if out := x.out("repo", "list"); strings.Contains(out, "\x1b") {
		t.Fatalf("got %q", out)
	}
}

// checkoutOfRepo is a folder holding .ezc with manifest mToken and the given values; secret goes in common.enc.yaml
// (encrypted for keys) when it is not empty.
func checkoutOfRepo(t *testing.T, f *fake, values, secret string, keys ...string) string {
	t.Helper()
	top := t.TempDir()
	dir := filepath.Join(top, ezcDir)
	write(t, filepath.Join(dir, "manifest.yaml"), mToken)
	write(t, filepath.Join(dir, "env.local.yaml"), values)
	if secret != "" {
		if err := f.backend.WritePolicy(dir, keys); err != nil {
			t.Fatal(err)
		}
		if err := f.backend.Set(filepath.Join(dir, "common.enc.yaml"), "TOKEN", secret); err != nil {
			t.Fatal(err)
		}
	}
	return top
}

// checkFix is a store whose registry names gene, three registered repos and a checkout for two.
func checkFix(t *testing.T) (repoFix, string, string) {
	t.Helper()
	x := repoStore(t, "")
	good := checkoutOfRepo(t, x.f, "NAME: v4lue-good\nSIZE: 3\n", "s3cr3t-good", accessGene)
	bad := checkoutOfRepo(t, x.f, "NAME: v4lue-bad\n", "s3cr3t-bad", accessGene)
	for _, id := range []string{"o/bad", "o/good", "o/gone"} {
		x.out("repo", "add", id)
	}
	if err := (recipients.Registry{Recipients: map[string]recipients.Recipient{"gene": accessEntry(accessGene)}}).Save(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	x.setConfig(config.Config{User: "gene", Checkouts: map[string]string{"o/good": good, "o/bad": bad}})
	return x, good, bad
}

func noValues(t *testing.T, x repoFix, out string) {
	t.Helper()
	for _, v := range []string{"v4lue-good", "v4lue-bad", "s3cr3t-good", "s3cr3t-bad"} {
		if strings.Contains(out, v) || strings.Contains(x.f.errOut.String(), v) {
			t.Fatalf("a value is in the output: %s", v)
		}
	}
}

func TestRepoCheckAll(t *testing.T) {
	x, good, bad := checkFix(t)
	err := x.f.run("repo", "check", "--all")
	wantExit(t, err, 1)
	out := x.f.out.String()
	noValues(t, x, out)
	for _, want := range []string{
		"o/bad (" + bad + ")\n  environment local: validation failed\n",
		"o/good (" + good + ")\n  environment local ✓",
		"o/gone: no checkout on this machine. Run ezc repo add inside it.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "2 checked, 1 failed, 1 with no checkout here\n") {
		t.Errorf("last line of\n%s", out)
	}
}

func TestRepoCheckOneAndTheWorkingRepo(t *testing.T) {
	x, good, _ := checkFix(t)
	write(t, filepath.Join(good, ezcDir, "env.prod.yaml"), "NAME: v4lue-good\nSIZE: 4\n")
	out := x.out("repo", "check", "o/good", "prod")
	if !strings.Contains(out, "o/good ("+good+")\n  ") || strings.Contains(out, "o/bad") || strings.Contains(out, "checked,") {
		t.Fatalf("got %q", out)
	}
	if !strings.Contains(out, "environment prod") {
		t.Fatalf("env not used: %q", out)
	}
	// . is the working repo too, so that an environment can follow it.
	write(t, filepath.Join(x.f.wd, ezcDir, "env.prod.yaml"), "NAME: v4lue-good\nSIZE: 4\n")
	x.f.out.Reset()
	if err := x.f.run("repo", "check", ".", "prod"); err != nil {
		t.Fatal(err)
	}
	if out := x.f.out.String(); !strings.HasPrefix(out, filepath.Base(x.f.wd)+" ("+x.f.wd+")\n  ") || !strings.Contains(out, "environment prod") {
		t.Fatalf("got %q", out)
	}
	// No argument: the working repo, which is not registered.
	x.f.out.Reset()
	if err := x.f.run("repo", "check"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(x.f.out.String(), filepath.Base(x.f.wd)+" ("+x.f.wd+")\n  ") {
		t.Fatalf("got %q", x.f.out.String())
	}
}

func TestRepoCheckEncryptedFilesAgainstTheRegistry(t *testing.T) {
	x := repoStore(t, "")
	stale := checkoutOfRepo(t, x.f, "NAME: v4lue-good\nSIZE: 3\n", "s3cr3t-good", accessGene, accessStray)
	x.out("repo", "add", "o/stale")
	if err := (recipients.Registry{Recipients: map[string]recipients.Recipient{"gene": accessEntry(accessGene)}}).Save(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	x.setConfig(config.Config{User: "gene", Checkouts: map[string]string{"o/stale": stale}})
	err := x.f.run("repo", "check", "o/stale")
	wantExit(t, err, 1)
	out := x.f.out.String()
	noValues(t, x, out)
	if !strings.Contains(out, "  ✗ common.enc.yaml is not encrypted for exactly the store's recipients. Run ezc repo rewrap o/stale.\n") {
		t.Fatalf("got %q", out)
	}
}

func TestRepoCheckRefusals(t *testing.T) {
	x, _, _ := checkFix(t)
	err := x.f.run("repo", "check", "o/never")
	if err == nil || err.Error() != "o/never is not in repos.yaml." {
		t.Fatalf("got %v", err)
	}
	err = x.f.run("repo", "check", "a/b/\x1b[31m")
	wantUsage(t, err, "repo ID")
	if strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("echoed: %q", err)
	}
	wantUsage(t, x.f.run("repo", "check", "--all", "o/good", "prod"), "--all")
	// An environment name is checked as ezc env checks it, and never echoed.
	for _, args := range [][]string{{"--all", "pr\x1bod"}, {"o/good", "pr\x1bod"}, {".", "enc"}} {
		err := x.f.run(append([]string{"repo", "check"}, args...)...)
		wantUsage(t, err, "ezc repo check: ")
		if strings.Contains(err.Error(), "\x1b") {
			t.Fatalf("echoed: %q", err)
		}
	}
	// A recorded checkout whose folder is gone is no checkout.
	x.setConfig(config.Config{User: "gene", Checkouts: map[string]string{"o/good": filepath.Join(t.TempDir(), "gone")}})
	x.f.out.Reset()
	if err := x.f.run("repo", "check", "o/good"); err != nil || x.f.out.String() != "o/good: no checkout on this machine. Run ezc repo add inside it.\n" {
		t.Fatalf("got %v, %q", err, x.f.out.String())
	}
}

func TestRepoUses(t *testing.T) {
	x := repoStore(t, "")
	opt := "schemaVersion: v1\nrequired: {}\noptional:\n  API_URL: {type: str, desc: u}\n"
	req := "schemaVersion: v1\nrequired:\n  API_URL: {type: str, desc: u}\n"
	dirs := map[string]string{}
	for id, m := range map[string]string{"a": req, "b": req, "c": opt, "e": empty} {
		top := t.TempDir()
		write(t, filepath.Join(top, ezcDir, "manifest.yaml"), m)
		dirs[id] = top
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		x.out("repo", "add", id)
	}
	x.setConfig(config.Config{User: "gene", Checkouts: dirs})
	want := "required by: a, b\noptional in: c\nnot checked (no checkout here): d\n"
	if out := x.out("repo", "uses", "API_URL"); out != want {
		t.Fatalf("got %q", out)
	}
	if out := x.out("repo", "uses", "OTHER"); out != "No registered repo with a checkout here uses OTHER.\nnot checked (no checkout here): d\n" {
		t.Fatalf("got %q", out)
	}
	err := x.f.run("repo", "uses", "1\x1b[31m")
	wantUsage(t, err, "variable name")
	if strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("echoed: %q", err)
	}
}

const rewrapValue = "s3cr3t-rewrap-value"

// rewrapStore is gene's machine and a store whose registry is reg, with its files encrypted for storeKeys, and o/app
// and o/gone registered.
func rewrapStore(t *testing.T, reg map[string]recipients.Recipient, storeKeys ...string) (*fake, storeWriteFix) {
	t.Helper()
	f := newFake(t)
	x := accessStoreWith(t, f, reg, storeKeys...)
	repos := registry.Registry{Repos: map[string]registry.Repo{
		"o/app":  {Added: "2026-10-01", AddedBy: "gene"},
		"o/gone": {Added: "2026-10-01", AddedBy: "gene"},
	}}
	if err := repos.Save(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	if done, err := x.g.Commit(x.store, "repos", gitstore.Author{}, ezcDir+"/"+registry.File); err != nil || !done {
		t.Fatalf("commit: %v, %v", done, err)
	}
	x.push(x.store)
	return f, x
}

// rewrapCheckout is o/app's checkout: a .ezc whose files each hold API_TOKEN encrypted for keys (none: no policy and
// no encrypted file), recorded in config.toml.
func rewrapCheckout(t *testing.T, f *fake, keys []string, files ...string) string {
	t.Helper()
	top := t.TempDir()
	dir := filepath.Join(top, ezcDir)
	write(t, filepath.Join(dir, manifestName), mToken)
	if len(keys) > 0 {
		if err := f.backend.WritePolicy(dir, keys); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		if err := f.backend.Set(filepath.Join(dir, file), "API_TOKEN", rewrapValue); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Save(setupConfig(f), config.Config{User: "gene", Checkouts: map[string]string{"o/app": top}}); err != nil {
		t.Fatal(err)
	}
	return top
}

// folderBytes is every file of dir and its content.
func folderBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range ents {
		m[e.Name()] = readFile(t, filepath.Join(dir, e.Name()))
	}
	return m
}

// rewrapWant fails unless the command succeeded, printing exactly stdout and stderr.
func rewrapWant(t *testing.T, f *fake, stdout, stderr string, args ...string) {
	t.Helper()
	out, errOut, err := accessRun(f, args...)
	if err != nil || out != stdout || errOut != stderr {
		t.Fatalf("%v: %v\nstdout %q\nwant   %q\nstderr %q\nwant   %q", args, err, out, stdout, errOut, stderr)
	}
}

// noGitIn fails when a git command ran in one of the folders, or below it.
func noGitIn(t *testing.T, f *fake, dirs ...string) {
	t.Helper()
	for _, c := range f.gits {
		for _, dir := range dirs {
			if c.Dir == dir || strings.HasPrefix(c.Dir, dir+string(filepath.Separator)) {
				t.Errorf("git ran in %s: %v", c.Dir, c.Args)
			}
		}
	}
}

func TestRepoRewrap(t *testing.T) {
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	f, _ := rewrapStore(t, reg, accessGene, accessSam)
	top := rewrapCheckout(t, f, []string{accessGene}, "common.enc.yaml")
	ezc := filepath.Join(top, ezcDir)

	rewrapWant(t, f, "✓ o/app: re-wrapped common.enc.yaml in "+ezc+". Commit it there.\n", "! sam gain access to o/app's secrets.\n", "repo", "rewrap", "o/app", "--yes")
	noGitIn(t, f, top)
	got, err := f.backend.Recipients(filepath.Join(ezc, "common.enc.yaml"))
	if err != nil || !slices.Equal(got, []string{accessGene, accessSam}) {
		t.Fatalf("recipients = %v, %v", got, err)
	}
	if keys, err := f.backend.PolicyRecipients(filepath.Join(ezc, "common.enc.yaml")); err != nil || !slices.Equal(keys, []string{accessGene, accessSam}) {
		t.Fatalf("policy = %v, %v", keys, err)
	}

	before := folderBytes(t, ezc)
	accessWant(t, f, "✓ o/app: already encrypted for the store's recipients\n", "repo", "rewrap", "o/app")
	noGitIn(t, f, top)
	if after := folderBytes(t, ezc); !maps.Equal(before, after) {
		t.Errorf("a second run changed the checkout")
	}
}

func TestRepoRewrapSkipsARepoWithNoSecrets(t *testing.T) {
	f, _ := rewrapStore(t, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
	top := rewrapCheckout(t, f, nil)
	before := folderBytes(t, filepath.Join(top, ezcDir))
	accessWant(t, f, "- o/app: no secrets to re-wrap\n", "repo", "rewrap", "o/app")
	if after := folderBytes(t, filepath.Join(top, ezcDir)); !maps.Equal(before, after) {
		t.Errorf("wrote to the checkout:\n%v", after)
	}
}

func TestRepoRewrapNamesWhatAKeyThatLeftCouldRead(t *testing.T) {
	f, _ := rewrapStore(t, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
	top := rewrapCheckout(t, f, []string{accessGene, accessStray}, "common.enc.yaml", "env.prod.enc.yaml")
	ezc := filepath.Join(top, ezcDir)
	want := "✓ o/app: re-wrapped common.enc.yaml, env.prod.enc.yaml in " + ezc + ". Commit them there.\n" +
		"! o/app: common.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN.\n" +
		"! o/app: env.prod.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN --env prod.\n"
	out, errOut, err := accessRun(f, "repo", "rewrap", "o/app")
	if err != nil || out != want {
		t.Fatalf("%v\ngot  %q\nwant %q\nstderr %q", err, out, want, errOut)
	}
	if strings.Contains(out+errOut, accessStray) || strings.Contains(out+errOut, rewrapValue) {
		t.Fatalf("a key or a value was printed:\n%s%s", out, errOut)
	}
	for _, file := range []string{"common.enc.yaml", "env.prod.enc.yaml"} {
		if got, _ := f.backend.Recipients(filepath.Join(ezc, file)); !slices.Equal(got, []string{accessGene}) {
			t.Errorf("%s recipients = %v", file, got)
		}
	}
}

func TestRepoRewrapRefusedUntilTheStoreAgrees(t *testing.T) {
	// recipients.yaml names sam, and no file of the store is encrypted for sam.
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	f, x := rewrapStore(t, reg, accessGene)
	top := rewrapCheckout(t, f, []string{accessGene}, "common.enc.yaml")
	before := folderBytes(t, filepath.Join(top, ezcDir))
	_, _, err := accessRun(f, "repo", "rewrap", "o/app")
	if err == nil || !strings.HasSuffix(err.Error(), "Run ezc store rewrap first.") {
		t.Fatalf("got %v", err)
	}
	if after := folderBytes(t, filepath.Join(top, ezcDir)); !maps.Equal(before, after) {
		t.Errorf("wrote to the checkout")
	}
	noGitIn(t, f, top)

	// A store with no registry: nobody said who the secrets are for.
	if err := os.Remove(filepath.Join(x.store, ezcDir, recipients.File)); err != nil {
		t.Fatal(err)
	}
	_, _, err = accessRun(f, "repo", "rewrap", "o/app")
	if err == nil || !strings.Contains(err.Error(), "has no recipients.yaml") {
		t.Fatalf("got %v", err)
	}
	if after := folderBytes(t, filepath.Join(top, ezcDir)); !maps.Equal(before, after) {
		t.Errorf("wrote to the checkout")
	}
}

func TestRepoRewrapAFileThisKeyCannotOpen(t *testing.T) {
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	f, _ := rewrapStore(t, reg, accessGene, accessSam)
	top := rewrapCheckout(t, f, []string{accessSam}, "common.enc.yaml")
	before := folderBytes(t, filepath.Join(top, ezcDir))
	f.backend.Locked = true
	out, _, err := accessRun(f, "repo", "rewrap", "o/app", "--yes")
	wantExit(t, err, 1)
	if !strings.HasPrefix(out, "✗ o/app could not be re-wrapped (") || !strings.HasSuffix(out, "cannot decrypt). Nothing was changed there.\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("got %q", out)
	}
	if after := folderBytes(t, filepath.Join(top, ezcDir)); !maps.Equal(before, after) {
		t.Errorf("the checkout changed:\nbefore %v\nafter  %v", before, after)
	}
	noGitIn(t, f, top)
}

func TestRepoRewrapAll(t *testing.T) {
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	f, _ := rewrapStore(t, reg, accessGene, accessSam)
	top := rewrapCheckout(t, f, []string{accessGene}, "common.enc.yaml")
	want := "✓ o/app: re-wrapped common.enc.yaml in " + filepath.Join(top, ezcDir) + ". Commit it there.\n" +
		"- o/gone: no checkout on this machine. Run ezc repo add inside it, then run this again.\n"
	rewrapWant(t, f, want, "! sam gain access to o/app's secrets.\n", "repo", "rewrap", "--all", "--yes")
	noGitIn(t, f, top)

	wantUsage(t, f.run("repo", "rewrap", "--all", "o/app"), "--all")
	err := f.run("repo", "rewrap", "a/b/\x1b[31m")
	wantUsage(t, err, "repo ID")
	if strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("echoed: %q", err)
	}
}

func TestRepoRewrapRefusedWhileTheStorePolicyDiffers(t *testing.T) {
	// No encrypted file in the store: only its policy says who recipients.yaml was let in for.
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	f, x := rewrapStore(t, reg, accessGene)
	storeEzc := filepath.Join(x.store, ezcDir)
	for _, file := range accessEncFiles {
		if err := os.Remove(filepath.Join(storeEzc, file)); err != nil {
			t.Fatal(err)
		}
	}
	if done, err := x.g.Commit(x.store, "no secrets", gitstore.Author{}, ezcDir); err != nil || !done {
		t.Fatalf("commit: %v, %v", done, err)
	}
	x.push(x.store)
	top := rewrapCheckout(t, f, []string{accessGene}, "common.enc.yaml")
	before := folderBytes(t, filepath.Join(top, ezcDir))
	_, _, err := accessRun(f, "repo", "rewrap", "o/app", "--yes")
	if err == nil || !strings.HasSuffix(err.Error(), "Run ezc store rewrap first.") {
		t.Fatalf("got %v", err)
	}
	if after := folderBytes(t, filepath.Join(top, ezcDir)); !maps.Equal(before, after) {
		t.Errorf("wrote to the checkout")
	}
	// A store with no policy at all is refused too.
	if err := os.Remove(filepath.Join(storeEzc, backend.FakePolicy)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := accessRun(f, "repo", "rewrap", "o/app", "--yes"); err == nil || !strings.HasSuffix(err.Error(), "Run ezc store rewrap first.") {
		t.Fatalf("no policy: got %v", err)
	}
	if err := f.backend.WritePolicy(storeEzc, []string{accessGene, accessSam}); err != nil {
		t.Fatal(err)
	}
	if done, err := x.g.Commit(x.store, "policy", gitstore.Author{}, ezcDir); err != nil || !done {
		t.Fatalf("commit: %v, %v", done, err)
	}
	x.push(x.store)
	rewrapWant(t, f, "✓ o/app: re-wrapped common.enc.yaml in "+filepath.Join(top, ezcDir)+". Commit it there.\n", "! sam gain access to o/app's secrets.\n", "repo", "rewrap", "o/app", "--yes")
}

// gainFix is a store whose registry names gene and sam, and o/app's checkout encrypted for gene only: sam gains.
func gainFix(t *testing.T) (*fake, string) {
	t.Helper()
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	f, _ := rewrapStore(t, reg, accessGene, accessSam)
	top := rewrapCheckout(t, f, []string{accessGene}, "common.enc.yaml")
	return f, filepath.Join(top, ezcDir)
}

func TestRepoRewrapAsksBeforeLettingANameIn(t *testing.T) {
	f, ezc := gainFix(t)
	f.tty = true
	f.stdin = "y\n"
	out, errOut, err := accessRun(f, "repo", "rewrap", "o/app")
	if err != nil || out != "✓ o/app: re-wrapped common.enc.yaml in "+ezc+". Commit it there.\n" {
		t.Fatalf("%v\n%q", err, out)
	}
	if errOut != "! sam gain access to o/app's secrets.\nLet sam read o/app's secrets? [y/N] " {
		t.Fatalf("stderr %q", errOut)
	}
	if got, _ := f.backend.Recipients(filepath.Join(ezc, "common.enc.yaml")); !slices.Equal(got, []string{accessGene, accessSam}) {
		t.Fatalf("recipients = %v", got)
	}
}

func TestRepoRewrapNotLetIn(t *testing.T) {
	f, ezc := gainFix(t)
	before := folderBytes(t, ezc)
	f.tty = true
	f.stdin = "n\n"
	out, _, err := accessRun(f, "repo", "rewrap", "o/app")
	wantExit(t, err, 1)
	if out != "Not re-wrapped.\n" {
		t.Fatalf("got %q", out)
	}
	if after := folderBytes(t, ezc); !maps.Equal(before, after) {
		t.Errorf("wrote to the checkout")
	}
}

func TestRepoRewrapWithoutATerminal(t *testing.T) {
	f, ezc := gainFix(t)
	before := folderBytes(t, ezc)
	_, _, err := accessRun(f, "repo", "rewrap", "o/app")
	wantUsage(t, err, "ezc user list")
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("got %v", err)
	}
	if after := folderBytes(t, ezc); !maps.Equal(before, after) {
		t.Errorf("wrote to the checkout")
	}
	// --yes lets them in without a question.
	out, errOut, err := accessRun(f, "repo", "rewrap", "o/app", "--yes")
	if err != nil || out != "✓ o/app: re-wrapped common.enc.yaml in "+ezc+". Commit it there.\n" || errOut != "! sam gain access to o/app's secrets.\n" {
		t.Fatalf("%v\n%q\n%q", err, out, errOut)
	}
}

func TestRepoRewrapAsksNothingWhenNobodyGains(t *testing.T) {
	// Only a key leaves: nobody is let in, so there is no question, terminal or not.
	f, _ := rewrapStore(t, map[string]recipients.Recipient{"gene": accessEntry(accessGene)}, accessGene)
	rewrapCheckout(t, f, []string{accessGene, accessStray}, "common.enc.yaml")
	out, errOut, err := accessRun(f, "repo", "rewrap", "o/app")
	if err != nil || !strings.HasPrefix(out, "✓ o/app: re-wrapped common.enc.yaml") || errOut != "" {
		t.Fatalf("%v\n%q\n%q", err, out, errOut)
	}
}
