// Package sops is the Backend that encrypts with the sops program and age keys. Nothing else in ezc knows sops exists.
package sops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// PolicyFile is the file whose presence in a folder says what may be encrypted there and for whom.
const PolicyFile = ".sops.yaml"

const (
	envKey     = "SOPS_AGE_KEY"
	envKeyFile = "SOPS_AGE_KEY_FILE"
	envEditor  = "SOPS_EDITOR" // wins over EDITOR and VISUAL; split on spaces

	// noRuleMarker is what sops prints when the policy has no creation rule whose pattern matches the file.
	noRuleMarker = "no matching creation rules found"

	// noKeyMarker is what sops prints on stderr when no key it can find opens the file: the key file holds a
	// key that is not a recipient, does not exist, or is empty or unreadable. A corrupt file or a bad MAC prints
	// something else, and must not be mistaken for a missing key.
	noKeyMarker = "Failed to get the data key"
)

// DefaultKeyFile is the key file under home.
func DefaultKeyFile(home string) string {
	return filepath.Join(home, ".config", "sops", "age", "keys.txt")
}

// Options is everything the backend needs from the outside world.
type Options struct {
	Run      proc.Runner
	LookPath func(string) (string, error)
	Environ  []string // the process environment, K=V; the whole environment sops gets (nil gives it only the key file)
	KeyFile  string   // used unless Environ already sets SOPS_AGE_KEY or SOPS_AGE_KEY_FILE
}

// New returns the sops backend.
func New(o Options) backend.Backend { return &sopsBackend{o: o} }

type sopsBackend struct{ o Options }

func (b *sopsBackend) Name() string { return "sops" }

// kindError is an error with its own message that still matches one of the backend's error values.
type kindError struct {
	msg  string
	kind error
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }

// requireTool fails with ErrNoTool when sops is not installed.
func (b *sopsBackend) requireTool() error {
	if _, err := b.o.LookPath("sops"); err != nil {
		return &kindError{"sops is not installed. Install it (brew install sops, or https://github.com/getsops/sops/releases) and run `ezc doctor`.", backend.ErrNoTool}
	}
	return nil
}

func (b *sopsBackend) Check() error {
	if err := b.requireTool(); err != nil {
		return err
	}
	stdout, stderr, _ := b.o.Run(proc.Command{Env: b.env(), Name: "sops", Args: []string{"set", "--help"}})
	if !bytes.Contains(stdout, []byte("--value-stdin")) && !bytes.Contains(stderr, []byte("--value-stdin")) {
		return errors.New("this sops is too old for ezc (it lacks `sops set --value-stdin`); upgrade it")
	}
	return nil
}

func (b *sopsBackend) HasPolicy(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, PolicyFile))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("%s: %w", dir, err)
}

// Inspect reads the file as plain YAML: sops keeps names in the clear and only encrypts values, so no key is needed.
// An empty value is not reported as unencrypted; sops leaves it as `NAME: ""`, and there is nothing in it to leak.
func (b *sopsBackend) Inspect(path string) (names, unencrypted []string, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("%s: %w", path, backend.ErrNotSet)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("%s: not a readable encrypted file", path)
	}
	names, unencrypted = []string{}, []string{}
	if len(doc.Content) == 0 {
		return names, unencrypted, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s: expected a flat mapping of NAME: value", path)
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value == "sops" {
			continue
		}
		names = append(names, k.Value)
		if hasPlainValue(v) {
			unencrypted = append(unencrypted, k.Value)
		}
	}
	return names, unencrypted, nil
}

// hasPlainValue reports whether the node holds a non-empty scalar that sops did not encrypt.
func hasPlainValue(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Tag != "!!null" && n.Value != "" && !strings.HasPrefix(n.Value, "ENC[")
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			if hasPlainValue(n.Content[i]) {
				return true
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if hasPlainValue(c) {
				return true
			}
		}
	}
	return false
}

func (b *sopsBackend) Decrypt(path string) ([]backend.Pair, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, backend.ErrNotSet)
	}
	stdout, stderr, err := b.run(proc.Command{Dir: filepath.Dir(path), Args: []string{"decrypt", "--output-type", "json", filepath.Base(path)}})
	if err != nil {
		return nil, b.failure(path, stderr, err, "read")
	}
	return flatten(path, stdout)
}

