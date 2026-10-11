package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// noon is the fake's clock when a test does not move it.
var noon = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

const (
	behindOne = "store: 1 commit behind origin — run ezc store pull\n"
	behindTwo = "store: 2 commits behind origin — run ezc store pull\n"
)

// gitFix is the fake's store as a git repo, its bare remote, and a second clone of that remote.
type gitFix struct {
	t                  *testing.T
	g                  gitstore.Git
	store, bare, other string
}

// localStore makes the fake's store a git repo with one commit and no remote.
func localStore(t *testing.T, f *fake) gitFix {
	t.Helper()
	x := gitFix{t: t, g: gitstore.Git{Run: gittest.WithIdentity(t)}, store: filepath.Join(f.env["EZC_HOME"], "store")}
	if err := x.g.Init(x.store); err != nil {
		t.Fatal(err)
	}
	x.commit(x.store, "common.yaml", "NAME: n\n")
	return x
}

// remoteStore is localStore pushed to a new bare remote, which a second clone follows.
func remoteStore(t *testing.T, f *fake) gitFix {
	t.Helper()
	x := localStore(t, f)
	x.bare = gittest.Bare(t)
	if err := x.g.SetRemote(x.store, x.bare); err != nil {
		t.Fatal(err)
	}
	x.push(x.store)
	x.other = filepath.Join(t.TempDir(), "other")
	if err := x.g.Clone(x.bare, x.other); err != nil {
		t.Fatal(err)
	}
	return x
}

// commit writes a file in dir's .ezc and commits it.
func (x gitFix) commit(dir, name, content string) {
	x.t.Helper()
	write(x.t, filepath.Join(dir, ezcDir, name), content)
	if done, err := x.g.Commit(dir, "change "+name, gitstore.Author{}, filepath.Join(ezcDir, name)); err != nil || !done {
		x.t.Fatalf("commit %s: done %v, %v", name, done, err)
	}
}

func (x gitFix) push(dir string) {
	x.t.Helper()
	if _, err := x.g.Push(dir); err != nil {
		x.t.Fatal(err)
	}
}

// theirs commits n files in the other clone and pushes them.
func (x gitFix) theirs(n int) {
	x.t.Helper()
	for i := range n {
		x.commit(x.other, "env.their"+string(rune('a'+i))+".yaml", "A: b\n")
	}
	x.push(x.other)
}

// stderrOf runs a command that must succeed and returns what it printed on stderr.
func stderrOf(t *testing.T, f *fake, args ...string) string {
	t.Helper()
	f.out.Reset()
	f.errOut.Reset()
	f.gits = nil
	if err := f.run(args...); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return f.errOut.String()
}

// fetches are the git commands the fake ran that fetch.
func fetches(f *fake) []proc.Command {
	var out []proc.Command
	for _, c := range f.gits {
		if slices.Contains(c.Args, "fetch") {
			out = append(out, c)
		}
	}
	return out
}

func statePath(f *fake) string { return filepath.Join(f.env["EZC_HOME"], "state.json") }

// attemptedAt writes a state.json whose last attempt, and last fetch, was at when.
func attemptedAt(t *testing.T, f *fake, when time.Time) {
	t.Helper()
	stamp := when.Format(time.RFC3339)
	write(t, statePath(f), `{"attempted": "`+stamp+`", "fetched": "`+stamp+`"}`)
}

func readStateFile(t *testing.T, f *fake) map[string]string {
	t.Helper()
	data, err := os.ReadFile(statePath(f))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%v in %s", err, data)
	}
	return m
}

func TestSyncAhead(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.commit(x.store, "env.a.yaml", "A: b\n")
	if got, want := stderrOf(t, f, "list", "-g"), "store: 1 commit ahead of origin — run ezc store push\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	x.commit(x.store, "env.b.yaml", "A: b\n")
	if got, want := stderrOf(t, f, "list", "-g"), "store: 2 commits ahead of origin — run ezc store push\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSyncBehind(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	if got := stderrOf(t, f, "list", "-g"); got != behindOne {
		t.Fatalf("got %q, want %q", got, behindOne)
	}
	f = newFake(t)
	x = remoteStore(t, f)
	x.theirs(2)
	if got := stderrOf(t, f, "list", "-g"); got != behindTwo {
		t.Fatalf("got %q, want %q", got, behindTwo)
	}
}

