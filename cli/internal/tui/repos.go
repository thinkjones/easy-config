package tui

import (
	tea "charm.land/bubbletea/v2"
)

// reposPane is the repos view: the registry, each repo checked in its checkout here for the environment.
type reposPane struct {
	m        *Model
	repos    []Repo
	err      string
	sel, off int
	open     bool // the selected repo's report is open
}

type reposLoaded struct {
	repos []Repo
	err   error
}

func (p *reposPane) load() tea.Cmd {
	m := p.m
	env := m.env
	return func() tea.Msg {
		r, err := m.d.Repos.Load(env)
		return reposLoaded{r, err}
	}
}

func (p *reposPane) loaded(msg tea.Msg) bool {
	l, ok := msg.(reposLoaded)
	if !ok {
		return false
	}
	p.repos, p.err = nil, ""
	if l.err != nil {
		p.err = clean(l.err.Error())
	}
	for _, r := range l.repos {
		r.ID, r.Checkout, r.Report = clean(r.ID), clean(r.Checkout), cleanAll(r.Report)
		p.repos = append(p.repos, r)
	}
	p.sel = max(0, min(p.sel, len(p.repos)-1))
	return true
}

func (p *reposPane) forget() {}

func (p *reposPane) body(w, h int) []string {
	title := "repos ── env " + p.m.env
	switch {
	case p.err != "":
		return panel(title, []string{stError.Render("✗ " + p.err)}, w, true)
	case len(p.repos) == 0:
		return panel(title, []string{stMuted.Render("No repos are registered. Run ezc repo add inside a repo.")}, w, true)
	case p.open:
		r := p.repos[p.sel]
		lines := r.Report
		if !r.Here {
			lines = []string{"no checkout on this machine. Run ezc repo add inside it."}
		}
		return panel(r.ID, append(append([]string{}, lines...), "", stMuted.Render("esc to go back")), w, true)
	}
	var rows []string
	for _, r := range p.repos {
		switch {
		case !r.Here:
			rows = append(rows, columns([]string{stMuted.Render("-"), r.ID, stMuted.Render("no checkout on this machine")}, []int{1, 24}))
		case r.OK:
			rows = append(rows, columns([]string{stTeal.Render("✓"), r.ID, r.Checkout}, []int{1, 24}))
		default:
			rows = append(rows, columns([]string{stError.Render("✗"), r.ID, r.Checkout}, []int{1, 24}))
		}
	}
	return panel(title, listLines(rows, p.sel, &p.off, min(len(rows), h-2), w-4, true), w, true)
}

func (p *reposPane) hints() string { return "enter details  e env" }

func (p *reposPane) help() [][2]string {
	return [][2]string{{"enter", "what the check found"}, {"esc", "back to the list"}}
}

func (p *reposPane) key(k string) tea.Cmd {
	if p.open {
		if k == "esc" {
			p.open = false
		}
		return nil
	}
	if move(&p.sel, len(p.repos), k) {
		return nil
	}
	if k == "enter" && p.err == "" && len(p.repos) > 0 {
		p.open = true
	}
	return nil
}
