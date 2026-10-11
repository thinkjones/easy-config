package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

func newUserCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "user",
		Short: "say who can read the store, add someone or remove them, and answer requests for access",
	}, "list, add, remove, requests, approve or deny")
	cmd.AddCommand(newUserListCmd(sys), newUserAddCmd(sys), newUserRemoveCmd(sys), newUserRequestsCmd(sys), newUserApproveCmd(sys), newUserDenyCmd(sys))
	return cmd
}

func newUserListCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list who can read the store, and check that its files agree",
		Long: `List the store's recipients from recipients.yaml: name, kind, owner, when each was added and by whom. The row whose key is this machine's is marked.

Then compare the keys each encrypted file is encrypted for with the registry, and name every file that differs. Exits 1 when one does; ` + "`ezc store rewrap`" + ` fixes it. Needs no key, and prints no key that the registry names.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := openAccess(sys, false)
			if err != nil {
				return err
			}
			// The registry and the files come from the remote: what is not held to a rule by now is made printable.
			cell := func(s string) string {
				if s == "" {
					return "-"
				}
				return recipients.Printable(s)
			}
			rows := [][]string{{"NAME", "KIND", "OWNER", "ADDED", "ADDED BY"}}
			for _, name := range slices.Sorted(maps.Keys(a.reg.Recipients)) {
				r := a.reg.Recipients[name]
				row := []string{name, r.Kind, cell(r.Owner), cell(r.Added), cell(r.AddedBy)}
				if r.Key == a.myKey {
					row = append(row, "(this machine)")
				}
				rows = append(rows, row)
			}
			fmt.Fprint(sys.Stdout, table(rows))

			diffs, err := recipients.Differences(a.dir, a.reg, a.b, a.enc)
			if err != nil {
				return err
			}
			if len(diffs) == 0 {
				return nil
			}
			for _, file := range slices.Sorted(maps.Keys(diffs)) {
				var parts []string
				if d := diffs[file]; len(d.Missing) > 0 {
					parts = append(parts, "not encrypted for "+strings.Join(d.Missing, ", "))
				}
				if d := diffs[file]; len(d.Extra) > 0 {
					keys := make([]string, len(d.Extra))
					for i, key := range d.Extra {
						keys[i] = recipients.Printable(key)
					}
					parts = append(parts, "encrypted for "+strings.Join(keys, ", ")+" who are not in the registry")
				}
				fmt.Fprintf(sys.Stdout, "✗ %s: %s\n", recipients.Printable(file), strings.Join(parts, "; "))
			}
			fmt.Fprintln(sys.Stdout, "Run ezc store rewrap.")
			return &ExitError{1}
		},
	}
}

func newUserAddCmd(sys System) *cobra.Command {
	var kind, owner string
	cmd := &cobra.Command{
		Use:   "add <name> <key>",
		Short: "let a person or one of their machines read the store",
		Long: `Add a public key to recipients.yaml under name, write the encryption policy from it and re-wrap every encrypted file of the store for the new list, in one commit, then push.

The key is the public one, which ` + "`ezc setup --store`" + ` prints on a machine that cannot read the store yet. Never pass a private key. A machine (--kind machine) is a second key of a user who is already a recipient, named with --owner.

