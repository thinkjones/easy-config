package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/ghcli"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/setup"
)

// The GitHub Actions secrets of a repo whose workflows read the store: the bot's private key, which ezc delivers,
// and the credential that reads the store's repo, which only the user can create.
const (
	botSecret        = "EZC_AGE_KEY"
	storeTokenSecret = "EZC_STORE_TOKEN"
)

const (
	botStoreNote = "The bot also needs to read the store repo. Create a read-only deploy key or a fine-grained token for it and save it as the " + storeTokenSecret + " secret in each repo. ezc cannot create that credential."
	botNoGh      = "gh is not installed, so the key cannot be saved in a repo. Install it (https://cli.github.com), or pass --print-private without --repo and save the key yourself."
)

func newBotCmd(sys System) *cobra.Command {
	cmd := verbs(&cobra.Command{
		Use:   "bot",
		Short: "give a bot a key that reads the store, take it away or replace it",
	}, "add, remove or rotate")
	cmd.AddCommand(newBotAddCmd(sys), newBotRemoveCmd(sys), newBotRotateCmd(sys))
	return cmd
}

func botGh(sys System) ghcli.GH {
	return ghcli.GH{Run: sys.Exec, LookPath: sys.LookPath, Environ: sys.Environ()}
}

// deliveryError is a bot's key that could not be saved in a repo, after it was saved in given.
type deliveryError struct {
	repo  string
	given []string
	err   error
}

func (e *deliveryError) Error() string {
	s := fmt.Sprintf("%s could not be set in %s (%s)", botSecret, e.repo, strings.TrimSuffix(e.err.Error(), "."))
	if len(e.given) > 0 {
		s += fmt.Sprintf(". It was set in %s to a key the store does not name, which reads nothing until this is run again", strings.Join(e.given, ", "))
	}
	return s
}

// giveBotKey applies c, a change that registers the public half of private, and hands the private half over. Before
// the commit it proves that the key opens every encrypted file and saves it as a secret of each repo, on gh's stdin;
// a failure of either puts the store back (decision 4). With printPrivate, stdout then carries the key and a newline
// and nothing else, so out, where every other line of the command goes, is stderr. done is whether the key was
// handed over: the error is then nil or an *unpushedError.
//
// private stays a []byte and reaches only the backend, gh's stdin and, when asked for, stdout: never an error.
func giveBotKey(sys System, a *access, c change, private []byte, repos []string, printPrivate bool) (out io.Writer, done bool, err error) {
	out = sys.Stdout
	if printPrivate {
		out = sys.Stderr
		a.sys.Stdout = sys.Stderr
	}
	gh := botGh(sys)
	c.extra = func() error {
		for _, file := range a.enc {
			// The backend's error is not passed on: it was given the key.
			if a.b.CanDecrypt(filepath.Join(a.dir, file), private) != nil {
				return fmt.Errorf("the new key cannot read %s", file)
			}
		}
		for i, repo := range repos {
			if err := gh.SecretSet(repo, botSecret, private); err != nil {
				return &deliveryError{repo: repo, given: repos[:i], err: err}
			}
		}
		return nil
	}
	err = a.apply(c)
	var unpushed *unpushedError
	if err != nil && !errors.As(err, &unpushed) {
		var unchanged *unchangedError
		var failed *deliveryError
		if errors.As(err, &unchanged) && errors.As(err, &failed) && len(failed.given) > 0 {
			// The store is as it was, but a repo is not.
			return out, false, fmt.Errorf("%s. The store was not changed.", failed)
		}
		return out, false, err
	}
	// A commit that was not pushed is made all the same, and the repos hold its key.
	for _, repo := range repos {
		fmt.Fprintf(out, "✓ %s set in %s\n", botSecret, repo)
	}
	if printPrivate {
		sys.Stdout.Write(bytes.TrimRight(private, "\r\n"))
		fmt.Fprintln(sys.Stdout)
	}
	return out, true, err
}

