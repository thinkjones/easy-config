package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/migrate"
)

func newMigrateCmd(sys System) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: migrate.Short,
		Long:  migrate.Long,
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			wd, err := sys.Getwd()
			if err != nil {
				return err
			}
			p, err := storePaths(sys)
			if err != nil {
				return err
			}
			err = migrate.Run(migrate.Options{
				Dir:             wd,
				Home:            homeDir(sys),
				Now:             sys.Now(),
				Environ:         sys.Environ(),
				Exec:            sys.Exec,
				LookPath:        sys.LookPath,
				Backend:         backendFor(sys),
				StoreEzc:        p.StoreEzc(),
				SchemaURL:       schemaID,
				KeyTextVariable: keyTextVariable,
				Stdout:          sys.Stdout,
				DryRun:          dryRun,
			})
			if errors.Is(err, migrate.ErrNotProved) {
				return &ExitError{Code: 1}
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "prove and report, and write nothing")
	return cmd
}
