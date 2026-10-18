package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/check"
	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/registry"
)

// repoIDUsage is the usage error for an ID that fails registry.ValidID; the ID is never echoed.
func repoIDUsage(cmd *cobra.Command) error {
	return usagef("%s: give %s. Try `%s`.", cmd.CommandPath(), repoIDRule, helpFor(cmd))
}

func newRepoAddCmd(sys System) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "add [<owner/repo>]",
		Short: "register a repo in the store's repos.yaml and record where it is checked out",
		Long:  "Register the working repo in the store's repos.yaml, and record its folder in config.toml's [checkouts]. Its ID is the owner/repo of its origin on github.com, else --name, else the folder's name. With an ID, register that repo; its checkout is recorded only when it is the working repo. The change is committed to the store, not pushed.",
		Args:  maxArgs(1, "at most one repo ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name != "" && !registry.ValidID(name) {
				return usagef("%s: --name takes %s. Try `%s`.", cmd.CommandPath(), repoIDRule, helpFor(cmd))
			}
			if len(args) == 1 && !registry.ValidID(args[0]) {
				return repoIDUsage(cmd)
			}
			if len(args) == 1 && name != "" {
				return usagef("%s: give a repo ID or --name, not both. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			}
			return repoAdd(sys, args, name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the working repo's ID when its origin is not on github.com")
	return cmd
}

func repoAdd(sys System, args []string, name string) error {
	p, err := storeFolder(sys)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(sys)
	if err != nil {
		return err
	}
	work, workErr := workingRepo(sys, name)
	id := work.ID
	if len(args) == 1 {
		id = args[0]
	} else if workErr != nil {
		return workErr
	}
	// The checkout is recorded only for the working repo: another ID may be checked out anywhere, or nowhere.
	here := workErr == nil && work.ID == id

	st, err := guardStoreWrite(sys)
	if err != nil {
		return err
	}
	if err := guardStoreFiles(sys, st, registry.File); err != nil {
		return err
	}
	reg, _, ezc, err := loadRepos(sys)
	if err != nil {
		return err
	}
	if _, ok := reg.Repos[id]; ok {
		fmt.Fprintf(sys.Stdout, "✓ %s is registered\n", id)
	} else {
		by := cfg.User
		if by == "" {
			by = "ezc"
		}
		next, err := reg.With(id, registry.Repo{Added: sys.Now().Format("2006-01-02"), AddedBy: by})
		if err != nil {
			return err
		}
		if err := next.Save(ezc); err != nil {
			return err
		}
		if err := commitStore(sys, st, "repo add "+id, registry.File); err != nil {
			return err
		}
		fmt.Fprintf(sys.Stdout, "✓ added %s to %s\n", id, registry.File)
	}
	if !here {
		return nil
	}
	return recordCheckout(sys, p, cfg, id, work.Top)
}

// recordCheckout saves top as the checkout of id in config.toml's [checkouts], unless another folder that still
// holds a .ezc is recorded there: that one is kept, with a line saying so.
func recordCheckout(sys System, p paths.Paths, cfg config.Config, id, top string) error {
	if was, ok := cfg.Checkouts[id]; ok && was != top && hasEzc(was) {
		fmt.Fprintf(sys.Stdout, "! %s's checkout stays %s, which config.toml names. Edit [checkouts] in %s to move it.\n", id, was, p.Config())
		return nil
	}
	if cfg.Checkouts[id] != top {
		if cfg.Checkouts == nil {
			cfg.Checkouts = map[string]string{}
		}
		cfg.Checkouts[id] = top
		if err := config.Save(p.Config(), cfg); err != nil {
			return err
		}
	}
	fmt.Fprintf(sys.Stdout, "✓ checkout %s\n", top)
	return nil
}

func newRepoRemoveCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <owner/repo>",
		Short: "take a repo out of the store's repos.yaml",
		Long:  "Take the repo out of the store's repos.yaml and its checkout out of config.toml. Refused while a bot's entry in recipients.yaml names the repo. The change is committed to the store, not pushed.",
		Args:  exactArgs(1, "the repo's ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !registry.ValidID(args[0]) {
				return repoIDUsage(cmd)
			}
			return repoRemove(sys, args[0])
		},
	}
}

