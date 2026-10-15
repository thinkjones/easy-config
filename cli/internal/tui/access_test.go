package tui

import (
	"slices"
	"strings"
	"testing"
)

const samKey = "age1samsamsamsamsamsamsamsamsamsamsamsamsamsamsamsamsamsamsam"

func accessFixture(f fakes) {
	f.access.data = AccessData{
		Recipients: []Recipient{
			{Name: "ci", Kind: "bot", Added: "2026-10-02", AddedBy: "gene", Repos: []string{"o/app"}},
			{Name: "gene", Kind: "user", Added: "2026-10-01", AddedBy: "gene", Me: true},
			{Name: "gene-laptop", Kind: "machine", Owner: "gene", Added: "2026-10-03", AddedBy: "gene"},
		},
		Requests: []Request{
			{Name: "bad", Kind: "user", Host: "h", Requested: "2026-10-04", Problem: "the key is not a valid key"},
			{Name: "sam", Kind: "user", Host: "sams-mac", Requested: "2026-10-05", Author: "Sam <sam@example.com>", Key: samKey},
		},
		Due: []DueEntry{
			{Secret: "common.enc.yaml/API_TOKEN", Removed: "zed", On: "2026-10-06"},
			{Secret: "env.prod.enc.yaml/DB_PASSWORD", Removed: "zed", On: "2026-10-06"},
		},
	}
}

// onAccess is a model on the access view over the fixture.
func onAccess(t *testing.T, setup func(fakes)) (*Model, fakes) {
	t.Helper()
	m, f := newTestWith(t, setup)
	press(t, m, "2")
	return m, f
}

func TestGoldenAccess(t *testing.T) {
	cases := map[string]func(t *testing.T) *Model{
		"access": func(t *testing.T) *Model { m, _ := onAccess(t, accessFixture); return m },
		"access-requests": func(t *testing.T) *Model {
			m, _ := onAccess(t, accessFixture)
			press(t, m, "tab", "j")
			return m
		},
		"access-differ": func(t *testing.T) *Model {
			m, _ := onAccess(t, func(f fakes) { accessFixture(f); f.access.data.Differ = []string{"common.enc.yaml"} })
			return m
		},
		"access-approve": func(t *testing.T) *Model {
			m, _ := onAccess(t, accessFixture)
			press(t, m, "tab", "j", "a")
			return m
		},
		"access-hostile": func(t *testing.T) *Model { m, _ := onAccess(t, hostile); return m },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) { golden(t, name, build(t)) })
	}
}

func hostile(f fakes) {
	f.access.data = AccessData{Requests: []Request{{Name: "sam", Kind: "user", Host: "h\x1b[2Jx", Owner: "a\nb", Requested: "2026-10-05\r", Key: samKey}}}
}

func TestHostileRequestStaysOnItsRow(t *testing.T) {
	m, _ := onAccess(t, hostile)
	raw := m.View().Content
	if strings.Contains(raw, "\x1b[2J") || strings.Contains(raw, "\r") {
		t.Fatalf("raw control characters reached the screen: %q", raw)
	}
	if !strings.Contains(view(m), "h?[2Jx") {
		t.Fatalf("not shown cleaned:\n%s", view(m))
	}
	press(t, m, "tab", "a")
	if raw := m.View().Content; strings.Contains(raw, "\x1b[2J") || !strings.Contains(view(m), "a?b") {
		t.Fatalf("the approve screen:\n%q", view(m))
	}
}

func TestApproveShowsTheRequestAndPinsItsKey(t *testing.T) {
	m, f := onAccess(t, accessFixture)
	press(t, m, "tab", "j", "a")
	v := view(m)
	for _, want := range []string{"ezc user approve sam --key " + samKey, "sams-mac", "2026-10-05", "Sam <sam@example.com>"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q on\n%s", want, v)
		}
	}
	press(t, m, "y")
	if len(f.commands.runs) != 1 || !slices.Equal(f.commands.runs[0].args, []string{"user", "approve", "sam", "--key", samKey}) {
		t.Fatalf("runs %+v", f.commands.runs)
	}
}

