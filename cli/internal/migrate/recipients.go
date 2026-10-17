package migrate

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/registry"
)

// addedBy is who the converted entries say added them.
const addedBy = "migrate"

// convert turns the old recipients.yaml into ezc's registry of recipients and the repos its bots list, each
// entry dated today and added by "migrate". purpose human is a user, machine a bot. An error names an entry by
// its position in the file, never by its text: either may be a key pasted in by mistake.
func convert(old []byte, today string, valid func(key string) error) (recipients.Registry, registry.Registry, error) {
	var none recipients.Registry
	var doc yaml.Node
	if err := yaml.Unmarshal(old, &doc); err != nil {
		return none, registry.Registry{}, errors.New("recipients.yaml is not valid YAML")
	}
	entries := recipientsNode(&doc)
	if entries == nil {
		return none, registry.Registry{}, errors.New("recipients.yaml has no recipients map")
	}
	users := recipients.Registry{Recipients: map[string]recipients.Recipient{}}
	repos := registry.Registry{Repos: map[string]registry.Repo{}}
	for i := 0; i+1 < len(entries.Content); i += 2 {
		n := i/2 + 1
		name := entries.Content[i].Value
		if !recipients.ValidName(name) {
			return none, registry.Registry{}, fmt.Errorf("recipient %d: not a valid name (lower case letters, digits and -)", n)
		}
		var e struct {
			Key     string   `yaml:"key"`
			Purpose string   `yaml:"purpose"`
			Repos   []string `yaml:"repos"`
		}
		if err := entries.Content[i+1].Decode(&e); err != nil {
			return none, registry.Registry{}, fmt.Errorf("recipient %d: not a map of key, purpose and repos", n)
		}
		r := recipients.Recipient{Key: e.Key, Added: today, AddedBy: addedBy}
		switch e.Purpose {
		case "human":
			r.Kind = "user"
		case "machine":
			r.Kind = "bot"
		default:
			return none, registry.Registry{}, fmt.Errorf("recipient %d: purpose must be human or machine", n)
		}
		if valid(e.Key) != nil {
			return none, registry.Registry{}, fmt.Errorf("recipient %d: the key is not valid", n)
		}
		for _, id := range e.Repos {
			if !registry.ValidID(id) {
				return none, registry.Registry{}, fmt.Errorf("recipient %d: a repo is not a valid repo ID", n)
			}
		}
		// Only a bot's repos are repos ezc acts on (its key is delivered there); a person's list is dropped.
		if r.Kind == "bot" && len(e.Repos) > 0 {
			r.Repos = e.Repos
			for _, id := range e.Repos {
				repos.Repos[id] = registry.Repo{Added: today, AddedBy: addedBy}
			}
		}
		users.Recipients[name] = r
	}
	if err := loads(users); err != nil {
		return none, registry.Registry{}, err
	}
	return users, repos, nil
}

// recipientsNode is the mapping under the document's top-level recipients key, or nil.
func recipientsNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "recipients" && top.Content[i+1].Kind == yaml.MappingNode {
			return top.Content[i+1]
		}
	}
	return nil
}

// loads proves the registry is one recipients.Load accepts, by saving it to a temporary folder and loading it.
func loads(r recipients.Registry) error {
	dir, err := os.MkdirTemp("", "ezc-recipients-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := r.Save(dir); err != nil {
		return errors.New("recipients.yaml would not load: it cannot be written")
	}
	if _, _, err := recipients.Load(dir); err != nil {
		return fmt.Errorf("recipients.yaml would not load: %s", trimPath(err.Error(), dir))
	}
	return nil
}

// trimPath drops "<dir>/recipients.yaml: " from the front of a recipients.Load error.
func trimPath(msg, dir string) string {
	prefix := dir + string(os.PathSeparator) + recipients.File + ": "
	if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		return msg[len(prefix):]
	}
	return msg
}
