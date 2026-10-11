package resolve

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"gopkg.in/yaml.v3"
)

func isEncrypted(path string) bool { return strings.HasSuffix(path, ".enc.yaml") }

// loadFile reads one values file: plaintext through loadYAML, an *.enc.yaml
// through decrypt. A file that does not exist is not an error; present says so.
func loadFile(path, home string, decrypt func(string) ([]backend.Pair, error)) (pairs []backend.Pair, present bool, err error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}

	if isEncrypted(path) {
		if decrypt == nil {
			return nil, true, fmt.Errorf("%s is encrypted, and this version of ezc cannot decrypt it yet", path)
		}
		pairs, err = decrypt(path)
	} else {
		pairs, err = loadYAML(path)
	}
	if err != nil {
		return nil, true, err
	}

	// Both paths converge here, so a home-relative path means the same thing
	// whether it was committed in plaintext or encrypted. Reference expansion
	// does NOT happen here: it needs the names resolved so far, which only the
	// caller composing the files has.
	for i := range pairs {
		expanded, err := expandHome(pairs[i].Value, pairs[i].Name, path, home)
		if err != nil {
			return nil, true, err
		}
		pairs[i].Value = expanded
	}
	return pairs, true, nil
}

// expandHome resolves a leading ~ or $HOME in a value.
//
// The store is SHARED across machines, so a path in it has to be written
// relative to home to be true on all of them. Nothing downstream can do the
// expansion: the shell emitter single-quotes every value on purpose, so that no
// credential can ever be interpreted as shell, and that same quoting stops
// $HOME too.
//
// Doing it HERE lets both properties hold at once: the value in the file stays
// portable, and the value in the environment stays inert. It also keeps the
// resolver the single source of truth, so every consumer sees the same string.
//
// Only a home prefix, only at the very start. A $ inside a password stays a $.
func expandHome(value, name, path, home string) (string, error) {
	var rest, prefix string
	switch {
	case value == "~", value == "$HOME", value == "${HOME}":
		rest, prefix = "", value
	case strings.HasPrefix(value, "~/"):
		rest, prefix = value[len("~"):], "~"
	case strings.HasPrefix(value, "$HOME/"):
		rest, prefix = value[len("$HOME"):], "$HOME"
	case strings.HasPrefix(value, "${HOME}/"):
		rest, prefix = value[len("${HOME}"):], "${HOME}"
	default:
		// Includes ~user/, which is deliberately left alone: resolving another
		// account's home is not something a config value should be able to ask
		// for, and the shell would not have expanded it inside quotes either.
		return value, nil
	}

	// Needed only when a value asks for it, so a machine with no home directory
	// still loads any store whose values do not.
	if home == "" {
		return "", fmt.Errorf("%s: %s starts with a home reference (%s) but this process has no home directory.\n"+
			"       Set HOME, or write the path out in full", path, name, prefix)
	}
	return home + rest, nil
}

// expandRefs substitutes $NAME and ${NAME} references against the names already
// resolved, so that one value can be composed from another.
//
// Resolving HERE is the only layer that can do it: the emitters single-quote
// every value, which suppresses a reference, and they sort names, so a shell
// would see a reference before the name it points at. Every command reads the
// same composed value, so none of them can disagree about what a variable holds.
//
// Scope is deliberately narrow:
//
//   - Only names ALREADY resolved: an earlier file, or an earlier line in this
//     file. One direction, so a cycle cannot be expressed.
//   - Only plaintext files are substituted INTO. A `$` inside a decrypted token
//     is data and must never be rewritten.
//   - An unknown name is an ERROR. Leaving it literal hides a typo.
//   - `$$` is a literal `$`, for the rare plaintext value that needs one.
//
// HOME is the one name resolvable without being declared, because it is what
// makes the shared store portable in the first place.
func expandRefs(value, name, path, home string, resolved map[string]string) (string, error) {
	if !strings.ContainsRune(value, '$') {
		return value, nil
	}
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '$' {
			b.WriteByte(value[i])
			i++
			continue
		}
		if i+1 < len(value) && value[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		ref, next, ok := parseRef(value, i)
		if !ok {
			// A $ that starts no reference at all, a trailing one or "$ ", is
			// just a dollar sign.
			b.WriteByte('$')
			i++
			continue
		}
		sub, found := lookupRef(ref, home, resolved)
		if !found {
			return "", unknownRefError(ref, name, path, resolved)
		}
		b.WriteString(sub)
		i = next
	}
	return b.String(), nil
}

