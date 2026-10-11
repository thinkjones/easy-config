package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
)

// doctorCmdLine is the line of doctor's output that starts with marker and label, "" when there is none.
func doctorCmdLine(out, label string) string {
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 1 && fields[1] == label {
			return line
		}
	}
	return ""
}

// doctorCmdCloned is a machine that cloned a store whose registry names its key, with origin a bare repo.
func doctorCmdCloned(t *testing.T) (f *fake, bare string) {
	t.Helper()
	f = setupFake(t)
	_, public := setupOwnKey(t, f)
	bare = setupRemote(t, public)
	if err := f.run("setup", "--store", bare, "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	return f, bare
}

// doctorCmdPushElsewhere commits a file to origin from another clone.
func doctorCmdPushElsewhere(t *testing.T, bare string) {
	t.Helper()
	g := gitstore.Git{Run: gittest.WithIdentity(t)}
	work := filepath.Join(t.TempDir(), "work")
	if err := g.Clone(bare, work); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(work, ".ezc", "elsewhere.txt"), "x\n")
	if _, err := g.Commit(work, "elsewhere", gitstore.Author{}, ".ezc/elsewhere.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Push(work); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorAfterSetup(t *testing.T) {
	f := setupFake(t)
	if err := f.run("setup", "--new-local", "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	out := f.out.String()
	for label, detail := range map[string]string{
		"config": "✓ config",
		"git":    "✓ git",
		"sync":   "local only",
		"access": "your key is the recipient gene",
	} {
		if line := doctorCmdLine(out, label); !strings.Contains(line, detail) {
			t.Errorf("%s: %q in\n%s", label, line, out)
		}
	}
	if doctorCmdLine(out, "requests") != "" {
		t.Errorf("requests shown\n%s", out)
	}
	if f.errOut.Len() != 0 {
		t.Errorf("stderr: %s", f.errOut.String())
	}
}

func TestDoctorWithOrigin(t *testing.T) {
	f, bare := doctorCmdCloned(t)
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if line := doctorCmdLine(f.out.String(), "sync"); !strings.HasPrefix(strings.TrimSpace(line), "✓ sync") || !strings.HasSuffix(line, "in sync") {
		t.Errorf("sync: %q", line)
	}

	// Behind: warns, exits 0.
	doctorCmdPushElsewhere(t, bare)
	f.now = time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)
	f.out.Reset()
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if out := f.out.String(); !strings.Contains(out, "! sync      1 commit behind origin\n      Run `ezc store pull`.\n") {
		t.Errorf("behind:\n%s", out)
	}
}

func TestDoctorDiverged(t *testing.T) {
	f, bare := doctorCmdCloned(t)
	doctorCmdPushElsewhere(t, bare)
	write(t, filepath.Join(setupStore(f), ".ezc", "local.txt"), "x\n")
	g := gitstore.Git{Run: f.runGit}
	if _, err := g.Commit(setupStore(f), "local", gitstore.Author{Name: "gene"}, ".ezc/local.txt"); err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)
	wantExit(t, f.run("doctor"), 1)
	if line := doctorCmdLine(f.out.String(), "sync"); !strings.HasPrefix(strings.TrimSpace(line), "✗ sync") || !strings.Contains(line, "diverged from origin (1 ahead, 1 behind)") {
		t.Errorf("sync: %q\n%s", line, f.out.String())
	}
}

