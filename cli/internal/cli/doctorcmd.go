package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/doctor"
)

func newDoctorCmd(sys System) *cobra.Command {
	var only string
	cmd := &cobra.Command{
		Use:   "doctor [ENV]",
		Short: "report the health of the machine, the store and the repo",
		Long: `Report what is installed, where the store and the key are, whether the store and the repo are consistent, and whether the environment resolves and passes the manifest. ENV names the environment to check; without it the commands' own default applies.

Each line is a check, a warning (!) or a problem (✗), with what to do about it. Prints names, counts and paths, never a value. Exits 1 when anything is broken; warnings alone exit 0. Works without a key, and outside a repo.`,
		Args: maxArgs(1, "at most one environment name"),
		RunE: func(_ *cobra.Command, args []string) error {
			if only != "" && !slices.Contains(doctor.Sections, only) {
				return usagef("ezc doctor: --only takes %s, not %q. Try `ezc help doctor`.", strings.Join(doctor.Sections, ", "), only)
			}
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			if err := checkEnvName("doctor", arg); err != nil {
				return err
			}
			p, explicit, err := storePathsLoose(sys)
			if err != nil {
				return err
			}
			repo, err := repoDir(sys)
			if err != nil {
				return err
			}
			note, found := keySource(sys)
			report := doctor.Collect(doctor.Options{
				Only:       only,
				Version:    sys.Version,
				Home:       homeDir(sys),
				StoreDir:   p.StoreEzc(),
				StoreSet:   explicit,
				RepoDir:    repo,
				Env:        envName(sys, arg),
				Backend:    backendFor(sys),
				KeyNote:    note,
				KeyFound:   found,
				ExtraFiles: policyFiles(sys),
			})
			fmt.Fprint(sys.Stdout, report.String())
			if report.Broken() {
				return &ExitError{1}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&only, "only", "", "report one part: "+strings.Join(doctor.Sections, ", "))
	return cmd
}
