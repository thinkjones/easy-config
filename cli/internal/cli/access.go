package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// accessFetchTimeout is how long an access change waits for the remote before it judges the store.
const accessFetchTimeout = 30 * time.Second

// requestBranch starts the name of the branch of the store's remote that holds a join request: ezc/join-<name>.
const requestBranch = "ezc/join-"

// access is the store as a command that reads or changes who can decrypt it sees it.
type access struct {
	sys    System
	sync   storeSync
	dir    string // the store's .ezc
	reg    recipients.Registry
	me     string // the user of config.toml
	myKey  string // this machine's public key; "" when it has no key file to read it from
	myName string // the registry's name for myKey; "" when it has none
	b      backend.Backend
	enc    []string // the store's encrypted files, base names
}

// openAccess loads the store, its registry and this machine's public key. A store with no registry is an error.
// With write it fetches now and refuses, before a byte is written, when the store's state cannot be told, when its
// remote was not reached, when it is behind its remote, when a file an access change rewrites has uncommitted
// changes, when this machine has no key file, and when its key is not one the registry names.
func openAccess(sys System, write bool) (*access, error) {
	c, err := loadConfig(sys)
	if err != nil {
		return nil, err
	}
	p, err := storeFolder(sys)
	if err != nil {
		return nil, err
	}
	a := &access{sys: sys, dir: p.StoreEzc(), me: c.User, b: backendFor(sys)}
	if write {
		a.sync = syncStore(sys, 0, accessFetchTimeout)
		if err := a.guard(); err != nil {
			return nil, err
		}
	} else {
		a.sync = touchStore(sys)
	}

	// Decision 8: keyFileInUse is the one place that says whether the key is in a file.
	file, inFile := keyFileInUse(sys)
	if write && !inFile {
		return nil, errors.New("An access change needs a key file, and this machine uses none. Run this on a machine that keeps its key in a file.")
	}
	found := false
	if a.reg, found, err = recipients.Load(a.dir); err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s has no %s, so ezc does not know who can read it. Write one (README, \"Access\"), then run ezc store rewrap.", p.Store(), recipients.File)
	}
	if a.enc, err = encryptedFiles(a.dir); err != nil {
		return nil, err
	}
	// Whoever can push to the store names these files, and the errors of what follows name them too.
	if err := unshowable(a.dir, a.enc); err != nil {
		return nil, err
	}
	if inFile {
		key, err := a.b.PublicKey(file)
		switch {
		case err == nil:
			a.myKey, a.myName = key, a.reg.NameOf(key)
		case write:
			return nil, fmt.Errorf("no key was found in %s, so this machine cannot re-wrap the store. Run ezc doctor.", file)
		}
	}
	if write && a.myName == "" {
		return nil, errors.New("This machine's key is not a recipient of the store, so it cannot re-wrap it. Run ezc join.")
	}
	return a, nil
}

// guard refuses an access change the store's git state does not allow. A store that is not a git repo allows any.
func (a *access) guard() error {
	s := a.sync
	if !s.Git {
		return nil
	}
	if s.Err != nil {
		return cannotTell(s.Dir, s.Err)
	}
	// A change nobody else gets is the start of two registries, which no pull can merge.
	if s.Offline && s.Status.Remote != "" {
		return errors.New("The store's remote could not be reached, so nobody else would get this change. Run this again when it can be.")
	}
	if err := behind(s.Status); err != nil {
		return err
	}
	if !s.Status.Dirty {
		return nil
	}
	// Every change under .ezc, so that a file deleted by hand counts as well as one edited or added.
	changed, err := storeGit(a.sys).Changed(s.Dir, ezcDir)
	if err != nil {
		return err
	}
	policy := policyFiles(a.sys)
	for _, path := range changed {
		file := strings.TrimPrefix(path, ezcDir+"/")
		if strings.Contains(file, "/") {
			continue
		}
		if file == recipients.File || file == recipients.DueFile || slices.Contains(policy, file) || resolve.IsEncrypted(file) {
			return fmt.Errorf("%s has uncommitted changes in %s. Commit or discard them with git, then run this again.", file, s.Dir)
		}
	}
	return nil
}

