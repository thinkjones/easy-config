// Package resolve composes a repo's environment from the shared store and the
// repo's own .ezc directory.
//
// This is the ONE resolver. Every command reads what it returns, so the thing
// being checked is by construction the thing that got loaded.
package resolve

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

// DefaultEnv applies when no environment is named. It must be the inert one:
// local reaches no live resource, so a forgotten selector cannot touch production.
const DefaultEnv = "local"

// ReservedEnv is not an environment name: it marks encrypted files (env.<name>.enc.yaml).
const ReservedEnv = "enc"

var envNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidEnvName reports whether s can name an environment.
func ValidEnvName(s string) bool { return envNamePattern.MatchString(s) && s != ReservedEnv }

// SelectorVars are the variables that name the environment. They are recorded
// in a Result and never exported.
var SelectorVars = []string{"EZC_ENV", "APP_ENV"}

// ShellOrigin is the Origin of a name that came from the shell, not a file.
const ShellOrigin = "shell"

// Options says where to look and what was asked for.
type Options struct {
	StoreDir string // the store's .ezc directory; "" or one that does not exist means no store
	RepoDir  string // the repo's .ezc directory; "" or one that does not exist means none (the store alone)
	Env      string // the environment named; "" when none was
	Home     string // the user's home, for ~ and $HOME; "" when there is none
	// Decrypt reads an *.enc.yaml. nil means this build cannot.
	Decrypt func(path string) ([]backend.Pair, error)
}

// Source is one candidate file, in load order.
type Source struct {
	Path    string
	Layer   string   // "store" or "repo"
	Present bool     // the file exists
	Vars    []string // names it supplied, in file order
}

// Result is the composed environment plus a record of where each value came
// from. Later files win, so Origin holds the LAST file to set each name.
type Result struct {
	Env    map[string]string
	Origin map[string]string // the last file to set each name, or ShellOrigin
	// Refs holds, for each name whose winning value referenced another, every name that value was built
	// from, sorted: the names it references and the ones their values were built from at that point.
	Refs map[string][]string
	// Encrypted marks each name whose winning value was read from an encrypted file or built from one that was.
	Encrypted    map[string]bool
	Sources      []Source // every candidate file, in precedence order
	EnvName      string   // "" when CommonOnly
	CommonOnly   bool
	StoreMissing bool
}

// Names returns the variable names in sorted order, so emission is deterministic.
func (r Result) Names() []string { return sortedKeys(r.Env) }

// EnvLabel names the environment for a human-readable report. It is never empty.
func (r Result) EnvLabel() string {
	if r.CommonOnly {
		return "(none)"
	}
	return r.EnvName
}

// Overrides returns the paths of the earlier sources that also set name, in
// load order: the values the last one to set it replaced.
func (r Result) Overrides(name string) []string {
	var paths []string
	for _, s := range r.Sources {
		if slices.Contains(s.Vars, name) {
			paths = append(paths, s.Path)
		}
	}
	if len(paths) > 0 {
		paths = paths[:len(paths)-1]
	}
	return paths
}

