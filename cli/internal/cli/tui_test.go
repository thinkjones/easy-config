package cli

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
	"github.com/thinkjones/easy-config/cli/internal/tui"
)

// tuiValue is the row of vals named name.
func tuiValue(t *testing.T, vals []tui.Value, name string) tui.Value {
	t.Helper()
	for _, v := range vals {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no %s in %+v", name, vals)
	return tui.Value{}
}

func TestTUICommandsRunInProcessWithoutATerminal(t *testing.T) {
	f := newFake(t)
	f.tty = true
	storeWriteStore(t, f)
	out := tuiDeps(f.sys()).Commands.Run([]string{"set", "-g", "TOKEN"}, "v4lue-tui\n")
	if !out.OK || slices.ContainsFunc(out.Lines, func(l string) bool { return strings.Contains(l, "v4lue-tui") }) {
		t.Fatalf("%+v", out)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("a prompt was shown: %v", f.prompts)
	}
	res, err := load(f.sys(), "", "")
	if err != nil || res.Env["TOKEN"] != "v4lue-tui" {
		t.Fatalf("%v %q", err, res.Env["TOKEN"])
	}
	if f.out.Len() != 0 || f.errOut.Len() != 0 {
		t.Fatalf("printed to the terminal: %q %q", f.out.String(), f.errOut.String())
	}
	// A usage error is the outcome's last line.
	out = tuiDeps(f.sys()).Commands.Run([]string{"set", "-g", "NOPE=1"}, "")
	if out.OK || !strings.HasPrefix(out.Lines[len(out.Lines)-1], "✗ ezc set: that is not a valid variable name") {
		t.Fatalf("%+v", out)
	}
}

func TestTUICommandsNeverAnswerTheCommandsQuestion(t *testing.T) {
	f := newFake(t)
	x := effectIntruder(t, f)
	before := readFile(t, x.file(recipients.File))
	out := tuiDeps(f.sys()).Commands.Run([]string{"user", "add", "zed", "fake1dd04"}, "y\n")
	if out.OK || !slices.ContainsFunc(out.Lines, func(l string) bool { return strings.Contains(l, "this would also let mallory read the store") }) {
		t.Fatalf("%+v", out)
	}
	if readFile(t, x.file(recipients.File)) != before {
		t.Fatal("recipients.yaml changed")
	}
}

func TestTUIValuesList(t *testing.T) {
	x := repoStore(t, "")
	f := x.f
	f.stdin = "s3cr3t-tui\n"
	x.out("set", "-g", "TOKEN")
	f.stdin = ""
	x.out("set", "-g", "NAME", "n4me")
	x.out("set", "-g", "SIZE", "3", "--env", "local")
	x.out("set", "-g", "MIXED", "$TOKEN-x", "--type", "str", "--desc", "built from a secret", "--expand", "--env", "local")
	// A registered repo whose checkout requires NAME, and a rotation due for TOKEN.
	app := checkoutOfRepo(t, f, "NAME: n\nSIZE: 3\n", "")
	x.out("repo", "add", "o/app")
	x.setConfig(config.Config{User: "gene", Checkouts: map[string]string{"o/app": app}})
	due := recipients.Due{}
	due.Add("common.enc.yaml", "TOKEN", "sam", "2026-10-01")
	if err := due.Save(filepath.Join(x.store, ezcDir)); err != nil {
		t.Fatal(err)
	}
	vals, err := tuiDeps(f.sys()).Values.List(tui.StoreScope, "local")
	if err != nil {
		t.Fatal(err)
	}
	tok := tuiValue(t, vals, "TOKEN")
	if !tok.Secret || tok.Shown != "" || tok.File != "store/common.enc.yaml" || tok.Type != "secret" || tok.Due != "readable by removed sam (2026-10-01)" {
		t.Errorf("TOKEN %+v", tok)
	}
	name := tuiValue(t, vals, "NAME")
	if name.Secret || name.Shown != "n4me" || name.Desc != "who" || !slices.Equal(name.RequiredBy, []string{"o/app"}) {
		t.Errorf("NAME %+v", name)
	}
	if size := tuiValue(t, vals, "SIZE"); size.File != "store/env.local.yaml" {
		t.Errorf("SIZE %+v", size)
	}
	if mixed := tuiValue(t, vals, "MIXED"); !mixed.Secret || mixed.Shown != "" {
		t.Errorf("MIXED %+v", mixed)
	}
	for _, v := range vals {
		if strings.Contains(v.Shown, "s3cr3t") {
			t.Fatalf("a secret is shown: %+v", v)
		}
	}
	// In the repo, a declared name with no value is listed with no file.
	write(t, filepath.Join(f.wd, ezcDir, "manifest.yaml"), "schemaVersion: v1\nrequired:\n  PORT: {type: int, desc: the port}\n")
	vals, err = tuiDeps(f.sys()).Values.List(tui.RepoScope, "local")
	if err != nil {
		t.Fatal(err)
	}
	if port := tuiValue(t, vals, "PORT"); port.File != "" || port.Type != "int" {
		t.Errorf("PORT %+v", port)
	}
}

func TestTUIValuesReveal(t *testing.T) {
	x := repoStore(t, "")
	x.f.stdin = "s3cr3t-tui\n"
	x.out("set", "-g", "TOKEN")
	d := tuiDeps(x.f.sys())
	if v, err := d.Values.Reveal(tui.StoreScope, "TOKEN", ""); err != nil || v != "s3cr3t-tui" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := d.Values.Reveal(tui.StoreScope, "MISSING", ""); err == nil || !strings.Contains(err.Error(), "MISSING is not set") {
		t.Fatalf("got %v", err)
	}
}

func TestTUIStatus(t *testing.T) {
	x := repoStore(t, "")
	x.theirs(1)
	x.f.env["EZC_ENV"] = "prod"
	h, err := tuiDeps(x.f.sys()).Store.Status()
	if err != nil || h.Behind != 1 || h.Env != "prod" || h.Repo != filepath.Base(x.f.wd) || h.Store != x.bare {
		t.Fatalf("%+v %v", h, err)
	}
	// Outside a repo, and with a store that is no git repo.
	f := newFake(t)
	write(t, filepath.Join(f.env["EZC_HOME"], "store", ezcDir, "manifest.yaml"), empty)
	h, err = tuiDeps(f.sys()).Store.Status()
	if err != nil || h.Repo != "" || h.Behind != 0 || h.Unknown || h.Store != filepath.Join(f.env["EZC_HOME"], "store") {
		t.Fatalf("%+v %v", h, err)
	}
}

func TestTUIAccessLoad(t *testing.T) {
	f := newFake(t)
	effectIntruder(t, f)
	d, err := tuiDeps(f.sys()).Access.Load()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range d.Recipients {
		names = append(names, r.Name)
		if r.Me != (r.Name == "gene") {
			t.Errorf("%s Me %v", r.Name, r.Me)
		}
	}
	if !slices.Equal(names, []string{"ci", "gene", "mallory", "sam"}) {
		t.Fatalf("names %v", names)
	}
	if ci := d.Recipients[0]; ci.Kind != "bot" || !slices.Equal(ci.Repos, []string{"o/a"}) {
		t.Errorf("ci %+v", ci)
	}
	if len(d.Differ) == 0 {
		t.Error("mallory's key is in no file, so the files differ")
	}
	// A store with no registry says so.
	g := newFake(t)
	storeWriteStore(t, g)
	if _, err := tuiDeps(g.sys()).Access.Load(); err == nil {
		t.Fatal("no error without recipients.yaml")
	}
}

func TestTUIAccessRequestsAreMadePrintable(t *testing.T) {
	f := newFake(t)
	x := accessStore(t, f)
	write(t, filepath.Join(x.store, ezcDir, "requests", "sam.yaml"), "name: sam\nkey: "+accessSam+"\nkind: user\nhost: \"h\\e[2Jx\"\nrequested: 2026-10-05\n")
	d, err := tuiDeps(f.sys()).Access.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Requests) != 1 || d.Requests[0].Name != "sam" || strings.Contains(d.Requests[0].Host, "\x1b") || d.Requests[0].Key != accessSam || d.Requests[0].Problem != "" {
		t.Fatalf("%+v", d.Requests)
	}
}