Needs this machine's key file, and the key must be one recipients.yaml names. Refused while the store is behind its remote or holds uncommitted changes to the files it rewrites. A failure puts the store back as it was; a push that fails leaves the commit, and ` + "`ezc store push`" + ` sends it.`,
		Args: exactArgs(2, "a name and a public key"),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, key := args[0], args[1]
			switch {
			case kind == recipients.KindBot:
				return usagef("%s: add a bot with ezc bot add.", cmd.CommandPath())
			case kind != recipients.KindUser && kind != recipients.KindMachine:
				return usagef("%s: --kind is user or machine. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			case kind == recipients.KindMachine && owner == "":
				return usagef("%s: --kind machine needs --owner, the user whose machine it is. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			case kind == recipients.KindUser && owner != "":
				return usagef("%s: --owner goes with --kind machine only. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			}
			// Neither argument is echoed unless it is a valid name: what was pasted may be a private key.
			if !recipients.ValidName(name) {
				return userNameRule(cmd)
			}
			if backendFor(sys).ValidRecipient(key) != nil {
				return errors.New("the second argument is not a valid public key.")
			}
			a, err := openAccess(sys, true)
			if err != nil {
				return err
			}
			by := a.me
			if by == "" {
				by = a.myName
			}
			next, err := a.reg.With(name, recipients.Recipient{Key: key, Kind: kind, Owner: owner, Added: sys.Now().Format("2006-01-02"), AddedBy: by})
			if err != nil {
				return fmt.Errorf("%w.", err)
			}
			return a.apply(change{next: next, message: "add " + name})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", recipients.KindUser, "user, or machine for a second key of a user")
	cmd.Flags().StringVar(&owner, "owner", "", "the user a machine belongs to")
	return cmd
}

func newUserRemoveCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "stop a person or one of their machines reading the store",
		Long: `Remove name from recipients.yaml, write the encryption policy from it and re-wrap every encrypted file for the new list, in one commit, then push. Then list the secrets the removed key could read, which need new values: they are recorded in rotation-due.yaml, and ` + "`ezc set -g`" + ` or ` + "`ezc unset -g`" + ` of one clears its entry.

Re-wrapping does not un-leak anything: the removed key still opens every older commit of the store, so only a new value protects a secret.

Refused for a bot (` + "`ezc bot remove`" + `), for the last recipient, for a user who owns machines (remove those first), and for the key of this machine when no other entry of the same person could read the store. Needs this machine's key file; a failure puts the store back as it was.`,
		Args: exactArgs(1, "a name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !recipients.ValidName(name) {
				return userNameRule(cmd)
			}
			a, err := openAccess(sys, true)
			if err != nil {
				return err
			}
			rec, ok := a.reg.Recipients[name]
			switch {
			case !ok:
				return fmt.Errorf("%s is not a recipient.", name)
			case rec.Kind == recipients.KindBot:
				return errors.New("remove a bot with ezc bot remove.")
			}
			next, err := a.reg.Without(name)
			var owns *recipients.OwnsError
			switch {
			case errors.Is(err, recipients.ErrLast):
				return fmt.Errorf("%s is the last recipient and cannot be removed.", name)
			case errors.As(err, &owns):
				return fmt.Errorf("this user owns machines: %s.", strings.Join(owns.Machines, ", "))
			case err != nil:
				return fmt.Errorf("%w.", err)
			}
			if name == a.myName && !a.otherWayIn(name) {
				return fmt.Errorf("%s is this machine's key and your only one. Removing it would lock you out.", name)
			}
			due, list, err := dueFor(a, name)
			if err != nil {
				return err
			}
			err = a.apply(change{next: next, message: "remove " + name, due: due})
			var unpushed *unpushedError
			if err != nil && !errors.As(err, &unpushed) {
				return err
			}
			// A commit that was not pushed is made all the same.
			if len(list) > 0 {
				was, need := "were", "need new values"
				if len(list) == 1 {
					was, need = "was", "needs a new value"
				}
				fmt.Fprintf(sys.Stdout, "%s %s readable by %s and %s:\n", count(len(list), "secret"), was, name, need)
				for _, entry := range list {
					fmt.Fprintf(sys.Stdout, "  %s\n", entry)
				}
				fmt.Fprintln(sys.Stdout, "Give each a new value with ezc set -g NAME.")
			}
			return err
		},
	}
}

// otherWayIn is whether a recipient other than name is the same person: a user of that name, or a machine of that user.
// A bot is nobody's.
func (a *access) otherWayIn(name string) bool {
	person := func(n string) string {
		switch r := a.reg.Recipients[n]; r.Kind {
		case recipients.KindUser:
			return n
		case recipients.KindMachine:
			return r.Owner
		}
		return ""
	}
	who := person(name)
	for n := range a.reg.Recipients {
		if n != name && who != "" && person(n) == who {
			return true
		}
	}
	return false
}

// dueFor is what removing a recipient leaves to rotate: for every name in every encrypted file of the store, an entry
// for removed dated today, and the "<file>/<NAME>" list to print. The names come from the files, which need no key.
func dueFor(a *access, removed string) (func(recipients.Due), []string, error) {
	today := a.sys.Now().Format("2006-01-02")
	type held struct{ file, name string }
	var all []held
	var list []string
	for _, file := range a.enc {
		names, _, err := a.b.Inspect(filepath.Join(a.dir, file))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: cannot read the names in this file.", file)
		}
		for _, name := range names {
			all = append(all, held{file, name})
			list = append(list, recipients.Printable(file+"/"+name))
		}
	}
	return func(d recipients.Due) {
		for _, h := range all {
			d.Add(h.file, h.name, removed, today)
		}
	}, list, nil
}

