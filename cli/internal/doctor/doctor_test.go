package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

		ConfigPath:  filepath.Join(root, "config.toml"),
		ConfigFound: true,
		User:        "gene",
		GitFound:    true,
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
	put(t, shim, "# ezc-shim: x\n")
	if it := item(t, Collect(o), "repo", "shim"); it.State != Broken {
		t.Fatalf("%+v", it)
	}
	put(t, shim, fmt.Sprintf("# ezc-shim: %d\n", share.ShimVersion+1))
	it := item(t, Collect(o), "repo", "shim")
	if it.State != Warn || it.Fix != "Upgrade ezc." || !strings.Contains(it.Detail, "newer than this ezc") {
		t.Fatalf("%+v", it)
	}
	put(t, shim, share.EnvSh)
	if it := item(t, Collect(o), "repo", "shim"); it.State != OK || it.Detail != fmt.Sprintf("version %d", share.ShimVersion) {
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
	if err := f.Set(filepath.Join(o.StoreDir, "env.prod.enc.yaml"), "NAME", "VALUE-six-9f3"); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(o.StoreDir, "env.dev.enc.yaml"), "VALUE-seven-9f3\n") // malformed: an error path
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

func TestFilesQuoteOddNames(t *testing.T) {
	o := healthy(t)
	put(t, filepath.Join(o.RepoDir, "a\nb"), "")
	out := Collect(o).String()
	if strings.Contains(out, "a\nb") || !strings.Contains(out, `"a\nb"`) {
		t.Fatalf("got\n%s", out)
	}
}

func TestPolicyInFolderWithGlobCharacters(t *testing.T) {
	o := healthy(t)
	o.RepoDir = filepath.Join(t.TempDir(), "proj[1]", ".ezc")
	put(t, filepath.Join(o.RepoDir, "manifest.yaml"), repoManifest)
	put(t, filepath.Join(o.RepoDir, "common.enc.yaml"), "EXTRA: FAKE[bg==]\n")
	if it := item(t, Collect(o), "repo", "policy"); it.State != Broken {
		t.Fatalf("%+v", it)
	}
}

func TestStoreAuditWithAnUnencryptedValueIsBrokenNotAborted(t *testing.T) {
	o := healthy(t)
	put(t, filepath.Join(o.StoreDir, backend.FakePolicy), "")
	put(t, filepath.Join(o.StoreDir, "common.enc.yaml"), "NAME: FAKE[bg==]\nLEAKED: plain-canary\n")
	var states []State
	var details []string
	for _, s := range Collect(o).Sections {
		for _, it := range s.Items {
			if s.Name == "store" && it.Label == "audit" {
				states, details = append(states, it.State), append(details, it.Detail)
			}
			if strings.Contains(it.Detail+it.Fix, "plain-canary") {
				t.Errorf("%s prints the value", it.Label)
			}
		}
	}
	want := []string{"3 to fix — run `ezc store audit`", "damaged: value checks skipped for common.enc.yaml"}
	if !reflect.DeepEqual(details, want) || !reflect.DeepEqual(states, []State{Broken, Warn}) {
		t.Fatalf("got %v %v", states, details)
	}
}

func TestConfigItem(t *testing.T) {
	o := healthy(t)
	if it := item(t, Collect(o), "machine", "config"); it.State != OK || it.Detail != o.ConfigPath+" (user gene)" {
		t.Errorf("found: %+v", it)
	}
	o.ConfigFound, o.User = false, ""
	want := Item{Warn, "config", "no config at " + o.ConfigPath + ". Run `ezc setup`.", ""}
	if it := item(t, Collect(o), "machine", "config"); it != want {
		t.Errorf("missing: %+v, want %+v", it, want)
	}
	// Setup refuses to run while EZC_STORE is set.
	o.StoreSet = true
	want = Item{Warn, "config", "no config at " + o.ConfigPath + ".", ""}
	if it := item(t, Collect(o), "machine", "config"); it != want {
		t.Errorf("missing, with EZC_STORE: %+v, want %+v", it, want)
	}
	o.StoreSet = false
	o.ConfigErr = "/x/config.toml: not valid TOML"
	want = Item{Broken, "config", o.ConfigErr, ""}
	if it := item(t, Collect(o), "machine", "config"); it != want {
		t.Errorf("invalid: %+v, want %+v", it, want)
	}
}