func newBotAddCmd(sys System) *cobra.Command {
	var repos []string
	var printPrivate bool
	cmd := &cobra.Command{
		Use:   "add <name> --repo owner/repo… [--print-private]",
		Short: "give a bot a key that reads the store, and save it in the repos that use it",
		Long: `Create a key for a bot, add its public half to recipients.yaml under name with kind bot, write the encryption policy and re-wrap every encrypted file of the store for the new list, in one commit named ` + "`add bot <name>`" + `, then push.

The private half is never written to disk. Before the commit, ezc checks that it opens every encrypted file and saves it as the ` + botSecret + ` Actions secret of each --repo, through gh, which reads it on stdin. When a repo cannot be given the key, the store is put back as it was and nothing is committed. The repos are recorded in the bot's entry.

With --print-private the private key is printed on stdout, with nothing else there: every other line goes to stderr. It is for a CI system that is not GitHub Actions; pipe it straight into that system's secret store. Give --repo, --print-private or both.

The bot also needs to read the store's repo, with a credential ezc cannot create: a read-only deploy key or a fine-grained token, saved as the ` + storeTokenSecret + ` secret of each repo.

Needs this machine's key file, as every change to who can read the store does, and gh when --repo is given.`,
		Args: exactArgs(1, "a name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if len(repos) == 0 && !printPrivate {
				return usagef("%s: name the repos that get the key with --repo, or pass --print-private.", cmd.CommandPath())
			}
			// Neither the name nor a repo is echoed unless it holds to its rule: what was pasted may be a key.
			if !recipients.ValidName(name) {
				return userNameRule(cmd)
			}
			var to []string
			for _, repo := range repos {
				if !setup.IsSlug(repo) {
					return usagef("%s: --repo is a GitHub repo, as owner/repo. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
				}
				if !slices.Contains(to, repo) {
					to = append(to, repo)
				}
			}
			// Decision 5: the repos are not checked against a registry of repos yet.
			if len(to) > 0 && !botGh(sys).Installed() {
				return errors.New(botNoGh)
			}
			a, err := openAccess(sys, true)
			if err != nil {
				return err
			}
			public, private, err := a.b.NewKey()
			if err != nil {
				return errors.New("a key could not be created for the bot.")
			}
			by := a.me
			if by == "" {
				by = a.myName
			}
			next, err := a.reg.With(name, recipients.Recipient{Key: public, Kind: recipients.KindBot, Added: sys.Now().Format("2006-01-02"), AddedBy: by, Repos: to})
			if err != nil {
				return fmt.Errorf("%w.", err)
			}
			out, done, err := giveBotKey(sys, a, change{next: next, message: "add bot " + name}, private, to, printPrivate)
			if done {
				fmt.Fprintln(out, botStoreNote)
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&repos, "repo", nil, "a GitHub repo, as owner/repo, whose Actions get the key; repeat it for more")
	cmd.Flags().BoolVar(&printPrivate, "print-private", false, "print the bot's private key on stdout, and everything else on stderr")
	return cmd
}

func newBotRemoveCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "stop a bot reading the store, and delete its key from its repos",
		Long: `Remove the bot name from recipients.yaml, write the encryption policy and re-wrap every encrypted file without its key, in one commit named ` + "`remove bot <name>`" + `, then push. Then delete the ` + botSecret + ` Actions secret from each repo the bot's entry names, through gh; a repo it could not be deleted from is named on a ` + "`!`" + ` line, and the bot is removed all the same.

As with ` + "`ezc user remove`" + `, the removed key still opens every older commit of the store, so the secrets it could read are recorded in rotation-due.yaml and listed: each needs a new value.

The ` + storeTokenSecret + ` secret is yours: ezc did not create it and does not delete it.

Refused for an entry that is not a bot (` + "`ezc user remove`" + `). Needs this machine's key file; a failure puts the store back as it was and deletes nothing.`,
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
			case rec.Kind != recipients.KindBot:
				return fmt.Errorf("%s is not a bot. Remove it with ezc user remove.", name)
			}
			next, err := a.reg.Without(name)
			if err != nil {
				return fmt.Errorf("%w.", err)
			}
			due, list, err := dueFor(a, name)
			if err != nil {
				return err
			}
			err = a.apply(change{next: next, message: "remove bot " + name, due: due})
			var unpushed *unpushedError
			if err != nil && !errors.As(err, &unpushed) {
				return err
			}
			// A commit that was not pushed is made all the same: the key is out of the registry here.
			deleteBotSecrets(sys, rec.Repos)
			sayDue(sys.Stdout, name, list)
			return err
		},
	}
}

