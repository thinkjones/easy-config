package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const (
	joinBranch  = "ezc/join-sam"
	joinRequest = ".ezc/requests/sam.yaml"
	joinClosing = "\nAsk someone who can read the store to run: ezc user approve sam\nThen run ezc store pull.\n"
	joinTaken   = "The store already has a recipient named sam. Pick another name with --name."
	joinPRURL   = "https://github.com/acme/config/pull/7"
)

// joinFix is two machines: a, gene's, whose store has a remote (x.bare) and holds two encrypted files only gene
// reads, and b, a new machine with nothing on it, whose host name is sams-laptop.
type joinFix struct {
	t    *testing.T
	a, b *fake
	x    storeWriteFix // a's store, its bare remote and a third clone
}

// joinMachines is machine b about to join the store machine a made.
func joinMachines(t *testing.T) joinFix {
	t.Helper()
	a := newFake(t)
	j := joinFix{t: t, a: a, x: accessStore(t, a)}
	j.b = j.machine("sams-laptop")
	return j
}

// machine is one more new machine. When its test ends, nothing it printed or ran may hold its private key.
func (j joinFix) machine(host string) *fake {
	j.t.Helper()
	f := newFake(j.t)
	f.hostname = host
	j.t.Cleanup(func() { joinNoPrivateKey(j.t, f) })
	return f
}

// join runs `ezc join <the remote> args...` on f, which must succeed with nothing on stderr, and returns its stdout.
func (j joinFix) join(f *fake, args ...string) string {
	j.t.Helper()
	out, errOut, err := joinRun(j.t, f, append([]string{"join", j.x.bare}, args...)...)
	if err != nil || errOut != "" {
		j.t.Fatalf("join %v: %v\nstdout %q\nstderr %q", args, err, out, errOut)
	}
	return out
}

// remote runs git in the bare remote and returns what it printed; "" when git failed.
func (j joinFix) remote(args ...string) string {
	j.t.Helper()
	stdout, _, err := j.x.g.Run(proc.Command{Dir: j.x.bare, Name: "git", Args: args})
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(stdout), "\n")
}

// branches are the remote's branches.
func (j joinFix) branches() []string {
	return strings.Fields(j.remote("for-each-ref", "--format=%(refname:short)", "refs/heads/"))
}

// request is the request for sam on the remote's request branch.
func (j joinFix) request(f *fake) recipients.Request {
	j.t.Helper()
	q, err := recipients.ParseRequest("sam", []byte(j.remote("show", joinBranch+":"+joinRequest)), f.backend.ValidRecipient)
	if err != nil {
		j.t.Fatalf("the request on the remote: %v", err)
	}
	return q
}

// joinRun runs a command on f and returns what it printed and its error; it fails when the private key was shown.
func joinRun(t *testing.T, f *fake, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	stdout, stderr, err = accessRun(f, args...)
	joinNoPrivateKey(t, f)
	return stdout, stderr, err
}

// joinNoPrivateKey fails when a line of f's key file that is not a comment is in what f printed, in an error
// it would print, or in the arguments, environment or stdin of a program it ran.
func joinNoPrivateKey(t *testing.T, f *fake) {
	t.Helper()
	data, err := os.ReadFile(setupKeyFile(f))
	if err != nil {
		return
	}
	shown := []string{f.out.String(), f.errOut.String()}
	for _, c := range slices.Concat(f.gits, f.execs) {
		shown = append(shown, c.Args...)
		shown = append(shown, c.Env...)
		shown = append(shown, string(c.Stdin))
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, s := range shown {
			if strings.Contains(s, line) {
				t.Fatalf("the private key is in %q", s)
			}
		}
	}
}

// storeAsCloned fails unless f's store is on main, level with the remote's main, with nothing uncommitted,
// no branch of its own besides main and no requests folder.
func (j joinFix) storeAsCloned(f *fake) {
	j.t.Helper()
	if got := setupGitOut(j.t, f, "status", "--porcelain", "--untracked-files=all"); got != "" {
		j.t.Fatalf("left uncommitted:\n%s", got)
	}
	if got := setupGitOut(j.t, f, "branch", "--format=%(refname:short)"); got != "main" {
		j.t.Fatalf("local branches %q", got)
	}
	if got, want := setupGitOut(j.t, f, "rev-parse", "HEAD"), j.remote("rev-parse", "main"); got != want {
		j.t.Fatalf("the store is at %s, the remote's main at %s", got, want)
	}
	if _, err := os.Lstat(filepath.Join(setupStore(f), ezcDir, recipients.RequestsDir)); !errors.Is(err, os.ErrNotExist) {
		j.t.Fatalf("the requests folder was left: %v", err)
	}
}

