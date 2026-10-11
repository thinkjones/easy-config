package cli

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

func newUserCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "user",
		Short: "say who can read the store, add someone or remove them",
	}, "list, add or remove")
	cmd.AddCommand(newUserListCmd(sys), newUserAddCmd(sys), newUserRemoveCmd(sys))
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
				return usagef("%s: the name is lower case letters, digits and -, starting with a letter or a digit. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
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
				return usagef("%s: the name is lower case letters, digits and -, starting with a letter or a digit. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
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
