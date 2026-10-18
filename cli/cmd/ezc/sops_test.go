//go:build unix

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

const e2eSecret = "s3cr3t-e2e"

// e2e is a throwaway home, store and repo, and a way to run the built ezc in them.
type e2e struct {
	t       *testing.T
	home    string
	ezcHome string
	repo    string
	key     string // the key file
	pub     string // its public key, which the repo's policy encrypts to
	path    string
	env     []string // more of the child's environment, after HOME, EZC_HOME and PATH
}

func requireSopsOnPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sops")
	if err != nil {
		if os.Getenv("EZC_REQUIRE_SOPS") == "1" {
			t.Fatal("sops is required (EZC_REQUIRE_SOPS=1) but is not on PATH")
		}
		t.Skip("sops is not installed")
	}
	return p
}

// writeAgeKey writes a freshly generated age identity to path and returns its public key.
func writeAgeKey(t *testing.T, path string) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return id.Recipient().String()
}

// newE2E makes the folders and the key. pathDirs come first on PATH, ahead of the folder holding sops.
func newE2E(t *testing.T, pathDirs ...string) *e2e {
	t.Helper()
	sops := requireSopsOnPath(t)
	root := t.TempDir()
	e := &e2e{
		t:       t,
		home:    filepath.Join(root, "home"),
		ezcHome: filepath.Join(root, "ezc"),
		repo:    filepath.Join(root, "repo"),
	}
	for _, d := range []string{e.home, e.ezcHome, e.repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.key = filepath.Join(e.home, ".config", "sops", "age", "keys.txt")
	e.path = strings.Join(append(pathDirs, filepath.Dir(sops), "/usr/bin", "/bin"), string(os.PathListSeparator))
	e.pub = writeAgeKey(t, e.key)
	e.writePolicy(filepath.Join(e.repo, ".ezc"), e.pub)
	return e
}

func (e *e2e) writePolicy(dir, pub string) {
	e.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	policy := "creation_rules:\n  - path_regex: \\.enc\\.yaml$\n    age: " + pub + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".sops.yaml"), []byte(policy), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// run runs ezc in dir with a clean environment that names no key and no editor.
func (e *e2e) run(dir, stdin string, args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	return e.exec(dir, stdin, binary, args...)
}

func (e *e2e) exec(dir, stdin, name string, args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"HOME=" + e.home, "EZC_HOME=" + e.ezcHome, "PATH=" + e.path}, e.env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	var exit *exec.ExitError
	if err := cmd.Run(); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		e.t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

func (e *e2e) mustRun(dir, stdin string, args ...string) string {
	e.t.Helper()
	out, errOut, code := e.run(dir, stdin, args...)
	if code != 0 {
		e.t.Fatalf("ezc %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return out
}

func (e *e2e) setSecret(value string) {
	e.t.Helper()
	e.mustRun(e.repo, value, "set", "API_TOKEN", "--type", "secret", "--desc", "token")
}

func TestSecretsEndToEnd(t *testing.T) {
	e := newE2E(t)
	e.mustRun(e.repo, "", "repo", "init")
	out := e.mustRun(e.repo, e2eSecret, "set", "API_TOKEN", "--type", "secret", "--desc", "token")
	if !strings.Contains(out, "set API_TOKEN in repo/common.enc.yaml") || strings.Contains(out, e2eSecret) {
		t.Fatalf("set: %q", out)
	}
	enc := filepath.Join(e.repo, ".ezc", "common.enc.yaml")
	data, err := os.ReadFile(enc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), e2eSecret) || !strings.Contains(string(data), "ENC[") {
		t.Fatal("the encrypted file holds the value in plaintext, or nothing encrypted")
	}

	if out := e.mustRun(e.repo, "", "env"); strings.Contains(out, e2eSecret) {
		t.Fatalf("env printed the value: %q", out)
	}
	if out := e.mustRun(e.repo, "", "env", "export"); !strings.Contains(out, "export API_TOKEN='"+e2eSecret+"'") {
		t.Fatalf("env export: %q", out)
	}
	if out := e.mustRun(e.repo, "", "run", "--", "sh", "-c", `printf %s "$API_TOKEN"`); out != e2eSecret {
		t.Fatalf("run: %q", out)
	}
	if out, _, code := e.run(e.repo, "", "get", "API_TOKEN"); code != 1 || out != "" {
		t.Fatalf("get without --reveal: exit %d stdout %q", code, out)
	}
	if out := e.mustRun(e.repo, "", "get", "API_TOKEN", "--reveal"); out != e2eSecret+"\n" {
		t.Fatalf("get --reveal: %q", out)
	}
	out = e.mustRun(e.repo, "", "list")
	if !strings.Contains(out, "API_TOKEN  secret  repo/common.enc.yaml") || strings.Contains(out, e2eSecret) {
		t.Fatalf("list: %q", out)
	}
	stdout, stderr, code := e.run(e.repo, "", "doctor")
	if code != 0 || strings.Contains(stdout+stderr, e2eSecret) {
		t.Fatalf("doctor: exit %d\n%s%s", code, stdout, stderr)
	}

	e.mustRun(e.repo, "", "unset", "API_TOKEN", "--undeclare")
	if _, err := os.Stat(enc); !os.IsNotExist(err) {
		t.Fatalf("the encrypted file is still there: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(e.repo, ".ezc", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "API_TOKEN") {
		t.Fatalf("the declaration is still there:\n%s", manifest)
	}
}

func TestNoKeyEndToEnd(t *testing.T) {
	e := newE2E(t)
	e.mustRun(e.repo, "", "repo", "init")
	e.setSecret(e2eSecret)
	// The shim runs the ezc it finds on PATH, so the built one must be there for its steps to test anything.
	shim := func(script string) (stdout, stderr string, code int) {
		t.Helper()
		saved := e.path
		defer func() { e.path = saved }()
		e.path = filepath.Dir(binary) + string(os.PathListSeparator) + saved
		return e.exec(e.repo, "", "sh", "-c", ". .ezc/env.sh; "+script)
	}
	if out, errOut, code := shim(`printf %s "$API_TOKEN"`); code != 0 || out != e2eSecret || errOut != "" {
		t.Fatalf("shim with the key: exit %d stdout %q stderr %q", code, out, errOut)
	}
	writeAgeKey(t, e.key) // a different key now sits where the right one was

	stdout, stderr, code := e.run(e.repo, "", "env")
	if code != 1 || !strings.Contains(stderr, "cannot decrypt") || !strings.Contains(stderr, e.key) || strings.Contains(stdout+stderr, e2eSecret) {
		t.Fatalf("env: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stdout, _, code := e.run(e.repo, "", "env", "export"); code != 1 || stdout != "" {
		t.Fatalf("env export: exit %d stdout %q", code, stdout)
	}
	if out, errOut, code := shim(`printf %s "$API_TOKEN"; echo ok`); code != 0 || out != "ok\n" || errOut != "" {
		t.Fatalf("shim without the key: exit %d stdout %q stderr %q", code, out, errOut)
	}
	stdout, stderr, code = e.run(e.repo, "", "doctor")
	if code != 0 || !strings.Contains(stdout, "cannot decrypt: no key") || strings.Contains(stdout+stderr, e2eSecret) {
		t.Fatalf("doctor: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestNoValueInAnyProcessListing(t *testing.T) {
	sops := requireSopsOnPath(t)
	wrapDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "sops.log")
	script := "#!/bin/sh\n{ printf '%s\\n' \"$@\"; env; } >> '" + log + "'\nexec '" + sops + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapDir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := newE2E(t, wrapDir)
	e.mustRun(e.repo, "", "repo", "init")
	e.mustRun(e.repo, "ps-probe-value", "set", "TOK", "--type", "secret", "--desc", "d")

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the wrapper was never used: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("the wrapper logged nothing")
	}
	if strings.Contains(string(data), "ps-probe-value") {
		t.Fatalf("the value reached sops's arguments or environment:\n%s", data)
	}
}

func TestStoreAuditEndToEnd(t *testing.T) {
	e := newE2E(t)
	store := filepath.Join(e.ezcHome, "store", ".ezc")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	e.writePolicy(store, e.pub)
	if err := os.WriteFile(filepath.Join(store, "manifest.yaml"), []byte("schemaVersion: v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun(e.repo, e2eSecret, "set", "-g", "API_TOKEN", "--type", "secret", "--desc", "token")
	if stdout, stderr, code := e.run(e.repo, "", "store", "audit"); code != 0 {
		t.Fatalf("audit of a clean store: exit %d\n%s%s", code, stdout, stderr)
	}

	f, err := os.OpenFile(filepath.Join(store, "common.yaml"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("STRAY: stray-plain-value\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	stdout, stderr, code := e.run(e.repo, "", "store", "audit")
	all := stdout + stderr
	if code != 1 || !strings.Contains(all, "STRAY") || strings.Contains(all, "stray-plain-value") || strings.Contains(all, e2eSecret) {
		t.Fatalf("audit of a store with an undeclared value: exit %d\n%s", code, all)
	}
}

func TestNoToolEndToEnd(t *testing.T) {
	e := newE2E(t)
	e.mustRun(e.repo, "", "repo", "init")
	e.setSecret(e2eSecret)
	e.path = t.TempDir() // nothing is installed here

	for _, args := range [][]string{{"env"}, {"env", "export"}, {"list"}, {"get", "API_TOKEN", "--reveal"}, {"run", "--", "true"}, {"set", "OTHER", "--type", "secret", "--desc", "d"}} {
		stdout, stderr, code := e.run(e.repo, "v", args...)
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "ezc: sops is not installed. Install it") || strings.Contains(stderr, "could not") {
			t.Errorf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
		}
	}
	stdout, stderr, code := e.run(e.repo, "", "doctor")
	if code != 1 || !strings.Contains(stdout, "sops is not installed") || strings.Contains(stdout+stderr, "could not") {
		t.Fatalf("doctor: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestPolicyThatMatchesNothingEndToEnd(t *testing.T) {
	e := newE2E(t)
	e.mustRun(e.repo, "", "repo", "init")
	dir := filepath.Join(e.repo, ".ezc")
	if err := os.WriteFile(filepath.Join(dir, ".sops.yaml"), []byte("creation_rules:\n  - path_regex: ^other$\n    age: "+e.pub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := e.run(e.repo, e2eSecret, "set", "API_TOKEN", "--type", "secret", "--desc", "token")
	// The folder is named as the process sees it, which on macOS is under /private.
	want := filepath.Join(dir, ".sops.yaml") + " has no rule that matches common.enc.yaml. Run ezc repo rewrap to write it from the store's recipients.\n"
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "ezc: ") || !strings.HasSuffix(stderr, want) || strings.Count(stderr, "rewrap") != 1 {
		t.Fatalf("set: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	after, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the manifest changed (%v)", err)
	}
}

func TestHandAddedPlaintextLineEndToEnd(t *testing.T) {
	e := newE2E(t)
	store := filepath.Join(e.ezcHome, "store", ".ezc")
	e.writePolicy(store, e.pub)
	if err := os.WriteFile(filepath.Join(store, "manifest.yaml"), []byte("schemaVersion: v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun(e.repo, e2eSecret, "set", "-g", "API_TOKEN", "--type", "secret", "--desc", "token")
	enc := filepath.Join(store, "common.enc.yaml")
	data, err := os.ReadFile(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enc, append([]byte("LEAKED: leaked-plain-value\n"), data...), 0o644); err != nil {
		t.Fatal(err)
	}

	// The key is present, and sops refuses the whole file; the audit must still name the leak.
	stdout, stderr, code := e.run(e.repo, "", "store", "audit")
	all := stdout + stderr
	for _, want := range []string{
		"LEAKED (common.enc.yaml) — is in an encrypted file but its value is not encrypted",
		"Restore common.enc.yaml from git, then set the value with `ezc set -g LEAKED`",
		"! damaged: value checks skipped for common.enc.yaml",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("audit: no %q", want)
		}
	}
	if code != 1 || strings.Contains(all, "leaked-plain-value") || strings.Contains(all, e2eSecret) {
		t.Fatalf("audit: exit %d\n%s", code, all)
	}

	stdout, stderr, code = e.run(e.repo, "", "doctor")
	all = stdout + stderr
	if code != 1 || !strings.Contains(stdout, "to fix — run `ezc store audit`") || !strings.Contains(stdout, "\nrepo\n") {
		t.Fatalf("doctor: exit %d\n%s", code, all)
	}
	if strings.Contains(all, "leaked-plain-value") || strings.Contains(all, e2eSecret) {
		t.Fatalf("doctor printed a value:\n%s", all)
	}

	// Commands that load the file keep failing, with the backend's fixed sentence.
	if stdout, stderr, code := e.run(e.repo, "", "env", "export"); code != 1 || stdout != "" || !strings.Contains(stderr, "could not read this file") || strings.Contains(stderr, "leaked-plain-value") {
		t.Fatalf("env export: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}
