//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoSecret is the value of the app repo's own secret.
const repoSecret = "r3p0-s3cr3t-e2e"

// in runs ezc on a machine in dir and keeps what it printed.
func (w *watch) in(e *e2e, dir, stdin string, args ...string) (stdout, stderr string, code int) {
	w.t.Helper()
	stdout, stderr, code = e.run(dir, stdin, args...)
	w.printed = append(w.printed, "ezc "+strings.Join(args, " ")+" (in "+dir+")\nstdout: "+stdout+"\nstderr: "+stderr)
	return stdout, stderr, code
}

// okIn is in for a command that must exit 0.
func (w *watch) okIn(e *e2e, dir, stdin string, args ...string) string {
	w.t.Helper()
	stdout, stderr, code := w.in(e, dir, stdin, args...)
	if code != 0 {
		w.t.Fatalf("ezc %v in %s: exit %d\nstdout: %s\nstderr: %s", args, dir, code, stdout, stderr)
	}
	return stdout
}

// gitIn runs git in dir on the machine, as a person who commits, and returns its trimmed stdout.
func gitIn(t *testing.T, e *e2e, dir string, args ...string) string {
	t.Helper()
	out, errOut, code := e.exec(dir, "", "git", append([]string{"-c", "user.name=A", "-c", "user.email=a@example.com"}, args...)...)
	if code != 0 {
		t.Fatalf("git %v in %s: exit %d\n%s%s", args, dir, code, out, errOut)
	}
	return strings.TrimSpace(out)
}

// commitApp commits the app repo's .ezc, as its owner does after a re-wrap.
func commitApp(t *testing.T, e *e2e, app, message string) {
	t.Helper()
	gitIn(t, e, app, "add", ".ezc")
	gitIn(t, e, app, "commit", "--quiet", "-m", message)
}

func TestRepoEndToEnd(t *testing.T) {
	root := t.TempDir()
	w := newWatch(t)
	a, bare := publishedStore(t, root)
	b := newMachine(t, root, "b")
	fakeGh(t, root, a, b)
	w.hide("the secret's value", e2eSecret)
	w.hide("the repo's secret", repoSecret)
	w.hide("A's private key", secretLineOf(t, a.key))

	// A repo of A's that keeps a secret of its own, encrypted under a copy of the store's policy, and is registered.
	app := filepath.Join(root, "a", "app")
	err := os.MkdirAll(app, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, a, app, "init", "--quiet")
	// The checkout is recorded as git names its top, with the links in the path resolved.
	if app, err = filepath.EvalSymlinks(app); err != nil {
		t.Fatal(err)
	}
	w.okIn(a, app, "", "repo", "init")
	policy, err := os.ReadFile(filepath.Join(a.store(), ".ezc", ".sops.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, ".ezc", ".sops.yaml"), policy, 0o644); err != nil {
		t.Fatal(err)
	}
	w.okIn(a, app, repoSecret, "set", "API_TOKEN", "--type", "secret", "--desc", "the app's token")
	commitApp(t, a, app, "ezc")
	if out := w.okIn(a, app, "", "repo", "add"); !strings.Contains(out, "✓ added app to repos.yaml\n") {
		t.Fatalf("repo add: %q", out)
	}
	w.ok(a, "", "store", "push")
	appFile := filepath.Join(app, ".ezc", "common.enc.yaml")
	appReads := func(key string) {
		t.Helper()
		if stdout, stderr, code := a.run(app, "", "get", "API_TOKEN", "--reveal"); code != 0 || stdout != repoSecret+"\n" {
			t.Fatalf("get --reveal in the repo: exit %d\nstdout: %s\nstderr: %s", code, strings.ReplaceAll(stdout, repoSecret, "<the value>"), stderr)
		}
		data, err := os.ReadFile(appFile)
		if err != nil {
			t.Fatal(err)
		}
		if len(dataKeys(t, data, key)) != 1 {
			t.Fatalf("the repo's file is not encrypted for the key in %s", key)
		}
	}
	appReads(a.key)

	// B joins and is approved: the repo follows the store, and its owner commits the result there.
	w.ok(b, "", "join", bare, "--name", "sam")
	w.hide("B's private key", secretLineOf(t, b.key))
	out := w.ok(a, "", "user", "approve", "sam", "--yes")
	rewrapped := "✓ app: re-wrapped common.enc.yaml in " + filepath.Join(app, ".ezc") + ". Commit it there.\n"
	if !strings.Contains(out, "✓ approve sam\n") || !strings.Contains(out, rewrapped) {
		t.Fatalf("approve: no re-wrap of the repo in\n%s", out)
	}
	appReads(a.key)
	if data, err := os.ReadFile(appFile); err != nil || len(dataKeys(t, data, b.key)) != 1 {
		t.Fatalf("the repo's file is not encrypted for B's key (%v)", err)
	}
	commitApp(t, a, app, "let sam in")

	// Removing B re-wraps the repo with a new data key and lists the repo's secret B could read.
	out = w.ok(a, "", "user", "remove", "sam")
	for _, want := range []string{"✓ remove sam\n", rewrapped, "! app: common.enc.yaml/API_TOKEN could be read by a key that has left. Give it a new value there: ezc set API_TOKEN.\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("remove: no %q in\n%s", want, out)
		}
	}
	commitApp(t, a, app, "shut sam out")
	appReads(a.key)
	wantShutOutIn(t, a, app, "common.enc.yaml", a.key, b.key)

	// A rotation of A's key re-wraps the repo with the old key before it moves to the new one.
	out = w.ok(a, "", "user", "rotate")
	_, rest, ok := strings.Cut(out, "✓ new key in ")
	fresh, _, _ := strings.Cut(rest, "\n")
	if !ok || !strings.Contains(out, rewrapped) || strings.Contains(out, "could be read by a key that has left") {
		t.Fatalf("rotate:\n%s", out)
	}
	w.hide("A's new private key", secretLineOf(t, fresh))
	commitApp(t, a, app, "rotate")
	appReads(fresh)
	wantShutOutIn(t, a, app, "common.enc.yaml", fresh, a.key)
	reads(t, a, e2eSecret)

	// The repo requires API_TOKEN: the store's value is not removed under it.
	if stdout, stderr, code := w.run(a, "", "unset", "-g", "API_TOKEN"); code != 1 || !strings.Contains(stderr, "API_TOKEN is required by app.") {
		t.Fatalf("unset -g: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	reads(t, a, e2eSecret)

	if out := w.ok(a, "", "repo", "check", "--all"); !strings.Contains(out, "app ("+app+")\n") {
		t.Fatalf("repo check --all:\n%s", out)
	}
	if got := gitIn(t, a, app, "status", "--porcelain"); got != "" {
		t.Errorf("the repo has uncommitted changes:\n%s", got)
	}
	if strings.Contains(gitIn(t, a, app, "log", "--all", "--patch"), repoSecret) {
		t.Error("a commit of the repo holds its secret in clear")
	}
	w.done(a, b)
}
