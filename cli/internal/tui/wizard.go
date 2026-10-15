package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// wizard is the setup wizard: what to do, where the store is and your name, then the command that does it.
type wizard struct {
	then       bool // ezc tui opens after a setup that worked; else the wizard quits
	choice     int  // 1 to 4, 0 before one is made
	repo, name string
}

// wizardChoices are the wizard's first question.
var wizardChoices = []string{
	"a new store on this machine",
	"a new store on GitHub",
	"join a store as a new person or machine",
	"an existing store, with this machine's key",
}

// NewWizard is the TUI as the setup wizard. With then, a setup that worked opens the values view; without, any key
// after it quits.
func NewWizard(d Deps, then bool) *Model {
	m := New(d, RepoScope)
	m.wiz = &wizard{then: then}
	return m
}

// Wizard reports whether the model is the setup wizard.
func (m *Model) Wizard() bool { return m.wiz != nil }

// args is the command line the answers make.
func (z *wizard) args() []string {
	var args []string
	switch z.choice {
	case 1:
		args = []string{"setup", "--new-local"}
	case 2:
		args = []string{"setup", "--new-github", z.repo}
	case 3:
		return []string{"join", z.repo, "--name", z.name}
	case 4:
		args = []string{"setup", "--store", z.repo}
	}
	if z.name != "" {
		args = append(args, "--name", z.name)
	}
	return args
}

func (m *Model) wizardBody() []string {
	lines := []string{stTitle.Render("Set up ezc on this machine"), ""}
	for i, c := range wizardChoices {
		label := stAmber.Render(string(rune('1'+i))) + " " + c
		if m.wiz.choice == i+1 {
			label = stSelected.Render(string(rune('1'+i))+" "+c) + stMuted.Render("  ‹")
		}
		lines = append(lines, label)
	}
	return panel("setup", append(lines, "", stMuted.Render("press 1 to 4 · esc quits")), m.w, true)
}

// wizardKey is the wizard's first question; the rest are asked in the status line.
func (m *Model) wizardKey(k string) tea.Cmd {
	z := m.wiz
	switch k {
	case "esc", "q":
		return tea.Quit
	case "1", "2", "3", "4":
	default:
		return nil
	}
	*z = wizard{then: z.then, choice: int(k[0] - '0')}
	if z.choice == 1 {
		return m.askName()
	}
	prompt := "The store's GitHub repo (owner/repo):"
	if z.choice == 4 {
		prompt = "The store's repo (owner/repo, a URL or a path):"
	}
	return m.ask(prompt, false, func(repo string) tea.Cmd {
		if z.repo = strings.TrimSpace(repo); z.repo == "" {
			m.setStatus("say which repo", false)
			return nil
		}
		return m.askName()
	})
}

func (m *Model) askName() tea.Cmd {
	z := m.wiz
	prompt := "Your name in the store (blank for the default):"
	if z.choice == 3 {
		prompt = "Your name in the store:"
	}
	return m.ask(prompt, false, func(name string) tea.Cmd {
		z.name = strings.TrimSpace(name)
		if z.choice == 3 && z.name == "" {
			m.setStatus("join needs your name in the store", false)
			return nil
		}
		m.wizardConfirm()
		return nil
	})
}

// wizardConfirm opens the confirm screen for the answers. Setup's install with brew is not offered: brew would need
// the terminal, which the TUI is using.
func (m *Model) wizardConfirm() {
	z := m.wiz
	notes := []string{"If a tool is missing, setup says how to install it; install it yourself, then run this again."}
	m.confirm(wizardChoices[z.choice-1], [][]string{z.args()}, "", notes)
}

// wizardDone closes the output of the wizard's command: after a setup that worked, ezc opens or the wizard quits;
// after one that failed, it starts again.
func (m *Model) wizardDone(ok bool) tea.Cmd {
	if !ok {
		*m.wiz = wizard{then: m.wiz.then}
		return nil
	}
	if !m.wiz.then {
		return tea.Quit
	}
	m.wiz = nil
	return m.loadHeader()
}
