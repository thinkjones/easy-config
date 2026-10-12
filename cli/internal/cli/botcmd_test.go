package cli

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const (
	botNote = "The bot also needs to read the store repo. Create a fine-grained token that can read that repo's contents and save it as the EZC_STORE_TOKEN secret in each repo. ezc cannot create that credential.\n"
	// botOldPrivate is the private half of accessSam, the key botStore registers for the bot.
	botOldPrivate = "FAKE-KEY-bb02\n"
	botGhSaid     = "gh: HTTP 403: Resource not accessible by integration"
)

// botFake is a fake gh: it records every call, with what stdin held, and fails for the repo fail names.
type botFake struct {
	calls []proc.Command
	fail  string
}

func botFakeGh(f *fake) *botFake {
	g := &botFake{}
	f.paths["gh"] = "/usr/bin/gh"
	f.execFn = func(c proc.Command) ([]byte, []byte, error) {
		if c.Name != "gh" {
			return nil, nil, errors.New("not gh")
		}
		c.Stdin = slices.Clone(c.Stdin)
		g.calls = append(g.calls, c)
		if g.fail != "" && slices.Contains(c.Args, g.fail) {
			return []byte("never shown\n"), []byte("HTTP 403: Resource not accessible by integration\n"), errors.New("exit status 1")
		}
		return nil, nil, nil
	}
	return g
}

// sets fails unless gh was asked to set EZC_AGE_KEY in each of repos, in order and nothing else, with one private key
// on stdin every time. It returns that key as gh was given it.
func (g *botFake) sets(t *testing.T, repos ...string) string {
	t.Helper()
	if len(g.calls) != len(repos) {
		t.Fatalf("gh ran %d times, want %d: %+v", len(g.calls), len(repos), g.calls)
	}
	private := ""
	for i, c := range g.calls {
		if want := []string{"secret", "set", "EZC_AGE_KEY", "--repo", repos[i]}; !reflect.DeepEqual(c.Args, want) {
			t.Fatalf("gh ran %q, want %q", c.Args, want)
		}
		if i > 0 && string(c.Stdin) != private {
			t.Fatal("the repos were given different keys")
		}
		private = string(c.Stdin)
	}
	return private
}

// deletes fails unless gh was asked to delete EZC_AGE_KEY from each of repos, in order and nothing else.
func (g *botFake) deletes(t *testing.T, repos ...string) {
	t.Helper()
	if len(g.calls) != len(repos) {
		t.Fatalf("gh ran %d times, want %d: %+v", len(g.calls), len(repos), g.calls)
	}
	for i, c := range g.calls {
		if want := []string{"secret", "delete", "EZC_AGE_KEY", "--repo", repos[i]}; !reflect.DeepEqual(c.Args, want) || len(c.Stdin) != 0 {
			t.Fatalf("gh ran %q with %d bytes on stdin, want %q", c.Args, len(c.Stdin), want)
		}
	}
}

// botHolds is what a failure says of the repos that took a key the store then did not register.
func botHolds(repos string) string {
	return "EZC_AGE_KEY was set in " + repos + " to a key the store does not name, which reads nothing. The store was not changed."
}

// botPublic is the public half of a fake private key.
func botPublic(t *testing.T, private string) string {
	t.Helper()
	h, ok := strings.CutPrefix(strings.TrimSpace(private), "FAKE-KEY-")
	if !ok || h == "" || strings.ContainsAny(h, " \n") {
		t.Fatalf("not one private key: %d bytes", len(private))
	}
	return "fake1" + h
}

