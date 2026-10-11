// Package backendtest holds the contract every backend.Backend must pass.
package backendtest

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
)

// Env is one backend under test in a fresh folder that has a policy, and a way to make it keyless.
type Env struct {
	Backend     backend.Backend
	Dir         string // a folder with the backend's policy in place
	Lock        func() // after this, the backend has no usable key
	NoPolicyDir string // a folder with no policy
}

func decrypted(t *testing.T, b backend.Backend, path string) []backend.Pair {
	t.Helper()
	got, err := b.Decrypt(path)
	if err != nil {
		t.Fatalf("Decrypt(%s): %v", path, err)
	}
	return got
}

func set(t *testing.T, b backend.Backend, path, name, value string) {
	t.Helper()
	if err := b.Set(path, name, value); err != nil {
		t.Fatalf("Set(%s, %s): %v", path, name, err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func wantNames(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

// Run exercises the behaviour every Backend must have. Each subtest gets a fresh Env from newEnv.
func Run(t *testing.T, newEnv func(t *testing.T) Env) {
	run := func(name string, f func(t *testing.T, e Env, file string)) {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			f(t, e, filepath.Join(e.Dir, "common.enc.yaml"))
		})
	}

	run("SetCreatesTheFile", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "TOKEN", "t0k")
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("file not created: %v", err)
		}
		if got, want := decrypted(t, e.Backend, file), []backend.Pair{{Name: "TOKEN", Value: "t0k"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("Decrypt = %v, want %v", got, want)
		}
	})

	run("FileIsNotPlaintext", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "TOKEN", "t0k")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("t0k")) {
			t.Error("the file holds the value in plaintext")
		}
		if !bytes.Contains(data, []byte("TOKEN")) {
			t.Error("the file does not hold the name")
		}
	})

	run("SetKeepsOrderAndOthers", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		set(t, e.Backend, file, "B", "b1")
		set(t, e.Backend, file, "C", "c1")
		set(t, e.Backend, file, "B", "b2")
		want := []backend.Pair{{Name: "A", Value: "a1"}, {Name: "B", Value: "b2"}, {Name: "C", Value: "c1"}}
		if got := decrypted(t, e.Backend, file); !reflect.DeepEqual(got, want) {
			t.Errorf("Decrypt = %v, want %v", got, want)
		}
	})

	run("ExactText", func(t *testing.T, e Env, file string) {
		values := []string{
			"08540", "1.20", "yes", "null", "!x", "&a b", " lead", "trail ", "a: b", "# h",
			`it's "q"`, "$(id)", "a\nb\nc", "", strings.Repeat("x", 4000), "héllo ✓",
		}
		for _, v := range values {
			set(t, e.Backend, file, "V", v)
			got := decrypted(t, e.Backend, file)
			if len(got) != 1 || got[0].Value != v {
				t.Errorf("value %q came back as %v", v, got)
			}
		}
	})

	run("InspectNeedsNoKey", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		set(t, e.Backend, file, "B", "b1")
		e.Lock()
		names, unencrypted, err := e.Backend.Inspect(file)
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		wantNames(t, names, []string{"A", "B"})
		wantNames(t, unencrypted, nil)
	})

	run("DecryptWithoutKey", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "s3cr3t-value")
		e.Lock()
		_, err := e.Backend.Decrypt(file)
		if !errors.Is(err, backend.ErrNoKey) {
			t.Fatalf("error = %v, want ErrNoKey", err)
		}
		if !strings.Contains(err.Error(), file) {
			t.Errorf("error %q does not name the file", err)
		}
		if strings.Contains(err.Error(), "s3cr3t-value") {
			t.Errorf("error %q contains the value", err)
		}
	})

	run("SetWithoutPolicy", func(t *testing.T, e Env, _ string) {
		file := filepath.Join(e.NoPolicyDir, "common.enc.yaml")
		err := e.Backend.Set(file, "A", "v")
		if !errors.Is(err, backend.ErrNoPolicy) {
			t.Fatalf("error = %v, want ErrNoPolicy", err)
		}
		if !strings.Contains(err.Error(), file) {
			t.Errorf("error %q does not name the file", err)
		}
		if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
			t.Errorf("a file was created in the folder with no policy (stat: %v)", statErr)
		}
	})

	run("HasPolicy", func(t *testing.T, e Env, _ string) {
		for dir, want := range map[string]bool{e.Dir: true, e.NoPolicyDir: false} {
			got, err := e.Backend.HasPolicy(dir)
			if err != nil {
				t.Fatalf("HasPolicy(%s): %v", dir, err)
			}
			if got != want {
				t.Errorf("HasPolicy(%s) = %v, want %v", dir, got, want)
			}
		}
	})

	run("UnsetRemovesOne", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		set(t, e.Backend, file, "B", "b1")
		if err := e.Backend.Unset(file, "A"); err != nil {
			t.Fatalf("Unset: %v", err)
		}
		if got, want := decrypted(t, e.Backend, file), []backend.Pair{{Name: "B", Value: "b1"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("Decrypt = %v, want %v", got, want)
		}
	})

	run("UnsetLastDeletesFile", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		if err := e.Backend.Unset(file, "A"); err != nil {
			t.Fatalf("Unset: %v", err)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("file still exists (stat: %v)", err)
		}
	})

	run("UnsetLastNeedsNoKey", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		e.Lock()
		if err := e.Backend.Unset(file, "A"); err != nil {
			t.Fatalf("Unset: %v", err)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("file still exists (stat: %v)", err)
		}
	})

	run("UnsetOneOfSeveralWithoutKey", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		set(t, e.Backend, file, "B", "b1")
		before, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		e.Lock()
		if err := e.Backend.Unset(file, "A"); !errors.Is(err, backend.ErrNoKey) {
			t.Fatalf("error = %v, want ErrNoKey", err)
		}
		if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
			t.Error("the file changed")
		}
	})

	run("UnsetMissingName", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		before, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		err = e.Backend.Unset(file, "NOPE")
		if !errors.Is(err, backend.ErrNotSet) {
			t.Fatalf("error = %v, want ErrNotSet", err)
		}
		if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
			t.Error("the file changed")
		}
	})

	run("UnsetMissingFile", func(t *testing.T, e Env, file string) {
		err := e.Backend.Unset(file, "A")
		if !errors.Is(err, backend.ErrNotSet) {
			t.Fatalf("error = %v, want ErrNotSet", err)
		}
	})

	run("SetFailureLeavesFile", func(t *testing.T, e Env, file string) {
		set(t, e.Backend, file, "A", "a1")
		before, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		entries := dirNames(t, e.Dir)
		e.Lock()
		if err := e.Backend.Set(file, "A", "new"); !errors.Is(err, backend.ErrNoKey) {
			t.Fatalf("error = %v, want ErrNoKey", err)
		}
		if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
			t.Error("the file changed")
		}
		if got := dirNames(t, e.Dir); !reflect.DeepEqual(got, entries) {
			t.Errorf("folder holds %v, want %v (a stray temporary file?)", got, entries)
		}
	})

	run("Check", func(t *testing.T, e Env, _ string) {
		if err := e.Backend.Check(); err != nil {
			t.Errorf("Check: %v", err)
		}
	})

	run("NewKeyIsFresh", func(t *testing.T, e Env, _ string) {
		a, _, err := e.Backend.NewKey()
		if err != nil {
			t.Fatalf("NewKey: %v", err)
		}
		b, _, err := e.Backend.NewKey()
		if err != nil {
			t.Fatalf("NewKey: %v", err)
		}
		if a == "" || a == b {
			t.Errorf("two keys have public halves %q and %q", a, b)
		}
	})

	run("PublicKeyOfWrittenKey", func(t *testing.T, e Env, _ string) {
		pub, private, err := e.Backend.NewKey()
		if err != nil {
			t.Fatalf("NewKey: %v", err)
		}
		keyFile := filepath.Join(t.TempDir(), "keys.txt")
		if err := os.WriteFile(keyFile, private, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := e.Backend.PublicKey(keyFile)
		if err != nil {
			t.Fatalf("PublicKey: %v", err)
		}
		if got != pub {
			t.Errorf("PublicKey = %q, want %q", got, pub)
		}
	})

	run("PublicKeyWithoutKey", func(t *testing.T, e Env, _ string) {
		dir := t.TempDir()
		empty := filepath.Join(dir, "empty.txt")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{filepath.Join(dir, "missing.txt"), empty} {
			_, err := e.Backend.PublicKey(f)
			if !errors.Is(err, backend.ErrNoKey) {
				t.Fatalf("PublicKey(%s) error = %v, want ErrNoKey", f, err)
			}
			if !strings.Contains(err.Error(), f) {
				t.Errorf("error %q does not name the file", err)
			}
		}
	})

	run("WritePolicyNeedsRecipients", func(t *testing.T, e Env, _ string) {
		if err := e.Backend.WritePolicy(e.NoPolicyDir, nil); err == nil {
			t.Error("WritePolicy with no recipients succeeded")
		}
		if ok, _ := e.Backend.HasPolicy(e.NoPolicyDir); ok {
			t.Error("a policy was written for no recipients")
		}
	})

	run("WritePolicy", func(t *testing.T, e Env, _ string) {
		a, _, err := e.Backend.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := e.Backend.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		dir := e.NoPolicyDir
		before := dirNames(t, dir)
		if err := e.Backend.WritePolicy(dir, []string{a}); err != nil {
			t.Fatalf("WritePolicy: %v", err)
		}
		if ok, err := e.Backend.HasPolicy(dir); err != nil || !ok {
			t.Fatalf("HasPolicy = %v, %v, want true", ok, err)
		}
		// The policy is whatever WritePolicy added to the folder: the contract does not know a backend's file names.
		policy := func() []byte {
			var found []byte
			for _, n := range dirNames(t, dir) {
				if !slices.Contains(before, n) {
					data, err := os.ReadFile(filepath.Join(dir, n))
					if err != nil {
						t.Fatal(err)
					}
					found = append(found, data...)
				}
			}
			return found
		}
		first := policy()
		if err := e.Backend.WritePolicy(dir, []string{a}); err != nil {
			t.Fatalf("WritePolicy again: %v", err)
		}
		if !bytes.Equal(first, policy()) {
			t.Error("writing the same recipients twice changed the policy")
		}
		if err := e.Backend.WritePolicy(dir, []string{b}); err != nil {
			t.Fatalf("WritePolicy second list: %v", err)
		}
		second := policy()
		if bytes.Equal(first, second) || bytes.Contains(second, []byte(a)) || !bytes.Contains(second, []byte(b)) {
			t.Error("the second list did not replace the first")
		}
	})
}
