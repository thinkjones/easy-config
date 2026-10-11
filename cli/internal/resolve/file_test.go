package resolve

import (
	"os"
	"path/filepath"
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
		{"alias", "A: &x v\nB: *x\n", `"B" is an anchor reference`},
		{"null", "A:\n", `"A" has no value`},
		{"tilde", "A: ~\n", `"A" has no value`},
		{"not a mapping", "- a\n", "expected a flat mapping of NAME: value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := loadErr(t, "common.yaml", c.content, ""); !strings.Contains(got, c.want) {
				t.Errorf("got %q, want it to contain %q", got, c.want)
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
	if got := loadErr(t, "common.yaml", "A: ~/x\n", ""); !strings.Contains(got, "A starts with a home reference") {
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