// botKeyNowhere fails when the private key is in an argument, a working directory or the environment of a program
// the command ran, in a file under the machine's home or ezc's folder (the store is there), in the store's history,
// or in one of texts.
func botKeyNowhere(t *testing.T, f *fake, x storeWriteFix, private string, texts ...string) {
	t.Helper()
	key := strings.TrimSpace(private)
	if key == "" {
		t.Fatal("no key to look for")
	}
	for _, c := range slices.Concat(f.execs, f.gits) {
		for _, s := range slices.Concat([]string{c.Name, c.Dir}, c.Args, c.Env) {
			if strings.Contains(s, key) {
				t.Fatalf("the private key was given to %s outside stdin", c.Name)
			}
		}
	}
	for _, c := range f.gits {
		if strings.Contains(string(c.Stdin), key) {
			t.Fatal("the private key was given to git")
		}
	}
	for path, data := range setupSnapshot(t, f.env["HOME"], f.env["EZC_HOME"]) {
		if strings.Contains(data, key) {
			t.Fatalf("the private key is in %s", path)
		}
	}
	for i, s := range append(texts, x.git("log", "-p", "--all")) {
		if strings.Contains(s, key) {
			t.Fatalf("the private key is in text %d", i)
		}
	}
}

// botStore is gene's machine and a store read by gene and the bot ci, whose key is delivered to repos.
func botStore(t *testing.T, f *fake, repos ...string) storeWriteFix {
	t.Helper()
	return accessStoreWith(t, f, map[string]recipients.Recipient{
		"gene": accessEntry(accessGene),
		"ci":   {Key: accessSam, Kind: recipients.KindBot, Added: "2026-10-01", AddedBy: "gene", Repos: repos},
	}, accessGene, accessSam)
}

// botOpens is whether the private key opens every encrypted file of the store.
func botOpens(f *fake, x storeWriteFix, private string) bool {
	for _, file := range accessEncFiles {
		if f.backend.CanDecrypt(x.file(file), []byte(private)) != nil {
			return false
		}
	}
	return true
}

func TestBotAdd(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	out, errOut, err := accessRun(f, "bot", "add", "ci", "--repo", "o/a", "--repo", "o/b")
	if err != nil || errOut != "" {
		t.Fatalf("%v, stderr %q", err, errOut)
	}
	if want := "✓ add bot ci\n✓ pushed to " + x.bare + "\n✓ EZC_AGE_KEY set in o/a\n✓ EZC_AGE_KEY set in o/b\n" + botNote; out != want {
		t.Fatalf("stdout %q\nwant   %q", out, want)
	}
	private := g.sets(t, "o/a", "o/b")
	public := botPublic(t, private)

	dir := filepath.Join(x.store, ezcDir)
	want := recipients.Recipient{Key: public, Kind: recipients.KindBot, Added: "2026-10-11", AddedBy: "gene", Repos: []string{"o/a", "o/b"}}
	if got := accessRegistry(t, dir).Recipients["ci"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("the entry is %+v, want %+v", got, want)
	}
	accessReaders(t, f, dir, accessGene, public)
	if !botOpens(f, x, private) {
		t.Fatal("the key gh was given does not open the store's files")
	}
	accessLevel(t, x)
	x.wantClean()
	botKeyNowhere(t, f, x, private, out, errOut)
	x.git("update-ref", "refs/remotes/origin/main", "HEAD~1")
	x.wantCommit("add bot ci", ".fakepolicy", "common.enc.yaml", "env.prod.enc.yaml", recipients.File)
}

func TestBotAddNamesARepoOnce(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	if _, _, err := accessRun(f, "bot", "add", "ci", "--repo", "o/a", "--repo", "o/a"); err != nil {
		t.Fatal(err)
	}
	g.sets(t, "o/a")
	if got := accessRegistry(t, filepath.Join(x.store, ezcDir)).Recipients["ci"].Repos; !slices.Equal(got, []string{"o/a"}) {
		t.Fatalf("repos %q", got)
	}
}

// Review Focus 5: without gh nothing is written, and nothing is even asked of git.
func TestBotAddWithoutGh(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	accessUntouched(t, f, x, "gh is not installed, so the key cannot be saved in a repo. Install it (https://cli.github.com), or pass --print-private without --repo and save the key yourself.", "bot", "add", "ci", "--repo", "o/a")
	if len(f.gits) != 0 || len(f.execs) != 0 {
		t.Fatalf("ran %d git commands and %d others", len(f.gits), len(f.execs))
	}
}

