package manifest

import (
	"fmt"
	"slices"
	"strings"
)

// Validate applies the rules JSON Schema cannot express.
//
// The schema catches shape: unknown fields, a bad type name, an enum with no
// values, min on a string. These are the CROSS-FIELD rules, which need the
// whole document at once. Rule 4 in particular is the one no declarative schema
// gets: it checks that a requiredWhen points at a value the target enum
// actually permits, so `equals: "flie"` is caught rather than shipped.
func (m Manifest) Validate() []error {
	var errs []error

	if m.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Errorf("schemaVersion %q — this ezc understands %q", m.SchemaVersion, SchemaVersion))
	}

	// 1. A name in both tables is a contradiction. Maps make within-table
	//    duplicates impossible, so this only has to check across.
	for name := range m.Optional {
		if _, dup := m.Required[name]; dup {
			errs = append(errs, fmt.Errorf("%s: declared both required and optional", name))
		}
	}

	byName := map[string]Var{}
	for _, e := range m.All() {
		byName[e.Name] = e.Var
	}

	for _, e := range m.All() {
		v := e.Var
		// The name is written unquoted into shell and generated code, and a
		// values file can only supply a name that passes the same rule.
		if !ValidName(e.Name) {
			errs = append(errs, fmt.Errorf("%q: not a valid variable name; use letters, digits and underscores, and do not start with a digit", e.Name))
		}
		// 2. Belt and braces with the schema, so `ezc manifest validate` is useful
		//    on its own for anyone whose editor is not schema-aware.
		if !slices.Contains(AllTypes(), v.Type) {
			names := make([]string, len(AllTypes()))
			for i, t := range AllTypes() {
				names[i] = string(t)
			}
			errs = append(errs, fmt.Errorf("%s: unknown type %q — use %s or %s", e.Name, v.Type, strings.Join(names[:len(names)-1], ", "), names[len(names)-1]))
		}
		if v.Desc == "" {
			errs = append(errs, fmt.Errorf("%s: empty desc", e.Name))
		}
		if v.Type == TypeEnum && len(v.Values) == 0 {
			errs = append(errs, fmt.Errorf("%s: type enum requires a non-empty values list", e.Name))
		}
		if v.Type != TypeEnum && len(v.Values) > 0 {
			errs = append(errs, fmt.Errorf("%s: values is only meaningful for type enum (got %q)", e.Name, v.Type))
		}
		if v.Type != TypeInt && (v.Min != nil || v.Max != nil) {
			errs = append(errs, fmt.Errorf("%s: min/max are only meaningful for type int (got %q)", e.Name, v.Type))
		}
		// 3. Inverted bounds admit no value at all.
		if v.Min != nil && v.Max != nil && *v.Min > *v.Max {
			errs = append(errs, fmt.Errorf("%s: min %d exceeds max %d", e.Name, *v.Min, *v.Max))
		}

		// 4. A conditional requirement must name a variable that exists, and
		//    compare against a value that variable can actually hold.
		if c := v.RequiredWhen; c != nil {
			if _, req := m.Required[e.Name]; req {
				errs = append(errs, fmt.Errorf("%s: requiredWhen on an already-required variable", e.Name))
			}
			target, ok := byName[c.Var]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: requiredWhen names unknown variable %q", e.Name, c.Var))
			} else if target.Type == TypeEnum && !contains(target.Values, c.Equals) {
				errs = append(errs, fmt.Errorf("%s: requiredWhen expects %s=%q, but %s only permits %v",
					e.Name, c.Var, c.Equals, c.Var, target.Values))
			}
		}
	}
	return errs
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
