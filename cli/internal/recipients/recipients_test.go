package recipients

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const example = `recipients:
  gene:
    key: age1aaa
    kind: user
    added: 2026-10-10
    addedBy: gene
  gene-mini:
    key: age1bbb
    kind: machine
    owner: gene
    added: 2026-10-10
    addedBy: gene
  ci:
    key: age1ccc
    kind: bot
    added: 2026-10-10
    addedBy: gene
    repos: [thinkjones/fs-news-digest, thinkjones/fs-website]
`

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRoundTrip(t *testing.T) {
	r, found, err := Load(write(t, example))
	if err != nil || !found {
		t.Fatalf("Load: found=%v err=%v", found, err)
	}
	if r.Recipients["gene-mini"].Owner != "gene" || r.Recipients["gene"].Added != "2026-10-10" ||
		!reflect.DeepEqual(r.Recipients["ci"].Repos, []string{"thinkjones/fs-news-digest", "thinkjones/fs-website"}) {
		t.Fatalf("unexpected registry: %+v", r)
	}
	out := t.TempDir()
	if err := r.Save(out); err != nil {
		t.Fatal(err)
	}
	back, found, err := Load(out)
	if err != nil || !found || !reflect.DeepEqual(r, back) {
		t.Fatalf("round trip differs: %+v vs %+v (err %v)", r, back, err)
	}
	data, _ := os.ReadFile(filepath.Join(out, File))
	s := string(data)
	if !(strings.Index(s, "  ci:") < strings.Index(s, "  gene:") && strings.Index(s, "  gene:") < strings.Index(s, "  gene-mini:")) {
		t.Fatalf("names not sorted:\n%s", s)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
}

func TestKeysAndNameOf(t *testing.T) {
	r := Registry{Recipients: map[string]Recipient{
		"zed": {Key: "k3"}, "amy": {Key: "k1"}, "bob": {Key: "k2"},
	}}
	if got := r.Keys(); !reflect.DeepEqual(got, []string{"k1", "k2", "k3"}) {
		t.Fatalf("Keys = %v", got)
	}
	if r.NameOf("k2") != "bob" || r.NameOf("nope") != "" {
		t.Fatalf("NameOf wrong")
	}
}

func TestLoadMissing(t *testing.T) {
	_, found, err := Load(t.TempDir())
	if found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"kind":    {"recipients:\n  a:\n    key: k\n    kind: robot\n", "recipient a"},
		"no key":  {"recipients:\n  a:\n    key: \"\"\n    kind: user\n", "recipient a"},
		"name":    {"recipients:\n  Bad:\n    key: k\n    kind: user\n", `"Bad"`},
		"dup key": {"recipients:\n  a:\n    key: k\n    kind: user\n  b:\n    key: k\n    kind: user\n", "a and b"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := Load(write(t, c.body))
			if err == nil || !strings.Contains(err.Error(), File) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to name %s and %q", err, File, c.want)
			}
		})
	}
}

func TestValidName(t *testing.T) {
	for _, s := range []string{"gene", "gene-mini", "ci2"} {
		if !ValidName(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"Gene", "-x", "a b", "a_b", ""} {
		if ValidName(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}
