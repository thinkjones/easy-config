// Package recipients is the registry of who can decrypt a store: the store's
// .ezc/recipients.yaml.
package recipients

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// File is the registry's file name inside the store's .ezc folder.
const File = "recipients.yaml"

// Recipient is one key that can decrypt the store.
type Recipient struct {
	Key     string   `yaml:"key"`
	Kind    string   `yaml:"kind"` // user | machine | bot
	Owner   string   `yaml:"owner,omitempty"`
	Added   string   `yaml:"added"` // YYYY-MM-DD
	AddedBy string   `yaml:"addedBy"`
	Repos   []string `yaml:"repos,omitempty"`
}

// Registry maps a recipient's name to the recipient.
type Registry struct {
	Recipients map[string]Recipient `yaml:"recipients"`
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidName reports whether s can name a recipient: ^[a-z0-9][a-z0-9-]*$
func ValidName(s string) bool { return nameRE.MatchString(s) }

// Load reads dir/recipients.yaml. found is false, with no error, when it does not exist.
func Load(dir string) (r Registry, found bool, err error) {
	path := filepath.Join(dir, File)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Registry{}, false, nil
	}
	if err != nil {
		return Registry{}, false, fmt.Errorf("%s: cannot read this file", path)
	}
	if err := yaml.Unmarshal(data, &r); err != nil {
		return Registry{}, false, fmt.Errorf("%s: not valid YAML", path)
	}
	if err := r.validate(path); err != nil {
		return Registry{}, false, err
	}
	return r, true, nil
}

func (r Registry) validate(path string) error {
	seen := map[string]string{}
	for _, name := range r.names() {
		e := r.Recipients[name]
		if !ValidName(name) {
			return fmt.Errorf("%s: %q is not a valid recipient name (lower case letters, digits and -)", path, name)
		}
		switch e.Kind {
		case "user", "machine", "bot":
		default:
			return fmt.Errorf("%s: recipient %s: kind must be user, machine or bot", path, name)
		}
		if e.Key == "" {
			return fmt.Errorf("%s: recipient %s: key is empty", path, name)
		}
		if other, dup := seen[e.Key]; dup {
			return fmt.Errorf("%s: recipients %s and %s have the same key", path, other, name)
		}
		seen[e.Key] = name
	}
	return nil
}

// Save writes it atomically, names in sorted order.
func (r Registry) Save(dir string) error {
	if r.Recipients == nil {
		r.Recipients = map[string]Recipient{}
	}
	data, err := yaml.Marshal(r) // yaml.v3 sorts map keys
	if err != nil {
		return fmt.Errorf("%s: cannot encode the registry", File)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: cannot write this file", filepath.Join(dir, File))
	}
	tmp, err := os.CreateTemp(dir, File+".*.tmp")
	if err != nil {
		return fmt.Errorf("%s: cannot write this file", filepath.Join(dir, File))
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("%s: cannot write this file", filepath.Join(dir, File))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%s: cannot write this file", filepath.Join(dir, File))
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("%s: cannot write this file", filepath.Join(dir, File))
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, File)); err != nil {
		return fmt.Errorf("%s: cannot write this file", filepath.Join(dir, File))
	}
	return nil
}

func (r Registry) names() []string {
	names := make([]string, 0, len(r.Recipients))
	for n := range r.Recipients {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Keys is every recipient's key, in order of name.
func (r Registry) Keys() []string {
	var keys []string
	for _, n := range r.names() {
		keys = append(keys, r.Recipients[n].Key)
	}
	return keys
}

// NameOf is the name of the recipient with this key; "" when there is none.
func (r Registry) NameOf(key string) string {
	for _, n := range r.names() {
		if r.Recipients[n].Key == key {
			return n
		}
	}
	return ""
}
