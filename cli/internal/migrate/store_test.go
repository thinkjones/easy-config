package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/registry"
)

const oldManifest = `# the catalogue
$schema: https://example.com/old.json
schemaVersion: v1

required:
  A:
    type: str
    desc: a
`

// oldStore is an old store in a new folder: A and B in plain text, S encrypted.
func oldStore(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := newWorld(t, dir)
	old := filepath.Join(dir, ".fs-config")
	write(t, filepath.Join(old, "global.yaml"), "# plain\nA: one\nB: ~/x\n")
	write(t, filepath.Join(old, "manifest.yaml"), oldManifest)
	write(t, filepath.Join(old, "recipients.yaml"), oldRecipients(w.keys[0], w.keys[1]))
	write(t, filepath.Join(old, "notes.txt"), "kept\n")
	encrypt(t, w.b, old, "global.enc.yaml", w.keys, "S", "s3cret-value")
	w.tool.out[""] = map[string]string{"A": "one", "B": w.o.Home + "/x", "S": "s3cret-value"}
	return w
}

func TestStoreMigrates(t *testing.T) {
	noTemp(t)
	w := oldStore(t)
	dir := w.o.Dir
	old, ezc := filepath.Join(dir, ".fs-config"), filepath.Join(dir, ".ezc")
	before := snapshot(t, old)
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	out := w.out.String()
	if read(t, filepath.Join(ezc, "common.yaml")) != read(t, filepath.Join(old, "global.yaml")) {
		t.Error("common.yaml differs from global.yaml")
	}
	if read(t, filepath.Join(ezc, "common.enc.yaml")) != read(t, filepath.Join(old, "global.enc.yaml")) {
		t.Error("common.enc.yaml differs from global.enc.yaml")
	}
	if got, want := read(t, filepath.Join(ezc, "manifest.yaml")), strings.Replace(oldManifest, "https://example.com/old.json", "https://example.com/new.json", 1); got != want {
		t.Errorf("manifest.yaml =\n%s", got)
	}
	reg, found, err := recipients.Load(ezc)
	if err != nil || !found || len(reg.Recipients) != 2 {
		t.Errorf("recipients: %v %v %v", reg, found, err)
	}
	repos, found, err := registry.Load(ezc)
	if err != nil || !found || !reflect.DeepEqual(repos.IDs(), []string{"owner/a", "owner/b"}) {
		t.Errorf("repos: %v %v %v", repos, found, err)
	}
	if read(t, filepath.Join(ezc, "notes.txt")) != "kept\n" {
		t.Error("notes.txt not copied")
	}
	policy, err := w.b.PolicyRecipients(filepath.Join(ezc, "common.enc.yaml"))
	keys := reg.Keys()
	slices.Sort(policy)
	slices.Sort(keys)
	if err != nil || !reflect.DeepEqual(policy, keys) {
		t.Errorf("policy %v, %v; want %v", policy, err, keys)
	}
	for _, name := range []string{"global.yaml", "global.enc.yaml"} {
		if _, err := os.Stat(filepath.Join(ezc, name)); err == nil {
			t.Errorf(".ezc/%s exists", name)
		}
	}
	sameSnapshot(t, before, snapshot(t, old))
	for _, want := range []string{"Migrating the store in " + dir, "✓ (store): 3 names, identical", "+ .ezc/common.enc.yaml", "Next: "} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret-value") || strings.Contains(out, "boom") {
		t.Errorf("output holds a value:\n%s", out)
	}
	if strings.Contains(out, "is not encrypted for exactly") {
		t.Errorf("recipients reported as differing:\n%s", out)
	}
	if len(w.tool.cmds) != 1 {
		t.Fatalf("old tool ran %d times", len(w.tool.cmds))
	}
	c := w.tool.cmds[0]
	if !reflect.DeepEqual(c.Args, []string{"codegen", "shell"}) {
		t.Errorf("args %v", c.Args)
	}
	if v, _ := envOf(c, "FS_CONFIG_HOME"); v != old {
		t.Errorf("FS_CONFIG_HOME = %q", v)
	}
	if _, ok := envOf(c, "APP_ENV"); ok {
		t.Error("APP_ENV passed to the old tool")
	}
	if c.Dir == "" || strings.HasPrefix(c.Dir, dir) {
		t.Errorf("old tool ran in %q", c.Dir)
	}
}

