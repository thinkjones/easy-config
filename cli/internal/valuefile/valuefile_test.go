package valuefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func slurp(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func plain(t *testing.T, path string) []backend.Pair {
	t.Helper()
	pairs, _, err := resolve.Plain(path)
	if err != nil {
		t.Fatal(err)
	}
	return pairs
}

func TestSetRoundTripsExactText(t *testing.T) {
	values := []string{"08540", "1.20", "yes", "null", "~", "!x", "&a b", "*a", " lead", "trail ", "a: b", "# h", "it's \"q\"",
		"$(id)", "a\nb\nc", "a\n", "héllo ✓", "- item", "{a: 1}", "[1]", "@x", "`x`", "%x", "key: |", strings.Repeat("abc def ", 500)}
	for _, v := range values {
		path := filepath.Join(t.TempDir(), "common.yaml")
		if err := Set(path, "A", v); err != nil {
			t.Errorf("%q: %v", v, err)
			continue
		}
		got := plain(t, path)
		if len(got) != 1 || got[0].Name != "A" || got[0].Value != v {
			t.Errorf("%q: read back %q", v, got)
		}
		if strings.Contains(slurp(t, path), "!!") {
			t.Errorf("%q: the file carries a tag:\n%s", v, slurp(t, path))
		}
	}
}

func TestSetRefusesAValueThatCannotBeStored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "common.yaml")
	put(t, path, "B: 2\n")
	err := Set(path, "A", "has\x00nul")
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "A") || strings.Contains(err.Error(), "has") {
		t.Fatalf("got %v", err)
	}
	if slurp(t, path) != "B: 2\n" {
		t.Fatal("the file changed")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("left %v", entries)
	}
}

func TestSetKeepsCommentsAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "common.yaml")
	put(t, path, "# head\nA: 1 # tail\n\n# about B\nB: 2\n")
	if err := Set(path, "B", "9"); err != nil {
		t.Fatal(err)
	}
	got := slurp(t, path)
	for _, want := range []string{"# head", "# tail", "# about B"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	pairs := plain(t, path)
	if len(pairs) != 2 || pairs[0] != (backend.Pair{Name: "A", Value: "1"}) || pairs[1] != (backend.Pair{Name: "B", Value: "9"}) {
		t.Fatalf("got %v", pairs)
	}
}

func TestSetAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "common.yaml")
	put(t, path, "A: 1\nB: 2\n")
	if err := Set(path, "C", "3"); err != nil {
		t.Fatal(err)
	}
	pairs := plain(t, path)
	if len(pairs) != 3 || pairs[2] != (backend.Pair{Name: "C", Value: "3"}) {
		t.Fatalf("got %v", pairs)
	}
}

func TestSetIntoACommentOnlyFileKeepsTheComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.local.yaml")
	put(t, path, "# Values for local.\n# Plain lines.\n")
	if err := Set(path, "A", "1"); err != nil {
		t.Fatal(err)
	}
	if got := slurp(t, path); got != "# Values for local.\n# Plain lines.\nA: \"1\"\n" && got != "# Values for local.\n# Plain lines.\nA: '1'\n" {
		t.Fatalf("got %q", got)
	}
	if p := plain(t, path); len(p) != 1 || p[0].Value != "1" {
		t.Fatalf("got %v", p)
	}
}

func TestSetCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "common.yaml")
	if err := Set(path, "A", "x"); err != nil {
		t.Fatal(err)
	}
	if got := slurp(t, path); got != "A: x\n" {
		t.Fatalf("got %q", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestSetKeepsTheMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "common.yaml")
	put(t, path, "A: 1\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, "A", "2"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestSetLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "common.yaml")
	put(t, path, "A: 1\n")
	if err := Set(path, "B", "2"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("left %v", entries)
	}
}

func TestSetRefusesAFileTheResolverRejects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "common.yaml")
	put(t, path, "A: &x 1\n")
	if err := Set(path, "B", "2"); err == nil {
		t.Fatal("want an error")
	}
	if slurp(t, path) != "A: &x 1\n" {
		t.Fatal("the file changed")
	}
}

func TestSetOnReadOnlyFolderFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "common.yaml")
	put(t, path, "A: 1\n")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if err := Set(path, "B", "2"); err == nil {
		t.Fatal("want an error")
	}
	if slurp(t, path) != "A: 1\n" {
		t.Fatal("the file changed")
	}
}

func TestUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "common.yaml")
	put(t, path, "# c\nA: 1\nB: 2\n")
	removed, err := Unset(path, "A")
	if err != nil || !removed {
		t.Fatalf("got %v, %v", removed, err)
	}
	if p := plain(t, path); len(p) != 1 || p[0].Name != "B" {
		t.Fatalf("got %v", p)
	}
	removed, err = Unset(path, "NOPE")
	if err != nil || removed {
		t.Fatalf("got %v, %v", removed, err)
	}
	removed, err = Unset(path, "B")
	if err != nil || !removed {
		t.Fatalf("got %v, %v", removed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file should be gone: %v", err)
	}
	if removed, err = Unset(path, "B"); err != nil || removed {
		t.Fatalf("absent file: got %v, %v", removed, err)
	}
}

const template = `# What this repo needs from its environment.
schemaVersion: v1

required: {}

# Example:
#   API_URL:
#     type: url
`

func TestDeclareAndUndeclare(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	put(t, path, template)
	if err := Declare(path, "API_URL", manifest.Var{Type: manifest.TypeURL, Desc: "Base URL"}, false); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(path)
	if err != nil || len(m.Validate()) > 0 {
		t.Fatalf("got %v, %v", err, m.Validate())
	}
	if v, ok := m.Required["API_URL"]; !ok || v.Type != manifest.TypeURL || v.Desc != "Base URL" {
		t.Fatalf("got %v", m.Required)
	}
	for _, want := range []string{"# What this repo needs", "# Example:"} {
		if !strings.Contains(slurp(t, path), want) {
			t.Errorf("lost %q:\n%s", want, slurp(t, path))
		}
	}
	if err := Declare(path, "API_URL", manifest.Var{Type: manifest.TypeStr, Desc: "x"}, false); err == nil || !strings.Contains(err.Error(), "API_URL is already declared") {
		t.Fatalf("got %v", err)
	}
	if err := Declare(path, "DEBUG", manifest.Var{Type: manifest.TypeBool, Desc: "d"}, true); err != nil {
		t.Fatal(err)
	}
	m, _ = manifest.Load(path)
	if _, ok := m.Optional["DEBUG"]; !ok || len(m.Validate()) > 0 {
		t.Fatalf("got %v %v", m.Optional, m.Validate())
	}
	if removed, err := Undeclare(path, "DEBUG"); err != nil || !removed {
		t.Fatalf("got %v, %v", removed, err)
	}
	if removed, err := Undeclare(path, "API_URL"); err != nil || !removed {
		t.Fatalf("got %v, %v", removed, err)
	}
	m, err = manifest.Load(path)
	if err != nil || len(m.Validate()) > 0 || len(m.Required) != 0 || len(m.Optional) != 0 {
		t.Fatalf("got %v %v %v", err, m, m.Validate())
	}
	if removed, err := Undeclare(path, "API_URL"); err != nil || removed {
		t.Fatalf("got %v, %v", removed, err)
	}
}

func TestDeclareKeepsBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	put(t, path, template)
	lo, hi := 1, 9
	if err := Declare(path, "SIZE", manifest.Var{Type: manifest.TypeInt, Desc: "n: how many", Min: &lo, Max: &hi}, false); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Load(path)
	v := m.Required["SIZE"]
	if v.Min == nil || *v.Min != 1 || *v.Max != 9 || v.Desc != "n: how many" {
		t.Fatalf("got %+v", v)
	}
}
