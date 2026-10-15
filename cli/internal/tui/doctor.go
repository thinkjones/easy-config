package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// doctorPane is the doctor view: the checks ezc doctor runs.
type doctorPane struct {
	seq      int // the latest load; an older one's result is dropped
	m        *Model
	sections []Section
	err      string
}

type doctorLoaded struct {
	seq      int
	sections []Section
	err      error
}

func (p *doctorPane) load() tea.Cmd {
	m := p.m
	env := m.env
	p.seq++
	seq := p.seq
	return func() tea.Msg {
		s, err := m.d.Doctor.Check(env)
		return doctorLoaded{seq, s, err}
	}
}

func (p *doctorPane) loaded(msg tea.Msg) bool {
	l, ok := msg.(doctorLoaded)
	if !ok {
		return false
	}
	if l.seq != p.seq {
		return true
	}
	p.sections, p.err = nil, ""
	if l.err != nil {
		p.err = clean(l.err.Error())
	}
	for _, s := range l.sections {
		c := Section{Name: clean(s.Name)}
		for _, it := range s.Items {
			c.Items = append(c.Items, Item{it.State, clean(it.Label), clean(it.Detail), clean(it.Fix)})
		}
		p.sections = append(p.sections, c)
	}
	return true
}

func (p *doctorPane) forget() {}

// body is one panel per section, laid out as ezc doctor prints it.
func (p *doctorPane) body(w, _ int) []string {
	if p.err != "" {
		return panel("doctor", []string{stError.Render("✗ " + p.err)}, w, true)
	}
	if len(p.sections) == 0 {
		return panel("doctor", []string{stMuted.Render("Nothing to report.")}, w, true)
	}
	var out []string
	for _, s := range p.sections {
		lw := 0
		for _, it := range s.Items {
			lw = max(lw, ansi.StringWidth(it.Label))
		}
		var lines []string
		for _, it := range s.Items {
			mark := stTeal.Render("✓")
			switch it.State {
			case Warn:
				mark = stAmber.Render("!")
			case Broken:
				mark = stError.Render("✗")
			}
			lines = append(lines, mark+" "+fit(it.Label, lw)+"  "+stText.Render(it.Detail))
			if it.State != OK && it.Fix != "" {
				lines = append(lines, "    "+stAmber.Render(it.Fix))
			}
		}
		out = append(out, panel(s.Name, lines, w, false)...)
	}
	return out
}

func (p *doctorPane) hints() string      { return "ctrl+r check again  e env" }
func (p *doctorPane) help() [][2]string  { return nil }
func (p *doctorPane) key(string) tea.Cmd { return nil }
