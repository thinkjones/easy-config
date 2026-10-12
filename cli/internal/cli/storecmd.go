package cli

import (
	"bufio"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
	"github.com/thinkjones/easy-config/cli/internal/storeaudit"
	"github.com/thinkjones/easy-config/cli/internal/valuefile"
)

const (
	// storeFetchTimeout is how long `ezc store status` and `push` wait for the remote before they say what they know.
	storeFetchTimeout = 10 * time.Second
	// storePullTimeout is how long `ezc store pull` waits for the remote's commits.
	storePullTimeout = time.Minute

	originUnreachable = "origin could not be reached."
)

func newStoreCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "store",
		Short: "work on the global store as a whole",
	}, "status, pull, push, rewrap or audit")
	cmd.AddCommand(newStoreStatusCmd(sys), newStorePullCmd(sys), newStorePushCmd(sys), newStoreRewrapCmd(sys), newStoreAuditCmd(sys))
	return cmd
}

// storeFolder is where ezc keeps things, or an error when there is no store there.
func storeFolder(sys System) (paths.Paths, error) {
	p, err := storePaths(sys)
	if err != nil {
		return p, err
	}
	if info, err := os.Stat(p.Store()); err != nil || !info.IsDir() {
		return p, fmt.Errorf("no store at %s. `ezc setup` creates one.", p.Store())
	}
	return p, nil
}

// storeRepo is storeFolder for a command that needs the store to be a git repo; verb is what the command does.
func storeRepo(sys System, verb string) (paths.Paths, gitstore.Git, error) {
	g := storeGit(sys)
	p, err := storeFolder(sys)
	if err != nil {
		return p, g, err
	}
	repo, err := g.IsRepo(p.Store())
	switch {
	case err != nil:
		return p, g, storeUnread(p.Store(), err)
	case !repo:
		return p, g, fmt.Errorf("the store in %s is not a git repo, so there is nothing to %s.", p.Store(), verb)
	}
	return p, g, nil
}

// storeUnread is cannotTell for a command that only looks at the store or moves its commits: without git it has no change to lose.
func storeUnread(dir string, err error) error {
	if errors.Is(err, gitstore.ErrNotInstalled) {
		return errors.New("git is not installed. Install git, then run this again.")
	}
	return cannotTell(dir, err)
}

// storeContacted records that origin was reached just now.
func storeContacted(sys System, p paths.Paths) {
	state, now := readState(p.State()), sys.Now()
	setStateTime(state, "attempted", now)
	setStateTime(state, "fetched", now)
	writeState(p.State(), state)
}

func storeDirty(dir, then string) error {
	return fmt.Errorf("the store has uncommitted changes in %s. Commit or discard them with git, then %s.", dir, then)
}

