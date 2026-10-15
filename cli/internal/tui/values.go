package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// detailRows is the height of the values view's detail panel, its border included.
const detailRows = 6

// valuesPane is the values view: every name of the scope, its type, the file that supplies it and its use.
type valuesPane struct {
	m        *Model
	vals     []Value
	err      string
	sel, off int
	focus    int // 0 the list, 1 the detail
	revealed struct{ name, value string }
}

type valuesLoaded struct {
	vals []Value
	err  error
}

type revealedMsg struct {
	name, value string
	err         error
}

func (p *valuesPane) load() tea.Cmd {
	m := p.m
	scope, env := m.scope, m.env
	return func() tea.Msg {
		vals, err := m.d.Values.List(scope, env)
		return valuesLoaded{vals, err}
	}
}

func (p *valuesPane) loaded(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case valuesLoaded:
		p.vals, p.err = nil, ""
		if msg.err != nil {
			p.err = clean(msg.err.Error())
		}
		for _, v := range msg.vals {
			p.vals = append(p.vals, cleanValue(v))
		}
		p.sel = max(0, min(p.sel, len(p.vals)-1))
		return true
	case revealedMsg:
		if msg.err != nil {
			p.m.setStatus(msg.err.Error(), false)
		} else if v, ok := p.selected(); ok && v.Name == msg.name {
			p.revealed.name, p.revealed.value = msg.name, clean(msg.value)
		}
		return true
	}
	return false
}

func cleanValue(v Value) Value {
	v.Name, v.Type, v.Desc, v.File, v.Shown, v.Due = clean(v.Name), clean(v.Type), clean(v.Desc), clean(v.File), clean(v.Shown), clean(v.Due)
	v.Overrides, v.RequiredBy, v.OptionalIn = cleanAll(v.Overrides), cleanAll(v.RequiredBy), cleanAll(v.OptionalIn)
	return v
}

func (p *valuesPane) forget() { p.revealed.name, p.revealed.value = "", "" }

func (p *valuesPane) selected() (Value, bool) {
	if p.sel < 0 || p.sel >= len(p.vals) {
		return Value{}, false
	}
	return p.vals[p.sel], true
}

func (p *valuesPane) body(w, h int) []string {
	title := "values ── global"
	if p.m.scope == RepoScope {
		title = "values ── repo " + p.m.head.Repo
	}
	listH := max(1, h-detailRows-2)
	var lines []string
	switch {
	case p.err != "":
		lines = []string{stError.Render("✗ " + p.err)}
	case len(p.vals) == 0:
		lines = []string{stMuted.Render("No values.")}
	default:
		lines = listLines(p.rows(), p.sel, &p.off, listH, w-4, p.focus == 0)
	}
	out := panel(title, lines, w, p.focus == 0)
	if v, ok := p.selected(); ok && p.err == "" {
		out = append(out, panel("detail ── "+v.Name, p.detail(v), w, p.focus == 1)...)
	}
	return out
}

func (p *valuesPane) rows() []string {
	nameW, fileW := 4, 4
	for _, v := range p.vals {
		nameW = max(nameW, len([]rune(v.Name)))
		fileW = max(fileW, len([]rune(v.File)))
	}
	widths := []int{min(nameW, 24), 7, min(fileW, 26), 18}
	var rows []string
	for _, v := range p.vals {
		file, value := v.File, v.Shown
		switch {
		case v.File == "":
			file = "(not set)"
		case v.Secret && p.revealed.name == v.Name:
			value = p.revealed.value
		case v.Secret:
			value = "••••••••"
		}
		use := ""
		if n := len(v.RequiredBy) + len(v.OptionalIn); n > 0 {
			use = fmt.Sprintf("used by %d repos", n)
			if n == 1 {
				use = "used by 1 repo"
			}
		} else if len(v.Overrides) > 0 {
			use = "overrides " + v.Overrides[0]
		}
		rows = append(rows, columns([]string{v.Name, v.Type, file, value, stMuted.Render(use)}, widths))
	}
	return rows
}