// joinUser is the user of f's config.toml; "" when it has none or there is no config.
func joinUser(t *testing.T, f *fake) string {
	t.Helper()
	c, _, err := config.Load(setupConfig(f))
	if err != nil {
		t.Fatal(err)
	}
	return c.User
}

// joinPushes are the git commands f ran that push.
func joinPushes(f *fake) []proc.Command {
	var out []proc.Command
	for _, c := range f.gits {
		if slices.Contains(c.Args, "push") {
			out = append(out, c)
		}
	}
	return out
}

// joinFakeGh makes f a machine with gh: `gh repo clone` clones from, and `gh pr create` prints a pull request's
// URL, or fails with stderr when that is not "".
func joinFakeGh(t *testing.T, f *fake, from, stderr string) {
	t.Helper()
	setupFakeGh(t, f, from)
	clone := f.execFn
	f.execFn = func(c proc.Command) ([]byte, []byte, error) {
		if c.Name == "gh" && len(c.Args) > 1 && c.Args[0] == "pr" && c.Args[1] == "create" {
			if stderr != "" {
				return nil, []byte(stderr), errors.New("exit status 1")
			}
			return []byte(joinPRURL + "\n"), nil, nil
		}
		return clone(c)
	}
}

func TestJoin(t *testing.T) {
	j := joinMachines(t)
	main := j.remote("rev-parse", "main")
	want := "+ cloned the store: " + setupStore(j.b) + "\n" +
		"+ created your key: ~/.config/sops/age/keys.txt\n" + setupBackupNotice +
		"+ wrote " + setupConfig(j.b) + "\n" +
		"+ pushed your request: " + joinBranch + "\n" + joinClosing
	if got := j.join(j.b, "--name", "sam"); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}

	info, err := os.Stat(setupKeyFile(j.b))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the key file: %v, %v", info, err)
	}
	public := setupPublicKey(t, j.b)
	c, found, err := config.Load(setupConfig(j.b))
	if want := (config.Config{User: "sam", Backend: "fake", KeyFile: setupKeyFile(j.b), Store: config.Store{Remote: j.x.bare}}); err != nil || !found || !reflect.DeepEqual(c, want) {
		t.Fatalf("config %+v (%v), want %+v", c, err, want)
	}

	// The remote: main as it was, and one new branch whose only change is the request.
	if got := j.branches(); !slices.Equal(got, []string{joinBranch, "main"}) {
		t.Fatalf("branches %q", got)
	}
	if j.remote("rev-parse", "main") != main {
		t.Fatal("the remote's main moved")
	}
	if got := j.remote("diff", "--name-status", "main", joinBranch); got != "A\t"+joinRequest {
		t.Fatalf("the branch changes %q", got)
	}
	if got := j.remote("log", "--format=%s", "main.."+joinBranch); got != "request access for sam" {
		t.Fatalf("commits %q", got)
	}
	wantRequest := recipients.Request{Name: "sam", Key: public, Kind: recipients.KindUser, Host: "sams-laptop", Requested: "2026-10-11"}
	if got := j.request(j.b); got != wantRequest {
		t.Fatalf("request %+v, want %+v", got, wantRequest)
	}
	j.storeAsCloned(j.b)
	if len(j.b.execs) != 0 {
		t.Fatalf("ran %+v", j.b.execs)
	}
	// Nothing of the store was rewritten: sam cannot read it yet.
	accessReaders(t, j.b, filepath.Join(setupStore(j.b), ezcDir), accessGene)
}

func TestJoinTwicePushesNothingNew(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	tip := j.remote("rev-parse", joinBranch)
	keyBefore := readFile(t, setupKeyFile(j.b))
	configBefore := readFile(t, setupConfig(j.b))

	// The name is the config's now, and the day has changed.
	j.b.now = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	want := "✓ the store: " + setupStore(j.b) + "\n" +
		"✓ your key: ~/.config/sops/age/keys.txt\n" +
		"✓ the config: " + setupConfig(j.b) + "\n" +
		"✓ your request is waiting: " + joinBranch + "\n" + joinClosing
	if got := j.join(j.b); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
	if pushes := joinPushes(j.b); len(pushes) != 0 {
		t.Fatalf("pushed again: %+v", pushes)
	}
	if j.remote("rev-parse", joinBranch) != tip {
		t.Fatal("the request branch moved")
	}
	if readFile(t, setupKeyFile(j.b)) != keyBefore || readFile(t, setupConfig(j.b)) != configBefore {
		t.Fatal("the key file or the config was rewritten")
	}
	j.storeAsCloned(j.b)
}

