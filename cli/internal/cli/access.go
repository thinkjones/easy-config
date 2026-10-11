package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
// With write it fetches now and refuses, before a byte is written, when the store's state cannot be told, when it is
// behind its remote, when a file an access change rewrites has uncommitted changes, when this machine has no key
// file, and when its key is not one the registry names.
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
	for _, name := range a.enc {
		if shown := recipients.Printable(name); shown != name {
			return nil, fmt.Errorf("an encrypted file in %s has a name that cannot be shown (%s). Rename it, then run this again.", a.dir, shown)
		}
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
	if s.Status.Behind > 0 {
		return errors.New("The store is behind its remote. Run ezc store pull, then run this again.")
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
}

// unchangedError is a failure of an access change after which the store is as it was.
type unchangedError struct{ err error }

func (e *unchangedError) Error() string {
	return strings.TrimSuffix(e.err.Error(), ".") + ". Nothing was changed."
}

func (e *unchangedError) Unwrap() error { return e.err }

// unpushedError is an access change that was committed here and whose push failed: the commit stays.
type unpushedError struct{ err error }

func (e *unpushedError) Error() string {
	return fmt.Sprintf("Committed, but the push failed (%s). Run ezc store push.", gitReason(e.err))
}

func (e *unpushedError) Unwrap() error { return e.err }

// apply is the one path every access change takes: the sequence (recipients.Apply), c.due, c.extra, the removals,
// one commit of the files that changed and a push when the store has a remote. A failure before the commit is
// made puts every file back; a push that fails leaves the commit.
func (a *access) apply(c change) error {
	_, err := a.run(c)
	return err
}

// run is apply, and whether it changed a file: a change that leaves every byte as it was commits and pushes nothing.
func (a *access) run(c change) (changed bool, err error) {
	files := append([]string{recipients.File, recipients.DueFile}, policyFiles(a.sys)...)
	files = append(append(files, a.enc...), c.remove...)
	before := fileStates(a.dir, files)
	undo := func(err error) error {
		if rerr := a.restore(files, before); rerr != nil {
			return fmt.Errorf("%s. %w", strings.TrimSuffix(err.Error(), "."), rerr)
		}
		return &unchangedError{err}
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
	done, err := g.Commit(a.sync.Dir, c.message, author, inEzc(written)...)
	if err != nil {
		return false, undo(fmt.Errorf("the change could not be committed: %s", gitReason(err)))
	}
	if !done {
		// Git saw nothing to commit in files whose bytes changed: it ignores them.
		return false, undo(fmt.Errorf("the change could not be committed: git ignores the store's files in %s", a.dir))
	}
	fmt.Fprintf(a.sys.Stdout, "✓ %s\n", c.message)
	remote := a.sync.Status.Remote
	if remote == "" {
		return true, nil
	}
	if _, err := g.Push(a.sync.Dir); err != nil {
		return true, &unpushedError{err}
	}
	fmt.Fprintf(a.sys.Stdout, "✓ pushed to %s\n", gitstore.ShowURL(remote))
	return true, nil
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