func TestDoctorOriginUnreachable(t *testing.T) {
	f, _ := doctorCmdCloned(t)
	g := gitstore.Git{Run: f.runGit}
	if err := g.SetRemote(setupStore(f), filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if line := doctorCmdLine(f.out.String(), "sync"); !strings.HasPrefix(strings.TrimSpace(line), "! sync") || !strings.HasSuffix(line, "in sync (origin could not be reached)") {
		t.Errorf("sync: %q", line)
	}
}

func TestDoctorKeyNotARecipient(t *testing.T) {
	f := setupFake(t)
	bare := setupRemote(t, "fake1someoneelse")
	if err := f.run("setup", "--store", bare, "--name", "gene", "--yes"); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	wantExit(t, f.run("doctor"), 1)
	want := "✗ access  your key is not one of this store's recipients. Give your public key to someone who has access."
	if line := doctorCmdLine(f.out.String(), "access"); strings.Join(strings.Fields(line), " ") != strings.Join(strings.Fields(want), " ") {
		t.Errorf("access: %q", line)
	}
}

func TestDoctorKeyAsText(t *testing.T) {
	f, _ := doctorCmdCloned(t)
	if err := os.Remove(setupKeyFile(f)); err != nil {
		t.Fatal(err)
	}
	f.backend.Locked = true
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if line := doctorCmdLine(f.out.String(), "access"); line != "" {
		t.Errorf("access shown without a readable key: %q", line)
	}
}

func TestDoctorBadRegistry(t *testing.T) {
	f, _ := doctorCmdCloned(t)
	write(t, filepath.Join(setupStore(f), ".ezc", "recipients.yaml"), "recipients: [\n")
	wantExit(t, f.run("doctor"), 1)
	if line := doctorCmdLine(f.out.String(), "access"); !strings.HasPrefix(strings.TrimSpace(line), "✗ access") || strings.Contains(line, "\n") {
		t.Errorf("access: %q", line)
	}
}

func TestDoctorPendingRequests(t *testing.T) {
	f, _ := doctorCmdCloned(t)
	write(t, filepath.Join(setupStore(f), ".ezc", "requests", "ana.yaml"), "key: x\n")
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if line := doctorCmdLine(f.out.String(), "requests"); !strings.Contains(line, "! requests  1 access request pending: ana") {
		t.Errorf("requests: %q", line)
	}
}

func TestDoctorInvalidConfig(t *testing.T) {
	f := setupFake(t)
	write(t, setupConfig(f), "user = [\n")
	wantExit(t, f.run("doctor"), 1)
	if line := doctorCmdLine(f.out.String(), "config"); !strings.HasPrefix(strings.TrimSpace(line), "✗ config") {
		t.Errorf("config: %q\n%s", line, f.out.String())
	}
}

func TestDoctorNoGit(t *testing.T) {
	f := setupFake(t)
	f.paths = nil
	wantExit(t, f.run("doctor"), 1)
	if line := doctorCmdLine(f.out.String(), "git"); strings.Join(strings.Fields(line), " ") != "✗ git git is not installed." {
		t.Errorf("git: %q", line)
	}
}

func TestDoctorPlainFolderStore(t *testing.T) {
	f := setupFake(t)
	plain := t.TempDir()
	write(t, filepath.Join(plain, ".ezc", "manifest.yaml"), "schemaVersion: v1\nrequired: {}\noptional: {}\n")
	f.env["EZC_STORE"] = plain
	if err := f.run("doctor"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	for _, label := range []string{"sync", "access", "requests"} {
		if line := doctorCmdLine(f.out.String(), label); line != "" {
			t.Errorf("%s shown for a plain folder: %q", label, line)
		}
	}
	for _, c := range f.gits {
		if len(c.Args) > 0 && c.Args[0] == "fetch" {
			t.Errorf("fetched a plain folder: %v", c.Args)
		}
	}
}

func TestDoctorOnlyMachine(t *testing.T) {
	f, _ := doctorCmdCloned(t)
	f.gits = nil
	if err := f.run("doctor", "--only", "machine"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	out := f.out.String()
	for _, label := range []string{"config", "git"} {
		if doctorCmdLine(out, label) == "" {
			t.Errorf("no %s\n%s", label, out)
		}
	}
	for _, label := range []string{"location", "sync", "access", "requests"} {
		if doctorCmdLine(out, label) != "" {
			t.Errorf("%s shown\n%s", label, out)
		}
	}
	if len(f.gits) != 0 {
		t.Errorf("git ran for --only machine: %v", f.gits)
	}
	f.out.Reset()
	if err := f.run("doctor", "--only", "store"); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	for _, label := range []string{"sync", "access"} {
		if doctorCmdLine(f.out.String(), label) == "" {
			t.Errorf("no %s\n%s", label, f.out.String())
		}
	}
	if doctorCmdLine(f.out.String(), "config") != "" {
		t.Errorf("config shown for --only store")
	}
}