func newStoreStatusCmd(sys System) *cobra.Command {
	var due bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "say how the store stands against its remote",
		Long: `Fetch from the store's remote, then print where the store is, its remote, whether it is in sync, ahead, behind, diverged or holds uncommitted changes, when the remote was last reached, and the names of pending access requests: those still on their branch of the remote and those in the store. ` + "`ezc user requests`" + ` shows them.

Exits 0 in every state it can read. A store that is not a git repo is not synced, and is said to be.

With --due, print instead the secrets that need a new value because somebody who could read them was removed, one per line, and nothing else. Names and dates only.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := storeFolder(sys)
			if err != nil {
				return err
			}
			if due {
				d, err := recipients.LoadDue(p.StoreEzc())
				if err != nil {
					return err
				}
				for _, k := range slices.Sorted(maps.Keys(d)) {
					fmt.Fprintf(sys.Stdout, "%s  readable by removed %s since %s\n", recipients.Printable(k), recipients.Printable(d[k].Removed), recipients.Printable(d[k].On))
				}
				return nil
			}
			owing := func() {
				switch d, err := recipients.LoadDue(p.StoreEzc()); {
				case err != nil:
					fmt.Fprintf(sys.Stdout, "rotation due: cannot read %s\n", recipients.DueFile)
				case len(d) > 0:
					fmt.Fprintf(sys.Stdout, "rotation due: %s (ezc store status --due lists them)\n", count(len(d), "secret"))
				}
			}
			row := func(label, value string) { fmt.Fprintf(sys.Stdout, "%-10s%s\n", label, value) }
			s := syncStore(sys, 0, storeFetchTimeout)
			if !s.Git {
				row("store", p.Store())
				row("state", "not a git repo, so it is not synced")
				owing()
				return nil
			}
			if s.Err != nil {
				return storeUnread(s.Dir, s.Err)
			}

			st := s.Status
			state := "in sync"
			switch st.State() {
			case gitstore.Ahead:
				state = count(st.Ahead, "commit") + " ahead of origin"
			case gitstore.Behind:
				state = count(st.Behind, "commit") + " behind origin"
			case gitstore.Diverged:
				state = fmt.Sprintf("diverged from origin (%d ahead, %d behind)", st.Ahead, st.Behind)
			case gitstore.Uncommitted:
				state = "uncommitted changes" + alsoStands(st)
			}
			fetched := "n/a"
			if st.Remote != "" {
				fetched = "never"
				if !s.Fetched.IsZero() {
					fetched = ago(sys.Now().Sub(s.Fetched))
				}
				if s.Offline {
					fetched += "; origin could not be reached"
				}
			}
			requests := "none"
			if len(s.Requests) > 0 {
				requests = someNames(s.Requests)
			}
			remote := "none (local only)"
			if st.Remote != "" {
				remote = gitstore.ShowURL(st.Remote)
			}
			row("store", s.Dir)
			row("remote", remote)
			row("state", state)
			row("fetched", fetched)
			row("requests", requests)
			owing()
			return nil
		},
	}
	cmd.Flags().BoolVar(&due, "due", false, "list the secrets that need a new value after somebody was removed")
	return cmd
}

func newStorePullCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "bring the remote's commits into the store",
		Long: `Fetch from the store's remote and bring its commits in; commits made here are replayed on top of them.

An encrypted file changed both here and on the remote cannot be merged: the remote's version is kept, the names to set again are listed on stderr and the command exits 1. A plaintext file changed in the same place on both sides stops the pull with the store as it was; when it is recipients.yaml or the encryption policy, the message says how to drop the commits made here and make the change again.

When the commits it brought changed the names or keys of recipients.yaml, a ! line on stderr says who was added, removed or given a new key. Afterwards every encrypted file that is not encrypted for the people recipients.yaml names is named on stderr, and the command exits 1: run ` + "`ezc store rewrap`" + ` before anything is set in it. Prints names and files, never a value.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, g, err := storeRepo(sys, "pull")
			if err != nil {
				return err
			}
			dir := p.Store()
			// Who the registry names before the pull; one that is not there or not valid names nobody.
			was, _, _ := recipients.Load(p.StoreEzc())
			res, err := g.Pull(dir, storePullTimeout, resolve.IsEncrypted)
			// Only the conflict itself: when more went wrong, the error says what.
			if conflict, ok := err.(*gitstore.ConflictError); ok {
				branch := "<branch>"
				if st, err := g.Status(dir); err == nil {
					branch = st.Branch
				}
				return conflictRefusal(dir, branch, conflict.Paths, policyFiles(sys))
			}
			switch {
			case errors.Is(err, gitstore.ErrDirty):
				return storeDirty(dir, "pull")
			case errors.Is(err, gitstore.ErrNoRemote):
				return errors.New("the store has no remote, so there is nothing to pull. `ezc setup --new-github owner/repo` gives it one.")
			case errors.Is(err, gitstore.ErrUnreachable):
				return errors.New(originUnreachable)
			case errors.Is(err, gitstore.ErrNoBranch):
				return storeUnread(dir, err)
			case err != nil:
				return err
			}
			storeContacted(sys, p)
			if res.Commits == 0 {
				fmt.Fprintln(sys.Stdout, "already up to date")
			} else {
				fmt.Fprintf(sys.Stdout, "pulled %s from origin\n", count(res.Commits, "commit"))
			}
			// What the store holds now is judged whatever the pull brought: a file that is encrypted for
			// somebody the registry does not name must not be written to, and the write would say no more.
			if now, found, err := recipients.Load(p.StoreEzc()); err == nil && res.Commits > 0 {
				// The names passed the registry's rule when it was loaded.
				switch change := registryChange(was, now); {
				case !found && len(was.Recipients) > 0:
					fmt.Fprintf(sys.Stderr, "! %s was deleted on the remote, so ezc no longer knows who can read the store.\n", recipients.File)
				case change != "":
					fmt.Fprintf(sys.Stderr, "! %s changed on the remote: %s. ezc user list shows who can read the store.\n", recipients.File, change)
				}
			}
			differs, derr := storeDiffers(sys, p.StoreEzc())
			var said []string
			for _, r := range res.Replaced {
				file := strings.TrimPrefix(r.Path, ezcDir+"/")
				if slices.Contains(differs, file) && rewrapped(sys, backendFor(sys), r.Base, r.Local) {
					// The commit made here changed who the file is for, and that is what was lost.
					fmt.Fprintf(sys.Stderr, "! %s was re-wrapped here and changed on the remote. The remote's version was kept, and it is not encrypted for the people %s names. Run ezc store rewrap, then set again any value you changed in it since your last push.\n", file, recipients.File)
					said = append(said, file)
					continue
				}
				fmt.Fprint(sys.Stderr, replacedNote(r))
			}
			if derr != nil {
				fmt.Fprintf(sys.Stderr, "! Cannot tell whether the store's files are encrypted for the registry: %s\n", firstLineOf(derr))
			}
			for _, file := range differs {
				if !slices.Contains(said, file) {
					fmt.Fprintf(sys.Stderr, "! %s is not encrypted for the registry. Run ezc store rewrap before you set anything.\n", file)
				}
			}
			if len(res.Replaced) > 0 || len(differs) > 0 || derr != nil {
				return &ExitError{1}
			}
			return nil
		},
	}
}