// Review Focus 5: a delivery that fails leaves no commit, a clean tree and the registry as it was.
func TestBotAddDeliveryFails(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)

	g.fail = "o/a"
	accessUntouched(t, f, x, "EZC_AGE_KEY could not be set in o/a ("+botGhSaid+"). Nothing was changed.", "bot", "add", "ci", "--repo", "o/a", "--repo", "o/b")
	botKeyNowhere(t, f, x, g.sets(t, "o/a"), f.out.String(), f.errOut.String())

	// The first repo now holds a key that was never registered: the error says so.
	g.calls, g.fail = nil, "o/b"
	accessUntouched(t, f, x, "EZC_AGE_KEY could not be set in o/b ("+botGhSaid+"). "+botHolds("o/a")+" Run the same ezc bot add again.", "bot", "add", "ci", "--repo", "o/a", "--repo", "o/b")
	private := g.sets(t, "o/a", "o/b")
	botKeyNowhere(t, f, x, private, f.out.String(), f.errOut.String())
	if botOpens(f, x, private) {
		t.Fatal("the key that was not registered opens the store's files")
	}
	x.wantClean()
	accessReaders(t, f, filepath.Join(x.store, ezcDir), accessGene)

	// Run again, it goes through with a new key.
	g.calls, g.fail = nil, ""
	if _, _, err := accessRun(f, "bot", "add", "ci", "--repo", "o/a", "--repo", "o/b"); err != nil {
		t.Fatal(err)
	}
	if again := g.sets(t, "o/a", "o/b"); again == private || !botOpens(f, x, again) {
		t.Fatal("the second run did not deliver a new key that opens the store")
	}
}

// blind is a backend whose proof always fails.
type blind struct{ backend.Backend }

func (blind) CanDecrypt(path string, private []byte) error {
	return errors.New(path + ": " + string(private))
}

