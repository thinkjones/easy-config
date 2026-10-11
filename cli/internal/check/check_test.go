package check

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

func mf(t *testing.T, text string) manifest.Manifest {
	t.Helper()
	var m manifest.Manifest
	if err := yaml.Unmarshal([]byte(text), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func res(env map[string]string) resolve.Result {
	return resolve.Result{Env: env, Origin: map[string]string{}, EnvName: "local"}
}

// one runs Run and requires exactly one problem, returning it.
func one(t *testing.T, m manifest.Manifest, env map[string]string) Problem {
	t.Helper()
	ps := Run(m, res(env))
	if len(ps) != 1 {
		t.Fatalf("want 1 problem, got %v", ps)
	}
	return ps[0]
}

func TestRequiredMissing(t *testing.T) {
	m := mf(t, "required:\n  A: {type: str, desc: a}\n")
	if p := one(t, m, map[string]string{}); p.Why != "not set" || p.Name != "A" {
		t.Errorf("got %+v", p)
	}
}

func TestRequiredEmpty(t *testing.T) {
	m := mf(t, "required:\n  A: {type: str, desc: a}\n")
	if p := one(t, m, map[string]string{"A": "  "}); p.Why != "set but empty" {
		t.Errorf("got %+v", p)
	}
}

func TestPlaceholder(t *testing.T) {
	m := mf(t, "required:\n  A: {type: str, desc: a}\n")
	if p := one(t, m, map[string]string{"A": "Replace-Me"}); p.Why != `still the placeholder "Replace-Me"` {
		t.Errorf("got %+v", p)
	}
}

func TestOptionalMissingIsFine(t *testing.T) {
	m := mf(t, "required: {}\noptional:\n  A: {type: str, desc: a}\n")
	if ps := Run(m, res(map[string]string{})); len(ps) != 0 {
		t.Errorf("got %v", ps)
	}
}

const requiredWhen = `
required:
  OUT: {type: enum, desc: out, values: [file, s3]}
optional:
  P: {type: path, desc: p, requiredWhen: {var: OUT, equals: file}}
`

func TestRequiredWhenMet(t *testing.T) {
	p := one(t, mf(t, requiredWhen), map[string]string{"OUT": "file"})
	if p.Name != "P" || p.Why != "required because OUT=file" {
		t.Errorf("got %+v", p)
	}
}

func TestRequiredWhenNotMet(t *testing.T) {
	if ps := Run(mf(t, requiredWhen), res(map[string]string{"OUT": "s3"})); len(ps) != 0 {
		t.Errorf("got %v", ps)
	}
}

func TestIntNotInteger(t *testing.T) {
	m := mf(t, "required:\n  N: {type: int, desc: n}\n")
	if p := one(t, m, map[string]string{"N": "x"}); p.Why != `not an integer: "x"` {
		t.Errorf("got %+v", p)
	}
}

func TestIntBelowMin(t *testing.T) {
	m := mf(t, "required:\n  N: {type: int, desc: n, min: 1}\n")
	if p := one(t, m, map[string]string{"N": "0"}); p.Why != "0 is below the minimum of 1" {
		t.Errorf("got %+v", p)
	}
}

func TestIntAboveMax(t *testing.T) {
	m := mf(t, "required:\n  N: {type: int, desc: n, max: 100}\n")
	if p := one(t, m, map[string]string{"N": "101"}); p.Why != "101 is above the maximum of 100" {
		t.Errorf("got %+v", p)
	}
}

func TestBool(t *testing.T) {
	m := mf(t, "required:\n  B: {type: bool, desc: b}\n")
	for _, ok := range []string{"1", "0", "true", "false", "yes", "no", "TRUE"} {
		if ps := Run(m, res(map[string]string{"B": ok})); len(ps) != 0 {
			t.Errorf("%q: got %v", ok, ps)
		}
	}
	if p := one(t, m, map[string]string{"B": "maybe"}); p.Why != `not a boolean: "maybe"` {
		t.Errorf("got %+v", p)
	}
}

func TestEnum(t *testing.T) {
	m := mf(t, "required:\n  E: {type: enum, desc: e, values: [file, s3]}\n")
	if p := one(t, m, map[string]string{"E": "slack"}); p.Why != `"slack" is not one of: file, s3` {
		t.Errorf("got %+v", p)
	}
}

func TestURL(t *testing.T) {
	m := mf(t, "required:\n  U: {type: url, desc: u}\n")
	if p := one(t, m, map[string]string{"U": "ftp://x"}); p.Why != `not a URL: "ftp://x"` {
		t.Errorf("got %+v", p)
	}
	if ps := Run(m, res(map[string]string{"U": "https://x"})); len(ps) != 0 {
		t.Errorf("got %v", ps)
	}
}

func TestProblemsSortedAndComplete(t *testing.T) {
	m := mf(t, "required:\n  C: {type: str, desc: c}\n  A: {type: str, desc: a}\n  B: {type: str, desc: b}\n")
	ps := Run(m, res(map[string]string{}))
	if len(ps) != 3 || ps[0].Name != "A" || ps[1].Name != "B" || ps[2].Name != "C" {
		t.Errorf("got %v", ps)
	}
}

func TestExtraValuesIgnored(t *testing.T) {
	m := mf(t, "required:\n  A: {type: str, desc: a}\n")
	if ps := Run(m, res(map[string]string{"A": "1", "Z": "2"})); len(ps) != 0 {
		t.Errorf("got %v", ps)
	}
}

func sources() []resolve.Source {
	return []resolve.Source{
		{Path: "/store/.ezc/common.yaml", Layer: "store", Present: true, Vars: []string{"A", "B"}},
		{Path: "/store/.ezc/env.local.yaml", Layer: "store", Present: false},
		{Path: "/repo/.ezc/env.local.yaml", Layer: "repo", Present: true, Vars: []string{"B"}},
	}
}

func TestReportOK(t *testing.T) {
	r := resolve.Result{
		Env:     map[string]string{"A": "1", "B": "2", "EZC_ENV": "local", "APP_ENV": "local"},
		EnvName: "local",
		Sources: []resolve.Source{
			{Path: "a", Vars: []string{"A"}},
			{Path: "b", Vars: []string{"B"}},
		},
	}
	want := "environment local ✓ 2 vars from 2 files\n"
	if got := ReportOK(r); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestReportOKSingular(t *testing.T) {
	r := resolve.Result{Env: map[string]string{"A": "1"}, EnvName: "dev", Sources: []resolve.Source{{Path: "a", Vars: []string{"A"}}}}
	want := "environment dev ✓ 1 var from 1 file\n"
	if got := ReportOK(r); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestReportNamesEveryProblemAndFile(t *testing.T) {
	r := resolve.Result{Env: map[string]string{}, EnvName: "local", Sources: sources()}
	got := Report([]Problem{{"A", "the a", "not set"}, {"B", "", "set but empty"}}, r)
	if !strings.HasPrefix(got, "environment local: validation failed\n") {
		t.Errorf("header: %q", got)
	}
	for _, want := range []string{"  A — the a\n      not set", "  B — set but empty", "\nFiles, in precedence order:\n",
		"  /store/.ezc/common.yaml (2 vars)", "- /store/.ezc/env.local.yaml (0 vars)", "  /repo/.ezc/env.local.yaml (1 vars)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if !strings.Contains(got, "\n\nFiles, in precedence order:") {
		t.Errorf("no blank line before files:\n%s", got)
	}
}

func TestReportVerboseHasNoValues(t *testing.T) {
	r := resolve.Result{
		Env: map[string]string{"SECRET": "hunter2-value", "B": "bval-xyz", "EZC_ENV": "local"},
		Origin: map[string]string{
			"SECRET":  "/home/u/store/.ezc/common.yaml",
			"B":       "/home/u/repo/.ezc/env.local.yaml",
			"EZC_ENV": resolve.ShellOrigin,
		},
		EnvName: "local",
		Sources: []resolve.Source{
			{Path: "/home/u/store/.ezc/common.yaml", Vars: []string{"SECRET", "B"}},
			{Path: "/home/u/repo/.ezc/env.local.yaml", Vars: []string{"B"}},
		},
	}
	got := ReportVerbose(r, "/home/u")
	for _, secret := range []string{"hunter2-value", "bval-xyz"} {
		if strings.Contains(got, secret) {
			t.Fatalf("value %q leaked:\n%s", secret, got)
		}
	}
	for _, want := range []string{"SECRET", "~/store/.ezc/common.yaml", "(overrides common.yaml)", "shell",
		"Names and paths only — no values. Use `ezc env export` to emit them.\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "/home/u/") {
		t.Errorf("home not shortened:\n%s", got)
	}
}

func TestPlaceholderWordsSorted(t *testing.T) {
	w := PlaceholderWords()
	for i := 1; i < len(w); i++ {
		if w[i-1] >= w[i] {
			t.Fatalf("not sorted: %v", w)
		}
	}
	if !IsPlaceholder(" TODO ") || IsPlaceholder("real") {
		t.Error("IsPlaceholder")
	}
}
