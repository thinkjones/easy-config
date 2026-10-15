package tui

import (
	"strings"
	"testing"
)

func reposFixture(f fakes) {
	f.repos.repos = []Repo{
		{ID: "o/app", Checkout: "/Users/gene/code/app", Here: true, OK: true, Report: []string{"environment local ✓ 12 vars from 3 files"}},
		{ID: "o/bad", Checkout: "/Users/gene/code/bad", Here: true, OK: false, Report: []string{"environment local: validation failed", "  ✗ API_URL is missing"}},
		{ID: "o/web"},
	}
}

func doctorFixture(f fakes) {
	f.doctor.sections = []Section{
		{Name: "machine", Items: []Item{{State: OK, Label: "ezc", Detail: "0.6.0"}, {State: OK, Label: "key", Detail: "~/.config/ezc/key.txt"}}},
		{Name: "store", Items: []Item{{State: Warn, Label: "sync", Detail: "2 commits behind origin", Fix: "Run ezc store pull."}}},
		{Name: "repo", Items: []Item{{State: Broken, Label: "recipients", Detail: "common.enc.yaml differs from the store's registry", Fix: "Run ezc repo rewrap."}}},
	}
}

func TestGoldenRepos(t *testing.T) {
	cases := map[string]func(t *testing.T) *Model{
		"repos": func(t *testing.T) *Model { m, _ := newTestWith(t, reposFixture); press(t, m, "3"); return m },
		"repos-detail": func(t *testing.T) *Model {
			m, _ := newTestWith(t, reposFixture)
			press(t, m, "3", "j", "enter")
			return m
		},
		"repos-empty": func(t *testing.T) *Model { m, _ := newTest(t); press(t, m, "3"); return m },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) { golden(t, name, build(t)) })
	}
}

func TestReposDetailOpensAndCloses(t *testing.T) {
	m, _ := newTestWith(t, reposFixture)
	press(t, m, "3", "j", "enter")
	if !strings.Contains(view(m), "✗ API_URL is missing") {
		t.Fatalf("\n%s", view(m))
	}
	press(t, m, "esc")
	if strings.Contains(view(m), "✗ API_URL is missing") {
		t.Fatalf("still open:\n%s", view(m))
	}
	press(t, m, "j", "enter")
	if !strings.Contains(view(m), "no checkout on this machine") {
		t.Fatalf("\n%s", view(m))
	}
}

func TestGoldenDoctor(t *testing.T) {
	m, _ := newTestWith(t, doctorFixture)
	press(t, m, "4")
	golden(t, "doctor", m)
	v := view(m)
	for _, want := range []string{"! sync", "Run ezc store pull.", "✗ recipients", "Run ezc repo rewrap.", "✓ ezc"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q in\n%s", want, v)
		}
	}
}

func TestReposAndDoctorFollowTheEnvironment(t *testing.T) {
	m, f := newTestWith(t, func(f fakes) { reposFixture(f); doctorFixture(f) })
	press(t, m, "3", "4", "e")
	typeText(t, m, "prod")
	press(t, m, "enter", "3")
	if got := f.repos.envs; len(got) != 2 || got[1] != "prod" {
		t.Fatalf("repos envs %v", got)
	}
	press(t, m, "4")
	if got := f.doctor.envs; len(got) != 2 || got[1] != "prod" {
		t.Fatalf("doctor envs %v", got)
	}
}