func TestStoreProofDiffers(t *testing.T) {
	noTemp(t)
	w := oldStore(t)
	w.tool.out[""]["S"] = "other-secret"
	w.tool.out[""]["Z"] = "zed-value"
	err := Run(w.o)
	if !errors.Is(err, ErrNotProved) {
		t.Fatalf("err = %v", err)
	}
	out := w.out.String()
	for _, want := range []string{"✗ (store): S differs", "✗ (store): Z only before", "Nothing was changed."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, never := range []string{"s3cret-value", "other-secret", "zed-value"} {
		if strings.Contains(out, never) {
			t.Errorf("output holds %q", never)
		}
	}
	if _, err := os.Stat(filepath.Join(w.o.Dir, ".ezc")); err == nil {
		t.Error(".ezc/ written")
	}
}

func TestStoreProofOnlyAfter(t *testing.T) {
	w := oldStore(t)
	delete(w.tool.out[""], "A")
	if err := Run(w.o); !errors.Is(err, ErrNotProved) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(w.out.String(), "✗ (store): A only after") {
		t.Errorf("output:\n%s", w.out)
	}
}

func TestDryRunWritesNothingInAStore(t *testing.T) {
	noTemp(t)
	w := oldStore(t)
	w.o.DryRun = true
	before := snapshot(t, w.o.Dir)
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	sameSnapshot(t, before, snapshot(t, w.o.Dir))
	out := strings.TrimSpace(w.out.String())
	if !strings.HasSuffix(out, "Dry run: nothing was written.") || !strings.Contains(out, "would write .ezc/common.yaml") || !strings.Contains(out, "✓ (store)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestOldToolFailureRefuses(t *testing.T) {
	cases := []struct {
		name string
		set  func(w *world)
		want string
	}{
		{"missing", func(w *world) {
			w.o.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		}, "fsconfig is not on PATH; the proof runs it to compose the environment as it stands."},
		{"fails", func(w *world) { w.tool.fail = exitErr(1) },
			"fsconfig codegen shell failed in DIR (exit status 1). To see why, run FS_CONFIG_HOME=DIR/.fs-config fsconfig codegen shell >/dev/null in an empty folder."},
		{"garbage", func(w *world) { w.tool.print = "oops\n" },
			"the old tool printed something that is not an export line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			noTemp(t)
			w := oldStore(t)
			c.set(w)
			err := Run(w.o)
			if err == nil {
				t.Fatal("no error")
			}
			if want := strings.ReplaceAll(c.want, "DIR", w.o.Dir); err.Error() != want {
				t.Errorf("error %q, want %q", err, want)
			}
			if strings.Contains(err.Error()+w.out.String(), "boom") || strings.Contains(err.Error()+w.out.String(), "s3cret-value") {
				t.Error("stderr or a value passed through")
			}
			if _, err := os.Stat(filepath.Join(w.o.Dir, ".ezc")); err == nil {
				t.Error(".ezc/ written")
			}
		})
	}
}

func TestRefusals(t *testing.T) {
	w := newWorld(t, t.TempDir())
	if err := Run(w.o); err == nil || err.Error() != "Nothing to migrate here: no .fs-config/ folder." {
		t.Errorf("no .fs-config: %v", err)
	}
	w = oldStore(t)
	write(t, filepath.Join(w.o.Dir, ".ezc", "x"), "")
	if err := Run(w.o); err == nil || err.Error() != w.o.Dir+" already has .ezc/. Nothing was changed." {
		t.Errorf(".ezc present: %v", err)
	}
	if len(w.tool.cmds) != 0 {
		t.Error("old tool ran")
	}
}

func TestStoreRefusesAFolderInside(t *testing.T) {
	w := oldStore(t)
	if err := os.Mkdir(filepath.Join(w.o.Dir, ".fs-config", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Run(w.o)
	if err == nil || !strings.Contains(err.Error(), "sub in .fs-config/ is not a plain file") {
		t.Errorf("err = %v", err)
	}
}

func TestStoreRecipientsDiffer(t *testing.T) {
	w := oldStore(t)
	other := fakeKeys(t, 1)
	old := filepath.Join(w.o.Dir, ".fs-config")
	os.Remove(filepath.Join(old, "global.enc.yaml"))
	encrypt(t, w.b, old, "global.enc.yaml", []string{w.keys[0], other[0]}, "S", "s3cret-value")
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	if !strings.Contains(w.out.String(), "! common.enc.yaml is not encrypted for exactly recipients.yaml's keys; run ezc store rewrap once the store is set up.") {
		t.Errorf("output:\n%s", w.out)
	}
}

var _ = backend.FakePolicy

func TestStagingErrorNamesEzcNotTheTemporaryFolder(t *testing.T) {
	noTemp(t)
	w := oldStore(t)
	os.Chmod(filepath.Join(w.o.Dir, ".fs-config", "manifest.yaml"), 0o444)
	err := Run(w.o)
	if err == nil || err.Error() != "could not write .ezc/manifest.yaml (permission denied)." {
		t.Errorf("err = %v", err)
	}
}

func TestOldToolHasATimeLimit(t *testing.T) {
	noTemp(t)
	w := oldStore(t)
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	if c := w.tool.cmds[0]; c.Timeout != oldToolTimeout || oldToolTimeout <= 0 {
		t.Errorf("timeout %v", c.Timeout)
	}
	w = oldStore(t)
	w.tool.fail = proc.ErrTimeout
	err := Run(w.o)
	if err == nil || !strings.Contains(err.Error(), "(timed out)") {
		t.Errorf("err = %v", err)
	}
}
