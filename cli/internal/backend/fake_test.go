package backend_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/backend/backendtest"
)

func TestFakeContract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) backendtest.Env {
		f := &backend.Fake{}
		dir, bare := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, backend.FakePolicy), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		public, private, err := f.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		return backendtest.Env{Backend: f, Dir: dir, NoPolicyDir: bare, Lock: func() { f.Locked = true }, Public: public, Private: private}
	})
}

func TestFakeMissing(t *testing.T) {
	f := &backend.Fake{Missing: true}
	if err := f.Check(); !errors.Is(err, backend.ErrNoTool) {
		t.Fatalf("Check = %v, want ErrNoTool", err)
	}
}

func TestFakeInspectReportsUnencrypted(t *testing.T) {
	file := filepath.Join(t.TempDir(), "common.enc.yaml")
	if err := os.WriteFile(file, []byte("A: plain\nB: FAKE[dg==]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	names, unencrypted, err := (&backend.Fake{}).Inspect(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A", "B"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	if want := []string{"A"}; !reflect.DeepEqual(unencrypted, want) {
		t.Errorf("unencrypted = %v, want %v", unencrypted, want)
	}
}

func TestFakeEdit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, backend.FakePolicy), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "common.enc.yaml")
	f := &backend.Fake{}
	if err := f.Set(file, "A", "a1"); err != nil {
		t.Fatal(err)
	}
	if err := f.Edit(file, "ed"); err != nil {
		t.Fatalf("Edit with no EditFunc: %v", err)
	}
	f.EditFunc = func(p []backend.Pair) []backend.Pair { return append(p, backend.Pair{Name: "B", Value: "b1"}) }
	if err := f.Edit(file, "ed"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	got, err := f.Decrypt(file)
	if err != nil {
		t.Fatal(err)
	}
	want := []backend.Pair{{Name: "A", Value: "a1"}, {Name: "B", Value: "b1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decrypt = %v, want %v", got, want)
	}
	f.EditFunc = func([]backend.Pair) []backend.Pair { return nil }
	if err := f.Edit(file, "ed"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("an empty edit should delete the file (stat: %v)", err)
	}
}

func TestFakeEditCreatesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "common.enc.yaml")
	f := &backend.Fake{}
	if err := f.Edit(file, "ed"); !errors.Is(err, backend.ErrNoPolicy) {
		t.Fatalf("no policy: got %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, backend.FakePolicy), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var seen []backend.Pair
	f.EditFunc = func(p []backend.Pair) []backend.Pair { seen = p; return nil }
	if err := f.Edit(file, "ed"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Errorf("a missing file should start from no pairs, got %v", seen)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("an empty result creates nothing (stat: %v)", err)
	}
	f.EditFunc = func([]backend.Pair) []backend.Pair { return []backend.Pair{{Name: "A", Value: "a1"}} }
	if err := f.Edit(file, "ed"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Decrypt(file); err != nil || !reflect.DeepEqual(got, []backend.Pair{{Name: "A", Value: "a1"}}) {
		t.Errorf("Decrypt = %v, %v", got, err)
	}
	f.Locked = true
	if err := f.Edit(file, "ed"); !errors.Is(err, backend.ErrNoKey) {
		t.Errorf("locked: got %v", err)
	}
}

func TestFakeRefusesAFileWithAnUnencryptedLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, backend.FakePolicy), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "common.enc.yaml")
	content := []byte("A: plain\nB: FAKE[dg==]\n")
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &backend.Fake{EditFunc: func(p []backend.Pair) []backend.Pair { return p }}
	ops := map[string]func() error{
		"Decrypt": func() error { _, err := f.Decrypt(file); return err },
		"Set":     func() error { return f.Set(file, "B", "v") },
		"Unset":   func() error { return f.Unset(file, "A") },
		"Edit":    func() error { return f.Edit(file, "ed") },
	}
	for name, op := range ops {
		err := op()
		if err == nil || errors.Is(err, backend.ErrNoKey) || !strings.Contains(err.Error(), file) || strings.Contains(err.Error(), "plain") {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if got, _ := os.ReadFile(file); string(got) != string(content) {
		t.Errorf("the file changed: %q", got)
	}
}

func TestFakeRewrapFail(t *testing.T) {
	f := &backend.Fake{}
	dir := t.TempDir()
	public, _, _ := f.NewKey()
	if err := f.WritePolicy(dir, []string{public}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.enc.yaml", "b.enc.yaml"} {
		if err := f.Set(filepath.Join(dir, name), "X", "v"); err != nil {
			t.Fatal(err)
		}
	}
	f.RewrapFail = "b.enc.yaml"
	if err := f.Rewrap(filepath.Join(dir, "a.enc.yaml")); err != nil {
		t.Errorf("Rewrap of another file: %v", err)
	}
	if err := f.Rewrap(filepath.Join(dir, "b.enc.yaml")); err == nil {
		t.Error("Rewrap of the failing file succeeded")
	}
}
