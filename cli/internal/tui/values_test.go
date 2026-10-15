package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const revealedSecret = "s3cr3t-reveal"

// sketch is the values of the spec's sketch, in repo scope; the store scope has the same, from the store.
func sketch(f fakes) {
	repo := []Value{
		{Name: "LLM_API_KEY", Type: "secret", File: "repo/common.enc.yaml", Secret: true, Desc: "API key for whichever provider LLM_PROVIDER names", RequiredBy: []string{"o/digest", "o/website", "o/gascity"}, Due: "readable by removed sam (2026-10-01)"},
		{Name: "CF_ACCOUNT_ID", Type: "str", File: "store/common.yaml", Shown: "a1b2c3d4e5f6", Desc: "the Cloudflare account", RequiredBy: []string{"o/a", "o/b", "o/c"}, OptionalIn: []string{"o/d", "o/e"}},
		{Name: "BATCH_SIZE", Type: "int", File: "store/env.local.yaml", Shown: "20", Desc: "rows per batch", Overrides: []string{"common.yaml"}},
		{Name: "PORT", Type: "int", Desc: "the port to listen on"},
	}
	f.values.vals[RepoScope] = repo
	f.values.vals[StoreScope] = repo[1:3]
	f.values.reveal["LLM_API_KEY"] = revealedSecret
}

func valuesPaneOf(m *Model) *valuesPane { return m.panes[valuesView].(*valuesPane) }

func TestGoldenValues(t *testing.T) {
	cases := map[string]func(t *testing.T) *Model{
		"values-repo": func(t *testing.T) *Model { m, _ := newTestWith(t, sketch); return m },
		"values-global": func(t *testing.T) *Model {
			m, _ := newTestWith(t, sketch)
			press(t, m, "g")
			return m
		},
		"values-narrow": func(t *testing.T) *Model {
			m, _ := newTestWith(t, sketch)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
			return m
		},
		"values-error": func(t *testing.T) *Model {
			m, _ := newTestWith(t, func(f fakes) { f.values.err = fmt.Errorf("environment prod: no such environment") })
			return m
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) { golden(t, name, build(t)) })
	}
}

func TestValuesShowTheSketch(t *testing.T) {
	m, _ := newTestWith(t, sketch)
	v := view(m)
	for _, want := range []string{"values ── repo o/app", "LLM_API_KEY", "••••••••", "used by 3 repos", "a1b2c3d4e5f6", "used by 5 repos", "overrides common.yaml", "(not set)", "detail ── LLM_API_KEY", "required by  o/digest · o/website · o/gascity", "! rotation due: readable by removed sam (2026-10-01)"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q in\n%s", want, v)
		}
	}
}

func TestRevealIsDroppedWhenAnythingMoves(t *testing.T) {
	moves := map[string]func(t *testing.T, m *Model){
		"down":  func(t *testing.T, m *Model) { press(t, m, "j") },
		"view":  func(t *testing.T, m *Model) { press(t, m, "2", "1") },
		"scope": func(t *testing.T, m *Model) { press(t, m, "g") },
		"env": func(t *testing.T, m *Model) {
			press(t, m, "e")
			typeText(t, m, "prod")
			press(t, m, "enter")
		},
		"run": func(t *testing.T, m *Model) { press(t, m, "P", "y", "enter") },
	}
	for name, move := range moves {
		t.Run(name, func(t *testing.T) {
			m, f := newTestWith(t, sketch)
			press(t, m, "r")
			if !strings.Contains(view(m), revealedSecret) || !slices.Equal(f.values.reveals, []string{"LLM_API_KEY"}) {
				t.Fatalf("not revealed:\n%s", view(m))
			}
			move(t, m)
			if strings.Contains(view(m), revealedSecret) || strings.Contains(fmt.Sprintf("%+v", *valuesPaneOf(m)), revealedSecret) {
				t.Fatalf("still held after %s", name)
			}
		})
	}
}

func TestRevealOnlyASecret(t *testing.T) {
	m, f := newTestWith(t, sketch)
	press(t, m, "j", "r")
	if len(f.values.reveals) != 0 || m.status != "CF_ACCOUNT_ID is not a secret: its value is shown" {
		t.Fatalf("reveals %v, status %q", f.values.reveals, m.status)
	}
}

func TestSetSecretTakesTheValueOnStdin(t *testing.T) {
	m, f := newTestWith(t, sketch)
	press(t, m, "s")
	typeText(t, m, "n3w-s3cr3t")
	if strings.Contains(view(m), "n3w") {
		t.Fatalf("the input shows the value:\n%s", view(m))
	}
	press(t, m, "enter")
	if v := view(m); !strings.Contains(v, "ezc set LLM_API_KEY") || strings.Contains(v, "n3w") {
		t.Fatalf("confirm:\n%s", v)
	}
	press(t, m, "y")
	if len(f.commands.runs) != 1 || !slices.Equal(f.commands.runs[0].args, []string{"set", "LLM_API_KEY"}) || f.commands.runs[0].stdin != "n3w-s3cr3t\n" {
		t.Fatalf("runs %+v", f.commands.runs)
	}
}

func TestSetForTheEnvironmentAndScope(t *testing.T) {
	m, f := newTestWith(t, sketch)
	press(t, m, "j", "j", "S")
	typeText(t, m, "40")
	press(t, m, "enter", "y", "enter")
	press(t, m, "g", "s")
	typeText(t, m, "x")
	press(t, m, "enter", "y")
	if len(f.commands.runs) != 2 {
		t.Fatalf("runs %+v", f.commands.runs)
	}
	if got := f.commands.runs[0].args; !slices.Equal(got, []string{"set", "BATCH_SIZE", "--env", "local"}) {
		t.Errorf("S: %v", got)
	}
	if got := f.commands.runs[1].args; !slices.Equal(got, []string{"set", "CF_ACCOUNT_ID", "-g"}) {
		t.Errorf("g s: %v", got)
	}
}

