//go:build unix

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
	"gopkg.in/yaml.v3"
)

// fakeGh puts a gh first on the machines' PATH that only records: call n leaves its arguments, one per line, in
// <n>.args and what it read on stdin in <n>.stdin, in the folder returned. The real gh is never run.
func fakeGh(t *testing.T, root string, machines ...*e2e) (log string) {
	t.Helper()
	bin := filepath.Join(root, "ghbin")
	log = filepath.Join(root, "ghlog")
	for _, d := range []string{bin, log} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nn=$(ls '" + log + "' | grep -c args)\nprintf '%s\\n' \"$@\" > '" + log + "'/$n.args\ncat > '" + log + "'/$n.stdin\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, m := range machines {
		m.path = bin + string(os.PathListSeparator) + m.path
	}
	return log
}

// ghCall is one run of the fake gh.
type ghCall struct{ args, stdin string }

// ghCalls are the runs the fake gh recorded in log, in order; args are joined with spaces.
func ghCalls(t *testing.T, log string) []ghCall {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(log, "*.args"))
	if err != nil {
		t.Fatal(err)
	}
	// Call 10 comes after call 9.
	sort.Slice(names, func(i, j int) bool {
		return len(names[i]) < len(names[j]) || len(names[i]) == len(names[j]) && names[i] < names[j]
	})
	var calls []ghCall
	for _, name := range names {
		args, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := os.ReadFile(strings.TrimSuffix(name, ".args") + ".stdin")
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, ghCall{strings.Join(strings.Fields(string(args)), " "), string(stdin)})
	}
	return calls
}

// secretLineOf is the private key in a key file: its one line that is not a comment.
func secretLineOf(t *testing.T, keyFile string) string {
	t.Helper()
	data, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			return line
		}
	}
	t.Fatalf("%s holds no private key", keyFile)
	return ""
}

// publicKeyOf is the public key a key file made by ezc names in its comment.
func publicKeyOf(t *testing.T, keyFile string) string {
	t.Helper()
	data, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if _, key, ok := strings.Cut(line, "public key: "); ok {
			return strings.TrimSpace(key)
		}
	}
	t.Fatalf("%s names no public key", keyFile)
	return ""
}

// dataKeys are the keys that encrypt the values of an encrypted file, as the key file's key unwraps them from the
// file's own entries: none when the file is not encrypted for it. It is what the key's holder can do without ezc.
func dataKeys(t *testing.T, file []byte, keyFile string) (keys []string) {
	t.Helper()
	text, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := age.ParseIdentities(bytes.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sops struct {
			Age []struct{ Enc string } `yaml:"age"`
		} `yaml:"sops"`
	}
	if err := yaml.Unmarshal(file, &doc); err != nil || len(doc.Sops.Age) == 0 {
		t.Fatalf("not an encrypted file (%v)", err)
	}
	for _, entry := range doc.Sops.Age {
		r, err := age.Decrypt(armor.NewReader(strings.NewReader(entry.Enc)), ids...)
		if err != nil {
			continue
		}
		key, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, string(key))
	}
	return keys
}

// wantShutOut fails when the key in gone, which was a recipient of the machine's store once, unwraps from any
// commit of file the key that encrypts the file's values now: whoever holds it and the store's history would
// read every value written since. reader is the key file the machine reads the store with.
func wantShutOut(t *testing.T, e *e2e, file, reader, gone string) {
	t.Helper()
	path := filepath.Join(e.store(), ".ezc", file)
	now, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	current := dataKeys(t, now, reader)
	if len(current) != 1 {
		t.Fatalf("the machine's key unwraps %d keys from %s", len(current), file)
	}
	for _, commit := range strings.Fields(e.git("log", "--all", "--diff-filter=AM", "--format=%H", "--", ".ezc/"+file)) {
		for _, old := range dataKeys(t, []byte(e.git("show", commit+":.ezc/"+file)), gone) {
			if old == current[0] {
				t.Fatalf("the removed key unwraps, from commit %s, the key that encrypts %s now", commit[:7], file)
			}
		}
	}
}

