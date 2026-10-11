package resolve

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// ShellQuote renders a value safe to eval in POSIX shell. Single quotes with
// the close-reopen trick, so no value can inject a command no matter what it
// contains — these strings include credentials and must never be interpreted.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Exported returns the names to emit: Names() without the selector variables.
func (r Result) Exported() []string {
	var names []string
	for _, n := range r.Names() {
		if !slices.Contains(SelectorVars, n) {
			names = append(names, n)
		}
	}
	return names
}

// Shell renders one `export NAME='value'` line per exported name, for eval.
// Names are written unquoted; they are valid identifiers because Load rejects
// anything else.
func (r Result) Shell() string {
	var b strings.Builder
	for _, n := range r.Exported() {
		b.WriteString("export " + n + "=" + ShellQuote(r.Env[n]) + "\n")
	}
	return b.String()
}

// JSON renders the exported names as one object with sorted keys.
func (r Result) JSON() ([]byte, error) {
	out := make(map[string]string, len(r.Env))
	for _, n := range r.Exported() {
		out[n] = r.Env[n]
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
