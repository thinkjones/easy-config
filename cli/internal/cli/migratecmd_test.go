package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/migrate"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// oldStoreIn writes an old store into f's working directory, A in plain text and S encrypted, and makes the
// old tool answer with value as S.
func oldStoreIn(t *testing.T, f *fake, value string) {
	t.Helper()
	old := filepath.Join(f.wd, migrate.OldDir)
	pub, _, err := f.backend.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"global.yaml":     "A: one\n",
		"manifest.yaml":   "$schema: https://example.com/old.json\nschemaVersion: v1\n",
		"recipients.yaml": "recipients:\n  gene:\n    key: " + pub + "\n    purpose: human\n",
	} {
		if err := os.MkdirAll(old, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(old, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.backend.WritePolicy(old, []string{pub}); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.Set(filepath.Join(old, "global.enc.yaml"), "S", "s3cret-value"); err != nil {
		t.Fatal(err)
	}
	f.paths[migrate.OldTool] = "/usr/local/bin/" + migrate.OldTool
	f.execFn = func(c proc.Command) ([]byte, []byte, error) {
		if c.Name != migrate.OldTool {
			return nil, nil, errors.New("unexpected " + c.Name)
		}
		return []byte(fmt.Sprintf("export A='one'\nexport S='%s'\n", value)), nil, nil
	}
}

func TestMigrateCommandWiresTheStore(t *testing.T) {
	f := newFake(t)
	oldStoreIn(t, f, "s3cret-value")
	if err := f.run("migrate", "--dry-run"); err != nil {
		t.Fatalf("dry run: %v\n%s%s", err, f.out.String(), f.errOut.String())
	}
	if !strings.Contains(f.out.String(), "✓ (store): 2 names, identical") || !strings.Contains(f.out.String(), "Dry run: nothing was written.") {
		t.Errorf("dry run output:\n%s", f.out.String())
	}
	if _, err := os.Stat(filepath.Join(f.wd, ".ezc")); err == nil {
		t.Fatal("the dry run wrote .ezc/")
	}
	if err := f.run("migrate"); err != nil {
		t.Fatalf("migrate: %v\n%s", err, f.out.String())
	}
	m, err := os.ReadFile(filepath.Join(f.wd, ".ezc", "manifest.yaml"))
	if err != nil || !strings.HasPrefix(string(m), "$schema: "+schemaID+"\n") {
		t.Errorf("manifest.yaml = %q, %v", m, err)
	}
	if strings.Contains(f.out.String()+f.errOut.String(), "s3cret-value") {
		t.Error("a value was printed")
	}
}

func TestMigrateCommandNotProved(t *testing.T) {
	f := newFake(t)
	oldStoreIn(t, f, "other")
	err := f.run("migrate")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.out.String(), "✗ (store): S differs") {
		t.Errorf("output:\n%s", f.out.String())
	}
}

func TestMigrateCommandRefusal(t *testing.T) {
	f := newFake(t)
	err := f.run("migrate")
	if err == nil || err.Error() != "Nothing to migrate here: no "+migrate.OldDir+"/ folder." {
		t.Errorf("err = %v", err)
	}
}

func TestMigrateRejectsArguments(t *testing.T) {
	f := newFake(t)
	var usage *UsageError
	if err := f.run("migrate", "x"); !errors.As(err, &usage) {
		t.Errorf("err = %v", err)
	}
}