func (p *valuesPane) detail(v Value) []string {
	lines := []string{stText.Render(v.Desc)}
	if len(v.RequiredBy) > 0 {
		lines = append(lines, stMuted.Render("required by  ")+strings.Join(v.RequiredBy, " · "))
	}
	if len(v.OptionalIn) > 0 {
		lines = append(lines, stMuted.Render("optional in  ")+strings.Join(v.OptionalIn, " · "))
	}
	if v.Due != "" {
		lines = append(lines, stAmber.Render("! rotation due: "+v.Due))
	}
	for len(lines) < detailRows-2 {
		lines = append(lines, "")
	}
	return lines[:detailRows-2]
}

func (p *valuesPane) hints() string {
	return "s set  S set for " + p.m.env + "  u unset  r reveal  e env  g scope  p pull  P push"
}

func (p *valuesPane) help() [][2]string {
	return [][2]string{
		{"s", "set the selected name for every environment"},
		{"S", "set it for this environment only"},
		{"u", "unset it from the file that supplies it"},
		{"r", "reveal a secret, until the selection moves"},
		{"e", "change the environment"},
		{"g", "the store's values, or the repo's"},
		{"p P", "pull, push the store"},
	}
}

func (p *valuesPane) key(k string) tea.Cmd {
	m := p.m
	if move(&p.sel, len(p.vals), k) {
		p.forget()
		return nil
	}
	switch k {
	case "tab":
		p.focus = 1 - p.focus
		return nil
	case "e":
		return m.ask("Environment:", false, func(env string) tea.Cmd {
			if env = strings.TrimSpace(env); env == "" {
				return nil
			}
			return m.changeEnv(env)
		})
	case "g":
		if m.head.Repo == "" {
			m.setStatus("not in a repo: the store only", false)
			return nil
		}
		m.scope = 1 - m.scope
		p.forget()
		p.sel, p.off = 0, 0
		return p.load()
	case "p":
		m.confirm("Pull the store", [][]string{{"store", "pull"}}, "", nil)
		return nil
	case "P":
		m.confirm("Push the store", [][]string{{"store", "push"}}, "", nil)
		return nil
	case "s", "S", "u", "r":
	default:
		return nil
	}
	v, ok := p.selected()
	if !ok {
		m.setStatus("nothing selected", false)
		return nil
	}
	switch k {
	case "r":
		if !v.Secret {
			m.setStatus(v.Name+" is not a secret: its value is shown", false)
			return nil
		}
		scope, env := m.scope, m.env
		return func() tea.Msg {
			value, err := m.d.Values.Reveal(scope, v.Name, env)
			return revealedMsg{v.Name, value, err}
		}
	case "u":
		return p.unset(v)
	}
	return p.set(v, k == "S")
}

// scopeFlag is -g in store scope.
func (p *valuesPane) scopeFlag(args []string) []string {
	if p.m.scope == StoreScope {
		return append(args, "-g")
	}
	return args
}

func (p *valuesPane) set(v Value, forEnv bool) tea.Cmd {
	m := p.m
	if v.Type == "-" || v.Type == "" {
		m.setStatus(v.Name+" is not declared: declare it with ezc set "+v.Name+" --type T --desc D", false)
		return nil
	}
	args := []string{"set", v.Name}
	where := "every environment"
	if forEnv {
		args = append(args, "--env", m.env)
		where = "environment " + m.env
	}
	args = p.scopeFlag(args)
	return m.ask(fmt.Sprintf("Value for %s (%s):", v.Name, where), v.Secret, func(value string) tea.Cmd {
		notes := []string{"The value goes to the command on stdin; it is not shown."}
		m.confirm("Set "+v.Name, [][]string{args}, value+"\n", notes)
		return nil
	})
}

func (p *valuesPane) unset(v Value) tea.Cmd {
	m := p.m
	layer, file, ok := strings.Cut(v.File, "/")
	if v.File == "" {
		m.setStatus(v.Name+" has no value here", false)
		return nil
	}
	if !ok || layer != "store" && layer != "repo" {
		m.setStatus(v.Name+" comes from the environment ezc was started in", false)
		return nil
	}
	args := []string{"unset", v.Name}
	base := strings.TrimSuffix(strings.TrimSuffix(file, ".yaml"), ".enc")
	if env, ok := strings.CutPrefix(base, "env."); ok {
		args = append(args, "--env", env)
	}
	if layer == "store" {
		args = append(args, "-g")
	}
	m.confirm("Unset "+v.Name, [][]string{args}, "", []string{"Removes the value from " + v.File + "; the declaration stays."})
	return nil
}