// parseRef reads the reference beginning at value[i], which is known to be '$'.
func parseRef(value string, i int) (ref string, next int, ok bool) {
	j := i + 1
	braced := j < len(value) && value[j] == '{'
	if braced {
		j++
	}
	start := j
	for j < len(value) && isRefChar(value[j], j == start) {
		j++
	}
	if j == start {
		return "", 0, false
	}
	if braced {
		if j >= len(value) || value[j] != '}' {
			return "", 0, false // "${oops" is not a reference, so not our business
		}
		return value[start:j], j + 1, true
	}
	return value[start:j], j, true
}

// isRefChar matches the shell's own variable-name rule, so that what looks like
// a reference to a reader is one, and what does not, is not.
func isRefChar(c byte, first bool) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return !first
	}
	return false
}

func lookupRef(ref, home string, resolved map[string]string) (string, bool) {
	if v, ok := resolved[ref]; ok {
		return v, true
	}
	if ref == "HOME" && home != "" {
		return home, true
	}
	return "", false
}

// unknownRefError names what is in scope, because the overwhelmingly likely
// cause is a typo or an ordering mistake, and both are answered by the list.
func unknownRefError(ref, name, path string, resolved map[string]string) error {
	available := sortedKeys(resolved)
	scope := "nothing is defined yet at this point in the file"
	if len(available) > 0 {
		scope = "defined so far: " + strings.Join(available, ", ")
	}
	return fmt.Errorf("%s: %s references $%s, which is not defined.\n"+
		"       A value may only use a name set in an earlier file or an earlier line, %s.\n"+
		"       Write $$%s if you meant a literal dollar sign", path, name, ref, scope, ref)
}

// loadYAML reads a plaintext values file.
//
// It walks the yaml.Node tree and takes each scalar's RAW SOURCE TEXT rather
// than unmarshalling into Go types, which is the whole trick. YAML infers type
// from shape, and every inference here is lossy in the same direction: 08540
// becomes the integer 8540, 1.20 becomes the float 1.2, 1e5 becomes 100000.
// Formatting those back into strings cannot recover what the author wrote.
//
// Node.Value is the literal text between the colon and the newline, so the
// value that lands in the environment is the value on the page, whether or not
// anyone remembered to quote it: PORT: 8080 and PORT: "8080" both yield exactly
// "8080", and ZIP: 08540 keeps its zero.
func loadYAML(path string) ([]backend.Pair, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []backend.Pair
	if doc.Kind == 0 {
		return out, nil // empty file, or comments only
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: expected a flat mapping of NAME: value", path)
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		switch {
		case v.Kind == yaml.MappingNode:
			return nil, fmt.Errorf("%s: %q is a nested block; these files are flat key/value only, because they become environment variables", path, k.Value)
		case v.Kind == yaml.SequenceNode:
			return nil, fmt.Errorf("%s: %q is a list; these files are flat key/value only", path, k.Value)
		case v.Kind == yaml.AliasNode:
			return nil, fmt.Errorf("%s: %q is an anchor reference; write the value out, these files are read by more than one language", path, k.Value)
		case v.Tag == "!!null":
			return nil, fmt.Errorf("%s: %q has no value. Write %s: \"\" if you mean the empty string; there are no defaults here, so a blank is always a mistake", path, k.Value, k.Value)
		}
		out = append(out, backend.Pair{Name: k.Value, Value: v.Value})
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