func TestBotKeyIsProvedBeforeItIsDelivered(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	head, before := x.git("rev-parse", "HEAD"), setupSnapshot(t, filepath.Join(x.store, ezcDir))
	f.out.Reset()
	a := accessOpen(t, f)
	a.b = blind{f.backend}
	next, err := a.reg.With("ci", recipients.Recipient{Key: "fake1dd04", Kind: recipients.KindBot, Added: "2026-10-11", AddedBy: "gene", Repos: []string{"o/a"}})
	if err != nil {
		t.Fatal(err)
	}
	_, done, err := giveBotKey(f.sys(), a, "ci", "the same ezc bot add", change{next: next, message: "add bot ci"}, []byte("FAKE-KEY-dd04\n"), []string{"o/a"}, false)
	if done || err == nil || err.Error() != "the new key cannot read common.enc.yaml. Nothing was changed." {
		t.Fatalf("done %v, err %v", done, err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("gh ran: %+v", g.calls)
	}
	if f.out.String() != "" || x.git("rev-parse", "HEAD") != head {
		t.Fatalf("stdout %q, or a commit was made", f.out.String())
	}
	x.wantClean()
	if after := setupSnapshot(t, filepath.Join(x.store, ezcDir)); !mapsEqual(before, after) {
		t.Fatal("a file of the store changed")
	}
}

func TestBotAddPrintPrivate(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	out, errOut, err := accessRun(f, "bot", "add", "ci", "--print-private")
	if err != nil {
		t.Fatal(err)
	}
	if want := "✓ add bot ci\n✓ pushed to " + x.bare + "\n" + botNote; errOut != want {
		t.Fatalf("stderr %q\nwant   %q", errOut, want)
	}
	// Stdout is the key and a newline, and nothing else.
	public := botPublic(t, out)
	if out != "FAKE-KEY-"+strings.TrimPrefix(public, "fake1")+"\n" {
		t.Fatalf("stdout is %d bytes that are not one key and a newline", len(out))
	}
	entry := accessRegistry(t, filepath.Join(x.store, ezcDir)).Recipients["ci"]
	if entry.Key != public || entry.Kind != recipients.KindBot || len(entry.Repos) != 0 {
		t.Fatalf("the entry is %+v", entry)
	}
	if !botOpens(f, x, out) || len(f.execs) != 0 {
		t.Fatalf("the printed key does not open the store, or a program ran: %+v", f.execs)
	}
	botKeyNowhere(t, f, x, out, errOut)
}

func TestBotAddPrintPrivateWithRepos(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	out, errOut, err := accessRun(f, "bot", "add", "ci", "--repo", "o/a", "--print-private")
	if err != nil {
		t.Fatal(err)
	}
	if want := "✓ add bot ci\n✓ pushed to " + x.bare + "\n✓ EZC_AGE_KEY set in o/a\n" + botNote; errOut != want {
		t.Fatalf("stderr %q\nwant   %q", errOut, want)
	}
	if private := g.sets(t, "o/a"); out != strings.TrimSpace(private)+"\n" {
		t.Fatal("stdout is not the key gh was given and a newline")
	}
}

func TestBotUsage(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	head := x.git("rev-parse", "HEAD")
	private := strings.TrimSpace(accessGenePrivate)
	const nameRule = ": the name is lower case letters, digits and -, starting with a letter or a digit."
	const repoRule = "ezc bot add: --repo is a GitHub repo, as owner/repo."
	for _, c := range []struct {
		want string
		args []string
	}{
		{"ezc bot: say add, remove or rotate.", []string{"bot"}},
		{"ezc bot add: give a name.", []string{"bot", "add"}},
		{"ezc bot add: name the repos that get the key with --repo, or pass --print-private.", []string{"bot", "add", "ci"}},
		{"ezc bot add" + nameRule, []string{"bot", "add", "CI", "--repo", "o/a"}},
		{"ezc bot add" + nameRule, []string{"bot", "add", private, "--print-private"}},
		{repoRule, []string{"bot", "add", "ci", "--repo", "not-a-repo"}},
		{repoRule, []string{"bot", "add", "ci", "--repo", "o/a", "--repo", private}},
		{repoRule, []string{"bot", "add", "ci", "--repo", "https://github.com/o/a"}},
		{"ezc bot remove: give a name.", []string{"bot", "remove"}},
		{"ezc bot remove" + nameRule, []string{"bot", "remove", private}},
		{"ezc bot rotate: give a name.", []string{"bot", "rotate"}},
		{"ezc bot rotate" + nameRule, []string{"bot", "rotate", private}},
	} {
		out, errOut, err := accessRun(f, c.args...)
		wantUsage(t, err, c.want)
		if out != "" || len(f.gits) != 0 || len(g.calls) != 0 || strings.Contains(err.Error()+errOut, "FAKE-KEY") {
			t.Errorf("%v: stdout %q, git ran %d times, gh %d, error %v", c.args[:2], out, len(f.gits), len(g.calls), err)
		}
	}
	if x.git("rev-parse", "HEAD") != head {
		t.Fatal("a commit was made")
	}
}

func TestBotAddRefused(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	accessUntouched(t, f, x, "gene is already a recipient.", "bot", "add", "gene", "--repo", "o/a")
	if len(g.calls) != 0 {
		t.Fatalf("gh ran: %+v", g.calls)
	}
}

func TestBotAddPushFails(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	f.gitFail = pushFails
	out, _, err := accessRun(f, "bot", "add", "ci", "--repo", "o/a")
	var unpushed *unpushedError
	if !errors.As(err, &unpushed) {
		t.Fatalf("got %v", err)
	}
	// The commit is made and the repo holds its key: the rest is said, and the push is owed.
	if want := "✓ add bot ci\n✓ EZC_AGE_KEY set in o/a\n" + botNote; out != want {
		t.Fatalf("stdout %q\nwant   %q", out, want)
	}
	if !botOpens(f, x, g.sets(t, "o/a")) {
		t.Fatal("the delivered key does not open the store")
	}
	if got := x.subjects(); !slices.Equal(got, []string{"add bot ci"}) {
		t.Fatalf("commits %q", got)
	}
}

const botDueList = "2 secrets were readable by ci and need new values:\n  common.enc.yaml/TOKEN\n  env.prod.enc.yaml/TOKEN\nGive each a new value with ezc set -g NAME.\n"

func TestBotRemove(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a", "o/b")
	g := botFakeGh(f)
	out, errOut, err := accessRun(f, "bot", "remove", "ci")
	if err != nil || errOut != "" {
		t.Fatalf("%v, stderr %q", err, errOut)
	}
	if want := "✓ remove bot ci\n✓ pushed to " + x.bare + "\n✓ EZC_AGE_KEY deleted from o/a\n✓ EZC_AGE_KEY deleted from o/b\n" + botDueList; out != want {
		t.Fatalf("stdout %q\nwant   %q", out, want)
	}
	g.deletes(t, "o/a", "o/b")
	dir := filepath.Join(x.store, ezcDir)
	if reg := accessRegistry(t, dir); len(reg.Recipients) != 1 || reg.Recipients["gene"].Key != accessGene {
		t.Fatalf("the registry is %+v", reg)
	}
	accessReaders(t, f, dir, accessGene)
	if botOpens(f, x, botOldPrivate) {
		t.Fatal("the removed key still opens the store")
	}
	due, err := recipients.LoadDue(dir)
	if err != nil || len(due) != 2 || due["common.enc.yaml/TOKEN"] != (recipients.DueEntry{Removed: "ci", On: "2026-10-11"}) {
		t.Fatalf("due %v, %v", due, err)
	}
	accessLevel(t, x)
	x.git("update-ref", "refs/remotes/origin/main", "HEAD~1")
	x.wantCommit("remove bot ci", ".fakepolicy", "common.enc.yaml", "env.prod.enc.yaml", recipients.File, recipients.DueFile)
}

func TestBotRemoveRefused(t *testing.T) {
	f := newFake(t)
	x := accessStoreWith(t, f, map[string]recipients.Recipient{
		"gene": accessEntry(accessGene),
		"sam":  accessEntry(accessSam),
	}, accessGene, accessSam)
	g := botFakeGh(f)
	accessUntouched(t, f, x, "sam is not a bot. Remove it with ezc user remove.", "bot", "remove", "sam")
	accessUntouched(t, f, x, "nobody is not a recipient.", "bot", "remove", "nobody")
	if len(g.calls) != 0 {
		t.Fatalf("gh ran: %+v", g.calls)
	}
}

func TestBotRemoveSaysWhatCouldNotBeDeleted(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a", "o/b")
	g := botFakeGh(f)
	g.fail = "o/a"
	accessWant(t, f, "✓ remove bot ci\n✓ pushed to "+x.bare+"\n! EZC_AGE_KEY could not be deleted from o/a ("+botGhSaid+"). Delete it there yourself.\n✓ EZC_AGE_KEY deleted from o/b\n"+botDueList, "bot", "remove", "ci")
	g.deletes(t, "o/a", "o/b")

	// Without gh the bot is removed all the same: that is what stops its key.
	f = newFake(t)
	x = botStore(t, f, "o/a", "o/b")
	accessWant(t, f, "✓ remove bot ci\n✓ pushed to "+x.bare+"\n! gh is not installed, so EZC_AGE_KEY was not deleted from o/a, o/b. Delete it there yourself.\n"+botDueList, "bot", "remove", "ci")
	if len(f.execs) != 0 {
		t.Fatalf("a program ran: %+v", f.execs)
	}
}

// A registry is whatever was pushed: a repo in it that is no owner/repo never reaches gh, and is not printed raw.
func TestBotRemoveNeverGivesGhARepoThatIsNotOne(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "--help", "o/\x1b[31mb", "o/a")
	g := botFakeGh(f)
	accessWant(t, f, "✓ remove bot ci\n✓ pushed to "+x.bare+"\n! recipients.yaml named a repo that is not owner/repo (--help), so nothing was deleted there.\n! recipients.yaml named a repo that is not owner/repo (o/?[31mb), so nothing was deleted there.\n✓ EZC_AGE_KEY deleted from o/a\n"+botDueList, "bot", "remove", "ci")
	g.deletes(t, "o/a")
}

