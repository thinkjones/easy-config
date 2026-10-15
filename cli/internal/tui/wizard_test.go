package tui

import (
	"slices"
	"strings"
	"testing"
)

// newWizard is the wizard over fresh fakes; then is whether ezc opens after it.
func newWizard(t *testing.T, then bool) (*Model, fakes) {
	t.Helper()
	f := newFakes()
	m := NewWizard(f.deps(), then)
	drain(t, m, m.Init())
	return m, f
}

// answer types each answer and presses enter after it.
func answer(t *testing.T, m *Model, answers ...string) {
	t.Helper()
	for _, a := range answers {
		typeText(t, m, a)
		press(t, m, "enter")
	}
}

func TestGoldenWizard(t *testing.T) {
	cases := map[string]func(t *testing.T) *Model{
		"wizard-choose": func(t *testing.T) *Model { m, _ := newWizard(t, true); return m },
		"wizard-confirm-github": func(t *testing.T) *Model {
			m, _ := newWizard(t, true)
			press(t, m, "2")
			answer(t, m, "o/store", "gene")
			return m
		},
		"wizard-done": func(t *testing.T) *Model {
			m, f := newWizard(t, true)
			f.commands.out = Outcome{Lines: []string{"✓ git", "✓ the encryption tool", "+ created your key: ~/.config/ezc/key.txt", "  Back this file up now."}, OK: true}
			press(t, m, "1")
			answer(t, m, "")
			press(t, m, "y")
			return m
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) { golden(t, name, build(t)) })
	}
}

func TestWizardCommandLines(t *testing.T) {
	for _, c := range []struct {
		keys    []string
		answers []string
		install bool
		want    []string
	}{
		{[]string{"1"}, []string{""}, false, []string{"setup", "--new-local"}},
		{[]string{"2"}, []string{"o/store", "gene"}, false, []string{"setup", "--new-github", "o/store", "--name", "gene"}},
		{[]string{"3"}, []string{"o/store", "sam"}, false, []string{"join", "o/store", "--name", "sam"}},
		{[]string{"4"}, []string{"/srv/store", ""}, false, []string{"setup", "--store", "/srv/store"}},
	} {
		m, f := newWizard(t, false)
		press(t, m, c.keys...)
		answer(t, m, c.answers...)
		if !strings.Contains(view(m), "ezc "+strings.Join(c.want, " ")) {
			t.Fatalf("%v: the confirm screen:\n%s", c.want, view(m))
		}
		press(t, m, "y")
		if len(f.commands.runs) != 1 || !slices.Equal(f.commands.runs[0].args, c.want) {
			t.Errorf("%v: runs %+v", c.want, f.commands.runs)
		}
	}
}

func TestWizardJoinNeedsAName(t *testing.T) {
	m, f := newWizard(t, false)
	press(t, m, "3")
	answer(t, m, "o/store", "")
	if m.box != nil || m.status != "join needs your name in the store" || len(f.commands.runs) != 0 {
		t.Fatalf("status %q", m.status)
	}
	press(t, m, "3")
	answer(t, m, "o/store", "sam")
	if strings.Contains(view(m), "--yes") {
		t.Fatalf("\n%s", view(m))
	}
}

func TestWizardNeedsARepo(t *testing.T) {
	m, _ := newWizard(t, false)
	press(t, m, "2")
	answer(t, m, "")
	if m.in != nil || m.status != "say which repo" {
		t.Fatalf("status %q", m.status)
	}
}

func TestWizardThen(t *testing.T) {
	m, f := newWizard(t, true)
	press(t, m, "1")
	answer(t, m, "")
	press(t, m, "y")
	before := f.values.calls
	if press(t, m, "enter") || f.values.calls != before+1 || m.wiz != nil {
		t.Fatalf("values loaded %d→%d", before, f.values.calls)
	}
	m, _ = newWizard(t, false)
	press(t, m, "1")
	answer(t, m, "")
	press(t, m, "y")
	if !press(t, m, "x") {
		t.Fatal("a key after setup did not quit")
	}
}

func TestWizardFailureGoesBackToTheStart(t *testing.T) {
	m, f := newWizard(t, false)
	f.commands.out = Outcome{Lines: []string{"✗ gh is not installed."}, OK: false}
	press(t, m, "2")
	answer(t, m, "o/store", "")
	if press(t, m, "y", "enter") {
		t.Fatal("quit after a failure")
	}
	if !strings.Contains(view(m), "1 a new store on this machine") {
		t.Fatalf("not back at the start:\n%s", view(m))
	}
}

func TestWizardEscQuitsAtTheStart(t *testing.T) {
	m, _ := newWizard(t, false)
	if !press(t, m, "esc") {
		t.Fatal("esc did not quit")
	}
	m, _ = newWizard(t, false)
	press(t, m, "2", "esc")
	if press(t, m, "esc") == false && !strings.Contains(view(m), "1 a new store on this machine") {
		t.Fatalf("\n%s", view(m))
	}
}

// The wizard offers no install: setup would hand the terminal to brew while the TUI owns it. It says what to do.
func TestWizardOffersNoInstall(t *testing.T) {
	m, f := newWizard(t, false)
	press(t, m, "1")
	answer(t, m, "")
	press(t, m, "i")
	if v := view(m); strings.Contains(v, "brew") && !strings.Contains(v, "install it yourself") || strings.Contains(v, "--yes") || strings.Contains(v, "[x]") {
		t.Fatalf("\n%s", v)
	}
	if !strings.Contains(view(m), "If a tool is missing, setup says how to install it; install it yourself, then run this again.") {
		t.Fatalf("\n%s", view(m))
	}
	press(t, m, "y")
	if slices.Contains(f.commands.runs[0].args, "--yes") {
		t.Fatalf("%v", f.commands.runs[0].args)
	}
}