func TestJoinOpensAPullRequest(t *testing.T) {
	j := joinMachines(t)
	joinFakeGh(t, j.b, j.x.bare, "")
	out, errOut, err := joinRun(t, j.b, "join", "acme/config", "--name", "sam")
	if err != nil || errOut != "" {
		t.Fatalf("%v\nstderr %q", err, errOut)
	}
	if want := "+ pushed your request: " + joinBranch + "\n+ opened a pull request: " + joinPRURL + "\n" + joinClosing; !strings.HasSuffix(out, want) {
		t.Fatalf("stdout:\n%s", out)
	}
	if len(j.b.execs) != 2 || !reflect.DeepEqual(j.b.execs[0].Args, []string{"repo", "clone", "acme/config", setupStore(j.b)}) {
		t.Fatalf("ran %+v", j.b.execs)
	}
	pr := j.b.execs[1]
	if pr.Name != "gh" || pr.Dir != setupStore(j.b) || !reflect.DeepEqual(pr.Args[:4], []string{"pr", "create", "--head", joinBranch}) {
		t.Fatalf("ran %+v", pr)
	}
	if got := j.request(j.b).Key; got != setupPublicKey(t, j.b) {
		t.Fatalf("the request's key is %q", got)
	}
	j.storeAsCloned(j.b)
}

func TestJoinWhenThePullRequestFails(t *testing.T) {
	j := joinMachines(t)
	joinFakeGh(t, j.b, j.x.bare, "pull request create failed: GraphQL: No commits between main and ezc/join-sam\n")
	out, errOut, err := joinRun(t, j.b, "join", "acme/config", "--name", "sam")
	if err != nil || errOut != "" {
		t.Fatalf("%v\nstderr %q", err, errOut)
	}
	want := "+ pushed your request: " + joinBranch + "\n" +
		"! No pull request was opened (gh: pull request create failed: GraphQL: No commits between main and ezc/join-sam). Your request is on its branch all the same.\n" + joinClosing
	if !strings.HasSuffix(out, want) {
		t.Fatalf("stdout:\n%s", out)
	}
	if got := j.branches(); !slices.Equal(got, []string{joinBranch, "main"}) {
		t.Fatalf("branches %q", got)
	}
}

func TestJoinWithoutGhForARemoteThatIsNotOnGitHub(t *testing.T) {
	j := joinMachines(t)
	j.b.paths["gh"] = "/usr/bin/gh"
	j.join(j.b, "--name", "sam")
	if len(j.b.execs) != 0 {
		t.Fatalf("ran %+v", j.b.execs)
	}

	// owner/repo is cloned through gh, so without gh nothing is made.
	c := j.machine("c")
	_, _, err := joinRun(t, c, "join", "acme/config", "--name", "sam")
	if err == nil || !strings.Contains(err.Error(), "gh is not installed") {
		t.Fatalf("err %v", err)
	}
	setupNothingMade(t, c)
}

func TestOnGitHub(t *testing.T) {
	for remote, want := range map[string]bool{
		"https://github.com/acme/config.git":     true,
		"https://tok@GitHub.com/acme/config":     true,
		"ssh://git@github.com:22/acme/config":    true,
		"git@github.com:acme/config.git":         true,
		"github.com:acme/config":                 true,
		"https://github.com.evil.test/acme/c":    false,
		"https://gitlab.com/github.com/config":   false,
		"git@gitlab.com:github.com/config.git":   false,
		"/srv/github.com:acme/config.git":        false,
		"/srv/git/config.git":                    false,
		"":                                       false,
		"https://example.test/?x=//github.com/a": false,
	} {
		if got := onGitHub(remote); got != want {
			t.Errorf("onGitHub(%q) = %v, want %v", remote, got, want)
		}
	}
}

func TestJoinAlreadyARecipient(t *testing.T) {
	j := joinMachines(t)
	// A second machine of gene's, with a copy of the first one's key file.
	write(t, setupKeyFile(j.b), accessGenePrivate)
	want := "+ cloned the store: " + setupStore(j.b) + "\n" +
		"✓ your key: ~/.config/sops/age/keys.txt\n" +
		"+ wrote " + setupConfig(j.b) + "\n" +
		"✓ gene can already read this store.\n"
	if got := j.join(j.b, "--name", "gene"); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
	if got := j.branches(); !slices.Equal(got, []string{"main"}) {
		t.Fatalf("branches %q", got)
	}
	if pushes := joinPushes(j.b); len(pushes) != 0 {
		t.Fatalf("pushed: %+v", pushes)
	}
	j.storeAsCloned(j.b)

	// The registry's name for the key is the one printed, whatever name was asked for.
	c := j.machine("c")
	write(t, setupKeyFile(c), accessGenePrivate)
	if got := j.join(c, "--name", "sam"); !strings.HasSuffix(got, "✓ gene can already read this store.\n") {
		t.Fatalf("stdout:\n%s", got)
	}
	if b, c := joinUser(t, j.b), joinUser(t, c); b != "gene" || c != "gene" {
		t.Fatalf("users %q and %q, want the registry's name for the key", b, c)
	}
}

