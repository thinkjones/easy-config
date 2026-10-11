package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/paths"
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
	}, "status, pull, push or audit")
	cmd.AddCommand(newStoreStatusCmd(sys), newStorePullCmd(sys), newStorePushCmd(sys), newStoreAuditCmd(sys))
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
	return &cobra.Command{
		Use:   "status",
		Short: "say how the store stands against its remote",
		Long: `Fetch from the store's remote, then print where the store is, its remote, whether it is in sync, ahead, behind, diverged or holds uncommitted changes, when the remote was last reached, and the names of pending access requests.

Exits 0 in every state it can read. A store that is not a git repo is not synced, and is said to be.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := storeFolder(sys)
			if err != nil {
				return err
			}
			row := func(label, value string) { fmt.Fprintf(sys.Stdout, "%-10s%s\n", label, value) }
			s := syncStore(sys, 0, storeFetchTimeout)
			if !s.Git {
				row("store", p.Store())
				row("state", "not a git repo, so it is not synced")
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
				state = "uncommitted changes"
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
				requests = strings.Join(s.Requests, ", ")
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
			return nil
		},
	}
}

func newStorePullCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "bring the remote's commits into the store",
		Long: `Fetch from the store's remote and bring its commits in; commits made here are replayed on top of them.

An encrypted file changed both here and on the remote cannot be merged: the remote's version is kept, the names to set again are listed on stderr and the command exits 1. A plaintext file changed in the same place on both sides stops the pull with the store as it was. Prints names and files, never a value.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, g, err := storeRepo(sys, "pull")
			if err != nil {
				return err
			}
			dir := p.Store()
			res, err := g.Pull(dir, storePullTimeout, resolve.IsEncrypted)
			switch {
			case errors.Is(err, gitstore.ErrConflict):
				file, ok := strings.CutSuffix(err.Error(), ": "+gitstore.ErrConflict.Error())
				if !ok {
					// More went wrong than the conflict; the error says what.
					return err
				}
				was, it := "was", "it"
				if strings.Contains(file, ", ") {
					// gitstore lists more than one file with commas.
					was, it = "were", "them"
				}
				return fmt.Errorf("%s %s changed here and on the remote in the same place, so nothing was pulled. Resolve %s in %s with git (git pull --rebase).", file, was, it, dir)
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
				return nil
			}
			fmt.Fprintf(sys.Stdout, "pulled %s from origin\n", count(res.Commits, "commit"))
			if len(res.Replaced) == 0 {
				return nil
			}
			for _, r := range res.Replaced {
				fmt.Fprint(sys.Stderr, replacedNote(r))
			}
			return &ExitError{1}
		},
	}
}

// replacedNote says that a file took the remote's version, and the commands that make its local changes again.
// It reads the names of an encrypted file, which are not encrypted, and never a value.
func replacedNote(r gitstore.Replaced) string {
	file, inEzc := strings.CutPrefix(r.Path, ezcDir+"/")
	shown := file
	if q := strconv.Quote(file); q != `"`+file+`"` {
		shown = q
	}
	note := fmt.Sprintf("! %s was changed here and on the remote. The remote's version was kept; ", shown)
	const unlisted = "its local changes must be set again.\n"

	// The flag that names the file to `ezc set -g`; a file that is not the store's own has none.
	env := ""
	if name, ok := strings.CutPrefix(strings.TrimSuffix(file, ".enc.yaml"), "env."); ok && resolve.ValidEnvName(name) {
		env = " --env " + name
	} else if file != "common.enc.yaml" {
		return note + unlisted
	}
	if !inEzc {
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

Refused while the store is behind its remote or has diverged from it (run ` + "`ezc store pull`" + ` first), while it holds uncommitted changes, and when it has no remote.`,
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
			if st.Behind > 0 {
				return errors.New("the store is behind origin. Run `ezc store pull` first.")
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