// userNameRule is the usage error for a name that could not be a recipient's. The argument is not repeated.
func userNameRule(cmd *cobra.Command) error {
	return usagef("%s: the name is lower case letters, digits and -, starting with a letter or a digit. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
}

// showRequest prints a valid request as whoever answers it must see it. Whoever filed it wrote all of it: the name,
// kind and owner are held to the name rule and the key to the backend's, and the rest is made printable.
func showRequest(w io.Writer, q pending) {
	cell := func(s string) string {
		if s == "" {
			return "-"
		}
		return recipients.Printable(s)
	}
	fmt.Fprintln(w, q.Name)
	for _, row := range [][2]string{
		{"kind", q.Req.Kind}, {"owner", cell(q.Req.Owner)}, {"key", q.Req.Key},
		{"host", cell(q.Req.Host)}, {"requested", cell(q.Req.Requested)}, {"author", cell(q.Author)},
	} {
		fmt.Fprintf(w, "  %-11s%s\n", row[0], row[1])
	}
}

func newUserRequestsCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "requests",
		Short: "list who is asking for access to the store",
		Long: `Fetch from the store's remote, then list the requests ` + "`ezc join`" + ` filed: for each its name, kind, owner, the whole public key, the host it was made on, its date and the Git author of the commit that filed it.

Everything in a request was written by whoever filed it, and anyone who can push to the store can file one under any name. The host, the date and the author are claims, not proof. Before ` + "`ezc user approve <name>`" + `, ask the person for their public key some other way and compare it with the key shown here.

A request that does not hold to the rules is listed as not valid, and cannot be approved; ` + "`ezc user deny <name>`" + ` removes it.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := storeFolder(sys)
			if err != nil {
				return err
			}
			if s := syncStore(sys, 0, accessFetchTimeout); s.Offline {
				fmt.Fprintln(sys.Stderr, "! The store's remote could not be reached: these are the requests as they were when it last was.")
			}
			all := pendingRequests(sys, p.Store())
			if len(all) == 0 {
				fmt.Fprintln(sys.Stdout, "No pending requests.")
			}
			for _, q := range all {
				if q.Err != nil {
					fmt.Fprintf(sys.Stdout, "%s: not a valid request (%s)\n", q.Name, q.Err)
					continue
				}
				showRequest(sys.Stdout, q)
			}
			return nil
		},
	}
}

// request is the pending request filed under name, as the remote holds it at the fetch openAccess just made. A
// remote that was not reached then is an error: what this machine remembers of it may not be the request any more.
func (a *access) request(name string) (pending, error) {
	if a.sync.Offline {
		return pending{}, errors.New("The store's remote could not be reached, so the request cannot be read as it stands. Run this again when it can be.")
	}
	for _, q := range pendingRequests(a.sys, filepath.Dir(a.dir)) {
		if q.Name == name {
			return q, nil
		}
	}
	return pending{}, fmt.Errorf("There is no request from %s. ezc user requests lists them.", name)
}

// deleteRequestBranch removes a request's branch from the remote.
func (a *access) deleteRequestBranch(name string) error {
	return storeGit(a.sys).DeleteRemoteBranch(a.sync.Dir, requestBranch+name, accessFetchTimeout)
}

// dropRequest removes a request's file from the store and commits that alone as message, then pushes. A failure
// before the commit is made puts the file back; a push that fails leaves the commit.
func (a *access) dropRequest(name, message string) error {
	files := []string{recipients.RequestPath(name)}
	before := fileStates(a.dir, files)
	undo := func(err error) error {
		if rerr := a.restore(files, before); rerr != nil {
			return fmt.Errorf("%s. %w", strings.TrimSuffix(err.Error(), "."), rerr)
		}
		return &unchangedError{err}
	}
	if err := os.Remove(filepath.Join(a.dir, filepath.FromSlash(files[0]))); err != nil {
		return undo(fmt.Errorf("%s: cannot remove this file", files[0]))
	}
	done := false
	if a.sync.Git {
		author := gitstore.Author{Name: "ezc"}
		if a.me != "" {
			author.Name = a.me
		}
		var err error
		// A file git never had leaves nothing to commit.
		if done, err = storeGit(a.sys).Commit(a.sync.Dir, message, author, inEzc(files)...); err != nil {
			return undo(fmt.Errorf("the change could not be committed: %s", gitReason(err)))
		}
	}
	fmt.Fprintf(a.sys.Stdout, "✓ %s\n", message)
	remote := a.sync.Status.Remote
	if !done || remote == "" {
		return nil
	}
	if _, err := storeGit(a.sys).Push(a.sync.Dir); err != nil {
		return &unpushedError{err}
	}
	fmt.Fprintf(a.sys.Stdout, "✓ pushed to %s\n", gitstore.ShowURL(remote))
	return nil
}

func newUserApproveCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <name>",
		Short: "let whoever filed a request read the store",
		Long: `Fetch from the store's remote, read the request filed under name as the remote holds it now, print it with its whole public key, then add that key to recipients.yaml, write the encryption policy and re-wrap every encrypted file, in one commit named ` + "`approve <name>`" + `, and push. Afterwards the request's branch is deleted, which closes its pull request.

The request's branch is never merged: only its request file is read, so nothing else on the branch reaches the store. A request file that is in the store itself, because its pull request was merged, is removed in the same commit.

Approving trusts a key that somebody else wrote down. Compare the key printed here, and by ` + "`ezc user requests`" + `, with the one the person gives you some other way before you run this. The host and the author are whatever the requester typed.

Needs this machine's key file, as ` + "`ezc user add`" + ` does, and refuses a name that is taken, a key that is registered and a request that is not valid. A failure puts the store back as it was; a push that fails leaves the commit and the branch.`,
		Args: exactArgs(1, "a name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !recipients.ValidName(name) {
				return userNameRule(cmd)
			}
			a, err := openAccess(sys, true)
			if err != nil {
				return err
			}
			q, err := a.request(name)
			if err != nil {
				return err
			}
			if q.Err != nil {
				return fmt.Errorf("The request of %s is not valid (%s). Refuse it with ezc user deny %s.", name, q.Err, name)
			}
			// Approved already, by a run whose push or whose clean-up failed: only what is left of the request goes.
			cur, already := a.reg.Recipients[name]
			already = already && cur.Key == q.Req.Key
			c := change{message: "approve " + name}
			if q.File {
				c.remove = []string{recipients.RequestPath(name)}
			}
			switch {
			case already && a.sync.Status.Ahead > 0:
				return errors.New("The store has commits its remote does not. Run ezc store push, then run this again.")
			case !already:
				by := a.me
				if by == "" {
					by = a.myName
				}
				if c.next, err = a.reg.With(name, recipients.Recipient{Key: q.Req.Key, Kind: q.Req.Kind, Owner: q.Req.Owner, Added: sys.Now().Format("2006-01-02"), AddedBy: by}); err != nil {
					return fmt.Errorf("%w.", err)
				}
			}

			// The key is shown before anything is changed.
			showRequest(sys.Stdout, q)
			branch := requestBranch + name
			if already {
				fmt.Fprintf(sys.Stdout, "✓ %s can already read the store\n", name)
				if q.File {
					if err := a.dropRequest(name, c.message); err != nil {
						return err
					}
				}
			} else if err := a.apply(c); err != nil {
				var unpushed *unpushedError
				if errors.As(err, &unpushed) && q.Branch {
					fmt.Fprintf(sys.Stdout, "! The request's branch %s was left. Run ezc store push, then ezc user approve %s again to delete it.\n", branch, name)
				}
				return err
			}
			if q.Branch {
				if err := a.deleteRequestBranch(name); err != nil {
					fmt.Fprintf(sys.Stdout, "! The request's branch %s could not be deleted (%s). Run ezc user approve %s again.\n", branch, gitReason(err), name)
				} else {
					fmt.Fprintf(sys.Stdout, "✓ deleted the branch %s\n", branch)
				}
			}
			fmt.Fprintf(sys.Stdout, "Tell %s to run ezc store pull.\n", name)
			return nil
		},
	}
}

func newUserDenyCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "deny <name>",
		Short: "refuse a request for access to the store",
		Long: `Fetch from the store's remote, then remove the request filed under name: its branch is deleted from the remote, which closes its pull request, and a request file that is in the store itself is removed in a commit named ` + "`deny <name>`" + ` and pushed. recipients.yaml and the encrypted files are not touched, and a request that is not valid is removed like any other.

Needs this machine's key file and a key that recipients.yaml names, as every change to who can read the store does.`,
		Args: exactArgs(1, "a name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !recipients.ValidName(name) {
				return userNameRule(cmd)
			}
			a, err := openAccess(sys, true)
			if err != nil {
				return err
			}
			q, err := a.request(name)
			if err != nil {
				return err
			}
			if q.Branch {
				branch := requestBranch + name
				if err := a.deleteRequestBranch(name); err != nil {
					return fmt.Errorf("The request's branch %s could not be deleted (%s). Nothing was changed.", branch, gitReason(err))
				}
				fmt.Fprintf(sys.Stdout, "✓ deleted the branch %s\n", branch)
			}
			if q.File {
				return a.dropRequest(name, "deny "+name)
			}
			return nil
		},
	}
}
