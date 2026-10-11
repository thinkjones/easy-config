package recipients

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DueFile is the store's list of values to rotate, inside .ezc.
const DueFile = "rotation-due.yaml"

// DueEntry says who was removed while the value was readable to them, and when.
type DueEntry struct {
	Removed string `yaml:"removed"`
	On      string `yaml:"on"`
}

// Due maps "<file>/<NAME>" to why that value should be rotated.
type Due map[string]DueEntry

// LoadDue reads dir/rotation-due.yaml; a missing file is an empty Due.
func LoadDue(dir string) (Due, error) {
	path := filepath.Join(dir, DueFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Due{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read this file", path)
	}
	d := Due{}
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%s: not valid YAML", path)
	}
	if d == nil {
		d = Due{}
	}
	return d, nil
}

// Save writes the file with sorted keys, or removes it when there is nothing due.
func (d Due) Save(dir string) error {
	path := filepath.Join(dir, DueFile)
	if len(d) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s: cannot remove this file", path)
		}
		return nil
	}
	var keys []string
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s: {removed: %s, on: %s}\n", scalar(k), scalar(d[k].Removed), scalar(d[k].On))
	}
	data := []byte(sb.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	return nil
}

// Add records a value as due, keeping the entry it already has.
func (d Due) Add(file, name, removed, on string) {
	k := file + "/" + name
	if _, ok := d[k]; !ok {
		d[k] = DueEntry{Removed: removed, On: on}
	}
}

// Clear drops the entry and reports whether there was one.
func (d Due) Clear(file, name string) bool {
	k := file + "/" + name
	_, ok := d[k]
	delete(d, k)
	return ok
}

var nullRE = regexp.MustCompile(`^(?i:null|true|false|yes|no|on|off|y|n)$`)

var plainRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// scalar writes s as it reads best: bare when that is safe, else quoted by the encoder.
func scalar(s string) string {
	if plainRE.MatchString(s) && !nullRE.MatchString(s) {
		return s
	}
	out, err := yaml.Marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimSpace(string(out))
}
