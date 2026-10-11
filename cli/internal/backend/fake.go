package backend

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// FakePolicy is the file whose presence in a folder makes Fake.HasPolicy true there.
const FakePolicy = ".fakepolicy"

const (
	fakePrefix = "FAKE["
	fakeSuffix = "]"

	// fakeRecipients opens a fake file: the public keys it is encrypted for, sorted and comma separated.
	fakeRecipients = "# recipients: "
)

// Fake is a Backend that needs no tool. It keeps each file on disk in a form that is not plaintext: one
// `NAME: FAKE[<base64 of the value>]` line per name, in insertion order. A line `NAME: plain` is a name
// whose value is not encrypted: Inspect reports it, and everything that reads the file refuses the whole
// file, as a tool that authenticates the file does once a line has been added by hand. A first line
// `# recipients: k1,k2` says whom the file is encrypted for; a file without it has no recipients.
type Fake struct {
	Locked     bool                      // Decrypt, Set, Unset (of any name but a file's last) and Edit fail with ErrNoKey
	Missing    bool                      // Check fails with ErrNoTool
	EditFunc   func(pairs []Pair) []Pair // what Edit does; nil leaves the file unchanged
	LastEditor string                    // the editor the last Edit was given
	RewrapFail string                    // Rewrap of a file with this base name fails
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
	recipients, err := f.policyRecipients(filepath.Dir(path))
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if pairs, err = f.Decrypt(path); err != nil {
			return err
		}
		if recipients, err = fakeFileRecipients(path); err != nil {
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
	return writeFake(path, recipients, pairs)
}

func (f *Fake) Unset(path, name string) error {
	lines, err := readFake(path)
	if err != nil {
		return err
	}
	// The last name goes with its file, which needs no key.
	if len(lines) == 1 && lines[0].name == name {
		return writeFake(path, nil, nil)
	}
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
	recipients, err := fakeFileRecipients(path)
	if err != nil {
		return err
	}
	return writeFake(path, recipients, kept)
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
	recipients, err := f.policyRecipients(filepath.Dir(path))
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if pairs, err = f.Decrypt(path); err != nil {
			return err
		}
		if recipients, err = fakeFileRecipients(path); err != nil {
			return err
		}
	}
	if f.EditFunc == nil {
		return nil
	}
	return writeFake(path, recipients, f.EditFunc(pairs))
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
		if line == "" || strings.HasPrefix(line, fakeRecipients) {
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
func writeFake(path string, recipients []string, pairs []Pair) error {
	if len(pairs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	var sb strings.Builder
	sb.WriteString(fakeRecipients + strings.Join(recipients, ",") + "\n")
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

const (
	fakePublicPrefix  = "fake1"
	fakePrivatePrefix = "FAKE-KEY-"
)

func (f *Fake) NewKey() (string, []byte, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	h := hex.EncodeToString(b)
	return fakePublicPrefix + h, []byte(fakePrivatePrefix + h + "\n"), nil
}

func (f *Fake) PublicKey(keyFile string) (string, error) {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return "", fmt.Errorf("%s: no key file: %w", keyFile, ErrNoKey)
	}
	if public, ok := fakePublicOf(data); ok {
		return public, nil
	}
	return "", fmt.Errorf("%s: holds no key: %w", keyFile, ErrNoKey)
}

// fakePublicOf derives the public key from the first private key in text.
func fakePublicOf(text []byte) (string, bool) {
	for _, line := range strings.Split(string(text), "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), fakePrivatePrefix); ok {
			return fakePublicPrefix + h, true
		}
	}
	return "", false
}

// policyRecipients is the sorted, de-duplicated keys of the policy in dir; none when the policy is empty.
func (f *Fake) policyRecipients(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, FakePolicy))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			keys = append(keys, line)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

// fakeFileRecipients reads the recipients line of a file that exists.
func fakeFileRecipients(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, ErrNotSet)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	first, _, _ := strings.Cut(string(data), "\n")
	list, ok := strings.CutPrefix(first, fakeRecipients)
	if !ok || list == "" {
		return []string{}, nil
	}
	keys := strings.Split(list, ",")
	slices.Sort(keys)
	return keys, nil
}

func (f *Fake) Recipients(path string) ([]string, error) { return fakeFileRecipients(path) }

func (f *Fake) Rewrap(path string) error {
	if _, err := fakeFileRecipients(path); err != nil {
		return err
	}
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
	if f.RewrapFail != "" && f.RewrapFail == filepath.Base(path) {
		return fmt.Errorf("%s: could not re-wrap this file", path)
	}
	pairs, err := f.Decrypt(path)
	if err != nil {
		return err
	}
	recipients, err := f.policyRecipients(filepath.Dir(path))
	if err != nil {
		return err
	}
	return writeFake(path, recipients, pairs)
}

func (f *Fake) CanDecrypt(path string, private []byte) error {
	recipients, err := fakeFileRecipients(path)
	if err != nil {
		return err
	}
	if public, ok := fakePublicOf(private); ok && slices.Contains(recipients, public) {
		return nil
	}
	return fmt.Errorf("%s: that key is not one of its recipients: %w", path, ErrNoKey)
}

func (f *Fake) ValidKey(private []byte) error {
	if public, ok := fakePublicOf(private); !ok || f.ValidRecipient(public) != nil {
		return errors.New("not a private key")
	}
	return nil
}

func (f *Fake) ValidRecipient(key string) error {
	h, ok := strings.CutPrefix(key, fakePublicPrefix)
	if _, err := hex.DecodeString(h); !ok || h == "" || err != nil {
		return errors.New("not a valid public key")
	}
	return nil
}

func (f *Fake) WritePolicy(dir string, recipients []string) error {
	if len(recipients) == 0 {
		return fmt.Errorf("%s: no recipients to write a policy for", dir)
	}
	return writeFileAtomic(filepath.Join(dir, FakePolicy), []byte(strings.Join(recipients, "\n")+"\n"))
}

func (f *Fake) ToolHint(goos string) string { return "Install the fake tool." }

func (f *Fake) BrewFormula() string { return "fake-tool" }

// writeFileAtomic replaces the file through a temporary file in the same folder and a rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fake-*")
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644)
	}
	if werr != nil {
		return fmt.Errorf("%s: %w", path, werr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
