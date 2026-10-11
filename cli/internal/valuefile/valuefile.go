// Package valuefile edits plaintext values files and manifests in place. Comments and order are kept,
// every write goes through a temporary file in the same folder and a rename, and the new content is read
// back before it replaces the old: a write that fails leaves the file as it was.
package valuefile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// Set writes name: value in a plaintext values file, replacing the name's value or appending it,
// creating the file when absent. Comments and order are kept. The value read back by
// resolve.Plain is exactly value, or Set fails and the file is as it was.
func Set(path, name, value string) error { return set(path, name, value, nil) }

// SetLiteral is Set for a value that must load exactly as given: it writes resolve.Escape(value), and the
// read-back also checks that the resolver turns what was written back into value.
func SetLiteral(path, name, value string) error {
	text, ok := resolve.Escape(value)
	if !ok {
		return fmt.Errorf("%s: cannot store %s in a values file: it starts with ~, which is expanded when the file is loaded", path, name)
	}
	return set(path, name, text, func(read string) bool {
		loaded, ok := resolve.Unescape(read)
		return ok && loaded == value
	})
}

// set is Set; loads, when given, must also accept the text read back for name.
func set(path, name, value string, loads func(read string) bool) error {
	// Plain reads a NUL back, but resolve.Load refuses it: no environment variable can hold one.
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s: cannot store %s in a values file: it contains a NUL byte", path, name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s: cannot store %s in a values file: it is not valid UTF-8", path, name)
	}
	before, present, err := resolve.Plain(path)
	if err != nil {
		return err
	}
	want := withValue(before, name, value)
	for _, style := range []yaml.Style{0, yaml.DoubleQuotedStyle} {
		data, err := encodeSet(path, present, name, value, style)
		if err == nil {
			err = write(path, data, 0o644, func(tmp string) error {
				got, _, err := resolve.Plain(tmp)
				if err != nil || !slices.Equal(got, want) {
					return errors.New("read back differently")
				}
				if loads != nil && !slices.ContainsFunc(got, func(p backend.Pair) bool { return p.Name == name && loads(p.Value) }) {
					return errors.New("loads differently")
				}
				return nil
			})
		}
		if !errors.Is(err, errVerify) {
			return err
		}
	}
	return fmt.Errorf("%s: cannot store %s in a values file: it would not read back unchanged", path, name)
}

// withValue is pairs with name set to value, appended when it was not there.
func withValue(pairs []backend.Pair, name, value string) []backend.Pair {
	out := slices.Clone(pairs)
	for i := range out {
		if out[i].Name == name {
			out[i].Value = value
			return out
		}
	}
	return append(out, backend.Pair{Name: name, Value: value})
}

// encodeSet is the file's new content, with the value's scalar given style (0 lets the encoder choose).
func encodeSet(path string, present bool, name, value string, style yaml.Style) ([]byte, error) {
	var old []byte
	if present {
		var err error
		if old, err = os.ReadFile(path); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	doc, ok := parse(old)
	if !ok {
		// No YAML document: absent, empty, or only comments. Keep what is there and add the line.
		line, err := encode(&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{keyNode(name), strNode(value, style)}})
		if err != nil {
			return nil, err
		}
		if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
			old = append(old, '\n')
		}
		return append(old, line...), nil
	}
	root := doc.Content[0]
	root.Style &^= yaml.FlowStyle
	if i := keyIndex(root, name); i >= 0 {
		v := root.Content[i+1]
		n := strNode(value, style)
		n.HeadComment, n.LineComment, n.FootComment = v.HeadComment, v.LineComment, v.FootComment
		root.Content[i+1] = n
	} else {
		root.Content = append(root.Content, keyNode(name), strNode(value, style))
	}
	return encode(doc)
}

// Unset removes name. It reports whether the name was there. A file left with no names is deleted.
func Unset(path, name string) (removed bool, err error) {
	before, present, err := resolve.Plain(path)
	if err != nil || !present {
		return false, err
	}
	if !slices.ContainsFunc(before, func(p backend.Pair) bool { return p.Name == name }) {
		return false, nil
	}
	if len(before) == 1 {
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		return true, nil
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	doc, ok := parse(old)
	if !ok {
		return false, fmt.Errorf("%s: cannot edit this file", path)
	}
	root := doc.Content[0]
	i := keyIndex(root, name)
	if i == 0 && root.Content[0].HeadComment != "" {
		// A header written directly above the first key is that key's head comment; the next key inherits it.
		next := root.Content[2]
		next.HeadComment = strings.TrimSuffix(root.Content[0].HeadComment+"\n"+next.HeadComment, "\n")
	}
	root.Content = slices.Delete(root.Content, i, i+2)
	data, err := encode(doc)
	if err != nil {
		return false, err
	}
	want := slices.DeleteFunc(slices.Clone(before), func(p backend.Pair) bool { return p.Name == name })
	err = write(path, data, 0o644, func(tmp string) error {
		got, _, err := resolve.Plain(tmp)
		if err != nil || !slices.Equal(got, want) {
			return errors.New("read back differently")
		}
		return nil
	})
	if errors.Is(err, errVerify) {
		return false, fmt.Errorf("%s: cannot remove %s: the file would not read back as intended", path, name)
	}
	return err == nil, err
}

// Declare adds name to the manifest's required (or optional) map. It fails if the name is declared.
func Declare(manifestPath, name string, v manifest.Var, optional bool) error {
	doc, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	root := doc.Content[0]
	for _, table := range []string{"required", "optional"} {
		if m := tableIn(root, table); m != nil && keyIndex(m, name) >= 0 {
			return fmt.Errorf("%s is already declared in %s", name, manifestPath)
		}
	}
	table := "required"
	if optional {
		table = "optional"
	}
	m := tableIn(root, table)
	if i := keyIndex(root, table); m == nil && i >= 0 {
		old := root.Content[i+1]
		if old.Kind != yaml.ScalarNode || old.Tag != "!!null" {
			return fmt.Errorf("%s: %s is not a mapping of names, so %s cannot be added to it", manifestPath, table, name)
		}
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: old.HeadComment, LineComment: old.LineComment, FootComment: old.FootComment}
		root.Content[i+1] = m
	} else if m == nil {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, keyNode(table), m)
	}
	m.Style &^= yaml.FlowStyle
	var vn yaml.Node
	if err := vn.Encode(v); err != nil {
		return err
	}
	m.Content = append(m.Content, keyNode(name), &vn)
	return writeManifest(manifestPath, doc, func(got manifest.Manifest) error {
		declared := got.Required
		if optional {
			declared = got.Optional
		}
		if _, ok := declared[name]; !ok {
			return errors.New("not declared")
		}
		return nil
	})
}

