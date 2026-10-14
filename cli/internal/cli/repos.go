package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
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

// repoUsers sorts the registered repos by how their manifest, in the checkout on this machine, declares name:
// required (also through requiredWhen), optional, and the repos that could not be looked at, which have no
// checkout here (unchecked) or a manifest that cannot be read (unread). A store with no repos.yaml has none.
func repoUsers(sys System, name string) (required, optional, unchecked, unread []string, err error) {
	reg, _, _, err := loadRepos(sys)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cfg, err := loadConfig(sys)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for _, id := range reg.IDs() {
		ezc, ok := checkoutOf(sys, cfg, id)
		if !ok {
			unchecked = append(unchecked, id)
			continue
		}
		m, err := loadManifest(filepath.Join(ezc, manifestName), false)
		if err != nil {
			unread = append(unread, id)
			continue
		}
		switch registry.Uses(m, name) {
		case registry.Required:
			required = append(required, id)
		case registry.Optional:
			optional = append(optional, id)
		}
	}
	return required, optional, unchecked, unread, nil
}

// repoRewrap is what rewrapRepo did to a checkout: the encrypted files it re-wrapped, whether it wrote the policy,
// and the secrets a key that left a file could read, as <file>/<NAME> made printable. Skipped is a checkout with
// neither an encrypted file nor a policy.
type repoRewrap struct {
	Changed []string
	Leaked  []string
	Policy  bool
	Skipped bool
}

// repoLeftError is a re-wrap that failed and whose files could not all be put back as they were.
type repoLeftError struct {
	err   error
	files []string
}

func (e *repoLeftError) Error() string { return e.err.Error() }
func (e *repoLeftError) Unwrap() error { return e.err }

// rewrapRepo re-wraps the checkout's .ezc for the registry's keys with recipients.Rewrap, which puts everything back
// when it fails. A checkout whose files and policy are already for them is not written to, and finding that out needs
// no key. No git runs there: the result is for the repo's owner to commit.
func rewrapRepo(sys System, b backend.Backend, ezc string, reg recipients.Registry) (repoRewrap, error) {
	enc, err := encryptedFiles(ezc)
	if err != nil {
		return repoRewrap{}, err
	}
	// The backend's errors name the file.
	if err := unshowable(ezc, enc); err != nil {
		return repoRewrap{}, err
	}
	hasPolicy, err := b.HasPolicy(ezc)
	if err != nil {
		return repoRewrap{}, err
	}
	if len(enc) == 0 && !hasPolicy {
		return repoRewrap{Skipped: true}, nil
	}
	diffs, err := recipients.Differences(ezc, reg, b, enc)
	if err != nil {
		return repoRewrap{}, err
	}
	want := slices.Clone(reg.Keys())
	slices.Sort(want)
	// A policy this cannot read is not known to be the registry's.
	if policy, err := b.PolicyRecipients(filepath.Join(ezc, "common.enc.yaml")); len(diffs) == 0 && err == nil && slices.Equal(policy, slices.Compact(want)) {
		return repoRewrap{}, nil
	}
	var r repoRewrap
	for _, file := range enc {
		if len(diffs[file].Extra) == 0 {
			continue
		}
		names, _, err := b.Inspect(filepath.Join(ezc, file))
		if err != nil {
			return repoRewrap{}, err
		}
		for _, name := range names {
			r.Leaked = append(r.Leaked, recipients.Printable(file+"/"+name))
		}
	}
	watched := append(slices.Clone(enc), policyFiles(sys)...)
	before := fileStates(ezc, watched)
	if err := recipients.Rewrap(ezc, reg.Keys(), b, enc); err != nil {
		if left := changedFiles(ezc, watched, before); len(left) > 0 {
			return repoRewrap{}, &repoLeftError{err, left}
		}
		return repoRewrap{}, err
	}
	for _, file := range changedFiles(ezc, watched, before) {
		if slices.Contains(enc, file) {
			r.Changed = append(r.Changed, file)
		} else {
			r.Policy = true
		}
	}
	return r, nil
}

// sayRewrap prints what rewrapRepo did to the checkout of id, whose .ezc is ezc. A failure is the caller's to say.
func sayRewrap(w io.Writer, id, ezc string, r repoRewrap) {
	switch {
	case r.Skipped:
		fmt.Fprintf(w, "- %s: no secrets to re-wrap\n", id)
	case len(r.Changed) == 1:
		fmt.Fprintf(w, "✓ %s: re-wrapped %s in %s. Commit it there.\n", id, r.Changed[0], ezc)
	case len(r.Changed) > 1:
		fmt.Fprintf(w, "✓ %s: re-wrapped %s in %s. Commit them there.\n", id, strings.Join(r.Changed, ", "), ezc)
	case r.Policy:
		fmt.Fprintf(w, "✓ %s: wrote the encryption policy in %s. Commit it there.\n", id, ezc)
	default:
		fmt.Fprintf(w, "✓ %s: already encrypted for the store's recipients\n", id)
	}
	for _, secret := range r.Leaked {
		fix := "."
		// Printable left a name that could be an argument as it was.
		if file, name, _ := strings.Cut(secret, "/"); manifest.ValidName(name) {
			if env, ok := envFlag(file); ok {
				fix = ": ezc set " + name + env + "."
			}
		}
		fmt.Fprintf(w, "! %s: %s could be read by a key that has left. Give it a new value there%s\n", id, secret, fix)
	}
}