func repoRemove(sys System, id string) error {
	p, err := storeFolder(sys)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(sys)
	if err != nil {
		return err
	}
	st, err := guardStoreWrite(sys)
	if err != nil {
		return err
	}
	if err := guardStoreFiles(sys, st, registry.File); err != nil {
		return err
	}
	reg, _, ezc, err := loadRepos(sys)
	if err != nil {
		return err
	}
	if _, ok := reg.Repos[id]; !ok {
		return fmt.Errorf("%s is not in %s.", id, registry.File)
	}
	// A bot's key is delivered to the repos its entry names: the repo must stay registered while one does.
	people, _, err := recipients.Load(ezc)
	if err != nil {
		return err
	}
	if bots := botsOf(people, id); len(bots) > 0 {
		return fmt.Errorf("the bot %s gets its key in %s. Run ezc bot remove %s first, then run this again.", bots[0], id, bots[0])
	}
	next, err := reg.Without(id)
	if err != nil {
		return err
	}
	if err := next.Save(ezc); err != nil {
		return err
	}
	if err := commitStore(sys, st, "repo remove "+id, registry.File); err != nil {
		return err
	}
	fmt.Fprintf(sys.Stdout, "✓ removed %s from %s\n", id, registry.File)
	if _, ok := cfg.Checkouts[id]; !ok {
		return nil
	}
	delete(cfg.Checkouts, id)
	if err := config.Save(p.Config(), cfg); err != nil {
		return err
	}
	fmt.Fprintf(sys.Stdout, "✓ forgot its checkout in %s\n", p.Config())
	return nil
}

// botsOf are the names, sorted, of the bots whose entry names the repo.
func botsOf(r recipients.Registry, id string) []string {
	var names []string
	for name, e := range r.Recipients {
		if e.Kind == recipients.KindBot && slices.Contains(e.Repos, id) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func newRepoListCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list the repos in the store's repos.yaml, and where each is checked out here",
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return repoList(sys) },
	}
}

func repoList(sys System) error {
	reg, _, ezc, err := loadRepos(sys)
	if err != nil {
		return err
	}
	if len(reg.Repos) == 0 {
		_, err := fmt.Fprintln(sys.Stdout, "No repos are registered. Run ezc repo add inside a repo.")
		return err
	}
	people, _, err := recipients.Load(ezc)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(sys)
	if err != nil {
		return err
	}
	for _, id := range reg.IDs() {
		e := reg.Repos[id]
		where := "no checkout here"
		if dir, ok := checkoutOf(sys, cfg, id); ok {
			where = filepath.Dir(dir)
		}
		fmt.Fprintf(sys.Stdout, "%s  added %s by %s  %s\n", id, recipients.Printable(e.Added), recipients.Printable(e.AddedBy), where)
		for _, bot := range botsOf(people, id) {
			fmt.Fprintf(sys.Stdout, "  bot %s\n", bot)
		}
	}
	return nil
}