func TestJoinAlreadyARecipientAsAMachine(t *testing.T) {
	a := newFake(t)
	x := accessStoreWith(t, a, map[string]recipients.Recipient{
		"gene":        accessEntry(accessGene),
		"gene-laptop": {Key: accessSam, Kind: recipients.KindMachine, Owner: "gene", Added: "2026-10-01", AddedBy: "gene"},
	}, accessGene, accessSam)
	f := newFake(t)
	write(t, setupKeyFile(f), "FAKE-KEY-bb02\n")
	out, _, err := joinRun(t, f, "join", x.bare, "--name", "sam")
	if err != nil || !strings.HasSuffix(out, "✓ gene-laptop can already read this store.\n") {
		t.Fatalf("%v\nstdout:\n%s", err, out)
	}
	if got := joinUser(t, f); got != "gene" {
		t.Fatalf("user %q, want the machine's owner", got)
	}
}

func TestJoinNeverChangesTheConfigsUser(t *testing.T) {
	j := joinMachines(t)
	if err := config.Save(setupConfig(j.b), config.Config{User: "samantha"}); err != nil {
		t.Fatal(err)
	}
	note := "! " + setupConfig(j.b) + " names you samantha, not sam, and was left as it is.\n"
	if got := j.join(j.b, "--name", "sam"); !strings.HasSuffix(got, "+ pushed your request: "+joinBranch+"\n"+note+joinClosing) {
		t.Fatalf("stdout:\n%s", got)
	}
	// Waiting, and then a recipient under the name asked for: the config's user still stays.
	if got := j.join(j.b, "--name", "sam"); !strings.HasSuffix(got, "✓ your request is waiting: "+joinBranch+"\n"+note+joinClosing) {
		t.Fatalf("stdout:\n%s", got)
	}
	accessWant(t, j.a, "✓ add sam\n✓ pushed to "+j.x.bare+"\n", "user", "add", "sam", setupPublicKey(t, j.b))
	if _, _, err := joinRun(t, j.b, "store", "pull"); err != nil {
		t.Fatal(err)
	}
	if got := j.join(j.b); !strings.HasSuffix(got, "✓ sam can already read this store.\n"+note) {
		t.Fatalf("stdout:\n%s", got)
	}
	if got := joinUser(t, j.b); got != "samantha" {
		t.Fatalf("user %q", got)
	}
}

func TestJoinAHostNameThatIsNotPlain(t *testing.T) {
	j := joinMachines(t)
	j.b.hostname = "sam's laptop: \x1b[2J\nkey: other"
	j.join(j.b, "--name", "sam")
	if q := j.request(j.b); q.Host != j.b.hostname || q.Key != setupPublicKey(t, j.b) {
		t.Fatalf("request %+v", q)
	}
}

