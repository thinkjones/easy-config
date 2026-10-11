package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

const (
	ezcDir       = ".ezc"
	manifestName = "manifest.yaml"
)

// homeDir is the user's home directory as sys sees it; "" when unset.
func homeDir(sys System) string {
	if sys.GOOS == "windows" {
		return sys.Getenv("USERPROFILE")
	}
	return sys.Getenv("HOME")
}

// repoDir is the .ezc directory of the nearest ancestor of the working directory that has
// one; "" when there is none. It need not hold a manifest.
func repoDir(sys System) (string, error) {
	dir, err := sys.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, ezcDir)
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// requireRepoDir is repoDir, or an error saying there is no repo.
func requireRepoDir(sys System) (string, error) {
	dir, err := repoDir(sys)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", fmt.Errorf("not in an ezc repo: no %s folder here or in any parent folder. Run `ezc repo init` to create one.", ezcDir)
	}
	return dir, nil
}

// envName is the first of: arg, $EZC_ENV, $APP_ENV. "" when none is set.
func envName(sys System, arg string) string {
	for _, v := range []string{arg, sys.Getenv("EZC_ENV"), sys.Getenv("APP_ENV")} {
		if v != "" {
			return v
		}
	}
	return ""
}

// load resolves the environment for repo (a .ezc directory, or "" for the store alone).
func load(sys System, repo, env string) (resolve.Result, error) {
	p, err := paths.Resolve(sys.Getenv, sys.GOOS)
	if err != nil {
		return resolve.Result{}, err
	}
	return resolve.Load(resolve.Options{
		StoreDir: p.StoreEzc(),
		RepoDir:  repo,
		Env:      env,
		Home:     homeDir(sys),
	})
}

// loadManifest reads and validates a manifest; an invalid one is an error listing every problem.
func loadManifest(path string) (manifest.Manifest, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return manifest.Manifest{}, fmt.Errorf("%s has no %s. Run `ezc repo init` to create one.", filepath.Dir(path), manifestName)
	}
	m, err := manifest.Load(path)
	if err != nil {
		return m, err
	}
	if errs := m.Validate(); len(errs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s is invalid:", path)
		for _, e := range errs {
			fmt.Fprintf(&b, "\n  %v", e)
		}
		return m, fmt.Errorf("%s", b.String())
	}
	return m, nil
}

// warn prints the stderr notes a Result carries.
func warn(sys System, res resolve.Result, repo string) {
	if res.StoreMissing {
		p, err := paths.Resolve(sys.Getenv, sys.GOOS)
		where := p.Store()
		if err != nil {
			where = "the default location"
		}
		fmt.Fprintf(sys.Stderr, "! no store at %s — only this repo's values were loaded. Run `ezc setup` to create one.\n", where)
	}
	if res.CommonOnly && repo != "" {
		fmt.Fprintln(sys.Stderr, "! no environment named and no env.local.yaml — only the common files were loaded. Set EZC_ENV or pass a name.")
	}
}
