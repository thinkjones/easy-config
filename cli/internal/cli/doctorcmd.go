package cli

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/doctor"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
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
			o := doctor.Options{
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
			}
			machineOptions(sys, &o)
			if only == "" || only == "store" {
				storeOptions(sys, p.StoreEzc(), &o)
			}
			if only == "" || only == "repo" {
				repoOptions(sys, p.StoreEzc(), repo, &o)
			}
			report := doctor.Collect(o)
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

// machineOptions fills in the config and git. An invalid config.toml is a finding, not a failure.
func machineOptions(sys System, o *doctor.Options) {
	if p, err := paths.Resolve(sys.Getenv, sys.GOOS); err == nil {
		o.ConfigPath = p.Config()
		c, found, err := config.Load(p.Config())
		o.ConfigFound, o.User = found, c.User
		if err != nil {
			o.ConfigErr = firstLineOf(err)
		}
	}
	_, err := sys.LookPath("git")
	o.GitFound = err == nil
	if o.GitFound && (o.Only == "" || o.Only == "machine") {
		var err error
		if o.GitVersion, err = storeGit(sys).Version(); err != nil {
			o.GitVersionUnread = true
		}
	}
}

// storeOptions fills in what doctor says about the store beyond its files: the sync state, whether this
// machine's key is a recipient, and the pending requests.
func storeOptions(sys System, storeEzc string, o *doctor.Options) {
	s := syncStore(sys, syncMaxAge, syncTimeout)
	o.Sync, o.SyncState, o.SyncFix = syncPhrase(sys, s)
	o.Requests = s.Requests
	if _, err := recipients.LoadDue(storeEzc); err != nil {
		o.DueErr = "cannot read " + recipients.DueFile
	} else {
		o.Due = s.Due
	}
	g := storeGit(sys)
	remote := ""
	if s.Git {
		remote, _ = g.Remote(s.Dir)
	}
	o.Remote = gitstore.ShowURL(remote)

	reg, found, err := recipients.Load(storeEzc)
	o.HasRegistry = found
	if err != nil {
		o.RegistryErr = firstLineOf(err)
		return
	}
	if !found {
		return
	}
	// Who the files are encrypted for is read from the files, so this needs no key.
	o.Recipients = len(reg.Recipients)
	// storeDiffers refuses a file whose name cannot be printed before the backend, whose errors name it, sees it.
	if differ, err := storeDiffers(sys, storeEzc); err != nil {
		o.DifferErr = firstLineOf(err)
	} else {
		o.Differ = differ
	}
	file, ok := keyFileInUse(sys)
	if !ok {
		return
	}
	public, err := backendFor(sys).PublicKey(file)
	if err != nil {
		return
	}
	o.AccessKnown, o.Recipient = true, reg.NameOf(public)
	if o.Recipient == "" {
		o.Request, o.RequestName = ownRequest(sys, filepath.Dir(storeEzc), remote != "", public)
	}
}

// repoOptions fills in whether the repo's encrypted files are encrypted for the store's registry. They are compared
// only when the store has a registry and the repo has an encrypted file, and never in the store's own folder.
// Who the files are encrypted for is read from the files, so this needs no key.
func repoOptions(sys System, storeEzc, repo string, o *doctor.Options) {
	if repo == "" || sameDir(repo, storeEzc) {
		return
	}
	// A registry that cannot be loaded is the store's access item.
	reg, found, err := recipients.Load(storeEzc)
	if err != nil || !found {
		return
	}
	enc, err := encryptedFiles(repo)
	if err != nil {
		o.RepoRecipients, o.RepoDifferErr = true, firstLineOf(err)
		return
	}
	if len(enc) == 0 {
		return
	}
	o.RepoRecipients = true
	diffs, err := repoDiffers(sys, repo, reg)
	if err != nil {
		o.RepoDifferErr = firstLineOf(err)
		return
	}
	o.RepoDiffer = slices.Sorted(maps.Keys(diffs))
}

// ownRequest is the state of this machine's request for access, and its name: pending when a valid request holds
// this machine's key, not found when none does.
func ownRequest(sys System, storeDir string, remote bool, public string) (state, name string) {
	all, _ := pendingRequests(sys, storeDir, remote)
	for _, q := range all {
		if q.Err == nil && q.Req.Key == public {
			return "pending", q.Name
		}
	}
	return "not found", ""
}

// syncPhrase is the store's state against its remote as one phrase, its state and what to do about it. The phrase is
// empty for a store that is not a git repo.
func syncPhrase(sys System, s storeSync) (phrase string, state doctor.State, fix string) {
	if !s.Git {
		return "", doctor.OK, ""
	}
	if s.Err != nil {
		return cannotTell(s.Dir, s.Err).Error(), doctor.Broken, ""
	}
	st := s.Status
	switch st.State() {
	case gitstore.Ahead:
		phrase, state, fix = count(st.Ahead, "commit")+" ahead of origin", doctor.Warn, "Run `ezc store push`."
	case gitstore.Behind:
		phrase, state, fix = count(st.Behind, "commit")+" behind origin", doctor.Warn, "Run `ezc store pull`."
	case gitstore.Diverged:
		phrase, state, fix = fmt.Sprintf("diverged from origin (%d ahead, %d behind)", st.Ahead, st.Behind), doctor.Broken, "Run `ezc store pull`."
	case gitstore.Uncommitted:
		phrase, state, fix = "uncommitted changes", doctor.Warn, "Commit or discard them with git in "+s.Dir+"."
	default:
		phrase = "local only"
		if remote, err := storeGit(sys).Remote(s.Dir); err == nil && remote != "" {
			phrase = "in sync"
		}
	}
	if s.Offline {
		phrase += " (origin could not be reached)"
		state = max(state, doctor.Warn)
	}
	return phrase, state, fix
}

func firstLineOf(err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	return strings.TrimSpace(first)
}