func TestJoinANameThatIsTaken(t *testing.T) {
	t.Run("by a recipient", func(t *testing.T) {
		j := joinMachines(t)
		_, _, err := joinRun(t, j.b, "join", j.x.bare, "--name", "gene")
		if err == nil || err.Error() != "The store already has a recipient named gene. Pick another name with --name." {
			t.Fatalf("err %v", err)
		}
		storeWriteExitsOne(t, err)
		if got := j.branches(); !slices.Equal(got, []string{"main"}) {
			t.Fatalf("branches %q", got)
		}
		j.storeAsCloned(j.b)
		// The name that was refused is not this machine's user; the one that goes through is.
		if got := joinUser(t, j.b); got != "" {
			t.Fatalf("user %q after a refused name", got)
		}
		_, _, err = joinRun(t, j.b, "join", j.x.bare)
		wantUsage(t, err, "ezc join: say --name <name>")
		want := "✓ the store: " + setupStore(j.b) + "\n" +
			"✓ your key: ~/.config/sops/age/keys.txt\n" +
			"✓ the config: " + setupConfig(j.b) + "\n" +
			"+ pushed your request: ezc/join-gene-2\n" +
			"+ wrote " + setupConfig(j.b) + "\n" +
			"\nAsk someone who can read the store to run: ezc user approve gene-2\nThen run ezc store pull.\n"
		if got := j.join(j.b, "--name", "gene-2"); got != want {
			t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
		}
		if got := joinUser(t, j.b); got != "gene-2" {
			t.Fatalf("user %q", got)
		}
	})
	t.Run("by a request that is waiting", func(t *testing.T) {
		j := joinMachines(t)
		j.join(j.b, "--name", "sam")
		tip := j.remote("rev-parse", joinBranch)
		c := j.machine("c")
		_, _, err := joinRun(t, c, "join", j.x.bare, "--name", "sam")
		if err == nil || err.Error() != joinTaken {
			t.Fatalf("err %v", err)
		}
		if j.remote("rev-parse", joinBranch) != tip || len(joinPushes(c)) != 0 {
			t.Fatal("the waiting request was touched")
		}
		j.storeAsCloned(c)
		if got := joinUser(t, c); got != "" {
			t.Fatalf("user %q after a refused name", got)
		}
		// Another name goes through.
		if got := j.join(c, "--name", "sam-2"); !strings.Contains(got, "+ pushed your request: ezc/join-sam-2\n") {
			t.Fatalf("stdout:\n%s", got)
		}
	})
	t.Run("by a request pushed since this machine looked", func(t *testing.T) {
		j := joinMachines(t)
		c := j.machine("c")
		// c's request reaches the remote after b has cloned and before b pushes.
		git, raced := gittest.Runner(t), false
		j.b.git = func(cmd proc.Command) ([]byte, []byte, error) {
			if !raced && slices.Contains(cmd.Args, "ls-remote") {
				raced = true
				j.join(c, "--name", "sam")
			}
			return git(cmd)
		}
		_, _, err := joinRun(t, j.b, "join", j.x.bare, "--name", "sam")
		if !raced || err == nil || err.Error() != joinTaken {
			t.Fatalf("raced %v, err %v", raced, err)
		}
		if got := j.request(c).Key; got != setupPublicKey(t, c) {
			t.Fatalf("the waiting request's key is %q", got)
		}
		if b, c := joinUser(t, j.b), joinUser(t, c); b != "" || c != "sam" {
			t.Fatalf("users %q and %q", b, c)
		}
		j.storeAsCloned(j.b)
	})
}

func TestJoinFromAStoreThatIsAhead(t *testing.T) {
	j := joinMachines(t)
	if err := j.x.g.Clone(j.x.bare, setupStore(j.b)); err != nil {
		t.Fatal(err)
	}
	j.x.commit(setupStore(j.b), "common.yaml", "NAME: mine\n")
	_, _, err := joinRun(t, j.b, "join", j.x.bare, "--name", "sam")
	if err == nil || err.Error() != "The store has commits its remote does not. Run ezc store push, then run ezc join again." {
		t.Fatalf("err %v", err)
	}
	if got := j.branches(); !slices.Equal(got, []string{"main"}) || len(joinPushes(j.b)) != 0 {
		t.Fatalf("branches %q, pushes %+v", got, joinPushes(j.b))
	}
	if _, err := os.Lstat(setupKeyFile(j.b)); err == nil {
		t.Fatal("a key was made")
	}
}

func TestJoinWhenTheRemoteCannotBeReached(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	j.b.gitFail = "fetch"
	_, _, err := joinRun(t, j.b, "join", j.x.bare)
	if err == nil || err.Error() != "The store's remote could not be reached. Run ezc join again when it can be." {
		t.Fatalf("err %v", err)
	}
	storeWriteExitsOne(t, err)
}

func TestJoinARequestThatWasMerged(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	// Somebody merged the pull request on GitHub: the request is in main, and the branch is gone.
	public := setupPublicKey(t, j.b)
	q := recipients.Request{Name: "sam", Key: public, Kind: recipients.KindUser, Host: "sams-laptop", Requested: "2026-10-11"}
	j.x.commit(j.x.other, "requests/sam.yaml", string(q.Marshal()))
	j.x.push(j.x.other)
	j.x.git("push", "--quiet", "origin", "--delete", joinBranch)

	_, _, err := joinRun(t, j.b, "join", j.x.bare)
	if err == nil || err.Error() != "The store is behind its remote. Run ezc store pull, then run ezc join again." {
		t.Fatalf("behind: %v", err)
	}
	if _, _, err := joinRun(t, j.b, "store", "pull"); err != nil {
		t.Fatal(err)
	}
	if got := j.join(j.b); !strings.HasSuffix(got, "✓ your request is in the store\n"+joinClosing) {
		t.Fatalf("stdout:\n%s", got)
	}
	if got := readFile(t, filepath.Join(setupStore(j.b), joinRequest)); got != string(q.Marshal()) {
		t.Fatalf("the request in the store was rewritten:\n%s", got)
	}
	if pushes := joinPushes(j.b); len(pushes) != 0 {
		t.Fatalf("pushed: %+v", pushes)
	}

	// Somebody else's request under that name, in the store: the name is taken, and the file stays.
	c := j.machine("c")
	_, _, err = joinRun(t, c, "join", j.x.bare, "--name", "sam")
	if err == nil || err.Error() != joinTaken {
		t.Fatalf("err %v", err)
	}
	if got := readFile(t, filepath.Join(setupStore(c), joinRequest)); got != string(q.Marshal()) {
		t.Fatalf("the request in the store was rewritten:\n%s", got)
	}
}