// Envs returns the sorted, de-duplicated environment names found in dirs: any
// env.<name>.yaml or env.<name>.enc.yaml whose name is a valid one. A directory that does not exist has
// none; any other read error is returned.
func Envs(dirs ...string) ([]string, error) {
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, "env.") || !strings.HasSuffix(n, ".yaml") {
				continue
			}
			if n == "env.enc.yaml" {
				continue
			}
			name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(n, "env."), ".yaml"), ".enc")
			if ValidEnvName(name) {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// isDir reports whether path is a directory. One that does not exist is not;
// one that is something else, or cannot be looked at, is an error, so that a
// layer which is there is never skipped as if it were absent. what names it.
func isDir(path, what string) (bool, error) {
	if path == "" {
		return false, nil
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case errors.Is(err, syscall.ENOTDIR), err == nil && !info.IsDir():
		return false, fmt.Errorf("%s: %s is not a folder", path, what)
	case err != nil:
		return false, err // an *fs.PathError, which names the path
	}
	return true, nil
}

// Load composes the environment named in o.
//
// Order is precedence, later wins. For environment E, store then repo, each:
//
//	common.yaml, common.enc.yaml, env.E.yaml, env.E.enc.yaml
//
// Every file is optional. An environment exists when any of its four files
// exists in either directory, so a repo that takes its per-environment values
// from the store can still name one.
//
// An EMPTY o.Env means none was named, which is not the same as asking for the
// default. If DefaultEnv exists it applies; if not, only the common.* files load
// and CommonOnly is set, rather than naming an environment never chosen. A NAMED
// environment that does not exist is an error listing the ones that do, so a
// typo cannot silently degrade to common-only.
func Load(o Options) (Result, error) {
	res := Result{Env: map[string]string{}, Origin: map[string]string{}, Refs: map[string][]string{}, Encrypted: map[string]bool{}}
	switch {
	case o.Env == ReservedEnv:
		return res, fmt.Errorf("%s is a reserved name; it marks encrypted files (env.<name>.enc.yaml)", ReservedEnv)
	case o.Env != "" && !ValidEnvName(o.Env):
		return res, fmt.Errorf("%q is not a valid environment name; use letters, digits, - and _", o.Env)
	}

	type layer struct{ name, dir string }
	var layers []layer
	ok, err := isDir(o.StoreDir, "the store")
	if err != nil {
		return res, err
	}
	if ok {
		layers = append(layers, layer{"store", o.StoreDir})
	} else {
		res.StoreMissing = true
	}
	if ok, err = isDir(o.RepoDir, "the repo's .ezc"); err != nil {
		return res, err
	} else if ok {
		layers = append(layers, layer{"repo", o.RepoDir})
	}
	dirs := make([]string, len(layers))
	for i, l := range layers {
		dirs[i] = l.dir
	}

	envs, err := Envs(dirs...)
	if err != nil {
		return res, err
	}
	name := o.Env
	switch {
	case name == "" && slices.Contains(envs, DefaultEnv):
		name = DefaultEnv
	case name == "":
		res.CommonOnly = true
	case !slices.Contains(envs, name):
		valid := "No environment files found."
		if len(envs) > 0 {
			valid = "Valid: " + strings.Join(envs, ", ")
		}
		return res, fmt.Errorf("no environment %q: no env.%s.yaml or env.%s.enc.yaml in %s.\n       %s",
			name, name, name, strings.Join(dirs, " or "), valid)
	}
	res.EnvName = name

	for _, l := range layers {
		files := []string{"common.yaml", "common.enc.yaml"}
		if !res.CommonOnly {
			files = append(files, "env."+name+".yaml", "env."+name+".enc.yaml")
		}
		for _, f := range files {
			res.Sources = append(res.Sources, Source{Path: filepath.Join(l.dir, f), Layer: l.name})
		}
	}

	for i := range res.Sources {
		s := &res.Sources[i]
		pairs, present, err := loadFile(s.Path, o.Home, o.Decrypt)
		if err != nil {
			return res, err
		}
		s.Present = present
		// res.Env is both the accumulating result and the scope a reference
		// resolves against, so a value sees every name from an earlier file and
		// every name above it in its own, and nothing else. Composition
		// therefore flows one direction only, which is what makes a cycle
		// impossible to write rather than something to detect.
		for _, pair := range pairs {
			// Emitters write names unquoted, so nothing else may reach them.
			if !manifest.ValidName(pair.Name) {
				return res, fmt.Errorf("%s: %q is not a valid variable name; use letters, digits and underscores, and do not start with a digit.", s.Path, pair.Name)
			}
			if slices.Contains(SelectorVars, pair.Name) {
				return res, fmt.Errorf("%s: %s names the environment and comes from the shell or the command line; remove it from this file.", s.Path, pair.Name)
			}
			if strings.ContainsRune(pair.Value, 0) {
				return res, fmt.Errorf("%s: %s contains a NUL byte, which an environment variable cannot hold", s.Path, pair.Name)
			}
			value, encrypted := pair.Value, IsEncrypted(s.Path)
			var built []string
			if !encrypted {
				var refs []string
				value, refs, err = expandRefs(value, pair.Name, s.Path, o.Home, res.Env)
				if err != nil {
					return res, err
				}
				// Taken before this name's own record changes: a value may reference the one it replaces.
				for _, ref := range refs {
					built = append(built, ref)
					built = append(built, res.Refs[ref]...)
					encrypted = encrypted || res.Encrypted[ref]
				}
			}
			delete(res.Refs, pair.Name)
			if len(built) > 0 {
				slices.Sort(built)
				res.Refs[pair.Name] = slices.Compact(built)
			}
			delete(res.Encrypted, pair.Name)
			if encrypted {
				res.Encrypted[pair.Name] = true
			}
			res.Env[pair.Name] = value
			res.Origin[pair.Name] = s.Path
			s.Vars = append(s.Vars, pair.Name)
		}
	}

	// The selector is part of the resolved environment but comes from the SHELL,
	// not from any file. It is recorded here so a check can validate it like
	// anything else in the manifest.
	//
	// The emitters deliberately omit it. If a shell integration exported it, that
	// tool would own the variable and unwind it before the next resolution, which
	// would then see no selector and silently fall back to local.
	//
	// Nothing is recorded when CommonOnly: the selector genuinely was not set,
	// and writing a default in would make a check validate a name the shell
	// never supplied.
	if !res.CommonOnly {
		for _, v := range SelectorVars {
			res.Env[v] = name
			res.Origin[v] = ShellOrigin
		}
	}
	return res, nil
}