func TestSyncDiverged(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.commit(x.store, "env.mine.yaml", "A: b\n")
	x.theirs(2)
	if got, want := stderrOf(t, f, "list", "-g"), "store: diverged from origin (1 ahead, 2 behind) — run ezc store pull\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSyncUncommitted(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	write(t, filepath.Join(x.store, ezcDir, "env.new.yaml"), "A: b\n")
	if got, want := stderrOf(t, f, "list", "-g"), "store: uncommitted changes in "+x.store+"\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A reader says that the store's state cannot be read, and carries on.
func TestSyncOffABranch(t *testing.T) {
	f := newFake(t)
	x := storeWriteFix{remoteStore(t, f)}
	x.theirs(1)
	x.git("checkout", "--quiet", "--detach")
	want := "store: cannot tell how the store stands in " + x.store + " (the store is not on a branch)\n"
	if got := stderrOf(t, f, "list", "-g"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !strings.Contains(f.out.String(), "NAME") {
		t.Fatalf("list printed %q", f.out.String())
	}
	f.errTTY = true
	if got := stderrOf(t, f, "get", "NAME"); got != "\x1b[31m"+strings.TrimSuffix(want, "\n")+"\x1b[0m\n" {
		t.Fatalf("on a terminal: %q", got)
	}
}

func TestSyncRequestsAppended(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	x.commit(x.store, "requests/sam.yaml", "name: sam\n")
	if got, want := stderrOf(t, f, "list", "-g"), "store: diverged from origin (1 ahead, 1 behind) — run ezc store pull · 1 access request pending: sam\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	f = newFake(t)
	x = remoteStore(t, f)
	x.commit(x.store, "requests/sam.yaml", "name: sam\n")
	x.commit(x.store, "requests/kim.yaml", "name: kim\n")
	if got, want := stderrOf(t, f, "list", "-g"), "store: 2 commits ahead of origin — run ezc store push · 2 access requests pending: kim, sam\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSyncRequestsAlone(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.commit(x.store, "requests/sam.yaml", "name: sam\n")
	// Only the names of .yaml files count.
	x.commit(x.store, "requests/README.md", "notes\n")
	x.push(x.store)
	if got, want := stderrOf(t, f, "list", "-g"), "store: 1 access request pending: sam\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Whoever can push to the store names these files, and the names reach a terminal: only a recipient's name is shown or counted.
func TestSyncRequestsOnlyValidNames(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	for _, name := range []string{"sam", "kim-2", "Lee", "bad name", "evil\x1b[31m", "-dash", "a\rb"} {
		x.commit(x.store, "requests/"+name+".yaml", "name: x\n")
	}
	x.push(x.store)
	if got, want := stderrOf(t, f, "list", "-g"), "store: 2 access requests pending: kim-2, sam\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	f = newFake(t)
	x = remoteStore(t, f)
	for _, name := range []string{"Lee", "bad name", "evil\x1b[31m"} {
		x.commit(x.store, "requests/"+name+".yaml", "name: x\n")
	}
	x.push(x.store)
	if got := stderrOf(t, f, "list", "-g"); got != "" {
		t.Fatalf("all invalid: stderr %q", got)
	}
	// With something else to say, the line says only that.
	x.commit(x.store, "env.a.yaml", "A: b\n")
	if got, want := stderrOf(t, f, "list", "-g"), "store: 1 commit ahead of origin — run ezc store push\n"; got != want {
		t.Fatalf("all invalid, ahead: got %q, want %q", got, want)
	}
}

// A state.json that says nothing readable means never: one fetch, and a clean file after it.
func TestSyncUnreadableState(t *testing.T) {
	future := noon.Add(time.Hour).Format(time.RFC3339)
	for _, content := range []string{"not json", "null", `{"attempted": 7}`, `{"attempted": "yesterday"}`, `{"attempted": "` + future + `"}`} {
		f := newFake(t)
		remoteStore(t, f)
		write(t, statePath(f), content)
		if got := stderrOf(t, f, "list", "-g"); got != "" || len(fetches(f)) != 1 {
			t.Errorf("%s: stderr %q, fetches %v", content, got, fetches(f))
		}
		if state, want := readStateFile(t, f), noon.Format(time.RFC3339); state["attempted"] != want || state["fetched"] != want {
			t.Errorf("%s: state %v", content, state)
		}
	}
}

func TestInSyncPrintsNothing(t *testing.T) {
	f := newFake(t)
	remoteStore(t, f)
	for _, args := range [][]string{{"list", "-g"}, {"get", "NAME"}, {"run", "--", "true"}} {
		if got := stderrOf(t, f, args...); got != "" {
			t.Errorf("%v: stderr %q", args, got)
		}
	}
	if len(f.children) != 1 {
		t.Errorf("children %v", f.children)
	}
}

func TestSyncEnvAndRun(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	repo(t, f, map[string]string{"manifest.yaml": empty, "env.local.yaml": "A: b\n"})
	x.theirs(1)
	if got, want := stderrOf(t, f, "env"), behindOne+"environment local ✓ 2 vars from 2 files\n"; got != want {
		t.Fatalf("env: got %q, want %q", got, want)
	}
	if got := stderrOf(t, f, "run", "--", "true"); got != behindOne || len(f.children) != 1 {
		t.Fatalf("run: got %q, children %v", got, f.children)
	}
}

func TestFetchOnlyWhenStale(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	attemptedAt(t, f, noon.Add(-9*time.Minute))
	if got := stderrOf(t, f, "list", "-g"); got != "" || len(fetches(f)) != 0 {
		t.Fatalf("9 minutes: stderr %q, fetches %v", got, fetches(f))
	}
	if got := readStateFile(t, f)["attempted"]; got != noon.Add(-9*time.Minute).Format(time.RFC3339) {
		t.Fatalf("attempted rewritten to %q", got)
	}

	attemptedAt(t, f, noon.Add(-11*time.Minute))
	if got := stderrOf(t, f, "list", "-g"); got != behindOne || len(fetches(f)) != 1 {
		t.Fatalf("11 minutes: stderr %q, fetches %v", got, fetches(f))
	}
	state := readStateFile(t, f)
	if want := noon.Format(time.RFC3339); state["attempted"] != want || state["fetched"] != want {
		t.Fatalf("state %v, want both %s", state, want)
	}
}

func TestSyncKeepsWhatElseTheStateHolds(t *testing.T) {
	f := newFake(t)
	remoteStore(t, f)
	write(t, statePath(f), `{"other": {"n": 1}}`)
	stderrOf(t, f, "list", "-g")
	data, err := os.ReadFile(statePath(f))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil || string(m["other"]) == "" || string(m["attempted"]) == "" {
		t.Fatalf("state %s (%v)", data, err)
	}
}

func TestOfflineUsesCachedState(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(2)
	if got := stderrOf(t, f, "list", "-g"); got != behindTwo {
		t.Fatalf("got %q", got)
	}

	if err := os.RemoveAll(x.bare); err != nil {
		t.Fatal(err)
	}
	f.now = noon.Add(2 * time.Hour)
	want := behindTwo + "store: origin could not be reached; last checked 2 hours ago\n"
	if got := stderrOf(t, f, "list", "-g"); got != want {
		t.Fatalf("offline: got %q, want %q", got, want)
	}
	if f.out.String() != "NAME  -  store/common.yaml\n" {
		t.Fatalf("stdout %q", f.out.String())
	}
	state := readStateFile(t, f)
	if state["attempted"] != f.now.Format(time.RFC3339) || state["fetched"] != noon.Format(time.RFC3339) {
		t.Fatalf("state %v", state)
	}

	f.now = f.now.Add(5 * time.Minute)
	if got := stderrOf(t, f, "list", "-g"); got != behindTwo || len(fetches(f)) != 0 {
		t.Fatalf("inside 10 minutes: stderr %q, fetches %v", got, fetches(f))
	}
}

func TestOfflineNeverChecked(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	if err := os.RemoveAll(x.bare); err != nil {
		t.Fatal(err)
	}
	if got, want := stderrOf(t, f, "list", "-g"), "store: origin could not be reached; never checked\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOfflineAges(t *testing.T) {
	for since, want := range map[time.Duration]string{
		30 * time.Second: "just now",
		time.Minute:      "1 minute ago",
		59 * time.Minute: "59 minutes ago",
		time.Hour:        "1 hour ago",
		47 * time.Hour:   "1 day ago",
		72 * time.Hour:   "3 days ago",
	} {
		f := newFake(t)
		indicate(f.sys(), storeSync{Git: true, Offline: true, Fetched: noon.Add(-since)})
		if got, want := f.errOut.String(), "store: origin could not be reached; last checked "+want+"\n"; got != want {
			t.Errorf("%v: got %q, want %q", since, got, want)
		}
	}
}

func TestFetchTimeoutIsThreeSeconds(t *testing.T) {
	f := newFake(t)
	remoteStore(t, f)
	stderrOf(t, f, "list", "-g")
	got := fetches(f)
	if len(got) != 1 || got[0].Timeout != 3*time.Second {
		t.Fatalf("fetches %+v", got)
	}
	if !slices.Contains(got[0].Env, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("env %v", got[0].Env)
	}
}

func TestSyncStoreTakesTheAgeAndTheTimeout(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	attemptedAt(t, f, noon)
	s := syncStore(f.sys(), 0, 10*time.Second)
	got := fetches(f)
	if len(got) != 1 || got[0].Timeout != 10*time.Second {
		t.Fatalf("fetches %+v", got)
	}
	if !s.Git || s.Offline || s.Status.Behind != 1 || !s.Fetched.Equal(noon) || f.errOut.Len() != 0 {
		t.Fatalf("got %+v, stderr %q", s, f.errOut.String())
	}
}

func TestExportNeverFetchesOrPrints(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	if got := stderrOf(t, f, "list", "-g"); got != behindOne {
		t.Fatalf("got %q", got)
	}
	attemptedAt(t, f, noon.Add(-time.Hour))
	if got := stderrOf(t, f, "env", "export"); got != "" || len(f.gits) != 0 {
		t.Fatalf("stderr %q, git ran %v", got, f.gits)
	}
	if f.out.String() != "export NAME='n'\n" {
		t.Fatalf("stdout %q", f.out.String())
	}
}

func TestPlainFolderStore(t *testing.T) {
	f := newFake(t)
	plain := t.TempDir()
	f.env["EZC_STORE"] = plain
	write(t, filepath.Join(plain, ezcDir, "common.yaml"), "NAME: n\n")
	write(t, filepath.Join(plain, ezcDir, "requests", "sam.yaml"), "name: sam\n")
	repo(t, f, map[string]string{"manifest.yaml": m1, "env.local.yaml": "SIZE: 3\n"})
	for _, args := range [][]string{{"env"}, {"list"}, {"get", "NAME"}, {"run", "--", "true"}} {
		got := stderrOf(t, f, args...)
		if strings.Contains(got, "store:") {
			t.Errorf("%v: stderr %q", args, got)
		}
		// One question, whether the folder is a repo, and nothing after it.
		if len(f.gits) != 1 || f.gits[0].Dir != plain || len(fetches(f)) != 0 {
			t.Errorf("%v: git ran %v", args, f.gits)
		}
	}
	if _, err := os.Stat(statePath(f)); err == nil {
		t.Error("state.json was written")
	}
}

func TestLocalOnlyStore(t *testing.T) {
	f := newFake(t)
	x := localStore(t, f)
	if got := stderrOf(t, f, "list", "-g"); got != "" || len(fetches(f)) != 0 {
		t.Fatalf("stderr %q, fetches %v", got, fetches(f))
	}
	if _, err := os.Stat(statePath(f)); err == nil {
		t.Error("state.json was written")
	}
	write(t, filepath.Join(x.store, ezcDir, "env.new.yaml"), "A: b\n")
	if got, want := stderrOf(t, f, "list", "-g"), "store: uncommitted changes in "+x.store+"\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestColour(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.commit(x.store, "env.a.yaml", "A: b\n")
	ahead := "store: 1 commit ahead of origin — run ezc store push"
	if got := stderrOf(t, f, "list", "-g"); got != ahead+"\n" {
		t.Fatalf("no terminal: got %q", got)
	}
	f.errTTY = true
	if got, want := stderrOf(t, f, "list", "-g"), "\x1b[33m"+ahead+"\x1b[0m\n"; got != want {
		t.Fatalf("ahead: got %q, want %q", got, want)
	}
	f.env["NO_COLOR"] = "1"
	if got := stderrOf(t, f, "list", "-g"); got != ahead+"\n" {
		t.Fatalf("NO_COLOR: got %q", got)
	}

	f = newFake(t)
	f.errTTY = true
	x = remoteStore(t, f)
	x.theirs(1)
	if got, want := stderrOf(t, f, "list", "-g"), "\x1b[31m"+strings.TrimSuffix(behindOne, "\n")+"\x1b[0m\n"; got != want {
		t.Fatalf("behind: got %q, want %q", got, want)
	}

	// Requests alone and the offline line are amber; a line that is red stays red with requests on it.
	f = newFake(t)
	f.errTTY = true
	indicate(f.sys(), storeSync{Git: true, Requests: []string{"sam"}, Offline: true})
	if got, want := f.errOut.String(), "\x1b[33mstore: 1 access request pending: sam\x1b[0m\n\x1b[33mstore: origin could not be reached; never checked\x1b[0m\n"; got != want {
		t.Fatalf("requests alone: got %q, want %q", got, want)
	}
	f.errOut.Reset()
	indicate(f.sys(), storeSync{Git: true, Status: gitstore.Status{Behind: 1}, Requests: []string{"sam"}})
	if got := f.errOut.String(); !strings.HasPrefix(got, "\x1b[31mstore: 1 commit behind") || !strings.HasSuffix(got, "pending: sam\x1b[0m\n") {
		t.Fatalf("behind with requests: got %q", got)
	}
}

func TestGetStdoutUntouched(t *testing.T) {
	f := newFake(t)
	x := remoteStore(t, f)
	x.theirs(1)
	if got := stderrOf(t, f, "get", "NAME"); got != behindOne {
		t.Fatalf("stderr %q", got)
	}
	if f.out.String() != "n\n" {
		t.Fatalf("stdout %q", f.out.String())
	}
}

func TestKeyFileFromConfig(t *testing.T) {
	f := newFake(t)
	key := filepath.Join(t.TempDir(), "ezc.key")
	if err := config.Save(filepath.Join(f.env["EZC_HOME"], "config.toml"), config.Config{KeyFile: key}); err != nil {
		t.Fatal(err)
	}
	f.paths = map[string]string{"sops": "/bin/sops"}
	f.execFn = func(proc.Command) ([]byte, []byte, error) { return []byte("{}"), nil, nil }
	file := filepath.Join(t.TempDir(), "common.enc.yaml")
	write(t, file, "A: x\n")
	sys := f.sys()
	sys.Backend = nil // the default backend, whose key file comes from the config

	keyFiles := func() []string {
		t.Helper()
		f.execs = nil
		if _, err := backendFor(sys).Decrypt(file); err != nil {
			t.Fatal(err)
		}
		if len(f.execs) != 1 {
			t.Fatalf("execs %v", f.execs)
		}
		var got []string
		for _, kv := range f.execs[0].Env {
			if v, ok := strings.CutPrefix(kv, "SOPS_AGE_KEY_FILE="); ok {
				got = append(got, v)
			}
		}
		return got
	}
	if got := keyFiles(); !slices.Equal(got, []string{key}) {
		t.Fatalf("from the config: got %v, want %s", got, key)
	}
	if note, found := keySource(sys); note != key || found {
		t.Fatalf("keySource: %q, %v", note, found)
	}
	write(t, key, "k")
	if note, found := keySource(sys); note != key || !found {
		t.Fatalf("keySource: %q, %v", note, found)
	}

	other := filepath.Join(t.TempDir(), "other.key")
	f.env["SOPS_AGE_KEY_FILE"] = other
	if got := keyFiles(); !slices.Equal(got, []string{other}) {
		t.Fatalf("from the environment: got %v, want %s", got, other)
	}
	if note, _ := keySource(sys); note != other {
		t.Fatalf("keySource: %q", note)
	}
}

func TestBrokenConfigIsAnError(t *testing.T) {
	for _, args := range [][]string{{"env"}, {"env", "export"}, {"list"}, {"get", "NAME"}, {"run", "--", "true"}, {"set", "NAME", "v"}, {"set", "-g", "NAME", "v"}} {
		f := newFake(t)
		good(t, f)
		path := filepath.Join(f.env["EZC_HOME"], "config.toml")
		write(t, path, "user = [\n")
		err := f.run(args...)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%v: got %v", args, err)
		}
		if f.out.Len() != 0 || len(f.children) != 0 {
			t.Errorf("%v: stdout %q, children %v", args, f.out.String(), f.children)
		}
	}
	// A broken config does not stop the default backend or the key's source from being worked out.
	f := newFake(t)
	write(t, filepath.Join(f.env["EZC_HOME"], "config.toml"), "user = [\n")
	sys := f.sys()
	sys.Backend = nil
	if note, _ := keySource(sys); note != "~/.config/sops/age/keys.txt" || backendFor(sys) == nil {
		t.Fatalf("keySource %q", note)
	}
}
