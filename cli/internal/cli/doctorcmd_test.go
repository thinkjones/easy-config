package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/gitstore/gittest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
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

// doctorSquash is out with each line's runs of spaces made one and its indent dropped, so a test does not depend on
// the width of the label column.
func doctorSquash(out string) string {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.Join(lines, "\n")
}

// doctorBroken are the labels of the items marked ✗.
func doctorBroken(out string) []string {
	var labels []string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[0] == "✗" {
			labels = append(labels, f[1])
		}
	}
	return labels
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
	if out := doctorSquash(f.out.String()); !strings.Contains(out, "! sync 1 commit behind origin\nRun `ezc store pull`.\n") {
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

func TestDoctorOffABranch(t *testing.T) {
	f, bare := doctorCmdCloned(t)
	doctorCmdPushElsewhere(t, bare)
	if _, _, err := f.runGit(proc.Command{Dir: setupStore(f), Name: "git", Args: []string{"checkout", "--quiet", "--detach"}}); err != nil {
		t.Fatal(err)
	}
	wantExit(t, f.run("doctor"), 1)
	want := "✗ sync the store in " + setupStore(f) + " is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again."
	if line := doctorCmdLine(f.out.String(), "sync"); strings.Join(strings.Fields(line), " ") != want {
		t.Errorf("sync: %q\n%s", line, f.out.String())
	}
}

func TestDoctorStoreWithoutGit(t *testing.T) {
	f, _ := doctorCmdCloned(t)
	f.noGit = true
	wantExit(t, f.run("doctor", "--only", "store"), 1)
	want := "✗ sync git is not installed, so a change to the store could not be committed. Install git, then run this again."
	if line := doctorCmdLine(f.out.String(), "sync"); strings.Join(strings.Fields(line), " ") != want {
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
	want := "✗ access  your key is not one of this store's recipients."
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
	if line := doctorSquash(doctorCmdLine(f.out.String(), "requests")); !strings.Contains(line, "! requests 1 access request pending: ana") {
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
	// Setup refuses to run with EZC_STORE set, so doctor does not send the user to it.
	if line := doctorCmdLine(f.out.String(), "config"); !strings.HasSuffix(line, "no config at "+setupConfig(f)+".") {
		t.Errorf("config: %q", line)
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
	for _, c := range f.gits {
		if len(c.Args) != 1 || c.Args[0] != "version" {
			t.Errorf("git ran for --only machine: %v", c)
		}
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

// doctorRun runs doctor on f and returns what it printed; the encryption tool is never named in it.
func doctorRun(t *testing.T, f *fake) (string, error) {
	t.Helper()
	out, _, err := accessRun(f, "doctor")
	if strings.Contains(strings.ToLower(out), "sops") {
		t.Errorf("doctor named the encryption tool:\n%s", out)
	}
	return out, err
}

// The fixture store declares names no file sets, so its audit is always broken: these tests look at the item they are about.
func TestDoctorJoinedButNotApproved(t *testing.T) {
	j := joinMachines(t)
	j.join(j.b, "--name", "sam")

	out, err := doctorRun(t, j.b)
	wantExit(t, err, 1)
	want := "✗ access this machine's key cannot read the store yet; request sam is waiting for approval\nAsk someone who can read the store to run ezc user approve sam.\n"
	if !strings.Contains(doctorSquash(out), want) {
		t.Errorf("no pending line in\n%s", out)
	}
	if got := doctorBroken(out); !slices.Equal(got, []string{"access", "audit"}) {
		t.Errorf("broken: %v", got)
	}

	if _, _, err := accessRun(j.a, "user", "approve", "sam", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := joinRun(t, j.b, "store", "pull"); err != nil {
		t.Fatal(err)
	}
	out, _ = doctorRun(t, j.b)
	if got := doctorBroken(out); !slices.Equal(got, []string{"audit"}) {
		t.Errorf("broken after approve: %v\n%s", got, out)
	}
	squashed := doctorSquash(out)
	for _, want := range []string{"✓ access your key is the recipient sam", "✓ recipients every file is encrypted for the 2 recipients"} {
		if !strings.Contains(squashed, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

func TestDoctorNotAskedForAccess(t *testing.T) {
	j := joinMachines(t)
	if _, _, err := accessRun(j.b, "setup", "--store", j.x.bare, "--name", "sam", "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err := doctorRun(t, j.b)
	wantExit(t, err, 1)
	if want := "✗ access your key is not one of this store's recipients.\nRun ezc join " + j.x.bare + ".\n"; !strings.Contains(doctorSquash(out), want) {
		t.Errorf("no join fix in\n%s", out)
	}
}

func TestDoctorFilesThatDifferFromTheRegistry(t *testing.T) {
	f := newFake(t)
	reg := map[string]recipients.Recipient{"gene": accessEntry(accessGene), "sam": accessEntry(accessSam)}
	accessStoreWith(t, f, reg, accessGene, accessStray)

	out, err := doctorRun(t, f)
	wantExit(t, err, 1)
	want := "✗ recipients 2 files are not encrypted for the registry: common.enc.yaml, env.prod.enc.yaml\nRun ezc store rewrap.\n"
	if !strings.Contains(doctorSquash(out), want) {
		t.Errorf("no differing files in\n%s", out)
	}
	if strings.Contains(out, accessStray) {
		t.Errorf("doctor printed a key:\n%s", out)
	}

	if _, _, err := accessRun(f, "store", "rewrap"); err != nil {
		t.Fatal(err)
	}
	out, _ = doctorRun(t, f)
	if got := doctorBroken(out); !slices.Equal(got, []string{"audit"}) {
		t.Errorf("broken after rewrap: %v\n%s", got, out)
	}
	if !strings.Contains(doctorSquash(out), "✓ recipients every file is encrypted for the 2 recipients") {
		t.Errorf("recipients not clear:\n%s", out)
	}
}

func TestDoctorRotationDue(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	write(t, filepath.Join(x.store, ".ezc", "rotation-due.yaml"), "common.enc.yaml/TOKEN:\n  removed: sam\n  on: \"2026-10-11\"\n")
	out, _ := doctorRun(t, f)
	want := "! rotation 1 secret was readable by a removed key\nezc store status --due lists them; give each a new value with ezc set -g NAME.\n"
	if !strings.Contains(doctorSquash(out), want) {
		t.Errorf("no rotation warning in\n%s", out)
	}
	if got := doctorBroken(out); !slices.Equal(got, []string{"audit"}) {
		t.Errorf("a due list must only warn: %v", got)
	}
	if strings.Contains(out, "TOKEN") {
		t.Errorf("doctor named a secret:\n%s", out)
	}

	write(t, filepath.Join(x.store, ".ezc", "rotation-due.yaml"), "[\n")
	out, _ = doctorRun(t, f)
	if got := doctorBroken(out); !slices.Equal(got, []string{"rotation", "audit"}) {
		t.Errorf("unreadable: %v", got)
	}
	if !strings.Contains(doctorSquash(out), "✗ rotation cannot read rotation-due.yaml") {
		t.Errorf("no rotation error in\n%s", out)
	}
}
