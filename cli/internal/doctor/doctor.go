// Package doctor reports the health of the machine, the store and the repo.
//
// Nearly every way this tool goes wrong is quiet: a missing key loads nothing, an out-of-date shim
// evaluates to an empty environment, a misspelt file name is simply never read. `check` says an
// environment is wrong; doctor says where the setup is.
//
// Nothing here reads the process environment: the command passes in everything. Nothing here ever
// returns a value, only names, counts and paths.
package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/check"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
	"github.com/thinkjones/easy-config/cli/internal/storeaudit"
	"github.com/thinkjones/easy-config/cli/share"
)

// State is how one line reads. Warn is worth saying and does not fail the command; Broken does.
type State int

const (
	OK State = iota
	Warn
	Broken
)

func (s State) marker() string {
	switch s {
	case OK:
		return "✓"
	case Warn:
		return "!"
	}
	return "✗"
}

// Item is one fact. Fix, when there is one, says what to do.
type Item struct {
	State              State
	Label, Detail, Fix string
}

// Section is a group of items. Name is one of Sections.
type Section struct {
	Name  string
	Items []Item
}

type Report struct{ Sections []Section }

// Sections are the valid values of Options.Only, in report order.
var Sections = []string{"machine", "store", "repo"}

// Broken reports whether anything is broken, as opposed to merely worth saying.
func (r Report) Broken() bool {
	for _, s := range r.Sections {
		for _, it := range s.Items {
			if it.State == Broken {
				return true
			}
		}
	}
	return false
}

// String is the report: a line per section, then a line per item with its label padded to the section's
// widest, and under a Warn or Broken item its fix.
func (r Report) String() string {
	var b strings.Builder
	for i, s := range r.Sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s.Name + "\n")
		w := 0
		for _, it := range s.Items {
			w = max(w, len([]rune(it.Label)))
		}
		for _, it := range s.Items {
			line := fmt.Sprintf("  %s %-*s  %s", it.State.marker(), w, it.Label, it.Detail)
			b.WriteString(strings.TrimRight(line, " ") + "\n")
			if it.State != OK && it.Fix != "" {
				b.WriteString("      " + it.Fix + "\n")
			}
		}
	}
	return b.String()
}

// Options is what to look at. The command fills it in from System; this package reads only the folders it is given.
type Options struct {
	Only     string // "" for all, else one of Sections
	Version  string
	Home     string
	StoreDir string // the store's .ezc
	StoreSet bool   // EZC_STORE was set explicitly
	RepoDir  string // the repo's .ezc; "" when not in a repo
	Env      string // "" when none was named
	Backend  backend.Backend
	KeyNote  string // where the key comes from, for display
	KeyFound bool
	// ExtraFiles are file names that belong in a .ezc folder besides ezc's own, such as the backend's policy file.
	ExtraFiles []string

	ConfigPath  string // where config.toml is
	ConfigFound bool
	ConfigErr   string // "" unless the file could not be read
	User        string
	GitFound    bool
	GitVersion  string // "" when it could not be told
	// GitVersionUnread is set when git was asked its version and the answer could not be read.
	GitVersionUnread bool
	Sync             string // the store's state as one phrase, "" when the store is not a git repo
	SyncState        State  // OK, Warn or Broken for that phrase
	SyncFix          string // what to do about Sync, "" when nothing
	// Access: AccessKnown is false when this machine's public key could not be read, which leaves out the access item
	// unless the registry itself is the problem. Recipient is the registry's name for the key, "" when it is not a recipient.
	AccessKnown bool
	Recipient   string
	HasRegistry bool   // the store has a recipients.yaml
	RegistryErr string // "" unless it could not be loaded
	Requests    []string
	// Remote is the store's origin as it may be shown, "" for a store with none.
	Remote string
	// Request is the state of this machine's request for access, when its key is not a recipient: "pending" when a
	// request holds this machine's key (RequestName is its name), "not found" when none does, "" when not asked.
	Request, RequestName string
	// Recipients is how many the registry names; Differ the encrypted files whose recipients differ from it, and
	// DifferErr why that could not be told.
	Recipients int
	Differ     []string
	DifferErr  string
	// Due is how many entries rotation-due.yaml holds, DueErr why it could not be read.
	Due    int
	DueErr string
}

