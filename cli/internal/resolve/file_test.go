package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

// write puts content in a file called name under a temp directory.
func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustLoad(t *testing.T, name, content, home string) []backend.Pair {
	t.Helper()
	pairs, present, err := loadFile(write(t, name, content), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("file reported absent")
	}
	return pairs
}

func loadErr(t *testing.T, name, content, home string) string {
	t.Helper()
	_, _, err := loadFile(write(t, name, content), home, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err.Error()
}

func TestRawScalars(t *testing.T) {
	pairs := mustLoad(t, "common.yaml", "ZIP: 08540\nVER: 1.20\nBIG: 1e5\nPORT: \"8080\"\nON: yes\n", "")
	want := []backend.Pair{{Name: "ZIP", Value: "08540"}, {Name: "VER", Value: "1.20"}, {Name: "BIG", Value: "1e5"}, {Name: "PORT", Value: "8080"}, {Name: "ON", Value: "yes"}}
	if len(pairs) != len(want) {
		t.Fatalf("got %v", pairs)
	}
	for i := range want {
		if pairs[i] != want[i] {
			t.Errorf("pair %d: got %v, want %v", i, pairs[i], want[i])
		}
	}
}

func TestEmptyFile(t *testing.T) {
	for _, content := range []string{"", "# only a comment\n"} {
		if pairs := mustLoad(t, "common.yaml", content, ""); len(pairs) != 0 {
			t.Errorf("%q: got %v", content, pairs)
		}
	}
}

func TestRejectedShapes(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"nested", "A: {b: 1}\n", `"A" is a nested block`},
		{"list", "A: [1, 2]\n", `"A" is a list`},
		{"alias", "&x A: v\nB: *x\n", `"B" is an anchor reference`},
		{"anchor", "PW: &hunter2 x\n", `: "PW" has an anchor; write the value out`},
		{"anchor alone", "PW: &hunter2\n", `: "PW" has an anchor; write the value out`},
		{"custom tag", "PW: !hunter2bang rest\n", `: "PW" has a YAML tag; quote the value if it starts with !`},
		{"custom tag alone", "PW: !hunter2only\n", `: "PW" has a YAML tag; quote the value if it starts with !`},
		{"core tag", "PW: !!str hunter2\n", `: "PW" has a YAML tag; quote the value if it starts with !`},
		{"bare tag", "A: 1\nPW: ! hunter2\n", `: "PW" has a YAML tag; quote the value if it starts with !`},
		{"duplicate key", "A: hunter2\nB: x\nA: 2\n", `: "A" is defined twice`},
		{"second document", "A: 1\n---\nB: hunter2\n", ": has more than one YAML document; a values file is a single mapping"},
		{"second document, malformed", "A: 1\n---\nB: [hunter2\n", ": has more than one YAML document; a values file is a single mapping"},
		{"null", "A:\n", `"A" has no value`},
		{"tilde", "A: ~\n", `"A" has no value`},
		{"not a mapping", "- a\n", "expected a flat mapping of NAME: value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := loadErr(t, "common.yaml", c.content, "")
			if !strings.Contains(got, c.want) {
				t.Errorf("got %q, want it to contain %q", got, c.want)
			}
			if strings.Contains(got, "hunter2") {
				t.Errorf("value leaked: %q", got)
			}
		})
	}
}

func TestHomeExpansion(t *testing.T) {
	pairs := mustLoad(t, "common.yaml", "A: ~/x\nB: $HOME/y\nC: ${HOME}/z\nD: \"~\"\nE: ~other/x\nF: a/~/b\n", "/h")
	want := []string{"/h/x", "/h/y", "/h/z", "/h", "~other/x", "a/~/b"}
	for i, w := range want {
		if pairs[i].Value != w {
			t.Errorf("%s: got %q, want %q", pairs[i].Name, pairs[i].Value, w)
		}
	}
}

func TestHomeExpansionWithoutHome(t *testing.T) {
	got := loadErr(t, "common.yaml", "A: ~/secret-rest\n", "")
	if !strings.Contains(got, "A starts with a home reference") || strings.Contains(got, "secret-rest") {
		t.Errorf("got %q", got)
	}
}

