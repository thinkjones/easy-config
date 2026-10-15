package tui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// view is one of the four views, switched with 1 to 4.
type viewID int

const (
	valuesView viewID = iota
	accessView
	reposView
	doctorView
)

var viewNames = [...]string{"values", "access", "repos", "doctor"}

// pane is a view's own part of the model: what it draws, the keys it takes and how it loads.
type pane interface {
	// body is the view's panels, at most h lines of width w.
	body(w, h int) []string
	// key handles a key no part of the shell took; it returns the command to run, if any.
	key(k string) tea.Cmd
	// hints are the view's keys for the footer, and help its rows for the ? overlay.
	hints() string
	help() [][2]string
	// load reads the view through its interface; loaded takes the message load produced and reports whether it was its.
	load() tea.Cmd
	loaded(msg tea.Msg) bool
	// forget drops what must not outlive a move: a revealed value.
	forget()
}

// minW and minH are the smallest terminal the views are drawn in.
const minW, minH = 60, 15

// Model is the TUI.
type Model struct {
	d     Deps
	w, h  int
	plain bool

	head    Header
	headErr string

	cur   viewID
	panes [4]pane
	stale [4]bool

	scope Scope
	env   string

	help     bool
	status   string
	statusOK bool

	box   *confirmBox // the confirm screen, when open
	busy  bool        // a command is running
	armed bool        // ctrl+c was pressed once while it runs
	out   *Outcome    // the output panel, when open

	in *asking // a text input, when one has focus
}

// asking is a question in the status line.
type asking struct {
	prompt string
	ti     textinput.Model
	submit func(string) tea.Cmd
}

type headerLoaded struct {
	h   Header
	err error
}

// New is the TUI over d, starting in scope s. Outside a repo it is the store's alone.
func New(d Deps, s Scope) *Model {
	m := &Model{d: d, w: 120, h: 30, scope: s, plain: os.Getenv("NO_COLOR") != ""}
	m.panes = [4]pane{
		&valuesPane{m: m},
		newStub(m, "access", "Loading…"),
		newStub(m, "repos", "Loading…"),
		newStub(m, "doctor", "Loading…"),
	}
	for i := range m.stale {
		m.stale[i] = true
	}
	return m
}

// Init loads the header; the first view loads once the header says which environment applies.
func (m *Model) Init() tea.Cmd { return m.loadHeader() }

func (m *Model) loadHeader() tea.Cmd {
	return func() tea.Msg {
		h, err := m.d.Store.Status()
		return headerLoaded{h, err}
	}
}

// show switches to v, loading it when it is stale.
func (m *Model) show(v viewID) tea.Cmd {
	if v != m.cur {
		m.forgetAll()
	}
	m.cur = v
	if m.stale[v] {
		m.stale[v] = false
		return m.panes[v].load()
	}
	return nil
}

// reload marks every view stale and loads the header and the current view.
func (m *Model) reload() tea.Cmd {
	m.forgetAll()
	for i := range m.stale {
		m.stale[i] = true
	}
	m.stale[m.cur] = false
	return tea.Batch(m.loadHeader(), m.panes[m.cur].load())
}

// changeEnv makes env the environment of every view, and loads the current one again.
func (m *Model) changeEnv(env string) tea.Cmd {
	m.env = env
	return m.reload()
}

func (m *Model) forgetAll() {
	for _, p := range m.panes {
		p.forget()
	}
}

func (m *Model) setStatus(text string, ok bool) { m.status, m.statusOK = clean(text), ok }

// Update is Bubble Tea's.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case headerLoaded:
		first := m.env == ""
		m.head, m.headErr = cleanHeader(msg.h), ""
		if msg.err != nil {
			m.headErr = clean(msg.err.Error())
		}
		if first {
			m.env = m.head.Env
			if m.head.Repo == "" {
				m.scope = StoreScope
			}
			return m, m.show(m.cur)
		}
		return m, nil
	case ranMsg:
		m.busy, m.armed, m.box = false, false, nil
		out := Outcome{Lines: cleanAll(msg.out.Lines), OK: msg.out.OK}
		m.out = &out
		m.forgetAll()
		return m, nil
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	for _, p := range m.panes {
		if p.loaded(msg) {
			return m, nil
		}
	}
	if m.in != nil {
		var cmd tea.Cmd
		m.in.ti, cmd = m.in.ti.Update(msg)
		return m, cmd
	}
	return m, nil
}

func cleanHeader(h Header) Header {
	h.Store, h.Repo, h.Env = clean(h.Store), clean(h.Repo), clean(h.Env)
	return h
}