// Collect gathers the picture Options asks for. It never fails: whatever it cannot read is a Broken item.
func Collect(o Options) Report {
	var r Report
	for _, name := range Sections {
		if o.Only != "" && o.Only != name {
			continue
		}
		s := Section{Name: name}
		switch name {
		case "machine":
			s.Items = machine(o)
		case "store":
			s.Items = store(o)
		case "repo":
			s.Items = repo(o)
		}
		r.Sections = append(r.Sections, s)
	}
	return r
}

func machine(o Options) []Item {
	items := []Item{{OK, "ezc", o.Version, ""}}
	if err := o.Backend.Check(); err != nil {
		items = append(items, Item{Broken, "backend", firstLine(err), ""})
	} else {
		items = append(items, Item{OK, "backend", o.Backend.Name(), ""})
	}
	items = append(items, keyItem(o))
	return append(items, configItem(o), gitItem(o))
}

func keyItem(o Options) Item {
	if o.KeyFound {
		return Item{OK, "key", o.KeyNote, ""}
	}
	return Item{Warn, "key", "no key at " + o.KeyNote,
		"An environment with no encrypted file still loads. One that has any needs a key, for every command; `ezc setup` creates one."}
}

func configItem(o Options) Item {
	switch {
	case o.ConfigErr != "":
		return Item{Broken, "config", o.ConfigErr, ""}
	case !o.ConfigFound && o.StoreSet:
		// Setup does not run for a store EZC_STORE names, so it is not the thing to suggest.
		return Item{Warn, "config", "no config at " + o.ConfigPath + ".", ""}
	case !o.ConfigFound:
		return Item{Warn, "config", "no config at " + o.ConfigPath + ". Run `ezc setup`.", ""}
	case o.User != "":
		return Item{OK, "config", o.ConfigPath + " (user " + o.User + ")", ""}
	}
	return Item{OK, "config", o.ConfigPath, ""}
}

func gitItem(o Options) Item {
	if !o.GitFound {
		return Item{Broken, "git", "git is not installed.", ""}
	}
	if o.GitVersion != "" && gitstore.TooOld(o.GitVersion) {
		return Item{Broken, "git", fmt.Sprintf("git %s is too old; ezc needs %s or later.", o.GitVersion, gitstore.MinVersion), ""}
	}
	if o.GitVersionUnread {
		return Item{Warn, "git", "installed, but its version could not be read", fmt.Sprintf("ezc needs git %s or later.", gitstore.MinVersion)}
	}
	return Item{OK, "git", "installed", ""}
}

// syncItem is the store's state against its remote; none when the store is not a git repo.
func syncItem(o Options) []Item {
	if o.Sync == "" {
		return nil
	}
	return []Item{{o.SyncState, "sync", o.Sync, o.SyncFix}}
}

// accessItem says whether this machine's key can read the store. It is left out when there is nothing to say:
// the store has no registry and no policy, or the key's public half is not known.
func accessItem(o Options) []Item {
	switch {
	case o.RegistryErr != "":
		return []Item{{Broken, "access", o.RegistryErr, ""}}
	case !o.HasRegistry:
		if ok, _ := o.Backend.HasPolicy(o.StoreDir); ok {
			return []Item{{Warn, "access", "the store has an encryption policy but no recipients.yaml, so ezc cannot say who has access.",
				`Write recipients.yaml (README, "Access"), then run ezc store rewrap.`}}
		}
		return nil
	case !o.AccessKnown:
		return nil
	case o.Recipient == "" && o.Request == "pending":
		return []Item{{Broken, "access", "this machine's key cannot read the store yet; request " + o.RequestName + " is waiting for approval",
			"Ask someone who can read the store to run ezc user approve " + o.RequestName + "."}}
	case o.Recipient == "" && o.Remote == "":
		return []Item{{Broken, "access", "your key is not one of this store's recipients.", "Run ezc join --print."}}
	case o.Recipient == "":
		return []Item{{Broken, "access", "your key is not one of this store's recipients.", "Run ezc join " + o.Remote + "."}}
	}
	return []Item{{OK, "access", "your key is the recipient " + o.Recipient, ""}}
}