func TestApproveRefusesAnInvalidRequest(t *testing.T) {
	m, _ := onAccess(t, accessFixture)
	press(t, m, "tab", "a")
	if m.box != nil || m.status != "this request is not valid: the key is not a valid key" {
		t.Fatalf("status %q", m.status)
	}
}

func TestDeny(t *testing.T) {
	m, f := onAccess(t, accessFixture)
	press(t, m, "tab", "d", "y")
	if len(f.commands.runs) != 1 || !slices.Equal(f.commands.runs[0].args, []string{"user", "deny", "bad"}) {
		t.Fatalf("runs %+v", f.commands.runs)
	}
}

func TestRemoveByKind(t *testing.T) {
	m, f := onAccess(t, accessFixture)
	press(t, m, "x", "y", "enter", "j", "j", "x", "y")
	if len(f.commands.runs) != 2 {
		t.Fatalf("runs %+v", f.commands.runs)
	}
	if got := f.commands.runs[0].args; !slices.Equal(got, []string{"bot", "remove", "ci"}) {
		t.Errorf("bot: %v", got)
	}
	if got := f.commands.runs[1].args; !slices.Equal(got, []string{"user", "remove", "gene-laptop"}) {
		t.Errorf("machine: %v", got)
	}
}

func TestRotateOnlyOwnOrBot(t *testing.T) {
	m, f := onAccess(t, accessFixture)
	press(t, m, "R", "y", "enter", "j", "R", "y", "enter", "j", "R")
	if m.box != nil || m.status != "only this machine's key or a bot's can be rotated here" {
		t.Fatalf("status %q", m.status)
	}
	if len(f.commands.runs) != 2 || !slices.Equal(f.commands.runs[0].args, []string{"bot", "rotate", "ci"}) || !slices.Equal(f.commands.runs[1].args, []string{"user", "rotate"}) {
		t.Fatalf("runs %+v", f.commands.runs)
	}
}

func TestAddBot(t *testing.T) {
	m, f := onAccess(t, accessFixture)
	press(t, m, "b")
	typeText(t, m, "deploy")
	press(t, m, "enter")
	typeText(t, m, "o/app o/web")
	press(t, m, "enter", "y")
	if len(f.commands.runs) != 1 || !slices.Equal(f.commands.runs[0].args, []string{"bot", "add", "deploy", "--repo", "o/app", "--repo", "o/web"}) {
		t.Fatalf("runs %+v", f.commands.runs)
	}
	press(t, m, "enter", "b")
	typeText(t, m, "x")
	press(t, m, "enter", "enter")
	if m.box != nil || m.status != "a bot needs at least one repo" {
		t.Fatalf("status %q", m.status)
	}
}

// No access action answers the command's question about letting somebody else in.
func TestNoAccessActionPassesYes(t *testing.T) {
	m, f := onAccess(t, accessFixture)
	press(t, m, "x", "y", "enter", "R", "y", "enter", "tab", "d", "y", "enter", "j", "a", "y", "enter")
	for _, r := range f.commands.runs {
		if slices.Contains(r.args, "--yes") {
			t.Errorf("--yes in %v", r.args)
		}
	}
	if len(f.commands.runs) != 4 {
		t.Fatalf("runs %+v", f.commands.runs)
	}
}

func TestAccessLoadError(t *testing.T) {
	m, _ := onAccess(t, func(f fakes) { f.access.err = errString("the store has no recipients.yaml. Run ezc setup.") })
	if !strings.Contains(view(m), "✗ the store has no recipients.yaml. Run ezc setup.") {
		t.Fatalf("\n%s", view(m))
	}
	press(t, m, "x")
	if m.status != "nothing selected" {
		t.Fatalf("status %q", m.status)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
