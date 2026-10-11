// Package config loads and saves the machine's config.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the machine's config.toml.
type Config struct {
	User      string            `toml:"user"`
	Backend   string            `toml:"backend"`
	KeyFile   string            `toml:"key_file"`
	Store     Store             `toml:"store"`
	Checkouts map[string]string `toml:"checkouts,omitempty"`
}

// Store describes the shared store. An empty Remote is a local-only store.
type Store struct {
	Remote string `toml:"remote,omitempty"`
}

// Load reads path. found is false, with no error, when the file does not exist.
func Load(path string) (c Config, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("%s: cannot read this file", path)
	}
	if err := toml.Unmarshal(data, &c); err != nil {
		return Config{}, false, fmt.Errorf("%s: not valid TOML: %w", path, err)
	}
	if c.KeyFile != "" && !filepath.IsAbs(c.KeyFile) {
		return Config{}, false, fmt.Errorf("%s: key_file must be an absolute path", path)
	}
	return c, true, nil
}

// Save writes c to path atomically, creating the folder.
func Save(path string, c Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("%s: cannot encode the config: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: cannot create the folder", dir)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("%s: cannot write this file", path)
	}
	name := tmp.Name()
	_, werr := tmp.Write(buf.Bytes())
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o644)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		os.Remove(name)
		return fmt.Errorf("%s: cannot write this file", path)
	}
	return nil
}