func requestsItem(o Options) []Item {
	if len(o.Requests) == 0 {
		return nil
	}
	unit := "requests"
	if len(o.Requests) == 1 {
		unit = "request"
	}
	return []Item{{Warn, "requests", fmt.Sprintf("%d access %s pending: %s", len(o.Requests), unit, someNames(o.Requests)), "Run ezc user requests."}}
}

// someNames is the first names of a list and how many more there are: whoever can push to the store can file any
// number of requests.
func someNames(names []string) string {
	const shown = 5
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
}

// recipientsFix is what to do about files that are not encrypted for the registry. The registry may be the side
// that is wrong: a re-wrap lets in every name it holds.
const recipientsFix = "Run ezc user list and check who it names, then ezc store rewrap."

// recipientsItem says whether the encrypted files are encrypted for the registry's recipients. It is left out
// when the store has no usable registry.
func recipientsItem(o Options) []Item {
	switch {
	case !o.HasRegistry || o.RegistryErr != "":
		return nil
	case o.DifferErr != "":
		return []Item{{Broken, "recipients", o.DifferErr, "Run ezc store rewrap, or ezc store audit for the detail."}}
	case len(o.Differ) == 1:
		return []Item{{Broken, "recipients", "1 file is not encrypted for the registry: " + displayName(o.Differ[0]), recipientsFix}}
	case len(o.Differ) > 1:
		names := make([]string, len(o.Differ))
		for i, f := range o.Differ {
			names[i] = displayName(f)
		}
		return []Item{{Broken, "recipients", fmt.Sprintf("%d files are not encrypted for the registry: %s", len(o.Differ), someNames(names)), recipientsFix}}
	case o.Recipients == 1:
		return []Item{{OK, "recipients", "every file is encrypted for the 1 recipient", ""}}
	}
	return []Item{{OK, "recipients", fmt.Sprintf("every file is encrypted for the %d recipients", o.Recipients), ""}}
}

// rotationItem is the secrets that were readable by a key that has been removed; none when there are none.
func rotationItem(o Options) []Item {
	switch {
	case o.DueErr != "":
		return []Item{{Broken, "rotation", o.DueErr, ""}}
	case o.Due == 0:
		return nil
	}
	unit := "secrets were"
	if o.Due == 1 {
		unit = "secret was"
	}
	return []Item{{Warn, "rotation", fmt.Sprintf("%d %s readable by a removed key", o.Due, unit),
		"ezc store status --due lists them; give each a new value with ezc set -g NAME, and --env for an environment's file."}}
}

func store(o Options) []Item {
	loc, ok := storeLocation(o)
	items := []Item{loc}
	if !ok {
		return items
	}
	items = append(items, syncItem(o)...)
	items = append(items, accessItem(o)...)
	items = append(items, recipientsItem(o)...)
	items = append(items, requestsItem(o)...)
	items = append(items, rotationItem(o)...)
	_, item, valid := manifestItem(o.StoreDir, func(m manifest.Manifest) string {
		return fmt.Sprintf("%d declared", len(m.Required)+len(m.Optional))
	}, Warn)
	items = append(items, item)
	if valid {
		items = append(items, auditItems(o)...)
	}
	items = append(items, policyItem(o, o.StoreDir))
	return append(items, filesItems(o, o.StoreDir, true)...)
}

// storeLocation is the location item, and whether there is a store to look into.
func storeLocation(o Options) (Item, bool) {
	info, err := os.Stat(o.StoreDir)
	switch {
	case errors.Is(err, fs.ErrNotExist) && o.StoreSet:
		return Item{Broken, "location", o.StoreDir + " does not exist", "EZC_STORE names a folder that is not there. Check its spelling."}, false
	case errors.Is(err, fs.ErrNotExist):
		return Item{Warn, "location", "no store", "Run `ezc setup` to create one."}, false
	case err != nil:
		return Item{Broken, "location", firstLine(err), ""}, false
	case !info.IsDir():
		return Item{Broken, "location", o.StoreDir + " is not a folder", ""}, false
	}
	return Item{OK, "location", o.StoreDir, ""}, true
}

