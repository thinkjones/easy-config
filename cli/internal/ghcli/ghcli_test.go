package ghcli

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/thinkjones/easy-config/cli/internal/proc"
)

// recorder is a gh that records every call and answers with what it holds.
type recorder struct {
	calls          []proc.Command
	stdout, stderr string
	err            error
}

func (r *recorder) gh() GH {
	return GH{
		Run: func(c proc.Command) ([]byte, []byte, error) {
			r.calls = append(r.calls, c)
			return []byte(r.stdout), []byte(r.stderr), r.err
		},
		LookPath: func(string) (string, error) { return "/usr/bin/gh", nil },
		Environ:  []string{"PATH=/usr/bin", "GH_TOKEN=tok"},
	}
}

// one fails unless gh ran once, and returns that call.
func (r *recorder) one(t *testing.T) proc.Command {
	t.Helper()
	if len(r.calls) != 1 || r.calls[0].Name != "gh" {
		t.Fatalf("ran %+v", r.calls)
	}
	return r.calls[0]
}

const secretValue = "FAKE-KEY-0123456789abcdef"

func TestInstalled(t *testing.T) {
	if !(&recorder{}).gh().Installed() {
		t.Fatal("gh on PATH is not installed")
	}
	missing := GH{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
	if missing.Installed() {
		t.Fatal("gh missing from PATH is installed")
	}
}

func TestSecretSetPutsTheValueOnStdinOnly(t *testing.T) {
	r := &recorder{}
	if err := r.gh().SecretSet("acme/app", "EZC_AGE_KEY", []byte(secretValue)); err != nil {
		t.Fatal(err)
	}
	c := r.one(t)
	if want := []string{"secret", "set", "EZC_AGE_KEY", "--repo", "acme/app"}; !reflect.DeepEqual(c.Args, want) {
		t.Fatalf("args %q, want %q", c.Args, want)
	}
	if string(c.Stdin) != secretValue {
		t.Fatalf("stdin %q", c.Stdin)
	}
	if !reflect.DeepEqual(c.Env, []string{"PATH=/usr/bin", "GH_TOKEN=tok"}) || c.Interactive {
		t.Fatalf("env %q, interactive %v", c.Env, c.Interactive)
	}
	for _, s := range append(append([]string{c.Dir, c.Name}, c.Args...), c.Env...) {
		if strings.Contains(s, secretValue) {
			t.Fatalf("the value is outside stdin: %q", s)
		}
	}
}

func TestAFailureHoldsNoStdinContent(t *testing.T) {
	// A gh that repeats what it was given on stdin, in the line that says what went wrong.
	r := &recorder{stderr: "failed to set secret " + secretValue + "\n", err: errors.New("exit status 1")}
	err := r.gh().SecretSet("acme/app", "EZC_AGE_KEY", []byte(secretValue+"\n"))
	if err == nil || strings.Contains(err.Error(), "FAKE-KEY-") {
		t.Fatalf("err %v", err)
	}
	if err.Error() != "gh: it failed" {
		t.Fatalf("err %q", err)
	}
}

func TestAFailureHoldsNoLineOfStdin(t *testing.T) {
	// The value is one line among others, and gh repeats that line alone, inside a sentence.
	for _, stdin := range []string{secretValue + "\n", "# public key: age1xyz\n" + secretValue + "\n", "  " + secretValue + "  \r\nmore\n"} {
		r := &recorder{stderr: "could not encrypt \"" + secretValue + "\" for acme/app\n", err: errors.New("exit status 1")}
		err := r.gh().SecretSet("acme/app", "EZC_AGE_KEY", []byte(stdin))
		if err == nil || err.Error() != "gh: it failed" {
			t.Fatalf("stdin %q: err %q", stdin, err)
		}
	}
}

func TestAFailureCarriesOneLineWithoutCredentials(t *testing.T) {
	r := &recorder{
		stdout: "never shown\n",
		stderr: "\nHTTP 404: Not Found (https://user:tok@api.github.com/repos/acme/app?access_token=tok)\nmore\n",
		err:    errors.New("exit status 1"),
	}
	err := r.gh().SecretDelete("acme/app", "EZC_AGE_KEY")
	if want := "gh: HTTP 404: Not Found (https://api.github.com/repos/acme/app"; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err %v, want it to start %q", err, want)
	}
	for _, hidden := range []string{"tok", "never shown", "more"} {
		if strings.Contains(err.Error(), hidden) {
			t.Fatalf("err %q holds %q", err, hidden)
		}
	}
	if want := []string{"secret", "delete", "EZC_AGE_KEY", "--repo", "acme/app"}; !reflect.DeepEqual(r.one(t).Args, want) {
		t.Fatalf("args %q", r.calls[0].Args)
	}
}

func TestAFailureWithNothingSaid(t *testing.T) {
	r := &recorder{err: errors.New("exit status 4")}
	if err := r.gh().SecretDelete("acme/app", "X"); err == nil || err.Error() != "gh: exit status 4" {
		t.Fatalf("err %v", err)
	}
}

func TestAFailureHoldsNoControlCharacter(t *testing.T) {
	r := &recorder{stderr: "X-Message: \x1b[2Jgone\x07 é\n", err: errors.New("exit status 1")}
	if err := r.gh().SecretDelete("acme/app", "X"); err == nil || err.Error() != "gh: X-Message: ?[2Jgone? é" {
		t.Fatalf("err %q", err)
	}
}

func TestNotInstalled(t *testing.T) {
	r := &recorder{err: exec.ErrNotFound}
	if err := r.gh().SecretSet("acme/app", "X", []byte("v")); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err %v", err)
	}
}

func TestPRCreate(t *testing.T) {
	r := &recorder{stdout: "Creating pull request for ezc/join-sam into main in acme/config\n\nhttps://github.com/acme/config/pull/7\n"}
	url, err := r.gh().PRCreate("/store", "ezc/join-sam", "a title", "a body")
	if err != nil || url != "https://github.com/acme/config/pull/7" {
		t.Fatalf("url %q, %v", url, err)
	}
	c := r.one(t)
	want := []string{"pr", "create", "--head", "ezc/join-sam", "--title", "a title", "--body", "a body"}
	if !reflect.DeepEqual(c.Args, want) || c.Dir != "/store" || len(c.Stdin) != 0 {
		t.Fatalf("ran %+v", c)
	}

	r = &recorder{stderr: "a pull request for branch \"ezc/join-sam\" already exists\n", err: errors.New("exit status 1")}
	if _, err := r.gh().PRCreate("/store", "ezc/join-sam", "t", "b"); err == nil || err.Error() != `gh: a pull request for branch "ezc/join-sam" already exists` {
		t.Fatalf("err %v", err)
	}
}
