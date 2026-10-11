package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/share"
)

const storeManifest = "schemaVersion: v1\nrequired:\n  NAME: {type: str, desc: who}\n"
const repoManifest = "schemaVersion: v1\nrequired:\n  NAME: {type: str, desc: who}\noptional:\n  EXTRA: {type: str, desc: more}\n"

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// healthy is options for a store and a repo that pass, on a fake backend with a key.
func healthy(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	o := Options{
		Version:    "0.1.0",
		Home:       root,
		StoreDir:   filepath.Join(root, "store", ".ezc"),
		RepoDir:    filepath.Join(root, "repo", ".ezc"),
		Backend:    &backend.Fake{},
		KeyNote:    "~/key",
		KeyFound:   true,
		ExtraFiles: []string{backend.FakePolicy},
	}
	put(t, filepath.Join(o.StoreDir, "manifest.yaml"), storeManifest)
	put(t, filepath.Join(o.StoreDir, "common.yaml"), "NAME: n\n")
	put(t, filepath.Join(o.RepoDir, "manifest.yaml"), repoManifest)
	put(t, filepath.Join(o.RepoDir, "env.local.yaml"), "EXTRA: x\n")
	put(t, filepath.Join(o.RepoDir, "env.sh"), share.EnvSh)
	return o
}

// item finds a section's item by label.
func item(t *testing.T, r Report, section, label string) Item {
	t.Helper()
	for _, s := range r.Sections {
		if s.Name != section {
			continue
		}
		for _, it := range s.Items {
			if it.Label == label {
				return it
			}
		}
	}
	t.Fatalf("no %s/%s in\n%s", section, label, r)
	return Item{}
}

func TestAllHealthy(t *testing.T) {
	r := Collect(healthy(t))
	for _, s := range r.Sections {
		for _, it := range s.Items {
			if it.State != OK {
				t.Errorf("%s/%s: %v %q", s.Name, it.Label, it.State, it.Detail)
			}
		}
	}
	if r.Broken() {
		t.Fatal("Broken")
	}
	out := r.String()
	m, s, p := strings.Index(out, "machine"), strings.Index(out, "store"), strings.Index(out, "repo")
	if m < 0 || !(m < s && s < p) {
		t.Fatalf("sections out of order:\n%s", out)
	}
	if got := item(t, r, "repo", "manifest").Detail; got != "1 required, 1 optional" {
		t.Errorf("repo manifest: %q", got)
	}
	if got := item(t, r, "repo", "environment local").Detail; got != "2 vars from 2 files" {
		t.Errorf("environment: %q", got)
	}
}

func TestBackendMissing(t *testing.T) {
	o := healthy(t)
	o.Backend = &backend.Fake{Missing: true}
	r := Collect(o)
	if it := item(t, r, "machine", "backend"); it.State != Broken {
		t.Fatalf("%+v", it)
	}
	if !r.Broken() {
		t.Fatal("not Broken")
	}
}

func TestNoKeyIsAWarning(t *testing.T) {
	o := healthy(t)
	o.KeyFound = false
	r := Collect(o)
	it := item(t, r, "machine", "key")
	if it.State != Warn || it.Detail != "no key at ~/key" || it.Fix == "" {
		t.Fatalf("%+v", it)
	}
	if r.Broken() {
		t.Fatal("Broken")
	}
}

func TestNoStoreDefaultVersusExplicit(t *testing.T) {
	o := healthy(t)
	o.StoreDir = filepath.Join(t.TempDir(), "nope", ".ezc")
	r := Collect(o)
	if it := item(t, r, "store", "location"); it.State != Warn {
		t.Fatalf("%+v", it)
	}
	for _, s := range r.Sections {
		if s.Name == "store" && len(s.Items) != 1 {
			t.Fatalf("store section has %d items", len(s.Items))
		}
	}
	o.StoreSet = true
	r = Collect(o)
	if it := item(t, r, "store", "location"); it.State != Broken {
		t.Fatalf("%+v", it)
	}
	if !r.Broken() {
		t.Fatal("not Broken")
	}
}

func TestStoreAuditFindings(t *testing.T) {
	o := healthy(t)
	put(t, filepath.Join(o.StoreDir, "common.yaml"), "NAME: n\nSTRAY: s\n")
	it := item(t, Collect(o), "store", "audit")
	if it.State != Broken || it.Detail != "1 to fix — run `ezc store audit`" {
		t.Fatalf("%+v", it)
	}
}