// auditItems runs the store audit: its verdict, and a warning when it could not check some values.
func auditItems(o Options) []Item {
	res, err := storeaudit.Audit(o.StoreDir, o.Backend)
	var items []Item
	switch {
	case err != nil:
		items = append(items, Item{Broken, "audit", firstLine(err), ""})
	case res.OK():
		items = append(items, Item{OK, "audit", "nothing to fix", ""})
	default:
		items = append(items, Item{Broken, "audit", fmt.Sprintf("%d to fix — run `ezc store audit`", len(res.Findings)), ""})
	}
	if len(res.KeylessSkipped) > 0 {
		items = append(items, Item{Warn, "audit", "no key: value checks skipped for " + strings.Join(res.KeylessSkipped, ", "), ""})
	}
	if len(res.Unreadable) > 0 {
		items = append(items, Item{Warn, "audit", "damaged: value checks skipped for " + strings.Join(res.Unreadable, ", "), ""})
	}
	return items
}

func repo(o Options) []Item {
	if o.RepoDir == "" {
		return []Item{{Warn, "folder", "not in an ezc repo", "Run `ezc repo init` to create .ezc/ here."}}
	}
	items := []Item{{OK, "folder", o.RepoDir, ""}}
	m, item, valid := manifestItem(o.RepoDir, func(m manifest.Manifest) string {
		return fmt.Sprintf("%d required, %d optional", len(m.Required), len(m.Optional))
	}, Broken)
	items = append(items, item, shimItem(o.RepoDir), environmentsItem(o))
	if valid {
		items = append(items, environmentItem(o, m))
	}
	items = append(items, policyItem(o, o.RepoDir))
	return append(items, filesItems(o, o.RepoDir, false)...)
}

// manifestItem loads dir's manifest. absent is the state of the item when there is none.
func manifestItem(dir string, summary func(manifest.Manifest) string, absent State) (manifest.Manifest, Item, bool) {
	path := filepath.Join(dir, storeaudit.ManifestName)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return manifest.Manifest{}, Item{absent, "manifest", "no " + storeaudit.ManifestName, ""}, false
	}
	m, err := manifest.Load(path)
	if err != nil {
		return m, Item{Broken, "manifest", firstLine(err), ""}, false
	}
	if errs := m.Validate(); len(errs) > 0 {
		detail := "invalid: " + firstLine(errs[0])
		if len(errs) > 1 {
			detail += fmt.Sprintf(" (and %d more)", len(errs)-1)
		}
		return m, Item{Broken, "manifest", detail, ""}, false
	}
	return m, Item{OK, "manifest", summary(m), ""}, true
}

// shimItem compares the repo's env.sh with the one this binary carries. A stale shim is invisible
// otherwise: the file is there, it evaluates, and it can load nothing.
func shimItem(dir string) Item {
	const fix = "Run `ezc repo init`."
	data, err := os.ReadFile(filepath.Join(dir, "env.sh"))
	if errors.Is(err, fs.ErrNotExist) {
		return Item{Warn, "shim", "no .ezc/env.sh", fix}
	}
	if err != nil {
		return Item{Broken, "shim", firstLine(err), ""}
	}
	got, ok := share.VersionOf(string(data))
	switch {
	case !ok:
		return Item{Broken, "shim", "no version marker", fix}
	case got > share.ShimVersion:
		// repo init keeps it, so the fix is a newer ezc, not a rewrite.
		return Item{Warn, "shim", fmt.Sprintf("version %d, newer than this ezc (%d)", got, share.ShimVersion), "Upgrade ezc."}
	case got != share.ShimVersion:
		return Item{Broken, "shim", fmt.Sprintf("version %d, want %d", got, share.ShimVersion), fix}
	}
	return Item{OK, "shim", fmt.Sprintf("version %d", got), ""}
}

