package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// golden compares the screen with testdata/<name>.golden.
func golden(t *testing.T, name string, m *Model) {
	t.Helper()
	got := view(m)
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -run Golden -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s:\n%s", name, path, got)
	}
}

// view is the screen as text, without styling.
func view(m *Model) string { return ansi.Strip(m.View().Content) }

// key is a key press as Bubble Tea delivers it: a single character, or a name such as "enter", "ctrl+c".
func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	if c, ok := strings.CutPrefix(s, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: rune(c[0]), Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// drain runs cmd and every command its messages lead to, feeding each message back into m, and reports whether
// any of them asked to quit.
func drain(t *testing.T, m *Model, cmd tea.Cmd) (quit bool) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 1000 {
			t.Fatal("drain: commands never settle")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case tea.QuitMsg:
			quit = true
		default:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
	return quit
}

// press sends keys to m one at a time, running the commands each produces.
func press(t *testing.T, m *Model, keys ...string) (quit bool) {
	t.Helper()
	for _, k := range keys {
		_, cmd := m.Update(key(k))
		if drain(t, m, cmd) {
			quit = true
		}
	}
	return quit
}

// typeText types s into whatever has focus, one key at a time.
func typeText(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		if r == ' ' {
			press(t, m, "space")
			continue
		}
		press(t, m, string(r))
	}
}

type fakeStore struct {
	h     Header
	err   error
	calls int
}

func (f *fakeStore) Status() (Header, error) { f.calls++; return f.h, f.err }

type fakeValues struct {
	vals    map[Scope][]Value
	err     error
	reveal  map[string]string
	calls   int
	scopes  []Scope
	envs    []string
	reveals []string
}

func (f *fakeValues) List(s Scope, env string) ([]Value, error) {
	f.calls++
	f.scopes = append(f.scopes, s)
	f.envs = append(f.envs, env)
	return f.vals[s], f.err
}

func (f *fakeValues) Reveal(_ Scope, name, _ string) (string, error) {
	f.reveals = append(f.reveals, name)
	return f.reveal[name], nil
}

type fakeAccess struct {
	data  AccessData
	err   error
	calls int
}

func (f *fakeAccess) Load() (AccessData, error) { f.calls++; return f.data, f.err }

type fakeRepos struct {
	repos []Repo
	err   error
	envs  []string
}

func (f *fakeRepos) Load(env string) ([]Repo, error) {
	f.envs = append(f.envs, env)
	return f.repos, f.err
}

type fakeDoctor struct {
	sections []Section
	err      error
	envs     []string
}

func (f *fakeDoctor) Check(env string) ([]Section, error) {
	f.envs = append(f.envs, env)
	return f.sections, f.err
}

// ran is one call of Commands.Run.
type ran struct {
	args  []string
	stdin string
}

type fakeCommands struct {
	runs []ran
	out  Outcome
}

func (f *fakeCommands) Run(args []string, stdin string) Outcome {
	f.runs = append(f.runs, ran{args, stdin})
	return f.out
}

type fakes struct {
	store    *fakeStore
	values   *fakeValues
	access   *fakeAccess
	repos    *fakeRepos
	doctor   *fakeDoctor
	commands *fakeCommands
}

func (f fakes) deps() Deps {
	return Deps{Store: f.store, Values: f.values, Access: f.access, Repos: f.repos, Doctor: f.doctor, Commands: f.commands}
}

func newFakes() fakes {
	return fakes{
		store:    &fakeStore{h: Header{Store: "github.com/o/store", Repo: "o/app", Env: "local"}},
		values:   &fakeValues{vals: map[Scope][]Value{}, reveal: map[string]string{}},
		access:   &fakeAccess{},
		repos:    &fakeRepos{},
		doctor:   &fakeDoctor{},
		commands: &fakeCommands{out: Outcome{Lines: []string{"✓ done"}, OK: true}},
	}
}

// newTest is a model in repo scope over fresh fakes, started as ezc tui starts it.
func newTest(t *testing.T) (*Model, fakes) {
	t.Helper()
	return newTestWith(t, func(fakes) {})
}

// newTestWith lets setup fill the fakes before the model starts.
func newTestWith(t *testing.T, setup func(fakes)) (*Model, fakes) {
	t.Helper()
	f := newFakes()
	setup(f)
	m := New(f.deps(), RepoScope)
	drain(t, m, m.Init())
	return m, f
}