func TestEncryptedWithoutDecrypter(t *testing.T) {
	p := write(t, "common.enc.yaml", "K: ciphertext\n")
	_, _, err := loadFile(p, "/h", nil)
	if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "is encrypted, and this version of ezc cannot decrypt") {
		t.Errorf("got %v", err)
	}
}

func TestEncryptedUsesDecrypter(t *testing.T) {
	p := write(t, "common.enc.yaml", "K: ciphertext\n")
	decrypt := func(string) ([]backend.Pair, error) { return []backend.Pair{{Name: "K", Value: "~/k"}}, nil }
	pairs, _, err := loadFile(p, "/h", decrypt)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].Value != "/h/k" {
		t.Errorf("got %v", pairs)
	}
}

func TestMissingFileIsAbsent(t *testing.T) {
	pairs, present, err := loadFile(filepath.Join(t.TempDir(), "common.yaml"), "", nil)
	if err != nil || present || pairs != nil {
		t.Errorf("got %v, %v, %v", pairs, present, err)
	}
}

// A parser error never quotes the file: yaml.v3 names an unknown anchor, which here is most of a password.
func TestYAMLErrorsNeverQuoteTheFile(t *testing.T) {
	p := write(t, "env.local.yaml", "PW: *hunter2star\n")
	_, _, err := loadFile(p, "", nil)
	if err == nil || err.Error() != p+": not valid YAML" {
		t.Errorf("got %v", err)
	}
	p = write(t, "env.local.yaml", "A: 1\n  PW: hunter2star\n")
	_, _, err = loadFile(p, "", nil)
	if err == nil || err.Error() != p+": not valid YAML (line 2)" {
		t.Errorf("got %v", err)
	}
}

func TestInvalidYAML(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("yaml: line 12: did not find expected key"), "f.yaml: not valid YAML (line 12)"},
		{errors.New("yaml: unknown anchor 'hunter2star' referenced"), "f.yaml: not valid YAML"},
		{errors.New("yaml: unmarshal errors:\n  line 3: cannot unmarshal !!str `hunter2` into int"), "f.yaml: not valid YAML (line 3)"},
	}
	for _, c := range cases {
		if got := invalidYAML("f.yaml", c.err).Error(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// What the rejections above must not catch: a quoted or block value may start with ! or &.
func TestSpecialCharactersStillLoadVerbatim(t *testing.T) {
	pairs := mustLoad(t, "common.yaml", "---\nA: \"!quoted\"\nB: 08540\nC: yes\nD: '&literal'\nE: a!b&c\nF: |\n  !block\nÉ: \"!x\"\n", "")
	want := []backend.Pair{{Name: "A", Value: "!quoted"}, {Name: "B", Value: "08540"}, {Name: "C", Value: "yes"}, {Name: "D", Value: "&literal"}, {Name: "E", Value: "a!b&c"}, {Name: "F", Value: "!block\n"}, {Name: "É", Value: "!x"}}
	if !slices.Equal(pairs, want) {
		t.Errorf("got %q, want %q", pairs, want)
	}
}

func TestBareTagBeforeQuotedValue(t *testing.T) {
	for _, content := range []string{"PW: ! \"hunter2\"\n", "PW: ! 'hunter2'\n", "PW: ! |\n  hunter2\n"} {
		if got := loadErr(t, "common.yaml", content, ""); !strings.Contains(got, `"PW" has a YAML tag`) {
			t.Errorf("%q: got %q", content, got)
		}
	}
}

func TestBOMDoesNotHideATag(t *testing.T) {
	if got := loadErr(t, "common.yaml", "\ufeffPW: ! hunter2\n", ""); !strings.Contains(got, "has a YAML tag") {
		t.Errorf("got %q", got)
	}
}

func TestDocumentEndMarker(t *testing.T) {
	pairs := mustLoad(t, "common.yaml", "A: 1\n...\n", "")
	if len(pairs) != 1 || pairs[0].Name != "A" || pairs[0].Value != "1" {
		t.Errorf("got %v", pairs)
	}
	if got := loadErr(t, "common.yaml", "A: 1\n...\nB: 2\n", ""); !strings.Contains(got, "more than one YAML document") {
		t.Errorf("got %q", got)
	}
}