func environmentsItem(o Options) Item {
	envs, err := resolve.Envs(o.StoreDir, o.RepoDir)
	switch {
	case err != nil:
		return Item{Broken, "environments", firstLine(err), ""}
	case len(envs) == 0:
		return Item{Warn, "environments", "none", ""}
	}
	return Item{OK, "environments", strings.Join(envs, ", "), ""}
}

// environmentItem resolves the environment as the commands do and checks it against the repo's manifest.
func environmentItem(o Options, m manifest.Manifest) Item {
	repoDir := o.RepoDir
	if sameDir(o.StoreDir, repoDir) {
		repoDir = "" // run from inside the store, its .ezc is the repo's too
	}
	res, err := resolve.Load(resolve.Options{
		StoreDir: o.StoreDir,
		RepoDir:  repoDir,
		Env:      o.Env,
		Home:     o.Home,
		Decrypt:  o.Backend.Decrypt,
	})
	// Load records the environment it chose before it reads any file, so a failure still has a label.
	label := "environment"
	switch {
	case res.EnvName != "" || res.CommonOnly:
		label += " " + res.EnvLabel()
	case o.Env != "":
		label += " " + o.Env
	}
	if errors.Is(err, backend.ErrNoKey) {
		return Item{Warn, label, "cannot decrypt: no key", ""}
	}
	if err != nil {
		return Item{Broken, label, firstLine(err), ""}
	}
	if problems := check.Run(m, res); len(problems) > 0 {
		unit := "problems"
		if len(problems) == 1 {
			unit = "problem"
		}
		return Item{Broken, label, fmt.Sprintf("%d %s — run `ezc env`", len(problems), unit), ""}
	}
	vars, files := 0, 0
	for n := range res.Env {
		if !slices.Contains(resolve.SelectorVars, n) {
			vars++
		}
	}
	for _, s := range res.Sources {
		if s.Present {
			files++
		}
	}
	return Item{OK, label, fmt.Sprintf("%d vars from %d files", vars, files), ""}
}

// policyItem: a folder with encrypted files must hold the backend's policy, or nothing can be written there.
func policyItem(o Options, dir string) Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Item{Broken, "policy", firstLine(err), ""}
	}
	encrypted := false
	for _, e := range entries {
		encrypted = encrypted || (!e.IsDir() && strings.HasSuffix(e.Name(), ".enc.yaml"))
	}
	if !encrypted {
		return Item{OK, "policy", "not needed", ""}
	}
	ok, err := o.Backend.HasPolicy(dir)
	switch {
	case err != nil:
		return Item{Broken, "policy", firstLine(err), ""}
	case ok:
		return Item{OK, "policy", "present", ""}
	}
	return Item{Broken, "policy", "encrypted files but no policy", `See "Encryption" in the README.`}
}

// filesItems is one OK item, or one Warn item per file ezc does not read.
func filesItems(o Options, dir string, isStore bool) []Item {
	odd, err := Unrecognised(dir, isStore, o.ExtraFiles)
	if err != nil {
		return []Item{{Broken, "files", firstLine(err), ""}}
	}
	if len(odd) == 0 {
		return []Item{{OK, "files", "all recognised", ""}}
	}
	var items []Item
	for _, f := range odd {
		detail := displayName(f[0])
		if f[1] != "" {
			detail += " — did you mean " + f[1] + "?"
		}
		items = append(items, Item{Warn, "files", detail, ""})
	}
	return items
}

// displayName is a file name for the report: quoted when it holds whitespace or anything outside printable
// ASCII, so a name from a cloned repo cannot forge a line or send terminal escapes.
func displayName(name string) string {
	for _, r := range name {
		if r <= ' ' || r > '~' {
			return strconv.Quote(name)
		}
	}
	return name
}

// sameDir reports whether a and b are the same existing folder.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// firstLine is the first line of err's text: it names a path, never a value, and fits one report line.
func firstLine(err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	return strings.TrimSpace(first)
}