func TestBotRemoveDeletesNothingWhenNothingWasChanged(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a")
	g := botFakeGh(f)
	f.gitFail = "commit"
	accessUntouched(t, f, x, "the change could not be committed: made to fail. Nothing was changed.", "bot", "remove", "ci")
	if len(g.calls) != 0 {
		t.Fatalf("gh ran: %+v", g.calls)
	}
}

func TestBotRemovePushFails(t *testing.T) {
	f := newFake(t)
	botStore(t, f, "o/a")
	g := botFakeGh(f)
	f.gitFail = pushFails
	out, _, err := accessRun(f, "bot", "remove", "ci")
	var unpushed *unpushedError
	if !errors.As(err, &unpushed) {
		t.Fatalf("got %v", err)
	}
	if want := "✓ remove bot ci\n✓ EZC_AGE_KEY deleted from o/a\n" + botDueList; out != want {
		t.Fatalf("stdout %q\nwant   %q", out, want)
	}
	g.deletes(t, "o/a")
}

func TestBotRotate(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a", "o/b")
	g := botFakeGh(f)
	out, errOut, err := accessRun(f, "bot", "rotate", "ci")
	if err != nil || errOut != "" {
		t.Fatalf("%v, stderr %q", err, errOut)
	}
	if want := "✓ rotate bot ci\n✓ pushed to " + x.bare + "\n✓ EZC_AGE_KEY set in o/a\n✓ EZC_AGE_KEY set in o/b\n"; out != want {
		t.Fatalf("stdout %q\nwant   %q", out, want)
	}
	private := g.sets(t, "o/a", "o/b")
	public := botPublic(t, private)
	if public == accessSam {
		t.Fatal("the key did not change")
	}
	dir := filepath.Join(x.store, ezcDir)
	want := recipients.Recipient{Key: public, Kind: recipients.KindBot, Added: "2026-10-01", AddedBy: "gene", Repos: []string{"o/a", "o/b"}}
	if got := accessRegistry(t, dir).Recipients["ci"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("the entry is %+v, want %+v", got, want)
	}
	accessReaders(t, f, dir, accessGene, public)
	if !botOpens(f, x, private) {
		t.Fatal("the new key does not open the store")
	}
	for _, file := range accessEncFiles {
		if f.backend.CanDecrypt(x.file(file), []byte(botOldPrivate)) == nil {
			t.Fatalf("the old key still opens %s", file)
		}
	}
	// A routine rotation leaves nothing to rotate (decision 6).
	missing(t, x.file(recipients.DueFile))
	accessLevel(t, x)
	x.wantClean()
	botKeyNowhere(t, f, x, private, out, errOut)
	x.git("update-ref", "refs/remotes/origin/main", "HEAD~1")
	x.wantCommit("rotate bot ci", ".fakepolicy", "common.enc.yaml", "env.prod.enc.yaml", recipients.File)
}

