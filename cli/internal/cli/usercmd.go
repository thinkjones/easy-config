package cli

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

func newUserCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "user",
		Short: "say who can read the store, and add someone",
	}, "list or add")
	cmd.AddCommand(newUserListCmd(sys), newUserAddCmd(sys))
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
