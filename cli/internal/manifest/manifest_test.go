package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func load(t *testing.T, yamlText string) (Manifest, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(p, []byte(yamlText), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func validate(t *testing.T, yamlText string) string {
	t.Helper()
	m, err := load(t, yamlText)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var msgs []string
	for _, e := range m.Validate() {
		msgs = append(msgs, e.Error())
	}
	return strings.Join(msgs, "\n")
}

func expectErr(t *testing.T, yamlText, want string) {
	t.Helper()
	if got := validate(t, yamlText); !strings.Contains(got, want) {
		t.Errorf("errors %q do not contain %q", got, want)
	}
}

func TestValidManifest(t *testing.T) {
	const y = `
schemaVersion: v1
required:
  LLM_API_KEY:
    type: secret
    desc: API key for whichever provider LLM_PROVIDER names
  BATCH_SIZE:
    type: int
    min: 1
    max: 100
    desc: articles per LLM scoring call
optional:
  DIGEST_OUTPUT_PATH:
    type: path
    desc: destination file
    requiredWhen: {var: BATCH_SIZE, equals: "1"}
`
	m, err := load(t, y)
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.Validate(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(m.Required) != 2 || len(m.Optional) != 1 {
		t.Errorf("got %d required, %d optional; want 2, 1", len(m.Required), len(m.Optional))
	}
}

func TestEmptyRequiredIsValid(t *testing.T) {
	if got := validate(t, "schemaVersion: v1\nrequired: {}\n"); got != "" {
		t.Errorf("unexpected errors: %s", got)
	}
}

func TestWrongSchemaVersion(t *testing.T) {
	expectErr(t, "schemaVersion: v2\nrequired: {}\n", `schemaVersion "v2" — this ezc understands "v1"`)
}

func TestBothRequiredAndOptional(t *testing.T) {
	expectErr(t, `
schemaVersion: v1
required:
  A: {type: str, desc: d}
optional:
  A: {type: str, desc: d}
`, "A: declared both required and optional")
}

func TestEmptyDesc(t *testing.T) {
	expectErr(t, "schemaVersion: v1\nrequired:\n  A: {type: str, desc: \"\"}\n", "A: empty desc")
}

func TestEnumNeedsValues(t *testing.T) {
	expectErr(t, "schemaVersion: v1\nrequired:\n  A: {type: enum, desc: d}\n", "type enum requires a non-empty values list")
}

func TestValuesOnlyForEnum(t *testing.T) {
	expectErr(t, "schemaVersion: v1\nrequired:\n  A: {type: str, desc: d, values: [x]}\n", "values is only meaningful for type enum")
}

func TestBoundsOnlyForInt(t *testing.T) {
	expectErr(t, "schemaVersion: v1\nrequired:\n  A: {type: str, desc: d, min: 1}\n", "min/max are only meaningful for type int")
}

func TestInvertedBounds(t *testing.T) {
	expectErr(t, "schemaVersion: v1\nrequired:\n  A: {type: int, desc: d, min: 5, max: 1}\n", "min 5 exceeds max 1")
}

func TestRequiredWhenOnRequired(t *testing.T) {
	expectErr(t, `
schemaVersion: v1
required:
  A: {type: str, desc: d}
  B: {type: str, desc: d, requiredWhen: {var: A, equals: x}}
`, "requiredWhen on an already-required variable")
}

func TestRequiredWhenUnknownVar(t *testing.T) {
	expectErr(t, `
schemaVersion: v1
required: {}
optional:
  A: {type: str, desc: d, requiredWhen: {var: NOPE, equals: x}}
`, `requiredWhen names unknown variable "NOPE"`)
}

func TestRequiredWhenNotInEnum(t *testing.T) {
	expectErr(t, `
schemaVersion: v1
required:
  MODE: {type: enum, desc: d, values: [file, s3]}
optional:
  A: {type: path, desc: d, requiredWhen: {var: MODE, equals: flie}}
`, "only permits [file s3]")
}

func TestUnknownFieldRejected(t *testing.T) {
	_, err := load(t, "schemaVersion: v1\nrequired:\n  A: {type: str, desc: d, default: x}\n")
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Errorf("want an error mentioning default, got %v", err)
	}
}

func TestSchemaKeyAccepted(t *testing.T) {
	if _, err := load(t, "$schema: https://example/x.json\nschemaVersion: v1\nrequired: {}\n"); err != nil {
		t.Errorf("Load: %v", err)
	}
}

const twoTables = `
schemaVersion: v1
required:
  ZED: {type: str, desc: d}
  ALPHA: {type: str, desc: d}
optional:
  YAK: {type: str, desc: d}
  BEE: {type: str, desc: d}
`

func TestLookup(t *testing.T) {
	m, err := load(t, twoTables)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ZED", "BEE"} {
		if v, ok := m.Lookup(name); !ok || v.Type != TypeStr {
			t.Errorf("Lookup(%s) = %v, %v", name, v, ok)
		}
	}
	if _, ok := m.Lookup("ABSENT"); ok {
		t.Error("Lookup(ABSENT) reported true")
	}
}

func TestAllIsSorted(t *testing.T) {
	m, err := load(t, twoTables)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range m.All() {
		got = append(got, e.Name)
	}
	want := []string{"ALPHA", "ZED", "BEE", "YAK"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("All() names = %v, want %v", got, want)
	}
}

func TestJSONSchema(t *testing.T) {
	b, err := JSONSchema("https://example/s.json")
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if s["$id"] != "https://example/s.json" {
		t.Errorf("$id = %v", s["$id"])
	}
	if s["additionalProperties"] != false {
		t.Errorf("top-level additionalProperties = %v, want false", s["additionalProperties"])
	}
	req := s["properties"].(map[string]any)["required"].(map[string]any)
	if _, ok := req["minProperties"]; ok {
		t.Error("required must not set minProperties")
	}
	vs := req["additionalProperties"].(map[string]any)
	enum := vs["properties"].(map[string]any)["type"].(map[string]any)["enum"].([]any)
	var got []VarType
	for _, e := range enum {
		got = append(got, VarType(e.(string)))
	}
	if !reflect.DeepEqual(got, AllTypes()) {
		t.Errorf("type enum = %v, want %v", got, AllTypes())
	}
	b2, _ := JSONSchema("https://example/s.json")
	if string(b) != string(b2) {
		t.Error("two calls returned different bytes")
	}
}