func TestSetRefusesAnUndeclaredName(t *testing.T) {
	m, f := newTestWith(t, func(f fakes) {
		f.values.vals[RepoScope] = []Value{{Name: "STRAY", Type: "-", File: "repo/common.yaml", Shown: "x"}}
	})
	press(t, m, "s")
	if m.in != nil || len(f.commands.runs) != 0 || !strings.HasPrefix(m.status, "STRAY is not declared") {
		t.Fatalf("status %q", m.status)
	}
}

func TestUnsetFromTheFileThatSuppliesIt(t *testing.T) {
	m, f := newTestWith(t, sketch)
	press(t, m, "j", "j", "u", "y", "enter")
	press(t, m, "k", "k", "u", "y", "enter")
	if len(f.commands.runs) != 2 {
		t.Fatalf("runs %+v", f.commands.runs)
	}
	if got := f.commands.runs[0].args; !slices.Equal(got, []string{"unset", "BATCH_SIZE", "--env", "local", "-g"}) {
		t.Errorf("store env file: %v", got)
	}
	if got := f.commands.runs[1].args; !slices.Equal(got, []string{"unset", "LLM_API_KEY"}) {
		t.Errorf("repo common: %v", got)
	}
	press(t, m, "j", "j", "j", "u")
	if m.box != nil || m.status != "PORT has no value here" {
		t.Fatalf("status %q", m.status)
	}
}

func TestScopeOutsideARepo(t *testing.T) {
	m, f := newTestWith(t, func(f fakes) { sketch(f); f.store.h.Repo = "" })
	press(t, m, "g")
	if m.status != "not in a repo: the store only" || slices.Contains(f.values.scopes, RepoScope) {
		t.Fatalf("status %q, scopes %v", m.status, f.values.scopes)
	}
}

func TestEnvironmentChangeReloads(t *testing.T) {
	m, f := newTestWith(t, sketch)
	press(t, m, "e")
	typeText(t, m, "prod")
	press(t, m, "enter")
	if f.values.envs[len(f.values.envs)-1] != "prod" || !strings.Contains(view(m), "env prod") {
		t.Fatalf("envs %v", f.values.envs)
	}
}

func TestPushAndPull(t *testing.T) {
	m, f := newTestWith(t, sketch)
	press(t, m, "P", "y", "enter", "p", "y")
	if len(f.commands.runs) != 2 || !slices.Equal(f.commands.runs[0].args, []string{"store", "push"}) || !slices.Equal(f.commands.runs[1].args, []string{"store", "pull"}) {
		t.Fatalf("runs %+v", f.commands.runs)
	}
}

func TestValuesKeysOnAnEmptyList(t *testing.T) {
	m, f := newTest(t)
	press(t, m, "j", "k", "s", "u", "r")
	if len(f.commands.runs) != 0 || m.box != nil || m.status != "nothing selected" {
		t.Fatalf("status %q", m.status)
	}
}

func TestNoEnvironmentNamed(t *testing.T) {
	m, f := newTestWith(t, func(f fakes) { sketch(f); f.store.h.Env = "" })
	if first := strings.Split(view(m), "\n")[0]; !strings.Contains(first, "· env default") {
		t.Fatalf("header %q", first)
	}
	press(t, m, "S")
	if m.in != nil || m.status != "no environment is named: choose one with e first" || len(f.commands.runs) != 0 {
		t.Fatalf("status %q", m.status)
	}
}

// A reveal that arrives after the selection, scope or environment moved is dropped.
func TestLateRevealIsDropped(t *testing.T) {
	for name, move := range map[string][]string{"scope": {"g"}, "down and back": {"j", "k"}, "reload": {"ctrl+r"}} {
		t.Run(name, func(t *testing.T) {
			m, _ := newTestWith(t, sketch)
			_, reveal := m.Update(key("r"))
			press(t, m, move...)
			if name == "scope" {
				press(t, m, "g")
			}
			drain(t, m, reveal)
			if strings.Contains(view(m), revealedSecret) || strings.Contains(fmt.Sprintf("%+v", *valuesPaneOf(m)), revealedSecret) {
				t.Fatalf("a late reveal was shown after %s", name)
			}
		})
	}
}

// A load that a later one overtook is dropped: the list is always the latest scope's.
func TestOlderLoadIsDropped(t *testing.T) {
	m, _ := newTestWith(t, sketch)
	_, toStore := m.Update(key("g"))
	_, toRepo := m.Update(key("g"))
	drain(t, m, toRepo)
	drain(t, m, toStore)
	if got := len(valuesPaneOf(m).vals); got != 4 || m.scope != RepoScope {
		t.Fatalf("%d values in scope %v:\n%s", got, m.scope, view(m))
	}
}

func TestOlderAccessLoadIsDropped(t *testing.T) {
	m, f := onAccess(t, accessFixture)
	_, older := m.Update(key("ctrl+r"))
	f.access.data.Recipients = f.access.data.Recipients[:1]
	_, newer := m.Update(key("ctrl+r"))
	drain(t, m, newer)
	f.access.data.Recipients = nil
	drain(t, m, older)
	if got := len(m.panes[accessView].(*accessPane).data.Recipients); got != 1 {
		t.Fatalf("%d recipients", got)
	}
}