// registryChange is how the names and keys of two registries differ: "+ a, b" for the names now has and was lacks,
// "- c" for the ones it lost and "~ d (new key)" for a name whose key changed; "" when they agree.
func registryChange(was, now recipients.Registry) string {
	var added, removed, rekeyed []string
	for _, name := range slices.Sorted(maps.Keys(now.Recipients)) {
		switch old, had := was.Recipients[name]; {
		case !had:
			added = append(added, name)
		case old.Key != now.Recipients[name].Key:
			rekeyed = append(rekeyed, name+" (new key)")
		}
	}
	for _, name := range slices.Sorted(maps.Keys(was.Recipients)) {
		if _, has := now.Recipients[name]; !has {
			removed = append(removed, name)
		}
	}
	var parts []string
	for _, part := range []struct {
		sign  string
		names []string
	}{{"+", added}, {"-", removed}, {"~", rekeyed}} {
		if len(part.names) > 0 {
			parts = append(parts, part.sign+" "+strings.Join(part.names, ", "))
		}
	}
	return strings.Join(parts, " ")
}

// conflictRefusal says which files stopped a pull, and where to resolve them. The registry and the policy are
// written by ezc and cannot be merged: when one of them is among the files, the way out is to drop the commits made
// here on branch and make the change again.
func conflictRefusal(dir, branch string, paths, policy []string) error {
	shown := make([]string, len(paths))
	access := false
	for i, path := range paths {
		shown[i] = quoted(path)
		file := strings.TrimPrefix(path, ezcDir+"/")
		access = access || file == recipients.File || slices.Contains(policy, file)
	}
	was, it := "was", "it"
	if len(paths) > 1 {
		was, it = "were", "them"
	}
	if access {
		origin := "refs/remotes/origin/" + branch
		return fmt.Errorf("%s %s changed here and on the remote, so nothing was pulled. Who can read the store was changed on two machines at once, and the files ezc writes for that cannot be merged. In %s, `git log %s..HEAD` lists the commits made here that the remote lacks; drop them with `git reset --hard %s`, then make your change again.", strings.Join(shown, ", "), was, dir, origin, origin)
	}
	return fmt.Errorf("%s %s changed here and on the remote in the same place, so nothing was pulled. Resolve %s in %s with git (git pull --rebase).", strings.Join(shown, ", "), was, it, dir)
}

