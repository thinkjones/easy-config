package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/ghcli"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/setup"
)

// joinFlags are `ezc join`'s flags.
type joinFlags struct {
	name, kind, owner string
	print             bool
}

func newJoinCmd(sys System) *cobra.Command {
	var o joinFlags
	cmd := &cobra.Command{
		Use:   "join <owner/repo | URL | path>",
		Short: "ask for access to a store from this machine",
		Long: `Ask to read an existing store's secrets from this machine: clone the store, create your key when there is none, write ezc's config, and push a request (your name, your public key and this machine's name) on a branch of its own, ezc/join-<name>. When gh is installed and the store is on GitHub, a pull request is opened for it. Someone who can read the store then runs ` + "`ezc user approve <name>`" + `, and ` + "`ezc store pull`" + ` here brings the secrets you can now read.

Nothing of the store's main branch is changed, and only the public key leaves this machine. Run again, it changes only what is missing; a request that is already waiting is not pushed twice. --kind machine --owner <user> asks for a second key of a user who is already a recipient.

With --print it takes no repo and needs no store: it prints the ` + "`ezc user add`" + ` line for this machine's key, to give to someone who can read the store.`,
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case o.print && len(args) > 0:
				return usagef("%s: --print takes no repo. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			case !o.print && len(args) != 1:
				return usagef("%s: give the store's repo: owner/repo, a URL or a path. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// No flag's value is echoed: what was pasted may be a private key.
			switch {
			case o.kind != recipients.KindUser && o.kind != recipients.KindMachine:
				return usagef("%s: --kind is user or machine. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			case o.kind == recipients.KindMachine && o.owner == "":
				return usagef("%s: --kind machine needs --owner, the user whose machine it is. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			case o.kind == recipients.KindUser && o.owner != "":
				return usagef("%s: --owner goes with --kind machine only. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			}
			source := ""
			if !o.print {
				source = args[0]
			}
			return runJoin(sys, o, source)
		},
	}
	cmd.Flags().StringVar(&o.name, "name", "", "your name in the store, or this machine's ("+nameRule+")")
	cmd.Flags().StringVar(&o.kind, "kind", recipients.KindUser, "user, or machine for a second key of a user")
	cmd.Flags().StringVar(&o.owner, "owner", "", "the user a machine belongs to")
	cmd.Flags().BoolVar(&o.print, "print", false, "print the `ezc user add` line for this machine's key, and push nothing")
	return cmd
}

func runJoin(sys System, o joinFlags, source string) error {
	// Join sets up the default store, as setup does, so EZC_STORE is left out of the paths it works with.
	p, err := paths.Resolve(func(key string) string {
		if key == "EZC_STORE" {
			return ""
		}
		return sys.Getenv(key)
	}, sys.GOOS)
	if err != nil {
		return err
	}
	if !o.print && sys.Getenv("EZC_STORE") != "" {
		return usagef("ezc join: unset EZC_STORE. Join sets up the store in %s.", p.Store())
	}
	if o.name != "" && !recipients.ValidName(o.name) {
		return usagef("ezc join: --name takes %s.", nameRule)
	}
	if o.owner != "" && !recipients.ValidName(o.owner) {
		return usagef("ezc join: --owner takes %s.", nameRule)
	}
	// A key the environment supplies is the one every command reads with, so it is the one to ask access for.
	envVar, envKeyFile := keyInEnvironment(sys)
	switch {
	case envVar != "" && envKeyFile == "":
		return usagef("ezc join: %s is set, so that key would be used instead of the one join registers. Unset it and run ezc join again.", envVar)
	case envVar != "" && !filepath.IsAbs(envKeyFile):
		return usagef("ezc join: %s must be an absolute path.", envVar)
	}
	cfg, found, err := config.Load(p.Config())
	if err != nil {
		return err
	}

	name := o.name
	if name == "" {
		name = cfg.User
	}
	if name == "" && sys.StdinIsTerminal() {
		name = (&asker{sys: sys, in: bufio.NewReader(sys.Stdin)}).ask("Your name in this store: ")
	}
	if !recipients.ValidName(name) {
		return usagef("ezc join: say --name <name>: your name in the store, %s.", nameRule)
	}
	if name == o.owner {
		return usagef("ezc join: a machine needs a name of its own, not its owner's. Say --name <name>.")
	}
	github := setup.IsSlug(source)
	if !o.print {
		// A path is checked here, so that git is never left to quote an argument that is no repo at all.
		source = localSource(sys, source)
		if !github && !strings.Contains(source, ":") {
			if info, err := os.Stat(source); err != nil || !info.IsDir() {
				return usagef("ezc join: the store's repo is owner/repo, a URL or the path of a folder that exists.")
			}
		}
	}

	kf := envKeyFile
	if kf == "" {
		kf = keyFile(sys)
	}
	if kf == "" {
		return errors.New("can't find your home directory, where your key is kept: set HOME.")
	}
	be := backendFor(sys)
	e := setup.Env{Git: storeGit(sys), Backend: be, Run: sys.Exec, LookPath: sys.LookPath, Now: sys.Now, Paths: p, GOOS: sys.GOOS}
	created := fmt.Sprintf("+ created your key: %s\n  Back this file up now. Without it your secrets cannot be read, and it cannot be recovered.\n", tilde(sys, kf))

	if o.print {
		public, made, err := setup.Key(e, kf)
		if err != nil {
			return err
		}
		if made {
			// Stdout holds the line to paste and nothing else.
			fmt.Fprint(sys.Stderr, created)
		}
		line := "ezc user add " + name + " " + public
		if o.kind == recipients.KindMachine {
			line += " --kind machine --owner " + o.owner
		}
		fmt.Fprintln(sys.Stdout, line)
		return nil
	}

	// The store: the one in place when it is the one asked for, else a clone. It comes before the key, so
	// that a repo that cannot be cloned or is no store leaves nothing behind.
	if _, err := sys.LookPath("git"); err != nil {
		return errors.New("git is not installed. Install it and run ezc join again.")
	}
	g, store := e.Git, p.Store()
	remote := ""
	if setup.HasStore(e) {
		if repo, _ := g.IsRepo(store); repo {
			if remote, err = g.Remote(store); err != nil {
				return err
			}
		}
		if !sameRemote(remote, source) {
			return fmt.Errorf("a store is already set up at %s (%s). Move it away to join another.", store, remoteNote(remote))
		}
		// What the remote holds is judged from this fetch: the registry, and the requests that are waiting.
		s := syncStore(sys, 0, accessFetchTimeout)
		switch {
		case s.Err != nil:
			return cannotTell(store, s.Err)
		case s.Offline:
			return errors.New("The store's remote could not be reached. Run ezc join again when it can be.")
		case s.Status.Behind > 0:
			return errors.New("The store is behind its remote. Run ezc store pull, then run ezc join again.")
		case s.Status.Ahead > 0:
			// The request's branch is cut from the store's last commit, and must carry the request alone.
			return errors.New("The store has commits its remote does not. Run ezc store push, then run ezc join again.")
		}
		fmt.Fprintf(sys.Stdout, "✓ the store: %s\n", store)
	} else {
		if err := setup.InTheWay(e); err != nil {
			return err
		}
		if _, err := sys.LookPath("gh"); github && err != nil {
			return setup.ErrNoGh
		}
		if remote, err = setup.CloneStore(e, source); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ cloned the store: %s\n", store)
	}

	public, made, err := setup.Key(e, kf)
	if err != nil {
		return err
	}
	if made {
		fmt.Fprint(sys.Stdout, created)
	} else {
		fmt.Fprintf(sys.Stdout, "✓ your key: %s\n", tilde(sys, kf))
	}

	// Config: what it says already stays; what it lacks is filled in. The user waits until the name is settled.
	filled := cfg
	for _, field := range []struct {
		have  *string
		value string
	}{
		{&filled.Backend, be.Name()}, {&filled.KeyFile, kf}, {&filled.Store.Remote, gitstore.ShowURL(remote)},
	} {
		if *field.have == "" {
			*field.have = field.value
		}
	}
	wrote := !found || filled.Backend != cfg.Backend || filled.KeyFile != cfg.KeyFile || filled.Store != cfg.Store
	if wrote {
		if err := config.Save(p.Config(), filled); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ wrote %s\n", p.Config())
	} else {
		fmt.Fprintf(sys.Stdout, "✓ the config: %s\n", p.Config())
	}
	// settle saves who this machine is in the store, once the store or a request of this key says so: a
	// name that was refused is never saved, and a user the config already names is never replaced.
	settle := func(user string) error {
		switch {
		case filled.User == user:
			return nil
		case filled.User != "":
			fmt.Fprintf(sys.Stdout, "! %s names you %s, not %s, and was left as it is.\n", p.Config(), filled.User, user)
			return nil
		}
		filled.User = user
		if err := config.Save(p.Config(), filled); err != nil {
			return err
		}
		if !wrote {
			fmt.Fprintf(sys.Stdout, "+ wrote %s\n", p.Config())
		}
		return nil
	}
	// A machine's user is its owner.
	user := name
	if o.kind == recipients.KindMachine {
		user = o.owner
	}

	reg, registered, err := recipients.Load(p.StoreEzc())
	if err != nil {
		return err
	}
	if !registered {
		return fmt.Errorf("%s has no %s, so ezc does not manage who can read it. Whoever keeps the store adds your public key by hand: %s", store, recipients.File, public)
	}
	if who := reg.NameOf(public); who != "" {
		fmt.Fprintf(sys.Stdout, "✓ %s can already read this store.\n", who)
		// The registry's word for this key, whatever name was asked for.
		if entry := reg.Recipients[who]; entry.Kind == recipients.KindMachine {
			who = entry.Owner
		}
		return settle(who)
	}
	taken := fmt.Errorf("The store already has a recipient named %s. Pick another name with --name.", name)
	if _, is := reg.Recipients[name]; is {
		return taken
	}
	if owner, is := reg.Recipients[o.owner]; o.kind == recipients.KindMachine && (!is || owner.Kind != recipients.KindUser) {
		return fmt.Errorf("The store has no user named %s, and a machine belongs to one. Say --owner <user>.", o.owner)
	}

	// The request: one that is already there for this key is left as it is, and one for another key has the name.
	branch := requestBranch + name
	rel := ezcDir + "/" + recipients.RequestPath(name)
	path := filepath.Join(store, filepath.FromSlash(rel))
	closing := fmt.Sprintf("\nAsk someone who can read the store to run: ezc user approve %s\nThen run ezc store pull.\n", name)
	mine := func(data []byte) bool {
		q, err := recipients.ParseRequest(name, data, be.ValidRecipient)
		return err == nil && q.Key == public
	}
	// Somebody merged the request's pull request: it is in the store, and approving takes it from there.
	if data, err := os.ReadFile(path); err == nil {
		if !mine(data) {
			return taken
		}
		fmt.Fprintln(sys.Stdout, "✓ your request is in the store")
		if err := settle(user); err != nil {
			return err
		}
		fmt.Fprint(sys.Stdout, closing)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: cannot read this file", path)
	}
	waiting, err := g.RemoteBranches(store, branch)
	if err != nil {
		return err
	}
	if slices.Contains(waiting, branch) {
		data, err := g.Show(store, branch, rel)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err != nil || !mine(data) {
			return taken
		}
		fmt.Fprintf(sys.Stdout, "✓ your request is waiting: %s\n", branch)
		if err := settle(user); err != nil {
			return err
		}
		fmt.Fprint(sys.Stdout, closing)
		return nil
	}

	host, _ := sys.Hostname() // a request without a host name is still one
	request := recipients.Request{Name: name, Key: public, Kind: o.kind, Owner: o.owner, Host: host, Requested: sys.Now().Format("2006-01-02")}
	dir := filepath.Dir(path)
	_, err = os.Lstat(dir)
	madeDir := errors.Is(err, fs.ErrNotExist)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: cannot create the folder", dir)
	}
	if err := os.WriteFile(path, request.Marshal(), 0o644); err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	err = g.PushBranch(store, branch, "request access for "+name, gitstore.Author{Name: name}, rel)
	// PushBranch takes the file out of the work tree again, except when it stopped at its first checks.
	os.Remove(path)
	if madeDir {
		os.Remove(dir)
	}
	switch {
	case errors.Is(err, gitstore.ErrBranchTaken):
		return taken
	case err != nil:
		return fmt.Errorf("Your request could not be pushed (%s). Run ezc join again.", gitReason(err))
	}
	fmt.Fprintf(sys.Stdout, "+ pushed your request: %s\n", branch)
	if err := settle(user); err != nil {
		return err
	}

	if gh := (ghcli.GH{Run: sys.Exec, LookPath: sys.LookPath, Environ: sys.Environ()}); gh.Installed() && (github || onGitHub(remote)) {
		title := "ezc: " + name + " asks for access"
		body := fmt.Sprintf("%s asks to read this store's secrets. Someone who can read it approves with `ezc user approve %s`, which closes this pull request. Do not merge it.", name, name)
		switch url, err := gh.PRCreate(store, branch, title, body); {
		case err != nil:
			fmt.Fprintf(sys.Stdout, "! No pull request was opened (%s). Your request is on its branch all the same.\n", strings.TrimSuffix(err.Error(), "."))
		case url == "":
			fmt.Fprintln(sys.Stdout, "+ opened a pull request")
		default:
			fmt.Fprintf(sys.Stdout, "+ opened a pull request: %s\n", recipients.Printable(gitstore.ShowURL(url)))
		}
	}
	fmt.Fprint(sys.Stdout, closing)
	return nil
}

// onGitHub reports whether remote is a repo on github.com, as a URL or in scp style (git@github.com:owner/repo).
func onGitHub(remote string) bool {
	shown := gitstore.ShowURL(remote)
	host, _, scp := strings.Cut(shown, ":")
	if _, rest, isURL := strings.Cut(shown, "://"); isURL {
		host, _, _ = strings.Cut(rest, "/")
		host, _, _ = strings.Cut(host, ":")
	} else if !scp || strings.Contains(host, "/") {
		return false
	}
	return strings.EqualFold(host[strings.LastIndex(host, "@")+1:], "github.com")
}
