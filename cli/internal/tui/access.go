package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// accessPane is the access view: who can read the store, who asked to, and the secrets due for a new value.
type accessPane struct {
	m          *Model
	data       AccessData
	err        string
	focus      int // 0 recipients, 1 requests
	rSel, rOff int
	qSel, qOff int
	dueOff     int
}

type accessLoaded struct {
	data AccessData
	err  error
}

func (p *accessPane) load() tea.Cmd {
	m := p.m
	return func() tea.Msg {
		d, err := m.d.Access.Load()
		return accessLoaded{d, err}
	}
}

func (p *accessPane) loaded(msg tea.Msg) bool {
	l, ok := msg.(accessLoaded)
	if !ok {
		return false
	}
	p.data, p.err = cleanAccess(l.data), ""
	if l.err != nil {
		p.err = clean(l.err.Error())
	}
	p.rSel = max(0, min(p.rSel, len(p.data.Recipients)-1))
	p.qSel = max(0, min(p.qSel, len(p.data.Requests)-1))
	return true
}

func cleanAccess(d AccessData) AccessData {
	var out AccessData
	for _, r := range d.Recipients {
		r.Name, r.Kind, r.Owner, r.Added, r.AddedBy, r.Repos = clean(r.Name), clean(r.Kind), clean(r.Owner), clean(r.Added), clean(r.AddedBy), cleanAll(r.Repos)
		out.Recipients = append(out.Recipients, r)
	}
	for _, q := range d.Requests {
		q.Name, q.Kind, q.Owner, q.Host, q.Requested, q.Author, q.Key, q.Problem = clean(q.Name), clean(q.Kind), clean(q.Owner), clean(q.Host), clean(q.Requested), clean(q.Author), clean(q.Key), clean(q.Problem)
		out.Requests = append(out.Requests, q)
	}
	for _, e := range d.Due {
		out.Due = append(out.Due, DueEntry{clean(e.Secret), clean(e.Removed), clean(e.On)})
	}
	out.Differ = cleanAll(d.Differ)
	return out
}

func (p *accessPane) forget() {}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (p *accessPane) body(w, h int) []string {
	if p.err != "" {
		return panel("access", []string{stError.Render("✗ " + p.err)}, w, true)
	}
	d := p.data
	var rec, req, due []string
	for _, r := range d.Recipients {
		row := columns([]string{r.Name, r.Kind, dash(r.Owner), dash(r.Added), dash(r.AddedBy)}, []int{16, 8, 12, 10, 12})
		if r.Me {
			row += stTeal.Render("(this machine)")
		} else if len(r.Repos) > 0 {
			row += stMuted.Render("repos " + strings.Join(r.Repos, " "))
		}
		rec = append(rec, row)
	}
	for _, q := range d.Requests {
		if q.Problem != "" {
			req = append(req, columns([]string{q.Name, stError.Render("not valid: " + q.Problem)}, []int{16}))
			continue
		}
		req = append(req, columns([]string{q.Name, q.Kind, q.Host, q.Requested}, []int{16, 8, 20, 10}))
	}
	for _, e := range d.Due {
		due = append(due, columns([]string{e.Secret, "removed " + e.Removed, "on " + e.On}, []int{36, 20}))
	}
	// Each panel gets its rows, the recipients first; what does not fit scrolls.
	room := h - 6
	if len(d.Differ) > 0 {
		room--
	}
	qN := max(1, min(len(req), room/3))
	dN := max(1, min(len(due), room/4))
	rN := max(1, room-qN-dN)
	var out []string
	if len(rec) == 0 {
		out = panel("recipients", []string{stMuted.Render("No recipients.")}, w, p.focus == 0)
	} else {
		out = panel("recipients", listLines(rec, p.rSel, &p.rOff, min(rN, len(rec)), w-4, p.focus == 0), w, p.focus == 0)
	}
	if len(req) == 0 {
		out = append(out, panel("requests", []string{stMuted.Render("No pending requests.")}, w, p.focus == 1)...)
	} else {
		out = append(out, panel("requests", listLines(req, p.qSel, &p.qOff, qN, w-4, p.focus == 1), w, p.focus == 1)...)
	}
	if len(due) == 0 {
		out = append(out, panel("rotation due", []string{stMuted.Render("Nothing is due for a new value.")}, w, false)...)
	} else {
		start := min(p.dueOff, len(due)-1)
		out = append(out, panel("rotation due", due[start:min(len(due), start+dN)], w, false)...)
	}
	if len(d.Differ) > 0 {
		out = append(out, stError.Render("✗ "+strings.Join(d.Differ, ", ")+" not encrypted for exactly recipients.yaml. Run ezc store rewrap."))
	}
	return out
}

