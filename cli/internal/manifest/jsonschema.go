package manifest

import (
	"encoding/json"
	"fmt"
)

// JSONSchema builds the JSON Schema that consuming repos reference from their
// manifest.yaml. It is DERIVED from the Go types in this package — regenerate
// rather than hand-editing schema/v<n>/manifest.schema.json.
//
// Hand-built rather than reflected: the interesting constraints (an enum needs
// values, min/max only apply to ints) are conditional and would need
// annotations a reflector cannot infer anyway. Also keeps this module's
// dependency list to yaml alone, which matters for a tool everyone installs.
func JSONSchema(schemaID string) ([]byte, error) {
	types := make([]string, 0, len(AllTypes()))
	for _, t := range AllTypes() {
		types = append(types, string(t))
	}

	varSchema := obj{
		"type":                 "object",
		"required":             []string{"type", "desc"},
		"additionalProperties": false,
		"properties": obj{
			"type": obj{"enum": types, "description": "how the value is parsed and typed in generated code"},
			"desc": obj{
				"type":        "string",
				"minLength":   1,
				"description": "prose for humans; emitted as the docstring on the generated field",
			},
			"values": obj{
				"type": "array", "items": obj{"type": "string"}, "minItems": 1, "uniqueItems": true,
				"description": "permitted values; required when type is enum, forbidden otherwise",
			},
			"min": obj{"type": "integer", "description": "inclusive lower bound; type int only"},
			"max": obj{"type": "integer", "description": "inclusive upper bound; type int only"},
			"requiredWhen": obj{
				"type":                 "object",
				"required":             []string{"var", "equals"},
				"additionalProperties": false,
				"properties": obj{
					"var":    obj{"type": "string", "description": "the variable this one depends on"},
					"equals": obj{"type": "string", "description": "the value of that variable which makes this one required"},
				},
				"description": "makes an optional variable required when another variable holds a given value",
			},
		},
		"allOf": []obj{
			// An enum with no values generates an uninhabitable type.
			{
				"if":   obj{"properties": obj{"type": obj{"const": "enum"}}, "required": []string{"type"}},
				"then": obj{"required": []string{"values"}},
				"else": obj{"not": obj{"required": []string{"values"}}},
			},
			// Bounds are meaningless on anything but an int.
			{
				"if":   obj{"properties": obj{"type": obj{"const": "int"}}, "required": []string{"type"}},
				"then": obj{},
				"else": obj{"not": obj{"anyOf": []obj{{"required": []string{"min"}}, {"required": []string{"max"}}}}},
			},
		},
	}

	// Variable names are shell identifiers; Validate applies the same rule.
	varMap := func(desc string) obj {
		return obj{
			"type":                 "object",
			"description":          desc,
			"propertyNames":        obj{"pattern": NamePattern},
			"additionalProperties": varSchema,
		}
	}

	root := obj{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  schemaID,
		"title":                "ezc environment manifest",
		"description":          "What one repo requires from its environment. Policy lives here; values live in .ezc/*.yaml and the shared ezc store.",
		"type":                 "object",
		"required":             []string{"schemaVersion", "required"},
		"additionalProperties": false,
		"properties": obj{
			// Self-reference. Editors key off this to validate the file, and
			// the root forbids unknown properties, so it has to be declared.
			"$schema": obj{
				"type":        "string",
				"description": "URL of this schema, so an editor can validate the file",
			},
			"schemaVersion": obj{
				"const":       SchemaVersion,
				"description": "manifest schema version this file is written against",
			},
			"required": varMap("variables that must be set and non-empty; ezc env fails without them"),
			"optional": varMap("variables whose absence turns a feature off; never defaulted"),
		},
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("building JSON schema: %w", err)
	}
	return append(out, '\n'), nil
}

type obj = map[string]any