// change is one change to who can decrypt the store.
type change struct {
	next    recipients.Registry
	message string                 // the commit's message, and the line printed after it
	due     func(d recipients.Due) // edits rotation-due.yaml; nil leaves the file alone
	extra   func() error           // runs after the sequence and before the commit; its failure restores
	remove  []string               // files in .ezc to delete in the commit, as paths inside it
	// subject is the name the change is about: its key comes or goes without a word. Anybody else who gains or
	// loses access to a file by the change is named before it is made.
	subject string
	// confirm is asked, before anything is written, when the change lets names other than its subject read a
	// file they could not read; its error stops the change. Every command sets it: alsoLetsIn for one that has a
	// subject. nil does not ask.
	confirm func(names []string) error
	// pushed says the change is of no use until the remote has it: a push that fails for any reason takes the
	// commit back. Without it only a push the remote refused because it moved does.
	pushed bool
}

// unchangedError is a failure of an access change after which the store is as it was.
type unchangedError struct {
	err  error
	then string // what to do before running the command again; "" when there is nothing
	said bool   // err's own text is the whole message: it was stopped before a byte was written
}

func (e *unchangedError) Error() string {
	if e.said {
		return e.err.Error()
	}
	msg := strings.TrimSuffix(e.err.Error(), ".") + ". Nothing was changed."
	if e.then != "" {
		msg += " " + e.then
	}
	return msg
}

func (e *unchangedError) Unwrap() error { return e.err }

// pullFirst is what an access change that a moved remote undid says to do.
const pullFirst = "Run ezc store pull, then run this again."

// unpushedError is an access change that was committed here and whose push failed: the commit stays.
type unpushedError struct{ err error }

func (e *unpushedError) Error() string {
	return fmt.Sprintf("Committed, but the push failed (%s). Run ezc store push.", gitReason(e.err))
}

func (e *unpushedError) Unwrap() error { return e.err }

// apply is the one path every access change takes: the sequence (recipients.Apply), c.due, c.extra, the removals,
// one commit of the files that changed and a push when the store has a remote. A failure before the commit is
// made puts every file back. A push the remote refuses because it has moved takes the commit back and puts the
// files back too; a push that fails for another reason leaves the commit, unless c.pushed asks for it back.
func (a *access) apply(c change) error {
	_, err := a.run(c)
	return err
}

// effect is what a change does to who reads the store's files beyond what it was asked to do. The registry is a
// file in a repo: whoever can push to the store can put a name in it, and the next re-wrap would let that name in.
type effect struct {
	gains map[string][]string // a name of the next registry, and the files it could not read until now
	loses map[string][]string // a key the next registry does not name, and the files it could read until now
}

// effect compares who each encrypted file is for with c.next, leaving out the keys of c.subject. It needs no key.
func (a *access) effect(c change) (effect, error) {
	e := effect{map[string][]string{}, map[string][]string{}}
	var own []string
	for _, reg := range []recipients.Registry{a.reg, c.next} {
		if r, ok := reg.Recipients[c.subject]; ok {
			own = append(own, r.Key)
		}
	}
	for _, file := range a.enc {
		got, err := a.b.Recipients(filepath.Join(a.dir, file))
		if err != nil {
			return e, fmt.Errorf("%s: cannot read who this file is encrypted for.", file)
		}
		for _, name := range slices.Sorted(maps.Keys(c.next.Recipients)) {
			if key := c.next.Recipients[name].Key; !slices.Contains(got, key) && !slices.Contains(own, key) {
				e.gains[name] = append(e.gains[name], file)
			}
		}
		for _, key := range got {
			if c.next.NameOf(key) == "" && !slices.Contains(own, key) {
				e.loses[key] = append(e.loses[key], file)
			}
		}
	}
	// The policy decides who a file made from now on is for, and in a store with no encrypted file it is all
	// there is to compare: a name it lacks, and that no file above already showed, gains what is set next.
	policy, err := a.b.PolicyRecipients(filepath.Join(a.dir, "common.enc.yaml"))
	if err != nil && !errors.Is(err, backend.ErrNoPolicy) {
		return e, errors.New("cannot read who the store's encryption policy names.")
	}
	if err == nil {
		for _, name := range slices.Sorted(maps.Keys(c.next.Recipients)) {
			if key := c.next.Recipients[name].Key; len(e.gains[name]) == 0 && !slices.Contains(policy, key) && !slices.Contains(own, key) {
				e.gains[name] = []string{"secrets set from now on"}
			}
		}
	}
	return e, nil
}

