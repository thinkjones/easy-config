package recipients

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Backend is what Apply needs of the encryption backend.
type Backend interface {
	WritePolicy(dir string, recipients []string) error
	Rewrap(path string) error
	Recipients(path string) ([]string, error)
}

// Apply is the one sequence every access change runs: save next, write the policy from its keys, re-wrap
// each file in encFiles (base names in dir) and check that each is now encrypted for exactly next's keys.
// Any failure puts back recipients.yaml, the encrypted files and every dot file in dir (the policy file,
// whose name recipients does not know), removes what it created, and returns the first error.
func Apply(dir string, next Registry, b Backend, encFiles []string) (err error) {
	saved, err := snapshotFiles(dir, encFiles)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if rerr := restore(dir, saved); rerr != nil {
				err = errors.Join(err, rerr)
			}
		}
	}()
	if err = next.Save(dir); err != nil {
		return err
	}
	want := slices.Clone(next.Keys())
	sort.Strings(want)
	want = slices.Compact(want)
	if err = b.WritePolicy(dir, next.Keys()); err != nil {
		return fmt.Errorf("the policy: %w", err)
	}
	for _, f := range encFiles {
		path := filepath.Join(dir, f)
		if err = b.Rewrap(path); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		got, rerr := b.Recipients(path)
		if rerr != nil {
			return fmt.Errorf("%s: %w", f, rerr)
		}
		got = slices.Clone(got)
		sort.Strings(got)
		if !slices.Equal(got, want) {
			return fmt.Errorf("%s: is not encrypted for exactly the registry's keys after re-wrapping", f)
		}
	}
	return nil
}

// snapshotFiles reads the bytes of the registry, the encrypted files and every dot file; absent is nil.
func snapshotFiles(dir string, encFiles []string) (map[string][]byte, error) {
	names := append([]string{File}, encFiles...)
	ents, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: cannot read this folder", dir)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") && !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	saved := map[string][]byte{}
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			saved[n] = nil
		case err != nil:
			return nil, fmt.Errorf("%s: cannot read this file", n)
		default:
			saved[n] = data
		}
	}
	return saved, nil
}

// restore puts the files back as snapshotFiles saw them and removes what was not there, dot files that
// appeared (a policy file that did not exist) included. It names each file it could not put back.
func restore(dir string, saved map[string][]byte) error {
	var errs []error
	fail := func(n string) {
		errs = append(errs, fmt.Errorf("could not restore %s: the folder is not as it was", n))
	}
	for n, data := range saved {
		path := filepath.Join(dir, n)
		if data == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				fail(n)
			}
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fail(n)
		}
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if _, had := saved[e.Name()]; !had && strings.HasPrefix(e.Name(), ".") && !e.IsDir() {
				if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
					fail(e.Name())
				}
			}
		}
	}
	return errors.Join(errs...)
}

// Diff is how a file's recipients differ from the registry: registry names that cannot read it, and what
// can that the registry does not list (the key itself).
type Diff struct{ Missing, Extra []string }

// Differences compares each file's recipients with the registry; only files that differ are in the map.
func Differences(dir string, r Registry, b interface {
	Recipients(string) ([]string, error)
}, encFiles []string) (map[string]Diff, error) {
	out := map[string]Diff{}
	for _, f := range encFiles {
		got, err := b.Recipients(filepath.Join(dir, f))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		var d Diff
		for _, n := range r.names() {
			if !slices.Contains(got, r.Recipients[n].Key) {
				d.Missing = append(d.Missing, n)
			}
		}
		for _, k := range got {
			if r.NameOf(k) == "" {
				d.Extra = append(d.Extra, k)
			}
		}
		if d.Missing != nil || d.Extra != nil {
			out[f] = d
		}
	}
	return out, nil
}