func TestJoinAMachine(t *testing.T) {
	j := joinMachines(t)
	out := j.join(j.b, "--name", "gene-laptop", "--kind", "machine", "--owner", "gene")
	if want := "+ pushed your request: ezc/join-gene-laptop\n\nAsk someone who can read the store to run: ezc user approve gene-laptop\nThen run ezc store pull.\n"; !strings.HasSuffix(out, want) {
		t.Fatalf("stdout:\n%s", out)
	}
	data := j.remote("show", "ezc/join-gene-laptop:.ezc/requests/gene-laptop.yaml")
	q, err := recipients.ParseRequest("gene-laptop", []byte(data), j.b.backend.ValidRecipient)
	want := recipients.Request{Name: "gene-laptop", Key: setupPublicKey(t, j.b), Kind: recipients.KindMachine, Owner: "gene", Host: "sams-laptop", Requested: "2026-10-11"}
	if err != nil || q != want {
		t.Fatalf("request %+v (%v), want %+v", q, err, want)
	}
	// The machine's user is its owner.
	if c, _, _ := config.Load(setupConfig(j.b)); c.User != "gene" {
		t.Fatalf("config %+v", c)
	}

	// An owner the store does not know is refused before anything is pushed.
	c := j.machine("c")
	_, _, err = joinRun(t, c, "join", j.x.bare, "--name", "pat-laptop", "--kind", "machine", "--owner", "pat")
	if err == nil || err.Error() != "The store has no user named pat, and a machine belongs to one. Say --owner <user>." {
		t.Fatalf("err %v", err)
	}
	if len(joinPushes(c)) != 0 {
		t.Fatal("pushed")
	}
}

func TestJoinUsage(t *testing.T) {
	const pasted = "FAKE-KEY-0123456789abcdef"
	bare := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"join"}, "ezc join: give the store's repo: owner/repo, a URL or a path. Try `ezc help join`."},
		{[]string{"join", bare, bare}, "ezc join: give the store's repo: owner/repo, a URL or a path. Try `ezc help join`."},
		{[]string{"join", "--print", bare}, "ezc join: --print takes no repo. Try `ezc help join`."},
		{[]string{"join", bare, "--name", "sam", "--kind", "machine"}, "ezc join: --kind machine needs --owner, the user whose machine it is. Try `ezc help join`."},
		{[]string{"join", "--print", "--name", "sam", "--kind", "machine"}, "ezc join: --kind machine needs --owner, the user whose machine it is. Try `ezc help join`."},
		{[]string{"join", bare, "--name", "sam", "--owner", "gene"}, "ezc join: --owner goes with --kind machine only. Try `ezc help join`."},
		{[]string{"join", bare, "--name", "sam", "--kind", pasted}, "ezc join: --kind is user or machine. Try `ezc help join`."},
		{[]string{"join", bare, "--name", "sam", "--kind", "bot"}, "ezc join: --kind is user or machine. Try `ezc help join`."},
		{[]string{"join", bare, "--name", pasted}, "ezc join: --name takes " + nameRule + "."},
		{[]string{"join", bare, "--name", "sam", "--kind", "machine", "--owner", pasted}, "ezc join: --owner takes " + nameRule + "."},
		{[]string{"join", bare, "--name", "gene", "--kind", "machine", "--owner", "gene"}, "ezc join: a machine needs a name of its own, not its owner's. Say --name <name>."},
		{[]string{"join", bare}, "ezc join: say --name <name>: your name in the store, " + nameRule + "."},
		{[]string{"join", "--print"}, "ezc join: say --name <name>: your name in the store, " + nameRule + "."},
		{[]string{"join", pasted, "--name", "sam"}, "ezc join: the store's repo is owner/repo, a URL or the path of a folder that exists."},
	} {
		f := setupFake(t)
		out, errOut, err := joinRun(t, f, tc.args...)
		wantUsage(t, err, tc.want)
		if err.Error() != tc.want || strings.Contains(err.Error(), "FAKE-KEY-") || out != "" || errOut != "" {
			t.Errorf("%v: err %q\nstdout %q\nstderr %q", tc.args, err, out, errOut)
		}
		setupNothingMade(t, f)
		if len(f.gits)+len(f.execs) != 0 {
			t.Errorf("%v: ran %+v %+v", tc.args, f.gits, f.execs)
		}
	}
}

