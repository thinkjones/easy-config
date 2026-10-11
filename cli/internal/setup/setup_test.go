package setup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

// machine is an Env over temporary folders: the fake backend, the real git as run runs it, and a gh that fails the test unless gh is set.
type machine struct {
	Env
	t   *testing.T
	ghs [][]string                // every gh call's arguments
	gh  func(args []string) error // what gh does; nil fails the test
}

func newMachine(t *testing.T, git proc.Runner) *machine {
	t.Helper()
	home := t.TempDir()
	p, err := paths.Resolve(func(k string) string {
		if k == "EZC_HOME" {
			return home
		}
		return ""
	}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	m := &machine{t: t}
	m.Env = Env{
		Git:     gitstore.Git{Run: git},
		Backend: &backend.Fake{},
		Run: func(c proc.Command) ([]byte, []byte, error) {
			if c.Name != "gh" || m.gh == nil {
				t.Errorf("ran %s %v", c.Name, c.Args)
				return nil, nil, errors.New("unexpected program")
			}
			m.ghs = append(m.ghs, c.Args)
			return nil, nil, m.gh(c.Args)
		},
		LookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		Now:      func() time.Time { return time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC) },
		Paths:    p,
		GOOS:     "linux",
	}
	return m
}

// git runs git in the store and returns its trimmed stdout.
func (m *machine) git(args ...string) string {
	m.t.Helper()
	out, stderr, err := m.Env.Git.Run(proc.Command{Dir: m.Paths.Store(), Name: "git", Args: args})
	if err != nil {
		m.t.Fatalf("git %v: %v\n%s", args, err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// remoteWith is a bare repo holding one commit of files (paths relative to the repo's top).
func remoteWith(t *testing.T, files map[string]string) string {
	t.Helper()
	g := gitstore.Git{Run: gittest.WithIdentity(t)}
	bare, work := gittest.Bare(t), filepath.Join(t.TempDir(), "work")
	if err := g.Init(work); err != nil {
		t.Fatal(err)
	}
	var names []string
	for name, content := range files {
		path := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if _, err := g.Commit(work, "first", gitstore.Author{}, names...); err != nil {
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

func TestKeyCreates(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	file := filepath.Join(t.TempDir(), "a", "b", "keys.txt")
	public, created, err := Key(m.Env, file)
	if err != nil || !created {
		t.Fatalf("created %v, err %v", created, err)
	}
	if got, err := m.Backend.PublicKey(file); err != nil || got != public {
		t.Fatalf("public %q, the file's %q (%v)", public, got, err)
	}
	for path, want := range map[string]os.FileMode{file: 0o600, filepath.Dir(file): 0o700, filepath.Dir(filepath.Dir(file)): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: mode %o, want %o", path, info.Mode().Perm(), want)
		}
	}
}

func TestKeyReusesAnExistingFile(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	file := filepath.Join(t.TempDir(), "keys.txt")
	want, private, err := m.Backend.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	content := append([]byte("# mine\n"), private...)
	if err := os.WriteFile(file, content, 0o640); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}

	public, created, err := Key(m.Env, file)
	if err != nil || created || public != want {
		t.Fatalf("public %q (want %q), created %v, err %v", public, want, created, err)
	}
	unchanged(t, file, content, old, 0o640)
}

func TestKeyRefusesAFileWithNoKey(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	file := filepath.Join(t.TempDir(), "keys.txt")
	content := []byte("# nothing here yet\n")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}

	_, created, err := Key(m.Env, file)
	if err == nil || created || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), "holds no key") {
		t.Fatalf("created %v, err %v", created, err)
	}
	unchanged(t, file, content, old, 0o600)
}

// unchanged fails unless path still has these bytes, this modification time and this mode.
func unchanged(t *testing.T, path string, content []byte, mod time.Time, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) || !info.ModTime().Equal(mod) || info.Mode().Perm() != mode {
		t.Fatalf("%s changed: %d bytes, %v, %o", path, len(got), info.ModTime(), info.Mode().Perm())
	}
}

