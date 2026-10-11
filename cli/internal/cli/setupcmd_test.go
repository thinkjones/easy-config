package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const (
	setupBackupNotice = "  Back this file up now. Without it your secrets cannot be read, and it cannot be recovered.\n"
	setupNextLine     = "\nNext: ezc set -g NAME --type secret --desc \"what it is\"\n"
)

// setupFake is a fresh machine with git on PATH and a login name. When its test ends, nothing it printed may hold the private key.
func setupFake(t *testing.T) *fake {
	t.Helper()
	f := newFake(t)
	f.paths = map[string]string{"git": "/usr/bin/git"}
	f.env["USER"] = "Gene"
	t.Cleanup(func() { setupNoPrivateKey(t, f) })
	return f
}

func setupKeyFile(f *fake) string {
	return filepath.Join(f.env["HOME"], ".config", "sops", "age", "keys.txt")
}
func setupStore(f *fake) string  { return filepath.Join(f.env["EZC_HOME"], "store") }
func setupConfig(f *fake) string { return filepath.Join(f.env["EZC_HOME"], "config.toml") }

// setupNoPrivateKey fails when stdout or stderr holds a line of the key file that is not a comment.
func setupNoPrivateKey(t *testing.T, f *fake) {
	t.Helper()
	data, err := os.ReadFile(setupKeyFile(f))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(f.out.String(), line) || strings.Contains(f.errOut.String(), line) {
			t.Errorf("the private key was printed")
		}
	}
}

// setupFreshOutput is the report of setup on a machine with nothing, making a local store.
func setupFreshOutput(f *fake) string {
	return "✓ git\n✓ fake\n" +
		"+ created your key: ~/.config/sops/age/keys.txt\n" + setupBackupNotice +
		"+ created the store: " + setupStore(f) + "\n" +
		"+ wrote " + setupConfig(f) + "\n" + setupNextLine
}

// setupPublicKey is the public half of the machine's key.
func setupPublicKey(t *testing.T, f *fake) string {
	t.Helper()
	public, err := f.backend.PublicKey(setupKeyFile(f))
	if err != nil {
		t.Fatal(err)
	}
	return public
}

