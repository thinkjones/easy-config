package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// shellCases are the screens of the shell, each built from a fresh model.
var shellCases = map[string]func(t *testing.T) *Model{
	"shell-values-empty": func(t *testing.T) *Model { m, _ := newTest(t); return m },
	"shell-header-behind": func(t *testing.T) *Model {
		m, _ := newTestWith(t, func(f fakes) { f.store.h.Behind, f.store.h.Requests = 2, 1 })
		return m
	},
	"shell-help": func(t *testing.T) *Model { m, _ := newTest(t); press(t, m, "?"); return m },
	"shell-too-small": func(t *testing.T) *Model {
		m, _ := newTest(t)
		m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
		return m
	},
	"shell-confirm": func(t *testing.T) *Model {
		m, _ := newTest(t)
		m.confirm("Remove sam", [][]string{{"user", "remove", "sam"}}, "", []string{"Every secret sam could read is listed as due for a new value."})
		return m
	},
	"shell-output-failed": func(t *testing.T) *Model {
		m, _ := newTest(t)
		m.Update(ranMsg{out: Outcome{Lines: []string{"✗ the store is 2 commits behind origin."}, OK: false}})
		return m
	},
}

func TestGoldenShell(t *testing.T) {
	for name, build := range shellCases {
		t.Run(name, func(t *testing.T) { golden(t, name, build(t)) })
	}
}

func TestHeaderSaysBehindAndRequests(t *testing.T) {
	m := shellCases["shell-header-behind"](t)
	first := strings.Split(view(m), "\n")[0]
	if !strings.HasSuffix(strings.TrimRight(first, " "), "▲ 2 behind  ● 1 request") || !strings.HasPrefix(first, "ezc  store github.com/o/store · env local") {
		t.Fatalf("header %q", first)
	}
}

func TestEveryLineIsTheTerminalsWidth(t *testing.T) {
	for name, build := range shellCases {
		m := build(t)
		lines := strings.Split(view(m), "\n")
		w, h := 120, 30
		if name == "shell-too-small" {
			w, h = 40, 10
		}
		if len(lines) != h {
			t.Errorf("%s: %d lines", name, len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) != w {
				t.Errorf("%s: line %d is %d wide: %q", name, i, lipgloss.Width(l), l)
			}
		}
	}
}

func TestConfirmRunsOnceAndShowsTheCommand(t *testing.T) {
	m, f := newTest(t)
	m.confirm("Remove sam", [][]string{{"user", "remove", "sam"}}, "", nil)
	if !strings.Contains(view(m), "ezc user remove sam") {
		t.Fatalf("the command is not shown:\n%s", view(m))
	}
	_, cmd := m.Update(key("y"))
	_, again := m.Update(key("y"))
	if again != nil {
		drain(t, m, again)
	}
	if m.status != "wait for the running command to finish" {
		t.Fatalf("status %q", m.status)
	}
	drain(t, m, cmd)
	if len(f.commands.runs) != 1 || !slices.Equal(f.commands.runs[0].args, []string{"user", "remove", "sam"}) {
		t.Fatalf("runs %v", f.commands.runs)
	}
	if !strings.Contains(view(m), "✓ done") {
		t.Fatalf("no output:\n%s", view(m))
	}
}

func TestConfirmStopsAtTheFirstFailure(t *testing.T) {
	m, f := newTest(t)
	f.commands.out = Outcome{Lines: []string{"✗ no"}, OK: false}
	m.confirm("Two", [][]string{{"store", "pull"}, {"store", "push"}}, "", nil)
	press(t, m, "y")
	if len(f.commands.runs) != 1 || !strings.Contains(view(m), "failed") {
		t.Fatalf("runs %v\n%s", f.commands.runs, view(m))
	}
}

func TestConfirmEscRunsNothing(t *testing.T) {
	m, f := newTest(t)
	m.confirm("Remove sam", [][]string{{"user", "remove", "sam"}}, "", nil)
	press(t, m, "esc", "y")
	if len(f.commands.runs) != 0 {
		t.Fatalf("runs %v", f.commands.runs)
	}
}

func TestRunReloadsTheHeaderAndTheView(t *testing.T) {
	m, f := newTest(t)
	store, values := f.store.calls, f.values.calls
	m.confirm("Push", [][]string{{"store", "push"}}, "", nil)
	press(t, m, "y", "enter")
	if f.store.calls != store+1 || f.values.calls != values+1 {
		t.Fatalf("store %d→%d, values %d→%d", store, f.store.calls, values, f.values.calls)
	}
}

func TestResizeWhileConfirmOpen(t *testing.T) {
	m, _ := newTest(t)
	m.confirm("Remove sam", [][]string{{"user", "remove", "sam"}}, "", nil)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(view(m), "ezc user remove sam") {
		t.Fatalf("the confirm screen is gone:\n%s", view(m))
	}
}

func TestQuitAndCtrlCWhileRunning(t *testing.T) {
	m, _ := newTest(t)
	if !press(t, m, "q") {
		t.Fatal("q did not quit")
	}
	m, _ = newTest(t)
	m.confirm("Push", [][]string{{"store", "push"}}, "", nil)
	m.Update(key("y"))
	if _, cmd := m.Update(key("ctrl+c")); cmd != nil && drain(t, m, cmd) {
		t.Fatal("the first ctrl+c quit while running")
	}
	if _, cmd := m.Update(key("ctrl+c")); cmd == nil || !drain(t, m, cmd) {
		t.Fatal("the second ctrl+c did not quit")
	}
}

func TestLoadErrorsAndHostileHeaderAreCleaned(t *testing.T) {
	m, _ := newTestWith(t, func(f fakes) { f.store.h.Store = "evil\x1b[2Jstore" })
	if strings.Contains(m.View().Content, "\x1b[2J") || !strings.Contains(view(m), "evil?[2Jstore") {
		t.Fatalf("header not cleaned:\n%q", view(m))
	}
	m.Update(ranMsg{out: Outcome{Lines: []string{"remote: \x1b]0;pwned\x07"}}})
	if strings.Contains(m.View().Content, "\x1b]0;") {
		t.Fatal("output not cleaned")
	}
}

func TestSwitchingViewsLoadsEachOnce(t *testing.T) {
	m, f := newTest(t)
	press(t, m, "2", "1", "2")
	if f.access.calls != 1 {
		t.Fatalf("access loaded %d times", f.access.calls)
	}
	press(t, m, "ctrl+r")
	if f.access.calls != 2 {
		t.Fatalf("ctrl+r: access loaded %d times", f.access.calls)
	}
}