func (p *accessPane) hints() string {
	if p.focus == 1 {
		return "a approve  d deny  tab recipients"
	}
	return "x remove  R rotate  b add bot  tab requests"
}

func (p *accessPane) help() [][2]string {
	return [][2]string{
		{"a", "approve the selected request"},
		{"d", "deny it"},
		{"x", "remove the selected recipient"},
		{"R", "rotate this machine's key, or a bot's"},
		{"b", "add a bot"},
	}
}

func (p *accessPane) key(k string) tea.Cmd {
	m := p.m
	if p.focus == 0 && move(&p.rSel, len(p.data.Recipients), k) || p.focus == 1 && move(&p.qSel, len(p.data.Requests), k) {
		return nil
	}
	switch k {
	case "tab":
		p.focus = 1 - p.focus
		return nil
	case "b":
		return p.addBot()
	case "a", "d":
		if p.focus != 1 {
			return nil
		}
		if p.err != "" || len(p.data.Requests) == 0 {
			m.setStatus("nothing selected", false)
			return nil
		}
		q := p.data.Requests[p.qSel]
		if k == "d" {
			m.confirm("Deny "+q.Name, [][]string{{"user", "deny", q.Name}}, "", []string{"The request is removed; nobody's access changes."})
			return nil
		}
		if q.Problem != "" {
			m.setStatus("this request is not valid: "+q.Problem, false)
			return nil
		}
		notes := []string{
			"name       " + q.Name,
			"kind       " + q.Kind,
		}
		if q.Owner != "" {
			notes = append(notes, "owner      "+q.Owner)
		}
		notes = append(notes,
			"host       "+q.Host,
			"requested  "+q.Requested,
			"git author "+dash(q.Author),
			"key        "+q.Key,
			"",
			"Approving lets "+q.Name+" read every secret of the store. Check the key with "+q.Name+" first.",
		)
		m.confirm("Approve "+q.Name, [][]string{{"user", "approve", q.Name, "--yes"}}, "", notes)
		return nil
	case "x", "R":
		if p.focus != 0 {
			return nil
		}
		if p.err != "" || len(p.data.Recipients) == 0 {
			m.setStatus("nothing selected", false)
			return nil
		}
		r := p.data.Recipients[p.rSel]
		if k == "x" {
			if r.Kind == "bot" {
				m.confirm("Remove bot "+r.Name, [][]string{{"bot", "remove", r.Name}}, "", []string{r.Name + "'s key leaves the store, and its repos' secret is deleted."})
			} else {
				m.confirm("Remove "+r.Name, [][]string{{"user", "remove", r.Name}}, "", []string{r.Name + " can no longer read the store. Every secret " + r.Name + " could read is listed as due for a new value."})
			}
			return nil
		}
		switch {
		case r.Me:
			m.confirm("Rotate this machine's key", [][]string{{"user", "rotate"}}, "", []string{"This machine gets a new key in a new file; the old file is kept."})
		case r.Kind == "bot":
			m.confirm("Rotate bot "+r.Name, [][]string{{"bot", "rotate", r.Name}}, "", []string{r.Name + " gets a new key, delivered to its repos."})
		default:
			m.setStatus("only this machine's key or a bot's can be rotated here", false)
		}
		return nil
	}
	return nil
}

func (p *accessPane) addBot() tea.Cmd {
	m := p.m
	return m.ask("Bot name:", false, func(name string) tea.Cmd {
		if name = strings.TrimSpace(name); name == "" {
			return nil
		}
		return m.ask("Its repos (owner/repo, separated by spaces):", false, func(repos string) tea.Cmd {
			args := []string{"bot", "add", name}
			for _, r := range strings.Fields(repos) {
				args = append(args, "--repo", r)
			}
			if len(args) == 3 {
				m.setStatus("a bot needs at least one repo", false)
				return nil
			}
			m.confirm("Add bot "+name, [][]string{args}, "", []string{"A key is made for " + name + " and saved as the EZC_AGE_KEY secret of each repo."})
			return nil
		})
	})
}