// announce prints one ! line for each name and each key of e. A name passed the registry's rule; a key comes
// from a file, and is made printable.
func (a *access) announce(e effect) {
	for _, name := range slices.Sorted(maps.Keys(e.gains)) {
		fmt.Fprintf(a.sys.Stdout, "! %s gains access to %s.\n", name, strings.Join(e.gains[name], ", "))
	}
	for _, key := range slices.Sorted(maps.Keys(e.loses)) {
		fmt.Fprintf(a.sys.Stdout, "! %s, a key %s does not name, loses access to %s. The secrets it could read are recorded as needing new values: ezc store status --due lists them.\n", recipients.Printable(key), recipients.File, strings.Join(e.loses[key], ", "))
	}
}

// strangersDue is due with, after it, an entry for every name in a file that a key of e.loses could read: a secret
// written while a key nobody names could read its file is as leaked as one a removed recipient read.
func (a *access) strangersDue(e effect, due func(recipients.Due)) (func(recipients.Due), error) {
	if len(e.loses) == 0 {
		return due, nil
	}
	today := a.sys.Now().Format("2006-01-02")
	type held struct{ file, name, key string }
	var all []held
	for _, key := range slices.Sorted(maps.Keys(e.loses)) {
		for _, file := range e.loses[key] {
			names, _, err := a.b.Inspect(filepath.Join(a.dir, file))
			if err != nil {
				return nil, fmt.Errorf("%s: cannot read the names in this file.", file)
			}
			for _, name := range names {
				all = append(all, held{file, name, key})
			}
		}
	}
	return func(d recipients.Due) {
		if due != nil {
			due(d)
		}
		for _, h := range all {
			d.Add(h.file, h.name, h.key, today)
		}
	}, nil
}

// alsoLetsIn is the confirm of a command that has a subject: the change would let in names it was not asked
// about, which the registry holds and no file is encrypted for yet. yes goes on; a terminal is asked on stderr,
// through ask when the command has asked a question of its own; without either the change is refused.
func alsoLetsIn(sys System, cmd *cobra.Command, yes bool, ask *asker) func(names []string) error {
	return func(names []string) error {
		switch {
		case yes:
			return nil
		case !sys.StdinIsTerminal():
			return usagef("%s: this would also let %s read the store. Check ezc user list: pass --yes when that is right, or take the name out with ezc user remove first. Try `%s`.", cmd.CommandPath(), someNames(names), helpFor(cmd))
		}
		if ask == nil {
			ask = &asker{sys: sys, in: bufio.NewReader(sys.Stdin)}
		}
		switch strings.ToLower(ask.ask("Let them in too? [y/N] ")) {
		case "y", "yes":
			return nil
		}
		return errors.New("Nothing was changed.")
	}
}

// alsoYes adds the --yes that answers alsoLetsIn's question in advance.
func alsoYes(cmd *cobra.Command, yes *bool) {
	cmd.Flags().BoolVar(yes, "yes", false, "also let in whoever else recipients.yaml names, without being asked; needed for that when there is no terminal")
}

