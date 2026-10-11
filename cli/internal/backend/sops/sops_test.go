package sops

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/thinkjones/easy-config/cli/internal/backend"
	"github.com/thinkjones/easy-config/cli/internal/backend/backendtest"
	"github.com/thinkjones/easy-config/cli/internal/proc"
	"github.com/thinkjones/easy-config/cli/internal/valuefile"
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
	b := New(r.opts)
	public, err := b.PublicKey(r.keyFile)
	if err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(r.keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return backendtest.Env{
		Backend:     b,
		Dir:         r.dir,
		NoPolicyDir: noPolicy,
		Lock:        func() { writeKey(t, r.keyFile) },
		Public:      public,
		Private:     private,
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
	if !strings.Contains(err.Error(), "sops could not read") || strings.Contains(err.Error(), "s3cr3t-value") {
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
	err := b.Edit(filepath.Join(dir, "common.enc.yaml"), "nano")
	if !errors.Is(err, backend.ErrNoPolicy) || len(r.cmds) != 0 {
		t.Fatalf("error = %v, %d commands", err, len(r.cmds))
	}
}

func TestEditIsInteractive(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	if err := b.Edit(file, "code --wait"); err != nil {
		t.Fatal(err)
	}
	c := r.cmds[0]
	if !c.Interactive || !reflect.DeepEqual(c.Args, []string{"edit", "common.enc.yaml"}) {
		t.Errorf("command = %+v", c)
	}
}

func TestEditSetsExactlyOneEditor(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, []string{"HOME=/h", "SOPS_EDITOR=code", "EDITOR=vim"})
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	if err := b.Edit(file, "nano -u NONE"); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, kv := range r.cmds[0].Env {
		if strings.HasPrefix(kv, "SOPS_EDITOR=") {
			n++
			if kv != "SOPS_EDITOR=nano -u NONE" {
				t.Errorf("got %q", kv)
			}
		}
	}
	if n != 1 || !slicesContains(r.cmds[0].Env, "HOME=/h") {
		t.Errorf("Env = %v", r.cmds[0].Env)
	}
}

// TestEditRunsTheGivenEditor runs a real sops whose environment names a different, failing editor.
func TestEditRunsTheGivenEditor(t *testing.T) {
	r := newReal(t)
	r.opts.Environ = append(r.opts.Environ, "EDITOR=false", "VISUAL=false", "SOPS_EDITOR=false")
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := b.Set(file, "A", "a1"); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "append.sh")
	body := "#!/bin/sh\n[ \"$1\" = --flag ] || exit 1\nprintf 'B: b1\\n' >> \"$2\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.Edit(file, script+" --flag"); err != nil {
		t.Fatal(err)
	}
	got, err := b.Decrypt(file)
	if err != nil || !reflect.DeepEqual(got, []backend.Pair{{Name: "A", Value: "a1"}, {Name: "B", Value: "b1"}}) {
		t.Fatalf("Decrypt = %v, %v", got, err)
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
	r := &recorder{err: errors.New("exit status 128"), stderr: "Failed to get the data key required to decrypt the SOPS file.\n  - | age: no identity matched any of the\n      | recipients."}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	_, err := b.Decrypt(file)
	if !errors.Is(err, backend.ErrNoKey) || !strings.HasSuffix(err.Error(), "Your key file, /keys/keys.txt, does not exist.") || !strings.Contains(err.Error(), file) {
		t.Fatalf("error = %v", err)
	}
	for keyFile, want := range map[string]string{file: "Your key file is " + file + ".", "": "No key file is set."} {
		_, err = New(Options{Run: r.run, LookPath: func(string) (string, error) { return "/bin/sops", nil }, KeyFile: keyFile}).Decrypt(file)
		if !errors.Is(err, backend.ErrNoKey) || !strings.HasSuffix(err.Error(), "is one of its recipients. "+want) {
			t.Fatalf("key file %q: error = %v", keyFile, err)
		}
	}
	b, dir = recorded(t, r, []string{"SOPS_AGE_KEY=x"})
	file = filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	_, err = b.Decrypt(file)
	if !errors.Is(err, backend.ErrNoKey) || !strings.Contains(err.Error(), "comes from SOPS_AGE_KEY") {
		t.Fatalf("error = %v", err)
	}
}

