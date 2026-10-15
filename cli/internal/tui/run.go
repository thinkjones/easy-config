package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// confirmBox is the confirm screen: the commands an action runs, shown before y runs them.
type confirmBox struct {
	title string
	cmds  [][]string
	stdin string
	notes []string
}

// ranMsg is what the commands of a confirmed action printed.
type ranMsg struct{ out Outcome }

// confirm opens the confirm screen for cmds (ezc command lines without "ezc"), which run in order with stdin, the
// first failure stopping them. notes say what the action does beyond the lines.
func (m *Model) confirm(title string, cmds [][]string, stdin string, notes []string) {
	m.forgetAll()
	m.box = &confirmBox{title: clean(title), cmds: cmds, stdin: stdin, notes: cleanAll(notes)}
}

func (b *confirmBox) render(w int, busy bool) []string {
	lines := []string{"This runs:"}
	for _, c := range b.cmds {
		lines = append(lines, "  "+stTitle.Render(clean("ezc "+strings.Join(c, " "))))
	}
	if len(b.notes) > 0 {
		lines = append(lines, "")
		lines = append(lines, b.notes...)
	}
	lines = append(lines, "")
	if busy {
		lines = append(lines, stAmber.Render("running…"))
	} else {
		lines = append(lines, stAmber.Render("y")+" run · "+stMuted.Render("esc")+" cancel")
	}
	return panel(b.title, lines, w, true)
}

// run starts the confirmed commands; nothing else can start until their ranMsg arrives.
func (m *Model) run() tea.Cmd {
	b := m.box
	m.busy = true
	return func() tea.Msg {
		var all Outcome
		for _, c := range b.cmds {
			out := m.d.Commands.Run(c, b.stdin)
			all.Lines = append(all.Lines, out.Lines...)
			if all.OK = out.OK; !out.OK {
				break
			}
		}
		return ranMsg{all}
	}
}
