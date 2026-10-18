// Package dotenv reads a .env file: NAME=VALUE lines, with comments, an optional export, and single or double quotes.
// Its errors name a line, never the text on it, which may be a secret.
package dotenv

import (
	"fmt"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

// Entry is one name, its value and the line that value starts on.
type Entry struct {
	Name  string
	Value string
	Line  int
}

// Parse reads src. A name given twice keeps its first place and takes its last value, as a shell sourcing the file would.
// Single quotes are literal; double quotes take \n, \r, \t, \", \\ and \$ and may span lines; an unquoted value ends
// at a # that follows a space.
func Parse(src []byte) ([]Entry, error) {
	lines := strings.Split(string(src), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	var out []Entry
	at := map[string]int{}
	for i := 0; i < len(lines); i++ {
		n := i + 1
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export"); ok && (strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")) {
			line = strings.TrimSpace(rest)
		}
		name, raw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: not NAME=VALUE", n)
		}
		name = strings.TrimSpace(name)
		if !manifest.ValidName(name) {
			return nil, fmt.Errorf("line %d: not a variable name", n)
		}
		raw = strings.TrimLeft(raw, " \t")
		var value string
		switch {
		case strings.HasPrefix(raw, "'"):
			end := strings.IndexByte(raw[1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("line %d: the quoted value is not closed", n)
			}
			value = raw[1 : 1+end]
			if !onlyComment(raw[2+end:]) {
				return nil, fmt.Errorf("line %d: text after the closing quote", n)
			}
		case strings.HasPrefix(raw, `"`):
			var b strings.Builder
			rest, closed := raw[1:], false
			for !closed {
				for j := 0; j < len(rest); j++ {
					c := rest[j]
					if c == '"' {
						closed, rest = true, rest[j+1:]
						break
					}
					if c == '\\' && j+1 < len(rest) {
						j++
						switch rest[j] {
						case 'n':
							b.WriteByte('\n')
						case 'r':
							b.WriteByte('\r')
						case 't':
							b.WriteByte('\t')
						case '"', '\\', '$':
							b.WriteByte(rest[j])
						default:
							b.WriteByte('\\')
							b.WriteByte(rest[j])
						}
						continue
					}
					b.WriteByte(c)
				}
				if !closed {
					if i+1 >= len(lines) {
						return nil, fmt.Errorf("line %d: the quoted value is not closed", n)
					}
					i++
					b.WriteByte('\n')
					rest = lines[i]
				}
			}
			value = b.String()
			if !onlyComment(rest) {
				return nil, fmt.Errorf("line %d: text after the closing quote", n)
			}
		default:
			value = raw
			for j := 1; j < len(raw); j++ {
				if raw[j] == '#' && (raw[j-1] == ' ' || raw[j-1] == '\t') {
					value = raw[:j]
					break
				}
			}
			value = strings.TrimRight(value, " \t")
		}
		if k, seen := at[name]; seen {
			out[k].Value, out[k].Line = value, n
			continue
		}
		at[name] = len(out)
		out = append(out, Entry{name, value, n})
	}
	return out, nil
}

// onlyComment reports whether s, after a closing quote, is blank or a comment.
func onlyComment(s string) bool {
	s = strings.TrimLeft(s, " \t")
	return s == "" || strings.HasPrefix(s, "#")
}