func TestOtherFailureNeverPassesStderr(t *testing.T) {
	var lines []string
	for i := 1; i <= 9; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	lines[2] = "quoted plaintext s3cr3t-value"
	r := &recorder{err: errors.New("exit status 1"), stderr: strings.Join(lines, "\n"), stdout: "STDOUT-TEXT"}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	_, err := b.Decrypt(file)
	if err == nil {
		t.Fatal("Decrypt succeeded")
	}
	want := file + ": sops could not read this file. To see its own message, run `sops decrypt common.enc.yaml` in " + dir + "."
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err, want)
	}
	if err := b.Set(file, "A", "v"); err == nil || !strings.Contains(err.Error(), "could not write") || strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("Set error = %v", err)
	}
}

func TestInvalidUTF8IsRejected(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	err := b.Set(file, "A", "a\xffb")
	if err == nil || !strings.Contains(err.Error(), "A is not valid UTF-8 text and cannot be stored.") || len(r.cmds) != 0 {
		t.Fatalf("error = %v, %d commands", err, len(r.cmds))
	}
}

func TestReservedName(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, nil)
	if err := b.Set(filepath.Join(dir, "common.enc.yaml"), "sops", "v"); err == nil || len(r.cmds) != 0 {
		t.Fatalf("error = %v, %d commands", err, len(r.cmds))
	}
}

func TestEmptyKeyVariableIsNotSet(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, []string{"HOME=/h", "SOPS_AGE_KEY=", "SOPS_AGE_KEY_FILE="})
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	if err := b.Set(file, "A", "v"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"HOME=/h", "SOPS_AGE_KEY_FILE=/keys/keys.txt"}; !reflect.DeepEqual(r.cmds[0].Env, want) {
		t.Errorf("Env = %v, want %v", r.cmds[0].Env, want)
	}
}