func TestNewStore(t *testing.T) {
	for name, git := range map[string]proc.Runner{
		"identity":    gittest.WithIdentity(t),
		"no identity": gittest.NoIdentity(t), // Review Focus 4: a machine where git knows no one
	} {
		t.Run(name, func(t *testing.T) {
			m := newMachine(t, git)
			if err := NewStore(m.Env, "gene", "fake1abc"); err != nil {
				t.Fatal(err)
			}
			if got := m.git("log", "--format=%s"); got != "setup: create the store" {
				t.Errorf("commits %q", got)
			}
			if got := m.git("status", "--porcelain"); got != "" {
				t.Errorf("not clean:\n%s", got)
			}
			if got := m.git("branch", "--show-current"); got != "main" {
				t.Errorf("branch %q", got)
			}
			wantAuthor := gittest.Name
			if name == "no identity" {
				wantAuthor = "gene"
			}
			if got := m.git("log", "--format=%an"); got != wantAuthor {
				t.Errorf("author %q, want %q", got, wantAuthor)
			}

			mf, err := manifest.Load(filepath.Join(m.Paths.StoreEzc(), "manifest.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if errs := mf.Validate(); len(errs) > 0 || len(mf.All()) != 0 {
				t.Errorf("manifest: %v, %d names", errs, len(mf.All()))
			}
			reg, found, err := recipients.Load(m.Paths.StoreEzc())
			if err != nil || !found {
				t.Fatalf("registry: found %v, err %v", found, err)
			}
			want := map[string]recipients.Recipient{"gene": {Key: "fake1abc", Kind: "user", Added: "2026-10-11", AddedBy: "gene"}}
			if !reflect.DeepEqual(reg.Recipients, want) {
				t.Errorf("registry %+v", reg.Recipients)
			}
			if ok, err := m.Backend.HasPolicy(m.Paths.StoreEzc()); err != nil || !ok {
				t.Errorf("policy: %v, %v", ok, err)
			}
			if got, err := Recipient(m.Env, "fake1abc"); err != nil || got != "gene" {
				t.Errorf("Recipient = %q, %v", got, err)
			}
			if got, err := Recipient(m.Env, "fake1other"); err != nil || got != "" {
				t.Errorf("Recipient of another key = %q, %v", got, err)
			}
		})
	}
}

func TestNewStoreRefusesAStoreThatIsThere(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	file := filepath.Join(m.Paths.StoreEzc(), "common.yaml")
	if err := os.MkdirAll(m.Paths.StoreEzc(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("A: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(m.Env, "gene", "fake1abc"); err == nil {
		t.Fatal("no error")
	}
	entries, _ := os.ReadDir(m.Paths.Store())
	if got, _ := os.ReadFile(file); string(got) != "A: 1\n" || len(entries) != 1 {
		t.Fatalf("the store was touched: %q, %v", got, entries)
	}
}

// failingPolicy is a backend that cannot write its policy.
type failingPolicy struct{ backend.Fake }

func (*failingPolicy) WritePolicy(string, []string) error { return errors.New("no policy today") }

func TestNewStoreFailureLeavesNothing(t *testing.T) {
	t.Run("new folder", func(t *testing.T) {
		m := newMachine(t, gittest.Runner(t))
		m.Backend = &failingPolicy{}
		if err := NewStore(m.Env, "gene", "fake1abc"); err == nil {
			t.Fatal("no error")
		}
		if exists(m.Paths.Store()) {
			t.Fatal("the store's folder is still there")
		}
	})
	t.Run("folder that was there", func(t *testing.T) {
		m := newMachine(t, gittest.Runner(t))
		m.Backend = &failingPolicy{}
		note := filepath.Join(m.Paths.Store(), "note.txt")
		if err := os.MkdirAll(m.Paths.Store(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(note, []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := NewStore(m.Env, "gene", "fake1abc"); err == nil {
			t.Fatal("no error")
		}
		entries, _ := os.ReadDir(m.Paths.Store())
		if len(entries) != 1 || entries[0].Name() != "note.txt" {
			t.Fatalf("left behind: %v", entries)
		}
	})
}

// handMade puts a store made by hand, a plain folder, at the machine's store path.
func handMade(t *testing.T, m *machine, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(m.Paths.StoreEzc(), name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdopt(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	handMade(t, m, map[string]string{manifestName: emptyManifest, "common.yaml": "A: 1\n"})
	a, err := Adopt(m.Env, "gene", "fake1abc")
	if err != nil || a != (Adopted{Repo: true, Registry: true}) {
		t.Fatalf("got %+v, %v", a, err)
	}
	if got := m.git("log", "--format=%s|%an"); got != "setup: adopt the store|gene" {
		t.Fatalf("log %q", got)
	}
	if got := m.git("status", "--porcelain"); got != "" {
		t.Fatalf("left uncommitted:\n%s", got)
	}
	reg, found, err := recipients.Load(m.Paths.StoreEzc())
	if err != nil || !found || reg.NameOf("fake1abc") != "gene" {
		t.Fatalf("registry %+v, %v, %v", reg, found, err)
	}
	// All is there now: nothing more is done.
	if a, err := Adopt(m.Env, "gene", "fake1abc"); err != nil || a != (Adopted{}) || m.git("rev-list", "--count", "HEAD") != "1" {
		t.Fatalf("again: %+v, %v", a, err)
	}
}

func TestAdoptLeavesAHandWrittenPolicy(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	handMade(t, m, map[string]string{manifestName: emptyManifest, backend.FakePolicy: "by hand\n"})
	a, err := Adopt(m.Env, "gene", "fake1abc")
	if err != nil || a != (Adopted{Repo: true}) {
		t.Fatalf("got %+v, %v", a, err)
	}
	if got, _ := os.ReadFile(filepath.Join(m.Paths.StoreEzc(), backend.FakePolicy)); string(got) != "by hand\n" {
		t.Fatalf("the policy is now %q", got)
	}
	if exists(filepath.Join(m.Paths.StoreEzc(), recipients.File)) {
		t.Fatal("a registry was written")
	}
}

func TestAdoptFailureLeavesTheStoreAsItWas(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	m.Backend = &failingPolicy{}
	handMade(t, m, map[string]string{manifestName: emptyManifest})
	if a, err := Adopt(m.Env, "gene", "fake1abc"); err == nil || a != (Adopted{}) {
		t.Fatalf("got %+v, %v", a, err)
	}
	entries, _ := os.ReadDir(m.Paths.StoreEzc())
	if len(entries) != 1 || entries[0].Name() != manifestName || exists(filepath.Join(m.Paths.Store(), ".git")) {
		t.Fatalf("left behind: %v, .git %v", entries, exists(filepath.Join(m.Paths.Store(), ".git")))
	}
}

func TestCloneStore(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	bare := remoteWith(t, map[string]string{".ezc/manifest.yaml": emptyManifest})
	remote, err := CloneStore(m.Env, bare)
	if err != nil {
		t.Fatal(err)
	}
	if remote != bare || !HasStore(m.Env) {
		t.Fatalf("remote %q, store there: %v", remote, HasStore(m.Env))
	}
	if len(m.ghs) != 0 {
		t.Fatalf("gh ran: %v", m.ghs)
	}
}

func TestCloneStoreNotAStore(t *testing.T) {
	for _, existed := range []bool{false, true} {
		m := newMachine(t, gittest.Runner(t))
		if existed {
			if err := os.MkdirAll(m.Paths.Store(), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		bare := remoteWith(t, map[string]string{"README.md": "hello\n"})
		_, err := CloneStore(m.Env, bare)
		if want := bare + " is not an ezc store (it has no .ezc/manifest.yaml)."; err == nil || err.Error() != want {
			t.Fatalf("err %v, want %s", err, want)
		}
		entries, _ := os.ReadDir(m.Paths.Store())
		if exists(m.Paths.Store()) != existed || len(entries) != 0 {
			t.Fatalf("existed %v: folder there %v, holding %v", existed, exists(m.Paths.Store()), entries)
		}
	}
}

func TestCloneStoreKeepsAFolderInTheWay(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	note := filepath.Join(m.Paths.Store(), "note.txt")
	if err := os.MkdirAll(m.Paths.Store(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bare := remoteWith(t, map[string]string{".ezc/manifest.yaml": emptyManifest})
	if _, err := CloneStore(m.Env, bare); err == nil || !strings.Contains(err.Error(), "in the way") {
		t.Fatalf("err %v", err)
	}
	if got, _ := os.ReadFile(note); string(got) != "mine\n" {
		t.Fatalf("note %q", got)
	}
}

func TestCloneStoreSlugUsesGh(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	bare := remoteWith(t, map[string]string{".ezc/manifest.yaml": emptyManifest})
	m.gh = func(args []string) error { return m.Env.Git.Clone(bare, args[len(args)-1]) }
	if _, err := CloneStore(m.Env, "acme/config"); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"repo", "clone", "acme/config", m.Paths.Store()}}; !reflect.DeepEqual(m.ghs, want) {
		t.Fatalf("gh calls %v, want %v", m.ghs, want)
	}
}

func TestPublish(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	if err := NewStore(m.Env, "gene", "fake1abc"); err != nil {
		t.Fatal(err)
	}
	bare := gittest.Bare(t)
	m.gh = func([]string) error { return m.Env.Git.SetRemote(m.Paths.Store(), bare) }
	remote, err := Publish(m.Env, "acme/config")
	if err != nil || remote != bare {
		t.Fatalf("remote %q, err %v", remote, err)
	}
	want := [][]string{{"repo", "create", "acme/config", "--private", "--source", m.Paths.Store(), "--remote", "origin", "--push"}}
	if !reflect.DeepEqual(m.ghs, want) {
		t.Fatalf("gh calls %v, want %v", m.ghs, want)
	}
}

func TestPublishFailureSaysOneLine(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	m.Run = func(proc.Command) ([]byte, []byte, error) {
		return []byte("stdout is not passed on"), []byte("\nName already exists on this account\nmore\n"), errors.New("exit status 1")
	}
	if _, err := Publish(m.Env, "acme/config"); err == nil || err.Error() != "gh: Name already exists on this account" {
		t.Fatalf("err %v", err)
	}
}

func TestInstallTool(t *testing.T) {
	m := newMachine(t, gittest.Runner(t))
	var ran []proc.Command
	m.Run = func(c proc.Command) ([]byte, []byte, error) {
		ran = append(ran, c)
		return nil, nil, nil
	}
	if err := InstallTool(m.Env); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0].Name != "brew" || !reflect.DeepEqual(ran[0].Args, []string{"install", "fake-tool"}) {
		t.Fatalf("ran %+v", ran)
	}
}

func TestIsSlug(t *testing.T) {
	for s, want := range map[string]bool{
		"acme/config": true, "a-b/c.d_e": true,
		"/abs/path": false, "./a/b": false, "../a": false, "a/b/c": false, "config": false,
		"https://github.com/acme/config": false, "git@github.com:acme/config.git": false, "": false,
	} {
		if IsSlug(s) != want {
			t.Errorf("IsSlug(%q) = %v", s, !want)
		}
	}
}
