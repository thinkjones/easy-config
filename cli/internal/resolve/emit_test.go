package resolve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var nasty = map[string]string{
	"PLAIN":   "v",
	"QUOTE":   `it's`,
	"CMD":     "$(rm -rf /)`id`",
	"MULTI":   "a\nb",
	"DQ":      `say "hi" \ $X`,
	"EZC_ENV": "local",
	"APP_ENV": "local",
}

func TestShell(t *testing.T) {
	got := Result{Env: nasty}.Shell()
	want := "export CMD='$(rm -rf /)`id`'\nexport DQ='say \"hi\" \\ $X'\nexport MULTI='a\nb'\nexport PLAIN='v'\nexport QUOTE='it'\\''s'\n"
	if got != want {
		t.Errorf("Shell() =\n%q\nwant\n%q", got, want)
	}
}

func TestShellRoundTripsThroughSh(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is absent")
	}
	pwned := filepath.Join(t.TempDir(), "pwned")
	env := map[string]string{}
	for k, v := range nasty {
		env[k] = v
	}
	env["CMD"] = "$(touch " + pwned + ")`touch " + pwned + "`"
	r := Result{Env: env}
	script := r.Shell()
	for _, name := range r.Exported() {
		out, err := exec.Command("/bin/sh", "-c", `eval "$1"; printf %s "$`+name+`"`, "sh", script).Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(out) != env[name] {
			t.Errorf("%s round-tripped as %q, want %q", name, out, env[name])
		}
	}
	if _, err := os.Stat(pwned); !os.IsNotExist(err) {
		t.Errorf("command substitution ran: %s exists (stat err %v)", pwned, err)
	}
}

func TestJSON(t *testing.T) {
	r := Result{Env: nasty}
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for k, v := range nasty {
		if k != "EZC_ENV" && k != "APP_ENV" {
			want[k] = v
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("JSON() = %v, want %v", got, want)
	}
	if !strings.HasSuffix(string(b), "}\n") {
		t.Errorf("JSON() does not end in }\\n: %q", b)
	}
	if !strings.HasPrefix(string(b), "{\n  \"CMD\"") {
		t.Errorf("JSON() not two-space indented with sorted keys: %q", b)
	}
	b2, _ := r.JSON()
	if string(b) != string(b2) {
		t.Error("two JSON() calls differ")
	}
}

func TestEmptyResult(t *testing.T) {
	r := Result{Env: map[string]string{}}
	if got := r.Shell(); got != "" {
		t.Errorf("Shell() = %q", got)
	}
	b, err := r.JSON()
	if err != nil || string(b) != "{}\n" {
		t.Errorf("JSON() = %q, %v", b, err)
	}
}

func TestShellNamesAreIdentifiers(t *testing.T) {
	re := regexp.MustCompile(`^export [A-Za-z_][A-Za-z0-9_]*='`)
	for _, line := range strings.SplitAfter(Result{Env: nasty}.Shell(), "\n") {
		if line == "" || !strings.Contains(line, "export ") {
			continue
		}
		if strings.HasPrefix(line, "export ") && !re.MatchString(line) {
			t.Errorf("line does not start with an identifier export: %q", line)
		}
	}
}