// quoted is a file's name for a message: quoted when it would not read as one name on one line.
func quoted(name string) string {
	if q := strconv.Quote(name); q != `"`+name+`"` || strings.Contains(name, ", ") {
		return q
	}
	return name
}

// envFlag is what names an encrypted file of the store to `ezc set -g`: nothing for common.enc.yaml, " --env <name>"
// for env.<name>.enc.yaml. A file that is not one of the store's own has none, and ok is false.
func envFlag(file string) (flag string, ok bool) {
	if name, isEnv := strings.CutPrefix(strings.TrimSuffix(file, ".enc.yaml"), "env."); isEnv && resolve.ValidEnvName(name) {
		return " --env " + name, true
	}
	return "", file == "common.enc.yaml"
}

// replacedNote says that a file took the remote's version, and the commands that make its local changes again.
// It reads the names of an encrypted file, which are not encrypted, and never a value.
func replacedNote(r gitstore.Replaced) string {
	file, inEzc := strings.CutPrefix(r.Path, ezcDir+"/")
	note := fmt.Sprintf("! %s was changed here and on the remote. The remote's version was kept; ", quoted(file))
	const unlisted = "its local changes must be set again.\n"

	env, ok := envFlag(file)
	if !ok || !inEzc {
		return note + unlisted
	}
	changed, err := valuefile.ChangedNames(r.Base, r.Local)
	if err != nil || len(changed) == 0 {
		return note + unlisted
	}
	held, err := valuefile.ChangedNames(nil, r.Local)
	if err != nil {
		return note + unlisted
	}
	var set, unset strings.Builder
	for _, name := range changed {
		switch {
		case !manifest.ValidName(name):
			return note + unlisted
		case slices.Contains(held, name):
			fmt.Fprintf(&set, "  ezc set -g %s%s\n", name, env)
		default:
			// Removed here: the remote's version has it back.
			fmt.Fprintf(&unset, "  ezc unset -g %s%s\n", name, env)
		}
	}
	if unset.Len() == 0 {
		return note + "set these again:\n" + set.String()
	}
	return note + "run these again:\n" + set.String() + unset.String()
}

func newStorePushCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "send the store's commits to its remote",
		Long: `Fetch from the store's remote, then send it the commits made here.

Refused while the store is behind its remote or has diverged from it (run ` + "`ezc store pull`" + `, then this again), while it holds uncommitted changes, and when it has no remote. Also refused while an encrypted file is not encrypted for the people recipients.yaml names: ` + "`ezc store rewrap`" + ` fixes that.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, g, err := storeRepo(sys, "push")
			if err != nil {
				return err
			}
			dir := p.Store()
			st, err := g.Status(dir)
			switch {
			case errors.Is(err, gitstore.ErrNoBranch):
				return storeUnread(dir, err)
			case err != nil:
				return err
			case st.Remote == "":
				return errors.New("the store has no remote, so there is nothing to push to. `ezc setup --new-github owner/repo` gives it one.")
			case st.Dirty:
				return storeDirty(dir, "push")
			}
			if err := g.Fetch(dir, storeFetchTimeout); errors.Is(err, gitstore.ErrUnreachable) {
				return errors.New(originUnreachable)
			} else if err != nil {
				return err
			}
			storeContacted(sys, p)
			if st, err = g.Status(dir); err != nil {
				return err
			}
			if err := behind(st); err != nil {
				return err
			}
			if st.Ahead > 0 {
				// A commit made here may hold a file written under a policy the remote has since replaced.
				if differs, err := storeDiffers(sys, p.StoreEzc()); err != nil {
					return err
				} else if len(differs) > 0 {
					return fmt.Errorf("%s, so nothing was pushed. Run ezc store rewrap.", notForRegistry(differs...))
				}
			}
			pushed, err := g.Push(dir)
			if err != nil {
				return fmt.Errorf("the push to origin failed: %s.", strings.TrimSuffix(strings.TrimPrefix(err.Error(), "git: "), "."))
			}
			if pushed == 0 {
				fmt.Fprintln(sys.Stdout, "nothing to push")
				return nil
			}
			fmt.Fprintf(sys.Stdout, "pushed %s to origin\n", count(pushed, "commit"))
			return nil
		},
	}
}

func newStoreRewrapCmd(sys System) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rewrap [--yes]",
		Short: "encrypt every file of the store for the people recipients.yaml names",
		Long: `Write the store's encryption policy from recipients.yaml and re-wrap every encrypted file for exactly the keys it names, in one commit, then push. It is the fix when ` + "`ezc user list`" + ` says a file's recipients differ from the registry.

