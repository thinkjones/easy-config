package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

// dotenvFile writes a .env file outside .ezc and returns its path.
func dotenvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env.prod")
	write(t, path, content)
	return path
}

func TestEnvImportSortsBySecret(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	path := dotenvFile(t, "# prod\nNAME=alice\nexport SIZE=3\nTOKEN=\"t0k$en\"\n")
	if err := f.run("env", "import", "prod", path); err != nil {
		t.Fatal(err)
	}
	want := "set NAME in repo/env.prod.yaml\nset SIZE in repo/env.prod.yaml\nset TOKEN in repo/env.prod.enc.yaml\n"
	if got := f.out.String(); got != want {
		t.Fatalf("got %q", got)
	}
	if got := readFile(t, repoFile(f, "env.prod.yaml")); got != "NAME: alice\nSIZE: \"3\"\n" && got != "NAME: alice\nSIZE: 3\n" {
		t.Fatalf("got %q", got)
	}
	if v, ok := stored(t, f, "env.prod.enc.yaml", "TOKEN"); !ok || v != "t0k$en" {
		t.Fatalf("got %q, %v", v, ok)
	}
	f.out.Reset()
	if err := f.run("get", "TOKEN", "prod", "--reveal"); err != nil || f.out.String() != "t0k$en\n" {
		t.Fatalf("got %v, %q", err, f.out.String())
	}
}

func TestEnvImportRefusesUndeclaredNamesAndWritesNothing(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	before := snapshot(t, f)
	err := f.run("env", "import", "prod", dotenvFile(t, "NAME=alice\nZED=1\nOTHER=s3cr3t\n"))
	if err == nil || err.Error() != "the file has names repo/manifest.yaml does not declare: OTHER, ZED. Declare them (ezc set NAME --type T --desc D, or edit the manifest) or take them out of the file. Nothing was imported." {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestEnvImportChecksEveryValueBeforeWriting(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	before := snapshot(t, f)
	for _, tc := range []struct{ src, want string }{
		{"NAME=alice\nSIZE=99\n", "SIZE: above the maximum of 9. Nothing was imported."},
		{"NAME=\nSIZE=3\n", "NAME: empty. Nothing was imported."},
		{"NAME=~/x\n", "NAME: a value that starts with ~ is expanded to your home folder when loaded. Take it out of the file and run ezc set --expand. Nothing was imported."},
	} {
		err := f.run("env", "import", "prod", dotenvFile(t, tc.src))
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: got %v", tc.src, err)
		}
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestEnvImportParseErrorNamesTheLine(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	err := f.run("env", "import", "prod", dotenvFile(t, "NAME=alice\nTOKEN=\"s3cr3t\n"))
	if err == nil || err.Error() != "the file could not be read as a .env file (line 2: the quoted value is not closed). Nothing was imported." {
		t.Fatalf("got %v", err)
	}
}

func TestEnvImportSecretWithoutPolicy(t *testing.T) {
	f := newFake(t)
	repo(t, f, map[string]string{"manifest.yaml": mToken})
	before := snapshot(t, f)
	err := f.run("env", "import", "prod", dotenvFile(t, "NAME=alice\nTOKEN=t\n"))
	if !errors.Is(err, backend.ErrNoPolicy) || !strings.HasSuffix(err.Error(), "Run ezc repo rewrap to write it from the store's recipients. Nothing was imported.") {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestEnvImportAcrossTheBoundaryRefused(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"env.prod.yaml": "TOKEN: leaked\n"})
	before := snapshot(t, f)
	err := f.run("env", "import", "prod", dotenvFile(t, "NAME=alice\nTOKEN=t\n"))
	if err == nil || !strings.Contains(err.Error(), "TOKEN is already in repo/env.prod.yaml") {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestEnvImportFailedWritePutsEveryFileBack(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{"env.prod.yaml": "NAME: bob\n"})
	f.backend.Locked = true
	before := snapshot(t, f)
	err := f.run("env", "import", "prod", dotenvFile(t, "NAME=alice\nTOKEN=t\n"))
	if !errors.Is(err, backend.ErrNoKey) || !strings.HasSuffix(err.Error(), "Nothing was imported.") {
		t.Fatalf("got %v", err)
	}
	sameSnapshot(t, before, snapshot(t, f))
}

func TestEnvImportLeavesOutTheSelectors(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	if got := stderrOf(t, f, "env", "import", "prod", dotenvFile(t, "APP_ENV=prod\nNAME=alice\n")); got != "! APP_ENV names the environment and was not imported.\n" {
		t.Fatalf("stderr %q", got)
	}
	if got := readFile(t, repoFile(f, "env.prod.yaml")); got != "NAME: alice\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEnvImportArguments(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	path := dotenvFile(t, "NAME=alice\n")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"prod"}, "an environment name and a file"},
		{[]string{"enc", path}, "enc is a reserved name"},
		{[]string{"no/pe", path}, "not a valid environment name"},
	} {
		var usage *UsageError
		if err := f.run(append([]string{"env", "import"}, tc.args...)...); !errors.As(err, &usage) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v", tc.args, err)
		}
	}
	missing(t, repoFile(f, "env.prod.yaml"))
}

func TestEnvImportNeverPrintsAValue(t *testing.T) {
	f := newFake(t)
	setRepo(t, f, map[string]string{})
	stderr := stderrOf(t, f, "env", "import", "prod", dotenvFile(t, "NAME=alice-v4lue\nTOKEN=s3cr3t-v4lue\n"))
	if strings.Contains(f.out.String()+stderr, "v4lue") {
		t.Fatalf("a value was printed: %q %q", f.out.String(), stderr)
	}
}

func TestEnvImportGlobal(t *testing.T) {
	f := newFake(t)
	store(t, f, map[string]string{"manifest.yaml": m1})
	if err := f.run("env", "import", "-g", "prod", dotenvFile(t, "NAME=alice\n")); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "set NAME in store/env.prod.yaml\n" {
		t.Fatalf("got %q", f.out.String())
	}
	p, _ := storePaths(f.sys())
	if readFile(t, filepath.Join(p.StoreEzc(), "env.prod.yaml")) != "NAME: alice\n" {
		t.Fatal("not written to the store")
	}
}

func TestEnvImportGlobalCommitsAndClearsRotationDue(t *testing.T) {
	f := newFake(t)
	x := removeDue(t, f)
	stderrOf(t, f, "env", "import", "-g", "prod", dotenvFile(t, "TOKEN=fresh-value\n"))
	x.wantCommit("import env.prod (env.prod.enc.yaml, rotation-due.yaml)", "env.prod.enc.yaml", recipients.DueFile)
	due, _ := recipients.LoadDue(filepath.Join(x.store, ezcDir))
	if len(due) != 1 || due["common.enc.yaml/TOKEN"].Removed != "sam" {
		t.Fatalf("due %v", due)
	}
	if strings.Contains(x.git("log", "-p"), "fresh-value") {
		t.Fatal("the history holds a value")
	}
	if _, err := os.Stat(x.file("env.prod.enc.yaml")); err != nil {
		t.Fatal(err)
	}
}