func TestTUIReposLoad(t *testing.T) {
	x, good, bad := checkFix(t)
	repos, err := tuiDeps(x.f.sys()).Repos.Load("")
	if err != nil || len(repos) != 3 {
		t.Fatalf("%+v %v", repos, err)
	}
	byID := map[string]tui.Repo{}
	for _, r := range repos {
		byID[r.ID] = r
	}
	if r := byID["o/good"]; !r.Here || !r.OK || r.Checkout != good {
		t.Errorf("good %+v", r)
	}
	if r := byID["o/bad"]; !r.Here || r.OK || r.Checkout != bad || len(r.Report) == 0 {
		t.Errorf("bad %+v", r)
	}
	if r := byID["o/gone"]; r.Here {
		t.Errorf("gone %+v", r)
	}
	for _, r := range repos {
		for _, l := range r.Report {
			if strings.Contains(l, "v4lue") || strings.Contains(l, "s3cr3t") {
				t.Fatalf("a value in %q", l)
			}
		}
	}
	// No repos.yaml: nothing, and no error.
	y := repoStore(t, "")
	if repos, err := tuiDeps(y.f.sys()).Repos.Load(""); err != nil || len(repos) != 0 {
		t.Fatalf("%+v %v", repos, err)
	}
}

func TestTUIDoctorCheckIsTheDoctorReport(t *testing.T) {
	x := repoStore(t, "")
	sections, err := tuiDeps(x.f.sys()).Doctor.Check("")
	if err != nil {
		t.Fatal(err)
	}
	report, err := doctorReport(x.f.sys(), "", "")
	if err != nil || len(sections) != len(report.Sections) {
		t.Fatalf("%d sections, report %d (%v)", len(sections), len(report.Sections), err)
	}
	for i, s := range report.Sections {
		if sections[i].Name != s.Name || len(sections[i].Items) != len(s.Items) {
			t.Fatalf("section %d: %+v", i, sections[i])
		}
		for j, it := range s.Items {
			if got := sections[i].Items[j]; got.Label != it.Label || got.Detail != it.Detail || got.Fix != it.Fix || int(got.State) != int(it.State) {
				t.Errorf("%s/%s: %+v", s.Name, it.Label, got)
			}
		}
	}
}

func TestTUICommandNeedsATerminal(t *testing.T) {
	x := repoStore(t, "")
	f := x.f
	for _, c := range []struct{ in, out bool }{{false, true}, {true, false}, {false, false}} {
		f.tty, f.outTTY = c.in, c.out
		err := f.run("tui")
		var u *UsageError
		if !errors.As(err, &u) || err.Error() != "ezc tui needs a terminal. Use the commands instead; ezc help lists them." {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	f.tty, f.outTTY = true, true
	if err := f.run("tui"); err != nil || len(f.tuis) != 1 {
		t.Fatalf("%v, %d runs", err, len(f.tuis))
	}
	if err := f.run("tui", "-g"); err != nil || len(f.tuis) != 2 {
		t.Fatalf("%v", err)
	}
	for i, want := range []tui.Scope{tui.RepoScope, tui.StoreScope} {
		m, ok := f.tuis[i].(*tui.Model)
		if !ok || m.Scope() != want {
			t.Fatalf("run %d: %T", i, f.tuis[i])
		}
	}
}

var _ tea.Model = (*tui.Model)(nil)
