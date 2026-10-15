package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/share"
)

const oldMise = `[env]
_.source = "{{ env.HOME }}/.local/share/fsconfig/env.sh"

[tasks."env:check"]
run = "fsconfig env"
`

// oldRepo is an ezc store holding A, and a repo in a folder named app whose .fs-config/ has two environments.
func oldRepo(t *testing.T) *world {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	w := newWorld(t, dir)
	store := filepath.Join(t.TempDir(), ".ezc")
	write(t, filepath.Join(store, "common.yaml"), "A: one\n")
	w.o.StoreEzc = store
	old := filepath.Join(dir, ".fs-config")
	write(t, filepath.Join(old, "manifest.yaml"), oldManifest)
	write(t, filepath.Join(old, "common.yaml"), "R: two\n")
	write(t, filepath.Join(old, "env.local.yaml"), "L: three\n")
	write(t, filepath.Join(old, "env.prod.yaml"), "P: four\n")
	encrypt(t, w.b, old, "common.enc.yaml", w.keys, "RS", "repo-secret")
	write(t, filepath.Join(dir, "mise.toml"), oldMise)
	w.tool.out["local"] = map[string]string{"A": "one", "R": "two", "RS": "repo-secret", "L": "three"}
	w.tool.out["prod"] = map[string]string{"A": "one", "R": "two", "RS": "repo-secret", "P": "four"}
	return w
}

func TestRepoMigrates(t *testing.T) {
	noTemp(t)
	w := oldRepo(t)
	dir := w.o.Dir
	names := func(d string) []string {
		entries, _ := os.ReadDir(d)
		var n []string
		for _, e := range entries {
			n = append(n, e.Name())
		}
		return n
	}
	oldNamesList := names(filepath.Join(dir, ".fs-config"))
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	if got, want := names(filepath.Join(dir, ".ezc")), append(oldNamesList, "env.sh"); !sameSet(got, want) {
		t.Errorf(".ezc holds %v, want %v", got, want)
	}
	if read(t, filepath.Join(dir, ".ezc", "env.sh")) != share.EnvSh {
		t.Error("env.sh is not the shim")
	}
	if _, err := os.Stat(filepath.Join(dir, ".fs-config")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".fs-config/ still there")
	}
	if got := read(t, filepath.Join(dir, "mise.toml")); got != rewriteMise(oldMise) {
		t.Errorf("mise.toml =\n%s", got)
	}
	out := w.out.String()
	for _, want := range []string{"Migrating the repo in " + dir, "✓ local: 4 names, identical", "✓ prod: 4 names, identical", "Next: ezc repo add, commit .ezc/ and mise.toml, and run ezc doctor."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "repo-secret") {
		t.Errorf("output holds a value:\n%s", out)
	}
	if len(w.tool.cmds) != 2 {
		t.Fatalf("old tool ran %d times", len(w.tool.cmds))
	}
	for i, env := range []string{"local", "prod"} {
		c := w.tool.cmds[i]
		if c.Dir != dir || !reflect.DeepEqual(c.Args, []string{"codegen", "shell", env}) {
			t.Errorf("command %d: dir %q args %v", i, c.Dir, c.Args)
		}
		if _, ok := envOf(c, "APP_ENV"); ok {
			t.Error("APP_ENV passed to the old tool")
		}
	}
}

func TestRepoNeedsTheEzcStore(t *testing.T) {
	w := oldRepo(t)
	w.o.StoreEzc = filepath.Join(t.TempDir(), "missing", ".ezc")
	err := Run(w.o)
	if err == nil || err.Error() != "Set up the ezc store first: run ezc migrate in the old store's folder, commit and push it, then ezc setup --store <it>." {
		t.Errorf("err = %v", err)
	}
	if len(w.tool.cmds) != 0 {
		t.Error("old tool ran")
	}
}

func TestRepoWithNoEnvironmentFile(t *testing.T) {
	w := oldRepo(t)
	old := filepath.Join(w.o.Dir, ".fs-config")
	os.Remove(filepath.Join(old, "env.local.yaml"))
	os.Remove(filepath.Join(old, "env.prod.yaml"))
	w.tool.out[""] = map[string]string{"A": "one"}
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	out := w.out.String()
	if !strings.Contains(out, "✓ (store): 1 names, identical") || !strings.Contains(out, "! with no environment named, ezc also loads this repo's common files; the old tool did not.") {
		t.Errorf("output:\n%s", out)
	}
	if !reflect.DeepEqual(w.tool.cmds[0].Args, []string{"codegen", "shell"}) {
		t.Errorf("args %v", w.tool.cmds[0].Args)
	}
}