func newRepoCheckCmd(sys System) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "check [<owner/repo> | .] [ENV]",
		Short: "check a repo's environment, and that its encrypted files are for the store's recipients",
		Long:  "Check what `ezc env` checks, in a repo's checkout, and that each encrypted file there is encrypted for exactly the recipients the store's recipients.yaml names. With no argument, or . (so that ENV can follow), it checks the working repo, registered or not; with an ID, that repo; with --all, every repo in repos.yaml (the only argument is then ENV). A repo with no checkout on this machine is listed, and is not a failure. Exits 1 when a checked repo fails. Names and paths only.",
		Args:  maxArgs(2, "a repo ID and an environment name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 1 {
				return usagef("%s: with --all give at most an environment name. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			}
			if !all && len(args) > 0 && args[0] != workingID && !registry.ValidID(args[0]) {
				return repoIDUsage(cmd)
			}
			if all && len(args) == 1 || len(args) == 2 {
				if err := checkEnvName("repo check", args[len(args)-1]); err != nil {
					return err
				}
			}
			return repoCheck(sys, args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "check every repo in repos.yaml")
	return cmd
}

// repoTarget is a repo to act on: its ID, the .ezc folder of its checkout here, and whether it has one.
type repoTarget struct {
	ID, Ezc string
	Here    bool
}

// workingID is the argument that names the working repo, so that ENV can follow it. It is never a valid ID.
const workingID = "."

// repoTargets are the repos a command acts on. With all, every repo in repos.yaml and at most one argument (ENV);
// else no argument or workingID is the working repo, registered or not, and any other first argument is a
// registered repo's ID. A repo with no checkout here has Here false. Nothing is echoed from an invalid ID.
func repoTargets(sys System, args []string, all bool) ([]repoTarget, error) {
	if !all && (len(args) == 0 || args[0] == workingID) {
		w, err := workingRepo(sys, "")
		if err != nil {
			return nil, err
		}
		return []repoTarget{{ID: w.ID, Ezc: w.Ezc, Here: true}}, nil
	}
	if all && len(args) > 1 || !all && !registry.ValidID(args[0]) {
		return nil, usagef("give %s.", repoIDRule)
	}
	reg, _, _, err := loadRepos(sys)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(sys)
	if err != nil {
		return nil, err
	}
	ids := reg.IDs()
	if !all {
		if _, ok := reg.Repos[args[0]]; !ok {
			return nil, fmt.Errorf("%s is not in %s.", args[0], registry.File)
		}
		ids = []string{args[0]}
	}
	targets := make([]repoTarget, 0, len(ids))
	for _, id := range ids {
		ezc, ok := checkoutOf(sys, cfg, id)
		targets = append(targets, repoTarget{ID: id, Ezc: ezc, Here: ok})
	}
	return targets, nil
}

// repoDiffers are the encrypted files of a checkout that are not encrypted for exactly the registry's keys.
func repoDiffers(sys System, ezc string, reg recipients.Registry) (map[string]recipients.Diff, error) {
	enc, err := encryptedFiles(ezc)
	if err != nil {
		return nil, err
	}
	if err := unshowable(ezc, enc); err != nil {
		return nil, err
	}
	return recipients.Differences(ezc, reg, backendFor(sys), enc)
}

func repoCheck(sys System, args []string, all bool) error {
	touchStore(sys)
	targets, err := repoTargets(sys, args, all)
	if err != nil {
		return err
	}
	env := ""
	if all && len(args) == 1 || len(args) == 2 {
		env = args[len(args)-1]
	}
	p, err := storePaths(sys)
	if err != nil {
		return err
	}
	reg, haveReg, err := recipients.Load(p.StoreEzc())
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		_, err := fmt.Fprintln(sys.Stdout, "No repos are registered. Run ezc repo add inside a repo.")
		return err
	}
	var checked, failed, none int
	for _, t := range targets {
		if !t.Here {
			none++
			fmt.Fprintf(sys.Stdout, "%s: no checkout on this machine. Run ezc repo add inside it.\n", t.ID)
			continue
		}
		checked++
		report, ok := checkOne(sys, t, env, reg, haveReg)
		if !ok {
			failed++
		}
		fmt.Fprintf(sys.Stdout, "%s (%s)\n%s", t.ID, filepath.Dir(t.Ezc), report)
	}
	if all {
		fmt.Fprintf(sys.Stdout, "%d checked, %d failed, %d with no checkout here\n", checked, failed, none)
	}
	if failed > 0 {
		return &ExitError{1}
	}
	return nil
}

// checkOne is what `ezc env` checks in a checkout, then its encrypted files against the registry, as lines
// indented by two spaces; false when either fails.
func checkOne(sys System, t repoTarget, env string, reg recipients.Registry, haveReg bool) (string, bool) {
	var b strings.Builder
	ok := true
	line := func(s string) {
		for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			if l == "" {
				b.WriteString("\n")
			} else {
				fmt.Fprintf(&b, "  %s\n", l)
			}
		}
	}
	notes := &bytes.Buffer{}
	quiet := sys
	quiet.Stderr = notes
	m, err := loadManifest(filepath.Join(t.Ezc, manifestName), false)
	if err != nil {
		line("✗ " + err.Error())
		return b.String(), false
	}
	res, err := load(quiet, t.Ezc, envName(sys, env))
	if err != nil {
		line("✗ " + err.Error())
		return b.String(), false
	}
	warn(quiet, res, t.Ezc)
	if notes.Len() > 0 {
		line(notes.String())
	}
	if problems := check.Run(m, res); len(problems) > 0 {
		ok = false
		line(check.Report(problems, res))
	} else {
		line(check.ReportOK(res))
	}
	if !haveReg {
		return b.String(), ok
	}
	diffs, err := repoDiffers(sys, t.Ezc, reg)
	if err != nil {
		line("✗ " + err.Error())
		return b.String(), false
	}
	for _, f := range slices.Sorted(maps.Keys(diffs)) {
		ok = false
		line(fmt.Sprintf("✗ %s is not encrypted for exactly the store's recipients. Run ezc repo rewrap %s.", recipients.Printable(f), t.ID))
	}
	return b.String(), ok
}

