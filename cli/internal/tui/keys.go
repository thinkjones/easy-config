package tui

import (
	tea "charm.land/bubbletea/v2"
)

// shellKeys are the keys every view takes, for the ? overlay.
var shellKeys = [][2]string{
	{"1 2 3 4", "values, access, repos, doctor"},
	{"↑ ↓  j k", "move"},
	{"tab", "the next panel"},
	{"e", "change the environment"},
	{"ctrl+r", "load again"},
	{"?", "this help"},
	{"q", "quit"},
}

// key is where a key press goes, in order: a running command, ctrl+c, help, a terminal too small, the confirm
// screen, the output panel, a text input, the shell's keys, then the view.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if m.busy {
		if k == "ctrl+c" {
			if m.armed {
				return tea.Quit
			}
			m.armed = true
			m.setStatus("a command is running: ctrl+c again to quit anyway", false)
			return nil
		}
		m.setStatus("wait for the running command to finish", false)
		return nil
	}
	if k == "ctrl+c" {
		return tea.Quit
	}
	if m.help {
		switch k {
		case "esc", "?":
			m.help = false
		case "q":
			return tea.Quit
		}
		return nil
	}
	if m.w < minW || m.h < minH {
		if k == "q" {
			return tea.Quit
		}
		return nil
	}
	if m.box != nil {
		switch k {
		case "y":
			return m.run()
		case "esc":
			m.box = nil
			m.setStatus("cancelled", false)
		case "i":
			if m.wiz != nil && m.wiz.choice != 3 {
				m.wiz.install = !m.wiz.install
				m.wizardConfirm()
			}
		}
		return nil
	}
	if m.out != nil {
		ok := m.out.OK
		m.out, m.box = nil, nil
		if m.wiz != nil {
			return m.wizardDone(ok)
		}
		return m.reload()
	}
	if m.in != nil {
		switch k {
		case "esc":
			m.in = nil
			return nil
		case "enter":
			in := m.in
			m.in = nil
			return in.submit(in.ti.Value())
		}
		var cmd tea.Cmd
		m.in.ti, cmd = m.in.ti.Update(msg)
		return cmd
	}
	if m.wiz != nil {
		return m.wizardKey(k)
	}
	switch k {
	case "1", "2", "3", "4":
		return m.show(viewID(k[0] - '1'))
	case "?":
		m.help = true
		return nil
	case "e":
		return m.askEnv()
	case "q":
		return tea.Quit
	case "ctrl+r":
		m.panes[m.cur].forget()
		return m.panes[m.cur].load()
	}
	return m.panes[m.cur].key(k)
}

// helpPanel is the ? overlay: the shell's keys, then the current view's.
func (m *Model) helpPanel() []string {
	rows := append(append([][2]string{}, shellKeys...), m.panes[m.cur].help()...)
	w := 0
	for _, r := range rows {
		w = max(w, len([]rune(r[0])))
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, stAmber.Render(fit(r[0], w))+"  "+stText.Render(r[1]))
	}
	return panel("keys", append(lines, "", stMuted.Render("esc or ? to close")), m.w, true)
}