// flatten reads the JSON sops decrypted into pairs, in the order the file has them. The reading is a token
// stream because a map would lose the order. Only strings are accepted: sops records the type its parser
// inferred when the file was encrypted, so `ZIP: 08540` is stored as a number and decrypts to 8540 with the
// original text gone. That cannot be undone here, and a number in a file whose values become environment
// variables is an accident every time, so it is rejected rather than guessed at. Nesting is rejected because a
// block has no single value to join into. No error carries the value.
func flatten(path string, data []byte) ([]backend.Pair, error) {
	unreadable := fmt.Errorf("%s: sops returned output ezc cannot read", path)
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, unreadable
	}
	pairs := []backend.Pair{}
	for dec.More() {
		keyTok, err := dec.Token()
		name, ok := keyTok.(string)
		if err != nil || !ok {
			return nil, unreadable
		}
		valTok, err := dec.Token()
		if err != nil {
			return nil, unreadable
		}
		switch v := valTok.(type) {
		case string:
			pairs = append(pairs, backend.Pair{Name: name, Value: v})
		case json.Delim:
			return nil, fmt.Errorf("%s: %s is not a single value", path, name)
		default:
			return nil, fmt.Errorf("%s: %s is not a string; it was written unquoted and has lost its exact text. Set it again with `ezc set %s`.", path, name, name)
		}
	}
	return pairs, nil
}

// Set changes a value in place with `sops set`, which writes the file only after it has decrypted, changed and
// re-encrypted it; observed with a wrong key, it leaves the file byte for byte as it was. A file that does not
// exist is made with `sops encrypt`, whose output goes to a temporary file that is renamed into place.
func (b *sopsBackend) Set(path, name, value string) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	if name == "sops" {
		return fmt.Errorf("%s: sops is reserved by the file format and cannot be a name.", path)
	}
	// json.Marshal would swap each invalid byte for U+FFFD without an error, storing a different value.
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s: %s is not valid UTF-8 text and cannot be stored.", path, name)
	}
	_, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", path, statErr)
	}
	if statErr != nil {
		return b.create(path, name, value)
	}
	val := mustJSON(value)
	index := indexOf(name)
	_, stderr, err := b.run(proc.Command{Dir: dir, Stdin: []byte(val), Args: []string{"set", "--value-stdin", base, index}})
	if err != nil {
		return b.failure(path, stderr, err, "write")
	}
	return nil
}

// create encrypts a one-name file. The plaintext JSON goes to sops on stdin, so it is on no disk and in no
// argument; /dev/stdin as the input path makes this Unix-only, which is accepted for this part.
func (b *sopsBackend) create(path, name, value string) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	if err := b.requirePolicy(path); err != nil {
		return err
	}
	plain, _ := json.Marshal(map[string]string{name: value}) // cannot fail for strings
	stdout, stderr, err := b.run(proc.Command{
		Dir:   dir,
		Stdin: plain,
		Args:  []string{"encrypt", "--filename-override", base, "--input-type", "json", "--output-type", "yaml", "/dev/stdin"},
	})
	if err != nil {
		if bytes.Contains(stderr, []byte(noRuleMarker)) {
			return &kindError{fmt.Sprintf("%s has no rule that matches %s.", filepath.Join(dir, PolicyFile), base), backend.ErrNoPolicy}
		}
		return b.failure(path, stderr, err, "write")
	}
	if len(stdout) == 0 {
		return fmt.Errorf("%s: sops produced no output", path)
	}
	return writeFileAtomic(path, stdout)
}

// requirePolicy fails with ErrNoPolicy when the file's folder has no policy.
func (b *sopsBackend) requirePolicy(path string) error {
	dir := filepath.Dir(path)
	ok, err := b.HasPolicy(dir)
	if err != nil {
		return err
	}
	if !ok {
		return &kindError{fmt.Sprintf("%s: %s has no encryption policy (%s), so nothing can be encrypted there.", path, dir, PolicyFile), backend.ErrNoPolicy}
	}
	return nil
}

func (b *sopsBackend) Unset(path, name string) error {
	names, _, err := b.Inspect(path)
	if err != nil {
		return err
	}
	if !slices.Contains(names, name) {
		return fmt.Errorf("%s: %s: %w", path, name, backend.ErrNotSet)
	}
	// The last name is removed without a key: the file is gone either way, and the key proves nothing about who may delete it.
	if len(names) == 1 {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	index := indexOf(name)
	_, stderr, err := b.run(proc.Command{Dir: filepath.Dir(path), Args: []string{"unset", filepath.Base(path), index}})
	if err != nil {
		return b.failure(path, stderr, err, "write")
	}
	return nil
}

// Edit hands the terminal to `sops edit`, which runs editor (SOPS_EDITOR replaces any in the environment). A file that does not exist is created by it, which needs the policy.
func (b *sopsBackend) Edit(path, editor string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := b.requirePolicy(path); err != nil {
			return err
		}
	}
	c := proc.Command{Dir: filepath.Dir(path), Interactive: true, Args: []string{"edit", filepath.Base(path)}}
	c.Env = b.env(envEditor + "=" + editor)
	c.Name = "sops"
	if err := b.requireTool(); err != nil {
		return err
	}
	_, _, err := b.o.Run(c)
	if err != nil {
		return fmt.Errorf("%s: sops edit failed: %w", path, err)
	}
	return nil
}

