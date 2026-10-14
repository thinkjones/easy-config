package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// An encrypted file of the store is written to, pulled and pushed only while it is encrypted for exactly the keys
// recipients.yaml names: a key the registry no longer names would read whatever is written to the file next.
// Nothing here needs a key.

// notForRegistry says that the recipients of these files are not the registry's. Their names are made printable.
func notForRegistry(files ...string) string {
	shown := make([]string, len(files))
	for i, file := range files {
		shown[i] = recipients.Printable(file)
	}
	is := "is"
	if len(files) > 1 {
		is = "are"
	}
	return strings.Join(shown, ", ") + " " + is + " not encrypted for the people " + recipients.File + " names"
}

// unshowable is an error when an encrypted file of dir has a name that cannot be printed as it is. Whoever can push
// to the store names these files, and what reads them next names them in its errors.
func unshowable(dir string, enc []string) error {
	for _, name := range enc {
		if shown := recipients.Printable(name); shown != name {
			return fmt.Errorf("an encrypted file in %s has a name that cannot be shown (%s). Rename it, then run this again.", dir, shown)
		}
	}
	return nil
}

// storeDiffers are the encrypted files of a store's .ezc that are not encrypted for exactly the keys of its
// registry, sorted. A store with no registry has none: nobody said who its files are for.
func storeDiffers(sys System, dir string) ([]string, error) {
	reg, found, err := recipients.Load(dir)
	if err != nil || !found {
		return nil, err
	}
	enc, err := encryptedFiles(dir)
	if err != nil {
		return nil, err
	}
	if err := unshowable(dir, enc); err != nil {
		return nil, err
	}
	diffs, err := recipients.Differences(dir, reg, backendFor(sys), enc)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(diffs)), nil
}

// policyIsFor reports whether the encryption policy in dir names exactly keys, as a new file there would be
// encrypted for them. No policy is false; a policy that cannot be read is false and its error.
func policyIsFor(b backend.Backend, dir string, keys []string) (bool, error) {
	got, err := b.PolicyRecipients(filepath.Join(dir, "common.enc.yaml"))
	if errors.Is(err, backend.ErrNoPolicy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want := slices.Clone(keys)
	slices.Sort(want)
	return slices.Equal(got, slices.Compact(want)), nil
}

// guardRecipients refuses a write to an encrypted file of the store (base names) that is not encrypted for the
// registry's keys. A file that is not there yet is for guardNewFile.
func (s scope) guardRecipients(sys System, files ...string) error {
	if !s.store {
		return nil
	}
	var enc []string
	for _, file := range files {
		if _, err := os.Stat(filepath.Join(s.dir, file)); err == nil && resolve.IsEncrypted(file) {
			enc = append(enc, file)
		}
	}
	// A write to plaintext files alone asks nothing of the registry, not even that it can be read.
	if len(enc) == 0 {
		return nil
	}
	reg, found, err := recipients.Load(s.dir)
	if err != nil || !found {
		return err
	}
	diffs, err := recipients.Differences(s.dir, reg, backendFor(sys), enc)
	if err != nil {
		return err
	}
	for _, file := range enc {
		if _, differs := diffs[file]; differs {
			return errors.New(notForRegistry(file) + ". Run ezc store rewrap.")
		}
	}
	return nil
}

// guardNewFile refuses to create an encrypted file of the store (a base name) while the policy it would be made
// from names other keys than the registry.
func (s scope) guardNewFile(sys System, file string) error {
	if !s.store {
		return nil
	}
	path := filepath.Join(s.dir, file)
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	reg, found, err := recipients.Load(s.dir)
	if err != nil || !found {
		return err
	}
	want := slices.Clone(reg.Keys())
	slices.Sort(want)
	// A policy this cannot read is not known to be the registry's.
	if got, err := backendFor(sys).PolicyRecipients(path); err != nil || !slices.Equal(got, slices.Compact(want)) {
		return fmt.Errorf("The store's encryption policy does not name the people %s names, so %s would not be encrypted for them. Run ezc store rewrap.", recipients.File, recipients.Printable(file))
	}
	return nil
}

// rewrapped reports whether a file's recipients differ between two versions of it, as a re-wrap leaves them and a
// changed value does not. The versions are read from files in a folder made for that beside state.json and removed again.
func rewrapped(sys System, b backend.Backend, base, local []byte) bool {
	if base == nil || local == nil || bytes.Equal(base, local) {
		return false
	}
	p, err := storePaths(sys)
	if err != nil {
		return false
	}
	dir, err := os.MkdirTemp(filepath.Dir(p.State()), ".compare-*")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	readers := func(name string, data []byte) ([]string, bool) {
		path := filepath.Join(dir, name)
		if os.WriteFile(path, data, 0o600) != nil {
			return nil, false
		}
		keys, err := b.Recipients(path)
		return keys, err == nil
	}
	was, ok1 := readers("base.enc.yaml", base)
	is, ok2 := readers("local.enc.yaml", local)
	return ok1 && ok2 && !slices.Equal(was, is)
}