Read ` + "`ezc user list`" + ` first. recipients.yaml is a file in a repo, and whoever can push to the store can add a name to it: a re-wrap lets every name it holds read every secret. Each name that would gain access to a file, and each key that would lose it, is printed before anything is changed. When a name would gain access the command asks ` + "`Let <names> read the store? [y/N]`" + ` on a terminal, and needs --yes without one. The secrets in a file that a key recipients.yaml does not name could read are recorded in rotation-due.yaml.

Needs this machine's key file, and the key must be one recipients.yaml names. Refused when the store's remote cannot be reached, while the store is behind it, and while it holds uncommitted changes to the files it rewrites. A failure puts the store back as it was. No value is read or printed.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := openAccess(sys, true)
			if err != nil {
				return err
			}
			// The registry is taken as it is, and it is a file anybody who can push can write to.
			confirm := func(names []string) error {
				who := someNames(names)
				switch {
				case yes:
					return nil
				case !sys.StdinIsTerminal():
					return usagef("%s: this would let %s read the store. Say --yes to do that without being asked: there is no terminal to ask on. Try `%s`.", cmd.CommandPath(), who, helpFor(cmd))
				}
				switch strings.ToLower((&asker{sys: sys, in: bufio.NewReader(sys.Stdin)}).ask("Let " + who + " read the store? [y/N] ")) {
				case "y", "yes":
					return nil
				}
				fmt.Fprintln(sys.Stdout, "Not re-wrapped.")
				return &ExitError{1}
			}
			changed, err := a.run(change{next: a.reg, message: "rewrap", confirm: confirm})
			if err == nil && !changed {
				fmt.Fprintln(sys.Stdout, "✓ every file is encrypted for the registry")
			}
			if errors.Is(err, backend.ErrNoKey) {
				// The usual cause is a rotation of this machine's key that the file never saw.
				return fmt.Errorf("%w A key file this machine used before may still open it: see \"A file this machine's key does not open\" in docs/troubleshooting.md.", err)
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "let in whoever recipients.yaml names without being asked; needed when there is no terminal")
	return cmd
}

func newStoreAuditCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "audit",
		Short: "check the store's values files against its manifest",
		Long: `Check that every value in the store is declared, that every required name has a value, that a secret is in an encrypted file and nothing else is, and that each value is not a placeholder and fits its type.

Prints names, files and what to do, never a value. Exits 1 when there is anything to fix. Without a key it still checks names and says which files' values it could not check.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sc, err := scopeFor(sys, true)
			if err != nil {
				return err
			}
			touchStore(sys)
			res, err := storeaudit.Audit(sc.dir, backendFor(sys))
			if err != nil {
				return err
			}
			fmt.Fprint(sys.Stdout, res.Report())
			if !res.OK() {
				return &ExitError{1}
			}
			return nil
		},
	}
}
