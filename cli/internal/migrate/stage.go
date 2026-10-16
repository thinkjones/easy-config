package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/share"
)

// staged is the new .ezc, built in a temporary folder.
type staged struct {
	dir   string
	files []string // the file names in it, sorted
	notes []string // ! lines to print after the proof
}

// storeRenames are the store's files whose ezc name differs.
var storeRenames = map[string]string{"global.yaml": "common.yaml", "global.enc.yaml": "common.enc.yaml"}

// stageStore builds the store's new .ezc: every file copied (the global files renamed), the manifest's $schema
// replaced, recipients.yaml converted with the repos its bots list in repos.yaml, and the policy written for
// the registry's keys. A store with no recipients.yaml keeps the policy it has.
func stageStore(o Options) (staged, error) {
	s, err := o.stage(storeRenames)
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(filepath.Join(s.dir, recipients.File))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("could not read recipients.yaml (%s).", reason(err))
	}
	users, repos, err := convert(data, o.Now.Format("2006-01-02"), o.Backend.ValidRecipient)
	if err != nil {
		return s, err
	}
	if err := users.Save(s.dir); err != nil {
		return s, err
	}
	if len(repos.Repos) > 0 {
		if err := repos.Save(s.dir); err != nil {
			return s, err
		}
	}
	keys := users.Keys()
	if err := o.Backend.WritePolicy(s.dir, keys); err != nil {
		return s, fmt.Errorf("could not write the encryption policy (%v).", err)
	}
	enc := filepath.Join(s.dir, "common.enc.yaml")
	if _, err := os.Stat(enc); err == nil {
		have, err := o.Backend.Recipients(enc)
		if err != nil || !sameSet(have, keys) {
			s.notes = append(s.notes, "! common.enc.yaml is not encrypted for exactly recipients.yaml's keys; run ezc store rewrap once the store is set up.")
		}
	}
	return s, s.list()
}

// stageRepo builds a repo's new .ezc: every file copied, the manifest's $schema replaced, and the shim. The
// repo's policy file is copied as it is; ezc repo rewrap writes it from the store's registry later.
func stageRepo(o Options) (staged, error) {
	s, err := o.stage(nil)
	if err != nil {
		return s, err
	}
	if err := os.WriteFile(filepath.Join(s.dir, "env.sh"), []byte(share.EnvSh), 0o644); err != nil {
		return s, fmt.Errorf("could not write env.sh (%s).", reason(err))
	}
	return s, s.list()
}

// stage copies every plain file of .fs-config/ into a new temporary folder, renamed by renames, with the
// manifest's $schema replaced. A folder or a link inside is an error: the migration would drop it.
func (o Options) stage(renames map[string]string) (staged, error) {
	var s staged
	old := filepath.Join(o.Dir, OldDir)
	entries, err := os.ReadDir(old)
	if err != nil {
		return s, fmt.Errorf("could not read .fs-config/ (%s).", reason(err))
	}
	if s.dir, err = os.MkdirTemp("", "ezc-migrate-"); err != nil {
		return s, fmt.Errorf("could not make a temporary folder (%s).", reason(err))
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return s, fmt.Errorf("%s in .fs-config/ is not a plain file; move it out and run this again.", recipients.Printable(e.Name()))
		}
		name := e.Name()
		if n, ok := renames[name]; ok {
			name = n
		}
		if err := copyFile(filepath.Join(old, e.Name()), filepath.Join(s.dir, name)); err != nil {
			return s, fmt.Errorf("could not copy .fs-config/%s (%s).", recipients.Printable(e.Name()), reason(err))
		}
	}
	if err := o.replaceSchema(filepath.Join(s.dir, "manifest.yaml")); err != nil {
		return s, err
	}
	return s, s.list()
}

// list fills files from the folder.
func (s *staged) list() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("could not read the temporary folder (%s).", reason(err))
	}
	s.files = s.files[:0]
	for _, e := range entries {
		s.files = append(s.files, e.Name())
	}
	sort.Strings(s.files)
	return nil
}

// replaceSchema points a manifest's top-level $schema line at ezc's schema; a manifest without one is left alone.
func (o Options) replaceSchema(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) || o.SchemaURL == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read manifest.yaml (%s).", reason(err))
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "$schema:") {
			end := ""
			if strings.HasSuffix(l, "\n") {
				end = "\n"
			}
			lines[i] = "$schema: " + o.SchemaURL + end
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		return fmt.Errorf("could not write .ezc/manifest.yaml (%s).", reason(err))
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}