// View is Bubble Tea's: the screen in the alternate screen.
func (m *Model) View() tea.View {
	content := strings.Join(m.render(), "\n")
	if m.plain {
		content = ansi.Strip(content)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// render is the screen as exactly m.h lines of width m.w.
func (m *Model) render() []string {
	if m.w < minW || m.h < minH {
		lines := []string{fit(stError.Render(fmt.Sprintf("too small: %d×%d at least · q quits", minW, minH)), m.w)}
		for len(lines) < m.h {
			lines = append(lines, spaces(m.w))
		}
		return lines
	}
	bodyH := m.h - 3
	var body []string
	switch {
	case m.help:
		body = m.helpPanel()
	case m.box != nil:
		body = m.box.render(m.w, m.busy)
	case m.out != nil:
		body = m.outputPanel()
	default:
		body = m.panes[m.cur].body(m.w, bodyH)
	}
	if len(body) > bodyH {
		body = body[:bodyH]
	}
	lines := []string{m.headerLine()}
	for _, l := range body {
		lines = append(lines, fit(l, m.w))
	}
	for len(lines) < bodyH+1 {
		lines = append(lines, spaces(m.w))
	}
	return append(lines, m.statusLine(), m.footer())
}

// headerLine is "ezc  store <store> · env <env>", with how the store stands on the right.
func (m *Model) headerLine() string {
	left := stTitle.Render("ezc") + "  " + stText.Render("store "+m.head.Store+" · env "+m.env)
	var right []string
	h := m.head
	switch {
	case m.headErr != "":
		right = append(right, stError.Render("✗ "+m.headErr))
	case h.Unknown:
		right = append(right, stError.Render("store state unknown"))
	case h.Diverged:
		right = append(right, stError.Render(fmt.Sprintf("◆ diverged (%d ahead, %d behind)", h.Ahead, h.Behind)))
	case h.Behind > 0:
		right = append(right, stAmber.Render(fmt.Sprintf("▲ %d behind", h.Behind)))
	case h.Ahead > 0:
		right = append(right, stAmber.Render(fmt.Sprintf("▼ %d ahead", h.Ahead)))
	}
	if h.Offline {
		right = append(right, stAmber.Render("offline"))
	}
	if h.Requests > 0 {
		noun := "requests"
		if h.Requests == 1 {
			noun = "request"
		}
		right = append(right, stAmber.Render(fmt.Sprintf("● %d %s", h.Requests, noun)))
	}
	r := strings.Join(right, "  ")
	gap := m.w - lipgloss.Width(r)
	return fit(fit(left, max(0, gap-2))+"  "+r, m.w)
}

// statusLine is the question being asked, else the last status.
func (m *Model) statusLine() string {
	if m.in != nil {
		return fit(stAmber.Render(m.in.prompt+" ")+m.in.ti.View(), m.w)
	}
	st := stTeal
	if !m.statusOK {
		st = stAmber
	}
	return fit(st.Render(m.status), m.w)
}

// footer is the views, the current view's keys and help.
func (m *Model) footer() string {
	var parts []string
	for i, name := range viewNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if viewID(i) == m.cur {
			parts = append(parts, stSelected.Render(label))
		} else {
			parts = append(parts, stMuted.Render(label))
		}
	}
	line := strings.Join(parts, "  ") + "  " + stMuted.Render("? help")
	if hints := m.panes[m.cur].hints(); hints != "" {
		line += "     " + stText.Render(hints)
	}
	return fit(line, m.w)
}

// outputPanel is what the last command printed.
func (m *Model) outputPanel() []string {
	title := stTeal.Render("done")
	if !m.out.OK {
		title = stError.Render("failed")
	}
	lines := append([]string{}, m.out.Lines...)
	return panel(title, append(lines, "", stMuted.Render("any key to go back")), m.w, true)
}

// ask opens a text input in the status line; submit gets its value on enter.
func (m *Model) ask(prompt string, secret bool, submit func(string) tea.Cmd) tea.Cmd {
	ti := textinput.New()
	st := ti.Styles()
	st.Cursor.Blink = false
	ti.SetStyles(st)
	ti.Prompt = ""
	ti.SetWidth(max(10, m.w-len(prompt)-4))
	if secret {
		ti.EchoMode = textinput.EchoPassword
	}
	m.in = &asking{prompt: prompt, ti: ti, submit: submit}
	return m.in.ti.Focus()
}

// stub is a view not built yet: a panel with one line.
type stub struct {
	m           *Model
	name, empty string
}

func newStub(m *Model, name, empty string) *stub { return &stub{m: m, name: name, empty: empty} }

type stubLoaded struct{ name string }

func (s *stub) body(w, _ int) []string { return panel(s.name, []string{s.empty}, w, true) }
func (s *stub) key(string) tea.Cmd     { return nil }
func (s *stub) hints() string          { return "" }
func (s *stub) help() [][2]string      { return nil }
func (s *stub) forget()                {}
func (s *stub) load() tea.Cmd {
	return func() tea.Msg {
		switch s.name {
		case "values":
			s.m.d.Values.List(s.m.scope, s.m.env)
		case "access":
			s.m.d.Access.Load()
		case "repos":
			s.m.d.Repos.Load(s.m.env)
		case "doctor":
			s.m.d.Doctor.Check(s.m.env)
		}
		return stubLoaded{s.name}
	}
}
func (s *stub) loaded(msg tea.Msg) bool { l, ok := msg.(stubLoaded); return ok && l.name == s.name }