// indexOf is sops's index argument for a top-level name: a JSON array holding the name.
func indexOf(name string) string {
	b, _ := json.Marshal([]string{name}) // cannot fail for strings
	return string(b)
}

// run runs sops with the key file in its environment. With no sops installed it runs nothing and fails with ErrNoTool.
func (b *sopsBackend) run(c proc.Command) (stdout, stderr []byte, err error) {
	if err := b.requireTool(); err != nil {
		return nil, nil, err
	}
	c.Name = "sops"
	c.Env = b.env()
	return b.o.Run(c)
}

// env is the environment plus the key file, unless the environment already says where the key is.
// extra entries replace any entry of the same name.
func (b *sopsBackend) env(extra ...string) []string {
	var env []string
	for _, kv := range b.o.Environ {
		if slices.ContainsFunc(extra, func(e string) bool { return strings.HasPrefix(kv, e[:strings.Index(e, "=")+1]) }) {
			continue
		}
		if kv == envKey+"=" || kv == envKeyFile+"=" { // sops ignores an empty value; so does the check below
			continue
		}
		env = append(env, kv)
	}
	if b.o.Environ != nil && env == nil {
		env = []string{}
	}
	if b.keyFromEnv() == "" && b.o.KeyFile != "" {
		env = append(env, envKeyFile+"="+b.o.KeyFile)
	}
	return append(env, extra...)
}

// keyFromEnv names the variable the environment supplies the key through, or "" when it supplies none.
func (b *sopsBackend) keyFromEnv() string {
	found := ""
	for _, kv := range b.o.Environ {
		switch {
		case strings.HasPrefix(kv, envKey+"=") && kv != envKey+"=":
			return envKey
		case strings.HasPrefix(kv, envKeyFile+"=") && kv != envKeyFile+"=":
			found = envKeyFile
		}
	}
	return found
}

// failure turns a failed sops run into an error. sops's own text is never passed on: its messages can quote
// a decrypted value (a value whose recorded type was edited makes it print the text it failed to convert), and
// a file can be edited by someone with no key. The failure is classified by what stderr says and answered
// with a fixed sentence. verb is "read" or "write". A run that never started for want of the tool says so.
func (b *sopsBackend) failure(path string, stderr []byte, err error, verb string) error {
	if errors.Is(err, backend.ErrNoTool) {
		return err
	}
	if isNoKey(stderr) {
		source := "Your key file is " + b.o.KeyFile + "."
		if v := b.keyFromEnv(); v != "" {
			source = "The key comes from " + v + "."
		}
		return &kindError{fmt.Sprintf("cannot decrypt %s: no key on this machine is one of its recipients. %s", path, source), backend.ErrNoKey}
	}
	return fmt.Errorf("%s: sops could not %s this file. To see its own message, run `sops decrypt %s` in %s.", path, verb, filepath.Base(path), filepath.Dir(path))
}

// noIdentityPhrases are what sops prints, in its detail lines, when the age library found no identity that opens
// the file: the key file holds a key that is not a recipient, does not exist, or is empty or unreadable. Only
// these count as a missing key. sops prints the same first line when the file is damaged (a bad header, bad
// armour, a failed payload check), so the first line alone proves nothing.
var noIdentityPhrases = []string{
	"identity did not match any of the recipients",
	"no identity matched any of the recipients",
	"failed to load age identities",
}

// isNoKey reports whether stderr says no key opened the file. sops wraps its detail lines in a `|` box, so
// the text is flattened to single spaces before matching.
func isNoKey(stderr []byte) bool {
	if !bytes.Contains(stderr, []byte(noKeyMarker)) {
		return false
	}
	flat := strings.Join(strings.Fields(strings.ReplaceAll(string(stderr), "|", " ")), " ")
	for _, p := range noIdentityPhrases {
		if strings.Contains(flat, p) {
			return true
		}
	}
	return false
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s) // cannot fail for strings
	return string(b)
}

// writeFileAtomic writes data through a temporary file in the same folder and a rename, so the file is either
// absent or complete. The data is ciphertext.
func writeFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ezc-*")
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	_, werr := io.Copy(tmp, bytes.NewReader(data))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("%s: %w", path, werr)
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
