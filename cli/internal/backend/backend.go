// Package backend is the encryption backend ezc stores secrets with: the Backend interface, the errors it reports, and a Fake that needs no tool.
package backend

import "errors"

// Pair is one name and its value, in the order its file declares it.
type Pair struct{ Name, Value string }

// Backend stores secrets in *.enc.yaml files.
type Backend interface {
	// Name is the value of backend in config.toml.
	Name() string
	// Check reports whether the backend can work on this machine at all (its tool is installed and able).
	Check() error
	// HasPolicy reports whether dir holds the backend's encryption policy, without which nothing can be encrypted there.
	HasPolicy(dir string) (bool, error)
	// Inspect reads an encrypted file without a key: the names it holds, in file order, and
	// those among them whose value is not encrypted.
	Inspect(path string) (names, unencrypted []string, err error)
	// Decrypt returns the file's values in file order.
	Decrypt(path string) ([]Pair, error)
	// Set writes one value, creating the file when it does not exist.
	Set(path, name, value string) error
	// Unset removes one name. Removing the last one deletes the file.
	Unset(path, name string) error
	// Edit opens the decrypted file in editor, a command line, and re-encrypts it on save.
	Edit(path, editor string) error
}

// Every error a backend returns names the file, wraps one of these where it applies, and never contains a value.
var (
	ErrNoTool   = errors.New("the encryption tool is not installed")
	ErrNoPolicy = errors.New("no encryption policy")
	ErrNoKey    = errors.New("cannot decrypt")
	ErrNotSet   = errors.New("not set")
)