// run is apply, and whether it changed a file: a change that leaves every byte as it was commits and pushes nothing.
func (a *access) run(c change) (changed bool, err error) {
	// Who else the change lets in or shuts out is said, and asked about, before a byte is written.
	e, err := a.effect(c)
	if err != nil {
		return false, &unchangedError{err: err}
	}
	if c.due, err = a.strangersDue(e, c.due); err != nil {
		return false, &unchangedError{err: err}
	}
	a.announce(e)
	if len(e.gains) > 0 && c.confirm != nil {
		if err := c.confirm(slices.Sorted(maps.Keys(e.gains))); err != nil {
			// The answer's own error, a usage error or an exit status, is what the command ends with.
			return false, &unchangedError{err: err, said: true}
		}
	}
	files := append([]string{recipients.File, recipients.DueFile}, policyFiles(a.sys)...)
	files = append(append(files, a.enc...), c.remove...)
	before := fileStates(a.dir, files)
	undo := func(err error) error {
		if rerr := a.restore(files, before); rerr != nil {
			return fmt.Errorf("%s. %w", strings.TrimSuffix(err.Error(), "."), rerr)
		}
		return &unchangedError{err: err}
	}
	if err := a.write(c); err != nil {
		return false, undo(err)
	}
	written := changedFiles(a.dir, files, before)
	if len(written) == 0 {
		return false, nil
	}
	if !a.sync.Git {
		fmt.Fprintf(a.sys.Stdout, "✓ %s\n", c.message)
		return true, nil
	}

	g := storeGit(a.sys)
	author := gitstore.Author{Name: "ezc"}
	if a.me != "" {
		author.Name = a.me
	}
	// Where the store is now, to return to when the commit has to be taken back.
	parent, parentErr := g.Head(a.sync.Dir)
	done, err := g.Commit(a.sync.Dir, c.message, author, inEzc(written)...)
	if err != nil {
		return false, undo(fmt.Errorf("the change could not be committed: %s", gitReason(err)))
	}
	if !done {
		// Git saw nothing to commit in files whose bytes changed: it ignores them.
		return false, undo(fmt.Errorf("the change could not be committed: git ignores the store's files in %s", a.dir))
	}
	// The commit just made, read now: one another command makes while the push runs is never taken back.
	made, madeErr := g.Head(a.sync.Dir)
	remote := a.sync.Status.Remote
	if remote == "" {
		fmt.Fprintf(a.sys.Stdout, "✓ %s\n", c.message)
		return true, nil
	}
	if _, err := g.Push(a.sync.Dir); err != nil {
		moved := errors.Is(err, gitstore.ErrRemoteMoved)
		if (moved || c.pushed) && parentErr == nil && madeErr == nil && a.uncommit(parent, made) {
			// The store's branch is where it was: only the commit's own files are left to put back.
			cause := &unchangedError{err: fmt.Errorf("The push failed (%s)", gitReason(err))}
			if moved {
				cause = &unchangedError{err: errors.New("The store's remote changed while this ran"), then: pullFirst}
			}
			if rerr := a.restore(files, before); rerr != nil {
				return false, fmt.Errorf("%s. %w", strings.TrimSuffix(cause.err.Error(), "."), rerr)
			}
			return false, cause
		}
		fmt.Fprintf(a.sys.Stdout, "✓ %s\n", c.message)
		return true, &unpushedError{err}
	}
	fmt.Fprintf(a.sys.Stdout, "✓ %s\n", c.message)
	fmt.Fprintf(a.sys.Stdout, "✓ pushed to %s\n", gitstore.ShowURL(remote))
	return true, nil
}

// uncommit takes back made, the commit run made on top of parent, and reports whether the store's branch is at
// parent again; it is not when the branch has moved on from made. Nothing in the work tree is touched: restore
// puts the commit's files back.
func (a *access) uncommit(parent, made string) bool {
	return made != parent && storeGit(a.sys).Uncommit(a.sync.Dir, parent, made) == nil
}

// write does everything of a change that comes before its commit.
func (a *access) write(c change) error {
	if err := recipients.Apply(a.dir, c.next, a.b, a.enc); err != nil {
		return err
	}
	if c.due != nil {
		due, err := recipients.LoadDue(a.dir)
		if err != nil {
			return err
		}
		c.due(due)
		if err := due.Save(a.dir); err != nil {
			return err
		}
	}
	if c.extra != nil {
		if err := c.extra(); err != nil {
			return err
		}
	}
	for _, name := range c.remove {
		if err := os.Remove(filepath.Join(a.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s: cannot remove this file", name)
		}
	}
	return nil
}

// restore puts the files that differ from before back: through git when the store is a repo, so that nothing stays
// staged, then from the bytes, which is all a plain folder has. It is an error when a file is still not as it was,
// or when git could not put the files back and still lists one as changed.
func (a *access) restore(files []string, before []fileState) error {
	var gitErr error
	if a.sync.Git {
		// What git cannot put back, the bytes below do.
		gitErr = storeGit(a.sys).Restore(a.sync.Dir, inEzc(changedFiles(a.dir, files, before))...)
	}
	left := changedFiles(a.dir, files, before)
	for i, name := range files {
		if !slices.Contains(left, name) {
			continue
		}
		path := filepath.Join(a.dir, name)
		if !before[i].found {
			os.Remove(path)
		} else if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
			os.WriteFile(path, before[i].data, 0o644)
		}
	}
	if left = changedFiles(a.dir, files, before); len(left) > 0 {
		return fmt.Errorf("The store could not be put back as it was: look at %s in %s.", strings.Join(left, ", "), a.dir)
	}
	if gitErr != nil {
		// The bytes are back, but a failed commit leaves its files staged.
		if staged, err := storeGit(a.sys).Changed(a.sync.Dir, inEzc(files)...); err != nil || len(staged) > 0 {
			return fmt.Errorf("The store's files are as they were, but git could not be put back (%s). Run git status in %s and unstage what it lists.", gitReason(gitErr), a.sync.Dir)
		}
	}
	return nil
}

// inEzc are files of the store's .ezc as paths from the top of the store, which is how git is given them.
func inEzc(files []string) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = ezcDir + "/" + file
	}
	return paths
}
