package sops

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
	"gopkg.in/yaml.v3"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// ageEntry is one `sops.age` entry of an encrypted file: a recipient and the data key wrapped for it.
type ageEntry struct {
	Recipient string `yaml:"recipient"`
	Enc       string `yaml:"enc"`
}

// ageEntries reads the age entries from the file's own YAML, which needs no key and no tool.
func ageEntries(path string) ([]ageEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, backend.ErrNotSet)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var doc struct {
		Sops struct {
			Age []ageEntry `yaml:"age"`
		} `yaml:"sops"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: not a readable encrypted file", path)
	}
	return doc.Sops.Age, nil
}

func (b *sopsBackend) Recipients(path string) ([]string, error) {
	entries, err := ageEntries(path)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for _, e := range entries {
		keys = append(keys, e.Recipient)
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

// Rewrap runs `sops updatekeys`, which opens the file with the key and wraps its data key again for the
// policy in the file's folder.
func (b *sopsBackend) Rewrap(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", path, backend.ErrNotSet)
	}
	if err := b.requirePolicy(path); err != nil {
		return err
	}
	_, stderr, err := b.run(proc.Command{Dir: filepath.Dir(path), Args: []string{"updatekeys", "--yes", filepath.Base(path)}})
	if err != nil {
		return b.failure(path, stderr, err, "write")
	}
	return nil
}

// CanDecrypt unwraps the data key of the entry for private's public half, in-process: sops is not run and no
// error carries the key.
func (b *sopsBackend) CanDecrypt(path string, private []byte) error {
	entries, err := ageEntries(path)
	if err != nil {
		return err
	}
	noKey := fmt.Errorf("%s: that key is not one of its recipients: %w", path, backend.ErrNoKey)
	ids, err := age.ParseIdentities(bytes.NewReader(private))
	if err != nil {
		return noKey
	}
	for _, id := range ids {
		x, ok := id.(*age.X25519Identity)
		if !ok {
			continue
		}
		for _, e := range entries {
			if e.Recipient != x.Recipient().String() {
				continue
			}
			r, err := age.Decrypt(armor.NewReader(strings.NewReader(e.Enc)), x)
			if err == nil {
				if _, err = io.Copy(io.Discard, r); err == nil {
					return nil
				}
			}
		}
	}
	return noKey
}

// ValidKey parses the text as a key file's: at least one identity of the kind NewKey makes.
func (b *sopsBackend) ValidKey(private []byte) error {
	ids, err := age.ParseIdentities(bytes.NewReader(private))
	if err == nil {
		for _, id := range ids {
			if _, ok := id.(*age.X25519Identity); ok {
				return nil
			}
		}
	}
	return errors.New("not a private key")
}

func (b *sopsBackend) ValidRecipient(key string) error {
	if _, err := age.ParseX25519Recipient(key); err != nil {
		return errors.New("not a valid public key")
	}
	return nil
}