func newRepoRewrapCmd(sys System) *cobra.Command {
	var all, yes bool
	cmd := &cobra.Command{
		Use:   "rewrap [<owner/repo>] [--all] [--yes]",
		Short: "encrypt a repo's secrets for the people the store's recipients.yaml names",
		Long: `Write the encryption policy of a repo's checkout from the store's recipients.yaml, and re-wrap each encrypted file there for exactly the keys it names. With no argument it re-wraps the working repo; with an ID, that repo; with --all, every repo in repos.yaml that has a checkout on this machine. The result is left in the checkout's .ezc for you to commit there: no git runs in a repo.

Refused before anything is written when the store has no recipients.yaml, while an encrypted file of the store is not encrypted for the people it names, and while the store's encryption policy does not name exactly them: a name somebody pushed into recipients.yaml is let in by ` + "`ezc store rewrap`" + `, which asks, before any repo follows it. A checkout with neither an encrypted file nor a policy gets its first policy when it is the working repo or the one named, after the same question for everyone recipients.yaml names, and is skipped with --all; one whose files are already for the store's recipients is not written to. Each secret a key that has left a file could read is named, with the command that gives it a new value.

Read ` + "`ezc user list`" + ` first. Each name that would gain access to a repo's secrets is printed before anything is written there, and the command asks ` + "`Let <names> read <id>'s secrets? [y/N]`" + ` on a terminal; anything but y leaves that repo as it is. Without a terminal it needs --yes.

Needs this machine's key for a file it re-wraps. A repo that fails, or is not let in, is left as it was and the command exits 1. Prints names and paths, never a value.`,
		Args: maxArgs(1, "a repo ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return usagef("%s: --all takes no repo ID. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			}
			if len(args) > 0 && !registry.ValidID(args[0]) {
				return repoIDUsage(cmd)
			}
			return repoRewrapAll(sys, cmd, args, all, yes)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "re-wrap every repo in repos.yaml that has a checkout here")
	cmd.Flags().BoolVar(&yes, "yes", false, "let in whoever recipients.yaml names without being asked; needed for that when there is no terminal")
	return cmd
}