func TestGitItem(t *testing.T) {
	o := healthy(t)
	if it := item(t, Collect(o), "machine", "git"); it.State != OK {
		t.Errorf("found: %+v", it)
	}
	o.GitFound = false
	want := Item{Broken, "git", "git is not installed.", ""}
	if it := item(t, Collect(o), "machine", "git"); it != want {
		t.Errorf("missing: %+v, want %+v", it, want)
	}
}

// labels are the labels of a section's items.
func labels(r Report, section string) []string {
	var out []string
	for _, s := range r.Sections {
		if s.Name == section {
			for _, it := range s.Items {
				out = append(out, it.Label)
			}
		}
	}
	return out
}

func TestSyncItem(t *testing.T) {
	o := healthy(t)
	for _, label := range labels(Collect(o), "store") {
		if label == "sync" || label == "access" || label == "requests" {
			t.Errorf("%s shown for a store that is not a git repo", label)
		}
	}
	for _, c := range []struct {
		phrase string
		state  State
		fix    string
	}{
		{"in sync", OK, ""},
		{"local only", OK, ""},
		{"2 commits ahead of origin", Warn, "Run `ezc store push`."},
		{"1 commit behind origin", Warn, "Run `ezc store pull`."},
		{"uncommitted changes", Warn, "Commit them."},
		{"diverged from origin (1 ahead, 1 behind)", Broken, "Run `ezc store pull`."},
	} {
		o.Sync, o.SyncState, o.SyncFix = c.phrase, c.state, c.fix
		want := Item{c.state, "sync", c.phrase, c.fix}
		if it := item(t, Collect(o), "store", "sync"); it != want {
			t.Errorf("%+v, want %+v", it, want)
		}
	}
}

func TestAccessItem(t *testing.T) {
	o := healthy(t)
	o.HasRegistry, o.AccessKnown, o.Recipient = true, true, "gene"
	if it := item(t, Collect(o), "store", "access"); it != (Item{OK, "access", "your key is the recipient gene", ""}) {
		t.Errorf("recipient: %+v", it)
	}
	o.Recipient = ""
	want := Item{Broken, "access", "your key is not one of this store's recipients.", "Run ezc join --print."}
	if it := item(t, Collect(o), "store", "access"); it != want {
		t.Errorf("not a recipient, no remote: %+v", it)
	}
	o.Remote = "github.com/acme/store"
	want.Fix = "Run ezc join github.com/acme/store."
	if it := item(t, Collect(o), "store", "access"); it != want {
		t.Errorf("not a recipient: %+v", it)
	}
	o.Request, o.RequestName = "not found", ""
	if it := item(t, Collect(o), "store", "access"); it != want {
		t.Errorf("no request: %+v", it)
	}
	o.Request, o.RequestName = "pending", "gene-laptop"
	want = Item{Broken, "access", "this machine's key cannot read the store yet; request gene-laptop is waiting for approval",
		"Ask someone who can read the store to run ezc user approve gene-laptop."}
	if it := item(t, Collect(o), "store", "access"); it != want {
		t.Errorf("pending: %+v", it)
	}
	o.Request, o.RequestName = "", ""
	if !Collect(o).Broken() {
		t.Error("not a recipient must be broken")
	}
	o.AccessKnown = false
	if slices.Contains(labels(Collect(o), "store"), "access") {
		t.Error("access shown with no public key")
	}
	o.RegistryErr = "/x/recipients.yaml: not valid YAML"
	if it := item(t, Collect(o), "store", "access"); it != (Item{Broken, "access", o.RegistryErr, ""}) {
		t.Errorf("registry error: %+v", it)
	}

	o = healthy(t)
	if slices.Contains(labels(Collect(o), "store"), "access") {
		t.Error("access shown with no registry and no policy")
	}
	put(t, filepath.Join(o.StoreDir, backend.FakePolicy), "age1x\n")
	want = Item{Warn, "access", "the store has an encryption policy but no recipients.yaml, so ezc cannot say who has access.",
		`Write recipients.yaml (README, "Access"), then run ezc store rewrap.`}
	if it := item(t, Collect(o), "store", "access"); it != want {
		t.Errorf("policy only: %+v", it)
	}
}

