// Package setup is what `ezc setup` does to a machine: the key, the store as
// a git repo, and the GitHub repo behind it. Every function leaves alone what
// is already there.
package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const (
	ezcDir       = ".ezc"
	manifestName = "manifest.yaml"
	// emptyManifest is the manifest of a new store: it declares nothing yet.
	emptyManifest = "schemaVersion: " + manifest.SchemaVersion + "\nrequired: {}\noptional: {}\n"
	commitMessage = "setup: create the store"
)

// ErrNoGh is gh missing from PATH.
var ErrNoGh = errors.New("gh is not installed; it is needed to create a GitHub repo. https://cli.github.com")

// Env is the machine as setup sees it.
type Env struct {
	Git      gitstore.Git
	Backend  backend.Backend
	Run      proc.Runner
	LookPath func(string) (string, error)
	Now      func() time.Time
	Paths    paths.Paths
	GOOS     string
}

var slugRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

// IsSlug reports whether s names a GitHub repo as owner/repo.
func IsSlug(s string) bool { return slugRE.MatchString(s) }

// HasStore reports whether the store's path already holds a store: a .ezc folder.
func HasStore(e Env) bool {
	info, err := os.Stat(e.Paths.StoreEzc())
	return err == nil && info.IsDir()
}

// Key returns the public half of the key in keyFile, creating the file (0600, its folder 0700) when it does not exist.
func Key(e Env, keyFile string) (public string, created bool, err error) {
	_, err = os.Lstat(keyFile)
	switch {
	case err == nil:
		public, err = e.Backend.PublicKey(keyFile)
		if errors.Is(err, backend.ErrNoKey) {
			return "", false, fmt.Errorf("%s exists but holds no key. Move it away and run ezc setup again.", keyFile)
		}
		return public, false, err
	case !errors.Is(err, fs.ErrNotExist):
		return "", false, fmt.Errorf("%s: cannot read this file", keyFile)
	}

	public, private, err := e.Backend.NewKey()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return "", false, fmt.Errorf("%s: cannot create the folder", filepath.Dir(keyFile))
	}
	// O_EXCL: a file that appeared since the check is never written over.
	f, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false, fmt.Errorf("%s: cannot write this file", keyFile)
	}
	_, werr := f.Write(private)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(keyFile)
		return "", false, fmt.Errorf("%s: cannot write this file", keyFile)
	}
	return public, true, nil
}

// InstallTool installs the backend's tool with Homebrew, wired to the terminal.
func InstallTool(e Env) error {
	formula := e.Backend.BrewFormula()
	if formula == "" {
		return errors.New("there is nothing to install with brew")
	}
	if _, _, err := e.Run(proc.Command{Env: e.Git.Environ, Name: "brew", Args: []string{"install", formula}, Interactive: true}); err != nil {
		return fmt.Errorf("brew install %s failed", formula)
	}
	return nil
}

// NewStore creates the store: git init, .ezc/manifest.yaml, recipients.yaml with name as its one user, the policy, one commit.
// When it fails it removes what it made, so that it can run again.
func NewStore(e Env, name, public string) (err error) {
	store, ezc := e.Paths.Store(), e.Paths.StoreEzc()
	if exists(ezc) {
		return fmt.Errorf("a store is already set up at %s.", store)
	}
	// What to take away again: the store's folder when it is new, else the two folders made inside it.
	made := []string{store}
	if exists(store) {
		made = nil
		for _, dir := range []string{ezc, filepath.Join(store, ".git")} {
			if !exists(dir) {
				made = append(made, dir)
			}
		}
	}
	defer func() {
		if err != nil {
			for _, dir := range made {
				os.RemoveAll(dir)
			}
		}
	}()

	if err := e.Git.Init(store); err != nil {
		return err
	}
	if err := os.Mkdir(ezc, 0o755); err != nil {
		return fmt.Errorf("%s: cannot create the folder", ezc)
	}
	if err := os.WriteFile(filepath.Join(ezc, manifestName), []byte(emptyManifest), 0o644); err != nil {
		return fmt.Errorf("%s: cannot write this file", filepath.Join(ezc, manifestName))
	}
	reg := recipients.Registry{Recipients: map[string]recipients.Recipient{
		name: {Key: public, Kind: "user", Added: e.Now().Format("2006-01-02"), AddedBy: name},
	}}
	if err := reg.Save(ezc); err != nil {
		return err
	}
	if err := e.Backend.WritePolicy(ezc, reg.Keys()); err != nil {
		return err
	}
	// The folder is new, so everything in it is what was written here.
	done, err := e.Git.Commit(store, commitMessage, gitstore.Author{Name: name}, ezcDir)
	if err != nil {
		return err
	}
	if !done {
		return fmt.Errorf("git: nothing in %s could be committed (is it ignored?)", ezc)
	}
	return nil
}

// Publish creates the private GitHub repo slug from the store and pushes to it; it returns the remote's URL.
func Publish(e Env, slug string) (remote string, err error) {
	store := e.Paths.Store()
	if err := e.gh("repo", "create", slug, "--private", "--source", store, "--remote", "origin", "--push"); err != nil {
		return "", err
	}
	return e.Git.Remote(store)
}

// CloneStore clones source into the store's path; it returns the remote's URL.
// A clone that is not a store is removed again.
func CloneStore(e Env, source string) (remote string, err error) {
	store := e.Paths.Store()
	existed := exists(store)
	if existed {
		if entries, err := os.ReadDir(store); err != nil || len(entries) > 0 {
			return "", fmt.Errorf("%s is in the way: it is not empty and not a store. Move it away and run ezc setup again.", store)
		}
	}
	if IsSlug(source) {
		err = e.gh("repo", "clone", source, store)
	} else {
		err = e.Git.Clone(source, store)
	}
	if err == nil && !exists(filepath.Join(e.Paths.StoreEzc(), manifestName)) {
		err = fmt.Errorf("%s is not an ezc store (it has no %s/%s).", Redact(source), ezcDir, manifestName)
	}
	if err != nil {
		unclone(store, existed)
		return "", err
	}
	return e.Git.Remote(store)
}

// unclone takes a clone away: the folder, or only what is in it when the folder was there before.
func unclone(store string, existed bool) {
	if !existed {
		os.RemoveAll(store)
		return
	}
	entries, _ := os.ReadDir(store)
	for _, entry := range entries {
		os.RemoveAll(filepath.Join(store, entry.Name()))
	}
}

// Recipient is the name the store's registry gives this key; "" when it is not a recipient.
func Recipient(e Env, public string) (string, error) {
	reg, _, err := recipients.Load(e.Paths.StoreEzc())
	if err != nil {
		return "", err
	}
	return reg.NameOf(public), nil
}

// gh runs gh. An error carries one line of what it said, never its stdout.
func (e Env) gh(args ...string) error {
	_, stderr, err := e.Run(proc.Command{Env: e.Git.Environ, Name: "gh", Args: args})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, exec.ErrNotFound):
		return ErrNoGh
	}
	for _, line := range strings.Split(string(stderr), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return errors.New("gh: " + line)
		}
	}
	return fmt.Errorf("gh: %v", err)
}

// Redact is a repo's URL without the name and password it may carry, for a message.
func Redact(source string) string {
	if u, err := url.Parse(source); err == nil && u.User != nil && u.Host != "" {
		u.User = nil
		return u.String()
	}
	return source
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