func TestJoinWithEzcStore(t *testing.T) {
	j := joinMachines(t)
	j.b.env["EZC_STORE"] = t.TempDir()
	_, _, err := joinRun(t, j.b, "join", j.x.bare, "--name", "sam")
	wantUsage(t, err, "ezc join: unset EZC_STORE. Join sets up the store in "+setupStore(j.b)+".")
	setupNothingMade(t, j.b)
}

func TestJoinKeyInEnvironment(t *testing.T) {
	j := joinMachines(t)
	j.b.env["SOPS_AGE_KEY"] = "FAKE-KEY-0123456789abcdef"
	for _, args := range [][]string{{"join", j.x.bare, "--name", "sam"}, {"join", "--print", "--name", "sam"}} {
		out, _, err := joinRun(t, j.b, args...)
		wantUsage(t, err, "ezc join: SOPS_AGE_KEY is set, so that key would be used instead of the one join registers. Unset it and run ezc join again.")
		if strings.Contains(err.Error(), "FAKE-KEY-") || out != "" {
			t.Fatalf("err %v, stdout %q", err, out)
		}
	}
	setupNothingMade(t, j.b)

	// The key a CI job is given is text too, and it is the one named when both are set.
	j.b.env["EZC_AGE_KEY"] = "FAKE-KEY-c1c1c1c1"
	for _, args := range [][]string{{"join", j.x.bare, "--name", "sam"}, {"join", "--print", "--name", "sam"}} {
		out, _, err := joinRun(t, j.b, args...)
		wantUsage(t, err, "ezc join: EZC_AGE_KEY is set, so that key would be used instead of the one join registers. Unset it and run ezc join again.")
		if strings.Contains(err.Error(), "FAKE-KEY-") || out != "" {
			t.Fatalf("err %v, stdout %q", err, out)
		}
	}
	setupNothingMade(t, j.b)
	delete(j.b.env, "EZC_AGE_KEY")

	delete(j.b.env, "SOPS_AGE_KEY")
	j.b.env["SOPS_AGE_KEY_FILE"] = "keys.txt"
	_, _, err := joinRun(t, j.b, "join", j.x.bare, "--name", "sam")
	wantUsage(t, err, "ezc join: SOPS_AGE_KEY_FILE must be an absolute path.")
	setupNothingMade(t, j.b)
}

func TestJoinAsksForTheNameOnATerminal(t *testing.T) {
	j := joinMachines(t)
	j.b.tty, j.b.stdin = true, "sam\n"
	out, errOut, err := joinRun(t, j.b, "join", j.x.bare)
	if err != nil || errOut != "Your name in this store: " || !strings.HasSuffix(out, joinClosing) {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
}

func TestJoinADifferentStoreIsRefused(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")
	other := setupRemote(t, "fake1someoneelse")
	_, _, err := joinRun(t, j.b, "join", other)
	if want := "a store is already set up at " + setupStore(j.b) + " (remote " + j.x.bare + "). Move it away to join another."; err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}
}

func TestJoinNotAStoreMakesNoKey(t *testing.T) {
	f := setupFake(t)
	_, _, err := joinRun(t, f, "join", t.TempDir(), "--name", "sam")
	if err == nil {
		t.Fatal("joined a folder that is not a repo")
	}
	storeWriteExitsOne(t, err)
	setupNothingMade(t, f)
}

func TestJoinAStoreWithNoRegistry(t *testing.T) {
	a := newFake(t)
	x := accessStoreWith(t, a, nil, accessGene)
	f := setupFake(t)
	_, _, err := joinRun(t, f, "join", x.bare, "--name", "sam")
	want := setupStore(f) + " has no recipients.yaml, so ezc does not manage who can read it. Whoever keeps the store adds your public key by hand: " + setupPublicKey(t, f)
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\nwant %s", err, want)
	}
}

