package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/config"
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