func TestRequestsItem(t *testing.T) {
	o := healthy(t)
	if slices.Contains(labels(Collect(o), "store"), "requests") {
		t.Error("requests shown with none")
	}
	o.Requests = []string{"ana"}
	want := Item{Warn, "requests", "1 access request pending: ana", "Run ezc user requests."}
	if it := item(t, Collect(o), "store", "requests"); it != want {
		t.Errorf("%+v", it)
	}
	o.Requests = []string{"ana", "bo"}
	if it := item(t, Collect(o), "store", "requests"); it.Detail != "2 access requests pending: ana, bo" {
		t.Errorf("%+v", it)
	}
	if Collect(o).Broken() {
		t.Error("requests alone must not fail")
	}
}

func TestRequestsItemCapsNames(t *testing.T) {
	o := healthy(t)
	o.Requests = []string{"a", "b", "c", "d", "e", "f", "g"}
	want := "7 access requests pending: a, b, c, d, e and 2 more"
	if it := item(t, Collect(o), "store", "requests"); it.Detail != want {
		t.Errorf("%q", it.Detail)
	}
}

func TestRecipientsItem(t *testing.T) {
	o := healthy(t)
	if slices.Contains(labels(Collect(o), "store"), "recipients") {
		t.Error("recipients shown with no registry")
	}
	o.HasRegistry, o.Recipients = true, 3
	if it := item(t, Collect(o), "store", "recipients"); it != (Item{OK, "recipients", "every file is encrypted for the 3 recipients", ""}) {
		t.Errorf("agree: %+v", it)
	}
	o.Differ = []string{"common.enc.yaml", "prod.enc.yaml"}
	want := Item{Broken, "recipients", "2 files are not encrypted for the registry: common.enc.yaml, prod.enc.yaml", "Run ezc user list and check who it names, then ezc store rewrap."}
	if it := item(t, Collect(o), "store", "recipients"); it != want {
		t.Errorf("differ: %+v", it)
	}
	if !Collect(o).Broken() {
		t.Error("a differing file must be broken")
	}
	o.Differ = []string{"bad\x1b[31mname.enc.yaml"}
	if it := item(t, Collect(o), "store", "recipients"); strings.Contains(it.Detail, "\x1b") {
		t.Errorf("name not made safe: %q", it.Detail)
	}
	o.Differ, o.DifferErr = nil, "common.enc.yaml: cannot read this file"
	if it := item(t, Collect(o), "store", "recipients"); it.State != Broken || it.Detail != o.DifferErr || it.Fix == "" {
		t.Errorf("error: %+v", it)
	}
}

func TestRotationItem(t *testing.T) {
	o := healthy(t)
	if slices.Contains(labels(Collect(o), "store"), "rotation") {
		t.Error("rotation shown with nothing due")
	}
	o.Due = 2
	want := Item{Warn, "rotation", "2 secrets were readable by a removed key", "ezc store status --due lists them; give each a new value with ezc set -g NAME, and --env for an environment's file."}
	if it := item(t, Collect(o), "store", "rotation"); it != want {
		t.Errorf("%+v", it)
	}
	if Collect(o).Broken() {
		t.Error("a due list alone must not fail")
	}
	o.Due, o.DueErr = 0, "cannot read rotation-due.yaml"
	if it := item(t, Collect(o), "store", "rotation"); it != (Item{Broken, "rotation", o.DueErr, ""}) {
		t.Errorf("unreadable: %+v", it)
	}
}

func TestGitItemVersionUnread(t *testing.T) {
	o := healthy(t)
	o.GitVersionUnread = true
	it := item(t, Collect(o), "machine", "git")
	if it.State != Warn || it.Detail != "installed, but its version could not be read" || Collect(o).Broken() {
		t.Errorf("%+v", it)
	}
}

func TestGitItemVersion(t *testing.T) {
	o := healthy(t)
	o.GitVersion = "2.22.1"
	want := Item{Broken, "git", "git 2.22.1 is too old; ezc needs 2.23 or later.", ""}
	if it := item(t, Collect(o), "machine", "git"); it != want {
		t.Errorf("%+v", it)
	}
	o.GitVersion = "2.23.0"
	if it := item(t, Collect(o), "machine", "git"); it.State != OK {
		t.Errorf("%+v", it)
	}
}
