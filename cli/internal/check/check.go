// Package check validates a resolved environment against a repo's manifest.
//
// It validates TYPES and BOUNDS rather than only presence, and it checks the
// environment the resolver actually composed, including one you are not
// currently in (`ezc env prod` from a local shell).
package check

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
	"github.com/thinkjones/easy-config/cli/internal/resolve"
)

// Placeholders are values that are technically set but obviously unfilled.
// Treated as missing: a `replace-me` left in a file surfaces as a 401 from a
// live API rather than as a configuration error, which is far more expensive to
// diagnose.
var placeholders = map[string]bool{
	"replace-me": true, "replace_me": true, "changeme": true,
	"change-me": true, "todo": true, "tbd": true, "xxx": true,
}

// IsPlaceholder reports whether a value is technically set but obviously
// unfilled. Exported for the same reason as ValidateValue.
func IsPlaceholder(value string) bool {
	return placeholders[strings.ToLower(strings.TrimSpace(value))]
}

// PlaceholderWords returns the same set, SORTED, for generators that have to
// re-express this rule in another language.
//
// Sorted because a code generator's output is diffed against a committed file
// by every consumer's drift check. Ranging a Go map is deliberately
// randomised, so emitting these in map order would reorder the generated file
// on roughly every other run and make the drift gate fail at random — a far
// more confusing bug than the one this set exists to prevent.
func PlaceholderWords() []string {
	words := make([]string, 0, len(placeholders))
	for w := range placeholders {
		words = append(words, w)
	}
	sort.Strings(words)
	return words
}

type Problem struct {
	Name string
	Desc string
	Why  string
}

func (p Problem) String() string {
	if p.Desc == "" {
		return fmt.Sprintf("%s — %s", p.Name, p.Why)
	}
	return fmt.Sprintf("%s — %s\n      %s", p.Name, p.Desc, p.Why)
}

// Run validates res against m and returns every problem at once. Never
// returning early matters: a fresh clone should get one list to work through,
// not ten sequential fix-and-retry cycles.
func Run(m manifest.Manifest, res resolve.Result) []Problem {
	var problems []Problem

	for _, e := range m.All() {
		_, isRequired := m.Required[e.Name]
		raw, present := res.Env[e.Name]
		value := strings.TrimSpace(raw)

		missing := !present || value == "" || placeholders[strings.ToLower(value)]

		if missing {
			if isRequired {
				problems = append(problems, Problem{e.Name, e.Var.Desc, whyMissing(present, value)})
				continue
			}
			// An optional variable that is absent is the normal case. But one
			// whose requiredWhen condition is met is required right now.
			if c := e.Var.RequiredWhen; c != nil {
				if actual, ok := res.Env[c.Var]; ok && strings.TrimSpace(actual) == c.Equals {
					problems = append(problems, Problem{e.Name, e.Var.Desc,
						fmt.Sprintf("required because %s=%s", c.Var, c.Equals)})
				}
			}
			continue
		}

		// Present: check it is actually usable, not merely non-empty.
		if why := ValidateValue(e.Var, value); why != "" {
			problems = append(problems, Problem{e.Name, e.Var.Desc, why})
		}
	}

	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Name < problems[j].Name })
	return problems
}

func whyMissing(present bool, value string) string {
	switch {
	case !present:
		return "not set"
	case value == "":
		return "set but empty"
	default:
		return fmt.Sprintf("still the placeholder %q", strings.ToLower(value))
	}
}

// ValidateValue applies the type. Exported so anything that checks a value
// applies the SAME rules — two implementations of
// "is this a valid int" is precisely the drift this tool exists to prevent.
//
// The reason NEVER contains the value: it is printed by `ezc env`, whose
// output lands in CI logs, and a failing value is as likely a credential as not.
func ValidateValue(v manifest.Var, value string) string {
	switch v.Type {
	case manifest.TypeInt:
		n, err := strconv.Atoi(value)
		if err != nil {
			return "not an integer"
		}
		if v.Min != nil && n < *v.Min {
			return fmt.Sprintf("below the minimum of %d", *v.Min)
		}
		if v.Max != nil && n > *v.Max {
			return fmt.Sprintf("above the maximum of %d", *v.Max)
		}
	case manifest.TypeBool:
		switch strings.ToLower(value) {
		case "1", "0", "true", "false", "yes", "no":
		default:
			return "not a boolean (use 1, 0, true, false, yes or no)"
		}
	case manifest.TypeEnum:
		for _, allowed := range v.Values {
			if value == allowed {
				return ""
			}
		}
		return "not one of: " + strings.Join(v.Values, ", ")
	case manifest.TypeURL:
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return "not a URL (must start with http:// or https://)"
		}
	}
	return ""
}

// Report renders problems with the files that were loaded, in precedence
// order, so the reader knows which to open.
func Report(problems []Problem, res resolve.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "environment %s: validation failed\n", res.EnvLabel())
	for _, p := range problems {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	b.WriteString("\nFiles, in precedence order:\n")
	for _, s := range res.Sources {
		fmt.Fprintf(&b, "  %s %s (%d vars)\n", marker(s), s.Path, len(s.Vars))
	}
	return b.String()
}

// marker is "-" for a file that is absent or supplied nothing.
func marker(s resolve.Source) string {
	if len(s.Vars) == 0 {
		return "-"
	}
	return " "
}

// ReportOK renders the one-line summary a passing check prints. A check whose
// success is indistinguishable from never having run would hide the failure it
// exists to catch, so it is never silent. The caller writes it to stderr.
func ReportOK(res resolve.Result) string {
	files := 0
	for _, s := range res.Sources {
		if len(s.Vars) > 0 {
			files++
		}
	}
	return fmt.Sprintf("environment %s ✓ %s from %s\n", res.EnvLabel(), plural(len(res.Exported()), "var"), plural(files, "file"))
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// ReportVerbose renders the origin of every value: which file set it, and
// which earlier files it overrode. It answers "which of the files decided
// this", which precedence hides.
//
// NEVER prints a value. The output is meant to be pasted into a chat window
// while debugging, and half these names are credentials.
func ReportVerbose(res resolve.Result, home string) string {
	var b strings.Builder
	b.WriteString(ReportOK(res))
	b.WriteString("\nFiles loaded, in precedence order (later wins):\n")

	width := 0
	for _, s := range res.Sources {
		if n := len(tilde(s.Path, home)); n > width {
			width = n
		}
	}
	for _, s := range res.Sources {
		fmt.Fprintf(&b, "  %s %-*s  %s\n", marker(s), width, tilde(s.Path, home), plural(len(s.Vars), "var"))
	}

	b.WriteString("\nWhere each value came from:\n")
	nameW := 0
	for _, n := range res.Names() {
		if len(n) > nameW {
			nameW = len(n)
		}
	}
	for _, name := range res.Names() {
		fmt.Fprintf(&b, "  %-*s  %s", nameW, name, tilde(res.Origin[name], home))
		if beat := res.Overrides(name); len(beat) > 0 {
			names := make([]string, len(beat))
			for i, p := range beat {
				names[i] = filepath.Base(p)
			}
			fmt.Fprintf(&b, "  (overrides %s)", strings.Join(names, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nNames and paths only — no values. Use `ezc env export` to emit them.\n")
	return b.String()
}

// tilde shortens home so absolute paths do not dominate a line whose subject is
// the filename. The origin "shell" is not a path and passes through.
func tilde(p, home string) string {
	if home == "" || !strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return p
	}
	return "~" + p[len(home):]
}
