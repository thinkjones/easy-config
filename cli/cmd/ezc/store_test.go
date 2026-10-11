//go:build unix

package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// staleState is a state.json whose last fetch is old enough for the next command to fetch again.
const staleState = `{"attempted":"2020-01-01T00:00:00Z","fetched":"2020-01-01T00:00:00Z"}` + "\n"

func requireGitOnPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		if os.Getenv("EZC_REQUIRE_GIT") == "1" {
			t.Fatal("git is required (EZC_REQUIRE_GIT=1) but is not on PATH")
		}
		t.Skip("git is not installed")
	}
	return p
}

// newMachine is an empty machine under root: a home with no key, an ezc home with no store, and a repo folder.
// Its children see the built ezc, git and sops on PATH and none of the user's git config.
func newMachine(t *testing.T, root, name string) *e2e {
	t.Helper()
	sops, git := requireSopsOnPath(t), requireGitOnPath(t)
	e := &e2e{
		t:       t,
		home:    filepath.Join(root, name, "home"),
		ezcHome: filepath.Join(root, name, "ezc"),
		repo:    filepath.Join(root, name, "repo"),
	}
	for _, d := range []string{e.home, e.ezcHome, e.repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitConfig := filepath.Join(root, name, "gitconfig")
	if err := os.WriteFile(gitConfig, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.key = filepath.Join(e.home, ".config", "sops", "age", "keys.txt")
	e.path = strings.Join([]string{filepath.Dir(binary), filepath.Dir(git), filepath.Dir(sops), "/usr/bin", "/bin"}, string(os.PathListSeparator))
	e.env = []string{"GIT_CONFIG_GLOBAL=" + gitConfig, "GIT_CONFIG_NOSYSTEM=1"}
	return e
}

func (e *e2e) store() string { return filepath.Join(e.ezcHome, "store") }

// git runs git in the machine's store and returns its trimmed stdout.
func (e *e2e) git(args ...string) string {
	e.t.Helper()
	out, errOut, code := e.exec(e.store(), "", "git", args...)
	if code != 0 {
		e.t.Fatalf("git %v: exit %d\n%s%s", args, code, out, errOut)
	}
	return strings.TrimSpace(out)
}

// stale makes the machine's next command fetch.
func (e *e2e) stale() {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.ezcHome, "state.json"), []byte(staleState), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// twoMachines are A, whose new store is published to a bare repo, and B, which has a copy of A's key file and a clone.
func twoMachines(t *testing.T) (a, b *e2e, bare string) {
	t.Helper()
	root := t.TempDir()
	a, b = newMachine(t, root, "a"), newMachine(t, root, "b")
	a.mustRun(a.repo, "", "setup", "--new-local", "--name", "gene", "--yes")
	a.mustRun(a.repo, e2eSecret, "set", "-g", "API_TOKEN", "--type", "secret", "--desc", "d")

	bare = filepath.Join(root, "remote.git")
	// The bare repo's branch is named, as a host names it after the first push; git's own default may differ.
	if out, errOut, code := a.exec(root, "", "git", "init", "--quiet", "--bare", "--initial-branch", a.git("branch", "--show-current"), bare); code != 0 {
		t.Fatalf("git init --bare: exit %d\n%s%s", code, out, errOut)
	}
	a.git("remote", "add", "origin", bare)
	if out := a.mustRun(a.repo, "", "store", "push"); out != "pushed 2 commits to origin\n" {
		t.Fatalf("push: %q", out)
	}

	key, err := os.ReadFile(a.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(b.key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.key, key, 0o600); err != nil {
		t.Fatal(err)
	}
	out := b.mustRun(b.repo, "", "setup", "--store", bare, "--name", "gene", "--yes")
	if !strings.Contains(out, "✓ your key: ") || !strings.Contains(out, "+ cloned the store: ") {
		t.Fatalf("setup --store: %q", out)
	}
	if now, err := os.ReadFile(b.key); err != nil || !bytes.Equal(now, key) {
		t.Fatalf("setup changed the key file that was there (%v)", err)
	}
	return a, b, bare
}

// wantNoValue fails when value is in a file of dir (a commit's files included) or in any commit git can show.
func wantNoValue(t *testing.T, e *e2e, value string) {
	t.Helper()
	err := filepath.WalkDir(e.store(), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte(value)) {
			t.Errorf("%s holds the value in clear", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.git("log", "--all", "--patch", "--format=fuller"), value) {
		t.Error("a commit holds the value in clear")
	}
}

func TestSetupEndToEnd(t *testing.T) {
	e := newMachine(t, t.TempDir(), "a")
	if out := e.mustRun(e.repo, "", "version"); out != "ezc 0.3.0\n" {
		t.Fatalf("version: %q", out)
	}
	out := e.mustRun(e.repo, "", "setup", "--new-local", "--name", "gene", "--yes")
	for _, want := range []string{"✓ git\n", "✓ sops\n", "+ created your key: ~/.config/sops/age/keys.txt\n", "+ created the store: ", "+ wrote "} {
		if !strings.Contains(out, want) {
			t.Errorf("setup: no %q in\n%s", want, out)
		}
	}
	key, err := os.ReadFile(e.key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, strings.TrimSpace(string(key))) {
		t.Fatal("setup printed the private key")
	}
	for path, want := range map[string]fs.FileMode{e.key: 0o600, filepath.Dir(e.key): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s is %o, want %o", path, got, want)
		}
	}

	out = e.mustRun(e.repo, e2eSecret, "set", "-g", "API_TOKEN", "--type", "secret", "--desc", "d")
	if !strings.Contains(out, "set API_TOKEN in store/common.enc.yaml") || strings.Contains(out, e2eSecret) {
		t.Fatalf("set: %q", out)
	}
	if out := e.mustRun(e.repo, "", "get", "API_TOKEN", "--reveal"); out != e2eSecret+"\n" {
		t.Fatalf("get --reveal: %q", out)
	}
	if stdout, stderr, code := e.run(e.repo, "", "doctor"); code != 0 || !strings.Contains(stdout, "✓ access    your key is the recipient gene") || strings.Contains(stdout+stderr, e2eSecret) {
		t.Fatalf("doctor: exit %d\n%s%s", code, stdout, stderr)
	}
	if got, want := e.git("log", "--format=%s"), "set API_TOKEN (manifest.yaml, common.enc.yaml)\nsetup: create the store"; got != want {
		t.Fatalf("log:\n%s\nwant:\n%s", got, want)
	}
	if got := e.git("status", "--porcelain"); got != "" {
		t.Fatalf("the store has uncommitted changes:\n%s", got)
	}
	wantNoValue(t, e, e2eSecret)

	head := e.git("rev-parse", "HEAD")
	out = e.mustRun(e.repo, "", "setup", "--new-local", "--yes")
	if strings.Contains(out, "+ ") {
		t.Fatalf("the second setup made something:\n%s", out)
	}
	if e.git("rev-parse", "HEAD") != head {
		t.Fatal("the second setup added a commit")
	}
	if now, err := os.ReadFile(e.key); err != nil || !bytes.Equal(now, key) {
		t.Fatalf("the second setup changed the key file (%v)", err)
	}
}

// A store made by hand under part 2, a plain folder with a policy written by hand, becomes a git repo that ezc
// commits to; its policy and its secret are left as they were.
func TestSetupAdoptsAHandMadeStoreEndToEnd(t *testing.T) {
	e := newMachine(t, t.TempDir(), "a")
	pub := writeAgeKey(t, e.key)
	ezc := filepath.Join(e.store(), ".ezc")
	e.writePolicy(ezc, pub)
	if err := os.WriteFile(filepath.Join(ezc, "manifest.yaml"), []byte("schemaVersion: v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun(e.repo, e2eSecret, "set", "-g", "API_TOKEN", "--type", "secret", "--desc", "d")
	if _, err := os.Stat(filepath.Join(e.store(), ".git")); err == nil {
		t.Fatal("the store was a git repo before setup")
	}
	before := map[string][]byte{}
	for _, name := range []string{".sops.yaml", "manifest.yaml", "common.enc.yaml"} {
		data, err := os.ReadFile(filepath.Join(ezc, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = data
	}
	key, err := os.ReadFile(e.key)
	if err != nil {
		t.Fatal(err)
	}

	out := e.mustRun(e.repo, "", "setup", "--new-local", "--name", "gene", "--yes")
	for _, want := range []string{"✓ your key: ", "+ made the store a git repo: " + e.store() + "\n", "! This store's encryption policy was written by hand and it has no recipients.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("setup: no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "Next:") || strings.Contains(out, "wrote the registry") {
		t.Errorf("setup:\n%s", out)
	}
	for name, want := range before {
		if now, err := os.ReadFile(filepath.Join(ezc, name)); err != nil || !bytes.Equal(now, want) {
			t.Errorf("setup changed %s (%v)", name, err)
		}
	}
	if now, err := os.ReadFile(e.key); err != nil || !bytes.Equal(now, key) {
		t.Fatalf("setup changed the key file (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(ezc, "recipients.yaml")); err == nil {
		t.Error("setup wrote a registry beside a policy written by hand")
	}
	if got := e.git("log", "--format=%s"); got != "setup: adopt the store" || e.git("status", "--porcelain") != "" {
		t.Fatalf("log:\n%s\nstatus:\n%s", got, e.git("status", "--porcelain"))
	}
	if out := e.mustRun(e.repo, "", "get", "API_TOKEN", "--reveal"); out != e2eSecret+"\n" {
		t.Fatalf("get --reveal: %q", out)
	}

	e.mustRun(e.repo, e2eSecret+"-2", "set", "-g", "DB_PASS", "--type", "secret", "--desc", "d")
	if got, want := e.git("log", "--format=%s"), "set DB_PASS (manifest.yaml, common.enc.yaml)\nsetup: adopt the store"; got != want {
		t.Fatalf("log:\n%s\nwant:\n%s", got, want)
	}
	if got := e.git("status", "--porcelain"); got != "" {
		t.Fatalf("the store has uncommitted changes:\n%s", got)
	}
	wantNoValue(t, e, e2eSecret)

	head := e.git("rev-parse", "HEAD")
	if out := e.mustRun(e.repo, "", "setup", "--yes"); strings.Contains(out, "+ ") || e.git("rev-parse", "HEAD") != head {
		t.Fatalf("the second setup made something:\n%s", out)
	}
}

// The key file the environment names is the one setup creates and records, and the one a later command decrypts with.
func TestSetupKeyFileFromEnvironmentEndToEnd(t *testing.T) {
	e := newMachine(t, t.TempDir(), "a")
	keyFile := filepath.Join(e.home, "keys", "mine.txt")
	plain := e.env
	e.env = append(plain[:len(plain):len(plain)], "SOPS_AGE_KEY_FILE="+keyFile)

	e.mustRun(e.repo, "", "setup", "--new-local", "--name", "gene", "--yes")
	if info, err := os.Stat(keyFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the key file the environment names: %v, %v", info, err)
	}
	if _, err := os.Stat(e.key); !os.IsNotExist(err) {
		t.Fatalf("a key was also made at the default path: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(e.ezcHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `key_file = "` + keyFile + `"`; !strings.Contains(string(config), want) {
		t.Fatalf("config.toml has no %s:\n%s", want, config)
	}
	e.mustRun(e.repo, e2eSecret, "set", "-g", "API_TOKEN", "--type", "secret", "--desc", "d")

	// Without the variable, the config alone leads to the key.
	e.env = plain
	if out := e.mustRun(e.repo, "", "get", "API_TOKEN", "--reveal"); out != e2eSecret+"\n" {
		t.Fatalf("get --reveal: %q", out)
	}
	if stdout, stderr, code := e.run(e.repo, "", "doctor", "--only", "store"); code != 0 || !strings.Contains(stdout, "✓ access    your key is the recipient gene") {
		t.Fatalf("doctor: exit %d\n%s%s", code, stdout, stderr)
	}
}

func TestTwoMachinesEndToEnd(t *testing.T) {
	a, b, _ := twoMachines(t)
	if out := b.mustRun(b.repo, "", "get", "API_TOKEN", "--reveal"); out != e2eSecret+"\n" {
		t.Fatalf("get --reveal on B: %q", out)
	}

	a.mustRun(a.repo, "", "set", "-g", "REGION", "us-east-1", "--type", "str", "--desc", "d")
	if out := a.mustRun(a.repo, "", "store", "push"); out != "pushed 1 commit to origin\n" {
		t.Fatalf("push: %q", out)
	}

	b.stale()
	const behind = "store: 1 commit behind origin — run ezc store pull\n"
	if stdout, stderr, code := b.run(b.repo, "", "list", "-g"); code != 0 || stderr != behind || strings.Contains(stdout, "REGION") {
		t.Fatalf("list -g: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	head := b.git("rev-parse", "HEAD")
	stdout, stderr, code := b.run(b.repo, "", "set", "-g", "X", "y", "--type", "str", "--desc", "d")
	if want := behind + "ezc: the store is 1 commit behind origin. Run `ezc store pull` first: a change made now could not be merged.\n"; code != 1 || stdout != "" || stderr != want {
		t.Fatalf("set while behind: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if b.git("rev-parse", "HEAD") != head || b.git("status", "--porcelain") != "" {
		t.Fatal("the refused set changed the store")
	}
	if stdout, stderr, code := b.run(b.repo, "", "store", "pull"); code != 0 || stdout != "pulled 1 commit from origin\n" || stderr != "" {
		t.Fatalf("pull: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if out := b.mustRun(b.repo, "", "get", "REGION"); out != "us-east-1\n" {
		t.Fatalf("get after the pull: %q", out)
	}
}

func TestDivergedSecretEndToEnd(t *testing.T) {
	a, b, _ := twoMachines(t)
	const first, fromA, fromB = "first-value", "value-from-a", "value-from-b"
	for _, name := range []string{"SEC_A", "SEC_B"} {
		a.mustRun(a.repo, first, "set", "-g", name, "--type", "secret", "--desc", "d")
	}
	a.mustRun(a.repo, "", "store", "push")
	b.mustRun(b.repo, "", "store", "pull")

	a.mustRun(a.repo, fromA, "set", "-g", "SEC_A")
	a.mustRun(a.repo, "", "store", "push")
	// B fetched a moment ago, so it does not know it is behind and the write goes through.
	b.mustRun(b.repo, fromB, "set", "-g", "SEC_B")

	stdout, stderr, code := b.run(b.repo, "", "store", "pull")
	want := "! common.enc.yaml was changed here and on the remote. The remote's version was kept; set these again:\n  ezc set -g SEC_B\n"
	if code != 1 || stdout != "pulled 1 commit from origin\n" || stderr != want {
		t.Fatalf("pull: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if out := b.mustRun(b.repo, "", "get", "SEC_A", "--reveal"); out != fromA+"\n" {
		t.Fatalf("A's secret on B: %q", out)
	}
	if out := b.mustRun(b.repo, "", "get", "SEC_B", "--reveal"); out != first+"\n" {
		t.Fatalf("B's secret after the remote's version was kept: %q", out)
	}
	if out := b.mustRun(b.repo, "", "store", "status"); !strings.Contains(out, "state     in sync\n") {
		t.Fatalf("status: %q", out)
	}
}

// Two machines that each declare a new name both add to the end of the same plaintext files: the pull stops.
func TestDeclaredOnBothMachinesEndToEnd(t *testing.T) {
	a, b, _ := twoMachines(t)
	// B looks at the remote now, so for the next 10 minutes it does not know of what A pushes.
	b.mustRun(b.repo, "", "store", "status")
	a.mustRun(a.repo, "", "set", "-g", "FROM_A", "1", "--type", "str", "--desc", "d")
	a.mustRun(a.repo, "", "store", "push")
	b.mustRun(b.repo, "", "set", "-g", "FROM_B", "1", "--type", "str", "--desc", "d")

	head := b.git("rev-parse", "HEAD")
	stdout, stderr, code := b.run(b.repo, "", "store", "pull")
	if code != 1 || stdout != "" || !strings.Contains(stderr, ".ezc/manifest.yaml were changed here and on the remote in the same place, so nothing was pulled.") || !strings.Contains(stderr, "(git pull --rebase)") {
		t.Fatalf("pull: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if b.git("rev-parse", "HEAD") != head || b.git("status", "--porcelain") != "" {
		t.Fatal("the stopped pull changed the store")
	}
	if out := b.mustRun(b.repo, "", "get", "FROM_B"); out != "1\n" {
		t.Fatalf("B's own value after the stopped pull: %q", out)
	}
	if out := b.mustRun(b.repo, "", "store", "status"); !strings.Contains(out, "state     diverged from origin (1 ahead, 1 behind)\n") {
		t.Fatalf("status: %q", out)
	}
}

// A write is refused over a store file that was changed by hand and not committed.
func TestUncommittedStoreFileEndToEnd(t *testing.T) {
	e := newMachine(t, t.TempDir(), "a")
	e.mustRun(e.repo, "", "setup", "--new-local", "--name", "gene", "--yes")
	e.mustRun(e.repo, "", "set", "-g", "REGION", "us-east-1", "--type", "str", "--desc", "d")
	file := filepath.Join(e.store(), ".ezc", "common.yaml")
	if err := os.WriteFile(file, []byte("REGION: by-hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := e.git("rev-parse", "HEAD")

	stdout, stderr, code := e.run(e.repo, "", "set", "-g", "REGION", "eu-west-1")
	want := "store: uncommitted changes in " + e.store() + "\n" +
		"ezc: common.yaml has uncommitted changes in " + e.store() + ". Commit or discard them with git, then run this again.\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Fatalf("set: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "REGION: by-hand\n" {
		t.Fatalf("the refused set wrote the file: %q (%v)", data, err)
	}
	if e.git("rev-parse", "HEAD") != head {
		t.Fatal("the refused set made a commit")
	}
}

// Review Focus 2: a remote that cannot be reached never fails a command, and never holds it up for long.
func TestOfflineEndToEnd(t *testing.T) {
	_, b, bare := twoMachines(t)
	b.mustRun(b.repo, "", "repo", "init")
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	b.stale()

	start := time.Now()
	stdout, stderr, code := b.run(b.repo, "", "env")
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("env took %s with the remote gone", took)
	}
	if code != 0 || !strings.Contains(stderr, "store: origin could not be reached; last checked ") || strings.Contains(stdout+stderr, e2eSecret) {
		t.Fatalf("env: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if out := b.mustRun(b.repo, "", "run", "--", "sh", "-c", `printf %s "$API_TOKEN"`); out != e2eSecret {
		t.Fatalf("run with the remote gone: %q", out)
	}
}

func TestExportStaysQuietEndToEnd(t *testing.T) {
	a, b, _ := twoMachines(t)
	a.mustRun(a.repo, "", "set", "-g", "REGION", "us-east-1", "--type", "str", "--desc", "d")
	a.mustRun(a.repo, "", "store", "push")
	b.stale()

	stdout, stderr, code := b.run(b.repo, "", "env", "export")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "export API_TOKEN='"+e2eSecret+"'") {
		t.Fatalf("env export: exit %d\nstdout: %s\nstderr: %q", code, stdout, stderr)
	}
	// It did not fetch either: the state is as stale as before, and the next command is the one that learns.
	if state, err := os.ReadFile(filepath.Join(b.ezcHome, "state.json")); err != nil || string(state) != staleState {
		t.Fatalf("env export touched state.json: %q (%v)", state, err)
	}
	if _, stderr, _ := b.run(b.repo, "", "list", "-g"); stderr != "store: 1 commit behind origin — run ezc store pull\n" {
		t.Fatalf("list -g after the export: %q", stderr)
	}
}
