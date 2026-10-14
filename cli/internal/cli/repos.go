package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/registry"
	"github.com/thinkjones/easy-config/cli/internal/setup"
)

// repoIDRule is what a repo ID is, for a usage error that does not echo the one given.
const repoIDRule = "a repo ID: owner/repo, or a plain name of letters, digits, ., _ and -"

// githubSlug is the owner/repo of a remote on github.com, as a URL or in scp style; false for any other remote.
// What follows the host is the whole of it: github.com/mirror/owner/repo is no owner/repo.
func githubSlug(remote string) (string, bool) {
	if remote == "" || !onGitHub(remote) {
		return "", false
	}
	shown := strings.TrimSuffix(strings.TrimSuffix(gitstore.ShowURL(remote), "/"), ".git")
	var path string
	if _, rest, isURL := strings.Cut(shown, "://"); isURL {
		_, path, _ = strings.Cut(rest, "/")
	} else {
		_, path, _ = strings.Cut(shown, ":")
	}
	return path, setup.IsSlug(path)
}

// workRepo is the repo the working directory is in: its ID, the folder holding its .ezc, and that .ezc.
type workRepo struct {
	ID, Top, Ezc string
}

// workingRepo is the repo holding the nearest .ezc folder. Its ID is the owner/repo of its origin on github.com,
// else name (--name, "" for none), else the folder's name. The store is not a repo of its own registry.
// Reading origin is the one git call made in the repo.
func workingRepo(sys System, name string) (workRepo, error) {
	if name != "" && !registry.ValidID(name) {
		return workRepo{}, usagef("--name takes %s.", repoIDRule)
	}
	dir, err := requireRepoDir(sys)
	if err != nil {
		return workRepo{}, err
	}
	p, err := storePaths(sys)
	if err != nil {
		return workRepo{}, err
	}
	if sameDir(dir, p.StoreEzc()) {
		return workRepo{}, errors.New("this is the store's own folder, and the store is not a repo of its registry. Run this inside a repo that uses the store.")
	}
	r := workRepo{Top: filepath.Dir(dir), Ezc: dir}
	// Any error is a folder with no origin to read: a folder that is not a git repo, or no git.
	if remote, err := storeGit(sys).Remote(r.Top); err == nil {
		if slug, ok := githubSlug(remote); ok && registry.ValidID(slug) {
			r.ID = slug
			return r, nil
		}
	}
	r.ID = name
	if r.ID == "" {
		r.ID = filepath.Base(r.Top)
	}
	if !registry.ValidID(r.ID) {
		return workRepo{}, usagef("this repo's folder name is not %s. Give one with --name.", repoIDRule)
	}
	return r, nil
}

// hasEzc reports whether dir is an absolute path to a folder that holds a .ezc folder.
func hasEzc(dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, ezcDir))
	return err == nil && info.IsDir()
}

// checkoutOf is the .ezc folder of the repo's checkout on this machine: config.toml's [checkouts] path when that
// folder holds a .ezc, else the working repo when the ID is its own. It never writes the config.
func checkoutOf(sys System, c config.Config, id string) (ezc string, ok bool) {
	if dir, found := c.Checkouts[id]; found && hasEzc(dir) {
		return filepath.Join(dir, ezcDir), true
	}
	if w, err := workingRepo(sys, ""); err == nil && w.ID == id {
		return w.Ezc, true
	}
	return "", false
}

// loadRepos is the store's repos.yaml, whether it is there, and the store's .ezc folder.
func loadRepos(sys System) (registry.Registry, bool, string, error) {
	p, err := storePaths(sys)
	if err != nil {
		return registry.Registry{}, false, "", err
	}
	r, found, err := registry.Load(p.StoreEzc())
	return r, found, p.StoreEzc(), err
}
