// Package backend is the encryption backend ezc stores secrets with: the Backend interface, the errors it reports, and a Fake that needs no tool.
package backend

import (
	"bytes"
	"errors"
)

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
	// NewKey creates a keypair: the public half, and the private text to write to a key file.
	NewKey() (public string, private []byte, err error)
	// PublicKey reads the public half of the first key in keyFile. A file that is absent or holds no key wraps ErrNoKey.
	PublicKey(keyFile string) (string, error)
	// WritePolicy writes the backend's policy file in dir for these recipients, replacing one that is there.
	// No recipients is an error.
	WritePolicy(dir string, recipients []string) error
	// Recipients returns the public keys the file is encrypted for, sorted. It needs no key and no tool.
	Recipients(path string) ([]string, error)
	// Rewrap re-encrypts the file's data key for the policy in its folder. It needs the key and the tool, and
	// fails with ErrNoKey, ErrNoTool or ErrNoPolicy as Set does.
	Rewrap(path string) error
	// CanDecrypt is nil when that private key alone opens the file. It needs no tool; its error names the
	// file, never the key.
	CanDecrypt(path string, private []byte) error
	// ValidRecipient is nil for a well-formed public key.
	ValidRecipient(key string) error
	// ToolHint is one line saying how to install the backend's tool; "" when it needs none.
	ToolHint(goos string) string
	// BrewFormula is the Homebrew formula that installs the tool; "" when there is none.
	BrewFormula() string
}

// Every error a backend returns names the file, wraps one of these where it applies, and never contains a value.
var (
	ErrNoTool   = errors.New("the encryption tool is not installed")
	ErrNoPolicy = errors.New("no encryption policy")
	ErrNoKey    = errors.New("cannot decrypt")
	ErrNotSet   = errors.New("not set")
)

// SecretLine is the one line of a private key's text that is the key: the first that is not blank and does not start
// with #, ending in a newline. NewKey returns the text of a key file, which may open with comment lines; a secret
// store is given this line alone. It is nil when the text holds no such line.
func SecretLine(private []byte) []byte {
	for _, line := range bytes.Split(private, []byte("\n")) {
		if line = bytes.TrimSpace(line); len(line) > 0 && line[0] != '#' {
			return append(bytes.Clone(line), '\n')
		}
	}
	return nil
}