func repoRewrapAll(sys System, cmd *cobra.Command, args []string, all, yes bool) error {
	touchStore(sys)
	targets, err := repoTargets(sys, args, all)
	if err != nil {
		return err
	}
	p, err := storePaths(sys)
	if err != nil {
		return err
	}
	reg, found, err := recipients.Load(p.StoreEzc())
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s has no %s, so ezc does not know who a repo's secrets are for. Write one (README, \"Access\"), then run ezc store rewrap.", p.Store(), recipients.File)
	}
	differs, err := storeDiffers(sys, p.StoreEzc())
	if err != nil {
		return err
	}
	if len(differs) > 0 {
		return fmt.Errorf("%s, so no repo is re-wrapped for it. Run ezc store rewrap first.", notForRegistry(differs...))
	}
	b := backendFor(sys)
	// In a store with no encrypted file the policy is all that says who recipients.yaml was let in for.
	if ok, err := policyIsFor(b, p.StoreEzc(), reg.Keys()); err != nil || !ok {
		return fmt.Errorf("The store's encryption policy does not name the people %s names, so no repo is re-wrapped for it. Run ezc store rewrap first.", recipients.File)
	}
	if len(targets) == 0 {
		_, err := fmt.Fprintln(sys.Stdout, "No repos are registered. Run ezc repo add inside a repo.")
		return err
	}

	// Who each repo would be opened to is known before any repo is written, so a refusal writes nothing.
	gains := make([][]string, len(targets))
	gainErrs := make([]error, len(targets))
	var gaining, opened []string
	for i, t := range targets {
		if !t.Here || sameDir(t.Ezc, p.StoreEzc()) {
			continue
		}
		gains[i], gainErrs[i] = repoGains(b, t.Ezc, reg, !all)
		if len(gains[i]) > 0 {
			gaining = append(gaining, gains[i]...)
			opened = append(opened, t.ID)
		}
	}
	if len(opened) > 0 && !yes && !sys.StdinIsTerminal() {
		slices.Sort(gaining)
		return usagef("%s: this would let %s read the secrets of %s. Check ezc user list: pass --yes when that is right, or take the name out with ezc user remove first. Try `%s`.", cmd.CommandPath(), someNames(slices.Compact(gaining)), someNames(opened), helpFor(cmd))
	}

	// One reader on stdin for every question: a second would lose what the first read ahead.
	var ask *asker
	failed := false
	for i, t := range targets {
		switch {
		case !t.Here:
			fmt.Fprintf(sys.Stdout, "- %s: no checkout on this machine. Run ezc repo add inside it, then run this again.\n", t.ID)
			continue
		case sameDir(t.Ezc, p.StoreEzc()):
			// A hand-written [checkouts] entry: the store's own files change through ezc store rewrap only.
			failed = true
			fmt.Fprintf(sys.Stdout, "✗ %s's checkout is the store's own folder. Nothing was changed there.\n", t.ID)
			continue
		case gainErrs[i] != nil:
			failed = true
			fmt.Fprintf(sys.Stdout, "✗ %s could not be re-wrapped (%s). Nothing was changed there.\n", t.ID, firstLineOf(gainErrs[i]))
			continue
		}
		if names := gains[i]; len(names) > 0 {
			// The names passed recipients.Load's rule.
			fmt.Fprintf(sys.Stderr, "! %s gain access to %s's secrets.\n", someNames(names), t.ID)
			if !yes {
				if ask == nil {
					ask = &asker{sys: sys, in: bufio.NewReader(sys.Stdin)}
				}
				if answer := strings.ToLower(ask.ask("Let " + someNames(names) + " read " + t.ID + "'s secrets? [y/N] ")); answer != "y" && answer != "yes" {
					failed = true
					fmt.Fprintln(sys.Stdout, "Not re-wrapped.")
					continue
				}
			}
		}
		r, err := rewrapRepo(sys, b, t.Ezc, reg, "", !all)
		var left *repoLeftError
		switch {
		case errors.As(err, &left):
			failed = true
			fmt.Fprintf(sys.Stdout, "✗ %s could not be re-wrapped (%s), and %s in %s could not be put back as it was. Restore it with git there.\n", t.ID, firstLineOf(err), strings.Join(left.files, ", "), t.Ezc)
		case err != nil:
			failed = true
			fmt.Fprintf(sys.Stdout, "✗ %s could not be re-wrapped (%s). Nothing was changed there.\n", t.ID, firstLineOf(err))
		default:
			sayRewrap(sys.Stdout, t.ID, t.Ezc, r)
		}
	}
	if failed {
		return &ExitError{1}
	}
	return nil
}

func newRepoUsesCmd(sys System) *cobra.Command {
	return &cobra.Command{
		Use:   "uses <NAME>",
		Short: "list the registered repos whose manifest declares a name",
		Long:  "List the repos in repos.yaml whose manifest, in the checkout on this machine, declares NAME: where it is required, where it is optional, and which repos could not be looked at. Names only.",
		Args:  exactArgs(1, "a variable name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !manifest.ValidName(args[0]) {
				return usagef("%s: give a variable name of letters, digits and _, not starting with a digit. Try `%s`.", cmd.CommandPath(), helpFor(cmd))
			}
			return repoUses(sys, args[0])
		},
	}
}

func repoUses(sys System, name string) error {
	touchStore(sys)
	required, optional, unchecked, unread, err := repoUsers(sys, name)
	if err != nil {
		return err
	}
	if len(required) == 0 && len(optional) == 0 {
		fmt.Fprintf(sys.Stdout, "No registered repo with a checkout here uses %s.\n", name)
	}
	say(sys.Stdout, "required by", required)
	say(sys.Stdout, "optional in", optional)
	say(sys.Stdout, "not checked (no checkout here)", unchecked)
	say(sys.Stdout, "not checked (manifest cannot be read)", unread)
	return nil
}

func say(w io.Writer, label string, ids []string) {
	if len(ids) > 0 {
		fmt.Fprintf(w, "%s: %s\n", label, strings.Join(ids, ", "))
	}
}