// deleteBotSecrets deletes a removed bot's key from its repos and says how each went. The repos come from the
// registry, which whoever can push to the store wrote: one that is no owner/repo never reaches gh or the terminal raw.
func deleteBotSecrets(sys System, repos []string) {
	var from []string
	for _, repo := range repos {
		if !setup.IsSlug(repo) {
			fmt.Fprintf(sys.Stdout, "! %s named a repo that is not owner/repo (%s), so nothing was deleted there.\n", recipients.File, recipients.Printable(repo))
			continue
		}
		from = append(from, repo)
	}
	gh := botGh(sys)
	if len(from) > 0 && !gh.Installed() {
		fmt.Fprintf(sys.Stdout, "! gh is not installed, so %s was not deleted from %s. Delete it there yourself.\n", botSecret, strings.Join(from, ", "))
		return
	}
	for _, repo := range from {
		if err := gh.SecretDelete(repo, botSecret); err != nil {
			fmt.Fprintf(sys.Stdout, "! %s could not be deleted from %s (%s). Delete it there yourself.\n", botSecret, repo, strings.TrimSuffix(err.Error(), "."))
			continue
		}
		fmt.Fprintf(sys.Stdout, "✓ %s deleted from %s\n", botSecret, repo)
	}
}

func newBotRotateCmd(sys System) *cobra.Command {
	var printPrivate, compromised bool
	cmd := &cobra.Command{
		Use:   "rotate <name> [--print-private] [--compromised]",
		Short: "replace a bot's key, and save the new one in its repos",
		Long: `Create a new key for the bot name, put its public half in the bot's entry of recipients.yaml in place of the old one, write the encryption policy and re-wrap every encrypted file, in one commit named ` + "`rotate bot <name>`" + `, then push. From that commit on the old key opens no file.

The new private key is delivered as ` + "`ezc bot add`" + ` delivers one: checked against every encrypted file, then saved as the ` + botSecret + ` Actions secret of each repo the entry names, before the commit, and a failure puts the store back as it was. --print-private prints it on stdout, with every other line on stderr; a bot whose entry names no repo needs it.

A routine rotation records nothing to rotate. With --compromised, for a key that may have leaked, every secret the old key could read is recorded in rotation-due.yaml and listed, as ` + "`ezc bot remove`" + ` does: the old key still opens every older commit of the store.

Needs this machine's key file, and gh when the entry names a repo.`,
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
			case rec.Kind != recipients.KindBot:
				return fmt.Errorf("%s is not a bot.", name)
			case len(rec.Repos) == 0 && !printPrivate:
				return fmt.Errorf("%s has no repos to deliver a new key to. Pass --print-private and save the key yourself.", name)
			}
			// Whoever can push to the store wrote the entry's repos.
			for _, repo := range rec.Repos {
				if !setup.IsSlug(repo) {
					return fmt.Errorf("%s names a repo for %s that is not owner/repo (%s). Correct it there, then run this again.", recipients.File, name, recipients.Printable(repo))
				}
			}
			if len(rec.Repos) > 0 && !botGh(sys).Installed() {
				return errors.New(botNoGh)
			}
			public, private, err := a.b.NewKey()
			if err != nil {
				return errors.New("a key could not be created for the bot.")
			}
			next, err := a.reg.WithKey(name, public)
			if err != nil {
				return fmt.Errorf("%w.", err)
			}
			c := change{next: next, message: "rotate bot " + name}
			// Decision 6: a key that never left its repos leaks nothing by being replaced.
			old := name + " (old key)"
			var list []string
			if compromised {
				if c.due, list, err = dueFor(a, old); err != nil {
					return err
				}
			}
			out, done, err := giveBotKey(sys, a, c, private, rec.Repos, printPrivate)
			if done {
				sayDue(out, old, list)
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&printPrivate, "print-private", false, "print the bot's new private key on stdout, and everything else on stderr")
	cmd.Flags().BoolVar(&compromised, "compromised", false, "the old key may have leaked: record every secret it could read as due for a new value")
	return cmd
}
