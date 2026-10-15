package cli

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
	"github.com/thinkjones/easy-config/cli/internal/setup"
)

const nameRule = "lower case letters, digits and -, starting with a letter or a digit"

// setupFlags are `ezc setup`'s flags.
type setupFlags struct {
	newLocal         bool
	newGithub, store string
	name             string
	yes              bool
	ci               bool
}

// storePlan is what setup does about the store.
type storePlan int

const (
	keepStore    storePlan = iota // it is there, and stays as it is
	publishStore                  // it is there with no remote: create the GitHub repo and push
	newStore
	newPublishedStore
	cloneStore
)

func newSetupCmd(sys System) *cobra.Command {
	var o setupFlags
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "set this machine up: the tools, your key, the store and the config",
		Long: `Take this machine from nothing to a working store: check the tools, create your key, create or clone the store (a git repo of encrypted values) and write ezc's config.

Say where the store comes from with --new-local, --new-github owner/repo or --store <owner/repo | URL | path>; on a terminal, setup asks for what no flag says. Run again, it changes only what is missing: a key, a store or a config that is there is never overwritten.

In a CI job, --ci --store owner/repo sets up a machine that only reads the store: the bot's key is in ` + botSecret + ` and a token that can read the store's repo is in ` + storeTokenSecret + ` (or GH_TOKEN). It clones the latest commit with the token, checks that the key reads the store and writes the config. It makes no key file, asks nothing, and git is told the name of the token's variable, never the token. The store's repo must be on github.com: the token is offered to no other host, so a GitHub Enterprise host is not supported.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error { return runSetup(sys, o) },
	}
	cmd.Flags().BoolVar(&o.newLocal, "new-local", false, "create a new store on this machine")
	cmd.Flags().StringVar(&o.newGithub, "new-github", "", "create a new store and a private GitHub repo `owner/repo` for it")
	cmd.Flags().StringVar(&o.store, "store", "", "clone an existing store from `repo`: owner/repo, a URL or a path")
	cmd.Flags().StringVar(&o.name, "name", "", "your name in the store ("+nameRule+")")
	cmd.Flags().BoolVar(&o.yes, "yes", false, "answer yes to the questions setup asks")
	cmd.Flags().BoolVar(&o.ci, "ci", false, "set up a CI job, with --store: the key and the token come from the environment")
	return cmd
}

// asker asks one line at a time: the prompt on stderr, the answer from stdin.
type asker struct {
	sys System
	in  *bufio.Reader
}

// ask returns the trimmed answer; "" when the input has ended.
func (a *asker) ask(prompt string) string {
	fmt.Fprint(a.sys.Stderr, prompt)
	line, _ := a.in.ReadString('\n')
	return strings.TrimSpace(line)
}

func runSetup(sys System, o setupFlags) error {
	// Setup manages the default store, so EZC_STORE is left out of the paths it works with.
	p, err := paths.Resolve(func(key string) string {
		if key == "EZC_STORE" {
			return ""
		}
		return sys.Getenv(key)
	}, sys.GOOS)
	if err != nil {
		return err
	}
	if sys.Getenv("EZC_STORE") != "" {
		return usagef("ezc setup: unset EZC_STORE. Setup manages the store in %s.", p.Store())
	}
	if o.ci {
		return runSetupCI(sys, p, o)
	}
	chosen := 0
	for _, set := range []bool{o.newLocal, o.newGithub != "", o.store != ""} {
		if set {
			chosen++
		}
	}
	if chosen > 1 {
		return usagef("ezc setup: give only one of --new-local, --new-github and --store.")
	}
	if o.newGithub != "" && !setup.IsSlug(o.newGithub) {
		return usagef("ezc setup: --new-github takes owner/repo.")
	}
	if o.name != "" && !recipients.ValidName(o.name) {
		return usagef("ezc setup: --name takes %s.", nameRule)
	}
	// A key the environment supplies is the one every command reads with, so it is the one to register.
	envVar, envKeyFile := keyInEnvironment(sys)
	switch {
	case envVar != "" && envKeyFile == "":
		return usagef("ezc setup: %s is set, so that key would be used instead of the one setup registers. Unset it and run ezc setup again.", envVar)
	case envVar != "" && !filepath.IsAbs(envKeyFile):
		return usagef("ezc setup: %s must be an absolute path.", envVar)
	}
	cfg, found, err := config.Load(p.Config())
	if err != nil {
		return err
	}
	be := backendFor(sys)
	e := setup.Env{Git: storeGit(sys), Backend: be, Run: sys.Exec, LookPath: sys.LookPath, Now: sys.Now, Paths: p, GOOS: sys.GOOS}
	tty := sys.StdinIsTerminal()
	has := setup.HasStore(e)
	if !has && chosen == 0 && !tty {
		return usagef("ezc setup: say --new-local, --new-github owner/repo or --store <repo>.")
	}
	a := &asker{sys: sys, in: bufio.NewReader(sys.Stdin)}

	// Tools.
	if _, err := sys.LookPath("git"); err != nil {
		return errors.New("git is not installed. Install it and run ezc setup again.")
	}
	fmt.Fprintln(sys.Stdout, "✓ git")
	toolMark := "✓"
	if err := be.Check(); err != nil {
		if !errors.Is(err, backend.ErrNoTool) {
			return err
		}
		if _, noBrew := sys.LookPath("brew"); noBrew == nil && be.BrewFormula() != "" && (o.yes || tty && yes(a.ask("Install it now with brew? [y/N] "))) {
			if ierr := setup.InstallTool(e); ierr != nil {
				return ierr
			}
			err = be.Check()
		}
		if errors.Is(err, backend.ErrNoTool) {
			return toolMissing(sys, be)
		}
		if err != nil {
			return err
		}
		toolMark = "+"
	}
	fmt.Fprintln(sys.Stdout, toolMark, be.Name())

	// Name.
	name := o.name
	if name == "" {
		name = cfg.User
	}
	if name == "" {
		name = strings.ToLower(sys.Getenv("USER"))
		if tty {
			if answer := a.ask(fmt.Sprintf("Your name in this store [%s]: ", name)); answer != "" {
				name = answer
			}
		}
	}
	if !recipients.ValidName(name) {
		return usagef("ezc setup: say --name <name>: your name in the store, %s.", nameRule)
	}

	// What to do about the store, decided before anything is made.
	source, slug := o.store, o.newGithub
	plan := keepStore
	remote := ""
	if has {
		if repo, _ := e.Git.IsRepo(p.Store()); repo {
			if remote, err = e.Git.Remote(p.Store()); err != nil {
				return err
			}
		}
		if err := setup.Adoptable(e, resolve.IsEncrypted); err != nil {
			return adoptError(p.Store(), err)
		}
		want := source + slug
		switch {
		case slug != "" && remote == "":
			plan = publishStore
		case want != "" && !sameRemote(remote, localSource(sys, want)):
			return fmt.Errorf("a store is already set up at %s (%s). Move it away to set up another.", p.Store(), remoteNote(remote))
		}
	} else {
		if chosen == 0 {
			switch a.ask("Store: [1] new, on this machine  [2] new, on GitHub  [3] an existing one: ") {
			case "1":
				o.newLocal = true
			case "2":
				if slug = a.ask("Its GitHub repo, as owner/repo: "); !setup.IsSlug(slug) {
					return errors.New("that is not owner/repo. Run ezc setup again.")
				}
			case "3":
				if source = a.ask("The store's repo (owner/repo, a URL or a path): "); source == "" {
					return errors.New("no repo given. Run ezc setup again.")
				}
			default:
				return errors.New("answer 1, 2 or 3. Run ezc setup again.")
			}
		}
		switch {
		case slug != "":
			plan = newPublishedStore
		case source != "":
			plan = cloneStore
			source = localSource(sys, source)
			if err := setup.InTheWay(e); err != nil {
				return err
			}
		default:
			plan = newStore
		}
	}
	if plan == publishStore || plan == newPublishedStore || plan == cloneStore && setup.IsSlug(source) {
		if _, err := sys.LookPath("gh"); err != nil {
			return setup.ErrNoGh
		}
	}

	// Key.
	kf := envKeyFile
	if kf == "" {
		kf = keyFile(sys)
	}
	if kf == "" {
		return errors.New("can't find your home directory, where your key is kept: set HOME.")
	}
	public, created, err := setup.Key(e, kf)
	if err != nil {
		return err
	}
	made := created || toolMark == "+"
	if created {
		fmt.Fprintf(sys.Stdout, "+ created your key: %s\n  Back this file up now. Without it your secrets cannot be read, and it cannot be recovered.\n", tilde(sys, kf))
		if tty && !o.yes {
			a.ask("Press Enter when you have backed it up. ")
		}
	} else {
		fmt.Fprintf(sys.Stdout, "✓ your key: %s\n", tilde(sys, kf))
	}

	// Store. One that was not made here gets what it lacks, before it is published.
	var adopted setup.Adopted
	switch plan {
	case keepStore, publishStore:
		if adopted, err = setup.Adopt(e, name, public, resolve.IsEncrypted); err != nil {
			return adoptError(p.Store(), err)
		}
		if adopted.Repo {
			fmt.Fprintf(sys.Stdout, "+ made the store a git repo: %s\n", p.Store())
		} else {
			fmt.Fprintf(sys.Stdout, "✓ the store: %s\n", p.Store())
		}
	case newStore, newPublishedStore:
		if err := setup.NewStore(e, name, public); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ created the store: %s\n", p.Store())
	case cloneStore:
		if remote, err = setup.CloneStore(e, source); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ cloned the store: %s\n", p.Store())
		if adopted, err = setup.Adopt(e, name, public, resolve.IsEncrypted); err != nil {
			return adoptError(p.Store(), err)
		}
	}
	if adopted.Manifest {
		fmt.Fprintln(sys.Stdout, "+ wrote an empty manifest")
	}
	switch {
	case adopted.Registry:
		fmt.Fprintln(sys.Stdout, "+ wrote the registry and the encryption policy")
	case adopted.Policy:
		fmt.Fprintln(sys.Stdout, "+ wrote the encryption policy")
	}
	if plan == publishStore || plan == newPublishedStore {
		if remote, err = setup.Publish(e, slug); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ published the store: %s\n", slug)
	}
	made = made || plan != keepStore || adopted.Repo || adopted.Manifest || adopted.Registry || adopted.Policy

	// Config: what it says already stays; what it lacks is filled in.
	filled := cfg
	for _, field := range []struct {
		have  *string
		value string
	}{
		{&filled.User, name}, {&filled.Backend, be.Name()}, {&filled.KeyFile, kf}, {&filled.Store.Remote, gitstore.ShowURL(remote)},
	} {
		if *field.have == "" {
			*field.have = field.value
		}
	}
	if found && filled.User == cfg.User && filled.Backend == cfg.Backend && filled.KeyFile == cfg.KeyFile && filled.Store == cfg.Store {
		fmt.Fprintf(sys.Stdout, "✓ the config: %s\n", p.Config())
	} else {
		if err := config.Save(p.Config(), filled); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ wrote %s\n", p.Config())
		made = true
	}

	who, err := setup.Recipient(e, public)
	if err != nil {
		return err
	}
	// A store with no registry says nothing about who can read it; one with no policy cannot take a secret.
	_, registered, _ := recipients.Load(p.StoreEzc())
	hasPolicy, _ := be.HasPolicy(p.StoreEzc())
	switch {
	case who == "" && registered:
		fmt.Fprintf(sys.Stdout, "\n! Your key cannot read this store's secrets yet. Give this public key to someone who has access:\n%s\n", public)
	case hasPolicy && !registered:
		fmt.Fprint(sys.Stdout, "\n! This store's encryption policy was written by hand and it has no recipients.yaml, so ezc does not manage who can read it. Keep editing the policy by hand for now.\n")
	case adopted.Unmanaged:
		fmt.Fprint(sys.Stdout, "\n! This store holds encrypted values but has no recipients.yaml and no encryption policy in .ezc, so ezc does not manage who can read it.\n")
	case made && hasPolicy:
		fmt.Fprint(sys.Stdout, "\nNext: ezc set -g NAME --type secret --desc \"what it is\"\n")
	}
	return nil
}

// toolMissing says that the backend's tool is not installed and how to get it, and ends setup.
// One thing to do, said once: the backend's own text also sends the user elsewhere.
func toolMissing(sys System, be backend.Backend) error {
	fmt.Fprintf(sys.Stderr, "✗ %s is not installed.\n", be.Name())
	if hint := be.ToolHint(sys.GOOS); hint != "" {
		fmt.Fprintln(sys.Stderr, hint)
	}
	fmt.Fprintln(sys.Stderr, "Then run ezc setup again.")
	return &ExitError{1}
}

// runSetupCI sets up a CI job's machine, which only reads the store: a shallow clone made with the token the
// environment holds, and the config. The key stays in the environment, so no key file is made, nothing is
// registered and nothing is asked. The key is checked twice: that it is one, before anything is made, and
// that it opens the store, before the config is written. A store that is in place and came from the same repo is kept.
func runSetupCI(sys System, p paths.Paths, o setupFlags) error {
	if o.newLocal || o.newGithub != "" || o.name != "" {
		return usagef("ezc setup: --ci cannot be used with --new-local, --new-github or --name.")
	}
	if o.store == "" {
		return usagef("ezc setup --ci: say --store owner/repo.")
	}
	// What was given is not repeated: a URL can hold a password.
	if !setup.IsSlug(o.store) && !filepath.IsAbs(o.store) {
		return usagef("ezc setup --ci: --store takes owner/repo, or the absolute path of a local repo.")
	}
	if sys.Getenv(botSecret) == "" {
		return fmt.Errorf("ezc setup --ci: %s is not set. Save the bot's key as that secret; ezc bot add does it.", botSecret)
	}
	if tokenVar(sys) == "" {
		return fmt.Errorf("ezc setup --ci: set %s (or GH_TOKEN) to a token that can read %s.", storeTokenSecret, o.store)
	}
	be := backendFor(sys)
	key := []byte(sys.Getenv(botSecret))
	if be.ValidKey(key) != nil {
		return fmt.Errorf("ezc setup --ci: %s does not hold a key. Save the bot's key as that secret; ezc bot add does it.", botSecret)
	}
	cfg, found, err := config.Load(p.Config())
	if err != nil {
		return err
	}
	e := setup.Env{Git: storeGit(sys), Backend: be, Run: sys.Exec, LookPath: sys.LookPath, Now: sys.Now, Paths: p, GOOS: sys.GOOS}

	if _, err := sys.LookPath("git"); err != nil {
		return errors.New("git is not installed. Install it and run ezc setup again.")
	}
	fmt.Fprintln(sys.Stdout, "✓ git")
	if err := be.Check(); err != nil {
		if errors.Is(err, backend.ErrNoTool) {
			return toolMissing(sys, be)
		}
		return err
	}
	fmt.Fprintln(sys.Stdout, "✓", be.Name())

	remote := ""
	if setup.HasStore(e) {
		if repo, _ := e.Git.IsRepo(p.Store()); repo {
			if remote, err = e.Git.Remote(p.Store()); err != nil {
				return err
			}
		}
		if !sameRemote(remote, o.store) {
			return fmt.Errorf("a store is already set up at %s (%s). Move it away to set up another.", p.Store(), remoteNote(remote))
		}
		fmt.Fprintf(sys.Stdout, "✓ the store: %s\n", p.Store())
	} else {
		if remote, err = setup.CloneShallowStore(e, o.store); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ cloned the store: %s\n", p.Store())
	}

	// The key is said to be there once it is known to read the store: it must open an encrypted file, when the store has one.
	encrypted, err := encryptedFiles(p.StoreEzc())
	if err != nil {
		return err
	}
	if len(encrypted) > 0 {
		if err := be.CanDecrypt(filepath.Join(p.StoreEzc(), encrypted[0]), key); errors.Is(err, backend.ErrNoKey) {
			fmt.Fprintf(sys.Stderr, "✗ The key in %s cannot read this store.\n", botSecret)
			return &ExitError{1}
		} else if err != nil {
			return err
		}
	}
	fmt.Fprintf(sys.Stdout, "✓ the key: %s\n", botSecret)

	// Config: no user and no key file, because this machine has neither.
	filled := cfg
	if filled.Backend == "" {
		filled.Backend = be.Name()
	}
	if filled.Store.Remote == "" {
		filled.Store.Remote = gitstore.ShowURL(remote)
	}
	if found && filled.Backend == cfg.Backend && filled.Store == cfg.Store {
		fmt.Fprintf(sys.Stdout, "✓ the config: %s\n", p.Config())
		return nil
	}
	if err := config.Save(p.Config(), filled); err != nil {
		return err
	}
	fmt.Fprintf(sys.Stdout, "+ wrote %s\n", p.Config())
	return nil
}

// adoptError is err from setup.Adopt as setup says it: a store whose state cannot be read gets the write guard's sentence.
func adoptError(dir string, err error) error {
	var state *setup.StateError
	if errors.As(err, &state) {
		return cannotTell(dir, state.Err)
	}
	return err
}

// yes reports whether answer is a yes.
func yes(answer string) bool {
	answer = strings.ToLower(answer)
	return answer == "y" || answer == "yes"
}

// tilde is path with the home folder written as ~, as the key file is shown.
func tilde(sys System, path string) string {
	if home := homeDir(sys); home != "" {
		if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return "~" + string(filepath.Separator) + rest
		}
	}
	return path
}

// localSource is source as git is given it: a relative path is made absolute, owner/repo and URLs are left alone.
func localSource(sys System, source string) string {
	if setup.IsSlug(source) || filepath.IsAbs(source) || strings.Contains(source, ":") {
		return source
	}
	if abs, err := absPath(sys, source); err == nil {
		return filepath.Clean(abs)
	}
	return source
}

// sameRemote reports whether a store whose origin is remote is the store source names:
// the same URL or path, with or without .git, or, for an owner/repo, that repo on github.com and nowhere else.
func sameRemote(remote, source string) bool {
	if remote == "" {
		return false
	}
	bare := func(s string) string { return strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git") }
	r, s := bare(remote), bare(source)
	if r == s {
		return true
	}
	if !setup.IsSlug(source) {
		return false
	}
	slug, ok := githubSlug(remote)
	return ok && slug == s
}

// remoteNote is "remote <url>" for a message, the URL without what it may carry besides the host and the path.
func remoteNote(remote string) string {
	if remote == "" {
		return "no remote"
	}
	return "remote " + gitstore.ShowURL(remote)
}
