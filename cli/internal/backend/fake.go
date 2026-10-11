package backend

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FakePolicy is the file whose presence in a folder makes Fake.HasPolicy true there.
const FakePolicy = ".fakepolicy"

const (
	fakePrefix = "FAKE["
	fakeSuffix = "]"
)

// Fake is a Backend that needs no tool. It keeps each file on disk in a form that is not plaintext: one
// `NAME: FAKE[<base64 of the value>]` line per name, in insertion order. A line `NAME: plain` is a name
// whose value is not encrypted: Inspect reports it, and everything that reads the file refuses the whole
// file, as a tool that authenticates the file does once a line has been added by hand.
type Fake struct {
	Locked     bool                      // Decrypt, Set, Unset and Edit fail with ErrNoKey
	Missing    bool                      // Check fails with ErrNoTool
	EditFunc   func(pairs []Pair) []Pair // what Edit does; nil leaves the file unchanged
	LastEditor string                    // the editor the last Edit was given
}

// fakeLine is one parsed line of a fake file; enc is false for a value stored without FAKE[ ].
type fakeLine struct {
	name, raw string
	enc       bool
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Check() error {
	if f.Missing {
		return fmt.Errorf("fake: %w", ErrNoTool)
	}
	return nil
}

func (f *Fake) HasPolicy(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, FakePolicy))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("%s: %w", dir, err)
}

func (f *Fake) Inspect(path string) (names, unencrypted []string, err error) {
	lines, err := readFake(path)
	if err != nil {
		return nil, nil, err
	}
	names, unencrypted = []string{}, []string{}
	for _, l := range lines {
		names = append(names, l.name)
		if !l.enc {
			unencrypted = append(unencrypted, l.name)
		}
	}
	return names, unencrypted, nil
}

func (f *Fake) Decrypt(path string) ([]Pair, error) {
	if f.Locked {
		return nil, fmt.Errorf("%s: %w", path, ErrNoKey)
	}
	lines, err := readFake(path)
	if err != nil {
		return nil, err
	}
	return decodeFake(path, lines)
}

func (f *Fake) Set(path, name, value string) error {
	ok, err := f.HasPolicy(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: %w", path, ErrNoPolicy)
	}
	if f.Locked {
		return fmt.Errorf("%s: %w", path, ErrNoKey)
	}
	var pairs []Pair
	if _, statErr := os.Stat(path); statErr == nil {
		if pairs, err = f.Decrypt(path); err != nil {
			return err
		}
	}
	replaced := false
	for i := range pairs {
		if pairs[i].Name == name {
			pairs[i].Value, replaced = value, true
		}
	}
	if !replaced {
		pairs = append(pairs, Pair{Name: name, Value: value})
	}
	return writeFake(path, pairs)
}

func (f *Fake) Unset(path, name string) error {
	if f.Locked {
		return fmt.Errorf("%s: %w", path, ErrNoKey)
	}
	pairs, err := f.Decrypt(path)
	if err != nil {
		return err
	}
	kept := pairs[:0]
	for _, p := range pairs {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	if len(kept) == len(pairs) {
		return fmt.Errorf("%s: %s: %w", path, name, ErrNotSet)
	}
	return writeFake(path, kept)
}

func (f *Fake) Edit(path, editor string) error {
	f.LastEditor = editor
	ok, err := f.HasPolicy(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: %w", path, ErrNoPolicy)
	}
	if f.Locked {
		return fmt.Errorf("%s: %w", path, ErrNoKey)
	}
	var pairs []Pair
	if _, statErr := os.Stat(path); statErr == nil {
		if pairs, err = f.Decrypt(path); err != nil {
			return err
		}
	}
	if f.EditFunc == nil {
		return nil
	}
	return writeFake(path, f.EditFunc(pairs))
}

// readFake parses the file; a file that does not exist is ErrNotSet.
func readFake(path string) ([]fakeLine, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, ErrNotSet)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var lines []fakeLine
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		name, raw, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("%s: not a fake encrypted file", path)
		}
		enc := strings.HasPrefix(raw, fakePrefix) && strings.HasSuffix(raw, fakeSuffix)
		lines = append(lines, fakeLine{name: name, raw: raw, enc: enc})
	}
	return lines, nil
}

func decodeFake(path string, lines []fakeLine) ([]Pair, error) {
	pairs := []Pair{}
	for _, l := range lines {
		if !l.enc {
			return nil, fmt.Errorf("%s: holds a value that is not encrypted, so the file cannot be read", path)
		}
		b, err := base64.StdEncoding.DecodeString(l.raw[len(fakePrefix) : len(l.raw)-len(fakeSuffix)])
		if err != nil {
			return nil, fmt.Errorf("%s: %s: not a fake encrypted value", path, l.name)
		}
		pairs = append(pairs, Pair{Name: l.name, Value: string(b)})
	}
	return pairs, nil
}

// writeFake replaces the file through a temporary file in the same folder and a rename; no pairs deletes it.
func writeFake(path string, pairs []Pair) error {
	if len(pairs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	var sb strings.Builder
	for _, p := range pairs {
		sb.WriteString(p.Name + ": " + fakePrefix + base64.StdEncoding.EncodeToString([]byte(p.Value)) + fakeSuffix + "\n")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fake-*")
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(sb.String())
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("%s: %w", path, werr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
