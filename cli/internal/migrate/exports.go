package migrate

import (
	"errors"
	"strings"

	"github.com/thinkjones/easy-config/cli/internal/manifest"
)

var errNotExports = errors.New("the old tool printed something that is not an export line")

// parseExports reads the old tool's `codegen shell` output: one `export NAME='value'` per name, where a quote
// inside a value is written '\” and a value may span lines. Anything else is an error that quotes nothing.
func parseExports(out []byte) (map[string]string, error) {
	env := map[string]string{}
	s := string(out)
	for len(s) > 0 {
		rest, ok := strings.CutPrefix(s, "export ")
		if !ok {
			return nil, errNotExports
		}
		name, rest, ok := strings.Cut(rest, "=")
		if !ok || !manifest.ValidName(name) {
			return nil, errNotExports
		}
		var value strings.Builder
		for {
			body, ok := strings.CutPrefix(rest, "'")
			if !ok {
				return nil, errNotExports
			}
			seg, after, ok := strings.Cut(body, "'")
			if !ok {
				return nil, errNotExports
			}
			value.WriteString(seg)
			if next, ok := strings.CutPrefix(after, `\'`); ok {
				value.WriteByte('\'')
				rest = next
				continue
			}
			if next, ok := strings.CutPrefix(after, "\n"); ok {
				rest = next
				break
			}
			return nil, errNotExports
		}
		env[name] = value.String()
		s = rest
	}
	return env, nil
}