// watch keeps everything the commands of a test print, apart from what the test asks to see, and the texts that
// must be in none of it: the secrets' values and every private key.
type watch struct {
	t       *testing.T
	printed []string
	hidden  map[string]string // what must not be printed, by what it is
}

func newWatch(t *testing.T) *watch {
	return &watch{t: t, hidden: map[string]string{}}
}

// run runs ezc on a machine in its repo folder and keeps what it printed.
func (w *watch) run(e *e2e, stdin string, args ...string) (stdout, stderr string, code int) {
	w.t.Helper()
	stdout, stderr, code = e.run(e.repo, stdin, args...)
	w.printed = append(w.printed, fmt.Sprintf("ezc %s\nstdout: %s\nstderr: %s", strings.Join(args, " "), stdout, stderr))
	return stdout, stderr, code
}

// ok is run for a command that must exit 0.
func (w *watch) ok(e *e2e, stdin string, args ...string) (stdout string) {
	w.t.Helper()
	stdout, stderr, code := w.run(e, stdin, args...)
	if code != 0 {
		w.t.Fatalf("ezc %v: exit %d\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
	}
	return stdout
}

// hide names a text no command may print and no commit may hold.
func (w *watch) hide(what, text string) {
	w.t.Helper()
	if strings.TrimSpace(text) == "" {
		w.t.Fatalf("%s is empty", what)
	}
	w.hidden[what] = strings.TrimSpace(text)
}

// done fails when a command printed a hidden text, or when a store of machines holds one in a file or a commit.
func (w *watch) done(machines ...*e2e) {
	w.t.Helper()
	for what, text := range w.hidden {
		for _, out := range w.printed {
			if strings.Contains(out, text) {
				w.t.Errorf("%s was printed by:\n%s", what, strings.ReplaceAll(out, text, "<"+what+">"))
			}
		}
		for _, m := range machines {
			wantNoValue(w.t, m, text)
		}
	}
}

// reads fails unless the machine reads the store's secret as value.
func reads(t *testing.T, e *e2e, value string) {
	t.Helper()
	if stdout, stderr, code := e.run(e.repo, "", "get", "API_TOKEN", "--reveal"); code != 0 || stdout != value+"\n" {
		t.Fatalf("get --reveal: exit %d\nstdout: %s\nstderr: %s", code, strings.ReplaceAll(stdout, value, "<the value>"), stderr)
	}
}

// cannotRead fails unless the machine's get of the store's secret fails and prints nothing on stdout.
func cannotRead(t *testing.T, w *watch, e *e2e) {
	t.Helper()
	if stdout, stderr, code := w.run(e, "", "get", "API_TOKEN", "--reveal"); code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("get --reveal without access: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// joined is A, whose store holds one secret and is published to a bare repo, and B, which has a key of its own,
// asked for access as sam with ezc join, was approved by A and pulled.
func joined(t *testing.T) (w *watch, a, b *e2e, log string) {
	t.Helper()
	root := t.TempDir()
	w = newWatch(t)
	a, bare := publishedStore(t, root)
	b = newMachine(t, root, "b")
	log = fakeGh(t, root, a, b)
	w.hide("the secret's value", e2eSecret)
	w.hide("A's private key", secretLineOf(t, a.key))

	out := w.ok(b, "", "join", bare, "--name", "sam")
	for _, want := range []string{"+ cloned the store: " + b.store() + "\n", "+ created your key: ~/.config/sops/age/keys.txt\n", "+ pushed your request: ezc/join-sam\n", "ezc user approve sam\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("join: no %q in\n%s", want, out)
		}
	}
	w.hide("B's private key", secretLineOf(t, b.key))
	public := publicKeyOf(t, b.key)
	if secretLineOf(t, b.key) == secretLineOf(t, a.key) {
		t.Fatal("B has A's key")
	}

	// Before the approval B reads nothing, and doctor says what it is waiting for.
	cannotRead(t, w, b)
	if stdout, stderr, code := w.run(b, "", "doctor"); code != 1 || !strings.Contains(stdout, "request sam is waiting for approval") || !strings.Contains(stdout, "ezc user approve sam") {
		t.Fatalf("doctor before the approval: exit %d\n%s%s", code, stdout, stderr)
	}

	if out := w.ok(a, "", "user", "requests"); !strings.Contains(out, "sam\n") || !strings.Contains(out, "  key        "+public+"\n") {
		t.Fatalf("user requests does not show the whole key %s:\n%s", public, out)
	}
	out = w.ok(a, "", "user", "approve", "sam", "--yes")
	for _, want := range []string{"  key        " + public + "\n", "✓ approve sam\n", "✓ pushed to " + bare + "\n", "✓ deleted the branch ezc/join-sam\n", "Tell sam to run ezc store pull.\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("approve: no %q in\n%s", want, out)
		}
	}
	if out := w.ok(a, "", "user", "requests"); out != "No pending requests.\n" {
		t.Fatalf("user requests after the approval: %q", out)
	}

	if out := w.ok(b, "", "store", "pull"); out != "pulled 1 commit from origin\n" {
		t.Fatalf("pull: %q", out)
	}
	reads(t, b, e2eSecret)
	if stdout, stderr, code := w.run(b, "", "doctor"); code != 0 || !strings.Contains(stdout, "✓ access      your key is the recipient sam") {
		t.Fatalf("doctor after the approval: exit %d\n%s%s", code, stdout, stderr)
	}
	if calls := ghCalls(t, log); len(calls) != 0 {
		t.Fatalf("gh was run for a store that is not on GitHub: %+v", calls)
	}
	return w, a, b, log
}

func TestJoinApproveEndToEnd(t *testing.T) {
	w, a, b, _ := joined(t)
	reads(t, a, e2eSecret)
	if got, want := a.git("log", "--format=%s", "-1"), "approve sam"; got != want {
		t.Fatalf("the last commit is %q, want %q", got, want)
	}
	for _, m := range []*e2e{a, b} {
		if got := m.git("status", "--porcelain"); got != "" {
			t.Errorf("the store has uncommitted changes:\n%s", got)
		}
		if out := w.ok(m, "", "user", "list"); !strings.Contains(out, "gene") || !strings.Contains(out, "sam") || strings.Contains(out, "✗") {
			t.Errorf("user list:\n%s", out)
		}
	}
	w.done(a, b)
}

func TestRemoveEndToEnd(t *testing.T) {
	const fresh = "n3w-value-after-sam"
	w, a, b, _ := joined(t)
	w.hide("the secret's new value", fresh)

	out := w.ok(a, "", "user", "remove", "sam")
	for _, want := range []string{"✓ remove sam\n", "1 secret was readable by sam and needs a new value:\n  common.enc.yaml/API_TOKEN\n", "ezc set -g NAME"} {
		if !strings.Contains(out, want) {
			t.Errorf("remove: no %q in\n%s", want, out)
		}
	}
	if out := w.ok(a, "", "store", "status"); !strings.Contains(out, "rotation due: 1 secret (ezc store status --due lists them)\n") {
		t.Fatalf("status after the removal:\n%s", out)
	}
	if out := w.ok(a, "", "store", "status", "--due"); !strings.Contains(out, "API_TOKEN") || !strings.Contains(out, "sam") {
		t.Fatalf("status --due: %q", out)
	}
	reads(t, a, e2eSecret)

	// B has the commit that shut it out: the file it holds now is not encrypted for its key.
	if out := w.ok(b, "", "store", "pull"); out != "pulled 1 commit from origin\n" {
		t.Fatalf("pull: %q", out)
	}
	cannotRead(t, w, b)
	if stdout, stderr, code := w.run(b, "", "doctor"); code != 1 || !strings.Contains(stdout, "✗ access") {
		t.Fatalf("doctor on the removed machine: exit %d\n%s%s", code, stdout, stderr)
	}

	// The new value clears the entry, and the removed key never opens it.
	if out := w.ok(a, fresh, "set", "-g", "API_TOKEN"); !strings.Contains(out, "set API_TOKEN in store/common.enc.yaml") {
		t.Fatalf("set: %q", out)
	}
	if out := w.ok(a, "", "store", "status"); strings.Contains(out, "rotation due") {
		t.Fatalf("status after the new value:\n%s", out)
	}
	if out := w.ok(a, "", "store", "status", "--due"); out != "" {
		t.Fatalf("status --due after the new value: %q", out)
	}
	if stdout, stderr, code := w.run(a, "", "doctor"); code != 0 || strings.Contains(stdout, "rotation") {
		t.Fatalf("doctor after the new value: exit %d\n%s%s", code, stdout, stderr)
	}
	reads(t, a, fresh)
	wantShutOut(t, a, "common.enc.yaml", a.key, b.key)
	w.ok(a, "", "store", "push")
	w.ok(b, "", "store", "pull")
	cannotRead(t, w, b)
	if got := a.git("status", "--porcelain"); got != "" {
		t.Errorf("the store has uncommitted changes:\n%s", got)
	}

	w.done(a, b)
}

func TestBotEndToEnd(t *testing.T) {
	root := t.TempDir()
	w := newWatch(t)
	a, bare := publishedStore(t, root)
	log := fakeGh(t, root, a)
	w.hide("the secret's value", e2eSecret)
	w.hide("A's private key", secretLineOf(t, a.key))

	out := w.ok(a, "", "bot", "add", "ci", "--repo", "o/a")
	for _, want := range []string{"✓ add bot ci\n", "✓ pushed to " + bare + "\n", "✓ EZC_AGE_KEY set in o/a\n", "EZC_STORE_TOKEN"} {
		if !strings.Contains(out, want) {
			t.Errorf("bot add: no %q in\n%s", want, out)
		}
	}
	calls := ghCalls(t, log)
	if len(calls) != 1 || calls[0].args != "secret set EZC_AGE_KEY --repo o/a" {
		t.Fatalf("gh ran %d times, without the one secret set", len(calls))
	}
	// gh was given the key as one line, and that line is all a job needs.
	botKey := strings.TrimSuffix(calls[0].stdin, "\n")
	if !strings.HasPrefix(botKey, "AGE-SECRET-KEY-") || strings.ContainsAny(botKey, "\n\r ") || botKey+"\n" != calls[0].stdin {
		t.Fatalf("gh read %d bytes on stdin that are not one line holding a key", len(calls[0].stdin))
	}
	w.hide("the bot's private key", botKey)
	if botKey == secretLineOf(t, a.key) {
		t.Fatal("the bot was given A's key")
	}
	// The bot's key is on no disk of A's.
	for _, dir := range []string{a.home, a.ezcHome} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte(botKey)) {
				t.Errorf("%s holds the bot's private key", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if out := w.ok(a, "", "user", "list"); !strings.Contains(out, "ci") || !strings.Contains(out, "bot") || strings.Contains(out, "✗") {
		t.Fatalf("user list:\n%s", out)
	}

	// A job: the key gh was given and a token, both in its environment, and nothing else.
	job := func(name string) *e2e {
		m := newMachine(t, root, name)
		m.env = append(m.env, "EZC_AGE_KEY="+botKey, "EZC_STORE_TOKEN=x")
		return m
	}
	ci := job("ci")
	if stdout, stderr, code := w.run(ci, "", "setup", "--ci", "--store", bare); code != 0 || !strings.Contains(stdout, "+ cloned the store: "+ci.store()+"\n✓ the key: EZC_AGE_KEY\n") || stderr != "" {
		t.Fatalf("setup --ci: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stdout, stderr, code := ci.run(ci.repo, "", "get", "API_TOKEN", "--reveal"); code != 0 || stdout != e2eSecret+"\n" || stderr != "" {
		t.Fatalf("get --reveal in the job: exit %d\nstdout: %s\nstderr: %s", code, strings.ReplaceAll(stdout, e2eSecret, "<the value>"), stderr)
	}
	err := filepath.WalkDir(ci.home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("%s was made in the job's home folder", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	out = w.ok(a, "", "bot", "remove", "ci")
	for _, want := range []string{"✓ remove bot ci\n", "✓ EZC_AGE_KEY deleted from o/a\n", "1 secret was readable by ci and needs a new value:\n  common.enc.yaml/API_TOKEN\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("bot remove: no %q in\n%s", want, out)
		}
	}
	calls = ghCalls(t, log)
	if len(calls) != 2 || calls[1].args != "secret delete EZC_AGE_KEY --repo o/a" || calls[1].stdin != "" {
		t.Fatalf("gh ran %d times; the last: %q", len(calls), calls[len(calls)-1].args)
	}
	reads(t, a, e2eSecret)
	// The key gh was given unwraps nothing that encrypts the file from now on.
	botKeyFile := filepath.Join(root, "bot-key.txt")
	if err := os.WriteFile(botKeyFile, []byte(calls[0].stdin), 0o600); err != nil {
		t.Fatal(err)
	}
	wantShutOut(t, a, "common.enc.yaml", a.key, botKeyFile)

	// A job that starts now, with the key that was removed, is told that the key does not read the store.
	late := job("late")
	if stdout, stderr, code := w.run(late, "", "setup", "--ci", "--store", bare); code != 1 || stderr == "" {
		t.Fatalf("setup --ci with the removed key: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	cannotRead(t, w, late)
	w.done(a, ci)
}

func TestRotateEndToEnd(t *testing.T) {
	w := newWatch(t)
	// B holds only a copy of A's key file, as a second machine of the same person did before part 4.
	a, b, bare := twoMachines(t)
	w.hide("the secret's value", e2eSecret)
	oldKey := secretLineOf(t, a.key)
	w.hide("A's old private key", oldKey)
	before, err := os.ReadFile(a.key)
	if err != nil {
		t.Fatal(err)
	}
	reads(t, b, e2eSecret)

	out := w.ok(a, "", "user", "rotate")
	for _, want := range []string{"✓ rotate gene\n", "✓ pushed to " + bare + "\n", "✓ new key in "} {
		if !strings.Contains(out, want) {
			t.Errorf("rotate: no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "needs a new value") || strings.Contains(out, "need new values") {
		t.Errorf("a routine rotation listed secrets to rotate:\n%s", out)
	}
	if now, err := os.ReadFile(a.key); err != nil || !bytes.Equal(now, before) {
		t.Fatalf("rotate changed the old key file (%v)", err)
	}
	made, err := filepath.Glob(filepath.Join(filepath.Dir(a.key), "keys-*.txt"))
	if err != nil || len(made) != 1 {
		t.Fatalf("the new key files beside the old one: %v (%v)", made, err)
	}
	if info, err := os.Stat(made[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the new key file: %v, %v", info, err)
	}
	newKey := secretLineOf(t, made[0])
	w.hide("A's new private key", newKey)
	if newKey == oldKey {
		t.Fatal("the new file holds the old key")
	}
	if !strings.Contains(out, "✓ new key in "+made[0]+"\n") {
		t.Errorf("rotate does not name %s:\n%s", made[0], out)
	}
	config, err := os.ReadFile(filepath.Join(a.ezcHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `key_file = "` + made[0] + `"`; !strings.Contains(string(config), want) {
		t.Fatalf("config.toml has no %s:\n%s", want, config)
	}

	// A reads with the new key, and the store is level with its remote.
	reads(t, a, e2eSecret)
	if stdout, stderr, code := w.run(a, "", "doctor"); code != 0 || !strings.Contains(stdout, "✓ access      your key is the recipient gene") {
		t.Fatalf("doctor after the rotation: exit %d\n%s%s", code, stdout, stderr)
	}
	if out := w.ok(a, "", "store", "status"); !strings.Contains(out, "state     in sync\n") || strings.Contains(out, "rotation due") {
		t.Fatalf("status after the rotation:\n%s", out)
	}
	if got := a.git("status", "--porcelain"); got != "" {
		t.Errorf("the store has uncommitted changes:\n%s", got)
	}

	// The old key opens nothing from that commit on, and unwraps nothing that would.
	wantShutOut(t, a, "common.enc.yaml", made[0], a.key)
	if out := w.ok(b, "", "store", "pull"); out != "pulled 1 commit from origin\n" {
		t.Fatalf("pull: %q", out)
	}
	cannotRead(t, w, b)
	w.done(a, b)
}

// refused runs a command that must exit 1 and say want on stderr, and returns that stderr.
func (w *watch) refused(e *e2e, stdin, want string, args ...string) string {
	w.t.Helper()
	stdout, stderr, code := w.run(e, stdin, args...)
	if code != 1 || !strings.Contains(stderr, want) {
		w.t.Fatalf("ezc %v: exit %d, want 1 and %q\nstdout: %s\nstderr: %s", args, code, want, stdout, stderr)
	}
	return stderr
}

// onRemote are the commits of the bare repo that touched a file of the store's .ezc.
func onRemote(t *testing.T, e *e2e, bare, file string) string {
	t.Helper()
	out, errOut, code := e.exec(bare, "", "git", "log", "--all", "--format=%H", "--", ".ezc/"+file)
	if code != 0 {
		t.Fatalf("git log in the remote: exit %d\n%s", code, errOut)
	}
	return strings.TrimSpace(out)
}

// A key is removed and the removal pushed while another machine, whose last fetch is minutes old, writes a secret to
// a new file under the policy it still has, which names the removed key. Nothing of that file leaves the machine,
// and nothing more is written to it, until it is re-wrapped: the removed key never unwraps the key it has then.
func TestWriteUnderAStalePolicyEndToEnd(t *testing.T) {
	const fresh, fresher = "n3w-value-under-the-old-policy", "n3w-value-after-the-rewrap"
	w, a, b, _ := joined(t)
	bare := a.git("remote", "get-url", "origin")
	w.hide("the new secret's value", fresh)
	w.hide("the new secret's second value", fresher)

	// The key that will be removed is a bot's, printed once: it is kept in a file only for wantShutOut to try.
	botKey, stderr, code := a.run(a.repo, "", "bot", "add", "ci", "--print-private")
	if code != 0 || !strings.HasPrefix(botKey, "AGE-SECRET-KEY-") {
		t.Fatalf("bot add --print-private: exit %d\nstderr: %s", code, stderr)
	}
	w.hide("the bot's private key", botKey)
	botKeyFile := filepath.Join(t.TempDir(), "bot-key.txt")
	if err := os.WriteFile(botKeyFile, []byte(botKey), 0o600); err != nil {
		t.Fatal(err)
	}
	w.ok(b, "", "store", "pull")
	w.ok(a, "", "bot", "remove", "ci")

	// B fetched a moment ago, so this write asks the remote nothing: the new file is made for the bot's key too.
	w.ok(b, fresh, "set", "-g", "NEW_KEY", "--env", "staging", "--type", "secret", "--desc", "d")
	made, err := os.ReadFile(filepath.Join(b.store(), ".ezc", "env.staging.enc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dataKeys(t, made, botKeyFile)) != 1 {
		t.Fatal("the new file is not encrypted for the removed key: this test proves nothing")
	}

	w.refused(b, "", "behind origin", "store", "push")
	w.refused(b, "", "! env.staging.enc.yaml is not encrypted for the registry. Run ezc store rewrap before you set anything.\n", "store", "pull")
	w.refused(b, "", "env.staging.enc.yaml is not encrypted for the people recipients.yaml names, so nothing was pushed. Run ezc store rewrap.", "store", "push")
	w.refused(b, fresher, "env.staging.enc.yaml is not encrypted for the people recipients.yaml names. Run ezc store rewrap.", "set", "-g", "NEW_KEY", "--env", "staging")
	if commits := onRemote(t, b, bare, "env.staging.enc.yaml"); commits != "" {
		t.Fatalf("the remote has the file that the removed key reads, in %s", commits)
	}

	if out := w.ok(b, "", "store", "rewrap"); !strings.Contains(out, "✓ rewrap\n") || !strings.Contains(out, "✓ pushed to "+bare+"\n") {
		t.Fatalf("rewrap: %q", out)
	}
	wantShutOut(t, b, "env.staging.enc.yaml", b.key, botKeyFile)
	w.ok(b, fresher, "set", "-g", "NEW_KEY", "--env", "staging")
	w.ok(b, "", "store", "push")
	wantShutOut(t, b, "env.staging.enc.yaml", b.key, botKeyFile)
	for _, m := range []*e2e{a, b} {
		w.ok(m, "", "store", "pull")
		if out := w.ok(m, "", "user", "list"); strings.Contains(out, "✗") {
			t.Errorf("user list:\n%s", out)
		}
	}
	w.done(a, b)
}

// A removal that was committed and could not be pushed, then a value changed on the remote in the same file. The
// pull keeps the remote's file, which the removed key still reads: it says so, the value it used to say to set
// again is refused, and so is the push, until the file is re-wrapped.
func TestAccessChangeThatWasNotPushedEndToEnd(t *testing.T) {
	const theirs, fresh = "th3ir-value-before-the-pull", "n3w-value-after-the-rewrap"
	w, a, b, _ := joined(t)
	bare := a.git("remote", "get-url", "origin")
	w.hide("the value B set", theirs)
	w.hide("the secret's new value", fresh)

	// The remote takes no push for a while: the removal is committed on A and goes nowhere.
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.refused(a, "", "Committed, but the push failed (", "user", "remove", "sam")
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	// B, a recipient still as far as the remote knows, changes the secret.
	w.ok(b, theirs, "set", "-g", "API_TOKEN")
	w.ok(b, "", "store", "push")

	// A does what it was told to.
	w.refused(a, "", "behind origin", "store", "push")
	said := w.refused(a, "", "! common.enc.yaml was re-wrapped here and changed on the remote.", "store", "pull")
	if strings.Contains(said, "set these again") || strings.Contains(said, "ezc set -g") {
		t.Fatalf("the pull names a value to set in a file the removed key reads:\n%s", said)
	}
	w.refused(a, fresh, "common.enc.yaml is not encrypted for the people recipients.yaml names. Run ezc store rewrap.", "set", "-g", "API_TOKEN")
	w.refused(a, "", "so nothing was pushed. Run ezc store rewrap.", "store", "push")
	if out := w.ok(a, "", "store", "status", "--due"); !strings.Contains(out, "API_TOKEN") {
		t.Fatalf("the secret is no longer due before it has a new value: %q", out)
	}

	w.ok(a, "", "store", "rewrap")
	w.ok(a, fresh, "set", "-g", "API_TOKEN")
	reads(t, a, fresh)
	wantShutOut(t, a, "common.enc.yaml", a.key, b.key)
	if out := w.ok(a, "", "store", "status", "--due"); out != "" {
		t.Fatalf("status --due after the new value: %q", out)
	}
	w.ok(a, "", "store", "push")
	w.ok(b, "", "store", "pull")
	cannotRead(t, w, b)
	if got := a.git("status", "--porcelain"); got != "" {
		t.Errorf("the store has uncommitted changes:\n%s", got)
	}
	w.done(a, b)
}
