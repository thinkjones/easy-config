package migrate

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOldNames(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "README.md"), "x\nuse fsconfig\nx\nx\nx\nx\nx\nx\nthe .fs-config/ folder\n")
	write(t, filepath.Join(root, "src", "env.py"), "FsConfig\nfsconfig\nfsconfig\nfsconfig\n")
	write(t, filepath.Join(root, "node_modules", "x.js"), "fsconfig\n")
	write(t, filepath.Join(root, ".git", "config"), "fsconfig\n")
	write(t, filepath.Join(root, ".fs-config", "a.yaml"), "fsconfig\n")
	write(t, filepath.Join(root, "big.txt"), "fsconfig\n"+strings.Repeat("x", 1<<20))
	write(t, filepath.Join(root, "bin.dat"), "fsconfig\x00\n")
	write(t, filepath.Join(root, "clean.md"), "nothing here\n")
	got := oldNames(root, nil)
	want := []string{"! README.md:2,9 names the old tool", "! src/env.py:1,2,3 names the old tool"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	got = oldNames(root, map[string]string{"README.md": "clean\n"})
	if !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("with override, got %q", got)
	}
}

func TestOldNamesStopsAtTwenty(t *testing.T) {
	root := t.TempDir()
	for i := range 25 {
		write(t, filepath.Join(root, fmt.Sprintf("f%02d.md", i)), "fsconfig\n")
	}
	got := oldNames(root, nil)
	if len(got) != 21 || got[20] != "! and 5 more files" || got[0] != "! f00.md:1 names the old tool" {
		t.Errorf("got %d lines: %q", len(got), got)
	}
}

func TestCIFindings(t *testing.T) {
	root := filepath.Join(t.TempDir(), "b")
	write(t, filepath.Join(root, ".github", "workflows", "a.yml"), "env:\n  SOPS_AGE_KEY: x\n")
	write(t, filepath.Join(root, ".github", "workflows", "clean.yml"), "on: push\n")
	got := ciFindings(root, "SOPS_AGE_KEY", map[string][]string{"ci": {"owner/a", "owner/b"}})
	want := []string{"! .github/workflows/a.yml passes SOPS_AGE_KEY to CI; ezc reads EZC_AGE_KEY. Run ezc bot rotate ci, then see docs/ci.md."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	got = ciFindings(root, "SOPS_AGE_KEY", nil)
	want = []string{"! .github/workflows/a.yml passes SOPS_AGE_KEY to CI; ezc reads EZC_AGE_KEY. Run ezc bot add <name> --repo <owner/repo>, then see docs/ci.md."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	if got := ciFindings(t.TempDir(), "SOPS_AGE_KEY", nil); len(got) != 0 {
		t.Errorf("no .github: %q", got)
	}
}