func TestBotRotateCompromised(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a")
	g := botFakeGh(f)
	list := strings.Replace(botDueList, "readable by ci and", "readable by ci (old key) and", 1)
	accessWant(t, f, "✓ rotate bot ci\n✓ pushed to "+x.bare+"\n✓ EZC_AGE_KEY set in o/a\n"+list, "bot", "rotate", "ci", "--compromised")
	g.sets(t, "o/a")
	due, err := recipients.LoadDue(filepath.Join(x.store, ezcDir))
	if err != nil || len(due) != 2 {
		t.Fatalf("due %v, %v", due, err)
	}
	for _, k := range []string{"common.enc.yaml/TOKEN", "env.prod.enc.yaml/TOKEN"} {
		if e := due[k]; e != (recipients.DueEntry{Removed: "ci (old key)", On: "2026-10-11"}) {
			t.Errorf("%s: %+v", k, e)
		}
	}
	x.git("update-ref", "refs/remotes/origin/main", "HEAD~1")
	x.wantCommit("rotate bot ci", ".fakepolicy", "common.enc.yaml", "env.prod.enc.yaml", recipients.File, recipients.DueFile)
}

func TestBotRotateWithoutRepos(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f)
	accessUntouched(t, f, x, "ci has no repos to deliver a new key to. Pass --print-private and save the key yourself.", "bot", "rotate", "ci")

	out, errOut, err := accessRun(f, "bot", "rotate", "ci", "--print-private", "--compromised")
	if err != nil {
		t.Fatal(err)
	}
	list := strings.Replace(botDueList, "readable by ci and", "readable by ci (old key) and", 1)
	if want := "✓ rotate bot ci\n✓ pushed to " + x.bare + "\n" + list; errOut != want {
		t.Fatalf("stderr %q\nwant   %q", errOut, want)
	}
	public := botPublic(t, out)
	if out != "FAKE-KEY-"+strings.TrimPrefix(public, "fake1")+"\n" || accessRegistry(t, filepath.Join(x.store, ezcDir)).Recipients["ci"].Key != public {
		t.Fatal("stdout is not the registered key's private half and a newline")
	}
	if !botOpens(f, x, out) || botOpens(f, x, botOldPrivate) || len(f.execs) != 0 {
		t.Fatal("the new key does not open the store, the old one does, or a program ran")
	}
	botKeyNowhere(t, f, x, out, errOut)
}

