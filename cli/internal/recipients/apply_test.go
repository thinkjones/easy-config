package recipients

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m[e.Name()] = string(b)
	}
	return m
}

func twoKeys() (Registry, Registry) {
	old := Registry{Recipients: map[string]Recipient{"a": {Key: "fake1aa", Kind: KindUser}}}
	next := Registry{Recipients: map[string]Recipient{
		"a": {Key: "fake1aa", Kind: KindUser},
		"b": {Key: "fake1bb", Kind: KindUser},
	}}
	return old, next
}

// store makes a folder with a registry, a policy and two encrypted files for old.
func store(t *testing.T, b *backend.Fake, old Registry) string {
	t.Helper()
	dir := t.TempDir()
	if err := old.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := b.WritePolicy(dir, old.Keys()); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"common.enc.yaml", "dev.enc.yaml"} {
		if err := b.Set(filepath.Join(dir, f), "N", "v"); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var encFiles = []string{"common.enc.yaml", "dev.enc.yaml"}

func TestApplySuccess(t *testing.T) {
	b := &backend.Fake{}
	old, next := twoKeys()
	dir := store(t, b, old)
	if err := Apply(dir, next, b, encFiles); err != nil {
		t.Fatal(err)
	}
	for _, f := range encFiles {
		got, _ := b.Recipients(filepath.Join(dir, f))
		if !reflect.DeepEqual(got, next.Keys()) {
			t.Errorf("%s recipients = %v", f, got)
		}
	}
	r, found, err := Load(dir)
	if err != nil || !found || len(r.Recipients) != 2 {
		t.Errorf("registry = %v %v %v", r, found, err)
	}
}

func TestApplyRestoresOnRewrapFailure(t *testing.T) {
	b := &backend.Fake{RewrapFail: "dev.enc.yaml"}
	old, next := twoKeys()
	dir := store(t, b, old)
	before := snapshot(t, dir)
	err := Apply(dir, next, b, encFiles)
	if err == nil || !strings.Contains(err.Error(), "dev.enc.yaml") {
		t.Fatalf("err = %v", err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("folder changed:\nbefore %v\nafter  %v", before, after)
	}
}

// liar re-wraps nothing, so the recipients it reports differ from the registry.
type liar struct{ *backend.Fake }

func (l liar) Rewrap(string) error { return nil }

func TestApplyRestoresOnMismatch(t *testing.T) {
	b := &backend.Fake{}
	old, next := twoKeys()
	dir := store(t, b, old)
	before := snapshot(t, dir)
	err := Apply(dir, next, liar{b}, encFiles)
	if err == nil || !strings.Contains(err.Error(), "common.enc.yaml") {
		t.Fatalf("err = %v", err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("folder changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestApplyNoFiles(t *testing.T) {
	b := &backend.Fake{}
	_, next := twoKeys()
	dir := t.TempDir()
	if err := Apply(dir, next, b, nil); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := Load(dir); !found {
		t.Error("registry not written")
	}
	if ok, _ := b.HasPolicy(dir); !ok {
		t.Error("policy not written")
	}
}

func TestApplyRemovesWhatItCreated(t *testing.T) {
	b := &backend.Fake{RewrapFail: "common.enc.yaml"}
	_, next := twoKeys()
	dir := t.TempDir()
	if err := b.Set(filepath.Join(dir, "x"), "N", "v"); err == nil {
		t.Fatal("expected no policy")
	}
	if err := b.WritePolicy(dir, []string{"fake1aa"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(filepath.Join(dir, "common.enc.yaml"), "N", "v"); err != nil {
		t.Fatal(err)
	}
	// no registry yet: a failed Apply must not leave one
	if err := Apply(dir, next, b, []string{"common.enc.yaml"}); err == nil {
		t.Fatal("expected failure")
	}
	if _, found, _ := Load(dir); found {
		t.Error("registry left behind")
	}
}

func TestDifferences(t *testing.T) {
	b := &backend.Fake{}
	old, next := twoKeys()
	dir := store(t, b, old) // files hold fake1aa
	// the registry now has a and b; add a file that holds an unregistered key instead of a
	odd := filepath.Join(dir, "odd.enc.yaml")
	if err := os.WriteFile(odd, []byte("# recipients: fake1bb,fake1zz\nN: FAKE[dg==]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Differences(dir, next, b, append([]string{"odd.enc.yaml"}, encFiles...))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Diff{
		"common.enc.yaml": {Missing: []string{"b"}},
		"dev.enc.yaml":    {Missing: []string{"b"}},
		"odd.enc.yaml":    {Missing: []string{"a"}, Extra: []string{"fake1zz"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Differences = %v", got)
	}
	same, err := Differences(dir, old, b, encFiles)
	if err != nil || len(same) != 0 {
		t.Errorf("matching registry = %v, %v", same, err)
	}
}

// failAfter writes through the real fake, then fails the step named.
type failAfter struct {
	*backend.Fake
	policy bool // WritePolicy writes, then fails
	lie    bool // Rewrap does nothing
	hook   func()
}

func (f failAfter) WritePolicy(dir string, keys []string) error {
	if err := f.Fake.WritePolicy(dir, keys); err != nil {
		return err
	}
	if f.policy {
		return errors.New("policy failed after writing")
	}
	return nil
}

func (f failAfter) Rewrap(path string) error {
	if f.hook != nil {
		f.hook()
	}
	if f.lie {
		return nil
	}
	return f.Fake.Rewrap(path)
}

// bareFolder holds two encrypted files, with no registry and no policy file.
func bareFolder(t *testing.T) (string, *backend.Fake) {
	t.Helper()
	b := &backend.Fake{}
	dir := t.TempDir()
	if err := b.WritePolicy(dir, []string{"fake1aa"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range encFiles {
		if err := b.Set(filepath.Join(dir, f), "N", "v"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(dir, backend.FakePolicy)); err != nil {
		t.Fatal(err)
	}
	return dir, b
}

func TestApplyRemovesCreatedFiles(t *testing.T) {
	_, next := twoKeys()
	steps := map[string]func(b *backend.Fake) Backend{
		"policy":    func(b *backend.Fake) Backend { return failAfter{Fake: b, policy: true} },
		"rewrap":    func(b *backend.Fake) Backend { b.RewrapFail = "dev.enc.yaml"; return b },
		"mismatch":  func(b *backend.Fake) Backend { return failAfter{Fake: b, lie: true} },
		"first one": func(b *backend.Fake) Backend { b.RewrapFail = "common.enc.yaml"; return b },
	}
	for label, mk := range steps {
		dir, b := bareFolder(t)
		before := snapshot(t, dir)
		if err := Apply(dir, next, mk(b), encFiles); err == nil {
			t.Errorf("%s: no error", label)
		}
		if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
			t.Errorf("%s: folder changed:\nbefore %v\nafter  %v", label, before, after)
		}
	}
}

func TestApplySaveFailureLeavesFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	_, next := twoKeys()
	dir, b := bareFolder(t)
	before := snapshot(t, dir)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if err := Apply(dir, next, b, encFiles); err == nil {
		t.Fatal("expected failure")
	}
	os.Chmod(dir, 0o755)
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("folder changed")
	}
}

func TestApplyReportsAFailedRollback(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	_, next := twoKeys()
	dir, b := bareFolder(t)
	// The folder turns read-only once re-wrapping starts, so the registry Apply created cannot be removed.
	bk := failAfter{Fake: b, lie: true, hook: func() { os.Chmod(dir, 0o500) }}
	defer os.Chmod(dir, 0o755)
	err := Apply(dir, next, bk, encFiles)
	if err == nil || !strings.Contains(err.Error(), "could not restore "+File) || !strings.Contains(err.Error(), "common.enc.yaml") {
		t.Fatalf("err = %v", err)
	}
}
