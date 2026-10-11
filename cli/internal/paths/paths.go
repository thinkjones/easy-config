// Package paths says where ezc keeps things on this machine: its home, with
// the config and the global store, and the store's location. EZC_HOME and
// EZC_STORE override; otherwise the platform's conventions apply (macOS
// ~/Library, XDG on Linux, LOCALAPPDATA on Windows).
package paths

import (
	"fmt"
	"path/filepath"
)

// Paths is where ezc keeps things on this machine.
type Paths struct {
	Home  string // EZC_HOME
	store string // EZC_STORE, "" for the default
}

// Config is ezc's config file.
func (p Paths) Config() string { return filepath.Join(p.Home, "config.toml") }

// State is ezc's state file, which records what ezc did last (a fetch, say).
func (p Paths) State() string { return filepath.Join(p.Home, "state.json") }

// Store is the global store's root: EZC_STORE, else <Home>/store.
func (p Paths) Store() string {
	if p.store != "" {
		return p.store
	}
	return filepath.Join(p.Home, "store")
}

// StoreEzc is the .ezc directory inside the store, where its files live.
func (p Paths) StoreEzc() string { return filepath.Join(p.Store(), ".ezc") }

// Resolve works out the paths from the environment and the OS (runtime.GOOS).
func Resolve(getenv func(string) string, goos string) (Paths, error) {
	home, err := absOverride(getenv, "EZC_HOME")
	if err != nil {
		return Paths{}, err
	}
	store, err := absOverride(getenv, "EZC_STORE")
	if err != nil {
		return Paths{}, err
	}
	if home != "" {
		return Paths{Home: home, store: store}, nil
	}
	if data := getenv("XDG_DATA_HOME"); data != "" {
		return Paths{Home: filepath.Join(data, "ezc"), store: store}, nil // XDG_DATA_HOME wins on every OS
	}
	user, err := userDir(getenv, goos)
	if err != nil {
		return Paths{}, err
	}
	switch goos {
	case "darwin":
		home = filepath.Join(user, "Library", "ezc")
	case "windows":
		local := getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(user, "AppData", "Local")
		}
		home = filepath.Join(local, "ezc")
	default:
		home = filepath.Join(user, ".local", "share", "ezc")
	}
	return Paths{Home: home, store: store}, nil
}

// absOverride reads an override, which must be absolute.
func absOverride(getenv func(string) string, key string) (string, error) {
	v := getenv(key)
	if v != "" && !filepath.IsAbs(v) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", key, v)
	}
	return v, nil
}

// userDir is the user's home directory, from HOME or on Windows USERPROFILE.
func userDir(getenv func(string) string, goos string) (string, error) {
	user := getenv("HOME")
	if goos == "windows" {
		user = getenv("USERPROFILE")
	}
	if user == "" {
		return "", fmt.Errorf("can't find your home directory: set HOME (or EZC_HOME)")
	}
	return user, nil
}
