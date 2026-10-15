package registry

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

func TestValidID(t *testing.T) {
	for _, s := range []string{"thinkjones/app", "scratch-tool", "my.tool_2", "a/.github"} {
		if !ValidID(s) {
			t.Errorf("ValidID(%q) = false", s)
		}
	}
	for _, s := range []string{"", ".", "..", "../x", "a/b/c", "-x", "--upload-pack=x", "a b", "a\x1b[31m", "a/..", "a/", "/a", strings.Repeat("a", 101)} {
		if ValidID(s) {
			t.Errorf("ValidID(%q) = true", s)
		}
	}
	if !ValidID(strings.Repeat("a", 100)) {
		t.Error("100 characters should be valid")
	}
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissing(t *testing.T) {
	r, found, err := Load(t.TempDir())
	if err != nil || found || len(r.Repos) != 0 {
		t.Fatalf("got %v %v %v", r, found, err)
	}
}

const two = "repos:\n  scratch-tool: {added: 2026-10-13, addedBy: gene}\n  thinkjones/app: {added: 2026-10-13, addedBy: gene}\n"

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := Registry{Repos: map[string]Repo{
		"thinkjones/app": {Added: "2026-10-13", AddedBy: "gene"},
		"scratch-tool":   {Added: "2026-10-13", AddedBy: "gene"},
	}}
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, File))
	if string(got) != two {
		t.Fatalf("bytes:\n%s", got)
	}
	back, found, err := Load(dir)
	if err != nil || !found || len(back.Repos) != 2 || back.Repos["thinkjones/app"].AddedBy != "gene" {
		t.Fatalf("got %v %v %v", back, found, err)
	}
	if ids := back.IDs(); ids[0] != "scratch-tool" || ids[1] != "thinkjones/app" {
		t.Fatalf("ids %v", ids)
	}
}

// Whoever can push to the store writes addedBy: whatever Load accepts, Save must write back as a file Load reads.
func TestSaveKeepsAnAddedByThatIsNotPlain(t *testing.T) {
	for _, by := range []string{"a\nb", "a: b", "{x}", "- x", "yes", "", " lead", "a\x1b[31m", "#x", "a\"b'c", "\u2028", "|"} {
		dir := t.TempDir()
		write(t, dir, "repos:\n  app: {added: 2026-10-13, addedBy: "+strconv.Quote(by)+"}\n")
		r, _, err := Load(dir)
		if err != nil {
			t.Fatalf("%q: %v", by, err)
		}
		if err := r.Save(dir); err != nil {
			t.Fatal(err)
		}
		back, _, err := Load(dir)
		if err != nil || back.Repos["app"].AddedBy != by {
			data, _ := os.ReadFile(filepath.Join(dir, File))
			t.Fatalf("%q: got %q, %v\n%s", by, back.Repos["app"].AddedBy, err, data)
		}
	}
}

func TestLoadRefuses(t *testing.T) {
	cases := map[string]string{
		"id":      "repos:\n  \"../evil\": {added: 2026-10-13, addedBy: gene}\n",
		"field":   "repos:\n  a/b: {added: 2026-10-13, addedBy: gene, extra: x}\n",
		"date":    "repos:\n  a/b: {added: yesterday, addedBy: gene}\n",
		"top":     "repos: {}\nother: 1\n",
		"notyaml": "repos: [\n",
	}
	for name, body := range cases {
		dir := t.TempDir()
		write(t, dir, body)
		_, found, err := Load(dir)
		if err == nil || found {
			t.Errorf("%s: got found=%v err=%v", name, found, err)
			continue
		}
		if !strings.Contains(err.Error(), File) || strings.Contains(err.Error(), "evil") {
			t.Errorf("%s: error %q", name, err)
		}
	}
}

func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "")
	r, found, err := Load(dir)
	if err != nil || !found || r.Repos == nil {
		t.Fatalf("got %v %v %v", r, found, err)
	}
}

func TestWithWithout(t *testing.T) {
	r := Registry{Repos: map[string]Repo{"a/b": {Added: "2026-10-13", AddedBy: "gene"}}}
	if _, err := r.With("a/b", Repo{}); err == nil {
		t.Error("With of a registered ID should fail")
	}
	if _, err := r.With("../x", Repo{}); err == nil || strings.Contains(err.Error(), "../x") {
		t.Errorf("With of an invalid ID: %v", err)
	}
	if _, err := r.Without("\x1b[31mevil"); err == nil || strings.Contains(err.Error(), "\x1b") || strings.Contains(err.Error(), "evil") {
		t.Errorf("Without of an invalid ID: %v", err)
	}
	if _, err := r.Without("c/d"); err == nil {
		t.Error("Without of an unknown ID should fail")
	}
	if len(r.Repos) != 1 {
		t.Fatal("receiver changed")
	}
	n, err := r.With("c/d", Repo{Added: "2026-10-14", AddedBy: "sam"})
	if err != nil || len(n.Repos) != 2 || len(r.Repos) != 1 {
		t.Fatalf("With: %v %v", n, err)
	}
	m, err := n.Without("a/b")
	if err != nil || len(m.Repos) != 1 || len(n.Repos) != 2 {
		t.Fatalf("Without: %v %v", m, err)
	}
	var zero Registry
	if z, err := zero.With("a/b", Repo{}); err != nil || len(z.Repos) != 1 {
		t.Fatalf("zero With: %v %v", z, err)
	}
}

func TestUses(t *testing.T) {
	m := manifest.Manifest{
		Required: map[string]manifest.Var{"A": {}},
		Optional: map[string]manifest.Var{
			"B": {},
			"C": {RequiredWhen: &manifest.Condition{Var: "A", Equals: "x"}},
		},
	}
	for name, want := range map[string]Use{"A": Required, "B": Optional, "C": Required, "D": NotUsed} {
		if got := Uses(m, name); got != want {
			t.Errorf("Uses(%s) = %v, want %v", name, got, want)
		}
	}
}
