package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/storeaudit"
)

func newStoreCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "store",
		Short: "work on the global store as a whole",
	}, "audit")
	cmd.AddCommand(newStoreAuditCmd(sys))
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