func TestBotRotateRefused(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a")
	accessUntouched(t, f, x, "gh is not installed, so the key cannot be saved in a repo. Install it (https://cli.github.com), or pass --print-private without --repo and save the key yourself.", "bot", "rotate", "ci")
	g := botFakeGh(f)
	accessUntouched(t, f, x, "gene is not a bot.", "bot", "rotate", "gene")
	accessUntouched(t, f, x, "nobody is not a recipient.", "bot", "rotate", "nobody")
	if len(g.calls) != 0 {
		t.Fatalf("gh ran: %+v", g.calls)
	}

	f = newFake(t)
	x = botStore(t, f, "o/a", "--body-file=\x07/etc/passwd")
	g = botFakeGh(f)
	accessUntouched(t, f, x, "recipients.yaml names a repo for ci that is not owner/repo (--body-file=?/etc/passwd). Correct it there, then run this again.", "bot", "rotate", "ci")
	if len(g.calls) != 0 {
		t.Fatalf("gh ran: %+v", g.calls)
	}
}

func TestBotRotateDeliveryFails(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a", "o/b")
	g := botFakeGh(f)
	g.fail = "o/b"
	accessUntouched(t, f, x, "EZC_AGE_KEY could not be set in o/b ("+botGhSaid+"). "+botHolds("o/a")+" Run ezc bot rotate ci again.", "bot", "rotate", "ci", "--compromised")
	private := g.sets(t, "o/a", "o/b")
	botKeyNowhere(t, f, x, private, f.out.String(), f.errOut.String())
	// The old key is still the bot's.
	if !botOpens(f, x, botOldPrivate) || botOpens(f, x, private) {
		t.Fatal("the store is not as it was")
	}
	x.wantClean()
}

// A commit that fails after every repo took the key leaves those repos with a key nobody registered.
func TestBotAddCommitFails(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	g := botFakeGh(f)
	f.gitFail = "commit"
	accessUntouched(t, f, x, "the change could not be committed: made to fail. "+botHolds("o/a, o/b")+" Run the same ezc bot add again.", "bot", "add", "ci", "--repo", "o/a", "--repo", "o/b", "--print-private")
	private := g.sets(t, "o/a", "o/b")
	botKeyNowhere(t, f, x, private, f.out.String(), f.errOut.String())
	if f.errOut.String() != "" || botOpens(f, x, private) {
		t.Fatalf("stderr %q, or the key that was not registered opens the store", f.errOut.String())
	}
	x.wantClean()
}

func TestBotRotateCommitFails(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a", "o/b")
	g := botFakeGh(f)
	f.gitFail = "commit"
	accessUntouched(t, f, x, "the change could not be committed: made to fail. "+botHolds("o/a, o/b")+" Run ezc bot rotate ci again.", "bot", "rotate", "ci", "--print-private")
	private := g.sets(t, "o/a", "o/b")
	botKeyNowhere(t, f, x, private, f.out.String(), f.errOut.String())
	if !botOpens(f, x, botOldPrivate) || botOpens(f, x, private) {
		t.Fatal("the store is not as it was")
	}
	x.wantClean()
}

