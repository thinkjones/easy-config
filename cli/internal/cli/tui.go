package cli

import (
	"bytes"
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
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/registry"
	"github.com/thinkjones/easy-config/cli/internal/tui"
)

func newTUICmd(sys System) *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "browse and change values, access and repos in a terminal UI",
		Long: `Open the terminal UI: the values of the repo you are in (or the store's, with -g or g), who can read the store and who asked to, the registered repos and the doctor's checks.

Every change it makes is an ezc command, shown before it runs, and runs as it would on the command line without a terminal: a change the command would ask about first, such as an access change that also lets in someone else, is refused there and is made with the command itself. With no store set up it opens the setup wizard.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			if !sys.StdinIsTerminal() || sys.StdoutIsTerminal == nil || !sys.StdoutIsTerminal() || sys.RunTUI == nil {
				return usagef("ezc tui needs a terminal. Use the commands instead; ezc help lists them.")
			}
			// With no store at the default location, the wizard sets one up and then opens the views.
			p, explicit, err := storePathsLoose(sys)
			if err != nil {
				return err
			}
			if info, err := os.Stat(p.StoreEzc()); !explicit && (err != nil || !info.IsDir()) {
				return sys.RunTUI(tui.NewWizard(tuiDeps(sys), true))
			}
			scope := tui.RepoScope
			if global {
				scope = tui.StoreScope
			}
			return sys.RunTUI(tui.New(tuiDeps(sys), scope))
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "start with the store's values")
	return cmd
}

// tuiDeps is the TUI's view of ezc: reads call what the commands call, and every change is a command line.
func tuiDeps(sys System) tui.Deps {
	return tui.Deps{
		Store:    tuiStore{sys},
		Values:   tuiValues{sys},
		Access:   tuiAccess{sys},
		Repos:    tuiRepos{sys},
		Doctor:   tuiDoctor{sys},
		Commands: tuiCommands{sys},
	}
}

// quietSys is sys for a read: what a command would say on stderr (the store: line, notes) is not the TUI's.
func quietSys(sys System) System {
	sys.Stderr = io.Discard
	return sys
}

type tuiStore struct{ sys System }

func (s tuiStore) Status() (tui.Header, error) {
	q := quietSys(s.sys)
	p, err := storePaths(q)
	if err != nil {
		return tui.Header{}, err
	}
	st := touchStore(q)
	h := tui.Header{Store: p.Store(), Env: envName(q, "")}
	if st.Git && st.Status.Remote != "" {
		h.Store = gitstore.ShowURL(st.Status.Remote)
	}
	if st.Err != nil {
		h.Unknown = true
	} else {
		h.Ahead, h.Behind = st.Status.Ahead, st.Status.Behind
		h.Diverged = h.Ahead > 0 && h.Behind > 0
	}
	h.Offline = st.Offline
	h.Requests = len(st.Requests)
	repo := ""
	if w, err := workingRepo(q, ""); err == nil {
		h.Repo, repo = w.ID, w.Ezc
	}
	if h.Env == "" {
		// The default: local when it exists. A store with no environment file of that name has none to name.
		if res, err := load(q, repo, ""); err == nil && !res.CommonOnly {
			h.Env = res.EnvName
		}
	}
	return h, nil
}

type tuiValues struct{ sys System }

// scoped is the repo's .ezc for RepoScope, "" for the store's.
func (v tuiValues) scoped(s tui.Scope) (string, error) {
	if s == tui.StoreScope {
		return "", nil
	}
	return requireRepoDir(v.sys)
}

// List is what ezc list shows of the scope, and what ezc get and ezc repo uses would say of each name.
func (v tuiValues) List(s tui.Scope, env string) ([]tui.Value, error) {
	q := quietSys(v.sys)
	repo, err := v.scoped(s)
	if err != nil {
		return nil, err
	}
	res, err := load(q, repo, env)
	if err != nil {
		return nil, err
	}
	var ms []manifest.Manifest
	if repo != "" {
		m, _, err := manifestIfPresent(filepath.Join(repo, manifestName))
		if err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	sm, _, err := storeManifest(q)
	if err != nil {
		return nil, err
	}
	ms = append(ms, sm)
	users := repoManifests(q)
	due := map[string]recipients.DueEntry{}
	if p, err := storePaths(q); err == nil {
		due = dueEntries(p.StoreEzc())
	}

	var out []tui.Value
	row := func(name string) tui.Value {
		val := tui.Value{Name: name, Type: typeOf(ms, name)}
		for _, m := range ms {
			if d, ok := m.Lookup(name); ok {
				val.Desc = d.Desc
				break
			}
		}
		for _, id := range slices.Sorted(maps.Keys(users)) {
			switch registry.Uses(users[id], name) {
			case registry.Required:
				val.RequiredBy = append(val.RequiredBy, id)
			case registry.Optional:
				val.OptionalIn = append(val.OptionalIn, id)
			}
		}
		return val
	}
	for _, name := range res.Names() {
		val := row(name)
		val.File = fileOf(res, name)
		for _, p := range res.Overrides(name) {
			val.Overrides = append(val.Overrides, filepath.Base(p))
		}
		val.Secret = res.Encrypted[name]
		for _, n := range append([]string{name}, res.Refs[name]...) {
			for _, m := range ms {
				if d, ok := m.Lookup(n); ok && d.IsSecret() {
					val.Secret = true
				}
			}
		}
		if !val.Secret {
			val.Shown = res.Env[name]
		}
		if layer, file, ok := strings.Cut(val.File, "/"); ok && layer == "store" {
			if e, ok := due[file+"/"+name]; ok {
				val.Due = fmt.Sprintf("readable by removed %s (%s)", recipients.Printable(e.Removed), recipients.Printable(e.On))
			}
		}
		out = append(out, val)
	}
	for _, e := range ms[0].All() {
		if _, ok := res.Env[e.Name]; !ok {
			out = append(out, row(e.Name))
		}
	}
	return out, nil
}

// repoManifests are the manifests of the registered repos checked out here, by ID; a repo whose manifest cannot be
// read is left out, as ezc repo uses lists it apart.
func repoManifests(sys System) map[string]manifest.Manifest {
	reg, found, _, err := loadRepos(sys)
	if err != nil || !found {
		return nil
	}
	cfg, err := loadConfig(sys)
	if err != nil {
		return nil
	}
	out := map[string]manifest.Manifest{}
	for _, id := range reg.IDs() {
		ezc, ok := checkoutOf(sys, cfg, id)
		if !ok {
			continue
		}
		if m, err := loadManifest(filepath.Join(ezc, manifestName), false); err == nil {
			out[id] = m
		}
	}
	return out
}

// Reveal is the value ezc get --reveal prints, of the scope alone.
func (v tuiValues) Reveal(s tui.Scope, name, env string) (string, error) {
	repo, err := v.scoped(s)
	if err != nil {
		return "", err
	}
	res, err := load(quietSys(v.sys), repo, env)
	if err != nil {
		return "", err
	}
	value, ok := res.Env[name]
	if !ok {
		return "", fmt.Errorf("%s is not set in environment %s.", name, res.EnvLabel())
	}
	return value, nil
}

type tuiAccess struct{ sys System }

// Load is what ezc user list and ezc user requests show, and the rotation-due entries.
func (a tuiAccess) Load() (tui.AccessData, error) {
	q := quietSys(a.sys)
	acc, err := openAccess(q, false)
	if err != nil {
		return tui.AccessData{}, err
	}
	var d tui.AccessData
	for _, name := range slices.Sorted(maps.Keys(acc.reg.Recipients)) {
		r := acc.reg.Recipients[name]
		rec := tui.Recipient{Name: name, Kind: r.Kind, Owner: recipients.Printable(r.Owner), Added: recipients.Printable(r.Added), AddedBy: recipients.Printable(r.AddedBy), Me: r.Key != "" && r.Key == acc.myKey}
		for _, repo := range r.Repos {
			rec.Repos = append(rec.Repos, recipients.Printable(repo))
		}
		d.Recipients = append(d.Recipients, rec)
	}
	all, _ := pendingRequests(q, filepath.Dir(acc.dir), acc.sync.Git && acc.sync.Status.Remote != "")
	for _, p := range all {
		req := tui.Request{Name: p.Name, Author: recipients.Printable(p.Author)}
		if p.Err != nil {
			req.Problem = p.Err.Error()
		} else {
			req.Kind, req.Owner, req.Host, req.Requested, req.Key = p.Req.Kind, recipients.Printable(p.Req.Owner), recipients.Printable(p.Req.Host), recipients.Printable(p.Req.Requested), p.Req.Key
		}
		d.Requests = append(d.Requests, req)
	}
	due := dueEntries(acc.dir)
	for _, k := range slices.Sorted(maps.Keys(due)) {
		d.Due = append(d.Due, tui.DueEntry{Secret: recipients.Printable(k), Removed: recipients.Printable(due[k].Removed), On: recipients.Printable(due[k].On)})
	}
	if d.Differ, err = storeDiffers(q, acc.dir); err != nil {
		return d, err
	}
	return d, nil
}

type tuiRepos struct{ sys System }

// Load is ezc repo check --all, as rows.
func (r tuiRepos) Load(env string) ([]tui.Repo, error) {
	q := quietSys(r.sys)
	repos, found, storeEzc, err := loadRepos(q)
	if err != nil || !found {
		return nil, err
	}
	reg, haveReg, err := recipients.Load(storeEzc)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(q)
	if err != nil {
		return nil, err
	}
	var out []tui.Repo
	for _, id := range repos.IDs() {
		row := tui.Repo{ID: id}
		if ezc, ok := checkoutOf(q, cfg, id); ok {
			report, ok := checkOne(q, repoTarget{ID: id, Ezc: ezc, Here: true}, env, reg, haveReg)
			row.Here, row.OK, row.Checkout = true, ok, filepath.Dir(ezc)
			for _, l := range strings.Split(strings.TrimRight(report, "\n"), "\n") {
				row.Report = append(row.Report, strings.TrimPrefix(l, "  "))
			}
		}
		out = append(out, row)
	}
	return out, nil
}

type tuiDoctor struct{ sys System }

func (d tuiDoctor) Check(env string) ([]tui.Section, error) {
	report, err := doctorReport(quietSys(d.sys), env, "")
	if err != nil {
		return nil, err
	}
	var out []tui.Section
	for _, s := range report.Sections {
		sec := tui.Section{Name: s.Name}
		for _, it := range s.Items {
			sec.Items = append(sec.Items, tui.Item{State: tui.State(it.State), Label: it.Label, Detail: it.Detail, Fix: it.Fix})
		}
		out = append(out, sec)
	}
	return out, nil
}

type tuiCommands struct{ sys System }

// Run runs one command line in-process, as it runs without a terminal: what it prints on stdout and stderr is
// the outcome's lines, stdin is the given text and nothing is asked.
func (c tuiCommands) Run(args []string, stdin string) tui.Outcome {
	var buf bytes.Buffer
	sys := c.sys
	sys.Stdout, sys.Stderr = &buf, &buf
	sys.Stdin = strings.NewReader(stdin)
	no := func() bool { return false }
	sys.StdinIsTerminal, sys.StderrIsTerminal, sys.StdoutIsTerminal = no, no, no
	sys.ReadSecret = func(string) (string, error) { return "", errors.New("there is no terminal to ask on") }
	sys.RunTUI = nil
	err := Run(args, sys)
	var lines []string
	if text := strings.TrimRight(buf.String(), "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	var exit *ExitError
	if err != nil && !errors.As(err, &exit) {
		lines = append(lines, "✗ "+err.Error())
	}
	return tui.Outcome{Lines: lines, OK: err == nil}
}
