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
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/setup"
)

const nameRule = "lower case letters, digits and -, starting with a letter or a digit"

// setupFlags are `ezc setup`'s flags.
type setupFlags struct {
	newLocal         bool
	newGithub, store string
	name             string
	yes              bool
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

Say where the store comes from with --new-local, --new-github owner/repo or --store <owner/repo | URL | path>; on a terminal, setup asks for what no flag says. Run again, it changes only what is missing: a key, a store or a config that is there is never overwritten.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error { return runSetup(sys, o) },
	}
	cmd.Flags().BoolVar(&o.newLocal, "new-local", false, "create a new store on this machine")
	cmd.Flags().StringVar(&o.newGithub, "new-github", "", "create a new store and a private GitHub repo `owner/repo` for it")
	cmd.Flags().StringVar(&o.store, "store", "", "clone an existing store from `repo`: owner/repo, a URL or a path")
	cmd.Flags().StringVar(&o.name, "name", "", "your name in the store ("+nameRule+")")
	cmd.Flags().BoolVar(&o.yes, "yes", false, "answer yes to the questions setup asks")
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
		if err != nil {
			fmt.Fprintln(sys.Stderr, err)
			if hint := be.ToolHint(sys.GOOS); hint != "" {
				fmt.Fprintln(sys.Stderr, hint)
			}
			return &ExitError{1}
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

	// Store.
	switch plan {
	case keepStore, publishStore:
		fmt.Fprintf(sys.Stdout, "✓ the store: %s\n", p.Store())
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
	}
	if plan == publishStore || plan == newPublishedStore {
		if remote, err = setup.Publish(e, slug); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "+ published the store: %s\n", slug)
	}
	made = made || plan != keepStore

	// Config: what it says already stays; what it lacks is filled in.
	filled := cfg
	for _, field := range []struct {
		have  *string
		value string
	}{
		{&filled.User, name}, {&filled.Backend, be.Name()}, {&filled.KeyFile, kf}, {&filled.Store.Remote, remote},
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
	// A store with no registry says nothing about who can read it.
	_, registered, _ := recipients.Load(p.StoreEzc())
	switch {
	case err != nil:
		return err
	case who == "" && registered:
		fmt.Fprintf(sys.Stdout, "\n! Your key cannot read this store's secrets yet. Give this public key to someone who has access:\n%s\n", public)
	case made:
		fmt.Fprint(sys.Stdout, "\nNext: ezc set -g NAME --type secret --desc \"what it is\"\n")
	}
	return nil
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
// the same URL or path, with or without .git, or a URL that ends in the owner/repo.
func sameRemote(remote, source string) bool {
	if remote == "" {
		return false
	}
	bare := func(s string) string { return strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git") }
	r, s := bare(remote), bare(source)
	if r == s {
		return true
	}
	return setup.IsSlug(source) && (strings.HasSuffix(r, "/"+s) || strings.HasSuffix(r, ":"+s))
}

// remoteNote is "remote <url>" for a message, the URL without the name and password it may carry.
func remoteNote(remote string) string {
	if remote == "" {
		return "no remote"
	}
	return "remote " + setup.Redact(remote)
}
