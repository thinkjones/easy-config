package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCleanReplacesControlCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"a\x1b[31mb": "a?[31mb", "x\ry": "x?y", "two\nlines": "two?lines", "\x07": "?", "tab\there": "tab?here", "plain é": "plain é", "\u009b31m": "?31m",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFitTruncatesAndPads(t *testing.T) {
	for _, c := range []struct {
		in   string
		w    int
		want string
	}{{"abcdef", 4, "abc…"}, {"ab", 4, "ab  "}, {"x", 0, ""}, {"x", -1, ""}} {
		if got := fit(c.in, c.w); got != c.want {
			t.Errorf("fit(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
}

func TestPanelShape(t *testing.T) {
	rows := panel("values ── global", []string{"a"}, 30, false)
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, r := range rows {
		if w := lipgloss.Width(r); w != 30 {
			t.Errorf("width %d: %q", w, ansi.Strip(r))
		}
	}
	top := ansi.Strip(rows[0])
	if !strings.HasPrefix(top, "╭─ values ── global ") || !strings.HasSuffix(top, "╮") {
		t.Errorf("top %q", top)
	}
	if got := ansi.Strip(rows[2]); got != "╰"+strings.Repeat("─", 28)+"╯" {
		t.Errorf("bottom %q", got)
	}
}
