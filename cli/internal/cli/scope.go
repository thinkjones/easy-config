package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/backend/sops"
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

// storePaths is where ezc keeps things. The default store may be absent, but
// a store named with EZC_STORE that does not exist is an error: it is a typo.
func storePaths(sys System) (paths.Paths, error) {
	p, explicit, err := storePathsLoose(sys)
	if err != nil {
		return p, err
	}
	if explicit {
		if _, err := os.Stat(p.Store()); errors.Is(err, fs.ErrNotExist) {
			return p, fmt.Errorf("EZC_STORE is set to %s, which does not exist.", p.Store())
		}
	}
	return p, nil
}

// storePathsLoose is storePaths without the check that an EZC_STORE exists, and whether EZC_STORE was set.
// ezc doctor uses it, so that a typo is a finding in its report and not the end of it.
func storePathsLoose(sys System) (p paths.Paths, explicit bool, err error) {
	p, err = paths.Resolve(sys.Getenv, sys.GOOS)
	return p, sys.Getenv("EZC_STORE") != "", err
}

// policyFiles are the names of the backend's policy file, which doctor recognises in a .ezc folder.
func policyFiles(sys System) []string {
	if sys.Backend != nil {
		return []string{backend.FakePolicy}
	}
	return []string{sops.PolicyFile}
}

// keySource says where the default backend finds the key, for display, and whether a key is there: the key
// in the environment, else the key file the environment names, else the config's, else the default key file (shown with ~).
// With a backend set (tests) there is no real key: the key is found unless the backend is a locked Fake.
func keySource(sys System) (note string, found bool) {
	if sys.Backend != nil {
		locked := false
		if f, ok := sys.Backend.(*backend.Fake); ok {
			locked = f.Locked
		}
		return "test key", !locked
	}
	if sys.Getenv("SOPS_AGE_KEY") != "" {
		return "SOPS_AGE_KEY", true
	}
	if file := sys.Getenv("SOPS_AGE_KEY_FILE"); file != "" {
		_, err := os.Stat(file)
		return file, err == nil
	}
	if c, err := loadConfig(sys); err == nil && c.KeyFile != "" {
		_, err := os.Stat(c.KeyFile)
		return c.KeyFile, err == nil
	}
	home := homeDir(sys)
	if home == "" {
		return sops.DefaultKeyFile("~"), false
	}
	_, err := os.Stat(sops.DefaultKeyFile(home))
	return sops.DefaultKeyFile("~"), err == nil
}

// backendFor is sys.Backend, else the sops backend for this machine.
func backendFor(sys System) backend.Backend {
	if sys.Backend != nil {
		return sys.Backend
	}
	return sops.New(sops.Options{
		Run:      sys.Exec,
		LookPath: sys.LookPath,
		Environ:  sys.Environ(),
		KeyFile:  keyFile(sys),
	})
}

// keyFile is the config's key file, else the default one; "" with neither a config nor a home, where the path would be a relative one.
// A config that cannot be read counts as none: load reports it.
func keyFile(sys System) string {
	if c, err := loadConfig(sys); err == nil && c.KeyFile != "" {
		return c.KeyFile
	}
	home := homeDir(sys)
	if home == "" {
		return ""
	}
	return sops.DefaultKeyFile(home)
}

// load resolves the environment for repo (a .ezc directory, or "" for the store alone). An invalid config.toml is an error.
func load(sys System, repo, env string) (resolve.Result, error) {
	if _, err := loadConfig(sys); err != nil {
		return resolve.Result{}, err
	}
	p, err := storePaths(sys)
	if err != nil {
		return resolve.Result{}, err
	}
	// Run from inside the store, its .ezc is the repo's too; loading it as both
	// layers would list every name twice.
	if sameDir(repo, p.StoreEzc()) {
		repo = ""
	}
	return resolve.Load(resolve.Options{
		StoreDir: p.StoreEzc(),
		RepoDir:  repo,
		Env:      env,
		Home:     homeDir(sys),
		Decrypt:  backendFor(sys).Decrypt,
	})
}

// sameDir reports whether a and b are the same existing directory.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// target is the manifest a command works on. Source is how generated files name it: the same on every machine.
type target struct {
	Repo     string // the .ezc directory whose values go with the manifest
	Path     string // absolute, where the manifest is read
	Source   string
	Explicit bool // the path came from -m
}

// manifestTarget is the repo's own manifest, or the file named by -m, resolved against the working directory.
func manifestTarget(sys System, flag string) (target, error) {
	if flag == "" {
		repo, err := requireRepoDir(sys)
		return target{Repo: repo, Path: filepath.Join(repo, manifestName), Source: ezcDir + "/" + manifestName}, err
	}
	path, err := absPath(sys, flag)
	if err != nil {
		return target{}, err
	}
	return target{Repo: filepath.Dir(path), Path: path, Source: filepath.ToSlash(flag), Explicit: true}, nil
}

// absPath is p resolved against the working directory as sys sees it.
func absPath(sys System, p string) (string, error) {
	if filepath.IsAbs(p) {
		return p, nil
	}
	wd, err := sys.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, p), nil
}

// loadManifest reads and validates a manifest; an invalid one is an error listing every problem.
// explicit says the path came from -m rather than being the repo's own manifest.
func loadManifest(path string, explicit bool) (manifest.Manifest, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if explicit {
			return manifest.Manifest{}, fmt.Errorf("%s: no such file.", path)
		}
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

// warn prints the stderr notes a Result carries. repo is "" when no repo is in scope.
func warn(sys System, res resolve.Result, repo string) {
	if res.StoreMissing {
		p, err := paths.Resolve(sys.Getenv, sys.GOOS)
		where := p.Store()
		if err != nil {
			where = "the default location"
		}
		if repo == "" {
			fmt.Fprintf(sys.Stderr, "! no store at %s, so nothing was loaded. Run `ezc setup` to create one.\n", where)
		} else {
			fmt.Fprintf(sys.Stderr, "! no store at %s — only this repo's values were loaded. Run `ezc setup` to create one.\n", where)
		}
	}
	if res.CommonOnly && repo != "" {
		fmt.Fprintln(sys.Stderr, "! no environment named and no env.local.yaml — only the common files were loaded. Set EZC_ENV or pass a name.")
	}
}
