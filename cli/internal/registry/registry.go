// Package registry is the store's list of the repos that use it: the store's
// .ezc/repos.yaml.
package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

// File is the registry's file name inside the store's .ezc folder.
const File = "repos.yaml"

// Repo says when a repo was registered and by whom.
type Repo struct {
	Added   string `yaml:"added"` // YYYY-MM-DD
	AddedBy string `yaml:"addedBy"`
}

// Registry maps a repo's ID to the repo.
type Registry struct {
	Repos map[string]Repo `yaml:"repos"`
}

// idRE is an owner/repo slug, or a single name for a repo that has no GitHub origin. The slug's owner follows
// setup.IsSlug; a repo or a name may start with neither "-" nor "." alone, so "." and ".." never pass.
var idRE = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+|[A-Za-z0-9][A-Za-z0-9._-]*)$`)

const maxID = 100

const errBadID = "not a valid repo ID (owner/repo, or a plain name of letters, digits, . _ and -)"

// ValidID reports whether s can name a repo: owner/repo or a bare name of letters, digits, ".", "_" and "-",
// at most 100 characters, never "." or ".." as a part.
func ValidID(s string) bool {
	if len(s) == 0 || len(s) > maxID || !idRE.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// Load reads dir/repos.yaml. found is false, with no error, when it does not exist. The file is untrusted: an
// invalid entry fails the whole load, and no error quotes an ID.
func Load(dir string) (r Registry, found bool, err error) {
	path := filepath.Join(dir, File)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Registry{}, false, nil
	}
	if err != nil {
		return Registry{}, false, fmt.Errorf("%s: cannot read this file", path)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return Registry{}, false, fmt.Errorf("%s: not valid YAML for a list of repos", path)
	}
	if r.Repos == nil {
		r.Repos = map[string]Repo{}
	}
	for _, id := range r.IDs() {
		if !ValidID(id) {
			return Registry{}, false, fmt.Errorf("%s: an entry has an ID that is not owner/repo or a plain name", path)
		}
		if _, err := time.Parse("2006-01-02", r.Repos[id].Added); err != nil {
			return Registry{}, false, fmt.Errorf("%s: an entry has an added date that is not YYYY-MM-DD", path)
		}
	}
	return r, true, nil
}

// Save writes it atomically, IDs in sorted order, one entry to a line.
func (r Registry) Save(dir string) error {
	path := filepath.Join(dir, File)
	var sb strings.Builder
	if len(r.Repos) == 0 {
		sb.WriteString("repos: {}\n")
	} else {
		sb.WriteString("repos:\n")
		for _, id := range r.IDs() {
			e := r.Repos[id]
			fmt.Fprintf(&sb, "  %s: {added: %s, addedBy: %s}\n", scalar(id), scalar(e.Added), scalar(e.AddedBy))
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	tmp, err := os.CreateTemp(dir, File+".*.tmp")
	if err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(sb.String()); err != nil {
		tmp.Close()
		return fmt.Errorf("%s: cannot write this file", path)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	return nil
}

var (
	plainRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	nullRE  = regexp.MustCompile(`^(?i:null|true|false|yes|no|on|off|y|n)$`)
)

// scalar writes s as it reads best: bare when that is safe, else double-quoted. A JSON string is a YAML
// double-quoted scalar on one line whatever it holds; the encoder's own choice can be a block scalar, which is not
// valid inside a flow mapping.
func scalar(s string) string {
	if plainRE.MatchString(s) && !nullRE.MatchString(s) {
		return s
	}
	out, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(out)
}

// With returns a copy that also holds the repo.
func (r Registry) With(id string, rep Repo) (Registry, error) {
	if !ValidID(id) {
		return Registry{}, errors.New(errBadID)
	}
	if _, ok := r.Repos[id]; ok {
		return Registry{}, fmt.Errorf("%s is already registered", id)
	}
	n := r.clone()
	n.Repos[id] = rep
	return n, nil
}

// Without returns a copy that does not hold the repo.
func (r Registry) Without(id string) (Registry, error) {
	if !ValidID(id) {
		return Registry{}, errors.New(errBadID)
	}
	if _, ok := r.Repos[id]; !ok {
		return Registry{}, fmt.Errorf("%s is not registered", id)
	}
	n := r.clone()
	delete(n.Repos, id)
	return n, nil
}

func (r Registry) clone() Registry {
	n := Registry{Repos: make(map[string]Repo, len(r.Repos)+1)}
	for id, e := range r.Repos {
		n.Repos[id] = e
	}
	return n
}

// IDs is every repo's ID, sorted.
func (r Registry) IDs() []string {
	ids := make([]string, 0, len(r.Repos))
	for id := range r.Repos {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Use says how a repo's manifest treats a variable.
type Use int

const (
	NotUsed  Use = iota // the manifest does not declare the name
	Optional            // declared under optional, with no condition
	Required            // declared under required, or optional with requiredWhen
)

// Uses reports how the manifest declares name.
func Uses(m manifest.Manifest, name string) Use {
	if _, ok := m.Required[name]; ok {
		return Required
	}
	if v, ok := m.Optional[name]; ok {
		if v.RequiredWhen != nil {
			return Required
		}
		return Optional
	}
	return NotUsed
}
