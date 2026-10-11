package valuefile

import (
	"errors"
	"slices"

	"gopkg.in/yaml.v3"
)

// ChangedNames is the top-level names whose scalar text differs between old and new, or that only one of
// them holds, in new's order and then old's. Entries that are not scalars are ignored. old may be nil.
func ChangedNames(old, new []byte) ([]string, error) {
	was, err := scalars(old)
	if err != nil {
		return nil, err
	}
	is, err := scalars(new)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, e := range is {
		i := slices.IndexFunc(was, func(o entry) bool { return o.name == e.name })
		if i < 0 || was[i] != e {
			changed = append(changed, e.name)
		}
	}
	for _, o := range was {
		if !slices.ContainsFunc(is, func(e entry) bool { return e.name == o.name }) {
			changed = append(changed, o.name)
		}
	}
	return changed, nil
}

// entry is one top-level scalar: its name, its text, and the kind of value the text reads as.
type entry struct{ name, text, tag string }

// scalars is the top-level scalar entries of a file, in its order. The error never quotes the file.
func scalars(src []byte) ([]entry, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, errors.New("not valid YAML")
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping of names to values")
	}
	var entries []entry
	for i := 0; i+1 < len(root.Content); i += 2 {
		if v := root.Content[i+1]; v.Kind == yaml.ScalarNode {
			entries = append(entries, entry{root.Content[i].Value, v.Value, v.ShortTag()})
		}
	}
	return entries, nil
}