// setupGitOut runs git in the store and returns its trimmed stdout.
func setupGitOut(t *testing.T, f *fake, args ...string) string {
	t.Helper()
	out, stderr, err := f.runGit(proc.Command{Dir: setupStore(f), Name: "git", Args: args})
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// setupSnapshot is every file under the dirs, and the files named, by path.
func setupSnapshot(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			files[path] = string(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// setupFakeGh answers `gh repo create` by pushing the store to a new bare repo, and `gh repo clone` by cloning from.
// It returns the bare repo `create` pushes to.
func setupFakeGh(t *testing.T, f *fake, from string) string {
	t.Helper()
	f.paths["gh"] = "/usr/bin/gh"
	bare := gittest.Bare(t)
	g := gitstore.Git{Run: f.runGit}
	f.execFn = func(c proc.Command) ([]byte, []byte, error) {
		if c.Name != "gh" || len(c.Args) < 2 {
			return nil, nil, errors.New("unexpected program")
		}
		switch c.Args[1] {
		case "create":
			if err := g.SetRemote(setupStore(f), bare); err != nil {
				return nil, nil, err
			}
			_, err := g.Push(setupStore(f))
			return nil, nil, err
		case "clone":
			return nil, nil, g.Clone(from, c.Args[len(c.Args)-1])
		}
		return nil, nil, errors.New("unexpected gh command")
	}
	return bare
}

// setupRemote is a bare repo holding a store whose registry names key as gene; it returns the bare repo.
func setupRemote(t *testing.T, key string) string {
	t.Helper()
	g := gitstore.Git{Run: gittest.WithIdentity(t)}
	bare, work := gittest.Bare(t), filepath.Join(t.TempDir(), "work")
	if err := g.Init(work); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(work, ".ezc", "manifest.yaml"), "schemaVersion: v1\nrequired: {}\noptional: {}\n")
	reg := recipients.Registry{Recipients: map[string]recipients.Recipient{"gene": {Key: key, Kind: "user", Added: "2026-10-01", AddedBy: "gene"}}}
	if err := reg.Save(filepath.Join(work, ".ezc")); err != nil {
		t.Fatal(err)
	}
	if err := (&backend.Fake{}).WritePolicy(filepath.Join(work, ".ezc"), reg.Keys()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(work, "a store", gitstore.Author{}, ".ezc"); err != nil {
		t.Fatal(err)
	}
	if err := g.SetRemote(work, bare); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Push(work); err != nil {
		t.Fatal(err)
	}
	return bare
}

// setupOwnKey writes a key file before setup runs and returns its bytes and its public half.
func setupOwnKey(t *testing.T, f *fake) (content, public string) {
	t.Helper()
	public, private, err := f.backend.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	content = "# made before ezc\n" + string(private)
	if err := os.MkdirAll(filepath.Dir(setupKeyFile(f)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setupKeyFile(f), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return content, public
}

// setupNothingMade fails when setup left a key, a store or a config.
func setupNothingMade(t *testing.T, f *fake) {
	t.Helper()
	for _, path := range []string{setupKeyFile(f), setupStore(f), setupConfig(f)} {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("%s was created", path)
		}
	}
}

func TestSetupNewLocal(t *testing.T) {
	f := setupFake(t)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != setupFreshOutput(f) {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, setupFreshOutput(f))
	}
	if f.errOut.Len() != 0 {
		t.Fatalf("stderr: %s", f.errOut.String())
	}
	c, found, err := config.Load(setupConfig(f))
	want := config.Config{User: "gene", Backend: "fake", KeyFile: setupKeyFile(f)}
	if err != nil || !found || !reflect.DeepEqual(c, want) {
		t.Fatalf("config %+v (found %v, err %v), want %+v", c, found, err, want)
	}

	f.out.Reset()
	f.stdin = "s3cr3t\n"
	if err := f.run("set", "-g", "TOKEN", "--type", "secret", "--desc", "d"); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	if err := f.run("get", "TOKEN", "--reveal"); err != nil || f.out.String() != "s3cr3t\n" {
		t.Fatalf("get: %v, %q", err, f.out.String())
	}
	f.out.Reset()
	if err := f.run("doctor"); err != nil {
		t.Fatalf("doctor: %v\n%s", err, f.out.String())
	}
}

// Review Focus 3: setup again changes nothing.
func TestSetupAgainChangesNothing(t *testing.T) {
	f := setupFake(t)
	args := []string{"setup", "--new-local", "--name", "gene", "--yes"}
	if err := f.run(args...); err != nil {
		t.Fatal(err)
	}
	before := setupSnapshot(t, f.env["EZC_HOME"], setupKeyFile(f))
	commit := setupGitOut(t, f, "rev-parse", "HEAD")

	for _, again := range [][]string{args, {"setup"}} {
		f.out.Reset()
		if err := f.run(again...); err != nil {
			t.Fatal(err)
		}
		want := "✓ git\n✓ fake\n✓ your key: ~/.config/sops/age/keys.txt\n✓ the store: " + setupStore(f) + "\n✓ the config: " + setupConfig(f) + "\n"
		if got := f.out.String(); got != want {
			t.Fatalf("%v: stdout:\n%s\nwant:\n%s", again, got, want)
		}
		if after := setupSnapshot(t, f.env["EZC_HOME"], setupKeyFile(f)); !reflect.DeepEqual(after, before) {
			t.Fatalf("%v: files changed", again)
		}
		if got := setupGitOut(t, f, "rev-parse", "HEAD"); got != commit {
			t.Fatalf("%v: a new commit", again)
		}
	}
}

func TestSetupReusesKey(t *testing.T) {
	f := setupFake(t)
	content, public := setupOwnKey(t, f)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(setupKeyFile(f)); string(got) != content {
		t.Fatal("the key file changed")
	}
	if !strings.Contains(f.out.String(), "✓ your key: ~/.config/sops/age/keys.txt\n") || strings.Contains(f.out.String(), "Back this file up") {
		t.Fatalf("stdout:\n%s", f.out.String())
	}
	reg, _, err := recipients.Load(filepath.Join(setupStore(f), ".ezc"))
	if err != nil || reg.NameOf(public) != "gene" || len(reg.Recipients) != 1 {
		t.Fatalf("registry %+v, %v", reg, err)
	}
}

func TestSetupKeyFileFromConfig(t *testing.T) {
	f := setupFake(t)
	key := filepath.Join(t.TempDir(), "keys", "ezc.key")
	if err := config.Save(setupConfig(f), config.Config{KeyFile: key, Checkouts: map[string]string{"app": "/src/app"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "+ created your key: "+key+"\n") {
		t.Fatalf("stdout:\n%s", f.out.String())
	}
	if _, err := os.Stat(setupKeyFile(f)); err == nil {
		t.Fatal("a key was made at the default place too")
	}
	c, _, err := config.Load(setupConfig(f))
	want := config.Config{User: "gene", Backend: "fake", KeyFile: key, Checkouts: map[string]string{"app": "/src/app"}}
	if err != nil || !reflect.DeepEqual(c, want) {
		t.Fatalf("config %+v, %v", c, err)
	}
}

func TestSetupNoHome(t *testing.T) {
	f := setupFake(t)
	delete(f.env, "HOME")
	err := f.run("setup", "--new-local", "--name", "gene", "--yes")
	if err == nil || !strings.Contains(err.Error(), "HOME") {
		t.Fatalf("err %v", err)
	}
	setupNothingMade(t, f)
}

func TestSetupNewGithub(t *testing.T) {
	f := setupFake(t)
	bare := setupFakeGh(t, f, "")
	if err := f.run("setup", "--new-github", "acme/config", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	want := []string{"repo", "create", "acme/config", "--private", "--source", setupStore(f), "--remote", "origin", "--push"}
	if len(f.execs) != 1 || f.execs[0].Name != "gh" || !reflect.DeepEqual(f.execs[0].Args, want) {
		t.Fatalf("ran %+v", f.execs)
	}
	remote := setupGitOut(t, f, "remote", "get-url", "origin")
	c, _, err := config.Load(setupConfig(f))
	if err != nil || remote != bare || c.Store.Remote != remote {
		t.Fatalf("remote %q, config %q, %v", remote, c.Store.Remote, err)
	}
	if !strings.Contains(f.out.String(), "+ created the store: "+setupStore(f)+"\n+ published the store: acme/config\n") {
		t.Fatalf("stdout:\n%s", f.out.String())
	}

	// Again: the store has its remote, so gh is not needed and not run.
	delete(f.paths, "gh")
	f.out.Reset()
	if err := f.run("setup", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.execs) != 1 || strings.Contains(f.out.String(), "+ ") {
		t.Fatalf("ran %d, stdout:\n%s", len(f.execs), f.out.String())
	}
}

func TestSetupNewGithubWithoutGh(t *testing.T) {
	f := setupFake(t)
	err := f.run("setup", "--new-github", "acme/config", "--name", "gene", "--yes")
	if err == nil || err.Error() != "gh is not installed; it is needed to create a GitHub repo. https://cli.github.com" {
		t.Fatalf("err %v", err)
	}
	setupNothingMade(t, f)
}

func TestSetupPublishesExistingLocalStore(t *testing.T) {
	f := setupFake(t)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	commit := setupGitOut(t, f, "rev-parse", "HEAD")
	bare := setupFakeGh(t, f, "")
	f.out.Reset()
	if err := f.run("setup", "--new-github", "o/r", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.execs) != 1 || f.execs[0].Args[2] != "o/r" {
		t.Fatalf("ran %+v", f.execs)
	}
	if got := setupGitOut(t, f, "rev-parse", "HEAD"); got != commit {
		t.Fatal("a second store was made")
	}
	want := "✓ git\n✓ fake\n✓ your key: ~/.config/sops/age/keys.txt\n✓ the store: " + setupStore(f) + "\n+ published the store: o/r\n+ wrote " + setupConfig(f) + "\n" + setupNextLine
	if f.out.String() != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", f.out.String(), want)
	}
	if c, _, _ := config.Load(setupConfig(f)); c.Store.Remote != bare || c.User != "gene" {
		t.Fatalf("config %+v", c)
	}
}

func TestSetupClone(t *testing.T) {
	t.Run("a recipient", func(t *testing.T) {
		f := setupFake(t)
		_, public := setupOwnKey(t, f)
		bare := setupRemote(t, public)
		args := []string{"setup", "--store", bare, "--name", "gene", "--yes"}
		if err := f.run(args...); err != nil {
			t.Fatal(err)
		}
		want := "✓ git\n✓ fake\n✓ your key: ~/.config/sops/age/keys.txt\n+ cloned the store: " + setupStore(f) + "\n+ wrote " + setupConfig(f) + "\n" + setupNextLine
		if f.out.String() != want {
			t.Fatalf("stdout:\n%s\nwant:\n%s", f.out.String(), want)
		}
		if c, _, _ := config.Load(setupConfig(f)); c.Store.Remote != bare {
			t.Fatalf("config %+v", c)
		}
		// The same store again is kept.
		f.out.Reset()
		if err := f.run(args...); err != nil || strings.Contains(f.out.String(), "+ ") {
			t.Fatalf("again: %v\n%s", err, f.out.String())
		}
	})
	t.Run("not a recipient", func(t *testing.T) {
		f := setupFake(t)
		bare := setupRemote(t, "fake1someoneelse")
		if err := f.run("setup", "--store", bare, "--name", "gene", "--yes"); err != nil {
			t.Fatal(err)
		}
		want := "\n! Your key cannot read this store's secrets yet. Give this public key to someone who has access:\n" + setupPublicKey(t, f) + "\n"
		if !strings.HasSuffix(f.out.String(), want) || strings.Contains(f.out.String(), "Next:") {
			t.Fatalf("stdout:\n%s", f.out.String())
		}
	})
	t.Run("a relative path", func(t *testing.T) {
		f := setupFake(t)
		bare := setupRemote(t, "fake1someoneelse")
		f.wd = filepath.Dir(bare)
		if err := f.run("setup", "--store", "./"+filepath.Base(bare), "--name", "gene", "--yes"); err != nil {
			t.Fatal(err)
		}
		if c, _, _ := config.Load(setupConfig(f)); c.Store.Remote != bare {
			t.Fatalf("config %+v", c)
		}
	})
}

func TestSetupCloneOwnerRepo(t *testing.T) {
	f := setupFake(t)
	_, public := setupOwnKey(t, f)
	setupFakeGh(t, f, setupRemote(t, public))
	if err := f.run("setup", "--store", "acme/config", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	want := []string{"repo", "clone", "acme/config", setupStore(f)}
	if len(f.execs) != 1 || f.execs[0].Name != "gh" || !reflect.DeepEqual(f.execs[0].Args, want) {
		t.Fatalf("ran %+v", f.execs)
	}
	if !strings.HasSuffix(f.out.String(), "+ wrote "+setupConfig(f)+"\n"+setupNextLine) {
		t.Fatalf("stdout:\n%s", f.out.String())
	}

	g := setupFake(t)
	err := g.run("setup", "--store", "acme/config", "--name", "gene", "--yes")
	if err == nil || !strings.Contains(err.Error(), "gh is not installed") {
		t.Fatalf("without gh: %v", err)
	}
	setupNothingMade(t, g)
}

func TestSetupCloneNotAStore(t *testing.T) {
	f := setupFake(t)
	bare := gittest.Bare(t)
	err := f.run("setup", "--store", bare, "--name", "gene", "--yes")
	if want := bare + " is not an ezc store (it has no .ezc/manifest.yaml)."; err == nil || err.Error() != want {
		t.Fatalf("err %v", err)
	}
	for _, path := range []string{setupStore(f), setupConfig(f)} {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("%s was left behind", path)
		}
	}
}

func TestSetupDifferentStoreRefused(t *testing.T) {
	f := setupFake(t)
	bare := setupRemote(t, "fake1someoneelse")
	if err := f.run("setup", "--store", bare, "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	before := setupSnapshot(t, f.env["EZC_HOME"], setupKeyFile(f))
	other := setupRemote(t, "fake1someoneelse")
	err := f.run("setup", "--store", other, "--name", "gene", "--yes")
	want := "a store is already set up at " + setupStore(f) + " (remote " + bare + "). Move it away to set up another."
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}
	if after := setupSnapshot(t, f.env["EZC_HOME"], setupKeyFile(f)); !reflect.DeepEqual(after, before) {
		t.Fatal("files changed")
	}
}

func TestSameRemote(t *testing.T) {
	for _, c := range []struct {
		remote, source string
		want           bool
	}{
		{"https://github.com/acme/config.git", "acme/config", true},
		{"https://github.com/acme/config", "acme/config", true},
		{"git@github.com:acme/config.git", "acme/config", true},
		{"https://github.com/acme/config.git", "https://github.com/acme/config", true},
		{"/srv/store.git", "/srv/store.git", true},
		{"https://github.com/acme/config2.git", "acme/config", false},
		{"https://github.com/notacme/config.git", "acme/config", false},
		{"/srv/store.git", "/srv/other.git", false},
		{"", "acme/config", false},
	} {
		if got := sameRemote(c.remote, c.source); got != c.want {
			t.Errorf("sameRemote(%q, %q) = %v", c.remote, c.source, got)
		}
	}
}

func TestSetupRefusalHidesCredentials(t *testing.T) {
	f := setupFake(t)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	if err := (gitstore.Git{Run: f.runGit}).SetRemote(setupStore(f), "https://gene:hunter2@example.invalid/acme/config.git"); err != nil {
		t.Fatal(err)
	}
	err := f.run("setup", "--store", "acme/other", "--yes")
	if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "(remote https://example.invalid/acme/config.git)") {
		t.Fatalf("err %v", err)
	}
}

func TestSetupNoChoiceNoTerminal(t *testing.T) {
	f := setupFake(t)
	wantUsage(t, f.run("setup", "--name", "gene"), "ezc setup: say --new-local, --new-github owner/repo or --store <repo>.")
	setupNothingMade(t, f)
	if f.out.Len() != 0 {
		t.Fatalf("stdout: %s", f.out.String())
	}
}

func TestSetupTwoChoices(t *testing.T) {
	f := setupFake(t)
	wantUsage(t, f.run("setup", "--new-local", "--store", "/srv/store.git"), "only one of")
	wantUsage(t, f.run("setup", "--new-github", "o/r", "--store", "/srv/store.git"), "only one of")
	wantUsage(t, f.run("setup", "--new-github", "not a repo"), "owner/repo")
	wantUsage(t, f.run("setup", "--new-local", "extra"), "unexpected argument")
	setupNothingMade(t, f)
}

func TestSetupWithEzcStore(t *testing.T) {
	f := setupFake(t)
	f.env["EZC_STORE"] = t.TempDir()
	wantUsage(t, f.run("setup", "--new-local", "--name", "gene", "--yes"), "ezc setup: unset EZC_STORE. Setup manages the store in "+setupStore(f)+".")
	setupNothingMade(t, f)
}

func TestSetupBadName(t *testing.T) {
	f := setupFake(t)
	wantUsage(t, f.run("setup", "--new-local", "--name", "Gene Jones", "--yes"), "lower case letters, digits and -")
	setupNothingMade(t, f)

	// A login name that cannot be a recipient's name is the same error, with nothing made.
	f.env["USER"] = "gene.jones"
	wantUsage(t, f.run("setup", "--new-local", "--yes"), "--name")
	setupNothingMade(t, f)
}

func TestSetupNameFromUser(t *testing.T) {
	f := setupFake(t)
	if err := f.run("setup", "--new-local", "--yes"); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := config.Load(setupConfig(f)); c.User != "gene" {
		t.Fatalf("config %+v", c)
	}
}

func TestSetupPrompts(t *testing.T) {
	f := setupFake(t)
	f.tty = true
	f.stdin = "\n1\n\n"
	if err := f.run("setup"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.String(); got != setupFreshOutput(f) {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, setupFreshOutput(f))
	}
	for _, prompt := range []string{"Your name in this store [gene]: ", "Store: [1] new, on this machine  [2] new, on GitHub  [3] an existing one: "} {
		if !strings.Contains(f.errOut.String(), prompt) {
			t.Errorf("no prompt %q in\n%s", prompt, f.errOut.String())
		}
	}
	if c, _, _ := config.Load(setupConfig(f)); c.User != "gene" {
		t.Fatalf("config %+v", c)
	}
}

func TestSetupPromptsForARepo(t *testing.T) {
	f := setupFake(t)
	bare := setupFakeGh(t, f, "")
	f.tty = true
	f.stdin = "ada\n2\nacme/config\n\n"
	if err := f.run("setup"); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := config.Load(setupConfig(f)); c.User != "ada" || c.Store.Remote != bare {
		t.Fatalf("config %+v", c)
	}

	g := setupFake(t)
	from := setupRemote(t, "fake1someoneelse")
	g.tty = true
	g.stdin = "\n3\n" + from + "\n\n"
	if err := g.run("setup"); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := config.Load(setupConfig(g)); c.Store.Remote != from {
		t.Fatalf("config %+v", c)
	}

	h := setupFake(t)
	h.tty = true
	h.stdin = "\n7\n"
	if err := h.run("setup"); err == nil {
		t.Fatal("no error for choice 7")
	}
	setupNothingMade(t, h)
}

func TestSetupToolMissing(t *testing.T) {
	t.Run("no brew", func(t *testing.T) {
		f := setupFake(t)
		f.backend.Missing = true
		err := f.run("setup", "--new-local", "--name", "gene", "--yes")
		wantExit(t, err, 1)
		if want := "fake: the encryption tool is not installed\nInstall the fake tool.\n"; f.errOut.String() != want {
			t.Fatalf("stderr %q", f.errOut.String())
		}
		if f.out.String() != "✓ git\n" || len(f.execs) != 0 {
			t.Fatalf("stdout %q, ran %v", f.out.String(), f.execs)
		}
		setupNothingMade(t, f)
	})
	brew := func(f *fake) {
		f.paths["brew"] = "/opt/homebrew/bin/brew"
		f.execFn = func(proc.Command) ([]byte, []byte, error) {
			f.backend.Missing = false
			return nil, nil, nil
		}
	}
	t.Run("brew and --yes", func(t *testing.T) {
		f := setupFake(t)
		f.backend.Missing = true
		brew(f)
		if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
			t.Fatal(err)
		}
		if len(f.execs) != 1 || f.execs[0].Name != "brew" || !reflect.DeepEqual(f.execs[0].Args, []string{"install", "fake-tool"}) {
			t.Fatalf("ran %+v", f.execs)
		}
		if want := strings.Replace(setupFreshOutput(f), "✓ fake", "+ fake", 1); f.out.String() != want {
			t.Fatalf("stdout:\n%s", f.out.String())
		}
	})
	t.Run("brew, asked", func(t *testing.T) {
		f := setupFake(t)
		f.backend.Missing = true
		brew(f)
		f.tty = true
		f.stdin = "y\n\n1\n\n"
		if err := f.run("setup"); err != nil {
			t.Fatal(err)
		}
		if len(f.execs) != 1 || !strings.Contains(f.errOut.String(), "Install it now with brew? [y/N] ") {
			t.Fatalf("ran %+v, stderr %q", f.execs, f.errOut.String())
		}
	})
	t.Run("brew, declined", func(t *testing.T) {
		f := setupFake(t)
		f.backend.Missing = true
		brew(f)
		f.tty = true
		f.stdin = "\n"
		wantExit(t, f.run("setup", "--new-local"), 1)
		if len(f.execs) != 0 {
			t.Fatalf("ran %+v", f.execs)
		}
		setupNothingMade(t, f)
	})
	t.Run("brew does not help", func(t *testing.T) {
		f := setupFake(t)
		f.backend.Missing = true
		f.paths["brew"] = "/opt/homebrew/bin/brew"
		f.execFn = func(proc.Command) ([]byte, []byte, error) { return nil, nil, nil }
		wantExit(t, f.run("setup", "--new-local", "--name", "gene", "--yes"), 1)
		setupNothingMade(t, f)
	})
}

func TestSetupNoGit(t *testing.T) {
	f := setupFake(t)
	delete(f.paths, "git")
	err := f.run("setup", "--new-local", "--name", "gene", "--yes")
	if err == nil || err.Error() != "git is not installed. Install it and run ezc setup again." {
		t.Fatalf("err %v", err)
	}
	if f.out.Len() != 0 || len(f.gits) != 0 {
		t.Fatalf("stdout %q, git ran %d times", f.out.String(), len(f.gits))
	}
	setupNothingMade(t, f)
}

func TestSetupNeverPrintsThePrivateKey(t *testing.T) {
	f := setupFake(t)
	f.tty = true
	f.stdin = "\n1\n\n"
	if err := f.run("setup"); err != nil {
		t.Fatal(err)
	}
	if err := f.run("setup", "--store", "acme/other"); err == nil {
		t.Fatal("a different store was accepted")
	}
	data, err := os.ReadFile(setupKeyFile(f))
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSpace(string(data))
	if !strings.HasPrefix(secret, "FAKE-KEY-") {
		t.Fatalf("the key file is not what this test expects")
	}
	if strings.Contains(f.out.String()+f.errOut.String(), secret) {
		t.Fatal("the private key was printed")
	}
	for _, c := range append(f.execs, f.gits...) {
		if strings.Contains(strings.Join(c.Args, " ")+strings.Join(c.Env, " ")+string(c.Stdin), secret) {
			t.Fatalf("the private key was given to %s", c.Name)
		}
	}
}
