package sops

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/backend/backendtest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// execRunner runs the real program. Interactive commands are not used by these tests.
func execRunner(c proc.Command) (stdout, stderr []byte, err error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir, cmd.Env = c.Dir, c.Env
	cmd.Stdin = bytes.NewReader(c.Stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// requireSops skips when sops is not installed, and fails instead when EZC_REQUIRE_SOPS=1.
func requireSops(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sops"); err != nil {
		if os.Getenv("EZC_REQUIRE_SOPS") == "1" {
			t.Fatal("sops is required (EZC_REQUIRE_SOPS=1) but is not on PATH")
		}
		t.Skip("sops is not installed")
	}
}

// writeKey writes a freshly generated age identity to path and returns its public key.
func writeKey(t *testing.T, path string) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return id.Recipient().String()
}

// realEnvironment is a real sops, a generated key, and a folder whose policy encrypts to that key.
// The environment sops sees is only a temporary HOME, so nothing of the real one leaks in.
type realEnvironment struct {
	opts    Options
	dir     string
	keyFile string
}

func newReal(t *testing.T) realEnvironment {
	t.Helper()
	requireSops(t)
	root := t.TempDir()
	keyFile := filepath.Join(root, "keys.txt")
	recipient := writeKey(t, keyFile)
	dir := filepath.Join(root, "store")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := fmt.Sprintf("creation_rules:\n  - path_regex: .*\n    age: %s\n", recipient)
	if err := os.WriteFile(filepath.Join(dir, PolicyFile), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	return realEnvironment{
		opts: Options{
			Run:      execRunner,
			LookPath: exec.LookPath,
			Environ:  []string{"HOME=" + filepath.Join(root, "home")},
			KeyFile:  keyFile,
		},
		dir:     dir,
		keyFile: keyFile,
	}
}

func realEnv(t *testing.T) backendtest.Env {
	r := newReal(t)
	noPolicy := filepath.Join(filepath.Dir(r.dir), "bare")
	if err := os.Mkdir(noPolicy, 0o755); err != nil {
		t.Fatal(err)
	}
	return backendtest.Env{
		Backend:     New(r.opts),
		Dir:         r.dir,
		NoPolicyDir: noPolicy,
		Lock:        func() { writeKey(t, r.keyFile) },
	}
}

func TestContract(t *testing.T) { backendtest.Run(t, realEnv) }

func TestKeyFileFromEnvironmentWins(t *testing.T) {
	r := newReal(t)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := New(r.opts).Set(file, "TOKEN", "t0k"); err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(t.TempDir(), "wrong.txt")
	writeKey(t, wrong)
	o := r.opts
	o.Environ = append(o.Environ, "SOPS_AGE_KEY_FILE="+r.keyFile)
	o.KeyFile = wrong
	got, err := New(o).Decrypt(file)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if want := []backend.Pair{{Name: "TOKEN", Value: "t0k"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Decrypt = %v, want %v", got, want)
	}
}

func TestUnquotedNumberIsRejected(t *testing.T) {
	r := newReal(t)
	file := filepath.Join(r.dir, "common.enc.yaml")
	out, stderr, err := execRunner(proc.Command{
		Dir: r.dir, Env: append(r.opts.Environ, "SOPS_AGE_KEY_FILE="+r.keyFile), Stdin: []byte(`{"ZIP": 8540}`), Name: "sops",
		Args: []string{"encrypt", "--filename-override", "common.enc.yaml", "--input-type", "json", "--output-type", "yaml", "/dev/stdin"},
	})
	if err != nil {
		t.Fatalf("sops encrypt: %v: %s", err, stderr)
	}
	if err := os.WriteFile(file, out, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = New(r.opts).Decrypt(file)
	if err == nil || !strings.Contains(err.Error(), "ZIP is not a string") {
		t.Fatalf("error = %v, want one saying ZIP is not a string", err)
	}
	if strings.Contains(err.Error(), "8540") {
		t.Errorf("error %q contains the value", err)
	}
}

func TestInspectReportsUnencrypted(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := b.Set(file, "TOKEN", "t0k"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append([]byte("LEAK: oops\n"), data...), 0o644); err != nil {
		t.Fatal(err)
	}
	names, unencrypted, err := b.Inspect(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"LEAK", "TOKEN"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	if want := []string{"LEAK"}; !reflect.DeepEqual(unencrypted, want) {
		t.Errorf("unencrypted = %v, want %v", unencrypted, want)
	}
}

func TestInspectEmptyValueIsNotUnencrypted(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := b.Set(file, "EMPTY", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(file, "TOKEN", "t0k"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	if !bytes.Contains(data, []byte(`EMPTY: ""`)) {
		t.Fatalf("sops no longer leaves an empty value in the clear; the premise of this test changed:\n%s", data)
	}
	names, unencrypted, err := b.Inspect(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"EMPTY", "TOKEN"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	if len(unencrypted) != 0 {
		t.Errorf("unencrypted = %v, want none", unencrypted)
	}
}

func TestMissingKeyFileIsNoKey(t *testing.T) {
	r := newReal(t)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := New(r.opts).Set(file, "A", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	for name, setup := range map[string]func(path string){
		"does not exist": func(path string) { os.Remove(path) },
		"empty":          func(path string) { os.WriteFile(path, nil, 0o600) },
		"unreadable":     func(path string) { os.Chmod(path, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			if name == "unreadable" && os.Getuid() == 0 {
				t.Skip("root can read anything")
			}
			o := r.opts
			o.KeyFile = filepath.Join(t.TempDir(), "keys.txt")
			writeKey(t, o.KeyFile)
			setup(o.KeyFile)
			_, err := New(o).Decrypt(file)
			if !errors.Is(err, backend.ErrNoKey) {
				t.Fatalf("error = %v, want ErrNoKey", err)
			}
			if !strings.Contains(err.Error(), o.KeyFile) || !strings.Contains(err.Error(), file) {
				t.Errorf("error %q should name the file and the key file", err)
			}
		})
	}
}

func TestCorruptFileIsNotNoKey(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := b.Set(file, "A", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	i := bytes.Index(data, []byte("ENC[AES256_GCM,data:"))
	corrupt := append([]byte{}, data...)
	copy(corrupt[i+len("ENC[AES256_GCM,data:"):], "AAAA")
	if err := os.WriteFile(file, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := b.Decrypt(file)
	if err == nil || errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("error = %v, want a failure that is not ErrNoKey", err)
	}
	if !strings.Contains(err.Error(), "sops failed") || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Errorf("error = %q", err)
	}
}

func TestUnsetWithoutKeyIsNoKey(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	for _, n := range []string{"A", "B"} {
		if err := b.Set(file, n, "v-"+n); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(file)
	writeKey(t, r.keyFile)
	if err := b.Unset(file, "A"); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("error = %v, want ErrNoKey", err)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Error("the file changed")
	}
}

// recorder is a Runner that records its commands and answers with fixed output.
type recorder struct {
	cmds   []proc.Command
	stdout string
	stderr string
	err    error
}

func (r *recorder) run(c proc.Command) ([]byte, []byte, error) {
	r.cmds = append(r.cmds, c)
	return []byte(r.stdout), []byte(r.stderr), r.err
}

func recorded(t *testing.T, r *recorder, environ []string) (backend.Backend, string) {
	t.Helper()
	dir := t.TempDir()
	return New(Options{
		Run:      r.run,
		LookPath: func(string) (string, error) { return "/bin/sops", nil },
		Environ:  environ,
		KeyFile:  "/keys/keys.txt",
	}), dir
}

func TestSetSendsTheValueOnlyOnStdin(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, []string{"HOME=/h"})
	file := filepath.Join(dir, "common.enc.yaml")
	if err := os.WriteFile(file, []byte("anything"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(file, "TOKEN", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	if len(r.cmds) != 1 {
		t.Fatalf("%d commands, want 1", len(r.cmds))
	}
	c := r.cmds[0]
	if c.Name != "sops" || !reflect.DeepEqual(c.Args, []string{"set", "--value-stdin", "common.enc.yaml", `["TOKEN"]`}) {
		t.Errorf("command = %s %q", c.Name, c.Args)
	}
	if string(c.Stdin) != `"s3cr3t-value"` {
		t.Errorf("stdin = %q", c.Stdin)
	}
	if c.Dir != dir {
		t.Errorf("Dir = %q, want %q", c.Dir, dir)
	}
	for _, s := range append(append([]string{}, c.Args...), c.Env...) {
		if strings.Contains(s, "s3cr3t-value") {
			t.Errorf("%q carries the value", s)
		}
	}
}

func TestCreateSendsTheValueOnlyOnStdin(t *testing.T) {
	r := &recorder{stdout: "CIPHERTEXT"}
	b, dir := recorded(t, r, nil)
	if err := os.WriteFile(filepath.Join(dir, PolicyFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "common.enc.yaml")
	if err := b.Set(file, "TOKEN", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	c := r.cmds[0]
	if !reflect.DeepEqual(c.Args[:3], []string{"encrypt", "--filename-override", "common.enc.yaml"}) {
		t.Errorf("args = %q", c.Args)
	}
	if string(c.Stdin) != `{"TOKEN":"s3cr3t-value"}` {
		t.Errorf("stdin = %q", c.Stdin)
	}
	for _, s := range append(append([]string{}, c.Args...), c.Env...) {
		if strings.Contains(s, "s3cr3t-value") {
			t.Errorf("%q carries the value", s)
		}
	}
	if got, _ := os.ReadFile(file); string(got) != "CIPHERTEXT" {
		t.Errorf("file = %q", got)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("folder holds %d entries, want the policy and the file", len(entries))
	}
}

func TestCreateFailureLeavesNoFile(t *testing.T) {
	r := &recorder{err: errors.New("exit status 1"), stderr: "boom"}
	b, dir := recorded(t, r, nil)
	if err := os.WriteFile(filepath.Join(dir, PolicyFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(filepath.Join(dir, "common.enc.yaml"), "TOKEN", "v"); err == nil {
		t.Fatal("Set succeeded")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("folder holds %d entries, want only the policy", len(entries))
	}
}

func TestCreateWithoutPolicyRunsNothing(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, nil)
	err := b.Set(filepath.Join(dir, "common.enc.yaml"), "A", "v")
	if !errors.Is(err, backend.ErrNoPolicy) || len(r.cmds) != 0 {
		t.Fatalf("error = %v, %d commands", err, len(r.cmds))
	}
}

func TestEditWithoutPolicyOnNewFile(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, nil)
	err := b.Edit(filepath.Join(dir, "common.enc.yaml"))
	if !errors.Is(err, backend.ErrNoPolicy) || len(r.cmds) != 0 {
		t.Fatalf("error = %v, %d commands", err, len(r.cmds))
	}
}

func TestEditIsInteractive(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	if err := b.Edit(file); err != nil {
		t.Fatal(err)
	}
	c := r.cmds[0]
	if !c.Interactive || !reflect.DeepEqual(c.Args, []string{"edit", "common.enc.yaml"}) {
		t.Errorf("command = %+v", c)
	}
}

func TestEnvCarriesTheKeyFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		environ []string
		want    bool
	}{
		{"none", []string{"HOME=/h"}, true},
		{"key", []string{"HOME=/h", "SOPS_AGE_KEY=AGE-SECRET-KEY-1"}, false},
		{"key file", []string{"HOME=/h", "SOPS_AGE_KEY_FILE=/other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			b, dir := recorded(t, r, tc.environ)
			file := filepath.Join(dir, "common.enc.yaml")
			os.WriteFile(file, []byte("x"), 0o644)
			if err := b.Set(file, "A", "v"); err != nil {
				t.Fatal(err)
			}
			env := r.cmds[0].Env
			if got := slicesContains(env, "SOPS_AGE_KEY_FILE=/keys/keys.txt"); got != tc.want {
				t.Errorf("Env = %v; has the key file = %v, want %v", env, got, tc.want)
			}
			if !slicesContains(env, "HOME=/h") {
				t.Errorf("Env = %v lost the environment", env)
			}
		})
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestCheckWithoutSops(t *testing.T) {
	b := New(Options{LookPath: func(string) (string, error) { return "", errors.New("not found") }})
	err := b.Check()
	if !errors.Is(err, backend.ErrNoTool) || !strings.Contains(err.Error(), "sops is not installed") {
		t.Fatalf("error = %v", err)
	}
}

func TestCheckOldSops(t *testing.T) {
	r := &recorder{stdout: "Usage: sops set [flags]\n  --idx\n"}
	b, _ := recorded(t, r, nil)
	err := b.Check()
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("error = %v", err)
	}
	r.stdout = "Usage: sops set\n  --value-stdin\n"
	if err := b.Check(); err != nil {
		t.Fatalf("Check on a new sops: %v", err)
	}
}

func TestNoKeyError(t *testing.T) {
	r := &recorder{err: errors.New("exit status 128"), stderr: "Failed to get the data key required to decrypt the SOPS file."}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	_, err := b.Decrypt(file)
	if !errors.Is(err, backend.ErrNoKey) || !strings.Contains(err.Error(), "/keys/keys.txt") || !strings.Contains(err.Error(), file) {
		t.Fatalf("error = %v", err)
	}
	b, dir = recorded(t, r, []string{"SOPS_AGE_KEY=x"})
	file = filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	_, err = b.Decrypt(file)
	if !errors.Is(err, backend.ErrNoKey) || !strings.Contains(err.Error(), "comes from SOPS_AGE_KEY") {
		t.Fatalf("error = %v", err)
	}
}

func TestOtherFailurePassesFiveLines(t *testing.T) {
	var lines []string
	for i := 1; i <= 9; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	r := &recorder{err: errors.New("exit status 1"), stderr: strings.Join(lines, "\n"), stdout: "STDOUT-TEXT"}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	_, err := b.Decrypt(file)
	if err == nil {
		t.Fatal("Decrypt succeeded")
	}
	msg := err.Error()
	if !strings.Contains(msg, "line1") || !strings.Contains(msg, "line5") || strings.Contains(msg, "line6") {
		t.Errorf("message = %q", msg)
	}
	if !strings.Contains(msg, file) || strings.Contains(msg, "STDOUT-TEXT") {
		t.Errorf("message = %q", msg)
	}
}

func TestFailureNeverEchoesTheValue(t *testing.T) {
	r := &recorder{err: errors.New("exit status 1"), stderr: `bad input "s3cr3t-value" and s3cr3t-value`}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	err := b.Set(file, "A", "s3cr3t-value")
	if err == nil || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("error = %v", err)
	}
}

func TestNameIsJSONEncodedInTheIndex(t *testing.T) {
	got, err := indexOf("A_B")
	if err != nil || got != `["A_B"]` {
		t.Errorf("indexOf = %q, %v", got, err)
	}
}
