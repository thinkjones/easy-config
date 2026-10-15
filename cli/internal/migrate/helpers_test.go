package migrate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// oldTool is a fake of the old tool: it answers `codegen shell [ENV]` with exports built from out[ENV]
// ("" when no ENV is given), and records every command.
type oldTool struct {
	out   map[string]map[string]string
	fail  error
	print string // printed instead of exports, when set
	cmds  []proc.Command
}

type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d: boom s3cret-value", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

func (o *oldTool) run(c proc.Command) ([]byte, []byte, error) {
	o.cmds = append(o.cmds, c)
	if c.Name != OldTool {
		return nil, nil, errors.New("unexpected program " + c.Name)
	}
	if o.fail != nil {
		return nil, []byte("boom s3cret-value"), o.fail
	}
	if o.print != "" {
		return []byte(o.print), nil, nil
	}
	env := ""
	if len(c.Args) == 3 {
		env = c.Args[2]
	}
	vals, ok := o.out[env]
	if !ok {
		return nil, []byte("no such environment"), exitErr(1)
	}
	var b strings.Builder
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "export %s='%s'\n", n, strings.ReplaceAll(vals[n], "'", `'\''`))
	}
	return []byte(b.String()), []byte("warning on stderr"), nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// snapshot is every path under dir with its bytes ("<dir>" for a folder).
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			snap[p] = "<dir>"
			return nil
		}
		b, err := os.ReadFile(p)
		snap[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func sameSnapshot(t *testing.T, a, b map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		for p := range a {
			if a[p] != b[p] {
				t.Errorf("changed: %s", p)
			}
		}
		for p := range b {
			if _, ok := a[p]; !ok {
				t.Errorf("added: %s", p)
			}
		}
	}
}

// noTemp points the temporary folder at an empty one and fails the test if anything is left in it.
func noTemp(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Cleanup(func() {
		entries, _ := os.ReadDir(tmp)
		for _, e := range entries {
			t.Errorf("left in the temporary folder: %s", e.Name())
		}
	})
}

func encrypt(t *testing.T, b *backend.Fake, dir, file string, keys []string, pairs ...string) {
	t.Helper()
	if err := b.WritePolicy(dir, keys); err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if err := b.Set(filepath.Join(dir, file), pairs[i], pairs[i+1]); err != nil {
			t.Fatal(err)
		}
	}
}

type world struct {
	o    Options
	out  *bytes.Buffer
	tool *oldTool
	b    *backend.Fake
	keys []string
}

func newWorld(t *testing.T, dir string) *world {
	t.Helper()
	out := &bytes.Buffer{}
	tool := &oldTool{out: map[string]map[string]string{}}
	b := &backend.Fake{}
	home := t.TempDir()
	return &world{
		o: Options{
			Dir:     dir,
			Home:    home,
			Now:     time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC),
			Environ: []string{"PATH=/bin", "HOME=" + home, "APP_ENV=prod", "FS_CONFIG_HOME=/elsewhere"},
			Exec:    tool.run,
			LookPath: func(name string) (string, error) {
				if name == OldTool {
					return "/usr/local/bin/" + name, nil
				}
				return "", errors.New("not found")
			},
			Backend:         b,
			SchemaURL:       "https://example.com/new.json",
			KeyTextVariable: "SOPS_AGE_KEY",
			Stdout:          out,
		},
		out:  out,
		tool: tool,
		b:    b,
		keys: fakeKeys(t, 2),
	}
}

func envOf(c proc.Command, name string) (string, bool) {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}