func TestRepoProofDiffersInOneEnvironment(t *testing.T) {
	w := oldRepo(t)
	w.tool.out["prod"]["P"] = "five"
	before := snapshot(t, w.o.Dir)
	if err := Run(w.o); !errors.Is(err, ErrNotProved) {
		t.Fatalf("err = %v", err)
	}
	out := w.out.String()
	if !strings.Contains(out, "✓ local") || !strings.Contains(out, "✗ prod: P differs") || strings.Contains(out, "five") {
		t.Errorf("output:\n%s", out)
	}
	sameSnapshot(t, before, snapshot(t, w.o.Dir))
}

func TestRepoApplyFailurePutsBack(t *testing.T) {
	t.Run("no .ezc", func(t *testing.T) {
		w := oldRepo(t)
		dir := w.o.Dir
		before := snapshot(t, dir)
		w.o.afterProof = func() { os.Chmod(dir, 0o555) }
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		err := Run(w.o)
		os.Chmod(dir, 0o755)
		if err == nil || !strings.HasPrefix(err.Error(), "could not write .ezc/ (") || !strings.HasSuffix(err.Error(), "). Nothing was changed.") {
			t.Fatalf("err = %v", err)
		}
		sameSnapshot(t, before, snapshot(t, dir))
	})
	t.Run("no mise.toml", func(t *testing.T) {
		w := oldRepo(t)
		dir := w.o.Dir
		oldBefore := snapshot(t, filepath.Join(dir, ".fs-config"))
		w.o.afterProof = func() {
			os.Remove(filepath.Join(dir, "mise.toml"))
			write(t, filepath.Join(dir, "mise.toml", "inside"), "x")
		}
		if err := Run(w.o); err == nil {
			t.Fatal("no error")
		}
		if _, err := os.Stat(filepath.Join(dir, ".ezc")); !errors.Is(err, os.ErrNotExist) {
			t.Error(".ezc/ left behind")
		}
		sameSnapshot(t, oldBefore, snapshot(t, filepath.Join(dir, ".fs-config")))
	})
}

func TestDryRunWritesNothingInARepo(t *testing.T) {
	noTemp(t)
	w := oldRepo(t)
	write(t, filepath.Join(w.o.Dir, "README.md"), "Run fsconfig env first.\n")
	w.o.DryRun = true
	before := snapshot(t, w.o.Dir)
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	sameSnapshot(t, before, snapshot(t, w.o.Dir))
	out := strings.TrimSpace(w.out.String())
	for _, want := range []string{"would write .ezc/env.sh", "would rewrite mise.toml", "! README.md:1 names the old tool"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "mise.toml:") {
		t.Errorf("mise.toml reported though the rewrite fixes it:\n%s", out)
	}
	if !strings.HasSuffix(out, "Dry run: nothing was written.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestResolverErrorNamesEzcNotTheTemporaryFolder(t *testing.T) {
	noTemp(t)
	w := oldRepo(t)
	write(t, filepath.Join(w.o.Dir, ".fs-config", "common.yaml"), "APP_ENV: x\n")
	err := Run(w.o)
	if err == nil || !strings.Contains(err.Error(), ".ezc/common.yaml: APP_ENV names the environment") || strings.Contains(err.Error(), os.TempDir()) {
		t.Errorf("err = %v", err)
	}
}

func TestRepoCIFindingsFromTheStoreRegistry(t *testing.T) {
	w := oldRepo(t)
	k := fakeKeys(t, 2)
	write(t, filepath.Join(w.o.StoreEzc, "recipients.yaml"), `recipients:
  gene: {key: `+k[0]+`, kind: user, added: "2026-10-15", addedBy: migrate}
  ci: {key: `+k[1]+`, kind: bot, added: "2026-10-15", addedBy: migrate, repos: [owner/app]}
`)
	write(t, filepath.Join(w.o.Dir, ".github", "workflows", "ci.yml"), "env:\n  SOPS_AGE_KEY: ${{ secrets.SOPS_AGE_KEY }}\n")
	if err := Run(w.o); err != nil {
		t.Fatalf("Run: %v\n%s", err, w.out)
	}
	if !strings.Contains(w.out.String(), "! .github/workflows/ci.yml passes SOPS_AGE_KEY to CI; ezc reads EZC_AGE_KEY. Run ezc bot rotate ci, then see docs/ci.md.") {
		t.Errorf("output:\n%s", w.out)
	}
}
