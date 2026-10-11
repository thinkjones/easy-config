// Package manifest is the single source of truth for what an ezc environment
// manifest looks like.
//
// These Go types define the schema. Everything downstream is derived from them:
// the versioned JSON Schema consumers reference from their YAML, the semantic
// validator, and the per-language config generators. Change a type here and
// regenerate — never hand-edit schema/v1/manifest.schema.json.
package manifest

import "fmt"

// SchemaVersion is the version consuming repos pin in their manifest.yaml.
// Bump it only for a BREAKING change to the manifest shape; additive fields do
// not need a bump, because JSON Schema ignores what it does not know and the
// validator treats absent optional fields as unset.
const SchemaVersion = "v1"

// NamePattern is the one rule for a variable's name, in the manifest and in a
// values file: a shell identifier. ValidName applies it.
const NamePattern = "^[A-Za-z_][A-Za-z0-9_]*$"

// ValidName reports whether s matches NamePattern.
func ValidName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

type VarType string

const (
	TypeStr    VarType = "str"
	TypeInt    VarType = "int"
	TypeBool   VarType = "bool"
	TypeSecret VarType = "secret" // a str the generated code must redact
	TypeURL    VarType = "url"
	TypePath   VarType = "path"
	TypeEnum   VarType = "enum"
)

func AllTypes() []VarType {
	return []VarType{TypeStr, TypeInt, TypeBool, TypeSecret, TypeURL, TypePath, TypeEnum}
}

// Condition expresses a cross-field requirement: an optional variable becomes
// required when another variable holds a particular value.
type Condition struct {
	Var    string `json:"var" yaml:"var"`
	Equals string `json:"equals" yaml:"equals"`
}

// Var is one environment variable's declaration. Name is the map key in YAML,
// so it is not a field here.
type Var struct {
	Type VarType `json:"type" yaml:"type"`
	// Prose for humans; the generators emit it as the docstring on the
	// generated field.
	Desc         string     `json:"desc" yaml:"desc"`
	Values       []string   `json:"values,omitempty" yaml:"values,omitempty"`
	Min          *int       `json:"min,omitempty" yaml:"min,omitempty"`
	Max          *int       `json:"max,omitempty" yaml:"max,omitempty"`
	RequiredWhen *Condition `json:"requiredWhen,omitempty" yaml:"requiredWhen,omitempty"`
}

// Manifest is one repo's declaration of what it needs from the environment.
//
// Required and Optional are maps because that is what reads well in YAML and
// what lets JSON Schema constrain the variable NAMES via propertyNames. Map
// order is undefined in Go, so every consumer of these must sort — see
// SortedNames.
type Manifest struct {
	// Schema is the editor's hint, not ours. It carries no meaning for
	// validation, but the loader runs with KnownFields(true), so without a
	// field to land in, the one line that makes an editor validate the file
	// would make ezc reject it.
	Schema        string         `json:"$schema,omitempty" yaml:"$schema,omitempty"`
	SchemaVersion string         `json:"schemaVersion" yaml:"schemaVersion"`
	Required      map[string]Var `json:"required" yaml:"required"`
	Optional      map[string]Var `json:"optional,omitempty" yaml:"optional,omitempty"`
}

// Entry pairs a name with its declaration, for ordered iteration.
type Entry struct {
	Name string
	Var  Var
}

func (m Manifest) All() []Entry {
	out := make([]Entry, 0, len(m.Required)+len(m.Optional))
	for _, n := range SortedNames(m.Required) {
		out = append(out, Entry{n, m.Required[n]})
	}
	for _, n := range SortedNames(m.Optional) {
		out = append(out, Entry{n, m.Optional[n]})
	}
	return out
}

// Lookup finds a variable by name, checking required then optional.
func (m Manifest) Lookup(name string) (Var, bool) {
	if v, ok := m.Required[name]; ok {
		return v, true
	}
	v, ok := m.Optional[name]
	return v, ok
}

func (v Var) IsSecret() bool { return v.Type == TypeSecret }

// GoString is only used in error messages.
func (v Var) String() string { return fmt.Sprintf("%s", v.Type) }
