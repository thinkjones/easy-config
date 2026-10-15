package tui

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The palette the reference TUI uses.
var (
	colText   = lipgloss.Color("#F3EFE6")
	colSoft   = lipgloss.Color("#C9D1D9")
	colMuted  = lipgloss.Color("#8B95A3")
	colBorder = lipgloss.Color("#2C3747")
	colAmber  = lipgloss.Color("#F0A847")
	colTeal   = lipgloss.Color("#5CC8A8")
	colRed    = lipgloss.Color("#F77E7E")

	stTitle    = lipgloss.NewStyle().Foreground(colText).Bold(true)
	stText     = lipgloss.NewStyle().Foreground(colSoft)
	stMuted    = lipgloss.NewStyle().Foreground(colMuted)
	stAmber    = lipgloss.NewStyle().Foreground(colAmber)
	stTeal     = lipgloss.NewStyle().Foreground(colTeal)
	stError    = lipgloss.NewStyle().Foreground(colRed)
	stSelected = lipgloss.NewStyle().Foreground(colAmber).Bold(true)
	stRule     = lipgloss.NewStyle().Foreground(colBorder)

	// The selected row of the panel you're not in: a grey caret on a background.
	colSelBg        = lipgloss.Color("#263142")
	stInactive      = lipgloss.NewStyle().Background(colSelBg).Foreground(colSoft)
	stInactiveCaret = lipgloss.NewStyle().Background(colSelBg).Foreground(colMuted)
)

// caretWidth is the selection column on the far left of every list.
const caretWidth = 2

// clean replaces every control character with "?". Everything from Deps passes through it where it enters the
// model: the store, a repo, a request and a command's output are written by others, and an escape sequence in
// them must not move the cursor or colour the screen.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}

// cleanAll is clean of each string, in a new slice.
func cleanAll(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = clean(s)
	}
	return out
}

// selectedRow draws a list row with the caret column: an amber caret when its panel is active, a grey caret on a
// background when not. w includes the caret column.
func selectedRow(row string, w int, active bool) string {
	if active {
		return stAmber.Render("▶ ") + fit(row, w-caretWidth)
	}
	return stInactiveCaret.Render("▶ ") + stInactive.Render(fit(ansi.Strip(row), w-caretWidth))
}

// plainRow is an unselected list row, with an empty caret column.
func plainRow(row string, w int) string { return spaces(caretWidth) + fit(row, w-caretWidth) }

// fit cuts s to width w with "…" and pads it to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += spaces(pad)
	}
	return s
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// panel draws lines in a rounded border with title in its top edge: exactly len(lines)+2 rows of width w. The
// focused panel's border is amber.
func panel(title string, lines []string, w int, focused bool) []string {
	border := stRule
	if focused {
		border = stAmber
	}
	inner := max(0, w-4)
	title = ansi.Truncate(title, max(0, w-6), "…")
	top := border.Render("╭─ ") + title + " "
	top += border.Render(strings.Repeat("─", max(0, w-lipgloss.Width(top)-1)) + "╮")
	out := []string{fit(top, w)}
	for _, l := range lines {
		out = append(out, border.Render("│")+" "+fit(l, inner)+" "+border.Render("│"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", max(0, w-2))+"╯"))
}