func TestBotRotatePushFails(t *testing.T) {
	f := newFake(t)
	x := botStore(t, f, "o/a")
	g := botFakeGh(f)
	f.gitFail = pushFails
	out, _, err := accessRun(f, "bot", "rotate", "ci")
	var unpushed *unpushedError
	if !errors.As(err, &unpushed) || out != "✓ rotate bot ci\n✓ EZC_AGE_KEY set in o/a\n" {
		t.Fatalf("got %v, stdout %q", err, out)
	}
	if !botOpens(f, x, g.sets(t, "o/a")) || botOpens(f, x, botOldPrivate) {
		t.Fatal("the commit does not hold the new key alone")
	}
}

// closed is a stdout that takes nothing.
type closed struct{}

func (closed) Write([]byte) (int, error) { return 0, errors.New("write |1: broken pipe") }

// The key on stdout may be its only copy: a write that fails is not a success.
func TestBotPrintPrivateSaysWhenStdoutFails(t *testing.T) {
	for _, c := range []struct {
		want string
		args []string
	}{
		{"The bot ci is registered, but its key could not be written to stdout and is lost. Run ezc bot rotate ci --print-private for a new one.", []string{"bot", "add", "ci", "--print-private"}},
		{"The bot ci is registered and its key is saved in o/a, but it could not be written to stdout. Run ezc bot rotate ci --print-private for a new one.", []string{"bot", "add", "ci", "--print-private", "--repo", "o/a"}},
	} {
		f := newFake(t)
		x := accessStore(t, f)
		botFakeGh(f)
		sys := f.sys()
		sys.Stdout = closed{}
		err := Run(c.args, sys)
		if err == nil || err.Error() != c.want {
			t.Fatalf("%v: got %v\nwant %s", c.args, err, c.want)
		}
		storeWriteExitsOne(t, err)
		// The bot is registered, as the message says, and the rest was still said.
		if got := x.git("log", "-1", "--format=%s"); got != "add bot ci" || !strings.HasSuffix(f.errOut.String(), botNote) {
			t.Fatalf("last commit %q, stderr %q", got, f.errOut.String())
		}
	}

	// A rotation too, and the push it also owes is still said.
	f := newFake(t)
	botStore(t, f)
	f.gitFail = pushFails
	sys := f.sys()
	sys.Stdout = closed{}
	err := Run([]string{"bot", "rotate", "ci", "--print-private"}, sys)
	const want = "The bot ci is registered, but its key could not be written to stdout and is lost. Run ezc bot rotate ci --print-private for a new one. Committed, but the push failed ("
	if err == nil || !strings.HasPrefix(err.Error(), want) || !strings.HasSuffix(err.Error(), "). Run ezc store push.") {
		t.Fatalf("got %v", err)
	}
}

// commented is a backend whose new key is the text of a key file: comment lines, then the key.
type commented struct{ *backend.Fake }

func (c commented) NewKey() (string, []byte, error) {
	public, private, err := c.Fake.NewKey()
	return public, append([]byte("# created: 2026-10-11\n# public key: "+public+"\n"), private...), err
}

// Only the key's own line is delivered, to gh and to stdout.
func TestBotDeliversTheSecretLineAlone(t *testing.T) {
	for _, args := range [][]string{
		{"bot", "add", "ci", "--repo", "o/a", "--print-private"},
		{"bot", "rotate", "ci", "--print-private"},
	} {
		f := newFake(t)
		var x storeWriteFix
		if args[1] == "add" {
			x = accessStore(t, f)
		} else {
			x = botStore(t, f, "o/a")
		}
		g := botFakeGh(f)
		sys := f.sys()
		sys.Backend = commented{f.backend}
		if err := Run(args, sys); err != nil {
			t.Fatal(err)
		}
		out := f.out.String()
		public := botPublic(t, out)
		if out != "FAKE-KEY-"+strings.TrimPrefix(public, "fake1")+"\n" || g.sets(t, "o/a") != out {
			t.Fatalf("%v: stdout %q, gh's stdin %q: want one line, the same in both", args[:2], out, g.calls[0].Stdin)
		}
		if accessRegistry(t, filepath.Join(x.store, ezcDir)).Recipients["ci"].Key != public || !botOpens(f, x, out) {
			t.Fatalf("%v: the line delivered is not the registered key", args[:2])
		}
	}
}