func TestStoreAuditSkippedWithoutKey(t *testing.T) {
	o := healthy(t)
	o.Backend = &backend.Fake{Locked: true}
	put(t, filepath.Join(o.StoreDir, backend.FakePolicy), "")
	put(t, filepath.Join(o.StoreDir, "env.prod.enc.yaml"), "NAME: FAKE[bg==]\n")
	r := Collect(o)
	var warn Item
	for _, s := range r.Sections {
		if s.Name != "store" {
			continue
		}
		for _, it := range s.Items {
			if it.Label == "audit" && it.State == Warn {
				warn = it
			}
		}
	}
	if !strings.Contains(warn.Detail, "env.prod.enc.yaml") {
		t.Fatalf("no skipped warning in\n%s", r)
	}
}

func TestEncryptedFilesWithoutPolicy(t *testing.T) {
	o := healthy(t)
	put(t, filepath.Join(o.StoreDir, "env.prod.enc.yaml"), "NAME: FAKE[bg==]\n")
	put(t, filepath.Join(o.RepoDir, "common.enc.yaml"), "EXTRA: FAKE[bg==]\n")
	r := Collect(o)
	for _, sec := range []string{"store", "repo"} {
		it := item(t, r, sec, "policy")
		if it.State != Broken || it.Detail != "encrypted files but no policy" || it.Fix != `See "Encryption" in the README.` {
			t.Errorf("%s: %+v", sec, it)
		}
	}
	put(t, filepath.Join(o.RepoDir, backend.FakePolicy), "")
	r = Collect(o)
	if it := item(t, r, "repo", "policy"); it.State != OK || it.Detail != "present" {
		t.Errorf("%+v", it)
	}
	if it := item(t, Collect(healthy(t)), "repo", "policy"); it.Detail != "not needed" {
		t.Errorf("%+v", it)
	}
}

func TestUnrecognisedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"common.yml", "comon.yaml", "env.enc.yaml", "notes.txt", "env.prod.yaml", "manifest.yaml", ".gitignore", ".DS_Store", "env.sh", "recipients.yaml", backend.FakePolicy} {
		put(t, filepath.Join(dir, n), "")
	}
	if err := os.Mkdir(filepath.Join(dir, "requests"), 0o755); err != nil {
		t.Fatal(err)
	}
	extra := []string{backend.FakePolicy}

	got, err := Unrecognised(dir, false, extra)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{
		{"common.yml", "common.yaml"},
		{"comon.yaml", "common.yaml"},
		{"env.enc.yaml", "env.<name>.enc.yaml"},
		{"notes.txt", ""},
		{"recipients.yaml", ""},
		{"requests", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repo:\n got %v\nwant %v", got, want)
	}

	got, err = Unrecognised(dir, true, extra)
	if err != nil {
		t.Fatal(err)
	}
	want = [][2]string{
		{"common.yml", "common.yaml"},
		{"comon.yaml", "common.yaml"},
		{"env.enc.yaml", "env.<name>.enc.yaml"},
		{"env.sh", ""},
		{"notes.txt", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("store:\n got %v\nwant %v", got, want)
	}
}

func TestFilesItems(t *testing.T) {
	o := healthy(t)
	put(t, filepath.Join(o.RepoDir, "comon.yaml"), "")
	put(t, filepath.Join(o.RepoDir, "notes.txt"), "")
	r := Collect(o)
	var got []string
	for _, s := range r.Sections {
		if s.Name != "repo" {
			continue
		}
		for _, it := range s.Items {
			if it.Label == "files" {
				if it.State != Warn {
					t.Errorf("%+v", it)
				}
				got = append(got, it.Detail)
			}
		}
	}
	want := []string{"comon.yaml — did you mean common.yaml?", "notes.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if it := item(t, r, "store", "files"); it.State != OK || it.Detail != "all recognised" {
		t.Fatalf("%+v", it)
	}
	if r.Broken() {
		t.Fatal("warnings must not break")
	}
}

func TestNotInARepo(t *testing.T) {
	o := healthy(t)
	o.RepoDir = ""
	r := Collect(o)
	for _, s := range r.Sections {
		if s.Name == "repo" && (len(s.Items) != 1 || s.Items[0].State != Warn) {
			t.Fatalf("%+v", s)
		}
	}
	if r.Broken() {
		t.Fatal("Broken")
	}
}

func TestShimStates(t *testing.T) {
	o := healthy(t)
	shim := filepath.Join(o.RepoDir, "env.sh")
	if err := os.Remove(shim); err != nil {
		t.Fatal(err)
	}
	if it := item(t, Collect(o), "repo", "shim"); it.State != Warn || it.Detail != "no .ezc/env.sh" {
		t.Fatalf("%+v", it)
	}
	put(t, shim, "# ezc-shim: 0\n")
	if it := item(t, Collect(o), "repo", "shim"); it.State != Broken || it.Fix != "Run `ezc repo init`." {
		t.Fatalf("%+v", it)
	}
	put(t, shim, "echo hi\n")
	if it := item(t, Collect(o), "repo", "shim"); it.State != Broken {
		t.Fatalf("%+v", it)
	}
	put(t, shim, share.EnvSh)
	if it := item(t, Collect(o), "repo", "shim"); it.State != OK || it.Detail != "version 1" {
		t.Fatalf("%+v", it)
	}
}

func TestEnvironmentProblems(t *testing.T) {
	o := healthy(t)
	if err := os.Remove(filepath.Join(o.StoreDir, "common.yaml")); err != nil {
		t.Fatal(err)
	}
	it := item(t, Collect(o), "repo", "environment local")
	if it.State != Broken || it.Detail != "1 problem — run `ezc env`" {
		t.Fatalf("%+v", it)
	}
	o.Env = "nope"
	it = item(t, Collect(o), "repo", "environment nope")
	if it.State != Broken || strings.Contains(it.Detail, "\n") {
		t.Fatalf("%+v", it)
	}
}

func TestEnvironmentWithoutKey(t *testing.T) {
	o := healthy(t)
	o.Backend = &backend.Fake{Locked: true}
	put(t, filepath.Join(o.RepoDir, backend.FakePolicy), "")
	put(t, filepath.Join(o.RepoDir, "common.enc.yaml"), "EXTRA: FAKE[bg==]\n")
	r := Collect(o)
	it := item(t, r, "repo", "environment local")
	if it.State != Warn || it.Detail != "cannot decrypt: no key" {
		t.Fatalf("%+v", it)
	}
	if r.Broken() {
		t.Fatalf("Broken:\n%s", r)
	}
}

func TestOnly(t *testing.T) {
	o := healthy(t)
	o.Only = "store"
	r := Collect(o)
	if len(r.Sections) != 1 || r.Sections[0].Name != "store" {
		t.Fatalf("%+v", r.Sections)
	}
}

func TestNoValuesInReport(t *testing.T) {
	o := healthy(t)
	put(t, filepath.Join(o.StoreDir, "common.yaml"), "NAME: VALUE-one-9f3\nSTRAY: VALUE-two-9f3\n")
	put(t, filepath.Join(o.StoreDir, backend.FakePolicy), "")
	put(t, filepath.Join(o.RepoDir, backend.FakePolicy), "")
	f := &backend.Fake{}
	if err := f.Set(filepath.Join(o.RepoDir, "common.enc.yaml"), "EXTRA", "VALUE-three-9f3"); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(o.RepoDir, "env.local.yaml"), "NAME: VALUE-four-9f3\nBAD: VALUE-five-9f3\n")
	for _, locked := range []bool{false, true} {
		o.Backend = &backend.Fake{Locked: locked}
		out := Collect(o).String()
		if strings.Contains(out, "9f3") {
			t.Fatalf("a value leaked:\n%s", out)
		}
	}
}

func TestStringLayout(t *testing.T) {
	r := Report{Sections: []Section{
		{Name: "machine", Items: []Item{
			{State: OK, Label: "ezc", Detail: "0.1.0"},
			{State: Warn, Label: "key", Detail: "no key at ~/k", Fix: "Make one."},
		}},
		{Name: "store", Items: []Item{
			{State: Broken, Label: "location", Detail: "gone", Fix: "Fix it."},
			{State: OK, Label: "files", Detail: "all recognised"},
		}},
	}}
	want := "machine\n" +
		"  ✓ ezc  0.1.0\n" +
		"  ! key  no key at ~/k\n" +
		"      Make one.\n" +
		"\n" +
		"store\n" +
		"  ✗ location  gone\n" +
		"      Fix it.\n" +
		"  ✓ files     all recognised\n"
	if got := r.String(); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}