// Replace puts data at path through a temporary file and a rename, keeping the file's mode. It restores a file whose
// bytes were read earlier.
func Replace(path string, data []byte) error {
	return write(path, data, 0o644, func(string) error { return nil })
}

// Undeclare removes name from the manifest. It reports whether it was there.
func Undeclare(manifestPath, name string) (removed bool, err error) {
	doc, err := readManifest(manifestPath)
	if err != nil {
		return false, err
	}
	root := doc.Content[0]
	for _, table := range []string{"required", "optional"} {
		m := tableIn(root, table)
		if m == nil {
			continue
		}
		i := keyIndex(m, name)
		if i < 0 {
			continue
		}
		m.Content = slices.Delete(m.Content, i, i+2)
		if len(m.Content) == 0 {
			if table == "required" {
				m.Style = yaml.FlowStyle
			} else {
				j := keyIndex(root, table)
				root.Content = slices.Delete(root.Content, j, j+2)
			}
		}
		err = writeManifest(manifestPath, doc, func(got manifest.Manifest) error {
			if _, ok := got.Lookup(name); ok {
				return errors.New("still declared")
			}
			return nil
		})
		return err == nil, err
	}
	return false, nil
}

// tableIn is the mapping under key in root, or nil.
func tableIn(root *yaml.Node, key string) *yaml.Node {
	if i := keyIndex(root, key); i >= 0 && root.Content[i+1].Kind == yaml.MappingNode {
		return root.Content[i+1]
	}
	return nil
}

func readManifest(path string) (*yaml.Node, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	doc, ok := parse(old)
	if !ok || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: not a manifest", path)
	}
	return doc, nil
}

// writeManifest replaces the manifest with doc if the result loads, validates and passes check.
func writeManifest(path string, doc *yaml.Node, check func(manifest.Manifest) error) error {
	data, err := encode(doc)
	if err != nil {
		return err
	}
	err = write(path, data, 0o644, func(tmp string) error {
		m, err := manifest.Load(tmp)
		if err != nil {
			return errors.New("it does not parse")
		}
		if errs := m.Validate(); len(errs) > 0 {
			return errs[0]
		}
		return check(m)
	})
	var ve *verifyError
	if errors.As(err, &ve) {
		return fmt.Errorf("%s: the edited manifest would not be valid: %w", path, ve.cause)
	}
	return err
}

// pathErr is err with the operating system's own text only: a *fs.PathError names the temporary file.
func pathErr(path string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return fmt.Errorf("%s: %v", path, err)
}

// errVerify marks a write abandoned because the new content did not read back as intended.
var errVerify = errors.New("verification failed")

// verifyError is errVerify with the reason the content was refused.
type verifyError struct{ cause error }

func (e *verifyError) Error() string        { return "verification failed: " + e.cause.Error() }
func (e *verifyError) Is(target error) bool { return target == errVerify }

// write puts data at path through a temporary file next to it, which verify reads before the rename.
// An existing file keeps its mode; a new one gets newMode.
func write(path string, data []byte, newMode fs.FileMode, verify func(tmp string) error) error {
	mode := newMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return pathErr(path, err)
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			os.Remove(name)
		}
	}()
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr != nil {
		return pathErr(path, werr)
	}
	if err := verify(name); err != nil {
		return &verifyError{err}
	}
	if err := os.Rename(name, path); err != nil {
		return pathErr(path, err)
	}
	done = true
	return nil
}

// parse reads a document with a mapping at its root; ok is false when there is none.
func parse(src []byte) (*yaml.Node, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false
	}
	return &doc, true
}

// encode renders n with a 2-space indent.
func encode(n *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func keyNode(name string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
}

// strNode is a string scalar. Tagged !!str without the tagged style, the encoder quotes it
// when plain text would read back as something else, and writes no tag.
func strNode(value string, style yaml.Style) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: style}
}