// flipType rewrites the recorded type of every value in an encrypted file, the edit that makes sops quote the
// decrypted text in its own error. It needs no key.
func flipType(t *testing.T, file string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, bytes.ReplaceAll(data, []byte("type:str]"), []byte("type:int]")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEditedTypeNeverLeaksTheValue(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	for _, n := range []string{"A", "B"} {
		if err := b.Set(file, n, "s3cr3t-value-"+n); err != nil {
			t.Fatal(err)
		}
	}
	flipType(t, file)
	_, derr := b.Decrypt(file)
	serr := b.Set(file, "B", "other")
	uerr := b.Unset(file, "A")
	for name, err := range map[string]error{"Decrypt": derr, "Set": serr, "Unset": uerr} {
		if err == nil {
			t.Errorf("%s succeeded on an edited file", name)
			continue
		}
		if errors.Is(err, backend.ErrNoKey) || strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%s error = %q", name, err)
		}
	}
}

func TestDamagedKeyBlockIsNotNoKey(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := b.Set(file, "A", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(file)
	generic := file + ": sops could not read this file. To see its own message, run `sops decrypt common.enc.yaml` in " + r.dir + "."
	damage := func(t *testing.T, data []byte) {
		t.Helper()
		if err := os.WriteFile(file, data, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := b.Decrypt(file)
		if err == nil {
			return // damage that changes nothing the key block needs
		}
		if errors.Is(err, backend.ErrNoKey) || err.Error() != generic {
			t.Errorf("error = %q, want the generic failure", err)
		}
	}
	t.Run("begin line", func(t *testing.T) {
		damage(t, bytes.Replace(orig, []byte("-----BEGIN AGE ENCRYPTED FILE-----"), []byte("-----BEGIN AGE ENCRYPTEX FILE-----"), 1))
	})
	t.Run("end line", func(t *testing.T) {
		damage(t, bytes.Replace(orig, []byte("-----END AGE ENCRYPTED FILE-----"), []byte("-----END AGE ENCRYPTEX FILE-----"), 1))
	})
	t.Run("payload", func(t *testing.T) {
		i := bytes.Index(orig, []byte("-----END AGE")) - 10
		d := append([]byte{}, orig...)
		d[i] = 'A' + (d[i]-'A'+1)%26
		damage(t, d)
	})
	t.Run("sweep", func(t *testing.T) {
		start := bytes.Index(orig, []byte("-----BEGIN AGE")) + len("-----BEGIN AGE ENCRYPTED FILE-----\n")
		stop := bytes.Index(orig, []byte("-----END AGE"))
		for i := start; i < stop; i += 5 {
			if c := orig[i]; c == '\n' || c == ' ' {
				continue
			}
			d := append([]byte{}, orig...)
			d[i] = '!'
			damage(t, d)
		}
	})
}

func TestNameIsJSONEncodedInTheIndex(t *testing.T) {
	got := indexOf("A_B")
	if got != `["A_B"]` {
		t.Errorf("indexOf = %q", got)
	}
}

func TestEveryOperationThatRunsSopsNamesAMissingTool(t *testing.T) {
	r := &recorder{}
	dir := t.TempDir()
	b := New(Options{Run: r.run, LookPath: func(string) (string, error) { return "", errors.New("not found") }})
	file := filepath.Join(dir, "common.enc.yaml")
	fresh := filepath.Join(dir, "env.local.enc.yaml")
	for _, name := range []string{PolicyFile, "common.enc.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("A: ENC[x]\nB: ENC[y]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ops := map[string]func() error{
		"Decrypt": func() error { _, err := b.Decrypt(file); return err },
		"Set":     func() error { return b.Set(file, "A", "v") },
		"create":  func() error { return b.Set(fresh, "A", "v") },
		"Unset":   func() error { return b.Unset(file, "A") },
		"Edit":    func() error { return b.Edit(file, "nano") },
		"Rewrap":  func() error { return b.Rewrap(file) },
	}
	for name, op := range ops {
		err := op()
		if !errors.Is(err, backend.ErrNoTool) || !strings.HasPrefix(err.Error(), "sops is not installed. Install it") {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if len(r.cmds) != 0 {
		t.Errorf("ran %d commands", len(r.cmds))
	}
}

func TestCreateWithNoMatchingRuleIsNoPolicy(t *testing.T) {
	r := newReal(t)
	policy := filepath.Join(r.dir, PolicyFile)
	data, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, bytes.Replace(data, []byte("path_regex: .*"), []byte("path_regex: ^other$"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(r.dir, "common.enc.yaml")
	err = New(r.opts).Set(file, "TOKEN", "t0k")
	if want := policy + " has no rule that matches common.enc.yaml."; !errors.Is(err, backend.ErrNoPolicy) || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file exists: %v", err)
	}
}

// exitError is a failed run with an exit status, as the real runner's *exec.ExitError is.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitError) ExitCode() int { return int(e) }

func TestEditThatChangesNothingSucceeds(t *testing.T) {
	// sops edit exits 200 when the file was saved unchanged.
	r := &recorder{err: fmt.Errorf("wrapped: %w", exitError(200))}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	os.WriteFile(file, []byte("x"), 0o644)
	if err := b.Edit(file, "nano"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
}

func TestEditFailureHasAFixedMessage(t *testing.T) {
	for _, runErr := range []error{exitError(1), errors.New("fork/exec: s3cr3t-value")} {
		r := &recorder{err: runErr, stderr: "s3cr3t-value", stdout: "s3cr3t-value"}
		b, dir := recorded(t, r, nil)
		file := filepath.Join(dir, "common.enc.yaml")
		os.WriteFile(file, []byte("x"), 0o644)
		err := b.Edit(file, "nano")
		if want := file + ": the editor or sops failed; the file was not changed."; err == nil || err.Error() != want {
			t.Errorf("%v: error = %v, want %q", runErr, err, want)
		}
	}
}

func TestRealEditThatChangesNothingSucceeds(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	if err := b.Set(file, "TOKEN", "t0k"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true on PATH")
	}
	if err := b.Edit(file, truePath); err != nil {
		t.Fatalf("Edit with an editor that saves nothing: %v", err)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Error("the file changed")
	}
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no false on PATH")
	}
	err = b.Edit(file, falsePath)
	if want := file + ": the editor or sops failed; the file was not changed."; err == nil || err.Error() != want {
		t.Fatalf("Edit with a failing editor: %v", err)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Error("the file changed")
	}
}

func TestWritePolicyBytes(t *testing.T) {
	dir := t.TempDir()
	if err := New(Options{}).WritePolicy(dir, []string{"age1aaa", "age1bbb"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, PolicyFile))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Generated by ezc from recipients.yaml. Do not edit.\ncreation_rules:\n  - path_regex: \\.enc\\.yaml$\n    age: age1aaa,age1bbb\n"
	if string(got) != want {
		t.Errorf("policy = %q, want %q", got, want)
	}
}

func TestPublicKeyIsTheFirstIdentity(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "keys.txt")
	first := writeKey(t, file)
	second := writeKey(t, filepath.Join(dir, "other.txt"))
	a, _ := os.ReadFile(file)
	b, _ := os.ReadFile(filepath.Join(dir, "other.txt"))
	if err := os.WriteFile(file, append(append(a, '\n'), b...), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := New(Options{}).PublicKey(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != first || got == second {
		t.Errorf("PublicKey = %q, want the first identity", got)
	}
}

func TestPublicKeyOfCommentsOnlyIsNoKey(t *testing.T) {
	file := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(file, []byte("# public key: age1xyz\n\n# nothing else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{}).PublicKey(file); !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("error = %v, want ErrNoKey", err)
	}
}

func TestToolHint(t *testing.T) {
	b := New(Options{})
	const url = "https://github.com/getsops/sops/releases"
	for _, goos := range []string{"darwin", "linux"} {
		if got, want := b.ToolHint(goos), "Install it with `brew install sops`, or download it from "+url+"."; got != want {
			t.Errorf("ToolHint(%s) = %q, want %q", goos, got, want)
		}
	}
	if got, want := b.ToolHint("windows"), "Download it from "+url+"."; got != want {
		t.Errorf("ToolHint(windows) = %q, want %q", got, want)
	}
	if b.BrewFormula() != "sops" {
		t.Errorf("BrewFormula = %q", b.BrewFormula())
	}
}

func TestNewKeyWorksWithRealSops(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	public, private, err := b.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(private), "# public key: "+public+"\nAGE-SECRET-KEY-1") {
		t.Fatal("the private text is not in the key file format")
	}
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, private, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := b.WritePolicy(dir, []string{public}); err != nil {
		t.Fatal(err)
	}
	o := r.opts
	o.KeyFile = keyFile
	b = New(o)
	file := filepath.Join(dir, "common.enc.yaml")
	if err := b.Set(file, "TOKEN", "t0k"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := b.Decrypt(file)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if want := []backend.Pair{{Name: "TOKEN", Value: "t0k"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Decrypt = %v, want %v", got, want)
	}
}

// Setting one name leaves the stored text of every other name alone, so two
// versions of a file can be compared without a key.
func TestSetLeavesOtherStoredValuesAlone(t *testing.T) {
	r := newReal(t)
	b := New(r.opts)
	file := filepath.Join(r.dir, "common.enc.yaml")
	for _, name := range []string{"A", "B"} {
		if err := b.Set(file, name, "first"); err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Set(file, "B", "second"); err != nil {
		t.Fatalf("Set B: %v", err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := valuefile.ChangedNames(before, after)
	if err != nil {
		t.Fatalf("ChangedNames: %v", err)
	}
	if want := []string{"B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedNames = %v, want %v", got, want)
	}
}

func TestRewrapRunsUpdatekeysInTheFilesFolder(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, []string{"HOME=/h"})
	file := filepath.Join(dir, "common.enc.yaml")
	for _, name := range []string{PolicyFile, "common.enc.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("A: ENC[x]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Rewrap(file); err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	if len(r.cmds) != 1 {
		t.Fatalf("ran %d commands, want 1", len(r.cmds))
	}
	c := r.cmds[0]
	if want := []string{"updatekeys", "--yes", "common.enc.yaml"}; !slices.Equal(c.Args, want) {
		t.Errorf("Args = %v, want %v", c.Args, want)
	}
	if c.Dir != dir {
		t.Errorf("Dir = %q, want %q", c.Dir, dir)
	}
	for _, s := range append(slices.Clone(c.Args), c.Env...) {
		if strings.Contains(s, "AGE-SECRET-KEY") {
			t.Errorf("key text in the command: %q", s)
		}
	}
}

func TestRewrapWithoutPolicyRunsNothing(t *testing.T) {
	r := &recorder{}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	if err := os.WriteFile(file, []byte("A: ENC[x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.Rewrap(file); !errors.Is(err, backend.ErrNoPolicy) {
		t.Fatalf("Rewrap error = %v, want ErrNoPolicy", err)
	}
	if len(r.cmds) != 0 {
		t.Errorf("ran %d commands", len(r.cmds))
	}
}

func TestRewrapFailureNeverPassesStderr(t *testing.T) {
	r := &recorder{stderr: "boom s3cr3t-value", err: errors.New("exit status 1")}
	b, dir := recorded(t, r, nil)
	file := filepath.Join(dir, "common.enc.yaml")
	for _, name := range []string{PolicyFile, "common.enc.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("A: ENC[x]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := b.Rewrap(file)
	if err == nil || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("Rewrap error = %v", err)
	}
}

func TestRewrapWithAKeyThatCannotOpenTheFileIsNoKey(t *testing.T) {
	e := realEnv(t)
	file := filepath.Join(e.Dir, "common.enc.yaml")
	if err := e.Backend.Set(file, "A", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	second, _, err := e.Backend.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	// A policy the file already matches is a no-op that needs no key.
	if err := e.Backend.WritePolicy(e.Dir, []string{e.Public, second}); err != nil {
		t.Fatal(err)
	}
	e.Lock()
	err = e.Backend.Rewrap(file)
	if !errors.Is(err, backend.ErrNoKey) {
		t.Fatalf("Rewrap error = %v, want ErrNoKey", err)
	}
	if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Error("the error holds a value")
	}
}