func TestJoinPrint(t *testing.T) {
	j := joinMachines(t)
	out, errOut, err := joinRun(t, j.b, "join", "--print", "--name", "sam")
	if err != nil {
		t.Fatal(err)
	}
	public := setupPublicKey(t, j.b)
	if out != "ezc user add sam "+public+"\n" {
		t.Fatalf("stdout %q", out)
	}
	if want := "+ created your key: ~/.config/sops/age/keys.txt\n" + setupBackupNotice; errOut != want {
		t.Fatalf("stderr %q", errOut)
	}
	if info, err := os.Stat(setupKeyFile(j.b)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the key file: %v, %v", info, err)
	}
	// It needs no store and makes none, and it runs nothing.
	for _, path := range []string{setupStore(j.b), setupConfig(j.b)} {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("%s was created", path)
		}
	}
	if len(j.b.gits)+len(j.b.execs) != 0 {
		t.Fatalf("ran %+v %+v", j.b.gits, j.b.execs)
	}

	// Again, the key is the same one and nothing but the line is printed.
	again, errOut, err := joinRun(t, j.b, "join", "--print", "--name", "sam")
	if err != nil || again != out || errOut != "" {
		t.Fatalf("again: %v\nstdout %q\nstderr %q", err, again, errOut)
	}

	// The first machine pastes the line.
	accessWant(t, j.a, "✓ add sam\n✓ pushed to "+j.x.bare+"\n", strings.Fields(strings.TrimPrefix(out, "ezc "))...)
	if got := accessRegistry(t, filepath.Join(j.x.store, ezcDir)).Recipients["sam"]; got.Key != public || got.Kind != recipients.KindUser {
		t.Fatalf("sam is %+v", got)
	}
	accessReaders(t, j.a, filepath.Join(j.x.store, ezcDir), accessGene, public)

	// A machine's line names its kind and its owner, and is accepted as well.
	c := j.machine("c")
	out, _, err = joinRun(t, c, "join", "--print", "--name", "sam-laptop", "--kind", "machine", "--owner", "sam")
	laptop := setupPublicKey(t, c)
	if want := "ezc user add sam-laptop " + laptop + " --kind machine --owner sam\n"; err != nil || out != want {
		t.Fatalf("%v\nstdout %q\nwant   %q", err, out, want)
	}
	accessWant(t, j.a, "✓ add sam-laptop\n✓ pushed to "+j.x.bare+"\n", strings.Fields(strings.TrimPrefix(out, "ezc "))...)
	want := recipients.Recipient{Key: laptop, Kind: recipients.KindMachine, Owner: "sam", Added: "2026-10-11", AddedBy: "gene"}
	if got := accessRegistry(t, filepath.Join(j.x.store, ezcDir)).Recipients["sam-laptop"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("sam-laptop is %+v", got)
	}
}

func TestJoinPrintTakesTheNameFromTheConfig(t *testing.T) {
	f := setupFake(t)
	if err := config.Save(setupConfig(f), config.Config{User: "sam"}); err != nil {
		t.Fatal(err)
	}
	_, public := setupOwnKey(t, f)
	out, errOut, err := joinRun(t, f, "join", "--print")
	if err != nil || out != "ezc user add sam "+public+"\n" || errOut != "" {
		t.Fatalf("%v\nstdout %q\nstderr %q", err, out, errOut)
	}
}

func TestJoinNoGit(t *testing.T) {
	f := setupFake(t)
	delete(f.paths, "git")
	_, _, err := joinRun(t, f, "join", t.TempDir(), "--name", "sam")
	if err == nil || err.Error() != "git is not installed. Install it and run ezc join again." {
		t.Fatalf("err %v", err)
	}
	setupNothingMade(t, f)
}

func TestJoinWhenThePushFails(t *testing.T) {
	j := joinMachines(t)
	j.b.gitFail = "push"
	_, _, err := joinRun(t, j.b, "join", j.x.bare, "--name", "sam")
	if err == nil || err.Error() != "Your request could not be pushed (made to fail). Run ezc join again." {
		t.Fatalf("err %v", err)
	}
	storeWriteExitsOne(t, err)
	if got := j.branches(); !slices.Equal(got, []string{"main"}) {
		t.Fatalf("branches %q", got)
	}
	j.storeAsCloned(j.b)
	if got := joinUser(t, j.b); got != "" {
		t.Fatalf("user %q after a push that failed", got)
	}

	// Run again, it goes through with what the first run made.
	j.b.gitFail = ""
	want := "✓ the store: " + setupStore(j.b) + "\n" +
		"✓ your key: ~/.config/sops/age/keys.txt\n" +
		"✓ the config: " + setupConfig(j.b) + "\n" +
		"+ pushed your request: " + joinBranch + "\n" +
		"+ wrote " + setupConfig(j.b) + "\n" + joinClosing
	if got := j.join(j.b, "--name", "sam"); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
	if got := joinUser(t, j.b); got != "sam" {
		t.Fatalf("user %q", got)
	}
}
